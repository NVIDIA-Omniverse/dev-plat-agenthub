# agenthub

**agenthub** is a Go service that acts as a hub between a Slack workspace and a fleet of [openclaw](https://github.com/NVIDIA-DevPlat/openclaw) AI agent instances. It handles bot registration, liveness monitoring, work item routing, and provides an admin web UI for managing the full agent ecosystem.

## Features

### Bot Registry
- Register openclaw AI agent instances by Slack channel using `/agenthub bind host:port name`
- Each bot is bound to the channel where it was registered; only the binding user can remove it
- Remove bots with `/agenthub remove <name>`
- List all bots in the current channel with alive/dead status via `/agenthub list`

### Liveness Monitoring
- Background liveness checker polls all registered bots on a configurable interval (default 60s)
- Calls each bot's `GET /health` endpoint; marks it alive or dead based on the response
- State-change transitions (alive→dead, dead→alive) are logged via `slog`

### Work Item Routing
- Create Beads issues directly from Slack: `/agenthub <task description> [@botname]`
- Optionally route to a specific bot by name, or let agenthub assign to any alive bot
- All work items are tracked as [Beads](https://github.com/steveyegge/beads) issues in an embedded Dolt database

### Kanban Board
- Admin web UI includes a kanban board backed by Beads issues
- Columns are configurable in `config.yaml` (default: backlog → ready → in_progress → review → done)
- Falls back to an empty board if Beads is unavailable

### Slack Integration
- Uses Socket Mode — no public HTTP endpoint required
- Handles slash commands, `app_mention` events, and DMs
- DM the bot directly to check status, create work items, or ask questions in natural language

### OpenAI-Powered Intelligence
- Uses OpenAI (default: `gpt-4o-mini`) to understand natural language Slack messages
- Powers responses to direct mentions and DMs
- Model, max tokens, and system prompt are all configurable

### Bot Directives
- On binding, sets `mention_only: true` on the bot via `POST /directives`
- Toggle chatty mode (responds to all messages, not just @mentions): `/agenthub chatty <name>`

### Admin Web UI
- Session-based admin UI served on the configured HTTP address
- Dashboard with bot status overview
- Bot list with per-bot remove and liveness check actions
- Kanban board for work item tracking
- Secrets manager: store OpenAI API key, Slack tokens — encrypted at rest, never in config files
- All static assets and templates embedded in the binary

### Encrypted Secrets Store
- All secrets (API keys, tokens, password hash, session key) stored in an AES-256-GCM encrypted file
- Encryption key derived from the admin password via Argon2id
- Store path is configurable; defaults to `~/.agenthub/secrets.enc`
- Secrets are never written to `config.yaml` or environment variables

### Security
- Admin password hashed with bcrypt
- Random 32-byte session signing secret generated at setup time
- Encrypted store unlocked at startup by prompting for the admin password (echo-suppressed on TTY)
- Session cookie with configurable name

---

## Prerequisites

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | 1.25.8+ | CGO must be enabled |
| ICU4C | any recent | Required by Dolt's embedded regex engine |
| Dolt SQL server | latest | For agenthub's bot registry schema |
| Slack App | — | Socket Mode + required scopes |

### Install ICU4C

```bash
# macOS
brew install icu4c

# Ubuntu / Debian
sudo apt-get install libicu-dev
```

### Install and start Dolt

```bash
# macOS
brew install dolt

# Linux
curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash

# Initialize and start the SQL server
mkdir -p ~/.agenthub/dolt && cd ~/.agenthub/dolt
dolt init
dolt sql-server --host=127.0.0.1 --port=3306
```

---

## Building

```bash
git clone https://github.com/NVIDIA-DevPlat/agenthub
cd agenthub
make build
# Binary: ./agenthub
```

The Makefile automatically detects the Homebrew ICU4C prefix and sets the required `CGO_CFLAGS` / `CGO_LDFLAGS`.

---

## Setup (first run)

```bash
./agenthub setup
# or: make setup
```

You will be prompted to choose an admin password. This will:

1. Derive an AES-256-GCM encryption key from the password (Argon2id)
2. Generate a random session signing secret
3. Hash the password with bcrypt
4. Write `~/.agenthub/secrets.enc`

---

## Configuration

All tunable behavior lives in `config.yaml`. No secrets belong here.

```bash
cp config.yaml /etc/agenthub/config.yaml
export AGENTHUB_CONFIG=/etc/agenthub/config.yaml
```

### Key settings

| Section | Key | Default | Description |
|---------|-----|---------|-------------|
| `server` | `http_addr` | `:8080` | Admin UI listen address |
| `server` | `read_timeout` | `30s` | HTTP read timeout |
| `server` | `write_timeout` | `30s` | HTTP write timeout |
| `store` | `path` | `~/.agenthub/secrets.enc` | Encrypted secrets file |
| `slack` | `command_prefix` | `/agenthub` | Slash command prefix |
| `slack` | `heartbeat_interval` | `60s` | Slack connection heartbeat log interval |
| `openclaw` | `liveness_interval` | `60s` | How often to poll all bots |
| `openclaw` | `liveness_timeout` | `10s` | Per-bot health check timeout |
| `openclaw` | `health_path` | `/health` | Health endpoint on each bot |
| `openclaw` | `directives_path` | `/directives` | Directives endpoint on each bot |
| `openai` | `model` | `gpt-4o-mini` | OpenAI model for Slack intelligence |
| `openai` | `max_tokens` | `1024` | Max tokens per OpenAI response |
| `beads` | `db_path` | `.beads/dolt` | Beads/Dolt embedded database path |
| `dolt` | `dsn` | `root:@tcp(127.0.0.1:3306)/agenthub` | Dolt SQL server DSN |
| `kanban` | `columns` | `[backlog, ready, in_progress, review, done]` | Kanban column names and order |
| `log` | `level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `log` | `format` | `json` | Log format: `json` or `text` |

---

## Slack App Setup

1. Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From scratch**
2. Under **OAuth & Permissions** → **Bot Token Scopes**, add:

   | Scope | Purpose |
   |-------|---------|
   | `chat:write` | Send messages |
   | `commands` | Respond to slash commands |
   | `app_mentions:read` | Receive @mentions |
   | `im:history` | Read DMs |
   | `channels:read` | Channel info for bot binding |

3. Under **Basic Information** → **App-Level Tokens**, generate a token with the `connections:write` scope. This is your `xapp-` token.
4. Under **Settings** → **Socket Mode**, enable Socket Mode.
5. Under **Slash Commands**, create `/agenthub` (any placeholder URL is fine for Socket Mode).
6. Install the app to your workspace and copy the `xoxb-` Bot Token.

After running `./agenthub setup`, log into the admin UI and go to **Secrets** to enter:
- `slack_bot_token` — your `xoxb-` token
- `slack_app_token` — your `xapp-` token
- `openai_api_key` — your OpenAI API key

---

## Running

```bash
./agenthub serve
```

You will be prompted for the admin password to unlock the encrypted store. On a real terminal, echo is suppressed. The admin UI will be available at `http://localhost:8080`.

### Subcommands

| Command | Description |
|---------|-------------|
| `agenthub serve` | Start the server (default) |
| `agenthub setup` | First-run: set admin password and initialize encrypted store |
| `agenthub version` | Print version and build info |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `AGENTHUB_CONFIG` | Override path to `config.yaml` |

---

## Slack Commands

All commands use the prefix `/agenthub` (configurable).

| Command | Description |
|---------|-------------|
| `/agenthub bind host:port name` | Register an openclaw bot in this channel |
| `/agenthub remove name` | Unregister a bot (owner only) |
| `/agenthub list` | List bots in this channel with alive/dead status |
| `/agenthub chatty name` | Toggle chatty mode (bot responds to all messages, not just @mentions) |
| `/agenthub <task> [@botname]` | Create a work item, optionally routed to a specific bot |

You can also DM the agenthub bot directly for natural language interaction.

---

## Admin Web UI Routes

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/admin/` | Dashboard |
| `GET` | `/admin/login` | Login form |
| `POST` | `/admin/login` | Authenticate |
| `POST` | `/admin/logout` | Clear session |
| `GET` | `/admin/bots` | List all registered bots |
| `POST` | `/admin/bots/{name}/remove` | Remove a bot |
| `POST` | `/admin/bots/{name}/check` | Trigger immediate liveness check |
| `GET` | `/admin/kanban` | Kanban board |
| `GET` | `/admin/secrets` | Secrets manager |
| `POST` | `/admin/secrets` | Save secrets to encrypted store |
| `GET` | `/health` | Service health check (unauthenticated) |

---

## Development

```bash
# Run all tests
make test

# Run tests with coverage (minimum 90% enforced)
make test-cover

# Format source
make fmt

# Run static analysis
make lint

# Remove build artifacts
make clean
```

Integration tests require a running Dolt SQL server:

```bash
make test-integration
```

See [AGENTS.md](AGENTS.md) for contribution guidelines, the development workflow, and the pre-merge checklist.

---

## Project Structure

```
agenthub/
├── AGENTS.md                   # Contribution guidelines and commandments
├── VERSION                     # SEMVER version (currently 0.1.0)
├── config.yaml                 # All tunable settings
├── Makefile
├── docs/
│   ├── architecture.md         # Component diagram and data flows
│   ├── api.md                  # Openclaw API contract + admin HTTP routes
│   ├── configuration.md        # Full configuration reference
│   ├── deployment.md           # Deployment prerequisites and instructions
│   └── slack-integration.md    # Slack app setup guide
├── plans/                      # Phase implementation plans
├── src/
│   ├── cmd/agenthub/           # Main entry point
│   └── internal/
│       ├── api/                # HTTP handlers and admin UI server
│       ├── auth/               # Session auth, bcrypt, cookie management
│       ├── beads/              # Beads task tracker wrapper (CGO)
│       ├── config/             # config.yaml loader
│       ├── dolt/               # Dolt SQL client and schema migrations
│       ├── kanban/             # Kanban board grouping logic
│       ├── openclaw/           # Openclaw HTTP client and liveness checker
│       ├── openai/             # OpenAI chat wrapper
│       ├── slack/              # Slack Socket Mode handler and slash commands
│       └── store/              # AES-256-GCM encrypted secrets store
├── web/
│   ├── templates/              # Go HTML templates (embedded in binary)
│   └── static/                 # CSS and static assets (embedded in binary)
└── tests/
    └── integration/            # Integration test suite
```

---

## License

Copyright © 2025 NVIDIA Corporation. All rights reserved.
