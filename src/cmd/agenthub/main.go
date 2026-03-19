package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	agenthub "github.com/NVIDIA-DevPlat/agenthub"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/api"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/auth"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/beads"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/config"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/kanban"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/openclaw"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/openai"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/settings"
	islack "github.com/NVIDIA-DevPlat/agenthub/src/internal/slack"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/store"
	goslack "github.com/slack-go/slack"
	"golang.org/x/term"
)

// Version and Build are set at compile time via -ldflags.
var (
	Version = "dev"
	Build   = "unknown"
)

// openDB is the factory used to open the dolt database. Tests can override it.
var openDB = dolt.Open

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}

	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "setup":
		return cmdSetup(args[1:])
	case "secret":
		return cmdSecret(args[1:])
	case "client":
		return cmdClient(args[1:])
	case "version":
		fmt.Printf("agenthub %s (build %s)\n", Version, Build)
		return nil
	default:
		return fmt.Errorf("unknown command %q — try: serve, setup, secret, client, version", args[0])
	}
}

// resolveConfigPath returns the first config file that exists, checking:
//  1. $AGENTHUB_CONFIG env var
//  2. /etc/agenthub/config.yaml  (system install)
//  3. ~/.agenthub/config.yaml    (user install)
//  4. ./config.yaml              (development / current directory)
func resolveConfigPath() string {
	if v := os.Getenv("AGENTHUB_CONFIG"); v != "" {
		return v
	}
	candidates := []string{
		"/etc/agenthub/config.yaml",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".agenthub", "config.yaml"))
	}
	candidates = append(candidates, "config.yaml")
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "config.yaml" // let Load produce the "not found" error
}

func cmdServe(_ []string) error {
	cfgPath := resolveConfigPath()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	tmpl, err := loadTemplates()
	if err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}

	// Open Dolt and run migrations first (no password needed for Dolt itself).
	db, err := openDB(cfg.Dolt.DSN)
	if err != nil {
		return fmt.Errorf("opening dolt: %w", err)
	}
	defer db.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	// Check if this is a first-run (settings not yet initialised).
	initialised, err := dolt.IsInitialised(db)
	if err != nil {
		return fmt.Errorf("checking initialisation: %w", err)
	}
	if !initialised {
		return cmdServeSetupMode(cfg, db, tmpl)
	}

	// Unlock the encrypted settings. AGENTHUB_ADMIN_PASSWORD env var takes
	// precedence (for systemd/CI); falls back to interactive prompt.
	var password string
	if pw := os.Getenv("AGENTHUB_ADMIN_PASSWORD"); pw != "" {
		password = pw
	} else {
		var err error
		password, err = readPassword("Admin password: ")
		if err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
	}

	p, err := dolt.OpenDoltPersister(db, password)
	if err != nil {
		return fmt.Errorf("opening settings: %w", err)
	}

	// Auto-migrate from old file store if it exists and settings are sparse.
	if cfg.Store.Path != "" {
		if st, openErr := store.Open(cfg.Store.Path, password); openErr == nil {
			if migrateErr := p.MigrateFrom(st); migrateErr != nil {
				slog.Warn("auto-migration from file store incomplete", "error", migrateErr)
			}
		}
	}

	// settings.Store is the single source of truth for all runtime configuration.
	// It loads persisted values from the encrypted store, then seeds any YAML
	// defaults for keys not yet set. All components read from it (O(1) memory);
	// writes go through it so in-memory state + persistence update atomically.
	sett := settings.New(p)
	sett.Seed("openai.base_url", cfg.OpenAI.BaseURL)
	sett.Seed("openai.model", cfg.OpenAI.Model)
	sett.Seed("openai.max_tokens_str", fmt.Sprintf("%d", cfg.OpenAI.MaxTokens))
	sett.Seed("openai.system_prompt", cfg.OpenAI.SystemPrompt)

	adminHash := sett.Get("admin_password_hash")
	if adminHash == "" {
		return fmt.Errorf("admin password hash not found — run 'agenthub setup' first")
	}

	sessionSecret := sett.Get("session_secret")
	if sessionSecret == "" {
		return fmt.Errorf("session secret not found — run 'agenthub setup' first")
	}

	authMgr := auth.NewManager([]byte(sessionSecret), []byte(adminHash), cfg.Server.SessionCookieName)

	// Wire kanban to beads if configured, otherwise fall back to empty board.
	var kb api.KanbanBuilder
	var beadsClient *beads.Client
	if cfg.Beads.DBPath != "" {
		bc, beadsErr := beads.New(ctx, cfg.Beads.DBPath)
		if beadsErr != nil {
			slog.Warn("beads unavailable, kanban will show empty board", "error", beadsErr)
			kb = &simpleKanbanBuilder{cfg: cfg.Kanban}
		} else {
			if err := bc.EnsureInitialized(ctx, "AH"); err != nil {
				slog.Warn("beads init failed", "error", err)
			}
			beadsClient = bc
			kb = &beadsKanbanBuilder{storage: bc.Storage(), columns: cfg.Kanban.Columns}
		}
	} else {
		kb = &simpleKanbanBuilder{cfg: cfg.Kanban}
	}

	checker := &botChecker{db: db, timeout: cfg.Openclaw.LivenessTimeout, cfg: cfg.Openclaw}

	// Build server options.
	opts := []api.ServerOption{
		api.WithDeleter(db),
		api.WithChecker(checker),
		api.WithRegistrar(db),
		api.WithAgentSlackChannelUpdater(db),
		api.WithHealthProber(&openclawProber{cfg: cfg.Openclaw, timeout: cfg.Openclaw.LivenessTimeout}),
		api.WithCapacityReader(db),
		api.WithKanbanColumns(cfg.Kanban.Columns),
		api.WithPublicURL(cfg.Server.PublicURL),
	}
	if beadsClient != nil {
		btm := &beadsTaskManager{client: beadsClient}
		opts = append(opts, api.WithTaskManager(btm), api.WithTaskLogger(btm))
	}

	srv := api.NewServer(authMgr, db, kb, sett, tmpl, opts...)

	// Serve static files.
	staticSub, err := fs.Sub(agenthub.Static, "web/static")
	if err != nil {
		return fmt.Errorf("static fs sub: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	mux.Handle("/", srv)

	httpSrv := &http.Server{
		Addr:         cfg.Server.HTTPAddr,
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start Slack handler if tokens are available.
	slackBotToken := sett.Get("slack_bot_token")
	slackAppToken := sett.Get("slack_app_token")
	if slackBotToken != "" && slackAppToken != "" {
		regToken := sett.Get("registration_token")
		aiChat := newReactiveChatter(sett, db, cfg.Server.PublicURL)
		prober := &openclawProber{cfg: cfg.Openclaw, timeout: cfg.Openclaw.LivenessTimeout}

		// Wire the announcer for new bot registration announcements.
		if cfg.Slack.DefaultChannel != "" {
			announceClient := goslack.New(slackBotToken)
			srv.SetAnnouncer(&slackReplier{client: announceClient}, cfg.Slack.DefaultChannel)
		}

		slackDeps := &islack.Deps{
			BotRegistry:        &doltBotRegistry{db: db, cfg: cfg.Openclaw},
			TaskManager:        &slackTaskManager{beads: beadsClient, db: db, inbox: srv.Inbox(), publicURL: cfg.Server.PublicURL},
			AIChat:             aiChat,
			OpenclawCheck:      prober,
			Inbox:              srv.Inbox(),
			AgentChannelLookup: &doltAgentChannelLookup{db: db},
			Config: islack.SlackConfig{
				CommandPrefix:     cfg.Slack.CommandPrefix,
				AgenthubURL:       cfg.Server.PublicURL,
				RegistrationToken: regToken,
			},
		}
		slackHandler := islack.NewHandler(slackBotToken, slackAppToken, slackDeps)
		go func() {
			if err := slackHandler.Run(ctx); err != nil {
				slog.Error("slack handler exited", "error", err)
			}
		}()
		slog.Info("slack: connected via Socket Mode")
	} else {
		slog.Info("slack: tokens not configured, skipping Slack integration")
	}

	fmt.Printf("agenthub %s serving on %s\n", Version, cfg.Server.HTTPAddr)

	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// cmdServeSetupMode starts the server in first-run setup mode.
func cmdServeSetupMode(cfg config.Config, db *dolt.DB, tmpl map[string]*template.Template) error {
	// Placeholder auth — login always fails in setup mode (setup page is public).
	authMgr := auth.NewManager([]byte("setup-placeholder-32-bytes-pad!!"), nil, cfg.Server.SessionCookieName)

	setupFn := func(password string) (string, error) {
		p, err := dolt.OpenDoltPersister(db, password)
		if err != nil {
			return "", fmt.Errorf("initialising settings: %w", err)
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return "", err
		}
		if err := p.Set("admin_password_hash", hash); err != nil {
			return "", err
		}
		secret, err := generateSecret(32)
		if err != nil {
			return "", err
		}
		if err := p.Set("session_secret", secret); err != nil {
			return "", err
		}
		regToken, err := generateSecret(16)
		if err != nil {
			return "", err
		}
		if err := p.Set("registration_token", regToken); err != nil {
			return "", err
		}
		return regToken, nil
	}

	srv := api.NewServer(
		authMgr,
		&noopBotLister{},
		&simpleKanbanBuilder{cfg: cfg.Kanban},
		nil,
		tmpl,
		api.WithSetupMode(setupFn),
	)

	staticSub, err := fs.Sub(agenthub.Static, "web/static")
	if err != nil {
		return fmt.Errorf("static fs sub: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	mux.Handle("/", srv)

	httpSrv := &http.Server{
		Addr:         cfg.Server.HTTPAddr,
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	fmt.Printf("agenthub %s in setup mode — visit http://localhost%s/admin/setup\n", Version, cfg.Server.HTTPAddr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server (setup mode): %w", err)
	}
	return nil
}

// noopBotLister satisfies api.BotLister with no bots (used in setup mode).
type noopBotLister struct{}

func (n *noopBotLister) ListAllInstances(_ context.Context) ([]*dolt.Instance, error) {
	return nil, nil
}

func cmdSetup(_ []string) error {
	cfgPath := resolveConfigPath()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	fmt.Print("Choose admin password: ")
	password, err := readPassword("")
	if err != nil {
		return err
	}

	fmt.Print("Confirm password: ")
	confirm, err := readPassword("")
	if err != nil {
		return err
	}
	if password != confirm {
		return fmt.Errorf("passwords do not match")
	}

	db, err := openDB(cfg.Dolt.DSN)
	if err != nil {
		return fmt.Errorf("opening dolt: %w", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	p, err := dolt.OpenDoltPersister(db, password)
	if err != nil {
		return fmt.Errorf("initialising settings: %w", err)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := p.Set("admin_password_hash", hash); err != nil {
		return err
	}

	// Generate a random session secret.
	secret, err := generateSecret(32)
	if err != nil {
		return err
	}
	if err := p.Set("session_secret", secret); err != nil {
		return err
	}

	// Generate a registration token for bot auto-registration.
	regToken, err := generateSecret(16)
	if err != nil {
		return err
	}
	if err := p.Set("registration_token", regToken); err != nil {
		return err
	}

	fmt.Printf("Setup complete. Registration token: %s\nRun 'agenthub serve' to start.\n", regToken)
	return nil
}

// cmdSecret implements "agenthub secret set|get|list [key] [value]".
// It opens the Dolt-backed settings with the admin password and reads or writes secrets
// without requiring the full server to be running.
func cmdSecret(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthub secret <set|get|list> [key] [value]")
	}

	cfgPath := resolveConfigPath()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var password string
	if pw := os.Getenv("AGENTHUB_ADMIN_PASSWORD"); pw != "" {
		password = pw
	} else {
		password, err = readPassword("Admin password: ")
		if err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
	}

	db, err := openDB(cfg.Dolt.DSN)
	if err != nil {
		return fmt.Errorf("opening dolt: %w", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	p, err := dolt.OpenDoltPersister(db, password)
	if err != nil {
		return fmt.Errorf("opening settings: %w", err)
	}
	sett := settings.New(p)

	switch args[0] {
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("usage: agenthub secret set <key> <value>")
		}
		if err := sett.Set(args[1], args[2]); err != nil {
			return fmt.Errorf("setting %q: %w", args[1], err)
		}
		fmt.Printf("Secret %q saved.\n", args[1])

	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthub secret get <key>")
		}
		val := sett.Get(args[1])
		if val == "" {
			return fmt.Errorf("%q is not set", args[1])
		}
		fmt.Println(val)

	case "list":
		knownKeys := []string{
			"openai_api_key", "slack_bot_token", "slack_app_token",
			"registration_token", "admin_password_hash", "session_secret",
		}
		for _, key := range knownKeys {
			if sett.Get(key) != "" {
				fmt.Printf("%-24s (set)\n", key)
			} else {
				fmt.Printf("%-24s (not set)\n", key)
			}
		}

	default:
		return fmt.Errorf("unknown secret subcommand %q — try: set, get, list", args[0])
	}
	return nil
}

// loadTemplates builds a per-page template map. Each entry is the layout
// template parsed together with one page file so that {{define "title"}} and
// {{define "content"}} in each page override the layout's {{block}} defaults
// independently — preventing the last-parsed file from winning for all pages.
// humanStatus converts a beads status value to a display-friendly string.
// "in_progress" → "In Progress", "open" → "Open", etc.
func humanStatus(s string) string {
	words := strings.Split(strings.ReplaceAll(s, "_", " "), " ")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func loadTemplates() (map[string]*template.Template, error) {
	sub, err := fs.Sub(agenthub.Templates, "web/templates")
	if err != nil {
		return nil, err
	}
	funcs := template.FuncMap{"humanStatus": humanStatus}
	pages := []string{
		"login.html", "setup.html", "dashboard.html",
		"bots.html", "kanban.html", "secrets.html", "task-create.html", "task-detail.html",
		"resources.html", "projects.html", "chat.html",
	}
	out := make(map[string]*template.Template, len(pages)+1)
	for _, page := range pages {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(sub, "layout.html", page)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", page, err)
		}
		out[page] = t
	}
	// Alias HTMX fragment names to their parent page template sets.
	out["bots-table"]        = out["bots.html"]
	out["task-create-panel"] = out["task-create.html"]
	out["task-detail-panel"] = out["task-detail.html"]
	out["kanban-agents"]     = out["kanban.html"]
	return out, nil
}

// simpleKanbanBuilder returns columns from config with no issues loaded.
type simpleKanbanBuilder struct {
	cfg config.KanbanConfig
}

func (kb *simpleKanbanBuilder) Build(_ context.Context, _ kanban.BoardFilter) (*kanban.Board, error) {
	board := &kanban.Board{}
	for _, col := range kb.cfg.Columns {
		board.Columns = append(board.Columns, &kanban.Column{Status: col})
	}
	return board, nil
}

// beadsKanbanBuilder builds a live kanban board from the beads issue tracker.
type beadsKanbanBuilder struct {
	storage kanban.IssueSearcher
	columns []string
}

func (kb *beadsKanbanBuilder) Build(ctx context.Context, filter kanban.BoardFilter) (*kanban.Board, error) {
	return kanban.BuildBoard(ctx, kb.storage, kb.columns, filter)
}

// instancesLister is the subset of dolt.DB used by botChecker.
type instancesLister interface {
	ListAllInstances(ctx context.Context) ([]*dolt.Instance, error)
}

// botChecker implements api.BotChecker using a dolt DB and openclaw HTTP client.
type botChecker struct {
	db      instancesLister
	timeout time.Duration
	cfg     config.OpenclawConfig
}

func (bc *botChecker) CheckBot(ctx context.Context, name string) (bool, error) {
	instances, err := bc.db.ListAllInstances(ctx)
	if err != nil {
		return false, fmt.Errorf("listing instances: %w", err)
	}
	for _, inst := range instances {
		if inst.Name == name {
			client := openclaw.NewClient(inst.Host, inst.Port, bc.timeout,
				bc.cfg.HealthPath, bc.cfg.DirectivesPath)
			checkCtx, cancel := context.WithTimeout(ctx, bc.timeout)
			defer cancel()
			err := client.Health(checkCtx)
			return err == nil, err
		}
	}
	return false, fmt.Errorf("bot %q not found", name)
}

// openclawProber satisfies api.HealthProber using a fresh openclaw.Client.
type openclawProber struct {
	cfg     config.OpenclawConfig
	timeout time.Duration
}

func (p *openclawProber) Probe(ctx context.Context, host string, port int) error {
	client := openclaw.NewClient(host, port, p.timeout, p.cfg.HealthPath, p.cfg.DirectivesPath)
	probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return client.Health(probeCtx)
}

// doltCapacityUpdater adapts dolt.DB to openclaw.CapacityUpdater.
type doltCapacityUpdater struct{ db *dolt.DB }

func (d *doltCapacityUpdater) UpdateCapacity(ctx context.Context, id string, cap *openclaw.CapacityReport) error {
	return d.db.UpdateCapacity(ctx, id, dolt.Capacity{
		BotID:       id,
		GPUFreeMB:   cap.GPUFreeMB,
		JobsQueued:  cap.JobsQueued,
		JobsRunning: cap.JobsRunning,
	})
}

// beadsTaskManager adapts *beads.Client to api.TaskManager.
type beadsTaskManager struct{ client *beads.Client }

func (m *beadsTaskManager) UpdateStatus(ctx context.Context, issueID, status, note, actor string) error {
	return m.client.UpdateStatus(ctx, issueID, status, note, actor)
}

func (m *beadsTaskManager) GetTask(ctx context.Context, id string) (api.TaskRecord, error) {
	issue, err := m.client.GetTask(ctx, id)
	if err != nil {
		return api.TaskRecord{}, err
	}
	rec := api.TaskRecord{
		ID:                 issue.ID,
		Title:              issue.Title,
		Status:             string(issue.Status),
		Description:        issue.Description,
		Priority:           issue.Priority,
		IssueType:          string(issue.IssueType),
		Assignee:           issue.Assignee,
		AcceptanceCriteria: issue.AcceptanceCriteria,
		Notes:              issue.Notes,
		Labels:             strings.Join(issue.Labels, ", "),
		CreatedBy:          issue.CreatedBy,
		CreatedAt:          issue.CreatedAt.Format("2006-01-02 15:04 UTC"),
		UpdatedAt:          issue.UpdatedAt.Format("2006-01-02 15:04 UTC"),
	}
	if issue.EstimatedMinutes != nil {
		rec.EstimatedMinutes = *issue.EstimatedMinutes
	}
	if issue.DueAt != nil {
		rec.DueAt = issue.DueAt.Format("2006-01-02")
	}
	return rec, nil
}

func (m *beadsTaskManager) UpdateTask(ctx context.Context, issueID string, req api.TaskUpdateRequest) error {
	return m.client.UpdateFields(ctx, issueID, req.Title, req.Description, req.Status,
		req.Priority, req.IssueType, req.Assignee, req.EstimatedMinutes,
		req.AcceptanceCriteria, req.Notes, req.DueAt, req.Labels, req.Actor)
}

// AddLog implements api.TaskLogger by appending a comment to the beads issue.
func (m *beadsTaskManager) AddLog(ctx context.Context, issueID, actor, message string) error {
	return m.client.AddComment(ctx, issueID, actor, message)
}

func (m *beadsTaskManager) CreateTask(ctx context.Context, req api.TaskCreateRequest) (api.TaskRecord, error) {
	issue, err := m.client.CreateTask(ctx, beads.TaskRequest{
		Title:              req.Title,
		Description:        req.Description,
		Status:             req.Status,
		Priority:           req.Priority,
		IssueType:          req.IssueType,
		Assignee:           req.Assignee,
		EstimatedMinutes:   req.EstimatedMinutes,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Notes:              req.Notes,
		DueAt:              req.DueAt,
		Labels:             req.Labels,
		Actor:              req.Actor,
	})
	if err != nil {
		return api.TaskRecord{}, err
	}
	return api.TaskRecord{ID: issue.ID, Title: issue.Title, Status: string(issue.Status)}, nil
}

// readPassword reads a password from stdin with echo suppressed when on a real
// terminal. Falls back to fmt.Scan for pipes and non-TTY environments (tests, CI).
func readPassword(prompt string) (string, error) {
	if prompt != "" {
		fmt.Print(prompt)
	}
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		pw, err := term.ReadPassword(fd)
		if err != nil {
			return "", fmt.Errorf("reading password: %w", err)
		}
		fmt.Println() // emit newline suppressed by ReadPassword
		return string(pw), nil
	}
	var pw string
	if _, err := fmt.Scan(&pw); err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return pw, nil
}

// generateSecret generates n random bytes encoded as hex.
func generateSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating secret: %w", err)
	}
	return fmt.Sprintf("%x", buf), nil
}

// ── openclawProber: satisfies both api.HealthProber and slack.OpenclawChecker ──

// CheckHealth satisfies slack.OpenclawChecker.
func (p *openclawProber) CheckHealth(ctx context.Context, host string, port int) error {
	return p.Probe(ctx, host, port)
}

// SendMentionOnly satisfies slack.OpenclawChecker.
func (p *openclawProber) SendMentionOnly(ctx context.Context, host string, port int) error {
	client := openclaw.NewClient(host, port, p.timeout, p.cfg.HealthPath, p.cfg.DirectivesPath)
	sendCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return client.SetMentionOnly(sendCtx)
}

// SendOnboarding satisfies slack.OpenclawChecker and delivers the full BOTJILE directive.
func (p *openclawProber) SendOnboarding(ctx context.Context, host string, port int, agenthubURL, regToken, botName string) error {
	client := openclaw.NewClient(host, port, p.timeout, p.cfg.HealthPath, p.cfg.DirectivesPath)
	sendCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return client.SendOnboarding(sendCtx, openclaw.OnboardingInfo{
		AgenthubURL:       agenthubURL,
		RegistrationToken: regToken,
		BotName:           botName,
		CreateTaskOnDM:    true,
		Instructions: "BOTJILE policy: when a user DMs you with a task, call " +
			"POST " + agenthubURL + "/api/tasks (X-Registration-Token: " + regToken + ") " +
			"with {\"title\":\"<task>\",\"bot_name\":\"" + botName + "\"} BEFORE starting work. " +
			"Update status via POST " + agenthubURL + "/api/tasks/{id}/status as you progress.",
	})
}

// ── doltBotRegistry adapts dolt.DB to slack.BotRegistry ──────────────────────

type doltBotRegistry struct {
	db  *dolt.DB
	cfg config.OpenclawConfig
}

func (r *doltBotRegistry) RegisterBot(ctx context.Context, channelID, name, host string, port int, owner string) error {
	// Generate a new UUID v4 for the instance.
	id, err := newRegistryUUID()
	if err != nil {
		return fmt.Errorf("generating bot id: %w", err)
	}
	return r.db.CreateInstance(ctx, dolt.Instance{
		ID:             id,
		Name:           name,
		Host:           host,
		Port:           port,
		ChannelID:      channelID,
		OwnerSlackUser: owner,
		IsAlive:        true,
	})
}

func (r *doltBotRegistry) UnregisterBot(ctx context.Context, channelID, name, _ string) error {
	return r.db.DeleteInstance(ctx, name, channelID)
}

func (r *doltBotRegistry) ListBots(ctx context.Context, channelID string) ([]islack.BotSummary, error) {
	instances, err := r.db.ListInstances(ctx, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]islack.BotSummary, len(instances))
	for i, inst := range instances {
		out[i] = islack.BotSummary{Name: inst.Name, Host: inst.Host, Port: inst.Port,
			IsAlive: inst.IsAlive, Chatty: inst.Chatty}
		if profile, err := r.db.GetBotProfile(ctx, inst.Name); err == nil && profile != nil {
			out[i].Specializations = profile.Specializations
		}
	}
	return out, nil
}

func (r *doltBotRegistry) SetChatty(ctx context.Context, channelID, name string, chatty bool) error {
	return r.db.UpdateChatty(ctx, name, channelID, chatty)
}

func (r *doltBotRegistry) AliveBots(ctx context.Context, channelID string) ([]islack.BotSummary, error) {
	instances, err := r.db.ListInstances(ctx, channelID)
	if err != nil {
		return nil, err
	}
	var out []islack.BotSummary
	for _, inst := range instances {
		if inst.IsAlive {
			s := islack.BotSummary{Name: inst.Name, Host: inst.Host,
				Port: inst.Port, IsAlive: true, Chatty: inst.Chatty}
			if profile, pErr := r.db.GetBotProfile(ctx, inst.Name); pErr == nil && profile != nil {
				s.Specializations = profile.Specializations
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// newRegistryUUID generates a UUID v4 for new bot registrations.
func newRegistryUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// ── slackTaskManager: CreateAndRoute for the Slack handler ────────────────────

// slackTaskManager implements slack.TaskManager using beads for task creation
// and dolt for bot lookup (to route to alive bots when no specific bot is named).
type slackTaskManager struct {
	beads     *beads.Client
	db        *dolt.DB
	inbox     *api.Inbox
	publicURL string
}

func (m *slackTaskManager) CreateAndRoute(ctx context.Context, desc, botName, actor string) (string, string, error) {
	if m.beads == nil {
		return "", "", fmt.Errorf("beads not configured")
	}
	issue, err := m.beads.CreateTask(ctx, beads.TaskRequest{Title: desc, Actor: actor, Priority: 2})
	if err != nil {
		return "", "", fmt.Errorf("creating task: %w", err)
	}
	assigned := botName
	if assigned == "" && m.db != nil {
		if bots, err := m.db.ListAllInstances(ctx); err == nil {
			for _, b := range bots {
				if b.IsAlive {
					assigned = b.Name
					break
				}
			}
		}
	}
	if assigned != "" {
		if err := m.beads.AssignTask(ctx, issue.ID, assigned, actor); err != nil {
			slog.Warn("slack: could not assign task to bot", "task", issue.ID, "bot", assigned, "error", err)
		}
		if m.db != nil && m.inbox != nil {
			agentID := ""
			if bots, err := m.db.ListAllInstances(ctx); err == nil {
				for _, b := range bots {
					if b.Name == assigned {
						agentID = b.ID
						break
					}
				}
			}
			if agentID != "" {
				assignmentID := fmt.Sprintf("ta-%x", time.Now().UnixNano())
				ta := dolt.TaskAssignment{
					ID:         assignmentID,
					TaskID:     issue.ID,
					AgentID:    agentID,
					AssignedBy: actor,
					AssignedAt: time.Now().UTC(),
				}
				if err := m.db.CreateTaskAssignment(ctx, ta); err == nil {
					credURL := ""
					if m.publicURL != "" {
						credURL = m.publicURL + "/api/credentials/" + assignmentID
					}
					tc := &api.TaskContext{
						TaskAssignmentID: assignmentID,
						TaskID:           issue.ID,
						CredentialURL:    credURL,
					}
					m.inbox.EnqueueWithContext(assigned, actor, "", fmt.Sprintf("New task: [%s] %s", issue.ID, desc), tc)
				}
			}
		}
	}
	return issue.ID, assigned, nil
}

// ── doltAgentChannelLookup: maps Slack channel ID → agent name ────────────────

type doltAgentChannelLookup struct{ db *dolt.DB }

func (l *doltAgentChannelLookup) AgentBySlackChannel(ctx context.Context, channelID string) (string, error) {
	return l.db.GetAgentBySlackChannel(ctx, channelID)
}

// ── slackReplier: posts agent replies to Slack via the bot token ──────────────

type slackReplier struct{ client *goslack.Client }

func (r *slackReplier) PostMessage(_ context.Context, channel, text string) error {
	_, _, err := r.client.PostMessage(channel, goslack.MsgOptionText(text, false))
	return err
}

// ── reactiveChatter: rebuilds openai.Client only when settings change ─────────
//
// newReactiveChatter registers Watch callbacks on the settings keys it cares
// about. The openai.Client is cached and only rebuilt when one of those keys
// changes — not on every Respond call. This gives O(1) hot-path reads with
// immediate propagation of setting changes (no restart required).

type reactiveChatter struct {
	mu             sync.RWMutex
	sett           *settings.Store
	client         *openai.Client
	botLister      api.BotLister
	projectLister  interface{ ListAllProjects(ctx context.Context) ([]*dolt.Project, error) }
	publicURL      string
}

func newReactiveChatter(s *settings.Store, db api.BotLister, publicURL string) *reactiveChatter {
	c := &reactiveChatter{sett: s, publicURL: publicURL}
	if db != nil {
		c.botLister = db
		if pl, ok := db.(interface {
			ListAllProjects(ctx context.Context) ([]*dolt.Project, error)
		}); ok {
			c.projectLister = pl
		}
	}
	c.rebuild()
	rebuild := func(_ string) { c.rebuild() }
	s.Watch("openai_api_key", rebuild)
	s.Watch("openai.base_url", rebuild)
	s.Watch("openai.model", rebuild)
	s.Watch("openai.system_prompt", rebuild)
	return c
}

func (c *reactiveChatter) rebuild() {
	key := c.sett.Get("openai_api_key")
	var client *openai.Client
	if key != "" {
		maxTokens := cfg2int(c.sett.Get("openai.max_tokens_str"), 1024)
		client = openai.NewClient(
			key,
			c.sett.Get("openai.model"),
			maxTokens,
			c.sett.Get("openai.system_prompt"),
			c.sett.Get("openai.base_url"),
		)
	}
	c.mu.Lock()
	c.client = client
	c.mu.Unlock()
}

func (c *reactiveChatter) Respond(ctx context.Context, msg, _ string) (string, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()
	if client == nil {
		return "", nil
	}

	oc := openai.OnboardingContext{PublicURL: c.publicURL}
	if c.botLister != nil {
		if bots, err := c.botLister.ListAllInstances(ctx); err == nil {
			for _, b := range bots {
				bi := openai.BotInfo{Name: b.Name, IsAlive: b.IsAlive}
				if pdb, ok := c.botLister.(interface {
					GetBotProfile(ctx context.Context, name string) (*dolt.BotProfile, error)
				}); ok {
					if profile, err := pdb.GetBotProfile(ctx, b.Name); err == nil && profile != nil {
						bi.Specializations = profile.Specializations
					}
				}
				oc.Bots = append(oc.Bots, bi)
			}
		}
	}
	if c.projectLister != nil {
		if projects, err := c.projectLister.ListAllProjects(ctx); err == nil {
			for _, p := range projects {
				oc.Projects = append(oc.Projects, openai.ProjectInfo{Name: p.Name, Description: p.Description})
			}
		}
	}
	systemPrompt := openai.BuildOnboardingPrompt(oc)

	return client.ChatWithSystem(ctx, systemPrompt, []openai.Message{{Role: "user", Content: msg}})
}

func cfg2int(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 0 {
		return n
	}
	return def
}
