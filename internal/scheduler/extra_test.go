package scheduler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/postproc"
	"github.com/asdfgh129/ud/internal/resolve"
	"github.com/asdfgh129/ud/internal/security"
	"github.com/asdfgh129/ud/internal/store"
	"github.com/asdfgh129/ud/internal/transport"
	httptransport "github.com/asdfgh129/ud/internal/transport/http"
)

// newHarnessAt builds the full scheduler stack against a caller-supplied origin
// so tests can script failures (404, 5xx, ...).
func newHarnessAt(t *testing.T, srv *httptest.Server) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DownloadDir = dir
	cfg.Global.ChunkSize = "4KiB"
	cfg.Global.MaxDownloadRate = "0"
	cfg.Security.AllowLoopbackForDev = true
	mgr := config.NewManager(cfg, nil)
	bus := core.NewEventBus(256)
	st, err := store.Open(filepath.Join(dir, "state.db"), bus)
	if err != nil {
		t.Fatal(err)
	}
	guard := security.NewDevGuard()
	resolvers := resolve.NewRegistry()
	resolvers.Register(resolve.NewDirectHTTP(guard))
	resolvers.Register(resolve.Unsupported{})
	transports := transport.NewRegistry()
	transports.Register(httptransport.New(guard))
	s, err := New(Options{Store: st, Resolvers: resolvers, Transports: transports,
		PostProcessors: postproc.NewRegistry(), Guard: guard,
		Credentials: security.NewMemoryCredentialStore(), Config: mgr, Bus: bus})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); st.Close(); srv.Close() })
	return &harness{s: s, st: st, srv: srv, dir: dir, events: bus}
}

func TestNewRequiresStoreAndConfig(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without store/config must fail")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := New(Options{Store: st}); err == nil {
		t.Fatal("New without config must fail")
	}
}

func TestNewFallsBackToDefaultBusAndLogger(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := New(Options{Store: st, Config: config.NewManager(config.Defaults(), nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.bus == nil || s.log == nil {
		t.Fatal("New must install default bus and logger")
	}
	if h := s.Health(context.Background()); h.Status != "ok" || h.Version == "" {
		t.Fatalf("unexpected health: %+v", h)
	}
}

// docs/13 §7: maxConcurrentTasks is patchable at runtime; the change callback
// must reach both the slot pool and the single Limiter.
func TestConfigPatchPropagates(t *testing.T) {
	h := newHarness(t, 1024)
	next, err := h.s.ConfigManager().Patch(map[string]any{
		"global.maxConcurrentTasks": 2, "global.maxDownloadRate": "1MiB/s"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Global.MaxConcurrentTasks != 2 {
		t.Fatalf("patch not stored: %+v", next.Global)
	}
	if got := h.s.slots.max; got != 2 {
		t.Fatalf("slot pool not resized: %d", got)
	}
	if got := h.s.limiter.GlobalRate(); got != core.BytesPerSec(1<<20) {
		t.Fatalf("limiter not updated: %d", got)
	}
	// The hook also calls Limiter.Apply, which must push the upload bucket.
	h.s.limiter.Apply(config.GlobalConfig{MaxDownloadRate: "2MiB/s", MaxUploadRate: "512KiB/s"})
	if got := h.s.limiter.GlobalRate(); got != core.BytesPerSec(2<<20) {
		t.Fatalf("Apply did not update the global rate: %d", got)
	}
}

func TestControlPlaneSurface(t *testing.T) {
	h := newHarness(t, 1024)
	ctx := context.Background()
	caps := h.s.Capabilities()
	if len(caps.Resolvers) != 2 || caps.RPCLevel != "L0" || len(caps.Sidecars) != 0 {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if caps.Transports[0] != "http" {
		t.Fatalf("transport not advertised: %+v", caps.Transports)
	}
	if h.s.ConfigManager() == nil || h.s.Credentials() == nil {
		t.Fatal("accessors must return the wired collaborators")
	}

	id := h.add(t, core.TaskOptions{MaxConnections: 1})
	tasks, total, err := h.s.List(ctx, core.TaskFilter{})
	if err != nil || total != 1 || len(tasks) != 1 || tasks[0].ID != id {
		t.Fatalf("list: %+v total=%d err=%v", tasks, total, err)
	}
	stats, err := h.s.Stats(ctx)
	if err != nil || stats.Tasks != 1 {
		t.Fatalf("stats: %+v err=%v", stats, err)
	}
	if d, err := h.s.Get(ctx, id); err != nil || d.ID != id {
		t.Fatalf("get: %+v err=%v", d, err)
	}
	if _, err := h.s.Get(ctx, "task-missing"); err == nil {
		t.Fatal("missing task must error")
	}
	if err := h.s.Delete(ctx, "task-missing", false); err == nil {
		t.Fatal("deleting a missing task must error")
	}
}

func TestResolveOnlyUsesRegistry(t *testing.T) {
	h := newHarness(t, 16)
	res, err := h.s.ResolveOnly(context.Background(),
		core.Input{Raw: "https://example.com/a.bin", Kind: core.InputURL}, core.TaskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files[0].Path != "a.bin" || len(res.Mirrors) != 1 {
		t.Fatalf("unexpected resolution: %+v", res)
	}
	// A P1 BT input is now resolved by the magnet resolver when registered.
	// With only DirectHTTP + Unsupported present, it still reaches Unsupported,
	// which classifies it as an already-implemented class whose resolver was not
	// wired into this particular registry — an explicit degradation, never a silent pass.
	_, err = h.s.ResolveOnly(context.Background(),
		core.Input{Raw: "magnet:?xt=urn:btih:abc", Kind: core.InputMagnet}, core.TaskOptions{})
	if e := core.AsError(err); e == nil || e.Code != core.E_RESOLVE_FAILED || !strings.Contains(e.Message, "Resolver") {
		t.Fatalf("P1 input must degrade explicitly, got %v", err)
	}
}

func TestSetOptionPersistsAndAppliesTaskRate(t *testing.T) {
	h := newHarness(t, 16)
	ctx := context.Background()
	id := h.add(t, core.TaskOptions{MaxConnections: 1})
	if err := h.s.SetOption(ctx, id, core.TaskOptions{
		MaxConnections: 7, MaxDownloadLimit: "256KiB/s", Priority: 3, PostProcess: "none",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := h.st.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Options.MaxConnections != 7 || task.Options.Priority != 3 ||
		task.Options.MaxDownloadLimit != "256KiB/s" {
		t.Fatalf("options not merged: %+v", task.Options)
	}
	h.s.limiter.mu.RLock()
	_, ok := h.s.limiter.perTask[string(id)]
	h.s.limiter.mu.RUnlock()
	if !ok {
		t.Fatal("per-task rate was not installed on the limiter")
	}
	if err := h.s.SetOption(ctx, "task-missing", core.TaskOptions{}); err == nil {
		t.Fatal("SetOption on a missing task must fail")
	}
}

func TestDeleteRemovesOutputsWhenRequested(t *testing.T) {
	h := newHarness(t, 1024)
	ctx := context.Background()
	id := h.add(t, core.TaskOptions{MaxConnections: 1})
	if err := h.s.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, id, core.StateCanceled, 5*time.Second)

	final := filepath.Join(h.dir, "final.bin")
	if err := os.WriteFile(final, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	part := final + ".part"
	if err := os.WriteFile(part, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := &core.Resource{ID: "res-" + string(id), Kind: core.KindSingle, Engine: core.EngineGo,
		Files: []core.ResourceFile{{Index: 0, Path: "final.bin", Selected: true}}}
	if err := h.st.SaveResource(ctx, id, res); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetFinalPath(ctx, res.ID, 0, final); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Delete(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{final, part} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should have been removed", p)
		}
	}
}

func TestLifecycleErrors(t *testing.T) {
	h := newHarness(t, 1024)
	ctx := context.Background()
	id := h.add(t, core.TaskOptions{MaxConnections: 1})
	if err := h.s.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, id, core.StateCanceled, 5*time.Second)
	if err := h.s.Pause(ctx, id); err == nil ||
		core.AsError(err).Code != core.E_ILLEGAL_TRANSITION {
		t.Fatalf("pausing a terminal task must be rejected, got %v", err)
	}
	if err := h.s.Resume(ctx, id); err == nil ||
		core.AsError(err).Code != core.E_ILLEGAL_TRANSITION {
		t.Fatalf("resuming a canceled task must be rejected, got %v", err)
	}
	for name, fn := range map[string]func(context.Context, core.TaskID) error{
		"pause": h.s.Pause, "resume": h.s.Resume, "cancel": h.s.Cancel,
	} {
		if err := fn(ctx, "task-missing"); err == nil ||
			core.AsError(err).Code != core.E_TASK_NOT_FOUND {
			t.Fatalf("%s on missing task: %v", name, err)
		}
	}
}

// A permanent chunk failure must end in Failed (never a silent hang), and it
// must be re-queued by Resume.
func TestFailedChunksDriveTaskToFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "4096")
			w.Header().Set("Accept-Ranges", "bytes")
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	h := newHarnessAt(t, srv)
	id := h.add(t, core.TaskOptions{MaxConnections: 1})
	d := h.waitState(t, id, core.StateFailed, 30*time.Second)
	if d.ErrorKind != core.KindTransient.String() {
		t.Fatalf("error kind=%q want %q", d.ErrorKind, core.KindTransient.String())
	}
	if d.ErrorMessage == "" {
		t.Fatal("failure must record a message")
	}
	if err := h.s.Resume(context.Background(), id); err != nil {
		t.Fatalf("resume after failure: %v", err)
	}
}

func TestInjectHeadersMergesCredential(t *testing.T) {
	h := newHarness(t, 16)
	ctx := context.Background()
	ref, err := h.s.Credentials().Put(ctx, "Bearer SECRET")
	if err != nil {
		t.Fatal(err)
	}
	r := &core.Resource{}
	h.s.injectHeaders(ctx, r, core.TaskOptions{
		Headers: map[string]string{"Referer": "https://x"}, CredentialRef: ref})
	if r.Headers["Referer"] != "https://x" || r.Headers["Authorization"] != "Bearer SECRET" {
		t.Fatalf("headers not injected: %+v", r.Headers)
	}
	// An unknown reference degrades to no Authorization header.
	r2 := &core.Resource{}
	h.s.injectHeaders(ctx, r2, core.TaskOptions{CredentialRef: "mem://missing"})
	if _, ok := r2.Headers["Authorization"]; ok {
		t.Fatalf("unknown credential must not add a header: %+v", r2.Headers)
	}
}

func TestHostOf(t *testing.T) {
	if got := hostOf("https://example.com:8443/a"); got != "example.com:8443" {
		t.Fatalf("hostOf=%q", got)
	}
	if got := hostOf("http://[::1/x"); got != "" {
		t.Fatalf("malformed URL must yield empty host, got %q", got)
	}
}

// ---------- chunkWriter ----------

func newWriterFixture(t *testing.T) (*store.Store, core.TaskID, *core.Resource, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"), core.NewEventBus(32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	now := time.Now().UnixMilli()
	id := core.NewID()
	if err := st.InsertTask(ctx, &core.Task{ID: id, State: core.StateRunning,
		Input: "https://x/a", InputKind: core.InputURL, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	res := &core.Resource{ID: "res-" + string(id), Kind: core.KindSingle, Engine: core.EngineGo,
		Files: []core.ResourceFile{{Index: 0, Path: "a.bin", Size: 8, Selected: true}}}
	if err := st.SaveResource(ctx, id, res); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateChunks(ctx, id, 0, 8, 4); err != nil {
		t.Fatal(err)
	}
	return st, id, res, filepath.Join(dir, "a.bin.part")
}

func TestChunkWriterWritesVerifiesAndCloses(t *testing.T) {
	st, id, res, part := newWriterFixture(t)
	ctx := context.Background()
	var written int64
	bus := core.NewEventBus(8)
	events, cancel := bus.Subscribe()
	defer cancel()
	w, err := newChunkWriter(st, id, res.ID, part, 8, bus, 3, func(n int64) { written += n })
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := w.Next(ctx)
	if err != nil || !ok {
		t.Fatalf("next: %v %v", ok, err)
	}
	if err := w.WriteChunk(ctx, c, []byte("AAAA")); err != nil {
		t.Fatal(err)
	}
	if written != 4 {
		t.Fatalf("onWrite not called, got %d", written)
	}
	data, err := os.ReadFile(part)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "AAAA" {
		t.Fatalf("bytes not written at the chunk offset: %q", data)
	}
	if err := w.Verified(ctx, c, core.Hash{}); err != nil {
		t.Fatal(err)
	}
	chunks, _ := st.Chunks(ctx, id)
	if chunks[0].State != core.ChunkVerified || !chunks[0].HashOK {
		t.Fatalf("verified state not persisted: %+v", chunks[0])
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case e := <-events:
		if e.Type != core.EventTaskProgress {
			t.Fatalf("unexpected event %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("writing a chunk must publish progress")
	}
}

func TestChunkWriterErrorPaths(t *testing.T) {
	st, id, res, part := newWriterFixture(t)
	ctx := context.Background()
	w, err := newChunkWriter(st, id, res.ID, part, 8, core.NewEventBus(8), 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	c, ok, err := w.Next(ctx)
	if err != nil || !ok {
		t.Fatalf("next: %v %v", ok, err)
	}
	if err := w.WriteChunk(ctx, c, []byte("AA")); err == nil ||
		core.AsError(err).Code != core.E_INTERNAL {
		t.Fatalf("length mismatch must be rejected, got %v", err)
	}
	if done, total := w.currentProgress(ctx); done != 0 || total != 0 {
		t.Fatalf("progress of an unknown task must be zero, got %d/%d", done, total)
	}
	// A path whose parent is a regular file cannot be created.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newChunkWriter(st, id, res.ID, filepath.Join(blocker, "a.part"), 8, nil, 3, nil); err == nil ||
		core.AsError(err).Code != core.E_STORAGE_FULL {
		t.Fatalf("unwritable path must report E_STORAGE_FULL, got %v", err)
	}
}

func TestChunkWriterReleasePublishesAndRetries(t *testing.T) {
	st, id, res, part := newWriterFixture(t)
	bus := core.NewEventBus(8)
	events, cancel := bus.Subscribe()
	defer cancel()
	w, err := newChunkWriter(st, id, res.ID, part, 8, bus, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx := context.Background()
	c, ok, err := w.Next(ctx)
	if err != nil || !ok {
		t.Fatalf("next: %v %v", ok, err)
	}
	if err := w.Release(ctx, c, core.Err(core.E_TRANSIENT_NETWORK, "boom")); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Type != core.EventTaskChunkFailed {
			t.Fatalf("unexpected event %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("release must publish a chunk-failed event")
	}
	chunks, _ := st.Chunks(ctx, id)
	if chunks[0].State != core.ChunkPending {
		t.Fatalf("retryable failure must return to pending, got %s", chunks[0].State)
	}
	// Cancellation is never a terminal chunk failure either.
	c2, _, _ := w.Next(ctx)
	if err := w.Release(ctx, c2, context.Canceled); err != nil {
		t.Fatal(err)
	}
	chunks, _ = st.Chunks(ctx, id)
	if chunks[0].State != core.ChunkPending {
		t.Fatalf("cancellation must keep the chunk retryable, got %s", chunks[0].State)
	}
}
