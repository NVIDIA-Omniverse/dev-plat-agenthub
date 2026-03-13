# agenthub Configuration Reference

All tunable behavior is controlled by `config.yaml`. No behavior is hard-coded.
Secrets (API keys, tokens, passwords) are NOT in `config.yaml` — they live in the encrypted store.

## `config.yaml` Keys

### `server`

| Key | Default | Description |
|-----|---------|-------------|
| `server.http_addr` | `":8080"` | HTTP listen address for the admin web UI |
| `server.read_timeout` | `30s` | HTTP server read timeout |
| `server.write_timeout` | `30s` | HTTP server write timeout |
| `server.session_cookie_name` | `"agenthub_session"` | Name of the session cookie |

### `store`

| Key | Default | Description |
|-----|---------|-------------|
| `store.path` | `"~/.agenthub/secrets.enc"` | Path to the encrypted secrets file |

### `slack`

| Key | Default | Description |
|-----|---------|-------------|
| `slack.socket_mode` | `true` | Use Slack Socket Mode (recommended) |
| `slack.heartbeat_interval` | `60s` | How often to log Slack connection heartbeat |
| `slack.command_prefix` | `"/agenthub"` | Slash command prefix |

### `openclaw`

| Key | Default | Description |
|-----|---------|-------------|
| `openclaw.liveness_timeout` | `10s` | Timeout for each liveness HTTP request |
| `openclaw.liveness_interval` | `60s` | How often to poll all registered bots |
| `openclaw.health_path` | `"/health"` | Health endpoint path on openclaw instances |
| `openclaw.directives_path` | `"/directives"` | Directives endpoint path |

### `openai`

| Key | Default | Description |
|-----|---------|-------------|
| `openai.model` | `"gpt-4o-mini"` | OpenAI model for agenthub's intelligence |
| `openai.max_tokens` | `1024` | Maximum tokens per OpenAI response |
| `openai.system_prompt` | *(see config.yaml)* | System prompt for the assistant |

### `beads`

| Key | Default | Description |
|-----|---------|-------------|
| `beads.db_path` | `".beads/dolt"` | Path to the Beads/Dolt database directory |

### `dolt`

| Key | Default | Description |
|-----|---------|-------------|
| `dolt.dsn` | `"root:@tcp(127.0.0.1:3306)/agenthub"` | MySQL DSN for the Dolt SQL server |
| `dolt.max_open_conns` | `10` | Max open DB connections |
| `dolt.max_idle_conns` | `5` | Max idle DB connections |
| `dolt.conn_max_lifetime` | `5m` | Connection max lifetime |

### `log`

| Key | Default | Description |
|-----|---------|-------------|
| `log.level` | `"info"` | Log level: `debug`, `info`, `warn`, `error` |
| `log.format` | `"json"` | Log format: `json` or `text` |

### `kanban`

| Key | Default | Description |
|-----|---------|-------------|
| `kanban.columns` | `[backlog, ready, in_progress, review, done]` | Kanban column order and names |

## Secrets (Encrypted Store)

The following values are stored in the encrypted store at `store.path` and are NEVER in `config.yaml`:

| Key | Description | How to Set |
|-----|-------------|------------|
| `admin_password_hash` | bcrypt hash of admin password | Set via `make setup` |
| `session_secret` | 32-byte random session signing key | Generated on `make setup` |
| `openai_api_key` | OpenAI API key | Set via Admin UI → Secrets |
| `slack_bot_token` | Slack Bot Token (`xoxb-...`) | Set via Admin UI → Secrets |
| `slack_app_token` | Slack App-Level Token (`xapp-...`) | Set via Admin UI → Secrets |

## First-Run Setup

Run once to initialize the encrypted store and set the admin password:
```bash
make setup
# or
./agenthub setup
```

This will:
1. Prompt for an admin username and password
2. Derive the store encryption key from the password (Argon2id)
3. Generate a random session secret
4. Write `~/.agenthub/secrets.enc`
