# Phase 4: Slack Integration

**Status:** Planned
**Goal:** Implement Socket Mode Slack app with slash commands, event routing, and OpenAI-powered responses.

## Package: `src/internal/slack/`

### Handler (`handler.go`)

```go
type Handler struct {
    slackClient *slack.Client
    socketMode  *socketmode.Client
    deps        *Deps
}

type Deps struct {
    DB        *dolt.DB
    Beads     *beads.Client
    Openclaw  func(host string, port int) openclaw.HealthChecker
    OpenAI    openai.Completer
    Config    config.Config
}

func NewHandler(deps *Deps) (*Handler, error)
func (h *Handler) Run(ctx context.Context) error
func (h *Handler) handleEvent(evt socketmode.Event)
```

### Slash Commands (`slash.go`)

**`/agenthub bind host:port unique-name`**
- Parse and validate `host:port` (format + DNS resolution)
- Validate `unique-name` matches `^[a-z0-9-]+$`
- Check unique-name not already used in this channel
- Call `openclaw.Health()` to verify reachability (5s timeout)
- Write to `openclaw_instances` in Dolt
- Send `SetMentionOnly()` directive to the new instance
- Respond with confirmation: "✓ Bot `unique-name` bound successfully."

**`/agenthub <task description> [@botname]`**
- Create Beads issue with the task description
- If `@botname` specified: validate bot is alive, assign to it
- If no bot specified: assign to any alive bot (round-robin)
- Respond with: "Task `bd-xxxx` created and assigned to `botname`."

**`/agenthub list`**
- List all bots in the current channel with their alive/dead status

**`/agenthub remove unique-name`**
- Only the original binding user can remove a bot
- Delete from `openclaw_instances`

### Event Routing (`events.go`)

- `app_mention` → extract message text → OpenAI chat → respond in thread
- `message` in DM with agenthub → same OpenAI routing
- For bot status queries (detected by OpenAI): query DB and format response
- For task creation requests: call beads.CreateTask and respond with ID

## Required Slack App Scopes

Bot Token (`xoxb-`):
- `chat:write`
- `commands`
- `app_mentions:read`
- `im:history`
- `channels:read`

App Token (`xapp-`):
- `connections:write` (Socket Mode)

## Test Strategy

- `slash_test.go`: valid bind (mock openclaw health), duplicate name, bad host, bad name format
- `events_test.go`: app_mention routing, DM routing; use mock Completer and mock DB
- No live Slack API calls in unit tests; all external interfaces mocked

## Key Gotchas

- Socket Mode needs two tokens; both fetched from encrypted store at startup
- Slash command responses must arrive within 3 seconds; use async goroutine + response_url for slow ops
- `app_mention` events include the bot's own user ID in the text; strip it before sending to OpenAI

## Verification

```bash
make test
```
