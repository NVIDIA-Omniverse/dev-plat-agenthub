# Phase 1: Core Infrastructure

**Status:** Planned
**Goal:** Build the foundational packages: config loading, encrypted store, auth, and Dolt DB.

## Packages to Build

### `src/internal/config/`
- Load `config.yaml` via `gopkg.in/yaml.v3`
- `Config` struct with all fields matching config.yaml keys
- Return typed errors for missing required fields
- No defaults in code — all defaults live in config.yaml

### `src/internal/store/`
- AES-256-GCM encrypted secrets store
- Key derived via `golang.org/x/crypto/argon2.IDKey(password, salt, ...)`
- On-disk format: JSON envelope `{version, salt, nonce, ciphertext}` (all base64)
- Plaintext: JSON map of key→value strings
- `Open(path, password string) (*Store, error)`
- `Get(key string) (string, error)`
- `Set(key string, value string) error`
- `Keys() ([]string, error)`
- Default store path: `~/.agenthub/secrets.enc`

### `src/internal/auth/`
- `gorilla/sessions` cookie-based sessions (stateless, no server-side DB)
- Admin password bcrypt hash stored in encrypted store (key: `admin_password_hash`)
- `NewManager(sessionSecret []byte, adminHash []byte) *Manager`
- `Login(w, r, password)` — verify bcrypt, set session cookie
- `RequireAuth(next http.Handler) http.Handler` — redirect to /admin/login if not authed
- `Logout(w, r)` — clear session
- Admin password also derives store encryption key (same password, dual use)

### `src/internal/dolt/`
- Connect to Dolt SQL server via `go-sql-driver/mysql`
- DSN from config: `root:@tcp(127.0.0.1:3306)/agenthub`
- `Open(dsn string) (*DB, error)`
- `Migrate(ctx) error` — run SQL migration files from `src/internal/dolt/migrations/`
- Migration files: `001_initial.sql` (openclaw_instances table)

## Schema: `openclaw_instances`

```sql
CREATE TABLE IF NOT EXISTS openclaw_instances (
    id           VARCHAR(36)  PRIMARY KEY,
    name         VARCHAR(255) UNIQUE NOT NULL,
    host         VARCHAR(255) NOT NULL,
    port         INT          NOT NULL,
    owner_slack_user VARCHAR(255) NOT NULL,
    channel_id   VARCHAR(255) NOT NULL,
    chatty       BOOLEAN      NOT NULL DEFAULT FALSE,
    last_seen_at TIMESTAMP    NULL,
    is_alive     BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);
```

## Key Decisions

- **Dual-use password**: Admin login password both unlocks encrypted store AND is bcrypt-verified for web UI. On password change, store must be re-encrypted.
- **Dolt server mode**: Connect via MySQL driver, not embedded CGO. Avoids double-CGO with beads.
- **No global state**: All packages accept deps via constructor, not package-level vars.

## Test Strategy

- `config_test.go`: load valid YAML, missing file error, unknown fields ignored
- `store_test.go`: round-trip Set/Get, wrong password returns error, file is not readable as plain text
- `auth_test.go`: correct password allows login, wrong password rejected, RequireAuth redirects
- `dolt_test.go`: migration is idempotent (run twice, no error); requires running Dolt server (integration tag)

## Verification

```bash
make test
```
