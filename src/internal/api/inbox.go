package api

// Agent Inbox API — Feature 1
//
// Agents are outbound-only (sandboxed) and cannot receive inbound Slack Socket
// Mode connections. agenthub buffers messages on their behalf so they can poll.
// Agents post replies directly to Slack using the bot token received at registration.
//
// Routes (token-authenticated):
//   GET  /api/inbox          — poll all pending messages
//   POST /api/inbox/{id}/ack — ack a single message (idempotent remove)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

// TaskContext carries the task assignment details needed for credential delivery.
type TaskContext struct {
	TaskAssignmentID string                `json:"task_assignment_id"`
	TaskID           string                `json:"task_id"`
	ProjectID        string                `json:"project_id"`
	ProjectName      string                `json:"project_name"`
	ResourceGrants   []dolt.ResourceGrant  `json:"resource_grants"` // metadata only
	CredentialURL    string                `json:"credential_url"`
}

// InboxMessage is a message buffered for an agent to read.
type InboxMessage struct {
	ID          string       `json:"id"`
	From        string       `json:"from"`    // Slack user ID, "system", or "webhook:<channel>"
	Channel     string       `json:"channel"` // Slack channel (used when posting replies)
	Text        string       `json:"text"`
	CreatedAt   time.Time    `json:"created_at"`
	TaskContext *TaskContext  `json:"task_context,omitempty"`
}

// Inbox is a concurrent per-bot in-memory message queue.
// Messages remain in the queue until Poll or Ack removes them.
// When a db is set via SetDB, Inbox acts as a write-through cache backed by the database.
type Inbox struct {
	mu     sync.Mutex
	queues map[string][]*InboxMessage
	seq    atomic.Uint64
	db     InboxDB // optional; set via SetDB
}

func newInbox() *Inbox {
	return &Inbox{queues: make(map[string][]*InboxMessage)}
}

// SetDB wires an optional database backend for durable message storage.
func (b *Inbox) SetDB(db InboxDB) {
	b.mu.Lock()
	b.db = db
	b.mu.Unlock()
}

// Enqueue adds a message to botName's queue and returns the new message ID.
func (b *Inbox) Enqueue(botName, from, channel, text string) string {
	return b.EnqueueWithContext(botName, from, channel, text, nil)
}

// EnqueueWithContext adds a message with an optional TaskContext to botName's queue
// and returns the new message ID.
func (b *Inbox) EnqueueWithContext(botName, from, channel, text string, tc *TaskContext) string {
	n := b.seq.Add(1)
	msg := &InboxMessage{
		ID:          fmt.Sprintf("msg-%d", n),
		From:        from,
		Channel:     channel,
		Text:        text,
		CreatedAt:   time.Now().UTC(),
		TaskContext: tc,
	}
	b.mu.Lock()
	b.queues[botName] = append(b.queues[botName], msg)
	db := b.db
	b.mu.Unlock()

	// Persist to DB if available (write-through cache).
	if db != nil {
		dbMsg := inboxMessageToDB(botName, msg)
		_ = db.CreateInboxMessage(context.Background(), dbMsg)
	}

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

// inboxMessageToDB converts an in-memory InboxMessage to a dolt.InboxDBMessage.
func inboxMessageToDB(botName string, msg *InboxMessage) dolt.InboxDBMessage {
	var tcJSON []byte
	if msg.TaskContext != nil {
		tcJSON, _ = json.Marshal(msg.TaskContext)
	}
	if len(tcJSON) == 0 {
		tcJSON = []byte("{}")
	}
	return dolt.InboxDBMessage{
		ID:          msg.ID,
		BotName:     botName,
		FromUser:    msg.From,
		Channel:     msg.Channel,
		Body:        msg.Text,
		TaskContext: tcJSON,
		CreatedAt:   msg.CreatedAt,
	}
}

// dbMessageToInbox converts a dolt.InboxDBMessage to an in-memory InboxMessage.
func dbMessageToInbox(m *dolt.InboxDBMessage) *InboxMessage {
	msg := &InboxMessage{
		ID:        m.ID,
		From:      m.FromUser,
		Channel:   m.Channel,
		Text:      m.Body,
		CreatedAt: m.CreatedAt,
	}
	if len(m.TaskContext) > 0 && string(m.TaskContext) != "{}" {
		var tc TaskContext
		if err := json.Unmarshal(m.TaskContext, &tc); err == nil {
			msg.TaskContext = &tc
		}
	}
	return msg
}

// --------------------------------------------------------------------------
// HTTP handlers
// --------------------------------------------------------------------------

// handleInboxPoll handles GET /api/inbox.
// Returns all pending messages for the calling agent without removing them.
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

	var msgs []*InboxMessage
	// Prefer DB if available for durable read.
	if s.inbox.db != nil {
		if dbMsgs, err := s.inbox.db.ListPendingMessages(r.Context(), botName); err == nil {
			msgs = make([]*InboxMessage, 0, len(dbMsgs))
			for _, m := range dbMsgs {
				msgs = append(msgs, dbMessageToInbox(m))
			}
		} else {
			msgs = s.inbox.Poll(botName)
		}
	} else {
		msgs = s.inbox.Poll(botName)
	}
	if msgs == nil {
		msgs = []*InboxMessage{}
	}
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
	if s.inbox.db != nil {
		_ = s.inbox.db.AckInboxMessage(r.Context(), msgID)
	}
	w.WriteHeader(http.StatusNoContent)
}

