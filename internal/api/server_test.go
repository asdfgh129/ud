package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

type fakeControl struct {
	mu    sync.Mutex
	tasks map[core.TaskID]*core.TaskDetail
	calls map[string]int
}

func newFakeControl() *fakeControl {
	return &fakeControl{tasks: make(map[core.TaskID]*core.TaskDetail), calls: make(map[string]int)}
}

func (f *fakeControl) note(name string) {
	f.mu.Lock()
	f.calls[name]++
	f.mu.Unlock()
}

func (f *fakeControl) Create(_ context.Context, in core.Input, opt core.TaskOptions) (core.TaskID, error) {
	f.note("create")
	id := core.NewID()
	f.mu.Lock()
	f.tasks[id] = &core.TaskDetail{Task: core.Task{ID: id, State: core.StatePending,
		Input: security.RedactURL(in.Raw), InputKind: in.Kind, Options: opt}}
	f.mu.Unlock()
	return id, nil
}
func (f *fakeControl) Pause(_ context.Context, id core.TaskID) error {
	f.note("pause")
	return f.set(id, core.StatePaused)
}
func (f *fakeControl) Resume(_ context.Context, id core.TaskID) error {
	f.note("resume")
	return f.set(id, core.StateReady)
}
func (f *fakeControl) Cancel(_ context.Context, id core.TaskID) error {
	f.note("cancel")
	return f.set(id, core.StateCanceled)
}
func (f *fakeControl) SetOption(context.Context, core.TaskID, core.TaskOptions) error { return nil }
func (f *fakeControl) set(id core.TaskID, state core.TaskState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return core.Err(core.E_TASK_NOT_FOUND, "task %s not found", id)
	}
	t.State = state
	return nil
}
func (f *fakeControl) Get(_ context.Context, id core.TaskID) (*core.TaskDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return nil, core.Err(core.E_TASK_NOT_FOUND, "task %s not found", id)
	}
	cp := *t
	return &cp, nil
}
func (f *fakeControl) List(context.Context, core.TaskFilter) ([]*core.Task, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*core.Task
	for _, d := range f.tasks {
		cp := d.Task
		out = append(out, &cp)
	}
	return out, len(out), nil
}
func (f *fakeControl) Delete(_ context.Context, id core.TaskID, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.tasks[id]; !ok {
		return core.Err(core.E_TASK_NOT_FOUND, "task %s not found", id)
	}
	delete(f.tasks, id)
	return nil
}
func (f *fakeControl) ResolveOnly(_ context.Context, in core.Input, _ core.TaskOptions) (*core.Resource, error) {
	return &core.Resource{ID: "res-1", Kind: core.KindSingle, Engine: core.EngineGo,
		Files:   []core.ResourceFile{{Index: 0, Path: "a.bin", Selected: true}},
		Mirrors: []core.Mirror{{URL: in.Raw}}}, nil
}
func (f *fakeControl) Stats(context.Context) (core.Stats, error) {
	return core.Stats{Tasks: len(f.tasks)}, nil
}
func (f *fakeControl) Health(context.Context) core.Health {
	return core.Health{Status: "ok", DB: true, Limiter: true, Sidecars: map[string]string{}}
}
func (f *fakeControl) Capabilities() core.Capabilities {
	return core.Capabilities{Resolvers: []string{"direct-http"}, Transports: []string{"http"}, RPCLevel: "L0"}
}
func (f *fakeControl) Recover(context.Context) (int, error) { return 0, nil }
func (f *fakeControl) Close() error                         { return nil }

func newTestServer(t *testing.T) (*httptest.Server, *core.EventBus, *fakeControl) {
	t.Helper()
	bus := core.NewEventBus(64)
	ctl := newFakeControl()
	srv := New(Options{
		Control: ctl, Config: config.NewManager(config.Defaults(), nil),
		Credentials: security.NewMemoryCredentialStore(), Bus: bus,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, bus, ctl
}

func request(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestRESTContract(t *testing.T) {
	ts, _, ctl := newTestServer(t)
	code, body := request(t, "GET", ts.URL+"/api/v1/healthz", "")
	if code != 200 || body["status"] != "ok" {
		t.Fatalf("healthz: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks", `{"input":"https://example.com/a.iso","options":{"maxConnections":4}}`)
	if code != 201 || body["state"] != "Pending" {
		t.Fatalf("create: %d %v", code, body)
	}
	id, _ := body["id"].(string)
	code, body = request(t, "GET", ts.URL+"/api/v1/tasks/"+id, "")
	if code != 200 || body["id"] != id {
		t.Fatalf("get: %d %v", code, body)
	}
	code, _ = request(t, "POST", ts.URL+"/api/v1/tasks/"+id+"/pause", "")
	if code != 200 {
		t.Fatalf("pause: %d", code)
	}
	code, body = request(t, "GET", ts.URL+"/api/v1/tasks/"+id, "")
	if body["state"] != "Paused" {
		t.Fatalf("state not persisted: %v", body)
	}
	code, body = request(t, "GET", ts.URL+"/api/v1/tasks/unknown-id", "")
	if code != 404 || errCode(body) != core.E_TASK_NOT_FOUND {
		t.Fatalf("not-found contract: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks", `{"input":"file:///etc/passwd"}`)
	if code != 403 || errCode(body) != core.E_RESOLVE_SSRF_BLOCKED {
		t.Fatalf("SSRF contract: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/tasks", `{"input":""}`)
	if code != 400 || errCode(body) != core.E_INVALID_INPUT {
		t.Fatalf("invalid input contract: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/resolve", `{"input":"https://example.com/a.iso"}`)
	if code != 200 || body["kind"] != "single" {
		t.Fatalf("resolve: %d %v", code, body)
	}
	code, body = request(t, "GET", ts.URL+"/api/v1/capabilities", "")
	if code != 200 || body["rpcLevel"] != "L0" {
		t.Fatalf("capabilities: %d %v", code, body)
	}
	code, body = request(t, "PATCH", ts.URL+"/api/v1/config", `{"global.maxDownloadRate":"1MiB/s"}`)
	if code != 200 {
		t.Fatalf("config patch: %d %v", code, body)
	}
	code, body = request(t, "POST", ts.URL+"/api/v1/credentials", `{"value":"Bearer SUPER-SECRET"}`)
	if code != 201 || !strings.HasPrefix(body["credentialRef"].(string), "mem://") {
		t.Fatalf("credentials: %d %v", code, body)
	}
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if ctl.calls["create"] != 1 || ctl.calls["pause"] != 1 {
		t.Fatalf("unexpected control calls: %+v", ctl.calls)
	}
}

// docs/06 §6 / docs/07 §2: inline credential headers are rejected in favour
// of POST /credentials + credentialRef, on every write path.
func TestInlineCredentialHeadersRejected(t *testing.T) {
	ts, _, ctl := newTestServer(t)
	bad := `{"input":"https://example.com/a.iso","options":{"headers":{"Authorization":"Bearer SUPER-SECRET"}}}`
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"create", "POST", "/api/v1/tasks", bad},
		{"batch", "POST", "/api/v1/tasks/batch", `{"inputs":["https://x/a"],"action":"add","options":{"headers":{"Cookie":"sid=SUPER-SECRET"}}}`},
		{"patch", "PATCH", "/api/v1/tasks/any-id", `{"headers":{"X-Api-Key":"SUPER-SECRET"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := request(t, tc.method, ts.URL+tc.path, tc.body)
			if code != 400 || errCode(body) != core.E_INVALID_INPUT {
				t.Fatalf("%s: %d %v", tc.name, code, body)
			}
			if strings.Contains(fmt.Sprint(body), "SUPER-SECRET") {
				t.Fatalf("%s echoed the secret", tc.name)
			}
		})
	}
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if ctl.calls["create"] != 0 {
		t.Fatalf("rejected request still reached the scheduler: %+v", ctl.calls)
	}
}

func errCode(body map[string]any) string {
	e, ok := body["error"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := e["code"].(string)
	return s
}

func TestIdempotencyKey(t *testing.T) {
	ts, _, _ := newTestServer(t)
	req := func() (int, map[string]any) {
		r, _ := http.NewRequest("POST", ts.URL+"/api/v1/tasks", strings.NewReader(`{"input":"https://x/a"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "fixed-key")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	_, first := req()
	code, second := req()
	if code != 200 || second["id"] != first["id"] || second["idempotent"] != true {
		t.Fatalf("idempotency broken: %d %v", code, second)
	}
}

func TestWebSocketEventDelivery(t *testing.T) {
	ts, bus, _ := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Give the hub a moment to register the client.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(core.Event{Type: core.EventTaskCompleted, TaskID: "task-1"})
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var e core.Event
	if err := json.Unmarshal(payload, &e); err != nil {
		t.Fatal(err)
	}
	if e.Type != core.EventTaskCompleted || e.TaskID != "task-1" {
		t.Fatalf("bad WS event: %+v", e)
	}
}

func TestMinimalUIIsServed(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("UI unavailable: %d", resp.StatusCode)
	}
}
