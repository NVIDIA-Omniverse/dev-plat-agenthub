package openclaw

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSendDirectiveHTTPError tests the case where the server is unreachable.
func TestSendDirectiveHTTPError(t *testing.T) {
	c := &Client{
		httpClient:     &http.Client{Timeout: 10 * time.Millisecond},
		baseURL:        "http://127.0.0.1:1", // no server on port 1
		healthPath:     "/health",
		directivesPath: "/directives",
	}
	err := c.SetMentionOnly(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "sending directive")
}

// TestSendDirectiveChattyHTTPError tests SetChatty against an unreachable server.
func TestSendDirectiveChattyHTTPError(t *testing.T) {
	c := &Client{
		httpClient:     &http.Client{Timeout: 10 * time.Millisecond},
		baseURL:        "http://127.0.0.1:1",
		healthPath:     "/health",
		directivesPath: "/directives",
	}
	err := c.SetChatty(context.Background(), true)
	require.Error(t, err)
}

// TestHealthHTTPError tests Health against an unreachable server.
func TestHealthHTTPError(t *testing.T) {
	c := &Client{
		httpClient:     &http.Client{Timeout: 10 * time.Millisecond},
		baseURL:        "http://127.0.0.1:1",
		healthPath:     "/health",
		directivesPath: "/directives",
	}
	err := c.Health(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "health check failed")
}
