package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/plugin"
)

func TestPluginCommandScaffoldLifecycle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "site.example")
	if err := pluginCommand([]string{"scaffold", dir}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "index.js", "cases.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if err := pluginCommand([]string{"scaffold", dir}); err == nil {
		t.Fatal("scaffold must refuse to overwrite an existing pack")
	}
}

func TestPluginCommandSignVerifyTestList(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "site.example")
	if err := pluginCommand([]string{"scaffold", dir}); err != nil {
		t.Fatal(err)
	}
	pubB64, privB64, err := plugin.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	pubFile := filepath.Join(root, "pub.key")
	privFile := filepath.Join(root, "priv.key")
	if err := os.WriteFile(pubFile, []byte("# trusted\npublic: "+pubB64+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privFile, []byte(privB64+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pluginCommand([]string{"verify", dir}); err == nil {
		t.Fatal("unsigned pack must be rejected without --insecure-plugins")
	}
	if err := pluginCommand([]string{"verify", dir, "--insecure-plugins"}); err != nil {
		t.Fatalf("insecure verify: %v", err)
	}
	if err := pluginCommand([]string{"sign", dir, "--key", privFile}); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := pluginCommand([]string{"verify", dir, "--key", pubFile}); err != nil {
		t.Fatalf("signed verify: %v", err)
	}
	fixture := filepath.Join(dir, "cases.json")
	if err := pluginCommand([]string{"test", dir, "--fixture", fixture, "--key", pubFile}); err != nil {
		t.Fatalf("replay test: %v", err)
	}
	if err := pluginCommand([]string{"list", dir, "--key", pubFile}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := pluginCommand([]string{"keygen"}); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	// Tamper and confirm the offline verifier rejects it.
	if err := os.WriteFile(filepath.Join(dir, "index.js"),
		[]byte("export function resolve(){ return {}; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pluginCommand([]string{"verify", dir, "--key", pubFile}); err == nil {
		t.Fatal("tampered signed pack must be rejected")
	}
	// A failing fixture case must propagate as an error.
	bad := filepath.Join(root, "bad.json")
	if err := os.WriteFile(bad,
		[]byte(`{"responses":{},"cases":[{"url":"https://example.com/video/1","expect":{"mirrors":["https://wrong.example/x"]}}]}`),
		0o600); err != nil {
		t.Fatal(err)
	}
	insecurePlugins = true
	defer func() { insecurePlugins = false }()
	if err := pluginCommand([]string{"test", dir, "--fixture", bad}); err == nil {
		t.Fatal("failing fixture case must return an error")
	}
}

func TestPluginCommandUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{}, {"unknown"}, {"scaffold"}, {"verify"}, {"test", "x"},
		{"list"}, {"sign", "x"}, {"keygen", "extra"},
	} {
		if err := pluginCommand(args); err == nil {
			t.Errorf("pluginCommand(%v) should have failed", args)
		}
	}
	if err := pluginCommand([]string{"test", "x", "--fixture", "y", "--key", filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("missing key file must error")
	}
	if _, err := loadKeys("", false); err == nil {
		t.Fatal("loadKeys must require a key file")
	}
	if _, err := loadKeys("", true); err != nil {
		t.Fatalf("insecure loadKeys: %v", err)
	}
}

func TestReadKeyLinesAndReorderFlags(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "keys")
	if err := os.WriteFile(p, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readKeyLines(p); err == nil {
		t.Fatal("empty key file must error")
	}
	if err := os.WriteFile(p, []byte("#c\n one \n\nprivate: two \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := readKeyLines(p)
	if err != nil || len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("readKeyLines: %v %v", lines, err)
	}
	if _, err := readPrivateKey(p); err == nil {
		t.Fatal("two keys must not be accepted as a private key")
	}
	if _, err := readPrivateKey(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing key file must error")
	}
	got := reorderFlags([]string{"dir", "--insecure-plugins", "x", "--key", "k"})
	if strings.Join(got, " ") != "--insecure-plugins --key k dir x" {
		t.Fatalf("reorderFlags = %v", got)
	}
}
