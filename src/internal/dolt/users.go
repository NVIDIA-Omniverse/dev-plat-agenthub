package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateUser inserts a new user record.
func (db *DB) CreateUser(ctx context.Context, u User) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, email, role, api_token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Email, u.Role, u.APIToken, u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating user %q: %w", u.Username, err)
	}
	return nil
}

// GetUserByUsername looks up a user by username.
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, username, email, role, api_token, created_at, updated_at
		FROM users WHERE username = ?`, username)
	return scanUser(row)
}

// GetUserByID looks up a user by ID.
func (db *DB) GetUserByID(ctx context.Context, id string) (*User, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, username, email, role, api_token, created_at, updated_at
		FROM users WHERE id = ?`, id)
	return scanUser(row)
}

// UpdateUserAPIToken sets the (hashed) API token for a user.
func (db *DB) UpdateUserAPIToken(ctx context.Context, userID, hashedToken string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE users SET api_token = ?, updated_at = ? WHERE id = ?`,
		hashedToken, time.Now().UTC(), userID,
	)
	return err
}

// EnsureAdminUser inserts the bootstrap admin user if no users exist.
func (db *DB) EnsureAdminUser(ctx context.Context) error {
	var count int
	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`)
	if err := row.Scan(&count); err != nil {
		return fmt.Errorf("counting users: %w", err)
	}
	if count > 0 {
		return nil
	}
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx, `
		INSERT IGNORE INTO users (id, username, email, role, api_token, created_at, updated_at)
		VALUES ('admin-bootstrap-user', 'admin', '', 'admin', '', ?, ?)`,
		now, now,
	)
	return err
}

func scanUser(s scanner) (*User, error) {
	var u User
	if err := s.Scan(
		&u.ID, &u.Username, &u.Email, &u.Role, &u.APIToken,
		&u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	return &u, nil
}
