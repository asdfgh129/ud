package bttransport

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

func seedTestResource(private bool) *core.Resource {
	return &core.Resource{
		ID:   "res-seed-1",
		Kind: core.KindTorrent,
		Identity: core.ProtocolIdentity{
			Private: private,
		},
	}
}

// recordingObserver is a deterministic stand-in for the daemon observer.
type recordingObserver struct {
	mu       sync.Mutex
	started  int
	progress int
	stopped  []string
}

func (o *recordingObserver) SeedingStarted(_ context.Context, _ string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started++
}
func (o *recordingObserver) SeedingProgress(_ context.Context, _ SeedingStats) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.progress++
}
func (o *recordingObserver) SeedingStopped(_ context.Context, _ string, reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = append(o.stopped, reason)
}
func (o *recordingObserver) snapshot() (int, int, []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.started, o.progress, append([]string(nil), o.stopped...)
}

func TestClientConfigLeechOnlyDefaults(t *testing.T) {
	for _, private := range []bool{false, true} {
		r := seedTestResource(private)
		cfg := clientConfig(r, t.TempDir(), false)
		if !cfg.NoUpload || cfg.Seed {
			t.Fatalf("private=%v: no policy must stay leech-only (NoUpload=%v Seed=%v)",
				private, cfg.NoUpload, cfg.Seed)
		}
		if cfg.NoDHT != private || cfg.DisablePEX != private {
			t.Fatalf("private=%v: DHT/PEX must follow only the private flag (NoDHT=%v DisablePEX=%v)",
				private, cfg.NoDHT, cfg.DisablePEX)
		}
		if !cfg.NoDefaultPortForwarding {
			t.Fatal("leech-only must never open a NAT port mapping")
		}
		if !strings.HasPrefix(cfg.PeerID, honestPeerIDPrefix) || len(cfg.PeerID) != 20 {
			t.Fatalf("PeerID must stay honest (ADR-0017): %q", cfg.PeerID)
		}
	}
}

func TestClientConfigAppliesSeedingPolicy(t *testing.T) {
	p := DefaultSeedingPolicy()
	p.MaxUploadSlots = 4
	p.ListenPort = 6881
	p.ListenHost = "127.0.0.1"
	p.EnableDHT = false
	p.EnablePEX = false
	cfg := clientConfig(seedTestResource(false), t.TempDir(), false, p)

	if cfg.NoUpload || !cfg.Seed {
		t.Fatalf("seeding policy must enable upload: NoUpload=%v Seed=%v", cfg.NoUpload, cfg.Seed)
	}
	if cfg.EstablishedConnsPerTorrent != 4 {
		t.Fatalf("upload slots = %d, want 4", cfg.EstablishedConnsPerTorrent)
	}
	if cfg.ListenPort != 6881 {
		t.Fatalf("listen port = %d, want 6881", cfg.ListenPort)
	}
	if got := cfg.ListenHost("tcp"); got != "127.0.0.1" {
		t.Fatalf("listen host = %q", got)
	}
	if cfg.NoDefaultPortForwarding {
		t.Fatal("enableUPnP=true must allow the library's UPnP / NAT-PMP forwarding")
	}
	if !cfg.NoDHT || !cfg.DisablePEX {
		t.Fatal("disabled DHT/PEX in the policy must be honoured")
	}
	if !strings.HasPrefix(cfg.PeerID, honestPeerIDPrefix) {
		t.Fatalf("PeerID lost its honest prefix: %q", cfg.PeerID)
	}
}

func TestPrivateTorrentNeverSeedsEvenWithPolicy(t *testing.T) {
	cfg := clientConfig(seedTestResource(true), t.TempDir(), false, DefaultSeedingPolicy())
	if !cfg.NoUpload || cfg.Seed {
		t.Fatalf("private torrent must stay leech-only: NoUpload=%v Seed=%v", cfg.NoUpload, cfg.Seed)
	}
	if !cfg.NoDHT || !cfg.DisablePEX {
		t.Fatal("BT-08: private torrent must never use DHT or PEX")
	}
	if !cfg.NoDefaultPortForwarding {
		t.Fatal("private torrent must not request a NAT mapping")
	}
}

func TestSeedingFromConfigMapsLiveBTKeys(t *testing.T) {
	p := SeedingFromConfig(config.Defaults().BT)
	if !p.Enabled || p.SeedRatio != 1.0 || p.SeedTime != 60*time.Minute || p.MaxUploadSlots != 8 {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if !p.EnableUPnP || !p.EnableDHT || !p.EnablePEX {
		t.Fatalf("discovery/UPnP flags not mapped: %+v", p)
	}
	if !strings.HasPrefix(p.PeerID, honestPeerIDPrefix) || len(p.PeerID) != 20 {
		t.Fatalf("configured peerID must resolve to an honest 20-byte id: %q", p.PeerID)
	}

	bad := config.Defaults().BT
	bad.PeerID = "-GT0001-123456789012"
	bad.SeedEnabled = false
	bad.MaxUploadSlots = 3
	bad.ListenPort = 7777
	p2 := SeedingFromConfig(bad)
	if p2.Enabled {
		t.Fatal("seedEnabled=false must disable seeding")
	}
	if p2.MaxUploadSlots != 3 || p2.ListenPort != 7777 {
		t.Fatalf("config keys not mapped: %+v", p2)
	}
	if strings.HasPrefix(p2.PeerID, "-GT") {
		t.Fatal("a non-honest configured peerID must never reach the wire")
	}
}

func TestHonestPeerIDFor(t *testing.T) {
	cases := []struct {
		in, wantPrefix string
		wantSame       bool
	}{
		{"", "-UD", false},
		{"-GT0001-123456789012", "-UD", false},
		{"-UD0000-", "-UD", false}, // prefix only: not a full 20-byte id
		{"-UD0001-abcdef123456", "-UD", true},
	}
	for _, tc := range cases {
		got := honestPeerIDFor(tc.in)
		if !strings.HasPrefix(got, tc.wantPrefix) || len(got) != 20 {
			t.Fatalf("honestPeerIDFor(%q) = %q (len %d)", tc.in, got, len(got))
		}
		if tc.wantSame && got != tc.in {
			t.Fatalf("stable configured id was not preserved: %q != %q", got, tc.in)
		}
		if !tc.wantSame && got == tc.in && tc.in != "" {
			t.Fatalf("dishonest/partial id was reused: %q", tc.in)
		}
	}
}

func TestSeedingGoalMetIsBounded(t *testing.T) {
	cases := []struct {
		name                 string
		uploaded, downloaded int64
		elapsed              time.Duration
		p                    SeedingPolicy
		wantMet              bool
		wantReason           string
	}{
		{"ratio reached", 100, 100, time.Second, SeedingPolicy{SeedRatio: 1}, true, "seed-ratio"},
		{"ratio not reached", 50, 100, time.Second, SeedingPolicy{SeedRatio: 1}, false, ""},
		{"time reached", 0, 100, 2 * time.Minute, SeedingPolicy{SeedTime: time.Minute}, true, "seed-time"},
		{"ratio with zero download", 100, 0, time.Second, SeedingPolicy{SeedRatio: 1, SeedTime: time.Hour}, false, ""},
		{"no target stops immediately", 0, 0, 0, SeedingPolicy{}, true, "no-seed-target"},
		{"neither target reached", 1, 100, time.Second, SeedingPolicy{SeedRatio: 2, SeedTime: time.Hour}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			met, reason := seedingGoalMet(tc.uploaded, tc.downloaded, tc.elapsed, tc.p)
			if met != tc.wantMet || reason != tc.wantReason {
				t.Fatalf("got (%v,%q), want (%v,%q)", met, reason, tc.wantMet, tc.wantReason)
			}
		})
	}
}

func TestSeedingSessionStopsOnTargetAndCloses(t *testing.T) {
	obs := &recordingObserver{}
	closed := false
	s := &seedingSession{
		resourceID: "res-seed-1",
		policy: SeedingPolicy{
			SeedRatio: 1, SeedTime: time.Hour, StatsInterval: time.Millisecond,
		},
		observer: obs,
		readStats: func() (int64, int64, int64) {
			return 100, 100, 0 // ratio 1.0 already met
		},
		closeFn: func() { closed = true },
	}
	s.run(context.Background())
	if !closed {
		t.Fatal("session must close its client/temp dir when the target is met")
	}
	started, progress, stopped := obs.snapshot()
	if started != 1 || progress < 1 || len(stopped) != 1 || stopped[0] != "seed-ratio" {
		t.Fatalf("observer events = started %d, progress %d, stopped %v", started, progress, stopped)
	}
}

func TestSeedingSessionStopsOnContextAndReportsStats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	obs := &recordingObserver{}
	closed := make(chan struct{})
	s := &seedingSession{
		resourceID: "res-seed-1",
		policy: SeedingPolicy{
			SeedRatio: 100, SeedTime: time.Hour, StatsInterval: time.Millisecond,
		},
		observer:  obs,
		readStats: func() (int64, int64, int64) { return 7, 100, 10 },
		closeFn:   func() { close(closed) },
	}
	go s.run(ctx)
	cancel()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not stop on context cancellation")
	}
	_, progress, stopped := obs.snapshot()
	if progress < 1 || len(stopped) != 1 || stopped[0] != "context-canceled" {
		t.Fatalf("unexpected observer events: progress %d, stopped %v", progress, stopped)
	}
}

func TestSeedingManagerCloseCancelsAllSessions(t *testing.T) {
	m := newSeedingManager(context.Background())
	closed := make(chan struct{})
	s := &seedingSession{
		policy: SeedingPolicy{
			SeedRatio: 100, SeedTime: time.Hour, StatsInterval: time.Millisecond,
		},
		readStats: func() (int64, int64, int64) { return 0, 100, 0 },
		closeFn:   func() { close(closed) },
	}
	if !m.start(s) {
		t.Fatal("start on an open manager must succeed")
	}
	m.Close()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not cancel the session")
	}
	if m.start(&seedingSession{closeFn: func() {}}) {
		t.Fatal("start after Close() must be rejected")
	}
	m.Close() // idempotent
}

func TestTransportOptionsAndBeginSeedingGuards(t *testing.T) {
	tr := New(nil, nil)
	defer tr.Close()
	if tr.policy.Enabled {
		t.Fatal("Transport without WithSeedingPolicy must default to leech-only")
	}
	if tr.beginSeeding(seedTestResource(false), nil, nil, t.TempDir()) {
		t.Fatal("leech-only transport must not hand off a seeding session")
	}

	tr2 := New(nil, nil, WithSeedingPolicy(DefaultSeedingPolicy()), WithoutSeeding())
	defer tr2.Close()
	if tr2.policy.Enabled {
		t.Fatal("WithoutSeeding must win when applied last")
	}

	tr3 := New(nil, nil, WithSeedingPolicy(DefaultSeedingPolicy()))
	defer tr3.Close()
	if tr3.beginSeeding(seedTestResource(true), nil, nil, t.TempDir()) {
		t.Fatal("private torrent must never enter a seeding session")
	}

	dir := t.TempDir()
	tr4 := New(nil, nil, WithSeedingPolicy(SeedingPolicy{
		Enabled: true, SeedRatio: 100, SeedTime: time.Hour, StatsInterval: time.Millisecond,
	}))
	if !tr4.beginSeeding(seedTestResource(false), nil, nil, dir) {
		t.Fatal("a public torrent with an enabled policy must enter a seeding session")
	}
	tr4.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session cleanup must remove the temp dir, stat err = %v", err)
	}
}

func TestNewSessionCountersAreNilSafe(t *testing.T) {
	s := newSession(seedTestResource(false), nil, nil, t.TempDir(), DefaultSeedingPolicy(), nil)
	u, d, l := s.readStats()
	if u != 0 || d != 0 || l != 0 {
		t.Fatalf("nil client counters must be zero, got %d/%d/%d", u, d, l)
	}
	s.closeFn() // must not panic without a client
}
