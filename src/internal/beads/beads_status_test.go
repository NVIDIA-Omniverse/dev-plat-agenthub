package beads

import (
	"context"
	"errors"
	"testing"

	beadslib "github.com/steveyegge/beads"
	"github.com/stretchr/testify/require"
)

func TestUpdateStatusNoNote(t *testing.T) {
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(context.Background(), TaskRequest{Title: "fix bug", Description: "", Actor: "alice", Priority: 1})
	require.NoError(t, err)

	require.NoError(t, c.UpdateStatus(context.Background(), issue.ID, "in_progress", "", "alice"))

	updated, err := c.GetTask(context.Background(), issue.ID)
	require.NoError(t, err)
	require.Equal(t, beadslib.Status("in_progress"), updated.Status)
	require.Empty(t, m.comments[issue.ID])
}

func TestUpdateStatusWithNote(t *testing.T) {
	m := newMockStorage()
	c := NewWithStorage(m)

	issue, err := c.CreateTask(context.Background(), TaskRequest{Title: "deploy service", Description: "", Actor: "bob", Priority: 2})
	require.NoError(t, err)

	require.NoError(t, c.UpdateStatus(context.Background(), issue.ID, "blocked", "waiting on infra", "bob"))

	updated, err := c.GetTask(context.Background(), issue.ID)
	require.NoError(t, err)
	require.Equal(t, beadslib.Status("blocked"), updated.Status)
	require.Len(t, m.comments[issue.ID], 1)
	require.Equal(t, "waiting on infra", m.comments[issue.ID][0].Text)
}

func TestUpdateStatusCommentError(t *testing.T) {
	m := newMockStorage()
	m.commentErr = errors.New("comment storage down")
	c := NewWithStorage(m)

	issue, err := c.CreateTask(context.Background(), TaskRequest{Title: "task", Description: "", Actor: "a", Priority: 1})
	require.NoError(t, err)

	err = c.UpdateStatus(context.Background(), issue.ID, "done", "closing note", "a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "adding status note")
}

func TestUpdateStatusUpdateError(t *testing.T) {
	m := newMockStorage()
	m.updateErr = errors.New("update storage down")
	c := NewWithStorage(m)

	issue, err := c.CreateTask(context.Background(), TaskRequest{Title: "task", Description: "", Actor: "a", Priority: 1})
	require.NoError(t, err)

	err = c.UpdateStatus(context.Background(), issue.ID, "done", "", "a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "updating status of task")
}
