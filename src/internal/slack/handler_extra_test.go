package slack

import (
	"context"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/require"
)

func TestNewHandler(t *testing.T) {
	deps := &Deps{
		BotRegistry:   &mockRegistry{},
		OpenclawCheck: &mockOpenclawChecker{},
		TaskManager:   &mockTaskManager{taskID: "x", assignedBot: "y"},
		AIChat:        &mockAIChat{response: "ok"},
	}
	h := NewHandler("xoxb-test-bot-token", "xapp-test-app-token", deps)
	require.NotNil(t, h)
	require.NotNil(t, h.client)
	require.NotNil(t, h.socketMode)
	require.Equal(t, deps, h.deps)
}

func TestHandleEventConnecting(t *testing.T) {
	h := &Handler{
		client: slack.New("test-token"),
		socketMode: socketmode.New(slack.New("test-token",
			slack.OptionAppLevelToken("xapp-test-app-token"),
		)),
		deps: &Deps{
			BotRegistry:   &mockRegistry{},
			OpenclawCheck: &mockOpenclawChecker{},
			TaskManager:   &mockTaskManager{},
			AIChat:        &mockAIChat{},
		},
	}
	// EventTypeConnecting only logs; no socketMode methods are called.
	h.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeConnecting,
	})
}

func TestHandleEventConnected(t *testing.T) {
	h := &Handler{
		client: slack.New("test-token"),
		socketMode: socketmode.New(slack.New("test-token",
			slack.OptionAppLevelToken("xapp-test"),
		)),
		deps: &Deps{
			BotRegistry:   &mockRegistry{},
			OpenclawCheck: &mockOpenclawChecker{},
			TaskManager:   &mockTaskManager{},
			AIChat:        &mockAIChat{},
		},
	}
	h.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeConnected,
	})
}

func TestHandleEventConnectionError(t *testing.T) {
	h := &Handler{
		client: slack.New("test-token"),
		socketMode: socketmode.New(slack.New("test-token",
			slack.OptionAppLevelToken("xapp-test"),
		)),
		deps: &Deps{
			BotRegistry:   &mockRegistry{},
			OpenclawCheck: &mockOpenclawChecker{},
			TaskManager:   &mockTaskManager{},
			AIChat:        &mockAIChat{},
		},
	}
	h.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeConnectionError,
		Data: "connection refused",
	})
}

func TestHandleEventSlashCommandWrongType(t *testing.T) {
	// When the event data is not a SlashCommand, handleEvent returns early.
	h := &Handler{
		client: slack.New("test-token"),
		socketMode: socketmode.New(slack.New("test-token",
			slack.OptionAppLevelToken("xapp-test"),
		)),
		deps: &Deps{
			BotRegistry:   &mockRegistry{},
			OpenclawCheck: &mockOpenclawChecker{},
			TaskManager:   &mockTaskManager{},
			AIChat:        &mockAIChat{},
		},
	}
	// Data is not a slack.SlashCommand → cast fails → returns early without calling Ack.
	h.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: "wrong type",
	})
}

func TestHandleEventEventsAPIWrongType(t *testing.T) {
	h := &Handler{
		client: slack.New("test-token"),
		socketMode: socketmode.New(slack.New("test-token",
			slack.OptionAppLevelToken("xapp-test"),
		)),
		deps: &Deps{
			BotRegistry:   &mockRegistry{},
			OpenclawCheck: &mockOpenclawChecker{},
			TaskManager:   &mockTaskManager{},
			AIChat:        &mockAIChat{},
		},
	}
	// Data is not a slackevents.EventsAPIEvent → cast fails → returns early.
	h.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: "wrong type",
	})
}
