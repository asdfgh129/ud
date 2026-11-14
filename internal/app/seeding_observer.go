// The daemon-side SeedingObserver closes the loop between anacrolix's wire
// counters and the persisted task view (ADR-0004 "announce 统计统一取
// tasks.bytes_*", ADR-0021 path A, docs/05 §4).
package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/asdfgh129/ud/internal/store"
	bttransport "github.com/asdfgh129/ud/internal/transport/bt"
)

// seedingObserver persists upload counters and drives the state machine.
// State changes are conditional (store.TrySetSeedingState), so a session that
// starts before scheduler.Commit finishes simply retries on the next progress
// tick instead of racing the scheduler's own transitions.
type seedingObserver struct {
	st     *store.Store
	log    *slog.Logger
	mu     sync.Mutex
	active map[string]bool // resourceID -> state successfully set to Seeding
}

func newSeedingObserver(st *store.Store, log *slog.Logger) *seedingObserver {
	return &seedingObserver{st: st, log: log, active: make(map[string]bool)}
}

func (o *seedingObserver) warn(msg string, err error, resourceID string) {
	if o.log != nil {
		o.log.Warn(msg, "resource", resourceID, "error", err)
	}
}

func (o *seedingObserver) SeedingStarted(_ context.Context, resourceID string) {
	o.mu.Lock()
	o.active[resourceID] = false
	o.mu.Unlock()
	if o.log != nil {
		o.log.Info("bt seeding session started", "resource", resourceID)
	}
}

func (o *seedingObserver) SeedingProgress(ctx context.Context, s bttransport.SeedingStats) {
	if o.st == nil {
		return
	}
	if err := o.st.UpdateSeedingStats(ctx, s.ResourceID, s.Uploaded); err != nil {
		o.warn("bt seeding stats persist failed", err, s.ResourceID)
	}
	o.mu.Lock()
	pending := !o.active[s.ResourceID]
	o.mu.Unlock()
	if !pending {
		return
	}
	changed, err := o.st.TrySetSeedingState(ctx, s.ResourceID, true)
	if err != nil {
		o.warn("bt seeding state transition failed", err, s.ResourceID)
		return
	}
	if changed {
		o.mu.Lock()
		o.active[s.ResourceID] = true
		o.mu.Unlock()
		if o.log != nil {
			o.log.Info("task entered Seeding", "resource", s.ResourceID)
		}
	}
}

func (o *seedingObserver) SeedingStopped(ctx context.Context, resourceID string, reason string) {
	o.mu.Lock()
	delete(o.active, resourceID)
	o.mu.Unlock()
	if o.st != nil {
		if _, err := o.st.TrySetSeedingState(ctx, resourceID, false); err != nil {
			o.warn("bt seeding state restore failed", err, resourceID)
		}
	}
	if o.log != nil {
		o.log.Info("bt seeding session stopped", "resource", resourceID, "reason", reason)
	}
}
