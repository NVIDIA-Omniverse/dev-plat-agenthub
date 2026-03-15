package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// InboxDBMessage represents a row in the inbox_messages table.
type InboxDBMessage struct {
	ID          string
	BotName     string
	FromUser    string
	Channel     string
	Body        string
	TaskContext json.RawMessage
	CreatedAt   time.Time
	AckedAt     *time.Time
}

// CreateInboxMessage inserts a new inbox message into the database.
func (db *DB) CreateInboxMessage(ctx context.Context, msg InboxDBMessage) error {
	taskContext := msg.TaskContext
	if len(taskContext) == 0 {
		taskContext = json.RawMessage("{}")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO inbox_messages (id, bot_name, from_user, channel, body, task_context, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.BotName, msg.FromUser, msg.Channel, msg.Body, string(taskContext), msg.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating inbox message: %w", err)
	}
	return nil
}

// ListPendingMessages returns all unacked messages for a bot.
func (db *DB) ListPendingMessages(ctx context.Context, botName string) ([]*InboxDBMessage, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, bot_name, from_user, channel, body, task_context, created_at, acked_at
		FROM inbox_messages
		WHERE bot_name = ? AND acked_at IS NULL
		ORDER BY created_at`,
		botName,
	)
	if err != nil {
		return nil, fmt.Errorf("listing pending messages: %w", err)
	}
	defer rows.Close()

	var msgs []*InboxDBMessage
	for rows.Next() {
		m, err := scanInboxMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// AckInboxMessage marks a message as acknowledged by setting acked_at = NOW().
func (db *DB) AckInboxMessage(ctx context.Context, id string) error {
	now := time.Now()
	_, err := db.ExecContext(ctx, `
		UPDATE inbox_messages SET acked_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("acking inbox message %q: %w", id, err)
	}
	return nil
}

func scanInboxMessage(s scanner) (*InboxDBMessage, error) {
	var m InboxDBMessage
	var taskContext string
	if err := s.Scan(
		&m.ID, &m.BotName, &m.FromUser, &m.Channel, &m.Body,
		&taskContext, &m.CreatedAt, &m.AckedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning inbox message: %w", err)
	}
	m.TaskContext = json.RawMessage(taskContext)
	return &m, nil
}
