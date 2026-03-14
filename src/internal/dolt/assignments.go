package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateTaskAssignment inserts a new task assignment record.
func (db *DB) CreateTaskAssignment(ctx context.Context, ta TaskAssignment) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO task_assignments
			(id, task_id, project_id, agent_id, assigned_by, assigned_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ta.ID, ta.TaskID, ta.ProjectID, ta.AgentID, ta.AssignedBy, ta.AssignedAt, ta.RevokedAt,
	)
	if err != nil {
		return fmt.Errorf("creating task assignment: %w", err)
	}
	return nil
}

// GetTaskAssignment retrieves a task assignment by ID.
func (db *DB) GetTaskAssignment(ctx context.Context, id string) (*TaskAssignment, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, task_id, project_id, agent_id, assigned_by, assigned_at, revoked_at
		FROM task_assignments WHERE id = ?`, id)
	return scanTaskAssignment(row)
}

// GetActiveAssignmentByTaskAndAgent returns the active (not revoked) assignment
// for a specific task and agent, or nil if none.
func (db *DB) GetActiveAssignmentByTaskAndAgent(ctx context.Context, taskID, agentID string) (*TaskAssignment, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, task_id, project_id, agent_id, assigned_by, assigned_at, revoked_at
		FROM task_assignments
		WHERE task_id = ? AND agent_id = ? AND revoked_at IS NULL
		LIMIT 1`, taskID, agentID)
	return scanTaskAssignment(row)
}

// GetActiveAssignmentByTask returns the active (not revoked) assignment for a task, or nil if none.
func (db *DB) GetActiveAssignmentByTask(ctx context.Context, taskID string) (*TaskAssignment, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, task_id, project_id, agent_id, assigned_by, assigned_at, revoked_at
		FROM task_assignments
		WHERE task_id = ? AND revoked_at IS NULL
		LIMIT 1`, taskID)
	return scanTaskAssignment(row)
}

// RevokeTaskAssignment sets the revoked_at timestamp for an assignment.
func (db *DB) RevokeTaskAssignment(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx, `
		UPDATE task_assignments SET revoked_at = ? WHERE id = ?`, now, id)
	return err
}

// ListActiveAssignmentsByAgent returns all active assignments for an agent.
func (db *DB) ListActiveAssignmentsByAgent(ctx context.Context, agentID string) ([]*TaskAssignment, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, task_id, project_id, agent_id, assigned_by, assigned_at, revoked_at
		FROM task_assignments
		WHERE agent_id = ? AND revoked_at IS NULL
		ORDER BY assigned_at DESC`, agentID)
	if err != nil {
		return nil, fmt.Errorf("listing assignments: %w", err)
	}
	defer rows.Close()
	var out []*TaskAssignment
	for rows.Next() {
		ta, err := scanTaskAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ta)
	}
	return out, rows.Err()
}

func scanTaskAssignment(s scanner) (*TaskAssignment, error) {
	var ta TaskAssignment
	if err := s.Scan(
		&ta.ID, &ta.TaskID, &ta.ProjectID, &ta.AgentID,
		&ta.AssignedBy, &ta.AssignedAt, &ta.RevokedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning task assignment: %w", err)
	}
	return &ta, nil
}
