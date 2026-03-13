# agenthub Architecture

## Overview

agenthub is a Go service that acts as a hub between Slack users and a fleet of openclaw AI agent instances. It is registered as a Slack app and uses Socket Mode for real-time event handling.

## Component Diagram

```
                    ┌─────────────────────────────────────┐
                    │           agenthub (Go)              │
                    │                                     │
  Slack ────────────│── slack/    api/ ────────────────── │── Browser (Admin UI)
  (Socket Mode)     │     │        │                      │
                    │     ▼        ▼                      │
                    │   openai/  auth/                    │
                    │     │                               │
                    │     ▼                               │
                    │   openclaw/  beads/  dolt/          │
                    └──────┬──────────┬──────┬────────────┘
                           │          │      │
                           ▼          ▼      ▼
                    openclaw    Beads/Dolt  Dolt SQL
                    instances   (.beads/)   server
                    (HTTP API)  (local)    (bot registry)
```

## Subsystems

### Slack Integration (`src/internal/slack/`)
- Uses `github.com/slack-go/slack` with Socket Mode
- Handles slash commands: `/agenthub bind`, `/agenthub <task>`, `/agenthub list`, `/agenthub remove`
- Routes `app_mention` and DM events to OpenAI for intelligent responses
- Both Slack Bot Token (`xoxb-`) and App Token (`xapp-`) required

### Openclaw Client (`src/internal/openclaw/`)
- HTTP client for talking to registered openclaw instances
- Enforces the openclaw API contract: `GET /health`, `POST /directives`
- Background `LivenessChecker` goroutine polls all instances at configurable interval
- On state change (alive↔dead), notifies the binding Slack channel

### OpenAI (`src/internal/openai/`)
- Powers agenthub's own intelligence for Slack conversations
- Used for: understanding user intent, formatting bot status responses, routing decisions
- Model and system prompt are configurable in `config.yaml`
- API key stored in encrypted store, never in config.yaml

### Beads + Kanban (`src/internal/beads/`, `src/internal/kanban/`)
- Uses `github.com/steveyegge/beads` library (embedded Dolt, CGO required)
- All work items created via Slack commands or the web UI are Beads issues
- Kanban board groups issues by status into configurable columns

### Dolt DB (`src/internal/dolt/`)
- agenthub's own schema (bot registry) stored in a Dolt SQL server
- MySQL-compatible connection via `go-sql-driver/mysql`
- Schema managed via migration files in `src/internal/dolt/migrations/`

### Encrypted Store (`src/internal/store/`)
- Stores all secrets: OpenAI API key, Slack tokens, admin password hash, session secret
- AES-256-GCM encryption; key derived from admin password via Argon2id
- On-disk at `~/.agenthub/secrets.enc` (path configurable in `config.yaml`)
- Never stored in the repository; never in `config.yaml`

### Auth + Web UI (`src/internal/auth/`, `src/internal/api/`)
- Admin web UI served on the configured HTTP address
- Session-based auth with bcrypt password verification
- Go HTML templates + HTMX for dynamic UI without a JavaScript build step
- Template and static assets embedded in the binary via `//go:embed`

## Data Flow: Bot Registration

```
User → Slack: /agenthub bind 1.2.3.4:8080 mybot
  → slack.Handler receives SlashCommand
  → openclaw.Client.Health("1.2.3.4", 8080) → GET http://1.2.3.4:8080/health
  → OK → dolt.DB.CreateInstance(name="mybot", host="1.2.3.4", port=8080, ...)
  → openclaw.Client.SetMentionOnly() → POST /directives {"mention_only": true}
  → slack.Client.PostMessage("✓ Bot mybot bound.")
```

## Data Flow: Task Creation

```
User → Slack: /agenthub fix the login bug @mybot
  → slack.Handler receives SlashCommand
  → beads.Client.CreateTask("fix the login bug")
  → beads.Client.AssignTask(issueID, "mybot")
  → slack.Client.PostMessage("Task bd-a1b2 created, assigned to mybot.")
```

## Deployment Dependencies

- **Dolt SQL server**: For agenthub's own bot registry schema. Run `dolt sql-server` in the agenthub data directory.
- **ICU4C** (macOS/Linux): Required for Dolt's embedded regex engine (via beads). Install with `brew install icu4c`.
- **Go 1.25.8+** with CGO enabled.
- **Slack App**: Registered at api.slack.com with Socket Mode enabled and required scopes granted.

See `docs/deployment.md` for full setup instructions.
