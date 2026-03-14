package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/kanban"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/store"
	"github.com/stretchr/testify/require"
)

// errBodyReader is an io.Reader that always returns an error, used to trigger ParseForm failures.
type errBodyReader struct{}

func (errBodyReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type mockBotDeleter struct{ err error }

func (m *mockBotDeleter) DeleteInstanceByName(_ context.Context, _ string) error { return m.err }

type mockBotChecker struct {
	alive bool
	err   error
}

func (m *mockBotChecker) CheckBot(_ context.Context, _ string) (bool, error) {
	return m.alive, m.err
}

type mockBotRegistrar struct{ err error }

func (m *mockBotRegistrar) CreateInstance(_ context.Context, _ dolt.Instance) error { return m.err }

type mockHealthProber struct{ err error }

func (m *mockHealthProber) Probe(_ context.Context, _ string, _ int) error { return m.err }

type mockCapacityReader struct {
	caps map[string]*dolt.Capacity
	err  error
}

func (m *mockCapacityReader) GetAllCapacities(_ context.Context) (map[string]*dolt.Capacity, error) {
	return m.caps, m.err
}

type mockTaskManager struct {
	record TaskRecord
	err    error
}

func (m *mockTaskManager) UpdateStatus(_ context.Context, _, _, _, _ string) error { return m.err }
func (m *mockTaskManager) UpdateTask(_ context.Context, _ string, _ TaskUpdateRequest) error {
	return m.err
}
func (m *mockTaskManager) GetTask(_ context.Context, _ string) (TaskRecord, error) {
	return m.record, m.err
}
func (m *mockTaskManager) CreateTask(_ context.Context, req TaskCreateRequest) (TaskRecord, error) {
	return TaskRecord{ID: "t1", Title: req.Title, Status: "open"}, m.err
}

func testServerWithDeleterChecker(t *testing.T, d BotDeleter, c BotChecker) *Server {
	t.Helper()
	srv, _, _ := testServer(t)
	srv.deleter = d
	srv.checker = c
	return srv
}

func testServerWithOptions(t *testing.T, opts ...ServerOption) *Server {
	t.Helper()
	srv, _, _ := testServer(t)
	for _, o := range opts {
		o(srv)
	}
	return srv
}

func TestHandleRootNotFound(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/some/other/path", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleBotRemove(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/bots/mybot/remove", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/bots", w.Header().Get("Location"))
}

func TestHandleBotCheck(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/bots/mybot/check", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/bots", w.Header().Get("Location"))
}

func TestHandleBotListError(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.db.(*mockBotLister).err = errors.New("db down")
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/bots", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "db down")
}

func TestHandleBotListHXRequest(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/bots", nil)
	r.AddCookie(cookie)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "<table>")
}

func TestHandleBotListWithCapacity(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.db.(*mockBotLister).instances = []*dolt.Instance{{ID: "id1", Name: "bot1", IsAlive: true}}
	srv.capacityReader = &mockCapacityReader{caps: map[string]*dolt.Capacity{
		"id1": {BotID: "id1", GPUFreeMB: 8192, JobsQueued: 2, JobsRunning: 1},
	}}
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/bots", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleKanbanError(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.kanban.(*mockKanbanBuilder).err = errors.New("beads unavailable")
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "beads unavailable")
}

func TestHandleLoginPageAlreadyAuthed(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "/admin/", w.Header().Get("Location"))
}

func TestHandleDashboardSubPath(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/unknown", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleKanbanWithColumns(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.kanban.(*mockKanbanBuilder).board = &kanban.Board{
		Columns: []*kanban.Column{
			{Status: "open"},
			{Status: "in_progress"},
		},
	}
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestSecretsSubmitStoreError(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	dir := t.TempDir()
	storePath := filepath.Join(dir, "readonly_subdir", "store.enc")
	newStore, err := store.Open(storePath, "testpassword")
	require.NoError(t, err)

	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	srv.store = newStore

	form := url.Values{"openai_api_key": {"sk-test-key"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/secrets", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	_ = w.Body.String()
}

func TestRenderTemplateError(t *testing.T) {
	srv, _, _ := testServer(t)
	w := httptest.NewRecorder()
	srv.render(w, "nonexistent_template.html", pageData{Title: "Test"})
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "template not found")
}

func TestRenderFragmentNotFound(t *testing.T) {
	srv, _, _ := testServer(t)
	w := httptest.NewRecorder()
	srv.renderFragment(w, "nonexistent-fragment", pageData{Title: "Test"})
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "template not found")
}

func TestHandleLoginSubmitParseFormError(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/login", errBodyReader{})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleSecretsSubmitParseFormError(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/secrets", errBodyReader{})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleBotRemoveWithDeleterSuccess(t *testing.T) {
	srv := testServerWithDeleterChecker(t, &mockBotDeleter{}, nil)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/bots/mybot/remove", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleBotRemoveWithDeleterError(t *testing.T) {
	srv := testServerWithDeleterChecker(t, &mockBotDeleter{err: errors.New("db error")}, nil)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/bots/mybot/remove", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleBotCheckWithCheckerAlive(t *testing.T) {
	srv := testServerWithDeleterChecker(t, nil, &mockBotChecker{alive: true})
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/bots/mybot/check", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleBotCheckWithCheckerError(t *testing.T) {
	srv := testServerWithDeleterChecker(t, nil, &mockBotChecker{err: errors.New("probe failed")})
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/bots/mybot/check", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

// --------------------------------------------------------------------------
// Setup mode tests
// --------------------------------------------------------------------------

func TestSetupModeRedirectsAdmin(t *testing.T) {
	srv := testServerWithOptions(t, WithSetupMode(t.TempDir()+"/store.enc"))

	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/setup", w.Header().Get("Location"))
}

func TestSetupModeAllowsSetupRoute(t *testing.T) {
	srv := testServerWithOptions(t, WithSetupMode(t.TempDir()+"/store.enc"))

	r := httptest.NewRequest(http.MethodGet, "/admin/setup", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Setup")
}

func TestSetupModeAllowsLogin(t *testing.T) {
	srv := testServerWithOptions(t, WithSetupMode(t.TempDir()+"/store.enc"))

	r := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	// Login page renders (not redirected to setup).
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleSetupGetNotSetupMode(t *testing.T) {
	srv, _, _ := testServer(t)
	// setup mode is false — GET /admin/setup redirects to /admin/
	r := httptest.NewRequest(http.MethodGet, "/admin/setup", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/", w.Header().Get("Location"))
}

func TestHandleSetupPostPasswordMismatch(t *testing.T) {
	dir := t.TempDir()
	srv := testServerWithOptions(t, WithSetupMode(dir+"/store.enc"))

	form := url.Values{"password": {"abc"}, "confirm_password": {"xyz"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "do not match")
}

func TestHandleSetupPostEmptyPassword(t *testing.T) {
	dir := t.TempDir()
	srv := testServerWithOptions(t, WithSetupMode(dir+"/store.enc"))

	form := url.Values{"password": {""}, "confirm_password": {""}}
	r := httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "must not be empty")
}

func TestHandleSetupPostSuccess(t *testing.T) {
	dir := t.TempDir()
	srv := testServerWithOptions(t, WithSetupMode(dir+"/store.enc"))

	form := url.Values{"password": {"securepass"}, "confirm_password": {"securepass"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Setup complete")
}

func TestHandleSetupPostNotSetupMode(t *testing.T) {
	srv, _, _ := testServer(t)
	form := url.Values{"password": {"x"}, "confirm_password": {"x"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

// --------------------------------------------------------------------------
// Registration endpoint tests
// --------------------------------------------------------------------------

func TestHandleRegisterMissingToken(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"name":"bot","host":"1.2.3.4","port":8080}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleRegisterValidToken(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "testtoken"))
	srv.registrar = &mockBotRegistrar{}

	r := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"name":"mybot","host":"1.2.3.4","port":8080,"channel_id":"C1","owner_slack_user":"U1"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "testtoken")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, w.Body.String(), "mybot")
}

func TestHandleRegisterInvalidJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(`not-json`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleRegisterMissingFields(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"name":"","host":"","port":0}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleRegisterHealthProbeFail(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.healthProber = &mockHealthProber{err: errors.New("timeout")}
	srv.registrar = &mockBotRegistrar{}

	r := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"name":"b","host":"1.2.3.4","port":8080}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleRegisterNoRegistrar(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"name":"b","host":"1.2.3.4","port":8080}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleRegisterRegistrarError(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.registrar = &mockBotRegistrar{err: errors.New("db error")}

	r := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"name":"b","host":"1.2.3.4","port":8080}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// Task status callback tests
// --------------------------------------------------------------------------

func TestHandleTaskStatusUnauthorized(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status",
		strings.NewReader(`{"status":"in_progress"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleTaskStatusInvalidJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status", strings.NewReader(`bad`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTaskStatusInvalidStatus(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status",
		strings.NewReader(`{"status":"nonsense"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTaskStatusNoTaskManager(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status",
		strings.NewReader(`{"status":"closed"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleTaskStatusSuccess(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = &mockTaskManager{}

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status",
		strings.NewReader(`{"status":"closed","note":"finished"}`))
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "mybot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandleTaskStatusError(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = &mockTaskManager{err: errors.New("beads down")}

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/status",
		strings.NewReader(`{"status":"closed"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// Kanban action tests
// --------------------------------------------------------------------------

func TestHandleKanbanTaskCreateNilTaskManager(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"fix bug"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskCreateSuccess(t *testing.T) {
	srv := testServerWithOptions(t, WithTaskManager(&mockTaskManager{}))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"add feature"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskStatusNilTaskManager(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	form := url.Values{"status": {"done"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/t1/status", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskStatusSuccess(t *testing.T) {
	srv := testServerWithOptions(t, WithTaskManager(&mockTaskManager{}))
	cookie := loginTo(t, srv)

	form := url.Values{"status": {"in_progress"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/t1/status", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskCreateEmptyTitle(t *testing.T) {
	srv := testServerWithOptions(t, WithTaskManager(&mockTaskManager{}))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {""}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskStatusError(t *testing.T) {
	srv := testServerWithOptions(t, WithTaskManager(&mockTaskManager{err: errors.New("update failed")}))
	cookie := loginTo(t, srv)

	form := url.Values{"status": {"done"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/t1/status", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskCreateError(t *testing.T) {
	srv := testServerWithOptions(t, WithTaskManager(&mockTaskManager{err: errors.New("create failed")}))
	cookie := loginTo(t, srv)

	form := url.Values{"title": {"my task"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskStatusParseFormError(t *testing.T) {
	srv := testServerWithOptions(t, WithTaskManager(&mockTaskManager{}))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks/t1/status", errBodyReader{})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ContentLength = -1
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestHandleKanbanTaskCreateParseFormError(t *testing.T) {
	srv := testServerWithOptions(t, WithTaskManager(&mockTaskManager{}))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/kanban/tasks", errBodyReader{})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ContentLength = -1
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

// --------------------------------------------------------------------------
// ServerOption function tests (exercise each option constructor)
// --------------------------------------------------------------------------

func TestWithDeleterOption(t *testing.T) {
	d := &mockBotDeleter{}
	srv := testServerWithOptions(t, WithDeleter(d))
	require.Equal(t, d, srv.deleter)
}

func TestWithCheckerOption(t *testing.T) {
	c := &mockBotChecker{alive: true}
	srv := testServerWithOptions(t, WithChecker(c))
	require.Equal(t, c, srv.checker)
}

func TestWithRegistrarOption(t *testing.T) {
	reg := &mockBotRegistrar{}
	srv := testServerWithOptions(t, WithRegistrar(reg))
	require.Equal(t, reg, srv.registrar)
}

func TestWithHealthProberOption(t *testing.T) {
	p := &mockHealthProber{}
	srv := testServerWithOptions(t, WithHealthProber(p))
	require.Equal(t, p, srv.healthProber)
}

func TestWithCapacityReaderOption(t *testing.T) {
	cr := &mockCapacityReader{caps: map[string]*dolt.Capacity{}}
	srv := testServerWithOptions(t, WithCapacityReader(cr))
	require.Equal(t, cr, srv.capacityReader)
}

// --------------------------------------------------------------------------
// renderFragment error path
// --------------------------------------------------------------------------

func TestRenderFragmentError(t *testing.T) {
	srv, _, _ := testServer(t)
	w := httptest.NewRecorder()
	srv.renderFragment(w, "nonexistent_fragment", pageData{})
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// validateRegistrationToken: missing store key path
// --------------------------------------------------------------------------

func TestValidateRegistrationTokenStoreKeyMissing(t *testing.T) {
	// Store has no registration_token key → token validation fails.
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"name":"b","host":"1.2.3.4","port":8080}`))
	r.Header.Set("X-Registration-Token", "anything")
	// store has no registration_token key
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// --------------------------------------------------------------------------
// handleBotList: capacityReader error is silently ignored
// --------------------------------------------------------------------------

func TestHandleBotListCapacityError(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.db.(*mockBotLister).instances = []*dolt.Instance{{ID: "id1", Name: "bot1", IsAlive: true}}
	srv.capacityReader = &mockCapacityReader{err: errors.New("capacity db error")}
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/bots", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	// Capacity errors are silently ignored; page still renders.
	require.Equal(t, http.StatusOK, w.Code)
}

// --------------------------------------------------------------------------
// handleBotTaskCreate tests
// --------------------------------------------------------------------------

func TestHandleBotTaskCreateUnauthorized(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"do work"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleBotTaskCreateInvalidJSON(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`bad`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBotTaskCreateMissingTitle(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":""}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBotTaskCreateNoTaskManager(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"some work"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleBotTaskCreateSuccess(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = &mockTaskManager{}

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"do work","bot_name":"mybot","priority":1}`))
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "mybot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, w.Body.String(), "t1")
}

func TestHandleBotTaskCreateSuccessDefaultPriority(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = &mockTaskManager{}

	// priority=0 in request → defaults to 2
	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"work"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
}

func TestHandleBotTaskCreateError(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskManager = &mockTaskManager{err: errors.New("beads down")}

	r := httptest.NewRequest(http.MethodPost, "/api/tasks",
		strings.NewReader(`{"title":"do work"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --------------------------------------------------------------------------
// handleSetupPost: bad store path triggers error branch
// --------------------------------------------------------------------------

func TestHandleSetupPostBadStorePath(t *testing.T) {
	// /nonexistent/... cannot be created → store.Open fails.
	srv := testServerWithOptions(t, WithSetupMode("/nonexistent/deep/path/store.enc"))

	form := url.Values{"password": {"goodpass"}, "confirm_password": {"goodpass"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	// Should fail when saving (store.Open succeeds in-memory but Set triggers write).
	// We just verify a response is returned — may be success or error depending on OS.
	require.Equal(t, http.StatusOK, w.Code)
}
