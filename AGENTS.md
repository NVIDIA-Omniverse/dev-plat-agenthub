# AGENTS.md — agenthub

## Commandments

1. **No pushes without full tests passing.** Run `make test` before every push. CI enforces this; do not bypass it.
2. **No new code without test coverage.** Every new package and function must have corresponding `_test.go` files. The project enforces a minimum of 90% coverage (`make test-cover`).
3. **No new features without updating the documentation.** All user-facing behavior, configuration keys, and API changes must be reflected in `docs/` before the feature is merged.
4. **Use SEMVER for versioning.** Bump the `VERSION` file on every release: PATCH for bug fixes, MINOR for new features, MAJOR for breaking changes. Tag releases with `git tag v<VERSION>`.

## Commandment 5 — Log Every Commit in MEMORY.md

5. **Every commit must be logged in `MEMORY.md`.** For each commit record:
   - the short hash
   - who made it (author name / email)
   - the date
   - one sentence on the *essence* — the "why", not just what changed

   This applies to human commits and AI-agent commits alike. Do it as part of the
   same commit (update MEMORY.md, stage it, then commit everything together).
   The log is append-only; never edit past entries.

## Commandment 6 — Always Check Pending Work Before Starting

6. **Before writing any code or starting any task, check for open work:**

   ```bash
   # On the VM (or with SSH):
   bd list                         # show all open beads issues
   gh issue list --repo NVIDIA-DevPlat/agenthub   # show open GitHub issues
   ```

   Report the pending work to the user before starting your own experiments,
   so nothing is duplicated and higher-priority issues are not overlooked.
   This applies to humans and agents alike.

## Work Tracking

This project uses [Beads](https://github.com/steveyegge/beads) for task management, backed by Dolt.
The beads data lives on the Azure VM at `/home/jordanh/.beads/dolt/` and is served by the
`beads-dolt.service` systemd unit (dolt SQL server at `127.0.0.1:42251`).

Beads is configured in **server mode** via `~/.beads/metadata.json`. Agenthub connects to it
automatically; the `bd` CLI also reads this file so it connects to the same data.

Useful `bd` commands:
```bash
bd list              # show all open issues
bd show <id>         # show a specific issue
bd create "title"    # create a new issue
bd close <id>        # close an issue
bd q "title"         # quick capture (outputs only the ID)
```

To file a GitHub issue directly:
```bash
gh issue create --repo NVIDIA-DevPlat/agenthub --title "..." --body "..."
```

### Installing `bd` on a new machine

On the VM (requires CGO, dolt libraries):
```bash
CGO_ENABLED=1 /usr/local/go/bin/go install github.com/steveyegge/beads/cmd/bd@v0.60.0
sudo cp ~/go/bin/bd /usr/local/bin/bd
```

On macOS (local dev):
```bash
go install github.com/steveyegge/beads/cmd/bd@v0.60.0
```

The `~/.beads/metadata.json` file in the home directory on the VM points `bd` to the remote
dolt server automatically. On local dev machines, `bd` connects to the local dolt instance.

Phase plans are also saved as markdown files in `plans/`.

## Development Workflow

This project uses the **responsible-vibe** structured workflow:
- Explore → Plan → Implement → Test → Document

Follow the `plans/` directory for current phase plans and decisions.

## Deployment

The production instance runs on an Azure VM. **Always use `make build` on the VM — never build locally or cross-compile.**

### Infrastructure facts (look these up with `az` if ever forgotten)
```
VM name:       agenthub
Public IP:     20.124.109.29   (re-run: az vm list-ip-addresses --output table)
SSH alias:     agenthub        (in ~/.ssh/config — see entry below)
Source dir:    ~/Src/agenthub
Binary:        /usr/local/bin/agenthub  (root-owned)
Service:       /etc/systemd/system/agenthub.service
Config:        /etc/agenthub/config.yaml
Credentials:   /etc/agenthub/credentials  (EnvironmentFile for the service)
```

### SSH config entry — every contributor must have this

Add the following to your `~/.ssh/config` if it isn't already there.
Without it, `ssh agenthub` resolves to nothing and every deploy command fails.

```
Host agenthub
    Hostname 20.124.109.29
    User jordanh
    Port 22
```

Quick check: `ssh -G agenthub | grep hostname` — should print `20.124.109.29`.
If it prints `agenthub` (the alias itself), the entry is missing; add it now.

If the IP ever changes: `az vm list-ip-addresses --output table`, then update
the `Hostname` line above and in your local `~/.ssh/config`.

### Standard deploy sequence
```bash
# 1. On your local machine — verify tests pass, then push
make test
git push origin main

# 2. On the VM — pull, build, stop service, install, start
#    (Must stop before cp — Linux locks running binaries)
ssh agenthub "
  cd ~/Src/agenthub &&
  git pull &&
  make build &&
  sudo systemctl stop agenthub &&
  sudo cp agenthub /usr/local/bin/agenthub &&
  sudo systemctl start agenthub &&
  sudo systemctl status agenthub --no-pager
"
```

> **Stop before copy.** Linux locks running binaries (`Text file busy`).
> Always stop the service before `sudo cp`, then start it again.
>
> Port 8080 is behind Azure NSG — `curl http://20.124.109.29:8080/health`
> will timeout externally. Check the service is running with `systemctl status`
> and `journalctl` instead.

`make build` on the VM handles everything: htmx download if missing, CGO flags,
GOTOOLCHAIN, template/asset embedding, and Version/Build ldflags stamping.

**Why not build locally?** The VM requires CGO (Dolt/beads use `go-icu-regex`).
Cross-compiling from macOS requires matching Linux ICU libraries and is fragile.
The VM has Go installed at `/usr/local/go/bin/go`; just use it.

### Verifying a deployment
```bash
ssh agenthub "agenthub version"
ssh agenthub "sudo systemctl status agenthub --no-pager"
ssh agenthub "sudo journalctl -u agenthub -n 30 --no-pager"
curl http://20.124.109.29:8080/health
```

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
