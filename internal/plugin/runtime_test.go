package plugin

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

func TestRuntimeHappyPath(t *testing.T) {
	m := baseManifest()
	d := happyDoer()
	rt := newRuntime(t, m, happyScript, d, blockGuard{})
	res, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Kind != core.KindSingle || res.Engine != core.EngineGo || res.ID == "" {
		t.Fatalf("bad Resource header: %+v", res)
	}
	if len(res.Mirrors) != 1 || res.Mirrors[0].URL != "https://cdn.example.com/v/1.mp4" {
		t.Fatalf("bad mirrors: %+v", res.Mirrors)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "video.mp4" || res.Files[0].Size != 123 || !res.Files[0].Selected {
		t.Fatalf("bad files: %+v", res.Files)
	}
	if res.Headers["Referer"] != "https://example.com/video/1" || res.Identity.VariantID != "v1" {
		t.Fatalf("bad headers/identity: %+v", res)
	}
	seen := d.Seen()
	if len(seen) != 1 || seen[0] != "https://example.com/video/1" {
		t.Fatalf("unexpected requests: %v", seen)
	}
}

// ADR-0014 §7 security case: an infinite loop must be killed by the deadline.
func TestRuntimeInfiniteLoopKilledByTimeout(t *testing.T) {
	m := baseManifest()
	m.Limits.TimeoutMs = 120
	rt := newRuntime(t, m,
		"export function resolve() { while (true) {} }", happyDoer(), blockGuard{})
	start := time.Now()
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("infinite loop was not terminated: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout took far too long")
	}
}

// ADR-0014 §7 security case: no fs / env / subprocess capability exists.
func TestRuntimeHasNoFsEnvOrSubprocess(t *testing.T) {
	m := baseManifest()
	script := `export function resolve(url, ctx) {
  if (ctx.fs || ctx.env || ctx.subprocess) { throw new Error("capability leak"); }
  if (typeof ctx.http.get !== "function") { throw new Error("ctx.http missing"); }
  return {
    files: [{ path: "a.bin" }],
    mirrors: [{ url: "https://cdn.example.com/a.bin" }]
  };
}
`
	rt := newRuntime(t, m, script, happyDoer(), blockGuard{})
	if _, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{}); err != nil {
		t.Fatalf("no-capability rule should resolve: %v", err)
	}

	// With no http capability the injected ctx has no ctx.http at all.
	m2 := baseManifest()
	m2.Capabilities.HTTP = nil
	rt2 := newRuntime(t, m2, script, happyDoer(), blockGuard{})
	if _, err := resolveRaw(t, rt2, "https://example.com/video/1", blockGuard{}); err == nil {
		t.Fatal("ctx.http must be absent when the capability is not granted")
	}
	// A script asking for ctx.http.get explicitly must never reach the Doer.
	rt3 := newRuntime(t, m2,
		`export function resolve(url, ctx) { return ctx.http.get(url).body; }`,
		happyDoer(), blockGuard{})
	if _, err := resolveRaw(t, rt3, "https://example.com/video/1", blockGuard{}); err == nil {
		t.Fatal("ctx.http.get must not work without the capability")
	}
}

// ADR-0014 §7 security case: a host outside allowedHosts is refused.
func TestRuntimeWhitelistBlocksRequests(t *testing.T) {
	m := baseManifest()
	script := `export function resolve(url, ctx) {
  return ctx.http.get("https://evil.example.com/steal").body;
}
`
	rt := newRuntime(t, m, script, &ReplayDoer{Responses: map[string]FixtureResponse{}}, blockGuard{})
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("whitelist bypass: %v", err)
	}
	if !strings.Contains(err.Error(), "allowedHosts") {
		t.Fatalf("error should name the whitelist: %v", err)
	}
}

// ADR-0014 §7 security case: private/loopback URLs are refused even when the
// manifest whitelists them (the shared SSRF Guard has the final say).
func TestRuntimePrivateURLBlockedByGuard(t *testing.T) {
	m := baseManifest()
	m.Match = []string{`^http://127\.0\.0\.1/`}
	m.Capabilities.HTTP.AllowedHosts = []string{"127.0.0.1"}
	script := `export function resolve(url, ctx) {
  return ctx.http.get("http://127.0.0.1/admin").body;
}
`
	rt := newRuntime(t, m, script, &ReplayDoer{}, blockGuard{})
	_, err := rt.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "http://127.0.0.1/x", Kind: core.InputURL})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("private URL was not blocked: %v", err)
	}
}

// ADR-0014 §7 security case: oversized responses are rejected.
func TestRuntimeOversizedResponseRejected(t *testing.T) {
	m := baseManifest()
	m.Limits.MaxResponseBytes = 16
	d := &ReplayDoer{Responses: map[string]FixtureResponse{
		"https://example.com/video/1": {Status: 200, Body: strings.Repeat("x", 4096)},
	}}
	rt := newRuntime(t, m,
		`export function resolve(url, ctx) { return ctx.http.get(url).body; }`, d, blockGuard{})
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err == nil || !strings.Contains(err.Error(), "maxResponseBytes") {
		t.Fatalf("oversized response not rejected: %v", err)
	}
	// Content-Length short-circuits before reading the body at all.
	d2 := &ReplayDoer{Responses: map[string]FixtureResponse{
		"https://example.com/video/1": {Status: 200, Body: strings.Repeat("x", 4096)},
	}}
	rt2 := newRuntime(t, m,
		`export function resolve(url, ctx) { return ctx.http.get(url).body; }`, d2, blockGuard{})
	if _, err := resolveRaw(t, rt2, "https://example.com/video/1", blockGuard{}); err == nil {
		t.Fatal("Content-Length overflow must be rejected")
	}
}

func TestRuntimeRedirectHopIsWhitelisted(t *testing.T) {
	m := baseManifest()
	d := &ReplayDoer{Responses: map[string]FixtureResponse{
		"https://example.com/video/1": {
			Status: 302, Headers: map[string]string{"Location": "https://evil.example.com/steal"},
		},
	}}
	rt := newRuntime(t, m,
		`export function resolve(url, ctx) { return ctx.http.get(url).body; }`, d, blockGuard{})
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("redirect to a non-whitelisted host must be blocked: %v", err)
	}
}

func TestRuntimeRedirectLimitAndLoop(t *testing.T) {
	m := baseManifest()
	d := &ReplayDoer{Responses: map[string]FixtureResponse{
		"https://example.com/video/1": {
			Status: 302, Headers: map[string]string{"Location": "https://example.com/video/1"},
		},
	}}
	rt := newRuntime(t, m,
		`export function resolve(url, ctx) { return ctx.http.get(url).body; }`, d, blockGuard{})
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err == nil || !strings.Contains(err.Error(), "redirect limit") {
		t.Fatalf("redirect loop must hit the hop limit: %v", err)
	}
}

func TestRuntimeOutputValidation(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{"private mirror host", `export function resolve() { return { files:[{path:"a"}], mirrors:[{url:"http://127.0.0.1/secret"}] }; }`, "allowedHosts"},
		{"path traversal", `export function resolve() { return { files:[{path:"../evil"}], mirrors:[{url:"https://cdn.example.com/a"}] }; }`, "output path"},
		{"sensitive header", `export function resolve() { return { files:[{path:"a"}], mirrors:[{url:"https://cdn.example.com/a"}], headers:{"Authorization":"Bearer x"} }; }`, "forbidden"},
		{"bad mirror scheme", `export function resolve() { return { files:[{path:"a"}], mirrors:[{url:"file:///etc/passwd"}] }; }`, "invalid mirror URL"},
		{"no mirrors", `export function resolve() { return { files:[{path:"a"}] }; }`, "mirrors"},
		{"no files", `export function resolve() { return { mirrors:[{url:"https://cdn.example.com/a"}] }; }`, "files"},
		{"non-string header", `export function resolve() { return { files:[{path:"a"}], mirrors:[{url:"https://cdn.example.com/a"}], headers:{Referer:1} }; }`, "must be a string"},
		{"negative size", `export function resolve() { return { files:[{path:"a",size:-1}], mirrors:[{url:"https://cdn.example.com/a"}] }; }`, "negative"},
		{"bad checksum algo", `export function resolve() { return { files:[{path:"a"}], mirrors:[{url:"https://cdn.example.com/a"}], checksum:{algo:"crc32"} }; }`, "checksum algo"},
		{"too many checksum pieces", `export function resolve() { var p = []; for (var i=0;i<5000;i++) { p.push("aa"); } return { files:[{path:"a"}], mirrors:[{url:"https://cdn.example.com/a"}], checksum:{algo:"sha256",pieces:p} }; }`, "too many checksum"},
		{"mirror host outside allowlist", `export function resolve() { return { files:[{path:"a"}], mirrors:[{url:"https://evil.example.com/a"}] }; }`, "allowedHosts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRuntime(t, baseManifest(), tc.script, happyDoer(), blockGuard{})
			_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestRuntimeOutputBudgetAndNonObject(t *testing.T) {
	m := baseManifest()
	m.Limits.MemoryMB = 1
	m.Limits.MaxResponseBytes = 256
	// A large output JSON must exceed maxResponseBytes.
	script := `export function resolve() {
  var s = "";
  for (var i = 0; i < 5000; i++) { s += "x"; }
  return { files:[{path:s}], mirrors:[{url:"https://cdn.example.com/a"}] };
}
`
	rt := newRuntime(t, m, script, happyDoer(), blockGuard{})
	if _, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{}); err == nil {
		t.Fatal("oversized output must be rejected")
	}
	for _, bad := range []string{
		`export function resolve() { return null; }`,
		`export function resolve() { return 42; }`,
		`export function resolve() { throw new Error("boom"); }`,
	} {
		rt := newRuntime(t, baseManifest(), bad, happyDoer(), blockGuard{})
		if _, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{}); err == nil {
			t.Fatalf("invalid output %q must be rejected", bad)
		}
	}
	// A script without an exported resolve function compiles but fails at call.
	rt = newRuntime(t, baseManifest(), `var meta = { a: 1 };`, happyDoer(), blockGuard{})
	if _, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{}); err == nil {
		t.Fatal("missing resolve must be rejected")
	}
}

func TestRuntimeRuleOutdatedIsClassified(t *testing.T) {
	rt := newRuntime(t, baseManifest(),
		`export function resolve() { throw new Error("RULE_OUTDATED: selector changed"); }`,
		happyDoer(), blockGuard{})
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if !IsRuleOutdated(err) {
		t.Fatalf("RULE_OUTDATED not classified: %v", err)
	}
	e := core.AsError(err)
	if e.Code != core.E_CAPABILITY_UNAVAILABLE || e.Details["plugin"] != "site.example" {
		t.Fatalf("wrong error shape: %+v", e)
	}
	if IsRuleOutdated(nil) || IsRuleOutdated(core.Err(core.E_RESOLVE_FAILED, "x")) {
		t.Fatal("IsRuleOutdated false positives")
	}
}

func TestRuntimeCryptoHelpersAndLogging(t *testing.T) {
	m := baseManifest()
	script := `export function resolve(url, ctx) {
  var enc = ctx.base64.encode("hello");
  var dec = ctx.base64.decode(enc);
  var sum = ctx.sha256("hello");
  ctx.log.info("encoded=" + enc);
  ctx.log.warn("decoded=" + dec);
  ctx.log.error("sha=" + sum);
  return {
    files: [{ path: dec + ".bin" }],
    mirrors: [{ url: "https://cdn.example.com/a" }],
    headers: { "X-Checksum": sum }
  };
}
`
	rt := newRuntime(t, m, script, happyDoer(), blockGuard{})
	res, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err != nil {
		t.Fatalf("crypto helpers: %v", err)
	}
	if res.Files[0].Path != "hello.bin" {
		t.Fatalf("base64 round trip failed: %q", res.Files[0].Path)
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	const wantSHA = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if res.Headers["X-Checksum"] != wantSHA {
		t.Fatalf("sha256 = %q", res.Headers["X-Checksum"])
	}
	// Crypto helpers disappear when not granted.
	m2 := baseManifest()
	m2.Capabilities.Crypto = nil
	rt2 := newRuntime(t, m2, script, happyDoer(), blockGuard{})
	if _, err := resolveRaw(t, rt2, "https://example.com/video/1", blockGuard{}); err == nil {
		t.Fatal("ungranted crypto helpers must not be injected")
	}
	_ = base64.StdEncoding
}

func TestRuntimeRequestHeaderValidation(t *testing.T) {
	m := baseManifest()
	checks := []string{
		`export function resolve(url, ctx) { ctx.http.get(url, {headers:{"Authorization":"x"}}); return {files:[{path:"a"}],mirrors:[{url:"https://cdn.example.com/a"}]}; }`,
		`export function resolve(url, ctx) { ctx.http.get(url, {headers:{"X-A":"a\r\nX-B:b"}}); return {files:[{path:"a"}],mirrors:[{url:"https://cdn.example.com/a"}]}; }`,
		`export function resolve(url, ctx) { ctx.http.get(); return {files:[{path:"a"}],mirrors:[{url:"https://cdn.example.com/a"}]}; }`,
		`export function resolve(url, ctx) { ctx.http.get("ftp://example.com/a"); return {files:[{path:"a"}],mirrors:[{url:"https://cdn.example.com/a"}]}; }`,
	}
	for _, s := range checks {
		rt := newRuntime(t, m, s, happyDoer(), blockGuard{})
		if _, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{}); err == nil {
			t.Fatalf("invalid request must be rejected: %s", s[:40])
		}
	}
	// A non-whitelisted scheme in the produced Resource is separately caught.
	rt := newRuntime(t, m,
		`export function resolve(url, ctx) { return ctx.http.get(url).status; }`,
		&ReplayDoer{Responses: map[string]FixtureResponse{
			"https://example.com/video/1": {Status: 200, Body: "ok"},
		}}, blockGuard{})
	if _, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{}); err == nil {
		t.Fatal("a non-Resource return value must be rejected")
	}
}

func TestRuntimeCancelledContextInterrupts(t *testing.T) {
	m := baseManifest()
	m.Limits.TimeoutMs = 5000
	rt := newRuntime(t, m, "export function resolve() { while (true) {} }", happyDoer(), blockGuard{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := rt.Resolve(core.ResolveContext{Ctx: ctx, Guard: blockGuard{}},
		core.Input{Raw: "https://example.com/video/1", Kind: core.InputURL})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("cancelled context must terminate the VM: %v", err)
	}
}
