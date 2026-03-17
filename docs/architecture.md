# agenthub Architecture

## Overview

agenthub is a Go service that acts as a hub between Slack users and a fleet of openclaw AI agent instances. It is registered as a Slack app and uses Socket Mode for real-time event handling.

## Component Diagram

```
                    ┌──────────────────────────────────────────┐
                    │              agenthub (Go)                │
                    │                                          │
  Slack ────────────│── slack/    api/ ──────────────────────── │── Browser (Admin UI)
  (Socket Mode)     │     │        │                           │
                    │     ▼        ▼                           │
                    │   openai/  auth/                         │
                    │     │      settings/                     │
                    │     ▼                                    │
                    │   openclaw/  beads/  dolt/               │
                    └──────┬──────────┬──────┬─────────────────┘
                           │          │      │
                           ▼          ▼      ▼
                    openclaw    Beads/Dolt  Dolt SQL
                    instances   (.beads/)   server
                    (HTTP API)  (local)    (registry + settings + inbox)
```

## Subsystems

### Slack Integration (`src/internal/slack/`)
- Uses `github.com/slack-go/slack` with Socket Mode
- Handles slash commands: `/agenthub bind`, `/agenthub <task>`, `/agenthub list`, `/agenthub remove`
- Routes `app_mention` and DM events to OpenAI for intelligent responses
- Routes messages in per-agent channels (`#agent-<name>`) directly to the agent's inbox
- Both Slack Bot Token (`xoxb-`) and App Token (`xapp-`) required

### Openclaw Client (`src/internal/openclaw/`)
- HTTP client for talking to registered openclaw instances
- Enforces the openclaw API contract: `GET /health`, `POST /directives`
- Background `LivenessChecker` goroutine polls all instances at configurable interval
- On state change (alive↔dead), logs the transition

### OpenAI (`src/internal/openai/`)
- Powers agenthub's own intelligence for Slack conversations
- Reactive client: rebuilt only when relevant settings change (no restart required)
- Model, system prompt, and API key are all live-updatable via `PUT /api/settings/{key}`
- API key stored encrypted in Dolt settings, never in config.yaml

### Beads + Kanban (`src/internal/beads/`, `src/internal/kanban/`)
- Uses `github.com/steveyegge/beads` library (embedded Dolt, CGO required)
- All work items created via Slack commands or the web UI are Beads issues
- Kanban board groups issues by status into configurable columns

### Dolt DB (`src/internal/dolt/`)
- agenthub's own schema stored in a Dolt SQL server (MySQL-compatible)
- Manages: bot registry (`openclaw_instances`), inbox (`inbox_messages`), settings, projects, task assignments, `bot_profiles`, `chat_messages`, `usage_log`
- Schema managed via sequential migration files embedded in the binary
- `DoltPersister` implements the `settings.Persister` interface with AES-256-GCM per-row encryption

### Settings (`src/internal/settings/`)
- Reactive write-through cache over `DoltPersister`
- `settings.Store.Get(key)` — O(1) in-memory read
- `settings.Store.Watch(key, fn)` — callback fires on any write (used by reactive OpenAI client)
- `settings.Store.Seed(key, value)` — sets a default only if key is not yet stored
- On startup, YAML config defaults are seeded; secrets come from Dolt

### Encrypted Settings Store (`src/internal/dolt/settings_store.go`)
- All secrets stored in the Dolt `settings` table with per-row AES-256-GCM encryption
- Encryption key derived from admin password via Argon2id (salt stored unencrypted in same table)
- Auto-migrates from legacy `secrets.enc` file on first serve run if `store.path` is configured
- `PUT /api/settings/{key}` writes through the reactive cache, taking effect immediately

### Auth + Web UI (`src/internal/auth/`, `src/internal/api/`)
- Admin web UI served on the configured HTTP address
- Session-based auth with bcrypt password verification
- First-run detection: if `admin_password_hash` is absent from Dolt settings, server enters setup mode
- Go HTML templates + HTMX for dynamic UI without a JavaScript build step
- Templates and static assets embedded in the binary via `//go:embed`

### Agent Inbox (`src/internal/api/inbox.go`)
- Per-agent message queue backed by the Dolt `inbox_messages` table
- Agents poll `GET /api/inbox?bot_name=<name>` for pending messages
- Sources: Slack DMs with `@botname` prefix, per-agent Slack channel messages
- Messages include originating Slack user ID and channel for reply routing
- `POST /api/inbox/{id}/reply` posts agent replies back to the Slack thread

### Bot Capability Profiles (`src/internal/dolt/profiles.go`)
- Structured identity for each bot: description, specializations, tools, hardware, max concurrent tasks
- CRUD via `bot_profiles` Dolt table
- Shown in Slack `/agenthub list` output and admin bots page
- Profile can be set during registration or via `PUT /api/bots/{name}/profile`

### Owner-Bot Web Chat (`src/internal/api/chat_handler.go`)
- Private chat between admin and individual bots at `/admin/chat/{botName}`
- Messages stored in `chat_messages` Dolt table
- Real-time updates via SSE
- Bot client detects `owner:chat` inbox messages and replies through the chat API

### Onboarding Agent (`src/internal/openai/system_prompts.go`)
- Dynamic system prompt built before each Slack LLM call
- Injects live bot registry and project data so the assistant can answer agenthub-specific questions
- Covers: installation, bot registration, slash commands, admin UI, project management

### Credential Delivery Pipeline (`src/internal/api/credentials_handler.go`)
- Task assignment creates a `TaskAssignment` record with a credential URL
- Agent fetches credentials via `GET /api/credentials/{botName}` — scoped to active assignments only
- Task close auto-revokes the assignment, cutting off credential access

### Model Tiering (`src/internal/api/llm_handler.go`)
- `POST /api/llm/escalate` proxies requests to a configured stronger model
- Usage logging in `usage_log` Dolt table (bot, tier, model, tokens, latency)
- Agent client has a `shouldEscalate` heuristic that detects uncertainty in the default model's reply
- Admin can view usage summaries via `GET /api/usage`

## Data Flow: Agent Registration

```
Client → POST /api/register  (X-Registration-Token: <token>)
  → api.Server.handleRegister
  → dolt.DB.ListAllInstances → name uniqueness check
  → openclaw.Prober.Probe(host, port)  (unless skip_probe=1)
  → dolt.DB.CreateInstance(name, host, port, ...)
  → api.Server.createSlackChannel("agent-" + name)
      → Slack API: conversations.create  → channel ID
  → dolt.DB.UpdateAgentSlackChannel(name, channelID)
  → BotAnnouncer.PostMessage(defaultChannel, "New agent … post in #agent-<name>")
  → 201 Created  {"id": "...", "name": "..."}
```

## Data Flow: Per-Agent Channel Message

```
User → Slack: posts "do the thing" in #agent-mybot
  → slack.Handler receives message event
  → deps.AgentChannelLookup.AgentBySlackChannel(channelID) → "mybot"
  → deps.Inbox.Enqueue("mybot", userID, channelID, "do the thing")
  → agent polls GET /api/inbox?bot_name=mybot → receives message
  → agent replies → POST /api/inbox/{id}/reply → Slack thread reply
```

## Data Flow: Task Creation via DM

```
User → Slack DM: "@mybot fix the login bug"
  → slack.Handler receives DM message event
  → parseAgentPrefix → botName="mybot", text="fix the login bug"
  → TaskManager.CreateAndRoute("fix the login bug", "mybot", userID)
      → beads.CreateTask + beads.AssignTask
  → Inbox.Enqueue("mybot", userID, dmChannelID, "fix the login bug")
  → PostMessage(dmChannel, "Task AH-abc created, assigned to mybot.")
```

## Deployment Dependencies

- **Dolt SQL server**: For agenthub's bot registry, settings, and inbox. Run `dolt sql-server` in the agenthub data directory.
- **ICU4C** (macOS/Linux): Required for Dolt's embedded regex engine (via beads). Install with `brew install icu4c`.
- **Go 1.25.8+** with CGO enabled.
- **Slack App**: Registered at api.slack.com with Socket Mode enabled, required scopes granted (including `channels:manage` for per-agent channel creation).

See `docs/deployment.md` for full setup instructions.
