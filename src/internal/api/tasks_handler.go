package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/kanban"
)

// validStatuses mirrors the beads status constants that pass validation on write.
var validStatuses = map[string]bool{
	"open":        true,
	"in_progress": true,
	"blocked":     true,
	"deferred":    true,
	"closed":      true,
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

	s.events.Broadcast("kanban-update", issueID)

	if req.Status == "closed" {
		if adb := s.assignmentDB(); adb != nil {
			if ta, err := adb.GetActiveAssignmentByTask(r.Context(), issueID); err == nil && ta != nil {
				_ = adb.RevokeTaskAssignment(r.Context(), ta.ID)
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// botTaskCreateRequest is the JSON body for POST /api/tasks (bot-initiated task creation).
type botTaskCreateRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	BotName     string `json:"bot_name"`   // bot that will work on this task
	Priority    int    `json:"priority"`   // 1=high … 5=low; 0 treated as 2 (normal)
	Visibility  string `json:"visibility"` // "private" = only visible to this bot; "" = global
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

	createReq := TaskCreateRequest{
		Title:       req.Title,
		Description: req.Description,
		Actor:       actor,
		Priority:    req.Priority,
	}
	if strings.EqualFold(req.Visibility, "private") {
		// Private tasks are assigned to the creating bot and labeled "private"
		// so the board filter can scope them to that agent's view.
		createReq.Assignee = actor
		createReq.Labels = "private"
	}
	task, err := s.taskManager.CreateTask(r.Context(), createReq)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	s.events.Broadcast("kanban-update", task.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"id":%q,"title":%q,"status":%q}`, task.ID, task.Title, task.Status)
}

// taskListItem is a single task in the GET /api/tasks response.
type taskListItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee,omitempty"`
	Labels   string `json:"labels,omitempty"`
}

// handleBotTaskList handles GET /api/tasks — returns the requesting bot's task queue.
// Tasks are filtered to those assigned to the bot identified by X-Bot-Name.
func (s *Server) handleBotTaskList(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	botName := r.Header.Get("X-Bot-Name")
	if botName == "" {
		http.Error(w, `{"error":"X-Bot-Name header required"}`, http.StatusBadRequest)
		return
	}

	if s.kanban == nil {
		http.Error(w, `{"error":"task management not configured"}`, http.StatusServiceUnavailable)
		return
	}

	board, err := s.kanban.Build(r.Context(), kanban.BoardFilter{Assignee: botName})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	tasks := make([]taskListItem, 0)
	for _, col := range board.Columns {
		for _, issue := range col.Issues {
			tasks = append(tasks, taskListItem{
				ID:       issue.ID,
				Title:    issue.Title,
				Status:   string(issue.Status),
				Assignee: issue.Assignee,
				Labels:   strings.Join(issue.Labels, ","),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tasks)
}
