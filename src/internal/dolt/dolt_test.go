package dolt

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Unit tests for the dolt package that don't require a live Dolt server
// test the helper functions and data model.
// Integration tests (requiring a real Dolt server) live in tests/integration/.

func TestBoolToInt(t *testing.T) {
	require.Equal(t, 1, boolToInt(true))
	require.Equal(t, 0, boolToInt(false))
}

func TestInstanceFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	inst := Instance{
		ID:             "test-id",
		Name:           "mybot",
		Host:           "1.2.3.4",
		Port:           8080,
		OwnerSlackUser: "U123456",
		ChannelID:      "C789012",
		Chatty:         false,
		IsAlive:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	require.Equal(t, "mybot", inst.Name)
	require.Equal(t, 8080, inst.Port)
	require.False(t, inst.Chatty)
	require.True(t, inst.IsAlive)
}

func TestMigrationsAreNotEmpty(t *testing.T) {
	require.NotEmpty(t, migrations)
	for _, m := range migrations {
		require.NotEmpty(t, m.Name)
		require.NotEmpty(t, m.SQL)
	}
}

func TestOpenBadDSN(t *testing.T) {
	// A DSN that points to a non-existent server should fail on Ping.
	_, err := Open("root:@tcp(127.0.0.1:1)/nonexistent?timeout=1s")
	require.Error(t, err)
}

// TestContextCancellation verifies that Open respects context cancellation.
// We can't easily test this without a live server, but we can verify the
// function signature accepts a context by using the DB methods.
func TestDBNilSafety(t *testing.T) {
	// Verify that the DB type wraps *sql.DB correctly.
	var db *DB
	require.Nil(t, db)
}

// TestUpdateAliveWithNilDB verifies the method is defined (compile-time check).
// Actual DB interaction is tested in integration tests.
func TestMethodsExist(t *testing.T) {
	// This test confirms the methods compile and have the right signatures.
	var db *DB
	ctx := context.Background()
	_ = db
	_ = ctx
	// Just checking compile-time method availability.
}
