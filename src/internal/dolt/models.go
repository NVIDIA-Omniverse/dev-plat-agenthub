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
