package api

// Webhook Relay — Feature 5
//
// agenthub has full internet reachability. External services (GitHub, CI,
// monitoring) POST webhooks here; agenthub queues the payloads in the
// subscribed agents' inboxes so they can poll and react.
//
// Routes:
//   POST /api/webhooks/subscribe       — agent registers for a channel (token-auth)
//   POST /api/webhooks/unsubscribe     — agent deregisters from a channel (token-auth)
//   GET  /api/webhooks/subscriptions   — list channels the calling agent is subscribed to (token-auth)
//   POST /api/webhooks/{channel}       — external webhook receiver (unauthenticated)
//
// Channels are arbitrary strings (e.g. "github", "ci", "pagerduty").
// The channel name in the POST URL acts as a shared secret — only share it
// with services you trust.  HMAC signature verification can be layered on
// without changing the interface.

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// WebhookRelay stores per-channel subscriptions and routes incoming
// webhook payloads to the matching agents' inboxes.
type WebhookRelay struct {
	mu   sync.RWMutex
	subs map[string][]string // channel → []botName (deduplicated)
}

func newWebhookRelay() *WebhookRelay {
	return &WebhookRelay{subs: make(map[string][]string)}
}

// Subscribe registers botName to receive payloads on channel (idempotent).
func (wr *WebhookRelay) Subscribe(channel, botName string) {
	wr.mu.Lock()
	for _, existing := range wr.subs[channel] {
		if existing == botName {
			wr.mu.Unlock()
			return
		}
	}
	wr.subs[channel] = append(wr.subs[channel], botName)
	wr.mu.Unlock()
}

// Unsubscribe removes botName from channel.
func (wr *WebhookRelay) Unsubscribe(channel, botName string) {
	wr.mu.Lock()
	bots := wr.subs[channel]
	for i, b := range bots {
		if b == botName {
			wr.subs[channel] = append(bots[:i], bots[i+1:]...)
			break
		}
	}
	wr.mu.Unlock()
}

// Subscribers returns the current subscriber list for channel.
func (wr *WebhookRelay) Subscribers(channel string) []string {
	wr.mu.RLock()
	out := make([]string, len(wr.subs[channel]))
	copy(out, wr.subs[channel])
	wr.mu.RUnlock()
	return out
}

// ChannelsFor returns all channels that botName is subscribed to.
func (wr *WebhookRelay) ChannelsFor(botName string) []string {
	wr.mu.RLock()
	var out []string
	for ch, bots := range wr.subs {
		for _, b := range bots {
			if b == botName {
				out = append(out, ch)
				break
			}
		}
	}
	wr.mu.RUnlock()
	return out
}

// --------------------------------------------------------------------------
// HTTP handlers
// --------------------------------------------------------------------------

type webhookSubscribeRequest struct {
	Channel string `json:"channel"` // e.g. "github", "ci", "pagerduty"
	BotName string `json:"bot_name"`
}

// handleWebhookSubscribe handles POST /api/webhooks/subscribe.
func (s *Server) handleWebhookSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req webhookSubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Channel == "" || req.BotName == "" {
		http.Error(w, `{"error":"channel and bot_name are required"}`, http.StatusBadRequest)
		return
	}
	s.webhooks.Subscribe(req.Channel, req.BotName)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleWebhookUnsubscribe handles POST /api/webhooks/unsubscribe.
func (s *Server) handleWebhookUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req webhookSubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Channel == "" || req.BotName == "" {
		http.Error(w, `{"error":"channel and bot_name are required"}`, http.StatusBadRequest)
		return
	}
	s.webhooks.Unsubscribe(req.Channel, req.BotName)
	w.WriteHeader(http.StatusNoContent)
}

// handleWebhookListSubscriptions handles GET /api/webhooks/subscriptions.
func (s *Server) handleWebhookListSubscriptions(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	botName := r.Header.Get("X-Bot-Name")
	channels := s.webhooks.ChannelsFor(botName)
	if channels == nil {
		channels = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(channels)
}

// handleWebhookReceive handles POST /api/webhooks/{channel}.
// This is intentionally unauthenticated so external services (GitHub, CI,
// monitoring tools) can POST without our registration token. The channel
// name itself serves as a shared secret — only share URLs you trust.
func (s *Server) handleWebhookReceive(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")

	// Reject the reserved management sub-paths routed separately.
	if channel == "subscribe" || channel == "unsubscribe" || channel == "subscriptions" {
		http.NotFound(w, r)
		return
	}

	// Read body, capped at 1 MiB.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read error"}`, http.StatusBadRequest)
		return
	}

	bots := s.webhooks.Subscribers(channel)
	if len(bots) == 0 {
		// Accept but silently discard; don't reveal whether the channel exists.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	from := "webhook:" + channel
	for _, bot := range bots {
		s.inbox.Enqueue(bot, from, "", string(body))
	}

	// Nudge SSE clients so the dashboard updates immediately.
	s.events.Broadcast("inbox-update", channel)
	w.WriteHeader(http.StatusNoContent)
}
