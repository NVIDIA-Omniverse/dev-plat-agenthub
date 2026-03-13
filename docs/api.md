# agenthub API Reference

## Openclaw Instance API Contract

Every openclaw instance registered with agenthub MUST implement the following HTTP endpoints. agenthub calls these endpoints to check liveness and send behavioral directives.

### `GET /health`

Health check endpoint.

**Response:**
- `200 OK` — instance is alive and ready
- Any other status or timeout — instance is considered down

**Example:**
```
GET http://mybot.example.com:8080/health
→ 200 OK
```

### `POST /directives`

Send a behavioral directive to the openclaw instance.

**Request Body** (JSON):
```json
{
  "mention_only": true
}
```

When `mention_only: true`, the openclaw instance should only respond when directly @mentioned in a Slack channel. This is set automatically when a bot is first bound.

```json
{
  "chatty": true
}
```

When `chatty: true`, the openclaw instance may respond to any message in the channel it is part of (not just @mentions). The bot owner can grant this via `/agenthub chatty mybot`.

**Response:**
- `200 OK` — directive accepted
- `400 Bad Request` — unrecognized directive
- `500 Internal Server Error` — directive could not be applied

**Example:**
```
POST http://mybot.example.com:8080/directives
Content-Type: application/json

{"mention_only": true}

→ 200 OK
```

---

## agenthub Admin HTTP API

The admin web UI uses HTML forms and HTMX. There is no JSON REST API for the admin UI currently. The following routes exist:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Redirect to `/admin/` |
| GET | `/admin/` | Dashboard (auth required) |
| GET | `/admin/login` | Login form |
| POST | `/admin/login` | Authenticate |
| POST | `/admin/logout` | Clear session |
| GET | `/admin/bots` | List all registered bots |
| POST | `/admin/bots/{name}/remove` | Remove a bot binding |
| POST | `/admin/bots/{name}/check` | Trigger immediate liveness check |
| GET | `/admin/kanban` | Kanban board |
| GET | `/admin/config` | View configuration |
| POST | `/admin/config` | Save configuration |
| GET | `/admin/secrets` | Manage secrets (API keys, tokens) |
| POST | `/admin/secrets` | Save secrets to encrypted store |
| GET | `/health` | Service health check (not auth-gated) |

---

## Slack Commands

All commands begin with `/agenthub` (configurable via `config.yaml: slack.command_prefix`).

### `/agenthub bind host:port unique-name`

Register an openclaw instance.

- `host:port` — the HTTP address of the openclaw instance
- `unique-name` — a unique identifier for the bot (`^[a-z0-9-]+$`)

The bot is bound to the channel where the command is issued. Only the binding user can later remove it.

### `/agenthub remove unique-name`

Remove a bot binding. Only the original binding user may do this.

### `/agenthub list`

List all bots registered in the current channel with their alive/dead status.

### `/agenthub chatty unique-name`

Grant the bot permission to respond to any message (not just @mentions) in the channel. Only the bot's owner may do this.

### `/agenthub <task description> [@botname]`

Create a work item (Beads issue) with the given description. Optionally route it to a specific bot by including `@botname`. If no bot is specified, the task is assigned to any alive bot.

Example:
```
/agenthub fix the authentication bug @mybot
/agenthub add dark mode support
```
