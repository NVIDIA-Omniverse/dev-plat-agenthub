package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
	"github.com/stretchr/testify/require"
)

type mockUsageDB struct {
	instances []*dolt.Instance
	summaries []*dolt.UsageSummary
	listErr   error
	summaryErr error
}

func (m *mockUsageDB) ListAllInstances(_ context.Context) ([]*dolt.Instance, error) {
	return m.instances, m.listErr
}

func (m *mockUsageDB) GetUsageSummary(_ context.Context) ([]*dolt.UsageSummary, error) {
	return m.summaries, m.summaryErr
}

func (m *mockUsageDB) CreateUsageLog(_ context.Context, _ dolt.UsageLog) error {
	return nil
}

func TestHandleLLMEscalateUnauthorized(t *testing.T) {
	srv, _, st := testServer(t)
	st.Set("registration_token", "secret")
	st.Set("llm_escalation_base_url", "https://api.example.com")
	st.Set("llm_escalation_model", "gpt-4")
	st.Set("llm_escalation_api_key", "sk-xxx")

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Bot-Name", "mybot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "unauthorized")
}

func TestHandleLLMEscalateMissingBotName(t *testing.T) {
	srv, _, st := testServer(t)
	st.Set("registration_token", "secret")
	st.Set("llm_escalation_base_url", "https://api.example.com")
	st.Set("llm_escalation_model", "gpt-4")
	st.Set("llm_escalation_api_key", "sk-xxx")

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "secret")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "X-Bot-Name")
}

func TestHandleLLMEscalateEmptyMessages(t *testing.T) {
	srv, _, st := testServer(t)
	st.Set("registration_token", "secret")

	body := `{"messages":[]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "secret")
	r.Header.Set("X-Bot-Name", "mybot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "messages required")
}

func TestHandleLLMEscalateNotConfigured(t *testing.T) {
	srv, _, st := testServer(t)
	st.Set("registration_token", "secret")

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/llm/escalate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Registration-Token", "secret")
	r.Header.Set("X-Bot-Name", "mybot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "escalation model not configured")
}

func TestHandleUsageSummaryRequiresAuth(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/admin/login", w.Header().Get("Location"))
}

func TestHandleUsageSummaryEmptyWhenNoDB(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var result []interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	require.Empty(t, result)
}

func TestHandleUsageSummaryWithData(t *testing.T) {
	udb := &mockUsageDB{
		summaries: []*dolt.UsageSummary{
			{BotName: "bot1", Tier: "escalation", Model: "gpt-4", TotalCalls: 5, TotalInput: 500, TotalOutput: 250, AvgLatencyMs: 120},
		},
	}
	srv := testServerWithOptions(t, withUsageDB(udb))
	cookie := loginTo(t, srv)

	r := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var result []*dolt.UsageSummary
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	require.Len(t, result, 1)
	require.Equal(t, "bot1", result[0].BotName)
	require.Equal(t, "escalation", result[0].Tier)
	require.Equal(t, 5, result[0].TotalCalls)
}

func withUsageDB(udb *mockUsageDB) ServerOption {
	return func(s *Server) {
		s.db = udb
	}
}
