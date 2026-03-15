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

var inboxCols = []string{
	"id", "bot_name", "from_user", "channel", "body", "task_context", "created_at", "acked_at",
}

// --------------------------------------------------------------------------
// UpdateHeartbeat tests
// --------------------------------------------------------------------------

func TestUpdateHeartbeatSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := doltDB.UpdateHeartbeat(context.Background(), "bot1", "AH-5", "working", "halfway done")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateHeartbeatError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").
		WillReturnError(fmt.Errorf("db error"))
	err := doltDB.UpdateHeartbeat(context.Background(), "bot1", "", "idle", "")
	require.Error(t, err)
}

func TestUpdateHeartbeatEmptyStatus(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Empty status is passed as-is to the DB.
	err := doltDB.UpdateHeartbeat(context.Background(), "mybot", "", "", "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --------------------------------------------------------------------------
// CreateInboxMessage tests
// --------------------------------------------------------------------------

func TestCreateInboxMessageSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO inbox_messages").
		WillReturnResult(sqlmock.NewResult(1, 1))
	msg := InboxDBMessage{
		ID:          "msg-1",
		BotName:     "bot1",
		FromUser:    "alice",
		Channel:     "C123",
		Body:        "hello",
		TaskContext: json.RawMessage(`{}`),
		CreatedAt:   time.Now().UTC(),
	}
	require.NoError(t, doltDB.CreateInboxMessage(context.Background(), msg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateInboxMessageError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO inbox_messages").
		WillReturnError(fmt.Errorf("insert failed"))
	msg := InboxDBMessage{ID: "msg-1", BotName: "bot1"}
	err := doltDB.CreateInboxMessage(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating inbox message")
}

func TestCreateInboxMessageNilTaskContext(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO inbox_messages").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// nil TaskContext should be treated as empty JSON object.
	msg := InboxDBMessage{
		ID:          "msg-2",
		BotName:     "bot2",
		TaskContext: nil,
		CreatedAt:   time.Now().UTC(),
	}
	require.NoError(t, doltDB.CreateInboxMessage(context.Background(), msg))
	require.NoError(t, mock.ExpectationsWereMet())
}

// --------------------------------------------------------------------------
// ListPendingMessages tests
// --------------------------------------------------------------------------

func TestListPendingMessagesSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(inboxCols).
		AddRow("msg-1", "bot1", "alice", "C123", "hello", `{}`, now, nil).
		AddRow("msg-2", "bot1", "bob", "C456", "world", `{"task_id":"T1"}`, now, nil)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	msgs, err := doltDB.ListPendingMessages(context.Background(), "bot1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "msg-1", msgs[0].ID)
	require.Equal(t, "bot1", msgs[0].BotName)
	require.Equal(t, "alice", msgs[0].FromUser)
	require.Equal(t, "hello", msgs[0].Body)
	require.Nil(t, msgs[0].AckedAt)
}

func TestListPendingMessagesEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(inboxCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	msgs, err := doltDB.ListPendingMessages(context.Background(), "bot1")
	require.NoError(t, err)
	require.Empty(t, msgs)
}

func TestListPendingMessagesQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.ListPendingMessages(context.Background(), "bot1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing pending messages")
}

// --------------------------------------------------------------------------
// AckInboxMessage tests
// --------------------------------------------------------------------------

func TestAckInboxMessageSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE inbox_messages").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.AckInboxMessage(context.Background(), "msg-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAckInboxMessageError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE inbox_messages").
		WillReturnError(fmt.Errorf("update failed"))
	err := doltDB.AckInboxMessage(context.Background(), "msg-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "acking inbox message")
}

func TestAckInboxMessageIdempotent(t *testing.T) {
	doltDB, mock := newMockDB(t)
	// Returns 0 rows affected (already acked) — should still succeed.
	mock.ExpectExec("UPDATE inbox_messages").
		WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, doltDB.AckInboxMessage(context.Background(), "msg-already-acked"))
	require.NoError(t, mock.ExpectationsWereMet())
}
