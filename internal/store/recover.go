package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"os"

	"github.com/asdfgh129/ud/internal/core"
)

// Recover implements docs/06 §5 / ADR-0015 step 5: it trusts only the chunks
// table plus the actual bytes on disk, never in-memory state.
//
//  1. Active states -> Paused.
//  2. `assigned` chunks -> `pending` (no memory state can be trusted).
//  3. Every `done`/`verified` chunk is re-hashed against its .part byte range;
//     a mismatch or a truncated file resets only that chunk.
//  4. bytes_done is recomputed from surviving chunks.
//
// It returns the IDs the Scheduler should re-enqueue.
func (s *Store) Recover(ctx context.Context) ([]core.TaskID, error) {
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET state='Paused', updated_at=?
			WHERE state IN ('Resolving','Running','PostProcessing','Seeding')`, s.now()); err != nil {
			return err
		}
		// `assigned` has no trustworthy in-memory owner; `failed` may be the
		// residue of the killed process, so both are retried after restart.
		_, err := tx.ExecContext(ctx, `UPDATE chunks SET state='pending', updated_at=?
			WHERE state IN ('assigned','failed')`, s.now())
		return err
	}); err != nil {
		return nil, err
	}

	// Failed tasks are NOT auto-retried: the user must call Resume explicitly.
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(resource_id,'') FROM tasks
		WHERE state IN ('Pending','Ready','Paused') ORDER BY priority DESC, created_at`)
	if err != nil {
		return nil, err
	}
	type pending struct {
		id         core.TaskID
		resourceID string
	}
	var tasks []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.resourceID); err != nil {
			rows.Close()
			return nil, err
		}
		tasks = append(tasks, p)
	}
	rows.Close()

	for _, p := range tasks {
		if err := s.verifyTaskChunks(ctx, p.id, p.resourceID); err != nil {
			return nil, err
		}
	}
	out := make([]core.TaskID, 0, len(tasks))
	for _, p := range tasks {
		out = append(out, p.id)
	}
	return out, nil
}

func (s *Store) verifyTaskChunks(ctx context.Context, id core.TaskID, resourceID string) error {
	tempPath, err := s.Option(ctx, id, "temp_path")
	if err != nil {
		return err
	}
	chunks, err := s.Chunks(ctx, id)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return nil
	}
	f, err := os.Open(tempPath)
	if err != nil {
		// No .part: every claimed chunk must be re-downloaded.
		for _, c := range chunks {
			if c.State == core.ChunkDone || c.State == core.ChunkVerified || c.State == core.ChunkAssigned {
				if err := s.ResetChunk(ctx, c); err != nil {
					return err
				}
			}
		}
		return s.SetProgress(ctx, id, 0, s.totalSize(chunks))
	}
	defer f.Close()

	var done int64
	for _, c := range chunks {
		if c.State != core.ChunkDone && c.State != core.ChunkVerified {
			continue
		}
		want, err := s.ChunkDigest(ctx, resourceID, c.FileIndex, c.Index)
		if err != nil {
			return err
		}
		ok := want != "" && hashRange(f, c) == want
		if !ok {
			if err := s.ResetChunk(ctx, c); err != nil {
				return err
			}
			continue
		}
		done += c.Length
	}
	return s.SetProgress(ctx, id, done, s.totalSize(chunks))
}

func (s *Store) totalSize(chunks []core.Chunk) int64 {
	var n int64
	seen := make(map[int]bool)
	for _, c := range chunks {
		if !seen[c.Index] {
			n += c.Length
			seen[c.Index] = true
		}
	}
	return n
}

func hashRange(f *os.File, c core.Chunk) string {
	if c.Length == 0 {
		h := sha256.Sum256(nil)
		return hex.EncodeToString(h[:])
	}
	buf := make([]byte, 32<<10)
	r := io.NewSectionReader(f, c.Start, c.Length)
	h := sha256.New()
	if _, err := io.CopyBuffer(h, r, buf); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyAllChunks is used before atomic commit; it returns chunks whose bytes
// no longer match their recorded digest (docs/12 §4 external tampering case).
func (s *Store) VerifyAllChunks(ctx context.Context, id core.TaskID, resourceID, tempPath string) ([]core.Chunk, error) {
	chunks, err := s.Chunks(ctx, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(tempPath)
	if err != nil {
		return chunks, err
	}
	defer f.Close()
	var bad []core.Chunk
	for _, c := range chunks {
		if c.State != core.ChunkDone && c.State != core.ChunkVerified {
			bad = append(bad, c)
			continue
		}
		want, err := s.ChunkDigest(ctx, resourceID, c.FileIndex, c.Index)
		if err != nil {
			return nil, err
		}
		if want == "" || hashRange(f, c) != want {
			bad = append(bad, c)
			continue
		}
		if c.State == core.ChunkDone {
			if err := s.Verified(ctx, c); err != nil {
				return nil, err
			}
		}
	}
	return bad, nil
}

// WholeFileChecksum verifies the final file against Resource.Checksum.Whole.
func WholeFileChecksum(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
