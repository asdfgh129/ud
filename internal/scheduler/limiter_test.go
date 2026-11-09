package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

// T-FR-TRN-09-01: one global token bucket; a per-task limit can only ever
// make a task SLOWER, never bypass or double the global rate.
func TestLimiterSingleAuthority(t *testing.T) {
	l := NewLimiter(config.GlobalConfig{MaxDownloadRate: "0", MaxUploadRate: "0"})
	if l.GlobalRate() != 0 {
		t.Fatalf("unlimited rate expected, got %d", l.GlobalRate())
	}
	b, err := l.Acquire(context.Background(),
		core.Scope{TaskID: "t1", Host: "example.com", Direction: core.DirectionDownload}, 0)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := b.Consume(context.Background(), 8<<20); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("unlimited limiter blocked the caller")
	}

	l.SetGlobal(core.BytesPerSec(1 << 20))
	if l.GlobalRate() != 1<<20 {
		t.Fatalf("SetGlobal failed: %d", l.GlobalRate())
	}
	l.SetTaskRate("t1", core.BytesPerSec(512<<10))
	b2, _ := l.Acquire(context.Background(),
		core.Scope{TaskID: "t1", Direction: core.DirectionDownload}, 0)
	start = time.Now()
	if err := b2.Consume(context.Background(), 256<<10); err != nil {
		t.Fatal(err)
	}
	// 256KiB at 512KiB/s costs >= 0.4s once the initial burst is spent.
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("per-task limit was not enforced (elapsed=%v)", elapsed)
	}
}

func TestLimiterRespectsContextCancellation(t *testing.T) {
	l := NewLimiter(config.GlobalConfig{MaxDownloadRate: "1KiB/s", MaxUploadRate: "0"})
	b, _ := l.Acquire(context.Background(), core.Scope{Direction: core.DirectionDownload}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := b.Consume(ctx, 64<<10); err == nil {
		t.Fatal("budget consumption should honour context cancellation")
	}
}

func TestDynamicSlots(t *testing.T) {
	s := newSlots(1)
	ctx := context.Background()
	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{})
	go func() {
		_ = s.acquire(ctx)
		close(ch)
	}()
	select {
	case <-ch:
		t.Fatal("second slot acquired while max=1")
	case <-time.After(30 * time.Millisecond):
	}
	s.setMax(2)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("setMax did not wake the waiting worker")
	}
	s.release()
	s.release()
}
