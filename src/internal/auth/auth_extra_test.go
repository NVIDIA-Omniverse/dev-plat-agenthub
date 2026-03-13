package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAuthenticatedWithTamperedCookie(t *testing.T) {
	// Log in with mgr1 (secret1).
	mgr1 := newTestManager(t, "pw")
	loginReq := httptest.NewRequest(http.MethodPost, "/", nil)
	loginW := httptest.NewRecorder()
	require.NoError(t, mgr1.Login(loginW, loginReq, "pw"))
	cookies := loginW.Result().Cookies()

	// Check with mgr2 (different secret) — decode fails → IsAuthenticated returns false.
	hash, err := HashPassword("pw")
	require.NoError(t, err)
	mgr2 := NewManager([]byte("different-secret-32bytes-!!!!!"), []byte(hash), "test_session")

	authReq := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range cookies {
		authReq.AddCookie(c)
	}
	require.False(t, mgr2.IsAuthenticated(authReq))
}

func TestLoginWithInvalidExistingCookie(t *testing.T) {
	// Create a session cookie with mgr1.
	mgr1 := newTestManager(t, "pw")
	loginReq := httptest.NewRequest(http.MethodPost, "/", nil)
	loginW := httptest.NewRecorder()
	require.NoError(t, mgr1.Login(loginW, loginReq, "pw"))
	cookies := loginW.Result().Cookies()

	// Login with mgr2 (different secret) — decode error triggers fallback new().
	hash, err := HashPassword("pw")
	require.NoError(t, err)
	mgr2 := NewManager([]byte("different-secret-32bytes-!!!!!"), []byte(hash), "test_session")

	loginReq2 := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, c := range cookies {
		loginReq2.AddCookie(c)
	}
	loginW2 := httptest.NewRecorder()
	// Should succeed — creates a new session despite decode error.
	require.NoError(t, mgr2.Login(loginW2, loginReq2, "pw"))
	require.NotEmpty(t, loginW2.Result().Cookies())
}
