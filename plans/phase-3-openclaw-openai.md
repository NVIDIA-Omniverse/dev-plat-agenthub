# Phase 3: Openclaw Client + OpenAI

**Status:** Planned
**Goal:** Build the openclaw HTTP client with liveness checking and the OpenAI chat wrapper.

## Packages to Build

### `src/internal/openclaw/`

HTTP client for interacting with registered openclaw instances.

```go
type Client struct {
    httpClient *http.Client
    baseURL    string
}

// HealthChecker interface (for mocking in tests)
type HealthChecker interface {
    Health(ctx context.Context) error
    SetMentionOnly(ctx context.Context) error
    SetChatty(ctx context.Context, chatty bool) error
}

func New(host string, port int, timeout time.Duration) *Client
func (c *Client) Health(ctx context.Context) error           // GET /health → 200 = alive
func (c *Client) SetMentionOnly(ctx context.Context) error   // POST /directives {"mention_only":true}
func (c *Client) SetChatty(ctx context.Context, chatty bool) error
```

**LivenessChecker** — background goroutine:
```go
type LivenessChecker struct {
    db          *dolt.DB
    slackClient SlackNotifier  // interface
    cfg         config.OpenclawConfig
}

func NewLivenessChecker(db *dolt.DB, slack SlackNotifier, cfg config.OpenclawConfig) *LivenessChecker
func (lc *LivenessChecker) Run(ctx context.Context)  // ticks per cfg.LivenessInterval
```

On alive→dead transition: send Slack notification to the instance's channel_id.
On dead→alive transition: re-send directives (mention_only) to ensure state is correct.

### `src/internal/openai/`

Thin wrapper with an interface for mocking.

```go
// Completer interface (for mocking in tests)
type Completer interface {
    Chat(ctx context.Context, messages []openai.ChatCompletionMessage) (string, error)
}

type Client struct { client *openai.Client; model string; maxTokens int }

func New(apiKey, model string, maxTokens int) *Client
func (c *Client) Chat(ctx context.Context, messages []openai.ChatCompletionMessage) (string, error)
```

System prompt comes from config (`openai.system_prompt`).

## Openclaw API Contract

This is the interface that openclaw instances MUST implement:

| Method | Path | Request | Response |
|--------|------|---------|----------|
| GET | `/health` | — | 200 OK (alive) |
| POST | `/directives` | `{"mention_only": true}` | 200 OK |
| POST | `/directives` | `{"chatty": true}` | 200 OK |

Documented in `docs/api.md`.

## Test Strategy

- `openclaw_test.go`: use `httptest.NewServer()` to mock the openclaw API
  - Health: 200 → no error; 503 → error; timeout → error
  - SetMentionOnly: verify correct JSON body sent
  - LivenessChecker: mock goes down → DB updated + Slack notification sent
- `openai_test.go`: use mock Completer interface; verify message formatting

## Verification

```bash
make test
```
