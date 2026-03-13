package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

var testSecret = []byte("test-session-secret-32-bytes-!!!!")

func newTestManager(t *testing.T, password string) *Manager {
	t.Helper()
	hash, err := HashPassword(password)
	require.NoError(t, err)
	return NewManager(testSecret, []byte(hash), "test_session")
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("mysecretpassword")
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	require.NotEqual(t, "mysecretpassword", hash)
}

func TestLoginSuccess(t *testing.T) {
	m := newTestManager(t, "correctpassword")

	r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	w := httptest.NewRecorder()

	err := m.Login(w, r, "correctpassword")
	require.NoError(t, err)

	resp := w.Result()
	require.NotEmpty(t, resp.Cookies())
}

func TestLoginWrongPassword(t *testing.T) {
	m := newTestManager(t, "correctpassword")

	r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	w := httptest.NewRecorder()

	err := m.Login(w, r, "wrongpassword")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid password")
}

func TestIsAuthenticatedAfterLogin(t *testing.T) {
	m := newTestManager(t, "pw")

	// Login.
	loginReq := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	loginW := httptest.NewRecorder()
	require.NoError(t, m.Login(loginW, loginReq, "pw"))

	// Use the session cookie on a subsequent request.
	cookies := loginW.Result().Cookies()
	require.NotEmpty(t, cookies)

	authReq := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	for _, c := range cookies {
		authReq.AddCookie(c)
	}
	require.True(t, m.IsAuthenticated(authReq))
}

func TestIsAuthenticatedWithoutCookie(t *testing.T) {
	m := newTestManager(t, "pw")
	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	require.False(t, m.IsAuthenticated(r))
}

func TestLogout(t *testing.T) {
	m := newTestManager(t, "pw")

	// Login.
	loginReq := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	loginW := httptest.NewRecorder()
	require.NoError(t, m.Login(loginW, loginReq, "pw"))
	cookies := loginW.Result().Cookies()

	// Logout.
	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	for _, c := range cookies {
		logoutReq.AddCookie(c)
	}
	logoutW := httptest.NewRecorder()
	m.Logout(logoutW, logoutReq)

	// Session should now be invalid — but since we can't perfectly simulate
	// the browser clearing the cookie, we verify that logout sets MaxAge=-1.
	logoutCookies := logoutW.Result().Cookies()
	found := false
	for _, c := range logoutCookies {
		if c.Name == "test_session" {
			require.Equal(t, -1, c.MaxAge)
			found = true
		}
	}
	require.True(t, found, "logout should set cookie MaxAge=-1")
}

func TestRequireAuthRedirects(t *testing.T) {
	m := newTestManager(t, "pw")

	protected := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Unauthenticated request.
	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, r)

	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/login", w.Header().Get("Location"))
}

func TestRequireAuthPassesThrough(t *testing.T) {
	m := newTestManager(t, "pw")

	reached := false
	protected := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	// Login to get a session cookie.
	loginReq := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	loginW := httptest.NewRecorder()
	require.NoError(t, m.Login(loginW, loginReq, "pw"))
	cookies := loginW.Result().Cookies()

	authReq := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	for _, c := range cookies {
		authReq.AddCookie(c)
	}
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, authReq)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, reached)
}
