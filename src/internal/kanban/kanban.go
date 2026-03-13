// Package kanban provides a simple kanban board model built from Beads issues.
package kanban

import (
	"context"
	"fmt"

	beadslib "github.com/steveyegge/beads"
)

// IssueSearcher is the narrow interface that kanban needs from beads.Storage.
type IssueSearcher interface {
	SearchIssues(ctx context.Context, query string, filter beadslib.IssueFilter) ([]*beadslib.Issue, error)
}

// Board represents the full kanban board.
type Board struct {
	Columns []*Column
}

// Column is a single kanban column (e.g., "backlog", "in_progress").
type Column struct {
	Status string
	Issues []*beadslib.Issue
}

// BuildBoard reads all issues from storage and groups them into columns.
// columns defines the ordered column list (from config.yaml: kanban.columns).
// Issues whose status doesn't match any column are placed in an "other" column.
func BuildBoard(ctx context.Context, storage IssueSearcher, columns []string) (*Board, error) {
	issues, err := storage.SearchIssues(ctx, "", beadslib.IssueFilter{})
	if err != nil {
		return nil, fmt.Errorf("loading issues for kanban: %w", err)
	}

	// Build a map of column name → index for fast lookup.
	colIndex := make(map[string]int, len(columns))
	for i, c := range columns {
		colIndex[c] = i
	}

	cols := make([]*Column, len(columns))
	for i, name := range columns {
		cols[i] = &Column{Status: name}
	}

	var other *Column
	for _, issue := range issues {
		status := string(issue.Status)
		if idx, ok := colIndex[status]; ok {
			cols[idx].Issues = append(cols[idx].Issues, issue)
		} else {
			if other == nil {
				other = &Column{Status: "other"}
			}
			other.Issues = append(other.Issues, issue)
		}
	}

	board := &Board{Columns: cols}
	if other != nil {
		board.Columns = append(board.Columns, other)
	}
	return board, nil
}

// IssueCount returns the total number of issues across all columns.
func (b *Board) IssueCount() int {
	n := 0
	for _, col := range b.Columns {
		n += len(col.Issues)
	}
	return n
}

// Column returns the column with the given status name, or nil if not found.
func (b *Board) Column(status string) *Column {
	for _, col := range b.Columns {
		if col.Status == status {
			return col
		}
	}
	return nil
}
