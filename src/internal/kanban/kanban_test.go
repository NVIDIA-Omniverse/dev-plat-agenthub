package kanban

import (
	"context"
	"testing"

	beadslib "github.com/steveyegge/beads"
	"github.com/stretchr/testify/require"
)

// mockSearcher implements IssueSearcher for kanban tests.
type mockSearcher struct {
	issues []*beadslib.Issue
}

func (m *mockSearcher) SearchIssues(_ context.Context, _ string, _ beadslib.IssueFilter) ([]*beadslib.Issue, error) {
	return m.issues, nil
}

var _ IssueSearcher = (*mockSearcher)(nil) // compile-time check

// Tests

func TestBuildBoardEmpty(t *testing.T) {
	m := &mockSearcher{}
	columns := []string{"backlog", "open", "in_progress", "done"}

	board, err := BuildBoard(context.Background(), m, columns)
	require.NoError(t, err)
	require.NotNil(t, board)
	require.Len(t, board.Columns, len(columns))
	require.Equal(t, 0, board.IssueCount())
}

func TestBuildBoardWithIssues(t *testing.T) {
	m := &mockSearcher{
		issues: []*beadslib.Issue{
			{ID: "1", Title: "Task 1", Status: beadslib.StatusOpen},
			{ID: "2", Title: "Task 2", Status: beadslib.StatusInProgress},
		},
	}

	columns := []string{"open", "in_progress", "done"}
	board, err := BuildBoard(context.Background(), m, columns)
	require.NoError(t, err)
	require.Equal(t, 2, board.IssueCount())

	openCol := board.Column("open")
	require.NotNil(t, openCol)
	require.Len(t, openCol.Issues, 1)
	require.Equal(t, "Task 1", openCol.Issues[0].Title)

	inProgressCol := board.Column("in_progress")
	require.NotNil(t, inProgressCol)
	require.Len(t, inProgressCol.Issues, 1)
	require.Equal(t, "Task 2", inProgressCol.Issues[0].Title)

	doneCol := board.Column("done")
	require.NotNil(t, doneCol)
	require.Empty(t, doneCol.Issues)
}

func TestBuildBoardUnknownStatusGoesToOther(t *testing.T) {
	m := &mockSearcher{
		issues: []*beadslib.Issue{
			{ID: "1", Title: "Blocked task", Status: beadslib.StatusBlocked},
		},
	}

	// "blocked" is not in the columns list.
	columns := []string{"open", "in_progress", "done"}
	board, err := BuildBoard(context.Background(), m, columns)
	require.NoError(t, err)

	otherCol := board.Column("other")
	require.NotNil(t, otherCol, "issues with unknown status should go to 'other' column")
	require.Len(t, otherCol.Issues, 1)
	require.Equal(t, 1, board.IssueCount())
}

func TestColumnReturnsNilForMissing(t *testing.T) {
	board := &Board{Columns: []*Column{{Status: "open"}}}
	require.Nil(t, board.Column("nonexistent"))
}

func TestIssueCountAcrossColumns(t *testing.T) {
	board := &Board{
		Columns: []*Column{
			{Status: "a", Issues: make([]*beadslib.Issue, 3)},
			{Status: "b", Issues: make([]*beadslib.Issue, 2)},
			{Status: "c"},
		},
	}
	require.Equal(t, 5, board.IssueCount())
}
