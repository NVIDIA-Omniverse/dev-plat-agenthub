package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// cmdClientRun starts the native Go agent polling loop.
// Usage: agenthub client run [name]
//
// If name is given, reads ~/.agenthub/agents/<name>/agent.env.
// Otherwise falls back to AGENT_NAME / AGENTHUB_* environment variables.
func cmdClientRun(args []string) error {
	cfg, err := loadAgentConfig(args)
	if err != nil {
		return fmt.Errorf("loading agent config: %w", err)
	}
	if cfg.AgentName == "" {
		return fmt.Errorf("agent name is required (pass name as argument or set AGENT_NAME)")
	}
	if cfg.ServerURL == "" {
		return fmt.Errorf("AGENTHUB_SERVER_URL is required")
	}
	if cfg.RegToken == "" {
		return fmt.Errorf("AGENTHUB_REGISTRATION_TOKEN is required")
	}

	fmt.Printf("[agenthub] Agent %q starting\n", cfg.AgentName)
	fmt.Printf("[agenthub] Server: %s\n", cfg.ServerURL)
	if cfg.LLMBaseURL != "" {
		fmt.Printf("[agenthub] LLM:    %s / %s\n", cfg.LLMBaseURL, cfg.LLMModel)
	} else {
		fmt.Printf("[agenthub] LLM:    (not configured — will echo tasks back)\n")
	}
	fmt.Printf("[agenthub] Press Ctrl+C to stop.\n\n")

	return runAgentLoop(cfg)
}

// agentConfig holds all settings needed to run the agent loop.
type agentConfig struct {
	AgentName     string
	AgentHost     string
	AgentPort     string
	LLMBaseURL    string
	LLMModel      string
	LLMAPIKey     string
	ServerURL     string
	RegToken      string
	SlackBotToken string // received from server at registration; used to post replies directly
}

// loadAgentConfig resolves agent configuration from either an env file
// (if a name argument is provided) or the current process environment.
func loadAgentConfig(args []string) (*agentConfig, error) {
	if len(args) > 0 && args[0] != "" && args[0] != "--" {
		name := strings.TrimSpace(args[0])
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		envPath := filepath.Join(home, ".agenthub", "agents", name, "agent.env")
		return loadAgentEnvFile(envPath)
	}
	return &agentConfig{
		AgentName:     os.Getenv("AGENT_NAME"),
		AgentHost:     os.Getenv("AGENT_HOST"),
		AgentPort:     os.Getenv("AGENT_PORT"),
		LLMBaseURL:    os.Getenv("LLM_BASE_URL"),
		LLMModel:      os.Getenv("LLM_MODEL"),
		LLMAPIKey:     os.Getenv("LLM_API_KEY"),
		ServerURL:     os.Getenv("AGENTHUB_SERVER_URL"),
		RegToken:      os.Getenv("AGENTHUB_REGISTRATION_TOKEN"),
		SlackBotToken: os.Getenv("SLACK_BOT_TOKEN"),
	}, nil
}

// loadAgentEnvFile parses a KEY='value' shell env file into agentConfig.
func loadAgentEnvFile(path string) (*agentConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		k := line[:idx]
		v := line[idx+1:]
		// Strip surrounding single quotes written by writeAgentEnv.
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
			v = strings.ReplaceAll(v, `'\''`, `'`)
		}
		env[k] = v
	}

	return &agentConfig{
		AgentName:     env["AGENT_NAME"],
		AgentHost:     env["AGENT_HOST"],
		AgentPort:     env["AGENT_PORT"],
		LLMBaseURL:    env["LLM_BASE_URL"],
		LLMModel:      env["LLM_MODEL"],
		LLMAPIKey:     env["LLM_API_KEY"],
		ServerURL:     env["AGENTHUB_SERVER_URL"],
		RegToken:      env["AGENTHUB_REGISTRATION_TOKEN"],
		SlackBotToken: env["SLACK_BOT_TOKEN"],
	}, nil
}

// runAgentLoop is the main heartbeat + inbox poll loop.
func runAgentLoop(cfg *agentConfig) error {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	llmClient := &http.Client{Timeout: 120 * time.Second}

	// Send immediate heartbeat on startup.
	if err := sendHeartbeat(httpClient, cfg, "idle", "agent started"); err != nil {
		slog.Warn("initial heartbeat failed", "error", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	heartbeatTick := time.NewTicker(60 * time.Second)
	inboxTick := time.NewTicker(30 * time.Second)
	defer heartbeatTick.Stop()
	defer inboxTick.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[agenthub] Shutting down agent %q.\n", cfg.AgentName)
			return nil

		case <-heartbeatTick.C:
			if err := sendHeartbeat(httpClient, cfg, "idle", ""); err != nil {
				slog.Warn("heartbeat failed", "error", err)
			}

		case <-inboxTick.C:
			if err := pollAndProcess(httpClient, llmClient, cfg); err != nil {
				slog.Warn("inbox poll failed", "error", err)
			}
		}
	}
}

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func sendHeartbeat(client *http.Client, cfg *agentConfig, status, message string) error {
	type hbReq struct {
		BotName string `json:"bot_name"`
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}
	body, _ := json.Marshal(hbReq{BotName: cfg.AgentName, Status: status, Message: message})
	req, err := http.NewRequest(http.MethodPost, cfg.ServerURL+"/api/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Registration-Token", cfg.RegToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ── Inbox ─────────────────────────────────────────────────────────────────────

type clientInboxMessage struct {
	ID          string             `json:"id"`
	From        string             `json:"from"`
	Channel     string             `json:"channel"`
	Text        string             `json:"text"`
	CreatedAt   time.Time          `json:"created_at"`
	TaskContext *clientTaskContext `json:"task_context,omitempty"`
}

type clientTaskContext struct {
	TaskAssignmentID string `json:"task_assignment_id"`
	TaskID           string `json:"task_id"`
	ProjectID        string `json:"project_id"`
	ProjectName      string `json:"project_name"`
	CredentialURL    string `json:"credential_url"`
}

func pollAndProcess(client, llmClient *http.Client, cfg *agentConfig) error {
	req, err := http.NewRequest(http.MethodGet, cfg.ServerURL+"/api/inbox", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Registration-Token", cfg.RegToken)
	req.Header.Set("X-Bot-Name", cfg.AgentName)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET /api/inbox: %w", err)
	}
	defer resp.Body.Close()

	var msgs []clientInboxMessage
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return fmt.Errorf("decoding inbox: %w", err)
	}

	if len(msgs) > 0 {
		slog.Info("inbox messages waiting", "count", len(msgs))
	}

	for _, msg := range msgs {
		if err := handleMessage(client, llmClient, cfg, msg); err != nil {
			slog.Warn("failed to handle message", "id", msg.ID, "error", err)
		}
	}
	return nil
}

func handleMessage(client, llmClient *http.Client, cfg *agentConfig, msg clientInboxMessage) error {
	preview := msg.Text
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	fmt.Printf("[agenthub] Message %s: %s\n", msg.ID, preview)

	// Mark task in_progress if we have a task ID.
	if msg.TaskContext != nil && msg.TaskContext.TaskID != "" {
		_ = updateTaskStatus(client, cfg, msg.TaskContext.TaskID, "in_progress", "")
	}

	// Build reply via LLM (or echo if not configured).
	var reply string
	if cfg.LLMBaseURL != "" && cfg.LLMAPIKey != "" && cfg.LLMModel != "" {
		systemPrompt := fmt.Sprintf(
			"You are %s, an AI assistant. "+
				"When given a task or question, respond helpfully and concisely. "+
				"If this is a software task, describe what you would do or provide the answer directly.",
			cfg.AgentName,
		)
		var err error
		reply, err = callLLM(llmClient, cfg, systemPrompt, msg.Text)
		if err != nil {
			slog.Warn("LLM call failed", "error", err)
			reply = fmt.Sprintf("I received your message but my LLM is unavailable (%v). Message was: %s", err, msg.Text)
		}
	} else {
		reply = fmt.Sprintf("Received (LLM not configured): %s", msg.Text)
	}

	// Update task status.
	if msg.TaskContext != nil && msg.TaskContext.TaskID != "" {
		note := reply
		if len(note) > 500 {
			note = note[:500] + "..."
		}
		_ = updateTaskStatus(client, cfg, msg.TaskContext.TaskID, "closed", note)
	}

	// Post reply directly to Slack.
	if cfg.SlackBotToken != "" && msg.Channel != "" {
		text := fmt.Sprintf("[%s] %s", cfg.AgentName, reply)
		if err := postSlackMessage(client, cfg.SlackBotToken, msg.Channel, text); err != nil {
			slog.Warn("slack post failed", "id", msg.ID, "error", err)
		}
	}

	// Ack so we don't process this message again.
	return ackMessage(client, cfg, msg.ID)
}

// ── LLM ───────────────────────────────────────────────────────────────────────

func callLLM(client *http.Client, cfg *agentConfig, system, user string) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type request struct {
		Model     string    `json:"model"`
		Messages  []message `json:"messages"`
		MaxTokens int       `json:"max_tokens"`
	}
	type choice struct {
		Message message `json:"message"`
	}
	type llmResponse struct {
		Choices []choice `json:"choices"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	reqBody := request{
		Model: cfg.LLMModel,
		Messages: []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens: 1024,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, cfg.LLMBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.LLMAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading LLM response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		preview := string(raw)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return "", fmt.Errorf("LLM returned HTTP %d: %s", resp.StatusCode, preview)
	}

	var llmResp llmResponse
	if err := json.Unmarshal(raw, &llmResp); err != nil {
		return "", fmt.Errorf("decoding LLM response: %w", err)
	}
	if llmResp.Error != nil {
		return "", fmt.Errorf("LLM error: %s", llmResp.Error.Message)
	}
	if len(llmResp.Choices) == 0 {
		return "", fmt.Errorf("LLM returned no choices")
	}
	return strings.TrimSpace(llmResp.Choices[0].Message.Content), nil
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

// postSlackMessage posts a message directly to Slack's chat.postMessage API.
// The caller must supply a valid bot token (xoxb-...).
func postSlackMessage(client *http.Client, botToken, channel, text string) error {
	type slackReq struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	body, _ := json.Marshal(slackReq{Channel: channel, Text: text})
	req, err := http.NewRequest(http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+botToken)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("slack API request: %w", err)
	}
	defer resp.Body.Close()
	var slackResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&slackResp); err != nil {
		return fmt.Errorf("decoding slack response: %w", err)
	}
	if !slackResp.OK {
		return fmt.Errorf("slack API error: %s", slackResp.Error)
	}
	return nil
}

func ackMessage(client *http.Client, cfg *agentConfig, msgID string) error {
	req, err := http.NewRequest(http.MethodPost,
		cfg.ServerURL+"/api/inbox/"+msgID+"/ack",
		nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Registration-Token", cfg.RegToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func updateTaskStatus(client *http.Client, cfg *agentConfig, taskID, status, note string) error {
	type statusReq struct {
		Status string `json:"status"`
		Note   string `json:"note,omitempty"`
	}
	body, _ := json.Marshal(statusReq{Status: status, Note: note})
	req, err := http.NewRequest(http.MethodPost,
		cfg.ServerURL+"/api/tasks/"+taskID+"/status",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Registration-Token", cfg.RegToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
