package resolve

import (
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// RFC 6249 (Metalink/HTTP) describes mirrors and digests in HTTP response
// headers. The Resolver never performs the HTTP request itself (ADR-0006 §4:
// no transport implementation); the headers are handed in through
// ResolveContext.Options by the caller / future Transport integration.
const (
	OptMetalinkXML     = "metalink_xml"
	OptMetalinkHeaders = "metalink_headers"
	OptMetalinkBase    = "metalink_base"
)

// headerSet normalises the option value into an http.Header. Accepting the
// plain map forms keeps the Resolver usable from JSON-driven callers.
func headerSet(v any) (http.Header, bool) {
	switch h := v.(type) {
	case http.Header:
		return h, true
	case map[string][]string:
		return http.Header(h), true
	case map[string]string:
		out := http.Header{}
		for k, val := range h {
			out.Set(k, val)
		}
		return out, true
	default:
		return nil, false
	}
}

// digestStrength ranks algorithms so a multi-Digest response picks the
// strongest one representable by the frozen core.Checksum (single Algo).
var digestStrength = map[string]int{"md5": 1, "sha-1": 2, "sha-256": 3, "sha-512": 4}

// parseMetalinkHeaders turns Link/Digest/Content-* headers into one file.
func parseMetalinkHeaders(base string, h http.Header, lim metalinkLimits) (*metalinkDoc, error) {
	if strings.TrimSpace(base) == "" {
		return nil, core.Err(core.E_INVALID_INPUT, "Metalink-HTTP requires a base URL")
	}
	f := &metalinkFile{name: filenameFromRaw(base)}
	if n, err := strconv.ParseInt(strings.TrimSpace(h.Get("Content-Length")), 10, 64); err == nil && n >= 0 {
		f.size = n
	}
	if cd := h.Get("Content-Disposition"); cd != "" {
		if name := dispositionFilename(cd); name != "" {
			f.name = sanitizeName(name)
		}
	}

	// The requested URL is always a mirror (RFC 6249 §3). Its priority is 0 so
	// it wins over duplicates unless a duplicate explicitly says otherwise.
	seen := map[string]bool{}
	add := func(raw string, pri int, geo string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			return
		}
		seen[raw] = true
		f.mirrors = append(f.mirrors, core.Mirror{URL: raw, Priority: pri, Geo: geo, Weight: 1})
	}
	add(base, 0, "")
	for _, line := range h.Values("Link") {
		for _, seg := range splitLinkValues(line) {
			target, params := parseLinkValue(seg)
			if target == "" || !hasRelation(params["rel"], "duplicate") {
				continue
			}
			pri := 100
			if v, err := strconv.Atoi(strings.Trim(strings.TrimSpace(params["pri"]), `"`)); err == nil {
				// Project-wide convention: Mirror.Priority is an ascending
				// rank, so the Link pri value is mapped onto the same numeric
				// scale as RFC 5854 <url priority> and left unmodified.
				pri = v
			}
			add(target, pri, strings.Trim(strings.TrimSpace(params["geo"]), `"`))
		}
	}
	if len(f.mirrors) > lim.maxMirrors {
		return nil, core.Err(core.E_INVALID_INPUT, "Metalink mirrors exceed limit %d", lim.maxMirrors)
	}

	// Digest: SHA-256=<base64> (RFC 3230) or sha-256=:base64: (RFC 9530).
	// Multiple algorithms are common; pick the strongest supported one and
	// convert to the lowercase hex used by Metalink XML and core.Checksum.
	bestAlgo, bestSum, bestRank := "", "", 0
	digestLines := make([]string, 0, 2)
	digestLines = append(digestLines, h.Values("Digest")...)
	digestLines = append(digestLines, h.Values("Content-Digest")...)
	for _, line := range digestLines {
		for _, item := range splitHeaderList(line) {
			k, v, ok := strings.Cut(item, "=")
			if !ok {
				continue
			}
			algo := normalizeAlgo(k)
			rank := digestStrength[algo]
			if rank == 0 || rank <= bestRank {
				continue
			}
			sum, err := decodeDigestValue(algo, v)
			if err != nil {
				return nil, err
			}
			bestAlgo, bestSum, bestRank = algo, sum, rank
		}
	}
	if bestAlgo != "" {
		f.whole, f.wholeAlgo = bestSum, bestAlgo
	}
	return &metalinkDoc{version: "rfc6249", files: []metalinkFile{*f}}, nil
}

func hasRelation(rel, want string) bool {
	for _, tok := range strings.Fields(strings.Trim(strings.TrimSpace(rel), `"`)) {
		if strings.EqualFold(tok, want) {
			return true
		}
	}
	return false
}

// splitLinkValues splits a Link header on commas that are outside <...> and
// outside quoted strings, because a URI may legally contain a comma.
func splitLinkValues(line string) []string {
	var out []string
	var start int
	inAngle, inQuote := false, false
	for i, r := range line {
		switch r {
		case '<':
			inAngle = true
		case '>':
			inAngle = false
		case '"':
			if !inAngle {
				inQuote = !inQuote
			}
		case ',':
			if !inAngle && !inQuote {
				out = append(out, line[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, line[start:])
	return out
}

func parseLinkValue(seg string) (string, map[string]string) {
	params := map[string]string{}
	open := strings.IndexByte(seg, '<')
	close := strings.IndexByte(seg, '>')
	if open < 0 || close <= open {
		return "", params
	}
	target := strings.TrimSpace(seg[open+1 : close])
	for _, part := range strings.Split(seg[close+1:], ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			params[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return target, params
}

// splitHeaderList splits on commas but keeps base64 values intact (base64 has
// no comma, while Digest lists use commas between members).
func splitHeaderList(line string) []string {
	var out []string
	for _, s := range strings.Split(line, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func decodeDigestValue(algo, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	v = strings.Trim(v, `"`)
	// RFC 9530 structured field byte sequence: :base64:
	structured := strings.HasPrefix(v, ":") && strings.HasSuffix(v, ":") && len(v) > 2
	if structured {
		v = v[1 : len(v)-1]
	} else if sum, err := normalizeHexDigest(algo, v); err == nil {
		// Try the unambiguous hex form FIRST. A 64-char hex SHA-256 is also
		// valid base64 (it would decode to 48 bytes), so attempting base64
		// first silently corrupts servers that emit hex directly.
		return sum, nil
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		if b, err := enc.DecodeString(v); err == nil {
			return normalizeHexDigest(algo, hex.EncodeToString(b))
		}
	}
	// Re-run on the original value to surface the classified error.
	return normalizeHexDigest(algo, v)
}

func dispositionFilename(cd string) string {
	for _, part := range strings.Split(cd, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "filename") {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}
