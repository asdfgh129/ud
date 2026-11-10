package httptransport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

// coreOnlyGuard implements core.URLGuard without the optional NewHTTPClient
// extension, forcing Transport.New to build its own client (the redirect path).
type coreOnlyGuard struct {
	mu      sync.Mutex
	checked []string
	block   string
	max     int
}

func (g *coreOnlyGuard) Check(_ context.Context, raw string) error {
	g.mu.Lock()
	g.checked = append(g.checked, raw)
	g.mu.Unlock()
	if g.block != "" && strings.Contains(raw, g.block) {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "blocked by test guard")
	}
	return nil
}

func (g *coreOnlyGuard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

func (g *coreOnlyGuard) MaxRedirects() int { return g.max }

func TestNameAndSupports(t *testing.T) {
	tr := New(&coreOnlyGuard{max: 3})
	if tr.Name() != "http" {
		t.Fatalf("unexpected name %q", tr.Name())
	}
	cases := []struct {
		urls []string
		want bool
	}{
		{[]string{"https://x/a"}, true},
		{[]string{"ftp://x/a", "http://x/a"}, true},
		{[]string{"ftp://x/a"}, false},
		{nil, false},
	}
	for _, tc := range cases {
		r := &core.Resource{}
		for _, u := range tc.urls {
			r.Mirrors = append(r.Mirrors, core.Mirror{URL: u})
		}
		if got := tr.Supports(r); got != tc.want {
			t.Errorf("Supports(%v)=%v want %v", tc.urls, got, tc.want)
		}
	}
}

func TestProbeReadsMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Length", "1234")
		h.Set("Accept-Ranges", "bytes")
		h.Set("ETag", `"v1"`)
		h.Set("Last-Modified", "Mon, 09 Nov 2026 10:00:00 GMT")
		h.Set("Content-Disposition", `attachment; filename*=UTF-8''%E4%B8%AD%E6%96%87.bin`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	res, err := New(&coreOnlyGuard{max: 3}).Probe(context.Background(),
		&core.Resource{Mirrors: []core.Mirror{{URL: srv.URL + "/f.bin"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Size != 1234 || !res.AcceptRanges || res.ETag != `"v1"` ||
		res.LastModified == "" || res.Filename != "中文.bin" {
		t.Fatalf("unexpected probe result: %+v", res)
	}
}

// Some servers reject HEAD; Probe must retry with a one-byte ranged GET.
func TestProbeFallsBackToRangedGET(t *testing.T) {
	var sawRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		sawRange = r.Header.Get("Range")
		w.Header().Set("Content-Range", "bytes 0-0/98765")
		w.Header().Set("Content-Disposition", `attachment; filename="fallback.iso"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	res, err := New(&coreOnlyGuard{max: 3}).Probe(context.Background(),
		&core.Resource{Mirrors: []core.Mirror{{URL: srv.URL + "/f.bin"}}})
	if err != nil {
		t.Fatal(err)
	}
	if sawRange != "bytes=0-0" {
		t.Fatalf("probe GET must send a one-byte range, got %q", sawRange)
	}
	if res.Size != 98765 || !res.AcceptRanges || res.Filename != "fallback.iso" {
		t.Fatalf("unexpected fallback probe: %+v", res)
	}
}

// A 200 answer to a ranged GET means the origin ignored Range.
func TestProbeGETWithoutRangeSupport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", "42")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	res, err := New(&coreOnlyGuard{max: 3}).Probe(context.Background(),
		&core.Resource{Mirrors: []core.Mirror{{URL: srv.URL + "/f.bin"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.AcceptRanges || res.Size != 42 {
		t.Fatalf("expected non-range probe with size 42, got %+v", res)
	}
}

func TestProbeErrors(t *testing.T) {
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv404.Close()
	tr := New(&coreOnlyGuard{max: 3})

	if _, err := tr.Probe(context.Background(), &core.Resource{}); err == nil {
		t.Fatal("resource without mirrors must fail")
	}
	if _, err := tr.Probe(context.Background(), &core.Resource{
		Mirrors: []core.Mirror{{URL: srv404.URL + "/f"}}}); err == nil ||
		core.AsError(err).Code != core.E_RESOLVE_FAILED {
		t.Fatalf("want E_RESOLVE_FAILED for 404, got %v", err)
	}
	if _, err := tr.Probe(context.Background(), &core.Resource{
		Mirrors: []core.Mirror{{URL: "http://[::1/x"}}}); err == nil {
		t.Fatal("malformed URL must fail")
	}
}

func TestProbeAppliesCustomHeaders(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Referer")
		if r.Header.Get("User-Agent") != userAgent {
			t.Errorf("missing default UA: %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Length", "0")
	}))
	defer srv.Close()
	if _, err := New(&coreOnlyGuard{max: 3}).Probe(context.Background(), &core.Resource{
		Mirrors: []core.Mirror{{URL: srv.URL + "/f"}},
		Headers: map[string]string{"Referer": "https://example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com" {
		t.Fatalf("custom header not forwarded, got %q", got)
	}
}

// The redirect limit and per-hop guard are only wired on the fallback client.
func TestFallbackClientEnforcesRedirectLimit(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()
	_, err := New(&coreOnlyGuard{max: 2}).Probe(context.Background(),
		&core.Resource{Mirrors: []core.Mirror{{URL: srv.URL + "/loop"}}})
	if err == nil || !strings.Contains(core.AsError(err).Message, "redirect limit") {
		t.Fatalf("want redirect limit error, got %v", err)
	}
	if hits > 4 {
		t.Fatalf("redirect limit not enforced, %d requests", hits)
	}
}

func TestFallbackClientChecksRedirectTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.Header().Set("Content-Length", "0")
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer srv.Close()
	g := &coreOnlyGuard{max: 3, block: "/final"}
	_, err := New(g).Probe(context.Background(),
		&core.Resource{Mirrors: []core.Mirror{{URL: srv.URL + "/start"}}})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("redirect target must be re-validated, got %v", err)
	}
}

// Pause/cancel must surface context cancellation, not a fake network error.
func TestRunStopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "1048576")
			w.Header().Set("Accept-Ranges", "bytes")
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r := &core.Resource{ID: "r", Mirrors: []core.Mirror{{URL: srv.URL + "/f"}},
		Limits: core.TaskLimits{MaxConnections: 2}}
	w := newMemWriter(1<<20, 512<<10, 3)
	err := New(&coreOnlyGuard{max: 3}).Run(ctx, r, w, nil)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestRunRequiresMirror(t *testing.T) {
	if err := New(&coreOnlyGuard{max: 3}).Run(context.Background(), &core.Resource{},
		newMemWriter(0, 1, 1), nil); err == nil {
		t.Fatal("run without mirrors must fail")
	}
}

func TestParseContentRange(t *testing.T) {
	cases := []struct {
		in    string
		size  int64
		ok    bool
		start int64
	}{
		{"bytes 0-0/146515", 146515, true, 0},
		{"bytes 10-20/100", 100, true, 10},
		{"bytes 0-0/*", 0, false, 0},
		{"items 0-0/10", 0, false, 0},
		{"bytes 0-0", 0, false, 0},
		{"bytes x-0/10", 0, false, 0},
		{"bytes 0/10", 0, false, 0},
	}
	for _, tc := range cases {
		start, size, ok := parseContentRange(tc.in)
		if ok != tc.ok || size != tc.size || start != tc.start {
			t.Errorf("parseContentRange(%q) = (%d,%d,%v), want (%d,%d,%v)",
				tc.in, start, size, ok, tc.start, tc.size, tc.ok)
		}
	}
}

func TestFilenameFromDisposition(t *testing.T) {
	cases := map[string]string{
		`attachment; filename="a.iso"`:                            "a.iso",
		`attachment; filename*=UTF-8''%E4%B8%AD.bin`:              "中.bin",
		`attachment; filename="../../etc/passwd"`:                 ".._.._etc_passwd",
		`attachment; filename="."`:                                "",
		`attachment; filename=".."`:                               "",
		`attachment; filename*=UTF-8''broken%ZZ`:                  "",
		`inline`:                                                  "",
		`attachment; filename="` + strings.Repeat("x", 250) + `"`: strings.Repeat("x", 200),
	}
	for in, want := range cases {
		if got := filenameFromDisposition(in); got != want {
			t.Errorf("filenameFromDisposition(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename("  a/b\\c.bin  "); got != "a_b_c.bin" {
		t.Fatalf("unexpected sanitize result %q", got)
	}
	if got := sanitizeFilename("   "); got != "" {
		t.Fatalf("blank name must be dropped, got %q", got)
	}
}

func TestClassifyNetwork(t *testing.T) {
	if classifyNetwork(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	classified := core.Err(core.E_FLOOD_WAIT, "already classified")
	if got := classifyNetwork(classified); got != error(classified) {
		t.Fatalf("classified error must pass through, got %v", got)
	}
	if got := classifyNetwork(context.Canceled); !errors.Is(got, context.Canceled) {
		t.Fatalf("cancellation must pass through, got %v", got)
	}
	got := classifyNetwork(errors.New("dial tcp: no route"))
	if core.AsError(got).Code != core.E_TRANSIENT_NETWORK {
		t.Fatalf("want E_TRANSIENT_NETWORK, got %v", got)
	}
}

func TestReadWithBudgetRejectsTruncatedBody(t *testing.T) {
	dst := make([]byte, 8)
	err := readWithBudget(context.Background(), iotest.TimeoutReader(io.LimitReader(strings.NewReader("abc"), 3)), dst, nil)
	if core.AsError(err).Code != core.E_TRANSIENT_NETWORK {
		t.Fatalf("want E_TRANSIENT_NETWORK, got %v", err)
	}
	// A budget error must be surfaced unchanged.
	budgetErr := core.Err(core.E_FLOOD_WAIT, "slow down")
	if err := readWithBudget(context.Background(), strings.NewReader("abcd"), dst, failingBudget{budgetErr}); err != budgetErr {
		t.Fatalf("budget error must propagate, got %v", err)
	}
	if err := readWithBudget(context.Background(), strings.NewReader("abcdefgh"), dst, nil); err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
}

type failingBudget struct{ err error }

func (b failingBudget) Consume(context.Context, int) error { return b.err }
