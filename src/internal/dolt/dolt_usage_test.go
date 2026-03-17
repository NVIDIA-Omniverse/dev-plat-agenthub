package dolt

import (
	"context"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCreateUsageLogSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO usage_log").
		WillReturnResult(sqlmock.NewResult(1, 1))
	now := time.Now().UTC()
	u := UsageLog{
		ID:           "ul-1",
		BotName:      "bot1",
		Tier:         "escalation",
		Model:        "gpt-4",
		InputTokens:  100,
		OutputTokens: 50,
		LatencyMs:    150,
		CreatedAt:    now,
	}
	require.NoError(t, doltDB.CreateUsageLog(context.Background(), u))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateUsageLogError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO usage_log").
		WillReturnError(fmt.Errorf("insert failed"))
	u := UsageLog{ID: "ul-1", BotName: "bot1", Tier: "default", Model: "gpt-3.5"}
	err := doltDB.CreateUsageLog(context.Background(), u)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating usage log")
}

func TestGetUsageSummarySuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	cols := []string{"bot_name", "tier", "model", "total_calls", "total_input", "total_output", "avg_latency"}
	rows := sqlmock.NewRows(cols).
		AddRow("bot1", "escalation", "gpt-4", 5, 500, 250, 120.5).
		AddRow("bot1", "default", "gpt-3.5", 10, 1000, 500, 80.0)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	summaries, err := doltDB.GetUsageSummary(context.Background())
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	require.Equal(t, "bot1", summaries[0].BotName)
	require.Equal(t, "escalation", summaries[0].Tier)
	require.Equal(t, "gpt-4", summaries[0].Model)
	require.Equal(t, 5, summaries[0].TotalCalls)
	require.Equal(t, 500, summaries[0].TotalInput)
	require.Equal(t, 250, summaries[0].TotalOutput)
	require.Equal(t, 120, summaries[0].AvgLatencyMs)
	require.Equal(t, "default", summaries[1].Tier)
	require.Equal(t, 80, summaries[1].AvgLatencyMs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUsageSummaryEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	cols := []string{"bot_name", "tier", "model", "total_calls", "total_input", "total_output", "avg_latency"}
	rows := sqlmock.NewRows(cols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	summaries, err := doltDB.GetUsageSummary(context.Background())
	require.NoError(t, err)
	require.Empty(t, summaries)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUsageSummaryQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.GetUsageSummary(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "querying usage summary")
}
