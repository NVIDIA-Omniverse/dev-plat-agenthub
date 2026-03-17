// Package openai provides a thin wrapper around the OpenAI chat completions API.
//
// All interaction with OpenAI goes through the Completer interface, which makes
// it easy to mock in tests.
package openai

import (
	"context"
	"fmt"

	goopenai "github.com/sashabaranov/go-openai"
)

// Completer is the interface for sending chat messages to an AI model.
// Define the interface here so callers can mock it in tests.
type Completer interface {
	Chat(ctx context.Context, messages []Message) (string, error)
}

// Message is a single chat message (role + content).
type Message struct {
	Role    string // "system", "user", or "assistant"
	Content string
}

// Client implements Completer using the OpenAI API.
type Client struct {
	client       *goopenai.Client
	model        string
	maxTokens    int
	systemPrompt string
}

// NewClient creates a new OpenAI-compatible Client.
// If baseURL is non-empty it overrides the default api.openai.com endpoint,
// allowing any OpenAI-compatible API (NVIDIA Inference, etc.) to be used.
func NewClient(apiKey, model string, maxTokens int, systemPrompt string, baseURL string) *Client {
	cfg := goopenai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return &Client{
		client:       goopenai.NewClientWithConfig(cfg),
		model:        model,
		maxTokens:    maxTokens,
		systemPrompt: systemPrompt,
	}
}

// Chat sends the messages to OpenAI and returns the assistant's response.
// The configured system prompt is automatically prepended.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	return c.ChatWithSystem(ctx, c.systemPrompt, messages)
}

// ChatWithSystem sends messages with an explicit system prompt, bypassing the
// client's configured system prompt.
func (c *Client) ChatWithSystem(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	apiMessages := make([]goopenai.ChatCompletionMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		apiMessages = append(apiMessages, goopenai.ChatCompletionMessage{
			Role:    goopenai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}
	for _, m := range messages {
		apiMessages = append(apiMessages, goopenai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	resp, err := c.client.CreateChatCompletion(ctx, goopenai.ChatCompletionRequest{
		Model:     c.model,
		Messages:  apiMessages,
		MaxTokens: c.maxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("openai chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

// MockCompleter is a test double for Completer.
// Set Response to control what Chat returns.
type MockCompleter struct {
	Response string
	Err      error
	Calls    [][]Message // all calls recorded here
}

// Chat records the call and returns the configured Response/Err.
func (m *MockCompleter) Chat(_ context.Context, messages []Message) (string, error) {
	m.Calls = append(m.Calls, messages)
	return m.Response, m.Err
}
