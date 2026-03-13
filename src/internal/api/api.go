// Package api provides the HTTP handlers for the agenthub admin web UI.
//
// All /admin/* routes require authentication via the auth.Manager middleware.
// Templates and static assets are embedded in the binary via //go:embed.
package api

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/auth"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/kanban"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/store"
)

// BotLister is the interface the API needs to list bots.
type BotLister interface {
	ListAllInstances(ctx context.Context) ([]*dolt.Instance, error)
}

// BotDeleter can remove a registered bot by name (across all channels).
type BotDeleter interface {
	DeleteInstanceByName(ctx context.Context, name string) error
}

// BotChecker performs an on-demand liveness probe for a named bot.
type BotChecker interface {
	CheckBot(ctx context.Context, name string) (alive bool, err error)
}

// KanbanBuilder is the interface for building the kanban board.
type KanbanBuilder interface {
	Build(ctx context.Context) (*kanban.Board, error)
}

// Server holds all dependencies for the HTTP API.
type Server struct {
	auth    *auth.Manager
	db      BotLister
	deleter BotDeleter // optional; handleBotRemove is a no-op if nil
	checker BotChecker // optional; handleBotCheck is a no-op if nil
	kanban  KanbanBuilder
	store   *store.Store
	tmpl    *template.Template
	mux     *http.ServeMux
}

// pageData is the common data passed to every template.
type pageData struct {
	Title   string
	Error   string
	Success string
	Data    interface{}
}

// NewServer creates a Server and registers all routes.
// deleter and checker are optional (pass nil to disable bot removal/checking).
func NewServer(
	authMgr *auth.Manager,
	db BotLister,
	deleter BotDeleter,
	checker BotChecker,
	kb KanbanBuilder,
	st *store.Store,
	tmpl *template.Template,
) *Server {
	s := &Server{
		auth:    authMgr,
		db:      db,
		deleter: deleter,
		checker: checker,
		kanban:  kb,
		store:   st,
		tmpl:    tmpl,
		mux:     http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// Public routes.
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /admin/login", s.handleLoginPage)
	s.mux.HandleFunc("POST /admin/login", s.handleLoginSubmit)
	s.mux.HandleFunc("POST /admin/logout", s.handleLogout)
	s.mux.HandleFunc("GET /", s.handleRoot)

	// Protected routes — wrapped with RequireAuth.
	protected := s.auth.RequireAuth
	s.mux.Handle("GET /admin/", protected(http.HandlerFunc(s.handleDashboard)))
	s.mux.Handle("GET /admin/bots", protected(http.HandlerFunc(s.handleBotList)))
	s.mux.Handle("POST /admin/bots/{name}/remove", protected(http.HandlerFunc(s.handleBotRemove)))
	s.mux.Handle("POST /admin/bots/{name}/check", protected(http.HandlerFunc(s.handleBotCheck)))
	s.mux.Handle("GET /admin/kanban", protected(http.HandlerFunc(s.handleKanban)))
	s.mux.Handle("GET /admin/secrets", protected(http.HandlerFunc(s.handleSecretsPage)))
	s.mux.Handle("POST /admin/secrets", protected(http.HandlerFunc(s.handleSecretsSubmit)))
}

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleRoot redirects / to /admin/.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

// handleHealth returns 200 OK — used by load balancers and liveness probes.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.auth.IsAuthenticated(r) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
		return
	}
	s.render(w, "login.html", pageData{Title: "Login"})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "login.html", pageData{Title: "Login", Error: "invalid form"})
		return
	}
	password := r.FormValue("password")
	if err := s.auth.Login(w, r, password); err != nil {
		s.render(w, "login.html", pageData{Title: "Login", Error: "Invalid password."})
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Logout(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	bots, _ := s.db.ListAllInstances(r.Context())
	aliveCount := 0
	for _, b := range bots {
		if b.IsAlive {
			aliveCount++
		}
	}
	type dashData struct {
		BotCount  int
		AliveCount int
	}
	s.render(w, "dashboard.html", pageData{
		Title: "Dashboard",
		Data:  dashData{BotCount: len(bots), AliveCount: aliveCount},
	})
}

func (s *Server) handleBotList(w http.ResponseWriter, r *http.Request) {
	bots, err := s.db.ListAllInstances(r.Context())
	if err != nil {
		s.render(w, "bots.html", pageData{Title: "Bots", Error: err.Error()})
		return
	}
	s.render(w, "bots.html", pageData{Title: "Bots", Data: bots})
}

func (s *Server) handleBotRemove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.deleter != nil {
		if err := s.deleter.DeleteInstanceByName(r.Context(), name); err != nil {
			slog.Error("removing bot", "name", name, "error", err)
		}
	}
	http.Redirect(w, r, "/admin/bots", http.StatusSeeOther)
}

func (s *Server) handleBotCheck(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.checker != nil {
		alive, err := s.checker.CheckBot(r.Context(), name)
		if err != nil {
			slog.Warn("bot check failed", "name", name, "error", err)
		} else {
			slog.Info("bot check", "name", name, "alive", alive)
		}
	}
	http.Redirect(w, r, "/admin/bots", http.StatusSeeOther)
}

func (s *Server) handleKanban(w http.ResponseWriter, r *http.Request) {
	board, err := s.kanban.Build(r.Context())
	if err != nil {
		s.render(w, "kanban.html", pageData{Title: "Kanban", Error: err.Error()})
		return
	}
	s.render(w, "kanban.html", pageData{Title: "Kanban", Data: board})
}

func (s *Server) handleSecretsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "secrets.html", pageData{Title: "Secrets"})
}

func (s *Server) handleSecretsSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "secrets.html", pageData{Title: "Secrets", Error: "invalid form"})
		return
	}

	type secretField struct {
		formKey  string
		storeKey string
	}
	fields := []secretField{
		{"openai_api_key", "openai_api_key"},
		{"slack_bot_token", "slack_bot_token"},
		{"slack_app_token", "slack_app_token"},
	}

	for _, f := range fields {
		if v := r.FormValue(f.formKey); v != "" {
			if err := s.store.Set(f.storeKey, v); err != nil {
				s.render(w, "secrets.html", pageData{Title: "Secrets", Error: "failed to save secrets"})
				return
			}
		}
	}
	s.render(w, "secrets.html", pageData{Title: "Secrets", Success: "Secrets saved."})
}
