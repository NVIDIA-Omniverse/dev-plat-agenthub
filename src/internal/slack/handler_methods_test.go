package slack

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/stretchr/testify/require"
)

// newHandlerForTest creates a Handler with a fake Slack API server for PostMessage calls.
func newHandlerForTest(t *testing.T, deps *Deps) *Handler {
	t.Helper()
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok": true}`)
	}))
	t.Cleanup(apiSrv.Close)
	client := slack.New("test-token", slack.OptionAPIURL(apiSrv.URL+"/"))
	return &Handler{client: client, deps: deps}
}

func newDeps(registry *mockRegistry, checker *mockOpenclawChecker, tm *mockTaskManager, ai *mockAIChat) *Deps {
	if tm == nil {
		tm = &mockTaskManager{taskID: "ah-1", assignedBot: "bot1"}
	}
	if ai == nil {
		ai = &mockAIChat{response: "ok"}
	}
	return &Deps{
		BotRegistry:   registry,
		OpenclawCheck: checker,
		TaskManager:   tm,
		AIChat:        ai,
	}
}

// --- handleBind ---

func TestHandlerBindSuccess(t *testing.T) {
	registry := &mockRegistry{}
	checker := &mockOpenclawChecker{}
	h := newHandlerForTest(t, newDeps(registry, checker, nil, nil))

	reply, err := h.handleBind(context.Background(), slack.SlashCommand{
		Text: "bind 127.0.0.1:8080 mybot", ChannelID: "C1", UserID: "U1",
	})
	require.NoError(t, err)
	require.Contains(t, reply, "mybot")
	require.Len(t, registry.registered, 1)
}

func TestHandlerBindParseError(t *testing.T) {
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, nil))
	_, err := h.handleBind(context.Background(), slack.SlashCommand{Text: "bind"})
	require.Error(t, err)
}

func TestHandlerBindHealthError(t *testing.T) {
	checker := &mockOpenclawChecker{healthErr: errors.New("connection refused")}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, checker, nil, nil))
	_, err := h.handleBind(context.Background(), slack.SlashCommand{
		Text: "bind 127.0.0.1:8080 mybot", ChannelID: "C1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot reach")
}

func TestHandlerBindRegisterError(t *testing.T) {
	checker := &mockOpenclawChecker{}
	registry := &mockRegistry{registerErr: errors.New("duplicate name")}
	h := newHandlerForTest(t, newDeps(registry, checker, nil, nil))
	_, err := h.handleBind(context.Background(), slack.SlashCommand{
		Text: "bind 127.0.0.1:8080 mybot", ChannelID: "C1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "registering bot")
}

func TestHandlerBindMentionOnlyWarning(t *testing.T) {
	// SendMentionOnly fails but bind still succeeds (only logs warning).
	checker := &mockOpenclawChecker{directiveErr: errors.New("directive failed")}
	registry := &mockRegistry{}
	h := newHandlerForTest(t, newDeps(registry, checker, nil, nil))
	reply, err := h.handleBind(context.Background(), slack.SlashCommand{
		Text: "bind 127.0.0.1:8080 mybot", ChannelID: "C1",
	})
	require.NoError(t, err)
	require.Contains(t, reply, "mybot")
}

// --- handleList ---

func TestHandlerListEmpty(t *testing.T) {
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, nil))
	reply, err := h.handleList(context.Background(), slack.SlashCommand{ChannelID: "C1"})
	require.NoError(t, err)
	require.Contains(t, reply, "No bots")
}

func TestHandlerListWithBots(t *testing.T) {
	registry := &mockRegistry{
		registered: []BotSummary{
			{Name: "bot1", Host: "1.2.3.4", Port: 8080, IsAlive: true},
		},
	}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	reply, err := h.handleList(context.Background(), slack.SlashCommand{ChannelID: "C1"})
	require.NoError(t, err)
	require.Contains(t, reply, "bot1")
}

func TestHandlerListError(t *testing.T) {
	registry := &mockRegistry{listErr: errors.New("db down")}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	_, err := h.handleList(context.Background(), slack.SlashCommand{ChannelID: "C1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing bots")
}

// --- handleRemove ---

func TestHandlerRemoveSuccess(t *testing.T) {
	registry := &mockRegistry{
		registered: []BotSummary{{Name: "mybot"}},
	}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	reply, err := h.handleRemove(context.Background(), slack.SlashCommand{
		Text: "remove mybot", ChannelID: "C1", UserID: "U1",
	})
	require.NoError(t, err)
	require.Contains(t, reply, "mybot")
	require.Empty(t, registry.registered)
}

func TestHandlerRemoveMissingArg(t *testing.T) {
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, nil))
	_, err := h.handleRemove(context.Background(), slack.SlashCommand{Text: "remove"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage")
}

func TestHandlerRemoveError(t *testing.T) {
	registry := &mockRegistry{unregisterErr: errors.New("not found")}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	_, err := h.handleRemove(context.Background(), slack.SlashCommand{
		Text: "remove ghostbot", ChannelID: "C1",
	})
	require.Error(t, err)
}

// --- handleChatty ---

func TestHandlerChattySuccess(t *testing.T) {
	registry := &mockRegistry{
		registered: []BotSummary{{Name: "mybot"}},
	}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	reply, err := h.handleChatty(context.Background(), slack.SlashCommand{
		Text: "chatty mybot", ChannelID: "C1",
	})
	require.NoError(t, err)
	require.Contains(t, reply, "mybot")
}

func TestHandlerChattyMissingArg(t *testing.T) {
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, nil))
	_, err := h.handleChatty(context.Background(), slack.SlashCommand{Text: "chatty"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage")
}

func TestHandlerChattySetChattyError(t *testing.T) {
	registry := &mockRegistry{setChattyErr: errors.New("db unavailable")}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	_, err := h.handleChatty(context.Background(), slack.SlashCommand{
		Text: "chatty mybot", ChannelID: "C1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "db unavailable")
}

// --- handleTask ---

func TestHandlerTaskSuccess(t *testing.T) {
	tm := &mockTaskManager{taskID: "ah-42", assignedBot: "mybot"}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, tm, nil))
	reply, err := h.handleTask(context.Background(), slack.SlashCommand{
		Text: "fix the bug @mybot", ChannelID: "C1", UserID: "U1",
	})
	require.NoError(t, err)
	require.Contains(t, reply, "ah-42")
	require.Contains(t, reply, "mybot")
}

func TestHandlerTaskEmptyDesc(t *testing.T) {
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, nil))
	reply, err := h.handleTask(context.Background(), slack.SlashCommand{Text: ""})
	require.NoError(t, err)
	require.Contains(t, reply, "Usage")
}

func TestHandlerTaskError(t *testing.T) {
	tm := &mockTaskManager{err: errors.New("no alive bots")}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, tm, nil))
	_, err := h.handleTask(context.Background(), slack.SlashCommand{
		Text: "do something", ChannelID: "C1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating task")
}

// --- handleSlashCommand ---

func TestHandleSlashCommandList(t *testing.T) {
	registry := &mockRegistry{
		registered: []BotSummary{{Name: "bot1", IsAlive: true}},
	}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	// handleSlashCommand calls PostMessage (fake server handles it).
	h.handleSlashCommand(context.Background(), slack.SlashCommand{
		Text: "list", ChannelID: "C1",
	})
}

func TestHandleSlashCommandBind(t *testing.T) {
	registry := &mockRegistry{}
	checker := &mockOpenclawChecker{}
	h := newHandlerForTest(t, newDeps(registry, checker, nil, nil))
	h.handleSlashCommand(context.Background(), slack.SlashCommand{
		Text: "bind 127.0.0.1:8080 testbot", ChannelID: "C1", UserID: "U1",
	})
	require.Len(t, registry.registered, 1)
}

func TestHandleSlashCommandError(t *testing.T) {
	// Registry list error → error reply sent to Slack.
	registry := &mockRegistry{listErr: errors.New("db down")}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	h.handleSlashCommand(context.Background(), slack.SlashCommand{
		Text: "list", ChannelID: "C1",
	})
}

func TestHandleSlashCommandTask(t *testing.T) {
	// Default case (non-bind/list/remove/chatty) → handleTask.
	tm := &mockTaskManager{taskID: "ah-99", assignedBot: "mybot"}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, tm, nil))
	h.handleSlashCommand(context.Background(), slack.SlashCommand{
		Text: "fix the login bug", ChannelID: "C1", UserID: "U1",
	})
}

func TestHandleSlashCommandRemove(t *testing.T) {
	registry := &mockRegistry{registered: []BotSummary{{Name: "bot1"}}}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	h.handleSlashCommand(context.Background(), slack.SlashCommand{
		Text: "remove bot1", ChannelID: "C1", UserID: "U1",
	})
}

func TestHandleSlashCommandChatty(t *testing.T) {
	registry := &mockRegistry{registered: []BotSummary{{Name: "bot1"}}}
	h := newHandlerForTest(t, newDeps(registry, &mockOpenclawChecker{}, nil, nil))
	h.handleSlashCommand(context.Background(), slack.SlashCommand{
		Text: "chatty bot1", ChannelID: "C1", UserID: "U1",
	})
}

// --- handleAPIEvent ---

func TestHandleAPIEventAppMention(t *testing.T) {
	ai := &mockAIChat{response: "There are 2 bots."}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, ai))

	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "app_mention",
			Data: &slackevents.AppMentionEvent{
				Text:      "<@UBOT> how many bots?",
				Channel:   "C1",
				TimeStamp: "1234.5678",
			},
		},
	})
	require.Equal(t, 1, ai.calls)
}

func TestHandleAPIEventAppMentionAIError(t *testing.T) {
	ai := &mockAIChat{err: errors.New("AI unavailable")}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, ai))
	// Should not panic even when AI fails.
	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "app_mention",
			Data: &slackevents.AppMentionEvent{Text: "<@UBOT> hi", Channel: "C1"},
		},
	})
}

func TestHandleAPIEventMessage(t *testing.T) {
	ai := &mockAIChat{response: "DM reply"}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, ai))
	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "message",
			Data: &slackevents.MessageEvent{
				Text:    "hello bot",
				Channel: "D1",
			},
		},
	})
	require.Equal(t, 1, ai.calls)
}

func TestHandleAPIEventMessageSubtype(t *testing.T) {
	// Messages with a subtype (e.g., bot_message) should be ignored.
	ai := &mockAIChat{response: "should not be called"}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, ai))
	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "message",
			Data: &slackevents.MessageEvent{
				Text:    "bot message",
				Channel: "D1",
				SubType: "bot_message",
			},
		},
	})
	require.Equal(t, 0, ai.calls)
}

func TestHandleAPIEventUnknownType(t *testing.T) {
	// Unknown inner event type should be silently ignored.
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, nil))
	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "reaction_added",
			Data: nil,
		},
	})
}

func TestHandleAPIEventAppMentionWrongType(t *testing.T) {
	// Inner data is wrong type for app_mention → type assertion fails → returns early.
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, &mockAIChat{}))
	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "app_mention",
			Data: "wrong type",
		},
	})
}

func TestHandleAPIEventMessageWrongType(t *testing.T) {
	// Inner data is wrong type for message → type assertion fails → returns early.
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, &mockAIChat{}))
	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "message",
			Data: "wrong type",
		},
	})
}

func TestHandleAPIEventMessageAIError(t *testing.T) {
	ai := &mockAIChat{err: errors.New("AI unavailable")}
	h := newHandlerForTest(t, newDeps(&mockRegistry{}, &mockOpenclawChecker{}, nil, ai))
	// Should not panic even when AI fails (errors are logged).
	h.handleAPIEvent(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "message",
			Data: &slackevents.MessageEvent{
				Text:    "hello",
				Channel: "D1",
			},
		},
	})
}

// --- stripMention, indexOf ---

func TestStripMentionWithMention(t *testing.T) {
	result := stripMention("<@UABC123> hello world")
	require.Equal(t, "hello world", result)
}

func TestStripMentionWithoutMention(t *testing.T) {
	result := stripMention("hello world")
	require.Equal(t, "hello world", result)
}

func TestStripMentionEmpty(t *testing.T) {
	result := stripMention("")
	require.Equal(t, "", result)
}

func TestIndexOf(t *testing.T) {
	require.Equal(t, 3, indexOf("foobar", "bar"))
	require.Equal(t, -1, indexOf("foobar", "baz"))
	require.Equal(t, 0, indexOf("foobar", ""))
}

func TestStripMentionNoClosingAngle(t *testing.T) {
	// Starts with < but no >, so indexBytePos returns -1.
	result := stripMention("<no-closing-bracket text")
	require.Equal(t, "<no-closing-bracket text", result)
}
