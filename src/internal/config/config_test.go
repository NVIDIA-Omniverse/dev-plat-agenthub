package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const validYAML = `
server:
  http_addr: ":9090"
  read_timeout: 15s
  write_timeout: 15s
  session_cookie_name: "test_session"

store:
  path: "/tmp/test.enc"

slack:
  socket_mode: true
  heartbeat_interval: 30s
  command_prefix: "/test"

openclaw:
  liveness_timeout: 5s
  liveness_interval: 30s
  health_path: "/health"
  directives_path: "/directives"

openai:
  model: "gpt-4o-mini"
  max_tokens: 512
  system_prompt: "test prompt"

beads:
  db_path: ".beads/dolt"

dolt:
  dsn: "root:@tcp(127.0.0.1:3306)/agenthub_test"
  max_open_conns: 5
  max_idle_conns: 2
  conn_max_lifetime: 2m

log:
  level: "debug"
  format: "text"

kanban:
  columns:
    - "backlog"
    - "ready"
    - "done"
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validYAML))
	require.NoError(t, err)

	require.Equal(t, ":9090", cfg.Server.HTTPAddr)
	require.Equal(t, 15*time.Second, cfg.Server.ReadTimeout)
	require.Equal(t, "test_session", cfg.Server.SessionCookieName)
	require.Equal(t, "/tmp/test.enc", cfg.Store.Path)
	require.Equal(t, true, cfg.Slack.SocketMode)
	require.Equal(t, "/test", cfg.Slack.CommandPrefix)
	require.Equal(t, 5*time.Second, cfg.Openclaw.LivenessTimeout)
	require.Equal(t, "gpt-4o-mini", cfg.OpenAI.Model)
	require.Equal(t, 512, cfg.OpenAI.MaxTokens)
	require.Equal(t, ".beads/dolt", cfg.Beads.DBPath)
	require.Equal(t, "root:@tcp(127.0.0.1:3306)/agenthub_test", cfg.Dolt.DSN)
	require.Equal(t, 5, cfg.Dolt.MaxOpenConns)
	require.Equal(t, 2*time.Minute, cfg.Dolt.ConnMaxLifetime)
	require.Equal(t, "debug", cfg.Log.Level)
	require.Equal(t, []string{"backlog", "ready", "done"}, cfg.Kanban.Columns)
}

func TestParseMissingHTTPAddr(t *testing.T) {
	data := `
store:
  path: "/tmp/x.enc"
dolt:
  dsn: "root:@tcp(127.0.0.1:3306)/test"
`
	_, err := Parse([]byte(data))
	require.Error(t, err)
	require.Contains(t, err.Error(), "http_addr")
}

func TestParseMissingDSN(t *testing.T) {
	data := `
server:
  http_addr: ":8080"
store:
  path: "/tmp/x.enc"
`
	_, err := Parse([]byte(data))
	require.Error(t, err)
	require.Contains(t, err.Error(), "dolt.dsn")
}

func TestParseMissingStorePath(t *testing.T) {
	data := `
server:
  http_addr: ":8080"
dolt:
  dsn: "root:@tcp(127.0.0.1:3306)/test"
`
	_, err := Parse([]byte(data))
	require.Error(t, err)
	require.Contains(t, err.Error(), "store.path")
}

func TestParseUnknownFieldsIgnored(t *testing.T) {
	data := validYAML + "\nunknown_future_field: some_value\n"
	_, err := Parse([]byte(data))
	require.NoError(t, err)
}

func TestParseInvalidYAML(t *testing.T) {
	_, err := Parse([]byte("{ this is not: valid yaml: ["))
	require.Error(t, err)
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading config file")
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(validYAML), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.Server.HTTPAddr)
}
