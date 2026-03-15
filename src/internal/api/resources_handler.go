package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

// --------------------------------------------------------------------------
// DB interface extensions needed for resources
// --------------------------------------------------------------------------

// ResourceDB is the subset of dolt.DB used by resource handlers.
type ResourceDB interface {
	CreateResource(ctx context.Context, r dolt.Resource) error
	GetResource(ctx context.Context, id string) (*dolt.Resource, error)
	ListResourcesByOwner(ctx context.Context, ownerID string) ([]*dolt.Resource, error)
	DeleteResource(ctx context.Context, id string) error
	UpdateResourceMeta(ctx context.Context, id string, meta json.RawMessage) error
}

// --------------------------------------------------------------------------
// Server extension: resourceDB field
// --------------------------------------------------------------------------

// resourceDB returns the ResourceDB if the server's db satisfies it, or nil.
func (s *Server) resourceDB() ResourceDB {
	if rdb, ok := s.db.(ResourceDB); ok {
		return rdb
	}
	return nil
}

// --------------------------------------------------------------------------
// Auth helper
// --------------------------------------------------------------------------

// authenticateAPIUser checks either:
//  1. Admin session cookie (any authenticated admin session → "admin-bootstrap-user")
//  2. Authorization: Bearer <api_token> header (hash, look up in DB)
//
// Returns the user ID and true if authenticated.
func (s *Server) authenticateAPIUser(r *http.Request) (string, bool) {
	// 1. Cookie-based admin session.
	if s.auth != nil && s.auth.IsAuthenticated(r) {
		return "admin-bootstrap-user", true
	}
	// 2. Bearer token — not yet wired to user DB; accept registration token as admin.
	bearer := r.Header.Get("Authorization")
	if len(bearer) > 7 && bearer[:7] == "Bearer " {
		token := bearer[7:]
		if s.store != nil {
			stored := s.store.Get("registration_token")
			if stored != "" && token == stored {
				return "admin-bootstrap-user", true
			}
		}
	}
	return "", false
}

// --------------------------------------------------------------------------
// Request/response types
// --------------------------------------------------------------------------

type createResourceRequest struct {
	ResourceType string            `json:"resource_type"`
	Name         string            `json:"name"`
	Meta         json.RawMessage   `json:"meta"`
	Credentials  map[string]string `json:"credentials"`
	OwnerID      string            `json:"owner_id"`
}

type resourceResponse struct {
	ID           string          `json:"id"`
	OwnerID      string          `json:"owner_id"`
	ResourceType string          `json:"resource_type"`
	Name         string          `json:"name"`
	Meta         json.RawMessage `json:"meta"`
	CreatedAt    time.Time       `json:"created_at"`
}

func resourceToResponse(r *dolt.Resource) resourceResponse {
	meta := r.ResourceMeta
	if meta == nil {
		meta = json.RawMessage("{}")
	}
	return resourceResponse{
		ID:           r.ID,
		OwnerID:      r.OwnerID,
		ResourceType: string(r.ResourceType),
		Name:         r.Name,
		Meta:         meta,
		CreatedAt:    r.CreatedAt,
	}
}

// --------------------------------------------------------------------------
// Handlers
// --------------------------------------------------------------------------

// handleCreateResource handles POST /api/resources.
func (s *Server) handleCreateResource(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	rdb := s.resourceDB()
	if rdb == nil {
		http.Error(w, `{"error":"resource db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req createResourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.ResourceType == "" {
		http.Error(w, `{"error":"name and resource_type are required"}`, http.StatusBadRequest)
		return
	}

	ownerID := req.OwnerID
	if ownerID == "" {
		ownerID = userID
	}

	meta := req.Meta
	if meta == nil {
		meta = json.RawMessage("{}")
	}

	id, err := newAPIUUID()
	if err != nil {
		http.Error(w, `{"error":"uuid generation failed"}`, http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	res := dolt.Resource{
		ID:           id,
		OwnerID:      ownerID,
		ResourceType: dolt.ResourceType(req.ResourceType),
		Name:         req.Name,
		ResourceMeta: meta,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := rdb.CreateResource(r.Context(), res); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Store credentials in the encrypted store.
	if s.store != nil {
		for k, v := range req.Credentials {
			if err := s.store.SetResourceCredential(id, k, v); err != nil {
				// Non-fatal: log but continue.
				_ = err
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resourceToResponse(&res))
}

// handleListResources handles GET /api/resources.
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	rdb := s.resourceDB()
	if rdb == nil {
		http.Error(w, `{"error":"resource db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	resources, err := rdb.ListResourcesByOwner(r.Context(), userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	resp := make([]resourceResponse, len(resources))
	for i, res := range resources {
		resp[i] = resourceToResponse(res)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleGetResource handles GET /api/resources/{id}.
func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	rdb := s.resourceDB()
	if rdb == nil {
		http.Error(w, `{"error":"resource db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	res, err := rdb.GetResource(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if res == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if res.OwnerID != userID && userID != "admin-bootstrap-user" {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resourceToResponse(res))
}

// handleDeleteResource handles DELETE /api/resources/{id}.
func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateAPIUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	rdb := s.resourceDB()
	if rdb == nil {
		http.Error(w, `{"error":"resource db not configured"}`, http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	res, err := rdb.GetResource(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if res == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if res.OwnerID != userID && userID != "admin-bootstrap-user" {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	if err := rdb.DeleteResource(r.Context(), id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Remove credentials from store.
	if s.store != nil {
		s.store.DeleteResourceCredentials(id)
	}

	w.WriteHeader(http.StatusNoContent)
}

// --------------------------------------------------------------------------
// Admin UI resource page handlers
// --------------------------------------------------------------------------

// handleResourcesPage renders GET /admin/resources.
func (s *Server) handleResourcesPage(w http.ResponseWriter, r *http.Request) {
	rdb := s.resourceDB()
	var resources []*dolt.Resource
	if rdb != nil {
		resources, _ = rdb.ListResourcesByOwner(r.Context(), "admin-bootstrap-user")
	}
	s.render(w, "resources.html", pageData{Title: "Resources", Data: resources})
}

// handleResourceCreate handles POST /admin/resources (form submit from admin UI).
func (s *Server) handleResourceCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/resources", http.StatusSeeOther)
		return
	}

	rdb := s.resourceDB()
	if rdb == nil {
		http.Redirect(w, r, "/admin/resources", http.StatusSeeOther)
		return
	}

	name := r.FormValue("name")
	resourceType := r.FormValue("resource_type")
	if name == "" || resourceType == "" {
		http.Redirect(w, r, "/admin/resources", http.StatusSeeOther)
		return
	}

	// Build meta JSON from form fields.
	metaMap := map[string]string{}
	if v := r.FormValue("url"); v != "" {
		metaMap["url"] = v
	}
	if v := r.FormValue("clone_url"); v != "" {
		metaMap["clone_url"] = v
	}
	metaBytes, _ := json.Marshal(metaMap)

	id, err := newAPIUUID()
	if err != nil {
		http.Redirect(w, r, "/admin/resources", http.StatusSeeOther)
		return
	}

	now := time.Now().UTC()
	res := dolt.Resource{
		ID:           id,
		OwnerID:      "admin-bootstrap-user",
		ResourceType: dolt.ResourceType(resourceType),
		Name:         name,
		ResourceMeta: metaBytes,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := rdb.CreateResource(r.Context(), res); err != nil {
		http.Redirect(w, r, "/admin/resources", http.StatusSeeOther)
		return
	}

	// Store credential fields from form.
	if s.store != nil {
		if tok := r.FormValue("token"); tok != "" {
			_ = s.store.SetResourceCredential(id, "token", tok)
		}
		if key := r.FormValue("api_key"); key != "" {
			_ = s.store.SetResourceCredential(id, "api_key", key)
		}
	}

	http.Redirect(w, r, "/admin/resources", http.StatusSeeOther)
}

// --------------------------------------------------------------------------
// UUID helper
// --------------------------------------------------------------------------

func newAPIUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
