package api

import (
	"crypto/rand"
	"fmt"
	"net/http"
)

func (s *Server) handleSetupGet(w http.ResponseWriter, r *http.Request) {
	if !s.setupMode {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	s.render(w, "setup.html", pageData{Title: "Setup"})
}

func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if !s.setupMode {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "setup.html", pageData{Title: "Setup", Error: "invalid form submission"})
		return
	}

	password := r.FormValue("password")
	confirm := r.FormValue("confirm_password")

	if password == "" {
		s.render(w, "setup.html", pageData{Title: "Setup", Error: "Password must not be empty."})
		return
	}
	if password != confirm {
		s.render(w, "setup.html", pageData{Title: "Setup", Error: "Passwords do not match."})
		return
	}

	if s.setupFn == nil {
		s.render(w, "setup.html", pageData{Title: "Setup", Error: "Setup not configured."})
		return
	}

	regToken, err := s.setupFn(password)
	if err != nil {
		s.render(w, "setup.html", pageData{Title: "Setup", Error: "Setup failed: " + err.Error()})
		return
	}

	s.render(w, "setup.html", pageData{
		Title:   "Setup",
		Success: fmt.Sprintf("Setup complete! Registration token: %s — Restart agenthub with your password to begin.", regToken),
	})
}

// generateRandHex returns n random bytes encoded as a hex string.
func generateRandHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}
