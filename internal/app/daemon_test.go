package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

// allowGuard lets the registration-chain test exercise pure parsing without
// any DNS lookup; the real SSRFGuard is covered in internal/security.
type allowGuard struct{}

func (allowGuard) Check(context.Context, string) error { return nil }
func (allowGuard) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("resolver registration test must not open a connection")
}
func (allowGuard) MaxRedirects() int { return 3 }

// daemonHandle owns one background Run call. stop is idempotent and returns
// whatever Run returned, so tests can assert a clean shutdown.
type daemonHandle struct {
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

func startDaemon(t *testing.T, opts Options) *daemonHandle {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := &daemonHandle{cancel: cancel, done: make(chan error, 1)}
	go func() { h.done <- Run(ctx, opts) }()
	t.Cleanup(func() { _ = h.stop(t) })
	return h
}

func (h *daemonHandle) stop(t *testing.T) error {
	t.Helper()
	h.once.Do(func() {
		h.cancel()
		select {
		case h.err = <-h.done:
		case <-time.After(15 * time.Second):
			t.Error("daemon did not stop within the shutdown budget")
			h.err = context.DeadlineExceeded
		}
	})
	return h.err
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForDiscovery(t *testing.T, path string) Discovery {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			var d Discovery
			if err := json.Unmarshal(b, &d); err == nil && d.Addr != "" {
				return d
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("discovery file %s never appeared", path)
	return Discovery{}
}

func TestRunServesHealthAndShutsDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := t.TempDir()
	t.Chdir(work)

	discovery := filepath.Join(work, "daemon.json")
	dl := filepath.Join(work, "dl")
	cfgPath := writeConfig(t, fmt.Sprintf(
		"listen: \"127.0.0.1:0\"\ndownloadDir: %q\nlogLevel: \"error\"\n", dl))

	h := startDaemon(t, Options{ConfigPath: cfgPath, DiscoveryOverride: discovery})
	d := waitForDiscovery(t, discovery)
	if d.PID != os.Getpid() || d.Version == "" || d.StartedAt == 0 {
		t.Fatalf("incomplete discovery record: %+v", d)
	}
	resp, err := http.Get("http://" + d.Addr + "/api/v1/healthz")
	if err != nil {
		t.Fatalf("daemon not reachable at %s: %v", d.Addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz=%d", resp.StatusCode)
	}
	if _, err := os.Stat(dl); err != nil {
		t.Fatalf("download directory not created: %v", err)
	}

	if err := h.stop(t); err != nil {
		t.Fatalf("graceful shutdown returned %v", err)
	}
	// Run removes the discovery file on the way out so a stale address is
	// never advertised after the process is gone.
	if _, err := os.Stat(discovery); !os.IsNotExist(err) {
		t.Fatalf("discovery file survived shutdown: %v", err)
	}
}

func TestRunUsesDefaultsAndOptionOverrides(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work := t.TempDir()
	t.Chdir(work) // no config.yaml here -> built-in defaults
	discovery := filepath.Join(work, "daemon.json")

	dir := filepath.Join(work, "custom-dl")
	dbPath := filepath.Join(work, "state.db")
	h := startDaemon(t, Options{
		Listen: "127.0.0.1:0", Dir: dir, LogLevel: "error",
		DBPath: dbPath, DiscoveryOverride: discovery,
	})
	d := waitForDiscovery(t, discovery)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("override download dir not created: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("override db path not created: %v", err)
	}
	resp, err := http.Get("http://" + d.Addr + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("capabilities=%d", resp.StatusCode)
	}
	if err := h.stop(t); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestRunPropagatesConfigErrors(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	if err := Run(context.Background(), Options{ConfigPath: filepath.Join(work, "missing.yaml")}); err == nil {
		t.Fatal("missing config file must fail")
	}
	// A config that fails Validate must be rejected before any socket opens.
	bad := writeConfig(t, "global:\n  maxConcurrentTasks: 0\n")
	if err := Run(context.Background(), Options{ConfigPath: bad,
		DiscoveryOverride: filepath.Join(work, "d.json")}); err == nil {
		t.Fatal("invalid config must fail")
	}
}

func TestRunRejectsNonLoopbackWithoutToken(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	cfg := writeConfig(t, "listen: \"0.0.0.0:0\"\n")
	if err := Run(context.Background(), Options{ConfigPath: cfg,
		DiscoveryOverride: filepath.Join(work, "d.json")}); err == nil {
		t.Fatal("non-loopback listen without remoteToken must fail")
	}
}

func TestRunReportsListenFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work := t.TempDir()
	t.Chdir(work)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := writeConfig(t, fmt.Sprintf("listen: %q\ndownloadDir: %q\n",
		ln.Addr().String(), filepath.Join(work, "dl")))
	err = Run(context.Background(), Options{ConfigPath: cfg,
		DBPath: filepath.Join(work, "state.db"), DiscoveryOverride: filepath.Join(work, "d.json")})
	if err == nil {
		t.Fatal("binding an occupied port must fail")
	}
}

func TestDiscoveryRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := DiscoveryPath()
	if filepath.Base(path) != "daemon.json" {
		t.Fatalf("unexpected discovery path %q", path)
	}
	if _, err := ReadDiscovery(); err == nil {
		t.Fatal("missing discovery file must error")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"addr":"127.0.0.1:1234","pid":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := ReadDiscovery()
	if err != nil {
		t.Fatal(err)
	}
	if d.Addr != "127.0.0.1:1234" || d.PID != 7 {
		t.Fatalf("unexpected discovery: %+v", d)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiscovery(); err == nil {
		t.Fatal("corrupt discovery file must error")
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for _, level := range []string{"debug", "warn", "error", "info", "", "unknown"} {
		if l := newLogger(level); l == nil {
			t.Fatalf("newLogger(%q) returned nil", level)
		}
	}
}

// WBS 8 regression: the two security-sensitive P1 built-ins must be
// registered ahead of DirectHTTP/Unsupported, and each input class must
// actually reach its own Resolver through the daemon's chain.
func TestBuiltinResolverChainOrderAndDispatch(t *testing.T) {
	r := newBuiltinResolvers(allowGuard{})
	want := []string{"metalink", "link-wrapper", "direct-http", "direct-file", "unsupported"}
	if got := r.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolver order = %v, want %v", got, want)
	}
	ctx := core.ResolveContext{Ctx: context.Background(), Guard: allowGuard{}}

	thunder := "thunder://" + base64.StdEncoding.EncodeToString(
		[]byte("AAhttps://mirror.example.com/a.isoZZ"))
	res, err := r.Resolve(ctx, core.Input{Raw: thunder, Kind: core.InputThunder})
	if err != nil || len(res.Mirrors) != 1 || res.Mirrors[0].URL != "https://mirror.example.com/a.iso" {
		t.Fatalf("thunder dispatch: %v %+v", err, res)
	}

	manifest := `<?xml version="1.0"?><metalink xmlns="urn:ietf:params:xml:ns:metalink">` +
		`<file name="a.iso"><size>4</size><url priority="1">https://mirror.example.com/a.iso</url>` +
		`</file></metalink>`
	res, err = r.Resolve(ctx, core.Input{Raw: manifest, Kind: core.InputMetalink})
	if err != nil || len(res.Mirrors) != 1 || res.Files[0].Path != "a.iso" {
		t.Fatalf("metalink dispatch: %v %+v", err, res)
	}

	res, err = r.Resolve(ctx, core.Input{Raw: "https://mirror.example.com/a.iso", Kind: core.InputURL})
	if err != nil || res.Mirrors[0].URL != "https://mirror.example.com/a.iso" {
		t.Fatalf("direct dispatch: %v %+v", err, res)
	}
	_, err = r.Resolve(ctx, core.Input{Raw: "magnet:?xt=urn:btih:abcd", Kind: core.InputMagnet})
	if core.AsError(err).Code != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("unsupported must stay last and classify P1 inputs, got %v", err)
	}
}

// WBS 9 regression: the byte movers are registered in a stable order and the
// FTP/SFTP transports receive the same credential store as the scheduler.
func TestBuiltinTransportChainOrder(t *testing.T) {
	creds := &noopCreds{}
	r := newBuiltinTransports(allowGuard{}, creds)
	want := []string{"http", "ftp", "sftp"}
	if got := r.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("transport order = %v, want %v", got, want)
	}
	for _, tc := range []struct {
		raw  string
		name string
	}{
		{"https://example.com/a.iso", "http"},
		{"ftp://example.com/a.iso", "ftp"},
		{"ftps://example.com/a.iso", "ftp"},
		{"sftp://example.com/a.iso", "sftp"},
	} {
		tr, err := r.Select(&core.Resource{Mirrors: []core.Mirror{{URL: tc.raw}}})
		if err != nil || tr.Name() != tc.name {
			t.Errorf("Select(%q) = %v, %v; want %q", tc.raw, tr, err, tc.name)
		}
	}
	if _, err := r.Select(&core.Resource{Mirrors: []core.Mirror{{URL: "file:///etc/passwd"}}}); core.AsError(err).Code != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("unsupported scheme must fail cleanly, got %v", err)
	}
}

type noopCreds struct{}

func (noopCreds) Get(context.Context, core.CredentialRef) (string, error) {
	return "", errors.New("no credentials in this test")
}
func (noopCreds) Put(context.Context, string) (core.CredentialRef, error) {
	return "mem://x", nil
}
func (noopCreds) Delete(context.Context, core.CredentialRef) error { return nil }

func TestIsLoopbackListen(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:6800": true,
		"localhost:6800": true,
		"[::1]:6800":     true,
		":6800":          true,
		"0.0.0.0:6800":   false,
		"192.168.1.2:80": false,
		"unix-socket":    true, // unparsable: fail safe to "assume local"
	}
	for listen, want := range cases {
		if got := isLoopbackListen(listen); got != want {
			t.Errorf("isLoopbackListen(%q)=%v want %v", listen, got, want)
		}
	}
}
