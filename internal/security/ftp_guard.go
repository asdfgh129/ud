package security

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

// FTP is the only protocol in scope where the SERVER picks a second address:
// EPSV / PASV replies carry the passive data-channel host and port. A hostile
// server can answer with 169.254.169.254, a loopback admin port, or any other
// internal endpoint. The control connection's DNS pin does not cover that
// second socket, so DialDataContext closes the hole with four rules:
//
//   - the data address must be a literal IP (no second DNS lookup at all),
//   - it must equal the IP of the already-pinned control connection,
//   - it must still pass the current private/reserved address policy,
//   - its port must be unprivileged (>= 1024), so a hostile PASV reply cannot
//     steer us at another service on the same host (22, 6379, ...).
//
// Every violation fails closed with E_RESOLVE_SSRF_BLOCKED. Transports reach
// this through an optional interface; a guard that does not implement it can
// never open an FTP data channel.
func (g *SSRFGuard) DialDataContext(ctx context.Context, addr string, control netip.Addr) (net.Conn, error) {
	ip, port, err := g.CheckDataAddr(addr, control)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
}

// CheckDataAddr validates a passive data-channel address without dialing.
// Exported so the transport can unit-test the policy and so the rule stays in
// one authority (docs/11 §2).
func (g *SSRFGuard) CheckDataAddr(addr string, control netip.Addr) (netip.Addr, int, error) {
	blocked := func(format string, args ...any) (netip.Addr, int, error) {
		return netip.Addr{}, 0, core.Err(core.E_RESOLVE_SSRF_BLOCKED, format, args...)
	}
	if !control.IsValid() {
		return blocked("FTP data channel has no pinned control connection")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return blocked("malformed FTP data address %q", addr)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return blocked("FTP data address must be a literal IP, got %q", host)
	}
	ip = ip.Unmap()
	if ip != control.Unmap() {
		return blocked("FTP data address %s does not match control connection %s", ip, control)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1024 || port > 65535 {
		return blocked("FTP data port %q is outside 1024..65535", portStr)
	}
	g.mu.RLock()
	cfg := g.cfg
	g.mu.RUnlock()
	if bad, reason := classifyIP(ip, cfg.BlockPrivateNetworks, cfg.AllowLoopbackForDev, false); bad {
		return blocked("FTP data address %s is blocked (%s)", ip, reason)
	}
	return ip, port, nil
}
