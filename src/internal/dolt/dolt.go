// Package dolt manages the agenthub database schema via a Dolt SQL server.
//
// agenthub's own data (bot registry, etc.) lives in a Dolt SQL server
// (MySQL-compatible). Beads manages its own separate embedded Dolt database.
//
// The schema is managed via SQL migration files embedded in this package.
// Migrations are idempotent: running them twice is safe.
package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// MySQL-compatible driver for Dolt.
	_ "github.com/go-sql-driver/mysql"
)

// DB wraps *sql.DB with agenthub-specific methods.
type DB struct {
	*sql.DB
}

// NewDB wraps an existing *sql.DB. Used in tests to inject a mock database.
func NewDB(db *sql.DB) *DB { return &DB{db} }

// Open opens a connection to the Dolt SQL server at dsn and verifies connectivity.
// Call Migrate after opening to ensure the schema is up-to-date.
func Open(dsn string) (*DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening dolt connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging dolt server: %w", err)
	}

	return &DB{db}, nil
}

// Migrate runs all schema migrations. It is idempotent.
func (db *DB) Migrate(ctx context.Context) error {
	for _, m := range migrations {
		if _, err := db.ExecContext(ctx, m.SQL); err != nil {
			return fmt.Errorf("migration %q: %w", m.Name, err)
		}
	}
	return nil
}

// migration is a named SQL statement.
type migration struct {
	Name string
	SQL  string
}

// migrations contains all schema migrations in order.
// Each statement must be idempotent (use IF NOT EXISTS, etc.).
var migrations = []migration{
	{
		Name: "001_create_openclaw_instances",
		SQL: `CREATE TABLE IF NOT EXISTS openclaw_instances (
			id                VARCHAR(36)  NOT NULL,
			name              VARCHAR(255) NOT NULL,
			host              VARCHAR(255) NOT NULL,
			port              INT          NOT NULL,
			owner_slack_user  VARCHAR(255) NOT NULL,
			channel_id        VARCHAR(255) NOT NULL,
			chatty            TINYINT(1)   NOT NULL DEFAULT 0,
			last_seen_at      TIMESTAMP    NULL,
			is_alive          TINYINT(1)   NOT NULL DEFAULT 0,
			created_at        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_name_channel (name, channel_id)
		)`,
	},
}

// Instance represents a registered openclaw bot.
type Instance struct {
	ID             string
	Name           string
	Host           string
	Port           int
	OwnerSlackUser string
	ChannelID      string
	Chatty         bool
	LastSeenAt     *time.Time
	IsAlive        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateInstance inserts a new openclaw instance into the registry.
func (db *DB) CreateInstance(ctx context.Context, inst Instance) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO openclaw_instances
			(id, name, host, port, owner_slack_user, channel_id, chatty, is_alive)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		inst.ID, inst.Name, inst.Host, inst.Port,
		inst.OwnerSlackUser, inst.ChannelID, boolToInt(inst.Chatty), boolToInt(inst.IsAlive),
	)
	if err != nil {
		return fmt.Errorf("creating instance %q: %w", inst.Name, err)
	}
	return nil
}

// GetInstance retrieves an instance by name and channel.
func (db *DB) GetInstance(ctx context.Context, name, channelID string) (*Instance, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, name, host, port, owner_slack_user, channel_id,
		       chatty, last_seen_at, is_alive, created_at, updated_at
		FROM openclaw_instances
		WHERE name = ? AND channel_id = ?`,
		name, channelID,
	)
	return scanInstance(row)
}

// ListInstances returns all instances for a channel.
func (db *DB) ListInstances(ctx context.Context, channelID string) ([]*Instance, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, host, port, owner_slack_user, channel_id,
		       chatty, last_seen_at, is_alive, created_at, updated_at
		FROM openclaw_instances
		WHERE channel_id = ?
		ORDER BY name`,
		channelID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing instances: %w", err)
	}
	defer rows.Close()

	var instances []*Instance
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, inst)
	}
	return instances, rows.Err()
}

// ListAllInstances returns all registered instances across all channels.
func (db *DB) ListAllInstances(ctx context.Context) ([]*Instance, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, host, port, owner_slack_user, channel_id,
		       chatty, last_seen_at, is_alive, created_at, updated_at
		FROM openclaw_instances
		ORDER BY channel_id, name`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing all instances: %w", err)
	}
	defer rows.Close()

	var instances []*Instance
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, inst)
	}
	return instances, rows.Err()
}

// UpdateAlive updates the is_alive and last_seen_at fields for an instance.
func (db *DB) UpdateAlive(ctx context.Context, id string, alive bool) error {
	now := time.Now()
	_, err := db.ExecContext(ctx, `
		UPDATE openclaw_instances
		SET is_alive = ?, last_seen_at = ?, updated_at = ?
		WHERE id = ?`,
		boolToInt(alive), now, now, id,
	)
	return err
}

// UpdateChatty sets the chatty flag for an instance.
func (db *DB) UpdateChatty(ctx context.Context, name, channelID string, chatty bool) error {
	_, err := db.ExecContext(ctx, `
		UPDATE openclaw_instances SET chatty = ? WHERE name = ? AND channel_id = ?`,
		boolToInt(chatty), name, channelID,
	)
	return err
}

// DeleteInstance removes an instance from the registry by name and channel.
func (db *DB) DeleteInstance(ctx context.Context, name, channelID string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM openclaw_instances WHERE name = ? AND channel_id = ?`,
		name, channelID,
	)
	return err
}

// DeleteInstanceByName removes all instances with the given name (across all channels).
// Used by the admin UI which operates globally without a channel filter.
func (db *DB) DeleteInstanceByName(ctx context.Context, name string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM openclaw_instances WHERE name = ?`, name)
	return err
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanInstance(s scanner) (*Instance, error) {
	var inst Instance
	var chatty, isAlive int
	if err := s.Scan(
		&inst.ID, &inst.Name, &inst.Host, &inst.Port,
		&inst.OwnerSlackUser, &inst.ChannelID,
		&chatty, &inst.LastSeenAt, &isAlive,
		&inst.CreatedAt, &inst.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("instance not found")
		}
		return nil, fmt.Errorf("scanning instance: %w", err)
	}
	inst.Chatty = chatty != 0
	inst.IsAlive = isAlive != 0
	return &inst, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
