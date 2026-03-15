package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	beadslib "github.com/steveyegge/beads"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/api"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/beads"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/config"
	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/stretchr/testify/require"
)

// --------------------------------------------------------------------------
// mockBeadsStorage — implements beads.Storage for unit tests
// --------------------------------------------------------------------------

type mockBeadsStorage struct {
	issues     map[string]*beadslib.Issue
	createErr  error
	getErr     error
	updateErr  error
	commentErr error
	nextID     int
}

func newMockBeadsStorage() *mockBeadsStorage {
	return &mockBeadsStorage{issues: make(map[string]*beadslib.Issue)}
}

func (m *mockBeadsStorage) CreateIssue(_ context.Context, issue *beadslib.Issue, _ string) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.nextID++
	issue.ID = fmt.Sprintf("mock-%d", m.nextID)
	m.issues[issue.ID] = issue
	return nil
}
func (m *mockBeadsStorage) GetIssue(_ context.Context, id string) (*beadslib.Issue, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	issue, ok := m.issues[id]
	if !ok {
		return nil, fmt.Errorf("not found: %s", id)
	}
	return issue, nil
}
func (m *mockBeadsStorage) UpdateIssue(_ context.Context, id string, updates map[string]interface{}, _ string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if issue, ok := m.issues[id]; ok {
		if s, ok := updates["status"]; ok {
			issue.Status = beadslib.Status(fmt.Sprintf("%v", s))
		}
	}
	return nil
}
func (m *mockBeadsStorage) CloseIssue(_ context.Context, _, _, _, _ string) error { return nil }
func (m *mockBeadsStorage) SearchIssues(_ context.Context, _ string, _ beadslib.IssueFilter) ([]*beadslib.Issue, error) {
	return nil, nil
}
func (m *mockBeadsStorage) GetReadyWork(_ context.Context, _ beadslib.WorkFilter) ([]*beadslib.Issue, error) {
	return nil, nil
}
func (m *mockBeadsStorage) AddIssueComment(_ context.Context, _, _, _ string) (*beadslib.Comment, error) {
	if m.commentErr != nil {
		return nil, m.commentErr
	}
	return &beadslib.Comment{}, nil
}
func (m *mockBeadsStorage) SetConfig(_ context.Context, _, _ string) error { return nil }
func (m *mockBeadsStorage) GetConfig(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("not found")
}

// --------------------------------------------------------------------------
// beadsTaskManager tests
// --------------------------------------------------------------------------

func TestBeadsTaskManagerCreateTask(t *testing.T) {
	storage := newMockBeadsStorage()
	client := beads.NewWithStorage(storage)
	mgr := &beadsTaskManager{client: client}

	rec, err := mgr.CreateTask(context.Background(), api.TaskCreateRequest{Title: "fix bug", Description: "desc", Actor: "user1", Priority: 2})
	require.NoError(t, err)
	require.Equal(t, "fix bug", rec.Title)
	require.NotEmpty(t, rec.ID)
}

func TestBeadsTaskManagerCreateTaskError(t *testing.T) {
	storage := newMockBeadsStorage()
	storage.createErr = fmt.Errorf("db failure")
	client := beads.NewWithStorage(storage)
	mgr := &beadsTaskManager{client: client}

	_, err := mgr.CreateTask(context.Background(), api.TaskCreateRequest{Title: "fix bug", Actor: "user1"})
	require.Error(t, err)
}

func TestBeadsTaskManagerGetTask(t *testing.T) {
	storage := newMockBeadsStorage()
	_ = storage.CreateIssue(context.Background(), &beadslib.Issue{Title: "task1"}, "u1") //nolint
	client := beads.NewWithStorage(storage)
	mgr := &beadsTaskManager{client: client}

	// The issue is stored as "mock-1".
	rec, err := mgr.GetTask(context.Background(), "mock-1")
	require.NoError(t, err)
	require.Equal(t, "task1", rec.Title)
}

func TestBeadsTaskManagerGetTaskError(t *testing.T) {
	storage := newMockBeadsStorage()
	storage.getErr = fmt.Errorf("not found")
	client := beads.NewWithStorage(storage)
	mgr := &beadsTaskManager{client: client}

	_, err := mgr.GetTask(context.Background(), "missing")
	require.Error(t, err)
}

func TestBeadsTaskManagerUpdateStatus(t *testing.T) {
	storage := newMockBeadsStorage()
	_ = storage.CreateIssue(context.Background(), &beadslib.Issue{ID: "i1", Title: "t"}, "u") //nolint
	client := beads.NewWithStorage(storage)
	mgr := &beadsTaskManager{client: client}

	err := mgr.UpdateStatus(context.Background(), "i1", "done", "", "u")
	require.NoError(t, err)
}

// --------------------------------------------------------------------------
// openclawProber.CheckHealth / SendMentionOnly / SendOnboarding tests
// --------------------------------------------------------------------------

func startFakeOpenclaw(t *testing.T) (host string, port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	fmt.Sscanf(u.Port(), "%d", &port)
	return u.Hostname(), port
}

func TestOpenclawProberCheckHealth(t *testing.T) {
	host, port := startFakeOpenclaw(t)
	prober := &openclawProber{
		cfg:     config.OpenclawConfig{HealthPath: "/health", DirectivesPath: "/directives"},
		timeout: time.Second,
	}
	err := prober.CheckHealth(context.Background(), host, port)
	require.NoError(t, err)
}

func TestOpenclawProberCheckHealthFail(t *testing.T) {
	prober := &openclawProber{
		cfg:     config.OpenclawConfig{HealthPath: "/health", DirectivesPath: "/directives"},
		timeout: 100 * time.Millisecond,
	}
	// Port 1 is unlikely to be open.
	err := prober.CheckHealth(context.Background(), "127.0.0.1", 1)
	require.Error(t, err)
}

func TestOpenclawProberSendMentionOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port := 0
	fmt.Sscanf(u.Port(), "%d", &port)

	prober := &openclawProber{
		cfg:     config.OpenclawConfig{HealthPath: "/health", DirectivesPath: "/"},
		timeout: time.Second,
	}
	err := prober.SendMentionOnly(context.Background(), u.Hostname(), port)
	require.NoError(t, err)
}

func TestOpenclawProberSendOnboarding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port := 0
	fmt.Sscanf(u.Port(), "%d", &port)

	prober := &openclawProber{
		cfg:     config.OpenclawConfig{HealthPath: "/health", DirectivesPath: "/"},
		timeout: time.Second,
	}
	err := prober.SendOnboarding(context.Background(), u.Hostname(), port,
		"http://agenthub.example.com", "secret-token", "mybot")
	require.NoError(t, err)
}

// --------------------------------------------------------------------------
// doltBotRegistry tests
// --------------------------------------------------------------------------

func TestNewRegistryUUID(t *testing.T) {
	id1, err := newRegistryUUID()
	require.NoError(t, err)
	require.Len(t, id1, 36) // standard UUID format: 8-4-4-4-12
	require.Contains(t, id1, "-")

	id2, err := newRegistryUUID()
	require.NoError(t, err)
	require.NotEqual(t, id1, id2)
}

func TestDoltBotRegistryRegisterBot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("INSERT INTO openclaw_instances").WillReturnResult(sqlmock.NewResult(1, 1))

	reg := &doltBotRegistry{db: dolt.NewDB(db), cfg: config.OpenclawConfig{}}
	err = reg.RegisterBot(context.Background(), "ch1", "mybot", "127.0.0.1", 8080, "user1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDoltBotRegistryUnregisterBot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("DELETE FROM openclaw_instances").WillReturnResult(sqlmock.NewResult(1, 1))

	reg := &doltBotRegistry{db: dolt.NewDB(db), cfg: config.OpenclawConfig{}}
	err = reg.UnregisterBot(context.Background(), "ch1", "mybot", "user1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDoltBotRegistryListBots(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now()
	// scanInstance scans: id, name, host, port, owner_slack_user, channel_id, chatty, last_seen_at, is_alive, created_at, updated_at
	rows := sqlmock.NewRows([]string{"id", "name", "host", "port", "owner_slack_user", "channel_id", "chatty", "last_seen_at", "is_alive", "created_at", "updated_at"}).
		AddRow("id1", "bot1", "localhost", 8080, "user1", "ch1", 0, nil, 1, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	reg := &doltBotRegistry{db: dolt.NewDB(db), cfg: config.OpenclawConfig{}}
	bots, err := reg.ListBots(context.Background(), "ch1")
	require.NoError(t, err)
	require.Len(t, bots, 1)
	require.Equal(t, "bot1", bots[0].Name)
}

func TestDoltBotRegistryListBotsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	reg := &doltBotRegistry{db: dolt.NewDB(db), cfg: config.OpenclawConfig{}}
	_, err = reg.ListBots(context.Background(), "ch1")
	require.Error(t, err)
}

func TestDoltBotRegistrySetChatty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnResult(sqlmock.NewResult(1, 1))

	reg := &doltBotRegistry{db: dolt.NewDB(db), cfg: config.OpenclawConfig{}}
	err = reg.SetChatty(context.Background(), "ch1", "bot1", true)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDoltBotRegistryAliveBots(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "name", "host", "port", "owner_slack_user", "channel_id", "chatty", "last_seen_at", "is_alive", "created_at", "updated_at"}).
		AddRow("id1", "alive-bot", "localhost", 8080, "user1", "ch1", 0, nil, 1, now, now).
		AddRow("id2", "dead-bot", "localhost", 8081, "user1", "ch1", 0, nil, 0, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	reg := &doltBotRegistry{db: dolt.NewDB(db), cfg: config.OpenclawConfig{}}
	bots, err := reg.AliveBots(context.Background(), "ch1")
	require.NoError(t, err)
	require.Len(t, bots, 1)
	require.Equal(t, "alive-bot", bots[0].Name)
}

func TestDoltBotRegistryAliveBotsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db down"))

	reg := &doltBotRegistry{db: dolt.NewDB(db), cfg: config.OpenclawConfig{}}
	_, err = reg.AliveBots(context.Background(), "ch1")
	require.Error(t, err)
}

// --------------------------------------------------------------------------
// slackTaskManager tests
// --------------------------------------------------------------------------

func TestSlackTaskManagerNoBeads(t *testing.T) {
	mgr := &slackTaskManager{beads: nil, db: nil}
	_, _, err := mgr.CreateAndRoute(context.Background(), "fix it", "", "user1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "beads not configured")
}

func TestSlackTaskManagerCreateOnly(t *testing.T) {
	storage := newMockBeadsStorage()
	client := beads.NewWithStorage(storage)
	mgr := &slackTaskManager{beads: client, db: nil}

	taskID, assigned, err := mgr.CreateAndRoute(context.Background(), "do something", "specificbot", "user1")
	require.NoError(t, err)
	require.NotEmpty(t, taskID)
	require.Equal(t, "specificbot", assigned)
}

func TestSlackTaskManagerRouteToAliveBot(t *testing.T) {
	storage := newMockBeadsStorage()
	client := beads.NewWithStorage(storage)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	// ListAllInstances scans: id, name, host, port, owner_slack_user, channel_id, chatty, last_seen_at, is_alive, created_at, updated_at
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "name", "host", "port", "owner_slack_user", "channel_id", "chatty", "last_seen_at", "is_alive", "created_at", "updated_at"}).
		AddRow("id1", "routed-bot", "localhost", 8080, "u1", "ch1", 0, nil, 1, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	// AssignTask calls UpdateIssue which is in beads (mock storage), no SQL needed.

	mgr := &slackTaskManager{beads: client, db: dolt.NewDB(db)}
	taskID, assigned, err := mgr.CreateAndRoute(context.Background(), "task desc", "", "user1")
	require.NoError(t, err)
	require.NotEmpty(t, taskID)
	require.Equal(t, "routed-bot", assigned)
}

func TestSlackTaskManagerCreateError(t *testing.T) {
	storage := newMockBeadsStorage()
	storage.createErr = fmt.Errorf("beads write failed")
	client := beads.NewWithStorage(storage)
	mgr := &slackTaskManager{beads: client, db: nil}

	_, _, err := mgr.CreateAndRoute(context.Background(), "broken task", "", "user1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating task")
}

// --------------------------------------------------------------------------
// storeBackedChatter tests
// --------------------------------------------------------------------------

// mockSecretStore implements secretGetter for tests.
type mockSecretStore struct{ secrets map[string]string }

func (m *mockSecretStore) Get(key string) (string, error) { return m.secrets[key], nil }

func TestStoreBackedChatterNoKey(t *testing.T) {
	// When no key is set, Respond returns ("", nil) — noop behaviour.
	c := &storeBackedChatter{
		store: &mockSecretStore{secrets: map[string]string{}},
		cfg:   config.OpenAIConfig{Model: "test-model", MaxTokens: 16},
	}
	resp, err := c.Respond(context.Background(), "hello", "ch1")
	require.NoError(t, err)
	require.Empty(t, resp)
}

func TestStoreBackedChatterWithKey(t *testing.T) {
	// Fake OpenAI-compatible server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1234567890,
			"model": "gpt-4o-mini",
			"choices": []map[string]interface{}{
				{"index": 0, "finish_reason": "stop",
					"message": map[string]interface{}{"role": "assistant", "content": "Hello!"}},
			},
		})
	}))
	defer srv.Close()

	c := &storeBackedChatter{
		store: &mockSecretStore{secrets: map[string]string{"openai_api_key": "fake-key"}},
		cfg:   config.OpenAIConfig{BaseURL: srv.URL, Model: "gpt-4o-mini", MaxTokens: 16},
	}
	resp, err := c.Respond(context.Background(), "hello", "ch1")
	require.NoError(t, err)
	require.Equal(t, "Hello!", resp)
}

func TestStoreBackedChatterKeySetLate(t *testing.T) {
	// Verify the store is consulted on every call: key absent → empty, then key set → response.
	secrets := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "x", "object": "chat.completion", "created": 1,
			"model": "m",
			"choices": []map[string]interface{}{
				{"index": 0, "finish_reason": "stop",
					"message": map[string]interface{}{"role": "assistant", "content": "pong"}},
			},
		})
	}))
	defer srv.Close()

	c := &storeBackedChatter{
		store: &mockSecretStore{secrets: secrets},
		cfg:   config.OpenAIConfig{BaseURL: srv.URL, Model: "m", MaxTokens: 16},
	}

	resp, err := c.Respond(context.Background(), "ping", "ch")
	require.NoError(t, err)
	require.Empty(t, resp) // no key yet

	secrets["openai_api_key"] = "fake-key" // simulate 'agenthub secret set'

	resp, err = c.Respond(context.Background(), "ping", "ch")
	require.NoError(t, err)
	require.Equal(t, "pong", resp) // now live, no restart needed
}
