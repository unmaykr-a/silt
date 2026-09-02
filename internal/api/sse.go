package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// heartbeatInterval keeps idle SSE connections from being culled by
// intermediaries. Reverse proxies and load balancers close quiet connections,
// and a comment line is enough to keep them open.
const heartbeatInterval = 20 * time.Second

// subscriberBuffer is how far behind a client may fall before its events are
// dropped. A slow reader must never block the collector.
const subscriberBuffer = 64

// Message is one server-sent event.
type Message struct {
	Event string `json:"-"`
	Data  any    `json:"data"`
}

// Hub fans events out to connected SSE clients.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[chan Message]struct{}
	log         *slog.Logger
}

// NewHub returns an empty hub.
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{subscribers: make(map[chan Message]struct{}), log: log}
}

// Subscribe registers a client and returns its channel plus an unsubscribe
// function.
func (h *Hub) Subscribe() (<-chan Message, func()) {
	ch := make(chan Message, subscriberBuffer)

	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subscribers, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// Publish delivers m to every subscriber.
//
// A subscriber whose buffer is full has its message dropped rather than
// blocking the publisher: the collector must never stall because a browser
// tab stopped reading. Live updates are a convenience; the database is the
// record.
func (h *Hub) Publish(m Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- m:
		default:
			h.log.Debug("dropping event for a slow SSE subscriber", "event", m.Event)
		}
	}
}

// Subscribers reports the current connection count, for /metrics.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// stream serves GET /api/stream.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Nginx honours this even without proxy_buffering off, which is the
	// difference between live updates and events arriving in batches minutes
	// late. Document the nginx directive anyway; not every proxy reads it.
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	events, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()

	// Tell the client we are live before anything else, so a UI can show a
	// connected state rather than waiting for the first real event.
	if err := writeSSE(w, rc, "ready", map[string]any{"at": time.Now().UnixMilli()}); err != nil {
		return
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case m, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, rc, m.Event, m.Data); err != nil {
				return
			}

		case <-ticker.C:
			// A named event rather than an SSE comment.
			//
			// A comment keeps proxies from closing an idle connection, which
			// was the whole job, but EventSource discards it without telling
			// anyone — so a browser could not distinguish a live connection
			// with nothing happening from one that had quietly wedged. It
			// still does the proxy job, and now the page can say how long ago
			// it last heard anything, which is the only honest way to show
			// "nothing has changed".
			if err := writeSSE(w, rc, "heartbeat", map[string]any{"at": time.Now().UnixMilli()}); err != nil {
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, rc *http.ResponseController, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	return rc.Flush()
}
