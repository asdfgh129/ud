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

// benchWriter discards the payload. The benchmark measures the HTTP Range
// transport + net/http loopback stack, not disk or SQLite throughput.
type benchWriter struct {
	mu     sync.Mutex
	chunks []core.Chunk
	bytes  int64
}

func newBenchWriter(size, piece int64) *benchWriter {
	var chunks []core.Chunk
	for start, idx := int64(0), 0; start < size; start, idx = start+piece, idx+1 {
		length := piece
		if start+length > size {
			length = size - start
		}
		chunks = append(chunks, core.Chunk{
			Index: idx, Start: start, Length: length, State: core.ChunkPending,
		})
	}
	return &benchWriter{chunks: chunks}
}

func (w *benchWriter) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bytes = 0
	for i := range w.chunks {
		w.chunks[i].State = core.ChunkPending
		w.chunks[i].Attempt = 0
	}
}

func (w *benchWriter) Next(context.Context) (core.Chunk, bool, error) {
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

func (w *benchWriter) WriteChunk(_ context.Context, c core.Chunk, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bytes += int64(len(p))
	for i := range w.chunks {
		if w.chunks[i].Index == c.Index {
			w.chunks[i].State = core.ChunkDone
		}
	}
	return nil
}

func (w *benchWriter) total() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytes
}

func (w *benchWriter) Verified(context.Context, core.Chunk, core.Hash) error { return nil }

func (w *benchWriter) Release(_ context.Context, c core.Chunk, _ error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.chunks {
		if w.chunks[i].Index == c.Index {
			w.chunks[i].State = core.ChunkPending
		}
	}
	return nil
}

// BenchmarkRangeThroughput is a P0-scope LOOPBACK microbenchmark. It is NOT
// the NFR-PERF-01 gate (docs/12 §5 requires 1GiB over a gigabit LAN). Its
// purpose is to establish a reproducible regression baseline for the HTTP
// Range path before P1 work starts; results are collected by tools/gates.py.
func BenchmarkRangeThroughput(b *testing.B) {
	const (
		size  = 32 << 20
		piece = 256 << 10
	)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*31 + 7) % 256)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "bench.bin", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()

	tr := New(security.NewDevGuard())
	r := &core.Resource{
		ID: "bench", Kind: core.KindSingle,
		Files:   []core.ResourceFile{{Index: 0, Path: "bench.bin", Size: size, Selected: true}},
		Mirrors: []core.Mirror{{URL: srv.URL + "/bench.bin", Weight: 1}},
		Limits:  core.TaskLimits{MaxConnections: 4},
	}
	w := newBenchWriter(size, piece)

	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.reset()
		if err := tr.Run(context.Background(), r, w, nil); err != nil {
			b.Fatal(err)
		}
		if got := w.total(); got != size {
			b.Fatalf("transferred %d bytes, want %d", got, size)
		}
	}
}
