package core

import (
	"sync"
	"sync/atomic"
	"time"
)

// EventType values follow docs/07 §4 exactly.
type EventType string

const (
	EventTaskCreated     EventType = "task.created"
	EventTaskState       EventType = "task.state"
	EventTaskProgress    EventType = "task.progress"
	EventTaskChunkFailed EventType = "task.chunk.failed"
	EventTaskCompleted   EventType = "task.completed"
	EventTaskFailed      EventType = "task.failed"
	EventTaskCanceled    EventType = "task.canceled"
	EventCapability      EventType = "capability.changed"
	EventConfigChanged   EventType = "config.changed"
)

// Event is the unit broadcast to WebSocket clients, the events table and logs.
// Payload must already be redacted by the publisher (docs/11 §3).
type Event struct {
	Type    EventType      `json:"type"`
	TaskID  TaskID         `json:"taskId,omitempty"`
	Ts      int64          `json:"ts"`
	Payload map[string]any `json:"payload,omitempty"`
}

// EventBus is an in-memory fan-out bus. Persistence into `events` is a store
// concern, so a slow WebSocket client can never block the scheduler.
type EventBus struct {
	mu       sync.RWMutex
	next     int
	subs     map[int]chan Event
	capacity int
	closed   bool
	// Publish only holds a read lock, so the counter must be atomic rather
	// than guarded by mu (a write under RLock would be a data race).
	dropped atomic.Int64
}

func NewEventBus(capacity int) *EventBus {
	if capacity <= 0 {
		capacity = 256
	}
	return &EventBus{subs: make(map[int]chan Event), capacity: capacity}
}

// Subscribe returns a channel and its unsubscribe function.
func (b *EventBus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan Event, b.capacity)
	if b.closed {
		close(ch)
		return ch, func() {}
	}
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}
}

// Publish never blocks the caller; a saturated subscriber loses the newest event.
func (b *EventBus) Publish(e Event) {
	if e.Ts == 0 {
		e.Ts = time.Now().UnixMilli()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			b.dropped.Add(1)
		}
	}
}

// Dropped is a diagnostic counter (never exposed with payloads).
func (b *EventBus) Dropped() int64 { return b.dropped.Load() }

func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}
