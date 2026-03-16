package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

// cmdClient dispatches agenthub client subcommands.
func cmdClient(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthub client <create|run> [name]")
	}
	switch args[0] {
	case "create":
		return cmdClientCreate(args[1:])
	case "run":
		return cmdClientRun(args[1:])
	default:
		return fmt.Errorf("unknown client subcommand %q — try: create, run", args[0])
	}
}

// cmdClientCreate runs the interactive agent setup wizard.
//
// Usage: agenthub client create [name]
//
// Collects name, LLM backend, server URL, and registration token.
// Writes config to ~/.agenthub/agents/<name>/agent.env, registers the agent,
// then immediately starts the polling loop (agenthub client run <name>).
func cmdClientCreate(args []string) error {
	r := bufio.NewReader(os.Stdin)

	// ── 1. Agent name ─────────────────────────────────────────────────────────
	var name string
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	if name == "" {
		name = prompt(r, "Agent name: ")
	}
	if name == "" {
		return fmt.Errorf("agent name is required")
	}

	fmt.Printf("\n=== Setting up agent %q ===\n\n", name)

	// ── 2. LLM backend ────────────────────────────────────────────────────────
	fmt.Println("Choose your LLM backend:")
	fmt.Println()
	fmt.Println("  [1] NVIDIA Inference API  (recommended for server nodes)")
	fmt.Println("      Cloud-hosted. Requires a personal NVIDIA API key.")
	fmt.Println("      Model: nvidia/llama-3.3-nemotron-super-49b-v1")
	fmt.Println("      → Get a free key at https://build.nvidia.com")
	fmt.Println()
	fmt.Println("  [2] Other OpenAI-compatible endpoint  (LM Studio, Ollama, etc.)")
	fmt.Println("      Provide a custom base URL and model name.")
	fmt.Println()
	choice := prompt(r, "Choice [1/2]: ")

	var llmBaseURL, llmModel, llmAPIKey string

	switch strings.TrimSpace(choice) {
	case "2":
		// ── Custom OpenAI-compatible endpoint ─────────────────────────────────
		llmBaseURL = strings.TrimRight(strings.TrimSpace(prompt(r, "LLM base URL (e.g. http://localhost:1234/v1): ")), "/")
		if llmBaseURL == "" {
			return fmt.Errorf("LLM base URL is required")
		}
		llmModel = strings.TrimSpace(prompt(r, "Model name: "))
		if llmModel == "" {
			return fmt.Errorf("model name is required")
		}
		llmAPIKey = promptSecretR(r, "API key (or leave blank if not required): ")
		llmAPIKey = strings.TrimSpace(llmAPIKey)
		if llmAPIKey == "" {
			llmAPIKey = "none"
		}

	default:
		// ── NVIDIA inference ──────────────────────────────────────────────────
		llmBaseURL = "https://integrate.api.nvidia.com/v1"
		llmModel = "nvidia/llama-3.3-nemotron-super-49b-v1"

		fmt.Printf("\nModel:    %s\n", llmModel)
		fmt.Printf("Endpoint: %s\n\n", llmBaseURL)

		for {
			llmAPIKey = promptSecretR(r, "NVIDIA API key (starts with nvapi-): ")
			llmAPIKey = strings.TrimSpace(llmAPIKey)
			if llmAPIKey == "" {
				fmt.Println()
				fmt.Println("  To get a free NVIDIA API key:")
				fmt.Println("  1. Visit https://build.nvidia.com")
				fmt.Println("  2. Sign in with your NVIDIA account")
				fmt.Println("  3. Click \"Get API Key\" on any model page")
				fmt.Println("  4. Copy the key (starts with nvapi-...)")
				fmt.Println()
				retry := prompt(r, "Enter key now, or 'q' to quit: ")
				retry = strings.TrimSpace(retry)
				if strings.EqualFold(retry, "q") {
					return fmt.Errorf("cancelled — re-run once you have an NVIDIA API key")
				}
				llmAPIKey = retry
			}
			if llmAPIKey != "" {
				break
			}
		}
	}

	// ── 3. Agenthub server URL ────────────────────────────────────────────────
	fmt.Println()
	serverURL := prompt(r, "Agenthub server URL (e.g. http://server:8080): ")
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if serverURL == "" {
		return fmt.Errorf("server URL is required")
	}

	// ── 4. Registration token ─────────────────────────────────────────────────
	regToken := promptSecretR(r, "Registration token: ")
	regToken = strings.TrimSpace(regToken)
	if regToken == "" {
		return fmt.Errorf("registration token is required — ask your agenthub admin")
	}

	// ── 5. Agent port + hostname ──────────────────────────────────────────────
	portStr := strings.TrimSpace(prompt(r, "Port for this agent [18789]: "))
	if portStr == "" {
		portStr = "18789"
	}
	agentHost := strings.TrimSpace(prompt(r, "Hostname/IP of this machine (as seen from the server) [auto]: "))
	if agentHost == "" {
		agentHost = detectHostname()
	}

	// ── 6. Write config ───────────────────────────────────────────────────────
	configDir, err := agentConfigDir(name)
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	envPath := filepath.Join(configDir, "agent.env")
	// Write initial config (no slack token yet — obtained after registration).
	if err := writeAgentEnv(envPath, name, agentHost, portStr, llmBaseURL, llmModel, llmAPIKey, serverURL, regToken, ""); err != nil {
		return fmt.Errorf("writing agent config: %w", err)
	}
	fmt.Printf("\nConfig written to: %s\n", envPath)

	// ── 7. Register with server (with name-uniqueness re-prompt) ─────────────
	var slackBotToken string
	for {
		fmt.Printf("Registering %q with %s …\n", name, serverURL)
		tok, suggestions, regErr := registerAgent(serverURL, regToken, name, agentHost, portStr)
		if regErr == nil {
			slackBotToken = tok
			fmt.Printf("Agent %q registered.\n", name)
			break
		}
		if len(suggestions) > 0 {
			fmt.Fprintf(os.Stderr, "\nName %q is already taken.\n", name)
			fmt.Fprintf(os.Stderr, "Suggestions: %s\n\n", strings.Join(suggestions, ", "))
			name = strings.TrimSpace(prompt(r, "Choose a different name: "))
			if name == "" {
				return fmt.Errorf("registration cancelled — name required")
			}
			// Rewrite config with new name.
			configDir, _ = agentConfigDir(name)
			_ = os.MkdirAll(configDir, 0700)
			envPath = filepath.Join(configDir, "agent.env")
			_ = writeAgentEnv(envPath, name, agentHost, portStr, llmBaseURL, llmModel, llmAPIKey, serverURL, regToken, "")
			fmt.Printf("Config updated for agent %q → %s\n", name, configDir)
			continue
		}
		return fmt.Errorf("registration failed: %v", regErr)
	}

	// Rewrite config with the slack bot token received from the server.
	if slackBotToken != "" {
		if err := writeAgentEnv(envPath, name, agentHost, portStr, llmBaseURL, llmModel, llmAPIKey, serverURL, regToken, slackBotToken); err != nil {
			return fmt.Errorf("updating agent config with slack token: %w", err)
		}
		fmt.Println("Slack bot token received and saved.")
	}

	// ── 8. Start the agent loop ───────────────────────────────────────────────
	fmt.Println()
	fmt.Println("Registration complete. Starting agent loop...")
	fmt.Println("(Press Ctrl+C to stop)")
	fmt.Println()
	return cmdClientRun([]string{name})
}

// ── Config writer ─────────────────────────────────────────────────────────────

func writeAgentEnv(path, name, host, port, llmBase, llmModel, llmKey, serverURL, regToken, slackBotToken string) error {
	var sb strings.Builder
	sb.WriteString("# agenthub agent config — generated by 'agenthub client create'\n")
	sb.WriteString(fmt.Sprintf("# Agent: %s  Created: %s\n\n", name, time.Now().UTC().Format("2006-01-02 15:04 UTC")))
	writeLine := func(k, v string) {
		safe := strings.ReplaceAll(v, "'", "'\\''")
		sb.WriteString(fmt.Sprintf("%s='%s'\n", k, safe))
	}
	writeLine("AGENT_NAME", name)
	writeLine("AGENT_HOST", host)
	writeLine("AGENT_PORT", port)
	writeLine("LLM_BASE_URL", llmBase)
	writeLine("LLM_MODEL", llmModel)
	writeLine("LLM_API_KEY", llmKey)
	writeLine("AGENTHUB_SERVER_URL", serverURL)
	writeLine("AGENTHUB_REGISTRATION_TOKEN", regToken)
	if slackBotToken != "" {
		writeLine("SLACK_BOT_TOKEN", slackBotToken)
	}
	return os.WriteFile(path, []byte(sb.String()), 0600)
}


// ── helpers ───────────────────────────────────────────────────────────────────

func prompt(r *bufio.Reader, question string) string {
	fmt.Print(question)
	line, _ := r.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

// promptSecretR reads a secret using the shared reader r on non-TTY to avoid
// double-buffering stdin.
func promptSecretR(r *bufio.Reader, question string) string {
	fmt.Print(question)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		pw, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return ""
		}
		return string(pw)
	}
	line, _ := r.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}


func detectHostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "localhost"
}

func agentConfigDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agenthub", "agents", name), nil
}

// registerAgent calls POST /api/register?skip_probe=1 on the agenthub server.
// Returns (slackBotToken, suggestions, nil) on success.
// Returns ("", suggestions, err) on 409 conflict so the caller can re-prompt.
// Returns ("", nil, err) on other errors.
func registerAgent(serverURL, regToken, name, host, portStr string) (slackBotToken string, suggestions []string, err error) {
	type regReq struct {
		Name string `json:"name"`
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	var port int
	if _, scanErr := fmt.Sscanf(portStr, "%d", &port); scanErr != nil {
		return "", nil, fmt.Errorf("invalid port %q: %w", portStr, scanErr)
	}
	body, err := json.Marshal(regReq{Name: name, Host: host, Port: port})
	if err != nil {
		return "", nil, err
	}
	// skip_probe=1: server and agent may be on different networks.
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/register?skip_probe=1", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Registration-Token", regToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		// Server returns {"error":"...", "suggestions":["foo-2","foo-agent"]}
		var conflict struct {
			Error       string   `json:"error"`
			Suggestions []string `json:"suggestions"`
		}
		if decErr := json.NewDecoder(resp.Body).Decode(&conflict); decErr == nil && len(conflict.Suggestions) > 0 {
			return "", conflict.Suggestions, fmt.Errorf("%s", conflict.Error)
		}
		return "", []string{name + "-2", name + "-bot"}, fmt.Errorf("name %q is already taken", name)
	}
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var regResp struct {
		SlackBotToken string `json:"slack_bot_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&regResp)
	return regResp.SlackBotToken, nil, nil
}
