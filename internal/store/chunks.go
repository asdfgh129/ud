package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/asdfgh129/ud/internal/core"
)

// CreateChunks builds the authoritative chunk plan from the probed file size.
// Idempotent: re-running after a restart never duplicates rows.
func (s *Store) CreateChunks(ctx context.Context, taskID core.TaskID, fileIdx int, size, chunkSize int64) (int, error) {
	if chunkSize < 1 {
		chunkSize = 4 << 20
	}
	count := 0
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE task_id=? AND file_idx=?`,
			string(taskID), fileIdx).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			count = existing
			return nil
		}
		if size <= 0 {
			// Zero-length file: one empty chunk keeps the state machine uniform.
			_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chunks
				(task_id,file_idx,idx,start,length,state,attempt,mirror_id,hash_ok,updated_at)
				VALUES(?,?,0,0,0,'pending',0,NULL,0,?)`, string(taskID), fileIdx, s.now())
			count = 1
			return err
		}
		for start, idx := int64(0), 0; start < size; start, idx = start+chunkSize, idx+1 {
			length := chunkSize
			if start+length > size {
				length = size - start
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chunks
				(task_id,file_idx,idx,start,length,state,attempt,mirror_id,hash_ok,updated_at)
				VALUES(?,?,?,?,?,'pending',0,NULL,0,?)`,
				string(taskID), fileIdx, idx, start, length, s.now()); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

// Next atomically claims the next pending chunk (pending -> assigned).
func (s *Store) Next(ctx context.Context, taskID core.TaskID) (core.Chunk, bool, error) {
	var c core.Chunk
	found := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var mirror sql.NullInt64
		var hashOK int
		err := tx.QueryRowContext(ctx, `SELECT id,task_id,file_idx,idx,start,length,state,attempt,mirror_id,hash_ok
			FROM chunks WHERE task_id=? AND state='pending' ORDER BY file_idx,idx LIMIT 1`, string(taskID)).
			Scan(&c.ID, &c.TaskID, &c.FileIndex, &c.Index, &c.Start, &c.Length,
				&c.State, &c.Attempt, &mirror, &hashOK)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE chunks SET state='assigned', attempt=attempt+1,
			mirror_id=?, updated_at=? WHERE id=? AND state='pending'`, mirror, s.now(), c.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return fmt.Errorf("chunk %d was claimed concurrently", c.ID)
		}
		c.State = core.ChunkAssigned
		c.Attempt++
		c.MirrorIndex = int(mirror.Int64)
		c.HashOK = hashOK != 0
		found = true
		return nil
	})
	return c, found, err
}

// WriteChunkDone records bytes + digest + progress in one transaction.
func (s *Store) WriteChunkDone(ctx context.Context, c core.Chunk, resourceID, digest string, mirrorIndex int) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE chunks SET state='done', hash_ok=0, mirror_id=?, updated_at=?
			WHERE id=? AND state='assigned'`, mirrorIndex, s.now(), c.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return core.Err(core.E_INTERNAL, "chunk %d is not in assigned state", c.ID)
		}
		if resourceID != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM checksums WHERE resource_id=?
				AND scope='chunk' AND file_idx=? AND chunk_index=?`, resourceID, c.FileIndex, c.Index); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO checksums
				(resource_id,file_idx,algo,scope,chunk_index,value) VALUES(?,?,'sha256','chunk',?,?)`,
				resourceID, c.FileIndex, c.Index, digest); err != nil {
				return err
			}
		}
		return addProgressTx(ctx, tx, c.TaskID, c.Length, s.now())
	})
}

func (s *Store) Verified(ctx context.Context, c core.Chunk) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE chunks SET state='verified', hash_ok=1, updated_at=? WHERE id=?`,
			s.now(), c.ID)
		return err
	})
}

// Release returns a chunk to pending for retry, or marks it failed.
func (s *Store) Release(ctx context.Context, c core.Chunk, retry bool) error {
	state := string(core.ChunkFailed)
	if retry {
		state = string(core.ChunkPending)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE chunks SET state=?, updated_at=? WHERE id=?`, state, s.now(), c.ID)
		return err
	})
}

func (s *Store) ResetChunk(ctx context.Context, c core.Chunk) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE chunks SET state='pending', hash_ok=0, updated_at=? WHERE id=?`,
			s.now(), c.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET bytes_done=MAX(0,bytes_done-?), updated_at=? WHERE id=?`,
			c.Length, s.now(), string(c.TaskID))
		return err
	})
}

// ResetFailedChunks returns permanently failed chunks to pending. It is used
// by user-initiated Resume and by crash recovery (never silently auto-retry).
func (s *Store) ResetFailedChunks(ctx context.Context, taskID core.TaskID) (int, error) {
	n := 0
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE chunks SET state='pending', updated_at=?
			WHERE task_id=? AND state='failed'`, s.now(), string(taskID))
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		n = int(affected)
		return err
	})
	return n, err
}

// Chunks returns the full plan ordered by index.
func (s *Store) Chunks(ctx context.Context, taskID core.TaskID) ([]core.Chunk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,file_idx,idx,start,length,state,attempt,
		COALESCE(mirror_id,0),hash_ok FROM chunks WHERE task_id=? ORDER BY file_idx,idx`, string(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Chunk
	for rows.Next() {
		var c core.Chunk
		var hashOK int
		if err := rows.Scan(&c.ID, &c.TaskID, &c.FileIndex, &c.Index, &c.Start, &c.Length,
			&c.State, &c.Attempt, &c.MirrorIndex, &hashOK); err != nil {
			return nil, err
		}
		c.HashOK = hashOK != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChunkDigest returns the sha256 recorded for a chunk (empty if absent).
func (s *Store) ChunkDigest(ctx context.Context, resourceID string, fileIdx, idx int) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM checksums WHERE resource_id=? AND scope='chunk'
		AND file_idx=? AND chunk_index=? ORDER BY id DESC LIMIT 1`, resourceID, fileIdx, idx).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// Bitfield renders the done/verified chunk map as hex (reserved for L1 facade).
func (s *Store) Bitfield(ctx context.Context, taskID core.TaskID) (string, int, int, error) {
	chunks, err := s.Chunks(ctx, taskID)
	if err != nil {
		return "", 0, 0, err
	}
	buf := make([]byte, (len(chunks)+7)/8)
	verified := 0
	for i, c := range chunks {
		if c.State == core.ChunkDone || c.State == core.ChunkVerified {
			buf[i/8] |= 0x80 >> (i % 8)
		}
		if c.State == core.ChunkVerified {
			verified++
		}
	}
	return fmt.Sprintf("%x", buf), len(chunks), verified, nil
}

// PendingCount counts chunks still requiring network I/O.
func (s *Store) PendingCount(ctx context.Context, taskID core.TaskID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE task_id=? AND state IN ('pending','assigned')`,
		string(taskID)).Scan(&n)
	return n, err
}
