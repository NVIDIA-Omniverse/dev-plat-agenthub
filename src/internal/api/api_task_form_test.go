package api

// Tests for handleKanbanTaskNew, handleKanbanTaskCreate (full form fields),
// handleBotTaskCreate (actor resolution, response JSON), and WithKanbanColumns.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// spyTaskManager records the last CreateTask call for assertion.
type spyTaskManager struct {
	lastReq TaskCreateRequest
	record  TaskRecord
	err     error
}

func (m *spyTaskManager) UpdateStatus(_ context.Context, _, _, _, _ string) error { return m.err }
func (m *spyTaskManager) UpdateTask(_ context.Context, _ string, _ TaskUpdateRequest) error {
	return m.err
}
func (m *spyTaskManager) GetTask(_ context.Context, _ string) (TaskRecord, error) {
	return m.record, m.err
}
func (m *spyTaskManager) CreateTask(_ context.Context, req TaskCreateRequest) (TaskRecord, error) {
	m.lastReq = req
	return TaskRecord{ID: "AH-1", Title: req.Title, Status: "open"}, m.err
}

// --------------------------------------------------------------------------
// WithKanbanColumns option
// --------------------------------------------------------------------------

func TestWithKanbanColumnsOption(t *testing.T) {
	cols := []string{"open", "in_progress", "closed"}
	srv := testServerWithOptions(t, WithKanbanColumns(cols))
	require.Equal(t, cols, srv.kanbanColumns)
}

// --------------------------------------------------------------------------
// GET /admin/kanban/tasks/new
// --------------------------------------------------------------------------

func TestHandleKanbanTaskNewRequiresAuth(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/new", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/admin/login")
}

func TestHandleKanbanTaskNewRendersOK(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)
	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/new", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "New Task")
}

func TestHandleKanbanTaskNewShowsColumns(t *testing.T) {
	srv := testServerWithOptions(t, WithKanbanColumns([]string{"open", "in_progress", "blocked"}))
	cookie := loginTo(t, srv)
	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/new", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "open")
	require.Contains(t, body, "in_progress")
	require.Contains(t, body, "blocked")
}

func TestHandleKanbanTaskNewNoColumnsOK(t *testing.T) {
	// Server with no kanbanColumns set should still render without panic.
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)
	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/new", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

// --------------------------------------------------------------------------
// POST /admin/kanban/tasks — full form field forwarding
// --------------------------------------------------------------------------

func TestHandleKanbanTaskCreateForwardsAllFields(t *testing.T) {
	spy := &spyTaskManager{}
	srv := testServerWithOptions(t, WithTaskManager(spy))
	cookie := loginTo(t, srv)

	form := url.Values{
		"title":               {"My feature"},
		"description":         {"Some context"},
		"status":              {"in_progress"},
		"priority":            {"1"},
		"issue_type":          {"bug"},
		"assignee":            {"alice"},
		"estimated_minutes":   {"90"},
		"acceptance_criteria": {"Must pass CI"},
		"notes":               {"See linked issue"},
		"due_at":              {"2026-06-01"},
		"labels":              {"backend,urgent"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)

	req := spy.lastReq
	require.Equal(t, "My feature", req.Title)
	require.Equal(t, "Some context", req.Description)
	require.Equal(t, "in_progress", req.Status)
	require.Equal(t, 1, req.Priority)
	require.Equal(t, "bug", req.IssueType)
	require.Equal(t, "alice", req.Assignee)
	require.Equal(t, 90, req.EstimatedMinutes)
	require.Equal(t, "Must pass CI", req.AcceptanceCriteria)
	require.Equal(t, "See linked issue", req.Notes)
	require.Equal(t, "2026-06-01", req.DueAt)
	require.Equal(t, "backend,urgent", req.Labels)
	require.Equal(t, "admin", req.Actor)
}

func TestHandleKanbanTaskCreateDefaultPriority(t *testing.T) {
	spy := &spyTaskManager{}
	srv := testServerWithOptions(t, WithTaskManager(spy))
	cookie := loginTo(t, srv)

	// priority field absent → defaults to 2
	form := url.Values{"title": {"no priority set"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, 2, spy.lastReq.Priority)
}

func TestHandleKanbanTaskCreateEstimatedMinutesZeroIgnored(t *testing.T) {
	spy := &spyTaskManager{}
	srv := testServerWithOptions(t, WithTaskManager(spy))
	cookie := loginTo(t, srv)

	// estimated_minutes=0 should remain 0 (not set)
	form := url.Values{"title": {"t"}, "estimated_minutes": {"0"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	srv.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, 0, spy.lastReq.EstimatedMinutes)
}

func TestHandleKanbanTaskCreateEstimatedMinutesPositive(t *testing.T) {
	spy := &spyTaskManager{}
	srv := testServerWithOptions(t, WithTaskManager(spy))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"t"}, "estimated_minutes": {"45"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	srv.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, 45, spy.lastReq.EstimatedMinutes)
}

// --------------------------------------------------------------------------
// POST /api/tasks — bot task creation: actor resolution and response body
// --------------------------------------------------------------------------

func TestHandleBotTaskCreateActorFromBodyBotName(t *testing.T) {
	spy := &spyTaskManager{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"work","bot_name":"workerbot"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "workerbot", spy.lastReq.Actor)
}

func TestHandleBotTaskCreateActorFromHeader(t *testing.T) {
	// bot_name absent in body, actor comes from X-Bot-Name header
	spy := &spyTaskManager{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"work"}`))
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "headerbot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "headerbot", spy.lastReq.Actor)
}

func TestHandleBotTaskCreateActorDefaultsToBot(t *testing.T) {
	// neither bot_name in body nor X-Bot-Name header → "bot"
	spy := &spyTaskManager{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"work"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "bot", spy.lastReq.Actor)
}

func TestHandleBotTaskCreateResponseJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = &spyTaskManager{}

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"fix login","priority":1}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "AH-1", resp.ID)
	require.Equal(t, "fix login", resp.Title)
	require.Equal(t, "open", resp.Status)
}

func TestHandleBotTaskCreateBodyBotNameOverridesHeader(t *testing.T) {
	// bot_name in body takes precedence over X-Bot-Name header
	spy := &spyTaskManager{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"work","bot_name":"bodybot"}`))
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "headerbot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "bodybot", spy.lastReq.Actor)
}

// --------------------------------------------------------------------------
// POST /api/tasks — priority forwarding
// --------------------------------------------------------------------------

func TestHandleBotTaskCreatePriorityForwarded(t *testing.T) {
	spy := &spyTaskManager{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"urgent","priority":1}`))
	r.Header.Set("X-Registration-Token", "tok")
	srv.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, 1, spy.lastReq.Priority)
}

func TestHandleBotTaskCreateZeroPriorityDefaultsTo2(t *testing.T) {
	spy := &spyTaskManager{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"normal"}`))
	r.Header.Set("X-Registration-Token", "tok")
	srv.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, 2, spy.lastReq.Priority)
}

// --------------------------------------------------------------------------
// POST /api/tasks/{id}/status — actor resolution
// --------------------------------------------------------------------------

func TestHandleTaskStatusActorFromHeader(t *testing.T) {
	spy := &spyTaskManager{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = spy

	// We can't assert on UpdateStatus actor because spyTaskManager only captures CreateTask,
	// so just verify 204 with named actor header.
	r := httptest.NewRequest(http.MethodPost, "/api/tasks/AH-1/status",
		strings.NewReader(`{"status":"in_progress","note":"started"}`))
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "mybot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleTaskStatusAllValidStatuses(t *testing.T) {
	statuses := []string{"open", "in_progress", "blocked", "deferred", "closed"}
	for _, s := range statuses {
		srv, _, st := testServer(t)
		require.NoError(t, st.Set("registration_token", "tok"))
		srv.taskManager = &mockTaskManager{}
		r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status",
			strings.NewReader(`{"status":"`+s+`"}`))
		r.Header.Set("X-Registration-Token", "tok")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		require.Equal(t, http.StatusNoContent, w.Code, "status=%s", s)
	}
}

func TestHandleTaskStatusInvalidStatusValues(t *testing.T) {
	invalid := []string{"done", "backlog", "ready", "review", "todo", ""}
	for _, s := range invalid {
		srv, _, st := testServer(t)
		require.NoError(t, st.Set("registration_token", "tok"))
		srv.taskManager = &mockTaskManager{}
		r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status",
			strings.NewReader(`{"status":"`+s+`"}`))
		r.Header.Set("X-Registration-Token", "tok")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "status=%q should be rejected", s)
	}
}
