package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

// InsertTask persists Task + its options in one transaction (Create -> Pending).
func (s *Store) InsertTask(ctx context.Context, t *core.Task) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO tasks
			(id,state,input,input_kind,resource_id,priority,error_kind,error_message,
			 bytes_done,bytes_total,bytes_uploaded,created_at,updated_at,completed_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			string(t.ID), string(t.State), t.Input, string(t.InputKind), nullable(t.ResourceID),
			t.Priority, nullable(t.ErrorKind), nullable(t.ErrorMessage),
			t.BytesDone, t.BytesTotal, t.BytesUploaded, t.CreatedAt, t.UpdatedAt, nil)
		if err != nil {
			return err
		}
		return saveOptions(ctx, tx, t.ID, t.Options, "")
	})
}

func saveOptions(ctx context.Context, tx *sql.Tx, id core.TaskID, opt core.TaskOptions, tempPath string) error {
	// Defense in depth: options are persisted as plain JSON, so credential
	// headers must be stripped here regardless of what the caller supplied.
	// Only the opaque credentialRef is allowed in the DB (docs/06 §6).
	stored := opt
	stored.Headers = security.StripSensitiveHeaders(opt.Headers)
	b, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	kv := map[string]string{
		"options":         string(b),
		"dir":             opt.Dir,
		"out":             opt.Out,
		"temp_path":       tempPath,
		"post_process":    opt.PostProcess,
		"max_connections": fmt.Sprint(opt.MaxConnections),
		"chunk_size":      fmt.Sprint(opt.ChunkSize),
	}
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_options(task_id,key,value) VALUES(?,?,?)
			ON CONFLICT(task_id,key) DO UPDATE SET value=excluded.value`, string(id), k, v); err != nil {
			return err
		}
	}
	return nil
}

// SetOption stores one runtime option (e.g. the .part path) transactionally.
func (s *Store) SetOption(ctx context.Context, id core.TaskID, key, value string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO task_options(task_id,key,value) VALUES(?,?,?)
			ON CONFLICT(task_id,key) DO UPDATE SET value=excluded.value`, string(id), key, value)
		return err
	})
}

func (s *Store) Option(ctx context.Context, id core.TaskID, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM task_options WHERE task_id=? AND key=?`, string(id), key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) loadOptions(ctx context.Context, id core.TaskID) (core.TaskOptions, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM task_options WHERE task_id=? AND key='options'`, string(id)).Scan(&raw)
	if err == sql.ErrNoRows {
		return core.TaskOptions{}, nil
	}
	if err != nil {
		return core.TaskOptions{}, err
	}
	var opt core.TaskOptions
	if err := json.Unmarshal([]byte(raw), &opt); err != nil {
		return core.TaskOptions{}, err
	}
	return opt, nil
}

// StateUpdate is the only way to change a task's state. Force is reserved for
// crash recovery, which must bypass the normal transition table.
type StateUpdate struct {
	To           core.TaskState
	ErrorKind    string
	ErrorMessage string
	BytesDone    *int64
	BytesTotal   *int64
	ResourceID   string
	Force        bool
}

func (s *Store) UpdateState(ctx context.Context, id core.TaskID, u StateUpdate) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM tasks WHERE id=?`, string(id)).Scan(&current); err != nil {
			if err == sql.ErrNoRows {
				return core.Err(core.E_TASK_NOT_FOUND, "task %s not found", id)
			}
			return err
		}
		if !u.Force && current != string(u.To) {
			if err := core.CanTransition(core.TaskState(current), u.To); err != nil {
				return err
			}
		}
		at := s.now()
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET state=?, error_kind=?, error_message=?,
			bytes_done=COALESCE(?,bytes_done), bytes_total=COALESCE(?,bytes_total),
			resource_id=COALESCE(?,resource_id), updated_at=?,
			completed_at=CASE WHEN ?='Completed' THEN ? ELSE completed_at END
			WHERE id=?`,
			string(u.To), nullable(u.ErrorKind), nullable(u.ErrorMessage),
			u.BytesDone, u.BytesTotal, nullable(u.ResourceID), at,
			string(u.To), at, string(id))
		return err
	})
}

// AddProgress adds bytes to a task and is always called in the same transaction
// as the chunk update (see chunks.go).
func addProgressTx(ctx context.Context, tx *sql.Tx, id core.TaskID, delta int64, at int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET bytes_done=bytes_done+?, updated_at=? WHERE id=?`, delta, at, string(id))
	return err
}

// SaveOptions rewrites the persisted options (PATCH /tasks/{id}).
func (s *Store) SaveOptions(ctx context.Context, id core.TaskID, opt core.TaskOptions) error {
	temp, err := s.Option(ctx, id, "temp_path")
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return saveOptions(ctx, tx, id, opt, temp)
	})
}

// Commit records the completion transition and the final output path in ONE
// transaction (docs/06 §5 transaction boundary / ADR-0015 invariant ③).
func (s *Store) Commit(ctx context.Context, id core.TaskID, resourceID string, fileIdx int, final string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		at := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE files SET final_path=? WHERE resource_id=? AND idx=?`,
			final, resourceID, fileIdx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET state='Completed', error_kind=NULL,
			error_message=NULL, updated_at=?, completed_at=? WHERE id=?`, at, at, string(id))
		return err
	})
}

// SetProgress sets the absolute counter (used after recovery recomputation).
func (s *Store) SetProgress(ctx context.Context, id core.TaskID, done, total int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET bytes_done=?, bytes_total=?, updated_at=? WHERE id=?`,
			done, total, s.now(), string(id))
		return err
	})
}

// GetTask loads a task with its options and full Resource (if already resolved).
func (s *Store) GetTask(ctx context.Context, id core.TaskID) (*core.Task, error) {
	t := &core.Task{}
	var completed sql.NullInt64
	var errKind, errMsg, resourceID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,state,input,input_kind,COALESCE(resource_id,''),priority,
		COALESCE(error_kind,''),COALESCE(error_message,''),bytes_done,bytes_total,bytes_uploaded,
		created_at,updated_at,completed_at FROM tasks WHERE id=?`, string(id)).
		Scan(&t.ID, &t.State, &t.Input, &t.InputKind, &resourceID, &t.Priority,
			&errKind, &errMsg, &t.BytesDone, &t.BytesTotal, &t.BytesUploaded,
			&t.CreatedAt, &t.UpdatedAt, &completed)
	if err == sql.ErrNoRows {
		return nil, core.Err(core.E_TASK_NOT_FOUND, "task %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	t.ResourceID = resourceID.String
	t.ErrorKind = errKind.String
	t.ErrorMessage = errMsg.String
	if completed.Valid {
		v := completed.Int64
		t.CompletedAt = &v
	}
	opt, err := s.loadOptions(ctx, id)
	if err != nil {
		return nil, err
	}
	t.Options = opt
	if t.ResourceID != "" {
		r, err := s.GetResource(ctx, t.ResourceID)
		if err == nil {
			t.Resource = r
		}
	}
	return t, nil
}

// ListTasks implements cursor pagination (docs/07 §1).
func (s *Store) ListTasks(ctx context.Context, f core.TaskFilter) ([]*core.Task, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,state,input,input_kind,priority,bytes_done,bytes_total,
		created_at,updated_at FROM tasks
		WHERE (?='' OR state=?) AND (?='' OR input_kind=?) AND (?='' OR id<?)
		ORDER BY id DESC LIMIT ?`,
		string(f.State), string(f.State), string(f.Kind), string(f.Kind), f.Cursor, f.Cursor, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*core.Task
	for rows.Next() {
		t := &core.Task{}
		if err := rows.Scan(&t.ID, &t.State, &t.Input, &t.InputKind, &t.Priority,
			&t.BytesDone, &t.BytesTotal, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// TaskSummary returns lightweight counters for /stats.
func (s *Store) TaskSummary(ctx context.Context) (total, running, completed, failed int, bytesDone int64, err error) {
	// SUM over an empty table yields NULL, so every aggregate needs COALESCE
	// (otherwise /stats fails on a fresh database before any task exists).
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN state='Running' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='Completed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='Failed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(bytes_done),0) FROM tasks`).
		Scan(&total, &running, &completed, &failed, &bytesDone)
	return
}

// DeleteTask removes the task (cascading resources/files/chunks) and returns
// final output paths so the caller can optionally remove files.
func (s *Store) DeleteTask(ctx context.Context, id core.TaskID) ([]string, error) {
	var paths []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var rid sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT resource_id FROM tasks WHERE id=?`, string(id)).Scan(&rid); err != nil {
			if err == sql.ErrNoRows {
				return core.Err(core.E_TASK_NOT_FOUND, "task %s not found", id)
			}
			return err
		}
		if rid.Valid && rid.String != "" {
			rows, err := tx.QueryContext(ctx, `SELECT COALESCE(final_path,'') FROM files WHERE resource_id=?`, rid.String)
			if err != nil {
				return err
			}
			for rows.Next() {
				var p string
				if err := rows.Scan(&p); err != nil {
					rows.Close()
					return err
				}
				if p != "" {
					paths = append(paths, p)
				}
			}
			rows.Close()
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, string(id))
		return err
	})
	return paths, err
}

// SetFinalPath records the committed output path in the same transaction as
// the completion transition (docs/06 §5 transaction boundary).
func (s *Store) SetFinalPath(ctx context.Context, resourceID string, fileIdx int, final string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE files SET final_path=? WHERE resource_id=? AND idx=?`,
			final, resourceID, fileIdx)
		return err
	})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
