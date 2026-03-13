package openai

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMockCompleterSuccess(t *testing.T) {
	mock := &MockCompleter{Response: "Hello from AI!"}

	resp, err := mock.Chat(context.Background(), []Message{
		{Role: "user", Content: "Hello"},
	})
	require.NoError(t, err)
	require.Equal(t, "Hello from AI!", resp)
}

func TestMockCompleterError(t *testing.T) {
	mock := &MockCompleter{Err: errors.New("api error")}

	_, err := mock.Chat(context.Background(), []Message{
		{Role: "user", Content: "Hello"},
	})
	require.Error(t, err)
	require.Equal(t, "api error", err.Error())
}

func TestMockCompleterRecordsCalls(t *testing.T) {
	mock := &MockCompleter{Response: "ok"}

	msgs1 := []Message{{Role: "user", Content: "first"}}
	msgs2 := []Message{{Role: "user", Content: "second"}}

	_, _ = mock.Chat(context.Background(), msgs1)
	_, _ = mock.Chat(context.Background(), msgs2)

	require.Len(t, mock.Calls, 2)
	require.Equal(t, "first", mock.Calls[0][0].Content)
	require.Equal(t, "second", mock.Calls[1][0].Content)
}

func TestMockCompleterImplementsCompleter(t *testing.T) {
	// Compile-time check that MockCompleter implements Completer.
	var _ Completer = &MockCompleter{}
}

func TestNewClientDoesNotPanic(t *testing.T) {
	// Verify constructor doesn't panic; no live API call is made.
	c := NewClient("test-key", "gpt-4o-mini", 1024, "system prompt")
	require.NotNil(t, c)
	require.Equal(t, "gpt-4o-mini", c.model)
	require.Equal(t, 1024, c.maxTokens)
	require.Equal(t, "system prompt", c.systemPrompt)
}

func TestMessageRoles(t *testing.T) {
	mock := &MockCompleter{Response: "ok"}

	msgs := []Message{
		{Role: "system", Content: "be helpful"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	_, err := mock.Chat(context.Background(), msgs)
	require.NoError(t, err)
	require.Len(t, mock.Calls[0], 3)
	require.Equal(t, "system", mock.Calls[0][0].Role)
}
