package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	if cc := c.CachedContext(); cc == nil || cc.LastSeq != 0 || cc.Attempt != clienttest.Attempt {
		t.Fatalf("context = %+v", cc)
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

// Idempotency-Key (colab-cli.md §1 v0.2): UUIDv5(namespace=colab,
// "task:<task_id>:<seq>") — a real UUID (openapi format: uuid), stable, and
// independent of the attempt.
func TestIdempotencyKeyIsUUIDv5(t *testing.T) {
	if ns, _ := client.UUIDv5("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "colab"); ns != client.IdempotencyNamespace {
		t.Fatalf("namespace must be UUIDv5(DNS, \"colab\"): got %s want %s", ns, client.IdempotencyNamespace)
	}
	k := client.IdempotencyKey(clienttest.TaskID, 1)
	// Known vector (python: uuid5(uuid5(NAMESPACE_DNS,'colab'), 'task:<id>:1')).
	if k != "47beee27-4c46-5269-ac72-040d886f1259" {
		t.Fatalf("key(1) = %s", k)
	}
	if len(k) != 36 || k[14] != '5' || !strings.ContainsRune("89ab", rune(k[19])) {
		t.Fatalf("not an RFC 4122 v5 uuid: %s", k)
	}
	if client.IdempotencyKey(clienttest.TaskID, 1) != k || client.IdempotencyKey(clienttest.TaskID, 2) == k ||
		client.IdempotencyKey(clienttest.LaneID, 1) == k {
		t.Fatalf("key must be deterministic per (task, seq)")
	}
}

func contextCalls(s *clienttest.Server) int {
	n := 0
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/cli/context") {
			n++
		}
	}
	return n
}

// E8-04 across the attempt boundary: attempt 1 posts seq 1·2, is killed and
// re-queued; attempt 2 (fresh process, CliContext.last_seq = 2) continues at
// seq 3 — its keys never collide with attempt 1's — and a re-send of an
// attempt-1 seq from attempt 2 is replayed, storing nothing.
func TestIdempotencyAcrossAttempts(t *testing.T) {
	s := clienttest.New(t)
	s.Attempt = 1
	state := t.TempDir() // same host: the seq state survives the re-queue
	ctx := context.Background()
	env := func(attempt string) func(map[string]string) {
		return func(e map[string]string) { e["COLAB_STATE_DIR"] = state; e["COLAB_TASK_ATTEMPT"] = attempt }
	}

	// attempt 1, process 1 → seq 1 (last_seq 0 + 1, one /cli/context call)
	c1 := newClient(t, s, env("1"))
	r1, key1, replayed, err := c1.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m1"}, "")
	if err != nil || replayed || key1 != clienttest.Key(1) {
		t.Fatalf("attempt1/seq1: key=%q replayed=%v err=%v", key1, replayed, err)
	}
	// attempt 1, process 2 → seq 2 from the state file, no extra round trip
	c2 := newClient(t, s, env("1"))
	_, key2, _, err := c2.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m2"}, "")
	if err != nil || key2 != clienttest.Key(2) {
		t.Fatalf("attempt1/seq2: key=%q err=%v", key2, err)
	}
	if n := contextCalls(s); n != 1 {
		t.Fatalf("/cli/context called %d times within attempt 1, want 1", n)
	}
	if s.LastSeq != 2 {
		t.Fatalf("fake last_seq = %d, want 2", s.LastSeq)
	}
	if s.Posted[0].ClientSeq != 1 || s.Posted[1].ClientSeq != 2 {
		t.Fatalf("X-Colab-Client-Seq stored = %d,%d, want 1,2", s.Posted[0].ClientSeq, s.Posted[1].ClientSeq)
	}

	// kill → re-queue: attempt 2. The agent re-posts m1 (same content); the
	// key is a NEW seq (3), so it is a new message — dedupe of content is the
	// prompt's posted_message_ids job — but no attempt-1 key is ever reused.
	s.Attempt = 2
	c3 := newClient(t, s, env("2"))
	_, key3, replayed, err := c3.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m1"}, "")
	if err != nil || replayed || key3 != clienttest.Key(3) {
		t.Fatalf("attempt2 first post: key=%q replayed=%v err=%v (want seq 3 = last_seq+1)", key3, replayed, err)
	}
	if key3 == key1 || key3 == key2 {
		t.Fatalf("attempt 2 reused an attempt-1 key")
	}
	if n := contextCalls(s); n != 2 {
		t.Fatalf("/cli/context called %d times, want 2 (once per attempt boundary)", n)
	}
	// Network re-send of an attempt-1 seq from attempt 2 → same key → replayed, stored nothing.
	c4 := newClient(t, s, func(e map[string]string) { env("2")(e); e["COLAB_CLIENT_SEQ"] = "1" })
	r4, key4, replayed, err := c4.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m1"}, "")
	if err != nil || !replayed || key4 != key1 || r4.Message.ID != r1.Message.ID {
		t.Fatalf("re-send seq1 from attempt 2: key=%q replayed=%v id=%q err=%v", key4, replayed, r4.Message.ID, err)
	}
	// Explicit key retry (the CLI's --idempotency-key) replays too.
	_, _, replayed, err = c3.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m2"}, key2)
	if err != nil || !replayed {
		t.Fatalf("explicit key retry: replayed=%v err=%v", replayed, err)
	}
	if len(s.Posted) != 3 {
		t.Fatalf("server stored %d messages, want 3 (m1, m2, attempt-2 m1; re-sends = 0)", len(s.Posted))
	}
	// Another host (empty state dir) on attempt 2 continues after last_seq too.
	c5 := newClient(t, s, func(e map[string]string) { e["COLAB_STATE_DIR"] = t.TempDir(); e["COLAB_TASK_ATTEMPT"] = "2" })
	_, key5, _, err := c5.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m4"}, "")
	if err != nil || key5 != clienttest.Key(4) {
		t.Fatalf("fresh host: key=%q err=%v", key5, err)
	}
}

// postHeaders returns the headers of the n-th (0-based) POST .../messages the
// fake saw.
func postHeaders(t *testing.T, s *clienttest.Server, n int) http.Header {
	t.Helper()
	i := 0
	for _, r := range s.Requests {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			if i == n {
				return r.Header
			}
			i++
		}
	}
	t.Fatalf("POST /messages #%d not seen (%d requests)", n, len(s.Requests))
	return nil
}

// v0.3: every derived-key post carries X-Colab-Client-Seq equal to the seq
// the Idempotency-Key was derived from (openapi ClientSeq). A forced seq
// (COLAB_CLIENT_SEQ) is sent too; an explicit --idempotency-key has no seq,
// so the header is omitted and the server falls back to its UUIDv5 probe.
func TestPostMessageSendsClientSeqHeader(t *testing.T) {
	s := clienttest.New(t)
	s.LastSeq = 4
	ctx := context.Background()

	c := newClient(t, s, nil)
	for i, want := range []int{5, 6} {
		_, key, _, err := c.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m"}, "")
		if err != nil || key != clienttest.Key(want) {
			t.Fatalf("post %d: key=%q err=%v", i, key, err)
		}
		h := postHeaders(t, s, i)
		if got := h.Get(client.HeaderClientSeq); got != strconv.Itoa(want) {
			t.Fatalf("post %d: %s = %q, want %d (the seq behind key %s)", i, client.HeaderClientSeq, got, want, key)
		}
		if h.Get("Idempotency-Key") != key {
			t.Fatalf("post %d: Idempotency-Key header %q != returned key %q", i, h.Get("Idempotency-Key"), key)
		}
		if s.Posted[i].ClientSeq != want || s.LastSeq != want {
			t.Fatalf("post %d: fake stored client_seq=%d last_seq=%d, want %d", i, s.Posted[i].ClientSeq, s.LastSeq, want)
		}
	}

	// Forced seq → header carries the forced value.
	cf := newClient(t, s, func(e map[string]string) { e["COLAB_CLIENT_SEQ"] = "6" })
	if _, _, replayed, err := cf.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m"}, ""); err != nil || !replayed {
		t.Fatalf("forced seq re-send: replayed=%v err=%v", replayed, err)
	}
	if got := postHeaders(t, s, 2).Get(client.HeaderClientSeq); got != "6" {
		t.Fatalf("forced seq header = %q, want 6", got)
	}

	// Explicit key → no seq known → header absent.
	if _, _, _, err := c.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m"}, "explicit-key"); err != nil {
		t.Fatal(err)
	}
	if h := postHeaders(t, s, 3); h.Get(client.HeaderClientSeq) != "" {
		t.Fatalf("explicit key must not send %s (got %q)", client.HeaderClientSeq, h.Get(client.HeaderClientSeq))
	}
}

// The server keeps last_seq = max(client_seq), not a count: a hole in the seq
// (post 5 failed on the network, 6 landed) must not make the next attempt
// reuse 5 or 6.
func TestLastSeqIsMaxNotCount(t *testing.T) {
	s := clienttest.New(t)
	ctx := context.Background()
	c := newClient(t, s, func(e map[string]string) { e["COLAB_CLIENT_SEQ"] = "6" })
	if _, key, _, err := c.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m6"}, ""); err != nil || key != clienttest.Key(6) {
		t.Fatalf("key=%q err=%v", key, err)
	}
	if s.LastSeq != 6 || len(s.Posted) != 1 {
		t.Fatalf("fake last_seq=%d posted=%d, want 6/1 (max, not count)", s.LastSeq, len(s.Posted))
	}
	// A header-less post with an out-of-window key leaves last_seq alone.
	if _, _, _, err := c.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "web"}, "not-a-seq-key"); err != nil {
		t.Fatal(err)
	}
	if s.LastSeq != 6 {
		t.Fatalf("header-less foreign key moved last_seq to %d", s.LastSeq)
	}
	// Fresh process on the next attempt continues at 7.
	c2 := newClient(t, s, func(e map[string]string) { e["COLAB_TASK_ATTEMPT"] = "3" })
	_, key, _, err := c2.PostMessage(ctx, clienttest.SessionID, client.MessageCreate{Content: "m7"}, "")
	if err != nil || key != clienttest.Key(7) {
		t.Fatalf("next attempt key=%q err=%v (want seq 7 = max+1)", key, err)
	}
	if postHeaders(t, s, 2).Get(client.HeaderClientSeq) != "7" {
		t.Fatalf("next attempt header = %q", postHeaders(t, s, 2).Get(client.HeaderClientSeq))
	}
}

// Attempt unknown in the env → /cli/context supplies attempt and last_seq.
func TestSeqFromContextWhenAttemptUnset(t *testing.T) {
	s := clienttest.New(t)
	s.LastSeq = 7
	c := newClient(t, s, func(e map[string]string) { delete(e, "COLAB_TASK_ATTEMPT") })
	_, key, _, err := c.PostMessage(context.Background(), clienttest.SessionID, client.MessageCreate{Content: "m"}, "")
	if err != nil || key != clienttest.Key(8) {
		t.Fatalf("key=%q err=%v", key, err)
	}
	if n := contextCalls(s); n != 1 {
		t.Fatalf("/cli/context called %d times, want 1", n)
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
	if len(res.Warnings) != 1 || res.Warnings[0].Code != client.WarningSuppressedDelegator || *res.Warnings[0].AgentID != clienttest.DelegatorID {
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
