package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestClient creates a Client pointed at the given test server URL.
func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	return &Client{
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		baseURL:        serverURL,
		healthPath:     "/health",
		directivesPath: "/directives",
	}
}

func TestHealthOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	require.NoError(t, c.Health(context.Background()))
}

func TestHealthNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	err := c.Health(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
}

func TestHealthTimeout(t *testing.T) {
	// Block until the server-side request context is cancelled, then respond.
	// The client context is pre-cancelled so Health returns immediately with
	// a context error — no timer race.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the call — Health must return a context error

	c := newTestClient(t, srv.URL)
	err := c.Health(ctx)
	require.Error(t, err)
}

func TestSetMentionOnly(t *testing.T) {
	var received directive
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/directives", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	require.NoError(t, c.SetMentionOnly(context.Background()))
	require.NotNil(t, received.MentionOnly)
	require.True(t, *received.MentionOnly)
}

func TestSetChattyTrue(t *testing.T) {
	var received directive
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	require.NoError(t, c.SetChatty(context.Background(), true))
	require.NotNil(t, received.Chatty)
	require.True(t, *received.Chatty)
}

func TestSetChattyFalse(t *testing.T) {
	var received directive
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	require.NoError(t, c.SetChatty(context.Background(), false))
	require.NotNil(t, received.Chatty)
	require.False(t, *received.Chatty)
}

func TestDirectiveNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	err := c.SetMentionOnly(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
}

// mockLister implements InstanceLister for testing LivenessChecker.
type mockLister struct {
	instances []InstanceRecord
	updates   map[string]bool
}

func newMockLister(instances []InstanceRecord) *mockLister {
	return &mockLister{instances: instances, updates: make(map[string]bool)}
}

func (m *mockLister) ListAllInstances(_ context.Context) ([]InstanceRecord, error) {
	return m.instances, nil
}

func (m *mockLister) UpdateAlive(_ context.Context, id string, alive bool) error {
	m.updates[id] = alive
	return nil
}

// mockNotifier implements LivenessNotifier for testing.
type mockNotifier struct {
	downs []string
	ups   []string
}

func (n *mockNotifier) NotifyBotDown(_ context.Context, _, name string) error {
	n.downs = append(n.downs, name)
	return nil
}

func (n *mockNotifier) NotifyBotUp(_ context.Context, _, name string) error {
	n.ups = append(n.ups, name)
	return nil
}

func TestLivenessCheckerAliveToDown(t *testing.T) {
	// Server that fails health checks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	// Parse host:port from srv.URL.
	// srv.URL is like "http://127.0.0.1:PORT"
	host, port := parseTestServerURL(t, srv.URL)

	lister := newMockLister([]InstanceRecord{
		{ID: "id1", Name: "bot1", Host: host, Port: port, ChannelID: "C1", WasAlive: true},
	})
	notifier := &mockNotifier{}
	cfg := LivenessCheckerConfig{
		Interval:       time.Hour, // won't tick
		Timeout:        500 * time.Millisecond,
		HealthPath:     "/health",
		DirectivesPath: "/directives",
	}
	lc := NewLivenessChecker(lister, notifier, cfg)
	lc.CheckOnce(context.Background())

	require.Equal(t, map[string]bool{"id1": false}, lister.updates)
	require.Equal(t, []string{"bot1"}, notifier.downs)
	require.Empty(t, notifier.ups)
}

func TestLivenessCheckerDeadToAlive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host, port := parseTestServerURL(t, srv.URL)

	lister := newMockLister([]InstanceRecord{
		{ID: "id2", Name: "bot2", Host: host, Port: port, ChannelID: "C2", WasAlive: false},
	})
	notifier := &mockNotifier{}
	cfg := LivenessCheckerConfig{
		Interval:       time.Hour,
		Timeout:        500 * time.Millisecond,
		HealthPath:     "/health",
		DirectivesPath: "/directives",
	}
	lc := NewLivenessChecker(lister, notifier, cfg)
	lc.CheckOnce(context.Background())

	require.Equal(t, map[string]bool{"id2": true}, lister.updates)
	require.Equal(t, []string{"bot2"}, notifier.ups)
	require.Empty(t, notifier.downs)
}

func TestLivenessCheckerNoChange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host, port := parseTestServerURL(t, srv.URL)

	// WasAlive=true and server is alive → no change.
	lister := newMockLister([]InstanceRecord{
		{ID: "id3", Name: "bot3", Host: host, Port: port, ChannelID: "C3", WasAlive: true},
	})
	notifier := &mockNotifier{}
	cfg := LivenessCheckerConfig{
		Interval:   time.Hour,
		Timeout:    500 * time.Millisecond,
		HealthPath: "/health",
	}
	lc := NewLivenessChecker(lister, notifier, cfg)
	lc.CheckOnce(context.Background())

	require.Empty(t, lister.updates)
	require.Empty(t, notifier.downs)
	require.Empty(t, notifier.ups)
}

func TestNewClient(t *testing.T) {
	c := NewClient("localhost", 8080, 10*time.Second, "/health", "/directives")
	require.Equal(t, "http://localhost:8080", c.baseURL)
	require.Equal(t, "/health", c.healthPath)
	require.Equal(t, "/directives", c.directivesPath)
}

func TestLivenessCheckerRun(t *testing.T) {
	// Run should poll on interval and stop when context is cancelled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host, port := parseTestServerURL(t, srv.URL)
	lister := newMockLister([]InstanceRecord{
		{ID: "id1", Name: "bot1", Host: host, Port: port, ChannelID: "C1", WasAlive: false},
	})
	notifier := &mockNotifier{}
	cfg := LivenessCheckerConfig{
		Interval:       10 * time.Millisecond, // fast tick
		Timeout:        500 * time.Millisecond,
		HealthPath:     "/health",
		DirectivesPath: "/directives",
	}
	lc := NewLivenessChecker(lister, notifier, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	lc.Run(ctx) // blocks until context expires
	// At least one check should have fired and flipped the bot to alive.
	require.NotEmpty(t, lister.updates)
}

func TestLivenessCheckerCheckAllListError(t *testing.T) {
	// When the lister returns an error, checkAll should silently skip.
	lister := &errorLister{}
	notifier := &mockNotifier{}
	cfg := LivenessCheckerConfig{
		Interval:   time.Hour,
		Timeout:    100 * time.Millisecond,
		HealthPath: "/health",
	}
	lc := NewLivenessChecker(lister, notifier, cfg)
	lc.CheckOnce(context.Background()) // should not panic
}

// errorLister always returns an error from ListAllInstances.
type errorLister struct{}

func (e *errorLister) ListAllInstances(_ context.Context) ([]InstanceRecord, error) {
	return nil, fmt.Errorf("db unavailable")
}

func (e *errorLister) UpdateAlive(_ context.Context, _ string, _ bool) error {
	return nil
}

// parseTestServerURL extracts host and port from a test server URL like "http://127.0.0.1:PORT".
func parseTestServerURL(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	host := u.Hostname()
	portStr := u.Port()
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

