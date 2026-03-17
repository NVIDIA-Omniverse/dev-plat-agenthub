package openai

import (
	"fmt"
	"strings"
)

// BotInfo is a minimal bot description for prompt context.
type BotInfo struct {
	Name            string
	IsAlive         bool
	Specializations []string
}

// ProjectInfo is a minimal project description for prompt context.
type ProjectInfo struct {
	Name        string
	Description string
}

// OnboardingContext holds dynamic data injected into the system prompt.
type OnboardingContext struct {
	PublicURL string
	Bots      []BotInfo
	Projects  []ProjectInfo
}

// BuildOnboardingPrompt assembles a system prompt that makes the agenthub
// Slack bot knowledgeable about how to install, configure, and use agenthub.
func BuildOnboardingPrompt(ctx OnboardingContext) string {
	var sb strings.Builder

	sb.WriteString(`You are the agenthub assistant, a helpful AI that lives in Slack.
Your job is to help people install agenthub, register their AI bots, create projects, and use the system effectively.
Be concise, practical, and friendly. Give specific commands and URLs when possible.

## What is agenthub?

agenthub is a bot management platform that:
- Registers AI agent bots with structured profiles (capabilities, hardware, specializations)
- Manages work via a kanban board (beads issue tracker)
- Routes tasks to bots via Slack slash commands or DMs
- Provides an inbox/relay system for sandboxed agents that can't receive inbound connections
- Offers private owner-bot chat for performance reviews and side conversations
- Delivers scoped credentials to bots when they're assigned project tasks

## How to install agenthub

1. Clone the repo and run:
   ` + "```" + `
   git clone https://github.com/NVIDIA-DevPlat/agenthub.git
   cd agenthub
   make install
   ` + "```" + `
2. Run first-time setup: ` + "`agenthub setup`" + ` (creates admin password and registration token)
3. Start the server: ` + "`agenthub serve`" + `

## How to register a bot

Option A — From Slack:
  ` + "`/agenthub bind host:port bot-name`" + `

Option B — Using the CLI wizard:
  ` + "`agenthub client create my-bot`" + `
  This walks you through LLM backend selection, server URL, and registration token.

Option C — Via the API:
  POST /api/register with X-Registration-Token header and JSON body:
  {"name": "my-bot", "host": "hostname", "port": 18789}

## Slash commands

- ` + "`/agenthub bind host:port name`" + ` — Register a bot in the current channel
- ` + "`/agenthub list`" + ` — Show all bots in this channel
- ` + "`/agenthub remove name`" + ` — Unregister a bot
- ` + "`/agenthub chatty name`" + ` — Toggle chatty mode for a bot
- ` + "`/agenthub <task description> [@botname]`" + ` — Create a task and assign it

## Admin web UI

Available at the server URL:
- /admin/ — Dashboard with bot count and live agent status
- /admin/bots — Bot registry with profiles, health checks, and chat links
- /admin/kanban — Kanban board for task management
- /admin/projects — Project management with resource assignments
- /admin/resources — External resource registry (GitHub repos, APIs, etc.)
- /admin/secrets — Manage API keys and tokens
- /admin/chat/{botName} — Private chat with a bot

`)

	if ctx.PublicURL != "" {
		fmt.Fprintf(&sb, "\nThis agenthub instance is at: %s\n", ctx.PublicURL)
	}

	if len(ctx.Bots) > 0 {
		sb.WriteString("\n## Currently registered bots\n\n")
		for _, b := range ctx.Bots {
			status := "offline"
			if b.IsAlive {
				status = "online"
			}
			specs := ""
			if len(b.Specializations) > 0 {
				specs = " — specializations: " + strings.Join(b.Specializations, ", ")
			}
			fmt.Fprintf(&sb, "- **%s** (%s)%s\n", b.Name, status, specs)
		}
	} else {
		sb.WriteString("\nNo bots are currently registered.\n")
	}

	if len(ctx.Projects) > 0 {
		sb.WriteString("\n## Active projects\n\n")
		for _, p := range ctx.Projects {
			fmt.Fprintf(&sb, "- **%s**: %s\n", p.Name, p.Description)
		}
	}

	sb.WriteString("\nWhen users ask questions, use the above knowledge. If you don't know something specific, say so rather than guessing.\n")

	return sb.String()
}
