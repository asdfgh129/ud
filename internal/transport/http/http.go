// Package httptransport is the P0 HTTP/HTTPS Range transport.
//
// It only moves bytes into a ChunkWriter. Chunk allocation, rate limiting,
// retries bookkeeping, state transitions and file layout all stay with the
// Scheduler/Store (ADR-0002, docs/05 §5).
package httptransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

const userAgent = "UniversalDownloader/0.1.0"

type Transport struct {
	guard  core.URLGuard
	client *http.Client
}

// httpClientProvider is an optional extension of core.URLGuard. Keeping it out
// of core avoids making the model package depend on net/http.
type httpClientProvider interface {
	NewHTTPClient(timeout time.Duration) *http.Client
}

func New(g core.URLGuard) *Transport {
	var client *http.Client
	if p, ok := g.(httpClientProvider); ok {
		client = p.NewHTTPClient(0)
	} else {
		client = &http.Client{
			Transport: &http.Transport{DialContext: g.DialContext},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= g.MaxRedirects() {
					return core.Err(core.E_RESOLVE_FAILED, "redirect limit exceeded")
				}
				return g.Check(req.Context(), req.URL.String())
			},
		}
	}
	return &Transport{guard: g, client: client}
}

func (t *Transport) Name() string { return "http" }

func (t *Transport) Supports(r *core.Resource) bool {
	for _, m := range r.Mirrors {
		s := core.SchemeOf(m.URL)
		if s == "http" || s == "https" {
			return true
		}
	}
	return false
}

// Probe learns size / Range capability / filename. A HEAD is attempted first,
// with a ranged GET fallback for servers that reject HEAD.
func (t *Transport) Probe(ctx context.Context, r *core.Resource) (*core.ProbeResult, error) {
	if len(r.Mirrors) == 0 {
		return nil, core.Err(core.E_RESOLVE_FAILED, "resource has no mirror")
	}
	raw := r.Mirrors[0].URL
	if t.guard != nil {
		if err := t.guard.Check(ctx, raw); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "invalid URL: %v", err)
	}
	t.applyHeaders(req, r.Headers)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, classifyNetwork(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 || resp.StatusCode == 405 {
		return t.probeByGET(ctx, r, raw)
	}
	res := &core.ProbeResult{
		Size:         resp.ContentLength,
		AcceptRanges: strings.Contains(strings.ToLower(resp.Header.Get("Accept-Ranges")), "bytes"),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Filename:     filenameFromDisposition(resp.Header.Get("Content-Disposition")),
	}
	if res.Size < 0 {
		res.Size = 0
	}
	return res, nil
}

func (t *Transport) probeByGET(ctx context.Context, r *core.Resource, raw string) (*core.ProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "invalid URL: %v", err)
	}
	t.applyHeaders(req, r.Headers)
	req.Header.Set("Range", "bytes=0-0")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, classifyNetwork(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
	if resp.StatusCode >= 400 {
		return nil, classifyStatus(resp.StatusCode)
	}
	res := &core.ProbeResult{
		AcceptRanges: resp.StatusCode == http.StatusPartialContent,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Filename:     filenameFromDisposition(resp.Header.Get("Content-Disposition")),
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if _, total, ok := parseContentRange(cr); ok {
			res.Size = total
			return res, nil
		}
	}
	res.Size = resp.ContentLength
	if res.Size < 0 {
		res.Size = 0
	}
	return res, nil
}

// Run drains the authoritative chunk plan from the ChunkWriter.
func (t *Transport) Run(ctx context.Context, r *core.Resource, w core.ChunkWriter, b core.Budget) error {
	if len(r.Mirrors) == 0 {
		return core.Err(core.E_RESOLVE_FAILED, "resource has no mirror")
	}
	probe, err := t.Probe(ctx, r)
	if err != nil {
		return err
	}
	if !probe.AcceptRanges {
		return t.runNoRange(ctx, r, w, b)
	}
	conns := r.Limits.MaxConnections
	if conns < 1 {
		conns = 1
	}
	sem := make(chan struct{}, conns)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	// A chunk that fails once is released back to pending and retried. Record
	// the outcome of the *latest* attempt per chunk: an older attempt that
	// reports its error after a newer attempt already succeeded must not poison
	// an otherwise successful download.
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
	// A writer can report "no pending chunks" while in-flight chunks are about
	// to be released back for retry. Drain the current wave before deciding the
	// plan is complete, otherwise transient failures never get a second chance.
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
			if err := sleepBackoff(ctx, c.Attempt); err != nil {
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
			err := t.fetchChunk(ctx, r, c, w, b)
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

func (t *Transport) fetchChunk(ctx context.Context, r *core.Resource, c core.Chunk,
	w core.ChunkWriter, b core.Budget) error {
	idx := (c.Attempt - 1) % len(r.Mirrors)
	if idx < 0 {
		idx = 0
	}
	mirror := r.Mirrors[idx]
	if t.guard != nil {
		if err := t.guard.Check(ctx, mirror.URL); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mirror.URL, nil)
	if err != nil {
		return core.Err(core.E_INVALID_INPUT, "invalid mirror URL: %v", err)
	}
	t.applyHeaders(req, r.Headers)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", c.Start, c.End()))
	resp, err := t.client.Do(req)
	if err != nil {
		return classifyNetwork(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return classifyStatus(resp.StatusCode)
	}
	if cl := resp.ContentLength; cl > 0 && cl != c.Length {
		return core.Err(core.E_TRANSIENT_NETWORK,
			"range length mismatch: want %d got %d", c.Length, cl)
	}
	buf := make([]byte, c.Length)
	if err := readWithBudget(ctx, resp.Body, buf, b); err != nil {
		return err
	}
	if err := w.WriteChunk(ctx, c, buf); err != nil {
		return err
	}
	return nil
}

// runNoRange handles servers without Range support: one connection streams the
// whole body, sliced into the already-claimed chunks (UC-01 fallback).
func (t *Transport) runNoRange(ctx context.Context, r *core.Resource, w core.ChunkWriter, b core.Budget) error {
	var chunks []core.Chunk
	for {
		c, ok, err := w.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		chunks = append(chunks, c)
	}
	if len(chunks) == 0 {
		return nil
	}
	raw := r.Mirrors[0].URL
	if t.guard != nil {
		if err := t.guard.Check(ctx, raw); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	t.applyHeaders(req, r.Headers)
	resp, err := t.client.Do(req)
	if err != nil {
		return classifyNetwork(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return classifyStatus(resp.StatusCode)
	}
	for _, c := range chunks {
		buf := make([]byte, c.Length)
		if err := readWithBudget(ctx, resp.Body, buf, b); err != nil {
			_ = w.Release(ctx, c, err)
			return err
		}
		if err := w.WriteChunk(ctx, c, buf); err != nil {
			return err
		}
	}
	return nil
}

func (t *Transport) applyHeaders(req *http.Request, headers map[string]string) {
	req.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

// readWithBudget fills dst and charges every byte to the one global Limiter.
func readWithBudget(ctx context.Context, src io.Reader, dst []byte, b core.Budget) error {
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
				return core.Err(core.E_TRANSIENT_NETWORK, "response body truncated at %d/%d bytes", off, len(dst))
			}
			return classifyNetwork(err)
		}
	}
	return nil
}

func classifyNetwork(err error) error {
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

func classifyStatus(code int) error {
	switch {
	case code == 429:
		return core.Err(core.E_FLOOD_WAIT, "server rate limited the request (HTTP 429)")
	case code >= 500, code == 408:
		return core.Err(core.E_TRANSIENT_NETWORK, "server returned HTTP %d", code)
	case code == 416, code == 404, code == 403, code == 401:
		return core.Err(core.E_RESOLVE_FAILED, "server returned HTTP %d", code)
	default:
		return core.Err(core.E_TRANSIENT_NETWORK, "unexpected HTTP status %d", code)
	}
}

// sleepBackoff implements min(base*2^n, cap) + jitter (docs/05 §7).
func sleepBackoff(ctx context.Context, attempt int) error {
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

func parseContentRange(v string) (start, total int64, ok bool) {
	// RFC 9110: Content-Range: bytes 0-0/146515
	if !strings.HasPrefix(v, "bytes ") {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(v, "bytes "), "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	total, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || total < 0 {
		return 0, 0, false
	}
	span := strings.SplitN(parts[0], "-", 2)
	if len(span) != 2 {
		return 0, 0, false
	}
	start, err = strconv.ParseInt(span[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return start, total, true
}

func filenameFromDisposition(v string) string {
	// Minimal RFC 6266 parser: filename*="UTF-8''x" then filename="x".
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, "filename*=") {
			if i := strings.Index(part, "''"); i > 0 {
				if s, err := neturl.PathUnescape(part[i+2:]); err == nil {
					return sanitizeFilename(s)
				}
			}
		}
		if strings.HasPrefix(lower, "filename=") {
			return sanitizeFilename(strings.Trim(strings.TrimSpace(part[len("filename="):]), `"`))
		}
	}
	return ""
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" || s == "." || s == ".." {
		return ""
	}
	if len(s) > 200 {
		s = s[len(s)-200:]
	}
	return s
}
