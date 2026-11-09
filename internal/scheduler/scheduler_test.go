package scheduler

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

type harness struct {
	s      *Scheduler
	st     *store.Store
	srv    *httptest.Server
	dir    string
	data   []byte
	events *core.EventBus
}

func newHarness(t *testing.T, size int) *harness {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "payload.bin", time.Time{}, bytes.NewReader(data))
	}))
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DownloadDir = dir
	cfg.Global.ChunkSize = "64KiB"
	cfg.Global.MaxDownloadRate = "0"
	cfg.Security.AllowLoopbackForDev = true
	mgr := config.NewManager(cfg, nil)
	bus := core.NewEventBus(1024)
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
	s, err := New(Options{
		Store: st, Resolvers: resolvers, Transports: transports,
		PostProcessors: postproc.NewRegistry(), Guard: guard,
		Credentials: security.NewMemoryCredentialStore(), Config: mgr, Bus: bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		st.Close()
		srv.Close()
	})
	return &harness{s: s, st: st, srv: srv, dir: dir, data: data, events: bus}
}

func (h *harness) add(t *testing.T, opt core.TaskOptions) core.TaskID {
	t.Helper()
	if opt.Dir == "" {
		opt.Dir = h.dir
	}
	if opt.ChunkSize == 0 {
		opt.ChunkSize = 64 << 10
	}
	id, err := h.s.Create(context.Background(),
		core.Input{Raw: h.srv.URL + "/payload.bin?token=SECRET", Kind: core.InputURL}, opt)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *harness) waitState(t *testing.T, id core.TaskID, want core.TaskState, timeout time.Duration) *core.TaskDetail {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d, err := h.s.Get(context.Background(), id)
		if err == nil && d.State == want {
			return d
		}
		time.Sleep(20 * time.Millisecond)
	}
	d, _ := h.s.Get(context.Background(), id)
	t.Fatalf("task %s never reached %s (last=%+v)", id, want, d)
	return nil
}

// T-FR-TRN-01-01 / UC-01: one HTTP direct link through the whole pipeline.
func TestFullPipelineSingleFile(t *testing.T) {
	h := newHarness(t, 300<<10)
	id := h.add(t, core.TaskOptions{MaxConnections: 4})
	d := h.waitState(t, id, core.StateCompleted, 20*time.Second)
	if d.BytesTotal != int64(len(h.data)) {
		t.Fatalf("bytesTotal=%d want %d", d.BytesTotal, len(h.data))
	}
	got, err := os.ReadFile(filepath.Join(h.dir, "payload.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, h.data) {
		t.Fatal("downloaded bytes differ from fixture")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "payload.bin.part")); !os.IsNotExist(err) {
		t.Fatal(".part file was not atomically renamed")
	}
	// Sensitive query values must never be persisted.
	raw, err := h.st.Option(context.Background(), id, "temp_path")
	if err != nil || raw == "" {
		t.Fatalf("temp_path not recorded: %v", err)
	}
	task, _ := h.st.GetTask(context.Background(), id)
	if bytes.Contains([]byte(task.Input), []byte("SECRET")) {
		t.Fatal("credential leaked into tasks.input")
	}
	if d.NumPieces == 0 || d.ChunksVerified != d.NumPieces {
		t.Fatalf("chunk verification incomplete: %d/%d", d.ChunksVerified, d.NumPieces)
	}
}

// T-FR-TSK-01-01: pause / resume / cancel semantics are uniform.
func TestPauseResumeCancel(t *testing.T) {
	h := newHarness(t, 8<<20)
	id := h.add(t, core.TaskOptions{MaxConnections: 1})
	h.waitState(t, id, core.StateRunning, 10*time.Second)
	if err := h.s.Pause(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, id, core.StatePaused, 10*time.Second)
	if err := h.s.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, id, core.StateCompleted, 30*time.Second)

	other := h.add(t, core.TaskOptions{MaxConnections: 1})
	if err := h.s.Cancel(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, other, core.StateCanceled, 10*time.Second)
	if err := h.s.Resume(context.Background(), other); err == nil {
		t.Fatal("resuming a canceled task must fail with E_ILLEGAL_TRANSITION")
	}
}

// T-NFR-REL-01-01: simulate kill -9 by reopening the DB and recovering.
func TestCrashRecoveryReusesChunks(t *testing.T) {
	h := newHarness(t, 4<<20)
	id := h.add(t, core.TaskOptions{MaxConnections: 1})
	h.waitState(t, id, core.StateRunning, 10*time.Second)
	// Wait until at least one chunk is durable, then hard-stop the scheduler.
	deadline := time.Now().Add(10 * time.Second)
	for {
		chunks, _ := h.st.Chunks(context.Background(), id)
		done := 0
		for _, c := range chunks {
			if c.State == core.ChunkDone || c.State == core.ChunkVerified {
				done++
			}
		}
		if done > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.s.Close(); err != nil {
		t.Fatal(err)
	}
	// Rebuild the scheduler on the same store (daemon restart after kill -9).
	cfg := config.Defaults()
	cfg.DownloadDir = h.dir
	cfg.Global.ChunkSize = "64KiB"
	cfg.Global.MaxDownloadRate = "0"
	cfg.Security.AllowLoopbackForDev = true
	mgr := config.NewManager(cfg, nil)
	guard := security.NewDevGuard()
	resolvers := resolve.NewRegistry()
	resolvers.Register(resolve.NewDirectHTTP(guard))
	resolvers.Register(resolve.Unsupported{})
	transports := transport.NewRegistry()
	transports.Register(httptransport.New(guard))
	s2, err := New(Options{
		Store: h.st, Resolvers: resolvers, Transports: transports,
		PostProcessors: postproc.NewRegistry(), Guard: guard,
		Credentials: security.NewMemoryCredentialStore(), Config: mgr, Bus: h.events,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	n, err := s2.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("Recover returned no tasks")
	}
	d := h.waitState2(t, s2, id, core.StateCompleted, 30*time.Second)
	if d.BytesTotal != int64(len(h.data)) {
		t.Fatalf("bytesTotal=%d", d.BytesTotal)
	}
	got, err := os.ReadFile(filepath.Join(h.dir, "payload.bin"))
	if err != nil || !bytes.Equal(got, h.data) {
		t.Fatal("recovered file content mismatch")
	}
}

func (h *harness) waitState2(t *testing.T, s *Scheduler, id core.TaskID, want core.TaskState, timeout time.Duration) *core.TaskDetail {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d, err := s.Get(context.Background(), id)
		if err == nil && d.State == want {
			return d
		}
		time.Sleep(20 * time.Millisecond)
	}
	d, _ := s.Get(context.Background(), id)
	t.Fatalf("recovered task never reached %s (last=%+v)", want, d)
	return nil
}
