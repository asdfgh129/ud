// Package tputil holds the pieces shared by the byte-moving transports that
// need a real network session (FTP / FTPS / SFTP). It owns no protocol code and
// never opens a socket; chunk allocation, retries and state stay with the
// Scheduler (ADR-0002, docs/05 §5).
package tputil

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// Credential is the JSON shape of a secret stored behind core.CredentialRef.
// Plaintext never enters the Resource, SQLite, events, logs or REST responses
// (docs/11 §3). Every field is optional at parse time; each transport then
// enforces the minimum its protocol needs.
//
// Example (SFTP with a pinned host key):
//
//	{"username":"alice","password":"s3cret",
//	 "host_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... server.example"}
//
// or with a fingerprint and a passphrase-protected key:
//
//	{"username":"alice","private_key":"-----BEGIN OPENSSH PRIVATE KEY-----...",
//	 "passphrase":"...", "host_key_fingerprint":"SHA256:..."}
type Credential struct {
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	PrivateKey         string `json:"private_key,omitempty"`
	Passphrase         string `json:"passphrase,omitempty"`
	HostKey            string `json:"host_key,omitempty"`
	HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
}

const (
	// A credential blob is small; refusing huge values avoids an accidental
	// memory amplification from a misconfigured store.
	MaxCredentialBytes = 1 << 20
	anonymousUser      = "anonymous"
	anonymousPassword  = "anonymous@ud.invalid"
)

// LoadCredential resolves a credential reference. FTP allows anonymous access
// when no ref is supplied; SFTP never does.
func LoadCredential(ctx context.Context, store core.CredentialStore, ref core.CredentialRef,
	anonymousOK bool) (Credential, error) {
	if ref == "" {
		if anonymousOK {
			return Credential{Username: anonymousUser, Password: anonymousPassword}, nil
		}
		return Credential{}, core.Err(core.E_INVALID_INPUT,
			"credentialRef is required for this protocol; plaintext credentials in URLs are forbidden")
	}
	if store == nil {
		return Credential{}, core.Err(core.E_INTERNAL, "credential store is not configured")
	}
	secret, err := store.Get(ctx, ref)
	if err != nil {
		// Do not echo the store error: it may contain the reference or, in a
		// future keychain adapter, backend diagnostics with sensitive detail.
		return Credential{}, core.Err(core.E_INVALID_INPUT, "credential reference could not be resolved")
	}
	return ParseCredential(secret)
}

// ParseCredential accepts only the documented JSON object. A bare password is
// deliberately rejected: silently guessing a format is how credentials end up
// being sent as a wrong username.
func ParseCredential(secret string) (Credential, error) {
	if len(secret) == 0 || len(secret) > MaxCredentialBytes {
		return Credential{}, core.Err(core.E_INVALID_INPUT,
			"credential payload must be 1..%d bytes", MaxCredentialBytes)
	}
	var c Credential
	if err := json.Unmarshal([]byte(secret), &c); err != nil {
		return Credential{}, core.Err(core.E_INVALID_INPUT,
			"credential must be a JSON object with username/password or private_key fields")
	}
	if strings.ContainsAny(c.Username, "\x00\r\n") ||
		strings.ContainsAny(c.Password, "\x00") ||
		strings.ContainsAny(c.PrivateKey, "\x00") ||
		strings.ContainsAny(c.HostKey, "\x00\r\n") {
		return Credential{}, core.Err(core.E_INVALID_INPUT, "credential contains control characters")
	}
	return c, nil
}

// Target is the validated, connection-level view of one mirror URL.
type Target struct {
	Raw    string
	Addr   string // host:port, using the default port when the URL omits one
	Host   string
	Port   string
	Path   string
	Scheme string
}

const maxTargetBytes = 8192

// ParseTarget validates one mirror URL before any socket is opened. The SSRF
// Guard performs the actual DNS/IP policy check; this function rejects the
// syntactic hazards that are protocol-specific: embedded credentials, control
// characters (FTP command injection) and path traversal.
func ParseTarget(raw string, allowed map[string]string) (Target, error) {
	fail := func(format string, args ...any) (Target, error) {
		return Target{}, core.Err(core.E_INVALID_INPUT, format, args...)
	}
	if len(raw) == 0 || len(raw) > maxTargetBytes {
		return fail("mirror URL length must be 1..%d bytes", maxTargetBytes)
	}
	if strings.ContainsAny(raw, "\x00\r\n\t ") {
		return fail("mirror URL contains whitespace or control characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fail("mirror URL is not an absolute URL")
	}
	scheme := strings.ToLower(u.Scheme)
	defaultPort, ok := allowed[scheme]
	if !ok {
		return fail("mirror scheme %q is not supported by this transport", scheme)
	}
	if u.User != nil {
		return Target{}, core.Err(core.E_RESOLVE_SSRF_BLOCKED,
			"credentials embedded in a mirror URL are forbidden; use credentialRef")
	}
	if u.Fragment != "" || u.RawQuery != "" {
		return fail("mirror URL must not carry a query or fragment")
	}
	p, err := url.PathUnescape(u.EscapedPath())
	if err != nil || p == "" || p == "/" {
		return fail("mirror URL must name a file path")
	}
	// Percent-encoded control characters survive the raw-URL check above; FTP
	// turns them into command injection, SFTP into a malformed handle.
	if strings.ContainsAny(p, "\x00\r\n") {
		return fail("decoded mirror path contains control characters")
	}
	// path.Clean neutralises ".." on absolute paths, so reject the segments
	// BEFORE cleaning: an untrusted URL must never be able to name a parent.
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fail("mirror path %q contains a traversal segment", p)
		}
	}
	clean := path.Clean(p)
	if clean == "." || clean == "/" || strings.HasPrefix(clean, "../") {
		return fail("mirror path %q is not a usable file path", p)
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fail("mirror URL has an invalid port %q", port)
	}
	return Target{
		Raw:    raw,
		Addr:   u.Hostname() + ":" + port,
		Host:   u.Hostname(),
		Port:   port,
		Path:   clean,
		Scheme: scheme,
	}, nil
}
