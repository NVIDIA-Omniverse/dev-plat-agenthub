package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

// --------------------------------------------------------------------------
// DB interface for credential delivery
// --------------------------------------------------------------------------

// AssignmentDB is the subset needed for task assignment + credential delivery.
type AssignmentDB interface {
	GetTaskAssignment(ctx context.Context, id string) (*dolt.TaskAssignment, error)
	CreateTaskAssignment(ctx context.Context, ta dolt.TaskAssignment) error
	GetActiveAssignmentByTaskAndAgent(ctx context.Context, taskID, agentID string) (*dolt.TaskAssignment, error)
	GetActiveAssignmentByTask(ctx context.Context, taskID string) (*dolt.TaskAssignment, error)
	RevokeTaskAssignment(ctx context.Context, id string) error
	ListActiveAssignmentsByAgent(ctx context.Context, agentID string) ([]*dolt.TaskAssignment, error)
}

// assignmentDB returns the AssignmentDB if the server's db satisfies it.
func (s *Server) assignmentDB() AssignmentDB {
	if adb, ok := s.db.(AssignmentDB); ok {
		return adb
	}
	return nil
}

// --------------------------------------------------------------------------
// Response type
// --------------------------------------------------------------------------

type credentialResource struct {
	ResourceID   string            `json:"resource_id"`
	ResourceType string            `json:"resource_type"`
	Name         string            `json:"name"`
	Meta         json.RawMessage   `json:"meta"`
	Credentials  map[string]string `json:"credentials"`
}

type credentialResponse struct {
	TaskAssignmentID string               `json:"task_assignment_id"`
	TaskID           string               `json:"task_id"`
	ProjectID        string               `json:"project_id"`
	ProjectName      string               `json:"project_name"`
	Resources        []credentialResource `json:"resources"`
}

// --------------------------------------------------------------------------
// Handler
// --------------------------------------------------------------------------

// handleGetCredentials handles GET /api/credentials/{task_assignment_id}.
// Auth: X-Registration-Token + X-Bot-Name (bot name must match the assignment's agent).
func (s *Server) handleGetCredentials(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	botName := r.Header.Get("X-Bot-Name")
	if botName == "" {
		http.Error(w, `{"error":"X-Bot-Name header required"}`, http.StatusBadRequest)
		return
	}

	adb := s.assignmentDB()
	if adb == nil {
		http.Error(w, `{"error":"assignment db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	assignmentID := r.PathValue("task_assignment_id")
	ta, err := adb.GetTaskAssignment(r.Context(), assignmentID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if ta == nil {
		http.Error(w, `{"error":"assignment not found"}`, http.StatusNotFound)
		return
	}

	// Verify the requesting bot owns this assignment.
	// Look up agent by name in the instance DB.
	agentID := ""
	if s.db != nil {
		if insts, err := s.db.ListAllInstances(r.Context()); err == nil {
			for _, inst := range insts {
				if inst.Name == botName {
					agentID = inst.ID
					break
				}
			}
		}
	}
	if agentID != ta.AgentID && ta.AgentID != "" {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	// Get project name.
	projectName := ""
	pdb := s.projectDB()
	var projectResources []*dolt.ProjectResource
	if pdb != nil && ta.ProjectID != "" {
		if proj, err := pdb.GetProject(r.Context(), ta.ProjectID); err == nil && proj != nil {
			projectName = proj.Name
		}
		projectResources, _ = pdb.ListProjectResources(r.Context(), ta.ProjectID)
	}

	// Build credential resources list.
	var credResources []credentialResource
	rdb := s.resourceDB()
	for _, pr := range projectResources {
		if rdb == nil {
			break
		}
		res, err := rdb.GetResource(r.Context(), pr.ResourceID)
		if err != nil || res == nil {
			continue
		}
		creds := map[string]string{}
		if s.store != nil {
			for _, key := range []string{"token", "refresh_token", "secret", "password", "api_key"} {
				if v := s.store.GetResourceCredential(res.ID, key); v != "" {
					creds[key] = v
				}
			}
		}
		meta := res.ResourceMeta
		if meta == nil {
			meta = json.RawMessage("{}")
		}
		credResources = append(credResources, credentialResource{
			ResourceID:   res.ID,
			ResourceType: string(res.ResourceType),
			Name:         res.Name,
			Meta:         meta,
			Credentials:  creds,
		})
	}

	resp := credentialResponse{
		TaskAssignmentID: ta.ID,
		TaskID:           ta.TaskID,
		ProjectID:        ta.ProjectID,
		ProjectName:      projectName,
		Resources:        credResources,
	}
	if resp.Resources == nil {
		resp.Resources = []credentialResource{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
