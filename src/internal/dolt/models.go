package dolt

import (
	"encoding/json"
	"time"
)

// ResourceType enumerates supported external resource types.
type ResourceType string

const (
	ResourceTypeGitHubRepo  ResourceType = "github_repo"
	ResourceTypeArtifactory ResourceType = "artifactory"
	ResourceTypeGoogleDrive ResourceType = "google_drive"
	ResourceTypeOneDrive    ResourceType = "onedrive"
	ResourceTypeICloud      ResourceType = "icloud"
)

// User represents an agenthub user account.
type User struct {
	ID        string
	Username  string
	Email     string
	Role      string // "admin" | "user"
	APIToken  string // hashed; empty = disabled
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Resource represents an external resource owned by a user.
type Resource struct {
	ID           string
	OwnerID      string
	ResourceType ResourceType
	Name         string
	ResourceMeta json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Project groups resources under a named project with a Slack channel.
type Project struct {
	ID               string
	OwnerID          string
	Name             string
	Description      string
	SlackChannelID   string
	SlackChannelName string
	BeadsPrefix      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ProjectResource links a resource to a project.
type ProjectResource struct {
	ProjectID  string
	ResourceID string
	IsPrimary  bool
	AddedAt    time.Time
}

// ProjectAgent records an agent authorized to work on a project.
type ProjectAgent struct {
	ProjectID string
	AgentID   string
	GrantedBy string
	GrantedAt time.Time
}

// TaskAssignment records the assignment of a task to an agent.
type TaskAssignment struct {
	ID         string
	TaskID     string
	ProjectID  string
	AgentID    string
	AssignedBy string
	AssignedAt time.Time
	RevokedAt  *time.Time
}

// ResourceGrant is sent in inbox messages — metadata only, no credentials.
type ResourceGrant struct {
	ResourceID   string          `json:"resource_id"`
	ResourceType ResourceType    `json:"resource_type"`
	Name         string          `json:"name"`
	Meta         json.RawMessage `json:"meta"`
}

// ChatMessage represents a message in an owner-bot private chat.
type ChatMessage struct {
	ID        string
	BotName   string
	Sender    string          // "owner" or the bot's name
	Body      string
	Metadata  json.RawMessage
	CreatedAt time.Time
}

// UsageLog records a single LLM API call for usage tracking.
type UsageLog struct {
	ID           string
	BotName      string
	Tier         string
	Model        string
	InputTokens  int
	OutputTokens int
	LatencyMs    int
	CreatedAt    time.Time
}

// UsageSummary aggregates usage by bot, tier, and model.
type UsageSummary struct {
	BotName      string
	Tier         string
	Model        string
	TotalCalls   int
	TotalInput   int
	TotalOutput  int
	AvgLatencyMs int
}

// BotProfile describes a bot's capabilities and constraints (matches Instance.Name).
type BotProfile struct {
	BotName            string
	Description        string
	Specializations    []string
	Tools              []string
	Hardware           json.RawMessage
	MaxConcurrentTasks int
	OwnerContact       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
