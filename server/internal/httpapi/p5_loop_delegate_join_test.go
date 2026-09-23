// S-76 (T-I5 PR #206 신규 결함 1, plan/P2_BACKLOG.md): a delegate ↔ join cycle
// ran past FR-3.5. `Delegate` recorded its hop but never asked the limiter,
// and `wake` (the join notice, the blocked question, the re-entry report)
// recorded nothing at all — so a delegator that re-delegates on every join
// notice was a loop with no mention in it, and the one path the limiter did
// not see was the one that needed it: 529 tasks in 70 seconds, session still
// `active` (e2e/p5/77_security.sh S1x).
//
// The regression injection is a COUNTER, not the loop itself: the cycle is
// driven up to `maxCycles` and the test asserts it was stopped well before
// that. Take `gateHop` out of Delegate and the loop runs to `maxCycles` with
// the session active — the shape of the original defect, bounded.
package httpapi

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// leadTaskOf returns the queued task the join notice woke Lead with, and marks
// it running so the NEXT wake makes a fresh task instead of coalescing onto it
// (FR-3.4). A delegator only ever re-delegates from the task the join woke —
// that is what makes each round a new join group (join_fired_at is per task).
func (f *p2Fixture) runLeadTask(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id FROM task WHERE session_id = $1 AND agent_id = $2 AND status = 'queued'
		ORDER BY created_at DESC LIMIT 1`, f.sessionID, f.lead).Scan(&id); err != nil {
		t.Fatalf("no queued Lead task — the join notice did not wake the delegator: %v", err)
	}
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE task SET status = 'running', dispatched_at = $2, started_at = $2, runtime_id = (SELECT id FROM runtime LIMIT 1)
		WHERE id = $1`, id, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *p2Fixture) sessionPause(t *testing.T) (status, reason string, detail *gen.PausedDetail) {
	t.Helper()
	var st string
	var rs *string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, paused_reason::text, paused_detail FROM work WHERE room_id = $1`, f.sessionID).
		Scan(&st, &rs, &detail); err != nil {
		t.Fatal(err)
	}
	return st, deref(rs), detail
}

func TestS76DelegateJoinCycleStopsAtPairRoundtrips(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	// Only the pair limit is under test, so the other two are out of the way.
	// (Chain depth used to trip first at the default 8 — a delegate and a
	// join were two depth each round; since S-78 the join returns Lead to its
	// own depth and the cycle runs at 2/1, see
	// TestS78DelegateJoinCycleStopsAtPairRoundtripsUnderDefaults.)
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_chain_depth": 100, "max_hops_per_hour": 100, "max_pair_roundtrips": 5},
	})
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 체인을 확인해줘"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'running', runtime_id = (SELECT id FROM runtime LIMIT 1) WHERE id = $1`, leadTask); err != nil {
		t.Fatal(err)
	}

	const maxCycles = 20 // the bounded stand-in for "infinite"
	delegations := 0
	var tripped error
	for i := 0; i < maxCycles; i++ {
		f.fake.Advance(time.Second)
		res, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "체인 확인"})
		if err != nil {
			tripped = err
			break
		}
		delegations++
		// R ends → the join fires → Lead is woken. That wake-up is the
		// second hop of the roundtrip.
		f.fake.Advance(time.Second)
		if _, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, res.Task.Id.String()), 1, "done", ""); err != nil {
			t.Fatal(err)
		}
		st, _, _ := f.sessionPause(t)
		if st == "paused" {
			break
		}
		leadTask = f.runLeadTask(t)
	}
	if tripped == nil {
		t.Fatalf("S-76: %d delegate↔join rounds and the limiter never spoke — this is the 529-task loop, bounded", delegations)
	}
	// max_pair_roundtrips=5: hops D→R, R→D, … — the 11th hop (the 6th
	// delegation) is roundtrip 6.
	if delegations != 5 {
		t.Errorf("delegations before the trip = %d, want 5 (max_pair_roundtrips)", delegations)
	}
	var p *apperr.Problem
	if !errors.As(tripped, &p) || p.Status != 409 || p.Code != "loop_limit" {
		t.Fatalf("Delegate returned %v, want 409 loop_limit — the agent must learn its delegation did not happen", tripped)
	}
	if p.Detail == "" || !containsHangul(p.Detail) {
		t.Errorf("Problem.detail = %q, want the §8.4 sentence", p.Detail)
	}

	st, reason, detail := f.sessionPause(t)
	if st != "paused" || reason != "loop" {
		t.Fatalf("session = %s/%s, want paused/loop", st, reason)
	}
	if detail == nil || detail.Loop == nil || detail.Loop.Limit == nil || string(*detail.Loop.Limit) != "pair_roundtrips" {
		t.Fatalf("paused_detail = %+v, want loop.limit = pair_roundtrips", detail)
	}
	if detail.Loop.Agents == nil || len(*detail.Loop.Agents) != 2 {
		t.Errorf("loop.agents = %v, want Lead and R", detail.Loop.Agents)
	}
	// No lane and no task for the refused delegation (E4-01: the message is
	// posted, the task is not).
	var rLanes, rTasks int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM lane WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r).Scan(&rLanes); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM task WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r).Scan(&rTasks); err != nil {
		t.Fatal(err)
	}
	if rLanes != 5 || rTasks != 5 {
		t.Errorf("R lanes/tasks = %d/%d, want 5/5 — the sixth delegation must create nothing", rLanes, rTasks)
	}
	var mentions int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND author_type = 'agent' AND author_id = $2`, f.sessionID, f.lead).Scan(&mentions); err != nil {
		t.Fatal(err)
	}
	if mentions != 6 {
		t.Errorf("Lead's delegation messages = %d, want 6 — the refused one is still in the timeline", mentions)
	}
	// Director notified: a system HITL with purpose loop, its card, an inbox item.
	var hitl, cards, inbox int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM hitl_request WHERE session_id = $1 AND source = 'system' AND purpose = 'loop'`, f.sessionID).Scan(&hitl); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'hitl'`, f.sessionID).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	// PRD v0.19 FR-3.5 · FR-8: the loop stops the ROOM, and its owner gets a
	// `room_paused` card (T-R1b1) — the one `session_paused` used to be.
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE session_id = $1 AND type = 'room_paused'`, f.sessionID).Scan(&inbox); err != nil {
		t.Fatal(err)
	}
	if hitl != 1 || cards != 1 || inbox != 1 {
		t.Errorf("hitl/cards/inbox = %d/%d/%d, want 1/1/1 (FR-3.5: paused(loop) + Director notice)", hitl, cards, inbox)
	}
	// The refused call is on the feed with the schema's reason slot (S-52).
	var rejected int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'status' AND verb = 'delegate'
		  AND outcome = 'rejected' AND payload->>'rejected_reason' = 'loop_limit'`, leadTask).Scan(&rejected); err != nil {
		t.Fatal(err)
	}
	if rejected != 1 {
		t.Errorf("rejected delegate events = %d, want 1", rejected)
	}
	// Hops: 5 delegations + 5 joins allowed, 1 refused.
	var allowed, refused int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE allowed), count(*) FILTER (WHERE NOT allowed)
		FROM session_hop WHERE session_id = $1 AND from_agent_id IS NOT NULL`, f.sessionID).Scan(&allowed, &refused); err != nil {
		t.Fatal(err)
	}
	if allowed != 10 || refused != 1 {
		t.Errorf("agent hops allowed/refused = %d/%d, want 10/1 — a join notice is a hop too", allowed, refused)
	}
}

// The wake-up itself can be the hop that trips: here the join notice is the
// third hop of an R ↔ Lead run with max_pair_roundtrips=1. The child's
// `status set done` still succeeds and the bundle is still posted — what the
// limiter withholds is the TASK, and the session pauses.
func TestS76JoinWakeIsGated(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_chain_depth": 100, "max_pair_roundtrips": 1},
	})
	// Human → R (reset), R → Lead (hop 1), Lead delegates R (hop 2), R done →
	// join R → Lead (hop 3 = roundtrip 2 > 1).
	post := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 시작"})
	rTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.fake.Advance(time.Second)
	out, err := f.srv.Router.Post(ctx, sessionID, router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " 맡아 주세요"})
	if err != nil {
		t.Fatal(err)
	}
	leadTask := mustUUID(t, out.Triggers[0].TaskId.String())
	f.fake.Advance(time.Second)
	res, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "다시"})
	if err != nil {
		t.Fatalf("second hop must pass (roundtrip 1): %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'running', runtime_id = (SELECT id FROM runtime LIMIT 1) WHERE id = $1`, leadTask); err != nil {
		t.Fatal(err)
	}
	f.fake.Advance(time.Second)
	if _, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, res.Task.Id.String()), 1, "done", ""); err != nil {
		t.Fatalf("the child's done must not fail — the limiter withholds the wake, not the status: %v", err)
	}
	if !joinFired(t, f, leadTask) {
		t.Fatal("the join still fires (once per group); only the task is withheld")
	}
	st, reason, detail := f.sessionPause(t)
	if st != "paused" || reason != "loop" || detail == nil || detail.Loop == nil || detail.Loop.Limit == nil || string(*detail.Loop.Limit) != "pair_roundtrips" {
		t.Fatalf("session = %s/%s %+v, want paused/loop pair_roundtrips", st, reason, detail)
	}
	var queued int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM task WHERE session_id = $1 AND agent_id = $2 AND status = 'queued'`, f.sessionID, f.lead).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Errorf("queued Lead tasks = %d, want 0 — the tripped wake creates no task", queued)
	}
	var bundles int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%위임한 작업이 모두 끝났습니다%'`, f.sessionID).Scan(&bundles); err != nil {
		t.Fatal(err)
	}
	if bundles != 1 {
		t.Errorf("join bundles = %d, want 1 — the notice is posted even when the wake is withheld", bundles)
	}
}

func containsHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}
