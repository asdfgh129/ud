package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

func TestParseManifestValidAndDefaults(t *testing.T) {
	m := baseManifest()
	m.Limits = Limits{}
	m.Capabilities.HTTP.MaxRedirects = 0
	parsed, err := ParseManifest(mustJSON(t, &m))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Limits.TimeoutMs != defaultTimeoutMs || parsed.Limits.MemoryMB != defaultMemoryMB ||
		parsed.Limits.MaxResponseBytes != defaultMaxResponseSize {
		t.Fatalf("limits defaults not applied: %+v", parsed.Limits)
	}
	if parsed.Capabilities.HTTP.MaxRedirects != defaultMaxRedirects {
		t.Fatal("maxRedirects default not applied")
	}
	if len(parsed.MatchRule()) != 1 {
		t.Fatal("MatchRule did not compile")
	}
}

func TestParseManifestRejectsInvalid(t *testing.T) {
	base := func() Manifest { return baseManifest() }
	cases := []struct {
		name   string
		mutate func(*Manifest)
		code   string
	}{
		{"unknown field", func(m *Manifest) {}, core.E_INVALID_INPUT}, // handled separately below
		{"bad name", func(m *Manifest) { m.Name = "../evil" }, core.E_INVALID_INPUT},
		{"bad version", func(m *Manifest) { m.Version = "one" }, core.E_INVALID_INPUT},
		{"bad apiVersion", func(m *Manifest) { m.APIVersion = "v2" }, core.E_INVALID_INPUT},
		{"bad type", func(m *Manifest) { m.Type = "transport" }, core.E_INVALID_INPUT},
		{"bad runtime", func(m *Manifest) { m.Runtime = "wasm" }, core.E_CAPABILITY_UNAVAILABLE},
		{"bad entry", func(m *Manifest) { m.Entry = "../x.js" }, core.E_INVALID_INPUT},
		{"entry not js", func(m *Manifest) { m.Entry = "index.ts" }, core.E_INVALID_INPUT},
		{"no match", func(m *Manifest) { m.Match = nil }, core.E_INVALID_INPUT},
		{"bad regex", func(m *Manifest) { m.Match = []string{"["} }, core.E_INVALID_INPUT},
		{"match shadows builtin", func(m *Manifest) { m.Match = []string{`^magnet:`} }, core.E_INVALID_INPUT},
		{"catch-all match", func(m *Manifest) { m.Match = []string{`.*`} }, core.E_INVALID_INPUT},
		{"fs capability", func(m *Manifest) { m.Capabilities.FS = true }, core.E_INVALID_INPUT},
		{"env capability", func(m *Manifest) { m.Capabilities.Env = true }, core.E_INVALID_INPUT},
		{"subprocess capability", func(m *Manifest) { m.Capabilities.Subprocess = true }, core.E_INVALID_INPUT},
		{"bad crypto", func(m *Manifest) { m.Capabilities.Crypto = []string{"md5"} }, core.E_INVALID_INPUT},
		{"wildcard host", func(m *Manifest) { m.Capabilities.HTTP.AllowedHosts = []string{"*"} }, core.E_INVALID_INPUT},
		{"host with scheme", func(m *Manifest) { m.Capabilities.HTTP.AllowedHosts = []string{"https://x.com"} }, core.E_INVALID_INPUT},
		{"empty host", func(m *Manifest) { m.Capabilities.HTTP.AllowedHosts = []string{""} }, core.E_INVALID_INPUT},
		{"too many redirects", func(m *Manifest) { m.Capabilities.HTTP.MaxRedirects = 99 }, core.E_INVALID_INPUT},
		{"timeout too large", func(m *Manifest) { m.Limits.TimeoutMs = hardTimeoutMs + 1 }, core.E_INVALID_INPUT},
		{"memory too large", func(m *Manifest) { m.Limits.MemoryMB = hardMemoryMB + 1 }, core.E_INVALID_INPUT},
		{"response too large", func(m *Manifest) { m.Limits.MaxResponseBytes = hardMaxResponseSize + 1 }, core.E_INVALID_INPUT},
		{"negative timeout", func(m *Manifest) { m.Limits.TimeoutMs = -1 }, core.E_INVALID_INPUT},
		{"bad signature scheme", func(m *Manifest) { m.Signature = "sha256:abc" }, core.E_INVALID_INPUT},
	}
	for _, tc := range cases {
		if tc.name == "unknown field" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.mutate(&m)
			if _, err := ParseManifest(mustJSON(t, &m)); err == nil {
				t.Fatal("expected rejection")
			} else if got := core.AsError(err).Code; got != tc.code {
				t.Fatalf("code = %s, want %s (%v)", got, tc.code, err)
			}
		})
	}
	// Compile must refuse a nil manifest and re-run validation.
	if _, err := Compile(nil, nil, Engine{}); err == nil {
		t.Fatal("nil manifest must be refused")
	}
}

func TestParseManifestDisallowsUnknownFieldsAndOversize(t *testing.T) {
	if _, err := ParseManifest([]byte(`{"name":"x","unexpected":1}`)); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := ParseManifest([]byte("[")); err == nil {
		t.Fatal("non-object manifest must be rejected")
	}
	if _, err := ParseManifest(make([]byte, (64<<10)+1)); err == nil {
		t.Fatal("oversize manifest must be rejected")
	}
}

func TestHostAllowed(t *testing.T) {
	cases := []struct {
		patterns []string
		host     string
		want     bool
	}{
		{[]string{"example.com"}, "example.com", true},
		{[]string{"example.com"}, "EXAMPLE.COM", true},
		{[]string{"example.com"}, "a.example.com", false},
		{[]string{"*.cdn.example.com"}, "x.cdn.example.com", true},
		{[]string{"*.cdn.example.com"}, "cdn.example.com", false},
		{[]string{"*.cdn.example.com"}, "evil.com", false},
		{[]string{"", "*.example.com."}, "a.example.com", true},
		{nil, "example.com", false},
	}
	for _, tc := range cases {
		if got := HostAllowed(tc.patterns, tc.host); got != tc.want {
			t.Errorf("HostAllowed(%v,%q)=%v want %v", tc.patterns, tc.host, got, tc.want)
		}
	}
}

func TestLoadDirBoundsAndTraversal(t *testing.T) {
	dir := t.TempDir()
	m := baseManifest()
	writePack(t, dir, &m, happyScript)
	parsed, script, raw, err := LoadDir(dir)
	if err != nil || parsed.Name != m.Name || len(script) == 0 || len(raw) == 0 {
		t.Fatalf("LoadDir: %v", err)
	}

	m.Entry = "../outside.js"
	writePack(t, dir, &m, happyScript)
	if _, _, _, err := LoadDir(dir); err == nil {
		t.Fatal("entry traversal must be refused")
	}

	m = baseManifest()
	writePack(t, dir, &m, happyScript)
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(strings.Repeat("x", maxScriptBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadDir(dir); err == nil {
		t.Fatal("oversize script must be refused")
	}
	if err := os.Remove(filepath.Join(dir, "index.js")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadDir(dir); err == nil {
		t.Fatal("missing entry must be refused")
	}
	if _, _, _, err := LoadDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing manifest must be refused")
	}
	// A directory in place of a regular file is not a valid manifest.
	if err := os.Remove(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadDir(dir); err == nil {
		t.Fatal("non-regular manifest must be refused")
	}
}

func TestMatchRuleAndName(t *testing.T) {
	m := baseManifest()
	rt := newRuntime(t, m, happyScript, happyDoer(), blockGuard{})
	if rt.Name() != "plugin:site.example" {
		t.Fatalf("Name()=%q", rt.Name())
	}
	if !rt.Match(core.Input{Raw: "https://example.com/video/1"}) {
		t.Fatal("expected match")
	}
	if rt.Match(core.Input{Raw: "https://other.com/video/1"}) {
		t.Fatal("unexpected match")
	}
	if _, err := resolveRaw(t, rt, "https://other.com/video/1", blockGuard{}); err == nil {
		t.Fatal("unmatched input must not resolve")
	}
	got := rt.Manifest()
	if got.Name != m.Name {
		t.Fatal("Manifest() copy mismatch")
	}
}
