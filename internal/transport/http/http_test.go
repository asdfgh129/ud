package httptransport

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

type memWriter struct {
	mu       sync.Mutex
	chunks   []core.Chunk
	written  map[int][]byte
	maxTries int
	released int
}

func newMemWriter(size, chunkSize int64, maxTries int) *memWriter {
	var chunks []core.Chunk
	for start, idx := int64(0), 0; start < size; start, idx = start+chunkSize, idx+1 {
		length := chunkSize
		if start+length > size {
			length = size - start
		}
		chunks = append(chunks, core.Chunk{
			Index: idx, Start: start, Length: length, State: core.ChunkPending, Attempt: 0,
		})
	}
	return &memWriter{chunks: chunks, written: make(map[int][]byte), maxTries: maxTries}
}

func (w *memWriter) Next(context.Context) (core.Chunk, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.chunks {
		if w.chunks[i].State == core.ChunkPending {
			w.chunks[i].State = core.ChunkAssigned
			w.chunks[i].Attempt++
			return w.chunks[i], true, nil
		}
	}
	return core.Chunk{}, false, nil
}

func (w *memWriter) WriteChunk(_ context.Context, c core.Chunk, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written[c.Index] = append([]byte(nil), p...)
	for i := range w.chunks {
		if w.chunks[i].Index == c.Index {
			w.chunks[i].State = core.ChunkDone
		}
	}
	return nil
}

func (w *memWriter) Verified(context.Context, core.Chunk, core.Hash) error { return nil }

func (w *memWriter) Release(_ context.Context, c core.Chunk, err error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.released++
	state := core.ChunkFailed
	if core.IsRetryable(err) && c.Attempt < w.maxTries {
		state = core.ChunkPending
	}
	for i := range w.chunks {
		if w.chunks[i].Index == c.Index {
			w.chunks[i].State = state
		}
	}
	return nil
}

func (w *memWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []byte
	for i := 0; i < len(w.chunks); i++ {
		out = append(out, w.written[i]...)
	}
	return out
}

func fixture(t *testing.T, size int) (*httptest.Server, []byte, *Transport) {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*31 + 7) % 256)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f.bin", time.Time{}, bytes.NewReader(data))
	}))
	return srv, data, New(security.NewDevGuard())
}

// T-FR-TRN-01-01: Range segmentation, multi-connection, exact byte recovery.
func TestRangeDownload(t *testing.T) {
	srv, data, tr := fixture(t, 300<<10)
	defer srv.Close()
	r := &core.Resource{ID: "r", Kind: core.KindSingle,
		Files:   []core.ResourceFile{{Index: 0, Path: "f.bin", Size: int64(len(data)), Selected: true}},
		Mirrors: []core.Mirror{{URL: srv.URL + "/f.bin", Weight: 1}},
		Limits:  core.TaskLimits{MaxConnections: 4}}
	w := newMemWriter(int64(len(data)), 64<<10, 3)
	if err := tr.Run(context.Background(), r, w, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.bytes(), data) {
		t.Fatal("range download produced wrong bytes")
	}
}

// UC-01 fallback: servers without Range support degrade to one stream.
func TestNoRangeFallback(t *testing.T) {
	data := make([]byte, 20<<10)
	for i := range data {
		data[i] = byte(i)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20480")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	tr := New(security.NewDevGuard())
	r := &core.Resource{ID: "r", Kind: core.KindSingle,
		Files:   []core.ResourceFile{{Index: 0, Path: "f.bin", Size: int64(len(data)), Selected: true}},
		Mirrors: []core.Mirror{{URL: srv.URL + "/f.bin", Weight: 1}},
		Limits:  core.TaskLimits{MaxConnections: 8}}
	w := newMemWriter(int64(len(data)), 8<<10, 3)
	if err := tr.Run(context.Background(), r, w, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.bytes(), data) {
		t.Fatal("no-range fallback produced wrong bytes")
	}
}

func TestTransientRetryThenSuccess(t *testing.T) {
	data := []byte("0123456789ABCDEF")
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "16")
			w.Header().Set("Accept-Ranges", "bytes")
			return
		}
		if fail {
			fail = false
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.ServeContent(w, r, "f.bin", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	tr := New(security.NewDevGuard())
	r := &core.Resource{ID: "r", Kind: core.KindSingle,
		Files:   []core.ResourceFile{{Index: 0, Path: "f.bin", Size: 16, Selected: true}},
		Mirrors: []core.Mirror{{URL: srv.URL + "/f.bin", Weight: 1}},
		Limits:  core.TaskLimits{MaxConnections: 1}}
	w := newMemWriter(16, 16, 3)
	if err := tr.Run(context.Background(), r, w, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.bytes(), data) {
		t.Fatalf("retry produced wrong bytes: %q", w.bytes())
	}
	if w.released != 1 {
		t.Fatalf("expected exactly one released chunk, got %d", w.released)
	}
}

func TestProbeBlockedBySSRF(t *testing.T) {
	tr := New(security.NewDevGuard())
	r := &core.Resource{ID: "r", Kind: core.KindSingle,
		Files:   []core.ResourceFile{{Index: 0, Path: "f.bin"}},
		Mirrors: []core.Mirror{{URL: "http://169.254.169.254/latest/meta-data/"}}}
	if _, err := tr.Probe(context.Background(), r); err == nil ||
		core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("expected SSRF block, got %v", err)
	}
}

func TestClassifyStatus(t *testing.T) {
	cases := map[int]bool{429: true, 500: true, 502: true, 408: true, 404: false, 403: false, 416: false}
	for code, retryable := range cases {
		e := core.AsError(classifyStatus(code))
		if e.Retryable != retryable {
			t.Errorf("HTTP %d retryable=%v want %v", code, e.Retryable, retryable)
		}
	}
}
