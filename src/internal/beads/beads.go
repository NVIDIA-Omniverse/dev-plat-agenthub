// Package beads wraps the github.com/steveyegge/beads library to provide
// agenthub-specific task management operations.
//
// All work items created via Slack commands or the web UI are Beads issues.
// This package connects to a running Dolt server via the beads library.
package beads

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	beadslib "github.com/steveyegge/beads"
)

// Storage is the narrow interface that this package needs from beads.Storage.
// Using a narrow interface keeps tests decoupled from beads internals.
type Storage interface {
	CreateIssue(ctx context.Context, issue *beadslib.Issue, actor string) error
	GetIssue(ctx context.Context, id string) (*beadslib.Issue, error)
	UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error
	CloseIssue(ctx context.Context, id string, reason string, actor string, session string) error
	SearchIssues(ctx context.Context, query string, filter beadslib.IssueFilter) ([]*beadslib.Issue, error)
	GetReadyWork(ctx context.Context, filter beadslib.WorkFilter) ([]*beadslib.Issue, error)
	AddIssueComment(ctx context.Context, issueID, author, text string) (*beadslib.Comment, error)
	SetConfig(ctx context.Context, key, value string) error
	GetConfig(ctx context.Context, key string) (string, error)
}

// Client provides task management operations backed by a Beads/Dolt database.
type Client struct {
	storage Storage
}

// New opens a Beads database rooted at dbPath (creating it if missing) and returns a Client.
// dbPath is the dolt subdirectory (e.g. ".beads/dolt"); the parent .beads directory is derived
// automatically and passed to OpenFromConfig so that server-mode settings in metadata.json are
// respected and the Dolt server port is properly resolved.
func New(ctx context.Context, dbPath string) (*Client, error) {
	beadsDir := filepath.Dir(dbPath)
	s, err := beadslib.OpenFromConfig(ctx, beadsDir)
	if err != nil {
		return nil, fmt.Errorf("opening beads db at %q: %w", dbPath, err)
	}
	return &Client{storage: s}, nil
}

// NewWithStorage creates a Client from an existing Storage (useful in tests).
func NewWithStorage(s Storage) *Client {
	return &Client{storage: s}
}

// Storage returns the underlying Storage for direct queries.
func (c *Client) Storage() Storage {
	return c.storage
}

// TaskRequest holds all user-settable fields for creating a new task.
// Zero values are treated as "not set"; defaults are applied before storage.
type TaskRequest struct {
	Title              string
	Description        string
	Status             string // beads status or kanban column name; defaults to StatusOpen
	Priority           int    // 0=critical 1=high 2=normal 3=low
	IssueType          string // "task", "bug", "feature", "epic", "chore"
	Assignee           string
	EstimatedMinutes   int    // 0 = unset
	AcceptanceCriteria string
	Notes              string
	DueAt              string // "YYYY-MM-DD" or ""
	Labels             string // comma-separated
	Actor              string
}

// CreateTask creates a new issue from a TaskRequest.
// Zero-value Status defaults to StatusOpen; zero-value IssueType defaults to TypeTask.
func (c *Client) CreateTask(ctx context.Context, req TaskRequest) (*beadslib.Issue, error) {
	status := beadslib.Status(req.Status)
	if status == "" {
		status = beadslib.StatusOpen
	}
	issueType := beadslib.IssueType(req.IssueType)
	if issueType == "" {
		issueType = beadslib.TypeTask
	}
	issue := &beadslib.Issue{
		Title:              req.Title,
		Description:        req.Description,
		Status:             status,
		Priority:           req.Priority,
		IssueType:          issueType,
		Assignee:           req.Assignee,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Notes:              req.Notes,
		CreatedBy:          req.Actor,
	}
	if req.EstimatedMinutes > 0 {
		issue.EstimatedMinutes = &req.EstimatedMinutes
	}
	if req.DueAt != "" {
		if t, err := time.Parse("2006-01-02", req.DueAt); err == nil {
			issue.DueAt = &t
		}
	}
	for _, l := range strings.Split(req.Labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			issue.Labels = append(issue.Labels, l)
		}
	}
	if err := c.storage.CreateIssue(ctx, issue, req.Actor); err != nil {
		return nil, fmt.Errorf("creating task %q: %w", req.Title, err)
	}
	return issue, nil
}

// AssignTask assigns an issue to an openclaw bot by name.
// botName should be the unique-name of the openclaw instance.
func (c *Client) AssignTask(ctx context.Context, issueID, botName, actor string) error {
	updates := map[string]interface{}{
		"assignee": botName,
		"status":   beadslib.StatusInProgress,
	}
	if err := c.storage.UpdateIssue(ctx, issueID, updates, actor); err != nil {
		return fmt.Errorf("assigning task %q to %q: %w", issueID, botName, err)
	}
	return nil
}

// ListReadyWork returns all open, unblocked, unassigned issues.
func (c *Client) ListReadyWork(ctx context.Context) ([]*beadslib.Issue, error) {
	issues, err := c.storage.GetReadyWork(ctx, beadslib.WorkFilter{})
	if err != nil {
		return nil, fmt.Errorf("listing ready work: %w", err)
	}
	return issues, nil
}

// RouteToBot routes an issue to a specific bot (or any alive bot if botName is empty).
// The caller is responsible for verifying that the bot is alive before routing.
func (c *Client) RouteToBot(ctx context.Context, issueID, botName, actor string) error {
	return c.AssignTask(ctx, issueID, botName, actor)
}

// CloseTask closes an issue with a reason.
func (c *Client) CloseTask(ctx context.Context, issueID, reason, actor string) error {
	if err := c.storage.CloseIssue(ctx, issueID, reason, actor, ""); err != nil {
		return fmt.Errorf("closing task %q: %w", issueID, err)
	}
	return nil
}

// UpdateFields updates the editable fields of an existing issue.
// Any field with a zero value is still written (clearing it); only empty title is skipped.
func (c *Client) UpdateFields(ctx context.Context, issueID, title, description, status string,
	priority int, issueType, assignee string, estimatedMins int,
	acceptanceCriteria, notes, dueAt, labels, actor string) error {

	updates := map[string]interface{}{
		"description":         description,
		"assignee":            assignee,
		"acceptance_criteria": acceptanceCriteria,
		"notes":               notes,
	}
	if title != "" {
		updates["title"] = title
	}
	if priority >= 0 {
		updates["priority"] = priority
	}
	if status != "" {
		updates["status"] = beadslib.Status(status)
	}
	if issueType != "" {
		updates["issue_type"] = beadslib.IssueType(issueType)
	}
	if estimatedMins > 0 {
		updates["estimated_minutes"] = estimatedMins
	}
	if dueAt != "" {
		if t, err := time.Parse("2006-01-02", dueAt); err == nil {
			updates["due_at"] = &t
		}
	}
	if labels != "" {
		var ls []string
		for _, l := range strings.Split(labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				ls = append(ls, l)
			}
		}
		updates["labels"] = ls
	}
	if err := c.storage.UpdateIssue(ctx, issueID, updates, actor); err != nil {
		return fmt.Errorf("updating task %q: %w", issueID, err)
	}
	return nil
}

// GetTask returns a single issue by ID.
func (c *Client) GetTask(ctx context.Context, issueID string) (*beadslib.Issue, error) {
	issue, err := c.storage.GetIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("getting task %q: %w", issueID, err)
	}
	return issue, nil
}

// ListAll returns all issues matching the given filter.
// Pass an empty IssueFilter{} to list all issues.
func (c *Client) ListAll(ctx context.Context, filter beadslib.IssueFilter) ([]*beadslib.Issue, error) {
	issues, err := c.storage.SearchIssues(ctx, "", filter)
	if err != nil {
		return nil, fmt.Errorf("listing all tasks: %w", err)
	}
	return issues, nil
}

// UpdateStatus changes the status of an issue and optionally records a note as a comment.
// newStatus should be one of: "open", "in_progress", "blocked", "done".
func (c *Client) UpdateStatus(ctx context.Context, issueID, newStatus, note, actor string) error {
	if note != "" {
		if err := c.AddComment(ctx, issueID, actor, note); err != nil {
			return fmt.Errorf("adding status note for %q: %w", issueID, err)
		}
	}
	updates := map[string]interface{}{"status": beadslib.Status(newStatus)}
	if err := c.storage.UpdateIssue(ctx, issueID, updates, actor); err != nil {
		return fmt.Errorf("updating status of task %q: %w", issueID, err)
	}
	return nil
}

// AddComment adds a comment to an issue.
func (c *Client) AddComment(ctx context.Context, issueID, author, text string) error {
	_, err := c.storage.AddIssueComment(ctx, issueID, author, text)
	if err != nil {
		return fmt.Errorf("adding comment to %q: %w", issueID, err)
	}
	return nil
}

// EnsureInitialized ensures the Beads database has been initialized with a prefix.
// Should be called on first startup.
func (c *Client) EnsureInitialized(ctx context.Context, prefix string) error {
	// GetConfig returns ("", nil) when the key is absent, so check the value not the error.
	value, err := c.storage.GetConfig(ctx, "issue_prefix")
	if err != nil {
		return fmt.Errorf("checking beads init: %w", err)
	}
	if value == "" {
		// Not initialized yet.
		if err2 := c.storage.SetConfig(ctx, "issue_prefix", prefix); err2 != nil {
			return fmt.Errorf("initializing beads prefix: %w", err2)
		}
		if err2 := c.storage.SetConfig(ctx, "created_at", time.Now().UTC().Format(time.RFC3339)); err2 != nil {
			return fmt.Errorf("setting beads created_at: %w", err2)
		}
	}
	return nil
}
