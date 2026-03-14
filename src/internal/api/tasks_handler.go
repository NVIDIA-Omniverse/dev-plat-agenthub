package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

var validStatuses = map[string]bool{
	"open":        true,
	"in_progress": true,
	"blocked":     true,
	"done":        true,
	"backlog":     true,
	"ready":       true,
	"review":      true,
}

type taskStatusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (s *Server) handleTaskStatusUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	actor := r.Header.Get("X-Bot-Name")
	if actor == "" {
		actor = "bot"
	}

	var req taskStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if !validStatuses[req.Status] {
		http.Error(w, `{"error":"invalid status"}`, http.StatusBadRequest)
		return
	}

	issueID := r.PathValue("id")

	if s.taskManager == nil {
		http.Error(w, `{"error":"task management not configured"}`, http.StatusServiceUnavailable)
		return
	}

	if err := s.taskManager.UpdateStatus(r.Context(), issueID, req.Status, req.Note, actor); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// botTaskCreateRequest is the JSON body for POST /api/tasks (bot-initiated task creation).
type botTaskCreateRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	BotName     string `json:"bot_name"` // bot that will work on this task
	Priority    int    `json:"priority"` // 1=high … 5=low; 0 treated as 2 (normal)
}

// handleBotTaskCreate handles POST /api/tasks — bots call this when they receive
// a DM with work to do, ensuring every task is visible on the kanban board before
// the bot begins working (BOTJILE contract).
func (s *Server) handleBotTaskCreate(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req botTaskCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
		return
	}
	if req.Priority == 0 {
		req.Priority = 2 // default: normal priority
	}

	actor := req.BotName
	if actor == "" {
		actor = r.Header.Get("X-Bot-Name")
	}
	if actor == "" {
		actor = "bot"
	}

	if s.taskManager == nil {
		http.Error(w, `{"error":"task management not configured"}`, http.StatusServiceUnavailable)
		return
	}

	task, err := s.taskManager.CreateTask(r.Context(), TaskCreateRequest{
		Title:       req.Title,
		Description: req.Description,
		Actor:       actor,
		Priority:    req.Priority,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"id":%q,"title":%q,"status":%q}`, task.ID, task.Title, task.Status)
}
