package plugin

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

func signedPack(t *testing.T, root, name string, m *Manifest, script, privB64 string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	writePack(t, dir, m, script)
	if _, err := SignPack(dir, privB64); err != nil {
		t.Fatal(err)
	}
	return dir
}

func managerFor(t *testing.T, dir string, pub ed25519.PublicKey, insecure bool, extra func(*Options)) *Manager {
	t.Helper()
	o := Options{
		Dir: dir, TrustedKeys: []ed25519.PublicKey{pub},
		VerifySignature: true, Insecure: insecure, Guard: blockGuard{},
		Doer: happyDoer(), Fallback: stubResolver{name: "fallback"},
		ReloadInterval: time.Second,
	}
	if extra != nil {
		extra(&o)
	}
	mgr, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mgr
}

func TestManagerLoadsSignedPackAndResolves(t *testing.T) {
	pub, _, _, privB64 := keyPair(t)
	root := t.TempDir()
	m := baseManifest()
	signedPack(t, root, "site.example", &m, happyScript, privB64)
	mgr := managerFor(t, root, pub, false, nil)
	if mgr.Active() != 1 || mgr.Name() != "plugin" {
		t.Fatalf("active=%d name=%q", mgr.Active(), mgr.Name())
	}
	in := core.Input{Raw: "https://example.com/video/1", Kind: core.InputURL}
	if !mgr.Match(in) {
		t.Fatal("manager must match the plugin rule")
	}
	if mgr.Match(core.Input{Raw: "https://other.com/x", Kind: core.InputURL}) {
		t.Fatal("manager must not match unrelated URLs")
	}
	res, err := mgr.Resolve(core.ResolveContext{Ctx: context.Background(), Guard: blockGuard{}}, in)
	if err != nil || res.Mirrors[0].URL != "https://cdn.example.com/v/1.mp4" {
		t.Fatalf("manager resolve: %v %+v", err, res)
	}
	info := mgr.Plugins()
	if len(info) != 1 || info[0].State != "active" || info[0].Name != "site.example" {
		t.Fatalf("Plugins()=%+v", info)
	}
	if len(mgr.Failures()) != 0 {
		t.Fatalf("unexpected failures: %+v", mgr.Failures())
	}
}

func TestManagerRejectsUnsignedAndBadPacks(t *testing.T) {
	pub, _, _, _ := keyPair(t)
	root := t.TempDir()
	m := baseManifest()
	writePack(t, filepath.Join(root, "unsigned"), &m, happyScript)
	mgr := managerFor(t, root, pub, false, nil)
	if mgr.Active() != 0 {
		t.Fatal("unsigned pack must not be loaded")
	}
	if len(mgr.Failures()) != 1 {
		t.Fatalf("expected one rejection, got %+v", mgr.Failures())
	}
	// Insecure mode loads the same unsigned pack.
	mgr2 := managerFor(t, root, pub, true, nil)
	if mgr2.Active() != 1 {
		t.Fatalf("insecure mode should load unsigned pack: %+v", mgr2.Failures())
	}
	// A pack with forbidden syntax is rejected even in insecure mode.
	bad := baseManifest()
	bad.Name = "bad.rule"
	writePack(t, filepath.Join(root, "bad"), &bad,
		`export function resolve() { const x = 1; return x; }`)
	mgr3 := managerFor(t, root, pub, true, nil)
	if mgr3.Active() != 1 || len(mgr3.Failures()) != 1 {
		t.Fatalf("bad pack must be rejected while good ones stay: active=%d failures=%+v",
			mgr3.Active(), mgr3.Failures())
	}
}

func TestManagerKeepsLastGoodVersionOnBadUpdate(t *testing.T) {
	pub, _, _, privB64 := keyPair(t)
	root := t.TempDir()
	m := baseManifest()
	dir := signedPack(t, root, "site.example", &m, happyScript, privB64)
	mgr := managerFor(t, root, pub, false, nil)
	if mgr.Active() != 1 {
		t.Fatal("initial load failed")
	}
	// Tamper with the script without re-signing.
	if err := os.WriteFile(filepath.Join(dir, "index.js"),
		[]byte(happyScript+"\n// tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if mgr.Active() != 1 {
		t.Fatal("last known-good version must stay active")
	}
	if len(mgr.Failures()) != 1 || !strings.Contains(mgr.Failures()["site.example"], "signature") {
		t.Fatalf("signature failure not recorded: %+v", mgr.Failures())
	}
	// Resolve still works using the previous compiled program.
	_, err := mgr.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "https://example.com/video/1", Kind: core.InputURL})
	if err != nil {
		t.Fatalf("last-good version must still resolve: %v", err)
	}
}

func TestManagerHotReloadSwapsSnapshot(t *testing.T) {
	pub, _, _, privB64 := keyPair(t)
	root := t.TempDir()
	m := baseManifest()
	dir := signedPack(t, root, "site.example", &m, happyScript, privB64)
	mgr := managerFor(t, root, pub, false, nil)
	fp1, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.Plugins()[0].Version != "1.0.0" {
		t.Fatal("initial version mismatch")
	}
	// Ship a new signed version of the same rule pack.
	m.Version = "2.0.0"
	writePack(t, dir, &m, happyScript)
	if _, err := SignPack(dir, privB64); err != nil {
		t.Fatal(err)
	}
	fp2, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == fp2 {
		t.Fatal("fingerprint must change on a rule-pack update")
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if mgr.Active() != 1 || mgr.Plugins()[0].Version != "2.0.0" {
		t.Fatalf("hot reload did not swap the snapshot: %+v", mgr.Plugins())
	}
	// A no-op Reload must not disturb the snapshot.
	if err := mgr.Reload(); err != nil || mgr.Active() != 1 {
		t.Fatalf("no-op reload: %v active=%d", err, mgr.Active())
	}
}

func TestManagerWatchReloads(t *testing.T) {
	pub, _, _, privB64 := keyPair(t)
	root := t.TempDir()
	m := baseManifest()
	dir := signedPack(t, root, "site.example", &m, happyScript, privB64)
	mgr := managerFor(t, root, pub, false, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Watch(ctx)
	m.Version = "3.0.0"
	writePack(t, dir, &m, happyScript)
	if _, err := SignPack(dir, privB64); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Plugins()[0].Version == "3.0.0" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Watch did not pick up the re-signed rule pack: %+v", mgr.Plugins())
}

func TestManagerFallbackOnRuleOutdated(t *testing.T) {
	pub, _, _, privB64 := keyPair(t)
	root := t.TempDir()
	m := baseManifest()
	signedPack(t, root, "site.example", &m,
		`export function resolve() { throw new Error("RULE_OUTDATED: selector changed"); }`, privB64)
	mgr := managerFor(t, root, pub, false, nil)
	res, err := mgr.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "https://example.com/video/1", Kind: core.InputURL})
	if err != nil || res.ID != "fallback" {
		t.Fatalf("fallback must serve the task: %v %+v", err, res)
	}
	// Without a fallback the classified error is returned (never silently dropped).
	mgr2 := managerFor(t, root, pub, false, func(o *Options) { o.Fallback = nil })
	_, err = mgr2.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "https://example.com/video/1", Kind: core.InputURL})
	if !IsRuleOutdated(err) {
		t.Fatalf("RULE_OUTDATED must surface when no fallback exists: %v", err)
	}
	// A failing fallback also returns the original classified error.
	mgr3 := managerFor(t, root, pub, false, func(o *Options) {
		o.Fallback = errResolver{}
	})
	_, err = mgr3.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "https://example.com/video/1", Kind: core.InputURL})
	if !IsRuleOutdated(err) || core.AsError(err).Details["fallback"] != "attempted and failed" {
		t.Fatalf("fallback failure detail missing: %v", err)
	}
}

type errResolver struct{}

func (errResolver) Name() string          { return "err" }
func (errResolver) Match(core.Input) bool { return true }
func (errResolver) Resolve(core.ResolveContext, core.Input) (*core.Resource, error) {
	return nil, core.Err(core.E_RESOLVE_FAILED, "fallback failed")
}

func TestManagerSinglePackLayoutAndEmptyDir(t *testing.T) {
	pub, _, _, privB64 := keyPair(t)
	root := t.TempDir()
	m := baseManifest()
	writePack(t, root, &m, happyScript)
	if _, err := SignPack(root, privB64); err != nil {
		t.Fatal(err)
	}
	mgr := managerFor(t, root, pub, false, nil)
	if mgr.Active() != 1 {
		t.Fatalf("single-pack layout: active=%d failures=%+v", mgr.Active(), mgr.Failures())
	}
	dirs, err := pluginDirs(root)
	if err != nil || len(dirs) != 1 || dirs[0] != root {
		t.Fatalf("pluginDirs single layout: %v %v", dirs, err)
	}
	empty := t.TempDir()
	mgr2 := managerFor(t, empty, pub, false, nil)
	if mgr2.Active() != 0 || mgr2.Match(core.Input{Raw: "https://x"}) {
		t.Fatal("empty rule dir must register no active rules")
	}
	if _, err := mgr2.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "https://x"}); err == nil {
		t.Fatal("no active rule must produce an explicit error")
	}
	if _, err := New(Options{Dir: filepath.Join(root, "not-a-dir")}); err == nil {
		t.Fatal("invalid dir must fail at New")
	}
	mgr3, err := New(Options{})
	if err != nil || mgr3.Active() != 0 {
		t.Fatalf("no-dir manager: %v", err)
	}
	// Watch on an empty manager returns immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mgr3.Watch(ctx)
}

func TestFingerprintAndPluginDirsLimits(t *testing.T) {
	root := t.TempDir()
	m := baseManifest()
	writePack(t, filepath.Join(root, "a"), &m, happyScript)
	fp1, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "index.js"),
		[]byte(happyScript+"\n/* changed */"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp2, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == fp2 {
		t.Fatal("fingerprint must change when a rule file changes")
	}
	for i := 0; i <= maxPlugins; i++ {
		d := filepath.Join(root, "bulk", string(rune('a'+i%26))+string(rune('a'+i/26)))
		writePack(t, d, &m, happyScript)
	}
	if _, err := pluginDirs(filepath.Join(root, "bulk")); err == nil {
		t.Fatal("too many packs must be rejected")
	}
	if _, err := Fingerprint(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing dir fingerprint must fail")
	}
}
