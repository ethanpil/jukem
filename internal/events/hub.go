// Package events fans change notifications out to SSE clients. An event
// says what changed and never what the state is now. Clients refetch the
// state over REST.
package events

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Type names what changed.
type Type string

const (
	Player   Type = "player"   // state, current track, volume, options
	Queue    Type = "queue"    // queue contents or order
	Library  Type = "library"  // files or the MPD database
	Devices  Type = "devices"  // output devices present or selected
	Schedule Type = "schedule" // rules, exceptions, overrides, scheduler switch
	Alerts   Type = "alerts"   // alerts raised or dismissed
	Settings Type = "settings" // settings changed
	Health   Type = "health"   // health status changed
)

// Event is one notification.
type Event struct {
	Type Type   `json:"type"`
	Ref  string `json:"ref,omitempty"` // an optional path or id the change concerns
}

// Hub holds the connected clients. The hub drops a client whose buffer is
// full. The client reconnects and gets the state again over REST.
type Hub struct {
	mu      sync.Mutex
	clients map[chan Event]struct{}
}

// New creates an empty hub.
func New() *Hub {
	return &Hub{clients: map[chan Event]struct{}{}}
}

// Publish sends an event to every client.
func (h *Hub) Publish(t Type, ref string) {
	ev := Event{Type: t, Ref: ref}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- ev:
		default:
			delete(h.clients, ch)
			close(ch)
		}
	}
}

// Subscribe registers a client. Unsubscribe with the returned function.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.clients[ch]; ok {
			delete(h.clients, ch)
			close(ch)
		}
	}
}

// Clients returns the number of connected clients.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// ServeHTTP streams events to one client until it disconnects. A comment
// line every 25 seconds keeps proxies and browsers from closing an idle
// stream.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// The browser reconnects after this many milliseconds.
	w.Write([]byte("retry: 3000\n\n"))
	flusher.Flush()

	ch, unsubscribe := h.Subscribe()
	defer unsubscribe()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			w.Write([]byte("data: "))
			w.Write(data)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		case <-keepalive.C:
			w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		}
	}
}
