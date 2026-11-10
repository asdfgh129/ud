package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

// blockGuard is a strict, offline URLGuard. It blocks loopback/private hosts
// so SSRF regressions are visible, and never allows a dial.
type blockGuard struct {
	extra func(raw string) error
	max   int
}

func (g blockGuard) Check(_ context.Context, raw string) error {
	if g.extra != nil {
		if err := g.extra(raw); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "scheme not allowed: %s", raw)
	}
	host := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://"), "/", 2)[0]
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" ||
		strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "10.") {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "private/loopback host %q blocked", host)
	}
	return nil
}
func (g blockGuard) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("test guard must never dial")
}
func (g blockGuard) MaxRedirects() int {
	if g.max > 0 {
		return g.max
	}
	return 3
}

func baseManifest() Manifest {
	return Manifest{
		Name: "site.example", Version: "1.0.0", APIVersion: APIVersion,
		Type: "resolver", Runtime: RuntimeGoja, Entry: "index.js",
		Match: []string{`^https?://example\.com/video/`},
		Capabilities: Capabilities{
			HTTP: &HTTPCapability{
				AllowedHosts: []string{"example.com", "cdn.example.com"}, MaxRedirects: 3,
			},
			Crypto: []string{"base64", "sha256"},
		},
		Limits: Limits{TimeoutMs: 1000, MemoryMB: 4, MaxResponseBytes: 1 << 20},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writePack(t *testing.T, dir string, m *Manifest, script string) []byte {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(t, m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, m.Entry), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func newRuntime(t *testing.T, m Manifest, script string, d Doer, g core.URLGuard) *Runtime {
	t.Helper()
	rt, err := Compile(&m, []byte(script), Engine{Guard: g, Doer: d})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return rt
}

func resolveRaw(t *testing.T, rt *Runtime, raw string, g core.URLGuard) (*core.Resource, error) {
	t.Helper()
	return rt.Resolve(core.ResolveContext{Ctx: context.Background(), Guard: g}, core.Input{Raw: raw, Kind: core.InputURL})
}

const happyScript = `export function resolve(url, ctx) {
  var r = ctx.http.get(url, { headers: { "User-Agent": "UD/1.0" } });
  var m = /"playUrl":"([^"]+)"/.exec(r.body);
  if (!m) throw new Error("RULE_OUTDATED: playUrl not found");
  return {
    files: [{ index: 0, path: "video.mp4", size: 123, selected: true }],
    mirrors: [{ url: unescape(m[1]), priority: 1, weight: 1.0 }],
    headers: { "Referer": url },
    identity: { isAdaptive: false, variantId: "v1" }
  };
}
`

func happyDoer() *ReplayDoer {
	return &ReplayDoer{Responses: map[string]FixtureResponse{
		"https://example.com/video/1": {
			Status: 200,
			Body:   `{"playUrl":"https%3A%2F%2Fcdn.example.com%2Fv%2F1.mp4"}`,
		},
	}}
}

// stubResolver is a built-in fallback used to prove RULE_OUTDATED does not
// silently drop a task.
type stubResolver struct{ name string }

func (s stubResolver) Name() string          { return s.name }
func (s stubResolver) Match(core.Input) bool { return true }
func (s stubResolver) Resolve(_ core.ResolveContext, _ core.Input) (*core.Resource, error) {
	return &core.Resource{ID: "fallback", Kind: core.KindSingle, Engine: core.EngineGo}, nil
}

var _ core.URLGuard = blockGuard{}
var _ Doer = (*ReplayDoer)(nil)
var _ core.Resolver = stubResolver{}
