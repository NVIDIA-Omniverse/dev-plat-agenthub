// Package slack handles the agenthub Slack app integration using Socket Mode.
//
// Two token types are required (stored in the encrypted store):
//   - Bot Token (xoxb-): chat:write, commands, app_mentions:read, im:history, channels:read
//   - App Token (xapp-): connections:write (Socket Mode)
package slack

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var validBotName = regexp.MustCompile(`^[a-z0-9-]+$`)

// BindCommand holds the parsed fields from `/agenthub bind host:port name`.
type BindCommand struct {
	Host string
	Port int
	Name string
}

// TaskCommand holds the parsed fields from `/agenthub <description> [@botname]`.
type TaskCommand struct {
	Description string
	BotName     string // empty means "any alive bot"
}

// ParseBind parses the text from a `/agenthub bind` slash command.
// Expected format: "bind host:port unique-name"
func ParseBind(text string) (*BindCommand, error) {
	parts := strings.Fields(text)
	if len(parts) < 3 || parts[0] != "bind" {
		return nil, fmt.Errorf("usage: /agenthub bind host:port unique-name")
	}
	hostPort := parts[1]
	name := parts[2]

	if !validBotName.MatchString(name) {
		return nil, fmt.Errorf("unique-name must match [a-z0-9-]+, got %q", name)
	}

	host, port, err := parseHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("invalid host:port %q: %w", hostPort, err)
	}

	return &BindCommand{Host: host, Port: port, Name: name}, nil
}

// ParseTask parses a task creation command: `/agenthub <description> [@botname]`
// The optional @botname at the end routes the task to a specific bot.
func ParseTask(text string) *TaskCommand {
	text = strings.TrimSpace(text)
	if text == "" {
		return &TaskCommand{}
	}

	// Check if the last word is a @mention.
	words := strings.Fields(text)
	last := words[len(words)-1]
	if strings.HasPrefix(last, "@") {
		botName := strings.TrimPrefix(last, "@")
		desc := strings.TrimSpace(strings.Join(words[:len(words)-1], " "))
		return &TaskCommand{Description: desc, BotName: botName}
	}
	return &TaskCommand{Description: text}
}

// ParseCommand parses the full slash command text and returns the sub-command type.
// Returns "bind", "list", "remove", "chatty", or "task".
func ParseCommand(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "task"
	}
	first := strings.Fields(text)[0]
	switch first {
	case "bind", "list", "remove", "chatty":
		return first
	default:
		return "task"
	}
}

// parseHostPort splits "host:port" into its components.
func parseHostPort(hostPort string) (string, int, error) {
	idx := strings.LastIndex(hostPort, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("missing port")
	}
	host := hostPort[:idx]
	portStr := hostPort[idx+1:]
	if host == "" {
		return "", 0, fmt.Errorf("missing host")
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}

// FormatBotList formats a list of bots for a Slack message.
func FormatBotList(bots []BotSummary) string {
	if len(bots) == 0 {
		return "No bots registered in this channel."
	}
	var sb strings.Builder
	sb.WriteString("*Registered bots:*\n")
	for _, b := range bots {
		status := ":red_circle: offline"
		if b.IsAlive {
			status = ":large_green_circle: online"
		}
		chatty := ""
		if b.Chatty {
			chatty = " (chatty)"
		}
		fmt.Fprintf(&sb, "• *%s* — %s:%d — %s%s\n", b.Name, b.Host, b.Port, status, chatty)
	}
	return sb.String()
}

// BotSummary is a minimal view of a registered openclaw instance for Slack messages.
type BotSummary struct {
	Name    string
	Host    string
	Port    int
	IsAlive bool
	Chatty  bool
}

// Deps holds the dependencies injected into the Slack handler.
// All fields are interfaces to allow easy mocking in tests.
type Deps struct {
	BotRegistry   BotRegistry
	TaskManager   TaskManager
	AIChat        AIChatter
	OpenclawCheck OpenclawChecker
	Config        SlackConfig
}

// SlackConfig holds the Slack-specific config values needed by the handler.
type SlackConfig struct {
	CommandPrefix string
	ChannelID     string // agenthub's own DM/notification channel
}

// BotRegistry manages the openclaw instance database.
type BotRegistry interface {
	RegisterBot(ctx context.Context, channelID, name, host string, port int, ownerSlackUser string) error
	UnregisterBot(ctx context.Context, channelID, name, ownerSlackUser string) error
	ListBots(ctx context.Context, channelID string) ([]BotSummary, error)
	SetChatty(ctx context.Context, channelID, name string, chatty bool) error
	AliveBots(ctx context.Context, channelID string) ([]BotSummary, error)
}

// TaskManager creates and routes work items.
type TaskManager interface {
	CreateAndRoute(ctx context.Context, desc, botName, actor string) (taskID string, assignedBot string, err error)
}

// AIChatter handles natural language messages using OpenAI.
type AIChatter interface {
	Respond(ctx context.Context, userMessage string, channelID string) (string, error)
}

// OpenclawChecker verifies that a host:port is reachable.
type OpenclawChecker interface {
	CheckHealth(ctx context.Context, host string, port int) error
	SendMentionOnly(ctx context.Context, host string, port int) error
}
