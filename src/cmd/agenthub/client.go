package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

// cmdClient dispatches agenthub client subcommands.
func cmdClient(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthub client <create> [name]")
	}
	switch args[0] {
	case "create":
		return cmdClientCreate(args[1:])
	default:
		return fmt.Errorf("unknown client subcommand %q — try: create", args[0])
	}
}

// cmdClientCreate runs the interactive agent setup wizard.
//
// Usage: agenthub client create <name>
//
// It collects LLM backend settings, optionally installs LM Studio,
// writes a config env file to ~/.agenthub/agents/<name>/, creates a
// startup script, and optionally registers the agent with an agenthub server.
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
	fmt.Println("LLM backend:")
	fmt.Println("  [1] Local — LM Studio (runs on this machine, no API key needed)")
	fmt.Println("  [2] NVIDIA Inference API (cloud-hosted, requires API key)")
	fmt.Println()
	choice := prompt(r, "Choice [1/2]: ")

	var llmBaseURL, llmModel, llmAPIKey string

	switch choice {
	case "2", "nvidia", "NVIDIA":
		// ── NVIDIA inference ──────────────────────────────────────────────────
		llmBaseURL = "https://inference-api.nvidia.com/v1/"
		llmModel = "azure/openai/gpt-5.2-codex"
		fmt.Printf("\nUsing model: %s\n", llmModel)
		fmt.Printf("Endpoint:    %s\n\n", llmBaseURL)

		llmAPIKey = promptSecret("NVIDIA API key: ")
		if llmAPIKey == "" {
			return fmt.Errorf("API key is required for the NVIDIA Inference backend")
		}

	default:
		// ── Local LM Studio ───────────────────────────────────────────────────
		llmModel = "nvidia_dynamo/nvidia/nemotron-3-super-preview"
		llmBaseURL = "http://localhost:1234/v1"
		fmt.Printf("\nUsing model: %s\n", llmModel)
		fmt.Printf("Endpoint:    %s\n\n", llmBaseURL)

		install := prompt(r, "Install / update LM Studio now? [Y/n]: ")
		if !strings.EqualFold(strings.TrimSpace(install), "n") {
			if err := installLMStudio(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: LM Studio install failed: %v\n", err)
				fmt.Fprintln(os.Stderr, "You can install it manually: curl -fsSL https://lmstudio.ai/install.sh | bash")
			} else {
				fmt.Println("LM Studio installed successfully.")
			}
		}
	}

	// ── 3. Agenthub server URL ────────────────────────────────────────────────
	fmt.Println()
	serverURL := prompt(r, "Agenthub server URL (e.g. http://server:8080): ")
	serverURL = strings.TrimRight(serverURL, "/")
	if serverURL == "" {
		return fmt.Errorf("server URL is required")
	}

	// ── 4. Registration token ─────────────────────────────────────────────────
	regToken := promptSecret("Registration token: ")
	if regToken == "" {
		return fmt.Errorf("registration token is required — run 'agenthub secret get registration_token' on the server")
	}

	// ── 5. Agent listen port ──────────────────────────────────────────────────
	portStr := prompt(r, "Port for this agent to listen on [18789]: ")
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		portStr = "18789"
	}

	// ── 6. This machine's address (as seen from the server) ───────────────────
	agentHost := prompt(r, "Hostname/IP of this machine (as seen from the server) [auto]: ")
	agentHost = strings.TrimSpace(agentHost)
	if agentHost == "" {
		agentHost = detectHostname()
	}

	// ── 7. Write config ───────────────────────────────────────────────────────
	configDir, err := agentConfigDir(name)
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	envPath := filepath.Join(configDir, "agent.env")
	if err := writeAgentEnv(envPath, name, agentHost, portStr, llmBaseURL, llmModel, llmAPIKey, serverURL, regToken); err != nil {
		return fmt.Errorf("writing agent config: %w", err)
	}

	scriptPath := filepath.Join(configDir, "start.sh")
	if err := writeStartScript(scriptPath, name, envPath); err != nil {
		return fmt.Errorf("writing start script: %w", err)
	}

	fmt.Printf("\nConfig written to: %s\n", configDir)
	fmt.Printf("Start script:      %s\n\n", scriptPath)

	// ── 8. Register with server ───────────────────────────────────────────────
	register := prompt(r, "Register agent with the server now? [Y/n]: ")
	if !strings.EqualFold(strings.TrimSpace(register), "n") {
		fmt.Printf("Registering %q with %s …\n", name, serverURL)
		if err := registerAgent(serverURL, regToken, name, agentHost, portStr); err != nil {
			fmt.Fprintf(os.Stderr, "warning: registration failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "You can re-register later; the agent will also self-register on startup.")
		} else {
			fmt.Printf("Agent %q registered successfully.\n", name)
		}
	}

	// ── 9. Done ───────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("Setup complete. To start the agent:")
	fmt.Printf("  bash %s\n", scriptPath)
	fmt.Println()
	fmt.Println("To install as a systemd service:")
	fmt.Printf("  agenthub client install %s  (coming soon)\n", name)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func prompt(r *bufio.Reader, question string) string {
	fmt.Print(question)
	line, _ := r.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func promptSecret(question string) string {
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
	// Non-TTY fallback (pipes / CI).
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func installLMStudio() error {
	fmt.Println("Running: curl -fsSL https://lmstudio.ai/install.sh | bash")
	cmd := exec.Command("bash", "-c", "curl -fsSL https://lmstudio.ai/install.sh | bash")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

func writeAgentEnv(path, name, host, port, llmBase, llmModel, llmKey, serverURL, regToken string) error {
	var sb strings.Builder
	sb.WriteString("# agenthub agent config — generated by 'agenthub client create'\n")
	sb.WriteString(fmt.Sprintf("# Agent: %s  Created: %s\n\n", name, time.Now().UTC().Format("2006-01-02 15:04 UTC")))
	writeLine := func(k, v string) {
		// Wrap value in single-quotes; escape any embedded single-quotes.
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
	return os.WriteFile(path, []byte(sb.String()), 0600)
}

func writeStartScript(path, name, envPath string) error {
	script := fmt.Sprintf(`#!/usr/bin/env bash
# Startup script for agenthub agent %q
set -euo pipefail
set -a; source %q; set +a

# openclaw is the agent runtime; install it separately if needed.
# See: https://github.com/NVIDIA-DevPlat/openclaw
exec openclaw \
  --name        "${AGENT_NAME}" \
  --listen      "${AGENT_HOST}:${AGENT_PORT}" \
  --llm-url     "${LLM_BASE_URL}" \
  --llm-model   "${LLM_MODEL}" \
  --llm-key     "${LLM_API_KEY}" \
  --hub-url     "${AGENTHUB_SERVER_URL}" \
  --hub-token   "${AGENTHUB_REGISTRATION_TOKEN}"
`, name, envPath)
	return os.WriteFile(path, []byte(script), 0755)
}

// registerAgent calls POST /api/register on the agenthub server.
func registerAgent(serverURL, regToken, name, host, portStr string) error {
	type regReq struct {
		Name  string `json:"name"`
		Host  string `json:"host"`
		Port  int    `json:"port"`
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	body, err := json.Marshal(regReq{Name: name, Host: host, Port: port})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Registration-Token", regToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
