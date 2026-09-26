package httpapi

// T-R3b: brief [4] 방 맥락 and the turn prompt's three history bundles
// (harness §10 v0.9.0, PRD FR-4.1 v0.19), read off a real claim — the bundle
// is the only place both halves meet, so the assertions open it.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// claimBundle claims and returns the bundle of one task, then settles the task
// so the same agent can be claimed again (one running task per agent).
func (f *p2Fixture) claimBundle(t *testing.T, taskID uuid.UUID) *contracts.TaskBundle {
	t.Helper()
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	bundles, err := f.srv.Queue.Claim(t.Context(), runtimeID.String(), 5, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	var got *contracts.TaskBundle
	for i := range bundles {
		if bundles[i].Task.ID == taskID.String() {
			got = &bundles[i]
		}
	}
	if got == nil {
		t.Fatalf("the queue handed out no bundle for task %s (got %d)", taskID, len(bundles))
	}
	for _, b := range bundles {
		if _, err := f.pool.Exec(t.Context(), `UPDATE task SET status = 'completed' WHERE id = $1`, b.Task.ID); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

// mentionTask posts a mention of one agent (optionally under a mission) and
// returns the task it made.
func (f *p2Fixture) mentionTask(t *testing.T, agent uuid.UUID, name, workID string) uuid.UUID {
	t.Helper()
	body := map[string]any{"content": router.MentionLink(name, agent) + " 부탁합니다"}
	if workID != "" {
		body["work_id"] = workID
	}
	out := f.post(t, body)
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent.String() {
			return mustUUID(t, str(tr, "task_id"))
		}
	}
	t.Fatalf("no task for %s: %v", name, out["triggers"])
	return uuid.Nil
}

// section returns one "[n] …" section of the server's brief, header included.
func section(brief string, n int) string {
	i := strings.Index(brief, fmt.Sprintf("[%d] ", n))
	if i < 0 {
		return ""
	}
	rest := brief[i+4:]
	j := len(rest)
	for k := n + 1; k <= 8; k++ {
		if x := strings.Index(rest, fmt.Sprintf("\n[%d] ", k)); x >= 0 && x < j {
			j = x + 1
		}
	}
	return brief[i : i+4+j]
}

// stablePrefix is [1]~[5] (E12-11): everything before [6]/[7]/[8].
func stablePrefix(brief string) string {
	j := len(brief)
	for _, h := range []string{"\n[6] ", "\n[7] ", "\n[8] "} {
		if x := strings.Index(brief, h); x >= 0 && x < j {
			j = x + 1
		}
	}
	return brief[:j]
}

func legacyWork(t *testing.T, f *p2Fixture) string {
	t.Helper()
	var w string
	if err := f.pool.QueryRow(t.Context(), `SELECT legacy_work_id::text FROM room WHERE id = $1`, f.sessionID).Scan(&w); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestR3bBriefRoomContext is harness §10 v0.9.0 [4]: the room part always,
// the turn's own mission when it has one, byte-identical [1]~[5] within one
// mission even while its progress moves (Lead T-R3b 판정 1), a different [4]
// for another mission of the same room, and the room part only outside any.
func TestR3bBriefRoomContext(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	wA := legacyWork(t, f)
	if _, err := f.pool.Exec(ctx, `UPDATE room SET name = '결제 방', description = E'결제 흐름\n개편' WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE work SET acceptance_criteria = '{"테스트 통과"}' WHERE id = $1`, wA); err != nil {
		t.Fatal(err)
	}

	b1 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	four := section(b1.Brief.Text, 4)
	for _, want := range []string{
		"[4] Room\nRoom: 결제 방\nAbout: 결제 흐름 개편\nOwner: Dir\nIsolation: none\n",
		"\nMission this turn belongs to: S\nGoal: g\nAcceptance criteria:\n- 테스트 통과\n",
		"Completion condition (all of these):\n",
		"- the Director's approval\n",
		"Director: Dir\n",
	} {
		if !strings.Contains(four, want) {
			t.Errorf("[4] lacks %q:\n%s", want, four)
		}
	}
	if strings.Contains(b1.Brief.Text, "[4] Session") || strings.Contains(four, "[x]") || strings.Contains(four, "met") {
		t.Errorf("[4] still the session section, or carries progress (판정 1):\n%s", four)
	}
	if b1.Task.RoomID != f.sessionID || b1.Task.WorkID != wA {
		t.Errorf("bundle room/work = %q/%q, want %s/%s", b1.Task.RoomID, b1.Task.WorkID, f.sessionID, wA)
	}
	if !strings.Contains(b1.Prompt, fmt.Sprintf("<mission_progress work=%q met=0 total=2 satisfied=false>\n", wA)) {
		t.Errorf("turn prompt lacks the mission's progress:\n%s", b1.Prompt)
	}

	// Progress moves inside the mission: [1]~[5] must not.
	if _, err := f.pool.Exec(ctx, `UPDATE work SET completion_met = '{"artifact_submitted": true}' WHERE id = $1`, wA); err != nil {
		t.Fatal(err)
	}
	b2 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	if p1, p2 := stablePrefix(b1.Brief.Text), stablePrefix(b2.Brief.Text); p1 != p2 {
		t.Errorf("[1]~[5] changed between two turns of one mission (E12-11 v0.9.0):\n--- 1\n%s\n--- 2\n%s", p1, p2)
	}
	if !strings.Contains(b2.Prompt, "met=1 total=2") || !strings.Contains(b2.Prompt, "- [x] an artifact submitted") {
		t.Errorf("the second turn's progress did not move:\n%s", b2.Prompt)
	}

	// Another mission of the same room: [4] differs, the rest of the prefix does not.
	wB := str(f.api.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/works", map[string]any{"title": "둘째", "goal": "둘째 미션", "assignee_agent_id": f.r}), "id")
	b3 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wB))
	if b3.Task.WorkID != wB {
		t.Fatalf("task ran for %q, want mission %s", b3.Task.WorkID, wB)
	}
	if s := section(b3.Brief.Text, 4); !strings.Contains(s, "Mission this turn belongs to: 둘째\nGoal: 둘째 미션\n") || strings.Contains(s, "Goal: g\n") {
		t.Errorf("[4] of mission B names the wrong mission:\n%s", s)
	}
	for _, n := range []int{1, 2, 5} {
		if section(b3.Brief.Text, n) != section(b2.Brief.Text, n) {
			t.Errorf("[%d] differs between missions of one room — only [4] should", n)
		}
	}

	// Outside any mission: the room part only, no mission sections in the prompt.
	task := f.mentionTask(t, f.rUUID, "R", "")
	if _, err := f.pool.Exec(ctx, `UPDATE task SET work_id = NULL WHERE id = $1`, task); err != nil {
		t.Fatal(err)
	}
	b4 := f.claimBundle(t, task)
	s4 := section(b4.Brief.Text, 4)
	if s4 != "[4] Room\nRoom: 결제 방\nAbout: 결제 흐름 개편\nOwner: Dir\nIsolation: none\n\n" {
		t.Errorf("[4] outside a mission = %q, want the room part only", s4)
	}
	if b4.Task.WorkID != "" || strings.Contains(b4.Prompt, "<mission_") {
		t.Errorf("mission-less turn carries a mission: work=%q\n%s", b4.Task.WorkID, b4.Prompt)
	}
}

// TestR3bRoomHistoryThreeBundles is PRD FR-4.1 v0.19's three bundles: ① the
// latest 50 (unchanged <history>), ② the turn's mission messages ① dropped,
// ③ the decisions [7] did not hold + the latest 「여기까지 정리」, with the one
// truncation line at the head of the history (Lead T-R3b 판정 3·5). Messages
// from before the agent joined are there like any other (FR-2.2).
func TestR3bRoomHistoryThreeBundles(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	wA := legacyWork(t, f)
	early := t0.Add(-48 * time.Hour)
	ins := func(at time.Time, content string, work any, kind string, rng any) uuid.UUID {
		var id uuid.UUID
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO message (session_id, author_type, content, kind, summary_range, created_at, work_id)
			VALUES ($1, 'system', $2, $3::message_kind, $4, $5, $6) RETURNING id`,
			f.sessionID, content, kind, rng, at, work).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for i := 0; i < 4; i++ {
		ins(early.Add(time.Duration(i)*time.Minute), fmt.Sprintf("MISSION-OLD-%d", i), wA, "text", nil)
		ins(early.Add(time.Duration(i)*time.Minute+time.Second), fmt.Sprintf("ROOM-OLD-%d", i), nil, "text", nil)
	}
	sum := ins(early.Add(10*time.Minute), "**여기까지 정리** — 결론: B 안", nil, "summary", map[string]any{"message_ids": []string{}})
	for i := 0; i < 60; i++ {
		ins(early.Add(time.Hour+time.Duration(i)*time.Minute), fmt.Sprintf("CHATTER-%d", i), nil, "text", nil)
	}
	for i := 0; i < 23; i++ {
		if _, err := f.pool.Exec(ctx, `INSERT INTO decision (session_id, summary, source, created_at, work_id) VALUES ($1, $2, 'agent', $3, $4)`,
			f.sessionID, fmt.Sprintf("DECISION-%02d", i), early.Add(time.Duration(i)*time.Minute), wA); err != nil {
			t.Fatal(err)
		}
	}
	// W "joins" after all of it.
	if _, err := f.pool.Exec(ctx, `UPDATE room_participant SET joined_at = $2 WHERE room_id = $1 AND agent_id = $3`, f.sessionID, t0.Add(time.Hour), f.wUUID); err != nil {
		t.Fatal(err)
	}
	f.fake.Advance(2 * time.Hour)
	b := f.claimBundle(t, f.mentionTask(t, f.wUUID, "W", wA))
	p := b.Prompt

	hist := strings.Index(p, "<history ")
	note := strings.Index(p, "Older room messages not shown below: ")
	if note < 0 || note > hist {
		t.Fatalf("no truncation line at the head of <history>:\n%s", p)
	}
	if !strings.Contains(p, "This mission's 4 among them are in <mission_messages>.") {
		t.Errorf("truncation line does not count the mission's older messages:\n%s", p[note:hist])
	}
	mm := between(p, "<mission_messages ", "</mission_messages>")
	for i := 0; i < 4; i++ {
		if !strings.Contains(mm, fmt.Sprintf("MISSION-OLD-%d", i)) {
			t.Errorf("② lacks MISSION-OLD-%d (joined later or not, FR-2.2)", i)
		}
		if strings.Contains(mm, fmt.Sprintf("ROOM-OLD-%d", i)) {
			t.Errorf("② carries a message of no mission: ROOM-OLD-%d", i)
		}
	}
	if strings.Contains(mm, "CHATTER-59") {
		t.Error("② repeats a line ① already has")
	}
	if !strings.Contains(p, "CHATTER-59") || strings.Contains(p, "CHATTER-0\n") || strings.Contains(p, "ROOM-OLD-0") {
		t.Errorf("① is not the latest 50 room messages")
	}

	rd := between(p, "<room_decisions ", "</room_decisions>")
	if !strings.HasPrefix(rd, "count=3 ") || !strings.Contains(rd, "DECISION-00") || !strings.Contains(rd, "DECISION-02") || strings.Contains(rd, "DECISION-03") {
		t.Errorf("③ decisions = %q, want the 3 older than [7]'s 20", rd)
	}
	seven := section(b.Brief.Text, 7)
	if !strings.Contains(seven, "DECISION-03") || !strings.Contains(seven, "DECISION-22") || strings.Contains(seven, "DECISION-02") {
		t.Errorf("[7] is not the newest 20:\n%s", seven)
	}
	if rs := between(p, "<room_summary ", "</room_summary>"); !strings.Contains(rs, sum.String()) || !strings.Contains(rs, "결론: B 안") {
		t.Errorf("③ lacks the latest 「여기까지 정리」: %q", rs)
	}
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], close)
	if j < 0 {
		return s[i+len(open):]
	}
	return s[i+len(open) : i+j]
}

// TestR3cBriefMissionOfAnotherRoom is #323 NN1: task.work_id is the server's
// own write and always names a mission of the task's room, but a row edited by
// hand (or a future bug) that points it at another room's mission must not
// put that mission's title or goal in this room's [4] — the room part only,
// like a turn outside any mission.
func TestR3cBriefMissionOfAnotherRoom(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	other := str(f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/rooms", map[string]any{"name": "남의 방"}), "id")
	foreign := str(f.api.must(201, "POST", f.p+"/rooms/"+other+"/works", map[string]any{"title": "남의미션제목", "goal": "남의미션목표"}), "id")

	task := f.mentionTask(t, f.rUUID, "R", legacyWork(t, f))
	if _, err := f.pool.Exec(ctx, `UPDATE task SET work_id = $2 WHERE id = $1`, task, foreign); err != nil {
		t.Fatal(err)
	}
	b := f.claimBundle(t, task)
	four := section(b.Brief.Text, 4)
	if !strings.HasPrefix(four, "[4] Room\nRoom: ") {
		t.Fatalf("[4] lost its room part:\n%s", four)
	}
	for _, leak := range []string{"남의미션제목", "남의미션목표", "Mission this turn belongs to"} {
		if strings.Contains(b.Brief.Text, leak) || strings.Contains(b.Prompt, leak) {
			t.Errorf("another room's mission leaked into the turn (%q):\n%s\n---\n%s", leak, four, b.Prompt)
		}
	}
	if strings.Contains(b.Prompt, "<mission_") {
		t.Errorf("turn prompt carries mission sections for a mission of another room:\n%s", b.Prompt)
	}
}

// TestR3cDecisionBoundaryTies is #323 NN2: [7] (newest 20) and ③
// <room_decisions> (the rest) split one ordering. Decisions sharing a
// created_at across the 20 boundary must land in exactly one of the two, and
// which one is fixed by id DESC — without a named tie-break both queries
// happen to agree on heap order, which is luck, not a rule.
func TestR3cDecisionBoundaryTies(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	wA := legacyWork(t, f)
	at := t0.Add(-time.Hour)
	const n = 25
	for i := 0; i < n; i++ {
		if _, err := f.pool.Exec(ctx, `INSERT INTO decision (session_id, summary, source, created_at, work_id) VALUES ($1, $2, 'agent', $3, $4)`,
			f.sessionID, fmt.Sprintf("TIE-%02d", i), at, wA); err != nil {
			t.Fatal(err)
		}
	}
	b := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	seven := section(b.Brief.Text, 7)
	rd := between(b.Prompt, "<room_decisions ", "</room_decisions>")
	both, missing := 0, 0
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("TIE-%02d", i)
		in7, in3 := strings.Contains(seven, name), strings.Contains(rd, name)
		switch {
		case in7 && in3:
			both++
		case !in7 && !in3:
			missing++
		}
	}
	var newest []string
	rows, err := f.pool.Query(ctx, `SELECT summary FROM decision WHERE session_id = $1 ORDER BY id DESC LIMIT 20`, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		newest = append(newest, s)
	}
	rows.Close()
	for _, s := range newest {
		if !strings.Contains(seven, s) {
			t.Errorf("[7] lacks %s — the tie at the boundary is not broken by id DESC", s)
		}
	}
	if both != 0 || missing != 0 {
		t.Errorf("tied decisions across the [7]/③ boundary: %d in both, %d in neither (want 0/0)\n[7]:\n%s\n③:\n%s", both, missing, seven, rd)
	}
}

// rosterStatusBlock pulls the turn prompt's <roster_status> block, tags included.
func rosterStatusBlock(prompt string) string {
	i := strings.Index(prompt, "<roster_status>\n")
	j := strings.Index(prompt, "</roster_status>\n")
	if i < 0 || j < i {
		return ""
	}
	return prompt[i : j+len("</roster_status>\n")]
}

// TestRosterStatusInTurnPrompt is harness v0.9.2 (E12-11 복구): who is
// working moves between two turns of one mission, so it lives in the turn
// prompt's <roster_status> — [5] and the whole [1]~[5] stay byte-identical,
// with nothing masked out of the comparison.
func TestRosterStatusInTurnPrompt(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	wA := legacyWork(t, f)

	b1 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))

	// Between the two turns another agent (W) starts working.
	wTask := f.mentionTask(t, f.wUUID, "W", wA)
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'running', runtime_id = $2 WHERE id = $1`, wTask, runtimeID); err != nil {
		t.Fatal(err)
	}
	b2 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))

	if p1, p2 := stablePrefix(b1.Brief.Text), stablePrefix(b2.Brief.Text); p1 != p2 {
		t.Errorf("[1]~[5] changed when W started working (E12-11, v0.9.2):\n--- 1\n%s\n--- 2\n%s", p1, p2)
	}
	five := section(b2.Brief.Text, 5)
	if strings.Contains(five, "status") || strings.Contains(five, "working") || strings.Contains(five, "idle") {
		t.Errorf("[5] still carries the live status (v0.9.2 moves it to <roster_status>):\n%s", five)
	}
	for _, want := range []string{"- Lead — lead: d — mention: ", "- R (you) — researcher: d — mention: ", "- W — writer: d — mention: "} {
		if !strings.Contains(five, want) {
			t.Errorf("[5] lacks %q:\n%s", want, five)
		}
	}

	s1, s2 := rosterStatusBlock(b1.Prompt), rosterStatusBlock(b2.Prompt)
	if !strings.Contains(s1, "\n- W: idle\n") || !strings.Contains(s2, "\n- W: working\n") {
		t.Errorf("<roster_status> did not follow W idle → working:\n--- 1\n%s--- 2\n%s", s1, s2)
	}
	// One line per participant, in [5]'s order, the turn's own agent included.
	re := "<roster_status>\n- Lead: (working|idle)\n- R: working\n- W: (working|idle)\n</roster_status>\n"
	for i, s := range []string{s1, s2} {
		if !regexp.MustCompile("^" + re + "$").MatchString(s) {
			t.Errorf("turn %d <roster_status> = %q, want %s", i+1, s, re)
		}
	}
	// It sits after the mission's progress and before the trigger.
	if mp, rs, tr := strings.Index(b2.Prompt, "<mission_progress"), strings.Index(b2.Prompt, "<roster_status>"), strings.Index(b2.Prompt, "<trigger>"); !(mp >= 0 && mp < rs && rs < tr) {
		t.Errorf("<roster_status> out of place (mission_progress=%d roster_status=%d trigger=%d)", mp, rs, tr)
	}
}
