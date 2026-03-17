package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

// UsageDB logs LLM usage.
type UsageDB interface {
	CreateUsageLog(ctx context.Context, u dolt.UsageLog) error
}

func (s *Server) usageDB() UsageDB {
	if udb, ok := s.db.(UsageDB); ok {
		return udb
	}
	return nil
}

type escalateRequest struct {
	Messages  []escalateMessage `json:"messages"`
	ModelHint string            `json:"model_hint,omitempty"`
	MaxTokens int               `json:"max_tokens,omitempty"`
}

type escalateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type escalateResponse struct {
	Model   string `json:"model"`
	Content string `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (s *Server) handleLLMEscalate(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	botName := r.Header.Get("X-Bot-Name")
	if botName == "" {
		http.Error(w, `{"error":"X-Bot-Name header required"}`, http.StatusBadRequest)
		return
	}

	var req escalateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		http.Error(w, `{"error":"messages required"}`, http.StatusBadRequest)
		return
	}

	if s.store == nil {
		http.Error(w, `{"error":"settings not configured"}`, http.StatusServiceUnavailable)
		return
	}

	baseURL := s.store.Get("llm_escalation_base_url")
	model := s.store.Get("llm_escalation_model")
	apiKey := s.store.Get("llm_escalation_api_key")

	if baseURL == "" || model == "" || apiKey == "" {
		http.Error(w, `{"error":"escalation model not configured — set llm_escalation_base_url, llm_escalation_model, and llm_escalation_api_key in secrets"}`, http.StatusServiceUnavailable)
		return
	}

	if req.ModelHint != "" {
		model = req.ModelHint
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	type llmMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type llmReq struct {
		Model     string   `json:"model"`
		Messages  []llmMsg `json:"messages"`
		MaxTokens int      `json:"max_tokens"`
	}
	msgs := make([]llmMsg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = llmMsg{Role: m.Role, Content: m.Content}
	}
	llmBody, _ := json.Marshal(llmReq{Model: model, Messages: msgs, MaxTokens: maxTokens})

	start := time.Now()
	llmReqHTTP, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(llmBody))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	llmReqHTTP.Header.Set("Content-Type", "application/json")
	llmReqHTTP.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(llmReqHTTP)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"escalation request failed: %s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	latencyMs := int(time.Since(start).Milliseconds())

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"reading escalation response"}`, http.StatusBadGateway)
		return
	}

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		w.Write(raw)
		return
	}

	var llmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &llmResp); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
		return
	}

	content := ""
	if len(llmResp.Choices) > 0 {
		content = llmResp.Choices[0].Message.Content
	}

	if udb := s.usageDB(); udb != nil {
		_ = udb.CreateUsageLog(r.Context(), dolt.UsageLog{
			ID:           fmt.Sprintf("ul-%x", time.Now().UnixNano()),
			BotName:      botName,
			Tier:         "escalation",
			Model:        model,
			InputTokens:  llmResp.Usage.PromptTokens,
			OutputTokens: llmResp.Usage.CompletionTokens,
			LatencyMs:    latencyMs,
			CreatedAt:    time.Now().UTC(),
		})
	}

	result := escalateResponse{
		Model:   model,
		Content: content,
	}
	result.Usage.InputTokens = llmResp.Usage.PromptTokens
	result.Usage.OutputTokens = llmResp.Usage.CompletionTokens

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// UsageSummaryDB reads usage summaries.
type UsageSummaryDB interface {
	GetUsageSummary(ctx context.Context) ([]*dolt.UsageSummary, error)
}

func (s *Server) usageSummaryDB() UsageSummaryDB {
	if udb, ok := s.db.(UsageSummaryDB); ok {
		return udb
	}
	return nil
}

func (s *Server) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	if !s.auth.IsAuthenticated(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	udb := s.usageSummaryDB()
	if udb == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]struct{}{})
		return
	}
	summaries, err := udb.GetUsageSummary(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if summaries == nil {
		summaries = []*dolt.UsageSummary{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summaries)
}
