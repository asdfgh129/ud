package resolve

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// MetalinkResolver implements FR-RSV-04 / ADR-0006: a pure-Go, cgo-free,
// zero-dependency parser for Metalink v3 / v4 (RFC 5854) and Metalink-over-
// HTTP (RFC 6249). It is a security-sensitive built-in resolver: every mirror
// URL goes through the SSRF Guard, and the manifest is bounded in size,
// nesting, mirror count and piece count before a Resource is produced.
//
// It performs NO network I/O (ADR-0006 §4, docs/08 §9). Manifest bytes reach
// it through ResolveContext.Options or a local .meta4/.metalink path; a remote
// manifest URL is rejected with an explicit capability error rather than
// silently fetched by a second download path.
type MetalinkResolver struct {
	guard core.URLGuard
}

func NewMetalinkResolver(g core.URLGuard) *MetalinkResolver {
	return &MetalinkResolver{guard: g}
}

func (m *MetalinkResolver) Name() string { return "metalink" }

func (m *MetalinkResolver) Match(in core.Input) bool {
	if in.Kind == core.InputMetalink {
		return true
	}
	// Inline XML submitted directly (ParseInput classifies it as unknown).
	s := strings.TrimSpace(in.Raw)
	return strings.HasPrefix(s, "<?xml") || strings.HasPrefix(s, "<metalink")
}

func (m *MetalinkResolver) Resolve(rc core.ResolveContext, in core.Input) (*core.Resource, error) {
	lim := defaultMetalinkLimits()
	if v, ok := rc.Options["max_xml_bytes"]; ok {
		if n, ok := asInt64(v); ok && n > 0 && n < lim.maxXMLBytes {
			lim.maxXMLBytes = n
		}
	}
	if v, ok := rc.Options["max_metalink_mirrors"]; ok {
		if n, ok := asInt64(v); ok && n > 0 && int(n) < lim.maxMirrors {
			lim.maxMirrors = int(n)
		}
	}

	doc, err := m.readDocument(rc, in, lim)
	if err != nil {
		return nil, err
	}
	if len(doc.files) != 1 {
		return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
			"multi-file Metalink is not representable in the frozen Resource model (ADR-0013)")
	}
	f := doc.files[0]
	f.name = sanitizeName(f.name)
	if err := validateDocument(&f, lim); err != nil {
		return nil, err
	}

	ctx := rc.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	guard := m.guard
	if guard == nil {
		guard = rc.Guard
	}
	for _, mr := range f.mirrors {
		if err := validateMirrorURL(mr.URL); err != nil {
			return nil, err
		}
		if guard != nil {
			if err := guard.Check(ctx, mr.URL); err != nil {
				return nil, err
			}
		}
	}

	sort.SliceStable(f.mirrors, func(i, j int) bool {
		return f.mirrors[i].Priority < f.mirrors[j].Priority
	})

	res := &core.Resource{
		ID:     "res-" + string(core.NewID()),
		Kind:   core.KindSingle,
		Engine: core.EngineGo,
		Files: []core.ResourceFile{{
			Index: 0, Path: f.name, Size: f.size, Selected: true,
		}},
		Mirrors:    f.mirrors,
		Headers:    map[string]string{},
		Credential: rc.Credential,
		Identity:   core.ProtocolIdentity{},
		Limits:     core.TaskLimits{},
	}
	if f.whole != "" || len(f.pieces) > 0 {
		res.Checksum = &core.Checksum{Algo: f.wholeAlgo, Whole: f.whole, Pieces: f.pieces}
	}
	// Piece hashes pin the chunk boundaries: if the scheduler used a different
	// chunk size every piece digest would be unverifiable. See the merge rule in
	// scheduler.runTask (ADR-0006 安全要求).
	if len(f.pieces) > 0 && f.pieceLength > 0 {
		res.Limits.ChunkSize = f.pieceLength
	}
	return res, nil
}

// readDocument resolves where the manifest bytes come from. Priority:
// explicit Options > inline XML > local file path > explicit capability error.
func (m *MetalinkResolver) readDocument(rc core.ResolveContext, in core.Input, lim metalinkLimits) (*metalinkDoc, error) {
	if v, ok := rc.Options[OptMetalinkHeaders]; ok {
		h, ok := headerSet(v)
		if !ok {
			return nil, core.Err(core.E_INVALID_INPUT, "metalink_headers must be an http.Header or string map")
		}
		base, _ := rc.Options[OptMetalinkBase].(string)
		if base == "" {
			base = in.Raw
		}
		return parseMetalinkHeaders(base, h, lim)
	}
	if v, ok := rc.Options[OptMetalinkXML]; ok {
		data, ok := asBytes(v)
		if !ok {
			return nil, core.Err(core.E_INVALID_INPUT, "metalink_xml must be a string or []byte")
		}
		raw, err := readLimitedXML(bytes.NewReader(data), lim)
		if err != nil {
			return nil, err
		}
		return parseMetalinkXML(raw, lim)
	}

	s := strings.TrimSpace(in.Raw)
	if strings.HasPrefix(s, "<") {
		raw, err := readLimitedXML(strings.NewReader(s), lim)
		if err != nil {
			return nil, err
		}
		return parseMetalinkXML(raw, lim)
	}
	if u, err := url.Parse(s); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
			"fetching a remote Metalink manifest belongs to the Transport layer; supply the manifest via %q or a local .meta4 path", OptMetalinkXML)
	}
	if ext := strings.ToLower(path.Ext(s)); ext != ".meta4" && ext != ".metalink" {
		return nil, core.Err(core.E_INVALID_INPUT, "Metalink manifest must be inline XML, a .meta4/.metalink path, or supplied via Options")
	}
	f, err := os.Open(s)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "open Metalink manifest: %v", err)
	}
	defer f.Close()
	raw, err := readLimitedXML(io.LimitReader(f, lim.maxXMLBytes+1), lim)
	if err != nil {
		return nil, err
	}
	return parseMetalinkXML(raw, lim)
}

func validateDocument(f *metalinkFile, lim metalinkLimits) error {
	if len(f.mirrors) == 0 {
		return core.Err(core.E_INVALID_INPUT, "Metalink <file> has no usable mirror URL")
	}
	if len(f.mirrors) > lim.maxMirrors {
		return core.Err(core.E_INVALID_INPUT, "Metalink mirrors exceed limit %d", lim.maxMirrors)
	}
	// The frozen core.Checksum has a single Algo field, so a manifest that
	// mixes whole-file and piece algorithms cannot be described faithfully.
	// Reject instead of silently dropping one of the two digests.
	algo := f.wholeAlgo
	if len(f.pieces) > 0 {
		if f.pieceAlgo == "" || f.pieceLength <= 0 {
			return core.Err(core.E_INVALID_INPUT, "Metalink <pieces> requires a supported type and a positive length")
		}
		if algo != "" && algo != f.pieceAlgo {
			return core.Err(core.E_INVALID_INPUT,
				"whole-file (%s) and piece (%s) checksums must use the same algorithm", algo, f.pieceAlgo)
		}
		algo = f.pieceAlgo
		if f.size > 0 {
			want := (f.size + f.pieceLength - 1) / f.pieceLength
			if want != int64(len(f.pieces)) {
				return core.Err(core.E_INVALID_INPUT,
					"Metalink declares %d pieces but size/size-per-piece needs %d", len(f.pieces), want)
			}
		}
	}
	f.wholeAlgo, f.pieceAlgo = algo, algo

	seen := map[string]bool{}
	kept := f.mirrors[:0]
	for _, mr := range f.mirrors {
		if seen[mr.URL] || strings.TrimSpace(mr.URL) == "" {
			continue
		}
		seen[mr.URL] = true
		kept = append(kept, mr)
	}
	f.mirrors = kept
	if len(f.mirrors) == 0 {
		return core.Err(core.E_INVALID_INPUT, "Metalink has no distinct mirror URL")
	}
	return nil
}

func validateMirrorURL(raw string) error {
	if len(raw) > 8192 {
		return core.Err(core.E_INVALID_INPUT, "Metalink mirror URL exceeds maximum length")
	}
	if strings.ContainsAny(raw, "\x00\r\n\t ") {
		return core.Err(core.E_INVALID_INPUT, "Metalink mirror URL contains whitespace or control characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "Metalink mirror URL is not an absolute URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "ftps", "sftp":
	default:
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "Metalink mirror scheme %q is not allowed", u.Scheme)
	}
	if u.User != nil {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "credentials embedded in Metalink mirror URL are forbidden")
	}
	return nil
}

func asBytes(v any) ([]byte, bool) {
	switch b := v.(type) {
	case []byte:
		return b, true
	case string:
		return []byte(b), true
	default:
		return nil, false
	}
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
