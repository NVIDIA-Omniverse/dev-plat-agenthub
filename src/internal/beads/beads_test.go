package beads

import (
	"context"
	"errors"
	"testing"

	beadslib "github.com/steveyegge/beads"
	"github.com/stretchr/testify/require"
)


func TestNewWithStorage(t *testing.T) {
	m := newMockStorage()
	c := NewWithStorage(m)
	require.NotNil(t, c)
	require.Equal(t, Storage(m), c.Storage())
}

func TestEnsureInitialized(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	require.NoError(t, c.EnsureInitialized(ctx, "ah"))
	require.Equal(t, "ah", m.configs["issue_prefix"])

	// Second call should be idempotent (prefix already set).
	require.NoError(t, c.EnsureInitialized(ctx, "ah"))
	require.Equal(t, "ah", m.configs["issue_prefix"])
}

func TestCreateTask(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, "Fix the bug", "A description", "U123", 1)
	require.NoError(t, err)
	require.NotEmpty(t, issue.ID)
	require.Equal(t, "Fix the bug", issue.Title)
	require.Equal(t, "A description", issue.Description)
	require.Equal(t, beadslib.StatusOpen, issue.Status)
	require.Equal(t, beadslib.TypeTask, issue.IssueType)
	require.Equal(t, 1, issue.Priority)
	require.Equal(t, "U123", issue.CreatedBy)
}

func TestCreateTaskError(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.createErr = errors.New("db error")
	c := NewWithStorage(m)

	_, err := c.CreateTask(ctx, "title", "", "actor", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "db error")
}

func TestAssignTask(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, "Task to assign", "", "actor", 2)
	require.NoError(t, err)

	require.NoError(t, c.AssignTask(ctx, issue.ID, "mybot", "actor"))

	got, err := c.GetTask(ctx, issue.ID)
	require.NoError(t, err)
	require.Equal(t, "mybot", got.Assignee)
	require.Equal(t, beadslib.StatusInProgress, got.Status)
}

func TestAssignTaskError(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.updateErr = errors.New("update failed")
	c := NewWithStorage(m)

	issue, _ := c.CreateTask(ctx, "t", "", "a", 1)
	m.updateErr = errors.New("update failed") // set after create
	err := c.AssignTask(ctx, issue.ID, "bot", "a")
	require.Error(t, err)
}

func TestCloseTask(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, "Task to close", "", "actor", 2)
	require.NoError(t, err)

	require.NoError(t, c.CloseTask(ctx, issue.ID, "done", "actor"))

	got, err := c.GetTask(ctx, issue.ID)
	require.NoError(t, err)
	require.Equal(t, beadslib.StatusClosed, got.Status)
	require.Equal(t, "done", got.CloseReason)
}

func TestListReadyWork(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	_, err := c.CreateTask(ctx, "Task 1", "", "actor", 1)
	require.NoError(t, err)
	t2, err := c.CreateTask(ctx, "Task 2", "", "actor", 2)
	require.NoError(t, err)
	// Assign Task 2 — it should not appear in ready work.
	require.NoError(t, c.AssignTask(ctx, t2.ID, "somebot", "actor"))

	issues, err := c.ListReadyWork(ctx)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	require.Equal(t, "Task 1", issues[0].Title)
}

func TestRouteToBot(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, "Task to route", "", "actor", 1)
	require.NoError(t, err)

	require.NoError(t, c.RouteToBot(ctx, issue.ID, "targetbot", "actor"))

	got, err := c.GetTask(ctx, issue.ID)
	require.NoError(t, err)
	require.Equal(t, "targetbot", got.Assignee)
}

func TestAddComment(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, "Commented task", "", "actor", 1)
	require.NoError(t, err)

	require.NoError(t, c.AddComment(ctx, issue.ID, "actor", "great task!"))
	require.Len(t, m.comments[issue.ID], 1)
	require.Equal(t, "great task!", m.comments[issue.ID][0].Text)
}

func TestListAll(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	_, err := c.CreateTask(ctx, "T1", "", "a", 1)
	require.NoError(t, err)
	_, err = c.CreateTask(ctx, "T2", "", "a", 1)
	require.NoError(t, err)

	all, err := c.ListAll(ctx, beadslib.IssueFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestGetTaskNotFound(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	_, err := c.GetTask(ctx, "nonexistent-id")
	require.Error(t, err)
}
