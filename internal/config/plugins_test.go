package config

import (
	"strings"
	"testing"
)

func TestPluginsConfigValidate(t *testing.T) {
	p := Defaults().Plugins
	if p.Dir != "" || !p.VerifySignature || p.Runtime != "goja" || p.Insecure {
		t.Fatalf("unsafe plugin defaults: %+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	bad := []PluginsConfig{
		{Runtime: "wasm", VerifySignature: true, ReloadInterval: "30s"},
		{Runtime: "goja", VerifySignature: true, ReloadInterval: "0"},
		{Runtime: "goja", VerifySignature: true, ReloadInterval: "25h"},
		{Runtime: "goja", VerifySignature: true, ReloadInterval: "nonsense"},
		{Runtime: "goja", VerifySignature: false, Insecure: false},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("case %d should be rejected: %+v", i, p)
		}
	}
	ok := PluginsConfig{Runtime: "goja", VerifySignature: false, Insecure: true, ReloadInterval: "1s"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("explicit insecure opt-in must be accepted: %v", err)
	}
	empty := PluginsConfig{}
	if err := empty.Validate(); err == nil {
		t.Fatal("empty VerifySignature without insecure must fail closed")
	}
}

func TestPluginsEnvOverrides(t *testing.T) {
	t.Setenv("UD_PLUGINS_DIR", "/tmp/rules")
	t.Setenv("UD_PLUGINS_RUNTIME", "goja")
	t.Setenv("UD_PLUGINS_RELOAD_INTERVAL", "5s")
	t.Setenv("UD_PLUGIN_KEYS", " keyA , ,keyB ")
	t.Setenv("UD_PLUGIN_INSECURE", "true")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Plugins.Dir != "/tmp/rules" || c.Plugins.ReloadInterval != "5s" || !c.Plugins.Insecure {
		t.Fatalf("env overrides not applied: %+v", c.Plugins)
	}
	if len(c.Plugins.TrustedKeys) != 2 || c.Plugins.TrustedKeys[0] != "keyA" || c.Plugins.TrustedKeys[1] != "keyB" {
		t.Fatalf("trusted keys = %v", c.Plugins.TrustedKeys)
	}
	t.Setenv("UD_PLUGIN_INSECURE", "not-a-bool")
	c2, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c2.Plugins.Insecure {
		t.Fatal("invalid bool env must not enable insecure mode")
	}
}

func TestConfigValidateRejectsInsecurePluginsOnRemoteListen(t *testing.T) {
	c := Defaults()
	c.Listen = "0.0.0.0:6800"
	c.RemoteToken = "token"
	if err := c.Validate(); err != nil {
		t.Fatalf("baseline remote config should validate: %v", err)
	}
	c.Plugins.Insecure = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "loopback-only") {
		t.Fatalf("insecure plugins on a remote listener must be rejected: %v", err)
	}
	c.Listen = "127.0.0.1:6800"
	if err := c.Validate(); err != nil {
		t.Fatalf("insecure plugins on loopback must be allowed: %v", err)
	}
}
