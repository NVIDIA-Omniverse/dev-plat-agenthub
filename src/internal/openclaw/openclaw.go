// Package openclaw provides an HTTP client for communicating with openclaw instances
// and a background liveness checker.
//
// Each openclaw instance must implement:
//   - GET  /health      → 200 OK means alive
//   - POST /directives  → JSON body with behavioral directives
//
// See docs/api.md for the full contract.
package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// HealthChecker is the interface for checking and directing an openclaw instance.
// It is defined here for mocking in tests.
type HealthChecker interface {
	Health(ctx context.Context) error
	SetMentionOnly(ctx context.Context) error
	SetChatty(ctx context.Context, chatty bool) error
}

// Client is an HTTP client for a single openclaw instance.
type Client struct {
	httpClient     *http.Client
	baseURL        string
	healthPath     string
	directivesPath string
}

// NewClient creates a Client for the openclaw instance at host:port.
func NewClient(host string, port int, timeout time.Duration, healthPath, directivesPath string) *Client {
	return &Client{
		httpClient:     &http.Client{Timeout: timeout},
		baseURL:        fmt.Sprintf("http://%s:%d", host, port),
		healthPath:     healthPath,
		directivesPath: directivesPath,
	}
}

// Health performs a GET /health request. Returns nil if the instance is alive.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+c.healthPath, nil)
	if err != nil {
		return fmt.Errorf("building health request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}

// directive is the JSON body for POST /directives.
type directive struct {
	MentionOnly *bool `json:"mention_only,omitempty"`
	Chatty      *bool `json:"chatty,omitempty"`
}

// SetMentionOnly sends a directive telling the instance to only respond when @mentioned.
func (c *Client) SetMentionOnly(ctx context.Context) error {
	t := true
	return c.sendDirective(ctx, directive{MentionOnly: &t})
}

// SetChatty sends a directive setting the instance's chatty mode.
func (c *Client) SetChatty(ctx context.Context, chatty bool) error {
	return c.sendDirective(ctx, directive{Chatty: &chatty})
}

func (c *Client) sendDirective(ctx context.Context, d directive) error {
	body, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("marshaling directive: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+c.directivesPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building directive request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending directive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("directive returned %d", resp.StatusCode)
	}
	return nil
}

// LivenessNotifier is called by the LivenessChecker on state transitions.
type LivenessNotifier interface {
	NotifyBotDown(ctx context.Context, channelID, botName string) error
	NotifyBotUp(ctx context.Context, channelID, botName string) error
}

// InstanceRecord is the minimal info the LivenessChecker needs per instance.
type InstanceRecord struct {
	ID        string
	Name      string
	Host      string
	Port      int
	ChannelID string
	WasAlive  bool
}

// InstanceLister loads the current set of registered instances.
type InstanceLister interface {
	ListAllInstances(ctx context.Context) ([]InstanceRecord, error)
	UpdateAlive(ctx context.Context, id string, alive bool) error
}

// LivenessCheckerConfig holds settings for the liveness checker.
type LivenessCheckerConfig struct {
	Interval       time.Duration
	Timeout        time.Duration
	HealthPath     string
	DirectivesPath string
}

// LivenessChecker polls all registered openclaw instances at a configurable interval.
// On alive→dead or dead→alive transitions it calls the notifier.
type LivenessChecker struct {
	lister   InstanceLister
	notifier LivenessNotifier
	cfg      LivenessCheckerConfig
}

// NewLivenessChecker creates a LivenessChecker.
func NewLivenessChecker(lister InstanceLister, notifier LivenessNotifier, cfg LivenessCheckerConfig) *LivenessChecker {
	return &LivenessChecker{
		lister:   lister,
		notifier: notifier,
		cfg:      cfg,
	}
}

// Run starts the liveness polling loop. It blocks until ctx is cancelled.
func (lc *LivenessChecker) Run(ctx context.Context) {
	ticker := time.NewTicker(lc.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lc.checkAll(ctx)
		}
	}
}

// CheckOnce performs a single liveness pass (useful for testing and on-demand checks).
func (lc *LivenessChecker) CheckOnce(ctx context.Context) {
	lc.checkAll(ctx)
}

func (lc *LivenessChecker) checkAll(ctx context.Context) {
	instances, err := lc.lister.ListAllInstances(ctx)
	if err != nil {
		slog.Error("liveness checker: failed to list instances", "error", err)
		return
	}
	for _, inst := range instances {
		lc.checkOne(ctx, inst)
	}
}

func (lc *LivenessChecker) checkOne(ctx context.Context, inst InstanceRecord) {
	client := NewClient(inst.Host, inst.Port, lc.cfg.Timeout, lc.cfg.HealthPath, lc.cfg.DirectivesPath)
	checkCtx, cancel := context.WithTimeout(ctx, lc.cfg.Timeout)
	defer cancel()

	err := client.Health(checkCtx)
	nowAlive := err == nil

	if nowAlive == inst.WasAlive {
		return // no state change
	}

	// Log the transition.
	if nowAlive {
		slog.Info("bot came alive", "bot", inst.Name, "host", inst.Host, "port", inst.Port)
	} else {
		slog.Warn("bot went down", "bot", inst.Name, "host", inst.Host, "port", inst.Port, "error", err)
	}

	// State changed — update DB.
	_ = lc.lister.UpdateAlive(ctx, inst.ID, nowAlive)

	// Notify Slack.
	if nowAlive {
		_ = lc.notifier.NotifyBotUp(ctx, inst.ChannelID, inst.Name)
	} else {
		_ = lc.notifier.NotifyBotDown(ctx, inst.ChannelID, inst.Name)
	}
}
