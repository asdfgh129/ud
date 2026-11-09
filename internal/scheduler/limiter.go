package scheduler

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

// tokenBucket is a single refillable bucket. rate 0 means unlimited.
// Burst is clamped so a chunk larger than the burst is consumed in slices
// instead of deadlocking (docs/05 §6).
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rate core.BytesPerSec) *tokenBucket {
	b := &tokenBucket{last: time.Now()}
	b.setRate(rate)
	return b
}

func (b *tokenBucket) setRate(rate core.BytesPerSec) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rate = float64(rate)
	b.burst = math.Max(float64(rate), 64<<10)
	if b.burst > 4<<20 {
		b.burst = 4 << 20
	}
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = time.Now()
}

func (b *tokenBucket) consume(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	b.mu.Lock()
	unlimited := b.rate <= 0
	burst := b.burst
	b.mu.Unlock()
	if unlimited {
		return ctx.Err()
	}
	for remaining := n; remaining > 0; {
		step := remaining
		if float64(step) > burst {
			step = int(burst)
		}
		if err := b.wait(ctx, step); err != nil {
			return err
		}
		remaining -= step
	}
	return nil
}

func (b *tokenBucket) wait(ctx context.Context, n int) error {
	for {
		b.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(b.last).Seconds()
		b.last = now
		if b.rate > 0 {
			b.tokens = math.Min(b.burst, b.tokens+elapsed*b.rate)
		}
		if b.tokens >= float64(n) {
			b.tokens -= float64(n)
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((float64(n) - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

type multiBudget struct{ buckets []*tokenBucket }

func (m multiBudget) Consume(ctx context.Context, n int) error {
	for _, b := range m.buckets {
		if err := b.consume(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// Limiter is the ONLY component in the process that reads rate configuration
// (docs/04 §5.3 / docs/15 §2). Every Transport asks it for a Budget.
type Limiter struct {
	mu         sync.RWMutex
	globalDown *tokenBucket
	globalUp   *tokenBucket
	perTask    map[string]*tokenBucket
	perHost    map[string]*tokenBucket
	rate       core.BytesPerSec
}

func NewLimiter(cfg config.GlobalConfig) *Limiter {
	down, _ := config.ParseRate(cfg.MaxDownloadRate)
	up, _ := config.ParseRate(cfg.MaxUploadRate)
	return &Limiter{
		globalDown: newTokenBucket(core.BytesPerSec(down)),
		globalUp:   newTokenBucket(core.BytesPerSec(up)),
		perTask:    make(map[string]*tokenBucket),
		perHost:    make(map[string]*tokenBucket),
		rate:       core.BytesPerSec(down),
	}
}

// Apply is the config.Manager onChange hook.
func (l *Limiter) Apply(cfg config.GlobalConfig) {
	down, _ := config.ParseRate(cfg.MaxDownloadRate)
	up, _ := config.ParseRate(cfg.MaxUploadRate)
	l.mu.Lock()
	l.rate = core.BytesPerSec(down)
	l.mu.Unlock()
	l.globalDown.setRate(core.BytesPerSec(down))
	l.globalUp.setRate(core.BytesPerSec(up))
}

func (l *Limiter) SetGlobal(rate core.BytesPerSec) {
	l.mu.Lock()
	l.rate = rate
	l.mu.Unlock()
	l.globalDown.setRate(rate)
}

func (l *Limiter) GlobalRate() core.BytesPerSec {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.rate
}

// SetTaskRate installs the per-task budget from TaskOptions.MaxDownloadLimit.
func (l *Limiter) SetTaskRate(taskID string, rate core.BytesPerSec) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rate <= 0 {
		delete(l.perTask, taskID)
		return
	}
	b, ok := l.perTask[taskID]
	if !ok {
		b = newTokenBucket(rate)
		l.perTask[taskID] = b
	} else {
		b.setRate(rate)
	}
}

func (l *Limiter) ForgetTask(taskID string) {
	l.mu.Lock()
	delete(l.perTask, taskID)
	l.mu.Unlock()
}

func (l *Limiter) Acquire(ctx context.Context, scope core.Scope, _ int64) (core.Budget, error) {
	global := l.globalDown
	if scope.Direction == core.DirectionUpload {
		global = l.globalUp
	}
	b := multiBudget{buckets: []*tokenBucket{global}}
	l.mu.RLock()
	if scope.TaskID != "" {
		if tb := l.perTask[scope.TaskID]; tb != nil {
			b.buckets = append(b.buckets, tb)
		}
	}
	if scope.Host != "" {
		if hb := l.perHost[scope.Host]; hb != nil {
			b.buckets = append(b.buckets, hb)
		}
	}
	l.mu.RUnlock()
	return b, ctx.Err()
}
