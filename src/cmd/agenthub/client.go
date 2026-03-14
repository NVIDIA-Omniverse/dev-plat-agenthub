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
// Collects LLM backend choice, configures openclaw via environment variables
// and workspace instructions, writes a working start + polling loop script,
// and registers the agent with an agenthub server.
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
	fmt.Println("  [1] Local — LM Studio")
	fmt.Println("      Runs the model directly on this machine using your GPU.")
	fmt.Println("      No API key needed. Model: nvidia_dynamo/nvidia/nemotron-3-super-preview")
	fmt.Println()
	fmt.Println("  [2] NVIDIA Inference API")
	fmt.Println("      Cloud-hosted inference. Requires a personal NVIDIA API key.")
	fmt.Println("      Model: nvidia/llama-3.3-nemotron-super-49b-v1")
	fmt.Println("      → Get a key at https://build.nvidia.com (free tier available)")
	fmt.Println()
	choice := prompt(r, "Choice [1/2]: ")

	var llmBaseURL, llmModel, llmAPIKey string
	var isLocal bool

	switch strings.TrimSpace(choice) {
	case "2", "nvidia", "NVIDIA":
		// ── NVIDIA inference ──────────────────────────────────────────────────
		llmBaseURL = "https://integrate.api.nvidia.com/v1"
		llmModel = "nvidia/llama-3.3-nemotron-super-49b-v1"
		isLocal = false

		fmt.Printf("\nModel:    %s\n", llmModel)
		fmt.Printf("Endpoint: %s\n\n", llmBaseURL)

		for {
			llmAPIKey = promptSecretR(r, "NVIDIA API key (or press Enter to get one): ")
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

	default:
		// ── Local LM Studio ───────────────────────────────────────────────────
		llmBaseURL = "http://localhost:1234/v1"
		llmModel = "nvidia_dynamo/nvidia/nemotron-3-super-preview"
		llmAPIKey = "lm-studio" // LM Studio ignores the key but most clients require non-empty
		isLocal = true

		fmt.Printf("\nModel:    %s\n", llmModel)
		fmt.Printf("Endpoint: %s\n\n", llmBaseURL)

		// Check if LM Studio is already installed.
		lmsPath := findLMStudio()
		if lmsPath == "" {
			install := prompt(r, "LM Studio not found. Install it now? [Y/n]: ")
			if !strings.EqualFold(strings.TrimSpace(install), "n") {
				if err := installLMStudio(); err != nil {
					fmt.Fprintf(os.Stderr, "warning: LM Studio install failed: %v\n", err)
					fmt.Fprintln(os.Stderr, "Install manually: curl -fsSL https://lmstudio.ai/install.sh | bash")
				} else {
					fmt.Println("LM Studio installed.")
					lmsPath = findLMStudio()
				}
			}
		} else {
			fmt.Printf("LM Studio found: %s\n", lmsPath)
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
		return fmt.Errorf("registration token is required — run 'agenthub secret get registration_token' on the server")
	}

	// ── 5. Agent listen port ──────────────────────────────────────────────────
	portStr := strings.TrimSpace(prompt(r, "Port for this agent [18789]: "))
	if portStr == "" {
		portStr = "18789"
	}

	// ── 6. Hostname as seen from the server ───────────────────────────────────
	agentHost := strings.TrimSpace(prompt(r, "Hostname/IP of this machine (as seen from the server) [auto]: "))
	if agentHost == "" {
		agentHost = detectHostname()
	}

	// ── 7. GitHub repo (optional) ─────────────────────────────────────────────
	fmt.Println()
	fmt.Println("GitHub repo for the agent to work on (optional).")
	fmt.Println("The agent will clone this repo and commit changes to it.")
	githubRepo := strings.TrimSpace(prompt(r, "GitHub repo URL (or Enter to skip): "))

	// ── 8. Write files ────────────────────────────────────────────────────────
	configDir, err := agentConfigDir(name)
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	envPath := filepath.Join(configDir, "agent.env")
	if err := writeAgentEnv(envPath, name, agentHost, portStr, llmBaseURL, llmModel, llmAPIKey, serverURL, regToken, githubRepo); err != nil {
		return fmt.Errorf("writing agent config: %w", err)
	}

	loopPath := filepath.Join(configDir, "loop.sh")
	if err := writeLoopScript(loopPath, name, envPath); err != nil {
		return fmt.Errorf("writing loop script: %w", err)
	}

	startPath := filepath.Join(configDir, "start.sh")
	if err := writeStartScript(startPath, name, envPath, loopPath, llmModel, isLocal); err != nil {
		return fmt.Errorf("writing start script: %w", err)
	}

	// Write BOTJILE instructions into openclaw's workspace so the LLM knows
	// how to report task progress back to agenthub.
	if err := writeBOTJILE(name, serverURL, regToken); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write openclaw workspace instructions: %v\n", err)
	}

	fmt.Printf("\nConfig written to: %s\n", configDir)

	// ── 9. Register with server ───────────────────────────────────────────────
	register := prompt(r, "Register agent with the server now? [Y/n]: ")
	if !strings.EqualFold(strings.TrimSpace(register), "n") {
		fmt.Printf("Registering %q with %s …\n", name, serverURL)
		if err := registerAgent(serverURL, regToken, name, agentHost, portStr); err != nil {
			fmt.Fprintf(os.Stderr, "warning: registration failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "You can re-register later by re-running this command.")
		} else {
			fmt.Printf("Agent %q registered.\n", name)
		}
	}

	// ── 10. Done ──────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("Setup complete.")
	fmt.Println()
	fmt.Println("To start the agent:")
	fmt.Printf("  bash %s\n", startPath)
	fmt.Println()
	fmt.Println("The agent will:")
	fmt.Println("  • Send a heartbeat to the agenthub server every 60 seconds")
	fmt.Println("  • Poll for new tasks every 30 seconds")
	fmt.Println("  • Use openclaw to work on each task and commit results to GitHub")
	fmt.Println("  • Post a reply to the original Slack message when done")
	if isLocal {
		fmt.Println()
		fmt.Printf("  NOTE: LM Studio must be running with model %q\n", llmModel)
		fmt.Println("  The start script will attempt to start it automatically.")
	}
	return nil
}

// ── File writers ──────────────────────────────────────────────────────────────

func writeAgentEnv(path, name, host, port, llmBase, llmModel, llmKey, serverURL, regToken, githubRepo string) error {
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
	writeLine("GITHUB_REPO", githubRepo)
	return os.WriteFile(path, []byte(sb.String()), 0600)
}

// writeStartScript writes start.sh — sources env, optionally starts LM Studio,
// sends an initial heartbeat, then exec's the polling loop.
func writeStartScript(path, name, envPath, loopPath, llmModel string, isLocal bool) error {
	lmsBlock := ""
	if isLocal {
		lmsBlock = fmt.Sprintf(`
# ── Start LM Studio server if not already running ─────────────────────────
if ! curl -sf http://localhost:1234/v1/models >/dev/null 2>&1; then
  echo "[agenthub] Starting LM Studio server with model %s..."
  LMS=$(command -v lms 2>/dev/null || echo "${HOME}/.lmstudio/bin/lms")
  if [ -x "${LMS}" ]; then
    "${LMS}" server start --model '%s' &
    LMS_PID=$!
    # Wait up to 30s for the server to become ready.
    for i in $(seq 1 30); do
      sleep 1
      curl -sf http://localhost:1234/v1/models >/dev/null 2>&1 && break
      echo "[agenthub] Waiting for LM Studio... (${i}s)"
    done
    if ! curl -sf http://localhost:1234/v1/models >/dev/null 2>&1; then
      echo "[agenthub] WARNING: LM Studio did not start in time. Tasks may fail."
    else
      echo "[agenthub] LM Studio ready."
    fi
  else
    echo "[agenthub] WARNING: lms not found. Start LM Studio manually."
  fi
else
  echo "[agenthub] LM Studio already running."
fi
`, llmModel, llmModel)
	}

	script := fmt.Sprintf(`#!/usr/bin/env bash
# agenthub agent start script — %s
# Generated by 'agenthub client create'
set -euo pipefail

# Load agent config.
set -a; source %q; set +a

# Pass LLM credentials to openclaw via OpenAI-compatible env vars.
export OPENAI_API_KEY="${LLM_API_KEY}"
export OPENAI_BASE_URL="${LLM_BASE_URL}"
# Some openclaw versions also read these:
export ANTHROPIC_API_KEY="${LLM_API_KEY}"
%s
echo "[agenthub] Starting agent ${AGENT_NAME}..."
echo "[agenthub] LLM: ${LLM_BASE_URL} / ${LLM_MODEL}"
echo "[agenthub] Server: ${AGENTHUB_SERVER_URL}"

# Send an immediate heartbeat so the server shows us alive right away.
curl -sf -X POST "${AGENTHUB_SERVER_URL}/api/heartbeat" \
  -H "Content-Type: application/json" \
  -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
  -d "{\"bot_name\":\"${AGENT_NAME}\",\"status\":\"idle\",\"message\":\"agent started\"}" \
  >/dev/null 2>&1 || true

exec bash %q
`, name, envPath, lmsBlock, loopPath)

	return os.WriteFile(path, []byte(script), 0755)
}

// writeLoopScript writes loop.sh — the inbox polling + openclaw work loop.
func writeLoopScript(path, name, envPath string) error {
	script := fmt.Sprintf(`#!/usr/bin/env bash
# agenthub agent polling loop — %s
# Polls /api/inbox, runs openclaw on each task, posts results back.
set -uo pipefail

set -a; source %q; set +a
export OPENAI_API_KEY="${LLM_API_KEY}"
export OPENAI_BASE_URL="${LLM_BASE_URL}"

OPENCLAW=$(command -v openclaw 2>/dev/null \
  || echo "${HOME}/.npm-global/bin/openclaw")

log() { echo "[$(date -u +%%H:%%M:%%S)] $*"; }

log "Loop started. Polling ${AGENTHUB_SERVER_URL}/api/inbox every 30s"

while true; do
  # ── Heartbeat ──────────────────────────────────────────────────────────
  curl -sf -X POST "${AGENTHUB_SERVER_URL}/api/heartbeat" \
    -H "Content-Type: application/json" \
    -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
    -d "{\"bot_name\":\"${AGENT_NAME}\",\"status\":\"idle\"}" \
    >/dev/null 2>&1 || true

  # ── Poll inbox ─────────────────────────────────────────────────────────
  INBOX=$(curl -sf "${AGENTHUB_SERVER_URL}/api/inbox" \
    -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
    -H "X-Bot-Name: ${AGENT_NAME}" 2>/dev/null || echo "[]")

  COUNT=$(echo "${INBOX}" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)

  if [ "${COUNT}" -gt 0 ]; then
    log "${COUNT} inbox message(s) waiting"

    echo "${INBOX}" | python3 -c "
import sys, json
msgs = json.load(sys.stdin)
for m in msgs:
    print(m.get('id',''), m.get('text',''), m.get('channel',''), sep='|SEP|')
" 2>/dev/null | while IFS='|SEP|' read -r MSG_ID MSG_TEXT MSG_CHANNEL; do
      [ -z "${MSG_ID}" ] && continue
      log "Processing message ${MSG_ID}: ${MSG_TEXT:0:80}..."

      # ── Create task ──────────────────────────────────────────────────
      TASK_RESP=$(curl -sf -X POST "${AGENTHUB_SERVER_URL}/api/tasks" \
        -H "Content-Type: application/json" \
        -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
        -d "{\"title\":$(echo "${MSG_TEXT}" | python3 -c 'import sys,json; print(json.dumps(sys.stdin.read().strip()))'),\"bot_name\":\"${AGENT_NAME}\",\"priority\":2}" \
        2>/dev/null || echo "{}")
      TASK_ID=$(echo "${TASK_RESP}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || echo "")

      if [ -n "${TASK_ID}" ]; then
        # Mark in_progress
        curl -sf -X POST "${AGENTHUB_SERVER_URL}/api/tasks/${TASK_ID}/status" \
          -H "Content-Type: application/json" \
          -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
          -d '{"status":"in_progress"}' >/dev/null 2>&1 || true
        log "Task ${TASK_ID} in_progress"
      fi

      # ── Run openclaw ─────────────────────────────────────────────────
      WORK_DIR=$(mktemp -d)
      if [ -n "${GITHUB_REPO:-}" ]; then
        git clone "${GITHUB_REPO}" "${WORK_DIR}/repo" 2>/dev/null && \
          cd "${WORK_DIR}/repo" || cd "${WORK_DIR}"
      else
        cd "${WORK_DIR}"
      fi

      RESULT_FILE="${WORK_DIR}/result.txt"
      AGENTHUB_TASK_ID="${TASK_ID}" \
        "${OPENCLAW}" agent \
        --workspace "${WORK_DIR}" \
        --message "${MSG_TEXT}" \
        --model "${LLM_MODEL}" \
        >"${RESULT_FILE}" 2>&1
      OPENCLAW_EXIT=$?
      RESULT=$(cat "${RESULT_FILE}" 2>/dev/null | tail -20)

      cd ~
      rm -rf "${WORK_DIR}"

      if [ "${OPENCLAW_EXIT}" -eq 0 ]; then
        NEW_STATUS="closed"
        REPLY="Done: ${RESULT:0:200}"
        log "Task ${TASK_ID} completed successfully"
      else
        NEW_STATUS="blocked"
        REPLY="Error (exit ${OPENCLAW_EXIT}): ${RESULT:0:200}"
        log "Task ${TASK_ID} failed: ${RESULT:0:80}"
      fi

      # ── Update task status ────────────────────────────────────────────
      if [ -n "${TASK_ID}" ]; then
        NOTE_JSON=$(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "${REPLY}" 2>/dev/null || echo "\"${REPLY}\"")
        curl -sf -X POST "${AGENTHUB_SERVER_URL}/api/tasks/${TASK_ID}/status" \
          -H "Content-Type: application/json" \
          -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
          -d "{\"status\":\"${NEW_STATUS}\",\"note\":${NOTE_JSON}}" \
          >/dev/null 2>&1 || true
      fi

      # ── Reply to inbox (posts back to Slack) ─────────────────────────
      REPLY_JSON=$(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "${REPLY}" 2>/dev/null || echo "\"${REPLY}\"")
      curl -sf -X POST "${AGENTHUB_SERVER_URL}/api/inbox/${MSG_ID}/reply" \
        -H "Content-Type: application/json" \
        -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
        -d "{\"text\":${REPLY_JSON}}" \
        >/dev/null 2>&1 || true

      # ── Ack message ───────────────────────────────────────────────────
      curl -sf -X POST "${AGENTHUB_SERVER_URL}/api/inbox/${MSG_ID}/ack" \
        -H "X-Registration-Token: ${AGENTHUB_REGISTRATION_TOKEN}" \
        >/dev/null 2>&1 || true

      log "Message ${MSG_ID} processed."
    done
  fi

  sleep 30
done
`, name, envPath)

	return os.WriteFile(path, []byte(script), 0755)
}

// writeBOTJILE writes agenthub task instructions into openclaw's workspace
// so the agent's LLM system prompt includes the task reporting policy.
func writeBOTJILE(agentName, serverURL, regToken string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	workspaceDir := filepath.Join(home, ".openclaw", "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		return err
	}
	content := fmt.Sprintf(`# AGENTHUB TASK POLICY

You are registered as agent **%s** on the agenthub task tracker at %s.

## When working on a task

The agenthub polling loop will set the environment variable AGENTHUB_TASK_ID
before invoking you. Use it to report progress back to the hub:

**Mark in-progress** (call at the start of every task):
  curl -s -X POST %s/api/tasks/$AGENTHUB_TASK_ID/status \
    -H "Content-Type: application/json" \
    -H "X-Registration-Token: %s" \
    -d '{"status":"in_progress"}'

**Mark done** (call when the task is complete):
  curl -s -X POST %s/api/tasks/$AGENTHUB_TASK_ID/status \
    -H "Content-Type: application/json" \
    -H "X-Registration-Token: %s" \
    -d '{"status":"closed","note":"<one-line summary of what you did>"}'

**Mark blocked** (call if you cannot complete the task):
  curl -s -X POST %s/api/tasks/$AGENTHUB_TASK_ID/status \
    -H "Content-Type: application/json" \
    -H "X-Registration-Token: %s" \
    -d '{"status":"blocked","note":"<reason>"}'

## GitHub workflow

When working on code tasks:
1. Clone or pull the repo
2. Make the changes described in the task
3. Run tests if a test command is documented
4. Commit with a descriptive message referencing the task ID: "feat: <desc> [%s/$AGENTHUB_TASK_ID]"
5. Push and open a PR if the branch is not main

Keep all work focused on the specific task. Do not make unrelated changes.
`,
		agentName, serverURL,
		serverURL, regToken,
		serverURL, regToken,
		serverURL, regToken,
		agentName)

	return os.WriteFile(filepath.Join(workspaceDir, "AGENTHUB.md"), []byte(content), 0644)
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

func findLMStudio() string {
	candidates := []string{
		"lms",
		os.Getenv("HOME") + "/.lmstudio/bin/lms",
		"/usr/local/bin/lms",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
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

// registerAgent calls POST /api/register?skip_probe=1 on the agenthub server.
func registerAgent(serverURL, regToken, name, host, portStr string) error {
	type regReq struct {
		Name string `json:"name"`
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	body, err := json.Marshal(regReq{Name: name, Host: host, Port: port})
	if err != nil {
		return err
	}
	// skip_probe=1: server and agent may be on different networks.
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/register?skip_probe=1", bytes.NewReader(body))
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
