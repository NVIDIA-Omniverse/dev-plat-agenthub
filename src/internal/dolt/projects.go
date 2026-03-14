package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateProject inserts a new project.
func (db *DB) CreateProject(ctx context.Context, p Project) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO projects
			(id, owner_id, name, description, slack_channel_id, slack_channel_name, beads_prefix, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.OwnerID, p.Name, p.Description,
		p.SlackChannelID, p.SlackChannelName, p.BeadsPrefix,
		p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating project %q: %w", p.Name, err)
	}
	return nil
}

// GetProject retrieves a project by ID.
func (db *DB) GetProject(ctx context.Context, id string) (*Project, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, owner_id, name, description, slack_channel_id, slack_channel_name, beads_prefix, created_at, updated_at
		FROM projects WHERE id = ?`, id)
	return scanProject(row)
}

// GetProjectByBeadsPrefix looks up a project by its beads prefix (e.g. "AH").
func (db *DB) GetProjectByBeadsPrefix(ctx context.Context, prefix string) (*Project, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, owner_id, name, description, slack_channel_id, slack_channel_name, beads_prefix, created_at, updated_at
		FROM projects WHERE beads_prefix = ? LIMIT 1`, prefix)
	return scanProject(row)
}

// ListProjectsByOwner returns all projects owned by a user.
func (db *DB) ListProjectsByOwner(ctx context.Context, ownerID string) ([]*Project, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, owner_id, name, description, slack_channel_id, slack_channel_name, beads_prefix, created_at, updated_at
		FROM projects WHERE owner_id = ? ORDER BY name`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListAllProjects returns all projects.
func (db *DB) ListAllProjects(ctx context.Context) ([]*Project, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, owner_id, name, description, slack_channel_id, slack_channel_name, beads_prefix, created_at, updated_at
		FROM projects ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing all projects: %w", err)
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProject updates the mutable fields of a project.
func (db *DB) UpdateProject(ctx context.Context, p Project) error {
	_, err := db.ExecContext(ctx, `
		UPDATE projects
		SET owner_id = ?, name = ?, description = ?,
		    slack_channel_id = ?, slack_channel_name = ?, beads_prefix = ?,
		    updated_at = ?
		WHERE id = ?`,
		p.OwnerID, p.Name, p.Description,
		p.SlackChannelID, p.SlackChannelName, p.BeadsPrefix,
		time.Now().UTC(), p.ID,
	)
	return err
}

// AddProjectResource attaches a resource to a project.
func (db *DB) AddProjectResource(ctx context.Context, projectID, resourceID string, isPrimary bool) error {
	_, err := db.ExecContext(ctx, `
		INSERT IGNORE INTO project_resources (project_id, resource_id, is_primary, added_at)
		VALUES (?, ?, ?, ?)`,
		projectID, resourceID, boolToInt(isPrimary), time.Now().UTC(),
	)
	return err
}

// RemoveProjectResource detaches a resource from a project.
func (db *DB) RemoveProjectResource(ctx context.Context, projectID, resourceID string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM project_resources WHERE project_id = ? AND resource_id = ?`,
		projectID, resourceID,
	)
	return err
}

// ListProjectResources returns all resource links for a project.
func (db *DB) ListProjectResources(ctx context.Context, projectID string) ([]*ProjectResource, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT project_id, resource_id, is_primary, added_at
		FROM project_resources WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing project resources: %w", err)
	}
	defer rows.Close()
	var out []*ProjectResource
	for rows.Next() {
		var pr ProjectResource
		var isPrimary int
		if err := rows.Scan(&pr.ProjectID, &pr.ResourceID, &isPrimary, &pr.AddedAt); err != nil {
			return nil, fmt.Errorf("scanning project resource: %w", err)
		}
		pr.IsPrimary = isPrimary != 0
		out = append(out, &pr)
	}
	return out, rows.Err()
}

// AddProjectAgent grants an agent access to a project.
func (db *DB) AddProjectAgent(ctx context.Context, projectID, agentID, grantedBy string) error {
	_, err := db.ExecContext(ctx, `
		INSERT IGNORE INTO project_agents (project_id, agent_id, granted_by, granted_at)
		VALUES (?, ?, ?, ?)`,
		projectID, agentID, grantedBy, time.Now().UTC(),
	)
	return err
}

// RemoveProjectAgent revokes an agent's access to a project.
func (db *DB) RemoveProjectAgent(ctx context.Context, projectID, agentID string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM project_agents WHERE project_id = ? AND agent_id = ?`,
		projectID, agentID,
	)
	return err
}

// ListProjectAgents returns all agents authorized for a project.
func (db *DB) ListProjectAgents(ctx context.Context, projectID string) ([]*ProjectAgent, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT project_id, agent_id, granted_by, granted_at
		FROM project_agents WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing project agents: %w", err)
	}
	defer rows.Close()
	var out []*ProjectAgent
	for rows.Next() {
		var pa ProjectAgent
		if err := rows.Scan(&pa.ProjectID, &pa.AgentID, &pa.GrantedBy, &pa.GrantedAt); err != nil {
			return nil, fmt.Errorf("scanning project agent: %w", err)
		}
		out = append(out, &pa)
	}
	return out, rows.Err()
}

func scanProject(s scanner) (*Project, error) {
	var p Project
	if err := s.Scan(
		&p.ID, &p.OwnerID, &p.Name, &p.Description,
		&p.SlackChannelID, &p.SlackChannelName, &p.BeadsPrefix,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning project: %w", err)
	}
	return &p, nil
}
