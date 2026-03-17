// Package config loads and validates agenthub's configuration from config.yaml.
// All tunable behavior lives here; nothing is hard-coded. Secrets are NOT stored
// in config — they live in the encrypted store (see package store).
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	LLMTiers LLMTiersConfig `yaml:"llm_tiers"`
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
	DefaultChannel    string        `yaml:"default_channel"` // channel ID for bot registration announcements + DM routing (e.g. simtech-sandbox)
}

// OpenclawConfig holds openclaw client and liveness settings.
type OpenclawConfig struct {
	LivenessTimeout  time.Duration `yaml:"liveness_timeout"`
	LivenessInterval time.Duration `yaml:"liveness_interval"`
	HealthPath       string        `yaml:"health_path"`
	DirectivesPath   string        `yaml:"directives_path"`
}

// OpenAIConfig holds OpenAI-compatible client settings.
// Set base_url to use any OpenAI-compatible endpoint (NVIDIA Inference API, etc.)
type OpenAIConfig struct {
	BaseURL      string `yaml:"base_url"`      // optional; defaults to api.openai.com
	Model        string `yaml:"model"`
	MaxTokens    int    `yaml:"max_tokens"`
	SystemPrompt string `yaml:"system_prompt"`
}

// LLMTierConfig holds settings for a single LLM tier.
type LLMTierConfig struct {
	BaseURL       string `yaml:"base_url"`
	Model         string `yaml:"model"`
	APIKeySetting string `yaml:"api_key_setting"`
	MaxTokens     int    `yaml:"max_tokens"`
}

// LLMTiersConfig holds the model tiering configuration.
type LLMTiersConfig struct {
	Default    LLMTierConfig `yaml:"default"`
	Escalation LLMTierConfig `yaml:"escalation"`
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
	cfg.Store.Path = expandHome(cfg.Store.Path)
	return cfg, nil
}

// expandHome replaces a leading ~ with the current user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
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
