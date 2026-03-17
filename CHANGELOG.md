# Changelog

All notable changes to agenthub are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [0.2.0] — 2026-03-17

Five-feature release closing the gap between bot registry and the full agenthub vision.

### Added

- **Bot Capability Profiles** — Structured identity for every bot: description, specializations,
  tools, hardware specs, max concurrent tasks. Profiles are set during registration or via
  `PUT /api/bots/{name}/profile`. Shown in Slack `/agenthub list` and the admin bots page.

- **Owner–Bot Web Chat** — Private chat between admin and individual bots at
  `/admin/chat/{botName}`. Messages stored in Dolt, real-time updates via SSE. Bots detect
  `owner:chat` inbox messages and reply through the chat API instead of Slack.

- **Helpful Onboarding Agent** — Dynamic system prompt built before each Slack LLM call. Injects
  live bot and project data so the assistant can answer agenthub-specific questions about
  installation, registration, slash commands, and project management.

- **Credential Delivery Pipeline** — Task assignment creates a `TaskAssignment` record with a
  credential URL. Agents fetch scoped credentials via `GET /api/credentials/{botName}`. Task
  close auto-revokes the assignment, cutting off credential access.

- **Model Tiering & Escalation** — `POST /api/llm/escalate` proxies to a configured stronger
  model with per-request usage logging. Agent client has a `shouldEscalate` heuristic that
  detects uncertainty in the default model's reply and transparently retries. Admin views usage
  summaries via `GET /api/usage`. Configured via `llm_tiers` in `config.yaml`.

- Three new Dolt schema migrations: `bot_profiles`, `chat_messages`, `usage_log`.

### Changed

- `make test-cover` now measures internal packages only (91.4% coverage). The `cmd/agenthub`
  package contains integration-level server bootstrap code tested via `make test`.

- `cmd/agenthub` tests no longer require a running Dolt server — all DB interactions are mocked.

### Fixed

- Pre-existing `cmd/agenthub` test failures due to missing Dolt server (tests now use sqlmock).

## [0.1.2] — 2026-03-15

### Added

- `make release` script and GitHub Actions release workflow.
- Native Go agent loop (`client_run.go`): heartbeat, inbox poll, LLM call, reply/ack.

## [0.1.1] — 2026-03-15

### Added

- First GitHub release with Linux amd64 binary.

## [0.1.0] — 2026-02-??

### Added

- Initial release: HTTP server, Dolt bot registry, Slack Socket Mode, admin web UI, kanban board,
  inbox relay, heartbeat, activity log, SSE events, webhook relay, secrets management.
