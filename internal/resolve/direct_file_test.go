package resolve

import (
	"context"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

func TestDirectFileMatch(t *testing.T) {
	d := NewDirectFile(nil)
	if d.Name() != "direct-file" {
		t.Fatalf("unexpected name %q", d.Name())
	}
	cases := []struct {
		raw  string
		kind core.InputKind
		want bool
	}{
		{"ftp://example.com/a.iso", core.InputURL, true},
		{"FTP://example.com/a.iso", core.InputURL, true},
		{"ftps://example.com:990/a.iso", core.InputURL, true},
		{"sftp://example.com/dir/a.bin", core.InputURL, true},
		{"http://example.com/a.iso", core.InputURL, false},
		{"magnet:?xt=urn:btih:abc", core.InputMagnet, false},
		{"ftp://example.com/a.iso", core.InputMetalink, false},
	}
	for _, tc := range cases {
		if got := d.Match(core.Input{Raw: tc.raw, Kind: tc.kind}); got != tc.want {
			t.Errorf("Match(%q, %s)=%v want %v", tc.raw, tc.kind, got, tc.want)
		}
	}
}

func TestDirectFileResolvesAllThreeSchemes(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com/pub/a.iso",
		"ftps://example.com:21/pub/a.iso",
		"sftp://example.com/pub/a.iso",
	} {
		t.Run(raw, func(t *testing.T) {
			g := &fakeGuard{}
			d := NewDirectFile(g)
			res, err := d.Resolve(core.ResolveContext{
				Ctx: context.Background(), Credential: "mem://c1",
			}, core.Input{Raw: raw, Kind: core.InputURL})
			if err != nil {
				t.Fatal(err)
			}
			if res.Kind != core.KindSingle || res.Engine != core.EngineGo {
				t.Fatalf("unexpected kind/engine: %v/%v", res.Kind, res.Engine)
			}
			if len(res.Files) != 1 || res.Files[0].Path != "a.iso" || !res.Files[0].Selected {
				t.Fatalf("unexpected files: %+v", res.Files)
			}
			if len(res.Mirrors) != 1 || res.Mirrors[0].URL != raw || res.Mirrors[0].Priority != 1 {
				t.Fatalf("unexpected mirrors: %+v", res.Mirrors)
			}
			if res.Credential != "mem://c1" {
				t.Fatalf("credential ref must travel to the Resource, got %q", res.Credential)
			}
			if len(g.checked) != 1 || g.checked[0] != raw {
				t.Fatalf("SSRF guard must check the file URL exactly once, got %v", g.checked)
			}
		})
	}
}

func TestDirectFileRejectsHazards(t *testing.T) {
	d := NewDirectFile(nil)
	for _, raw := range []string{
		"ftp://user:pass@example.com/a.iso",
		"ftp://example.com/",
		"ftp://example.com",
		"http://example.com/a.iso",
		"ftp://[::1/a",
	} {
		if _, err := d.Resolve(core.ResolveContext{Ctx: context.Background()},
			core.Input{Raw: raw, Kind: core.InputURL}); err == nil {
			t.Errorf("Resolve(%q) should fail", raw)
		}
	}
	if _, err := d.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "ftp://user:p@example.com/a", Kind: core.InputURL}); core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("embedded credentials must be a security error, got %v", err)
	}
}

func TestDirectFilePropagatesGuardBlockAndNilContext(t *testing.T) {
	blocked := core.Err(core.E_RESOLVE_SSRF_BLOCKED, "private address")
	d := NewDirectFile(&fakeGuard{err: blocked})
	_, err := d.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "sftp://169.254.169.254/a", Kind: core.InputURL})
	if core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("want SSRF block, got %v", err)
	}
	// A nil ResolveContext.Ctx must not panic inside the Guard's DNS lookup.
	g := &fakeGuard{}
	d = NewDirectFile(g)
	_, err = d.Resolve(core.ResolveContext{},
		core.Input{Raw: "ftp://example.com/a", Kind: core.InputURL})
	if err != nil {
		t.Fatalf("nil context should fall back to Background: %v", err)
	}
}

func TestParseInputClassifiesFileSchemesAsURL(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com/a", "ftps://example.com/a", "sftp://example.com/a",
	} {
		in, err := core.ParseInput(raw)
		if err != nil || in.Kind != core.InputURL {
			t.Errorf("ParseInput(%q) = %+v, %v", raw, in, err)
		}
	}
	// The whole chain must now resolve a file URL instead of falling through
	// to Unsupported.
	r := NewRegistry()
	r.Register(NewDirectHTTP(nil))
	r.Register(NewDirectFile(nil))
	r.Register(Unsupported{})
	res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "ftp://example.com/pub/a.iso", Kind: core.InputURL})
	if err != nil || !strings.HasSuffix(res.Mirrors[0].URL, "a.iso") {
		t.Fatalf("file URL did not reach DirectFile: %+v %v", res, err)
	}
}
