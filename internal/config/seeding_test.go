package config

import (
	"strings"
	"testing"
)

func TestBTDefaultsAreLiveAndHonest(t *testing.T) {
	bt := Defaults().BT
	if !bt.SeedEnabled {
		t.Fatal("ADR-0021 path A: seeding must be enabled by default in the daemon")
	}
	if bt.SeedRatio != 1.0 || bt.SeedTimeMinutes != 60 {
		t.Fatalf("bounded seeding targets changed: ratio=%v minutes=%d", bt.SeedRatio, bt.SeedTimeMinutes)
	}
	if bt.MaxUploadSlots != 8 || !bt.EnableUPnP || !bt.EnableDHT || !bt.EnablePEX {
		t.Fatalf("etiquette defaults changed: %+v", bt)
	}
	if !strings.HasPrefix(bt.PeerID, "-UD") {
		t.Fatalf("ADR-0017: the default peerID must be honest, got %q", bt.PeerID)
	}
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("stock defaults must validate: %v", err)
	}
}

func TestBTValidateRequiresBoundedSeeding(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*BTConfig)
		valid bool
	}{
		{"stock", func(*BTConfig) {}, true},
		{"disabled seeding may have zero targets", func(b *BTConfig) {
			b.SeedEnabled = false
			b.SeedRatio = 0
			b.SeedTimeMinutes = 0
			b.MaxUploadSlots = 0
		}, true},
		{"ratio only", func(b *BTConfig) { b.SeedTimeMinutes = 0 }, true},
		{"time only", func(b *BTConfig) { b.SeedRatio = 0 }, true},
		{"negative ratio", func(b *BTConfig) { b.SeedRatio = -1 }, false},
		{"negative time", func(b *BTConfig) { b.SeedTimeMinutes = -1 }, false},
		{"unbounded obligation", func(b *BTConfig) {
			b.SeedRatio = 0
			b.SeedTimeMinutes = 0
		}, false},
		{"no upload slots", func(b *BTConfig) { b.MaxUploadSlots = 0 }, false},
		{"invalid listen port", func(b *BTConfig) { b.ListenPort = 70000 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			tc.mut(&c.BT)
			err := c.Validate()
			if tc.valid && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("expected fail-closed validation error")
			}
		})
	}
}

func TestBTEnvOverridesAreApplied(t *testing.T) {
	t.Setenv("UD_BT_SEED_ENABLED", "false")
	t.Setenv("UD_BT_SEED_RATIO", "2.5")
	t.Setenv("UD_BT_SEED_TIME_MINUTES", "15")
	t.Setenv("UD_BT_UPLOAD_SLOTS", "3")
	t.Setenv("UD_BT_ENABLE_UPNP", "false")
	t.Setenv("UD_BT_LISTEN_PORT", "6881")
	t.Setenv("UD_BT_PEER_ID", "-UD0002-abcdef123456")
	t.Setenv("UD_BT_LISTEN_HOST", "127.0.0.1")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with BT env overrides: %v", err)
	}
	bt := cfg.BT
	if bt.SeedEnabled || bt.EnableUPnP {
		t.Fatalf("boolean overrides not applied: %+v", bt)
	}
	if bt.SeedRatio != 2.5 || bt.SeedTimeMinutes != 15 || bt.MaxUploadSlots != 3 || bt.ListenPort != 6881 {
		t.Fatalf("numeric overrides not applied: %+v", bt)
	}
	if bt.PeerID != "-UD0002-abcdef123456" || bt.ListenHost != "127.0.0.1" {
		t.Fatalf("string overrides not applied: %+v", bt)
	}
}
