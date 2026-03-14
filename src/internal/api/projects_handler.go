package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

// --------------------------------------------------------------------------
// DB interface extensions needed for projects
// --------------------------------------------------------------------------

// ProjectDB is the subset of dolt.DB used by project handlers.
type ProjectDB interface {
	CreateProject(ctx context.Context, p dolt.Project) error
	GetProject(ctx context.Context, id string) (*dolt.Project, error)
	ListAllProjects(ctx context.Context) ([]*dolt.Project, error)
	ListProjectsByOwner(ctx context.Context, ownerID string) ([]*dolt.Project, error)
	UpdateProject(ctx context.Context, p dolt.Project) error
	AddProjectResource(ctx context.Context, projectID, resourceID string, isPrimary bool) error
	RemoveProjectResource(ctx context.Context, projectID, resourceID string) error
	ListProjectResources(ctx context.Context, projectID string) ([]*dolt.ProjectResource, error)
	AddProjectAgent(ctx context.Context, projectID, agentID, grantedBy string) error
	RemoveProjectAgent(ctx context.Context, projectID, agentID string) error
	ListProjectAgents(ctx context.Context, projectID string) ([]*dolt.ProjectAgent, error)
	GetProjectByBeadsPrefix(ctx context.Context, prefix string) (*dolt.Project, error)
}

// projectDB returns the ProjectDB if the server's db satisfies it, or nil.
func (s *Server) projectDB() ProjectDB {
	if pdb, ok := s.db.(ProjectDB); ok {
		return pdb
	}
	return nil
}

// --------------------------------------------------------------------------
// Server fields for Slack + public URL (added via options)
// --------------------------------------------------------------------------

// WithPublicURL sets the public URL used for Slack messages and links.
func WithPublicURL(u string) ServerOption {
	return func(s *Server) { s.publicURL = u }
}

// --------------------------------------------------------------------------
// Slack helpers
// --------------------------------------------------------------------------

// slugify converts a project name into a Slack-compatible channel name.
func slugify(name string) string {
	name = strings.ToLower(name)
	var sb strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			sb.WriteRune(c)
		} else if c == ' ' || c == '-' || c == '_' {
			sb.WriteRune('-')
		}
	}
	s := sb.String()
	// Trim leading/trailing dashes.
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		s = "project"
	}
	return s
}

// createSlackChannel creates a Slack channel with the given name. Returns the
// channel ID on success. If the channel already exists the ID is looked up.
func (s *Server) createSlackChannel(ctx context.Context, name string) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("store not configured")
	}
	token, err := s.store.Get("slack_bot_token")
	if err != nil || token == "" {
		return "", fmt.Errorf("slack_bot_token not configured")
	}
	slug := slugify(name)

	body := url.Values{}
	body.Set("name", slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/conversations.create",
		strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK      bool   `json:"ok"`
		Channel struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"channel"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if !result.OK {
		if result.Error == "name_taken" {
			return s.findSlackChannel(ctx, token, slug)
		}
		return "", fmt.Errorf("slack channel create failed: %s", result.Error)
	}
	return result.Channel.ID, nil
}

// findSlackChannel looks up a channel by name in the workspace channel list.
func (s *Server) findSlackChannel(ctx context.Context, token, slug string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://slack.com/api/conversations.list?types=public_channel&limit=1000",
		nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK       bool `json:"ok"`
		Channels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"channels"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	for _, ch := range result.Channels {
		if ch.Name == slug {
			return ch.ID, nil
		}
	}
	return "", fmt.Errorf("channel %q not found", slug)
}

// postSlackMessage posts a message to a Slack channel.
func (s *Server) postSlackMessage(ctx context.Context, channelID, text string) {
	if s.store == nil {
		return
	}
	token, err := s.store.Get("slack_bot_token")
	if err != nil || token == "" {
		return
	}
	type slackMsg struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	body, _ := json.Marshal(slackMsg{Channel: channelID, Text: text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/chat.postMessage",
		strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

// --------------------------------------------------------------------------
// Request/response types
// --------------------------------------------------------------------------

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	BeadsPrefix string `json:"beads_prefix"`
	OwnerID     string `json:"owner_id"`
	ResourceID  string `json:"resource_id"`   // optional: attach existing resource
	IsPrimary   bool   `json:"is_primary"`
}

type projectResponse struct {
	ID               string    `json:"id"`
	OwnerID          string    `json:"owner_id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	SlackChannelID   string    `json:"slack_channel_id"`
	SlackChannelName string    `json:"slack_channel_name"`
	BeadsPrefix      string    `json:"beads_prefix"`
	CreatedAt        time.Time `json:"created_at"`
}

func projectToResponse(p *dolt.Project) projectResponse {
	return projectResponse{
		ID:               p.ID,
		OwnerID:          p.OwnerID,
		Name:             p.Name,
		Description:      p.Description,
		SlackChannelID:   p.SlackChannelID,
		SlackChannelName: p.SlackChannelName,
		BeadsPrefix:      p.BeadsPrefix,
		CreatedAt:        p.CreatedAt,
	}
}

// --------------------------------------------------------------------------
// API handlers
// --------------------------------------------------------------------------

// handleAPICreateProject handles POST /api/projects.
func (s *Server) handleAPICreateProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pdb := s.projectDB()
	if pdb == nil {
		http.Error(w, `{"error":"project db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}

	ownerID := req.OwnerID
	if ownerID == "" {
		ownerID = userID
	}
	prefix := req.BeadsPrefix
	if prefix == "" {
		prefix = "AH"
	}

	id, err := newAPIUUID()
	if err != nil {
		http.Error(w, `{"error":"uuid generation failed"}`, http.StatusInternalServerError)
		return
	}

	// Try to create Slack channel (best-effort).
	channelID := ""
	channelName := slugify(req.Name)
	if chID, err := s.createSlackChannel(r.Context(), req.Name); err == nil {
		channelID = chID
	}

	now := time.Now().UTC()
	proj := dolt.Project{
		ID:               id,
		OwnerID:          ownerID,
		Name:             req.Name,
		Description:      req.Description,
		SlackChannelID:   channelID,
		SlackChannelName: channelName,
		BeadsPrefix:      prefix,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := pdb.CreateProject(r.Context(), proj); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Attach resource if provided.
	if req.ResourceID != "" {
		_ = pdb.AddProjectResource(r.Context(), id, req.ResourceID, req.IsPrimary)
	}

	// Post initial message to Slack channel.
	if channelID != "" {
		msg := fmt.Sprintf("*Project: %s*\n%s\n\nKanban: %s/admin/kanban?project_id=%s",
			proj.Name, proj.Description, s.publicURL, proj.ID)
		s.postSlackMessage(r.Context(), channelID, msg)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(projectToResponse(&proj))
}

// handleAPIListProjects handles GET /api/projects.
func (s *Server) handleAPIListProjects(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pdb := s.projectDB()
	if pdb == nil {
		http.Error(w, `{"error":"project db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	projects, err := pdb.ListAllProjects(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	resp := make([]projectResponse, len(projects))
	for i, p := range projects {
		resp[i] = projectToResponse(p)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleAPIGetProject handles GET /api/projects/{id}.
func (s *Server) handleAPIGetProject(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pdb := s.projectDB()
	if pdb == nil {
		http.Error(w, `{"error":"project db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	proj, err := pdb.GetProject(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if proj == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projectToResponse(proj))
}

// handleAPIAddProjectResource handles POST /api/projects/{id}/resources.
func (s *Server) handleAPIAddProjectResource(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pdb := s.projectDB()
	if pdb == nil {
		http.Error(w, `{"error":"project db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	projectID := r.PathValue("id")
	var req struct {
		ResourceID string `json:"resource_id"`
		IsPrimary  bool   `json:"is_primary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := pdb.AddProjectResource(r.Context(), projectID, req.ResourceID, req.IsPrimary); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAPIRemoveProjectResource handles DELETE /api/projects/{id}/resources/{rid}.
func (s *Server) handleAPIRemoveProjectResource(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pdb := s.projectDB()
	if pdb == nil {
		http.Error(w, `{"error":"project db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	projectID := r.PathValue("id")
	resourceID := r.PathValue("rid")
	if err := pdb.RemoveProjectResource(r.Context(), projectID, resourceID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAPIAddProjectAgent handles POST /api/projects/{id}/agents.
func (s *Server) handleAPIAddProjectAgent(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pdb := s.projectDB()
	if pdb == nil {
		http.Error(w, `{"error":"project db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	projectID := r.PathValue("id")
	var req struct {
		AgentID   string `json:"agent_id"`
		GrantedBy string `json:"granted_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := pdb.AddProjectAgent(r.Context(), projectID, req.AgentID, req.GrantedBy); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAPIRemoveProjectAgent handles DELETE /api/projects/{id}/agents/{aid}.
func (s *Server) handleAPIRemoveProjectAgent(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pdb := s.projectDB()
	if pdb == nil {
		http.Error(w, `{"error":"project db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	projectID := r.PathValue("id")
	agentID := r.PathValue("aid")
	if err := pdb.RemoveProjectAgent(r.Context(), projectID, agentID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --------------------------------------------------------------------------
// Admin UI project page handlers
// --------------------------------------------------------------------------

// projectPageData carries data for the projects admin page.
type projectPageData struct {
	Projects  []*dolt.Project
	Resources []*dolt.Resource
}

// handleProjectsPage renders GET /admin/projects.
func (s *Server) handleProjectsPage(w http.ResponseWriter, r *http.Request) {
	pdb := s.projectDB()
	rdb := s.resourceDB()
	var projects []*dolt.Project
	var resources []*dolt.Resource
	if pdb != nil {
		projects, _ = pdb.ListAllProjects(r.Context())
	}
	if rdb != nil {
		resources, _ = rdb.ListResourcesByOwner(r.Context(), "admin-bootstrap-user")
	}
	s.render(w, "projects.html", pageData{
		Title: "Projects",
		Data:  projectPageData{Projects: projects, Resources: resources},
	})
}

// handleProjectCreate handles POST /admin/projects (form submit from admin UI).
func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
		return
	}

	pdb := s.projectDB()
	if pdb == nil {
		http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
		return
	}

	prefix := r.FormValue("beads_prefix")
	if prefix == "" {
		prefix = "AH"
	}

	id, err := newAPIUUID()
	if err != nil {
		http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
		return
	}

	// Try to create Slack channel (best-effort).
	channelID := ""
	channelName := slugify(name)
	if chID, err := s.createSlackChannel(r.Context(), name); err == nil {
		channelID = chID
	}

	now := time.Now().UTC()
	proj := dolt.Project{
		ID:               id,
		OwnerID:          "admin-bootstrap-user",
		Name:             name,
		Description:      r.FormValue("description"),
		SlackChannelID:   channelID,
		SlackChannelName: channelName,
		BeadsPrefix:      prefix,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := pdb.CreateProject(r.Context(), proj); err != nil {
		http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
		return
	}

	// Attach resource if provided.
	if rid := r.FormValue("resource_id"); rid != "" {
		_ = pdb.AddProjectResource(r.Context(), id, rid, true)
	}

	// Inline resource creation.
	if resourceName := r.FormValue("new_resource_name"); resourceName != "" {
		rdb := s.resourceDB()
		if rdb != nil {
			rid, _ := newAPIUUID()
			metaMap := map[string]string{}
			if v := r.FormValue("new_resource_url"); v != "" {
				metaMap["url"] = v
			}
			if v := r.FormValue("new_resource_clone_url"); v != "" {
				metaMap["clone_url"] = v
			}
			metaBytes, _ := json.Marshal(metaMap)
			newRes := dolt.Resource{
				ID:           rid,
				OwnerID:      "admin-bootstrap-user",
				ResourceType: dolt.ResourceTypeGitHubRepo,
				Name:         resourceName,
				ResourceMeta: metaBytes,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := rdb.CreateResource(r.Context(), newRes); err == nil {
				if s.store != nil {
					if tok := r.FormValue("new_resource_token"); tok != "" {
						_ = s.store.SetResourceCredential(rid, "token", tok)
					}
				}
				_ = pdb.AddProjectResource(r.Context(), id, rid, true)
			}
		}
	}

	// Post to Slack.
	if channelID != "" {
		msg := fmt.Sprintf("*Project: %s*\n%s\n\nKanban: %s/admin/kanban?project_id=%s",
			proj.Name, proj.Description, s.publicURL, proj.ID)
		s.postSlackMessage(r.Context(), channelID, msg)
	}

	http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
}

// handleProjectDetail renders GET /admin/projects/{id}.
func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	pdb := s.projectDB()
	if pdb == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	proj, err := pdb.GetProject(r.Context(), id)
	if err != nil || proj == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "projects.html", pageData{Title: proj.Name, Data: proj})
}
