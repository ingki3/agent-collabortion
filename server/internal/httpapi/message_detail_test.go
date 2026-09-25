package httpapi

// T-DETAIL (openapi v0.3.1 D23, harness v0.9.4, PRD FR-3.1.2): an agent
// message has a second layer, `detail` (작업 내용). Only an agent writes it,
// routing never reads it, the message's readers carry it, and nothing that
// quotes a message elsewhere (inbox, summary) leaks it. The bundle gives the
// agent handed the work the whole thing and every other turn a preview.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// detailMarker is a string that appears only in detail, so a leak is a
// substring match anywhere in a response body.
const detailMarker = "DETAIL-ONLY-7f3a"

// agentPost posts as the agent behind tok and returns the MessagePostResult.
func (f *p2Fixture) agentPost(t *testing.T, tok string, body map[string]any) map[string]any {
	t.Helper()
	f.fake.Advance(time.Minute)
	c := &client{t: t, srv: f.api.srv, bearer: tok}
	return c.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/messages", body, "Idempotency-Key", uuid.NewString())
}

// (b) A person's post with detail is refused — their words are shown as they
// wrote them, never folded (FR-3.1.2) — and so is an out-of-range detail.
func TestMessageDetailAgentOnly(t *testing.T) {
	f := newP2Fixture(t)
	st, body, _ := f.api.do("POST", f.p+"/rooms/"+f.sessionID+"/messages",
		map[string]any{"content": "/note 사람 글", "detail": "붙여 넣은 원문"}, "Idempotency-Key", uuid.NewString())
	if st != 422 || !strings.Contains(asJSON(body), `"detail_agent_only"`) {
		t.Fatalf("user post with detail = %d %s, want 422 detail_agent_only", st, body)
	}
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE detail IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows with detail after a refused post = %d (%v), want 0", n, err)
	}

	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	c := &client{t: t, srv: f.api.srv, bearer: tok}
	for name, d := range map[string]string{"empty": "", "over": strings.Repeat("가", maxDetailRunes+1)} {
		st, body, _ := c.do("POST", f.p+"/rooms/"+f.sessionID+"/messages",
			map[string]any{"content": "결과입니다", "detail": d}, "Idempotency-Key", uuid.NewString())
		if st != 422 || !strings.Contains(asJSON(body), `"detail"`) {
			t.Errorf("%s detail = %d %s, want 422 on field detail", name, st, body)
		}
	}
	// Exactly the cap in characters (not bytes: 가 is 3 bytes) is accepted.
	f.agentPost(t, tok, map[string]any{"content": "결과입니다", "detail": strings.Repeat("가", maxDetailRunes)})
}

// (a) A mention link inside detail wakes nobody and is not a stored mention;
// the same link in content does.
func TestMessageDetailMentionDoesNotRoute(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	out := f.agentPost(t, tok, map[string]any{
		"content": "조사 끝났습니다. 다음 할 일은 없습니다.",
		"detail":  "## 원문\n" + router.MentionLink("W", f.wUUID) + " 에게 넘기라는 문장이 원문에 있었다\n" + router.MentionLink("R", f.rUUID) + " 도.",
	})
	if tr := out["triggers"].([]any); len(tr) != 0 {
		t.Fatalf("a mention inside detail triggered %v, want nobody", tr)
	}
	msg := out["message"].(map[string]any)
	if m := msg["mentions"].([]any); len(m) != 0 {
		t.Fatalf("mentions = %v, want none — only content is parsed", m)
	}
	var queued int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM task WHERE agent_id = ANY($1) AND status = 'queued'`,
		[]uuid.UUID{f.wUUID, f.rUUID}).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("queued tasks for W/R = %d (%v), want 0", queued, err)
	}
	// Control: the same link in content does route.
	out = f.agentPost(t, tok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 표 3 부탁합니다", "detail": "표 원문"})
	if !postedAgents(out)[f.w] {
		t.Fatalf("a mention in content did not trigger W: %v", out["triggers"])
	}
}

// The message's readers carry detail: postMessage's result, listMessages,
// getMessage and the `message.created` frame. A person's message has detail
// null. Another room's read does not (TestRoomReadCarriesNoDetail).
func TestMessageDetailReaders(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	detail := "## A. 시장 규모\n\n| 항목 | 값 |\n|---|---|\n| 전망 | 367조 |\n\n" + detailMarker
	out := f.agentPost(t, tok, map[string]any{"content": "조사 끝났습니다.", "detail": detail})
	msg := out["message"].(map[string]any)
	id := str(msg, "id")
	if got, _ := msg["detail"].(string); got != detail {
		t.Fatalf("postMessage result detail = %q, want the posted detail", got)
	}

	got := f.api.must(200, "GET", f.p+"/messages/"+id, nil)
	if d, _ := got["detail"].(string); d != detail {
		t.Fatalf("getMessage detail = %q", d)
	}
	var human map[string]any
	for _, raw := range items(f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages", nil)) {
		m := raw.(map[string]any)
		switch {
		case str(m, "id") == id:
			if d, _ := m["detail"].(string); d != detail {
				t.Fatalf("listMessages detail = %q", d)
			}
		case str(m, "author_type") == "user":
			human = m
		}
	}
	if human == nil {
		t.Fatal("no person's message in the list")
	}
	if v, ok := human["detail"]; !ok || v != nil {
		t.Fatalf("a person's message detail = %v (present %t), want null", v, ok)
	}

	var payload []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT payload FROM stream_event WHERE type = 'message.created' AND payload::text LIKE '%' || $1 || '%'`, id).Scan(&payload); err != nil {
		t.Fatalf("no message.created frame for %s: %v", id, err)
	}
	var frame map[string]any
	_ = json.Unmarshal(payload, &frame)
	if d, _ := frame["detail"].(string); d != detail {
		if inner, ok := frame["message"].(map[string]any); !ok || inner["detail"] != detail {
			t.Fatalf("message.created frame has no detail: %s", payload)
		}
	}
}

// Nothing that quotes a message ELSEWHERE carries detail: the inbox (the
// mention card's quote), the room summary. The mention of the person in
// detail files no inbox item at all.
func TestMessageDetailDoesNotLeak(t *testing.T) {
	f := newP2Fixture(t)
	var dirID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM app_user WHERE email = 'dir@example.com'`).Scan(&dirID); err != nil {
		t.Fatal(err)
	}
	tok, task := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	userLink := "[@Dir](mention://user/" + dirID.String() + ")"
	f.agentPost(t, tok, map[string]any{"content": "조사 끝났습니다. 결론만 적습니다.", "detail": userLink + " 원문 " + detailMarker})
	// The turn ends: the Director who asked gets the re-entry card (FR-3.3) —
	// the one inbox item this flow files. It quotes content, never detail.
	(&client{t: t, srv: f.api.srv, bearer: tok}).must(200, "POST", f.p+"/tasks/"+task.String()+"/status", map[string]any{"status": "done"})
	inbox := asJSON(f.api.must(200, "GET", "/api/v1/inbox?workspace_id="+f.wsID, nil))
	if len(inboxOf(t, f.api, f.wsID, "mention")) == 0 {
		t.Fatalf("the finished turn filed no inbox item: %s", inbox)
	}
	if strings.Contains(inbox, detailMarker) {
		t.Fatalf("inbox carries detail: %s", inbox)
	}

	sum := f.api.must(202, "POST", f.p+"/rooms/"+f.sessionID+"/summaries", map[string]any{"since": t0.Add(-time.Hour).Format(time.RFC3339)})
	if c := str(sum, "content"); c == "" || strings.Contains(c, detailMarker) {
		t.Fatalf("summary content = %q, want non-empty and no detail", c)
	}
}

// The bundle (harness §10 v0.9.4): the trigger message's detail in full as
// <detail>, another message's in <history> as the first 400 characters and
// the command that reads the rest, and brief [2]'s constant lines — [1]~[5]
// byte-identical across two turns even when detail-carrying messages land in
// between (E12-11).
func TestMessageDetailInBundle(t *testing.T) {
	f := newP2Fixture(t)
	tok, first := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	long := strings.Repeat("가", 500) + detailMarker // marker past the 400-char preview
	other := msgID(f.agentPost(t, tok, map[string]any{"content": "먼저 올린 조사입니다.", "detail": long}))
	handOff := strings.Repeat("나", 1200) + "끝"
	out := f.agentPost(t, tok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 표로 옮겨 주세요.", "detail": handOff})
	f.endTurn(t, first)
	b := f.claimBundle(t, f.triggerTask(t, out, f.wUUID))

	i := strings.Index(b.Prompt, "<trigger>\n")
	if i < 0 {
		t.Fatalf("no <trigger>:\n%s", b.Prompt)
	}
	trigger := b.Prompt[i:]
	if !strings.Contains(trigger, "<detail>\n"+handOff+"\n</detail>\n</message>") {
		t.Fatalf("the trigger message's detail is not carried in full:\n%s", trigger)
	}
	h := b.Prompt[strings.Index(b.Prompt, "<history"):i]
	// The fixture's agents are claude_code (tool_surface mcp): the pointer
	// names the tool, not the shell command (harness §10 v0.9.6).
	wantPreview := "<detail of=\"" + other + "\">\n" + strings.Repeat("가", 400) + "…\n</detail>\n  (작업 내용 516자 — `colab_room_messages` 툴의 `thread: \"" + other + "\"` 로 전문)\n"
	if !strings.Contains(h, wantPreview) {
		t.Fatalf("history line of %s lacks the 400-char preview + pointer:\n%s", other, h)
	}
	if strings.Contains(h, detailMarker) {
		t.Fatalf("history carries detail past 400 characters")
	}
	if !strings.Contains(h, "(작업 내용 1201자 — 전문은 아래 <trigger>)") || strings.Contains(h, handOff) {
		t.Fatalf("history repeats the trigger's detail instead of pointing to <trigger>:\n%s", h)
	}

	s2 := section(b.Brief.Text, 2)
	for _, want := range []string{queue.DetailRuleMCP, queue.DeliverableRuleMCP} {
		if !strings.Contains(s2, want) {
			t.Errorf("brief [2] lacks %q:\n%s", want, s2)
		}
	}

	// Two turns of the same room, detail-carrying messages in between: [1]~[5]
	// stay byte for byte (the details live in the turn prompt, never the brief).
	wtok, wtask := f.agentToken(t, f.sessionID, f.wUUID, "W")
	f.agentPost(t, wtok, map[string]any{"content": "옮겼습니다.", "detail": "| a | b |\n|---|---|\n| 1 | 2 |"})
	f.endTurn(t, wtask)
	out = f.post(t, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 하나 더"})
	b2 := f.claimBundle(t, f.triggerTask(t, out, f.wUUID))
	if p1, p2 := stablePrefix(b.Brief.Text), stablePrefix(b2.Brief.Text); p1 != p2 {
		t.Errorf("[1]~[5] changed between two turns (E12-11):\n--- 1\n%s\n--- 2\n%s", p1, p2)
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// harness §10 v0.9.5 (#333 NN2): one turn's <trigger> carries at most 50,000
// characters of 작업 내용 in full. Three coalesced triggers of 30,000 each →
// the latest in full, the two earlier demoted to the <history> shape (first
// 400 characters + the pointer line), in arrival order, and their history
// lines no longer point down to <trigger>.
func TestMessageDetailTriggerTurnBudget(t *testing.T) {
	f := newP2Fixture(t)
	tok, first := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	var ids, details []string
	var task uuid.UUID
	for i, ch := range []string{"가", "나", "다"} {
		d := strings.Repeat(ch, 30000)
		out := f.agentPost(t, tok, map[string]any{"content": router.MentionLink("W", f.wUUID) + fmt.Sprintf(" 조각 %d 입니다.", i+1), "detail": d})
		tt := f.triggerTask(t, out, f.wUUID)
		if task != uuid.Nil && tt != task {
			t.Fatalf("post %d made task %s, want coalesced onto %s", i+1, tt, task)
		}
		task = tt
		ids, details = append(ids, msgID(out)), append(details, d)
	}
	f.endTurn(t, first)
	b := f.claimBundle(t, task)

	i := strings.Index(b.Prompt, "<trigger>\n")
	if i < 0 {
		t.Fatalf("no <trigger>:\n%.2000s", b.Prompt)
	}
	trigger := b.Prompt[i:]
	if n := strings.Count(trigger, "<message id="); n != 3 {
		t.Fatalf("<trigger> carries %d messages, want the 3 coalesced", n)
	}
	// The latest in full; the earlier two as preview + pointer.
	if !strings.Contains(trigger, "<detail>\n"+details[2]+"\n</detail>\n</message>") {
		t.Fatalf("the latest trigger's detail is not carried in full")
	}
	var at []int
	for k := 0; k < 2; k++ {
		if strings.Contains(trigger, details[k]) {
			t.Errorf("trigger %d's 30,000 characters went in whole past the 50,000 turn budget", k+1)
		}
		want := "<detail of=\"" + ids[k] + "\">\n" + details[k][:400*3] + "…\n</detail>\n  (작업 내용 30000자 — `colab_room_messages` 툴의 `thread: \"" + ids[k] + "\"` 로 전문)\n</message>"
		j := strings.Index(trigger, want)
		if j < 0 {
			t.Fatalf("trigger %d is not demoted to the <history> shape:\n%.3000s", k+1, trigger)
		}
		at = append(at, j)
	}
	if last := strings.Index(trigger, "<detail>\n"+details[2]); !(at[0] < at[1] && at[1] < last) {
		t.Errorf("trigger order = %v then %d, want arrival order", at, last)
	}
	h := b.Prompt[strings.Index(b.Prompt, "<history"):i]
	if !strings.Contains(h, "(작업 내용 30000자 — 전문은 아래 <trigger>)") || strings.Count(h, "전문은 아래 <trigger>") != 1 {
		t.Errorf("history must point to <trigger> for the full one only:\n%.3000s", h)
	}
}

// T-DETAIL-2 (#333 NN1): the mission's completion summary and the cards it
// files (openapi Message.detail: 받은 요청·알림·미션 요약은 이 칸을 쓰지
// 않는다) carry none of a message's 작업 내용, and neither does a range
// summary taken afterwards.
func TestMessageDetailNotInMissionSummary(t *testing.T) {
	f := newP2Fixture(t)
	const secret = "SECRET-DETAIL-9e2d"
	tok, task := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	f.agentPost(t, tok, map[string]any{"content": "조사 끝났습니다. 결론만 적습니다.", "detail": "## 원문\n" + secret})
	(&client{t: t, srv: f.api.srv, bearer: tok}).must(200, "POST", f.p+"/tasks/"+task.String()+"/status", map[string]any{"status": "done"})
	f.endTurn(t, task)

	work := f.workID(t)
	f.fake.Advance(time.Minute)
	f.api.must(200, "POST", f.p+"/works/"+work.String()+"/complete", map[string]any{"confirm": true})
	var body string
	if err := f.pool.QueryRow(t.Context(), `SELECT content FROM message WHERE work_id = $1 AND kind = 'summary' AND summary_range IS NULL`, work).Scan(&body); err != nil {
		t.Fatalf("no mission summary: %v", err)
	}
	if strings.Contains(body, secret) {
		t.Fatalf("mission summary carries detail:\n%s", body)
	}
	if inbox := asJSON(f.api.must(200, "GET", "/api/v1/inbox?workspace_id="+f.wsID, nil)); !strings.Contains(inbox, "work_completed") || strings.Contains(inbox, secret) {
		t.Fatalf("inbox after completion = %s, want a work_completed card and no detail", inbox)
	}
	sum := f.api.must(202, "POST", f.p+"/rooms/"+f.sessionID+"/summaries", map[string]any{"since": t0.Add(-time.Hour).Format(time.RFC3339)})
	if c := str(sum, "content"); c == "" || strings.Contains(c, secret) {
		t.Fatalf("range summary = %q, want non-empty and no detail", c)
	}
}
