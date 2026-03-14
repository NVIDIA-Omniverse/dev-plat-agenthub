# MEMORY.md — Commit & Session Log

Every commit to this repository must be logged here: hash, author, date, and one sentence
capturing the *essence* (the why, not just the what). Append-only — never edit past entries.
AI agent sessions that span multiple commits get a narrative section below the log.

---

## Commit Log

| Hash | Author | Date | Essence |
|------|--------|------|---------|
| `ab31857` | Jordan Hubbard `jordanhubbard@gmail.com` | 2026-02-? | Blank slate — initial empty repo commit. |
| `fe1396a` | jordanh `jordanh@nvidia.com` | 2026-02-? | First real commit: agenthub v0.1.0 with core HTTP server, Dolt bot registry, Slack Socket Mode integration, and admin web UI. |
| `b76f7f2` | jordanh `jordanh@nvidia.com` | 2026-02-? | Added README documenting setup, architecture, and features so new contributors can orient themselves. |
| `b825d48` | jordanh `jordanh@nvidia.com` | 2026-02-? | Six feature improvements plus a major UI overhaul — richer kanban, bots page, secrets management, HTMX partials. |
| `5d085b3` | jordanh `jordanh@nvidia.com` | 2026-02-? | Added Makefile `deps` and `install` targets so the project could be built and deployed from a clean machine. |
| `4a6b35b` | jordanh `jordanh@nvidia.com` | 2026-02-? | BOTJILE contract: every bot must create a kanban task before starting work, so all work is visible on the board. |
| `201c693` | jordanh `jordanh@nvidia.com` | 2026-02-? | `make install` now runs `deps` first so a single command fully sets up a new machine. |
| `220ebf2` | jordanh `jordanh@nvidia.com` | 2026-02-? | Fixed Linux `deps`: install Go from `go.dev/dl/` instead of `apt` so we always get the required version. |
| `8e0ef8a` | jordanh `jordanh@nvidia.com` | 2026-02-? | Fixed `go mod download` using stale system Go path after the fresh install — PATH ordering bug. |
| `395948f` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Pinned beads to published v0.60.0 and removed local `replace` directive so CI and the VM pull the same version. |
| `202e620` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Expanded `~` in `store.path` so `agenthub serve` could find the secrets file without an absolute path. |
| `92449d7` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Fixed Go template rendering: per-page template map prevents the last-parsed file from overriding all pages. |
| `b84e398` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Auto-create Dolt database on first connect and auto-run `deps` before build so a fresh VM needs no manual setup steps. |
| `445f89d` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Support `AGENTHUB_ADMIN_PASSWORD` env var so systemd/CI can start the server non-interactively. |
| `0d19d61` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Fixed beads integration to use `OpenFromConfig` instead of `Open` so server-mode settings in `metadata.json` are respected. |
| `3aa676b` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Added `agenthub secret set/get/list` subcommand for managing encrypted-store secrets from the CLI without running the server. |
| `075ef79` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Fixed kanban task creation and exposed all beads fields (priority, issue type, assignee, etc.) in the creation form. |
| `ea521d1` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Fixed kanban column names to use valid beads statuses (`open`, `in_progress`, `blocked`, `deferred`, `closed`) — `done` is not valid. |
| `3afa6dc` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Fixed beads init and valid status values — `EnsureInitialized` was silently skipping because `GetConfig` returns `("", nil)` not an error for missing keys. |
| `2935f85` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Root fix for `EnsureInitialized`: check `value == ""` not `err != nil` so the issue prefix is actually written on first startup. |
| `a3735b0` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Added full API test coverage for task form handlers (26 tests) using `spyTaskManager` to assert all 11 form fields are forwarded. |
| `82439ab` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Added 5 agent coordination features (inbox, heartbeat, activity log, SSE kanban, webhook relay) so outbound-only sandboxed agents can communicate bidirectionally through agenthub. |
| `a0add06` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Documented deployment process in AGENTS.md — stop-before-copy pattern, `make build` on VM, `az` IP lookup — so it is never forgotten. |
| `5798779` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Added MEMORY.md session log and corrected deploy sequence (stop service before `sudo cp`). |
| `5d2a248` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Added Commandment 5 to AGENTS.md requiring every commit to be logged in MEMORY.md; backfilled full commit history; added this entry per jordanh's instruction. |
| `(ssh)` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Expanded AGENTS.md SSH config section to instruct all contributors to add the `agenthub` host entry to `~/.ssh/config` — without it every deploy fails. |
| `900c96a` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Switched beads to server mode (remote dolt at 42251); created `beads-dolt.service`; installed `bd` CLI on VM; added Commandment 6 (check open issues before starting). |
| `8fa11cb` | jordanh `jordanh@nvidia.com` | 2026-03-14 | Fix MEMORY.md: updated placeholder hash to real commit hash for 900c96a. |

---

## Session Narratives

Deeper context behind groups of commits, written by the agent(s) who made them.

---

### 2026-03-14 — Claude Sonnet 4.6, Session 1: EnsureInitialized bug fix

**Problem:** `POST /api/tasks` always returned "database not initialized" even after first
startup. Root cause traced by reading beads library source from the module cache zip:
`GetConfig` returns `("", nil)` for missing keys — not an error. The `if err != nil` guard
in `EnsureInitialized` was never triggered, so `issue_prefix` was never written.

**Fix:** `if err != nil` → `if value == ""`. One condition change; confirmed live by
`POST /api/tasks` returning `{"id":"AH-tjm","title":"Test task","status":"open"}`.

**Also:** aligned valid status set (`deferred`, `closed` added; `done` removed); fixed
`GetConfig` mock to return `("", nil)` for missing keys to match real beads behavior.

---

### 2026-03-14 — Claude Sonnet 4.6, Session 2: Full API test coverage

Added `api_task_form_test.go` (26 tests). Key pattern: `spyTaskManager` captures the last
`TaskCreateRequest` so each of the 11 form fields can be asserted individually. Also
covered actor resolution priority (body `bot_name` > `X-Bot-Name` header > `"bot"`),
default priority=2, and all 5 valid / 6 invalid status values for the status endpoint.

---

### 2026-03-14 — Claude Sonnet 4.6, Session 3: Five agent coordination features

**Context:** Agents run in outbound-only sandboxed VMs. They can reach the internet but
cannot receive inbound connections, making Slack Socket Mode impossible. agenthub runs in
Azure with full bidirectional internet access and acts as the relay point for all agent
communication.

Five features added to close the architectural gap:

1. **Agent Inbox** — buffers Slack DMs per-agent; Poll is non-destructive (messages
   persist until Ack'd); Reply posts back to Slack on the agent's behalf.
2. **Heartbeat** — agents prove liveness; response includes `inbox_count` so they know
   when to poll; dashboard shows a live agent grid with staleness detection.
3. **Activity Log** — agents append structured progress notes to tasks as beads comments;
   triggers SSE refresh so the kanban card updates in real time.
4. **SSE Kanban** — replaced 30-second HTMX polling with a `text/event-stream` endpoint;
   `EventBroadcaster` fans out to all connected browsers non-blocking.
5. **Webhook Relay** — external services (GitHub, CI) POST to a channel URL; payloads
   route to all subscribed agents' inboxes. Channel name is the shared secret.

---

### 2026-03-14 — Claude Sonnet 4.6, Session 5: Beads server mode + Commandment 6

**Context:** Beads was running in embedded mode — agenthub spawned a dolt process on every
startup. Issuing multiple concurrent writes caused lock contention. The user asked to switch to
"remote dolt" (server mode) so the dolt process is managed independently.

**Investigation:** Two dolt processes found on VM: port 3306 (main agenthub dolt, systemd
`dolt.service`) and port 42251 (beads-specific dolt, started by agenthub in embedded mode with
the beads database already initialized, `issue_prefix=AH` set). No `~/.beads/metadata.json`
existed — without it, `OpenFromConfig` falls back to embedded mode.

**Fix:** Created `~/.beads/metadata.json` with `dolt_mode: "server"`, `dolt_server_port: 42251`.
Created `/etc/systemd/system/beads-dolt.service` to run the beads dolt persistently at that
port. Updated `agenthub.service` to `Require=beads-dolt.service`. After restarting, agenthub
no longer spawns its own dolt process — only the two managed services remain.

**Also:** Built and installed `bd` v0.60.0 to `/usr/local/bin/bd` on VM (CGO build on VM).
`bd list` shows live issues. Added **Commandment 6** to AGENTS.md: always run `bd list` +
`gh issue list` and report pending work before starting any new task.

---

### 2026-03-14 — Claude Sonnet 4.6, Session 4: Deployment, AGENTS.md, MEMORY.md

Discovered the SSH `agenthub` alias had no real hostname (`az vm list-ip-addresses`
showed `20.124.109.29`). Added it to `~/.ssh/config`. First deploy attempt failed with
`Text file busy` — Linux locks running binaries; fixed by stopping the service before
`sudo cp`. Wrote the correct stop→build→stop→copy→start sequence into AGENTS.md.

Created MEMORY.md as a running commit log. Added Commandment 5 to AGENTS.md requiring
every commit to be logged here. Backfilled all 24 prior commits. Deployed `0.1.0 (build 5798779)` to production.

Per jordanh's instruction: this conversation itself (adding Commandment 5 and backfilling
the commit log) is logged as the `(next)` entry above.
