// G4 웹 여정 2판이 격리한 실시간 전파 결함의 DB 통합 테스트.
//
// 새로고침하면 보이는 것이 스트림으로는 오지 않았다: 계약 StreamEvent 표가 S7
// 로 선언한 여섯 종류에 발행 자리가 없거나(참여자·아티팩트·결정·완료 진행률·
// 비용) 일부 경로에만 있었다(시스템 메시지·질문 카드). 여섯 건 모두 실제 SSE
// 본문을 읽어서 확인한다 — stream_event 테이블이 아니라 웹이 읽는 그 스트림.
package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// waitFrame returns the payload of the first frame of type typ that `want`
// accepts. Other types and other sessions' frames are skipped — one session
// stream carries a dozen kinds at once.
func waitFrame(t *testing.T, frames <-chan streamFrame, typ string, want func(json.RawMessage) bool) json.RawMessage {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case fr, ok := <-frames:
			if !ok {
				t.Fatalf("stream closed before a %s frame arrived", typ)
			}
			if fr.Type != typ || (want != nil && !want(fr.Payload)) {
				continue
			}
			return fr.Payload
		case <-deadline:
			t.Fatalf("no %s frame within 10s — S7 would only see it after a reload", typ)
			return nil
		}
	}
}

// waitTypes returns the first frame of each named type. One call is needed
// whenever a single request publishes several of them: frames arrive in publish
// order and waitFrame discards what it skips, so waiting for the second type
// after the first has already thrown the second away.
func waitTypes(t *testing.T, frames <-chan streamFrame, types ...string) map[string]json.RawMessage {
	t.Helper()
	want := map[string]bool{}
	for _, ty := range types {
		want[ty] = true
	}
	got := map[string]json.RawMessage{}
	deadline := time.After(10 * time.Second)
	for len(got) < len(want) {
		select {
		case fr, ok := <-frames:
			if !ok {
				t.Fatalf("stream closed with only %d of %d frames", len(got), len(want))
			}
			if want[fr.Type] && got[fr.Type] == nil {
				got[fr.Type] = fr.Payload
			}
		case <-deadline:
			for ty := range want {
				if got[ty] == nil {
					t.Errorf("no %s frame within 10s — S7 would only see it after a reload", ty)
				}
			}
			t.FailNow()
		}
	}
	return got
}

// ---------------------------------------------------------------------------
// message.created — 시스템 메시지와 질문 카드 (W10)
// ---------------------------------------------------------------------------

// TestSystemMessagesPublish covers the two message inserts router.Post and
// DelegateLane do not own: the `blocked` question card (FR-6.2.1) and the join
// bundle SystemPost writes when a delegation group ends (FR-6.5). Both are
// timeline messages; neither published, so the delegator saw the question and
// the bundle only on reload.
func TestSystemMessagesPublish(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))

	// Two children so the group is still open after the first one blocks: the
	// join fires only when every child has ended.
	blocked, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A 조사"})
	if err != nil {
		t.Fatal(err)
	}
	done, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.wUUID, Brief: "B 정리"})
	if err != nil {
		t.Fatal(err)
	}

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+f.sessionID)
	defer stop()

	if _, err := f.srv.Router.SetAgentStatus(ctx, blocked.Task.Id, 1, "blocked", "예산은 얼마입니까?"); err != nil {
		t.Fatal(err)
	}
	card := waitFrame(t, frames, "message.created", func(p json.RawMessage) bool {
		return messageKind(t, p) == "blocked_q"
	})
	if got := messageContent(t, card); got != "예산은 얼마입니까?" {
		t.Fatalf("blocked_q frame content = %q, want the question", got)
	}

	if _, err := f.srv.Router.SetAgentStatus(ctx, done.Task.Id, 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	bundle := waitFrame(t, frames, "message.created", func(p json.RawMessage) bool {
		return messageKind(t, p) == "system"
	})
	if got := messageContent(t, bundle); got == "" {
		t.Fatal("join bundle frame carried an empty message")
	}
}

func messageKind(t *testing.T, p json.RawMessage) string {
	t.Helper()
	var m struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(p, &m); err != nil {
		t.Fatalf("message.created payload: %v", err)
	}
	return m.Kind
}

func messageContent(t *testing.T, p json.RawMessage) string {
	t.Helper()
	var m struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(p, &m); err != nil {
		t.Fatalf("message.created payload: %v", err)
	}
	return m.Content
}

// ---------------------------------------------------------------------------
// participant.updated — FR-1.3 파생 상태 (W7)
// ---------------------------------------------------------------------------

// TestClaimPublishesParticipantWorking — the agent chip is derived from the
// agent's task rows, so it changes exactly when one moves. Nothing published
// it: S7's chips stayed `idle` while their lanes ran.
func TestClaimPublishesParticipantWorking(t *testing.T) {
	f := newG4Fixture(t)
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+f.sessionID)
	defer stop()

	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})

	var p struct {
		AgentID string `json:"agent_id"`
		Status  string `json:"status"`
		Agent   struct {
			Name string `json:"name"`
		} `json:"agent"`
	}
	raw := waitFrame(t, frames, "participant.updated", func(raw json.RawMessage) bool {
		_ = json.Unmarshal(raw, &p)
		return p.AgentID == f.lead
	})
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != "working" {
		t.Fatalf("participant.updated after claim = %q, want working", p.Status)
	}
	// The payload is a whole Participant (openapi StreamEvent), not a patch:
	// the web merges it into the row it already holds and renders the chip
	// from it, so a frame missing the agent block renders a nameless chip.
	if p.Agent.Name != "Lead" {
		t.Fatalf("participant.updated payload has no agent block: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// artifact.created · session.completion_progress (W13)
// ---------------------------------------------------------------------------

// TestSubmitPublishesArtifactAndProgress — submitArtifact both stores a row
// the 산출물 tab lists and moves the completion bar. Neither had a publisher,
// so a submitted artifact left both untouched until the page reloaded.
func TestSubmitPublishesArtifactAndProgress(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+sess)
	defer stop()

	if st, out := f.submit(t, sess, tok, "report.md", "document", []byte("# 결과\n")); st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}

	// Both frames come out of the one request, so they are collected together.
	out := waitTypes(t, frames, "artifact.created", "session.completion_progress")

	var a struct {
		Name      string `json:"name"`
		Version   int    `json:"version"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out["artifact.created"], &a); err != nil {
		t.Fatal(err)
	}
	if a.Name != "report.md" || a.Version != 1 {
		t.Fatalf("artifact.created = %+v, want report.md v1", a)
	}
	if a.SessionID != sess {
		t.Fatalf("artifact.created session_id = %s, want %s", a.SessionID, sess)
	}

	var prog struct {
		SessionID string `json:"session_id"`
		Progress  struct {
			Met   int `json:"met"`
			Total int `json:"total"`
		} `json:"completion_progress"`
	}
	if err := json.Unmarshal(out["session.completion_progress"], &prog); err != nil {
		t.Fatal(err)
	}
	if prog.SessionID != sess {
		t.Fatalf("completion_progress session_id = %s, want %s", prog.SessionID, sess)
	}
	if prog.Progress.Met != 1 || prog.Progress.Total != 2 {
		t.Fatalf("completion_progress = %d/%d, want 1/2 after the submit", prog.Progress.Met, prog.Progress.Total)
	}
}

// ---------------------------------------------------------------------------
// decision.created — FR-4.2
// ---------------------------------------------------------------------------

// TestRecordDecisionPublishes — the decision log is what a later reader
// consults to find out WHY. It published nowhere, so a decision recorded mid
// session was invisible to everyone watching it.
func TestRecordDecisionPublishes(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.rUUID, "R")

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+f.sessionID)
	defer stop()

	if st, out := f.rawPost(t, f.p+"/sessions/"+f.sessionID+"/decisions", tok,
		map[string]any{"summary": "B 안으로 간다", "rationale": "비용이 낮다"}); st != 201 {
		t.Fatalf("recordDecision = %d %v", st, out)
	}

	var d struct {
		Summary   string  `json:"summary"`
		Rationale *string `json:"rationale"`
		Source    string  `json:"source"`
		SessionID string  `json:"session_id"`
	}
	if err := json.Unmarshal(waitFrame(t, frames, "decision.created", nil), &d); err != nil {
		t.Fatal(err)
	}
	if d.Summary != "B 안으로 간다" || d.Source != "agent" || d.SessionID != f.sessionID {
		t.Fatalf("decision.created = %+v", d)
	}
	// Same mapping as listDecisions: `rationale` is a nullable field the web
	// reads off the frame directly, so it has to be there.
	if d.Rationale == nil || *d.Rationale != "비용이 낮다" {
		t.Fatalf("decision.created rationale = %v, want the recorded one", d.Rationale)
	}
}

// ---------------------------------------------------------------------------
// cost.updated — 세션 비용 (S5 · S7)
// ---------------------------------------------------------------------------

// TestFinishPublishesCost — session.cost_usd was read by the budget banner and
// the summary but written by nobody, and `cost.updated` had no publisher: the
// cost line sat at $0.00 for the whole session. The finish's usage rolls up
// and the frame carries the new total.
func TestFinishPublishesCost(t *testing.T) {
	f := newG4Fixture(t)
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	taskID := str(post["triggers"].([]any)[0].(map[string]any), "task_id")

	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/phase", map[string]any{"phase": "running", "pgid": 100})

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+f.sessionID)
	defer stop()

	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/finish", contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
		Usage: contracts.Usage{InputTokens: 100, OutputTokens: 20, CostUSD: 0.25, Estimated: true},
	})

	var c struct {
		SessionID string  `json:"session_id"`
		CostUSD   float64 `json:"cost_usd"`
		Estimated bool    `json:"estimated"`
	}
	if err := json.Unmarshal(waitFrame(t, frames, "cost.updated", nil), &c); err != nil {
		t.Fatal(err)
	}
	if c.SessionID != f.sessionID || c.CostUSD != 0.25 || !c.Estimated {
		t.Fatalf("cost.updated = %+v, want the attempt's 0.25 marked estimated", c)
	}
	// The frame and the reload have to agree — that disagreement is the whole
	// class of bug this file is about.
	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	if got, _ := sess["cost_usd"].(float64); got != 0.25 {
		t.Fatalf("getSession cost_usd = %v after cost.updated 0.25", sess["cost_usd"])
	}
	if est, _ := sess["cost_estimated"].(bool); !est {
		t.Fatalf("getSession cost_estimated = %v, want true", sess["cost_estimated"])
	}
}
