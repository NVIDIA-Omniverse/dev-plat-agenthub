package api

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

type registerProfile struct {
	Description        string          `json:"description,omitempty"`
	Specializations    []string        `json:"specializations,omitempty"`
	Tools              []string        `json:"tools,omitempty"`
	Hardware           json.RawMessage `json:"hardware,omitempty"`
	MaxConcurrentTasks int             `json:"max_concurrent_tasks,omitempty"`
	OwnerContact       string          `json:"owner_contact,omitempty"`
}

type registerRequest struct {
	Name           string           `json:"name"`
	Host           string           `json:"host"`
	Port           int              `json:"port"`
	ChannelID      string           `json:"channel_id"`
	OwnerSlackUser string           `json:"owner_slack_user"`
	Profile        *registerProfile `json:"profile,omitempty"`
}

type registerResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SlackBotToken string `json:"slack_bot_token,omitempty"`
}

type nameConflictResponse struct {
	Error       string   `json:"error"`
	Suggestions []string `json:"suggestions"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}

	// Check name uniqueness before anything else.
	if s.db != nil {
		if existing, err := s.db.ListAllInstances(r.Context()); err == nil {
			for _, inst := range existing {
				if strings.EqualFold(inst.Name, req.Name) {
					suggestions := nameSuggestions(req.Name, existing)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(nameConflictResponse{
						Error:       fmt.Sprintf("agent name %q is already taken", req.Name),
						Suggestions: suggestions,
					})
					return
				}
			}
		}
	}

	// Verify the bot is reachable before registering, unless the caller
	// explicitly opts out (e.g. when agent and server are on separate networks).
	skipProbe := r.URL.Query().Get("skip_probe") == "1"
	if s.healthProber != nil && !skipProbe {
		if err := s.healthProber.Probe(r.Context(), req.Host, req.Port); err != nil {
			http.Error(w, `{"error":"bot health check failed: `+err.Error()+`"}`, http.StatusServiceUnavailable)
			return
		}
	}

	id, err := newUUID()
	if err != nil {
		http.Error(w, `{"error":"failed to generate ID"}`, http.StatusInternalServerError)
		return
	}

	inst := dolt.Instance{
		ID:             id,
		Name:           req.Name,
		Host:           req.Host,
		Port:           req.Port,
		ChannelID:      req.ChannelID,
		OwnerSlackUser: req.OwnerSlackUser,
	}

	if s.registrar == nil {
		http.Error(w, `{"error":"registration not configured"}`, http.StatusServiceUnavailable)
		return
	}
	if err := s.registrar.CreateInstance(r.Context(), inst); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	if req.Profile != nil {
		if pdb := s.profileDB(); pdb != nil {
			hw := req.Profile.Hardware
			if hw == nil {
				hw = json.RawMessage("{}")
			}
			now := time.Now().UTC()
			_ = pdb.UpsertBotProfile(r.Context(), dolt.BotProfile{
				BotName:            req.Name,
				Description:        req.Profile.Description,
				Specializations:    req.Profile.Specializations,
				Tools:              req.Profile.Tools,
				Hardware:           hw,
				MaxConcurrentTasks: req.Profile.MaxConcurrentTasks,
				OwnerContact:       req.Profile.OwnerContact,
				CreatedAt:          now,
				UpdatedAt:          now,
			})
		}
	}

	// Create a dedicated Slack channel for this agent (best-effort).
	agentChannelID := ""
	if chID, err := s.createSlackChannel(r.Context(), "agent-"+req.Name); err == nil {
		agentChannelID = chID
		if s.agentSlackUpdater != nil {
			if err := s.agentSlackUpdater.UpdateAgentSlackChannel(r.Context(), req.Name, chID); err != nil {
				slog.Warn("register: failed to store agent slack channel", "agent", req.Name, "error", err)
			}
		}
	} else {
		slog.Warn("register: could not create agent slack channel", "agent", req.Name, "error", err)
	}

	// Announce the new agent to the default Slack channel.
	if s.announcer != nil && s.announceChannel != "" {
		msg := fmt.Sprintf(":robot_face: New agent *%s* has been registered and is ready for tasks!", req.Name)
		if agentChannelID != "" {
			msg += fmt.Sprintf("\nPost directly in <#%s> to send it a message.", agentChannelID)
		}
		if s.publicURL != "" {
			msg += fmt.Sprintf("\n<%s/admin/bots|View dashboard>", s.publicURL)
		}
		_ = s.announcer.PostMessage(r.Context(), s.announceChannel, msg)
	}

	slackBotToken := ""
	if s.store != nil {
		slackBotToken = s.store.Get("slack_bot_token")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(registerResponse{ID: id, Name: req.Name, SlackBotToken: slackBotToken})
}

// nameSuggestions returns a list of alternative names when the requested name is taken.
func nameSuggestions(name string, existing []*dolt.Instance) []string {
	taken := make(map[string]bool, len(existing))
	for _, inst := range existing {
		taken[strings.ToLower(inst.Name)] = true
	}

	var suggestions []string
	candidates := []string{
		name + "-2",
		name + "-agent",
		name + "-bot",
		"my-" + name,
		name + "-v2",
	}
	for _, c := range candidates {
		if !taken[strings.ToLower(c)] {
			suggestions = append(suggestions, c)
			if len(suggestions) == 3 {
				break
			}
		}
	}
	return suggestions
}

// newUUID generates a random UUID v4.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
