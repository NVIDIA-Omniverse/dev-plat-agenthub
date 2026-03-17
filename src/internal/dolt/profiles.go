package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// UpsertBotProfile inserts or updates a bot profile.
func (db *DB) UpsertBotProfile(ctx context.Context, p BotProfile) error {
	specs, _ := json.Marshal(p.Specializations)
	if specs == nil {
		specs = []byte("[]")
	}
	tools, _ := json.Marshal(p.Tools)
	if tools == nil {
		tools = []byte("[]")
	}
	hardware := p.Hardware
	if len(hardware) == 0 {
		hardware = json.RawMessage("{}")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO bot_profiles
			(bot_name, description, specializations, tools, hardware, max_concurrent_tasks, owner_contact, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			description          = VALUES(description),
			specializations      = VALUES(specializations),
			tools                = VALUES(tools),
			hardware             = VALUES(hardware),
			max_concurrent_tasks = VALUES(max_concurrent_tasks),
			owner_contact        = VALUES(owner_contact),
			updated_at           = VALUES(updated_at)`,
		p.BotName, p.Description, string(specs), string(tools), string(hardware),
		p.MaxConcurrentTasks, p.OwnerContact, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting bot profile %q: %w", p.BotName, err)
	}
	return nil
}

// GetBotProfile retrieves a bot profile by name.
func (db *DB) GetBotProfile(ctx context.Context, botName string) (*BotProfile, error) {
	row := db.QueryRowContext(ctx, `
		SELECT bot_name, description, specializations, tools, hardware,
		       max_concurrent_tasks, owner_contact, created_at, updated_at
		FROM bot_profiles WHERE bot_name = ?`, botName)
	p, err := scanBotProfile(row)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	return p, nil
}

// ListBotProfiles returns all bot profiles.
func (db *DB) ListBotProfiles(ctx context.Context) ([]*BotProfile, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT bot_name, description, specializations, tools, hardware,
		       max_concurrent_tasks, owner_contact, created_at, updated_at
		FROM bot_profiles ORDER BY bot_name`)
	if err != nil {
		return nil, fmt.Errorf("listing bot profiles: %w", err)
	}
	defer rows.Close()

	var out []*BotProfile
	for rows.Next() {
		p, err := scanBotProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteBotProfile removes a bot profile by name.
func (db *DB) DeleteBotProfile(ctx context.Context, botName string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM bot_profiles WHERE bot_name = ?`, botName)
	if err != nil {
		return fmt.Errorf("deleting bot profile %q: %w", botName, err)
	}
	return nil
}

func scanBotProfile(s scanner) (*BotProfile, error) {
	var p BotProfile
	var specs, tools, hardware string
	if err := s.Scan(
		&p.BotName, &p.Description, &specs, &tools, &hardware,
		&p.MaxConcurrentTasks, &p.OwnerContact, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning bot profile: %w", err)
	}
	if err := json.Unmarshal([]byte(specs), &p.Specializations); err != nil {
		return nil, fmt.Errorf("unmarshaling specializations: %w", err)
	}
	if err := json.Unmarshal([]byte(tools), &p.Tools); err != nil {
		return nil, fmt.Errorf("unmarshaling tools: %w", err)
	}
	p.Hardware = json.RawMessage(hardware)
	return &p, nil
}
