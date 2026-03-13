# AGENTS.md — agenthub

## Commandments

1. **No pushes without full tests passing.** Run `make test` before every push. CI enforces this; do not bypass it.
2. **No new code without test coverage.** Every new package and function must have corresponding `_test.go` files. The project enforces a minimum of 90% coverage (`make test-cover`).
3. **No new features without updating the documentation.** All user-facing behavior, configuration keys, and API changes must be reflected in `docs/` before the feature is merged.
4. **Use SEMVER for versioning.** Bump the `VERSION` file on every release: PATCH for bug fixes, MINOR for new features, MAJOR for breaking changes. Tag releases with `git tag v<VERSION>`.

## Work Tracking

This project uses [Beads](https://github.com/steveyegge/beads) for task management, backed by Dolt.
All planned work items are tracked as Beads issues in `.beads/dolt/`.

Useful commands:
```
bd list              # show all open issues
bd show <id>         # show a specific issue
bd add "title"       # create a new issue
bd close <id>        # close an issue
```

Phase plans are also saved as markdown files in `plans/`.

## Development Workflow

This project uses the **responsible-vibe** structured workflow:
- Explore → Plan → Implement → Test → Document

Follow the `plans/` directory for current phase plans and decisions.

## Non-Interactive Shell Rules

- Never use interactive prompts in scripts or Makefile targets.
- All secrets and configuration come from `config.yaml` or the encrypted store (`~/.agenthub/secrets.enc`), never from environment variables baked into scripts.
- Use `make setup` for first-run initialization.

## Landing the Plane Checklist

Before marking any issue closed:
- [ ] All tests pass (`make test`)
- [ ] Coverage is ≥ 90% (`make test-cover`)
- [ ] Docs updated if feature-facing
- [ ] `VERSION` bumped if releasing
- [ ] `CHANGELOG.md` entry added

## Project Structure

```
agenthub/
├── AGENTS.md           # This file
├── VERSION             # SEMVER version string
├── config.yaml         # All tunable global settings
├── go.mod / go.sum
├── Makefile
├── docs/               # Documentation (must stay current)
├── plans/              # Phase plans and architecture decisions
├── src/
│   ├── cmd/agenthub/   # Main entry point
│   └── internal/       # Internal packages
│       ├── api/        # HTTP handlers
│       ├── auth/       # Session auth
│       ├── beads/      # Beads task wrapper
│       ├── config/     # config.yaml loader
│       ├── dolt/       # Dolt SQL client + migrations
│       ├── kanban/     # Kanban board logic
│       ├── openclaw/   # Openclaw HTTP client + liveness
│       ├── openai/     # OpenAI chat wrapper
│       ├── slack/      # Slack Socket Mode + commands
│       └── store/      # Encrypted secrets store
├── web/
│   ├── templates/      # Go HTML templates
│   └── static/         # CSS, JS assets
└── tests/
    └── integration/    # Integration test suite
```
