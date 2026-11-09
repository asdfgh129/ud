package security

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/config"
)

// Coverage of the P0 helper surface that the release gate (docs/12 §8) names
// explicitly: security must reach 90%. These are all pure/contract helpers.

func TestStripSensitiveHeadersContract(t *testing.T) {
	if StripSensitiveHeaders(nil) != nil {
		t.Fatal("empty input must return nil so omitempty stays compact")
	}
	if got := StripSensitiveHeaders(map[string]string{
		"Authorization": "Bearer s", "Cookie": "sid=s", "X-Api-Key": "k",
		"Proxy-Authorization": "Basic s", "Set-Cookie": "sid=s",
	}); got != nil {
		t.Fatalf("all-sensitive input must return nil, got %+v", got)
	}
	got := StripSensitiveHeaders(map[string]string{
		"Referer": "https://example.com", "accept-language": "zh-CN",
		"X-Api-Key": "k", "AUTHORIZATION": "Bearer s",
	})
	if len(got) != 2 || got["Referer"] != "https://example.com" || got["accept-language"] != "zh-CN" {
		t.Fatalf("non-sensitive headers must survive: %+v", got)
	}
	for _, k := range []string{"Authorization", "X-Api-Key", "X_API_KEY", "cookie", "Set-Cookie"} {
		if !IsSensitiveHeader(k) {
			t.Errorf("%q must be classified as sensitive", k)
		}
	}
	for _, k := range []string{"Referer", "Accept", "User-Agent", "Range"} {
		if IsSensitiveHeader(k) {
			t.Errorf("%q must not be classified as sensitive", k)
		}
	}
}

func TestEnsureDirAndTempPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("EnsureDir did not create a directory: %v", err)
	}
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir must be idempotent: %v", err)
	}
	if got := TempPath("/dl/f.bin", ""); got != "/dl/f.bin.part" {
		t.Fatalf("default temp suffix = %q", got)
	}
	if got := TempPath("/dl/f.bin", ".udtmp"); got != "/dl/f.bin.udtmp" {
		t.Fatalf("custom temp suffix = %q", got)
	}
}

func TestRedactURLEdgeCasesAndSecretScan(t *testing.T) {
	// url.Parse failure must still scrub the query with the regex fallback.
	if got := RedactURL("http://[::1?token=abc"); strings.Contains(got, "abc") || !strings.Contains(got, "***") {
		t.Fatalf("unparsable URL leaked a token: %q", got)
	}
	// Both userinfo and a sensitive query value must be removed before persistence.
	got := RedactURL("https://user:hunter2@example.com/f?token=abc&keep=1")
	if strings.Contains(got, "hunter2") || strings.Contains(got, "abc") || !strings.Contains(got, "keep=1") {
		t.Fatalf("userinfo/query redaction incomplete: %q", got)
	}
	if !ContainsSecret("Authorization: Bearer xyz") || ContainsSecret("Authorization: ***") {
		t.Fatal("ContainsSecret must ignore already-redacted values")
	}
	if ContainsSecret("GET /f.bin 200") {
		t.Fatal("ContainsSecret false positive")
	}
}

// Every branch of the SSRF address classifier (docs/11 §2 / docs/12 §6).
func TestClassifyIPMatrix(t *testing.T) {
	cases := []struct {
		ip, want                     string
		block, allowLoopback, devAny bool
	}{
		{"8.8.8.8", "", true, false, false},
		{"192.168.1.1", "", false, false, false},
		{"127.0.0.1", "", true, true, false},
		{"127.0.0.1", "", true, false, true},
		{"127.0.0.1", "loopback", true, false, false},
		{"10.1.2.3", "private", true, false, false},
		{"169.254.169.254", "link-local / cloud metadata", true, false, false},
		{"224.0.0.1", "link-local / cloud metadata", true, false, false},
		{"239.1.1.1", "non-global address", true, false, false},
		{"0.0.0.0", "non-global address", true, false, false},
		{"100.64.0.1", "reserved range 100.64.0.0/10", true, false, false},
	}
	for _, c := range cases {
		blocked, reason := classifyIP(netip.MustParseAddr(c.ip), c.block, c.allowLoopback, c.devAny)
		if (!blocked && c.want != "") || (blocked && reason != c.want) {
			t.Errorf("classifyIP(%s, %v,%v,%v) = (%v,%q), want reason %q",
				c.ip, c.block, c.allowLoopback, c.devAny, blocked, reason, c.want)
		}
	}
}

func TestGuardRuntimeUpdateAndPortParsing(t *testing.T) {
	g := productionGuard()
	if g.MaxRedirects() != 3 || g.AllowLoopback() {
		t.Fatal("unexpected initial guard config")
	}
	g.Update(config.Config{Security: config.SecurityConfig{
		MaxRedirects: 7, AllowLoopbackForDev: true,
		AllowedSchemes: []string{"https"}, AllowedPorts: []int{443},
	}})
	if g.MaxRedirects() != 7 || !g.AllowLoopback() {
		t.Fatal("runtime config PATCH did not reach the guard")
	}

	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"443", 443, true}, {"1", 1, true}, {"65535", 65535, true},
		{"0", 0, false}, {"65536", 0, false}, {"44a", 0, false}, {"", 0, false},
	} {
		got, err := parsePort(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parsePort(%q)=%d,%v want %d,ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
	ports := map[string]string{
		"https": "443", "ftps": "990", "ftp": "21", "sftp": "22",
		"http": "80", "unknown": "80",
	}
	for scheme, want := range ports {
		if got := defaultPort(scheme); got != want {
			t.Errorf("defaultPort(%q)=%q want %q", scheme, got, want)
		}
	}
}
