package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/asdfgh129/ud/internal/core"
)

// InsertEvent appends a redacted audit event (docs/11 §3).
func (s *Store) InsertEvent(ctx context.Context, e core.Event) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO events(task_id,type,payload,ts) VALUES(?,?,?,?)`,
			nullable(string(e.TaskID)), string(e.Type), string(payload), e.Ts)
		return err
	})
}

// Events returns recent audit events, newest last.
func (s *Store) Events(ctx context.Context, limit int) ([]core.Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(task_id,''),type,payload,ts FROM
		(SELECT * FROM events ORDER BY id DESC LIMIT ?) ORDER BY id ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Event
	for rows.Next() {
		var e core.Event
		var taskID, payload string
		if err := rows.Scan(&taskID, &e.Type, &payload, &e.Ts); err != nil {
			return nil, err
		}
		e.TaskID = core.TaskID(taskID)
		_ = json.Unmarshal([]byte(payload), &e.Payload)
		out = append(out, e)
	}
	return out, rows.Err()
}
