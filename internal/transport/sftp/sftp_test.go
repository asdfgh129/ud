package sftptransport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/transport/tputil"
)

// ------------------------------------------------------------------- fixtures

type testGuard struct {
	mu      sync.Mutex
	checked []string
	err     error
}

func (g *testGuard) Check(_ context.Context, raw string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checked = append(g.checked, raw)
	return g.err
}
func (g *testGuard) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("fixture guard must not dial")
}
func (g *testGuard) MaxRedirects() int { return 3 }
func (g *testGuard) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.checked)
}

type testCreds struct{ value string }

func (c testCreds) Get(context.Context, core.CredentialRef) (string, error) {
	return c.value, nil
}
func (c testCreds) Put(context.Context, string) (core.CredentialRef, error) {
	return "mem://x", nil
}
func (c testCreds) Delete(context.Context, core.CredentialRef) error { return nil }

type testSession struct {
	data    []byte
	statErr error
	readErr error
	mu      sync.Mutex
	offsets []int64
	closes  int
}

func (s *testSession) Stat(string) (int64, error) {
	if s.statErr != nil {
		return 0, s.statErr
	}
	return int64(len(s.data)), nil
}
func (s *testSession) ReadAt(_ string, dst []byte, off int64) (int, error) {
	if s.readErr != nil {
		return 0, s.readErr
	}
	s.mu.Lock()
	s.offsets = append(s.offsets, off)
	s.mu.Unlock()
	if off >= int64(len(s.data)) {
		return 0, io.EOF
	}
	n := copy(dst, s.data[off:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}
func (s *testSession) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

type testWriter struct {
	mu      sync.Mutex
	chunks  []core.Chunk
	written map[int][]byte
}

func newTestWriter(size, chunkSize int64) *testWriter {
	var chunks []core.Chunk
	for start, idx := int64(0), 0; start < size; start, idx = start+chunkSize, idx+1 {
		chunks = append(chunks, core.Chunk{
			Index: idx, Start: start, Length: min(chunkSize, size-start), State: core.ChunkPending,
		})
	}
	return &testWriter{chunks: chunks, written: make(map[int][]byte)}
}
func (w *testWriter) Next(context.Context) (core.Chunk, bool, error) {
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
func (w *testWriter) WriteChunk(_ context.Context, c core.Chunk, p []byte) error {
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
func (w *testWriter) Verified(context.Context, core.Chunk, core.Hash) error { return nil }
func (w *testWriter) Release(context.Context, core.Chunk, error) error      { return nil }

type testBudget struct {
	mu    sync.Mutex
	total int
}

func (b *testBudget) Consume(_ context.Context, n int) error {
	b.mu.Lock()
	b.total += n
	b.mu.Unlock()
	return nil
}

func newFixture(data []byte) (*Transport, *testGuard, *testSession, ssh.PublicKey) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		panic(err)
	}
	g := &testGuard{}
	s := &testSession{data: data}
	cred := fmt.Sprintf(`{"username":"alice","password":"pw","host_key":%q}`,
		strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))))
	tr := &Transport{
		guard: g,
		creds: testCreds{value: cred},
		dial: func(context.Context, tputil.Target, tputil.Credential) (session, error) {
			return s, nil
		},
	}
	return tr, g, s, pub
}

func sftpResource(raw string) *core.Resource {
	return &core.Resource{
		Credential: "mem://c1",
		Mirrors:    []core.Mirror{{URL: raw}},
		Limits:     core.TaskLimits{MaxConnections: 2},
	}
}

// ---------------------------------------------------------------------- tests

func TestSupports(t *testing.T) {
	tr, _, _, _ := newFixture(nil)
	cases := map[string]bool{
		"sftp://example.com/a": true,
		"SFTP://example.com/a": true,
		"ftp://example.com/a":  false,
		"http://example.com/a": false,
	}
	for raw, want := range cases {
		if got := tr.Supports(sftpResource(raw)); got != want {
			t.Errorf("Supports(%q)=%v want %v", raw, got, want)
		}
	}
	if tr.Name() != "sftp" {
		t.Fatalf("unexpected name %q", tr.Name())
	}
}

func TestHostKeyCallbackFailsClosed(t *testing.T) {
	_, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub1, _ := ssh.NewPublicKey(priv1.Public())
	_, priv2, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _ := ssh.NewPublicKey(priv2.Public())
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub1)))
	addr := &net.TCPAddr{IP: net.ParseIP("203.0.113.1"), Port: 22}

	cb, err := hostKeyCallback(tputil.Credential{HostKey: line})
	if err != nil {
		t.Fatal(err)
	}
	if err := cb("example.com", addr, pub1); err != nil {
		t.Fatalf("pinned key must be accepted: %v", err)
	}
	if err := cb("example.com", addr, pub2); err == nil {
		t.Fatal("a different host key must be rejected")
	}

	fp := ssh.FingerprintSHA256(pub1)
	cb, err = hostKeyCallback(tputil.Credential{HostKeyFingerprint: fp})
	if err != nil {
		t.Fatal(err)
	}
	if err := cb("example.com", addr, pub1); err != nil {
		t.Fatalf("pinned fingerprint must be accepted: %v", err)
	}
	if err := cb("example.com", addr, pub2); err == nil {
		t.Fatal("mismatched fingerprint must be rejected")
	}

	if _, err := hostKeyCallback(tputil.Credential{}); core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("missing pin must fail closed, got %v", err)
	}
	if _, err := hostKeyCallback(tputil.Credential{HostKey: "garbage"}); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("invalid authorized_keys line must be E_INVALID_INPUT, got %v", err)
	}
}

func TestAuthMethods(t *testing.T) {
	if _, err := authMethods(tputil.Credential{Password: "pw"}); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatal("username is mandatory")
	}
	if _, err := authMethods(tputil.Credential{Username: "alice"}); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatal("an auth method is mandatory")
	}
	m, err := authMethods(tputil.Credential{Username: "alice", Password: "pw"})
	if err != nil || len(m) != 1 {
		t.Fatalf("password auth: %v %v", m, err)
	}
	// A real private key must parse into a public-key auth method.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	m, err = authMethods(tputil.Credential{Username: "alice", PrivateKey: string(keyPEM), Password: "pw"})
	if err != nil || len(m) != 2 {
		t.Fatalf("key+password auth: %v %v", m, err)
	}
	if _, err := authMethods(tputil.Credential{Username: "alice", PrivateKey: "not a key"}); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("bad key must be E_INVALID_INPUT, got %v", err)
	}
}

func TestProbe(t *testing.T) {
	data := []byte("0123456789")
	tr, g, s, _ := newFixture(data)
	p, err := tr.Probe(context.Background(), sftpResource("sftp://example.com/pub/a.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Size != int64(len(data)) || !p.AcceptRanges || p.Filename != "a.bin" {
		t.Fatalf("unexpected probe: %+v", p)
	}
	if g.count() != 1 || g.checked[0] != "sftp://example.com/pub/a.bin" {
		t.Fatalf("SSRF guard did not check the mirror: %v", g.checked)
	}
	if s.closes != 1 {
		t.Fatalf("session must close after Probe, closes=%d", s.closes)
	}
}

func TestProbeRequiresCredentialAndPath(t *testing.T) {
	tr, _, _, _ := newFixture([]byte("x"))
	r := sftpResource("sftp://example.com/a")
	r.Credential = ""
	if _, err := tr.Probe(context.Background(), r); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("SFTP must require a credentialRef, got %v", err)
	}
	for _, raw := range []string{
		"sftp://u:p@example.com/a", "sftp://example.com/", "ftp://example.com/a",
		"sftp://example.com/a\r\nrm -rf",
	} {
		if _, err := tr.Probe(context.Background(), sftpResource(raw)); err == nil {
			t.Errorf("Probe(%q) should fail", raw)
		}
	}
}

func TestRunDownloadsEveryChunkAtItsOffset(t *testing.T) {
	data := []byte("0123456789abcdefghij")
	tr, g, s, _ := newFixture(data)
	w := newTestWriter(int64(len(data)), 6)
	b := &testBudget{}
	if err := tr.Run(context.Background(), sftpResource("sftp://example.com/pub/a.bin"), w, b); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i := 0; i < 4; i++ {
		got = append(got, w.written[i]...)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("reassembled bytes = %q, want %q", got, data)
	}
	s.mu.Lock()
	offsets := append([]int64(nil), s.offsets...)
	s.mu.Unlock()
	// Chunks run concurrently, so compare the SET of offsets.
	slices.Sort(offsets)
	if len(offsets) != 4 || offsets[0] != 0 || offsets[1] != 6 || offsets[2] != 12 || offsets[3] != 18 {
		t.Fatalf("ReadAt offsets = %v, want [0 6 12 18]", offsets)
	}
	if b.total != len(data) {
		t.Fatalf("budget charged %d, want %d", b.total, len(data))
	}
	if g.count() != 4 {
		t.Fatalf("each chunk must re-check its mirror, got %d", g.count())
	}
}

func TestRunTruncatedAndSessionErrors(t *testing.T) {
	tr, _, _, _ := newFixture([]byte("short"))
	w := newTestWriter(20, 8)
	if err := tr.Run(context.Background(), sftpResource("sftp://example.com/a"), w, nil); core.AsError(err).Code != core.E_TRANSIENT_NETWORK {
		t.Fatalf("truncated body must be transient, got %v", err)
	}
	tr, _, _, _ = newFixture(nil)
	tr.dial = func(context.Context, tputil.Target, tputil.Credential) (session, error) {
		return nil, os.ErrNotExist
	}
	if _, err := tr.Probe(context.Background(), sftpResource("sftp://example.com/a")); core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("missing path must be permanent, got %v", err)
	}
}

func TestClassifySFTP(t *testing.T) {
	if core.AsError(classifySFTP(os.ErrNotExist)).Code != core.E_RESOLVE_FAILED {
		t.Fatal("missing path is permanent")
	}
	if core.AsError(classifySFTP(errors.New("read tcp: reset"))).Code != core.E_TRANSIENT_NETWORK {
		t.Fatal("socket error is transient")
	}
	if classifySFTP(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
