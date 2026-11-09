package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

func productionGuard() *SSRFGuard {
	cfg := config.SecurityConfig{
		AllowedSchemes: []string{"http", "https", "ftp", "ftps", "sftp"},
		AllowedPorts:   []int{80, 443, 21, 22, 990},
		MaxRedirects:   3, BlockPrivateNetworks: true,
	}
	return NewSSRFGuard(cfg)
}

// docs/12 §6 SSRF matrix (T-NFR-SEC-01-*).
func TestSSRFGuardBlocksUntrustedTargets(t *testing.T) {
	g := productionGuard()
	cases := []struct {
		name string
		url  string
	}{
		{"loopback", "http://127.0.0.1/x"},
		{"loopback-v6", "http://[::1]/x"},
		{"private-10", "http://10.1.2.3/x"},
		{"private-192", "http://192.168.1.1/x"},
		{"cloud-metadata", "http://169.254.169.254/latest/meta-data/"},
		{"ipv4-mapped-private", "http://[::ffff:192.168.1.1]/x"},
		{"ipv4-mapped-loopback", "http://[::ffff:127.0.0.1]/x"},
		{"cgnat", "http://100.64.0.1/x"},
		{"file-scheme", "file:///etc/passwd"},
		{"gopher-scheme", "gopher://127.0.0.1/x"},
		{"non-standard-port", "http://8.8.8.8:8080/x"},
		{"embedded-credentials", "http://user:pass@8.8.8.8/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := g.Check(context.Background(), c.url)
			if err == nil {
				t.Fatalf("%s should be blocked", c.url)
			}
			if e := core.AsError(err); e.Code != core.E_RESOLVE_SSRF_BLOCKED && e.Code != core.E_RESOLVE_FAILED {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	if err := g.Check(context.Background(), "https://8.8.8.8/file.iso"); err != nil {
		t.Fatalf("public target should be allowed: %v", err)
	}
}

func TestSSRFRedirectValidatedPerHopAndCredentialsStripped(t *testing.T) {
	g := NewDevGuard()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	client := g.NewHTTPClient(0)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("redirect to metadata address should have been blocked")
	}
	if !strings.Contains(err.Error(), core.E_RESOLVE_SSRF_BLOCKED) {
		t.Fatalf("wrong error: %v", err)
	}
	// Cross-host redirect must strip Authorization / Cookie (docs/11 §2).
	target, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/x", nil)
	target.Header.Set("Authorization", "Bearer SECRET")
	target.Header.Set("Cookie", "sid=SECRET")
	via, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:2/start", nil)
	if err := client.CheckRedirect(target, []*http.Request{via}); err != nil {
		t.Fatal(err)
	}
	if target.Header.Get("Authorization") != "" || target.Header.Get("Cookie") != "" {
		t.Fatal("credentials were not stripped on cross-host redirect")
	}
}

func TestDevGuardAllowsOnlyLoopback(t *testing.T) {
	g := NewDevGuard()
	if err := g.Check(context.Background(), "http://127.0.0.1:54321/x"); err != nil {
		t.Fatalf("loopback fixture should be allowed: %v", err)
	}
	if err := g.Check(context.Background(), "http://10.0.0.5/x"); err == nil {
		t.Fatal("private address must stay blocked in dev mode")
	}
}

func TestPathGuard(t *testing.T) {
	root := t.TempDir()
	pg := PathGuard{}
	if _, err := pg.Clean(root, "sub/file.bin"); err != nil {
		t.Fatalf("valid relative path rejected: %v", err)
	}
	for _, bad := range []string{"../escape.bin", "/etc/passwd", "a/../../x", "CON", "NUL.txt", "x\x00y"} {
		if _, err := pg.Clean(root, bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		if _, err := pg.Clean(root, "link/file.bin"); err == nil {
			t.Error("symlink escape should be rejected")
		}
	}
}

func TestRedaction(t *testing.T) {
	raw := "https://example.com/a?token=SECRET&passkey=KEY123&foo=ok"
	clean := RedactURL(raw)
	if strings.Contains(clean, "SECRET") || strings.Contains(clean, "KEY123") || !strings.Contains(clean, "foo=ok") {
		t.Fatalf("bad redaction: %s", clean)
	}
	if strings.Contains(Redact("Authorization: Bearer abc123"), "abc123") {
		t.Fatal("header value leaked")
	}
	h := RedactHeaders(map[string]string{"Authorization": "Bearer abc", "Cookie": "sid=1", "Referer": "https://x"})
	if h["Authorization"] != "***" || h["Cookie"] != "***" || h["Referer"] != "https://x" {
		t.Fatalf("bad header redaction: %+v", h)
	}
	if ContainsSecret("authorization: ***") {
		t.Fatal("redacted value must not be flagged as a leak")
	}
}

func TestMemoryCredentialStore(t *testing.T) {
	s := NewMemoryCredentialStore()
	ref, err := s.Put(context.Background(), "SUPER-SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ref), "SUPER-SECRET") {
		t.Fatal("secret leaked into the reference")
	}
	v, err := s.Get(context.Background(), ref)
	if err != nil || v != "SUPER-SECRET" {
		t.Fatalf("get failed: %v %q", err, v)
	}
	if err := s.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), ref); err == nil {
		t.Fatal("deleted credential still readable")
	}
}
