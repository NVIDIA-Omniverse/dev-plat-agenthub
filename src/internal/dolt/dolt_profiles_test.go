package dolt

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

var profileCols = []string{
	"bot_name", "description", "specializations", "tools", "hardware",
	"max_concurrent_tasks", "owner_contact", "created_at", "updated_at",
}

func TestUpsertBotProfileSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO bot_profiles").
		WillReturnResult(sqlmock.NewResult(1, 1))
	now := time.Now().UTC()
	p := BotProfile{
		BotName:            "bot1",
		Description:        "GPU worker",
		Specializations:    []string{"code", "ml"},
		Tools:              []string{"python", "git"},
		Hardware:           json.RawMessage(`{"gpu":"A100","gpu_count":2}`),
		MaxConcurrentTasks: 3,
		OwnerContact:       "alice@example.com",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, doltDB.UpsertBotProfile(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertBotProfileEmptySlices(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO bot_profiles").
		WillReturnResult(sqlmock.NewResult(1, 1))
	p := BotProfile{
		BotName:     "bot2",
		Description: "minimal",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, doltDB.UpsertBotProfile(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertBotProfileError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO bot_profiles").
		WillReturnError(fmt.Errorf("duplicate key"))
	p := BotProfile{BotName: "bot1", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	err := doltDB.UpsertBotProfile(context.Background(), p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "upserting bot profile")
}

func TestGetBotProfileFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(profileCols).AddRow(
		"bot1", "GPU worker", `["code","ml"]`, `["python","git"]`,
		`{"gpu":"A100","gpu_count":2}`, 3, "alice@example.com", now, now,
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	p, err := doltDB.GetBotProfile(context.Background(), "bot1")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, "bot1", p.BotName)
	require.Equal(t, "GPU worker", p.Description)
	require.Equal(t, []string{"code", "ml"}, p.Specializations)
	require.Equal(t, []string{"python", "git"}, p.Tools)
	require.Equal(t, json.RawMessage(`{"gpu":"A100","gpu_count":2}`), p.Hardware)
	require.Equal(t, 3, p.MaxConcurrentTasks)
	require.Equal(t, "alice@example.com", p.OwnerContact)
}

func TestGetBotProfileNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(profileCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	p, err := doltDB.GetBotProfile(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Nil(t, p)
}

func TestGetBotProfileQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.GetBotProfile(context.Background(), "bot1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scanning bot profile")
}

func TestListBotProfilesSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(profileCols).
		AddRow("bot1", "desc1", `[]`, `[]`, `{}`, 1, "", now, now).
		AddRow("bot2", "desc2", `["ml"]`, `["python"]`, `{"gpu":"A100"}`, 2, "bob@x.com", now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	profiles, err := doltDB.ListBotProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	require.Equal(t, "bot1", profiles[0].BotName)
	require.Equal(t, "bot2", profiles[1].BotName)
	require.Equal(t, []string{"ml"}, profiles[1].Specializations)
}

func TestListBotProfilesEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(profileCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	profiles, err := doltDB.ListBotProfiles(context.Background())
	require.NoError(t, err)
	require.Empty(t, profiles)
}

func TestListBotProfilesQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))
	_, err := doltDB.ListBotProfiles(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing bot profiles")
}

func TestDeleteBotProfileSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM bot_profiles").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.DeleteBotProfile(context.Background(), "bot1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteBotProfileError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM bot_profiles").WillReturnError(fmt.Errorf("delete failed"))
	err := doltDB.DeleteBotProfile(context.Background(), "bot1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "deleting bot profile")
}
