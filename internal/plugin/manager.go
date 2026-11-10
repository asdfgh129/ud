package plugin

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/asdfgh129/ud/internal/core"
)

// Options configures the rule-pack manager. Dir is the independent update
// channel: writing a new signed pack there is picked up without a daemon
// restart or a main-binary release (ADR-0014 §6).
type Options struct {
	Dir             string
	TrustedKeys     []ed25519.PublicKey
	VerifySignature bool
	Insecure        bool // --insecure-plugins / plugins.insecure (local dev only)
	Guard           core.URLGuard
	Doer            Doer
	Logger          *slog.Logger
	// Fallback is invoked only on RULE_OUTDATED, so an obsolete rule never
	// silently drops a task. The daemon wires the built-in direct-http
	// Resolver here; yt-dlp fallback remains a P2+ capability.
	Fallback       core.Resolver
	ReloadInterval time.Duration
}

// PluginInfo is the diagnostics view exposed to `udctl plugin list`.
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}

// Manager owns the atomic snapshot of compiled rule packs and the hot-reload
// loop. It also implements core.Resolver so the resolve.Registry sees plugins
// as exactly one entry.
type Manager struct {
	opts Options
	mu   sync.RWMutex

	active      []*Runtime
	failures    map[string]string
	fingerprint string
}

// New builds the first snapshot. A broken individual pack never prevents the
// daemon from starting: it is recorded in Failures and the last known-good
// version (if any) stays active.
func New(o Options) (*Manager, error) {
	if o.ReloadInterval <= 0 {
		o.ReloadInterval = 30 * time.Second
	}
	if o.ReloadInterval < time.Second {
		o.ReloadInterval = time.Second
	}
	m := &Manager{opts: o, failures: make(map[string]string)}
	if o.Dir == "" {
		m.fingerprint = "empty"
		return m, nil
	}
	if st, err := os.Stat(o.Dir); err != nil || !st.IsDir() {
		return nil, core.Err(core.E_INVALID_INPUT, "plugin dir %q is not a directory", o.Dir)
	}
	// fingerprint starts empty so the first Reload always performs a load.
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// pluginDirs supports both layouts: a single pack in Dir itself, or one
// subdirectory per pack. Results are sorted for deterministic registration.
func pluginDirs(root string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err == nil {
		return []string{root}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(p, "manifest.json")); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	if len(out) > maxPlugins {
		return nil, core.Err(core.E_INVALID_INPUT, "plugin dir contains more than %d packs", maxPlugins)
	}
	return out, nil
}

// Fingerprint hashes the content of every manifest + .js file in the tree so
// hot reload detects in-place edits even when size/mtime happen to match.
func Fingerprint(root string) (string, error) {
	dirs, err := pluginDirs(root)
	if err != nil {
		return "", core.Err(core.E_INVALID_INPUT, "scan plugin dir: %v", err)
	}
	h := sha256.New()
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			return "", err
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".js") || e.Name() == "manifest.json" {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		rel, _ := filepath.Rel(root, d)
		for _, n := range names {
			b, err := readBounded(filepath.Join(d, n), maxScriptBytes)
			if err != nil {
				return "", err
			}
			_, _ = h.Write([]byte(filepath.ToSlash(filepath.Join(rel, n))))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write(b)
			_, _ = h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Reload rescans the rule channel and atomically swaps the snapshot. A pack
// that fails validation/signature/compile keeps its previous compiled version
// and is reported in Failures (fail-safe hot update).
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadLocked(false)
}

func (m *Manager) reloadLocked(force bool) error {
	if m.opts.Dir == "" {
		return nil
	}
	fp, err := Fingerprint(m.opts.Dir)
	if err != nil {
		return err
	}
	if !force && fp == m.fingerprint {
		return nil
	}
	dirs, err := pluginDirs(m.opts.Dir)
	if err != nil {
		return err
	}
	old := make(map[string]*Runtime, len(m.active))
	for _, r := range m.active {
		old[r.Name()] = r
	}
	engine := Engine{Guard: m.opts.Guard, Doer: m.opts.Doer, Logger: m.opts.Logger}
	var next []*Runtime
	failures := make(map[string]string)
	for _, d := range dirs {
		manifest, script, raw, err := LoadDir(d)
		if err != nil {
			failures[filepath.Base(d)] = err.Error()
			continue
		}
		if m.opts.VerifySignature && !m.opts.Insecure {
			if err := Verify(raw, script, manifest.Signature, m.opts.TrustedKeys); err != nil {
				failures[manifest.Name] = err.Error()
				if prev := old["plugin:"+manifest.Name]; prev != nil {
					next = append(next, prev)
				}
				continue
			}
		}
		runtime, err := Compile(manifest, script, engine)
		if err != nil {
			failures[manifest.Name] = err.Error()
			if prev := old["plugin:"+manifest.Name]; prev != nil {
				next = append(next, prev)
			}
			continue
		}
		next = append(next, runtime)
	}
	m.active = next
	m.failures = failures
	m.fingerprint = fp
	if m.opts.Logger != nil {
		m.opts.Logger.Info("plugin rule channel reloaded",
			"active", len(next), "failed", len(failures), "dir", m.opts.Dir)
	}
	return nil
}

// Watch polls the rule channel. Polling (instead of fsnotify) keeps the
// dependency graph pure-stdlib and works identically on all six platforms.
func (m *Manager) Watch(ctx context.Context) {
	if m.opts.Dir == "" {
		return
	}
	t := time.NewTicker(m.opts.ReloadInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fp, err := Fingerprint(m.opts.Dir)
			if err != nil || fp == "" {
				continue
			}
			m.mu.RLock()
			changed := fp != m.fingerprint
			m.mu.RUnlock()
			if !changed {
				continue
			}
			m.mu.Lock()
			_ = m.reloadLocked(true)
			m.mu.Unlock()
		}
	}
}

// Plugins returns a stable diagnostics snapshot.
func (m *Manager) Plugins() []PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PluginInfo, 0, len(m.active)+len(m.failures))
	for _, r := range m.active {
		out = append(out, PluginInfo{Name: r.manifest.Name, Version: r.manifest.Version, State: "active"})
	}
	for name, msg := range m.failures {
		out = append(out, PluginInfo{Name: name, State: "rejected", Error: msg})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Failures returns the last load errors, keyed by pack name / directory.
func (m *Manager) Failures() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.failures))
	for k, v := range m.failures {
		out[k] = v
	}
	return out
}

// Active reports how many rule packs are currently serving.
func (m *Manager) Active() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.active)
}

func (m *Manager) Name() string { return "plugin" }

func (m *Manager) Match(in core.Input) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.active {
		if r.Match(in) {
			return true
		}
	}
	return false
}

func (m *Manager) Resolve(rc core.ResolveContext, in core.Input) (*core.Resource, error) {
	m.mu.RLock()
	var chosen *Runtime
	for _, r := range m.active {
		if r.Match(in) {
			chosen = r
			break
		}
	}
	fallback := m.opts.Fallback
	m.mu.RUnlock()
	if chosen == nil {
		return nil, core.Err(core.E_RESOLVE_FAILED, "no active plugin matches this input")
	}
	res, err := chosen.Resolve(rc, in)
	if err == nil || !IsRuleOutdated(err) {
		return res, err
	}
	if m.opts.Logger != nil {
		m.opts.Logger.Warn("plugin rule outdated, trying fallback resolver",
			"plugin", chosen.manifest.Name, "version", chosen.manifest.Version)
	}
	if fallback == nil {
		return nil, err
	}
	if fbRes, fbErr := fallback.Resolve(rc, in); fbErr == nil {
		return fbRes, nil
	}
	return nil, core.AsError(err).WithDetail("fallback", "attempted and failed")
}

// VerifyPack performs the full offline check used by `udctl plugin verify`:
// manifest → signature → ES5.1 static scan → goja compile. It never runs the
// script and never opens a network connection.
func VerifyPack(dir string, keys []ed25519.PublicKey, requireSignature, insecure bool) (*Manifest, error) {
	m, script, raw, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	if requireSignature && !insecure {
		if err := Verify(raw, script, m.Signature, keys); err != nil {
			return nil, err
		}
	}
	wrapped, err := Transform(string(script))
	if err != nil {
		return nil, err
	}
	if _, err := goja.Compile(m.Entry, wrapped, false); err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "compile %s: %v", m.Entry, err)
	}
	return m, nil
}

// SignPack signs a pack in place and writes the signature into manifest.json.
// Only the "signature" key is added: all other keys are preserved verbatim so
// the canonical payload stays byte-equivalent to what Verify will recompute.
func SignPack(dir, privateKeyB64 string) (*Manifest, error) {
	priv, err := ParsePrivateKey(privateKeyB64)
	if err != nil {
		return nil, err
	}
	m, script, raw, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	sig, err := Sign(raw, script, priv)
	if err != nil {
		return nil, err
	}
	if err := writeSignedManifest(filepath.Join(dir, "manifest.json"), raw, sig); err != nil {
		return nil, err
	}
	m.Signature = sig
	return m, nil
}
