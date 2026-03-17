package beads

import (
	"context"
	"errors"
	"testing"
	"time"

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

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "Fix the bug", Description: "A description", Actor: "U123", Priority: 1})
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

	_, err := c.CreateTask(ctx, TaskRequest{Title: "title", Description: "", Actor: "actor", Priority: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "db error")
}

func TestAssignTask(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "Task to assign", Description: "", Actor: "actor", Priority: 2})
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

	issue, _ := c.CreateTask(ctx, TaskRequest{Title: "t", Description: "", Actor: "a", Priority: 1})
	m.updateErr = errors.New("update failed") // set after create
	err := c.AssignTask(ctx, issue.ID, "bot", "a")
	require.Error(t, err)
}

func TestCloseTask(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "Task to close", Description: "", Actor: "actor", Priority: 2})
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

	_, err := c.CreateTask(ctx, TaskRequest{Title: "Task 1", Description: "", Actor: "actor", Priority: 1})
	require.NoError(t, err)
	t2, err := c.CreateTask(ctx, TaskRequest{Title: "Task 2", Description: "", Actor: "actor", Priority: 2})
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

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "Task to route", Description: "", Actor: "actor", Priority: 1})
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

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "Commented task", Description: "", Actor: "actor", Priority: 1})
	require.NoError(t, err)

	require.NoError(t, c.AddComment(ctx, issue.ID, "actor", "great task!"))
	require.Len(t, m.comments[issue.ID], 1)
	require.Equal(t, "great task!", m.comments[issue.ID][0].Text)
}

func TestListAll(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	_, err := c.CreateTask(ctx, TaskRequest{Title: "T1", Description: "", Actor: "a", Priority: 1})
	require.NoError(t, err)
	_, err = c.CreateTask(ctx, TaskRequest{Title: "T2", Description: "", Actor: "a", Priority: 1})
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

func TestNew_Error(t *testing.T) {
	ctx := context.Background()
	_, err := New(ctx, "/nonexistent/path/dolt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "opening beads db")
}

func TestUpdateFields_Success(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "T", Actor: "a"})
	require.NoError(t, err)

	err = c.UpdateFields(ctx, issue.ID, "New Title", "desc", "in_progress",
		2, "bug", "bot1", 30, "criteria", "notes", "2026-06-15", "p1,p2", "actor")
	require.NoError(t, err)

	require.Equal(t, "New Title", m.lastUpdates["title"])
	require.Equal(t, beadslib.Status("in_progress"), m.lastUpdates["status"])
	require.Equal(t, beadslib.IssueType("bug"), m.lastUpdates["issue_type"])
	require.Equal(t, 2, m.lastUpdates["priority"])
	require.Equal(t, 30, m.lastUpdates["estimated_minutes"])
	require.Equal(t, []string{"p1", "p2"}, m.lastUpdates["labels"])
	require.NotNil(t, m.lastUpdates["due_at"])
}

func TestUpdateFields_MinimalFields(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "T", Actor: "a"})
	require.NoError(t, err)

	err = c.UpdateFields(ctx, issue.ID, "", "desc", "", -1, "", "", 0, "", "", "", "", "actor")
	require.NoError(t, err)

	_, hasTitle := m.lastUpdates["title"]
	require.False(t, hasTitle)
	_, hasStatus := m.lastUpdates["status"]
	require.False(t, hasStatus)
	_, hasType := m.lastUpdates["issue_type"]
	require.False(t, hasType)
	_, hasPriority := m.lastUpdates["priority"]
	require.False(t, hasPriority)
	_, hasEst := m.lastUpdates["estimated_minutes"]
	require.False(t, hasEst)
	_, hasDue := m.lastUpdates["due_at"]
	require.False(t, hasDue)
	_, hasLabels := m.lastUpdates["labels"]
	require.False(t, hasLabels)
}

func TestUpdateFields_Error(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "T", Actor: "a"})
	require.NoError(t, err)

	m.updateErr = errors.New("update failed")
	err = c.UpdateFields(ctx, issue.ID, "title", "", "", 0, "", "", 0, "", "", "", "", "a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "updating task")
}

func TestUpdateFields_InvalidDueAt(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "T", Actor: "a"})
	require.NoError(t, err)

	err = c.UpdateFields(ctx, issue.ID, "", "", "", -1, "", "", 0, "", "", "not-a-date", "", "a")
	require.NoError(t, err)
	_, hasDue := m.lastUpdates["due_at"]
	require.False(t, hasDue)
}

func TestCreateTask_WithAllOptionalFields(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{
		Title:            "Full task",
		Description:      "desc",
		Status:           "in_progress",
		IssueType:        "bug",
		Priority:         1,
		Assignee:         "bot1",
		EstimatedMinutes: 60,
		DueAt:            "2026-12-25",
		Labels:           "urgent, backend, api",
		Actor:            "user1",
	})
	require.NoError(t, err)
	require.Equal(t, beadslib.Status("in_progress"), issue.Status)
	require.Equal(t, beadslib.IssueType("bug"), issue.IssueType)
	require.NotNil(t, issue.EstimatedMinutes)
	require.Equal(t, 60, *issue.EstimatedMinutes)
	require.NotNil(t, issue.DueAt)
	expected, _ := time.Parse("2006-01-02", "2026-12-25")
	require.Equal(t, expected, *issue.DueAt)
	require.Equal(t, []string{"urgent", "backend", "api"}, issue.Labels)
}

func TestCreateTask_InvalidDueAt(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{
		Title: "Bad date",
		DueAt: "not-a-date",
		Actor: "a",
	})
	require.NoError(t, err)
	require.Nil(t, issue.DueAt)
}

func TestCreateTask_EmptyLabels(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{
		Title:  "No labels",
		Labels: "  ,  , ",
		Actor:  "a",
	})
	require.NoError(t, err)
	require.Empty(t, issue.Labels)
}

func TestEnsureInitialized_GetConfigError(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.getConfigErr = errors.New("config read failed")
	c := NewWithStorage(m)

	err := c.EnsureInitialized(ctx, "ah")
	require.Error(t, err)
	require.Contains(t, err.Error(), "checking beads init")
}

func TestEnsureInitialized_SetConfigCreatedAtFails(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.setConfigErr = errors.New("write failed")
	m.setConfigFailAt = 2
	c := NewWithStorage(m)

	err := c.EnsureInitialized(ctx, "ah")
	require.Error(t, err)
	require.Contains(t, err.Error(), "setting beads created_at")
}

func TestListReadyWork_Error(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.readyWorkErr = errors.New("db error")
	c := NewWithStorage(m)

	_, err := c.ListReadyWork(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing ready work")
}

func TestCloseTask_Error(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.closeErr = errors.New("close failed")
	c := NewWithStorage(m)

	err := c.CloseTask(ctx, "some-id", "done", "actor")
	require.Error(t, err)
	require.Contains(t, err.Error(), "closing task")
}

func TestListAll_Error(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.searchErr = errors.New("search failed")
	c := NewWithStorage(m)

	_, err := c.ListAll(ctx, beadslib.IssueFilter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing all tasks")
}

func TestAddComment_Error(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	m.commentErr = errors.New("comment failed")
	c := NewWithStorage(m)

	err := c.AddComment(ctx, "some-id", "author", "text")
	require.Error(t, err)
	require.Contains(t, err.Error(), "adding comment")
}

func TestUpdateStatus_WithNote(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "T", Actor: "a"})
	require.NoError(t, err)

	err = c.UpdateStatus(ctx, issue.ID, "in_progress", "starting work", "actor")
	require.NoError(t, err)
	require.Len(t, m.comments[issue.ID], 1)
	require.Equal(t, "starting work", m.comments[issue.ID][0].Text)
}

func TestUpdateStatus_CommentError(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "T", Actor: "a"})
	require.NoError(t, err)

	m.commentErr = errors.New("comment failed")
	err = c.UpdateStatus(ctx, issue.ID, "in_progress", "note", "actor")
	require.Error(t, err)
	require.Contains(t, err.Error(), "adding status note")
}

func TestUpdateStatus_UpdateError(t *testing.T) {
	ctx := context.Background()
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(ctx, TaskRequest{Title: "T", Actor: "a"})
	require.NoError(t, err)

	m.updateErr = errors.New("update failed")
	err = c.UpdateStatus(ctx, issue.ID, "in_progress", "", "actor")
	require.Error(t, err)
	require.Contains(t, err.Error(), "updating status")
}
