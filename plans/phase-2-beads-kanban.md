# Phase 2: Beads + Kanban

**Status:** Planned
**Goal:** Integrate the Beads library for work item management and build the kanban data model.

## Packages to Build

### `src/internal/beads/`
Wrap `github.com/steveyegge/beads` library (library mode, not subprocess).

```go
type Client struct { store beads.Storage }

func New(ctx context.Context, dbPath string) (*Client, error)
func (c *Client) CreateTask(ctx, title, description string, priority int) (*beads.Issue, error)
func (c *Client) AssignTask(ctx, issueID, assignee string) error
func (c *Client) ListReadyWork(ctx) ([]*beads.Issue, error)
func (c *Client) RouteToBot(ctx, issueID, botName string) error
func (c *Client) CloseTask(ctx, issueID, reason string) error
func (c *Client) ListAll(ctx) ([]*beads.Issue, error)
```

### `src/internal/kanban/`
Group Beads issues into board columns.

```go
type Board struct { Columns []Column }
type Column struct { Status string; Issues []*beads.Issue }

func BuildBoard(ctx context.Context, store beads.Storage, columns []string) (*Board, error)
```

## Key Decisions

- **Library mode**: Use `beads.Open()` directly, not `bd` subprocess. Requires CGO (already enabled).
- **DB path**: From config: `beads.db_path: ".beads/dolt"`
- **Beads handles its own Dolt**: The beads db is separate from agenthub's Dolt server.
- **Assignee field**: Use openclaw instance `name` as assignee for routing.

## Test Strategy

- Use `beads.Open()` with `t.TempDir()` for isolated DB per test (mirrors beads' own test pattern)
- `beads_test.go`: create task, assign, list ready, close; verify state transitions
- `kanban_test.go`: board builds correct columns from mixed-status issues

## Verification

```bash
make test
```
