package api

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/auth"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/kanban"
	"github.com/stretchr/testify/require"
)

// --------------------------------------------------------------------------
// Composite mock DB that satisfies BotLister + ChatDB + ProfileDB
// --------------------------------------------------------------------------

type mockFullDB struct {
	instances   []*dolt.Instance
	listErr     error
	chatMsgs    []*dolt.ChatMessage
	chatListErr error
	chatCreateErr error
	profiles    []*dolt.BotProfile
	profileMap  map[string]*dolt.BotProfile
	profileErr  error
	upsertErr   error
}

func (m *mockFullDB) ListAllInstances(_ context.Context) ([]*dolt.Instance, error) {
	return m.instances, m.listErr
}

func (m *mockFullDB) CreateChatMessage(_ context.Context, _ dolt.ChatMessage) error {
	return m.chatCreateErr
}

func (m *mockFullDB) ListChatMessages(_ context.Context, _ string, _, _ int) ([]*dolt.ChatMessage, error) {
	return m.chatMsgs, m.chatListErr
}

func (m *mockFullDB) UpsertBotProfile(_ context.Context, _ dolt.BotProfile) error {
	return m.upsertErr
}

func (m *mockFullDB) GetBotProfile(_ context.Context, name string) (*dolt.BotProfile, error) {
	if m.profileMap != nil {
		return m.profileMap[name], m.profileErr
	}
	return nil, m.profileErr
}

func (m *mockFullDB) ListBotProfiles(_ context.Context) ([]*dolt.BotProfile, error) {
	return m.profiles, m.profileErr
}

func newTestServerWithFullDB(t *testing.T, fdb *mockFullDB, opts ...ServerOption) (*Server, *memSecretStore) {
	t.Helper()
	st := newMemSecretStore()
	hash, err := hashTestPassword()
	require.NoError(t, err)
	authMgr := newTestAuthMgr(hash)
	kb := &mockKanbanBuilder{board: &kanban.Board{}}
	tmpl := testTemplates(t)
	// Add extra templates needed by handlers under test.
	tmpl["task-detail.html"] = template.Must(template.New("").Parse(
		`{{define "layout.html"}}<!DOCTYPE html><html><body><h1>Task Detail</h1>{{with .Data}}{{.Task.ID}}{{end}}</body></html>{{end}}`))
	tmpl["task-detail-panel"] = template.Must(template.New("").Parse(
		`{{define "task-detail-panel"}}<div>{{with .Data}}{{.Task.ID}} {{.Task.Title}}{{end}}</div>{{end}}`))
	tmpl["kanban-agents"] = template.Must(template.New("").Parse(
		`{{define "kanban-agents"}}{{with .Data}}{{range .}}<span>{{.Name}}</span>{{end}}{{end}}{{end}}`))
	tmpl["chat.html"] = template.Must(template.New("").Parse(
		`{{define "layout.html"}}<!DOCTYPE html><html><body><h1>Chat</h1>{{if .Error}}{{.Error}}{{end}}</body></html>{{end}}`))
	tmpl["task-create-panel"] = template.Must(template.New("").Parse(
		`{{define "task-create-panel"}}<div>panel</div>{{end}}`))
	tmpl["resources.html"] = template.Must(template.New("").Parse(
		`{{define "layout.html"}}<!DOCTYPE html><html><body><h1>Resources</h1></body></html>{{end}}`))
	tmpl["projects.html"] = template.Must(template.New("").Parse(
		`{{define "layout.html"}}<!DOCTYPE html><html><body><h1>Projects</h1></body></html>{{end}}`))

	allOpts := make([]ServerOption, 0, len(opts))
	allOpts = append(allOpts, opts...)
	srv := NewServer(authMgr, fdb, kb, st, tmpl, allOpts...)
	return srv, st
}

// --------------------------------------------------------------------------
// extractBeadsPrefix unit tests
// --------------------------------------------------------------------------

func TestExtractBeadsPrefix(t *testing.T) {
	require.Equal(t, "AH", extractBeadsPrefix("AH-42"))
	require.Equal(t, "PROJ", extractBeadsPrefix("PROJ-1"))
	require.Equal(t, "", extractBeadsPrefix("noprefix"))
	require.Equal(t, "", extractBeadsPrefix(""))
	require.Equal(t, "A", extractBeadsPrefix("A-"))
}

// --------------------------------------------------------------------------
// handleKanbanTaskDetail tests
// --------------------------------------------------------------------------

func TestHandleKanbanTaskDetailNoTaskManager(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/AH-1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleKanbanTaskDetailSuccess(t *testing.T) {
	tm := &mockTaskManager{record: TaskRecord{ID: "AH-1", Title: "Fix bug", Status: "open"}}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/AH-1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "AH-1")
}

func TestHandleKanbanTaskDetailHXRequest(t *testing.T) {
	tm := &mockTaskManager{record: TaskRecord{ID: "AH-2", Title: "Add feature", Status: "open"}}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/AH-2", nil)
	r.AddCookie(cookie)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "AH-2")
}

func TestHandleKanbanTaskDetailGetError(t *testing.T) {
	tm := &mockTaskManager{err: errors.New("not found")}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/AH-999", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// handleKanbanTaskUpdate tests
// --------------------------------------------------------------------------

func TestHandleKanbanTaskUpdateNoTaskManager(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"Updated"}, "status": {"closed"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleKanbanTaskUpdateSuccess(t *testing.T) {
	tm := &mockTaskManager{record: TaskRecord{ID: "AH-1", Title: "Updated", Status: "closed"}}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"Updated"}, "status": {"closed"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskUpdateHXRequest(t *testing.T) {
	tm := &mockTaskManager{record: TaskRecord{ID: "AH-1", Title: "Updated", Status: "in_progress"}}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"Updated"}, "status": {"in_progress"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "AH-1")
}

func TestHandleKanbanTaskUpdateError(t *testing.T) {
	tm := &mockTaskManager{err: errors.New("db error")}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"fail"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleKanbanTaskUpdateParseFormError(t *testing.T) {
	tm := &mockTaskManager{}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1", errBodyReader{})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ContentLength = -1
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// --------------------------------------------------------------------------
// handleKanbanTaskAssign tests
// --------------------------------------------------------------------------

func TestHandleKanbanTaskAssignNoTaskManager(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	form := url.Values{"assignee": {"bot1"}, "status": {"in_progress"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1/assign", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleKanbanTaskAssignSuccess(t *testing.T) {
	tm := &mockTaskManager{record: TaskRecord{ID: "AH-1", Title: "Fix it"}}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	form := url.Values{"assignee": {"bot1"}, "status": {"in_progress"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1/assign", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleKanbanTaskAssignDefaultStatus(t *testing.T) {
	tm := &mockTaskManager{}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	form := url.Values{"assignee": {"bot1"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1/assign", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleKanbanTaskAssignWithAssignmentDB(t *testing.T) {
	tm := &mockTaskManager{record: TaskRecord{ID: "AH-1", Title: "Fix it"}}
	now := time.Now().UTC()
	cdb := &mockCombinedDB{
		mockFullDB: mockFullDB{
			instances: []*dolt.Instance{{ID: "agent-1", Name: "bot1"}},
		},
		projectMap: map[string]*dolt.Project{},
		prefixMap: map[string]*dolt.Project{
			"AH": {ID: "p1", Name: "AHProject"},
		},
	}
	_ = now
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB,
		WithTaskManager(tm), WithPublicURL("https://test.example.com"))
	srv.db = cdb
	cookie := loginTo(t, srv)

	form := url.Values{"assignee": {"bot1"}, "status": {"in_progress"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1/assign", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)

	msgs := srv.inbox.Poll("bot1")
	require.NotEmpty(t, msgs)
	require.Contains(t, msgs[0].Text, "AH-1")
	require.NotNil(t, msgs[0].TaskContext)
	require.Equal(t, "p1", msgs[0].TaskContext.ProjectID)
}

func TestHandleKanbanTaskAssignNoAssignee(t *testing.T) {
	tm := &mockTaskManager{}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	form := url.Values{}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/AH-1/assign", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

// --------------------------------------------------------------------------
// handleKanbanAgents tests
// --------------------------------------------------------------------------

func TestHandleKanbanAgentsEmpty(t *testing.T) {
	fdb := &mockFullDB{instances: []*dolt.Instance{}}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/agents", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleKanbanAgentsWithBots(t *testing.T) {
	now := time.Now().UTC()
	fdb := &mockFullDB{instances: []*dolt.Instance{
		{Name: "bot1", IsAlive: true},
		{Name: "bot2", IsAlive: false, LastSeenAt: &now},
	}}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/agents", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "bot1")
	require.Contains(t, body, "bot2")
}

// --------------------------------------------------------------------------
// handleChatHistory tests
// --------------------------------------------------------------------------

func TestHandleChatHistoryUnauthorized(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleChatHistoryEmpty(t *testing.T) {
	fdb := &mockFullDB{chatMsgs: []*dolt.ChatMessage{}}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var msgs []chatMessageResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&msgs))
	require.Empty(t, msgs)
}

func TestHandleChatHistoryWithMessages(t *testing.T) {
	now := time.Now().UTC()
	fdb := &mockFullDB{chatMsgs: []*dolt.ChatMessage{
		{ID: "m1", BotName: "bot1", Sender: "owner", Body: "hello", CreatedAt: now},
	}}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1?limit=10&offset=0", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var msgs []chatMessageResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&msgs))
	require.Len(t, msgs, 1)
	require.Equal(t, "hello", msgs[0].Body)
}

func TestHandleChatHistoryWithBearerToken(t *testing.T) {
	fdb := &mockFullDB{chatMsgs: []*dolt.ChatMessage{}}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1", nil)
	r.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

// --------------------------------------------------------------------------
// handleChatSend tests
// --------------------------------------------------------------------------

func TestHandleChatSendUnauthorized(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/send",
		strings.NewReader(`{"body":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleChatSendSuccess(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/send",
		strings.NewReader(`{"body":"hello bot"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp chatMessageResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "hello bot", resp.Body)
	require.Equal(t, "owner", resp.Sender)
	require.Equal(t, "bot1", resp.BotName)
}

func TestHandleChatSendFormEncoded(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	form := url.Values{"body": {"hello form"}}
	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/send",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
}

// --------------------------------------------------------------------------
// handleChatReply tests
// --------------------------------------------------------------------------

func TestHandleChatReplyUnauthorized(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/reply",
		strings.NewReader(`{"body":"hi"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleChatReplyBotNameMismatch(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/reply",
		strings.NewReader(`{"body":"hi"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot2")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "X-Bot-Name must match path")
}

func TestHandleChatReplySuccess(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/reply",
		strings.NewReader(`{"body":"I'm on it"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp chatMessageResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "I'm on it", resp.Body)
	require.Equal(t, "bot1", resp.Sender)
}

// --------------------------------------------------------------------------
// handleGetProfile / handleUpsertProfile / handleListProfiles tests
// --------------------------------------------------------------------------

func TestHandleGetProfileUnauthorized(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)

	r := httptest.NewRequest(http.MethodGet, "/api/bots/bot1/profile", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleGetProfileFound(t *testing.T) {
	now := time.Now().UTC()
	fdb := &mockFullDB{
		profileMap: map[string]*dolt.BotProfile{
			"bot1": {
				BotName:     "bot1",
				Description: "GPU bot",
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
	}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/bots/bot1/profile", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp profileResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "bot1", resp.BotName)
	require.Equal(t, "GPU bot", resp.Description)
}

func TestHandleGetProfileNotFound(t *testing.T) {
	fdb := &mockFullDB{profileMap: map[string]*dolt.BotProfile{}}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/bots/ghost/profile", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleUpsertProfileSuccess(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	body := `{"description":"updated","specializations":["code"],"tools":["go"],"max_concurrent_tasks":2}`
	r := httptest.NewRequest(http.MethodPut, "/api/bots/bot1/profile",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp profileResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "bot1", resp.BotName)
	require.Equal(t, "updated", resp.Description)
}

func TestHandleUpsertProfileInvalidJSON(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPut, "/api/bots/bot1/profile",
		strings.NewReader("not-json"))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleListProfilesSuccess(t *testing.T) {
	now := time.Now().UTC()
	fdb := &mockFullDB{profiles: []*dolt.BotProfile{
		{BotName: "bot1", Description: "first", CreatedAt: now, UpdatedAt: now},
		{BotName: "bot2", Description: "second", CreatedAt: now, UpdatedAt: now},
	}}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/bots/profiles", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp []profileResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp, 2)
}

func TestHandleListProfilesUnauthorized(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)

	r := httptest.NewRequest(http.MethodGet, "/api/bots/profiles", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// --------------------------------------------------------------------------
// handlePutSetting / handleGetSettings tests
// --------------------------------------------------------------------------

func TestHandlePutSettingSuccess(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPut, "/api/settings/openai.model",
		strings.NewReader(`{"value":"gpt-4"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "ok")
}

func TestHandlePutSettingUnauthorized(t *testing.T) {
	srv, _, _ := testServer(t)

	r := httptest.NewRequest(http.MethodPut, "/api/settings/foo",
		strings.NewReader(`{"value":"bar"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandlePutSettingInvalidJSON(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPut, "/api/settings/foo",
		strings.NewReader("not-json"))
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleGetSettingsSuccess(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("openai_api_key", "sk-test"))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var result []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Set   bool   `json:"set"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	require.NotEmpty(t, result)

	// Sensitive keys should be masked.
	for _, kv := range result {
		if kv.Key == "openai_api_key" {
			require.True(t, kv.Set)
			require.Equal(t, "***", kv.Value)
		}
	}
}

func TestHandleGetSettingsUnauthorized(t *testing.T) {
	srv, _, _ := testServer(t)

	r := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

// --------------------------------------------------------------------------
// handleKanbanTaskNew HX-Request fragment test
// --------------------------------------------------------------------------

func TestHandleKanbanTaskNewHXRequest(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban/tasks/new?status=blocked", nil)
	r.AddCookie(cookie)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "panel")
}

// --------------------------------------------------------------------------
// handleKanbanTaskCreate HX-Request fragment test
// --------------------------------------------------------------------------

func TestHandleKanbanTaskCreateHXRequest(t *testing.T) {
	tm := &mockTaskManager{}
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskManager(tm))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"hx task"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.NotEmpty(t, w.Header().Get("HX-Trigger"))
}

// --------------------------------------------------------------------------
// slugify tests
// --------------------------------------------------------------------------

func TestSlugify(t *testing.T) {
	require.Equal(t, "my-project", slugify("My Project"))
	require.Equal(t, "hello-world", slugify("Hello World"))
	require.Equal(t, "abc123", slugify("ABC123"))
	require.Equal(t, "project", slugify("!!!"))
	require.Equal(t, "a-b-c", slugify("a_b-c"))
}

// --------------------------------------------------------------------------
// authenticateAPIUser tests
// --------------------------------------------------------------------------

func TestAuthenticateAPIUserBearerToken(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "sekrit"))

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1", nil)
	r.Header.Set("Authorization", "Bearer sekrit")
	uid, ok := srv.authenticateAPIUser(r)
	require.True(t, ok)
	require.Equal(t, "admin-bootstrap-user", uid)
}

func TestAuthenticateAPIUserBadBearer(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "sekrit"))

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1", nil)
	r.Header.Set("Authorization", "Bearer wrong")
	_, ok := srv.authenticateAPIUser(r)
	require.False(t, ok)
}

// --------------------------------------------------------------------------
// Helpers (to avoid import cycle with auth package internals)
// --------------------------------------------------------------------------

// --------------------------------------------------------------------------
// Resource API handler tests
// --------------------------------------------------------------------------

type mockResourceDB struct {
	mockFullDB
	resources    []*dolt.Resource
	resourceMap  map[string]*dolt.Resource
	createErr    error
	getErr       error
	listErr2     error
	deleteErr    error
}

func (m *mockResourceDB) CreateResource(_ context.Context, _ dolt.Resource) error { return m.createErr }
func (m *mockResourceDB) GetResource(_ context.Context, id string) (*dolt.Resource, error) {
	if m.resourceMap != nil {
		return m.resourceMap[id], m.getErr
	}
	return nil, m.getErr
}
func (m *mockResourceDB) ListResourcesByOwner(_ context.Context, _ string) ([]*dolt.Resource, error) {
	return m.resources, m.listErr2
}
func (m *mockResourceDB) DeleteResource(_ context.Context, _ string) error { return m.deleteErr }
func (m *mockResourceDB) UpdateResourceMeta(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}

func TestHandleCreateResourceSuccess(t *testing.T) {
	rdb := &mockResourceDB{}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	body := `{"name":"myrepo","resource_type":"github_repo","credentials":{"token":"ghp_xxx"}}`
	r := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, w.Body.String(), "myrepo")
}

func TestHandleCreateResourceUnauthorized(t *testing.T) {
	rdb := &mockResourceDB{}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb

	body := `{"name":"x","resource_type":"github_repo"}`
	r := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleCreateResourceMissingFields(t *testing.T) {
	rdb := &mockResourceDB{}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	body := `{"name":"","resource_type":""}`
	r := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleListResourcesSuccess(t *testing.T) {
	now := time.Now().UTC()
	rdb := &mockResourceDB{resources: []*dolt.Resource{
		{ID: "r1", Name: "repo1", ResourceType: dolt.ResourceTypeGitHubRepo, CreatedAt: now, UpdatedAt: now},
	}}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/resources", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp []resourceResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp, 1)
}

func TestHandleGetResourceFound(t *testing.T) {
	now := time.Now().UTC()
	rdb := &mockResourceDB{resourceMap: map[string]*dolt.Resource{
		"r1": {ID: "r1", OwnerID: "admin-bootstrap-user", Name: "repo1", ResourceType: dolt.ResourceTypeGitHubRepo, CreatedAt: now, UpdatedAt: now},
	}}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleGetResourceNotFound(t *testing.T) {
	rdb := &mockResourceDB{resourceMap: map[string]*dolt.Resource{}}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/resources/nonexistent", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleDeleteResourceSuccess(t *testing.T) {
	now := time.Now().UTC()
	rdb := &mockResourceDB{resourceMap: map[string]*dolt.Resource{
		"r1": {ID: "r1", OwnerID: "admin-bootstrap-user", Name: "repo1", CreatedAt: now, UpdatedAt: now},
	}}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

// --------------------------------------------------------------------------
// Project API handler tests
// --------------------------------------------------------------------------

type mockProjectDB struct {
	mockFullDB
	projects    []*dolt.Project
	projectMap  map[string]*dolt.Project
	prefixMap   map[string]*dolt.Project
	createErr   error
	getErr      error
	listErr2    error
	agents      []*dolt.ProjectAgent
	projResources []*dolt.ProjectResource
}

func (m *mockProjectDB) CreateProject(_ context.Context, _ dolt.Project) error { return m.createErr }
func (m *mockProjectDB) GetProject(_ context.Context, id string) (*dolt.Project, error) {
	if m.projectMap != nil {
		return m.projectMap[id], m.getErr
	}
	return nil, m.getErr
}
func (m *mockProjectDB) GetProjectByBeadsPrefix(_ context.Context, prefix string) (*dolt.Project, error) {
	if m.prefixMap != nil {
		return m.prefixMap[prefix], m.getErr
	}
	return nil, m.getErr
}
func (m *mockProjectDB) ListAllProjects(_ context.Context) ([]*dolt.Project, error) {
	return m.projects, m.listErr2
}
func (m *mockProjectDB) ListProjectsByOwner(_ context.Context, _ string) ([]*dolt.Project, error) {
	return m.projects, m.listErr2
}
func (m *mockProjectDB) UpdateProject(_ context.Context, _ dolt.Project) error { return nil }
func (m *mockProjectDB) AddProjectResource(_ context.Context, _, _ string, _ bool) error { return nil }
func (m *mockProjectDB) RemoveProjectResource(_ context.Context, _, _ string) error { return nil }
func (m *mockProjectDB) ListProjectResources(_ context.Context, _ string) ([]*dolt.ProjectResource, error) {
	return m.projResources, nil
}
func (m *mockProjectDB) AddProjectAgent(_ context.Context, _, _, _ string) error { return nil }
func (m *mockProjectDB) RemoveProjectAgent(_ context.Context, _, _ string) error { return nil }
func (m *mockProjectDB) ListProjectAgents(_ context.Context, _ string) ([]*dolt.ProjectAgent, error) {
	return m.agents, nil
}

func TestHandleAPIListProjectsSuccess(t *testing.T) {
	now := time.Now().UTC()
	pdb := &mockProjectDB{projects: []*dolt.Project{
		{ID: "p1", Name: "Proj1", CreatedAt: now, UpdatedAt: now},
	}}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp []projectResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp, 1)
}

func TestHandleAPIGetProjectFound(t *testing.T) {
	now := time.Now().UTC()
	pdb := &mockProjectDB{projectMap: map[string]*dolt.Project{
		"p1": {ID: "p1", Name: "Proj1", CreatedAt: now, UpdatedAt: now},
	}}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/projects/p1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleAPIGetProjectNotFound(t *testing.T) {
	pdb := &mockProjectDB{projectMap: map[string]*dolt.Project{}}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/projects/nonexistent", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleAPIAddProjectResourceSuccess(t *testing.T) {
	pdb := &mockProjectDB{}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb
	cookie := loginTo(t, srv)

	body := `{"resource_id":"r1","is_primary":true}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleAPIRemoveProjectResourceSuccess(t *testing.T) {
	pdb := &mockProjectDB{}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/projects/p1/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleAPIAddProjectAgentSuccess(t *testing.T) {
	pdb := &mockProjectDB{}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb
	cookie := loginTo(t, srv)

	body := `{"agent_id":"a1","granted_by":"admin"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleAPIRemoveProjectAgentSuccess(t *testing.T) {
	pdb := &mockProjectDB{}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/projects/p1/agents/a1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleAPIListProjectsUnauthorized(t *testing.T) {
	pdb := &mockProjectDB{}
	srv, _ := newTestServerWithFullDB(t, &pdb.mockFullDB)
	srv.db = pdb

	r := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// --------------------------------------------------------------------------
// handleChatPage tests
// --------------------------------------------------------------------------

func TestHandleChatPageSuccess(t *testing.T) {
	now := time.Now().UTC()
	fdb := &mockFullDB{chatMsgs: []*dolt.ChatMessage{
		{ID: "m1", BotName: "bot1", Sender: "owner", Body: "hi", CreatedAt: now},
	}}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/chat/bot1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Chat")
}

func TestHandleChatPageEmptyHistory(t *testing.T) {
	fdb := &mockFullDB{chatMsgs: []*dolt.ChatMessage{}}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/chat/bot1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleChatPageError(t *testing.T) {
	fdb := &mockFullDB{chatListErr: errors.New("db down")}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/chat/bot1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "db down")
}

// --------------------------------------------------------------------------
// Admin UI resource page handler tests
// --------------------------------------------------------------------------

func TestHandleResourcesPageSuccess(t *testing.T) {
	rdb := &mockResourceDB{resources: []*dolt.Resource{}}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/resources", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Resources")
}

func TestHandleResourceCreateFormSuccess(t *testing.T) {
	rdb := &mockResourceDB{}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	form := url.Values{
		"name":          {"myrepo"},
		"resource_type": {"github_repo"},
		"url":           {"https://github.com/test/repo"},
		"token":         {"ghp_xxx"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/resources", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleResourceCreateFormMissingName(t *testing.T) {
	rdb := &mockResourceDB{}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	form := url.Values{"name": {""}, "resource_type": {""}}
	r := httptest.NewRequest(http.MethodPost, "/admin/resources", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

// --------------------------------------------------------------------------
// Admin UI project page handler tests
// --------------------------------------------------------------------------

type mockCombinedDB struct {
	mockFullDB
	// ResourceDB
	resources   []*dolt.Resource
	resourceMap map[string]*dolt.Resource
	createResErr error
	// ProjectDB
	projects    []*dolt.Project
	projectMap  map[string]*dolt.Project
	prefixMap   map[string]*dolt.Project
	createProjErr error
	agents      []*dolt.ProjectAgent
	projResources []*dolt.ProjectResource
	// AssignmentDB
	assignments map[string]*dolt.TaskAssignment
}

func (m *mockCombinedDB) CreateResource(_ context.Context, _ dolt.Resource) error { return m.createResErr }
func (m *mockCombinedDB) GetResource(_ context.Context, id string) (*dolt.Resource, error) {
	if m.resourceMap != nil {
		return m.resourceMap[id], nil
	}
	return nil, nil
}
func (m *mockCombinedDB) ListResourcesByOwner(_ context.Context, _ string) ([]*dolt.Resource, error) {
	return m.resources, nil
}
func (m *mockCombinedDB) DeleteResource(_ context.Context, _ string) error { return nil }
func (m *mockCombinedDB) UpdateResourceMeta(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}
func (m *mockCombinedDB) CreateProject(_ context.Context, _ dolt.Project) error { return m.createProjErr }
func (m *mockCombinedDB) GetProject(_ context.Context, id string) (*dolt.Project, error) {
	if m.projectMap != nil {
		return m.projectMap[id], nil
	}
	return nil, nil
}
func (m *mockCombinedDB) GetProjectByBeadsPrefix(_ context.Context, prefix string) (*dolt.Project, error) {
	if m.prefixMap != nil {
		return m.prefixMap[prefix], nil
	}
	return nil, nil
}
func (m *mockCombinedDB) ListAllProjects(_ context.Context) ([]*dolt.Project, error) {
	return m.projects, nil
}
func (m *mockCombinedDB) ListProjectsByOwner(_ context.Context, _ string) ([]*dolt.Project, error) {
	return m.projects, nil
}
func (m *mockCombinedDB) UpdateProject(_ context.Context, _ dolt.Project) error { return nil }
func (m *mockCombinedDB) AddProjectResource(_ context.Context, _, _ string, _ bool) error { return nil }
func (m *mockCombinedDB) RemoveProjectResource(_ context.Context, _, _ string) error { return nil }
func (m *mockCombinedDB) ListProjectResources(_ context.Context, _ string) ([]*dolt.ProjectResource, error) {
	return m.projResources, nil
}
func (m *mockCombinedDB) AddProjectAgent(_ context.Context, _, _, _ string) error { return nil }
func (m *mockCombinedDB) RemoveProjectAgent(_ context.Context, _, _ string) error { return nil }
func (m *mockCombinedDB) ListProjectAgents(_ context.Context, _ string) ([]*dolt.ProjectAgent, error) {
	return m.agents, nil
}
func (m *mockCombinedDB) GetTaskAssignment(_ context.Context, id string) (*dolt.TaskAssignment, error) {
	if m.assignments != nil {
		return m.assignments[id], nil
	}
	return nil, nil
}
func (m *mockCombinedDB) CreateTaskAssignment(_ context.Context, _ dolt.TaskAssignment) error { return nil }
func (m *mockCombinedDB) GetActiveAssignmentByTaskAndAgent(_ context.Context, _, _ string) (*dolt.TaskAssignment, error) {
	return nil, nil
}
func (m *mockCombinedDB) GetActiveAssignmentByTask(_ context.Context, _ string) (*dolt.TaskAssignment, error) {
	return nil, nil
}
func (m *mockCombinedDB) RevokeTaskAssignment(_ context.Context, _ string) error { return nil }
func (m *mockCombinedDB) ListActiveAssignmentsByAgent(_ context.Context, _ string) ([]*dolt.TaskAssignment, error) {
	return nil, nil
}

func TestHandleProjectsPageSuccess(t *testing.T) {
	cdb := &mockCombinedDB{projects: []*dolt.Project{}}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/projects", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Projects")
}

func TestHandleProjectDetailFound(t *testing.T) {
	now := time.Now().UTC()
	cdb := &mockCombinedDB{
		projectMap: map[string]*dolt.Project{
			"p1": {ID: "p1", Name: "MyProj", CreatedAt: now, UpdatedAt: now},
		},
	}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/projects/p1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleProjectDetailNotFound(t *testing.T) {
	cdb := &mockCombinedDB{projectMap: map[string]*dolt.Project{}}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/projects/nonexistent", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleProjectCreateFormSuccess(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	form := url.Values{
		"name":         {"TestProject"},
		"description":  {"A test project"},
		"beads_prefix": {"TP"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleProjectCreateFormMissingName(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	form := url.Values{"name": {""}}
	r := httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleProjectCreateWithInlineResource(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	form := url.Values{
		"name":               {"TestProject"},
		"new_resource_name":  {"myrepo"},
		"new_resource_url":   {"https://github.com/test/repo"},
		"new_resource_token": {"ghp_xxx"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

// --------------------------------------------------------------------------
// handleGetCredentials tests
// --------------------------------------------------------------------------

func TestHandleGetCredentialsUnauthorized(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)

	r := httptest.NewRequest(http.MethodGet, "/api/credentials/assign-1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleGetCredentialsMissingBotName(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/credentials/assign-1", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleGetCredentialsAssignmentNotFound(t *testing.T) {
	cdb := &mockCombinedDB{assignments: map[string]*dolt.TaskAssignment{}}
	srv, st := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/credentials/nonexistent", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleGetCredentialsSuccess(t *testing.T) {
	now := time.Now().UTC()
	cdb := &mockCombinedDB{
		mockFullDB: mockFullDB{
			instances: []*dolt.Instance{{ID: "agent-1", Name: "bot1"}},
		},
		assignments: map[string]*dolt.TaskAssignment{
			"assign-1": {
				ID:        "assign-1",
				TaskID:    "AH-42",
				ProjectID: "p1",
				AgentID:   "agent-1",
				AssignedAt: now,
			},
		},
		projectMap: map[string]*dolt.Project{
			"p1": {ID: "p1", Name: "TestProject"},
		},
		projResources: []*dolt.ProjectResource{
			{ProjectID: "p1", ResourceID: "r1"},
		},
		resourceMap: map[string]*dolt.Resource{
			"r1": {ID: "r1", Name: "repo1", ResourceType: dolt.ResourceTypeGitHubRepo, ResourceMeta: json.RawMessage(`{"url":"https://github.com"}`)},
		},
	}
	srv, st := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.SetResourceCredential("r1", "token", "ghp_secret"))

	r := httptest.NewRequest(http.MethodGet, "/api/credentials/assign-1", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp credentialResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "assign-1", resp.TaskAssignmentID)
	require.Equal(t, "AH-42", resp.TaskID)
	require.Equal(t, "TestProject", resp.ProjectName)
	require.Len(t, resp.Resources, 1)
	require.Equal(t, "ghp_secret", resp.Resources[0].Credentials["token"])
}

// --------------------------------------------------------------------------
// Server option tests
// --------------------------------------------------------------------------

func TestWithTaskLoggerOption(t *testing.T) {
	fdb := &mockFullDB{}
	tl := &mockTaskLogger{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithTaskLogger(tl))
	require.NotNil(t, srv.taskLogger)
}

type mockTaskLogger struct{}

func (m *mockTaskLogger) AddLog(_ context.Context, _, _, _ string) error { return nil }

func TestWithPublicURLOption(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithPublicURL("https://example.com"))
	require.Equal(t, "https://example.com", srv.publicURL)
}

func TestWithAnnouncerOption(t *testing.T) {
	fdb := &mockFullDB{}
	a := &mockAnnouncer{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithAnnouncer(a, "C123"))
	require.NotNil(t, srv.announcer)
	require.Equal(t, "C123", srv.announceChannel)
}

type mockAnnouncer struct{}

func (m *mockAnnouncer) PostMessage(_ context.Context, _, _ string) error { return nil }

type mockSlackUpdater struct{ called bool }

func (m *mockSlackUpdater) UpdateAgentSlackChannel(_ context.Context, _, _ string) error {
	m.called = true
	return nil
}

func TestWithAgentSlackChannelUpdaterOption(t *testing.T) {
	fdb := &mockFullDB{}
	u := &mockSlackUpdater{}
	srv, _ := newTestServerWithFullDB(t, fdb, WithAgentSlackChannelUpdater(u))
	require.NotNil(t, srv.agentSlackUpdater)
}

// --------------------------------------------------------------------------
// Inbox() and SetAnnouncer() accessor tests
// --------------------------------------------------------------------------

func TestInboxAccessor(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	require.NotNil(t, srv.Inbox())
}

func TestSetAnnouncerMethod(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	a := &mockAnnouncer{}
	srv.SetAnnouncer(a, "CH-1")
	require.NotNil(t, srv.announcer)
	require.Equal(t, "CH-1", srv.announceChannel)
}

// --------------------------------------------------------------------------
// Inbox message conversion tests
// --------------------------------------------------------------------------

func TestInboxMessageToDB(t *testing.T) {
	now := time.Now().UTC()
	msg := &InboxMessage{
		ID:        "msg-1",
		From:      "user1",
		Channel:   "C123",
		Text:      "hello",
		CreatedAt: now,
		TaskContext: &TaskContext{
			TaskAssignmentID: "ta-1",
			TaskID:           "AH-1",
			ProjectID:        "p1",
			ProjectName:      "Proj",
		},
	}
	dbMsg := inboxMessageToDB("bot1", msg)
	require.Equal(t, "msg-1", dbMsg.ID)
	require.Equal(t, "bot1", dbMsg.BotName)
	require.Equal(t, "user1", dbMsg.FromUser)
	require.Contains(t, string(dbMsg.TaskContext), "ta-1")
}

func TestInboxMessageToDBNilContext(t *testing.T) {
	msg := &InboxMessage{ID: "msg-2", Text: "no context", CreatedAt: time.Now().UTC()}
	dbMsg := inboxMessageToDB("bot1", msg)
	require.Equal(t, "{}", string(dbMsg.TaskContext))
}

func TestDBMessageToInbox(t *testing.T) {
	now := time.Now().UTC()
	tcJSON, _ := json.Marshal(TaskContext{
		TaskAssignmentID: "ta-1",
		TaskID:           "AH-1",
	})
	dbMsg := &dolt.InboxDBMessage{
		ID:          "msg-1",
		BotName:     "bot1",
		FromUser:    "user1",
		Channel:     "C123",
		Body:        "hello",
		TaskContext: tcJSON,
		CreatedAt:   now,
	}
	msg := dbMessageToInbox(dbMsg)
	require.Equal(t, "msg-1", msg.ID)
	require.Equal(t, "user1", msg.From)
	require.NotNil(t, msg.TaskContext)
	require.Equal(t, "ta-1", msg.TaskContext.TaskAssignmentID)
}

func TestDBMessageToInboxEmptyContext(t *testing.T) {
	dbMsg := &dolt.InboxDBMessage{
		ID:          "msg-2",
		Body:        "no ctx",
		TaskContext: []byte("{}"),
		CreatedAt:   time.Now().UTC(),
	}
	msg := dbMessageToInbox(dbMsg)
	require.Nil(t, msg.TaskContext)
}

func TestInboxSetDB(t *testing.T) {
	inbox := newInbox()
	require.Nil(t, inbox.db)
	inbox.SetDB(nil) // no-op but exercises the method
	require.Nil(t, inbox.db)
}

// --------------------------------------------------------------------------
// newAPIUUID test
// --------------------------------------------------------------------------

func TestNewAPIUUID(t *testing.T) {
	id, err := newAPIUUID()
	require.NoError(t, err)
	require.Len(t, id, 36) // UUID format: 8-4-4-4-12
	require.Contains(t, id, "-")

	id2, err := newAPIUUID()
	require.NoError(t, err)
	require.NotEqual(t, id, id2)
}

// --------------------------------------------------------------------------
// generateRandHex test
// --------------------------------------------------------------------------

func TestGenerateRandHex(t *testing.T) {
	hex1, err := generateRandHex(16)
	require.NoError(t, err)
	require.Len(t, hex1, 32) // 16 bytes -> 32 hex chars

	hex2, err := generateRandHex(16)
	require.NoError(t, err)
	require.NotEqual(t, hex1, hex2)
}

// --------------------------------------------------------------------------
// EventBroadcaster subscribe/broadcast tests
// --------------------------------------------------------------------------

func TestEventBroadcasterSubscribeAndBroadcast(t *testing.T) {
	eb := newEventBroadcaster()
	ch, unsub := eb.subscribe()
	defer unsub()

	eb.Broadcast("kanban-update", "AH-1")

	select {
	case msg := <-ch:
		require.Contains(t, msg, "kanban-update")
		require.Contains(t, msg, "AH-1")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for broadcast")
	}
}

func TestEventBroadcasterUnsubscribe(t *testing.T) {
	eb := newEventBroadcaster()
	ch, unsub := eb.subscribe()
	unsub()

	eb.Broadcast("test", "data")

	select {
	case <-ch:
		t.Fatal("should not receive after unsubscribe")
	default:
	}
}

func TestHandleAdminEventsConnectsAndReceives(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/admin/events", nil).WithContext(ctx)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.ServeHTTP(w, r)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	srv.events.Broadcast("kanban-update", "AH-99")
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-done

	body := w.Body.String()
	require.Contains(t, body, ": connected")
	require.Contains(t, body, "kanban-update")
	require.Contains(t, body, "AH-99")
}

// --------------------------------------------------------------------------
// handleAPICreateProject test (through API, not form)
// --------------------------------------------------------------------------

func TestHandleAPICreateProjectSuccess(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	body := `{"name":"NewProject","description":"test","beads_prefix":"NP"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp projectResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "NewProject", resp.Name)
	require.Equal(t, "NP", resp.BeadsPrefix)
}

func TestHandleAPICreateProjectMissingName(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	body := `{"name":"","description":"test"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleAPICreateProjectUnauthorized(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb

	body := `{"name":"Test"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// --------------------------------------------------------------------------
// resourceToResponse / projectToResponse unit tests
// --------------------------------------------------------------------------

func TestResourceToResponseNilMeta(t *testing.T) {
	r := &dolt.Resource{ID: "r1", Name: "repo", ResourceType: dolt.ResourceTypeGitHubRepo}
	resp := resourceToResponse(r)
	require.Equal(t, "{}", string(resp.Meta))
}

func TestProjectToResponse(t *testing.T) {
	now := time.Now().UTC()
	p := &dolt.Project{ID: "p1", Name: "Proj", OwnerID: "admin", Description: "desc",
		SlackChannelID: "C1", SlackChannelName: "proj", BeadsPrefix: "PR", CreatedAt: now}
	resp := projectToResponse(p)
	require.Equal(t, "p1", resp.ID)
	require.Equal(t, "Proj", resp.Name)
	require.Equal(t, "PR", resp.BeadsPrefix)
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func hashTestPassword() (string, error) {
	return auth.HashPassword("adminpassword")
}

func newTestAuthMgr(hash string) *auth.Manager {
	return auth.NewManager([]byte("test-secret-32-bytes-padding!!!"), []byte(hash), "test_session")
}

// --------------------------------------------------------------------------
// Mock types for DB-backed inbox, heartbeater, and HTTP transport
// --------------------------------------------------------------------------

type mockInboxListerDB struct {
	instances     []*dolt.Instance
	listErr       error
	pendingMsgs   []*dolt.InboxDBMessage
	pendingErr    error
	ackCalledWith string
}

func (m *mockInboxListerDB) ListAllInstances(_ context.Context) ([]*dolt.Instance, error) {
	return m.instances, m.listErr
}
func (m *mockInboxListerDB) CreateInboxMessage(_ context.Context, _ dolt.InboxDBMessage) error {
	return nil
}
func (m *mockInboxListerDB) ListPendingMessages(_ context.Context, _ string) ([]*dolt.InboxDBMessage, error) {
	return m.pendingMsgs, m.pendingErr
}
func (m *mockInboxListerDB) AckInboxMessage(_ context.Context, id string) error {
	m.ackCalledWith = id
	return nil
}

type mockHeartbeaterListerDB struct {
	instances  []*dolt.Instance
	listErr    error
	lastBot    string
	lastStatus string
}

func (m *mockHeartbeaterListerDB) ListAllInstances(_ context.Context) ([]*dolt.Instance, error) {
	return m.instances, m.listErr
}
func (m *mockHeartbeaterListerDB) UpdateHeartbeat(_ context.Context, name, _, status, _ string) error {
	m.lastBot = name
	m.lastStatus = status
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// --------------------------------------------------------------------------
// handleLLMEscalate — full flow with mocked LLM API server
// --------------------------------------------------------------------------

func TestHandleLLMEscalateSuccessfulFlow(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "Hello from LLM"}},
			},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
			},
		})
	}))
	defer llmServer.Close()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.Set("llm_escalation_base_url", llmServer.URL))
	require.NoError(t, st.Set("llm_escalation_model", "gpt-4"))
	require.NoError(t, st.Set("llm_escalation_api_key", "sk-test"))

	body := `{"messages":[{"role":"user","content":"hello"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "testbot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp escalateResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "gpt-4", resp.Model)
	require.Equal(t, "Hello from LLM", resp.Content)
	require.Equal(t, 10, resp.Usage.InputTokens)
	require.Equal(t, 5, resp.Usage.OutputTokens)
}

func TestHandleLLMEscalateWithModelHint(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "ok"}},
			},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer llmServer.Close()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.Set("llm_escalation_base_url", llmServer.URL))
	require.NoError(t, st.Set("llm_escalation_model", "gpt-4"))
	require.NoError(t, st.Set("llm_escalation_api_key", "sk-key"))

	body := `{"messages":[{"role":"user","content":"hi"}],"model_hint":"gpt-4-turbo","max_tokens":512}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp escalateResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "gpt-4-turbo", resp.Model)
}

func TestHandleLLMEscalateUpstreamNon200(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer llmServer.Close()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.Set("llm_escalation_base_url", llmServer.URL))
	require.NoError(t, st.Set("llm_escalation_model", "gpt-4"))
	require.NoError(t, st.Set("llm_escalation_api_key", "sk-key"))

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestHandleLLMEscalateUpstreamBadJSON(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("not json"))
	}))
	defer llmServer.Close()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.Set("llm_escalation_base_url", llmServer.URL))
	require.NoError(t, st.Set("llm_escalation_model", "gpt-4"))
	require.NoError(t, st.Set("llm_escalation_api_key", "sk-key"))

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "not json")
}

func TestHandleLLMEscalateInvalidRequestJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(`not json`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleLLMEscalateWithUsageLogging(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "logged"}},
			},
			"usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 50},
		})
	}))
	defer llmServer.Close()

	udb := &mockUsageDB{}
	srv, _, st := testServer(t)
	srv.db = udb
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.Set("llm_escalation_base_url", llmServer.URL))
	require.NoError(t, st.Set("llm_escalation_model", "gpt-4"))
	require.NoError(t, st.Set("llm_escalation_api_key", "sk-key"))

	body := `{"messages":[{"role":"user","content":"log me"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleLLMEscalateEmptyChoices(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 0},
		})
	}))
	defer llmServer.Close()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.Set("llm_escalation_base_url", llmServer.URL))
	require.NoError(t, st.Set("llm_escalation_model", "gpt-4"))
	require.NoError(t, st.Set("llm_escalation_api_key", "sk-key"))

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp escalateResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "", resp.Content)
}

// --------------------------------------------------------------------------
// Slack helper tests with mocked HTTP transport
// --------------------------------------------------------------------------

func TestCreateSlackChannelSuccess(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rec).Encode(map[string]interface{}{
			"ok":      true,
			"channel": map[string]string{"id": "C123", "name": "test-project"},
		})
		return rec.Result(), nil
	})
	defer func() { http.DefaultTransport = orig }()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("slack_bot_token", "xoxb-test"))

	chID, err := srv.createSlackChannel(context.Background(), "test-project")
	require.NoError(t, err)
	require.Equal(t, "C123", chID)
}

func TestCreateSlackChannelNameTaken(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.URL.String(), "conversations.create") {
			_ = json.NewEncoder(rec).Encode(map[string]interface{}{
				"ok":    false,
				"error": "name_taken",
			})
		} else if strings.Contains(req.URL.String(), "conversations.list") {
			_ = json.NewEncoder(rec).Encode(map[string]interface{}{
				"ok": true,
				"channels": []map[string]string{
					{"id": "C456", "name": "test-project"},
				},
			})
		}
		return rec.Result(), nil
	})
	defer func() { http.DefaultTransport = orig }()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("slack_bot_token", "xoxb-test"))

	chID, err := srv.createSlackChannel(context.Background(), "test-project")
	require.NoError(t, err)
	require.Equal(t, "C456", chID)
}

func TestCreateSlackChannelAPIError(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rec).Encode(map[string]interface{}{
			"ok":    false,
			"error": "invalid_auth",
		})
		return rec.Result(), nil
	})
	defer func() { http.DefaultTransport = orig }()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("slack_bot_token", "xoxb-test"))

	_, err := srv.createSlackChannel(context.Background(), "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid_auth")
}

func TestCreateSlackChannelNoStore(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.store = nil

	_, err := srv.createSlackChannel(context.Background(), "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "store not configured")
}

func TestCreateSlackChannelNoToken(t *testing.T) {
	srv, _, _ := testServer(t)

	_, err := srv.createSlackChannel(context.Background(), "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "slack_bot_token not configured")
}

func TestFindSlackChannelSuccess(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rec).Encode(map[string]interface{}{
			"ok": true,
			"channels": []map[string]string{
				{"id": "C789", "name": "target-channel"},
				{"id": "C000", "name": "other-channel"},
			},
		})
		return rec.Result(), nil
	})
	defer func() { http.DefaultTransport = orig }()

	srv, _, _ := testServer(t)
	chID, err := srv.findSlackChannel(context.Background(), "xoxb-test", "target-channel")
	require.NoError(t, err)
	require.Equal(t, "C789", chID)
}

func TestFindSlackChannelNotFound(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rec).Encode(map[string]interface{}{
			"ok":       true,
			"channels": []map[string]string{},
		})
		return rec.Result(), nil
	})
	defer func() { http.DefaultTransport = orig }()

	srv, _, _ := testServer(t)
	_, err := srv.findSlackChannel(context.Background(), "xoxb-test", "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestPostSlackMessageSuccess(t *testing.T) {
	orig := http.DefaultTransport
	called := false
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rec).Encode(map[string]interface{}{"ok": true})
		return rec.Result(), nil
	})
	defer func() { http.DefaultTransport = orig }()

	srv, _, st := testServer(t)
	require.NoError(t, st.Set("slack_bot_token", "xoxb-test"))

	srv.postSlackMessage(context.Background(), "C123", "hello world")
	require.True(t, called)
}

func TestPostSlackMessageNoStore(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.store = nil
	srv.postSlackMessage(context.Background(), "C123", "hello")
}

func TestPostSlackMessageNoToken(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.postSlackMessage(context.Background(), "C123", "hello")
}

// --------------------------------------------------------------------------
// handleInboxPoll / handleInboxAck — DB-backed paths
// --------------------------------------------------------------------------

func TestInboxPollWithDBBackend(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	now := time.Now().UTC()
	idb := &mockInboxListerDB{
		pendingMsgs: []*dolt.InboxDBMessage{
			{ID: "msg-1", BotName: "bot1", FromUser: "user1", Channel: "C1", Body: "db message", TaskContext: []byte("{}"), CreatedAt: now},
		},
	}
	srv.inbox.SetDB(idb)

	r := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var msgs []*InboxMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Len(t, msgs, 1)
	require.Equal(t, "db message", msgs[0].Text)
}

func TestInboxPollDBErrorFallsBackToMemory(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	idb := &mockInboxListerDB{pendingErr: errors.New("db down")}
	srv.inbox.SetDB(idb)
	srv.inbox.Enqueue("bot1", "user1", "C1", "memory message")

	r := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var msgs []*InboxMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Len(t, msgs, 1)
	require.Equal(t, "memory message", msgs[0].Text)
}

func TestInboxAckWithDBBackend(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	idb := &mockInboxListerDB{}
	srv.inbox.SetDB(idb)
	msgID := srv.inbox.Enqueue("bot1", "user1", "C1", "ack me")

	r := httptest.NewRequest(http.MethodPost, "/api/inbox/"+msgID+"/ack", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, msgID, idb.ackCalledWith)
}

// --------------------------------------------------------------------------
// handleHeartbeat — edge cases
// --------------------------------------------------------------------------

func TestHeartbeatInvalidJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/heartbeat",
		strings.NewReader(`not json`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHeartbeatWithBotHeartbeater(t *testing.T) {
	hdb := &mockHeartbeaterListerDB{}
	st := newMemSecretStore()
	hash, err := hashTestPassword()
	require.NoError(t, err)
	authMgr := newTestAuthMgr(hash)
	kb := &mockKanbanBuilder{board: &kanban.Board{}}
	tmpl := testTemplates(t)
	srv := NewServer(authMgr, hdb, kb, st, tmpl)

	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/heartbeat",
		strings.NewReader(`{"bot_name":"worker1","status":"working"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "worker1", hdb.lastBot)
	require.Equal(t, "working", hdb.lastStatus)
}

// --------------------------------------------------------------------------
// handleRegister — uncovered branches
// --------------------------------------------------------------------------

func TestHandleRegisterWithProfile(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.registrar = &mockBotRegistrar{}

	body := `{"name":"bot1","host":"1.2.3.4","port":8080,"profile":{"description":"GPU bot","specializations":["ml"],"tools":["pytorch"],"max_concurrent_tasks":3}}`
	r := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(body))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
}

func TestHandleRegisterSkipProbe(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.registrar = &mockBotRegistrar{}
	srv.healthProber = &mockHealthProber{err: errors.New("unreachable")}

	body := `{"name":"bot2","host":"1.2.3.4","port":8080}`
	r := httptest.NewRequest(http.MethodPost, "/api/register?skip_probe=1", strings.NewReader(body))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
}

func TestHandleRegisterWithAnnouncerAndPublicURL(t *testing.T) {
	fdb := &mockFullDB{}
	ann := &mockAnnouncer{}
	srv, st := newTestServerWithFullDB(t, fdb, WithAnnouncer(ann, "C-announce"), WithPublicURL("https://test.example.com"))
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.registrar = &mockBotRegistrar{}

	body := `{"name":"bot3","host":"1.2.3.4","port":8080}`
	r := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(body))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
}

func TestHandleRegisterWithSlackChannelUpdater(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rec).Encode(map[string]interface{}{
			"ok":      true,
			"channel": map[string]string{"id": "Cagent", "name": "agent-bot4"},
		})
		return rec.Result(), nil
	})
	defer func() { http.DefaultTransport = orig }()

	fdb := &mockFullDB{}
	upd := &mockSlackUpdater{}
	ann := &mockAnnouncer{}
	srv, st := newTestServerWithFullDB(t, fdb, WithAgentSlackChannelUpdater(upd), WithAnnouncer(ann, "C-ann"), WithPublicURL("https://x.com"))
	require.NoError(t, st.Set("registration_token", "tok"))
	require.NoError(t, st.Set("slack_bot_token", "xoxb-test"))
	srv.registrar = &mockBotRegistrar{}

	body := `{"name":"bot4","host":"1.2.3.4","port":8080}`
	r := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(body))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.True(t, upd.called)
}

// --------------------------------------------------------------------------
// Chat handler error paths
// --------------------------------------------------------------------------

func TestHandleChatSendNoChatDB(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/send",
		strings.NewReader(`{"body":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleChatSendDBError(t *testing.T) {
	fdb := &mockFullDB{chatCreateErr: errors.New("db error")}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/send",
		strings.NewReader(`{"body":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleChatSendInvalidJSON(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/send",
		strings.NewReader(`not json`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleChatReplyNoChatDB(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/reply",
		strings.NewReader(`{"body":"reply"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleChatReplyDBError(t *testing.T) {
	fdb := &mockFullDB{chatCreateErr: errors.New("db error")}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/reply",
		strings.NewReader(`{"body":"reply"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleChatReplyInvalidJSON(t *testing.T) {
	fdb := &mockFullDB{}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/chat/bot1/reply",
		strings.NewReader(`not json`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleChatHistoryNoChatDB(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleChatHistoryDBError(t *testing.T) {
	fdb := &mockFullDB{chatListErr: errors.New("db error")}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/chat/bot1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// handlePutSetting / handleGetSettings — edge paths
// --------------------------------------------------------------------------

func TestHandlePutSettingNilStore(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)
	srv.store = nil

	r := httptest.NewRequest(http.MethodPut, "/api/settings/openai.model",
		strings.NewReader(`{"value":"gpt-4"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandlePutSettingStoreError(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)
	srv.store = &failingSecretStore{newMemSecretStore()}

	r := httptest.NewRequest(http.MethodPut, "/api/settings/openai.model",
		strings.NewReader(`{"value":"gpt-4"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleGetSettingsNilStore(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)
	srv.store = nil

	r := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// --------------------------------------------------------------------------
// handleAPICreateProject — edge paths
// --------------------------------------------------------------------------

func TestHandleAPICreateProjectNoProjectDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	body := `{"name":"Test"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleAPICreateProjectInvalidJSON(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`not json`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleAPICreateProjectDBError(t *testing.T) {
	cdb := &mockCombinedDB{createProjErr: errors.New("db error")}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	body := `{"name":"Test"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleAPICreateProjectDefaultPrefix(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	body := `{"name":"NoPrefix"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp projectResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "AH", resp.BeadsPrefix)
}

func TestHandleAPICreateProjectWithResource(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	body := `{"name":"Proj","description":"test","resource_id":"r1","is_primary":true}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
}

// --------------------------------------------------------------------------
// handleAPIListProjects / handleAPIGetProject — edge paths
// --------------------------------------------------------------------------

func TestHandleAPIListProjectsNoProjectDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleAPIGetProjectNoProjectDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/projects/p1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// --------------------------------------------------------------------------
// handleAPIAddProjectResource / RemoveProjectResource — edge paths
// --------------------------------------------------------------------------

func TestHandleAPIAddProjectResourceNoProjectDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	body := `{"resource_id":"r1"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleAPIAddProjectResourceInvalidJSON(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(`not json`))
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleAPIRemoveProjectResourceNoProjectDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/projects/p1/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// --------------------------------------------------------------------------
// handleAPIAddProjectAgent / RemoveProjectAgent — edge paths
// --------------------------------------------------------------------------

func TestHandleAPIAddProjectAgentNoProjectDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	body := `{"agent_id":"a1","granted_by":"admin"}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleAPIAddProjectAgentInvalidJSON(t *testing.T) {
	cdb := &mockCombinedDB{}
	srv, _ := newTestServerWithFullDB(t, &cdb.mockFullDB)
	srv.db = cdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/agents", strings.NewReader(`not json`))
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleAPIRemoveProjectAgentNoProjectDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/projects/p1/agents/a1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// --------------------------------------------------------------------------
// Resource API handler — edge paths
// --------------------------------------------------------------------------

func TestHandleCreateResourceNoResourceDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	body := `{"name":"repo","resource_type":"github_repo"}`
	r := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleCreateResourceInvalidJSON(t *testing.T) {
	rdb := &mockResourceDB{}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(`not json`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleCreateResourceDBError(t *testing.T) {
	rdb := &mockResourceDB{createErr: errors.New("db error")}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	body := `{"name":"repo","resource_type":"github_repo"}`
	r := httptest.NewRequest(http.MethodPost, "/api/resources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleListResourcesNoResourceDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/resources", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleListResourcesDBError(t *testing.T) {
	rdb := &mockResourceDB{listErr2: errors.New("db error")}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/resources", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleListResourcesUnauthorized(t *testing.T) {
	rdb := &mockResourceDB{}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb

	r := httptest.NewRequest(http.MethodGet, "/api/resources", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleGetResourceNoResourceDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleGetResourceDBError(t *testing.T) {
	rdb := &mockResourceDB{getErr: errors.New("db error")}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleDeleteResourceNoResourceDB(t *testing.T) {
	fdb := &mockFullDB{}
	srv, _ := newTestServerWithFullDB(t, fdb)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleDeleteResourceNotFound(t *testing.T) {
	rdb := &mockResourceDB{resourceMap: map[string]*dolt.Resource{}}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/resources/nonexistent", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleDeleteResourceDBError(t *testing.T) {
	now := time.Now().UTC()
	rdb := &mockResourceDB{
		resourceMap: map[string]*dolt.Resource{
			"r1": {ID: "r1", OwnerID: "admin-bootstrap-user", Name: "repo1", CreatedAt: now, UpdatedAt: now},
		},
		deleteErr: errors.New("db error"),
	}
	srv, _ := newTestServerWithFullDB(t, &rdb.mockFullDB)
	srv.db = rdb
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodDelete, "/api/resources/r1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// Profile handler — edge paths
// --------------------------------------------------------------------------

func TestHandleGetProfileNoProfileDB(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/bots/bot1/profile", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleGetProfileDBError(t *testing.T) {
	fdb := &mockFullDB{profileErr: errors.New("db error"), profileMap: map[string]*dolt.BotProfile{}}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/bots/bot1/profile", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleUpsertProfileNoProfileDB(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPut, "/api/bots/bot1/profile",
		strings.NewReader(`{"description":"test"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleUpsertProfileDBError(t *testing.T) {
	fdb := &mockFullDB{upsertErr: errors.New("db error")}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPut, "/api/bots/bot1/profile",
		strings.NewReader(`{"description":"test"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleListProfilesNoProfileDB(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/bots/profiles", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleListProfilesDBError(t *testing.T) {
	fdb := &mockFullDB{profileErr: errors.New("db error")}
	srv, st := newTestServerWithFullDB(t, fdb)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodGet, "/api/bots/profiles", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// Webhook handler — missing paths
// --------------------------------------------------------------------------

func TestWebhookSubscribeInvalidJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/subscribe",
		strings.NewReader(`not json`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebhookUnsubscribeUnauthorized(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/unsubscribe",
		strings.NewReader(`{"channel":"github","bot_name":"bot1"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWebhookUnsubscribeInvalidJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/unsubscribe",
		strings.NewReader(`not json`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebhookUnsubscribeMissingFields(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	for _, body := range []string{
		`{"channel":"github"}`,
		`{"bot_name":"bot1"}`,
		`{}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/webhooks/unsubscribe",
			strings.NewReader(body))
		r.Header.Set("X-Registration-Token", "tok")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", body)
	}
}

func TestWebhookListSubscriptionsEmpty(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodGet, "/api/webhooks/subscriptions", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var channels []string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &channels))
	require.Empty(t, channels)
}

func TestWebhookReceiveReservedChannel(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/subscriptions",
		strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// --------------------------------------------------------------------------
// handleUsageSummary — edge paths
// --------------------------------------------------------------------------

func TestHandleUsageSummaryDBError(t *testing.T) {
	udb := &mockUsageDB{summaryErr: errors.New("db timeout")}
	srv := testServerWithOptions(t, withUsageDB(udb))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleUsageSummaryNilSummaries(t *testing.T) {
	udb := &mockUsageDB{summaries: nil}
	srv := testServerWithOptions(t, withUsageDB(udb))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

// --------------------------------------------------------------------------
// handleSetupPost — nil setupFn edge case
// --------------------------------------------------------------------------

func TestHandleSetupPostNilSetupFn(t *testing.T) {
	srv := testServerWithOptions(t, WithSetupMode(nil))

	form := url.Values{"password": {"pass"}, "confirm_password": {"pass"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Setup not configured")
}
