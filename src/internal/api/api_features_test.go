package api

// Tests for Features 1–5:
//   1. Agent Inbox API (poll, ack, reply)
//   2. Heartbeat + Task Claiming
//   3. Task Activity Log
//   4. SSE Real-time events endpoint
//   5. Webhook Relay (subscribe, receive, list)

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// --------------------------------------------------------------------------
// Feature 1: Agent Inbox API
// --------------------------------------------------------------------------

func TestInboxPollRequiresToken(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestInboxPollRequiresBotName(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestInboxPollReturnsEmptySlice(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var msgs []*InboxMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Empty(t, msgs)
}

func TestInboxPollReturnsPendingMessages(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.inbox.Enqueue("bot1", "user1", "C123", "hello world")

	r := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var msgs []*InboxMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Len(t, msgs, 1)
	require.Equal(t, "hello world", msgs[0].Text)
	require.Equal(t, "user1", msgs[0].From)
	require.Equal(t, "C123", msgs[0].Channel)

	// Second poll returns same message (still unacked).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	r2.Header.Set("X-Registration-Token", "tok")
	r2.Header.Set("X-Bot-Name", "bot1")
	srv.ServeHTTP(w2, r2)
	var msgs2 []*InboxMessage
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &msgs2))
	require.Len(t, msgs2, 1, "messages persist until acked")

	// After ack, message is gone.
	rAck := httptest.NewRequest(http.MethodPost, "/api/inbox/"+msgs[0].ID+"/ack", nil)
	rAck.Header.Set("X-Registration-Token", "tok")
	rAck.Header.Set("X-Bot-Name", "bot1")
	srv.ServeHTTP(httptest.NewRecorder(), rAck)
	require.Equal(t, 0, srv.inbox.Len("bot1"))
}

func TestInboxAckIsIdempotent(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	// Ack of non-existent message returns 204 (idempotent).
	r := httptest.NewRequest(http.MethodPost, "/api/inbox/msg-999/ack", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

// --------------------------------------------------------------------------
// Feature 2: Heartbeat
// --------------------------------------------------------------------------

func TestHeartbeatRequiresToken(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/heartbeat",
		strings.NewReader(`{"bot_name":"bot1","status":"idle"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHeartbeatRequiresBotName(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodPost, "/api/heartbeat",
		strings.NewReader(`{"status":"idle"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHeartbeatRecordsStatus(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodPost, "/api/heartbeat",
		strings.NewReader(`{"bot_name":"worker1","status":"working","current_task":"AH-5","message":"halfway done"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	bots := srv.heartbeats.All()
	require.Len(t, bots, 1)
	require.Equal(t, "worker1", bots[0].BotName)
	require.Equal(t, "working", bots[0].Status)
	require.Equal(t, "AH-5", bots[0].CurrentTask)
}

func TestHeartbeatReturnsInboxCount(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.inbox.Enqueue("worker1", "system", "", "msg1")
	srv.inbox.Enqueue("worker1", "system", "", "msg2")

	r := httptest.NewRequest(http.MethodPost, "/api/heartbeat",
		strings.NewReader(`{"bot_name":"worker1","status":"idle"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp heartbeatResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.OK)
	require.Equal(t, 2, resp.InboxCount)
}

func TestHeartbeatBotNameFromHeader(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodPost, "/api/heartbeat",
		strings.NewReader(`{"status":"idle"}`))
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "headerbot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	bots := srv.heartbeats.All()
	require.Len(t, bots, 1)
	require.Equal(t, "headerbot", bots[0].BotName)
}

func TestAdminHeartbeatsRequiresAuth(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/heartbeats", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

func TestAdminHeartbeatsReturnsJSON(t *testing.T) {
	srv, _, _ := testServer(t)
	cookie := loginTo(t, srv)
	srv.heartbeats.Update("bot1", "AH-1", "working", "doing stuff")

	r := httptest.NewRequest(http.MethodGet, "/admin/heartbeats", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var bots []*BotStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bots))
	require.Len(t, bots, 1)
	require.Equal(t, "bot1", bots[0].BotName)
}

// --------------------------------------------------------------------------
// Feature 3: Task Activity Log
// --------------------------------------------------------------------------

func TestTaskLogRequiresToken(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/tasks/AH-1/log",
		strings.NewReader(`{"message":"started"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTaskLogReturns503WithNoLogger(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodPost, "/api/tasks/AH-1/log",
		strings.NewReader(`{"message":"started"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestTaskLogRequiresMessage(t *testing.T) {
	spy := &spyTaskLogger{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskLogger = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/AH-1/log",
		strings.NewReader(`{}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTaskLogSuccess(t *testing.T) {
	spy := &spyTaskLogger{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskLogger = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/AH-5/log",
		strings.NewReader(`{"message":"step 1 complete","level":"info"}`))
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "worker1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "AH-5", spy.lastIssueID)
	require.Equal(t, "worker1", spy.lastActor)
	require.Equal(t, "step 1 complete", spy.lastMessage)
}

func TestTaskLogLevelPrefixedForNonInfo(t *testing.T) {
	spy := &spyTaskLogger{}
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.taskLogger = spy

	r := httptest.NewRequest(http.MethodPost, "/api/tasks/AH-5/log",
		strings.NewReader(`{"message":"rate limit hit","level":"warn"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "[warn] rate limit hit", spy.lastMessage)
}

// spyTaskLogger captures the last AddLog call.
type spyTaskLogger struct {
	lastIssueID string
	lastActor   string
	lastMessage string
	err         error
}

func (s *spyTaskLogger) AddLog(_ context.Context, issueID, actor, message string) error {
	s.lastIssueID = issueID
	s.lastActor = actor
	s.lastMessage = message
	return s.err
}

// --------------------------------------------------------------------------
// Feature 4: SSE Events endpoint
// --------------------------------------------------------------------------

func TestAdminEventsRequiresAuth(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/events", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
}

// --------------------------------------------------------------------------
// Feature 5: Webhook Relay
// --------------------------------------------------------------------------

func TestWebhookSubscribeRequiresToken(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/subscribe",
		strings.NewReader(`{"channel":"github","bot_name":"bot1"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWebhookSubscribeSuccess(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/subscribe",
		strings.NewReader(`{"channel":"github","bot_name":"bot1"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, srv.webhooks.Subscribers("github"), "bot1")
}

func TestWebhookSubscribeRequiresFields(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	for _, body := range []string{
		`{"channel":"github"}`,
		`{"bot_name":"bot1"}`,
		`{}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/webhooks/subscribe",
			strings.NewReader(body))
		r.Header.Set("X-Registration-Token", "tok")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", body)
	}
}

func TestWebhookSubscribeIsIdempotent(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/webhooks/subscribe",
			strings.NewReader(`{"channel":"ci","bot_name":"bot1"}`))
		r.Header.Set("X-Registration-Token", "tok")
		srv.ServeHTTP(httptest.NewRecorder(), r)
	}
	require.Len(t, srv.webhooks.Subscribers("ci"), 1)
}

func TestWebhookReceiveRoutesToSubscribedBots(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))

	// Subscribe two bots.
	for _, bot := range []string{"bot1", "bot2"} {
		srv.webhooks.Subscribe("github", bot)
	}

	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/github",
		strings.NewReader(`{"action":"push","repo":"myapp"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)

	// Both bots should have a message in their inbox.
	require.Equal(t, 1, srv.inbox.Len("bot1"))
	require.Equal(t, 1, srv.inbox.Len("bot2"))

	msgs := srv.inbox.Poll("bot1")
	require.Equal(t, `{"action":"push","repo":"myapp"}`, msgs[0].Text)
	require.Equal(t, "webhook:github", msgs[0].From)
}

func TestWebhookReceiveNoSubscribersIsQuiet(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/unknown-channel",
		strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestWebhookListSubscriptions(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.webhooks.Subscribe("github", "bot1")
	srv.webhooks.Subscribe("ci", "bot1")
	srv.webhooks.Subscribe("github", "bot2") // different bot; should not appear for bot1

	r := httptest.NewRequest(http.MethodGet, "/api/webhooks/subscriptions", nil)
	r.Header.Set("X-Registration-Token", "tok")
	r.Header.Set("X-Bot-Name", "bot1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var channels []string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &channels))
	require.ElementsMatch(t, []string{"github", "ci"}, channels)
}

func TestWebhookUnsubscribe(t *testing.T) {
	srv, _, st := testServer(t)
	require.NoError(t, st.Set("registration_token", "tok"))
	srv.webhooks.Subscribe("github", "bot1")
	require.Contains(t, srv.webhooks.Subscribers("github"), "bot1")

	r := httptest.NewRequest(http.MethodPost, "/api/webhooks/unsubscribe",
		strings.NewReader(`{"channel":"github","bot_name":"bot1"}`))
	r.Header.Set("X-Registration-Token", "tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.NotContains(t, srv.webhooks.Subscribers("github"), "bot1")
}

// --------------------------------------------------------------------------
// Inbox unit tests
// --------------------------------------------------------------------------

func TestInboxEnqueueAndLen(t *testing.T) {
	ib := newInbox()
	require.Equal(t, 0, ib.Len("bot1"))
	ib.Enqueue("bot1", "user", "C1", "hello")
	ib.Enqueue("bot1", "user", "C1", "world")
	require.Equal(t, 2, ib.Len("bot1"))
	require.Equal(t, 0, ib.Len("bot2"))
}

func TestInboxAckRemovesOneMessage(t *testing.T) {
	ib := newInbox()
	id := ib.Enqueue("bot1", "user", "C1", "hello")
	ib.Enqueue("bot1", "user", "C1", "world")
	require.True(t, ib.Ack("bot1", id))
	require.Equal(t, 1, ib.Len("bot1"))
	// Non-existent ack returns false but doesn't panic.
	require.False(t, ib.Ack("bot1", "nonexistent"))
}

func TestHeartbeatRegistrySortsAlphabetically(t *testing.T) {
	hr := newHeartbeatRegistry()
	hr.Update("zebra", "", "idle", "")
	hr.Update("alpha", "", "idle", "")
	hr.Update("middle", "", "idle", "")
	bots := hr.All()
	require.Len(t, bots, 3)
	require.Equal(t, "alpha", bots[0].BotName)
	require.Equal(t, "middle", bots[1].BotName)
	require.Equal(t, "zebra", bots[2].BotName)
}

func TestWebhookRelayUnsubscribeNotSubscribed(t *testing.T) {
	wr := newWebhookRelay()
	// Should not panic when bot not subscribed.
	wr.Unsubscribe("github", "nonexistent")
	require.Empty(t, wr.Subscribers("github"))
}
