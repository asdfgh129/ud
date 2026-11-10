package resolve

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

// fakeGuard records every URL handed to Check so tests can assert the SSRF
// guard runs before any Resource is produced.
type fakeGuard struct {
	checked []string
	err     error
}

func (g *fakeGuard) Check(_ context.Context, raw string) error {
	g.checked = append(g.checked, raw)
	return g.err
}

func (g *fakeGuard) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("fakeGuard: network access is not expected in resolver tests")
}

func (g *fakeGuard) MaxRedirects() int { return 3 }

type stubResolver struct {
	name  string
	match bool
	res   *core.Resource
	err   error
	calls int
}

func (s *stubResolver) Name() string { return s.name }
func (s *stubResolver) Match(core.Input) bool {
	return s.match
}
func (s *stubResolver) Resolve(core.ResolveContext, core.Input) (*core.Resource, error) {
	s.calls++
	return s.res, s.err
}

func TestRegistryRegistersAndListsNames(t *testing.T) {
	r := NewRegistry()
	if names := r.Names(); len(names) != 0 {
		t.Fatalf("new registry should be empty, got %v", names)
	}
	r.Register(&stubResolver{name: "a"})
	r.Register(&stubResolver{name: "b"})
	got := r.Names()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Names() must preserve registration order, got %v", got)
	}
}

// FR-RSV-01: the first matching resolver wins (built-ins before plugins).
func TestRegistryFirstMatchWins(t *testing.T) {
	first := &stubResolver{name: "first", match: true, res: &core.Resource{ID: "one"}}
	second := &stubResolver{name: "second", match: true, res: &core.Resource{ID: "two"}}
	r := NewRegistry()
	r.Register(first)
	r.Register(second)
	res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{Raw: "https://x/a"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "one" || second.calls != 0 {
		t.Fatalf("expected first resolver to win, got %q (second calls=%d)", res.ID, second.calls)
	}
}

func TestRegistrySkipsNonMatching(t *testing.T) {
	skip := &stubResolver{name: "skip", match: false}
	hit := &stubResolver{name: "hit", match: true, res: &core.Resource{ID: "hit"}}
	r := NewRegistry()
	r.Register(skip)
	r.Register(hit)
	if res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{}); err != nil ||
		res.ID != "hit" {
		t.Fatalf("expected fallthrough to matching resolver, got %v %v", res, err)
	}
	if skip.calls != 0 {
		t.Fatalf("non-matching resolver must not be invoked, calls=%d", skip.calls)
	}
}

func TestRegistryResolveWithoutResolverClassifiesError(t *testing.T) {
	r := NewRegistry()
	_, err := r.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{Kind: core.InputMagnet})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("want E_RESOLVE_FAILED, got %v", err)
	}
}

func TestRegistryPropagatesResolverError(t *testing.T) {
	boom := errors.New("boom")
	r := NewRegistry()
	r.Register(&stubResolver{name: "x", match: true, err: boom})
	if _, err := r.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{}); !errors.Is(err, boom) {
		t.Fatalf("want wrapped resolver error, got %v", err)
	}
}

func TestDirectHTTPMatch(t *testing.T) {
	d := NewDirectHTTP(nil)
	if d.Name() != "direct-http" {
		t.Fatalf("unexpected name %q", d.Name())
	}
	cases := []struct {
		in   core.Input
		want bool
	}{
		{core.Input{Raw: "https://example.com/a", Kind: core.InputURL}, true},
		{core.Input{Raw: "http://example.com/a", Kind: core.InputURL}, true},
		{core.Input{Raw: "HTTPS://example.com/a", Kind: core.InputURL}, true},
		{core.Input{Raw: "ftp://example.com/a", Kind: core.InputURL}, false},
		{core.Input{Raw: "magnet:?xt=urn:btih:abc", Kind: core.InputMagnet}, false},
	}
	for _, tc := range cases {
		if got := d.Match(tc.in); got != tc.want {
			t.Errorf("Match(%q)=%v want %v", tc.in.Raw, got, tc.want)
		}
	}
}

func TestDirectHTTPResolveProducesSingleFileResource(t *testing.T) {
	g := &fakeGuard{}
	d := NewDirectHTTP(g)
	res, err := d.Resolve(core.ResolveContext{Ctx: context.Background(),
		Credential: "mem://c1"}, core.Input{Raw: "https://example.com/files/a.iso"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != core.KindSingle || res.Engine != core.EngineGo {
		t.Fatalf("unexpected kind/engine: %v/%v", res.Kind, res.Engine)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "a.iso" || !res.Files[0].Selected {
		t.Fatalf("unexpected files: %+v", res.Files)
	}
	if len(res.Mirrors) != 1 || res.Mirrors[0].URL != "https://example.com/files/a.iso" ||
		res.Mirrors[0].Priority != 1 || res.Mirrors[0].Weight != 1 {
		t.Fatalf("unexpected mirrors: %+v", res.Mirrors)
	}
	if !strings.HasPrefix(res.ID, "res-") {
		t.Fatalf("resource ID must be prefixed, got %q", res.ID)
	}
	if res.Credential != "mem://c1" {
		t.Fatalf("credential reference must travel to the resource, got %q", res.Credential)
	}
	if len(g.checked) != 1 || g.checked[0] != "https://example.com/files/a.iso" {
		t.Fatalf("SSRF guard must run exactly once, got %v", g.checked)
	}
}

func TestDirectHTTPFilenameFallbacks(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"https://example.com/", "download.bin"},
		// path.Base drops the trailing separator, so a directory URL keeps the
		// last segment; Transport.Probe later overrides it with Content-Disposition.
		{"https://example.com/dir/", "dir"},
		{"https://example.com", "download.bin"},
		{"https://example.com/a%20b.bin", "a b.bin"},
		// %2F unescapes to a separator, which must never survive into a path.
		{"https://example.com/x%2Fy.bin", "xy.bin"},
		{"https://example.com/%00weird.bin", "weird.bin"},
		{"https://example.com/" + strings.Repeat("n", 250) + ".bin", strings.Repeat("n", 196) + ".bin"},
	}
	d := NewDirectHTTP(nil)
	for _, tc := range cases {
		res, err := d.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{Raw: tc.raw})
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		got := res.Files[0].Path
		if got != tc.want {
			t.Errorf("%s: filename=%q want %q", tc.raw, got, tc.want)
		}
		if len(got) > 200 {
			t.Errorf("%s: filename longer than 200 bytes: %d", tc.raw, len(got))
		}
	}
}

func TestDirectHTTPResolvePropagatesGuardBlock(t *testing.T) {
	blocked := core.Err(core.E_RESOLVE_SSRF_BLOCKED, "private address blocked")
	d := NewDirectHTTP(&fakeGuard{err: blocked})
	_, err := d.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "http://169.254.169.254/latest/meta-data/"})
	if core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("want SSRF block, got %v", err)
	}
}

func TestDirectHTTPRejectsUnsupportedScheme(t *testing.T) {
	d := NewDirectHTTP(nil)
	_, err := d.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{Raw: "ftp://example.com/a.bin"})
	if core.AsError(err).Code != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("want E_CAPABILITY_UNAVAILABLE, got %v", err)
	}
}

func TestDirectHTTPRejectsMalformedURL(t *testing.T) {
	d := NewDirectHTTP(nil)
	_, err := d.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{Raw: "https://[::1/a"})
	if core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("want E_INVALID_INPUT, got %v", err)
	}
}

// FR-RSV-02: every unimplemented input class must fail with a phase-aware
// reason instead of silently returning nil.
func TestUnsupportedIsPhaseAware(t *testing.T) {
	u := Unsupported{}
	if u.Name() != "unsupported" || !u.Match(core.Input{Kind: core.InputED2K}) {
		t.Fatalf("unsupported resolver must match everything")
	}
	cases := map[core.InputKind]string{
		core.InputMagnet:   "P1",
		core.InputTorrent:  "P1",
		core.InputSite:     "P2",
		core.InputTelegram: "P3",
		core.InputED2K:     "P3",
		core.InputUnknown:  "后续里程碑",
	}
	for kind, phase := range cases {
		_, err := u.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{Kind: kind})
		e := core.AsError(err)
		if e.Code != core.E_CAPABILITY_UNAVAILABLE {
			t.Errorf("%s: want E_CAPABILITY_UNAVAILABLE, got %v", kind, err)
			continue
		}
		if !strings.Contains(e.Message, phase) {
			t.Errorf("%s: message %q must name phase %q", kind, e.Message, phase)
		}
	}
	// WBS 8 is implemented in P1: reaching Unsupported for these kinds means
	// the daemon forgot to register the built-in resolvers, not "unplanned".
	for _, kind := range []core.InputKind{core.InputMetalink, core.InputThunder} {
		_, err := u.Resolve(core.ResolveContext{Ctx: context.Background()}, core.Input{Kind: kind})
		e := core.AsError(err)
		if e.Code != core.E_RESOLVE_FAILED || !strings.Contains(e.Message, "注册链") {
			t.Errorf("%s: want registration-chain error, got %v", kind, err)
		}
	}
}
