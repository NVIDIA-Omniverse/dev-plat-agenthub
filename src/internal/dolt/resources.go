package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// CreateResource inserts a new resource record.
func (db *DB) CreateResource(ctx context.Context, r Resource) error {
	meta := r.ResourceMeta
	if meta == nil {
		meta = json.RawMessage("{}")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO resources (id, owner_id, resource_type, name, resource_meta, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.OwnerID, string(r.ResourceType), r.Name, string(meta),
		r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating resource %q: %w", r.Name, err)
	}
	return nil
}

// GetResource retrieves a resource by ID.
func (db *DB) GetResource(ctx context.Context, id string) (*Resource, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, owner_id, resource_type, name, resource_meta, created_at, updated_at
		FROM resources WHERE id = ?`, id)
	return scanResource(row)
}

// ListResourcesByOwner returns all resources for an owner.
func (db *DB) ListResourcesByOwner(ctx context.Context, ownerID string) ([]*Resource, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, owner_id, resource_type, name, resource_meta, created_at, updated_at
		FROM resources WHERE owner_id = ? ORDER BY name`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("listing resources: %w", err)
	}
	defer rows.Close()
	var out []*Resource
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteResource removes a resource by ID.
func (db *DB) DeleteResource(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM resources WHERE id = ?`, id)
	return err
}

// UpdateResourceMeta updates the resource_meta JSON for a resource.
func (db *DB) UpdateResourceMeta(ctx context.Context, id string, meta json.RawMessage) error {
	_, err := db.ExecContext(ctx, `
		UPDATE resources SET resource_meta = ?, updated_at = ? WHERE id = ?`,
		string(meta), time.Now().UTC(), id,
	)
	return err
}

func scanResource(s scanner) (*Resource, error) {
	var r Resource
	var rt string
	var meta string
	if err := s.Scan(
		&r.ID, &r.OwnerID, &rt, &r.Name, &meta,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning resource: %w", err)
	}
	r.ResourceType = ResourceType(rt)
	r.ResourceMeta = json.RawMessage(meta)
	return &r, nil
}
