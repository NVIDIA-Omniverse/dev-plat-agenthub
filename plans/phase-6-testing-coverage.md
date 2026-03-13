# Phase 6: Integration Tests + Coverage Gate

**Status:** Planned
**Goal:** End-to-end integration tests and enforce 90% coverage gate in CI.

## Integration Tests (`tests/integration/`)

Build tag: `//go:build integration`
Run with: `make test-integration`

### Test Scenarios

1. **Bot registration flow**
   - Slack mock sends `/agenthub bind localhost:9999 testbot`
   - Mock openclaw at port 9999 responds 200 to `/health`
   - Verify `openclaw_instances` row created in Dolt
   - Verify `POST /directives` sent with `{"mention_only": true}`

2. **Liveness cycle**
   - Register a bot (alive)
   - Mock openclaw stops responding (timeout)
   - Liveness checker runs
   - Verify `is_alive = false` in DB
   - Verify Slack notification sent to channel

3. **Task creation via slash command**
   - `/agenthub fix the login bug`
   - Verify Beads issue created
   - Verify assigned to an alive bot
   - Verify Slack response with issue ID

4. **Web login flow**
   - POST `/admin/login` with correct password → session cookie set
   - POST `/admin/login` with wrong password → 401 or redirect with error
   - GET `/admin/` without session → redirect to `/admin/login`

5. **Kanban board**
   - Create tasks in multiple states
   - GET `/admin/kanban` → verify all columns rendered

## Coverage Gate

The `make test-cover` target already enforces 90% minimum on `./src/...`.

For CI (e.g., GitHub Actions), add:
```yaml
- name: Test with coverage
  run: make test-cover
```

## Verification

```bash
make test-cover           # unit tests + 90% gate
make test-integration     # integration tests (needs Dolt server + running services)
```
