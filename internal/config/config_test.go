package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRateAndSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"8MiB/s", 8 << 20}, {"512KiB/s", 512 << 10}, {"1MB/s", 1e6},
		{"0", 0}, {"unlimited", 0}, {"1024/s", 1024},
	}
	for _, c := range cases {
		got, err := ParseRate(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseRate(%q)=%d,%v want %d", c.in, got, err, c.want)
		}
	}
	sizes := []struct {
		in   string
		want int64
	}{
		{"4MiB", 4 << 20}, {"64KiB", 64 << 10}, {"1024", 1024}, {"1GiB", 1 << 30},
	}
	for _, c := range sizes {
		got, err := ParseSize(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseSize(%q)=%d,%v want %d", c.in, got, err, c.want)
		}
	}
	if _, err := ParseRate("garbageX/s"); err == nil {
		t.Fatal("invalid rate accepted")
	}
}

func TestLoadDefaultsAndOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: \"127.0.0.1:9999\"\nglobal:\n  maxDownloadRate: \"2MiB/s\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9999" || cfg.Global.MaxDownloadRate != "2MiB/s" {
		t.Fatalf("overlay failed: %+v", cfg)
	}
	if cfg.Global.MaxConnectionsPerTask != 16 || cfg.TempSuffix != ".part" {
		t.Fatal("defaults were lost during overlay")
	}
}

// docs/07 §1 / ADR-0016: remote control plane must be explicitly authenticated.
func TestRemoteListenRequiresToken(t *testing.T) {
	base := Defaults()
	cases := []struct {
		name    string
		listen  string
		token   string
		devMode bool
		wantErr bool
	}{
		{"loopback without token", "127.0.0.1:0", "", false, false},
		{"wildcard without token", "0.0.0.0:6800", "", false, true},
		{"wildcard with token", "0.0.0.0:6800", "s3cret", false, false},
		{"wildcard with token and dev mode", "0.0.0.0:6800", "s3cret", true, true},
		{"loopback dev mode", "127.0.0.1:0", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			c.Listen, c.RemoteToken = tc.listen, tc.token
			c.Security.AllowLoopbackForDev = tc.devMode
			err := c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate()=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestManagerPatchValidationAndCallback(t *testing.T) {
	calls := 0
	m := NewManager(Defaults(), func(Config) { calls++ })
	if _, err := m.Patch(map[string]any{"global.maxDownloadRate": "1MiB/s"}); err != nil {
		t.Fatal(err)
	}
	if m.Get().Global.MaxDownloadRate != "1MiB/s" || calls != 1 {
		t.Fatalf("patch not applied: %+v calls=%d", m.Get().Global, calls)
	}
	for _, bad := range []map[string]any{
		{"global.maxDownloadRate": "not-a-rate"},
		{"global.maxConcurrentTasks": "0"},
		{"unknown.key": "x"},
	} {
		if _, err := m.Patch(bad); err == nil {
			t.Errorf("invalid patch accepted: %v", bad)
		}
	}
	if calls != 1 {
		t.Fatal("onChange must not fire for a rejected patch")
	}
}
