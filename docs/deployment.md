# agenthub Deployment Guide

## Prerequisites

### macOS / Linux

1. **Go 1.25.8+** with CGO enabled
   ```bash
   go version  # should be >= 1.25.8
   ```

2. **ICU4C** (required by the Dolt embedded database via beads)
   ```bash
   # macOS
   brew install icu4c

   # Ubuntu/Debian
   sudo apt-get install libicu-dev
   ```

3. **Dolt SQL server** (for agenthub's bot registry schema)
   ```bash
   # Install Dolt
   curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash
   # or
   brew install dolt

   # Initialize and start
   mkdir -p ~/.agenthub/dolt && cd ~/.agenthub/dolt
   dolt init
   dolt sql-server --host=127.0.0.1 --port=3306 &
   ```

4. **Slack App** (see `docs/slack-integration.md`)

## Building

```bash
git clone https://github.com/NVIDIA-DevPlat/agenthub
cd agenthub
make build
# Binary: ./agenthub
```

## First-Run Setup

```bash
./agenthub setup
```

This prompts for an admin username and password, then:
- Creates `~/.agenthub/secrets.enc` (encrypted store)
- Generates a random session signing secret
- Stores the bcrypt password hash

## Configuration

Copy and edit `config.yaml`:
```bash
cp config.yaml /etc/agenthub/config.yaml
# or use the default in the working directory
```

Set secrets via the admin web UI after first login:
- OpenAI API key
- Slack Bot Token (`xoxb-`)
- Slack App Token (`xapp-`)

## Running

```bash
./agenthub serve
# or with custom config path:
./agenthub serve --config /etc/agenthub/config.yaml
```

The admin web UI will be available at `http://localhost:8080` (or the configured `server.http_addr`).

## Admin Password Recovery

If you lose the admin password, you must re-run setup. The encrypted store will be re-initialized and all stored secrets will need to be re-entered:
```bash
./agenthub setup --reset
```

**Important:** Re-running setup with `--reset` destroys all stored secrets. You will need to re-enter your OpenAI key and Slack tokens.

## Docker (Future)

A `Dockerfile` will be added in a future release. The binary requires CGO and ICU4C at build time; the runtime image will need `libicu` installed.

## Environment Variables

No secrets should be passed via environment variables. All secrets are in the encrypted store. The only environment-level override supported is:

| Variable | Description |
|----------|-------------|
| `AGENTHUB_CONFIG` | Override path to `config.yaml` |
