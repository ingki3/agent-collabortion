package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/testchat"
)

// ---------------------------------------------------------------------------
// FR-1.8.1 test chat — daemon-protocol v0.8 §4.5 (T-S12)
// ---------------------------------------------------------------------------
//
// The chat is not a session: nothing below touches task · lane · task_event ·
// task_usage. Each user turn is one token-less attempt on the shared daemon
// endpoints, and the assertions are the contract's table in §4.5, row by row.

// testChatFixture is the P2 fixture with a runtime that advertises claude_code
// (createTestChat picks "그 runtime_kind 가 온라인인 런타임" by capabilities).
type testChatFixture struct {
	*p2Fixture
	rtID uuid.UUID
	d    *client
}

func newTestChatFixture(t *testing.T) *testChatFixture {
	t.Helper()
	f := newP2Fixture(t)
	var rtID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 ORDER BY created_at LIMIT 1`, mustUUID(t, f.wsID)).Scan(&rtID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime SET capabilities = '[{"kind":"claude_code","version":"1.0.0"}]' WHERE id = $1`, rtID); err != nil {
		t.Fatal(err)
	}
	return &testChatFixture{p2Fixture: f, rtID: rtID, d: f.daemonFor(t, rtID)}
}

func (f *testChatFixture) create(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	return f.api.must(201, "POST", f.p+"/agents/"+f.lead+"/test-chats", body)
}

func (f *testChatFixture) turn(t *testing.T, chatID, content string) (int, map[string]any) {
	t.Helper()
	f.fake.Advance(time.Second)
	st, out, _ := f.api.do("POST", f.p+"/test-chats/"+chatID+"/turns", map[string]any{"content": content}, "Idempotency-Key", uuid.NewString())
	return st, out
}

func (f *testChatFixture) get(t *testing.T, chatID string) map[string]any {
	t.Helper()
	return f.api.must(200, "GET", f.p+"/test-chats/"+chatID, nil)
}

// claimChat claims through the daemon endpoint and returns the chat's bundle
// plus the whole response (commands ride on it).
func (f *testChatFixture) claim(t *testing.T) (map[string]any, []contracts.TaskBundle, []contracts.Command) {
	t.Helper()
	st, raw, _ := f.d.raw("POST", "/v1/daemon/runtimes/"+f.rtID.String()+"/claim", map[string]any{"capacity": 5, "wait_ms": 0})
	if st != 200 {
		t.Fatalf("claim = %d: %s", st, raw)
	}
	var out struct {
		Tasks    []contracts.TaskBundle `json:"tasks"`
		Commands []contracts.Command    `json:"commands"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	// The fixture's own session (isolation none, runtime unfixed) is claimable
	// by this runtime too; only the chat turns are what these tests read.
	var chats []contracts.TaskBundle
	for _, b := range out.Tasks {
		if b.Task.Kind == "test_chat" {
			chats = append(chats, b)
		}
	}
	return m, chats, out.Commands
}

func (f *testChatFixture) daemon(t *testing.T, want int, path string, body any) map[string]any {
	t.Helper()
	st, out, _ := f.d.do("POST", "/v1/daemon/tasks/"+path, body)
	if st != want {
		t.Fatalf("POST %s = %d, want %d: %v", path, st, want, out)
	}
	return out
}

func turnsOf(out map[string]any) []map[string]any {
	var turns []map[string]any
	for _, raw := range out["turns"].([]any) {
		turns = append(turns, raw.(map[string]any))
	}
	return turns
}

// TestP5TestChatLifecycle walks §4.5 end to end: open → turn → claim (bundle
// shape) → phase → events → heartbeat → finish → the answer, the tokens and
// the transport on getTestChat; a second turn rides `resume`; a failed turn
// carries a sentence; close queues cancel-less gc; the §6 receipt consumes it.
func TestP5TestChatLifecycle(t *testing.T) {
	f := newTestChatFixture(t)
	ctx := t.Context()

	chat := f.create(t, map[string]any{})
	chatID := str(chat, "id")
	if str(chat, "status") != "open" || str(chat, "runtime_id") != f.rtID.String() {
		t.Fatalf("created chat = %v, want open on runtime %s", chat, f.rtID)
	}
	if n := len(chat["turns"].([]any)); n != 0 {
		t.Fatalf("new chat has %d turns", n)
	}
	// Not a session, not a lane, not a task (FR-1.8.1 "세션이 아니다").
	var sessions, tokens int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM room WHERE workspace_id = $1`, mustUUID(t, f.wsID)).Scan(&sessions)
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM task_token`).Scan(&tokens)
	if sessions != 1 || tokens != 0 { // the fixture's own session only
		t.Fatalf("sessions=%d task_tokens=%d after createTestChat — a chat must create neither", sessions, tokens)
	}

	// --- turn 1 ---
	st, turn := f.turn(t, chatID, "안녕, 너는 누구니?")
	if st != 202 || str(turn, "role") != "user" || str(turn, "content") != "안녕, 너는 누구니?" {
		t.Fatalf("postTestChatTurn = %d %v, want 202 user turn", st, turn)
	}
	// 409 while the previous turn is in flight (openapi postTestChatTurn).
	if st, out := f.turn(t, chatID, "또 하나"); st != 409 || str(out, "code") != "turn_in_progress" {
		t.Fatalf("second turn while in flight = %d %v, want 409 turn_in_progress", st, out)
	}

	_, bundles, _ := f.claim(t)
	if len(bundles) != 1 {
		t.Fatalf("claim returned %d bundles, want the test chat turn", len(bundles))
	}
	b := bundles[0]
	// §4.5 bundle table.
	if b.Task.Kind != "test_chat" || b.Task.ID != chatID || b.Task.TestChatID != chatID || b.Task.Attempt != 1 {
		t.Errorf("task = %+v, want kind test_chat · id=test_chat.id · attempt 1", b.Task)
	}
	if b.Task.LaneID != "" || b.Task.SessionID != "" || b.Task.TriggerMessageID != "" {
		t.Errorf("lane/session/trigger must be empty: %+v", b.Task)
	}
	if b.TaskToken != "" {
		t.Errorf("task_token = %q, want empty (E15-03: no COLAB token for a test chat)", b.TaskToken)
	}
	if strings.Contains(b.Brief.Text, "[2]") || strings.Contains(b.Brief.Text, "colab message post") {
		t.Errorf("brief carries the colab command section — the agent has no token to use it:\n%s", b.Brief.Text)
	}
	if !strings.Contains(b.Brief.Text, "[1] Agent Identity") || !strings.Contains(b.Brief.Text, "be helpful") && !strings.Contains(b.Brief.Text, "Instructions:\ni") {
		t.Errorf("brief lacks the agent's identity/instructions:\n%s", b.Brief.Text)
	}
	if b.Workdir.Kind != "dir" || !b.Workdir.Reuse || b.Workdir.Path != "/Users/x/.colab/.colab/testchat/"+chatID {
		t.Errorf("workdir = %+v, want {dir, <workdir_root>/.colab/testchat/<id>, reuse:true}", b.Workdir)
	}
	if !strings.HasPrefix(b.Prompt, testchat.FirstTurnPreamble) || !strings.HasSuffix(b.Prompt, "안녕, 너는 누구니?") {
		t.Errorf("first-turn prompt = %q, want the §4.5 preamble line then the user turn", b.Prompt)
	}
	if b.Resume != nil {
		t.Errorf("first turn has resume %+v, want null", b.Resume)
	}
	if b.Limits.StallSeconds != 180 {
		t.Errorf("limits.stall_seconds = %d, want 180", b.Limits.StallSeconds)
	}
	if b.Profile.RuntimeKind != contracts.RuntimeClaudeCode || b.Profile.Model != "claude-sonnet-5" {
		t.Errorf("profile = %+v", b.Profile)
	}

	// The daemon's §4.2 reports on the shared URLs, with the chat id in the task slot.
	att := chatID + "/attempts/1/"
	f.daemon(t, 200, att+"phase", map[string]any{"phase": "preparing", "pgid": 1, "workdir_path": b.Workdir.Path})
	f.daemon(t, 200, att+"phase", map[string]any{"phase": "running", "pgid": 1, "workdir_path": b.Workdir.Path})

	// heartbeat preview → SSE test_chat.delta (ephemeral), nothing stored.
	sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
	defer sub.Close()
	f.daemon(t, 200, att+"heartbeat", map[string]any{"usage": map[string]any{"input_tokens": 100, "output_tokens": 10, "estimated": true, "model": "claude-sonnet-5"},
		"last_seq": 0, "preview": map[string]any{"text": "나는"}})
	select {
	case e := <-sub.C:
		if e.Type != "test_chat.delta" || !e.Ephemeral || !strings.Contains(string(e.Payload), chatID) || !strings.Contains(string(e.Payload), "나는") {
			t.Fatalf("heartbeat preview produced %s ephemeral=%v %s, want ephemeral test_chat.delta with the text", e.Type, e.Ephemeral, e.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat preview produced no SSE test_chat.delta")
	}

	ev := func(seq int, class, verb string, partial bool, payload map[string]any) map[string]any {
		return map[string]any{"task_id": chatID, "attempt": 1, "seq": seq, "ts": f.fake.Now(), "class": class, "verb": verb, "outcome": "ok", "partial": partial, "payload": payload}
	}
	out := f.daemon(t, 200, att+"events", map[string]any{"events": []map[string]any{
		ev(1, "runtime", "start", false, map[string]any{"runtime_kind": "claude_code"}),
		ev(2, "message", "say", true, map[string]any{"kind": "text", "text": "나는 "}),
		ev(3, "message", "say", false, map[string]any{"kind": "thought", "text": "(생각 중)"}),
		ev(4, "message", "say", false, map[string]any{"kind": "text", "text": "나는 Lead 에이전트다."}),
	}})
	if out["accepted_seq_max"].(float64) != 4 {
		t.Errorf("accepted_seq_max = %v, want 4", out["accepted_seq_max"])
	}
	// Re-sent batch: idempotent (same seq ignored), nothing doubled.
	f.daemon(t, 200, att+"events", map[string]any{"events": []map[string]any{
		ev(4, "message", "say", false, map[string]any{"kind": "text", "text": "나는 Lead 에이전트다."}),
	}})
	var stored int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM task_event`).Scan(&stored)
	if stored != 0 {
		t.Errorf("task_event rows = %d after a test chat batch — §4.5 stores none", stored)
	}

	// Drain the delta so the finish frame is the next one read.
	for len(sub.C) > 0 {
		<-sub.C
	}
	f.daemon(t, 200, att+"finish", map[string]any{
		"outcome": "completed", "stop_reason": "end_turn", "transport": "acp", "last_seq": 4,
		"usage":               map[string]any{"input_tokens": 1200, "output_tokens": 300, "estimated": true, "model": "claude-sonnet-5"},
		"runtime_session_ref": map[string]any{"runtime_kind": "claude_code", "session_id": "acp-tc-1", "cwd": b.Workdir.Path, "created_at": f.fake.Now()},
	})
	select {
	case e := <-sub.C:
		if e.Type != "test_chat.turn" || e.Ephemeral {
			t.Fatalf("finish produced %s (ephemeral=%v), want persisted test_chat.turn", e.Type, e.Ephemeral)
		}
		var p struct {
			TestChatID string `json:"test_chat_id"`
			Transport  string `json:"transport"`
			Turn       struct {
				Role, Content string
			} `json:"turn"`
			InputTokens int64 `json:"input_tokens"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		if p.TestChatID != chatID || p.Transport != "acp" || p.Turn.Role != "agent" || p.Turn.Content != "나는 Lead 에이전트다." || p.InputTokens != 1200 {
			t.Fatalf("test_chat.turn payload = %s", e.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finish produced no SSE test_chat.turn")
	}

	got := f.get(t, chatID)
	turns := turnsOf(got)
	if len(turns) != 2 || str(turns[0], "role") != "user" || str(turns[1], "role") != "agent" {
		t.Fatalf("turns = %v, want [user, agent]", turns)
	}
	if str(turns[1], "content") != "나는 Lead 에이전트다." {
		t.Errorf("agent turn = %q — only the non-partial text message.say is the answer (thought and partials are not)", str(turns[1], "content"))
	}
	if turns[1]["error"] != nil {
		t.Errorf("completed turn has error %v", turns[1]["error"])
	}
	u := turns[1]["usage"].(map[string]any)
	if u["input_tokens"].(float64) != 1200 || u["output_tokens"].(float64) != 300 {
		t.Errorf("turn usage = %v", u)
	}
	if str(got, "transport") != "acp" {
		t.Errorf("transport = %q, want acp (finish.transport → test_chat.transport, §4.5)", str(got, "transport"))
	}
	if got["input_tokens"].(float64) != 1200 || got["output_tokens"].(float64) != 300 {
		t.Errorf("chat tokens = %v/%v", got["input_tokens"], got["output_tokens"])
	}
	// estimated usage is priced from the workspace table (defaults: sonnet-5
	// $2/$10 per MTok → 1200·2 + 300·10 = 5400 µ$ = $0.0054).
	if c := got["cost_usd"].(float64); c < 0.0053 || c > 0.0055 {
		t.Errorf("cost_usd = %v, want ≈0.0054 (estimated from the price table like a session turn)", c)
	}
	if got["estimated"] != true {
		t.Errorf("estimated = %v, want true", got["estimated"])
	}
	if str(got, "status") != "open" {
		t.Errorf("chat status = %q after a turn, want open", str(got, "status"))
	}

	// --- turn 2 rides resume ---
	if st, _ := f.turn(t, chatID, "한 문장으로 다시"); st != 202 {
		t.Fatalf("turn 2 = %d", st)
	}
	_, bundles, _ = f.claim(t)
	if len(bundles) != 1 || bundles[0].Task.Attempt != 2 {
		t.Fatalf("turn 2 claim = %+v, want attempt 2", bundles)
	}
	b2 := bundles[0]
	if b2.Resume == nil || b2.Resume.SessionID != "acp-tc-1" {
		t.Errorf("turn 2 resume = %+v, want the ref finish stored (acp-tc-1) — §4.5 '턴마다 resume 을 이어'", b2.Resume)
	}
	if strings.Contains(b2.Prompt, testchat.FirstTurnPreamble) || b2.Prompt != "한 문장으로 다시" {
		t.Errorf("turn 2 prompt = %q, want the bare user turn", b2.Prompt)
	}
	// A failed turn: error in the user's words, chat stays open.
	att2 := chatID + "/attempts/2/"
	f.daemon(t, 200, att2+"phase", map[string]any{"phase": "running"})
	f.daemon(t, 200, att2+"finish", map[string]any{"outcome": "failed", "failure_kind": "auth", "transport": "acp"})
	got = f.get(t, chatID)
	turns = turnsOf(got)
	if len(turns) != 4 {
		t.Fatalf("turns = %d, want 4", len(turns))
	}
	errText := str(turns[3], "error")
	if errText != testchat.FailureText(contracts.FailAuth) || !strings.Contains(errText, "로그인") {
		t.Errorf("failed turn error = %q, want the §8.4 sentence for auth", errText)
	}
	if str(got, "status") != "open" {
		t.Errorf("a failed turn closed the chat (status %q) — §4.5: 턴은 남고 채팅은 열려 있다", str(got, "status"))
	}
	// A repeated finish for the same attempt is idempotent (200, nothing doubled).
	f.daemon(t, 200, att2+"finish", map[string]any{"outcome": "completed"})
	if n := len(turnsOf(f.get(t, chatID))); n != 4 {
		t.Errorf("repeated finish appended a turn (%d)", n)
	}
	// A stale attempt is 409 stale_attempt, as for tasks.
	if st, out, _ := f.d.do("POST", "/v1/daemon/tasks/"+chatID+"/attempts/1/heartbeat", map[string]any{}); st != 409 || str(out, "code") != "stale_attempt" {
		t.Errorf("heartbeat on attempt 1 = %d %v, want 409 stale_attempt", st, out)
	}

	// --- cost: workspace only ---
	costOut := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/cost", nil)
	if v, _ := costOut["test_chat_usd"].(float64); v < 0.0053 {
		t.Errorf("workspace test_chat_usd = %v, want the chat's cost", costOut["test_chat_usd"])
	}
	if bs, _ := costOut["by_session"].([]any); len(bs) != 0 {
		t.Errorf("by_session = %v — a test chat is not a session", bs)
	}
	sessCost := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID+"/cost", nil)
	if sessCost["total_usd"].(float64) != 0 {
		t.Errorf("session cost = %v, want 0 (test chat cost never lands on a session)", sessCost["total_usd"])
	}

	// --- close: gc only (no turn in flight), then 410 ---
	closed := f.api.must(200, "POST", f.p+"/test-chats/"+chatID+"/close", nil)
	if str(closed, "status") != "closed" || closed["closed_at"] == nil {
		t.Fatalf("close = %v", closed)
	}
	_, _, cmds := f.claim(t)
	var gc *contracts.Command
	for i := range cmds {
		if cmds[i].Type == contracts.CmdGC {
			gc = &cmds[i]
		}
		if cmds[i].Type == contracts.CmdCancel {
			t.Errorf("close with no turn in flight queued a cancel: %+v", cmds[i])
		}
	}
	if gc == nil {
		t.Fatalf("no gc command after close: %+v", cmds)
	}
	if gc.TestChatID != chatID || gc.SessionID != "" || len(gc.Workdirs) != 1 || gc.Workdirs[0].ID != chatID || gc.Workdirs[0].Path != b.Workdir.Path {
		t.Errorf("gc = %+v, want {test_chat_id, workdirs:[{id:<chat>, path}]} and no session_id (§4.5)", gc)
	}
	if st, out := f.turn(t, chatID, "닫힌 뒤"); st != 410 || str(out, "code") != "test_chat_closed" {
		t.Errorf("turn after close = %d %v, want 410 test_chat_closed", st, out)
	}
	// Idempotent close.
	f.api.must(200, "POST", f.p+"/test-chats/"+chatID+"/close", nil)

	// --- §6 receipt with test_chat_id: consumes gc, stores no workdir row ---
	// A report WITHOUT the receipt must not consume it (the daemon has not
	// deleted anything yet — this is the trap ConsumeGCCommands would fall in).
	f.d.must(200, "POST", "/v1/daemon/runtimes/"+f.rtID.String()+"/workdirs", map[string]any{"workdirs": []map[string]any{}})
	if _, _, cmds := f.claim(t); len(cmds) != 1 {
		t.Fatalf("gc consumed by an unrelated workdir report (commands now %+v) — §4.3: consumed only on the deletion receipt", cmds)
	}
	f.d.must(200, "POST", "/v1/daemon/runtimes/"+f.rtID.String()+"/workdirs", map[string]any{"workdirs": []map[string]any{{
		"id": chatID, "kind": "dir", "path": b.Workdir.Path, "test_chat_id": chatID, "bytes": 0, "gc": map[string]any{"status": "deleted"},
	}}})
	if _, _, cmds := f.claim(t); len(cmds) != 0 {
		t.Errorf("gc still pending after the deleted receipt: %+v", cmds)
	}
	var wd int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir`).Scan(&wd)
	if wd != 0 {
		t.Errorf("workdir rows = %d — a test_chat_id row is never stored (§4.5)", wd)
	}
}

// TestP5TestChatCloseCancelsTurnInFlight is §4.5 "닫기·취소": a turn out with
// the daemon gets `cancel {task_id: <chat>, attempt, reason: director}` plus
// the gc; the daemon's cancelled finish closes the turn with a sentence and
// consumes the cancel.
func TestP5TestChatCloseCancelsTurnInFlight(t *testing.T) {
	f := newTestChatFixture(t)
	chat := f.create(t, map[string]any{})
	chatID := str(chat, "id")
	f.turn(t, chatID, "긴 답을 써 줘")
	_, bundles, _ := f.claim(t)
	if len(bundles) != 1 {
		t.Fatal("no bundle")
	}
	f.daemon(t, 200, chatID+"/attempts/1/phase", map[string]any{"phase": "running"})

	f.api.must(200, "POST", f.p+"/test-chats/"+chatID+"/close", nil)
	_, _, cmds := f.claim(t)
	var cancel, gc int
	for _, c := range cmds {
		switch c.Type {
		case contracts.CmdCancel:
			cancel++
			if c.TaskID != chatID || c.Attempt != 1 || c.Reason != "director" {
				t.Errorf("cancel = %+v, want {task_id:<chat>, attempt:1, reason:director}", c)
			}
		case contracts.CmdGC:
			gc++
		}
	}
	if cancel != 1 || gc != 1 {
		t.Fatalf("commands after close = %+v, want one cancel and one gc", cmds)
	}
	// The daemon carries the cancel out.
	f.daemon(t, 200, chatID+"/attempts/1/finish", map[string]any{"outcome": "cancelled", "stop_reason": "cancelled"})
	got := f.get(t, chatID)
	turns := turnsOf(got)
	if len(turns) != 2 || str(turns[1], "error") != testchat.FailureText(contracts.FailCancelled) {
		t.Errorf("turns after cancelled finish = %v", turns)
	}
	_, _, cmds = f.claim(t)
	for _, c := range cmds {
		if c.Type == contracts.CmdCancel {
			t.Errorf("cancel still pending after its finish arrived: %+v", c)
		}
	}
	// A chat closed while a turn was still QUEUED (never claimed) closes that
	// turn itself — there is no attempt to cancel. The gc still goes out
	// (§4.5 "언제나"), naming the path the bundle would have carried.
	chat2 := f.create(t, map[string]any{})
	f.turn(t, str(chat2, "id"), "아직 안 나간 턴")
	closed := f.api.must(200, "POST", f.p+"/test-chats/"+str(chat2, "id")+"/close", nil)
	turns = turnsOf(closed)
	if len(turns) != 2 || turns[1]["error"] == nil {
		t.Errorf("queued turn on close = %v, want an agent turn carrying an error", turns)
	}
	_, bundles, cmds = f.claim(t)
	if len(bundles) != 0 {
		t.Errorf("a closed chat's queued turn was claimed: %+v", bundles)
	}
	var gcs int
	for _, c := range cmds {
		if c.Type == contracts.CmdCancel && c.TaskID == str(chat2, "id") {
			t.Errorf("cancel for a turn that never reached a machine: %+v", c)
		}
		if c.Type == contracts.CmdGC && c.TestChatID == str(chat2, "id") {
			gcs++
			if want := "/Users/x/.colab/.colab/testchat/" + str(chat2, "id"); len(c.Workdirs) != 1 || c.Workdirs[0].Path != want {
				t.Errorf("gc path = %+v, want %s", c.Workdirs, want)
			}
		}
	}
	if gcs != 1 {
		t.Errorf("gc commands for the never-dispatched chat = %d, want 1 (§4.5 언제나 gc)", gcs)
	}
}

// TestP5TestChatExpiryClosesTurnWithoutRequeue is §4.5's last row: the
// preparing (5 min from dispatch) and running (3 min heartbeat) bounds close
// the turn as an error — the turn number does not move and nothing is
// re-queued.
func TestP5TestChatExpiryClosesTurnWithoutRequeue(t *testing.T) {
	f := newTestChatFixture(t)
	ctx := t.Context()
	chat := f.create(t, map[string]any{})
	chatID := str(chat, "id")

	// (a) dispatched, never prepared.
	f.turn(t, chatID, "1")
	f.claim(t)
	f.fake.Advance(contracts.DispatchedTimeout - time.Second)
	if n, _ := f.srv.ExpireTestChatTurns(ctx); n != 0 {
		t.Fatalf("expired %d turns before the 5-minute bound", n)
	}
	f.fake.Advance(2 * time.Second)
	if n, err := f.srv.ExpireTestChatTurns(ctx); err != nil || n != 1 {
		t.Fatalf("expire after 5 min = %d, %v; want 1", n, err)
	}
	var status string
	var turnNo int
	_ = f.pool.QueryRow(ctx, `SELECT turn_status, turn_no FROM test_chat WHERE id = $1`, mustUUID(t, chatID)).Scan(&status, &turnNo)
	if status != "idle" || turnNo != 1 {
		t.Errorf("after expiry turn_status=%s turn_no=%d, want idle · 1 (no requeue)", status, turnNo)
	}
	turns := turnsOf(f.get(t, chatID))
	if len(turns) != 2 || !strings.Contains(str(turns[1], "error"), "5분") {
		t.Errorf("expired turn = %v, want an agent turn with the 5-minute sentence", turns)
	}
	// The next user turn is accepted (idle again) and gets attempt 2.
	if st, _ := f.turn(t, chatID, "2"); st != 202 {
		t.Fatalf("turn after expiry = %d", st)
	}
	_, bundles, _ := f.claim(t)
	if len(bundles) != 1 || bundles[0].Task.Attempt != 2 {
		t.Fatalf("claim after expiry = %+v, want attempt 2", bundles)
	}
	// (b) running, heartbeat stops.
	f.daemon(t, 200, chatID+"/attempts/2/phase", map[string]any{"phase": "running"})
	f.daemon(t, 200, chatID+"/attempts/2/heartbeat", map[string]any{"usage": map[string]any{"input_tokens": 50, "output_tokens": 5, "estimated": true}})
	f.fake.Advance(contracts.HeartbeatExpiry + time.Second)
	if n, _ := f.srv.ExpireTestChatTurns(ctx); n != 1 {
		t.Fatalf("running expiry = %d, want 1", n)
	}
	got := f.get(t, chatID)
	turns = turnsOf(got)
	if len(turns) != 4 || !strings.Contains(str(turns[3], "error"), "3분") {
		t.Errorf("running-expired turn = %v", turns)
	}
	// The heartbeat's usage was the turn's cost (S-19: an ended turn's usage is not lost).
	if got["input_tokens"].(float64) != 50 {
		t.Errorf("chat input_tokens = %v, want the heartbeat's 50", got["input_tokens"])
	}
	// A late finish from the daemon is idempotent, not an error.
	f.daemon(t, 200, chatID+"/attempts/2/finish", map[string]any{"outcome": "completed"})
}

// TestP5TestChatRoutingOnlyAfterTaskMiss is the §0-9 injection point: the
// daemon endpoints reach the chat only because taskForDaemon looks the id up
// as a test chat AFTER the task lookup failed. Remove that branch and finish
// falls to 404 — this test fails on that line. The two other assertions pin
// the "only after" half: an unknown id is still 404, and a real task is
// untouched.
func TestP5TestChatRoutingOnlyAfterTaskMiss(t *testing.T) {
	f := newTestChatFixture(t)
	chat := f.create(t, map[string]any{})
	chatID := str(chat, "id")
	f.turn(t, chatID, "x")
	f.claim(t)
	f.daemon(t, 200, chatID+"/attempts/1/phase", map[string]any{"phase": "running"})
	st, out, _ := f.d.do("POST", "/v1/daemon/tasks/"+chatID+"/attempts/1/finish", map[string]any{"outcome": "completed", "transport": "cli"})
	if st == 404 {
		t.Fatalf("finish on a test chat id = 404 — the §4.5 routing (task miss → test_chat) is not wired: %v", out)
	}
	if st != 200 {
		t.Fatalf("finish = %d %v", st, out)
	}
	if got := f.get(t, chatID); str(got, "transport") != "cli" {
		t.Errorf("transport = %q, want cli", str(got, "transport"))
	}
	// Neither a task nor a chat: 404, as before v0.8.
	if st, out, _ := f.d.do("POST", "/v1/daemon/tasks/"+uuid.NewString()+"/attempts/1/finish", map[string]any{"outcome": "completed"}); st != 404 {
		t.Errorf("finish on an unknown id = %d %v, want 404", st, out)
	}
	// A chat fixed to ANOTHER runtime is 403 for this daemon.
	var otherRT uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO runtime (workspace_id, name, status, workdir_root, created_at, updated_at) VALUES ($1, 'mac-2', 'online', '/Users/y/.colab', $2, $2) RETURNING id`, mustUUID(t, f.wsID), f.fake.Now()).Scan(&otherRT); err != nil {
		t.Fatal(err)
	}
	d2 := f.daemonFor(t, otherRT)
	if st, out, _ := d2.do("POST", "/v1/daemon/tasks/"+chatID+"/attempts/1/finish", map[string]any{"outcome": "completed"}); st != 403 || str(out, "code") != "runtime_mismatch" {
		t.Errorf("finish from another runtime = %d %v, want 403 runtime_mismatch", st, out)
	}
}

// TestP5TestChatCreateRules covers createTestChat's choices: default profile,
// explicit runtime (online / offline → 409), no runtime of the kind → 409, and
// who may read a chat.
func TestP5TestChatCreateRules(t *testing.T) {
	f := newTestChatFixture(t)
	ctx := t.Context()

	chat := f.create(t, map[string]any{"runtime_id": f.rtID})
	if str(chat, "runtime_id") != f.rtID.String() {
		t.Errorf("explicit runtime not honoured: %v", chat)
	}
	var profileID uuid.UUID
	_ = f.pool.QueryRow(ctx, `SELECT id FROM agent_profile WHERE agent_id = $1 AND is_default`, f.leadUUID).Scan(&profileID)
	if str(chat, "profile_id") != profileID.String() {
		t.Errorf("profile_id = %q, want the default profile %s", str(chat, "profile_id"), profileID)
	}

	// Another member of the workspace: 403 on the chat, not the opener.
	other := &client{t: t, srv: f.api.srv}
	_, _, hdr := other.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "M", "email": "m@example.com", "password": "password123"})
	other.cookie = hdr.Get("Set-Cookie")
	var otherID uuid.UUID
	_ = f.pool.QueryRow(ctx, `SELECT id FROM app_user WHERE email = 'm@example.com'`).Scan(&otherID)
	if _, err := f.pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'member', $3)`, mustUUID(t, f.wsID), otherID, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if st, out, _ := other.do("GET", f.p+"/test-chats/"+str(chat, "id"), nil); st != 403 {
		t.Errorf("another member reading the chat = %d %v, want 403", st, out)
	}
	if st, _, _ := other.do("POST", f.p+"/test-chats/"+str(chat, "id")+"/close", nil); st != 403 {
		t.Errorf("another member closing the chat = %d, want 403", st)
	}
	// A member may open their own chat with the same agent.
	if st, _, _ := other.do("POST", f.p+"/agents/"+f.lead+"/test-chats", map[string]any{}); st != 201 {
		t.Errorf("member createTestChat = %d, want 201", st)
	}

	// Offline runtime named explicitly → 409 in the user's words.
	if _, err := f.pool.Exec(ctx, `UPDATE runtime SET status = 'offline' WHERE id = $1`, f.rtID); err != nil {
		t.Fatal(err)
	}
	st, out, _ := f.api.do("POST", f.p+"/agents/"+f.lead+"/test-chats", map[string]any{"runtime_id": f.rtID})
	if st != 409 || str(out, "code") != "runtime_offline" || !strings.Contains(str(out, "detail"), "컴퓨터") {
		t.Errorf("offline runtime = %d %v, want 409 runtime_offline with a 컴퓨터 sentence", st, out)
	}
	// No online runtime of the kind → 409.
	st, out, _ = f.api.do("POST", f.p+"/agents/"+f.lead+"/test-chats", map[string]any{})
	if st != 409 || str(out, "code") != "no_online_runtime" {
		t.Errorf("no runtime = %d %v, want 409 no_online_runtime", st, out)
	}
	// Idempotency-Key replays.
	if _, err := f.pool.Exec(ctx, `UPDATE runtime SET status = 'online' WHERE id = $1`, f.rtID); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	first := f.api.must(201, "POST", f.p+"/agents/"+f.lead+"/test-chats", map[string]any{}, "Idempotency-Key", key)
	second := f.api.must(201, "POST", f.p+"/agents/"+f.lead+"/test-chats", map[string]any{}, "Idempotency-Key", key)
	if str(first, "id") != str(second, "id") {
		t.Errorf("idempotent replay opened a second chat")
	}
}

// TestP5TestChatTakesLeftoverCapacityOnly pins the claim rule: a session task
// and a test chat turn both wait; with capacity 1 the task goes first, the
// turn on the next claim. The session-task path is byte-for-byte what it was.
func TestP5TestChatTakesLeftoverCapacityOnly(t *testing.T) {
	f := newTestChatFixture(t)
	// Pin the fixture's session to this runtime so the task is claimable here.
	if _, err := f.pool.Exec(t.Context(), `UPDATE room SET runtime_id = $2 WHERE id = $1`, mustUUID(t, f.sessionID), f.rtID); err != nil {
		t.Fatal(err)
	}
	chat := f.create(t, map[string]any{})
	f.turn(t, str(chat, "id"), "먼저 온 시험 턴")
	f.post(t, map[string]any{"content": "[@Lead](mention://agent/" + f.lead + ") 진짜 일"})

	claimN := func(capacity int) []contracts.TaskBundle {
		bundles, err := f.srv.Queue.Claim(t.Context(), f.rtID.String(), capacity, f.fake.Now())
		if err != nil {
			t.Fatal(err)
		}
		return bundles
	}
	first := claimN(1)
	if len(first) != 1 || first[0].Task.Kind == "test_chat" {
		t.Fatalf("capacity 1 claim = %+v, want the SESSION task first", first)
	}
	if first[0].TaskToken == "" {
		t.Errorf("session task lost its token")
	}
	// Every further capacity-1 claim hands out a session task until none is
	// left; only then the chat turn — never ahead of one.
	var sawChat bool
	for i := 0; i < 5 && !sawChat; i++ {
		got := claimN(1)
		if len(got) != 1 {
			t.Fatalf("claim %d = %+v, want exactly one bundle", i+2, got)
		}
		sawChat = got[0].Task.Kind == "test_chat"
		if !sawChat {
			var queued int
			_ = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM task WHERE status = 'queued'`).Scan(&queued)
			_ = queued
		}
	}
	if !sawChat {
		t.Fatal("the test chat turn never came out behind the session tasks")
	}
	if third := claimN(5); len(third) != 0 {
		t.Errorf("a further claim handed out %+v", third)
	}
}
