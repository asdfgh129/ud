package ftptransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	g.checked = append(g.checked, raw)
	err := g.err
	g.mu.Unlock()
	return err
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
	size    int64
	statErr error
	retrErr error

	mu      sync.Mutex
	offsets []uint64
	paths   []string
	quits   int
}

func (s *testSession) FileSize(string) (int64, error) {
	if s.statErr != nil {
		return 0, s.statErr
	}
	if s.size != 0 {
		return s.size, nil
	}
	return int64(len(s.data)), nil
}
func (s *testSession) RetrFrom(p string, off uint64) (io.ReadCloser, error) {
	if s.retrErr != nil {
		return nil, s.retrErr
	}
	s.mu.Lock()
	s.offsets = append(s.offsets, off)
	s.paths = append(s.paths, p)
	s.mu.Unlock()
	if off > uint64(len(s.data)) {
		return nil, core.Err(core.E_INVALID_INPUT, "offset past end")
	}
	return io.NopCloser(bytes.NewReader(s.data[off:])), nil
}
func (s *testSession) Quit() error {
	s.mu.Lock()
	s.quits++
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

func newFixture(data []byte) (*Transport, *testGuard, *testSession) {
	g := &testGuard{}
	s := &testSession{data: data}
	t := &Transport{
		guard: g,
		creds: testCreds{},
		dial: func(context.Context, tputil.Target, tputil.Credential) (session, error) {
			return s, nil
		},
	}
	return t, g, s
}

func ftpResource(raw string) *core.Resource {
	return &core.Resource{
		Mirrors: []core.Mirror{{URL: raw}},
		Limits:  core.TaskLimits{MaxConnections: 2},
	}
}

// ---------------------------------------------------------------------- tests

func TestSupports(t *testing.T) {
	tr, _, _ := newFixture(nil)
	cases := map[string]bool{
		"ftp://example.com/a":     true,
		"FTPS://example.com/a":    true,
		"ftps://example.com:21/a": true,
		"sftp://example.com/a":    false,
		"http://example.com/a":    false,
		"file:///etc/passwd":      false,
	}
	for raw, want := range cases {
		if got := tr.Supports(ftpResource(raw)); got != want {
			t.Errorf("Supports(%q)=%v want %v", raw, got, want)
		}
	}
	if tr.Name() != "ftp" {
		t.Fatalf("unexpected name %q", tr.Name())
	}
}

func TestProbe(t *testing.T) {
	tr, g, s := newFixture([]byte("0123456789"))
	res := ftpResource("ftp://example.com/pub/a.bin")
	p, err := tr.Probe(context.Background(), res)
	if err != nil {
		t.Fatal(err)
	}
	if p.Size != 10 || !p.AcceptRanges || p.Filename != "a.bin" {
		t.Fatalf("unexpected probe: %+v", p)
	}
	if g.count() != 1 || g.checked[0] != "ftp://example.com/pub/a.bin" {
		t.Fatalf("SSRF guard did not check the mirror exactly once: %v", g.checked)
	}
	if s.quits != 1 {
		t.Fatalf("session must be closed after Probe, quits=%d", s.quits)
	}
}

func TestProbeRejectsBadInput(t *testing.T) {
	blocked := core.Err(core.E_RESOLVE_SSRF_BLOCKED, "blocked")
	for _, tc := range []struct {
		name string
		raw  string
		code string
	}{
		{"embedded-credentials", "ftp://u:p@example.com/a", core.E_RESOLVE_SSRF_BLOCKED},
		{"no-path", "ftp://example.com/", core.E_INVALID_INPUT},
		{"bad-scheme", "http://example.com/a", core.E_INVALID_INPUT},
		{"crlf-injection", "ftp://example.com/a\r\nDELE x", core.E_INVALID_INPUT},
		{"guard-block", "ftp://example.com/a", core.E_RESOLVE_SSRF_BLOCKED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, g, _ := newFixture([]byte("x"))
			g.err = nil
			if tc.name == "guard-block" {
				g.err = blocked
			}
			_, err := tr.Probe(context.Background(), ftpResource(tc.raw))
			if core.AsError(err).Code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
	tr, _, _ := newFixture(nil)
	if _, err := tr.Probe(context.Background(), &core.Resource{}); core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("missing mirror must fail, got %v", err)
	}
}

func TestProbeUsesCredentialRefAndPropagatesSessionError(t *testing.T) {
	tr, _, _ := newFixture(nil)
	tr.creds = testCreds{value: `{"username":"alice","password":"pw"}`}
	res := ftpResource("ftp://example.com/a")
	res.Credential = "mem://c1"
	if _, err := tr.Probe(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	tr.dial = func(context.Context, tputil.Target, tputil.Credential) (session, error) {
		return nil, &textproto.Error{Code: 550, Msg: "not found"}
	}
	if _, err := tr.Probe(context.Background(), res); core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("FTP 550 must be permanent, got %v", err)
	}
}

func TestRunDownloadsEveryChunkAtItsOffset(t *testing.T) {
	data := []byte("0123456789abcdefghij")
	tr, g, s := newFixture(data)
	r := ftpResource("ftp://example.com/pub/a.bin")
	w := newTestWriter(int64(len(data)), 7)
	b := &testBudget{}
	if err := tr.Run(context.Background(), r, w, b); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i := 0; i < 3; i++ {
		got = append(got, w.written[i]...)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("reassembled bytes = %q, want %q", got, data)
	}
	s.mu.Lock()
	offsets := append([]uint64(nil), s.offsets...)
	s.mu.Unlock()
	// Chunks run concurrently, so compare the SET of offsets.
	slices.Sort(offsets)
	if len(offsets) != 3 || offsets[0] != 0 || offsets[1] != 7 || offsets[2] != 14 {
		t.Fatalf("REST offsets = %v, want [0 7 14]", offsets)
	}
	if b.total != len(data) {
		t.Fatalf("budget charged %d, want %d", b.total, len(data))
	}
	if g.count() != 3 {
		t.Fatalf("every chunk must re-check its mirror through the Guard, got %d", g.count())
	}
}

func TestRunRejectsUnsafeInput(t *testing.T) {
	tr, _, _ := newFixture([]byte("0123456789"))
	for _, raw := range []string{
		"ftp://user:pw@example.com/a", "ftp://example.com/", "sftp://example.com/a",
	} {
		w := newTestWriter(4, 4)
		err := tr.Run(context.Background(), ftpResource(raw), w, nil)
		if err == nil {
			t.Errorf("Run(%q) should fail", raw)
		}
	}
	tr.creds = testCreds{value: "not-json"}
	res := ftpResource("ftp://example.com/a")
	res.Credential = "mem://bad"
	if _, err := tr.Probe(context.Background(), res); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("malformed credential must fail, got %v", err)
	}
}

func TestRunSessionFailureIsClassified(t *testing.T) {
	tr, _, _ := newFixture([]byte("0123456789"))
	boom := &textproto.Error{Code: 550, Msg: "gone"}
	tr.dial = func(context.Context, tputil.Target, tputil.Credential) (session, error) {
		return nil, boom
	}
	w := newTestWriter(4, 4)
	if err := tr.Run(context.Background(), ftpResource("ftp://example.com/a"), w, nil); core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("want permanent E_RESOLVE_FAILED, got %v", err)
	}
}

func TestRealDialFailsClosedWithoutDataGuard(t *testing.T) {
	// A URLGuard without the passive data-channel rule can never open an FTP
	// data connection: the transport refuses before any socket is created.
	_, err := realDial(&testGuard{})(context.Background(),
		tputil.Target{Addr: "example.com:21", Scheme: "ftp", Port: "21", Host: "example.com"},
		tputil.Credential{})
	if core.AsError(err).Code != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("want E_CAPABILITY_UNAVAILABLE, got %v", err)
	}
}

// ------------------------------------------------- control/data SSRF split

type fakeConn struct{}

func (fakeConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (fakeConn) Write(b []byte) (int, error) { return len(b), nil }
func (fakeConn) Close() error                { return nil }
func (fakeConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (fakeConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("203.0.113.10"), Port: 21}
}
func (fakeConn) SetDeadline(time.Time) error      { return nil }
func (fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (fakeConn) SetWriteDeadline(time.Time) error { return nil }

type dialGuard struct {
	*testGuard
	mu         sync.Mutex
	ctrlCalls  int
	dataCalls  []string
	dataPinned []netip.Addr
	dataErr    error
}

func (g *dialGuard) DialContext(context.Context, string, string) (net.Conn, error) {
	g.mu.Lock()
	g.ctrlCalls++
	g.mu.Unlock()
	return fakeConn{}, nil
}
func (g *dialGuard) DialDataContext(_ context.Context, addr string, control netip.Addr) (net.Conn, error) {
	g.mu.Lock()
	g.dataCalls = append(g.dataCalls, addr)
	g.dataPinned = append(g.dataPinned, control)
	err := g.dataErr
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return fakeConn{}, nil
}

func TestNewDialFuncSplitsControlAndData(t *testing.T) {
	g := &dialGuard{testGuard: &testGuard{}}
	tgt := tputil.Target{
		Addr: "example.com:21", Host: "example.com", Port: "21", Scheme: "ftp",
	}
	d := newDialFunc(context.Background(), g, g, tgt, false, false, nil)

	if _, err := d("tcp", tgt.Addr); err != nil {
		t.Fatal(err)
	}
	// The control socket must be the Guard's pinned connection, never a new
	// DNS lookup, and its peer IP must be recorded for the data-channel rule.
	if _, err := d("tcp", "203.0.113.10:40000"); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	ctrl, data, pinned := g.ctrlCalls, append([]string(nil), g.dataCalls...), append([]netip.Addr(nil), g.dataPinned...)
	g.mu.Unlock()
	if ctrl != 1 || len(data) != 1 || data[0] != "203.0.113.10:40000" {
		t.Fatalf("control=%d data=%v", ctrl, data)
	}
	if len(pinned) != 1 || pinned[0] != netip.MustParseAddr("203.0.113.10") {
		t.Fatalf("data channel was not pinned to the control IP: %v", pinned)
	}

	g.mu.Lock()
	g.dataErr = errors.New("blocked by guard")
	g.mu.Unlock()
	if _, err := d("tcp", "203.0.113.10:40001"); err == nil {
		t.Fatal("data-channel guard error must propagate")
	}
	if _, err := d("udp", tgt.Addr); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("non-TCP network must be rejected, got %v", err)
	}
}

func TestClassifyFTP(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&textproto.Error{Code: 530, Msg: "login"}, core.E_RESOLVE_FAILED},
		{&textproto.Error{Code: 550, Msg: "missing"}, core.E_RESOLVE_FAILED},
		{&textproto.Error{Code: 425, Msg: "busy"}, core.E_TRANSIENT_NETWORK},
		{errors.New("dial timeout"), core.E_TRANSIENT_NETWORK},
		{core.Err(core.E_INVALID_INPUT, "x"), core.E_INVALID_INPUT},
	}
	for _, tc := range cases {
		if got := core.AsError(classifyFTP(tc.err)).Code; got != tc.code {
			t.Errorf("classifyFTP(%v)=%s want %s", tc.err, got, tc.code)
		}
	}
	if classifyFTP(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	// Server messages can contain account names or paths; never echo them.
	msg := core.AsError(classifyFTP(&textproto.Error{Code: 550, Msg: "/home/secret"})).Message
	if strings.Contains(msg, "secret") {
		t.Fatalf("server text leaked into error: %q", msg)
	}
}
