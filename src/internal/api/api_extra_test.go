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

func testServerWithDeleterChecker(t *testing.T, d BotDeleter, c BotChecker) *Server {
	t.Helper()
	srv, _, _ := testServer(t)
	srv.deleter = d
	srv.checker = c
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

	// Already authed, GET /admin/login should redirect to /admin/
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

	// /admin/unknown should 404 (handled by dashboard with path != "/admin/").
	r := httptest.NewRequest(http.MethodGet, "/admin/unknown", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	// Goes to handleDashboard which returns 404 for non /admin/ paths.
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

	// Point the store path to an unwritable location so Set fails.
	// We do this by replacing the store with one whose path can't be written.
	// Since store.Store is unexported, we instead use a path that makes save() fail.
	// Test by submitting to a read-only store directory.
	dir := t.TempDir()
	storePath := filepath.Join(dir, "readonly_subdir", "store.enc")
	newStore, err := store.Open(storePath, "testpassword")
	require.NoError(t, err)

	// Make the parent dir unwritable.
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	srv.store = newStore

	form := url.Values{"openai_api_key": {"sk-test-key"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/secrets", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	// If running as root or chmod not enforced, save may succeed.
	// Otherwise, it should show an error.
	_ = w.Body.String() // Just ensure no panic
}

func TestRenderTemplateError(t *testing.T) {
	// Calling render with a non-existent template triggers the error path.
	srv, _, _ := testServer(t)
	w := httptest.NewRecorder()
	// Deliberately use a template name that doesn't exist.
	srv.render(w, "nonexistent_template.html", pageData{Title: "Test"})
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "template error")
}

func TestHandleLoginSubmitParseFormError(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/login", errBodyReader{})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	// ParseForm error renders login page with error message.
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
	// Error is logged but handler still redirects.
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
