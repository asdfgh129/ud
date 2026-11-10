package resolve

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// WrapperResolver implements FR-RSV-03 / ADR-0007: decode the three
// proprietary link wrappers into one ordinary URL, then describe it as a
// single-file Resource. It NEVER implements P2SP, 高速通道 or 离线下载, and it
// performs no network I/O. It is a security-sensitive built-in resolver and
// must be registered before plugins (docs/08 §5).
type WrapperResolver struct {
	guard core.URLGuard
}

func NewWrapperResolver(g core.URLGuard) *WrapperResolver {
	return &WrapperResolver{guard: g}
}

func (w *WrapperResolver) Name() string { return "link-wrapper" }

func (w *WrapperResolver) Match(in core.Input) bool {
	if in.Kind != core.InputThunder {
		return false
	}
	switch core.SchemeOf(in.Raw) {
	case "thunder", "flashget", "qqdl":
		return true
	default:
		return false
	}
}

const (
	maxWrapperInput = 8192 // same ceiling as security.SSRFGuard.Check
	flashgetTag     = "[FLASHGET]"
)

// wrapperSchemes is the post-decode allowlist from ADR-0007 §2. Note that the
// SSRF Guard narrows it further to its configured AllowedSchemes (http/https
// by default); ftp/ed2k wrappers therefore stay blocked until the matching
// Transport exists and the policy is explicitly relaxed.
var wrapperSchemes = map[string]bool{
	"http": true, "https": true, "ftp": true, "ed2k": true,
}

func (w *WrapperResolver) Resolve(rc core.ResolveContext, in core.Input) (*core.Resource, error) {
	raw := strings.TrimSpace(in.Raw)
	if len(raw) == 0 || len(raw) > maxWrapperInput {
		return nil, core.Err(core.E_INVALID_INPUT, "link wrapper length %d is outside 1..%d", len(raw), maxWrapperInput)
	}
	scheme := core.SchemeOf(raw)
	decoded, err := decodeWrapper(scheme, raw[len(scheme)+3:])
	if err != nil {
		return nil, err
	}
	u, err := validateWrapperURL(decoded)
	if err != nil {
		return nil, err
	}

	ctx := rc.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	guard := w.guard
	if guard == nil {
		guard = rc.Guard
	}
	if guard != nil {
		// docs/05 §10: every URL produced by a Resolver is validated at the
		// Resolve() exit. A decoded URL is fully attacker-controlled.
		if err := guard.Check(ctx, decoded); err != nil {
			return nil, err
		}
	}

	return &core.Resource{
		ID:     "res-" + string(core.NewID()),
		Kind:   core.KindSingle,
		Engine: core.EngineGo,
		Files:  []core.ResourceFile{{Index: 0, Path: filenameFromURL(u), Selected: true}},
		Mirrors: []core.Mirror{{
			URL: decoded, Priority: 1, Weight: 1,
		}},
		Headers:    map[string]string{},
		Credential: rc.Credential,
		Identity:   core.ProtocolIdentity{},
		Limits:     core.TaskLimits{},
	}, nil
}

// decodeWrapper unwraps one of the three formats. All of them are base64 of a
// plain URL with an optional marker; no encryption, no P2SP handshake.
func decodeWrapper(scheme, payload string) (string, error) {
	payload = strings.TrimSpace(payload)
	// flashget links sometimes carry a trailing "&<port>" parameter.
	if scheme == "flashget" {
		if i := strings.IndexByte(payload, '&'); i >= 0 {
			payload = payload[:i]
		}
	}
	if payload == "" {
		return "", core.Err(core.E_INVALID_INPUT, "%s link has an empty payload", scheme)
	}
	data, err := decodeBase64Loose(payload)
	if err != nil {
		return "", core.Err(core.E_INVALID_INPUT, "%s link is not valid base64: %v", scheme, err)
	}
	if len(data) == 0 || len(data) > maxWrapperInput {
		return "", core.Err(core.E_INVALID_INPUT, "%s decoded payload has invalid length %d", scheme, len(data))
	}
	s := strings.TrimSpace(string(data))
	if strings.ContainsAny(s, "\x00\r\n\t") {
		return "", core.Err(core.E_INVALID_INPUT, "%s decoded payload contains control characters", scheme)
	}

	switch scheme {
	case "thunder":
		if len(s) <= 4 || !strings.HasPrefix(s, "AA") || !strings.HasSuffix(s, "ZZ") {
			return "", core.Err(core.E_INVALID_INPUT, "thunder link must decode to AA<URL>ZZ")
		}
		return s[2 : len(s)-2], nil
	case "flashget":
		if len(s) <= 2*len(flashgetTag) || !strings.HasPrefix(s, flashgetTag) || !strings.HasSuffix(s, flashgetTag) {
			return "", core.Err(core.E_INVALID_INPUT, "flashget link must decode to [FLASHGET]<URL>[FLASHGET]")
		}
		return s[len(flashgetTag) : len(s)-len(flashgetTag)], nil
	case "qqdl":
		// QQ Xuanfeng stores a bare base64 URL; some generators reuse the
		// thunder AA/ZZ markers, so accept both.
		if len(s) > 4 && strings.HasPrefix(s, "AA") && strings.HasSuffix(s, "ZZ") {
			return s[2 : len(s)-2], nil
		}
		return s, nil
	default:
		return "", core.Err(core.E_INVALID_INPUT, "unsupported link wrapper %q", scheme)
	}
}

// decodeBase64Loose tolerates missing padding, URL-safe alphabets and a
// trailing slash (all seen in the wild) without ever accepting invalid data.
func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	candidates := []string{s}
	if strings.HasSuffix(s, "/") {
		candidates = append(candidates, strings.TrimRight(s, "/"))
	}
	var lastErr error
	for _, c := range candidates {
		for _, enc := range encodings {
			b, err := enc.DecodeString(c)
			if err == nil {
				return b, nil
			}
			lastErr = err
		}
	}
	return nil, lastErr
}

func validateWrapperURL(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > maxWrapperInput {
		return nil, core.Err(core.E_INVALID_INPUT, "decoded URL has invalid length %d", len(raw))
	}
	if strings.ContainsAny(raw, "\x00\r\n\t ") {
		return nil, core.Err(core.E_INVALID_INPUT, "decoded URL contains whitespace or control characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		// ed2k://|file|... has no authority; handled separately below.
		if strings.HasPrefix(strings.ToLower(raw), "ed2k://|file|") {
			return &url.URL{Scheme: "ed2k", Opaque: raw[len("ed2k://"):]}, nil
		}
		return nil, core.Err(core.E_INVALID_INPUT, "decoded payload is not an absolute URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if !wrapperSchemes[scheme] {
		return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED, "decoded scheme %q is not allowed", scheme)
	}
	if u.User != nil {
		return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED, "credentials embedded in decoded URL are forbidden")
	}
	return u, nil
}
