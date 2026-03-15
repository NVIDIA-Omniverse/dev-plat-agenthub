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
	"github.com/go-sql-driver/mysql"
)

// DB wraps *sql.DB with agenthub-specific methods.
type DB struct {
	*sql.DB
}

// NewDB wraps an existing *sql.DB. Used in tests to inject a mock database.
func NewDB(db *sql.DB) *DB { return &DB{db} }

// Open opens a connection to the Dolt SQL server at dsn, creating the target
// database if it does not already exist, and verifies connectivity.
// Call Migrate after opening to ensure the schema is up-to-date.
func Open(dsn string) (*DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing dolt dsn: %w", err)
	}

	// Ensure TIMESTAMP columns are scanned as time.Time, not []byte.
	cfg.ParseTime = true

	// ensureDatabase clears cfg.DBName; save and restore it afterwards.
	dbName := cfg.DBName
	if err := ensureDatabase(cfg); err != nil {
		return nil, err
	}
	cfg.DBName = dbName

	db, err := sql.Open("mysql", cfg.FormatDSN())
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

// ensureDatabase connects to the Dolt server without a database and creates
// the target database if it does not already exist.
func ensureDatabase(cfg *mysql.Config) error {
	dbName := cfg.DBName
	cfg.DBName = ""

	bootstrap, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("opening bootstrap dolt connection: %w", err)
	}
	defer bootstrap.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := bootstrap.PingContext(ctx); err != nil {
		return fmt.Errorf("pinging dolt server: %w", err)
	}

	_, err = bootstrap.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+dbName+"`")
	if err != nil {
		return fmt.Errorf("creating database %q: %w", dbName, err)
	}

	return nil
}

// Migrate runs all schema migrations. Each migration is tracked in the
// schema_migrations table and run at most once.
func (db *DB) Migrate(ctx context.Context) error {
	// Bootstrap the migrations tracking table first.
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       VARCHAR(255) NOT NULL,
		applied_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (name)
	)`)
	if err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	// Load already-applied migrations.
	rows, err := db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("reading schema_migrations: %w", err)
	}
	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("scanning schema_migrations: %w", err)
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating schema_migrations: %w", err)
	}

	for _, m := range migrations {
		if applied[m.Name] {
			continue
		}
		if _, err := db.ExecContext(ctx, m.SQL); err != nil {
			return fmt.Errorf("migration %q: %w", m.Name, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT IGNORE INTO schema_migrations (name) VALUES (?)`, m.Name); err != nil {
			return fmt.Errorf("recording migration %q: %w", m.Name, err)
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
	{
		Name: "002_create_bot_capacity",
		SQL: `CREATE TABLE IF NOT EXISTS bot_capacity (
			bot_id       VARCHAR(36)  NOT NULL,
			gpu_free_mb  INT          NOT NULL DEFAULT 0,
			jobs_queued  INT          NOT NULL DEFAULT 0,
			jobs_running INT          NOT NULL DEFAULT 0,
			updated_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (bot_id)
		)`,
	},
	{
		Name: "003_create_users",
		SQL: `CREATE TABLE IF NOT EXISTS users (
			id           VARCHAR(36)  NOT NULL,
			username     VARCHAR(64)  NOT NULL,
			email        VARCHAR(255) NOT NULL DEFAULT '',
			role         VARCHAR(32)  NOT NULL DEFAULT 'user',
			api_token    VARCHAR(64)  NOT NULL DEFAULT '',
			created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_username (username)
		)`,
	},
	{
		Name: "004_create_resources",
		SQL: `CREATE TABLE IF NOT EXISTS resources (
			id            VARCHAR(36)  NOT NULL,
			owner_id      VARCHAR(36)  NOT NULL,
			resource_type VARCHAR(64)  NOT NULL,
			name          VARCHAR(255) NOT NULL,
			resource_meta JSON         NOT NULL DEFAULT ('{}'),
			created_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			KEY idx_resources_owner (owner_id)
		)`,
	},
	{
		Name: "005_create_projects",
		SQL: `CREATE TABLE IF NOT EXISTS projects (
			id                 VARCHAR(36)  NOT NULL,
			owner_id           VARCHAR(36)  NOT NULL,
			name               VARCHAR(255) NOT NULL,
			description        TEXT         NOT NULL DEFAULT '',
			slack_channel_id   VARCHAR(64)  NOT NULL DEFAULT '',
			slack_channel_name VARCHAR(255) NOT NULL DEFAULT '',
			beads_prefix       VARCHAR(16)  NOT NULL DEFAULT 'AH',
			created_at         TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at         TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			KEY idx_projects_owner (owner_id)
		)`,
	},
	{
		Name: "006_create_project_resources",
		SQL: `CREATE TABLE IF NOT EXISTS project_resources (
			project_id  VARCHAR(36)  NOT NULL,
			resource_id VARCHAR(36)  NOT NULL,
			is_primary  TINYINT(1)   NOT NULL DEFAULT 0,
			added_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (project_id, resource_id)
		)`,
	},
	{
		Name: "007_alter_openclaw_instances",
		SQL: `ALTER TABLE openclaw_instances
			ADD COLUMN user_id     VARCHAR(36)  NOT NULL DEFAULT '',
			ADD COLUMN description TEXT         NOT NULL DEFAULT '',
			ADD COLUMN skills      JSON         NOT NULL DEFAULT ('[]')`,
	},
	{
		Name: "008_create_project_agents",
		SQL: `CREATE TABLE IF NOT EXISTS project_agents (
			project_id VARCHAR(36)  NOT NULL,
			agent_id   VARCHAR(36)  NOT NULL,
			granted_by VARCHAR(36)  NOT NULL DEFAULT '',
			granted_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (project_id, agent_id)
		)`,
	},
	{
		Name: "009_create_task_assignments",
		SQL: `CREATE TABLE IF NOT EXISTS task_assignments (
			id          VARCHAR(36)  NOT NULL,
			task_id     VARCHAR(64)  NOT NULL,
			project_id  VARCHAR(36)  NOT NULL DEFAULT '',
			agent_id    VARCHAR(36)  NOT NULL,
			assigned_by VARCHAR(36)  NOT NULL DEFAULT '',
			assigned_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			revoked_at  TIMESTAMP    NULL,
			PRIMARY KEY (id),
			KEY idx_ta_task (task_id),
			KEY idx_ta_agent (agent_id)
		)`,
	},
	{
		Name: "010_alter_openclaw_instances_status",
		SQL: `ALTER TABLE openclaw_instances
			ADD COLUMN current_task   VARCHAR(255) NOT NULL DEFAULT '',
			ADD COLUMN agent_status   VARCHAR(64)  NOT NULL DEFAULT 'idle',
			ADD COLUMN status_message TEXT         NOT NULL DEFAULT ''`,
	},
	{
		Name: "011_create_inbox_messages",
		SQL: `CREATE TABLE IF NOT EXISTS inbox_messages (
			id           VARCHAR(64)  NOT NULL,
			bot_name     VARCHAR(255) NOT NULL,
			from_user    VARCHAR(255) NOT NULL DEFAULT '',
			channel      VARCHAR(255) NOT NULL DEFAULT '',
			body         TEXT         NOT NULL DEFAULT '',
			task_context JSON         NOT NULL DEFAULT ('{}'),
			created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			acked_at     TIMESTAMP    NULL,
			PRIMARY KEY (id),
			KEY idx_bot_name_acked (bot_name, acked_at)
		)`,
	},
	{
		Name: "012_create_settings",
		SQL: `CREATE TABLE IF NOT EXISTS settings (
			key_name   VARCHAR(255) NOT NULL,
			value      TEXT         NOT NULL DEFAULT '',
			updated_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (key_name)
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

// UpdateAliveByName updates is_alive and last_seen_at for an instance by name.
func (db *DB) UpdateAliveByName(ctx context.Context, name string, alive bool) error {
	now := time.Now()
	_, err := db.ExecContext(ctx, `
		UPDATE openclaw_instances
		SET is_alive = ?, last_seen_at = ?, updated_at = ?
		WHERE name = ?`,
		boolToInt(alive), now, now, name,
	)
	return err
}

// UpdateHeartbeat persists all heartbeat fields for an instance by name.
// Sets is_alive=1, last_seen_at=NOW(), and the status fields.
func (db *DB) UpdateHeartbeat(ctx context.Context, name, currentTask, status, message string) error {
	now := time.Now()
	_, err := db.ExecContext(ctx, `
		UPDATE openclaw_instances
		SET is_alive = 1, last_seen_at = ?, current_task = ?, agent_status = ?, status_message = ?, updated_at = ?
		WHERE name = ?`,
		now, currentTask, status, message, now, name,
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

// Capacity holds the live capacity report for a bot instance.
type Capacity struct {
	BotID       string
	GPUFreeMB   int
	JobsQueued  int
	JobsRunning int
	UpdatedAt   time.Time
}

// UpdateCapacity upserts the capacity record for a bot.
func (db *DB) UpdateCapacity(ctx context.Context, id string, cap Capacity) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO bot_capacity (bot_id, gpu_free_mb, jobs_queued, jobs_running)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			gpu_free_mb  = VALUES(gpu_free_mb),
			jobs_queued  = VALUES(jobs_queued),
			jobs_running = VALUES(jobs_running),
			updated_at   = NOW()`,
		id, cap.GPUFreeMB, cap.JobsQueued, cap.JobsRunning,
	)
	if err != nil {
		return fmt.Errorf("updating capacity for %q: %w", id, err)
	}
	return nil
}

// GetCapacity returns the capacity record for a bot, or nil if none exists.
func (db *DB) GetCapacity(ctx context.Context, botID string) (*Capacity, error) {
	row := db.QueryRowContext(ctx, `
		SELECT bot_id, gpu_free_mb, jobs_queued, jobs_running, updated_at
		FROM bot_capacity WHERE bot_id = ?`, botID)
	var c Capacity
	if err := row.Scan(&c.BotID, &c.GPUFreeMB, &c.JobsQueued, &c.JobsRunning, &c.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning capacity: %w", err)
	}
	return &c, nil
}

// GetAllCapacities returns all bot capacity records keyed by bot ID.
func (db *DB) GetAllCapacities(ctx context.Context) (map[string]*Capacity, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT bot_id, gpu_free_mb, jobs_queued, jobs_running, updated_at FROM bot_capacity`)
	if err != nil {
		return nil, fmt.Errorf("querying capacities: %w", err)
	}
	defer rows.Close()

	result := make(map[string]*Capacity)
	for rows.Next() {
		var c Capacity
		if err := rows.Scan(&c.BotID, &c.GPUFreeMB, &c.JobsQueued, &c.JobsRunning, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning capacity row: %w", err)
		}
		result[c.BotID] = &c
	}
	return result, rows.Err()
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
