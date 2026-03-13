package dolt

import (
	"context"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

var instanceCols = []string{
	"id", "name", "host", "port", "owner_slack_user", "channel_id",
	"chatty", "last_seen_at", "is_alive", "created_at", "updated_at",
}

func newMockDB(t *testing.T) (*DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &DB{db}, mock
}

func TestMigrateSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	for range migrations {
		mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	require.NoError(t, doltDB.Migrate(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrateError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec(".*").WillReturnError(fmt.Errorf("syntax error"))
	err := doltDB.Migrate(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "migration")
}

func TestCreateInstanceSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO openclaw_instances").
		WillReturnResult(sqlmock.NewResult(1, 1))
	inst := Instance{
		ID: "id1", Name: "bot1", Host: "1.2.3.4", Port: 8080,
		OwnerSlackUser: "U1", ChannelID: "C1",
	}
	require.NoError(t, doltDB.CreateInstance(context.Background(), inst))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateInstanceError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO openclaw_instances").
		WillReturnError(fmt.Errorf("duplicate key"))
	inst := Instance{ID: "id1", Name: "bot1"}
	err := doltDB.CreateInstance(context.Background(), inst)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating instance")
}

func TestGetInstanceFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now()
	rows := sqlmock.NewRows(instanceCols).AddRow(
		"id1", "bot1", "1.2.3.4", 8080, "U1", "C1", 0, nil, 1, now, now,
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	inst, err := doltDB.GetInstance(context.Background(), "bot1", "C1")
	require.NoError(t, err)
	require.Equal(t, "bot1", inst.Name)
	require.True(t, inst.IsAlive)
	require.False(t, inst.Chatty)
}

func TestGetInstanceNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(instanceCols) // no rows → ErrNoRows on Scan
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	_, err := doltDB.GetInstance(context.Background(), "nonexistent", "C1")
	require.Error(t, err)
}

func TestGetInstanceWithLastSeen(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now()
	rows := sqlmock.NewRows(instanceCols).AddRow(
		"id2", "bot2", "5.6.7.8", 9090, "U2", "C2", 1, &now, 0, now, now,
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	inst, err := doltDB.GetInstance(context.Background(), "bot2", "C2")
	require.NoError(t, err)
	require.Equal(t, "bot2", inst.Name)
	require.True(t, inst.Chatty)
	require.False(t, inst.IsAlive)
	require.NotNil(t, inst.LastSeenAt)
}

func TestListInstancesSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now()
	rows := sqlmock.NewRows(instanceCols).
		AddRow("id1", "bot1", "1.2.3.4", 8080, "U1", "C1", 0, nil, 1, now, now).
		AddRow("id2", "bot2", "5.6.7.8", 9090, "U2", "C1", 1, &now, 0, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	instances, err := doltDB.ListInstances(context.Background(), "C1")
	require.NoError(t, err)
	require.Len(t, instances, 2)
	require.Equal(t, "bot1", instances[0].Name)
	require.True(t, instances[1].Chatty)
}

func TestListInstancesEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(instanceCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	instances, err := doltDB.ListInstances(context.Background(), "C1")
	require.NoError(t, err)
	require.Empty(t, instances)
}

func TestListInstancesQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))
	_, err := doltDB.ListInstances(context.Background(), "C1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing instances")
}

func TestListAllInstancesSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now()
	rows := sqlmock.NewRows(instanceCols).
		AddRow("id1", "bot1", "1.2.3.4", 8080, "U1", "C1", 0, nil, 0, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	instances, err := doltDB.ListAllInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, instances, 1)
	require.Equal(t, "bot1", instances[0].Name)
}

func TestListAllInstancesEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(instanceCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	instances, err := doltDB.ListAllInstances(context.Background())
	require.NoError(t, err)
	require.Empty(t, instances)
}

func TestListAllInstancesError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.ListAllInstances(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing all instances")
}

func TestUpdateAliveTrue(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.UpdateAlive(context.Background(), "id1", true))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAliveFalse(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.UpdateAlive(context.Background(), "id1", false))
}

func TestUpdateAliveError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnError(fmt.Errorf("update failed"))
	err := doltDB.UpdateAlive(context.Background(), "id1", true)
	require.Error(t, err)
}

func TestUpdateChattyTrue(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.UpdateChatty(context.Background(), "bot1", "C1", true))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateChattyFalse(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.UpdateChatty(context.Background(), "bot1", "C1", false))
}

func TestUpdateChattyError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnError(fmt.Errorf("update failed"))
	err := doltDB.UpdateChatty(context.Background(), "bot1", "C1", true)
	require.Error(t, err)
}

func TestDeleteInstanceSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.DeleteInstance(context.Background(), "bot1", "C1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteInstanceError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM openclaw_instances").WillReturnError(fmt.Errorf("delete failed"))
	err := doltDB.DeleteInstance(context.Background(), "bot1", "C1")
	require.Error(t, err)
}

func TestListInstancesScanError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	// Return a row where port (int) gets a string value → scan error (not ErrNoRows).
	rows := sqlmock.NewRows(instanceCols).
		AddRow("id1", "bot1", "1.2.3.4", "not-an-int", "U1", "C1", 0, nil, 0, time.Now(), time.Now())
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	_, err := doltDB.ListInstances(context.Background(), "C1")
	require.Error(t, err)
}

func TestListAllInstancesScanError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(instanceCols).
		AddRow("id1", "bot1", "1.2.3.4", "not-an-int", "U1", "C1", 0, nil, 0, time.Now(), time.Now())
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	_, err := doltDB.ListAllInstances(context.Background())
	require.Error(t, err)
}

func TestDeleteInstanceByNameSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.DeleteInstanceByName(context.Background(), "mybot"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteInstanceByNameError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM openclaw_instances").WillReturnError(fmt.Errorf("delete failed"))
	err := doltDB.DeleteInstanceByName(context.Background(), "mybot")
	require.Error(t, err)
}
