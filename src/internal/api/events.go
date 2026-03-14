package api

// SSE Real-time Kanban — Feature 4
//
// The admin UI subscribes to GET /admin/events as a text/event-stream.
// Whenever a task is created, its status changes, or an agent posts a log
// entry, agenthub broadcasts an SSE event so the browser refreshes
// immediately instead of waiting for the 30-second HTMX poll.
//
// Event types:
//   kanban-update   — task created or status changed (data: task ID)
//   task-log        — agent posted a log entry       (data: task ID)
//   heartbeat       — agent posted a heartbeat       (data: bot name)
//   inbox-update    — webhook routed to agent inbox  (data: channel name)
//
// Route (cookie-authenticated):
//   GET /admin/events

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// EventBroadcaster fans out server-sent events to all connected admin browsers.
// It is always created (no external dependencies) so SSE is always available.
type EventBroadcaster struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newEventBroadcaster() *EventBroadcaster {
	return &EventBroadcaster{clients: make(map[chan string]struct{})}
}

// subscribe registers a new SSE client channel. Call the returned cancel
// function when the client disconnects to avoid a channel leak.
func (b *EventBroadcaster) subscribe() (chan string, func()) {
	ch := make(chan string, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
	}
}

// Broadcast sends an SSE event to every connected client.
// Slow clients that can't keep up are skipped (non-blocking send).
func (b *EventBroadcaster) Broadcast(event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	b.mu.Lock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default: // slow client; skip rather than block
		}
	}
	b.mu.Unlock()
}

// handleAdminEvents serves GET /admin/events as a Server-Sent Events stream.
// The browser receives push notifications about kanban state changes and
// uses them to trigger HTMX refreshes without a 30-second polling interval.
func (s *Server) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/proxy buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ch, unsub := s.events.subscribe()
	defer unsub()

	// Initial keepalive so the browser confirms the connection opened.
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// 25-second keepalive prevents proxies from killing idle connections.
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg := <-ch:
			_, _ = fmt.Fprint(w, msg)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
