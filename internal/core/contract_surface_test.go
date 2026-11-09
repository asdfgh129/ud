package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// These tests cover the "thin" contract surface (error helpers, ID helpers,
// state predicates, event bus diagnostics) that the P0 gate previously left
// at 0% (docs/12 §9). They are deliberately table-driven and side-effect free.

func TestErrorKindStrings(t *testing.T) {
	cases := map[ErrorKind]string{
		KindPermanent: "permanent", KindTransient: "transient",
		KindCapability: "capability", KindSecurity: "security",
		ErrorKind(99): "unknown",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("ErrorKind(%d).String()=%q want %q", k, got, want)
		}
	}
}

func TestErrorHelpers(t *testing.T) {
	if got := Err("NO_SUCH_CODE", "x").Code; got != E_INTERNAL {
		t.Fatalf("unknown code must fall back to E_INTERNAL, got %s", got)
	}
	e := Err(E_TRANSIENT_NETWORK, "status %d", 503)
	if e.Error() != "E_TRANSIENT_NETWORK: status 503" {
		t.Fatalf("Error()=%q", e.Error())
	}
	var nilErr *Error
	if nilErr.Error() != "<nil>" {
		t.Fatal("nil *Error must not panic")
	}
	if e.WithDetail("attempt", 2) != e || e.Details["attempt"] != 2 {
		t.Fatal("WithDetail must attach non-sensitive context and return itself")
	}
	if e.HTTPStatus() != 502 {
		t.Fatalf("HTTPStatus()=%d", e.HTTPStatus())
	}
	if got := (&Error{Code: E_INTERNAL}).HTTPStatus(); got != 500 {
		t.Fatalf("zero status default=%d want 500", got)
	}
	if AsError(nil) != nil {
		t.Fatal("AsError(nil) must be nil")
	}
	wrapped := fmt.Errorf("transport: %w", e)
	if AsError(wrapped) != e {
		t.Fatal("AsError must unwrap a classified error")
	}
	plain := AsError(errors.New("boom"))
	if plain.Code != E_INTERNAL || !strings.Contains(plain.Message, "boom") {
		t.Fatalf("plain error conversion: %+v", plain)
	}
	if !IsRetryable(e) || IsRetryable(Err(E_INVALID_INPUT, "bad")) || IsRetryable(nil) {
		t.Fatal("IsRetryable classification is wrong")
	}
}

func TestIDHelpers(t *testing.T) {
	before := time.Now().Add(-time.Second).UnixMilli()
	id := NewID()
	after := time.Now().Add(time.Second).UnixMilli()
	ts, err := ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	got := TimeFromID(id)
	if got < before || got > after || got != ts.UnixMilli() {
		t.Fatalf("TimeFromID=%d want within [%d,%d]", got, before, after)
	}
	// "O" is deliberately excluded from the Crockford base32 alphabet.
	if TimeFromID("not-a-ulid") != 0 || TimeFromID("0000000000000000000000000O") != 0 {
		t.Fatal("invalid ULIDs must map to timestamp 0")
	}
	a, b := IDFromInt64(42), IDFromInt64(42)
	if a != b || len(a) != 26 {
		t.Fatalf("IDFromInt64 not deterministic: %q / %q", a, b)
	}
	if _, err := ParseID(a); err != nil {
		t.Fatalf("IDFromInt64 produced unparsable ULID: %v", err)
	}
}

// Deterministic coverage of same-millisecond ULID monotonicity. The existing
// TestULID relies on wall-clock timing, so the carry path was flaky when the
// whole suite ran in parallel (coverage swung between 89.4% and 92.4%).
func TestIncrementEntropyCarry(t *testing.T) {
	e := make([]byte, 10)
	e[9] = 0xFE
	incrementEntropy(e)
	if e[9] != 0xFF || e[8] != 0 {
		t.Fatalf("increment without carry: %v", e)
	}
	incrementEntropy(e)
	if e[9] != 0 || e[8] != 1 {
		t.Fatalf("carry must propagate: %v", e)
	}
	ulidState.mu.Lock()
	saved := ulidState.lastMS
	ulidState.mu.Unlock()
	for i := range e {
		e[i] = 0xFF
	}
	incrementEntropy(e) // full overflow rolls the timestamp forward
	ulidState.mu.Lock()
	rolled := ulidState.lastMS
	ulidState.lastMS = saved
	ulidState.mu.Unlock()
	if rolled != saved+1 {
		t.Fatalf("entropy overflow must roll timestamp: %d -> %d", saved, rolled)
	}
	for _, v := range e {
		if v != 0 {
			t.Fatalf("entropy must wrap to zero on overflow: %v", e)
		}
	}
}

func TestSchemeOfAndStateHelpers(t *testing.T) {
	cases := map[string]string{
		"https://EXAMPLE.COM/a": "https",
		"HTTP://x/a":            "http",
		"file:///etc/passwd":    "file",
		"magnet:?xt=urn:btih:x": "magnet",
		"":                      "",
		"http://[::1":           "",
	}
	for in, want := range cases {
		if got := SchemeOf(in); got != want {
			t.Errorf("SchemeOf(%q)=%q want %q", in, got, want)
		}
	}
	for _, s := range []TaskState{StateCompleted, StateCanceled} {
		if !IsTerminal(s) {
			t.Errorf("%s must be terminal", s)
		}
	}
	for _, s := range []TaskState{StatePending, StateReady, StateRunning, StatePaused, StateFailed} {
		if IsTerminal(s) {
			t.Errorf("%s must not be terminal", s)
		}
	}
	if CredentialRef("mem://cred/1").String() != "mem://cred/1" {
		t.Fatal("CredentialRef.String")
	}
	if (Chunk{Start: 100, Length: 10}).End() != 109 {
		t.Fatal("Chunk.End must be an exclusive upper bound minus one")
	}
}

// Regression: Dropped() used to be a documented diagnostic that was never
// incremented. A slow WebSocket client must be observable without blocking
// the scheduler (docs/05 §7, docs/12 §4).
func TestEventBusDroppedCounterAndClose(t *testing.T) {
	b := NewEventBus(1)
	if got := NewEventBus(0); got.capacity != 256 {
		t.Fatal("non-positive capacity must fall back to 256")
	}
	ch, unsubscribe := b.Subscribe()
	b.Publish(Event{Type: EventTaskCreated})
	b.Publish(Event{Type: EventTaskState})
	b.Publish(Event{Type: EventTaskProgress})
	if b.Dropped() != 2 {
		t.Fatalf("Dropped()=%d want 2", b.Dropped())
	}
	if got := <-ch; got.Type != EventTaskCreated || got.Ts == 0 {
		t.Fatalf("first event wrong or missing timestamp: %+v", got)
	}
	unsubscribe()
	unsubscribe() // idempotent
	b.Close()
	b.Close() // idempotent
	b.Publish(Event{Type: EventTaskCompleted})
	closed, _ := b.Subscribe()
	if _, ok := <-closed; ok {
		t.Fatal("Subscribe after Close must return an already-closed channel")
	}
}
