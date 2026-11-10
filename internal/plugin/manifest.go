// Package plugin implements the P1 WBS 10 rule engine: a goja (ES5.1) runtime
// for declarative site Resolver plugins, gated by a signed manifest, static
// API scanning, capability trimming and the shared SSRF Guard (ADR-0014,
// docs/08, docs/11 §5).
//
// Design invariants:
//   - A plugin is a directory containing manifest.json and one entry .js file.
//   - Capabilities are deny-by-default: fs / env / subprocess are rejected at
//     load time, and the only network path is the host-injected ctx.http.
//   - Plugins are registered AFTER built-in Resolvers and BEFORE Unsupported;
//     match patterns that could shadow a security-sensitive built-in are
//     refused at load time (docs/08 §5).
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

const (
	// APIVersion is the only supported manifest contract version.
	APIVersion = "ud.plugin/v1"
	// RuntimeGoja is the only implemented runtime. "wasm" is an explicit
	// future upgrade path (ADR-0014 §5) and must fail closed, not silently.
	RuntimeGoja = "goja"

	maxScriptBytes  = 512 << 10
	maxMatchRules   = 32
	maxAllowedHosts = 64
	maxPlugins      = 64

	defaultTimeoutMs       = 10_000
	defaultMemoryMB        = 32
	defaultMaxResponseSize = 1 << 20
	defaultMaxRedirects    = 3

	hardTimeoutMs       = 60_000
	hardMemoryMB        = 256
	hardMaxResponseSize = 8 << 20
	hardMaxRedirects    = 10
)

// Manifest is the on-disk rule-pack descriptor (docs/08 §2).
type Manifest struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	APIVersion   string       `json:"apiVersion"`
	Type         string       `json:"type"`
	Runtime      string       `json:"runtime"`
	Entry        string       `json:"entry"`
	Match        []string     `json:"match"`
	Capabilities Capabilities `json:"capabilities"`
	Limits       Limits       `json:"limits"`
	Signature    string       `json:"signature,omitempty"`
	Author       string       `json:"author,omitempty"`
	License      string       `json:"license,omitempty"`
}

// Capabilities is the least-privilege grant. FS/Env/Subprocess are parsed so a
// request for them is rejected explicitly instead of being ignored.
type Capabilities struct {
	HTTP       *HTTPCapability `json:"http,omitempty"`
	FS         bool            `json:"fs"`
	Env        bool            `json:"env"`
	Subprocess bool            `json:"subprocess"`
	Crypto     []string        `json:"crypto,omitempty"`
}

// HTTPCapability holds the ONLY allowed egress destinations. An empty
// AllowedHosts means "no network at all".
type HTTPCapability struct {
	AllowedHosts []string `json:"allowedHosts"`
	MaxRedirects int      `json:"maxRedirects,omitempty"`
}

// Limits are always clamped: a rule pack may ask for less, never for more.
type Limits struct {
	TimeoutMs        int   `json:"timeoutMs"`
	MemoryMB         int   `json:"memoryMB"`
	MaxResponseBytes int64 `json:"maxResponseBytes"`
}

var (
	nameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)
	hostRE    = regexp.MustCompile(`^(?:\*\.)?[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
)

// builtinSentinels are inputs owned by security-sensitive built-in Resolvers.
// A plugin whose match rule hits any of them would shadow a built-in if the
// registry order ever changed, so docs/08 §5 requires refusing the pack.
// Only scheme-specific sentinels are listed: http(s) URLs are deliberately
// allowed, because site rules must be able to match them (see registry.go for
// the exact placement relative to the generic direct-http fallback).
var builtinSentinels = []string{
	"thunder://AAhttp://example.com/aZZ",
	"flashget://[FLASHGET]http://example.com/a[FLASHGET]",
	"qqdl://aHR0cDovL2V4YW1wbGUuY29tL2E=",
	"magnet:?xt=urn:btih:0000000000000000000000000000000000000000",
	"ed2k://|file|a.bin|1|0123456789abcdef0123456789abcdef|/",
	"file:///etc/passwd",
	"d8:announce24:http://tracker/a/",
}

// ParseManifest decodes and validates a manifest. It never touches the disk.
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(raw) > 64<<10 {
		return nil, core.Err(core.E_INVALID_INPUT, "manifest exceeds 64 KiB")
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "invalid manifest JSON: %v", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate enforces the docs/08 §2 contract and the ADR-0014 deny-by-default
// capability policy. Match rules are compiled here so a broken regex can never
// reach the runtime.
func (m *Manifest) Validate() error {
	if !nameRE.MatchString(m.Name) {
		return core.Err(core.E_INVALID_INPUT, "invalid plugin name %q (want ^[a-z0-9][a-z0-9._-]{0,63}$)", m.Name)
	}
	if !versionRE.MatchString(m.Version) {
		return core.Err(core.E_INVALID_INPUT, "invalid plugin version %q (want semver)", m.Version)
	}
	if m.APIVersion != APIVersion {
		return core.Err(core.E_INVALID_INPUT, "unsupported apiVersion %q (want %s)", m.APIVersion, APIVersion)
	}
	if m.Type != "resolver" {
		return core.Err(core.E_INVALID_INPUT, "unsupported plugin type %q (want resolver)", m.Type)
	}
	if m.Runtime != RuntimeGoja {
		return core.Err(core.E_CAPABILITY_UNAVAILABLE,
			"runtime %q is not implemented; goja only (wasm hard isolation is a future upgrade path)", m.Runtime)
	}
	if err := validateEntry(m.Entry); err != nil {
		return err
	}
	if len(m.Match) == 0 || len(m.Match) > maxMatchRules {
		return core.Err(core.E_INVALID_INPUT, "match must contain 1..%d rules", maxMatchRules)
	}
	for _, pattern := range m.Match {
		if len(pattern) > 1024 {
			return core.Err(core.E_INVALID_INPUT, "match rule exceeds 1024 bytes")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return core.Err(core.E_INVALID_INPUT, "invalid match regex %q: %v", pattern, err)
		}
		for _, s := range builtinSentinels {
			if re.MatchString(s) {
				return core.Err(core.E_INVALID_INPUT,
					"match rule %q overlaps a security-sensitive built-in resolver (%q)", pattern, s)
			}
		}
	}

	if m.Capabilities.FS || m.Capabilities.Env || m.Capabilities.Subprocess {
		return core.Err(core.E_INVALID_INPUT,
			"fs/env/subprocess capabilities are forbidden for resolver plugins (docs/08 §2)")
	}
	for _, c := range m.Capabilities.Crypto {
		if c != "sha256" && c != "base64" {
			return core.Err(core.E_INVALID_INPUT, "unsupported crypto capability %q", c)
		}
	}
	if err := m.validateHTTP(); err != nil {
		return err
	}
	if err := m.validateLimits(); err != nil {
		return err
	}
	if m.Signature != "" && !strings.HasPrefix(m.Signature, "ed25519:") {
		return core.Err(core.E_INVALID_INPUT, "signature must use the ed25519:<base64> scheme")
	}
	return nil
}

func validateEntry(entry string) error {
	if entry == "" || !strings.HasSuffix(entry, ".js") {
		return core.Err(core.E_INVALID_INPUT, "entry %q must be a .js file", entry)
	}
	if filepath.IsAbs(entry) || strings.ContainsAny(entry, "\\\x00") ||
		strings.Contains(entry, "..") || strings.HasPrefix(entry, "/") {
		return core.Err(core.E_INVALID_INPUT, "entry %q must be a relative in-package path", entry)
	}
	return nil
}

func (m *Manifest) validateHTTP() error {
	h := m.Capabilities.HTTP
	if h == nil {
		return nil // no ctx.http injected at all
	}
	if len(h.AllowedHosts) > maxAllowedHosts {
		return core.Err(core.E_INVALID_INPUT, "allowedHosts exceeds %d entries", maxAllowedHosts)
	}
	for _, host := range h.AllowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || host == "*" || strings.Contains(host, "://") || strings.Contains(host, ":") {
			return core.Err(core.E_INVALID_INPUT, "invalid allowedHosts entry %q", host)
		}
		if !hostRE.MatchString(host) {
			return core.Err(core.E_INVALID_INPUT, "invalid allowedHosts entry %q", host)
		}
		if strings.HasPrefix(host, "*.") && len(host) < 4 {
			return core.Err(core.E_INVALID_INPUT, "wildcard host %q has no suffix", host)
		}
	}
	if h.MaxRedirects < 0 || h.MaxRedirects > hardMaxRedirects {
		return core.Err(core.E_INVALID_INPUT, "http.maxRedirects must be within 0..%d", hardMaxRedirects)
	}
	return nil
}

func (m *Manifest) validateLimits() error {
	if m.Limits.TimeoutMs == 0 {
		m.Limits.TimeoutMs = defaultTimeoutMs
	}
	if m.Limits.MemoryMB == 0 {
		m.Limits.MemoryMB = defaultMemoryMB
	}
	if m.Limits.MaxResponseBytes == 0 {
		m.Limits.MaxResponseBytes = defaultMaxResponseSize
	}
	if m.Limits.TimeoutMs < 1 || m.Limits.TimeoutMs > hardTimeoutMs {
		return core.Err(core.E_INVALID_INPUT, "timeoutMs must be within 1..%d", hardTimeoutMs)
	}
	if m.Limits.MemoryMB < 1 || m.Limits.MemoryMB > hardMemoryMB {
		return core.Err(core.E_INVALID_INPUT, "memoryMB must be within 1..%d", hardMemoryMB)
	}
	if m.Limits.MaxResponseBytes < 1 || m.Limits.MaxResponseBytes > hardMaxResponseSize {
		return core.Err(core.E_INVALID_INPUT, "maxResponseBytes must be within 1..%d", hardMaxResponseSize)
	}
	if m.Capabilities.HTTP != nil && m.Capabilities.HTTP.MaxRedirects == 0 {
		m.Capabilities.HTTP.MaxRedirects = defaultMaxRedirects
	}
	return nil
}

// MatchRule returns the compiled match expressions (Validate has already
// verified they compile).
func (m *Manifest) MatchRule() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(m.Match))
	for _, p := range m.Match {
		out = append(out, regexp.MustCompile(p))
	}
	return out
}

// HostAllowed implements exact + single-level "*.suffix" matching, always
// case-insensitive and always on the URL hostname (port is ignored here and
// validated separately by the SSRF Guard).
func HostAllowed(patterns []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, raw := range patterns {
		p := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "*.") {
			if p == host {
				return true
			}
			continue
		}
		suffix := p[1:] // ".example.com"
		if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
			return true
		}
	}
	return false
}

// LoadDir reads manifest.json + the entry script from one plugin directory.
// Both reads are constrained to regular files inside dir (no traversal, no
// symlink escape, bounded size). The raw manifest bytes are returned too,
// because the Ed25519 signature is computed over their canonical form.
func LoadDir(dir string) (*Manifest, []byte, []byte, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	raw, err := readBounded(manifestPath, 64<<10)
	if err != nil {
		return nil, nil, nil, core.Err(core.E_INVALID_INPUT, "read %s: %v", manifestPath, err)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	entryPath, err := safeJoin(dir, m.Entry)
	if err != nil {
		return nil, nil, nil, err
	}
	src, err := readBounded(entryPath, maxScriptBytes)
	if err != nil {
		return nil, nil, nil, core.Err(core.E_INVALID_INPUT, "read entry %s: %v", m.Entry, err)
	}
	return m, src, raw, nil
}

// safeJoin resolves name under root and rejects symlink escapes.
func safeJoin(root, name string) (string, error) {
	if err := validateEntry(name); err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	p := filepath.Join(absRoot, filepath.FromSlash(name))
	rel, err := filepath.Rel(absRoot, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", core.Err(core.E_INVALID_INPUT, "entry escapes plugin directory")
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err == nil {
		if resolved, err2 := filepath.EvalSymlinks(filepath.Dir(p)); err2 == nil {
			r, err3 := filepath.Rel(realRoot, resolved)
			if err3 != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return "", core.Err(core.E_INVALID_INPUT, "entry symlink escapes plugin directory")
			}
		}
	}
	return p, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("file is %d bytes, limit %d", st.Size(), limit)
	}
	return os.ReadFile(path)
}
