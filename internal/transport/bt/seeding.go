// Seeding implements the ADR-0021 path A ("补齐实现") policy layer for the
// BitTorrent transport: bounded, honest upload etiquette after a download
// completes. It is deliberately a separate file so the original leech-only
// data path stays obvious.
//
// Scope notes (must stay true, see docs/ADR-0021 and docs/11 §7.1):
//   - Upload is OPT-IN. A Transport constructed without WithSeedingPolicy
//     keeps the WBS 7 leech-only behaviour (NoUpload=true / Seed=false).
//     The daemon wires the policy from bt.* config; the WBS 11 interop lab
//     deliberately does not.
//   - Private torrents NEVER seed here: DHT/PEX stay disabled and upload
//     stays off. PT admission is not promised (ADR-0017).
//   - Peer identity stays honest (ADR-0017): a configured peerID that does
//     not start with "-UD" is ignored in favour of the generated honest id.
//   - Upload byte rate has NO second limiter here. The only rate authority
//     is scheduler.Limiter (docs/04 §5.3 / check_arch.py guard 4); this
//     layer bounds only the number of simultaneous upload connections.
package bttransport

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

const (
	honestPeerIDPrefix       = "-UD"
	defaultSeedStatsInterval = 2 * time.Second
)

// SeedingPolicy is the complete, bounded upload-etiquette configuration for
// one BT Transport. The zero value means "no seeding" (leech-only), which is
// also the safe default for tests and the interop lab.
type SeedingPolicy struct {
	Enabled        bool
	SeedRatio      float64       // stop once uploaded/downloaded >= SeedRatio
	SeedTime       time.Duration // stop once elapsed >= SeedTime
	MaxUploadSlots int           // bound on simultaneous peer connections
	EnableUPnP     bool          // UPnP / NAT-PMP port forwarding
	EnableDHT      bool
	EnablePEX      bool
	PeerID         string
	ListenPort     int    // 0 = ephemeral port
	ListenHost     string // empty = library default (all interfaces)
	StatsInterval  time.Duration
}

// DefaultSeedingPolicy is the daemon's v1 etiquette baseline: share at least
// 1.0x or seed for 60 minutes, whichever is reached first, with 8 upload
// slots. Both targets are bounded so a task can never seed forever.
func DefaultSeedingPolicy() SeedingPolicy {
	return SeedingPolicy{
		Enabled:        true,
		SeedRatio:      1.0,
		SeedTime:       60 * time.Minute,
		MaxUploadSlots: 8,
		EnableUPnP:     true,
		EnableDHT:      true,
		EnablePEX:      true,
		StatsInterval:  defaultSeedStatsInterval,
	}
}

// SeedingFromConfig maps the (previously reserved, now live) bt.* keys onto
// a policy. It never reads the global download/upload rate keys: the single
// rate authority stays in scheduler.Limiter.
func SeedingFromConfig(cfg config.BTConfig) SeedingPolicy {
	p := DefaultSeedingPolicy()
	p.Enabled = cfg.SeedEnabled
	p.SeedRatio = cfg.SeedRatio
	p.SeedTime = time.Duration(cfg.SeedTimeMinutes) * time.Minute
	if cfg.MaxUploadSlots > 0 {
		p.MaxUploadSlots = cfg.MaxUploadSlots
	}
	p.EnableUPnP = cfg.EnableUPnP
	p.EnableDHT = cfg.EnableDHT
	p.EnablePEX = cfg.EnablePEX
	p.PeerID = honestPeerIDFor(cfg.PeerID)
	p.ListenPort = cfg.ListenPort
	p.ListenHost = cfg.ListenHost
	return p
}

// honestPeerIDFor enforces ADR-0017: we never present ourselves as another
// client. A 20-byte "-UD…" id may be configured for stable identification;
// anything else (including the old "-GT0001-" placeholder) falls back to the
// generated honest id rather than being used or rejected at startup.
func honestPeerIDFor(want string) string {
	if len(want) == 20 && strings.HasPrefix(want, honestPeerIDPrefix) {
		return want
	}
	return honestPeerID()
}

// seedingApplicable is the single gate for enabling upload. Private torrents
// are excluded deliberately: no DHT/PEX, no upload, no PT admission claims.
func seedingApplicable(p SeedingPolicy, r *core.Resource) bool {
	return p.Enabled && r != nil && !r.Identity.Private
}

// applySeedingPolicy writes the policy into an anacrolix client config. It is
// a pure function of its arguments (no network, no filesystem), which keeps
// the etiquette invariants unit-testable.
func applySeedingPolicy(cfg *torrent.ClientConfig, r *core.Resource, p SeedingPolicy) {
	private := r != nil && r.Identity.Private
	on := seedingApplicable(p, r)

	cfg.NoUpload = !on
	cfg.Seed = on
	cfg.PeerID = honestPeerIDFor(p.PeerID)

	// The WBS 7 rule (BT-08) is unconditional: private torrents never use
	// DHT or PEX. When no policy is supplied we preserve the exact leech-only
	// behaviour of WBS 7 so the 9 L3 loopback cases stay byte-for-byte stable.
	cfg.NoDHT = private
	cfg.DisablePEX = private
	if on {
		cfg.NoDHT = private || !p.EnableDHT
		cfg.DisablePEX = private || !p.EnablePEX
		cfg.NoDefaultPortForwarding = !p.EnableUPnP
		if p.MaxUploadSlots > 0 {
			cfg.EstablishedConnsPerTorrent = p.MaxUploadSlots
		}
		if p.ListenPort > 0 {
			cfg.ListenPort = p.ListenPort
		}
		if p.ListenHost != "" {
			host := p.ListenHost
			cfg.ListenHost = func(string) string { return host }
		}
		return
	}
	// Leech-only: never open a NAT mapping just to download.
	cfg.NoDefaultPortForwarding = true
}

// SeedingStats mirrors the counters used by tracker announces so the REST /
// tasks.bytes_* view and the wire announce view cannot drift apart.
type SeedingStats struct {
	ResourceID string
	Uploaded   int64
	Downloaded int64
	Left       int64
	Ratio      float64
	Elapsed    time.Duration
}

// SeedingObserver is an optional sink. The daemon implements it to persist
// bytes_uploaded and drive the Completed -> Seeding -> Completed transitions;
// a nil observer means the seeding session is still real but unobserved.
type SeedingObserver interface {
	SeedingStarted(ctx context.Context, resourceID string)
	SeedingProgress(ctx context.Context, stats SeedingStats)
	SeedingStopped(ctx context.Context, resourceID string, reason string)
}

// seedingGoalMet reports whether the bounded seeding obligation is done.
// Either target being reached stops the session (ratio OR time), which is the
// conservative reading of "分享率/时长策略": no unbounded seeding.
func seedingGoalMet(uploaded, downloaded int64, elapsed time.Duration, p SeedingPolicy) (bool, string) {
	if p.SeedRatio <= 0 && p.SeedTime <= 0 {
		return true, "no-seed-target"
	}
	if p.SeedRatio > 0 && downloaded > 0 && float64(uploaded)/float64(downloaded) >= p.SeedRatio {
		return true, "seed-ratio"
	}
	if p.SeedTime > 0 && elapsed >= p.SeedTime {
		return true, "seed-time"
	}
	return false, ""
}

// seedingSession owns one handed-off torrent client. Once Run() returns, the
// client and its temp data directory belong to this session: it closes both
// when the seeding target, the daemon context, or Close() ends the session.
type seedingSession struct {
	resourceID string
	policy     SeedingPolicy
	observer   SeedingObserver
	readStats  func() (uploaded, downloaded, left int64)
	closeFn    func()
}

func (s *seedingSession) run(ctx context.Context) {
	interval := s.policy.StatsInterval
	if interval <= 0 {
		interval = defaultSeedStatsInterval
	}
	started := time.Now()
	if s.observer != nil {
		s.observer.SeedingStarted(ctx, s.resourceID)
	}
	reason := "context-canceled"
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
loop:
	for {
		uploaded, downloaded, left := s.readStats()
		elapsed := time.Since(started)
		ratio := 0.0
		if downloaded > 0 {
			ratio = float64(uploaded) / float64(downloaded)
		}
		if s.observer != nil {
			s.observer.SeedingProgress(ctx, SeedingStats{
				ResourceID: s.resourceID, Uploaded: uploaded, Downloaded: downloaded,
				Left: left, Ratio: ratio, Elapsed: elapsed,
			})
		}
		if met, why := seedingGoalMet(uploaded, downloaded, elapsed, s.policy); met {
			reason = why
			break loop
		}
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
		}
	}
	if s.closeFn != nil {
		s.closeFn()
	}
	if s.observer != nil {
		// A cancelled ctx must not prevent the final state write.
		s.observer.SeedingStopped(context.WithoutCancel(ctx), s.resourceID, reason)
	}
}

// SeedingManager holds every live session and provides deterministic shutdown
// (daemon exit / test teardown). Close() cancels all sessions and waits.
type SeedingManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func newSeedingManager(parent context.Context) *SeedingManager {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &SeedingManager{ctx: ctx, cancel: cancel}
}

func (m *SeedingManager) start(s *seedingSession) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		s.run(m.ctx)
	}()
	return true
}

func (m *SeedingManager) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// newSession builds the real, network-backed session: counters come from the
// same Client.ConnStats() the library uses for tracker announces, and cleanup
// closes the client before removing its temp data directory.
func newSession(r *core.Resource, cl *torrent.Client, tor *torrent.Torrent, dataDir string, p SeedingPolicy, obs SeedingObserver) *seedingSession {
	var total int64
	if tor != nil && tor.Info() != nil {
		total = tor.Info().TotalLength()
	}
	return &seedingSession{
		resourceID: r.ID,
		policy:     p,
		observer:   obs,
		readStats: func() (uploaded, downloaded, left int64) {
			if cl == nil {
				return 0, 0, total
			}
			cs := cl.ConnStats()
			uploaded = cs.BytesWrittenData.Int64()
			downloaded = cs.BytesReadUsefulData.Int64()
			left = total - downloaded
			if left < 0 {
				left = 0
			}
			return uploaded, downloaded, left
		},
		closeFn: func() {
			if cl != nil {
				cl.Close()
			}
			_ = os.RemoveAll(dataDir)
		},
	}
}
