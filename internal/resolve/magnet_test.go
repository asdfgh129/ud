package resolve

import (
	"context"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

// mustResolveMagnet parses a well-formed magnet and asserts a KindTorrent Resource.
func TestMagnetResolverResolve(t *testing.T) {
	r := NewMagnetResolver(nil)
	const ih = "0123456789abcdef0123456789abcdef01234567" // 40 hex chars (SHA-1)
	raw := "magnet:?xt=urn:btih:" + ih +
		"&dn=example.iso" +
		"&tr=udp://tracker.example.com:80" +
		"&tr=udp://tracker2.example.com:80"

	res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: raw, Kind: core.InputMagnet})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Kind != core.KindTorrent {
		t.Fatalf("kind = %v, want torrent", res.Kind)
	}
	if res.Identity.InfoHashV1 != ih {
		t.Fatalf("infohash = %q, want %q", res.Identity.InfoHashV1, ih)
	}
	// magnet trackers collapse into a single announce tier.
	if len(res.Identity.Trackers) != 1 {
		t.Fatalf("tracker tiers = %d, want 1", len(res.Identity.Trackers))
	}
	if got := res.Identity.Trackers[0]; len(got) != 2 ||
		got[0] != "udp://tracker.example.com:80" || got[1] != "udp://tracker2.example.com:80" {
		t.Fatalf("trackers[0] = %v", got)
	}
	// display name becomes the output filename.
	if res.Files[0].Path != "example.iso" || !res.Files[0].Selected {
		t.Fatalf("files = %+v", res.Files)
	}
	// BT has no HTTP-style mirrors; the Scheduler tolerates an empty list.
	if len(res.Mirrors) != 0 {
		t.Fatalf("BT must leave Mirrors empty, got %d", len(res.Mirrors))
	}
}

// Missing xt (infohash) must be a classified invalid-input error, not a silent nil.
func TestMagnetResolverMissingInfohash(t *testing.T) {
	r := NewMagnetResolver(nil)
	_, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "magnet:?dn=no-infohash.iso", Kind: core.InputMagnet})
	if err == nil {
		t.Fatal("expected error for magnet without xt")
	}
	if e := core.AsError(err); e == nil || e.Code != core.E_INVALID_INPUT {
		t.Fatalf("expected E_INVALID_INPUT, got %v", err)
	}
}

// Match must fire on the magnet: scheme regardless of the declared InputKind,
// and must not fire on unrelated inputs.
func TestMagnetResolverMatch(t *testing.T) {
	r := NewMagnetResolver(nil)
	cases := []struct {
		raw  string
		kind core.InputKind
		want bool
	}{
		{"magnet:?xt=urn:btih:" + "0123456789abcdef0123456789abcdef01234567", core.InputMagnet, true},
		{"MAGNET:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", core.InputURL, true}, // scheme match wins
		{"https://example.com/a.torrent", core.InputURL, false},
		{"d8:announce3:abc4:info...", core.InputURL, false},
		{"", core.InputURL, false},
	}
	for _, c := range cases {
		if got := r.Match(core.Input{Raw: c.raw, Kind: c.kind}); got != c.want {
			t.Errorf("Match(%q,%v) = %v, want %v", c.raw, c.kind, got, c.want)
		}
	}
}
