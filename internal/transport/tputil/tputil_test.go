package tputil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

// ---------------------------------------------------------------- credentials

type memCreds struct {
	value string
	err   error
}

func (m memCreds) Get(context.Context, core.CredentialRef) (string, error) {
	return m.value, m.err
}
func (m memCreds) Put(context.Context, string) (core.CredentialRef, error) { return "mem://x", nil }
func (m memCreds) Delete(context.Context, core.CredentialRef) error        { return nil }

func TestParseCredential(t *testing.T) {
	c, err := ParseCredential(`{"username":"alice","password":"s3cret","host_key_fingerprint":"SHA256:abc"}`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Username != "alice" || c.Password != "s3cret" || c.HostKeyFingerprint != "SHA256:abc" {
		t.Fatalf("unexpected credential: %+v", c)
	}
	for _, bad := range []string{
		"", "not-json", `["array"]`, `{"username":"a\r\n"}`,
		`{"password":"has\x00nul"}`, strings.Repeat("x", MaxCredentialBytes+1),
	} {
		if _, err := ParseCredential(bad); core.AsError(err).Code != core.E_INVALID_INPUT {
			t.Errorf("ParseCredential(%q) should be E_INVALID_INPUT, got %v", bad[:min(len(bad), 24)], err)
		}
	}
}

func TestLoadCredentialAnonymousAndStrict(t *testing.T) {
	ctx := context.Background()
	c, err := LoadCredential(ctx, nil, "", true)
	if err != nil || c.Username != "anonymous" || c.Password == "" {
		t.Fatalf("anonymous FTP credential: %+v %v", c, err)
	}
	if _, err := LoadCredential(ctx, nil, "", false); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("SFTP must reject a missing credentialRef, got %v", err)
	}
	if _, err := LoadCredential(ctx, nil, "mem://c1", false); core.AsError(err).Code != core.E_INTERNAL {
		t.Fatalf("missing store must be E_INTERNAL, got %v", err)
	}
	if _, err := LoadCredential(ctx, memCreds{err: errors.New("boom")}, "mem://c1", false); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("store failure must be E_INVALID_INPUT, got %v", err)
	}
	if _, err := LoadCredential(ctx, memCreds{err: errors.New("secret leaked")}, "mem://c1", false); err != nil && strings.Contains(err.Error(), "secret leaked") {
		t.Fatal("credential error must not echo store diagnostics")
	}
	c, err = LoadCredential(ctx, memCreds{value: `{"username":"bob"}`}, "mem://c1", false)
	if err != nil || c.Username != "bob" {
		t.Fatalf("store-backed credential: %+v %v", c, err)
	}
}

// --------------------------------------------------------------------- targets

var fileSchemes = map[string]string{"ftp": "21", "ftps": "990", "sftp": "22"}

func TestParseTarget(t *testing.T) {
	cases := []struct{ raw, addr, p, scheme string }{
		{"ftp://example.com/pub/a.iso", "example.com:21", "/pub/a.iso", "ftp"},
		{"FTP://example.com:2121/a", "example.com:2121", "/a", "ftp"},
		{"ftps://example.com/a", "example.com:990", "/a", "ftps"},
		{"sftp://example.com:2222/dir/a%20b.bin", "example.com:2222", "/dir/a b.bin", "sftp"},
		{"ftp://example.com/dir/./a.bin", "example.com:21", "/dir/a.bin", "ftp"},
	}
	for _, tc := range cases {
		got, err := ParseTarget(tc.raw, fileSchemes)
		if err != nil {
			t.Errorf("%s: %v", tc.raw, err)
			continue
		}
		if got.Addr != tc.addr || got.Path != tc.p || got.Scheme != tc.scheme {
			t.Errorf("%s: got %+v, want addr=%s path=%s scheme=%s", tc.raw, got, tc.addr, tc.p, tc.scheme)
		}
	}
}

func TestParseTargetRejectsHazards(t *testing.T) {
	bad := []string{
		"", "http://example.com/a", "ftp://example.com", "ftp://example.com/",
		"ftp://user:pass@example.com/a", "ftp://example.com/a?x=1", "ftp://example.com/a#frag",
		"ftp://example.com:0/a", "ftp://example.com:99999/a", "ftp://example.com/a\r\nDELE x",
		"ftp://example.com/../../etc/passwd", "ftp://example.com/dir/../a.bin",
		"ftp://example.com/%00x", "ftp://example.com/%0D%0ADELE%20x",
		strings.Repeat("ftp://example.com/", 500),
	}
	for _, raw := range bad {
		if _, err := ParseTarget(raw, fileSchemes); err == nil {
			t.Errorf("ParseTarget(%q) should fail", raw[:min(len(raw), 40)])
		}
	}
	if _, err := ParseTarget("ftp://user:p@example.com/a", fileSchemes); core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("embedded credentials must be a security error, got %v", err)
	}
}

// ------------------------------------------------------------------ chunk loop

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
		length := min(chunkSize, size-start)
		chunks = append(chunks, core.Chunk{
			Index: idx, Start: start, Length: length, State: core.ChunkPending,
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

func resourceForTest(mirrors ...string) *core.Resource {
	ms := make([]core.Mirror, 0, len(mirrors))
	for _, m := range mirrors {
		ms = append(ms, core.Mirror{URL: m})
	}
	return &core.Resource{Mirrors: ms, Limits: core.TaskLimits{MaxConnections: 3}}
}

func TestRunChunksWritesEveryChunk(t *testing.T) {
	r := resourceForTest("ftp://a/x", "ftp://b/x")
	w := newMemWriter(10, 4, 2)
	var mu sync.Mutex
	seen := map[int]int{}
	err := RunChunks(context.Background(), r, w, func(_ context.Context, c core.Chunk, m core.Mirror) error {
		mu.Lock()
		seen[c.Index]++
		mu.Unlock()
		return w.WriteChunk(context.Background(), c, bytes.Repeat([]byte{byte('a' + c.Index)}, int(c.Length)))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.written) != 3 {
		t.Fatalf("want 3 chunks, got %d", len(w.written))
	}
	for i, b := range w.written {
		if !bytes.Equal(b, bytes.Repeat([]byte{byte('a' + i)}, len(b))) {
			t.Errorf("chunk %d corrupted: %q", i, b)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("fetch not called for every chunk: %v", seen)
	}
}

func TestRunChunksRetriesAndRotatesMirrors(t *testing.T) {
	r := resourceForTest("ftp://a/x", "ftp://b/x")
	w := newMemWriter(4, 4, 3)
	var mu sync.Mutex
	var used []string
	err := RunChunks(context.Background(), r, w, func(_ context.Context, c core.Chunk, m core.Mirror) error {
		mu.Lock()
		used = append(used, m.URL)
		mu.Unlock()
		if c.Attempt == 1 {
			return core.Err(core.E_TRANSIENT_NETWORK, "transient")
		}
		return w.WriteChunk(context.Background(), c, []byte("done"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.released != 1 || string(w.written[0]) != "done" {
		t.Fatalf("retry did not recover the chunk: released=%d written=%q", w.released, w.written[0])
	}
	if len(used) != 2 || used[0] != "ftp://a/x" || used[1] != "ftp://b/x" {
		t.Fatalf("mirror rotation on retry: %v", used)
	}
}

func TestRunChunksPropagatesPermanentError(t *testing.T) {
	r := resourceForTest("ftp://a/x")
	w := newMemWriter(4, 4, 3)
	err := RunChunks(context.Background(), r, w, func(context.Context, core.Chunk, core.Mirror) error {
		return core.Err(core.E_RESOLVE_FAILED, "no such file")
	})
	if core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("want E_RESOLVE_FAILED, got %v", err)
	}
}

func TestRunChunksGuardsAndCancellation(t *testing.T) {
	if err := RunChunks(context.Background(), &core.Resource{}, newMemWriter(0, 4, 1), nil); core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("no-mirror must fail, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := newMemWriter(8, 4, 1)
	err := RunChunks(ctx, resourceForTest("ftp://a/x"), w, func(context.Context, core.Chunk, core.Mirror) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context must propagate, got %v", err)
	}
}

// ------------------------------------------------------------- budget / errors

type countingBudget struct {
	mu    sync.Mutex
	total int
	err   error
}

func (b *countingBudget) Consume(_ context.Context, n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += n
	return b.err
}

func TestReadWithBudget(t *testing.T) {
	b := &countingBudget{}
	dst := make([]byte, 40<<10)
	if err := ReadWithBudget(context.Background(), bytes.NewReader(dst), dst, b); err != nil {
		t.Fatal(err)
	}
	if b.total != len(dst) {
		t.Fatalf("budget charged %d bytes, want %d", b.total, len(dst))
	}
	short := make([]byte, 10)
	if err := ReadWithBudget(context.Background(), bytes.NewReader([]byte("abc")), short, nil); core.AsError(err).Code != core.E_TRANSIENT_NETWORK {
		t.Fatalf("truncated body must be transient, got %v", err)
	}
	boom := errors.New("quota")
	if err := ReadWithBudget(context.Background(), bytes.NewReader(dst), dst,
		&countingBudget{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("budget error must propagate, got %v", err)
	}
	if err := ReadWithBudget(context.Background(), errReader{}, make([]byte, 4), nil); core.AsError(err).Code != core.E_TRANSIENT_NETWORK {
		t.Fatalf("reader error must be classified, got %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestClassifyNetworkAndBackoff(t *testing.T) {
	ce := core.Err(core.E_RESOLVE_FAILED, "permanent")
	if ClassifyNetwork(ce) != ce {
		t.Fatal("core errors must pass through unchanged")
	}
	if !errors.Is(ClassifyNetwork(context.Canceled), context.Canceled) {
		t.Fatal("context cancellation must pass through")
	}
	if core.AsError(ClassifyNetwork(errors.New("dial failed"))).Code != core.E_TRANSIENT_NETWORK {
		t.Fatal("plain network errors must be transient")
	}
	if ClassifyNetwork(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	if err := SleepBackoff(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(SleepBackoff(ctx, 3), context.Canceled) {
		t.Fatal("backoff must respect cancellation")
	}
}
