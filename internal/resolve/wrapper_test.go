package resolve

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

func wrap(t *testing.T, scheme, payload string) string {
	t.Helper()
	return scheme + "://" + base64.StdEncoding.EncodeToString([]byte(payload))
}

func TestWrapperMatchAndName(t *testing.T) {
	w := NewWrapperResolver(nil)
	if w.Name() != "link-wrapper" {
		t.Fatalf("unexpected name %q", w.Name())
	}
	cases := []struct {
		in   core.Input
		want bool
	}{
		{core.Input{Raw: "thunder://QUFodHRwWlo=", Kind: core.InputThunder}, true},
		{core.Input{Raw: "flashget://ABC", Kind: core.InputThunder}, true},
		{core.Input{Raw: "QQDL://ABC", Kind: core.InputThunder}, true},
		{core.Input{Raw: "thunder://ABC", Kind: core.InputURL}, false},
		{core.Input{Raw: "https://example.com/a", Kind: core.InputURL}, false},
		{core.Input{Raw: "magnet:?xt=urn:btih:abc", Kind: core.InputMagnet}, false},
	}
	for _, tc := range cases {
		if got := w.Match(tc.in); got != tc.want {
			t.Errorf("Match(%q, %s)=%v want %v", tc.in.Raw, tc.in.Kind, got, tc.want)
		}
	}
}

// FR-RSV-03 / ADR-0007: all three wrappers decode to one ordinary URL.
func TestWrapperDecodesAllThreeFormats(t *testing.T) {
	const target = "https://example.com/files/demo.iso"
	cases := []struct {
		name string
		raw  string
	}{
		{"thunder", wrap(t, "thunder", "AA"+target+"ZZ")},
		{"thunder-unpadded", "thunder://" + strings.TrimRight(base64.StdEncoding.EncodeToString([]byte("AA"+target+"ZZ")), "=")},
		{"thunder-trailing-slash", wrap(t, "thunder", "AA"+target+"ZZ") + "/"},
		{"flashget", wrap(t, "flashget", flashgetTag+target+flashgetTag)},
		{"flashget-with-port", wrap(t, "flashget", flashgetTag+target+flashgetTag) + "&1234"},
		{"qqdl-bare", "qqdl://" + base64.StdEncoding.EncodeToString([]byte(target))},
		{"qqdl-aa-zz-variant", wrap(t, "qqdl", "AA"+target+"ZZ")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &fakeGuard{}
			w := NewWrapperResolver(g)
			res, err := w.Resolve(core.ResolveContext{Ctx: context.Background()},
				core.Input{Raw: tc.raw, Kind: core.InputThunder})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Mirrors) != 1 || res.Mirrors[0].URL != target {
				t.Fatalf("mirror = %+v, want %q", res.Mirrors, target)
			}
			if res.Kind != core.KindSingle || len(res.Files) != 1 || res.Files[0].Path != "demo.iso" {
				t.Fatalf("unexpected resource: %+v", res)
			}
			if len(g.checked) != 1 || g.checked[0] != target {
				t.Fatalf("decoded URL must be SSRF-checked exactly once, got %v", g.checked)
			}
		})
	}
}

func TestWrapperRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name, raw string
	}{
		{"empty", "thunder://"},
		{"invalid-base64", "thunder://!!!not-base64!!!"},
		{"missing-markers", "thunder://" + base64.StdEncoding.EncodeToString([]byte("https://example.com/a"))},
		{"only-markers", "thunder://" + base64.StdEncoding.EncodeToString([]byte("AAZZ"))},
		{"relative-url", wrap(t, "thunder", "AA/a/b.zipZZ")},
		{"nested-wrapper", wrap(t, "thunder", "AAthunder://QUFodHRwWlo=ZZ")},
		{"javascript-scheme", wrap(t, "thunder", "AAjavascript:alert(1)ZZ")},
		{"file-scheme", wrap(t, "thunder", "AAfile:///etc/passwdZZ")},
		{"embedded-credentials", wrap(t, "thunder", "AAhttps://user:pass@example.com/aZZ")},
		{"control-chars", "thunder://" + base64.StdEncoding.EncodeToString([]byte("AAhttps://example.com/a\nZZ"))},
		{"whitespace-url", wrap(t, "thunder", "AAhttps://example.com/a bZZ")},
		{"flashget-wrong-marker", wrap(t, "flashget", "AAhttps://example.com/aZZ")},
		{"oversized", "thunder://" + strings.Repeat("A", maxWrapperInput+10)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWrapperResolver(nil)
			_, err := w.Resolve(core.ResolveContext{Ctx: context.Background()},
				core.Input{Raw: tc.raw, Kind: core.InputThunder})
			if err == nil {
				t.Fatal("expected a classified rejection")
			}
			e := core.AsError(err)
			if e.Code != core.E_INVALID_INPUT && e.Code != core.E_RESOLVE_SSRF_BLOCKED {
				t.Fatalf("want E_INVALID_INPUT / E_RESOLVE_SSRF_BLOCKED, got %s: %v", e.Code, err)
			}
		})
	}
}

func TestWrapperPropagatesSSRFBlock(t *testing.T) {
	blocked := core.Err(core.E_RESOLVE_SSRF_BLOCKED, "private address blocked")
	w := NewWrapperResolver(&fakeGuard{err: blocked})
	_, err := w.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: wrap(t, "thunder", "AAhttp://169.254.169.254/latest/meta-data/ZZ"),
			Kind: core.InputThunder})
	if core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("want SSRF block, got %v", err)
	}
}

func TestWrapperCarriesCredentialRefAndUsesCtxGuard(t *testing.T) {
	g := &fakeGuard{}
	w := NewWrapperResolver(nil)
	res, err := w.Resolve(core.ResolveContext{Ctx: context.Background(),
		Guard: g, Credential: "mem://c1"},
		core.Input{Raw: wrap(t, "thunder", "AAhttps://example.com/a.binZZ"), Kind: core.InputThunder})
	if err != nil {
		t.Fatal(err)
	}
	if res.Credential != "mem://c1" {
		t.Fatalf("credential ref lost: %q", res.Credential)
	}
	if len(g.checked) != 1 {
		t.Fatalf("ctx.Guard must be used when no constructor guard was given: %v", g.checked)
	}
}

func TestWrapperAcceptsED2KAndFTPAfterDecode(t *testing.T) {
	for _, target := range []string{
		"ftp://example.com/a.iso",
		"ed2k://|file|a.rar|1234|ABCDEF|/",
	} {
		w := NewWrapperResolver(&fakeGuard{})
		res, err := w.Resolve(core.ResolveContext{Ctx: context.Background()},
			core.Input{Raw: wrap(t, "thunder", "AA"+target+"ZZ"), Kind: core.InputThunder})
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if res.Mirrors[0].URL != target {
			t.Fatalf("got %q want %q", res.Mirrors[0].URL, target)
		}
	}
}

func TestDecodeBase64LooseVariants(t *testing.T) {
	want := []byte("AAhttps://example.com/aZZ")
	for _, s := range []string{
		base64.StdEncoding.EncodeToString(want),
		base64.RawStdEncoding.EncodeToString(want),
		base64.URLEncoding.EncodeToString(want),
		base64.RawURLEncoding.EncodeToString(want),
		" " + base64.StdEncoding.EncodeToString(want) + " ",
	} {
		got, err := decodeBase64Loose(s)
		if err != nil || string(got) != string(want) {
			t.Errorf("decodeBase64Loose(%q) = %q, %v", s, got, err)
		}
	}
	if _, err := decodeBase64Loose("%%%%"); err == nil {
		t.Fatal("invalid base64 must be rejected")
	}
}
