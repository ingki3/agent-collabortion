// S-78 (T-S16, PR #213 "짚어 둘 것 1"): chain_depth counted "hops since the
// last human message", so sibling delegations and join notices were depth
// too. Once S-76 made delegations and wake-ups hops, an F1-shaped session —
// Director → Lead, Lead delegates three, join, Lead mentions a reviewer,
// re-entry report, Lead delegates again, join — reached chainDepth 9 (Hermes,
// PR #213 review §3) and paused at the default 8, one human message in.
//
// PRD FR-3.5 defines the limit as the DEPTH of the chain a human started
// ("Lead → 실무자 → 리뷰어 → Lead 가 깊이 4"): depth follows causes
// (router/loop.go chainDepth, session_hop.cause_hop_id), so the three
// delegations share a depth and the join brings Lead back to its own. The
// ring the golden E4-01 measures still climbs one per hop, and S-76's
// delegate ↔ join cycle is still stopped — by max_pair_roundtrips, which is
// the limit that describes it.
package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// chainFixture is the P2 fixture plus a fourth agent (QA) and a session of
// four, so Lead can delegate to three siblings and a four-agent ring can run.
type chainFixture struct {
	*p2Fixture
	qa     string
	qaUUID uuid.UUID
}

func newChainFixture(t *testing.T) *chainFixture {
	t.Helper()
	f := &chainFixture{p2Fixture: newP2Fixture(t)}
	a := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "QA", "role": "reviewer", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	})
	f.qa = str(a, "id")
	f.qaUUID = mustUUID(t, f.qa)
	sess := sessionRoom(t, f.api, f.pool, f.p, f.wsID, map[string]any{
		"title": "F1", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": f.lead,
		"participants": []map[string]any{
			{"agent_id": f.lead}, {"agent_id": f.r}, {"agent_id": f.w}, {"agent_id": f.qa},
		},
	})
	f.sessionID = str(sess, "id")
	return f
}

// run marks a task running: the next wake-up or mention for its agent makes a
// fresh task instead of coalescing onto the queued one (FR-3.4).
func (f *chainFixture) run(t *testing.T, task uuid.UUID) uuid.UUID {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE task SET status = 'running', dispatched_at = $2, started_at = $2, runtime_id = (SELECT id FROM runtime LIMIT 1)
		WHERE id = $1`, task, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	return task
}

// queuedTask is the newest queued task of `agent` — what a wake-up made.
func (f *chainFixture) queuedTask(t *testing.T, agent string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id FROM task WHERE session_id = $1 AND agent_id = $2 AND status = 'queued'
		ORDER BY created_at DESC LIMIT 1`, f.sessionID, agent).Scan(&id); err != nil {
		st, reason, detail := f.sessionPause(t)
		limit := "-"
		if detail != nil && detail.Loop != nil && detail.Loop.Limit != nil {
			limit = string(*detail.Loop.Limit)
		}
		t.Fatalf("no queued task for %s — the wake-up did not land (session %s/%s limit=%s): %v", agent, st, reason, limit, err)
	}
	return id
}

// mention posts a mention from `task` (an agent's turn) and returns the task
// it triggered.
func (f *chainFixture) mention(t *testing.T, from uuid.UUID, task uuid.UUID, name string, to uuid.UUID) uuid.UUID {
	t.Helper()
	f.fake.Advance(time.Second)
	out, err := f.srv.Router.Post(t.Context(), mustUUID(t, f.sessionID),
		router.Author{Type: "agent", AgentID: &from, TaskID: &task, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink(name, to) + " 부탁해"})
	if err != nil {
		t.Fatalf("mention %s: %v", name, err)
	}
	if len(out.Triggers) != 1 {
		t.Fatalf("mention %s triggered %d, want 1 (warnings %+v)", name, len(out.Triggers), out.Warnings)
	}
	return mustUUID(t, out.Triggers[0].TaskId.String())
}

func (f *chainFixture) delegate(t *testing.T, task uuid.UUID, to uuid.UUID) uuid.UUID {
	t.Helper()
	f.fake.Advance(time.Second)
	res, err := f.srv.Router.Delegate(t.Context(), task, router.DelegateInput{AgentID: to, Brief: "맡아 줘"})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	return mustUUID(t, res.Task.Id.String())
}

func (f *chainFixture) done(t *testing.T, task uuid.UUID) {
	t.Helper()
	f.fake.Advance(time.Second)
	if _, err := f.srv.Router.SetAgentStatus(t.Context(), task, 1, "done", ""); err != nil {
		t.Fatalf("done: %v", err)
	}
}

// hopDepths replays the session's hops through the limiter and returns each
// hop's chain depth in order — the number the pause decision was made on.
func hopDepths(t *testing.T, ctx context.Context, f *p2Fixture) []int {
	t.Helper()
	rows, err := f.pool.Query(ctx, `
		SELECT id, from_agent_id, to_agent_id, created_at, COALESCE(cause_hop_id, 0)
		FROM session_hop WHERE session_id = $1 ORDER BY id`, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var hops []router.Hop
	for rows.Next() {
		var h router.Hop
		var from *uuid.UUID
		if err := rows.Scan(&h.ID, &from, &h.ToAgent, &h.At, &h.CauseID); err != nil {
			t.Fatal(err)
		}
		if from != nil {
			h.FromAgent = *from
		}
		hops = append(hops, h)
	}
	depths := make([]int, len(hops))
	for i, h := range hops {
		depths[i] = router.CheckLoopLimits(hops[:i], h, router.DefaultLimits(), h.At).ChainDepth
	}
	return depths
}

func maxInt(xs []int) int {
	m := 0
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

// (1) The F1 sequence, hop for hop as Hermes measured it (PR #213 review §3):
// human → Lead, delegate ×3, join, mention, re-entry report, delegate, join —
// nine hops after one human message. Default limits (8/60/5), and the
// session must still be active at the end: the chain is never deeper than 2.
//
// Regression: the old count (hops since the last human hop) makes the ninth
// hop depth 9 and pauses the session on the last join.
func TestS78F1SequenceStaysUnderChainDepth(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 기능 하나 만들어줘"})
	lead := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))

	// 위임 3: three siblings, one cause.
	r := f.delegate(t, lead, f.rUUID)
	w := f.delegate(t, lead, f.wUUID)
	qa := f.delegate(t, lead, f.qaUUID)
	// 합류 1: the group ends, Lead is woken once.
	f.done(t, r)
	f.done(t, w)
	f.done(t, qa)
	lead = f.run(t, f.queuedTask(t, f.lead))
	// 멘션 1: Lead asks the reviewer.
	review := f.run(t, f.mention(t, f.leadUUID, lead, "QA", f.qaUUID))
	// 재진입 1: the reviewer's lane ends → "요청하신 작업이 끝났습니다" wakes Lead.
	f.done(t, review)
	lead = f.run(t, f.queuedTask(t, f.lead))
	// 위임 1 + 합류 1.
	r2 := f.delegate(t, lead, f.rUUID)
	f.done(t, r2)
	lead = f.queuedTask(t, f.lead)

	st, reason, detail := f.sessionPause(t)
	if st != "active" {
		t.Fatalf("session = %s/%s %+v after the F1 sequence, want active — a normal delegation round must not trip max_chain_depth 8", st, reason, detail)
	}
	depths := hopDepths(t, ctx, f.p2Fixture)
	// session start (human), Director's mention (human), delegate ×3, join,
	// mention, re-entry, delegate, join
	want := []int{1, 1, 2, 2, 2, 1, 2, 1, 2, 1}
	if len(depths) != len(want) {
		t.Fatalf("hops = %d %v, want %d %v", len(depths), depths, len(want), want)
	}
	for i := range want {
		if depths[i] != want[i] {
			t.Errorf("hop %d depth = %d, want %d (all: %v)", i+1, depths[i], want[i], depths)
		}
	}
	if m := maxInt(depths); m > 3 {
		t.Errorf("max chain depth = %d, want ≤ 3", m)
	}
	var allowed int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session_hop WHERE session_id = $1 AND allowed`, f.sessionID).Scan(&allowed); err != nil {
		t.Fatal(err)
	}
	if allowed != 10 {
		t.Errorf("allowed hops = %d, want 10 — every hop of the sequence lands", allowed)
	}
	_ = lead
}

// (2) The ring the golden E4-01 describes, through the service: Dir → Lead →
// R → W → QA → Lead → R → W → QA → Lead. Each mention is written from the task
// the previous hop created, so each hop's cause is the hop before it and the
// depth climbs by one — the ninth hop is depth 9 and pauses the session with
// limit = chain_depth. The causal count does not loosen a real chain.
func TestS78RingStillTripsChainDepth(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	task := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))
	type ag struct {
		name string
		id   uuid.UUID
	}
	ring := []ag{{"Lead", f.leadUUID}, {"R", f.rUUID}, {"W", f.wUUID}, {"QA", f.qaUUID}}
	// Hops 2..8 pass (depth 2..8).
	for i := 1; i < 8; i++ {
		from, to := ring[(i-1)%4], ring[i%4]
		task = f.run(t, f.mention(t, from.id, task, to.name, to.id))
	}
	if st, _, _ := f.sessionPause(t); st != "active" {
		t.Fatalf("session paused before hop 9 — depth 8 is within max_chain_depth 8")
	}
	// Hop 9: QA → Lead, depth 9.
	f.fake.Advance(time.Second)
	out, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID),
		router.Author{Type: "agent", AgentID: &f.qaUUID, TaskID: &task, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " 다시"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Triggers) != 0 || len(out.Warnings) != 1 || out.Warnings[0].Code != "loop_limit" {
		t.Fatalf("hop 9: triggers %d warnings %+v, want no task and a loop_limit warning (E4-01)", len(out.Triggers), out.Warnings)
	}
	st, reason, detail := f.sessionPause(t)
	if st != "paused" || reason != "loop" || detail == nil || detail.Loop == nil || detail.Loop.Limit == nil || string(*detail.Loop.Limit) != "chain_depth" {
		t.Fatalf("session = %s/%s %+v, want paused/loop chain_depth", st, reason, detail)
	}
	if detail.Loop.Count == nil || *detail.Loop.Count != 9 {
		t.Errorf("loop.count = %v, want 9", detail.Loop.Count)
	}
	depths := hopDepths(t, ctx, f.p2Fixture)
	// session start (human), then the ring's nine.
	want := []int{1, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	for i := range want {
		if i >= len(depths) || depths[i] != want[i] {
			t.Fatalf("ring depths = %v, want %v", depths, want)
		}
	}
}

// (3) S-76's delegate ↔ join cycle at the DEFAULT limits — no raised
// max_chain_depth (TestS76DelegateJoinCycleStopsAtPairRoundtrips used to
// need 100 because the old count reached 8 at the fourth join). The cycle
// is Lead → R, R → Lead, … at depths 2, 1, 2, 1: chain_depth never speaks,
// and max_pair_roundtrips 5 stops the sixth delegation.
func TestS78DelegateJoinCycleStopsAtPairRoundtripsUnderDefaults(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 체인을 확인해줘"})
	lead := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))

	const maxCycles = 20
	delegations := 0
	var tripped error
	for i := 0; i < maxCycles; i++ {
		f.fake.Advance(time.Second)
		res, err := f.srv.Router.Delegate(ctx, lead, router.DelegateInput{AgentID: f.rUUID, Brief: "체인 확인"})
		if err != nil {
			tripped = err
			break
		}
		delegations++
		f.done(t, mustUUID(t, res.Task.Id.String()))
		if st, _, _ := f.sessionPause(t); st == "paused" {
			break
		}
		lead = f.run(t, f.queuedTask(t, f.lead))
	}
	if tripped == nil {
		t.Fatalf("S-76: %d delegate↔join rounds and the limiter never spoke", delegations)
	}
	if delegations != 5 {
		t.Errorf("delegations before the trip = %d, want 5 (max_pair_roundtrips)", delegations)
	}
	var p *apperr.Problem
	if !errors.As(tripped, &p) || p.Status != 409 || p.Code != "loop_limit" {
		t.Fatalf("Delegate returned %v, want 409 loop_limit", tripped)
	}
	st, reason, detail := f.sessionPause(t)
	if st != "paused" || reason != "loop" || detail == nil || detail.Loop == nil || detail.Loop.Limit == nil || string(*detail.Loop.Limit) != "pair_roundtrips" {
		t.Fatalf("session = %s/%s %+v, want paused/loop pair_roundtrips — chain_depth must not be the limit that describes a two-agent cycle", st, reason, detail)
	}
	depths := hopDepths(t, ctx, f.p2Fixture)
	if m := maxInt(depths); m != 2 {
		t.Errorf("max chain depth over the cycle = %d, want 2 (delegate 2, join back at Lead's own 1): %v", m, depths)
	}
}

// (5) A HITL answer is a person intervening (FR-3.5 "사람이 개입하면 0"): the
// task that asked at depth 4 resumes on the Director's word, and what it does
// next is one hop below the human — depth 2 — not 5. The answer records a
// human hop toward the task's agent (handlers_hitl → router.RecordHumanHop),
// which also ends any pair run the agent was in.
func TestS78HitlAnswerRestartsTheChain(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	lead := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))
	r := f.run(t, f.mention(t, f.leadUUID, lead, "R", f.rUUID))
	w := f.run(t, f.mention(t, f.rUUID, r, "W", f.wUUID))
	qa := f.run(t, f.mention(t, f.wUUID, w, "QA", f.qaUUID)) // depth 4

	var laneID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT lane_id FROM task WHERE id = $1`, qa).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	tok, err := f.srv.Tokens.Issue(ctx, f.pool, tokens.Scope{
		TaskID: qa, Attempt: 1, LaneID: laneID, SessionID: mustUUID(t, f.sessionID), AgentID: f.qaUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := f.hitlOn(t, tok, map[string]any{"type": "question", "question": "기준은?", "proposed_default": "PRD"}, 201)
	hitlID := str(out["hitl_request"].(map[string]any), "id")
	f.fake.Advance(time.Second)
	if _, err := f.srv.Tasks.Finish(ctx, qa, 1, contracts.Finish{Outcome: "completed", StopReason: "end_turn"}); err != nil {
		t.Fatal(err)
	}
	f.fake.Advance(time.Second)
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response", map[string]any{"answer": "PRD 기준"}, "Idempotency-Key", uuid.NewString())

	// QA continues (attempt 2) and asks Lead.
	f.run(t, qa)
	f.fake.Advance(time.Second)
	res, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID),
		router.Author{Type: "agent", AgentID: &f.qaUUID, TaskID: &qa, Attempt: 2},
		gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " 답 받았어요"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Triggers) != 1 {
		t.Fatalf("triggers = %d, want 1", len(res.Triggers))
	}
	depths := hopDepths(t, ctx, f.p2Fixture)
	// start, Dir→Lead, Lead→R, R→W, W→QA, [HITL answer → QA], QA→Lead
	want := []int{1, 1, 2, 3, 4, 1, 2}
	if len(depths) != len(want) {
		t.Fatalf("hops = %v, want %v — the HITL answer must be a human hop", depths, want)
	}
	for i := range want {
		if depths[i] != want[i] {
			t.Errorf("hop %d depth = %d, want %d (all %v)", i+1, depths[i], want[i], depths)
		}
	}
	var humanHops int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session_hop WHERE session_id = $1 AND from_agent_id IS NULL`, f.sessionID).Scan(&humanHops); err != nil {
		t.Fatal(err)
	}
	if humanHops != 3 {
		t.Errorf("human hops = %d, want 3 (session start, the Director's mention, the answer)", humanHops)
	}
}
