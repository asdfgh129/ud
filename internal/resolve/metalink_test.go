package resolve

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func base64Of(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func metalinkV4(name string, size int64, pieces int, pieceLen int64) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<metalink xmlns="urn:ietf:params:xml:ns:metalink">`)
	fmt.Fprintf(&b, `<file name=%q><size>%d</size>`, name, size)
	b.WriteString(`<hash type="sha-256">` + shaA + `</hash>`)
	fmt.Fprintf(&b, `<pieces type="sha-256" length="%d">`, pieceLen)
	for i := 0; i < pieces; i++ {
		fmt.Fprintf(&b, `<hash>%s</hash>`, shaB)
	}
	b.WriteString(`</pieces>`)
	b.WriteString(`<url location="de" priority="3">https://mirror-de.example.com/a.iso</url>`)
	b.WriteString(`<url location="us" priority="1" type="http">https://mirror-us.example.com/a.iso</url>`)
	b.WriteString(`<url location="jp" priority="2">https://mirror-jp.example.com/a.iso</url>`)
	b.WriteString(`<metaurl mediatype="torrent">https://meta.example.com/a.torrent</metaurl>`)
	b.WriteString(`</file></metalink>`)
	return b.String()
}

func metalinkV3(name string, size int64, pieces int, pieceLen int64) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?>`)
	b.WriteString(`<metalink version="3.0" xmlns="http://www.metalinker.org/">`)
	b.WriteString(`<files>`)
	fmt.Fprintf(&b, `<file name=%q><size>%d</size>`, name, size)
	b.WriteString(`<verification>`)
	b.WriteString(`<hash type="sha1">` + strings.Repeat("a", 40) + `</hash>`)
	fmt.Fprintf(&b, `<pieces type="sha1" length="%d">`, pieceLen)
	for i := 0; i < pieces; i++ {
		fmt.Fprintf(&b, `<hash>%s</hash>`, strings.Repeat("b", 40))
	}
	b.WriteString(`</pieces></verification><resources>`)
	b.WriteString(`<url type="http" location="us" preference="90">https://v3-us.example.com/a.iso</url>`)
	b.WriteString(`<url type="http" location="de" preference="50">https://v3-de.example.com/a.iso</url>`)
	b.WriteString(`</resources></file></files></metalink>`)
	return b.String()
}

func resolveInlineWith(t *testing.T, xmlText string, opt map[string]any, g core.URLGuard) (*core.Resource, error) {
	t.Helper()
	if opt == nil {
		opt = map[string]any{}
	}
	opt[OptMetalinkXML] = xmlText
	return NewMetalinkResolver(g).Resolve(
		core.ResolveContext{Ctx: context.Background(), Credential: "mem://c1", Options: opt},
		core.Input{Kind: core.InputMetalink, Raw: "inline.meta4"})
}

func resolveInline(t *testing.T, xmlText string, opt map[string]any) (*core.Resource, error) {
	t.Helper()
	return resolveInlineWith(t, xmlText, opt, &fakeGuard{})
}

func TestMetalinkMatchAndName(t *testing.T) {
	m := NewMetalinkResolver(nil)
	if m.Name() != "metalink" {
		t.Fatalf("unexpected name %q", m.Name())
	}
	if !m.Match(core.Input{Raw: "/tmp/a.meta4", Kind: core.InputMetalink}) {
		t.Fatal("InputMetalink must match")
	}
	if !m.Match(core.Input{Raw: "<?xml version=\"1.0\"?><metalink/>", Kind: core.InputUnknown}) {
		t.Fatal("inline XML must match")
	}
	if m.Match(core.Input{Raw: "https://example.com/a.iso", Kind: core.InputURL}) {
		t.Fatal("ordinary URL must not be captured by Metalink resolver")
	}
}

// FR-RSV-04 / ADR-0006: v4 manifest -> multi-mirror, sorted, with checksums.
func TestMetalinkV4ProducesCompleteResource(t *testing.T) {
	res, err := resolveInline(t, metalinkV4("../../etc/demo.iso", 12, 3, 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != core.KindSingle || res.Engine != core.EngineGo {
		t.Fatalf("unexpected kind/engine: %+v", res)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "demo.iso" || res.Files[0].Size != 12 {
		t.Fatalf("traversal must be stripped and size kept: %+v", res.Files)
	}
	if len(res.Mirrors) != 3 {
		t.Fatalf("want 3 mirrors (metaurl excluded), got %+v", res.Mirrors)
	}
	for i, want := range []string{"us", "jp", "de"} {
		if res.Mirrors[i].Geo != want || res.Mirrors[i].Priority != i+1 {
			t.Fatalf("mirror %d not sorted by priority/geo: %+v", i, res.Mirrors[i])
		}
	}
	if res.Checksum == nil || res.Checksum.Algo != "sha-256" ||
		res.Checksum.Whole != shaA || len(res.Checksum.Pieces) != 3 {
		t.Fatalf("checksum not carried: %+v", res.Checksum)
	}
	if res.Limits.ChunkSize != 4 {
		t.Fatalf("piece length must pin ChunkSize, got %d", res.Limits.ChunkSize)
	}
	if res.Credential != "mem://c1" || !strings.HasPrefix(res.ID, "res-") {
		t.Fatalf("missing credential/id: %+v", res)
	}
}

func TestMetalinkV3Dialect(t *testing.T) {
	res, err := resolveInline(t, metalinkV3("v3.iso", 8, 2, 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checksum == nil || res.Checksum.Algo != "sha-1" || len(res.Checksum.Pieces) != 2 {
		t.Fatalf("v3 checksums: %+v", res.Checksum)
	}
	if len(res.Mirrors) != 2 || res.Mirrors[0].Geo != "us" || res.Mirrors[0].Priority != 10 ||
		res.Mirrors[1].Priority != 50 {
		t.Fatalf("v3 preference must invert into priority: %+v", res.Mirrors)
	}
	if res.Files[0].Path != "v3.iso" {
		t.Fatalf("v3 filename: %+v", res.Files)
	}
}

func TestMetalinkXMLSecurityLimits(t *testing.T) {
	longURL := "https://example.com/" + strings.Repeat("a", 9000)
	manyMirrors := `<metalink><file name="a"><size>4</size>` +
		strings.Repeat(`<url priority="1">https://example.com/a</url>`, 33) + `</file></metalink>`
	cases := []struct {
		name  string
		xml   string
		code  string
		guard core.URLGuard // nil -> permissive fakeGuard
	}{
		{"doctype", `<!DOCTYPE metalink [<!ENTITY x "boom">]><metalink><file name="a"><url>https://e.com/a</url></file></metalink>`, core.E_RESOLVE_SSRF_BLOCKED, nil},
		{"entity", `<!ENTITY x "boom"><metalink><file name="a"><url>https://e.com/a</url></file></metalink>`, core.E_RESOLVE_SSRF_BLOCKED, nil},
		{"wrong-root", `<foo><file name="a"/></foo>`, core.E_INVALID_INPUT, nil},
		{"empty-file", `<metalink></metalink>`, core.E_INVALID_INPUT, nil},
		{"no-url-or-hash", `<metalink><file name="a"/></metalink>`, core.E_INVALID_INPUT, nil},
		{"multi-file", `<metalink><file name="a"><url>https://e.com/a</url></file><file name="b"><url>https://e.com/b</url></file></metalink>`, core.E_CAPABILITY_UNAVAILABLE, nil},
		{"too-many-mirrors", manyMirrors, core.E_INVALID_INPUT, nil},
		// Private-network policy belongs to the SSRF Guard (configurable via
		// BlockPrivateNetworks), not to the Resolver. Use the real guard here
		// to prove the Resolver actually hands the untrusted mirror to it.
		{"private-url", `<metalink><file name="a"><url priority="1">http://169.254.169.254/latest/meta-data/</url></file></metalink>`, core.E_RESOLVE_SSRF_BLOCKED, security.NewDevGuard()},
		{"bad-scheme", `<metalink><file name="a"><url priority="1">file:///etc/passwd</url></file></metalink>`, core.E_RESOLVE_SSRF_BLOCKED, nil},
		{"embedded-credentials", `<metalink><file name="a"><url>https://u:p@example.com/a</url></file></metalink>`, core.E_RESOLVE_SSRF_BLOCKED, nil},
		{"overlong-url", `<metalink><file name="a"><url>` + longURL + `</url></file></metalink>`, core.E_INVALID_INPUT, nil},
		{"malformed-xml", `<metalink><file name="a"><url>https://e.com/a</file></metalink>`, core.E_INVALID_INPUT, nil},
		{"mixed-hash-algos", `<metalink><file name="a"><size>4</size><hash type="md5">` + strings.Repeat("a", 32) + `</hash><pieces type="sha-256" length="4"><hash>` + shaB + `</hash></pieces><url>https://e.com/a</url></file></metalink>`, core.E_INVALID_INPUT, nil},
		{"piece-count-mismatch", `<metalink><file name="a"><size>12</size><pieces type="sha-256" length="4"><hash>` + shaB + `</hash></pieces><url>https://e.com/a</url></file></metalink>`, core.E_INVALID_INPUT, nil},
		{"bad-hex", `<metalink><file name="a"><hash type="sha-256">zzzz</hash><url>https://e.com/a</url></file></metalink>`, core.E_INVALID_INPUT, nil},
		{"unknown-algo", `<metalink><file name="a"><hash type="crc32">abcd</hash><url>https://e.com/a</url></file></metalink>`, core.E_INVALID_INPUT, nil},
		{"pieces-without-length", `<metalink><file name="a"><pieces type="sha-256"><hash>` + shaB + `</hash></pieces><url>https://e.com/a</url></file></metalink>`, core.E_INVALID_INPUT, nil},
		{"stylesheet-pi", `<?xml version="1.0"?><?xml-stylesheet href="http://evil/x.xsl"?><metalink><file name="a"><url>https://e.com/a</url></file></metalink>`, core.E_INVALID_INPUT, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := core.URLGuard(&fakeGuard{})
			if tc.guard != nil {
				g = tc.guard
			}
			_, err := resolveInlineWith(t, tc.xml, nil, g)
			if err == nil {
				t.Fatal("expected classified rejection")
			}
			if got := core.AsError(err).Code; got != tc.code {
				t.Fatalf("want %s, got %s: %v", tc.code, got, err)
			}
		})
	}
}

func TestMetalinkXMLLimitsAreEnforced(t *testing.T) {
	// 4MiB cap: generate a manifest just above the default limit.
	huge := `<metalink><file name="a"><url>https://e.com/a</url><size>1</size>` +
		strings.Repeat("x", int(DefaultMaxXMLBytes)+10) + `</file></metalink>`
	_, err := resolveInline(t, huge, nil)
	if err == nil || core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("oversized XML must be rejected, got %v", err)
	}
	// Configurable tighter cap.
	_, err = resolveInline(t, metalinkV4("a.iso", 4, 1, 4),
		map[string]any{"max_xml_bytes": 64})
	if err == nil || core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("tighter cap must apply, got %v", err)
	}
	// Configurable tighter mirror cap.
	_, err = resolveInline(t, metalinkV4("a.iso", 4, 1, 4),
		map[string]any{"max_metalink_mirrors": 1})
	if err == nil || core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("tighter mirror cap must apply, got %v", err)
	}
}

func TestMetalinkDuplicateMirrorsAreDeduplicated(t *testing.T) {
	doc := `<metalink><file name="a.iso"><size>4</size>` +
		`<url priority="1">https://e.com/a</url><url priority="2">https://e.com/a</url>` +
		`<url priority="3"></url></file></metalink>`
	res, err := resolveInline(t, doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mirrors) != 1 {
		t.Fatalf("duplicate/empty mirrors must be dropped: %+v", res.Mirrors)
	}
}

func TestMetalinkEveryURLPassesSSRFGuard(t *testing.T) {
	g := &fakeGuard{}
	m := NewMetalinkResolver(g)
	_, err := m.Resolve(core.ResolveContext{Ctx: context.Background(),
		Options: map[string]any{OptMetalinkXML: metalinkV4("a.iso", 4, 1, 4)}},
		core.Input{Kind: core.InputMetalink})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.checked) != 3 {
		t.Fatalf("all 3 mirrors must be checked, got %v", g.checked)
	}
	blocked := core.Err(core.E_RESOLVE_SSRF_BLOCKED, "blocked")
	m2 := NewMetalinkResolver(&fakeGuard{err: blocked})
	_, err = m2.Resolve(core.ResolveContext{Ctx: context.Background(),
		Options: map[string]any{OptMetalinkXML: metalinkV4("a.iso", 4, 1, 4)}},
		core.Input{Kind: core.InputMetalink})
	if core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("guard rejection must propagate, got %v", err)
	}
}

func TestMetalinkInlineXMLAndLocalFile(t *testing.T) {
	inline := metalinkV4("inline.iso", 4, 1, 4)
	res, err := NewMetalinkResolver(&fakeGuard{}).Resolve(
		core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: inline, Kind: core.InputUnknown})
	if err != nil || res.Files[0].Path != "inline.iso" {
		t.Fatalf("inline XML: %v %+v", err, res)
	}
	// Local .meta4 path.
	p := t.TempDir() + "/sample.meta4"
	if err := writeFile(p, inline); err != nil {
		t.Fatal(err)
	}
	res, err = NewMetalinkResolver(&fakeGuard{}).Resolve(
		core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: p, Kind: core.InputMetalink})
	if err != nil || res.Files[0].Path != "inline.iso" {
		t.Fatalf("local manifest: %v %+v", err, res)
	}
	// Missing local file and wrong extension.
	for _, raw := range []string{p + ".missing", "/tmp/not-a-manifest.txt"} {
		_, err = NewMetalinkResolver(&fakeGuard{}).Resolve(
			core.ResolveContext{Ctx: context.Background()},
			core.Input{Raw: raw, Kind: core.InputMetalink})
		if err == nil || core.AsError(err).Code != core.E_INVALID_INPUT {
			t.Fatalf("%q must be rejected: %v", raw, err)
		}
	}
}

func TestMetalinkRemoteManifestIsExplicitlyNotFetched(t *testing.T) {
	_, err := NewMetalinkResolver(nil).Resolve(
		core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "https://example.com/a.meta4", Kind: core.InputMetalink})
	if err == nil || core.AsError(err).Code != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("remote manifest must not be silently fetched: %v", err)
	}
}

// RFC 6249: Link: rel=duplicate, Digest, Content-Disposition, Content-Length.
func TestMetalinkRFC6249Headers(t *testing.T) {
	payload := []byte("0123456789abcdef")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	h := http.Header{}
	h.Add("Link", `<https://mirror1.example.com/a.iso>; rel=duplicate; pri=1; geo=us`)
	h.Add("Link", `<https://mirror2.example.com/a.iso>; rel="duplicate copyright"; pri=2; geo=de, <https://ignored.example.com/x>; rel=describedby`)
	h.Set("Digest", `MD5=AAAAAAAAAAAAAAAAAAAAAA==, SHA-256=`+base64Of(sum[:]))
	h.Set("Content-Disposition", `attachment; filename="report.iso"`)
	h.Set("Content-Length", "16")

	res, err := NewMetalinkResolver(&fakeGuard{}).Resolve(
		core.ResolveContext{Ctx: context.Background(), Options: map[string]any{
			OptMetalinkHeaders: h,
			OptMetalinkBase:    "https://origin.example.com/a.iso",
		}}, core.Input{Kind: core.InputMetalink})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mirrors) != 3 || res.Mirrors[0].URL != "https://origin.example.com/a.iso" ||
		res.Mirrors[1].Geo != "us" || res.Mirrors[2].Priority != 2 {
		t.Fatalf("RFC 6249 mirrors: %+v", res.Mirrors)
	}
	if res.Files[0].Path != "report.iso" || res.Files[0].Size != 16 {
		t.Fatalf("headers-derived file: %+v", res.Files)
	}
	if res.Checksum == nil || res.Checksum.Algo != "sha-256" || res.Checksum.Whole != digest {
		t.Fatalf("strongest digest must win: %+v", res.Checksum)
	}
	if res.Limits.ChunkSize != 0 || len(res.Checksum.Pieces) != 0 {
		t.Fatal("RFC 6249 declares no pieces; ChunkSize must stay unset")
	}
}

func TestMetalinkRFC6249StructuredDigestAndFallback(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"rfc9530", `sha-256=:` + base64Of(make([]byte, 32)) + `:`},
		{"hex", "sha-256=" + shaA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Content-Digest", tc.value)
			res, err := NewMetalinkResolver(nil).Resolve(
				core.ResolveContext{Ctx: context.Background(), Options: map[string]any{
					OptMetalinkHeaders: h, OptMetalinkBase: "https://e.com/a.iso",
				}}, core.Input{Kind: core.InputMetalink})
			if err != nil {
				t.Fatal(err)
			}
			if res.Checksum == nil || res.Checksum.Algo != "sha-256" {
				t.Fatalf("digest: %+v", res.Checksum)
			}
		})
	}
}

func TestMetalinkOptionValidation(t *testing.T) {
	m := NewMetalinkResolver(nil)
	for _, opt := range []map[string]any{
		{OptMetalinkXML: 123},
		{OptMetalinkHeaders: "bad"},
	} {
		_, err := m.Resolve(core.ResolveContext{Ctx: context.Background(), Options: opt},
			core.Input{Kind: core.InputMetalink})
		if err == nil || core.AsError(err).Code != core.E_INVALID_INPUT {
			t.Fatalf("invalid options must be rejected: %v", err)
		}
	}
	// map[string]string header form is accepted.
	res, err := m.Resolve(core.ResolveContext{Ctx: context.Background(), Options: map[string]any{
		OptMetalinkHeaders: map[string]string{"Link": `<https://m.example.com/a.iso>; rel=duplicate; pri=1`},
		OptMetalinkBase:    "https://e.com/a.iso",
	}}, core.Input{Kind: core.InputMetalink})
	if err != nil || len(res.Mirrors) != 2 {
		t.Fatalf("string map header form: %v %+v", err, res)
	}
}

func TestHeaderHelpers(t *testing.T) {
	segs := splitLinkValues(`<https://a.example/x,y>; rel="duplicate"; pri=1, <https://b.example/z>; rel=duplicate`)
	if len(segs) != 2 {
		t.Fatalf("comma inside <> must not split: %q", segs)
	}
	target, params := parseLinkValue(segs[0])
	if target != "https://a.example/x,y" || !hasRelation(params["rel"], "duplicate") {
		t.Fatalf("link parse: %q %+v", target, params)
	}
	if _, ok := headerSet(map[string][]string{"Link": {"x"}}); !ok {
		t.Fatal("map[string][]string must be accepted")
	}
	if _, err := decodeDigestValue("sha-256", "not-valid-hex!"); err == nil {
		t.Fatal("invalid digest must be rejected")
	}
	_ = xml.Name{}
	_ = shaC
}
