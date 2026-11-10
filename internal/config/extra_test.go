package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsMatchDocumentedBaseline(t *testing.T) {
	c := Defaults()
	if c.Listen != "127.0.0.1:0" || c.TempSuffix != ".part" || c.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.Global.MaxConcurrentTasks != 5 || c.Global.MaxConnectionsPerTask != 16 {
		t.Fatalf("unexpected global defaults: %+v", c.Global)
	}
	if !c.Security.BlockPrivateNetworks || c.Security.MaxRedirects != 3 {
		t.Fatalf("security defaults must be safe: %+v", c.Security)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestLoadRejectsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil ||
		!strings.Contains(err.Error(), "read config") {
		t.Fatalf("missing file must fail with a read error, got %v", err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("listen: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("broken YAML must fail with a parse error, got %v", err)
	}
	invalid := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(invalid, []byte("global:\n  maxConcurrentTasks: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(invalid); err == nil {
		t.Fatal("config failing Validate must not load")
	}
}

func TestLoadAppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("UD_LISTEN", "127.0.0.1:12345")
	t.Setenv("UD_DIR", "~/ud-env-test")
	t.Setenv("UD_LOG_LEVEL", "debug")
	t.Setenv("UD_REMOTE_TOKEN", "env-token")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:12345" || cfg.LogLevel != "debug" || cfg.RemoteToken != "env-token" {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
	if strings.HasPrefix(cfg.DownloadDir, "~") {
		t.Fatalf("downloadDir must be expanded, got %q", cfg.DownloadDir)
	}
	if !strings.HasSuffix(cfg.DownloadDir, "ud-env-test") {
		t.Fatalf("unexpected downloadDir %q", cfg.DownloadDir)
	}
}

func TestValidateRejectsUnsafeOrUnusableValues(t *testing.T) {
	cases := map[string]func(*Config){
		"zero concurrent tasks":     func(c *Config) { c.Global.MaxConcurrentTasks = 0 },
		"zero connections per task": func(c *Config) { c.Global.MaxConnectionsPerTask = 0 },
		"bad download rate":         func(c *Config) { c.Global.MaxDownloadRate = "fast" },
		"bad upload rate":           func(c *Config) { c.Global.MaxUploadRate = "2 bananas" },
		"bad chunk size":            func(c *Config) { c.Global.ChunkSize = "" },
		"too many redirects":        func(c *Config) { c.Security.MaxRedirects = 11 },
		"negative redirects":        func(c *Config) { c.Security.MaxRedirects = -1 },
		"empty temp suffix":         func(c *Config) { c.TempSuffix = "" },
		"temp suffix with slash":    func(c *Config) { c.TempSuffix = "part/x" },
		"listen without port":       func(c *Config) { c.Listen = "127.0.0.1" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := Defaults()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", name)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	if got := ExpandHome("~"); got != home {
		t.Fatalf("ExpandHome(~)=%q want %q", got, home)
	}
	if got := ExpandHome("~/dl"); got != filepath.Join(home, "dl") {
		t.Fatalf("ExpandHome(~/dl)=%q", got)
	}
	for _, p := range []string{"/abs/path", "relative/path", "~other/path"} {
		if got := ExpandHome(p); got != p {
			t.Fatalf("ExpandHome(%q) must be unchanged, got %q", p, got)
		}
	}
}

func TestParseSizeRejectsEmptyAndNegative(t *testing.T) {
	if _, err := ParseSize(""); err == nil {
		t.Fatal("empty size must fail")
	}
	for _, s := range []string{"-1MiB", "-1024", "4XiB"} {
		if _, err := ParseSize(s); err == nil {
			t.Fatalf("ParseSize(%q) must fail", s)
		}
	}
	if got, err := ParseRate(""); err != nil || got != 0 {
		t.Fatalf(`ParseRate("") must mean unlimited, got %d %v`, got, err)
	}
	if got, err := ParseSize("1.5MiB"); err != nil || got != int64(1.5*(1<<20)) {
		t.Fatalf("fractional sizes must be accepted, got %d %v", got, err)
	}
}

func TestManagerPatchAllSupportedKeys(t *testing.T) {
	m := NewManager(Defaults(), nil)
	patch := map[string]any{
		"global.maxDownloadRate":        "4MiB/s",
		"global.maxUploadRate":          "1MiB/s",
		"global.maxConcurrentTasks":     9,
		"global.maxConnectionsPerTask":  32,
		"global.chunkSize":              "8MiB",
		"security.blockPrivateNetworks": false,
	}
	got, err := m.Patch(patch)
	if err != nil {
		t.Fatal(err)
	}
	if got.Global.MaxDownloadRate != "4MiB/s" || got.Global.MaxUploadRate != "1MiB/s" ||
		got.Global.MaxConcurrentTasks != 9 || got.Global.MaxConnectionsPerTask != 32 ||
		got.Global.ChunkSize != "8MiB" || got.Security.BlockPrivateNetworks {
		t.Fatalf("patch not fully applied: %+v", got)
	}
	if m.Get().Global.MaxConcurrentTasks != 9 {
		t.Fatal("Get must observe the patched config")
	}
}

func TestManagerRejectsInvalidPatches(t *testing.T) {
	m := NewManager(Defaults(), nil)
	for name, kv := range map[string]map[string]any{
		"bad upload rate":         {"global.maxUploadRate": "x/s"},
		"non numeric concurrency": {"global.maxConcurrentTasks": "many"},
		"non numeric connections": {"global.maxConnectionsPerTask": "0"},
		"bad chunk size":          {"global.chunkSize": "huge"},
		"bad bool":                {"security.blockPrivateNetworks": "maybe"},
	} {
		if _, err := m.Patch(kv); err == nil {
			t.Errorf("%s: invalid patch accepted", name)
		}
	}
	// A rejected patch must leave the stored config untouched.
	if m.Get().Global.MaxDownloadRate != Defaults().Global.MaxDownloadRate {
		t.Fatal("rejected patch mutated the config")
	}
}

func TestManagerSetOnChangeReplacesCallback(t *testing.T) {
	m := NewManager(Defaults(), func(Config) { t.Fatal("stale callback must not run") })
	var seen Config
	m.SetOnChange(func(c Config) { seen = c })
	if _, err := m.Patch(map[string]any{"global.maxConcurrentTasks": 2}); err != nil {
		t.Fatal(err)
	}
	if seen.Global.MaxConcurrentTasks != 2 {
		t.Fatalf("replacement callback did not receive the new config: %+v", seen.Global)
	}
}

func TestManagerPatchWithoutCallback(t *testing.T) {
	m := NewManager(Defaults(), nil)
	if _, err := m.Patch(map[string]any{"global.maxConcurrentTasks": 3}); err != nil {
		t.Fatalf("patch without callback must succeed: %v", err)
	}
}
