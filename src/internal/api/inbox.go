package api

// Agent Inbox API — Feature 1
//
// Agents are outbound-only (sandboxed) and cannot receive inbound Slack Socket
// Mode connections. agenthub buffers messages on their behalf so they can poll.
//
// Routes (token-authenticated):
//   GET  /api/inbox              — poll and dequeue all pending messages
//   POST /api/inbox/{id}/ack     — ack a single message (idempotent remove)
//   POST /api/inbox/{id}/reply   — post a reply back to Slack on agent's behalf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// InboxReplier is the optional interface used to post agent replies to Slack.
// Wire a Slack client in main.go to make POST /api/inbox/{id}/reply functional.
type InboxReplier interface {
	PostMessage(ctx context.Context, channel, text string) error
}

// InboxMessage is a message buffered for an agent to read.
type InboxMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`    // Slack user ID, "system", or "webhook:<channel>"
	Channel   string    `json:"channel"` // Slack channel (used when posting replies)
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Inbox is a concurrent per-bot in-memory message queue.
// Messages remain in the queue until Poll or Ack removes them.
type Inbox struct {
	mu     sync.Mutex
	queues map[string][]*InboxMessage
	seq    atomic.Uint64
}

func newInbox() *Inbox {
	return &Inbox{queues: make(map[string][]*InboxMessage)}
}

// Enqueue adds a message to botName's queue and returns the new message ID.
func (b *Inbox) Enqueue(botName, from, channel, text string) string {
	n := b.seq.Add(1)
	msg := &InboxMessage{
		ID:        fmt.Sprintf("msg-%d", n),
		From:      from,
		Channel:   channel,
		Text:      text,
		CreatedAt: time.Now().UTC(),
	}
	b.mu.Lock()
	b.queues[botName] = append(b.queues[botName], msg)
	b.mu.Unlock()
	return msg.ID
}

// Poll returns all pending messages for botName without removing them.
// Messages remain queued until the bot Acks them (individually) or Replies
// (which auto-Acks).  This allows the bot to re-fetch messages on restart.
func (b *Inbox) Poll(botName string) []*InboxMessage {
	b.mu.Lock()
	msgs := b.queues[botName]
	b.mu.Unlock()
	if msgs == nil {
		return []*InboxMessage{}
	}
	// Return a shallow copy so callers can't mutate the queue.
	out := make([]*InboxMessage, len(msgs))
	copy(out, msgs)
	return out
}

// Len returns the number of queued messages for botName without consuming them.
func (b *Inbox) Len(botName string) int {
	b.mu.Lock()
	n := len(b.queues[botName])
	b.mu.Unlock()
	return n
}

// Ack removes a single message by ID from botName's queue; returns true if found.
func (b *Inbox) Ack(botName, msgID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := b.queues[botName]
	for i, m := range q {
		if m.ID == msgID {
			b.queues[botName] = append(q[:i], q[i+1:]...)
			return true
		}
	}
	return false
}

// getMessage returns a message by ID without removing it (used before reply).
func (b *Inbox) getMessage(botName, msgID string) *InboxMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.queues[botName] {
		if m.ID == msgID {
			return m
		}
	}
	return nil
}

// --------------------------------------------------------------------------
// HTTP handlers
// --------------------------------------------------------------------------

// handleInboxPoll handles GET /api/inbox.
// Returns and dequeues all pending messages for the calling agent.
func (s *Server) handleInboxPoll(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	botName := r.Header.Get("X-Bot-Name")
	if botName == "" {
		http.Error(w, `{"error":"X-Bot-Name header required"}`, http.StatusBadRequest)
		return
	}
	msgs := s.inbox.Poll(botName)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs)
}

// handleInboxAck handles POST /api/inbox/{id}/ack.
// Idempotent: acking an already-removed message returns 204.
func (s *Server) handleInboxAck(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	botName := r.Header.Get("X-Bot-Name")
	msgID := r.PathValue("id")
	s.inbox.Ack(botName, msgID) // ignore not-found; idempotent
	w.WriteHeader(http.StatusNoContent)
}

// handleInboxReply handles POST /api/inbox/{id}/reply.
// The agent sends a reply text; agenthub posts it to the original Slack channel.
func (s *Server) handleInboxReply(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	botName := r.Header.Get("X-Bot-Name")
	msgID := r.PathValue("id")

	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		http.Error(w, `{"error":"text is required"}`, http.StatusBadRequest)
		return
	}

	msg := s.inbox.getMessage(botName, msgID)
	if msg == nil {
		http.Error(w, `{"error":"message not found"}`, http.StatusNotFound)
		return
	}

	if s.replier != nil && msg.Channel != "" {
		reply := fmt.Sprintf("[%s] %s", botName, body.Text)
		if err := s.replier.PostMessage(r.Context(), msg.Channel, reply); err != nil {
			http.Error(w, `{"error":"failed to post reply to Slack"}`, http.StatusInternalServerError)
			return
		}
	}

	s.inbox.Ack(botName, msgID)
	w.WriteHeader(http.StatusNoContent)
}
