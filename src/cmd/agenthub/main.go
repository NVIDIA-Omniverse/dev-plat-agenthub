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
	islack "github.com/NVIDIA-DevPlat/agenthub/src/internal/slack"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/store"
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
	case "version":
		fmt.Printf("agenthub %s (build %s)\n", Version, Build)
		return nil
	default:
		return fmt.Errorf("unknown command %q — try: serve, setup, secret, version", args[0])
	}
}

func cmdServe(_ []string) error {
	cfgPath := "config.yaml"
	if v := os.Getenv("AGENTHUB_CONFIG"); v != "" {
		cfgPath = v
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	tmpl, err := loadTemplates()
	if err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}

	// Detect first-run: if the store file doesn't exist, start in setup mode.
	if _, statErr := os.Stat(cfg.Store.Path); os.IsNotExist(statErr) {
		return cmdServeSetupMode(cfg, tmpl)
	}

	// Unlock the encrypted store. AGENTHUB_ADMIN_PASSWORD env var takes
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

	st, err := store.Open(cfg.Store.Path, password)
	if err != nil {
		return fmt.Errorf("opening secrets store: %w", err)
	}

	adminHash, err := st.Get("admin_password_hash")
	if err != nil {
		return fmt.Errorf("admin password hash not found — run 'agenthub setup' first")
	}

	sessionSecret, err := st.Get("session_secret")
	if err != nil {
		return fmt.Errorf("session secret not found — run 'agenthub setup' first")
	}

	authMgr := auth.NewManager([]byte(sessionSecret), []byte(adminHash), cfg.Server.SessionCookieName)

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

	// Wire kanban to beads if configured, otherwise fall back to empty board.
	var kb api.KanbanBuilder
	var beadsClient *beads.Client
	if cfg.Beads.DBPath != "" {
		bc, beadsErr := beads.New(ctx, cfg.Beads.DBPath)
		if beadsErr != nil {
			slog.Warn("beads unavailable, kanban will show empty board", "error", beadsErr)
			kb = &simpleKanbanBuilder{cfg: cfg.Kanban}
		} else {
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
		api.WithHealthProber(&openclawProber{cfg: cfg.Openclaw, timeout: cfg.Openclaw.LivenessTimeout}),
		api.WithCapacityReader(db),
		api.WithKanbanColumns(cfg.Kanban.Columns),
	}
	if beadsClient != nil {
		opts = append(opts, api.WithTaskManager(&beadsTaskManager{client: beadsClient}))
	}

	srv := api.NewServer(authMgr, db, kb, st, tmpl, opts...)

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
	slackBotToken, _ := st.Get("slack_bot_token")
	slackAppToken, _ := st.Get("slack_app_token")
	if slackBotToken != "" && slackAppToken != "" {
		regToken, _ := st.Get("registration_token")
		openaiKey, _ := st.Get("openai_api_key")
		var aiChat islack.AIChatter
		if openaiKey != "" {
			aiChat = &openaiChatter{
				client: openai.NewClient(openaiKey, cfg.OpenAI.Model, cfg.OpenAI.MaxTokens, cfg.OpenAI.SystemPrompt),
			}
		} else {
			aiChat = &noopAIChatter{}
		}
		prober := &openclawProber{cfg: cfg.Openclaw, timeout: cfg.Openclaw.LivenessTimeout}
		slackDeps := &islack.Deps{
			BotRegistry:   &doltBotRegistry{db: db, cfg: cfg.Openclaw},
			TaskManager:   &slackTaskManager{beads: beadsClient, db: db},
			AIChat:        aiChat,
			OpenclawCheck: prober,
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

// cmdServeSetupMode starts the server in first-run setup mode (no store, no dolt).
func cmdServeSetupMode(cfg config.Config, tmpl map[string]*template.Template) error {
	// Placeholder auth — login always fails in setup mode (setup page is public).
	authMgr := auth.NewManager([]byte("setup-placeholder-32-bytes-pad!!"), nil, cfg.Server.SessionCookieName)

	srv := api.NewServer(
		authMgr,
		&noopBotLister{},
		&simpleKanbanBuilder{cfg: cfg.Kanban},
		nil,
		tmpl,
		api.WithSetupMode(cfg.Store.Path),
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
	cfgPath := "config.yaml"
	if v := os.Getenv("AGENTHUB_CONFIG"); v != "" {
		cfgPath = v
	}

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

	st, err := store.Open(cfg.Store.Path, password)
	if err != nil {
		return fmt.Errorf("creating store: %w", err)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := st.Set("admin_password_hash", hash); err != nil {
		return err
	}

	// Generate a random session secret.
	secret, err := generateSecret(32)
	if err != nil {
		return err
	}
	if err := st.Set("session_secret", secret); err != nil {
		return err
	}

	// Generate a registration token for bot auto-registration.
	regToken, err := generateSecret(16)
	if err != nil {
		return err
	}
	if err := st.Set("registration_token", regToken); err != nil {
		return err
	}

	fmt.Printf("Setup complete. Registration token: %s\nRun 'agenthub serve' to start.\n", regToken)
	return nil
}

// cmdSecret implements "agenthub secret set|get|list [key] [value]".
// It opens the encrypted store with the admin password and reads or writes secrets
// without requiring the full server to be running.
func cmdSecret(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthub secret <set|get|list> [key] [value]")
	}

	cfgPath := "config.yaml"
	if v := os.Getenv("AGENTHUB_CONFIG"); v != "" {
		cfgPath = v
	}

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

	st, err := store.Open(cfg.Store.Path, password)
	if err != nil {
		return fmt.Errorf("opening secrets store: %w", err)
	}

	switch args[0] {
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("usage: agenthub secret set <key> <value>")
		}
		if err := st.Set(args[1], args[2]); err != nil {
			return fmt.Errorf("setting %q: %w", args[1], err)
		}
		fmt.Printf("Secret %q saved.\n", args[1])

	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthub secret get <key>")
		}
		val, err := st.Get(args[1])
		if err != nil {
			return fmt.Errorf("getting %q: %w", args[1], err)
		}
		fmt.Println(val)

	case "list":
		knownKeys := []string{
			"openai_api_key", "slack_bot_token", "slack_app_token",
			"registration_token", "admin_password_hash", "session_secret",
		}
		for _, key := range knownKeys {
			val, _ := st.Get(key)
			if val != "" {
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
func loadTemplates() (map[string]*template.Template, error) {
	sub, err := fs.Sub(agenthub.Templates, "web/templates")
	if err != nil {
		return nil, err
	}
	pages := []string{
		"login.html", "setup.html", "dashboard.html",
		"bots.html", "kanban.html", "secrets.html", "task-create.html",
	}
	out := make(map[string]*template.Template, len(pages)+1)
	for _, page := range pages {
		t, err := template.ParseFS(sub, "layout.html", page)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", page, err)
		}
		out[page] = t
	}
	// Alias HTMX fragment names to their parent page template sets.
	out["bots-table"] = out["bots.html"]
	return out, nil
}

// simpleKanbanBuilder returns columns from config with no issues loaded.
type simpleKanbanBuilder struct {
	cfg config.KanbanConfig
}

func (kb *simpleKanbanBuilder) Build(_ context.Context) (*kanban.Board, error) {
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

func (kb *beadsKanbanBuilder) Build(ctx context.Context) (*kanban.Board, error) {
	return kanban.BuildBoard(ctx, kb.storage, kb.columns)
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
	return api.TaskRecord{ID: issue.ID, Title: issue.Title, Status: string(issue.Status)}, nil
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
			out = append(out, islack.BotSummary{Name: inst.Name, Host: inst.Host,
				Port: inst.Port, IsAlive: true, Chatty: inst.Chatty})
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
	beads *beads.Client
	db    *dolt.DB
}

func (m *slackTaskManager) CreateAndRoute(ctx context.Context, desc, botName, actor string) (string, string, error) {
	if m.beads == nil {
		return "", "", fmt.Errorf("beads not configured")
	}
	issue, err := m.beads.CreateTask(ctx, beads.TaskRequest{Title: desc, Actor: actor, Priority: 2})
	if err != nil {
		return "", "", fmt.Errorf("creating task: %w", err)
	}
	// Route to a specific bot if named, otherwise pick any alive bot.
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
	}
	return issue.ID, assigned, nil
}

// ── openaiChatter: adapts openai.Client to slack.AIChatter ───────────────────

type openaiChatter struct{ client *openai.Client }

func (c *openaiChatter) Respond(ctx context.Context, msg, _ string) (string, error) {
	return c.client.Chat(ctx, []openai.Message{{Role: "user", Content: msg}})
}

// noopAIChatter is used when no OpenAI key is configured.
type noopAIChatter struct{}

func (n *noopAIChatter) Respond(_ context.Context, _ string, _ string) (string, error) {
	return "(OpenAI not configured — set openai_api_key in Secrets)", nil
}
