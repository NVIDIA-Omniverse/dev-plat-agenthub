package api

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/auth"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/kanban"
	"github.com/stretchr/testify/require"
)

// Mock implementations.

type mockBotLister struct {
	instances []*dolt.Instance
	err       error
}

func (m *mockBotLister) ListAllInstances(_ context.Context) ([]*dolt.Instance, error) {
	return m.instances, m.err
}

type mockKanbanBuilder struct {
	board *kanban.Board
	err   error
}

func (m *mockKanbanBuilder) Build(_ context.Context) (*kanban.Board, error) {
	return m.board, m.err
}

// testTemplates returns a minimal per-page template map for tests.
// Each entry contains a "layout.html" define (required by render) plus
// optional fragment defines for HTMX partials.
func testTemplates(t *testing.T) map[string]*template.Template {
	t.Helper()
	type entry struct {
		name string
		src  string
	}
	pages := []entry{
		{"login.html", `
{{define "layout.html"}}<!DOCTYPE html><html><body>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<form method="POST"><input name="password"><button>Login</button></form>
</body></html>{{end}}`},
		{"setup.html", `
{{define "layout.html"}}<!DOCTYPE html><html><body>
<h1>Setup</h1>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
{{if .Success}}<div class="success">{{.Success}}</div>{{end}}
<form method="POST"><input name="password"><input name="confirm_password"><button>Setup</button></form>
</body></html>{{end}}`},
		{"dashboard.html", `
{{define "layout.html"}}<!DOCTYPE html><html><body>
<h1>Dashboard</h1>
{{with .Data}}Bots: {{.BotCount}} Alive: {{.AliveCount}}{{end}}
</body></html>{{end}}`},
		{"bots.html", `
{{define "layout.html"}}<!DOCTYPE html><html><body>
<h1>Bots</h1>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
</body></html>{{end}}
{{define "bots-table"}}<table>{{if .Error}}<tr><td>{{.Error}}</td></tr>{{end}}</table>{{end}}`},
		{"kanban.html", `
{{define "layout.html"}}<!DOCTYPE html><html><body>
<h1>Kanban</h1>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
</body></html>{{end}}`},
		{"task-create.html", `
{{define "layout.html"}}<!DOCTYPE html><html><body>
<h1>New Task</h1>
{{with .Data}}{{range .Columns}}<option>{{.}}</option>{{end}}{{end}}
</body></html>{{end}}`},
		{"secrets.html", `
{{define "layout.html"}}<!DOCTYPE html><html><body>
<h1>Secrets</h1>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
{{if .Success}}<div class="success">{{.Success}}</div>{{end}}
<form method="POST"><input name="openai_api_key"></form>
</body></html>{{end}}`},
	}
	out := make(map[string]*template.Template, len(pages)+1)
	for _, p := range pages {
		out[p.name] = template.Must(template.New("").Parse(p.src))
	}
	out["bots-table"] = out["bots.html"]
	return out
}

// memSecretStore is a simple in-memory SecretStore for tests.
type memSecretStore struct {
	data map[string]string
}

func newMemSecretStore() *memSecretStore {
	return &memSecretStore{data: make(map[string]string)}
}

func (m *memSecretStore) Get(key string) string { return m.data[key] }
func (m *memSecretStore) Set(key, value string) error { m.data[key] = value; return nil }
func (m *memSecretStore) SetResourceCredential(rID, key, value string) error {
	m.data["resource:"+rID+":"+key] = value; return nil
}
func (m *memSecretStore) GetResourceCredential(rID, key string) string {
	return m.data["resource:"+rID+":"+key]
}
func (m *memSecretStore) DeleteResourceCredentials(rID string) {
	for _, k := range []string{"token", "refresh_token", "secret", "password", "api_key"} {
		delete(m.data, "resource:"+rID+":"+k)
	}
}

func testServer(t *testing.T) (*Server, *auth.Manager, *memSecretStore) {
	t.Helper()
	st := newMemSecretStore()

	hash, err := auth.HashPassword("adminpassword")
	require.NoError(t, err)
	authMgr := auth.NewManager([]byte("test-secret-32-bytes-padding!!!"), []byte(hash), "test_session")

	db := &mockBotLister{}
	kb := &mockKanbanBuilder{board: &kanban.Board{}}
	tmpl := testTemplates(t)

	srv := NewServer(authMgr, db, kb, st, tmpl)
	return srv, authMgr, st
}

// loginTo performs a login request and returns the session cookie.
func loginTo(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	form := url.Values{"password": {"adminpassword"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/login",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	cookies := w.Result().Cookies()
	require.NotEmpty(t, cookies)
	return cookies[0]
}

func TestHandleHealth(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
}

func TestHandleRootRedirect(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "/admin/", w.Header().Get("Location"))
}

func TestHandleLoginPageRendered(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "<form")
}

func TestHandleLoginSuccess(t *testing.T) {
	srv, _, _ := testServer(t)
	form := url.Values{"password": {"adminpassword"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/", w.Header().Get("Location"))
}

func TestHandleLoginWrongPassword(t *testing.T) {
	srv, _, _ := testServer(t)
	form := url.Values{"password": {"wrongpassword"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Invalid password")
}

func TestAdminRequiresAuth(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/login", w.Header().Get("Location"))
}

func TestDashboardAuthed(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Dashboard")
}

func TestBotListAuthed(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/bots", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestKanbanAuthed(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/kanban", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Kanban")
}

func TestSecretsPageAuthed(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/secrets", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Secrets")
}

func TestSecretsSubmitSavesKey(t *testing.T) {
	srv, _, st := testServer(t)
	cookie := loginTo(t, srv)

	form := url.Values{"openai_api_key": {"sk-test-key-123"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/secrets", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Secrets saved")

	// Verify it was persisted.
	v := st.Get("openai_api_key")
	require.Equal(t, "sk-test-key-123", v)
}

func TestLogout(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/login", w.Header().Get("Location"))
}

func TestDashboardShowsBotCounts(t *testing.T) {
	srv, _, _ := testServer(t)
	// Inject live bots into the mock DB.
	srv.db.(*mockBotLister).instances = []*dolt.Instance{
		{Name: "bot1", IsAlive: true},
		{Name: "bot2", IsAlive: false},
	}
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "Bots: 2")
	require.Contains(t, body, "Alive: 1")
}

// Ensure runtime is imported for file path resolution (unused in test, just ensure compile).
var _ = runtime.GOOS
