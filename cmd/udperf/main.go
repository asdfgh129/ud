// Command udperf measures the project's REAL HTTP Range Transport on a 1 GiB
// loopback fixture. It exists to make the §12 NFR-PERF-01 workload reproducible
// on any developer machine.
//
// IMPORTANT: this is a LOOPBACK REFERENCE measurement, not the NFR-PERF-01
// gate. NFR-PERF-01 requires a 1 GiB transfer over a real two-host gigabit LAN
// (>= 80 MB/s). Loopback has no NIC / switch / wire bottleneck, so any number
// measured here is explicitly reported as nfr_perf_01.met = false. CI does not
// run this target and the release gate stays closed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
	httptransport "github.com/asdfgh129/ud/internal/transport/http"
)

const (
	defaultSize = int64(1) << 30 // 1 GiB, the NFR-PERF-01 workload size
	pieceSize   = int64(256) << 10
	patternSize = 64 << 10
)

// patternFile is a deterministic 1 GiB io.ReadSeeker. Generating a real 1 GiB
// in-memory fixture would double the benchmark's memory footprint and measure
// allocation rather than the transport, so bytes are produced from a 64 KiB
// repeating pattern on demand.
type patternFile struct {
	size    int64
	pos     int64
	pattern []byte
}

func newPatternFile(size int64, pattern []byte) *patternFile {
	return &patternFile{size: size, pattern: pattern}
}

func (p *patternFile) Read(b []byte) (int, error) {
	if p.pos >= p.size {
		return 0, io.EOF
	}
	limit := len(b)
	if int64(limit) > p.size-p.pos {
		limit = int(p.size - p.pos)
	}
	written := 0
	for written < limit {
		off := p.pos % int64(len(p.pattern))
		n := copy(b[written:limit], p.pattern[off:])
		if n == 0 {
			break
		}
		written += n
		p.pos += int64(n)
	}
	return written, nil
}

func (p *patternFile) Seek(offset int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = p.pos
	case io.SeekEnd:
		base = p.size
	default:
		return 0, fmt.Errorf("invalid whence %d", whence)
	}
	next := base + offset
	if next < 0 {
		return 0, fmt.Errorf("negative seek position")
	}
	p.pos = next
	return next, nil
}

// discardWriter hands out the whole chunk plan sequentially and validates the
// byte count of every chunk. It measures the transport, not disk writes.
type discardWriter struct {
	mu       sync.Mutex
	size     int64
	piece    int64
	next     int64
	received int64
	chunks   int64
}

func (w *discardWriter) Next(context.Context) (core.Chunk, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.next >= w.size {
		return core.Chunk{}, false, nil
	}
	length := w.piece
	if w.next+length > w.size {
		length = w.size - w.next
	}
	c := core.Chunk{Index: int(w.next / w.piece), Start: w.next, Length: length, State: core.ChunkAssigned}
	w.next += length
	return c, true, nil
}

func (w *discardWriter) WriteChunk(_ context.Context, c core.Chunk, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if int64(len(p)) != c.Length {
		return fmt.Errorf("chunk %d: received %d bytes, want %d", c.Index, len(p), c.Length)
	}
	w.received += int64(len(p))
	w.chunks++
	return nil
}

func (w *discardWriter) Verified(context.Context, core.Chunk, core.Hash) error { return nil }
func (w *discardWriter) Release(context.Context, core.Chunk, error) error      { return nil }

type nfrVerdict struct {
	Target string `json:"target"`
	Met    bool   `json:"met"`
	Reason string `json:"reason"`
}

type perfReport struct {
	Schema         string         `json:"schema"`
	GeneratedAt    string         `json:"generated_at"`
	Scope          string         `json:"scope"`
	Workload       map[string]any `json:"workload"`
	ElapsedMS      int64          `json:"elapsed_ms"`
	MBPerS         float64        `json:"MB_per_s"`
	BytesVerified  int64          `json:"bytes_verified"`
	ChunksVerified int64          `json:"chunks_verified"`
	NFRPerf01      nfrVerdict     `json:"nfr_perf_01"`
	Reproduce      string         `json:"reproduce"`
}

func main() {
	out := flag.String("out", "dist/perf.json", "machine-readable evidence path")
	sizeFlag := flag.Int64("bytes", defaultSize, "workload size in bytes (default 1 GiB)")
	conns := flag.Int("connections", 4, "MaxConnections for the task")
	flag.Parse()
	size := *sizeFlag
	if size <= 0 || *conns <= 0 {
		fmt.Fprintln(os.Stderr, "udperf: --bytes and --connections must be positive")
		os.Exit(2)
	}

	pattern := make([]byte, patternSize)
	for i := range pattern {
		pattern[i] = byte((i*31 + 7) % 256)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "perf.bin", time.Time{}, newPatternFile(size, pattern))
	}))
	defer srv.Close()

	tr := httptransport.New(security.NewDevGuard())
	res := &core.Resource{
		ID: "perf", Kind: core.KindSingle,
		Files:   []core.ResourceFile{{Index: 0, Path: "perf.bin", Size: size, Selected: true}},
		Mirrors: []core.Mirror{{URL: srv.URL + "/perf.bin", Weight: 1}},
		Limits:  core.TaskLimits{MaxConnections: *conns},
	}
	w := &discardWriter{size: size, piece: pieceSize}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	start := time.Now()
	err := tr.Run(ctx, res, w, nil)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "udperf: transport failed after %s: %v\n", elapsed, err)
		os.Exit(1)
	}
	if w.received != size {
		fmt.Fprintf(os.Stderr, "udperf: received %d bytes, want %d\n", w.received, size)
		os.Exit(1)
	}
	mbps := float64(size) / (1024 * 1024) / elapsed.Seconds()
	rep := perfReport{
		Schema:      "ud.perf/v1",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Scope:       "1 GiB loopback reference via the real HTTP Range Transport (NOT NFR-PERF-01)",
		Workload: map[string]any{
			"bytes": size, "piece_bytes": pieceSize, "connections": *conns,
			"transport": "internal/transport/http Range path", "server": "httptest loopback, deterministic 64 KiB pattern",
		},
		ElapsedMS:      elapsed.Milliseconds(),
		MBPerS:         mbps,
		BytesVerified:  w.received,
		ChunksVerified: w.chunks,
		NFRPerf01: nfrVerdict{
			Target: "1 GiB over a real two-host gigabit LAN at >= 80 MB/s",
			Met:    false,
			Reason: "loopback-only measurement: no second host, NIC, switch or wire bottleneck; a loopback MB/s value cannot satisfy the LAN criterion. Run a two-host gigabit-LAN lab to close this NFR.",
		},
		Reproduce: "make perf (or go run ./cmd/udperf --bytes 1073741824)",
	}
	blob, _ := json.MarshalIndent(rep, "", "  ")
	_ = os.MkdirAll(filepath.Dir(*out), 0o755)
	if err := os.WriteFile(*out, blob, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "udperf: write evidence:", err)
		os.Exit(1)
	}
	fmt.Printf("udperf: %d bytes in %s = %.2f MB/s (loopback reference); NFR-PERF-01 met=false\n",
		size, elapsed.Round(time.Millisecond), mbps)
	fmt.Println(string(blob))
}
