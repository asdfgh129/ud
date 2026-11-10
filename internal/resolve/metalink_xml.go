package resolve

import (
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// Hard limits for untrusted Metalink manifests (ADR-0006 安全要求, docs/11 §2).
// The two configurable ones mirror config.SecurityConfig defaults.
const (
	DefaultMaxXMLBytes        int64 = 4 << 20
	DefaultMaxMetalinkMirrors       = 32
	maxXMLTokens                    = 200000
	maxXMLDepth                     = 64
	maxPieceHashes                  = 100000
	maxMetalinkFiles                = 1 // see note in parseMetalinkXML
)

var digestHexLen = map[string]int{
	"md5": 32, "sha-1": 40, "sha-256": 64, "sha-512": 128,
}

type metalinkLimits struct {
	maxXMLBytes int64
	maxMirrors  int
}

func defaultMetalinkLimits() metalinkLimits {
	return metalinkLimits{maxXMLBytes: DefaultMaxXMLBytes, maxMirrors: DefaultMaxMetalinkMirrors}
}

// metalinkDoc is the parser's neutral intermediate form. It deliberately has
// no dependency on a Transport: ADR-0006 §4 only asks the Resolver to DESCRIBE
// the resource, never to fetch it.
type metalinkDoc struct {
	version string // "v4" | "v3"
	files   []metalinkFile
}

type metalinkFile struct {
	name        string
	size        int64
	whole       string
	wholeAlgo   string
	pieceAlgo   string
	pieceLength int64
	pieces      []string
	mirrors     []core.Mirror
}

func normalizeAlgo(s string) string {
	switch strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(s))) {
	case "md5":
		return "md5"
	case "sha1":
		return "sha-1"
	case "sha256":
		return "sha-256"
	case "sha512":
		return "sha-512"
	default:
		return ""
	}
}

// normalizeHexDigest validates a hex digest and lower-cases it. A Metalink
// hash field is never trusted as-is: malformed values must be rejected rather
// than silently dropped, otherwise a forged manifest could bypass integrity
// checks downstream.
func normalizeHexDigest(algo, raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	n, ok := digestHexLen[algo]
	if !ok {
		return "", core.Err(core.E_INVALID_INPUT, "unsupported checksum algorithm %q", algo)
	}
	if len(v) != n {
		return "", core.Err(core.E_INVALID_INPUT, "%s digest must be %d hex chars, got %d", algo, n, len(v))
	}
	if _, err := hex.DecodeString(v); err != nil {
		return "", core.Err(core.E_INVALID_INPUT, "%s digest is not valid hex", algo)
	}
	return v, nil
}

// readLimitedXML enforces the byte cap before any parsing happens.
func readLimitedXML(r io.Reader, lim metalinkLimits) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, lim.maxXMLBytes+1))
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "read Metalink XML: %v", err)
	}
	if int64(len(data)) > lim.maxXMLBytes {
		return nil, core.Err(core.E_INVALID_INPUT, "Metalink XML exceeds %d bytes", lim.maxXMLBytes)
	}
	upper := strings.ToUpper(string(data))
	if strings.Contains(upper, "<!DOCTYPE") || strings.Contains(upper, "<!ENTITY") {
		return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED, "DTD / entity declarations are forbidden in Metalink XML")
	}
	return data, nil
}

// parseMetalinkXML parses RFC 5854 (v4, urn:ietf:params:xml:ns:metalink) and
// the older metalinker.org v3 dialect. Namespace prefixes are ignored on
// purpose: real-world manifests mix v3/v4 prefixes and even omit them.
//
// Known model limit (ADR-0013): the frozen core.Resource has ONE global
// Mirror list and ONE Checksum, with no per-file dimension. A multi-<file>
// manifest therefore cannot be described without silently attaching file A's
// URLs to file B. We reject it explicitly instead of producing a wrong
// Resource; the limitation is recorded in docs/14 §11.
func parseMetalinkXML(data []byte, lim metalinkLimits) (*metalinkDoc, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = true
	// No CharsetReader: only UTF-8 is accepted. A non-UTF-8 declaration is
	// rejected with a classified error rather than guessed at.

	doc := &metalinkDoc{version: "v4"}
	var stack []string
	var cur *metalinkFile
	var text strings.Builder
	var pendingURL *core.Mirror
	var pendingAlgo string
	var sawRoot bool
	tokens := 0
	files := 0
	urls := 0

	parent := func() string {
		if len(stack) < 2 {
			return ""
		}
		return stack[len(stack)-2]
	}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, core.Err(core.E_INVALID_INPUT, "malformed Metalink XML: %v", err)
		}
		tokens++
		if tokens > maxXMLTokens {
			return nil, core.Err(core.E_INVALID_INPUT, "Metalink XML has too many elements")
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED, "DTD / entity declarations are forbidden in Metalink XML")
		case xml.ProcInst:
			if !strings.EqualFold(t.Target, "xml") {
				return nil, core.Err(core.E_INVALID_INPUT, "processing instruction %q is not allowed", t.Target)
			}
		case xml.StartElement:
			name := t.Name.Local
			stack = append(stack, name)
			if len(stack) > maxXMLDepth {
				return nil, core.Err(core.E_INVALID_INPUT, "Metalink XML nesting exceeds %d", maxXMLDepth)
			}
			text.Reset()
			if !sawRoot {
				if name != "metalink" {
					return nil, core.Err(core.E_INVALID_INPUT, "root element must be <metalink>, got <%s>", name)
				}
				sawRoot = true
				for _, a := range t.Attr {
					if a.Name.Local == "version" && strings.HasPrefix(strings.TrimSpace(a.Value), "3") {
						doc.version = "v3"
					}
				}
			}
			switch name {
			case "file":
				files++
				if files > maxMetalinkFiles {
					return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
						"multi-file Metalink is not representable in the frozen Resource model (ADR-0013)")
				}
				f := &metalinkFile{}
				for _, a := range t.Attr {
					if a.Name.Local == "name" {
						f.name = a.Value
					}
				}
				cur = f
				doc.files = append(doc.files, metalinkFile{})
			case "pieces":
				if cur == nil {
					break
				}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "type":
						algo := normalizeAlgo(a.Value)
						if algo == "" {
							return nil, core.Err(core.E_INVALID_INPUT,
								"unsupported Metalink piece checksum algorithm %q", a.Value)
						}
						cur.pieceAlgo = algo
					case "length":
						n, err := strconv.ParseInt(strings.TrimSpace(a.Value), 10, 64)
						if err == nil {
							cur.pieceLength = n
						}
					}
				}
			case "hash":
				pendingAlgo = ""
				explicit, rawAlgo := false, ""
				for _, a := range t.Attr {
					if a.Name.Local == "type" {
						explicit, rawAlgo = true, a.Value
						pendingAlgo = normalizeAlgo(a.Value)
					}
				}
				// Never silently drop a declared algorithm we cannot verify:
				// a forged manifest could otherwise bypass integrity checks.
				if explicit && pendingAlgo == "" {
					return nil, core.Err(core.E_INVALID_INPUT,
						"unsupported Metalink checksum algorithm %q", rawAlgo)
				}
				// RFC 5854 requires @type, but tolerate its omission as
				// SHA-256 rather than discarding the digest entirely.
				if !explicit && parent() != "pieces" {
					pendingAlgo = "sha-256"
				}
			case "url":
				if cur == nil {
					break
				}
				urls++
				if urls > lim.maxMirrors {
					return nil, core.Err(core.E_INVALID_INPUT, "Metalink mirrors exceed limit %d", lim.maxMirrors)
				}
				m := &core.Mirror{Priority: 100, Weight: 1}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "priority":
						if n, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil {
							m.Priority = n
						}
					case "preference": // v3: higher is better, invert onto the v4 scale
						if n, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil {
							m.Priority = 100 - n
						}
					case "location", "geo":
						m.Geo = strings.TrimSpace(a.Value)
					}
				}
				pendingURL = m
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			name := t.Name.Local
			if cur != nil {
				switch name {
				case "file":
					// materialize from the fields accumulated in cur
					doc.files[len(doc.files)-1] = *cur
				case "size":
					if n, err := strconv.ParseInt(strings.TrimSpace(text.String()), 10, 64); err == nil && n >= 0 {
						cur.size = n
					}
				case "name":
					if cur.name == "" && parent() == "file" {
						cur.name = strings.TrimSpace(text.String())
					}
				case "hash":
					algo := pendingAlgo
					if parent() == "pieces" {
						algo = cur.pieceAlgo
						if len(cur.pieces) >= maxPieceHashes {
							return nil, core.Err(core.E_INVALID_INPUT, "Metalink piece hashes exceed limit %d", maxPieceHashes)
						}
						sum, err := normalizeHexDigest(algo, text.String())
						if err != nil {
							return nil, err
						}
						cur.pieces = append(cur.pieces, sum)
					} else if cur.whole == "" && algo != "" {
						sum, err := normalizeHexDigest(algo, text.String())
						if err != nil {
							return nil, err
						}
						cur.whole, cur.wholeAlgo = sum, algo
					}
				case "url":
					if pendingURL != nil {
						pendingURL.URL = strings.TrimSpace(text.String())
						cur.mirrors = append(cur.mirrors, *pendingURL)
						pendingURL = nil
					}
				}
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			text.Reset()
		}
	}
	if !sawRoot {
		return nil, core.Err(core.E_INVALID_INPUT, "empty Metalink document")
	}
	if len(doc.files) == 0 {
		return nil, core.Err(core.E_INVALID_INPUT, "Metalink document has no <file>")
	}
	f := doc.files[0]
	if len(f.mirrors) == 0 && len(f.pieces) == 0 && f.whole == "" {
		return nil, core.Err(core.E_INVALID_INPUT, "Metalink <file> has neither URL nor checksum")
	}
	return doc, nil
}
