# Phase 5: HTTP API + Web Admin UI

**Status:** Planned
**Goal:** Build the admin web interface with bot management, kanban board, config editor, and login.

## Package: `src/internal/api/`

### Routes (`api.go`)

```
GET  /                          → redirect to /admin/
GET  /admin/                    → dashboard (auth required)
GET  /admin/bots                → list all bots with status
POST /admin/bots/{name}/remove  → remove a bot binding
POST /admin/bots/{name}/check   → trigger immediate liveness check
GET  /admin/kanban              → kanban board (HTMX partial support)
GET  /admin/config              → view/edit non-secret config
POST /admin/config              → save config changes
GET  /admin/secrets             → set OpenAI key, Slack tokens
POST /admin/secrets             → save to encrypted store
GET  /admin/login               → login form
POST /admin/login               → authenticate
POST /admin/logout              → clear session
GET  /health                    → 200 OK (service liveness; not auth-gated)
```

### Handlers (`handlers.go`)

One function per route. HTMX-friendly:
- GET handlers render full page or HTMX partial (detect `HX-Request` header)
- POST handlers redirect (PRG pattern) or return HTMX swap response
- No JSON API for admin UI (HTML only); REST JSON endpoints reserved for Phase 7 if needed

### Template Embedding

```go
//go:embed ../../../web/templates
var templateFS embed.FS

//go:embed ../../../web/static
var staticFS embed.FS
```

Templates passed to `html/template.ParseFS(templateFS, "web/templates/*.html")`.

## Templates (`web/templates/`)

- `layout.html` — base with nav: Dashboard, Bots, Kanban, Config, Secrets, Logout
- `login.html` — username + password form
- `dashboard.html` — summary: bot count, alive count, open tasks
- `bots.html` — table with name, host, owner, status, last seen; HTMX liveness check button
- `kanban.html` — columns from config; HTMX polling for updates (`hx-trigger="every 30s"`)
- `config.html` — read-only view of config.yaml (editable non-secret fields only)
- `secrets.html` — form to set OpenAI key, Slack tokens (values never shown, only set)

## Static Assets (`web/static/`)

- `htmx.min.js` — vendored (no CDN dependency in production)
- `style.css` — minimal CSS; clean, functional, no framework

## Session Auth

- `auth.RequireAuth` middleware wraps all `/admin/` routes except `/admin/login`
- Session cookie: `agenthub_session` (name from config)
- Session secret: stored in encrypted store (key: `session_secret`); generated on first `setup`
- On login: verify password → open encrypted store → set session

## Test Strategy

- `api_test.go`: login flow, auth middleware redirects, bot list renders, kanban renders
- Use `httptest.NewRecorder()` and `httptest.NewRequest()` throughout
- Mock all dependencies (DB, beads, openclaw) behind interfaces

## Verification

```bash
make test
```
