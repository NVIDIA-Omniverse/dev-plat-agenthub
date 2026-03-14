package beads

import (
	"context"
	"errors"
	"testing"

	beadslib "github.com/steveyegge/beads"
	"github.com/stretchr/testify/require"
)

func TestListReadyWorkError(t *testing.T) {
	m := newMockStorage()
	m.readyWorkErr = errors.New("ready work failed")
	c := NewWithStorage(m)

	_, err := c.ListReadyWork(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing ready work")
}

func TestCloseTaskError(t *testing.T) {
	m := newMockStorage()
	m.closeErr = errors.New("close failed")
	c := NewWithStorage(m)

	issue, _ := c.CreateTask(context.Background(), TaskRequest{Title: "task", Description: "", Actor: "a", Priority: 1})
	err := c.CloseTask(context.Background(), issue.ID, "done", "a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "closing task")
}

func TestListAllError(t *testing.T) {
	m := newMockStorage()
	m.searchErr = errors.New("search failed")
	c := NewWithStorage(m)

	_, err := c.ListAll(context.Background(), beadslib.IssueFilter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing all tasks")
}

func TestAddCommentError(t *testing.T) {
	m := newMockStorage()
	m.commentErr = errors.New("comment failed")
	c := NewWithStorage(m)

	issue, _ := c.CreateTask(context.Background(), TaskRequest{Title: "task", Description: "", Actor: "a", Priority: 1})
	err := c.AddComment(context.Background(), issue.ID, "a", "text")
	require.Error(t, err)
	require.Contains(t, err.Error(), "adding comment")
}

func TestEnsureInitializedSetConfigError(t *testing.T) {
	m := newMockStorage()
	m.setConfigErr = errors.New("set config failed")
	c := NewWithStorage(m)

	err := c.EnsureInitialized(context.Background(), "ah")
	require.Error(t, err)
	require.Contains(t, err.Error(), "initializing beads prefix")
}

func TestEnsureInitializedCreatedAtError(t *testing.T) {
	// First SetConfig (issue_prefix) succeeds; second (created_at) fails.
	m := newMockStorage()
	m.setConfigErr = errors.New("created_at set failed")
	m.setConfigFailAt = 2 // fail on the 2nd call
	c := NewWithStorage(m)

	err := c.EnsureInitialized(context.Background(), "ah")
	require.Error(t, err)
	require.Contains(t, err.Error(), "setting beads created_at")
}

func TestEnsureInitializedAlreadySet(t *testing.T) {
	m := newMockStorage()
	// Pre-set the config so GetConfig succeeds.
	m.configs["issue_prefix"] = "ah"
	c := NewWithStorage(m)

	// Should be idempotent and not call SetConfig.
	require.NoError(t, c.EnsureInitialized(context.Background(), "ah"))
	// Prefix unchanged.
	require.Equal(t, "ah", m.configs["issue_prefix"])
}
