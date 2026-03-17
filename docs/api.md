# agenthub API Reference

## Authentication

### Registration Token (`X-Registration-Token`)

Agent-facing endpoints (`/api/*`) require the registration token in the request header:

```
X-Registration-Token: <token>
```

The token is displayed after first-run setup and is also available via `GET /api/settings` (admin-authenticated).

### Admin Session

Admin UI endpoints (`/admin/*`) require a valid session cookie obtained by `POST /admin/login`.

Settings API endpoints (`PUT /api/settings/{key}`, `GET /api/settings`) require an active admin session.

---

## Openclaw Instance API Contract

Every openclaw instance registered with agenthub MUST implement the following HTTP endpoints.

### `GET /health`

Health check endpoint.

**Response:** `200 OK` — instance is alive and ready. Any other status or timeout marks it down.

### `POST /directives`

Send a behavioral directive to the openclaw instance.

**Request Body** (JSON):
```json
{"mention_only": true}
```
or
```json
{"chatty": true}
```

**Response:** `200 OK` — directive accepted.

---

## Agent REST API

All agent endpoints require `X-Registration-Token` header.

### `POST /api/register`

Register a new agent.

**Query params:**
- `skip_probe=1` — skip reachability check (use when agent and server are on different networks)

**Request Body** (JSON):
```json
{
  "name": "my-agent",
  "host": "1.2.3.4",
  "port": 8080,
  "channel_id": "",
  "owner_slack_user": "U12345678"
}
```

**Response `201 Created`:**
```json
{"id": "550e8400-...", "name": "my-agent"}
```

**Response `409 Conflict`** (name already taken):
```json
{
  "error": "agent name \"my-agent\" is already taken",
  "suggestions": ["my-agent-2", "my-agent-bot", "my-agent-v2"]
}
```

Side effects on success:
- Creates `#agent-<name>` Slack channel (best-effort)
- Announces the new agent in `slack.default_channel`

---

### `POST /api/heartbeat`

Report agent liveness and current status. Must be called at least once every 2 minutes for the agent to show as alive in the dashboard.

**Request Body** (JSON):
```json
{
  "name": "my-agent",
  "current_task": "AH-abc123",
  "status": "working",
  "message": "Analysing log output…"
}
```

`status` values: `idle`, `working`, `error`

**Response `200 OK`**

---

### `GET /api/inbox`

Poll pending inbox messages for an agent.

**Query params:**
- `bot_name=<name>` (required)

**Response `200 OK`:**
```json
[
  {
    "id": "msg-abc",
    "bot_name": "my-agent",
    "from_user": "U12345678",
    "channel": "D0987654",
    "body": "please summarise the last build",
    "task_context": {},
    "created_at": "2026-03-15T10:00:00Z"
  }
]
```

Returns an empty array `[]` when no messages are pending.

---

### `POST /api/inbox/{id}/ack`

Acknowledge a message (mark as read).

**Response `200 OK`**

---

### `POST /api/inbox/{id}/reply`

Post a reply to the Slack thread that originated the message.

**Request Body** (JSON):
```json
{"text": "Done! I summarised the build in the thread."}
```

**Response `200 OK`**

---

## Bot Profiles

Agent-authenticated via `X-Registration-Token`.

### `GET /api/bots/profiles`

List all bot profiles.

**Response `200 OK`:**
```json
[
  {"bot_name": "my-agent", "description": "...", "specializations": ["python"], ...}
]
```

---

### `GET /api/bots/{name}/profile`

Get a specific bot's profile.

**Response `200 OK`:**
```json
{
  "bot_name": "my-agent",
  "description": "Python specialist",
  "specializations": ["python", "code-review"],
  "tools": ["github"],
  "hardware": {"gpu": "A100"},
  "max_concurrent_tasks": 3,
  "owner_contact": "user@example.com"
}
```

---

### `PUT /api/bots/{name}/profile`

Create or update a bot profile.

**Request Body** (JSON):
```json
{
  "description": "Python specialist",
  "specializations": ["python", "code-review"],
  "tools": ["github"],
  "hardware": {"gpu": "A100"},
  "max_concurrent_tasks": 3,
  "owner_contact": "user@example.com"
}
```

Profile can also be provided during registration by including a `profile` field in the register request body.

**Response `200 OK`**

---

## Chat (Owner-Bot Private Channel)

### `GET /api/chat/{botName}`

Get chat history. Admin-authenticated.

**Query params:**
- `limit` (default 50)
- `before` (ISO timestamp)

**Response `200 OK`:**
```json
[
  {
    "id": "msg-abc",
    "bot_name": "my-agent",
    "sender": "admin",
    "body": "Hello bot",
    "metadata": {},
    "created_at": "2026-03-15T10:00:00Z"
  }
]
```

---

### `POST /api/chat/{botName}/send`

Send a message to a bot. Admin-authenticated.

**Request Body** (JSON):
```json
{"body": "Hello bot"}
```

**Response `200 OK`:**
```json
{"id": "msg-abc", "bot_name": "my-agent", "sender": "admin", "body": "Hello bot", "metadata": {}, "created_at": "2026-03-15T10:00:00Z"}
```

Enqueues the message in the bot's inbox.

---

### `POST /api/chat/{botName}/reply`

Bot replies to a chat message. Agent-authenticated, requires `X-Bot-Name` header.

**Request Body** (JSON):
```json
{"body": "Hello owner"}
```

**Response `200 OK`:**
```json
{"id": "msg-def", "bot_name": "my-agent", "sender": "my-agent", "body": "Hello owner", "metadata": {}, "created_at": "2026-03-15T10:01:00Z"}
```

---

## LLM Escalation

### `POST /api/llm/escalate`

Escalate to a stronger LLM model. Agent-authenticated, requires `X-Bot-Name` header.

**Request Body** (JSON):
```json
{
  "messages": [{"role": "user", "content": "complex question"}],
  "model_hint": "optional-model-name"
}
```

**Response `200 OK`:**
```json
{"reply": "escalated response"}
```

Usage is logged.

---

### `GET /api/usage`

Get LLM usage summary. Admin-authenticated.

**Response `200 OK`:**
```json
[
  {
    "bot_name": "my-agent",
    "tier": "escalation",
    "model": "gpt-4o",
    "total_calls": 42,
    "total_input": 15000,
    "total_output": 3200,
    "avg_latency_ms": 850
  }
]
```

---

## Credential Delivery

### `GET /api/credentials/{botName}`

Fetch project credentials for an active task assignment. Agent-authenticated.

**Response `200 OK`:**
```json
[
  {
    "name": "my-project",
    "kind": "github",
    "credentials": {"token": "ghp_..."}
  }
]
```

Requires an active (non-revoked) task assignment for the bot. Returns `403 Forbidden` if no active assignment exists.

---

### `POST /api/tasks/{id}/status`

Update the status of a Beads task (moves the kanban card).

**Request Body** (JSON):
```json
{
  "status": "in_progress",
  "note": "Started investigating",
  "actor": "my-agent"
}
```

**Response `200 OK`**

---

### `POST /api/tasks/{id}/log`

Append a log line to a Beads task.

**Request Body** (JSON):
```json
{"text": "Checked 3 of 5 log files"}
```

**Response `200 OK`**

---

## Admin Settings API

Requires an active admin session.

### `PUT /api/settings/{key}`

Update a runtime setting immediately (no server restart required).

**Request Body** (JSON):
```json
{"value": "gpt-4o"}
```

**Response `200 OK`**

Common keys: `openai_api_key`, `openai.model`, `openai.system_prompt`, `openai.base_url`, `slack_bot_token`, `slack_app_token`.

---

### `GET /api/settings`

List all stored setting keys (values not returned).

**Response `200 OK`:**
```json
{"keys": ["openai_api_key", "openai.model", "slack_bot_token", ...]}
```

---

## Admin Web UI Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Redirect to `/admin/` |
| GET | `/admin/` | Dashboard (auth required) |
| GET | `/admin/login` | Login form |
| POST | `/admin/login` | Authenticate |
| POST | `/admin/logout` | Clear session |
| GET | `/admin/setup` | First-run setup form (setup mode only) |
| POST | `/admin/setup` | Submit admin password and initialize settings |
| GET | `/admin/bots` | List all registered agents |
| GET | `/admin/chat/{botName}` | Private chat with a bot (auth required) |
| POST | `/admin/bots/{name}/remove` | Remove an agent |
| POST | `/admin/bots/{name}/check` | Trigger immediate liveness check |
| GET | `/admin/kanban` | Kanban board |
| GET | `/admin/projects` | Project list |
| POST | `/admin/projects` | Create a new project |
| GET | `/admin/projects/{id}` | Project detail |
| GET | `/admin/secrets` | Manage secrets (API keys, tokens) |
| POST | `/admin/secrets` | Save secrets to encrypted Dolt settings |
| GET | `/health` | Service health check (unauthenticated) |
| GET | `/api/events` | SSE stream of live agent/task events (admin session) |

---

## Slack Commands

All commands begin with `/agenthub` (configurable via `config.yaml: slack.command_prefix`).

### `/agenthub bind host:port unique-name`

Register an openclaw instance (legacy channel-bound registration).

- `host:port` — the HTTP address of the openclaw instance
- `unique-name` — a unique identifier for the bot (`^[a-z0-9-]+$`)

The bot is bound to the channel where the command is issued.

### `/agenthub remove unique-name`

Remove a bot binding.

### `/agenthub list`

List all bots registered in the current channel with their alive/dead status.

### `/agenthub chatty unique-name`

Grant the bot permission to respond to any message in the channel (not just @mentions).

### `/agenthub <task description> [@botname]`

Create a Beads work item. Optionally route it to a specific bot by including `@botname`.

Examples:
```
/agenthub fix the authentication bug @mybot
/agenthub add dark mode support
```

### DMs

DM the agenthub bot for natural language interaction. Prefix messages with `@botname` to route to a specific agent:
```
@my-agent: what's the status of the build?
```

### Per-Agent Channels

Post directly in `#agent-<name>` to send a message straight to that agent's inbox (no task created, no AI processing).
