// Package auth provides session-based authentication for the agenthub admin UI.
//
// The admin password is verified against a bcrypt hash stored in the encrypted store.
// Sessions use gorilla/sessions with a cookie store (stateless — no server-side DB).
// The session secret is also stored in the encrypted store.
package auth

import (
	"fmt"
	"net/http"

	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionKeyAuthed = "authed"
	bcryptCost       = 12
)

// Manager handles admin login, session management, and auth middleware.
type Manager struct {
	store     *sessions.CookieStore
	adminHash []byte // bcrypt hash of the admin password
	cookieName string
}

// NewManager creates a Manager with the given session secret, admin bcrypt hash,
// and cookie name (from config).
func NewManager(sessionSecret []byte, adminHash []byte, cookieName string) *Manager {
	cs := sessions.NewCookieStore(sessionSecret)
	cs.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   false, // set to true in production with TLS
		SameSite: http.SameSiteLaxMode,
	}
	return &Manager{
		store:      cs,
		adminHash:  adminHash,
		cookieName: cookieName,
	}
}

// HashPassword creates a bcrypt hash of password for storage in the encrypted store.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// Login verifies the password against the stored bcrypt hash and, on success,
// sets an authenticated session cookie in w.
func (m *Manager) Login(w http.ResponseWriter, r *http.Request, password string) error {
	if err := bcrypt.CompareHashAndPassword(m.adminHash, []byte(password)); err != nil {
		return fmt.Errorf("invalid password")
	}
	sess, err := m.store.Get(r, m.cookieName)
	if err != nil {
		// On decode error (e.g., secret changed), create a new session.
		sess, _ = m.store.New(r, m.cookieName)
	}
	sess.Values[sessionKeyAuthed] = true
	return m.store.Save(r, w, sess)
}

// Logout clears the session cookie.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	sess, _ := m.store.Get(r, m.cookieName)
	sess.Values[sessionKeyAuthed] = false
	sess.Options.MaxAge = -1
	_ = m.store.Save(r, w, sess)
}

// IsAuthenticated reports whether the request has a valid authenticated session.
func (m *Manager) IsAuthenticated(r *http.Request) bool {
	sess, err := m.store.Get(r, m.cookieName)
	if err != nil {
		return false
	}
	v, ok := sess.Values[sessionKeyAuthed]
	if !ok {
		return false
	}
	authed, _ := v.(bool)
	return authed
}

// RequireAuth is an HTTP middleware that redirects unauthenticated requests to
// the login page. Authenticated requests pass through to next.
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.IsAuthenticated(r) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
