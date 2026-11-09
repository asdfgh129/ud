package core

import (
	"testing"
	"time"
)

func TestStateMachineLegalTransitions(t *testing.T) {
	legal := [][2]TaskState{
		{StatePending, StateResolving}, {StateResolving, StateReady},
		{StateResolving, StateFailed}, {StateReady, StateRunning},
		{StateRunning, StatePaused}, {StatePaused, StateRunning},
		{StateRunning, StatePostProcessing}, {StateRunning, StateCanceled},
		{StatePaused, StateCanceled}, {StatePostProcessing, StateCompleted},
		{StatePostProcessing, StateFailed}, {StateFailed, StateReady},
		{StateCompleted, StateSeeding}, {StateSeeding, StateCompleted},
	}
	for _, p := range legal {
		if err := CanTransition(p[0], p[1]); err != nil {
			t.Errorf("%s -> %s should be legal: %v", p[0], p[1], err)
		}
	}
	illegal := [][2]TaskState{
		{StateCompleted, StateRunning}, {StateCanceled, StateRunning},
		{StatePending, StateCompleted}, {StateRunning, StateReady},
	}
	for _, p := range illegal {
		err := CanTransition(p[0], p[1])
		if err == nil {
			t.Errorf("%s -> %s should be illegal", p[0], p[1])
		} else if e := AsError(err); e.Code != E_ILLEGAL_TRANSITION {
			t.Errorf("wrong error code: %s", e.Code)
		}
	}
}

func TestApplyTransitionClearsErrorAndSetsCompletedAt(t *testing.T) {
	task := &Task{State: StateRunning, ErrorKind: "transient", ErrorMessage: "boom"}
	at := time.Now().UnixMilli()
	if err := ApplyTransition(task, StatePostProcessing, at); err != nil {
		t.Fatal(err)
	}
	if task.State != StatePostProcessing || task.ErrorMessage != "" || task.UpdatedAt != at {
		t.Fatalf("unexpected task: %+v", task)
	}
	if err := ApplyTransition(task, StateCompleted, at+1); err != nil {
		t.Fatal(err)
	}
	if task.CompletedAt == nil || *task.CompletedAt != at+1 {
		t.Fatal("completedAt not recorded")
	}
}

func TestErrorCodeTable(t *testing.T) {
	cases := map[string]struct {
		status    int
		retryable bool
	}{
		E_INVALID_INPUT: {400, false}, E_RESOLVE_SSRF_BLOCKED: {403, false},
		E_TASK_NOT_FOUND: {404, false}, E_ILLEGAL_TRANSITION: {409, false},
		E_CHECKSUM_MISMATCH: {422, true}, E_TRANSIENT_NETWORK: {502, true},
		E_FLOOD_WAIT: {429, true}, E_CAPABILITY_UNAVAILABLE: {503, false},
		E_STORAGE_FULL: {507, false}, E_POSTPROCESS_FAILED: {500, true},
	}
	for code, want := range cases {
		e := Err(code, "x")
		if e.HTTPStatus() != want.status || e.Retryable != want.retryable {
			t.Errorf("%s: status=%d retry=%v", code, e.HTTPStatus(), e.Retryable)
		}
	}
	if e := Err("UNKNOWN", "x"); e.Code != E_INTERNAL {
		t.Fatalf("unknown code should map to E_INTERNAL, got %s", e.Code)
	}
}

func TestULID(t *testing.T) {
	a := NewID()
	b := NewID()
	if len(a) != 26 || len(b) != 26 {
		t.Fatalf("bad ULID length: %q %q", a, b)
	}
	if a >= b {
		t.Fatalf("ULIDs must be monotonic within the same millisecond: %s >= %s", a, b)
	}
	ts, err := ParseID(a)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(ts) > time.Minute || time.Since(ts) < -time.Minute {
		t.Fatalf("embedded timestamp is wrong: %v", ts)
	}
	if _, err := ParseID("not-a-ulid"); err == nil {
		t.Fatal("invalid ULID accepted")
	}
}

func TestEventBusFanout(t *testing.T) {
	bus := NewEventBus(4)
	ch, unsub := bus.Subscribe()
	bus.Publish(Event{Type: EventTaskCreated, TaskID: "x"})
	select {
	case e := <-ch:
		if e.Type != EventTaskCreated || e.Ts == 0 {
			t.Fatalf("bad event: %+v", e)
		}
	default:
		t.Fatal("event not delivered")
	}
	unsub()
	bus.Close()
}
