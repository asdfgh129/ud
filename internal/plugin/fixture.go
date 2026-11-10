package plugin

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/asdfgh129/ud/internal/core"
)

// Recorded-replay fixtures let `udctl plugin test` exercise a rule pack without
// ever connecting to a real site (docs/08 §8). A missing fixture URL yields a
// 404 rather than a dial attempt, so a stray URL is always visible as a test
// failure and never as network traffic.
type FixtureResponse struct {
	Status  int               `json:"status,omitempty"`
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type FixtureExpect struct {
	Mirrors []string `json:"mirrors,omitempty"`
	Files   []string `json:"files,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type FixtureCase struct {
	URL    string        `json:"url"`
	Expect FixtureExpect `json:"expect"`
}

type Fixture struct {
	Responses map[string]FixtureResponse `json:"responses"`
	Cases     []FixtureCase              `json:"cases"`
}

// ReplayDoer serves recorded responses and records every request URL.
type ReplayDoer struct {
	mu        sync.Mutex
	Responses map[string]FixtureResponse
	Requests  []string
}

func (d *ReplayDoer) Do(req *http.Request) (*http.Response, error) {
	key := req.URL.String()
	d.mu.Lock()
	d.Requests = append(d.Requests, key)
	resp, ok := d.Responses[key]
	d.mu.Unlock()
	if !ok {
		resp = FixtureResponse{Status: 404, Body: "fixture not found"}
	}
	status := resp.Status
	if status == 0 {
		status = 200
	}
	h := make(http.Header)
	for k, v := range resp.Headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d", status),
		Header: h, Body: io.NopCloser(strings.NewReader(resp.Body)),
		ContentLength: int64(len(resp.Body)), Request: req,
	}, nil
}

func (d *ReplayDoer) Seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.Requests...)
}

// replayGuard is deliberately test-only: it performs no DNS resolution, so
// fixtures can use realistic public hostnames offline. The production daemon
// always injects security.SSRFGuard. Host whitelisting is still enforced by
// httpGet / buildResource, so the replay path cannot silently widen a grant.
type replayGuard struct{}

func (replayGuard) Check(_ context.Context, raw string) error {
	if len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00") {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "malformed URL")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "only http/https URLs are allowed")
	}
	return nil
}
func (replayGuard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED, "replay fixtures must not dial the network")
}
func (replayGuard) MaxRedirects() int { return 3 }

type FixtureResult struct {
	URL     string `json:"url"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// RunFixture verifies the pack, compiles it with a replay Doer and runs every
// case, comparing mirrors / file names / expected error codes.
func RunFixture(dir, fixturePath string, keys []ed25519.PublicKey, requireSignature, insecure bool) ([]FixtureResult, error) {
	if _, err := VerifyPack(dir, keys, requireSignature, insecure); err != nil {
		return nil, err
	}
	m, script, _, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}
	if len(f.Cases) == 0 {
		return nil, core.Err(core.E_INVALID_INPUT, "fixture contains no cases")
	}
	doer := &ReplayDoer{Responses: f.Responses}
	rt, err := Compile(m, script, Engine{Guard: replayGuard{}, Doer: doer})
	if err != nil {
		return nil, err
	}
	results := make([]FixtureResult, 0, len(f.Cases))
	for _, c := range f.Cases {
		in, err := core.ParseInput(c.URL)
		if err != nil {
			results = append(results, FixtureResult{URL: c.URL, OK: false, Message: err.Error()})
			continue
		}
		res, err := rt.Resolve(core.ResolveContext{Ctx: context.Background()}, in)
		results = append(results, checkCase(c, res, err))
	}
	return results, nil
}

func checkCase(c FixtureCase, res *core.Resource, err error) FixtureResult {
	if c.Expect.Error != "" {
		if err == nil || !strings.Contains(err.Error(), c.Expect.Error) {
			return FixtureResult{URL: c.URL, Message: fmt.Sprintf("want error %q, got %v", c.Expect.Error, err)}
		}
		return FixtureResult{URL: c.URL, OK: true, Message: "expected error: " + err.Error()}
	}
	if err != nil {
		return FixtureResult{URL: c.URL, Message: err.Error()}
	}
	if len(c.Expect.Mirrors) > 0 {
		if len(res.Mirrors) != len(c.Expect.Mirrors) {
			return FixtureResult{URL: c.URL, Message: fmt.Sprintf("mirror count %d != %d", len(res.Mirrors), len(c.Expect.Mirrors))}
		}
		for i, want := range c.Expect.Mirrors {
			if res.Mirrors[i].URL != want {
				return FixtureResult{URL: c.URL, Message: fmt.Sprintf("mirror %d = %q, want %q", i, res.Mirrors[i].URL, want)}
			}
		}
	}
	if len(c.Expect.Files) > 0 {
		if len(res.Files) != len(c.Expect.Files) {
			return FixtureResult{URL: c.URL, Message: fmt.Sprintf("file count %d != %d", len(res.Files), len(c.Expect.Files))}
		}
		for i, want := range c.Expect.Files {
			if res.Files[i].Path != want {
				return FixtureResult{URL: c.URL, Message: fmt.Sprintf("file %d = %q, want %q", i, res.Files[i].Path, want)}
			}
		}
	}
	return FixtureResult{URL: c.URL, OK: true, Message: "OK"}
}
