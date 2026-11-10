package tputil

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

// MaxSessionConnections caps concurrent network sessions for the P1 file
// transports. Each chunk opens its own authenticated session (see the WBS 9
// delivery boundary in docs/14 §11), so an unbounded MaxConnections would mean
// one TCP + TLS + login handshake per chunk per connection. HTTP keeps its own
// connection-pooling behaviour in internal/transport/http.
const MaxSessionConnections = 8

// FetchFunc moves exactly one chunk. The transport owns session lifetime;
// RunChunks owns the chunk plan, concurrency, retries and error aggregation.
type FetchFunc func(ctx context.Context, c core.Chunk, mirror core.Mirror) error

// RunChunks drains the authoritative chunk plan from the Scheduler-owned
// ChunkWriter. A transport NEVER invents chunk boundaries or writes task state.
// The retry-wave logic mirrors internal/transport/http: a chunk released back
// to pending by an in-flight attempt must get another turn in the same run.
func RunChunks(ctx context.Context, r *core.Resource, w core.ChunkWriter, fetch FetchFunc) error {
	if len(r.Mirrors) == 0 {
		return core.Err(core.E_RESOLVE_FAILED, "resource has no mirror")
	}
	conns := r.Limits.MaxConnections
	if conns < 1 {
		conns = 1
	}
	if conns > MaxSessionConnections {
		conns = MaxSessionConnections
	}
	sem := make(chan struct{}, conns)
	var wg sync.WaitGroup
	var errMu sync.Mutex

	// Record the outcome of the *latest* attempt per chunk: an older attempt
	// reporting its error after a newer attempt succeeded must not poison an
	// otherwise successful download.
	type outcome struct {
		attempt int
		err     error
	}
	outcomes := make(map[int]outcome)
	record := func(c core.Chunk, e error) {
		errMu.Lock()
		defer errMu.Unlock()
		if prev, ok := outcomes[c.Index]; ok && c.Attempt < prev.attempt {
			return
		}
		outcomes[c.Index] = outcome{attempt: c.Attempt, err: e}
	}
	claim := func() (core.Chunk, bool, error) {
		c, ok, err := w.Next(ctx)
		if err != nil || ok {
			return c, ok, err
		}
		wg.Wait()
		return w.Next(ctx)
	}

	var loopErr error
	for {
		if ctx.Err() != nil {
			loopErr = ctx.Err()
			break
		}
		c, ok, err := claim()
		if err != nil {
			loopErr = err
			break
		}
		if !ok {
			break
		}
		if c.Attempt > 1 {
			if err := SleepBackoff(ctx, c.Attempt); err != nil {
				_ = w.Release(ctx, c, err)
				loopErr = err
				break
			}
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			_ = w.Release(ctx, c, ctx.Err())
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func(c core.Chunk) {
			defer wg.Done()
			defer func() { <-sem }()
			mirror := r.Mirrors[(c.Attempt-1)%len(r.Mirrors)]
			err := fetch(ctx, c, mirror)
			if err != nil {
				if relErr := w.Release(ctx, c, err); relErr != nil {
					err = relErr
				}
			}
			record(c, err)
		}(c)
	}
	wg.Wait()
	errMu.Lock()
	defer errMu.Unlock()
	if loopErr != nil {
		return loopErr
	}
	for _, o := range outcomes {
		if o.err != nil {
			return o.err
		}
	}
	return nil
}

// ReadWithBudget fills dst and charges every byte to the one global Limiter.
func ReadWithBudget(ctx context.Context, src io.Reader, dst []byte, b core.Budget) error {
	const block = 32 << 10
	for off := 0; off < len(dst); {
		n := len(dst) - off
		if n > block {
			n = block
		}
		if b != nil {
			if err := b.Consume(ctx, n); err != nil {
				return err
			}
		}
		got, err := io.ReadFull(src, dst[off:off+n])
		off += got
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return core.Err(core.E_TRANSIENT_NETWORK,
					"response body truncated at %d/%d bytes", off, len(dst))
			}
			return ClassifyNetwork(err)
		}
	}
	return nil
}

// ClassifyNetwork keeps core errors and context cancellation intact and maps
// everything else to a retryable transport error.
func ClassifyNetwork(err error) error {
	if err == nil {
		return nil
	}
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return core.Err(core.E_TRANSIENT_NETWORK, "network error: %s", err.Error())
}

// SleepBackoff implements min(base*2^n, cap) + jitter (docs/05 §7).
func SleepBackoff(ctx context.Context, attempt int) error {
	if attempt < 2 {
		return nil
	}
	d := time.Duration(100*(1<<min(attempt-1, 5))) * time.Millisecond
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	d += time.Duration(rand.Intn(50)) * time.Millisecond
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
