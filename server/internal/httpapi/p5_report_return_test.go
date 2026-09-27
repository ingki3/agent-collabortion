// T-LOOP (PRD v0.19.12 FR-3.5 「보고가 요청자에게 돌아오면 그 바퀴는 끝난다」):
// the live game-making room (2026-09-27 22:55) paused on max_chain_depth 8 in
// ordinary Lead-hub work — Lead asks a teammate, the teammate reports back
// with a mention, Lead asks the next one, … Every mention report was one
// deeper than the request it answered, so each round added 2.
//
// Now a report (speech = report) whose responds_to was written by the agent it
// wakes takes the request-writing task's own cause (router.returningReport),
// the same way join/re-entry wakes already did: the requester wakes at the
// depth it asked from. Depth counts only requests that beget requests; the
// pair and hourly limits are untouched.
package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// postFrom posts `content` from an agent's task and returns the triggered
// tasks by agent.
func (f *chainFixture) postFrom(t *testing.T, from, task uuid.UUID, content string) (*gen.MessagePostResult, map[uuid.UUID]uuid.UUID) {
	t.Helper()
	f.fake.Advance(time.Second)
	out, err := f.srv.Router.Post(t.Context(), mustUUID(t, f.sessionID),
		router.Author{Type: "agent", AgentID: &from, TaskID: &task, Attempt: 1},
		gen.MessageCreate{Content: content})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	by := map[uuid.UUID]uuid.UUID{}
	for _, tr := range out.Triggers {
		by[uuid.UUID(tr.AgentId)] = uuid.UUID(tr.TaskId)
	}
	return out, by
}

// speechOf is the stored speech of a posted message and its responds_to.
func (f *chainFixture) speechOf(t *testing.T, id uuid.UUID) (string, *uuid.UUID) {
	t.Helper()
	var sp string
	var resp *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT COALESCE(speech, ''), responds_to_message_id FROM message WHERE id = $1`, id).Scan(&sp, &resp); err != nil {
		t.Fatal(err)
	}
	return sp, resp
}

// (1) Lead hub, 12 rounds: Lead asks R or W (alternating), the teammate
// reports back to Lead with a mention. Depths oscillate 1 ↔ 2 and the room
// stays active under the default limits. Old count: +2 per round → hop 9
// (round 4) is depth 9 and pauses on chain_depth.
func TestLoopReportReturnLeadHubStaysShallow(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 게임 만들어줘"})
	lead := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))

	type ag struct {
		name string
		id   uuid.UUID
	}
	team := []ag{{"R", f.rUUID}, {"W", f.wUUID}}
	const rounds = 12
	for i := 0; i < rounds; i++ {
		m := team[i%2]
		// Lead → teammate: a request.
		_, by := f.postFrom(t, f.leadUUID, lead, router.MentionLink(m.name, m.id)+" 이거 해줘")
		mate, ok := by[m.id]
		if !ok {
			st, reason, d := f.sessionPause(t)
			t.Fatalf("round %d: request to %s did not land (room %s/%s %+v)", i+1, m.name, st, reason, d)
		}
		f.run(t, mate)
		// teammate → Lead: the report comes home.
		out, by := f.postFrom(t, m.id, mate, router.MentionLink("Lead", f.leadUUID)+" 끝났습니다")
		if sp, resp := f.speechOf(t, uuid.UUID(out.Message.Id)); sp != "report" || resp == nil {
			t.Fatalf("round %d: teammate's reply speech = %q responds_to=%v, want report with responds_to", i+1, sp, resp)
		}
		next, ok := by[f.leadUUID]
		if !ok {
			st, reason, d := f.sessionPause(t)
			t.Fatalf("round %d: report to Lead did not land (room %s/%s %+v)", i+1, st, reason, d)
		}
		lead = f.run(t, next)
	}
	if st, reason, d := f.sessionPause(t); st != "active" {
		t.Fatalf("room = %s/%s %+v after %d hub rounds, want active", st, reason, d, rounds)
	}
	depths := hopDepths(t, ctx, f.p2Fixture)
	// session start, Director → Lead, then (request 2, report 1) × 12.
	want := []int{1, 1}
	for i := 0; i < rounds; i++ {
		want = append(want, 2, 1)
	}
	if len(depths) != len(want) {
		t.Fatalf("hops = %d %v, want %d", len(depths), depths, len(want))
	}
	for i := range want {
		if depths[i] != want[i] {
			t.Fatalf("hop %d depth = %d, want %d (all %v)", i+1, depths[i], want[i], depths)
		}
	}
}

// (2) The report returns only to the agent that WROTE the request. R's report
// to Lead also carries a body chip for W: W is woken by the chip, is not the
// responds_to author, and so takes R's own depth + 1 — no return. Lead, in
// the same message, returns to its own depth.
func TestLoopReportReturnOnlyForTheRequester(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	lead := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))
	r := f.run(t, f.mention(t, f.leadUUID, lead, "R", f.rUUID)) // depth 2

	out, by := f.postFrom(t, f.rUUID, r,
		router.MentionLink("Lead", f.leadUUID)+" 끝났습니다, "+router.MentionLink("W", f.wUUID)+" 참고")
	if sp, _ := f.speechOf(t, uuid.UUID(out.Message.Id)); sp != "report" {
		t.Fatalf("speech = %q, want report", sp)
	}
	if _, ok := by[f.leadUUID]; !ok {
		t.Fatalf("Lead not woken: %+v", out.Triggers)
	}
	if _, ok := by[f.wUUID]; !ok {
		t.Fatalf("W not woken by the chip: %+v", out.Triggers)
	}
	depths := hopDepths(t, ctx, f.p2Fixture)
	// start, Dir→Lead, Lead→R, then R's message: →Lead (return: 1), →W (chip: 3).
	var toLead, toW int
	rows, err := f.pool.Query(ctx, `SELECT to_agent_id FROM session_hop WHERE session_id = $1 ORDER BY id`, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var tos []uuid.UUID
	for rows.Next() {
		var to uuid.UUID
		if err := rows.Scan(&to); err != nil {
			t.Fatal(err)
		}
		tos = append(tos, to)
	}
	rows.Close()
	if len(tos) != 5 || len(depths) != 5 {
		t.Fatalf("hops = %d %v, want 5", len(depths), depths)
	}
	for i := 3; i < 5; i++ {
		switch tos[i] {
		case f.leadUUID:
			toLead = depths[i]
		case f.wUUID:
			toW = depths[i]
		}
	}
	if toLead != 1 {
		t.Errorf("report → Lead depth = %d, want 1 (the requester returns to the depth it asked from)", toLead)
	}
	if toW != 3 {
		t.Errorf("chip → W depth = %d, want 3 (W did not write the request — no return)", toW)
	}
}

// (3) Two agents ping-pong request/report: depth stays 1/2 — so chain_depth
// is silent — and max_pair_roundtrips 5 stops the 11th pair hop.
func TestLoopReportReturnPingPongStopsAtPairRoundtrips(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	lead := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))
	pairHops := 0
	for i := 0; i < 20; i++ {
		out, by := f.postFrom(t, f.leadUUID, lead, router.MentionLink("R", f.rUUID)+" 한 번 더")
		pairHops++
		r, ok := by[f.rUUID]
		if !ok {
			_ = out
			break
		}
		f.run(t, r)
		_, by = f.postFrom(t, f.rUUID, r, router.MentionLink("Lead", f.leadUUID)+" 했습니다")
		pairHops++
		next, ok := by[f.leadUUID]
		if !ok {
			break
		}
		lead = f.run(t, next)
	}
	st, reason, d := f.sessionPause(t)
	if st != "paused" || reason != "loop" || d == nil || d.Loop == nil || d.Loop.Limit == nil || string(*d.Loop.Limit) != "pair_roundtrips" {
		t.Fatalf("room = %s/%s %+v, want paused/loop pair_roundtrips", st, reason, d)
	}
	if pairHops != 11 {
		t.Errorf("pair hops until the trip = %d, want 11 (the 6th roundtrip)", pairHops)
	}
	if m := maxInt(hopDepths(t, ctx, f.p2Fixture)); m != 2 {
		t.Errorf("max depth over the ping-pong = %d, want 2", m)
	}
}

// (4) Report hops still count toward max_hops_per_hour: with the limit at 5
// the hub trips on its 6th agent hop, which is a REQUEST after two full
// rounds plus one request and one report (req, rep, req, rep, req = 5).
func TestLoopReportReturnReportsCountPerHour(t *testing.T) {
	f := newChainFixture(t)
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_chain_depth": 8, "max_hops_per_hour": 5, "max_pair_roundtrips": 5},
	})
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	lead := f.run(t, mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id")))
	team := []struct {
		name string
		id   uuid.UUID
	}{{"R", f.rUUID}, {"W", f.wUUID}, {"QA", f.qaUUID}}
	agentHops := 0
	for i := 0; i < 5; i++ {
		m := team[i%3]
		_, by := f.postFrom(t, f.leadUUID, lead, router.MentionLink(m.name, m.id)+" 부탁")
		agentHops++
		mate, ok := by[m.id]
		if !ok {
			break
		}
		f.run(t, mate)
		_, by = f.postFrom(t, m.id, mate, router.MentionLink("Lead", f.leadUUID)+" 완료")
		agentHops++
		next, ok := by[f.leadUUID]
		if !ok {
			break
		}
		lead = f.run(t, next)
	}
	st, reason, d := f.sessionPause(t)
	if st != "paused" || reason != "loop" || d == nil || d.Loop == nil || d.Loop.Limit == nil || string(*d.Loop.Limit) != "hops_per_hour" {
		t.Fatalf("room = %s/%s %+v, want paused/loop hops_per_hour", st, reason, d)
	}
	if agentHops != 6 {
		t.Errorf("agent hops until the trip = %d, want 6 — report hops count toward the hour", agentHops)
	}
}
