package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/asdfgh129/ud/internal/core"
)

// wsHub fans EventBus messages out to WebSocket clients (docs/07 §4).
// A slow client never blocks the single scheduler: its buffer drops the newest event.
type wsHub struct {
	bus      *core.EventBus
	upgrader websocket.Upgrader
	mu       sync.Mutex
	clients  map[*wsClient]bool
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

func newWSHub(bus *core.EventBus) *wsHub {
	h := &wsHub{
		bus: bus,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				// The daemon listens on loopback by default; remote access is gated
				// by the Bearer token middleware before this handler is reached.
				return true
			},
		},
		clients: make(map[*wsClient]bool),
	}
	go h.run()
	return h
}

func (h *wsHub) run() {
	ch, _ := h.bus.Subscribe()
	for e := range ch {
		payload, err := marshalEvent(e)
		if err != nil {
			continue
		}
		h.mu.Lock()
		for c := range h.clients {
			select {
			case c.send <- payload:
			default:
			}
		}
		h.mu.Unlock()
	}
}

func (h *wsHub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *wsHub) remove(c *wsClient) {
	h.mu.Lock()
	if h.clients[c] {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// ServeHTTP upgrades GET /api/v1/events and pushes JSON events until disconnect.
func (h *wsHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsClient{conn: conn, send: make(chan []byte, 64)}
	h.add(c)
	defer func() {
		h.remove(c)
		_ = conn.Close()
	}()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	})
	go h.writer(c)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *wsHub) writer(c *wsClient) {
	ticker := time.NewTicker(50 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
