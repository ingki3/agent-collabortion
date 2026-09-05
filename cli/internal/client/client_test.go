package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

func newClient(t *testing.T, s *clienttest.Server, mut func(map[string]string)) *client.Client {
	t.Helper()
	env := s.Env(t.TempDir())
	if mut != nil {
		mut(env)
	}
	return client.New(client.FromEnv(clienttest.Getenv(env)))
}

// E15-04: test chat has no token → every command fails with exit 4.
func TestNoToken(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s, func(e map[string]string) { delete(e, "COLAB_TASK_TOKEN") })
	_, err := c.GetSession(context.Background(), clienttest.SessionID)
	if got := client.ExitCode(err); got != client.ExitNoToken {
		t.Fatalf("exit = %d, want 4 (%v)", got, err)
	}
	if client.AsError(err).Code != "no_token" {
		t.Fatalf("code = %q", client.AsError(err).Code)
	}
	if len(s.Requests) != 0 {
		t.Fatalf("no request must reach the server without a token, got %d", len(s.Requests))
	}
}

// E11-04: revoked token → 401 token_revoked → exit 4, nothing stored.
func TestRevokedToken(t *testing.T) {
	s := clienttest.New(t)
	s.Revoked = true
	c := newClient(t, s, nil)
	_, _, _, err := c.PostMessage(context.Background(), clienttest.SessionID, client.MessageCreate{Content: "orphan"}, "k1")
	e := client.AsError(err)
	if e.Exit != client.ExitNoToken || e.Code != "token_revoked" || e.Status != 401 {
		t.Fatalf("got exit=%d code=%q status=%d", e.Exit, e.Code, e.Status)
	}
	if e.Problem == nil || e.Problem.Title != "Task token revoked" {
		t.Fatalf("problem+json not parsed: %+v", e.Problem)
	}
	if len(s.Posted) != 0 {
		t.Fatalf("stored %d messages, want 0", len(s.Posted))
	}
}

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		status int
		code   string
		exit   int
	}{
		{401, "unauthorized", 4},
		{403, "not_participant", 3},
		{404, "not_found", 3},
		{409, "hitl_already_open", 3},
		{422, "validation_failed", 3},
		{500, "internal", 5},
		{503, "unavailable", 5},
	}
	for _, tc := range cases {
		s := clienttest.New(t)
		s.Fail, s.FailCode = tc.status, tc.code
		c := newClient(t, s, nil)
		_, err := c.GetSession(context.Background(), clienttest.SessionID)
		e := client.AsError(err)
		if e.Exit != tc.exit || e.Status != tc.status {
			t.Errorf("%d: exit=%d status=%d want exit=%d", tc.status, e.Exit, e.Status, tc.exit)
		}
		if tc.status != 401 && e.Code != tc.code {
			t.Errorf("%d: code=%q want %q", tc.status, e.Code, tc.code)
		}
	}
}

func TestUnreachable(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c := client.New(client.Config{Token: clienttest.Token, ServerURL: dead.URL, StateDir: t.TempDir()})
	_, err := c.GetSession(context.Background(), clienttest.SessionID)
	if got := client.ExitCode(err); got != client.ExitUnreachable {
		t.Fatalf("exit = %d, want 5 (%v)", got, err)
	}
	// Non-problem body (proxy HTML) still maps by status.
	c2 := client.New(client.Config{Token: clienttest.Token, ServerURL: "", StateDir: t.TempDir()})
	if _, err := c2.GetSession(context.Background(), "x"); client.ExitCode(err) != client.ExitUnreachable {
		t.Fatalf("empty server url should be exit 5, got %v", err)
	}
}

func TestBearerAndPrefix(t *testing.T) {
	s := clienttest.New(t)
	for _, url := range []string{s.URL, s.URL + "/", s.URL + "/api/v1"} {
		c := newClient(t, s, func(e map[string]string) { e["COLAB_SERVER_URL"] = url })
		if _, err := c.GetSession(context.Background(), clienttest.SessionID); err != nil {
			t.Fatalf("url %q: %v", url, err)
		}
	}
	last := s.Requests[len(s.Requests)-1]
	if last.Header.Get("Authorization") != "Bearer "+clienttest.Token {
		t.Fatalf("auth header = %q", last.Header.Get("Authorization"))
	}
	if last.URL.Path != "/api/v1/sessions/"+clienttest.SessionID {
		t.Fatalf("path = %q", last.URL.Path)
	}
}

func TestContextResolvesSessionAndAttempt(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s, func(e map[string]string) {
		delete(e, "COLAB_SESSION_ID")
		delete(e, "COLAB_TASK_ID")
	})
	ctx := context.Background()
	sid, err := c.SessionID(ctx, "")
	if err != nil || sid != clienttest.SessionID {
		t.Fatalf("session = %q, %v", sid, err)
	}
	task, attempt, err := c.TaskScope(ctx)
	if err != nil || task != clienttest.TaskID || attempt != clienttest.Attempt {
		t.Fatalf("scope = %q/%d, %v", task, attempt, err)
	}
	// /cli/context is fetched once and cached.
	n := 0
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/cli/context") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("/cli/context called %d times, want 1", n)
	}
	if sid, _ := c.SessionID(ctx, "explicit-id"); sid != "explicit-id" {
		t.Fatalf("explicit session not honoured")
	}
}

// E8-04: the Idempotency-Key is task:attempt:seq; re-sending the same key
// returns the same response (Idempotent-Replayed) and stores nothing new.
func TestIdempotencyKeyAndReplay(t *testing.T) {
	s := clienttest.New(t)
	state := t.TempDir()
	c := newClient(t, s, func(e map[string]string) { e["COLAB_STATE_DIR"] = state })
	ctx := context.Background()

	r1, key1, replayed, err := c.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m1"}, "")
	if err != nil || replayed {
		t.Fatalf("first post: %v replayed=%v", err, replayed)
	}
	if want := client.IdempotencyKey(clienttest.TaskID, clienttest.Attempt, 1); key1 != want {
		t.Fatalf("key = %q, want %q", key1, want)
	}
	// A new process (new client, same state dir) continues the sequence.
	c2 := newClient(t, s, func(e map[string]string) { e["COLAB_STATE_DIR"] = state })
	_, key2, _, err := c2.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m2"}, "")
	if err != nil || key2 != client.IdempotencyKey(clienttest.TaskID, clienttest.Attempt, 2) {
		t.Fatalf("second key = %q (%v)", key2, err)
	}
	// Retry with the first key → identical body, replayed, no new message.
	r3, key3, replayed, err := c2.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m1"}, key1)
	if err != nil || !replayed || key3 != key1 {
		t.Fatalf("replay: %v replayed=%v key=%q", err, replayed, key3)
	}
	if r3.Message.ID != r1.Message.ID {
		t.Fatalf("replayed id %q != original %q", r3.Message.ID, r1.Message.ID)
	}
	if len(s.Posted) != 2 {
		t.Fatalf("server stored %d messages, want 2 (duplicate = 0)", len(s.Posted))
	}
	// COLAB_CLIENT_SEQ forces the seq (explicit retry from a fresh process).
	c4 := newClient(t, s, func(e map[string]string) { e["COLAB_STATE_DIR"] = state; e["COLAB_CLIENT_SEQ"] = "1" })
	_, key4, replayed, err := c4.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m1"}, "")
	if err != nil || key4 != key1 || !replayed {
		t.Fatalf("forced seq: key=%q replayed=%v err=%v", key4, replayed, err)
	}
	if len(s.Posted) != 2 {
		t.Fatalf("server stored %d messages after forced-seq retry, want 2", len(s.Posted))
	}
}

func TestPostParsesTriggersAndWarnings(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s, nil)
	content := "[@Reviewer](mention://agent/" + clienttest.ReviewerID + ") [@Lead](mention://agent/" + clienttest.DelegatorID + ") please check"
	res, _, _, err := c.PostMessage(context.Background(), clienttest.SessionID, client.MessageCreate{Content: content}, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Triggers) != 1 || res.Triggers[0].AgentID != clienttest.ReviewerID || res.Triggers[0].Coalesced {
		t.Fatalf("triggers = %+v", res.Triggers)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "delegator_mention_suppressed" || *res.Warnings[0].AgentID != clienttest.DelegatorID {
		t.Fatalf("warnings = %+v", res.Warnings)
	}
	if res.Message.AuthorType != "agent" || res.Message.SourceTaskID == nil || *res.Message.SourceTaskID != clienttest.TaskID {
		t.Fatalf("message = %+v", res.Message)
	}
	var body map[string]any
	json.NewDecoder(strings.NewReader(string(s.Posted[0].Response))).Decode(&body)
	if body["session_paused"] != nil {
		t.Fatalf("session_paused = %v", body["session_paused"])
	}
}

func TestListMessagesQuery(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s, nil)
	ctx := context.Background()
	for _, m := range []string{"a", "b", "c"} {
		if _, _, _, err := c.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: m}, "k-"+m); err != nil {
			t.Fatal(err)
		}
	}
	page, err := c.ListMessages(ctx, clienttest.SessionID, client.MessagesQuery{Limit: 2})
	if err != nil || len(page.Items) != 2 || !page.HasMoreAfter || page.Total == nil || *page.Total != 3 {
		t.Fatalf("page = %+v err=%v", page, err)
	}
	first := page.Items[0].ID
	page, err = c.ListMessages(ctx, clienttest.SessionID, client.MessagesQuery{Since: first})
	if err != nil || len(page.Items) != 2 || page.Items[0].Content != "b" {
		t.Fatalf("since: %+v err=%v", page, err)
	}
	q := s.Requests[len(s.Requests)-1].URL.Query()
	if q.Get("after") != first {
		t.Fatalf("--since must map to after=, got %v", q)
	}
	root := page.Items[0].ID
	if _, _, _, err := c.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "reply", ParentID: &root}, "k-r"); err != nil {
		t.Fatal(err)
	}
	page, err = c.ListMessages(ctx, clienttest.SessionID, client.MessagesQuery{Thread: root})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("thread: %+v err=%v", page, err)
	}
	q = s.Requests[len(s.Requests)-1].URL.Query()
	if q.Get("thread") != root || q.Get("include_replies") != "true" {
		t.Fatalf("thread query = %v", q)
	}
	if _, err := c.ListMessages(ctx, clienttest.SessionID, client.MessagesQuery{Limit: 500}); client.ExitCode(err) != client.ExitUsage {
		t.Fatalf("limit 500 should be exit 2, got %v", err)
	}
}
