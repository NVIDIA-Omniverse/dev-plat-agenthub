package dolt

import (
	"context"
	"fmt"
)

func (db *DB) CreateUsageLog(ctx context.Context, u UsageLog) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO usage_log (id, bot_name, tier, model, input_tokens, output_tokens, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.BotName, u.Tier, u.Model, u.InputTokens, u.OutputTokens, u.LatencyMs, u.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating usage log: %w", err)
	}
	return nil
}

func (db *DB) GetUsageSummary(ctx context.Context) ([]*UsageSummary, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT bot_name, tier, model,
		       COUNT(*) as total_calls,
		       SUM(input_tokens) as total_input,
		       SUM(output_tokens) as total_output,
		       AVG(latency_ms) as avg_latency
		FROM usage_log
		GROUP BY bot_name, tier, model
		ORDER BY bot_name, tier`)
	if err != nil {
		return nil, fmt.Errorf("querying usage summary: %w", err)
	}
	defer rows.Close()
	var out []*UsageSummary
	for rows.Next() {
		var s UsageSummary
		var avgLat float64
		if err := rows.Scan(&s.BotName, &s.Tier, &s.Model, &s.TotalCalls, &s.TotalInput, &s.TotalOutput, &avgLat); err != nil {
			return nil, fmt.Errorf("scanning usage summary: %w", err)
		}
		s.AvgLatencyMs = int(avgLat)
		out = append(out, &s)
	}
	return out, rows.Err()
}
