package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

func mustInsertTask(t *testing.T, st *Store, state core.TaskState, kind core.InputKind, input string) core.TaskID {
	t.Helper()
	now := time.Now().UnixMilli()
	id := core.NewID()
	if err := st.InsertTask(context.Background(), &core.Task{
		ID: id, State: state, Input: input, InputKind: kind,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustSaveResource(t *testing.T, st *Store, id core.TaskID, size int64) *core.Resource {
	t.Helper()
	res := &core.Resource{ID: "res-" + string(id), Kind: core.KindSingle, Engine: core.EngineGo,
		Files:   []core.ResourceFile{{Index: 0, Path: "a.bin", Size: size, Selected: true}},
		Mirrors: []core.Mirror{{URL: "https://x/a.bin", Priority: 1, Weight: 1}}}
	if err := st.SaveResource(context.Background(), id, res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestOpenWithDefaultsUsesWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	st, err := Open("", nil) // empty path and nil bus must fall back to defaults
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Bus() == nil {
		t.Fatal("nil bus must be replaced with a default event bus")
	}
	if st.Now() == 0 {
		t.Fatal("Now() must return a wall-clock timestamp")
	}
	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if st.DB() == nil {
		t.Fatal("DB() must expose the handle")
	}
	if _, err := os.Stat("ud.db"); err != nil {
		t.Fatalf("default database file not created: %v", err)
	}
}

func TestOpenFailsOnUnwritablePath(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "missing", "state.db"), nil); err == nil {
		t.Fatal("opening a database in a missing directory must fail")
	}
}

func TestMetaRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.Meta(ctx, "absent"); err != sql.ErrNoRows {
		t.Fatalf("missing meta key must return sql.ErrNoRows, got %v", err)
	}
	if err := st.SetMeta(ctx, "install_id", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta(ctx, "install_id", "def"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if v, err := st.Meta(ctx, "install_id"); err != nil || v != "def" {
		t.Fatalf("Meta=%q err=%v", v, err)
	}
	if v, err := st.SchemaVersion(ctx); err != nil || v < 1 {
		t.Fatalf("schema version: %d %v", v, err)
	}
}

func TestEventsAppendAndRead(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	bus := st.Bus()
	if bus == nil {
		t.Fatal("store must own an event bus")
	}
	id := mustInsertTask(t, st, core.StatePending, core.InputURL, "https://x/a")
	if err := st.InsertEvent(ctx, core.Event{Type: core.EventTaskCreated, TaskID: id,
		Payload: map[string]any{"state": "Pending"}, Ts: 11}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvent(ctx, core.Event{Type: core.EventConfigChanged, Ts: 12}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvent(ctx, core.Event{Type: core.EventTaskProgress,
		Payload: map[string]any{"bad": make(chan int)}, Ts: 13}); err == nil {
		t.Fatal("unmarshalable payload must be rejected")
	}
	events, err := st.Events(ctx, 0) // default limit
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != core.EventTaskCreated || events[0].TaskID != id ||
		events[0].Payload["state"] != "Pending" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].TaskID != "" {
		t.Fatalf("event without task must have an empty task ID, got %q", events[1].TaskID)
	}
	if _, err := st.Events(ctx, 10_000); err != nil { // clamped, must not fail
		t.Fatal(err)
	}
}

func TestListTasksPaginationAndFilters(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	paused := make([]core.TaskID, 0, 2)
	for i := 0; i < 3; i++ {
		id := mustInsertTask(t, st, core.StatePending, core.InputURL, "https://x/a")
		if i == 0 {
			if err := st.UpdateState(ctx, id, StateUpdate{To: core.StatePaused, Force: true}); err != nil {
				t.Fatal(err)
			}
			paused = append(paused, id)
		}
	}
	mustInsertTask(t, st, core.StatePending, core.InputMagnet, "magnet:?xt=x")

	all, total, err := st.ListTasks(ctx, core.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(all) != 4 {
		t.Fatalf("total=%d len=%d", total, len(all))
	}
	// IDs sort descending: newest first.
	if all[0].ID < all[1].ID {
		t.Fatalf("list must be ordered newest first: %v", all[0].ID)
	}
	page, _, err := st.ListTasks(ctx, core.TaskFilter{Limit: 1})
	if err != nil || len(page) != 1 {
		t.Fatalf("limit ignored: %d %v", len(page), err)
	}
	next, _, err := st.ListTasks(ctx, core.TaskFilter{Cursor: string(page[0].ID), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range next {
		if task.ID >= page[0].ID {
			t.Fatalf("cursor must exclude newer tasks, got %v", task.ID)
		}
	}
	onlyPaused, _, err := st.ListTasks(ctx, core.TaskFilter{State: core.StatePaused})
	if err != nil || len(onlyPaused) != 1 || onlyPaused[0].ID != paused[0] {
		t.Fatalf("state filter broken: %+v err=%v", onlyPaused, err)
	}
	onlyMagnets, _, err := st.ListTasks(ctx, core.TaskFilter{Kind: core.InputMagnet})
	if err != nil || len(onlyMagnets) != 1 || onlyMagnets[0].InputKind != core.InputMagnet {
		t.Fatalf("kind filter broken: %+v err=%v", onlyMagnets, err)
	}
}

func TestCommitWritesFinalPathAndCompletion(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mustInsertTask(t, st, core.StateRunning, core.InputURL, "https://x/a")
	res := mustSaveResource(t, st, id, 4)
	final := filepath.Join(t.TempDir(), "a.bin")
	if err := st.Commit(ctx, id, res.ID, 0, final); err != nil {
		t.Fatal(err)
	}
	task, err := st.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != core.StateCompleted || task.CompletedAt == nil {
		t.Fatalf("commit must complete the task: %+v", task)
	}
	paths, err := st.DeleteTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != final {
		t.Fatalf("DeleteTask must return final paths, got %v", paths)
	}
	if _, err := st.DeleteTask(ctx, id); err == nil ||
		core.AsError(err).Code != core.E_TASK_NOT_FOUND {
		t.Fatalf("second delete must report not found, got %v", err)
	}
}

func TestSetFinalPath(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mustInsertTask(t, st, core.StateRunning, core.InputURL, "https://x/a")
	res := mustSaveResource(t, st, id, 4)
	if err := st.SetFinalPath(ctx, res.ID, 0, "/tmp/final.bin"); err != nil {
		t.Fatal(err)
	}
	paths, err := st.DeleteTask(ctx, id)
	if err != nil || len(paths) != 1 || paths[0] != "/tmp/final.bin" {
		t.Fatalf("final path not persisted: %v %v", paths, err)
	}
}

func TestUpdateFileSizes(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mustInsertTask(t, st, core.StateResolving, core.InputURL, "https://x/a")
	res := mustSaveResource(t, st, id, 0)
	if err := st.UpdateFileSizes(ctx, res.ID, map[int]int64{0: 4096}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetResource(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Files[0].Size != 4096 {
		t.Fatalf("size not updated: %d", got.Files[0].Size)
	}
}

func TestReleaseAndResetFailedChunks(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mustInsertTask(t, st, core.StateRunning, core.InputURL, "https://x/a")
	_ = mustSaveResource(t, st, id, 8)
	if _, err := st.CreateChunks(ctx, id, 0, 8, 4); err != nil {
		t.Fatal(err)
	}
	c1, ok, err := st.Next(ctx, id)
	if err != nil || !ok {
		t.Fatalf("next: %v %v", ok, err)
	}
	if err := st.Release(ctx, c1, false); err != nil {
		t.Fatal(err)
	}
	chunks, _ := st.Chunks(ctx, id)
	if chunks[0].State != core.ChunkFailed {
		t.Fatalf("non-retryable release must fail the chunk, got %s", chunks[0].State)
	}
	if n, err := st.ResetFailedChunks(ctx, id); err != nil || n != 1 {
		t.Fatalf("ResetFailedChunks=%d err=%v", n, err)
	}
	c1, ok, err = st.Next(ctx, id)
	if err != nil || !ok {
		t.Fatalf("next after reset: %v %v", ok, err)
	}
	if err := st.Release(ctx, c1, true); err != nil {
		t.Fatal(err)
	}
	chunks, _ = st.Chunks(ctx, id)
	if chunks[0].State != core.ChunkPending || chunks[0].Attempt < 1 {
		t.Fatalf("retryable release must return to pending, got %+v", chunks[0])
	}
	if n, err := st.ResetFailedChunks(ctx, id); err != nil || n != 0 {
		t.Fatalf("no failed chunks expected: %d %v", n, err)
	}
}

func TestVerifyAllChunksDetectsTampering(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mustInsertTask(t, st, core.StateRunning, core.InputURL, "https://x/a")
	res := mustSaveResource(t, st, id, 8)
	dir := t.TempDir()
	part := filepath.Join(dir, "a.bin.part")
	if err := os.WriteFile(part, []byte("AAAABBBB"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateChunks(ctx, id, 0, 8, 4); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.Next(ctx, id)
	if err != nil || !ok {
		t.Fatalf("next: %v %v", ok, err)
	}
	sum := sha256.Sum256([]byte("AAAA"))
	if err := st.WriteChunkDone(ctx, c, res.ID, hex.EncodeToString(sum[:]), 0); err != nil {
		t.Fatal(err)
	}
	bad, err := st.VerifyAllChunks(ctx, id, res.ID, part)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0].Index != 1 {
		t.Fatalf("only the pending chunk should be reported, got %+v", bad)
	}
	if d, err := st.ChunkDigest(ctx, res.ID, 0, 0); err != nil ||
		d != hex.EncodeToString(sum[:]) {
		t.Fatalf("chunk digest missing: %q %v", d, err)
	}
	// External tampering: rewrite the verified range.
	if err := os.WriteFile(part, []byte("XXXXBBBB"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad, err = st.VerifyAllChunks(ctx, id, res.ID, part)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range bad {
		if c.Index == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("tampered chunk not detected: %+v", bad)
	}
	if _, err := st.VerifyAllChunks(ctx, id, res.ID, filepath.Join(dir, "absent.part")); err == nil {
		t.Fatal("missing .part file must be an error")
	}
}

func TestWholeFileChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := WholeFileChecksum(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("payload"))
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("checksum=%s", sum)
	}
	if _, err := WholeFileChecksum(context.Background(), filepath.Join(dir, "nope")); err == nil {
		t.Fatal("missing file must fail")
	}
}

func TestHashRangeEmptyChunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sum := sha256.Sum256(nil)
	if got := hashRange(f, core.Chunk{Start: 0, Length: 0}); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("zero-length chunk digest=%q", got)
	}
}

func TestCreateChunksZeroLengthAndIdempotency(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mustInsertTask(t, st, core.StateRunning, core.InputURL, "https://x/empty")
	_ = mustSaveResource(t, st, id, 0)
	n, err := st.CreateChunks(ctx, id, 0, 0, 0) // 0 size + invalid chunk size fallback
	if err != nil || n != 1 {
		t.Fatalf("zero-length plan: %d %v", n, err)
	}
	again, err := st.CreateChunks(ctx, id, 0, 0, 0)
	if err != nil || again != 1 {
		t.Fatalf("CreateChunks must be idempotent, got %d %v", again, err)
	}
	if pending, err := st.PendingCount(ctx, id); err != nil || pending != 1 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
}

func TestGetMissingRecords(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.GetTask(ctx, "task-missing"); err == nil ||
		core.AsError(err).Code != core.E_TASK_NOT_FOUND {
		t.Fatalf("missing task: %v", err)
	}
	if _, err := st.GetResource(ctx, "res-missing"); err == nil {
		t.Fatal("missing resource must fail")
	}
	if _, err := st.Option(ctx, "task-missing", "temp_path"); err != nil {
		t.Fatalf("missing option must be empty, not an error: %v", err)
	}
	if err := st.UpdateState(ctx, "task-missing", StateUpdate{To: core.StateReady}); err == nil ||
		core.AsError(err).Code != core.E_TASK_NOT_FOUND {
		t.Fatalf("missing state update: %v", err)
	}
	if err := st.SaveOptions(ctx, "task-missing", core.TaskOptions{}); err != nil {
		// No FK enforcement guarantee across pooled connections: the write may
		// succeed silently. Accept either outcome as long as it does not panic.
		t.Logf("SaveOptions on missing task returned: %v", err)
	}
}

func TestCorruptOptionsAreReported(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := mustInsertTask(t, st, core.StatePending, core.InputURL, "https://x/a")
	if err := st.SetOption(ctx, id, "options", "{not-json"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTask(ctx, id); err == nil {
		t.Fatal("corrupt options JSON must surface as an error")
	}
}

// Regression for the CI-only "database is locked (5) (SQLITE_BUSY)" failure
// seen in scheduler.TestLifecycleErrors (2026-11-13). Under WAL, SQLite can
// return SQLITE_BUSY_SNAPSHOT without invoking busy_timeout, so withTx must
// replay the whole (rolled-back) transaction a bounded number of times.
func TestBusyErrorClassification(t *testing.T) {
	cases := []struct {
		err  error
		busy bool
	}{
		{nil, false},
		{errors.New("database is locked (5) (SQLITE_BUSY)"), true},
		{errors.New("database table is locked"), true},
		{errors.New("SQLITE_BUSY_SNAPSHOT"), true},
		{errors.New("no such table: tasks"), false},
		{errors.New("context canceled"), false},
	}
	for _, c := range cases {
		if got := isBusy(c.err); got != c.busy {
			t.Errorf("isBusy(%v) = %t, want %t", c.err, got, c.busy)
		}
	}
}

func TestWithTxReplaysTransientBusy(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	attempts := 0
	err := st.withTx(ctx, func(tx *sql.Tx) error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO app_meta(key,value) VALUES('busy-retry','ok')`)
		return err
	})
	if err != nil {
		t.Fatalf("transient BUSY was not recovered: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	got, err := st.Meta(ctx, "busy-retry")
	if err != nil || got != "ok" {
		t.Fatalf("committed value = %q, err=%v", got, err)
	}
}

func TestWithTxStopsAfterBusyRetries(t *testing.T) {
	st := newTestStore(t)
	attempts := 0
	err := st.withTx(context.Background(), func(*sql.Tx) error {
		attempts++
		return errors.New("database is locked")
	})
	if err == nil {
		t.Fatal("a permanently locked database must still fail")
	}
	if attempts != maxBusyRetries+1 {
		t.Fatalf("attempts = %d, want %d (bounded retry)", attempts, maxBusyRetries+1)
	}
}

// Many goroutines sharing one Store must never leak a transient SQLite lock:
// the single write mutex plus BUSY replay keeps every write successful.
func TestConcurrentWritesRemainSerialized(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- st.SetMeta(ctx, fmt.Sprintf("writer-%d", i), fmt.Sprint(i))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write failed: %v", err)
		}
	}
	for i := 0; i < writers; i++ {
		got, err := st.Meta(ctx, fmt.Sprintf("writer-%d", i))
		if err != nil || got != fmt.Sprint(i) {
			t.Fatalf("writer-%d: got %q, err=%v", i, got, err)
		}
	}
}
