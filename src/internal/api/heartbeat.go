package api

// Heartbeat + Task Claiming — Feature 2
//
// Outbound-only agents cannot be reached; they prove liveness by POSTing a
// heartbeat. The response tells them how many inbox messages are waiting.
//
// Routes (token-authenticated):
//   POST /api/heartbeat      — record liveness; returns inbox_count
//   GET  /admin/heartbeats   — admin view of all live agents (cookie-auth)

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// BotStatus is the latest heartbeat snapshot for one agent.
type BotStatus struct {
	BotName     string    `json:"bot_name"`
	CurrentTask string    `json:"current_task,omitempty"`
	Status      string    `json:"status"`          // "idle", "working", "blocked"
	Message     string    `json:"message,omitempty"` // free-form human-readable note
	LastSeen    time.Time `json:"last_seen"`
}

// HeartbeatRegistry tracks the most-recent heartbeat for every agent.
type HeartbeatRegistry struct {
	mu   sync.RWMutex
	bots map[string]*BotStatus
}

func newHeartbeatRegistry() *HeartbeatRegistry {
	return &HeartbeatRegistry{bots: make(map[string]*BotStatus)}
}

// Update records a heartbeat for botName.
func (h *HeartbeatRegistry) Update(name, currentTask, status, message string) {
	if status == "" {
		status = "idle"
	}
	h.mu.Lock()
	h.bots[name] = &BotStatus{
		BotName:     name,
		CurrentTask: currentTask,
		Status:      status,
		Message:     message,
		LastSeen:    time.Now().UTC(),
	}
	h.mu.Unlock()
}

// All returns a snapshot of all bot statuses sorted by bot name.
func (h *HeartbeatRegistry) All() []*BotStatus {
	h.mu.RLock()
	out := make([]*BotStatus, 0, len(h.bots))
	for _, b := range h.bots {
		out = append(out, b)
	}
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].BotName < out[j].BotName })
	return out
}

// --------------------------------------------------------------------------
// HTTP handlers
// --------------------------------------------------------------------------

type heartbeatRequest struct {
	BotName     string `json:"bot_name"`
	CurrentTask string `json:"current_task"`
	Status      string `json:"status"`  // "idle", "working", "blocked"
	Message     string `json:"message"` // optional status note
}

type heartbeatResponse struct {
	OK         bool `json:"ok"`
	InboxCount int  `json:"inbox_count"` // number of messages waiting to be polled
}

// handleHeartbeat handles POST /api/heartbeat.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req heartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	botName := req.BotName
	if botName == "" {
		botName = r.Header.Get("X-Bot-Name")
	}
	if botName == "" {
		http.Error(w, `{"error":"bot_name is required"}`, http.StatusBadRequest)
		return
	}

	s.heartbeats.Update(botName, req.CurrentTask, req.Status, req.Message)
	s.events.Broadcast("heartbeat", botName)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(heartbeatResponse{
		OK:         true,
		InboxCount: s.inbox.Len(botName),
	})
}

// handleAdminHeartbeats handles GET /admin/heartbeats (JSON) — returns live agent grid.
func (s *Server) handleAdminHeartbeats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.heartbeats.All())
}
