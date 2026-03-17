package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/NVIDIA-DevPlat/agenthub/src/internal/dolt"
)

type ChatDB interface {
	CreateChatMessage(ctx context.Context, msg dolt.ChatMessage) error
	ListChatMessages(ctx context.Context, botName string, limit, offset int) ([]*dolt.ChatMessage, error)
}

func (s *Server) chatDB() ChatDB {
	if cdb, ok := s.db.(ChatDB); ok {
		return cdb
	}
	return nil
}

type chatMessageResponse struct {
	ID        string    `json:"id"`
	BotName   string    `json:"bot_name"`
	Sender    string    `json:"sender"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type chatPageData struct {
	BotName  string
	Messages []*dolt.ChatMessage
	Profile  *dolt.BotProfile
}

func chatMessageToResponse(m *dolt.ChatMessage) chatMessageResponse {
	return chatMessageResponse{
		ID:        m.ID,
		BotName:   m.BotName,
		Sender:    m.Sender,
		Body:      m.Body,
		CreatedAt: m.CreatedAt,
	}
}

func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticateAPIUser(r); !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	cdb := s.chatDB()
	if cdb == nil {
		http.Error(w, `{"error":"chat db not configured"}`, http.StatusServiceUnavailable)
		return
	}
	botName := r.PathValue("botName")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}
	msgs, err := cdb.ListChatMessages(r.Context(), botName, limit, offset)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	if msgs == nil {
		msgs = []*dolt.ChatMessage{}
	}
	out := make([]chatMessageResponse, len(msgs))
	for i, m := range msgs {
		out[i] = chatMessageToResponse(m)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticateAPIUser(r); !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	cdb := s.chatDB()
	if cdb == nil {
		http.Error(w, `{"error":"chat db not configured"}`, http.StatusServiceUnavailable)
		return
	}
	botName := r.PathValue("botName")
	var body struct {
		Body string `json:"body"`
	}
	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, `{"error":"invalid form"}`, http.StatusBadRequest)
			return
		}
		body.Body = r.FormValue("body")
	}
	msg := dolt.ChatMessage{
		ID:        newMsgID(),
		BotName:   botName,
		Sender:    "owner",
		Body:      body.Body,
		CreatedAt: time.Now().UTC(),
	}
	if err := cdb.CreateChatMessage(r.Context(), msg); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	s.inbox.Enqueue(botName, "owner:chat", "", body.Body)
	s.events.Broadcast("chat-message", botName)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(chatMessageToResponse(&msg))
}

func (s *Server) handleChatReply(w http.ResponseWriter, r *http.Request) {
	if !s.validateRegistrationToken(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	botName := r.PathValue("botName")
	headerBot := r.Header.Get("X-Bot-Name")
	if headerBot != botName {
		http.Error(w, `{"error":"X-Bot-Name must match path"}`, http.StatusBadRequest)
		return
	}
	cdb := s.chatDB()
	if cdb == nil {
		http.Error(w, `{"error":"chat db not configured"}`, http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	msg := dolt.ChatMessage{
		ID:        newMsgID(),
		BotName:   botName,
		Sender:    botName,
		Body:      body.Body,
		CreatedAt: time.Now().UTC(),
	}
	if err := cdb.CreateChatMessage(r.Context(), msg); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	s.events.Broadcast("chat-message", botName)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(chatMessageToResponse(&msg))
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	botName := r.PathValue("botName")
	cdb := s.chatDB()
	msgs := []*dolt.ChatMessage{}
	if cdb != nil {
		var err error
		msgs, err = cdb.ListChatMessages(r.Context(), botName, 50, 0)
		if err != nil {
			s.render(w, "chat.html", pageData{Title: "Chat", Error: err.Error()})
			return
		}
		if msgs == nil {
			msgs = []*dolt.ChatMessage{}
		}
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	var profile *dolt.BotProfile
	if pdb := s.profileDB(); pdb != nil {
		profile, _ = pdb.GetBotProfile(r.Context(), botName)
	}
	data := chatPageData{BotName: botName, Messages: msgs, Profile: profile}
	s.render(w, "chat.html", pageData{Title: "Chat — " + botName, Data: data})
}
