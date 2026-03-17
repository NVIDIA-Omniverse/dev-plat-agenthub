package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

type ProfileDB interface {
	UpsertBotProfile(ctx context.Context, p dolt.BotProfile) error
	GetBotProfile(ctx context.Context, botName string) (*dolt.BotProfile, error)
	ListBotProfiles(ctx context.Context) ([]*dolt.BotProfile, error)
}

func (s *Server) profileDB() ProfileDB {
	if pdb, ok := s.db.(ProfileDB); ok {
		return pdb
	}
	return nil
}

type profileRequest struct {
	Description        string            `json:"description"`
	Specializations    []string          `json:"specializations"`
	Tools              []string          `json:"tools"`
	Hardware           json.RawMessage   `json:"hardware"`
	MaxConcurrentTasks int               `json:"max_concurrent_tasks"`
	OwnerContact       string            `json:"owner_contact"`
}

type profileResponse struct {
	BotName            string          `json:"bot_name"`
	Description        string          `json:"description"`
	Specializations    []string        `json:"specializations"`
	Tools              []string        `json:"tools"`
	Hardware           json.RawMessage `json:"hardware"`
	MaxConcurrentTasks int             `json:"max_concurrent_tasks"`
	OwnerContact       string          `json:"owner_contact"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

func profileToResponse(p *dolt.BotProfile) profileResponse {
	hardware := p.Hardware
	if hardware == nil {
		hardware = json.RawMessage("{}")
	}
	specs := p.Specializations
	if specs == nil {
		specs = []string{}
	}
	tools := p.Tools
	if tools == nil {
		tools = []string{}
	}
	return profileResponse{
		BotName:            p.BotName,
		Description:        p.Description,
		Specializations:    specs,
		Tools:              tools,
		Hardware:           hardware,
		MaxConcurrentTasks: p.MaxConcurrentTasks,
		OwnerContact:       p.OwnerContact,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		if _, ok := s.authenticateAPIUser(r); !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	}
	pdb := s.profileDB()
	if pdb == nil {
		http.Error(w, `{"error":"profile db not configured"}`, http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	profile, err := pdb.GetBotProfile(r.Context(), name)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if profile == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profileToResponse(profile))
}

func (s *Server) handleUpsertProfile(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		if _, ok := s.authenticateAPIUser(r); !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	}
	pdb := s.profileDB()
	if pdb == nil {
		http.Error(w, `{"error":"profile db not configured"}`, http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	var req profileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	hardware := req.Hardware
	if hardware == nil {
		hardware = json.RawMessage("{}")
	}
	now := time.Now().UTC()
	p := dolt.BotProfile{
		BotName:            name,
		Description:        req.Description,
		Specializations:    req.Specializations,
		Tools:              req.Tools,
		Hardware:           hardware,
		MaxConcurrentTasks: req.MaxConcurrentTasks,
		OwnerContact:       req.OwnerContact,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := pdb.UpsertBotProfile(r.Context(), p); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profileToResponse(&p))
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		if _, ok := s.authenticateAPIUser(r); !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	}
	pdb := s.profileDB()
	if pdb == nil {
		http.Error(w, `{"error":"profile db not configured"}`, http.StatusServiceUnavailable)
		return
	}
	profiles, err := pdb.ListBotProfiles(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	resp := make([]profileResponse, len(profiles))
	for i, p := range profiles {
		resp[i] = profileToResponse(p)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
