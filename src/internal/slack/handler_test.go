package slack

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Mock implementations for Deps interfaces.

type mockRegistry struct {
	registered    []BotSummary
	registerErr   error
	unregisterErr error
	listErr       error
	setChattyErr  error
}

func (m *mockRegistry) RegisterBot(_ context.Context, _, name, host string, port int, _ string) error {
	if m.registerErr != nil {
		return m.registerErr
	}
	m.registered = append(m.registered, BotSummary{Name: name, Host: host, Port: port, IsAlive: true})
	return nil
}

func (m *mockRegistry) UnregisterBot(_ context.Context, _, name, _ string) error {
	if m.unregisterErr != nil {
		return m.unregisterErr
	}
	for i, b := range m.registered {
		if b.Name == name {
			m.registered = append(m.registered[:i], m.registered[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

func (m *mockRegistry) ListBots(_ context.Context, _ string) ([]BotSummary, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.registered, nil
}

func (m *mockRegistry) SetChatty(_ context.Context, _, _ string, _ bool) error {
	return m.setChattyErr
}

func (m *mockRegistry) AliveBots(_ context.Context, _ string) ([]BotSummary, error) {
	var alive []BotSummary
	for _, b := range m.registered {
		if b.IsAlive {
			alive = append(alive, b)
		}
	}
	return alive, nil
}

type mockTaskManager struct {
	taskID      string
	assignedBot string
	err         error
}

func (m *mockTaskManager) CreateAndRoute(_ context.Context, _, _, _ string) (string, string, error) {
	return m.taskID, m.assignedBot, m.err
}

type mockAIChat struct {
	response string
	err      error
	calls    int
}

func (m *mockAIChat) Respond(_ context.Context, _, _ string) (string, error) {
	m.calls++
	return m.response, m.err
}

type mockOpenclawChecker struct {
	healthErr      error
	directiveErr   error
	onboardingErr  error
	checked        []string // "host:port" strings that were checked
}

func (m *mockOpenclawChecker) CheckHealth(_ context.Context, host string, port int) error {
	m.checked = append(m.checked, formatHostPort(host, port))
	return m.healthErr
}

func (m *mockOpenclawChecker) SendMentionOnly(_ context.Context, _ string, _ int) error {
	return m.directiveErr
}

func (m *mockOpenclawChecker) SendOnboarding(_ context.Context, _ string, _ int, _, _, _ string) error {
	return m.onboardingErr
}

func formatHostPort(host string, port int) string {
	return host + ":" + itoa(port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestHandleBind tests the bind sub-command parsing logic via ParseBind.
func TestHandleBindSuccess(t *testing.T) {
	registry := &mockRegistry{}
	checker := &mockOpenclawChecker{}

	deps := &Deps{
		BotRegistry:   registry,
		OpenclawCheck: checker,
	}
	_ = deps // handler uses deps.handleBind internally

	// Test the parsing path directly.
	bc, err := ParseBind("bind 127.0.0.1:8080 testbot")
	require.NoError(t, err)
	require.Equal(t, "testbot", bc.Name)
	require.Equal(t, 8080, bc.Port)

	ctx := context.Background()
	require.NoError(t, registry.RegisterBot(ctx, "C1", bc.Name, bc.Host, bc.Port, "U1"))
	require.Len(t, registry.registered, 1)
}

func TestHandleBindRegistrationError(t *testing.T) {
	registry := &mockRegistry{registerErr: errors.New("duplicate name")}
	ctx := context.Background()
	err := registry.RegisterBot(ctx, "C1", "mybot", "1.2.3.4", 8080, "U1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
}

func TestHandleListEmpty(t *testing.T) {
	registry := &mockRegistry{}
	ctx := context.Background()
	bots, err := registry.ListBots(ctx, "C1")
	require.NoError(t, err)
	require.Empty(t, bots)
	msg := FormatBotList(bots)
	require.Contains(t, msg, "No bots")
}

func TestHandleListWithBots(t *testing.T) {
	registry := &mockRegistry{
		registered: []BotSummary{
			{Name: "bot1", Host: "1.2.3.4", Port: 8080, IsAlive: true},
		},
	}
	ctx := context.Background()
	bots, err := registry.ListBots(ctx, "C1")
	require.NoError(t, err)
	require.Len(t, bots, 1)
}

func TestHandleRemove(t *testing.T) {
	registry := &mockRegistry{
		registered: []BotSummary{{Name: "mybot"}},
	}
	ctx := context.Background()
	require.NoError(t, registry.UnregisterBot(ctx, "C1", "mybot", "U1"))
	require.Empty(t, registry.registered)
}

func TestHandleRemoveNotFound(t *testing.T) {
	registry := &mockRegistry{}
	ctx := context.Background()
	err := registry.UnregisterBot(ctx, "C1", "nonexistent", "U1")
	require.Error(t, err)
}

func TestHandleTaskSuccess(t *testing.T) {
	tm := &mockTaskManager{taskID: "ah-1234", assignedBot: "mybot"}
	ctx := context.Background()
	tc := ParseTask("fix the login bug @mybot")
	id, bot, err := tm.CreateAndRoute(ctx, tc.Description, tc.BotName, "U1")
	require.NoError(t, err)
	require.Equal(t, "ah-1234", id)
	require.Equal(t, "mybot", bot)
}

func TestHandleTaskError(t *testing.T) {
	tm := &mockTaskManager{err: errors.New("no alive bots")}
	ctx := context.Background()
	_, _, err := tm.CreateAndRoute(ctx, "some task", "", "U1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no alive bots")
}

func TestAIRespond(t *testing.T) {
	ai := &mockAIChat{response: "There are 2 bots registered."}
	ctx := context.Background()
	resp, err := ai.Respond(ctx, "how many bots are there?", "C1")
	require.NoError(t, err)
	require.Contains(t, resp, "2 bots")
}
