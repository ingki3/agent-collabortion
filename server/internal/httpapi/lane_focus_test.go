package httpapi

// T-FOCUS (PRD FR-3.1.5 · openapi v0.3.8 Lane.focus · setTaskStatus working
// note): the lane's 「지금 하는 일」 end to end through the HTTP surface —
// the claim writes the derived sentence, `status set working --note` replaces
// it with the agent's (cut at 120, 60-second coalescing of the frames, every
// declaration on the feed), the end of the turn empties it, and a token can
// only speak for its own task.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// claimAll runs a real claim and returns task id → bundle.
func (f *p2Fixture) claimAll(t *testing.T) map[string]contracts.TaskBundle {
	t.Helper()
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	bundles, err := f.srv.Queue.Claim(t.Context(), runtimeID.String(), 10, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]contracts.TaskBundle{}
	for _, b := range bundles {
		out[b.Task.ID] = b
	}
	return out
}

// startTurn mentions agent with body as the Director, claims, and returns the
// task, its lane and the task token the daemon received.
func (f *p2Fixture) startTurn(t *testing.T, agent uuid.UUID, name, body string) (taskID, laneID uuid.UUID, agentAPI *client) {
	t.Helper()
	out := f.post(t, map[string]any{"content": router.MentionLink(name, agent) + " " + body})
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent.String() {
			taskID, laneID = mustUUID(t, str(tr, "task_id")), mustUUID(t, str(tr, "lane_id"))
		}
	}
	if taskID == uuid.Nil {
		t.Fatalf("no task for %s: %v", name, out["triggers"])
	}
	b, ok := f.claimAll(t)[taskID.String()]
	if !ok {
		t.Fatalf("claim handed out no bundle for %s", name)
	}
	f.runTask(t, taskID)
	return taskID, laneID, &client{t: t, srv: f.api.srv, bearer: b.TaskToken}
}

func focusOf(lane map[string]any) map[string]any {
	fo, _ := lane["focus"].(map[string]any)
	return fo
}

func (f *p2Fixture) declare(t *testing.T, c *client, taskID uuid.UUID, note string) {
	t.Helper()
	c.must(200, "POST", f.p+"/tasks/"+taskID.String()+"/status", map[string]any{"status": "working", "note": note})
}

// 회귀 주입: MarkDispatched 의 lanefocus.Derive 를 지우면 (derived) FAIL;
// SetAgentStatus working 의 Declare 를 지우면 (agent) FAIL; Declare 의
// Truncate 를 지우면 DB CHECK 가 500 을 내 (120) FAIL; tasks.publish 의
// ClearIfIdle 을 지우면 (turn end) FAIL; lanes.Load 의 focus 칸을 빼면
// (listLanes) FAIL; SetTaskStatus 의 task 범위 검사를 지우면 (scope) FAIL.
func TestLaneFocusLifecycle(t *testing.T) {
	f := newP2Fixture(t)
	taskID, laneID, agent := f.startTurn(t, f.rUUID, "R", "코너에서 차가 미끄러지는 원인을 찾아 주세요")

	// (derived) the claim wrote the server's sentence from the trigger row.
	fo := focusOf(f.laneCard(t, laneID))
	if fo == nil || str(fo, "source") != "derived" {
		t.Fatalf("(derived) focus after claim = %v", fo)
	}
	if want := "Dir의 「코너에서 차가 미끄러지는 원인을 찾아 주세요」 요청을 처리하고 있습니다"; str(fo, "text") != want {
		t.Fatalf("(derived) text = %q, want %q", str(fo, "text"), want)
	}

	// (agent) the declaration replaces it.
	f.fake.Advance(time.Minute)
	f.declare(t, agent, taskID, "타이어 접지 한계 공식을 점검하고 있습니다")
	fo = focusOf(f.laneCard(t, laneID))
	if str(fo, "source") != "agent" || str(fo, "text") != "타이어 접지 한계 공식을 점검하고 있습니다" {
		t.Fatalf("(agent) focus = %v", fo)
	}
	if at, _ := time.Parse(time.RFC3339Nano, str(fo, "at")); !at.Equal(f.fake.Now()) {
		t.Fatalf("(agent) at = %s, want the declaration's %s", str(fo, "at"), f.fake.Now())
	}

	// (empty) `working` with no note is a feed line, not a new 「지금」.
	agent.must(200, "POST", f.p+"/tasks/"+taskID.String()+"/status", map[string]any{"status": "working"})
	agent.must(200, "POST", f.p+"/tasks/"+taskID.String()+"/status", map[string]any{"status": "working", "note": "   "})
	if got := str(focusOf(f.laneCard(t, laneID)), "text"); got != "타이어 접지 한계 공식을 점검하고 있습니다" {
		t.Fatalf("(empty) an empty note replaced the focus: %q", got)
	}

	// (120) a long sentence is cut, not refused.
	long := strings.Repeat("가나다라마", 40)
	f.declare(t, agent, taskID, long)
	got := str(focusOf(f.laneCard(t, laneID)), "text")
	if utf8.RuneCountInString(got) != 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("(120) text = %d runes %q", utf8.RuneCountInString(got), got)
	}

	// (scope) another agent's token cannot write this lane's 「지금」.
	wTask, wLane, _ := f.startTurn(t, f.wUUID, "W", "FX 코드를 합쳐 주세요")
	st, _, _ := agent.do("POST", f.p+"/tasks/"+wTask.String()+"/status", map[string]any{"status": "working", "note": "남의 일"})
	if st != 403 {
		t.Fatalf("(scope) R wrote W's status: %d", st)
	}
	if fo := focusOf(f.laneCard(t, wLane)); str(fo, "source") != "derived" {
		t.Fatalf("(scope) W's focus changed: %v", fo)
	}

	// (feed) every declaration is on the activity feed.
	if n := f.count(t, `SELECT count(*) FROM task_event WHERE task_id = $1 AND verb = 'set_status' AND object_ref = '"working"'::jsonb`, taskID); n != 4 {
		t.Fatalf("(feed) set_status working events = %d, want 4 (two notes, two empty)", n)
	}

	// (turn end) finish empties it.
	f.fake.Advance(time.Minute)
	if _, err := f.srv.Tasks.Finish(t.Context(), taskID, currentAttempt(t, f, taskID), contracts.Finish{Outcome: "completed", StopReason: "end_turn"}); err != nil {
		t.Fatal(err)
	}
	if fo, ok := f.laneCard(t, laneID)["focus"]; !ok || fo != nil {
		t.Fatalf("(turn end) focus after finish = %v (present=%v), want null", fo, ok)
	}
	// W's turn is untouched by R's end.
	if fo := focusOf(f.laneCard(t, wLane)); fo == nil {
		t.Fatal("(turn end) R's finish emptied W's focus")
	}
}

// A declaration on a task that is not running (queued behind the running
// one, or already finished) must not store a sentence nobody will empty.
//
// 회귀 주입: SetAgentStatus 의 taskStatus 조건을 지우면 FAIL.
func TestLaneFocusOnlyWhileRunning(t *testing.T) {
	f := newP2Fixture(t)
	taskID, laneID, agent := f.startTurn(t, f.rUUID, "R", "정리해 주세요")
	if _, err := f.srv.Tasks.Finish(t.Context(), taskID, currentAttempt(t, f, taskID), contracts.Finish{Outcome: "completed", StopReason: "end_turn"}); err != nil {
		t.Fatal(err)
	}
	// The token is revoked by the finish; write through the service as a
	// late call would have reached it just before the revoke landed.
	if _, err := f.srv.Router.SetAgentStatus(t.Context(), taskID, 1, "working", "늦은 선언"); err != nil {
		t.Fatal(err)
	}
	_ = agent
	if fo := f.laneCard(t, laneID)["focus"]; fo != nil {
		t.Fatalf("a finished turn got a focus: %v", fo)
	}
}

// The end of a turn is every end: cancel and a failure that requeues.
//
// 회귀 주입: tasks.publish 의 ClearIfIdle 을 지우면 (cancel)·(error) FAIL.
func TestLaneFocusClearedOnCancelAndError(t *testing.T) {
	f := newP2Fixture(t)
	rTask, rLane, rAPI := f.startTurn(t, f.rUUID, "R", "첫 번째")
	f.declare(t, rAPI, rTask, "첫 번째 문제를 풀고 있습니다")
	f.api.must(202, "POST", f.p+"/lanes/"+rLane.String()+"/cancel", map[string]any{})
	if _, err := f.srv.Tasks.Finish(t.Context(), rTask, currentAttempt(t, f, rTask), contracts.Finish{Outcome: "cancelled", StopReason: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if fo := f.laneCard(t, rLane)["focus"]; fo != nil {
		t.Fatalf("(cancel) focus = %v", fo)
	}

	wTask, wLane, wAPI := f.startTurn(t, f.wUUID, "W", "두 번째")
	f.declare(t, wAPI, wTask, "두 번째 문제를 풀고 있습니다")
	if _, err := f.srv.Tasks.Finish(t.Context(), wTask, currentAttempt(t, f, wTask), contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther}); err != nil {
		t.Fatal(err)
	}
	if fo := f.laneCard(t, wLane)["focus"]; fo != nil {
		t.Fatalf("(error) focus after a failed attempt = %v", fo)
	}
}

// A delegated turn's derived sentence names the delegator and quotes the
// brief, not the server's mention-prefixed message.
//
// 회귀 주입: lanefocus.Sentence 의 Delegated 가지를 지우면 FAIL; Quote 의
// 앞 멘션 떼기를 지우면(위임 메시지는 서버의 멘션 링크 + brief) FAIL.
func TestLaneFocusDerivedForDelegation(t *testing.T) {
	f := newP2Fixture(t)
	leadTask, _, _ := f.startTurn(t, f.leadUUID, "Lead", "시작")
	res, err := f.srv.Router.Delegate(t.Context(), leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "타이어 접지 한계 공식을 점검"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.claimAll(t)[res.Task.Id.String()]; !ok {
		t.Fatal("the delegated task was not claimed")
	}
	fo := focusOf(f.laneCard(t, uuid.UUID(res.Lane.Id)))
	if want := "@Lead가 맡긴 「타이어 접지 한계 공식을 점검」을 하고 있습니다"; str(fo, "text") != want || str(fo, "source") != "derived" {
		t.Fatalf("focus = %v, want %q", fo, want)
	}
}

// NN2: a trigger that is ONLY a mention has nothing to quote, so the turn
// starts with no 「지금」 줄 rather than an empty 「」 one. The unit table covers
// Sentence("") but nothing took Derive — the DB path — through it.
//
// 회귀 주입: Derive 의 「text == "" 이면 Clear」 분기를 지우면 FAIL(빈 문장이
// 저장돼 CHECK 위반 500, 또는 「」 만 남은 줄).
func TestLaneFocusDerivedNeedsSomethingToQuote(t *testing.T) {
	f := newP2Fixture(t)
	// 멘션만 있는 지시 — 본문에 인용할 말이 없다.
	out := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID)})
	tr := out["triggers"].([]any)[0].(map[string]any)
	taskID, laneID := mustUUID(t, str(tr, "task_id")), mustUUID(t, str(tr, "lane_id"))
	if _, ok := f.claimAll(t)[taskID.String()]; !ok {
		t.Fatal("the task was not claimed")
	}
	if fo := f.laneCard(t, laneID)["focus"]; fo != nil {
		t.Fatalf("멘션만 있는 트리거가 문장을 만들었다: %v", fo)
	}
	// 그래도 에이전트가 선언하면 그 문장은 선다.
	f.runTask(t, taskID)
	if _, err := f.srv.Router.SetAgentStatus(t.Context(), taskID, 1, "working", "커브 자료를 읽고 있습니다"); err != nil {
		t.Fatal(err)
	}
	if got := str(focusOf(f.laneCard(t, laneID)), "text"); got != "커브 자료를 읽고 있습니다" {
		t.Fatalf("선언이 서지 않았다: %q", got)
	}
}

// openapi setTaskStatus v0.3.8: 「60초 안에 연속으로 오면 마지막 것만
// lane.updated 로 흘린다(피드에는 모두 남긴다)」 — read off the real SSE body.
//
// 회귀 주입: Declare 의 PublishNow 판정을 늘 true 로 하면 (B 가 흐름) FAIL;
// scheduleFocusFlush 를 지우면 (C 가 안 옴) FAIL.
func TestLaneFocusCoalescesFramesWithinSixtySeconds(t *testing.T) {
	f := newP2Fixture(t)
	taskID, laneID, agent := f.startTurn(t, f.rUUID, "R", "시작")

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?room_id="+f.sessionID)
	defer stop()

	focusFrame := func(p json.RawMessage) (string, bool) {
		var l struct {
			ID    string `json:"id"`
			Focus *struct {
				Text   string `json:"text"`
				Source string `json:"source"`
			} `json:"focus"`
		}
		_ = json.Unmarshal(p, &l)
		if l.ID != laneID.String() || l.Focus == nil || l.Focus.Source != "agent" {
			return "", false
		}
		return l.Focus.Text, true
	}

	f.declare(t, agent, taskID, "A 를 보고 있습니다")
	waitFrame(t, frames, "lane.updated", func(p json.RawMessage) bool { s, ok := focusFrame(p); return ok && s == "A 를 보고 있습니다" })

	f.fake.Advance(10 * time.Second)
	f.declare(t, agent, taskID, "B 를 보고 있습니다")
	f.fake.Advance(10 * time.Second)
	f.declare(t, agent, taskID, "C 를 보고 있습니다")

	// Nothing flows for B or C inside the window.
	quiet := time.After(700 * time.Millisecond)
loop:
	for {
		select {
		case fr := <-frames:
			if fr.Type == "lane.updated" {
				if s, ok := focusFrame(fr.Payload); ok && s != "A 를 보고 있습니다" {
					t.Fatalf("a declaration inside the 60s window went out at once: %q", s)
				}
			}
		case <-quiet:
			break loop
		}
	}
	// The window closes 60s after A: only the LAST one goes out.
	f.fake.Advance(41 * time.Second)
	got := waitFrame(t, frames, "lane.updated", func(p json.RawMessage) bool { _, ok := focusFrame(p); return ok })
	if s, _ := focusFrame(got); s != "C 를 보고 있습니다" {
		t.Fatalf("flush carried %q, want the last declaration", s)
	}
	// Every one of the three is on the feed.
	if n := f.count(t, `SELECT count(*) FROM task_event WHERE task_id = $1 AND verb = 'set_status' AND object_ref = '"working"'::jsonb`, taskID); n != 3 {
		t.Fatalf("feed has %d declarations, want 3", n)
	}
	// And after a quiet minute the next one goes out at once.
	f.fake.Advance(2 * time.Minute)
	f.declare(t, agent, taskID, "D 를 보고 있습니다")
	waitFrame(t, frames, "lane.updated", func(p json.RawMessage) bool { s, ok := focusFrame(p); return ok && s == "D 를 보고 있습니다" })
}

// harness v0.9.13 in the real bundle: the claim's brief [2] carries the
// focus line in the agent's surface words, and two turns of the same room
// keep [1]~[5] byte-identical (E12-11).
//
// 회귀 주입: Section2 에서 s.FocusRule 을 빼면 (bundle) FAIL; FocusRule 에
// 턴마다 바뀌는 값(시각)을 넣으면 (E12-11) FAIL.
func TestBriefCarriesFocusLine(t *testing.T) {
	f := newP2Fixture(t)
	mk := func(body string) contracts.TaskBundle {
		out := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " " + body})
		id := str(out["triggers"].([]any)[0].(map[string]any), "task_id")
		b, ok := f.claimAll(t)[id]
		if !ok {
			t.Fatal("no bundle")
		}
		f.endTurn(t, mustUUID(t, id))
		return b
	}
	b1, b2 := mk("하나"), mk("둘")
	two := section(b1.Brief.Text, 2)
	if !strings.Contains(two, queue.FocusRuleMCP) {
		t.Fatalf("(bundle) [2] lacks the focus line:\n%s", two)
	}
	if strings.Contains(two, queue.FocusRule) {
		t.Fatalf("(bundle) the claude_code brief carries the shell line:\n%s", two)
	}
	if p1, p2 := stablePrefix(b1.Brief.Text), stablePrefix(b2.Brief.Text); p1 != p2 {
		t.Fatalf("(E12-11) [1]~[5] changed between two turns:\n--- 1\n%s\n--- 2\n%s", p1, p2)
	}
}
