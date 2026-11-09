// Package security implements the controls of docs/11: SSRF Guard, PathGuard,
// redaction and the credential-store boundary (ADR-0015).
package security

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

// reservedPrefixes covers ranges netip does not classify as private/loopback
// but which must never be reachable from an untrusted URL.
var reservedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4",
		"240.0.0.0/4", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "::ffff:0:0/96",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// SSRFGuard validates every untrusted URL and pins the resolved IP so a second
// DNS lookup (rebinding) can never happen (docs/11 §2).
type SSRFGuard struct {
	mu     sync.RWMutex
	cfg    config.SecurityConfig
	pinned map[string][]netip.Addr
	devAny bool // test-only: accept any port on loopback
	res    *net.Resolver
}

func NewSSRFGuard(cfg config.SecurityConfig) *SSRFGuard {
	return &SSRFGuard{
		cfg:    cfg,
		pinned: make(map[string][]netip.Addr),
		res:    net.DefaultResolver,
	}
}

// NewDevGuard is used by local fixture tests: only loopback is relaxed; private,
// link-local and metadata addresses remain blocked so SSRF tests stay meaningful.
func NewDevGuard() *SSRFGuard {
	cfg := config.SecurityConfig{
		AllowedSchemes:       []string{"http", "https"},
		AllowedPorts:         []int{80, 443},
		MaxRedirects:         3,
		BlockPrivateNetworks: true,
		AllowLoopbackForDev:  true,
	}
	g := NewSSRFGuard(cfg)
	g.devAny = true
	return g
}

// Update applies a runtime config PATCH (Manager.onChange).
func (g *SSRFGuard) Update(cfg config.Config) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg = cfg.Security
}

func (g *SSRFGuard) MaxRedirects() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg.MaxRedirects
}

func (g *SSRFGuard) AllowLoopback() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg.AllowLoopbackForDev
}

// Check validates scheme, port and every resolved IP, then pins the result.
func (g *SSRFGuard) Check(ctx context.Context, raw string) error {
	if len(raw) > 8192 {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "URL exceeds maximum length")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "URL has no valid host")
	}
	if u.User != nil {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "credentials embedded in URL are forbidden")
	}

	g.mu.RLock()
	cfg := g.cfg
	devAny := g.devAny
	g.mu.RUnlock()

	scheme := strings.ToLower(u.Scheme)
	if !contains(cfg.AllowedSchemes, scheme) {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "scheme %q is not allowed", scheme)
	}
	port := u.Port()
	if port == "" {
		port = defaultPort(scheme)
	}
	portNum := 0
	if p, err := parsePort(port); err == nil {
		portNum = p
	}
	host := u.Hostname()
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip.Unmap()}
	} else {
		ips, err = g.res.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return core.Err(core.E_RESOLVE_FAILED, "DNS lookup failed for %s: %v", host, err)
		}
		if len(ips) == 0 {
			return core.Err(core.E_RESOLVE_FAILED, "no address for %s", host)
		}
	}
	loopbackOnly := true
	for i, ip := range ips {
		ips[i] = ip.Unmap()
		if blocked, reason := classifyIP(ips[i], cfg.BlockPrivateNetworks, cfg.AllowLoopbackForDev, devAny); blocked {
			return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "address %s for %s is blocked (%s)", ips[i], host, reason)
		}
		if !ips[i].IsLoopback() {
			loopbackOnly = false
		}
	}
	// Dev mode relaxes the port whitelist ONLY for loopback fixtures. Public
	// hosts still require an allowlisted port (docs/11 §2).
	devPort := devAny || (cfg.AllowLoopbackForDev && loopbackOnly)
	if !devPort && !containsInt(cfg.AllowedPorts, portNum) {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "port %s is not allowed", port)
	}

	g.mu.Lock()
	g.pinned[net.JoinHostPort(host, port)] = ips
	g.mu.Unlock()
	return nil
}

func classifyIP(ip netip.Addr, blockPrivate, allowLoopback, devAny bool) (bool, string) {
	if !blockPrivate {
		return false, ""
	}
	if ip.IsLoopback() && allowLoopback {
		return false, ""
	}
	if devAny && ip.IsLoopback() {
		return false, ""
	}
	if ip.IsLoopback() {
		return true, "loopback"
	}
	if ip.IsPrivate() {
		return true, "private"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true, "link-local / cloud metadata"
	}
	if ip.IsMulticast() || ip.IsUnspecified() || !ip.IsGlobalUnicast() {
		return true, "non-global address"
	}
	for _, p := range reservedPrefixes {
		if p.Contains(ip) {
			return true, "reserved range " + p.String()
		}
	}
	return false, ""
}

// DialContext connects only to an IP pinned by Check.
func (g *SSRFGuard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	key := net.JoinHostPort(host, port)
	g.mu.RLock()
	ips := append([]netip.Addr(nil), g.pinned[key]...)
	g.mu.RUnlock()
	if len(ips) == 0 {
		// Defensive: a transport must call Check before dialing. Retry once.
		if err := g.Check(ctx, "http://"+addr); err != nil {
			return nil, err
		}
		g.mu.RLock()
		ips = append([]netip.Addr(nil), g.pinned[key]...)
		g.mu.RUnlock()
	}
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = core.Err(core.E_TRANSIENT_NETWORK, "no usable pinned address for %s", host)
	}
	return nil, lastErr
}

// NewHTTPClient wires per-hop redirect validation and credential stripping.
func (g *SSRFGuard) NewHTTPClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		DialContext:           g.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= g.MaxRedirects() {
				return core.Err(core.E_RESOLVE_FAILED, "redirect limit %d exceeded", g.MaxRedirects())
			}
			if len(via) > 0 && !strings.EqualFold(via[0].URL.Host, req.URL.Host) {
				req.Header.Del("Authorization")
				req.Header.Del("Cookie")
			}
			return g.Check(req.Context(), req.URL.String())
		},
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

func parsePort(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, net.InvalidAddrError("bad port")
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 || n > 65535 {
		return 0, net.InvalidAddrError("bad port")
	}
	return n, nil
}

func defaultPort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "ftps":
		return "990"
	case "ftp":
		return "21"
	case "sftp":
		return "22"
	default:
		return "80"
	}
}
