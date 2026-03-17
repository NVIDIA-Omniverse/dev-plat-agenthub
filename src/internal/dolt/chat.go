package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func (db *DB) CreateChatMessage(ctx context.Context, msg ChatMessage) error {
	metadata := msg.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO chat_messages (id, bot_name, sender, body, metadata)
		VALUES (?, ?, ?, ?, ?)`,
		msg.ID, msg.BotName, msg.Sender, msg.Body, string(metadata),
	)
	if err != nil {
		return fmt.Errorf("creating chat message: %w", err)
	}
	return nil
}

func (db *DB) ListChatMessages(ctx context.Context, botName string, limit, offset int) ([]*ChatMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, bot_name, sender, body, metadata, created_at
		FROM chat_messages
		WHERE bot_name = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`,
		botName, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("listing chat messages: %w", err)
	}
	defer rows.Close()

	var out []*ChatMessage
	for rows.Next() {
		m, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (db *DB) GetChatMessage(ctx context.Context, id string) (*ChatMessage, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, bot_name, sender, body, metadata, created_at
		FROM chat_messages WHERE id = ?`, id)
	m, err := scanChatMessage(row)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	return m, nil
}

func scanChatMessage(s scanner) (*ChatMessage, error) {
	var m ChatMessage
	var metadata string
	if err := s.Scan(
		&m.ID, &m.BotName, &m.Sender, &m.Body, &metadata, &m.CreatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning chat message: %w", err)
	}
	m.Metadata = json.RawMessage(metadata)
	return &m, nil
}
