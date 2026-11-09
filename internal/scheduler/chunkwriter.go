package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/store"
)

// chunkWriter is the scheduler-owned implementation of core.ChunkWriter.
// The .part file is the only place bytes go; all state lives in SQLite.
type chunkWriter struct {
	st          *store.Store
	taskID      core.TaskID
	resourceID  string
	tempPath    string
	f           *os.File
	mu          sync.Mutex
	bus         *core.EventBus
	maxAttempts int
	onWrite     func(int64)
	lastEvent   time.Time
}

func newChunkWriter(st *store.Store, taskID core.TaskID, resourceID, tempPath string,
	total int64, bus *core.EventBus, maxAttempts int, onWrite func(int64)) (*chunkWriter, error) {
	if err := os.MkdirAll(filepath.Dir(tempPath), 0o755); err != nil {
		return nil, core.Err(core.E_STORAGE_FULL, "create output directory: %v", err)
	}
	f, err := os.OpenFile(tempPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, core.Err(core.E_STORAGE_FULL, "open .part file: %v", err)
	}
	if total > 0 {
		if err := f.Truncate(total); err != nil {
			f.Close()
			return nil, core.Err(core.E_STORAGE_FULL, "preallocate: %v", err)
		}
	}
	return &chunkWriter{
		st: st, taskID: taskID, resourceID: resourceID, tempPath: tempPath,
		f: f, bus: bus, maxAttempts: maxAttempts, onWrite: onWrite,
	}, nil
}

func (w *chunkWriter) Next(ctx context.Context) (core.Chunk, bool, error) {
	return w.st.Next(ctx, w.taskID)
}

func (w *chunkWriter) WriteChunk(ctx context.Context, c core.Chunk, p []byte) error {
	if int64(len(p)) != c.Length {
		return core.Err(core.E_INTERNAL, "chunk %d byte count mismatch: want %d got %d", c.Index, c.Length, len(p))
	}
	w.mu.Lock()
	_, err := w.f.WriteAt(p, c.Start)
	w.mu.Unlock()
	if err != nil {
		return core.Err(core.E_STORAGE_FULL, "write chunk %d: %v", c.Index, err)
	}
	sum := sha256.Sum256(p)
	if err := w.st.WriteChunkDone(ctx, c, w.resourceID, hex.EncodeToString(sum[:]), c.MirrorIndex); err != nil {
		return err
	}
	if w.onWrite != nil {
		w.onWrite(c.Length)
	}
	w.publishProgress(ctx)
	return nil
}

func (w *chunkWriter) Verified(ctx context.Context, c core.Chunk, _ core.Hash) error {
	return w.st.Verified(ctx, c)
}

func (w *chunkWriter) Release(ctx context.Context, c core.Chunk, err error) error {
	e := core.AsError(err)
	code := core.E_INTERNAL
	if e != nil {
		code = e.Code
	}
	w.bus.Publish(core.Event{
		Type:   core.EventTaskChunkFailed,
		TaskID: w.taskID,
		Payload: map[string]any{
			"chunk": c.Index, "attempt": c.Attempt, "mirror": c.MirrorIndex, "errorCode": code,
		},
	})
	// Chunk bookkeeping must survive an intentional cancel (pause/kill).
	// A cancellation is never a terminal chunk failure: the bytes simply were
	// not finished and must be retried after resume / crash recovery.
	dbCtx := context.WithoutCancel(ctx)
	cancelled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
	retry := cancelled || (core.IsRetryable(err) && c.Attempt < w.maxAttempts)
	return w.st.Release(dbCtx, c, retry)
}

func (w *chunkWriter) publishProgress(ctx context.Context) {
	w.mu.Lock()
	if time.Since(w.lastEvent) < 500*time.Millisecond {
		w.mu.Unlock()
		return
	}
	w.lastEvent = time.Now()
	w.mu.Unlock()
	done, total := w.currentProgress(ctx)
	w.bus.Publish(core.Event{
		Type:    core.EventTaskProgress,
		TaskID:  w.taskID,
		Payload: map[string]any{"bytesDone": done, "bytesTotal": total},
	})
}

func (w *chunkWriter) currentProgress(ctx context.Context) (int64, int64) {
	t, err := w.st.GetTask(ctx, w.taskID)
	if err != nil {
		return 0, 0
	}
	return t.BytesDone, t.BytesTotal
}

// Sync flushes the .part file before the atomic rename.
func (w *chunkWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Sync()
}

func (w *chunkWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
