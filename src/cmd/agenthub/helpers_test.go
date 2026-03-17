package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	beadslib "github.com/steveyegge/beads"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/config"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/openclaw"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/store"
	"github.com/stretchr/testify/require"
)

// pipeStdin replaces os.Stdin with a pipe containing the given lines,
// restores it when the test ends, and returns a cleanup func.
func pipeStdin(t *testing.T, lines ...string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
	go func() {
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
		w.Close()
	}()
}

// minimalConfig creates a valid config YAML in a temp dir and returns its path.
// The store file does NOT exist (first-run scenario).
func minimalConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "secrets.enc")
	cfgPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`
server:
  http_addr: ":8080"
dolt:
  dsn: "root:@tcp(127.0.0.1:3306)/agenthub?timeout=1s"
store:
  path: %q
`, storePath)
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0600))
	return cfgPath
}

// minimalConfigWithStore creates a config YAML AND pre-creates the store file
// with the given password (without admin_password_hash). This puts cmdServe
// in the normal (non-setup-mode) flow.
func minimalConfigWithStore(t *testing.T, password string) string {
	t.Helper()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "secrets.enc")
	cfgPath := filepath.Join(dir, "config.yaml")
	st, err := store.Open(storePath, password)
	require.NoError(t, err)
	// Force the file to be written to disk (Open only creates in-memory store).
	require.NoError(t, st.Set("_init", "1"))
	content := fmt.Sprintf(`
server:
  http_addr: ":8080"
dolt:
  dsn: "root:@tcp(127.0.0.1:3306)/agenthub?timeout=1s"
store:
  path: %q
`, storePath)
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0600))
	return cfgPath
}

func TestGenerateSecret(t *testing.T) {
	s, err := generateSecret(32)
	require.NoError(t, err)
	require.NotEmpty(t, s)
	// 32 bytes → 64 hex chars.
	require.Len(t, s, 64)

	// Two generated secrets should be different.
	s2, err := generateSecret(32)
	require.NoError(t, err)
	require.NotEqual(t, s, s2)
}

func TestGenerateSecretZeroBytes(t *testing.T) {
	s, err := generateSecret(0)
	require.NoError(t, err)
	require.Equal(t, "", s)
}

func TestLoadTemplates(t *testing.T) {
	tmpl, err := loadTemplates()
	require.NoError(t, err)
	require.NotNil(t, tmpl)

	// All expected page templates should be present in the map.
	for _, name := range []string{
		"login.html", "dashboard.html",
		"bots.html", "kanban.html", "secrets.html", "setup.html",
	} {
		require.NotNil(t, tmpl[name], "template %q not found", name)
	}
	// Fragment aliases should be present.
	require.NotNil(t, tmpl["bots-table"], "fragment bots-table not found")
}

func TestSimpleKanbanBuilderBuild(t *testing.T) {
	kb := &simpleKanbanBuilder{
		cfg: config.KanbanConfig{Columns: []string{"open", "in_progress", "done"}},
	}
	board, err := kb.Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, board)
	require.Len(t, board.Columns, 3)
	require.Equal(t, "open", board.Columns[0].Status)
	require.Equal(t, "done", board.Columns[2].Status)
}

func TestSimpleKanbanBuilderEmptyColumns(t *testing.T) {
	kb := &simpleKanbanBuilder{cfg: config.KanbanConfig{}}
	board, err := kb.Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, board)
	require.Empty(t, board.Columns)
}

func TestReadPassword(t *testing.T) {
	pipeStdin(t, "mysecretpassword")
	pw, err := readPassword("Password: ")
	require.NoError(t, err)
	require.Equal(t, "mysecretpassword", pw)
}

func TestReadPasswordNoPrompt(t *testing.T) {
	pipeStdin(t, "anotherpassword")
	pw, err := readPassword("")
	require.NoError(t, err)
	require.Equal(t, "anotherpassword", pw)
}

// minimalConfigWithBadStore creates a config where the store path is inside a
// regular file (not a directory), causing store.Set to fail on write.
func minimalConfigWithBadStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Create a regular file at the path that store would want to be a directory.
	blockingFile := filepath.Join(dir, "notadir")
	require.NoError(t, os.WriteFile(blockingFile, []byte("block"), 0600))
	// Store path is inside that file (which is not a directory → MkdirAll fails).
	storePath := filepath.Join(blockingFile, "secrets.enc")
	cfgPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`
server:
  http_addr: ":8080"
dolt:
  dsn: "root:@tcp(127.0.0.1:3306)/agenthub?timeout=1s"
store:
  path: %q
`, storePath)
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0600))
	return cfgPath
}

func TestRunSetupStoreSaveFails(t *testing.T) {
	cfgPath := minimalConfigWithBadStore(t)
	t.Setenv("AGENTHUB_CONFIG", cfgPath)
	pipeStdin(t, "password", "password")
	err := run([]string{"setup"})
	require.Error(t, err)
}

func TestRunSetupPasswordMismatch(t *testing.T) {
	cfgPath := minimalConfig(t)
	t.Setenv("AGENTHUB_CONFIG", cfgPath)
	// Pipe two different passwords → should fail.
	pipeStdin(t, "password1", "password2")
	err := run([]string{"setup"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}

// mockOpenDBForSetup overrides openDB with a mock that handles Migrate,
// OpenDoltPersister (salt query + insert), and Set calls (3 settings keys).
// Returns a cleanup function (also registered via t.Cleanup).
func mockOpenDBForSetup(t *testing.T) {
	t.Helper()
	orig := openDB
	t.Cleanup(func() { openDB = orig })
	openDB = func(_ string) (*dolt.DB, error) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			return nil, err
		}
		mock.MatchExpectationsInOrder(false)

		// Liberal expectations: accept any query/exec that the setup flow needs.
		// Migrate: 1 CREATE schema_migrations + 1 SELECT + 16 migrations × 2 = 33 execs.
		// Then OpenDoltPersister + Set × 3 + IsInitialised.
		for i := 0; i < 50; i++ {
			mock.ExpectExec(`.*`).WillReturnResult(sqlmock.NewResult(0, 0))
		}
		mock.ExpectQuery(`SELECT name FROM schema_migrations`).
			WillReturnRows(sqlmock.NewRows([]string{"name"}))
		mock.ExpectQuery(`SELECT value FROM settings WHERE`).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`SELECT COUNT.*FROM settings`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		return dolt.NewDB(db), nil
	}
}

func TestRunSetupSuccess(t *testing.T) {
	cfgPath := minimalConfig(t)
	t.Setenv("AGENTHUB_CONFIG", cfgPath)
	mockOpenDBForSetup(t)
	pipeStdin(t, "securepassword", "securepassword")
	err := run([]string{"setup"})
	require.NoError(t, err)
}

func TestRunServeFailsWithoutSetup(t *testing.T) {
	// When IsInitialised returns false, serve enters setup mode.
	// Use an invalid address so the setup-mode HTTP server fails immediately.
	cfgPath := minimalConfigWithAddr(t, "invalid:::addr")
	t.Setenv("AGENTHUB_CONFIG", cfgPath)

	orig := openDB
	t.Cleanup(func() { openDB = orig })
	openDB = func(_ string) (*dolt.DB, error) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			return nil, err
		}
		mock.MatchExpectationsInOrder(false)
		for i := 0; i < 50; i++ {
			mock.ExpectExec(`.*`).WillReturnResult(sqlmock.NewResult(0, 0))
		}
		mock.ExpectQuery(`SELECT name FROM schema_migrations`).
			WillReturnRows(sqlmock.NewRows([]string{"name"}))
		mock.ExpectQuery(`SELECT value FROM settings WHERE`).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`SELECT COUNT.*FROM settings`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		return dolt.NewDB(db), nil
	}

	pipeStdin(t, "anypassword")
	err := run([]string{"serve"})
	require.Error(t, err)
}

func TestRunServeSetupMode(t *testing.T) {
	// Store file does not exist → setup mode starts. Invalid addr → fails immediately.
	cfgPath := minimalConfigWithAddr(t, "invalid:::addr")
	t.Setenv("AGENTHUB_CONFIG", cfgPath)
	err := run([]string{"serve"})
	require.Error(t, err)
	// Should fail at ListenAndServe (http server), not at config loading.
	require.NotContains(t, err.Error(), "loading config")
}

func TestRunServeFailsAtDolt(t *testing.T) {
	cfgPath := minimalConfig(t)
	t.Setenv("AGENTHUB_CONFIG", cfgPath)

	mockOpenDBForSetup(t)
	pipeStdin(t, "mypassword", "mypassword")
	require.NoError(t, run([]string{"setup"}))

	// Now override openDB to fail on serve.
	orig := openDB
	t.Cleanup(func() { openDB = orig })
	openDB = func(_ string) (*dolt.DB, error) {
		return nil, fmt.Errorf("dolt connection refused")
	}

	pipeStdin(t, "mypassword")
	err := run([]string{"serve"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "loading config")
}

func TestCmdSetupConfigLoadError(t *testing.T) {
	t.Setenv("AGENTHUB_CONFIG", "/nonexistent/path/config.yaml")
	err := run([]string{"setup"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "loading config")
}

func TestCmdServeConfigLoadError(t *testing.T) {
	t.Setenv("AGENTHUB_CONFIG", "/nonexistent/path/config.yaml")
	pipeStdin(t, "anypassword")
	err := run([]string{"serve"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "loading config")
}

func TestCmdServeReadPasswordError(t *testing.T) {
	cfgPath := minimalConfigWithStore(t, "somepassword")
	t.Setenv("AGENTHUB_CONFIG", cfgPath)

	orig := openDB
	t.Cleanup(func() { openDB = orig })
	openDB = func(_ string) (*dolt.DB, error) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			return nil, err
		}
		mock.MatchExpectationsInOrder(false)
		for i := 0; i < 50; i++ {
			mock.ExpectExec(`.*`).WillReturnResult(sqlmock.NewResult(0, 0))
		}
		mock.ExpectQuery(`SELECT name FROM schema_migrations`).
			WillReturnRows(sqlmock.NewRows([]string{"name"}))
		mock.ExpectQuery(`SELECT value FROM settings WHERE`).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`SELECT COUNT.*FROM settings`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		return dolt.NewDB(db), nil
	}

	pipeStdin(t) // no lines → stdin closed immediately
	err := run([]string{"serve"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading password")
}

func TestRunServeWithMockDB(t *testing.T) {
	cfgPath := minimalConfig(t)
	t.Setenv("AGENTHUB_CONFIG", cfgPath)

	mockOpenDBForSetup(t)
	pipeStdin(t, "testpw", "testpw")
	require.NoError(t, run([]string{"setup"}))

	// Override openDB so we don't need a real Dolt server.
	orig := openDB
	t.Cleanup(func() { openDB = orig })
	openDB = func(_ string) (*dolt.DB, error) {
		return nil, fmt.Errorf("injected mock DB error")
	}

	pipeStdin(t, "testpw")
	err := run([]string{"serve"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected mock DB error")
}

// mockInstancesLister implements instancesLister for unit tests.
type mockInstancesLister struct {
	instances []*dolt.Instance
	err       error
}

func (m *mockInstancesLister) ListAllInstances(_ context.Context) ([]*dolt.Instance, error) {
	return m.instances, m.err
}

func TestBotCheckerSuccess(t *testing.T) {
	// Start a fake openclaw health server that returns 200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	port := 0
	fmt.Sscanf(u.Port(), "%d", &port)

	lister := &mockInstancesLister{instances: []*dolt.Instance{
		{Name: "mybot", Host: u.Hostname(), Port: port},
	}}
	checker := &botChecker{
		db:      lister,
		timeout: 2 * time.Second,
		cfg:     config.OpenclawConfig{HealthPath: "/health", DirectivesPath: "/directives"},
	}
	alive, err := checker.CheckBot(context.Background(), "mybot")
	require.NoError(t, err)
	require.True(t, alive)
}

func TestBotCheckerNotFound(t *testing.T) {
	lister := &mockInstancesLister{}
	checker := &botChecker{db: lister, timeout: time.Second}
	_, err := checker.CheckBot(context.Background(), "nobot")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestBotCheckerListError(t *testing.T) {
	lister := &mockInstancesLister{err: errors.New("db down")}
	checker := &botChecker{db: lister, timeout: time.Second}
	_, err := checker.CheckBot(context.Background(), "mybot")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing instances")
}

// mockIssueSearcher implements kanban.IssueSearcher for unit tests.
type mockIssueSearcher struct{}

func (m *mockIssueSearcher) SearchIssues(_ context.Context, _ string, _ beadslib.IssueFilter) ([]*beadslib.Issue, error) {
	return nil, nil
}

func TestBeadsKanbanBuilder(t *testing.T) {
	kb := &beadsKanbanBuilder{storage: &mockIssueSearcher{}, columns: []string{"open", "in_progress"}}
	board, err := kb.Build(context.Background())
	require.NoError(t, err)
	require.Len(t, board.Columns, 2)
}

func TestNoopBotLister(t *testing.T) {
	n := &noopBotLister{}
	instances, err := n.ListAllInstances(context.Background())
	require.NoError(t, err)
	require.Empty(t, instances)
}

func TestOpenclawProber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	port := 0
	fmt.Sscanf(u.Port(), "%d", &port)

	prober := &openclawProber{
		cfg:     config.OpenclawConfig{HealthPath: "/", DirectivesPath: "/"},
		timeout: time.Second,
	}
	err := prober.Probe(context.Background(), u.Hostname(), port)
	require.NoError(t, err)
}

func TestRunServeFailsAtMigrate(t *testing.T) {
	cfgPath := minimalConfig(t)
	t.Setenv("AGENTHUB_CONFIG", cfgPath)

	mockOpenDBForSetup(t)
	pipeStdin(t, "testpw", "testpw")
	require.NoError(t, run([]string{"setup"}))

	// Override openDB to return a mock DB whose Migrate call fails.
	orig := openDB
	t.Cleanup(func() { openDB = orig })
	openDB = func(_ string) (*dolt.DB, error) {
		db, mock, err := sqlmock.New()
		if err != nil {
			return nil, err
		}
		mock.ExpectExec(".*").WillReturnError(fmt.Errorf("migration error"))
		return dolt.NewDB(db), nil
	}

	pipeStdin(t, "testpw")
	err := run([]string{"serve"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "running migrations")
}

// minimalConfigWithAddr creates a config YAML using the provided http_addr.
func minimalConfigWithAddr(t *testing.T, addr string) string {
	t.Helper()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "secrets.enc")
	cfgPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`
server:
  http_addr: %q
dolt:
  dsn: "root:@tcp(127.0.0.1:3306)/agenthub?timeout=1s"
store:
  path: %q
`, addr, storePath)
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0600))
	return cfgPath
}

func TestDoltCapacityUpdaterSuccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("INSERT INTO bot_capacity").WillReturnResult(sqlmock.NewResult(1, 1))

	updater := &doltCapacityUpdater{db: dolt.NewDB(db)}
	cap := &openclaw.CapacityReport{GPUFreeMB: 4096, JobsQueued: 1, JobsRunning: 0}
	require.NoError(t, updater.UpdateCapacity(context.Background(), "bot1", cap))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDoltCapacityUpdaterError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("INSERT INTO bot_capacity").WillReturnError(fmt.Errorf("db error"))

	updater := &doltCapacityUpdater{db: dolt.NewDB(db)}
	cap := &openclaw.CapacityReport{}
	require.Error(t, updater.UpdateCapacity(context.Background(), "bot1", cap))
}

func TestRunServeHttpBindFails(t *testing.T) {
	cfgPath := minimalConfigWithAddr(t, "invalid:::addr")
	t.Setenv("AGENTHUB_CONFIG", cfgPath)

	mockOpenDBForSetup(t)
	pipeStdin(t, "testpw", "testpw")
	require.NoError(t, run([]string{"setup"}))

	// Override openDB to return a mock DB with successful Migrate.
	orig := openDB
	t.Cleanup(func() { openDB = orig })
	openDB = func(_ string) (*dolt.DB, error) {
		db, mock, err := sqlmock.New()
		if err != nil {
			return nil, err
		}
		// Expect all migration execs to succeed.
		for i := 0; i < 10; i++ {
			mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
		}
		return dolt.NewDB(db), nil
	}

	pipeStdin(t, "testpw")
	err := run([]string{"serve"})
	require.Error(t, err)
	// Fails at ListenAndServe or migration exhausted.
	require.NotContains(t, err.Error(), "loading config")
}
