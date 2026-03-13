# Phase 0: Scaffold

**Status:** Complete
**Goal:** Establish project skeleton, module identity, governance, and CI gate before any feature code.

## Deliverables

- [x] `AGENTS.md` — four commandments + workflow guidance
- [x] `VERSION` — `0.1.0`
- [x] `config.yaml` — all tunable settings with documentation
- [x] `go.mod` — module `github.com/NVIDIA-DevPlat/agenthub`, Go 1.25.8
- [x] `Makefile` — build, test, test-cover (90% gate), fmt, lint, clean, setup
- [x] `src/cmd/agenthub/main.go` — entry point stub (serve, setup, version subcommands)
- [x] `src/cmd/agenthub/main_test.go` — basic command dispatch tests
- [x] `plans/` — this directory
- [x] `docs/` — documentation stubs

## Key Decisions

- **Module path**: `github.com/NVIDIA-DevPlat/agenthub`
- **CGO**: Enabled (required by beads/Dolt). ICU4C handled in Makefile (mirrors beads pattern).
- **No global config singleton**: `Config` struct passed by value to all subsystems.
- **secrets.enc** not in the repo: default path `~/.agenthub/secrets.enc`.

## Verification

```bash
make build
make test
```
