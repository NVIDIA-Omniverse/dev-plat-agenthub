// Package config loads and validates agenthub's configuration from config.yaml.
// All tunable behavior lives here; nothing is hard-coded. Secrets are NOT stored
// in config — they live in the encrypted store (see package store).
package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure. It is passed by value to all
// subsystems; there is no global singleton.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Store    StoreConfig    `yaml:"store"`
	Slack    SlackConfig    `yaml:"slack"`
	Openclaw OpenclawConfig `yaml:"openclaw"`
	OpenAI   OpenAIConfig   `yaml:"openai"`
	Beads    BeadsConfig    `yaml:"beads"`
	Dolt     DoltConfig     `yaml:"dolt"`
	Log      LogConfig      `yaml:"log"`
	Kanban   KanbanConfig   `yaml:"kanban"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	HTTPAddr          string        `yaml:"http_addr"`
	PublicURL         string        `yaml:"public_url"`          // externally reachable base URL (used in bot onboarding)
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	SessionCookieName string        `yaml:"session_cookie_name"`
}

// StoreConfig holds the encrypted secrets store location.
type StoreConfig struct {
	Path string `yaml:"path"`
}

// SlackConfig holds Slack integration settings.
type SlackConfig struct {
	SocketMode        bool          `yaml:"socket_mode"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	CommandPrefix     string        `yaml:"command_prefix"`
}

// OpenclawConfig holds openclaw client and liveness settings.
type OpenclawConfig struct {
	LivenessTimeout  time.Duration `yaml:"liveness_timeout"`
	LivenessInterval time.Duration `yaml:"liveness_interval"`
	HealthPath       string        `yaml:"health_path"`
	DirectivesPath   string        `yaml:"directives_path"`
}

// OpenAIConfig holds OpenAI client settings.
type OpenAIConfig struct {
	Model        string `yaml:"model"`
	MaxTokens    int    `yaml:"max_tokens"`
	SystemPrompt string `yaml:"system_prompt"`
}

// BeadsConfig holds Beads/Dolt database settings.
type BeadsConfig struct {
	DBPath string `yaml:"db_path"`
}

// DoltConfig holds the Dolt SQL server connection settings.
type DoltConfig struct {
	DSN             string        `yaml:"dsn"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// KanbanConfig holds kanban board settings.
type KanbanConfig struct {
	Columns []string `yaml:"columns"`
}

// Load reads and parses the YAML config file at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes YAML bytes into a Config.
func Parse(data []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(false) // tolerate unknown fields for forward compatibility
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config YAML: %w", err)
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate checks that required fields are populated.
func validate(cfg Config) error {
	if cfg.Server.HTTPAddr == "" {
		return fmt.Errorf("server.http_addr must not be empty")
	}
	if cfg.Dolt.DSN == "" {
		return fmt.Errorf("dolt.dsn must not be empty")
	}
	if cfg.Store.Path == "" {
		return fmt.Errorf("store.path must not be empty")
	}
	return nil
}
