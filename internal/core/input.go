package core

import (
	"net/url"
	"strings"
)

// Input is the classified, still-untrusted user submission.
// Raw is redacted by security.RedactURL before it is persisted (docs/06 §6).
type Input struct {
	Raw           string        `json:"raw"`
	Kind          InputKind     `json:"kind"`
	CredentialRef CredentialRef `json:"credentialRef,omitempty"`
}

// ParseInput performs the FR-RSV-02 classification. It never performs network
// I/O and never validates reachability; that is the SSRF Guard's job.
func ParseInput(raw string) (Input, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Input{}, Err(E_INVALID_INPUT, "empty input")
	}
	if strings.ContainsAny(s, "\x00\r\n") {
		return Input{}, Err(E_INVALID_INPUT, "input contains control characters")
	}
	// ed2k://|file|name|size|hash|/ is not a valid RFC 3986 URI (pipes in the
	// authority), so net/url rejects it. Classify it before parsing.
	if strings.HasPrefix(strings.ToLower(s), "ed2k://") {
		return Input{Raw: s, Kind: InputED2K}, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return Input{}, Err(E_INVALID_INPUT, "malformed input: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)

	switch scheme {
	case "http", "https", "ftp", "ftps", "sftp":
		// t.me message links are a content source, not a plain direct link.
		if scheme == "https" && strings.EqualFold(u.Host, "t.me") {
			return Input{Raw: s, Kind: InputTelegram}, nil
		}
		return Input{Raw: s, Kind: InputURL}, nil
	case "magnet":
		return Input{Raw: s, Kind: InputMagnet}, nil
	case "ed2k":
		return Input{Raw: s, Kind: InputED2K}, nil
	case "thunder", "flashget", "qqdl":
		return Input{Raw: s, Kind: InputThunder}, nil
	case "tg":
		return Input{Raw: s, Kind: InputTelegram}, nil
	case "file", "gopher", "dict", "ldap":
		return Input{}, Err(E_RESOLVE_SSRF_BLOCKED, "scheme %q is not allowed", scheme)
	case "":
		// Bare domain / path from clipboard or a browser extension.
		lower := strings.ToLower(s)
		switch {
		case strings.HasSuffix(lower, ".torrent"), strings.HasPrefix(s, "d8:announce"):
			return Input{Raw: s, Kind: InputTorrent}, nil
		case strings.HasSuffix(lower, ".metalink"), strings.HasSuffix(lower, ".meta4"):
			return Input{Raw: s, Kind: InputMetalink}, nil
		case strings.Contains(s, ".") && !strings.ContainsAny(s, " \t"):
			return Input{Raw: "https://" + s, Kind: InputURL}, nil
		default:
			return Input{Raw: s, Kind: InputUnknown}, nil
		}
	default:
		return Input{Raw: s, Kind: InputUnknown}, nil
	}
}

// SchemeOf returns the lowercase scheme of an already parsed input.
func SchemeOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Scheme)
}
