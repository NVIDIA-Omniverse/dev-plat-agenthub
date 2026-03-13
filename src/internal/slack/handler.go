package slack

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Handler receives events from Slack via Socket Mode and dispatches them.
type Handler struct {
	client     *slack.Client
	socketMode *socketmode.Client
	deps       *Deps
}

// NewHandler creates a Handler from the given bot token, app token, and deps.
func NewHandler(botToken, appToken string, deps *Deps) *Handler {
	client := slack.New(botToken,
		slack.OptionAppLevelToken(appToken),
	)
	socket := socketmode.New(client)
	return &Handler{
		client:     client,
		socketMode: socket,
		deps:       deps,
	}
}

// Run starts the Socket Mode event loop. Blocks until ctx is cancelled.
func (h *Handler) Run(ctx context.Context) error {
	go func() {
		for evt := range h.socketMode.Events {
			h.handleEvent(ctx, evt)
		}
	}()
	return h.socketMode.RunContext(ctx)
}

func (h *Handler) handleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeSlashCommand:
		cmd, ok := evt.Data.(slack.SlashCommand)
		if !ok {
			return
		}
		h.socketMode.Ack(*evt.Request)
		h.handleSlashCommand(ctx, cmd)

	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		h.socketMode.Ack(*evt.Request)
		h.handleAPIEvent(ctx, eventsAPIEvent)

	case socketmode.EventTypeConnecting:
		slog.Info("slack: connecting")
	case socketmode.EventTypeConnected:
		slog.Info("slack: connected")
	case socketmode.EventTypeConnectionError:
		slog.Error("slack: connection error", "error", evt.Data)
	}
}

func (h *Handler) handleSlashCommand(ctx context.Context, cmd slack.SlashCommand) {
	subCmd := ParseCommand(cmd.Text)
	var reply string
	var err error

	switch subCmd {
	case "bind":
		reply, err = h.handleBind(ctx, cmd)
	case "list":
		reply, err = h.handleList(ctx, cmd)
	case "remove":
		reply, err = h.handleRemove(ctx, cmd)
	case "chatty":
		reply, err = h.handleChatty(ctx, cmd)
	default:
		reply, err = h.handleTask(ctx, cmd)
	}

	if err != nil {
		reply = fmt.Sprintf(":x: Error: %s", err.Error())
	}

	if reply != "" {
		_, _, _ = h.client.PostMessage(cmd.ChannelID,
			slack.MsgOptionText(reply, false),
			slack.MsgOptionResponseURL(cmd.ResponseURL, slack.ResponseTypeInChannel),
		)
	}
}

func (h *Handler) handleBind(ctx context.Context, cmd slack.SlashCommand) (string, error) {
	bc, err := ParseBind(cmd.Text)
	if err != nil {
		return "", err
	}
	if err := h.deps.OpenclawCheck.CheckHealth(ctx, bc.Host, bc.Port); err != nil {
		return "", fmt.Errorf("cannot reach %s:%d — is openclaw running? (%s)", bc.Host, bc.Port, err)
	}
	if err := h.deps.BotRegistry.RegisterBot(ctx, cmd.ChannelID, bc.Name, bc.Host, bc.Port, cmd.UserID); err != nil {
		return "", fmt.Errorf("registering bot: %s", err)
	}
	// Send the full BOTJILE onboarding directive (includes mention-only + task policy).
	// If the agenthub URL or token aren't configured, fall back to mention-only only.
	if h.deps.Config.AgenthubURL != "" && h.deps.Config.RegistrationToken != "" {
		if err := h.deps.OpenclawCheck.SendOnboarding(ctx, bc.Host, bc.Port,
			h.deps.Config.AgenthubURL, h.deps.Config.RegistrationToken, bc.Name); err != nil {
			slog.Warn("slack: could not send onboarding directive", "bot", bc.Name, "error", err)
			// Fall back to mention-only so the bot is at least configured.
			if err2 := h.deps.OpenclawCheck.SendMentionOnly(ctx, bc.Host, bc.Port); err2 != nil {
				slog.Warn("slack: could not set mention-only", "host", bc.Host, "port", bc.Port, "error", err2)
			}
		}
	} else {
		if err := h.deps.OpenclawCheck.SendMentionOnly(ctx, bc.Host, bc.Port); err != nil {
			slog.Warn("slack: could not set mention-only", "host", bc.Host, "port", bc.Port, "error", err)
		}
	}
	return fmt.Sprintf(":white_check_mark: Bot *%s* bound to this channel and briefed on BOTJILE task policy.", bc.Name), nil
}

func (h *Handler) handleList(ctx context.Context, cmd slack.SlashCommand) (string, error) {
	bots, err := h.deps.BotRegistry.ListBots(ctx, cmd.ChannelID)
	if err != nil {
		return "", fmt.Errorf("listing bots: %s", err)
	}
	return FormatBotList(bots), nil
}

func (h *Handler) handleRemove(ctx context.Context, cmd slack.SlashCommand) (string, error) {
	parts := splitArgs(cmd.Text, 2) // "remove botname"
	if len(parts) < 2 {
		return "", fmt.Errorf("usage: /agenthub remove unique-name")
	}
	name := parts[1]
	if err := h.deps.BotRegistry.UnregisterBot(ctx, cmd.ChannelID, name, cmd.UserID); err != nil {
		return "", err
	}
	return fmt.Sprintf(":wastebasket: Bot *%s* removed.", name), nil
}

func (h *Handler) handleChatty(ctx context.Context, cmd slack.SlashCommand) (string, error) {
	parts := splitArgs(cmd.Text, 2) // "chatty botname"
	if len(parts) < 2 {
		return "", fmt.Errorf("usage: /agenthub chatty unique-name")
	}
	name := parts[1]
	if err := h.deps.BotRegistry.SetChatty(ctx, cmd.ChannelID, name, true); err != nil {
		return "", err
	}
	return fmt.Sprintf(":speech_balloon: Bot *%s* is now chatty.", name), nil
}

func (h *Handler) handleTask(ctx context.Context, cmd slack.SlashCommand) (string, error) {
	tc := ParseTask(cmd.Text)
	if tc.Description == "" {
		return "Usage: `/agenthub <task description> [@botname]`\n" +
			"Or: `/agenthub bind|list|remove|chatty`", nil
	}
	taskID, assignedBot, err := h.deps.TaskManager.CreateAndRoute(ctx, tc.Description, tc.BotName, cmd.UserID)
	if err != nil {
		return "", fmt.Errorf("creating task: %s", err)
	}
	return fmt.Sprintf(":white_check_mark: Task `%s` created and assigned to *%s*.", taskID, assignedBot), nil
}

func (h *Handler) handleAPIEvent(ctx context.Context, event slackevents.EventsAPIEvent) {
	switch event.InnerEvent.Type {
	case "app_mention":
		ev, ok := event.InnerEvent.Data.(*slackevents.AppMentionEvent)
		if !ok {
			return
		}
		text := stripMention(ev.Text)
		response, err := h.deps.AIChat.Respond(ctx, text, ev.Channel)
		if err != nil {
			slog.Error("slack: AI error", "error", err)
			return
		}
		_, _, _ = h.client.PostMessage(ev.Channel,
			slack.MsgOptionText(response, false),
			slack.MsgOptionTS(ev.TimeStamp),
		)

	case "message":
		ev, ok := event.InnerEvent.Data.(*slackevents.MessageEvent)
		if !ok || ev.SubType != "" {
			return
		}
		// BOTJILE: every DM to agenthub that looks like work gets a bead.
		// Create the task first so it's on the board before we even respond.
		var taskRef string
		if h.deps.TaskManager != nil && ev.Text != "" {
			taskID, assignedBot, err := h.deps.TaskManager.CreateAndRoute(ctx, ev.Text, "", ev.User)
			if err != nil {
				slog.Warn("slack: could not create task from DM", "error", err)
			} else {
				taskRef = fmt.Sprintf(":white_check_mark: Task `%s` created", taskID)
				if assignedBot != "" {
					taskRef += fmt.Sprintf(" and routed to *%s*", assignedBot)
				}
				taskRef += ".\n"
			}
		}
		response, err := h.deps.AIChat.Respond(ctx, ev.Text, ev.Channel)
		if err != nil {
			slog.Error("slack: AI error in DM", "error", err)
			return
		}
		_, _, _ = h.client.PostMessage(ev.Channel,
			slack.MsgOptionText(taskRef+response, false),
		)
	}
}

// stripMention removes the leading <@USERID> mention from a message.
func stripMention(text string) string {
	if len(text) > 0 && text[0] == '<' {
		if end := indexBytePos(text, '>'); end >= 0 {
			return trimSpace(text[end+1:])
		}
	}
	return text
}

func indexBytePos(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// indexOf is kept for string-based searches.
func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

func splitArgs(text string, n int) []string {
	var parts []string
	remaining := text
	for len(parts) < n-1 {
		idx := indexByte(remaining, ' ')
		if idx < 0 {
			break
		}
		parts = append(parts, remaining[:idx])
		remaining = trimSpace(remaining[idx+1:])
	}
	if remaining != "" {
		parts = append(parts, remaining)
	}
	return parts
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
