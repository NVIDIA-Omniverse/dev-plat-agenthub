# agenthub Configuration Reference

All tunable behavior is controlled by `config.yaml`. No behavior is hard-coded.
Secrets (API keys, tokens, passwords) are NOT in `config.yaml` — they live in the Dolt `settings` table (encrypted).

## `config.yaml` Keys

### `server`

| Key | Default | Description |
|-----|---------|-------------|
| `server.http_addr` | `":8080"` | HTTP listen address for the admin web UI |
| `server.public_url` | `""` | Public base URL (used in Slack messages and credential URLs) |
| `server.read_timeout` | `30s` | HTTP server read timeout |
| `server.write_timeout` | `30s` | HTTP server write timeout |
| `server.session_cookie_name` | `"agenthub_session"` | Name of the session cookie |

### `store`

| Key | Default | Description |
|-----|---------|-------------|
| `store.path` | `""` | Path to a legacy `secrets.enc` file. If set and the file exists, keys are auto-migrated to Dolt settings on first serve. Leave empty once migration is complete. |

### `slack`

| Key | Default | Description |
|-----|---------|-------------|
| `slack.command_prefix` | `"/agenthub"` | Slash command prefix |
| `slack.default_channel` | `""` | Slack channel ID for registration announcements |
| `slack.heartbeat_interval` | `60s` | How often to log Slack connection heartbeat |

### `openclaw`

| Key | Default | Description |
|-----|---------|-------------|
| `openclaw.liveness_timeout` | `10s` | Timeout for each liveness HTTP request |
| `openclaw.liveness_interval` | `60s` | How often to poll all registered agents |
| `openclaw.health_path` | `"/health"` | Health endpoint path on openclaw instances |
| `openclaw.directives_path` | `"/directives"` | Directives endpoint path |

### `openai`

| Key | Default | Description |
|-----|---------|-------------|
| `openai.model` | `"gpt-4o-mini"` | OpenAI model for agenthub's intelligence |
| `openai.max_tokens` | `1024` | Maximum tokens per OpenAI response |
| `openai.base_url` | `""` | Override OpenAI API base URL (for compatible endpoints) |
| `openai.system_prompt` | *(see config.yaml)* | System prompt for the assistant |

These are seeded as defaults on startup. All can be overridden live via `PUT /api/settings/{key}` without restart.

### `beads`

| Key | Default | Description |
|-----|---------|-------------|
| `beads.db_path` | `".beads/dolt"` | Path to the Beads/Dolt embedded database directory |

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

### `llm_tiers`

| Key | Default | Description |
|-----|---------|-------------|
| `llm_tiers.default.base_url` | `""` | Base URL for default tier LLM |
| `llm_tiers.default.model` | `""` | Model name for default tier |
| `llm_tiers.default.api_key_setting` | `""` | Settings store key holding the API key |
| `llm_tiers.default.max_tokens` | `0` | Max tokens for default tier |
| `llm_tiers.escalation.base_url` | `""` | Base URL for escalation tier LLM |
| `llm_tiers.escalation.model` | `""` | Model name for escalation tier |
| `llm_tiers.escalation.api_key_setting` | `""` | Settings store key holding the API key |
| `llm_tiers.escalation.max_tokens` | `0` | Max tokens for escalation tier |

---

## Environment Variables

| Variable | Description |
|----------|-------------|
| `AGENTHUB_CONFIG` | Override path to `config.yaml` |
| `AGENTHUB_ADMIN_PASSWORD` | Admin password for non-interactive startup (service deployments, CI). Takes precedence over interactive prompt. |

---

## Secrets (Dolt Encrypted Settings)

The following values are stored encrypted in the Dolt `settings` table. They are NEVER in `config.yaml`.

| Key | Description | How to Set |
|-----|-------------|------------|
| `admin_password_hash` | bcrypt hash of admin password | Set via first-run setup form |
| `session_secret` | 32-byte random session signing key | Generated on first-run setup |
| `registration_token` | Shared secret for `POST /api/register` and heartbeat | Generated on first-run setup |
| `openai_api_key` | OpenAI API key | Admin UI → Secrets, or `PUT /api/settings/openai_api_key` |
| `slack_bot_token` | Slack Bot Token (`xoxb-...`) | Admin UI → Secrets |
| `slack_app_token` | Slack App-Level Token (`xapp-...`) | Admin UI → Secrets |

Settings can also be updated live (no restart) via:
```
PUT /api/settings/{key}
Content-Type: application/json
{"value": "new-value"}
```

---

## First-Run Setup

agenthub detects first-run automatically when the Dolt `settings` table has no `admin_password_hash`.

Start the server normally:
```bash
./agenthub serve
```

All admin requests will redirect to `/admin/setup`. Complete the web form to initialize the encrypted settings. On subsequent starts:
- **Interactive:** admin password is prompted on the terminal (echo-suppressed)
- **Non-interactive/service:** set `AGENTHUB_ADMIN_PASSWORD=<password>` in the environment

---

## Migrating from Legacy File Store

If you previously used the file-based `secrets.enc` store, set `store.path` in `config.yaml` pointing to the file. On the next `agenthub serve` run, all keys are automatically copied into Dolt settings (skipping any already-set keys). Once migrated, you can remove `store.path` from `config.yaml`.
