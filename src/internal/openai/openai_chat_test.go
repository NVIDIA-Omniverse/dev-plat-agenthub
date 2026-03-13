package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
)

func newTestClient(srv *httptest.Server, model string) *Client {
	cfg := goopenai.DefaultConfig("test-key")
	cfg.BaseURL = srv.URL + "/v1"
	return &Client{
		client:       goopenai.NewClientWithConfig(cfg),
		model:        model,
		maxTokens:    100,
		systemPrompt: "You are a helpful assistant.",
	}
}

func TestChatSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"id":     "chatcmpl-1",
			"object": "chat.completion",
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"message":       map[string]interface{}{"role": "assistant", "content": "Hello!"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv, "gpt-4o-mini")
	result, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.Equal(t, "Hello!", result)
}

func TestChatNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"id":      "chatcmpl-2",
			"object":  "chat.completion",
			"choices": []interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv, "gpt-4o-mini")
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no choices")
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv, "gpt-4o-mini")
	_, err := c.Chat(context.Background(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "openai chat completion")
}

func TestChatWithMultipleMessages(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		resp := map[string]interface{}{
			"id":     "chatcmpl-3",
			"object": "chat.completion",
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"message":       map[string]interface{}{"role": "assistant", "content": "response"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv, "gpt-4o-mini")
	msgs := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
	}
	result, err := c.Chat(context.Background(), msgs)
	require.NoError(t, err)
	require.Equal(t, "response", result)

	// System prompt is prepended, so messages array should have 3 items.
	apiMsgs := capturedBody["messages"].([]interface{})
	require.Len(t, apiMsgs, 3) // system + user + assistant
}
