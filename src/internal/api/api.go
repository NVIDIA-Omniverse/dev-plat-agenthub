// Package api provides the HTTP handlers for the agenthub admin web UI and
// the bot-facing REST API (/api/*).
//
// All /admin/* routes (except /admin/login, /admin/setup) require authentication
// via the auth.Manager middleware. /api/* routes use token authentication via
// the X-Registration-Token header.
//
// Templates and static assets are embedded in the binary via //go:embed.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/auth"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/kanban"
)

// --------------------------------------------------------------------------
// Interfaces
// --------------------------------------------------------------------------

// BotLister lists all registered bot instances.
type BotLister interface {
	ListAllInstances(ctx context.Context) ([]*dolt.Instance, error)
}

// BotDeleter removes a registered bot by name (across all channels).
type BotDeleter interface {
	DeleteInstanceByName(ctx context.Context, name string) error
}

// BotAliveUpdater persists heartbeat liveness to the database.
type BotAliveUpdater interface {
	UpdateAliveByName(ctx context.Context, name string, alive bool) error
}

// BotHeartbeater persists all heartbeat fields to the database.
type BotHeartbeater interface {
	UpdateHeartbeat(ctx context.Context, name, currentTask, status, message string) error
}

// InboxDB persists inbox messages to the database.
type InboxDB interface {
	CreateInboxMessage(ctx context.Context, msg dolt.InboxDBMessage) error
	ListPendingMessages(ctx context.Context, botName string) ([]*dolt.InboxDBMessage, error)
	AckInboxMessage(ctx context.Context, id string) error
}

// BotChecker performs an on-demand liveness probe for a named bot.
type BotChecker interface {
	CheckBot(ctx context.Context, name string) (alive bool, err error)
}

// BotRegistrar creates new bot registrations.
type BotRegistrar interface {
	CreateInstance(ctx context.Context, inst dolt.Instance) error
}

// BotAnnouncer posts a message to a Slack channel (used for registration announcements).
type BotAnnouncer interface {
	PostMessage(ctx context.Context, channel, text string) error
}

// HealthProber verifies a bot is reachable at the given host and port.
type HealthProber interface {
	Probe(ctx context.Context, host string, port int) error
}

// CapacityReader retrieves bot capacity data.
type CapacityReader interface {
	GetAllCapacities(ctx context.Context) (map[string]*dolt.Capacity, error)
}

// KanbanBuilder builds the kanban board.
type KanbanBuilder interface {
	Build(ctx context.Context) (*kanban.Board, error)
}

// SecretStore provides reactive, write-through configuration storage.
// settings.Store satisfies this interface.
type SecretStore interface {
	Get(key string) string
	Set(key, value string) error
	SetResourceCredential(resourceID, key, value string) error
	GetResourceCredential(resourceID, key string) string
	DeleteResourceCredentials(resourceID string)
}

// TaskRecord is the full representation of an issue returned by TaskManager.
type TaskRecord struct {
	ID                 string
	Title              string
	Status             string
	Description        string
	Priority           int
	IssueType          string
	Assignee           string
	EstimatedMinutes   int
	AcceptanceCriteria string
	Notes              string
	DueAt              string // "YYYY-MM-DD" or ""
	Labels             string // comma-separated
	CreatedAt          string
	UpdatedAt          string
	CreatedBy          string
}

// TaskCreateRequest carries all user-editable fields for creating a new task.
// Zero values are treated as "not set"; the TaskManager implementation applies defaults.
type TaskCreateRequest struct {
	Title              string
	Description        string
	Status             string // kanban column name; TaskManager defaults to first column if empty
	Priority           int    // 0=critical 1=high 2=normal 3=low
	IssueType          string // "task", "bug", "feature", "epic", "chore"
	Assignee           string
	EstimatedMinutes   int    // 0 = unset
	AcceptanceCriteria string
	Notes              string
	DueAt              string // "YYYY-MM-DD" or ""
	Labels             string // comma-separated
	Actor              string
}

// TaskUpdateRequest carries editable fields for updating an existing task.
type TaskUpdateRequest struct {
	Title              string
	Description        string
	Status             string
	Priority           int
	IssueType          string
	Assignee           string
	EstimatedMinutes   int
	AcceptanceCriteria string
	Notes              string
	DueAt              string
	Labels             string
	Actor              string
}

// TaskManager handles task creation and status updates for the kanban board
// and the bot task-status callback endpoint.
type TaskManager interface {
	UpdateStatus(ctx context.Context, issueID, newStatus, note, actor string) error
	GetTask(ctx context.Context, issueID string) (TaskRecord, error)
	CreateTask(ctx context.Context, req TaskCreateRequest) (TaskRecord, error)
	UpdateTask(ctx context.Context, issueID string, req TaskUpdateRequest) error
}

// --------------------------------------------------------------------------
// Server
// --------------------------------------------------------------------------

// Server holds all dependencies for the HTTP API.
type Server struct {
	auth           *auth.Manager
	db             BotLister
	deleter        BotDeleter      // optional; handleBotRemove is a no-op if nil
	checker        BotChecker      // optional; handleBotCheck is a no-op if nil
	registrar      BotRegistrar    // optional; handleRegister returns 503 if nil
	healthProber   HealthProber    // optional; health probe skipped if nil
	capacityReader CapacityReader  // optional; capacity columns hidden if nil
	taskManager    TaskManager     // optional; kanban actions and bot callbacks disabled if nil
	taskLogger     TaskLogger      // optional; POST /api/tasks/{id}/log disabled if nil
	replier        InboxReplier    // optional; POST /api/inbox/{id}/reply posts to Slack if set
	kanban         KanbanBuilder
	kanbanColumns  []string        // ordered column names, used by task-create form
	store          SecretStore
	tmpl           map[string]*template.Template
	mux            *http.ServeMux
	setupMode      bool                          // when true, /admin/* routes redirect to /admin/setup
	setupFn        func(string) (string, error)  // called with password on POST /admin/setup; returns regToken

	// Public URL used for Slack messages and credential URLs.
	publicURL string

	// announcer posts bot registration announcements to Slack.
	announcer      BotAnnouncer
	announceChannel string // Slack channel ID for registration announcements

	// Always-present, no external dependencies.
	inbox      *Inbox
	heartbeats *HeartbeatRegistry
	events     *EventBroadcaster
	webhooks   *WebhookRelay
}

// ServerOption is a functional option for configuring a Server.
type ServerOption func(*Server)

// WithDeleter sets the optional BotDeleter.
func WithDeleter(d BotDeleter) ServerOption { return func(s *Server) { s.deleter = d } }

// WithChecker sets the optional BotChecker.
func WithChecker(c BotChecker) ServerOption { return func(s *Server) { s.checker = c } }

// WithRegistrar sets the optional BotRegistrar for the auto-registration endpoint.
func WithRegistrar(r BotRegistrar) ServerOption { return func(s *Server) { s.registrar = r } }

// WithHealthProber sets the optional HealthProber used during bot registration.
func WithHealthProber(hp HealthProber) ServerOption { return func(s *Server) { s.healthProber = hp } }

// WithCapacityReader sets the optional CapacityReader for the bots page.
func WithCapacityReader(cr CapacityReader) ServerOption {
	return func(s *Server) { s.capacityReader = cr }
}

// WithTaskManager sets the optional TaskManager for kanban actions and bot callbacks.
func WithTaskManager(tm TaskManager) ServerOption { return func(s *Server) { s.taskManager = tm } }

// WithKanbanColumns sets the ordered column names shown in the task-creation form.
func WithKanbanColumns(cols []string) ServerOption {
	return func(s *Server) { s.kanbanColumns = cols }
}

// WithReplier sets the optional InboxReplier used by POST /api/inbox/{id}/reply
// to post agent replies back to Slack.
func WithReplier(ir InboxReplier) ServerOption { return func(s *Server) { s.replier = ir } }

// WithSetupMode puts the server into first-run setup mode.
// setupFn is called with the chosen password when the setup form is submitted.
// It should persist initial settings and return the registration token on success.
func WithSetupMode(setupFn func(password string) (regToken string, err error)) ServerOption {
	return func(s *Server) {
		s.setupMode = true
		s.setupFn = setupFn
	}
}

// Inbox returns the server's inbox so external callers (e.g. Slack handler)
// can enqueue messages for agents.
func (s *Server) Inbox() *Inbox { return s.inbox }

// SetReplier sets the InboxReplier after server creation (used when the Slack
// client is wired after NewServer returns).
func (s *Server) SetReplier(ir InboxReplier) { s.replier = ir }

// SetAnnouncer sets the BotAnnouncer and channel for registration announcements.
func (s *Server) SetAnnouncer(a BotAnnouncer, channelID string) {
	s.announcer = a
	s.announceChannel = channelID
}

// pageData is the common data passed to every template.
type pageData struct {
	Title   string
	Error   string
	Success string
	Data    interface{}
}

// NewServer creates a Server and registers all routes.
// The positional parameters are the core mandatory dependencies.
// Pass ServerOption values to configure optional features.
func NewServer(
	authMgr *auth.Manager,
	db BotLister,
	kb KanbanBuilder,
	st SecretStore,
	tmpl map[string]*template.Template,
	opts ...ServerOption,
) *Server {
	s := &Server{
		auth:       authMgr,
		db:         db,
		kanban:     kb,
		store:      st,
		tmpl:       tmpl,
		mux:        http.NewServeMux(),
		inbox:      newInbox(),
		heartbeats: newHeartbeatRegistry(),
		events:     newEventBroadcaster(),
		webhooks:   newWebhookRelay(),
	}
	for _, o := range opts {
		o(s)
	}
	// Wire DB-backed inbox if the db satisfies InboxDB.
	if idb, ok := s.db.(InboxDB); ok {
		s.inbox.SetDB(idb)
	}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler. In setup mode, admin routes redirect to /admin/setup.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.setupMode &&
		strings.HasPrefix(r.URL.Path, "/admin/") &&
		r.URL.Path != "/admin/setup" &&
		r.URL.Path != "/admin/login" {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// Public routes.
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /admin/login", s.handleLoginPage)
	s.mux.HandleFunc("POST /admin/login", s.handleLoginSubmit)
	s.mux.HandleFunc("POST /admin/logout", s.handleLogout)
	s.mux.HandleFunc("GET /admin/setup", s.handleSetupGet)
	s.mux.HandleFunc("POST /admin/setup", s.handleSetupPost)
	s.mux.HandleFunc("GET /", s.handleRoot)

	// Bot-facing API routes (token-authenticated, not cookie-authenticated).
	s.mux.HandleFunc("POST /api/register", s.handleRegister)
	s.mux.HandleFunc("POST /api/tasks", s.handleBotTaskCreate)
	s.mux.HandleFunc("POST /api/tasks/{id}/status", s.handleTaskStatusUpdate)
	s.mux.HandleFunc("POST /api/tasks/{id}/log", s.handleTaskLog)
	s.mux.HandleFunc("GET /api/inbox", s.handleInboxPoll)
	s.mux.HandleFunc("POST /api/inbox/{id}/ack", s.handleInboxAck)
	s.mux.HandleFunc("POST /api/inbox/{id}/reply", s.handleInboxReply)
	s.mux.HandleFunc("POST /api/heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("POST /api/webhooks/subscribe", s.handleWebhookSubscribe)
	s.mux.HandleFunc("POST /api/webhooks/unsubscribe", s.handleWebhookUnsubscribe)
	s.mux.HandleFunc("GET /api/webhooks/subscriptions", s.handleWebhookListSubscriptions)
	s.mux.HandleFunc("POST /api/webhooks/{channel}", s.handleWebhookReceive)
	// Credential delivery (token-authenticated).
	s.mux.HandleFunc("GET /api/credentials/{task_assignment_id}", s.handleGetCredentials)
	// Resource API (user-authenticated: cookie or Bearer token).
	s.mux.HandleFunc("POST /api/resources", s.handleCreateResource)
	s.mux.HandleFunc("GET /api/resources", s.handleListResources)
	s.mux.HandleFunc("GET /api/resources/{id}", s.handleGetResource)
	s.mux.HandleFunc("DELETE /api/resources/{id}", s.handleDeleteResource)
	// Project API (user-authenticated).
	s.mux.HandleFunc("POST /api/projects", s.handleAPICreateProject)
	s.mux.HandleFunc("GET /api/projects", s.handleAPIListProjects)
	s.mux.HandleFunc("GET /api/projects/{id}", s.handleAPIGetProject)
	s.mux.HandleFunc("POST /api/projects/{id}/resources", s.handleAPIAddProjectResource)
	s.mux.HandleFunc("DELETE /api/projects/{id}/resources/{rid}", s.handleAPIRemoveProjectResource)
	s.mux.HandleFunc("POST /api/projects/{id}/agents", s.handleAPIAddProjectAgent)
	s.mux.HandleFunc("DELETE /api/projects/{id}/agents/{aid}", s.handleAPIRemoveProjectAgent)

	// Protected admin routes.
	protected := s.auth.RequireAuth
	s.mux.Handle("GET /admin/", protected(http.HandlerFunc(s.handleDashboard)))
	s.mux.Handle("GET /admin/bots", protected(http.HandlerFunc(s.handleBotList)))
	s.mux.Handle("POST /admin/bots/{name}/remove", protected(http.HandlerFunc(s.handleBotRemove)))
	s.mux.Handle("POST /admin/bots/{name}/check", protected(http.HandlerFunc(s.handleBotCheck)))
	s.mux.Handle("GET /admin/kanban", protected(http.HandlerFunc(s.handleKanban)))
	s.mux.Handle("GET /admin/kanban/tasks/new", protected(http.HandlerFunc(s.handleKanbanTaskNew)))
	s.mux.Handle("POST /admin/kanban/tasks", protected(http.HandlerFunc(s.handleKanbanTaskCreate)))
	s.mux.Handle("POST /admin/kanban/tasks/{id}/status", protected(http.HandlerFunc(s.handleKanbanTaskStatus)))
	s.mux.Handle("GET /admin/kanban/tasks/{id}", protected(http.HandlerFunc(s.handleKanbanTaskDetail)))
	s.mux.Handle("POST /admin/kanban/tasks/{id}", protected(http.HandlerFunc(s.handleKanbanTaskUpdate)))
	s.mux.Handle("GET /admin/kanban/agents", protected(http.HandlerFunc(s.handleKanbanAgents)))
	s.mux.Handle("POST /admin/kanban/tasks/{id}/assign", protected(http.HandlerFunc(s.handleKanbanTaskAssign)))
	s.mux.Handle("GET /admin/secrets", protected(http.HandlerFunc(s.handleSecretsPage)))
	s.mux.Handle("POST /admin/secrets", protected(http.HandlerFunc(s.handleSecretsSubmit)))
	s.mux.Handle("PUT /api/settings/{key}", protected(http.HandlerFunc(s.handlePutSetting)))
	s.mux.Handle("GET /api/settings", protected(http.HandlerFunc(s.handleGetSettings)))
	s.mux.Handle("GET /admin/events", protected(http.HandlerFunc(s.handleAdminEvents)))
	s.mux.Handle("GET /admin/heartbeats", protected(http.HandlerFunc(s.handleAdminHeartbeats)))
	// Resources admin UI.
	s.mux.Handle("GET /admin/resources", protected(http.HandlerFunc(s.handleResourcesPage)))
	s.mux.Handle("POST /admin/resources", protected(http.HandlerFunc(s.handleResourceCreate)))
	// Projects admin UI.
	s.mux.Handle("GET /admin/projects", protected(http.HandlerFunc(s.handleProjectsPage)))
	s.mux.Handle("POST /admin/projects", protected(http.HandlerFunc(s.handleProjectCreate)))
	s.mux.Handle("GET /admin/projects/{id}", protected(http.HandlerFunc(s.handleProjectDetail)))
}

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	tmpl, ok := s.tmpl[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// renderFragment renders only a named sub-template block (for HTMX partials).
func (s *Server) renderFragment(w http.ResponseWriter, name string, data pageData) {
	tmpl, ok := s.tmpl[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// validateRegistrationToken checks the X-Registration-Token header against
// the stored registration token. Returns true if valid.
func (s *Server) validateRegistrationToken(r *http.Request) bool {
	if s.store == nil {
		return false
	}
	token := r.Header.Get("X-Registration-Token")
	if token == "" {
		return false
	}
	stored := s.store.Get("registration_token")
	return stored != "" && token == stored
}

// --------------------------------------------------------------------------
// Public handlers
// --------------------------------------------------------------------------

// handleRoot redirects / to /admin/.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

// handleHealth returns 200 OK for load-balancer and liveness probes.
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

// --------------------------------------------------------------------------
// Protected admin handlers
// --------------------------------------------------------------------------

// botWithCapacity pairs an Instance with its optional capacity record.
type botWithCapacity struct {
	*dolt.Instance
	Capacity *dolt.Capacity
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
		BotCount   int
		AliveCount int
		TaskCount  int
	}
	data := dashData{BotCount: len(bots), AliveCount: aliveCount}

	s.render(w, "dashboard.html", pageData{Title: "Dashboard", Data: data})
}

func (s *Server) handleBotList(w http.ResponseWriter, r *http.Request) {
	bots, err := s.db.ListAllInstances(r.Context())
	if err != nil {
		pd := pageData{Title: "Bots", Error: err.Error()}
		if r.Header.Get("HX-Request") == "true" {
			s.renderFragment(w, "bots-table", pd)
			return
		}
		s.render(w, "bots.html", pd)
		return
	}

	// Merge capacity data if available.
	var caps map[string]*dolt.Capacity
	if s.capacityReader != nil {
		caps, _ = s.capacityReader.GetAllCapacities(r.Context())
	}
	result := make([]botWithCapacity, len(bots))
	for i, b := range bots {
		result[i] = botWithCapacity{Instance: b, Capacity: caps[b.ID]}
	}

	pd := pageData{Title: "Bots", Data: result}
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, "bots-table", pd)
		return
	}
	s.render(w, "bots.html", pd)
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

func (s *Server) handleKanbanTaskStatus(w http.ResponseWriter, r *http.Request) {
	if s.taskManager == nil {
		http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
		return
	}
	issueID := r.PathValue("id")
	status := r.FormValue("status")
	if err := s.taskManager.UpdateStatus(r.Context(), issueID, status, "", "admin"); err != nil {
		slog.Error("updating kanban task status", "id", issueID, "status", status, "error", err)
	}
	s.events.Broadcast("kanban-update", issueID)
	http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
}

// handleKanbanTaskNew renders the full task-creation form.
// Returns an HTMX fragment if called with HX-Request header; full page otherwise.
func (s *Server) handleKanbanTaskNew(w http.ResponseWriter, r *http.Request) {
	d := s.taskFormDataWithBots(r.Context())
	// Pre-fill status if provided via query param (column quick-add).
	if st := r.URL.Query().Get("status"); st != "" {
		d.Task.Status = st
	}
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, "task-create-panel", pageData{Data: d})
	} else {
		s.render(w, "task-create.html", pageData{Title: "New Task", Data: d})
	}
}

func (s *Server) handleKanbanTaskCreate(w http.ResponseWriter, r *http.Request) {
	if s.taskManager == nil {
		http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
		return
	}
	title := r.FormValue("title")
	if title == "" {
		http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
		return
	}
	priority := 2
	if p, err := strconv.Atoi(r.FormValue("priority")); err == nil {
		priority = p
	}
	estMins := 0
	if e, err := strconv.Atoi(r.FormValue("estimated_minutes")); err == nil && e > 0 {
		estMins = e
	}
	req := TaskCreateRequest{
		Title:              title,
		Description:        r.FormValue("description"),
		Status:             r.FormValue("status"),
		Priority:           priority,
		IssueType:          r.FormValue("issue_type"),
		Assignee:           r.FormValue("assignee"),
		EstimatedMinutes:   estMins,
		AcceptanceCriteria: r.FormValue("acceptance_criteria"),
		Notes:              r.FormValue("notes"),
		DueAt:              r.FormValue("due_at"),
		Labels:             r.FormValue("labels"),
		Actor:              "admin",
	}
	task, err := s.taskManager.CreateTask(r.Context(), req)
	if err != nil {
		slog.Error("creating kanban task", "title", title, "error", err)
	} else {
		s.events.Broadcast("kanban-update", task.ID)
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", `{"kanban-update":"","close-panel":""}`)
		w.WriteHeader(http.StatusNoContent)
	} else {
		http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
	}
}

type taskFormData struct {
	Task       TaskRecord
	Columns    []string
	IssueTypes []struct{ Value, Label string }
	Priorities []struct {
		Value int
		Label string
	}
	Bots []string // registered bot names for assignee suggestions
}

func (s *Server) taskFormData() taskFormData {
	return taskFormData{
		Columns: s.kanbanColumns,
		IssueTypes: []struct{ Value, Label string }{
			{"task", "Task"}, {"bug", "Bug"}, {"feature", "Feature"},
			{"epic", "Epic"}, {"chore", "Chore"},
		},
		Priorities: []struct {
			Value int
			Label string
		}{
			{0, "Critical"}, {1, "High"}, {2, "Normal"}, {3, "Low"},
		},
	}
}

func (s *Server) taskFormDataWithBots(ctx context.Context) taskFormData {
	d := s.taskFormData()
	if s.db != nil {
		if insts, err := s.db.ListAllInstances(ctx); err == nil {
			for _, inst := range insts {
				d.Bots = append(d.Bots, inst.Name)
			}
		}
	}
	return d
}

// handleKanbanTaskDetail returns the task detail panel (HTMX fragment or full page).
func (s *Server) handleKanbanTaskDetail(w http.ResponseWriter, r *http.Request) {
	if s.taskManager == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	task, err := s.taskManager.GetTask(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := s.taskFormDataWithBots(r.Context())
	d.Task = task
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, "task-detail-panel", pageData{Data: d})
	} else {
		s.render(w, "task-detail.html", pageData{Title: task.Title, Data: d})
	}
}

// handleKanbanTaskUpdate saves changes to an existing task.
func (s *Server) handleKanbanTaskUpdate(w http.ResponseWriter, r *http.Request) {
	if s.taskManager == nil {
		http.Error(w, "task manager not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	priority := 2
	if p, err := strconv.Atoi(r.FormValue("priority")); err == nil {
		priority = p
	}
	estMins := 0
	if e, err := strconv.Atoi(r.FormValue("estimated_minutes")); err == nil && e > 0 {
		estMins = e
	}
	req := TaskUpdateRequest{
		Title:              r.FormValue("title"),
		Description:        r.FormValue("description"),
		Status:             r.FormValue("status"),
		Priority:           priority,
		IssueType:          r.FormValue("issue_type"),
		Assignee:           r.FormValue("assignee"),
		EstimatedMinutes:   estMins,
		AcceptanceCriteria: r.FormValue("acceptance_criteria"),
		Notes:              r.FormValue("notes"),
		DueAt:              r.FormValue("due_at"),
		Labels:             r.FormValue("labels"),
		Actor:              "admin",
	}
	if err := s.taskManager.UpdateTask(r.Context(), id, req); err != nil {
		slog.Error("updating kanban task", "id", id, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.events.Broadcast("kanban-update", id)
	if r.Header.Get("HX-Request") == "true" {
		// Return updated panel content so the modal can show the saved state.
		task, err := s.taskManager.GetTask(r.Context(), id)
		if err != nil {
			w.Header().Set("HX-Trigger", "kanban-update")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		d := s.taskFormDataWithBots(r.Context())
		d.Task = task
		s.renderFragment(w, "task-detail-panel", pageData{Data: d})
	} else {
		http.Redirect(w, r, "/admin/kanban", http.StatusSeeOther)
	}
}

// extractBeadsPrefix returns the prefix from a beads issue ID like "AH-42" → "AH".
func extractBeadsPrefix(issueID string) string {
	if idx := strings.Index(issueID, "-"); idx > 0 {
		return issueID[:idx]
	}
	return ""
}

// handleKanbanTaskAssign sets assignee + status=in_progress via drag-to-agent.
func (s *Server) handleKanbanTaskAssign(w http.ResponseWriter, r *http.Request) {
	if s.taskManager == nil {
		http.Error(w, "task manager not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	issueID := r.PathValue("id")
	assignee := r.FormValue("assignee")
	status := r.FormValue("status")
	if status == "" {
		status = "in_progress"
	}
	if err := s.taskManager.UpdateStatus(r.Context(), issueID, status, "", "admin"); err != nil {
		slog.Error("assigning task status", "id", issueID, "error", err)
	}
	// Set assignee via a separate targeted update (only assignee field).
	if assignee != "" {
		req := TaskUpdateRequest{Assignee: assignee, Status: status, Priority: -1, Actor: "admin"}
		if err := s.taskManager.UpdateTask(r.Context(), issueID, req); err != nil {
			slog.Error("assigning task", "id", issueID, "assignee", assignee, "error", err)
		}
	}

	// Create task assignment record for credential delivery.
	if adb := s.assignmentDB(); adb != nil && assignee != "" {
		agentID := ""
		if s.db != nil {
			if insts, err := s.db.ListAllInstances(r.Context()); err == nil {
				for _, inst := range insts {
					if inst.Name == assignee {
						agentID = inst.ID
						break
					}
				}
			}
		}
		if agentID != "" {
			prefix := extractBeadsPrefix(issueID)
			projectID := ""
			projectName := ""
			if prefix != "" {
				if pdb := s.projectDB(); pdb != nil {
					if project, err := pdb.GetProjectByBeadsPrefix(r.Context(), prefix); err == nil && project != nil {
						projectID = project.ID
						projectName = project.Name
						// Auto-grant agent to project.
						_ = pdb.AddProjectAgent(r.Context(), projectID, agentID, "system")
					}
				}
			}
			if assignmentID, err := newAPIUUID(); err == nil {
				if createErr := adb.CreateTaskAssignment(r.Context(), dolt.TaskAssignment{
					ID:         assignmentID,
					TaskID:     issueID,
					ProjectID:  projectID,
					AgentID:    agentID,
					AssignedBy: "system",
					AssignedAt: time.Now().UTC(),
				}); createErr == nil {
					// Enqueue inbox message so the agent learns about this assignment.
					credURL := ""
					if s.publicURL != "" {
						credURL = s.publicURL + "/api/credentials/" + assignmentID
					}
					taskTitle := issueID
					if task, taskErr := s.taskManager.GetTask(r.Context(), issueID); taskErr == nil {
						taskTitle = task.Title
					}
					tc := &TaskContext{
						TaskAssignmentID: assignmentID,
						TaskID:           issueID,
						ProjectID:        projectID,
						ProjectName:      projectName,
						CredentialURL:    credURL,
					}
					text := fmt.Sprintf("New task assigned: [%s] %s", issueID, taskTitle)
					s.inbox.EnqueueWithContext(assignee, "system", "", text, tc)
					slog.Info("task assigned to agent", "assignee", assignee, "task_id", issueID, "assignment_id", assignmentID)
				}
			}
		}
	}

	s.events.Broadcast("kanban-update", issueID)
	w.WriteHeader(http.StatusNoContent)
}

// handleKanbanAgents returns the agents pane fragment for the split kanban view.
// An agent is shown as alive if its DB last_seen_at is within the last 2 minutes.
func (s *Server) handleKanbanAgents(w http.ResponseWriter, r *http.Request) {
	type agentRow struct {
		Name    string
		IsAlive bool
	}

	const heartbeatWindow = 2 * time.Minute
	now := time.Now().UTC()

	var rows []agentRow
	if s.db != nil {
		if insts, err := s.db.ListAllInstances(r.Context()); err == nil {
			for _, inst := range insts {
				alive := inst.IsAlive
				if !alive && inst.LastSeenAt != nil {
					alive = now.Sub(*inst.LastSeenAt) <= heartbeatWindow
				}
				rows = append(rows, agentRow{Name: inst.Name, IsAlive: alive})
			}
		}
	}
	s.renderFragment(w, "kanban-agents", pageData{Data: rows})
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
		{"registration_token", "registration_token"},
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

// handlePutSetting handles PUT /api/settings/{key}.
// Admin-authenticated. Writes the value through the reactive settings store,
// notifying any registered watchers (e.g. rebuilds the OpenAI client on key change).
func (s *Server) handlePutSetting(w http.ResponseWriter, r *http.Request) {
	if !s.auth.IsAuthenticated(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if s.store == nil {
		http.Error(w, `{"error":"settings store not configured"}`, http.StatusServiceUnavailable)
		return
	}
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key is required"}`, http.StatusBadRequest)
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := s.store.Set(key, body.Value); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleGetSettings handles GET /api/settings.
// Admin-authenticated. Returns all setting keys (values masked for sensitive keys).
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if !s.auth.IsAuthenticated(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if s.store == nil {
		http.Error(w, `{"error":"settings store not configured"}`, http.StatusServiceUnavailable)
		return
	}
	// We can't list keys via the SecretStore interface — return a fixed list of known keys.
	sensitiveKeys := map[string]bool{
		"openai_api_key": true, "slack_bot_token": true, "slack_app_token": true,
		"admin_password_hash": true, "session_secret": true,
	}
	knownKeys := []string{
		"openai_api_key", "openai.model", "openai.base_url", "openai.max_tokens_str",
		"openai.system_prompt", "slack_bot_token", "slack_app_token",
		"registration_token", "admin_password_hash", "session_secret",
	}
	type kv struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Set   bool   `json:"set"`
	}
	var result []kv
	for _, k := range knownKeys {
		v := s.store.Get(k)
		display := v
		if sensitiveKeys[k] && v != "" {
			display = "***"
		}
		result = append(result, kv{Key: k, Value: display, Set: v != ""})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
