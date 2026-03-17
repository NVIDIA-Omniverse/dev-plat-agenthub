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

var chatCols = []string{
	"id", "bot_name", "sender", "body", "metadata", "created_at",
}

func TestCreateChatMessageSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO chat_messages").
		WillReturnResult(sqlmock.NewResult(1, 1))
	msg := ChatMessage{
		ID:       "msg-1",
		BotName:  "bot1",
		Sender:   "owner",
		Body:     "hello",
		Metadata: json.RawMessage(`{"foo":"bar"}`),
	}
	require.NoError(t, doltDB.CreateChatMessage(context.Background(), msg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateChatMessageError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO chat_messages").
		WillReturnError(fmt.Errorf("insert failed"))
	msg := ChatMessage{ID: "msg-1", BotName: "bot1", Sender: "owner"}
	err := doltDB.CreateChatMessage(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating chat message")
}

func TestCreateChatMessageEmptyMetadata(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO chat_messages").
		WillReturnResult(sqlmock.NewResult(1, 1))
	msg := ChatMessage{
		ID:      "msg-2",
		BotName: "bot2",
		Sender:  "bot2",
		Body:    "hi",
	}
	require.NoError(t, doltDB.CreateChatMessage(context.Background(), msg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListChatMessagesSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(chatCols).
		AddRow("msg-1", "bot1", "owner", "hello", `{}`, now).
		AddRow("msg-2", "bot1", "bot1", "hi back", `{"model":"gpt-4"}`, now)
	mock.ExpectQuery("SELECT").WithArgs("bot1", 50, 0).WillReturnRows(rows)
	msgs, err := doltDB.ListChatMessages(context.Background(), "bot1", 50, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "msg-1", msgs[0].ID)
	require.Equal(t, "owner", msgs[0].Sender)
	require.Equal(t, "hello", msgs[0].Body)
	require.Equal(t, json.RawMessage(`{}`), msgs[0].Metadata)
	require.Equal(t, "msg-2", msgs[1].ID)
	require.Equal(t, json.RawMessage(`{"model":"gpt-4"}`), msgs[1].Metadata)
}

func TestListChatMessagesDefaultLimit(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(chatCols)
	mock.ExpectQuery("SELECT").WithArgs("bot1", 50, 10).WillReturnRows(rows)
	_, err := doltDB.ListChatMessages(context.Background(), "bot1", 0, 10)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListChatMessagesEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(chatCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	msgs, err := doltDB.ListChatMessages(context.Background(), "bot1", 20, 0)
	require.NoError(t, err)
	require.Empty(t, msgs)
}

func TestListChatMessagesQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.ListChatMessages(context.Background(), "bot1", 50, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing chat messages")
}

func TestGetChatMessageFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(chatCols).
		AddRow("msg-1", "bot1", "owner", "hello", `{"key":"val"}`, now)
	mock.ExpectQuery("SELECT").WithArgs("msg-1").WillReturnRows(rows)
	m, err := doltDB.GetChatMessage(context.Background(), "msg-1")
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Equal(t, "msg-1", m.ID)
	require.Equal(t, "bot1", m.BotName)
	require.Equal(t, "owner", m.Sender)
	require.Equal(t, "hello", m.Body)
	require.Equal(t, json.RawMessage(`{"key":"val"}`), m.Metadata)
	require.Equal(t, now, m.CreatedAt)
}

func TestGetChatMessageNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(chatCols)
	mock.ExpectQuery("SELECT").WithArgs("nonexistent").WillReturnRows(rows)
	m, err := doltDB.GetChatMessage(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Nil(t, m)
}

func TestGetChatMessageQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))
	_, err := doltDB.GetChatMessage(context.Background(), "msg-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scanning chat message")
}
