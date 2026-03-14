package api

// Task Activity Log — Feature 3
//
// Agents narrate their progress by posting log entries to a task.
// Each entry is stored as a comment on the beads issue so it appears
// in the kanban card timeline.
//
// Routes (token-authenticated):
//   POST /api/tasks/{id}/log   — append a progress note to a task

import (
	"context"
	"encoding/json"
	"net/http"
)

// TaskLogger is the optional interface for appending a log entry to a task.
// Implementations bridge to beads.Client.AddComment or any comment store.
type TaskLogger interface {
	AddLog(ctx context.Context, issueID, actor, message string) error
}

// WithTaskLogger sets the optional TaskLogger.
func WithTaskLogger(tl TaskLogger) ServerOption {
	return func(s *Server) { s.taskLogger = tl }
}

type taskLogRequest struct {
	Message string `json:"message"`
	Level   string `json:"level"` // "info", "warn", "error" — stored as prefix
}

// handleTaskLog handles POST /api/tasks/{id}/log.
func (s *Server) handleTaskLog(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	issueID := r.PathValue("id")
	actor := r.Header.Get("X-Bot-Name")
	if actor == "" {
		actor = "bot"
	}

	var req taskLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}

	if s.taskLogger == nil {
		http.Error(w, `{"error":"task logging not configured"}`, http.StatusServiceUnavailable)
		return
	}

	text := req.Message
	if req.Level != "" && req.Level != "info" {
		text = "[" + req.Level + "] " + text
	}

	if err := s.taskLogger.AddLog(r.Context(), issueID, actor, text); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	// Broadcast so the kanban card refreshes in real-time.
	s.events.Broadcast("task-log", issueID)
	w.WriteHeader(http.StatusNoContent)
}
