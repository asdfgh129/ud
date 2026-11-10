package security

import (
	"context"
	"net/netip"
	"testing"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

// FTP's passive data channel is server-selected. These cases are the SSRF
// regression set for docs/11 §2 / WBS 9.
func TestCheckDataAddr(t *testing.T) {
	g := NewSSRFGuard(config.SecurityConfig{
		AllowedSchemes:       []string{"ftp", "ftps", "sftp"},
		AllowedPorts:         []int{21, 22, 990},
		BlockPrivateNetworks: true,
	})
	ctl := netip.MustParseAddr("93.184.216.34")

	cases := []struct {
		name    string
		addr    string
		control netip.Addr
		wantErr bool
	}{
		{"ok", "93.184.216.34:40000", ctl, false},
		{"ipv4-mapped", "[::ffff:93.184.216.34]:40000", ctl, false},
		{"no-control-pin", "93.184.216.34:40000", netip.Addr{}, true},
		{"different-ip", "198.51.100.1:40000", ctl, true},
		{"hostname-not-ip", "evil.example:40000", ctl, true},
		{"privileged-port", "93.184.216.34:22", ctl, true},
		{"port-zero", "93.184.216.34:0", ctl, true},
		{"port-too-large", "93.184.216.34:99999", ctl, true},
		{"malformed", "93.184.216.34", ctl, true},
		{"metadata", "169.254.169.254:40000", netip.MustParseAddr("169.254.169.254"), true},
		{"private", "192.168.1.7:40000", netip.MustParseAddr("192.168.1.7"), true},
		{"loopback", "127.0.0.1:40000", netip.MustParseAddr("127.0.0.1"), true},
		{"multicast", "224.0.0.1:40000", netip.MustParseAddr("224.0.0.1"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip, port, err := g.CheckDataAddr(tc.addr, tc.control)
			if tc.wantErr {
				if core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
					t.Fatalf("want E_RESOLVE_SSRF_BLOCKED, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected block: %v", err)
			}
			if !ip.IsValid() || port != 40000 {
				t.Fatalf("unexpected result: %s %d", ip, port)
			}
		})
	}
}

func TestCheckDataAddrHonoursRuntimePolicy(t *testing.T) {
	g := NewSSRFGuard(config.SecurityConfig{BlockPrivateNetworks: true})
	loopback := netip.MustParseAddr("127.0.0.1")
	if _, _, err := g.CheckDataAddr("127.0.0.1:40000", loopback); err == nil {
		t.Fatal("loopback must be blocked by default")
	}
	// Dev mode relaxes loopback for local fixtures (config.Validate forbids
	// combining it with a non-loopback listener).
	g.Update(config.Config{Security: config.SecurityConfig{
		BlockPrivateNetworks: true, AllowLoopbackForDev: true,
	}})
	if _, _, err := g.CheckDataAddr("127.0.0.1:40000", loopback); err != nil {
		t.Fatalf("dev mode should allow a loopback data port: %v", err)
	}
}

func TestDialDataContextFailsClosedBeforeDialing(t *testing.T) {
	g := NewSSRFGuard(config.SecurityConfig{BlockPrivateNetworks: true})
	ctl := netip.MustParseAddr("93.184.216.34")
	// A poisoned PASV address must be rejected without attempting a TCP
	// connection (otherwise this test would hang on an unreachable host).
	_, err := g.DialDataContext(context.Background(), "169.254.169.254:40000", ctl)
	if core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("want SSRF block, got %v", err)
	}
}
