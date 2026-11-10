package plugin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// clientGuard exercises the httpClientProvider path: production builds its
// HTTP client from the real SSRF Guard, but here the transport is an offline
// fixture so no socket is ever opened.
type clientGuard struct {
	blockGuard
	rt http.RoundTripper
}

func (g clientGuard) NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: g.rt, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func fixtureResponse(status int, body string, headers map[string]string) *http.Response {
	h := make(http.Header)
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status, Status: "status", Header: h,
		Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)),
	}
}

func TestDefaultGuardClientAndManualRedirects(t *testing.T) {
	var requested []string
	g := clientGuard{blockGuard: blockGuard{}, rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.String())
		switch req.URL.String() {
		case "https://example.com/video/1":
			return fixtureResponse(302, "", map[string]string{"Location": "https://cdn.example.com/final.html"}), nil
		default:
			return fixtureResponse(200, `{"playUrl":"https%3A%2F%2Fcdn.example.com%2Fv.mp4"}`, nil), nil
		}
	})}
	m := baseManifest()
	rt, err := Compile(&m, []byte(happyScript), Engine{Guard: g})
	if err != nil {
		t.Fatal(err)
	}
	res, err := resolveRaw(t, rt, "https://example.com/video/1", g)
	if err != nil {
		t.Fatalf("guard client path: %v", err)
	}
	if len(requested) != 2 || requested[1] != "https://cdn.example.com/final.html" {
		t.Fatalf("redirect was not followed hop-by-hop: %v", requested)
	}
	if res.Mirrors[0].URL != "https://cdn.example.com/v.mp4" {
		t.Fatalf("bad mirror: %+v", res.Mirrors)
	}
}

func TestGuardBlocksProducedMirrorEvenWhenWhitelisted(t *testing.T) {
	m := baseManifest()
	m.Capabilities.HTTP.AllowedHosts = []string{"example.com", "127.0.0.1"}
	rt := newRuntime(t, m,
		`export function resolve() { return { files:[{path:"a"}], mirrors:[{url:"http://127.0.0.1/secret"}] }; }`,
		happyDoer(), blockGuard{})
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err == nil || core.AsError(err).Code != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("SSRF Guard must have the final say on produced URLs: %v", err)
	}
}

func TestTooManyRequestHeadersAndMissingURL(t *testing.T) {
	m := baseManifest()
	script := `export function resolve(url, ctx) {
  var h = {};
  for (var i = 0; i < 40; i++) { h["X-" + i] = "v"; }
  ctx.http.get(url, { headers: h });
  return { files:[{path:"a"}], mirrors:[{url:"https://cdn.example.com/a"}] };
}
`
	rt := newRuntime(t, m, script, happyDoer(), blockGuard{})
	_, err := resolveRaw(t, rt, "https://example.com/video/1", blockGuard{})
	if err == nil || !strings.Contains(err.Error(), "too many request headers") {
		t.Fatalf("header count limit not enforced: %v", err)
	}
}

func TestReplayDoerMissingURLAndSeen(t *testing.T) {
	d := &ReplayDoer{Responses: map[string]FixtureResponse{
		"https://example.com/1": {Body: "ok"},
	}}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/1", nil)
	resp, err := d.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("recorded response: %v %+v", err, resp)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Fatalf("body=%q", b)
	}
	req2, _ := http.NewRequest(http.MethodGet, "https://example.com/missing", nil)
	resp2, err := d.Do(req2)
	if err != nil || resp2.StatusCode != 404 {
		t.Fatalf("missing URL must produce 404, not a dial: %v %+v", err, resp2)
	}
	if len(d.Seen()) != 2 || d.Seen()[0] != "https://example.com/1" {
		t.Fatalf("Seen()=%v", d.Seen())
	}
	// Custom status/headers survive the replay.
	d.Responses["https://example.com/h"] = FixtureResponse{Status: 201, Headers: map[string]string{"X-A": "b"}}
	req3, _ := http.NewRequest(http.MethodGet, "https://example.com/h", nil)
	resp3, _ := d.Do(req3)
	if resp3.StatusCode != 201 || resp3.Header.Get("X-A") != "b" {
		t.Fatalf("custom fixture not honoured: %+v", resp3)
	}
}

func TestIsRedirectMatrix(t *testing.T) {
	for _, s := range []int{301, 302, 303, 307, 308} {
		if !isRedirect(s) {
			t.Errorf("%d should be a redirect", s)
		}
	}
	for _, s := range []int{200, 304, 400, 500} {
		if isRedirect(s) {
			t.Errorf("%d should not be a redirect", s)
		}
	}
}

func TestHostStateSpendAndFailKeepFirstError(t *testing.T) {
	h := &hostState{budget: 10, maxBody: 100}
	if !h.spend(6) || h.spend(5) || h.spend(-1) {
		t.Fatal("memory budget accounting is wrong")
	}
	h.fail(core.Err(core.E_INVALID_INPUT, "first"))
	h.fail(core.Err(core.E_INVALID_INPUT, "second"))
	if !strings.Contains(h.err.Error(), "first") {
		t.Fatal("first denial must win")
	}
}

func TestFixtureResponseJSONShape(t *testing.T) {
	b, err := json.Marshal(FixtureResponse{Status: 200, Body: "x"})
	if err != nil || !strings.Contains(string(b), `"status"`) {
		t.Fatalf("fixture JSON: %v %s", err, b)
	}
}
