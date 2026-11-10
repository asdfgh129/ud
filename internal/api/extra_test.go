package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
)

// degradedControl overrides Health to exercise the 503 branch of /healthz.
type degradedControl struct{ *fakeControl }

func (d degradedControl) Health(context.Context) core.Health {
	return core.Health{Status: "degraded", DB: false, Limiter: true, Sidecars: map[string]string{}}
}

func TestListTasksEndpoint(t *testing.T) {
	ts, _, ctl := newTestServer(t)
	id, _ := ctl.Create(context.Background(), core.Input{Raw: "https://x/a", Kind: core.InputURL}, core.TaskOptions{})
	code, body := request(t, "GET", ts.URL+"/api/v1/tasks?limit=5&state=Pending&kind=url&cursor=", "")
	if code != 200 {
		t.Fatalf("list: %d %v", code, body)
	}
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 || body["total"] != float64(1) {
		t.Fatalf("unexpected list body: %v", body)
	}
	if body["nextCursor"] != string(id) {
		t.Fatalf("nextCursor=%v want %v", body["nextCursor"], id)
	}
}

func TestListTasksEmptyCursor(t *testing.T) {
	ts, _, _ := newTestServer(t)
	code, body := request(t, "GET", ts.URL+"/api/v1/tasks", "")
	if code != 200 || body["nextCursor"] != "" {
		t.Fatalf("empty list: %d %v", code, body)
	}
}

func TestConfigReadAndPatch(t *testing.T) {
	ts, bus, _ := newTestServer(t)
	events, cancel := bus.Subscribe()
	defer cancel()

	code, body := request(t, "GET", ts.URL+"/api/v1/config", "")
	if code != 200 || body["listen"] != "127.0.0.1:0" {
		t.Fatalf("get config: %d %v", code, body)
	}
	code, body = request(t, "PATCH", ts.URL+"/api/v1/config", `{"global.maxConcurrentTasks":3}`)
	if code != 200 {
		t.Fatalf("patch config: %d %v", code, body)
	}
	select {
	case e := <-events:
		if e.Type != core.EventConfigChanged {
			t.Fatalf("expected config.changed event, got %s", e.Type)
		}
	default:
		t.Fatal("config patch must publish an event")
	}
	code, body = request(t, "PATCH", ts.URL+"/api/v1/config", `{"nope":"x"}`)
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("unsupported key: %d %v", code, body)
	}
	code, body = request(t, "PATCH", ts.URL+"/api/v1/config", `{`)
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("bad json: %d %v", code, body)
	}
}

func TestStatsAndHealth(t *testing.T) {
	ts, _, ctl := newTestServer(t)
	if _, err := ctl.Create(context.Background(), core.Input{Raw: "https://x/a", Kind: core.InputURL}, core.TaskOptions{}); err != nil {
		t.Fatal(err)
	}
	code, body := request(t, "GET", ts.URL+"/api/v1/stats", "")
	if code != 200 || body["tasks"] != float64(1) {
		t.Fatalf("stats: %d %v", code, body)
	}
	code, _ = request(t, "GET", ts.URL+"/api/v1/healthz", "")
	if code != 200 {
		t.Fatalf("healthy daemon must answer 200, got %d", code)
	}

	// A degraded backend must surface as 503, never 200.
	bus := core.NewEventBus(8)
	srv := New(Options{Control: degradedControl{newFakeControl()},
		Config: config.NewManager(config.Defaults(), nil), Bus: bus})
	ts2 := httptest.NewServer(srv)
	defer ts2.Close()
	code, body = request(t, "GET", ts2.URL+"/api/v1/healthz", "")
	if code != 503 || body["status"] != "degraded" {
		t.Fatalf("degraded health: %d %v", code, body)
	}
}

func TestDeleteResumeCancelEndpoints(t *testing.T) {
	ts, _, ctl := newTestServer(t)
	id, _ := ctl.Create(context.Background(), core.Input{Raw: "https://x/a", Kind: core.InputURL}, core.TaskOptions{})

	code, _ := request(t, "POST", ts.URL+"/api/v1/tasks/"+string(id)+"/pause", "")
	if code != 200 {
		t.Fatalf("pause: %d", code)
	}
	code, body := request(t, "POST", ts.URL+"/api/v1/tasks/"+string(id)+"/resume", "")
	if code != 200 || body["accepted"] != true {
		t.Fatalf("resume: %d %v", code, body)
	}
	code, _ = request(t, "POST", ts.URL+"/api/v1/tasks/"+string(id)+"/cancel", "")
	if code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	code, _ = request(t, "DELETE", ts.URL+"/api/v1/tasks/"+string(id), "")
	if code != 204 {
		t.Fatalf("delete: %d", code)
	}
	code, body = request(t, "DELETE", ts.URL+"/api/v1/tasks/"+string(id), "")
	if code != 404 || errCode(body) != core.E_TASK_NOT_FOUND {
		t.Fatalf("delete missing: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks/unknown/resume", "")
	if code != 404 || errCode(body) != core.E_TASK_NOT_FOUND {
		t.Fatalf("resume missing: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks/unknown/cancel", "")
	if code != 404 || errCode(body) != core.E_TASK_NOT_FOUND {
		t.Fatalf("cancel missing: %d %v", code, body)
	}
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if ctl.calls["resume"] < 1 || ctl.calls["cancel"] < 1 {
		t.Fatalf("unexpected control calls: %+v", ctl.calls)
	}
}

func TestPatchTaskEndpoint(t *testing.T) {
	ts, _, ctl := newTestServer(t)
	id, _ := ctl.Create(context.Background(), core.Input{Raw: "https://x/a", Kind: core.InputURL}, core.TaskOptions{})
	code, body := request(t, "PATCH", ts.URL+"/api/v1/tasks/"+string(id), `{"maxConnections":4}`)
	if code != 200 || body["id"] != string(id) {
		t.Fatalf("patch: %d %v", code, body)
	}
	code, body = request(t, "PATCH", ts.URL+"/api/v1/tasks/"+string(id), `{`)
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("patch bad json: %d %v", code, body)
	}
}

func TestBatchCreateAndActions(t *testing.T) {
	ts, _, _ := newTestServer(t)
	code, body := request(t, "POST", ts.URL+"/api/v1/tasks/batch",
		`{"inputs":["https://x/a","https://x/b"],"action":"add"}`)
	if code != 201 || body["count"] != float64(2) {
		t.Fatalf("batch create: %d %v", code, body)
	}
	ids, _ := body["ids"].([]any)
	target := ids[0].(string)
	for _, action := range []string{"pause", "resume", "cancel"} {
		code, body = request(t, "POST", ts.URL+"/api/v1/tasks/batch",
			`{"ids":["`+target+`"],"action":"`+action+`"}`)
		if code != 201 || body["count"] != float64(1) {
			t.Fatalf("batch %s: %d %v", action, code, body)
		}
	}
	// Unknown actions are ignored, but the ID is still echoed back.
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks/batch", `{"ids":["`+target+`"],"action":"frobnicate"}`)
	if code != 201 || body["count"] != float64(1) {
		t.Fatalf("batch unknown action: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks/batch", `{"inputs":["file:///etc/passwd"]}`)
	if code != 403 || errCode(body) != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("batch bad input: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks/batch", `{"inputs":[]`)
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("batch bad json: %d %v", code, body)
	}
}

func TestResolveOnlyEndpointErrors(t *testing.T) {
	ts, _, _ := newTestServer(t)
	code, body := request(t, "POST", ts.URL+"/api/v1/resolve", `{"input":""}`)
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("empty input: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/resolve", `{`)
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("bad json: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/resolve",
		`{"input":"https://x/a","credentialRef":"mem://c"}`)
	if code != 200 || body["id"] != "res-1" {
		t.Fatalf("resolve: %d %v", code, body)
	}
}

func TestCredentialStoreUnavailable(t *testing.T) {
	srv := New(Options{Control: newFakeControl(),
		Config: config.NewManager(config.Defaults(), nil), Bus: core.NewEventBus(8)})
	ts := httptest.NewServer(srv)
	defer ts.Close()
	code, body := request(t, "POST", ts.URL+"/api/v1/credentials", `{"value":"secret"}`)
	if code != 503 || errCode(body) != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("missing credential store: %d %v", code, body)
	}
}

func TestServeUIRejectsUnknownPaths(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unknown path must 404, got %d", resp.StatusCode)
	}
	// The API namespace must not fall through to the UI handler.
	resp2, err := http.Get(ts.URL + "/api/v1/unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("unknown API path must 404, got %d", resp2.StatusCode)
	}
}

func TestBearerAuthIsEnforcedForAPIOnly(t *testing.T) {
	srv := New(Options{Control: newFakeControl(),
		Config: config.NewManager(config.Defaults(), nil), Bus: core.NewEventBus(8),
		AuthToken: "s3cret", RequireAuth: true})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	code, body := request(t, "GET", ts.URL+"/api/v1/healthz", "")
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("missing token: %d %v", code, body)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/healthz", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("wrong token must be rejected, got %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/healthz", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("valid token must pass, got %d", resp.StatusCode)
	}
	// The UI is not part of the API namespace and stays reachable.
	resp, err = http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("UI must not require a token, got %d", resp.StatusCode)
	}
}

func TestWriteErrorRedactsSecrets(t *testing.T) {
	rec := httptest.NewRecorder()
	e := core.Err(core.E_RESOLVE_FAILED, "fetch https://x/a?token=SUPERSECRET failed")
	e.Details = map[string]any{"url": "https://x/a?token=SUPERSECRET", "attempt": 2}
	writeError(rec, e)
	if rec.Code != 422 {
		t.Fatalf("status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SUPERSECRET") {
		t.Fatalf("secret leaked in error body: %s", rec.Body.String())
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"]["code"] != core.E_RESOLVE_FAILED {
		t.Fatalf("unexpected error body: %v", payload)
	}
}

func TestWriteErrorWrapsPlainErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, errors.New("boom"))
	if rec.Code != 500 {
		t.Fatalf("plain errors must map to 500, got %d", rec.Code)
	}
}

func TestMarshalEventRejectsUnencodablePayload(t *testing.T) {
	if _, err := marshalEvent(core.Event{Payload: map[string]any{"ch": make(chan int)}}); err == nil {
		t.Fatal("channel payload must not be encodable")
	}
	if _, err := marshalEvent(core.Event{Type: core.EventTaskCreated}); err != nil {
		t.Fatalf("plain event must encode: %v", err)
	}
}

func TestServeHTTPRecoversFromPanic(t *testing.T) {
	srv := New(Options{Control: panicControl{newFakeControl()},
		Config: config.NewManager(config.Defaults(), nil), Bus: core.NewEventBus(8)})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stats", nil))
	if rec.Code != 500 {
		t.Fatalf("panic must become 500, got %d", rec.Code)
	}
}

type panicControl struct{ *fakeControl }

func (p panicControl) Stats(context.Context) (core.Stats, error) { panic("boom") }
