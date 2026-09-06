package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// These tests exist because the golden tables drive pure functions. A pure
// function can be right while the code the user actually reaches is wrong, so
// each row below re-asserts a golden row against the REAL path — the HTTP
// handler, the transaction, the sweep — and fails if that path is broken even
// when the table stays green.

// TestP2FallbackCancelledByPrimaryReply is E1-13 against the database. Breaking
// router.cancelFallbacksFor (e.g. `AND false` in its WHERE) must turn this red:
// the assignee would be woken five minutes later even though R answered.
func TestP2FallbackCancelledByPrimaryReply(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	// R speaks, the Director replies to it: rule 5 wakes R, rule 7 defers Lead.
	var agentMsg uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, created_at)
		VALUES ($1, 'agent', $2, '조사 결과입니다', 'text', $3) RETURNING id`,
		f.sessionID, f.r, t0).Scan(&agentMsg); err != nil {
		t.Fatal(err)
	}
	out := f.post(t, map[string]any{"content": "조금 더 좁혀주세요", "parent_id": agentMsg.String()})

	var rTask uuid.UUID
	for _, raw := range out["triggers"].([]any) {
		if str(raw.(map[string]any), "agent_id") == f.r {
			rTask = mustUUID(t, str(raw.(map[string]any), "task_id"))
		}
	}
	if rTask == uuid.Nil {
		t.Fatalf("rule 5 did not trigger R: %v", out["triggers"])
	}
	deferredID, status := fallbackTask(t, f)
	if status != "deferred" {
		t.Fatalf("rule 7 task = %s, want deferred", status)
	}

	// R answers inside the window. The deferred assignee task must die.
	f.fake.Advance(4 * time.Minute)
	if _, err := f.srv.Router.Post(ctx, sessionID,
		router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1},
		gen.MessageCreate{Content: "좁혔습니다"}); err != nil {
		t.Fatal(err)
	}
	if _, got := taskStatus(t, f, deferredID); got != "cancelled" {
		t.Fatalf("deferred assignee task = %s after the primary replied, want cancelled (E1-13)", got)
	}

	// And the sweep must not resurrect it once the window passes.
	f.fake.Advance(2 * time.Minute)
	if _, err := f.srv.Tasks.ExpireStale(ctx, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, got := taskStatus(t, f, deferredID); got != "cancelled" {
		t.Fatalf("cancelled fallback = %s after the sweep, want it to stay cancelled", got)
	}
}

// TestP2FallbackPromotedAtFiveMinutes is E1-14 against the database: nobody
// answered, so the deferred task becomes real work.
func TestP2FallbackPromotedAtFiveMinutes(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	var agentMsg uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, created_at)
		VALUES ($1, 'agent', $2, '조사 결과입니다', 'text', $3) RETURNING id`,
		f.sessionID, f.r, t0).Scan(&agentMsg); err != nil {
		t.Fatal(err)
	}
	f.post(t, map[string]any{"content": "조금 더 좁혀주세요", "parent_id": agentMsg.String()})
	deferredID, _ := fallbackTask(t, f)

	// Four minutes in it is still deferred — the window has not closed.
	f.fake.Advance(4 * time.Minute)
	if _, err := f.srv.Tasks.ExpireStale(ctx, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, got := taskStatus(t, f, deferredID); got != "deferred" {
		t.Fatalf("fallback = %s at 4m, want deferred — the window is 5 minutes", got)
	}

	f.fake.Advance(2 * time.Minute)
	if _, err := f.srv.Tasks.ExpireStale(ctx, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	notBefore, got := taskStatus(t, f, deferredID)
	if got != "queued" {
		t.Fatalf("fallback = %s past 5m with no reply, want queued (E1-14)", got)
	}
	if notBefore != nil {
		t.Errorf("not_before = %v after promotion, want cleared so the queue can hand it out", notBefore)
	}
}

func fallbackTask(t *testing.T, f *p2Fixture) (uuid.UUID, string) {
	t.Helper()
	var id uuid.UUID
	var status string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id, status::text FROM task
		WHERE session_id = $1 AND agent_id = $2 AND fallback_for_task_id IS NOT NULL`,
		f.sessionID, f.lead).Scan(&id, &status); err != nil {
		t.Fatalf("no rule 7 fallback task was scheduled: %v", err)
	}
	return id, status
}

func taskStatus(t *testing.T, f *p2Fixture, id uuid.UUID) (*time.Time, string) {
	t.Helper()
	var status string
	var notBefore *time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, not_before FROM task WHERE id = $1`, id).
		Scan(&status, &notBefore); err != nil {
		t.Fatal(err)
	}
	return notBefore, status
}

// TestP2CoalescingInTheDatabase is E2-09·10·11 against the real transaction:
// the merge unit is the lane, arrival order survives, and the list includes the
// message that created the task (FR-3.4 "도착 순서대로 인용").
func TestP2CoalescingInTheDatabase(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	mention := router.MentionLink("R", f.rUUID)

	first := f.post(t, map[string]any{"content": mention + " 하나"})
	tr := first["triggers"].([]any)[0].(map[string]any)
	laneA, taskA := str(tr, "lane_id"), mustUUID(t, str(tr, "task_id"))
	m1 := str(first["message"].(map[string]any), "id")

	second := f.post(t, map[string]any{"content": mention + " 둘"})
	tr2 := second["triggers"].([]any)[0].(map[string]any)
	if str(tr2, "lane_id") != laneA || tr2["coalesced"] != true {
		t.Fatalf("second message = %v, want a merge into the same lane", tr2)
	}
	m2 := str(second["message"].(map[string]any), "id")

	var ids []uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT coalesced_message_ids FROM task WHERE id = $1`, taskA).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0].String() != m1 || ids[1].String() != m2 {
		t.Fatalf("coalesced_message_ids = %v, want [%s %s] in arrival order (E2-10)", ids, m1, m2)
	}
	// One queued task on the lane, not two.
	var queued int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM task WHERE lane_id = $1 AND status = 'queued'`, mustUUID(t, laneA)).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("queued tasks on the lane = %d, want 1", queued)
	}

	// A second lane of the SAME agent keeps its own queue (E2-11).
	forked := f.post(t, map[string]any{"content": mention + " 다른 갈래", "new_lane": true})
	laneB := str(forked["triggers"].([]any)[0].(map[string]any), "lane_id")
	if laneB == laneA {
		t.Fatal("new_lane must fork")
	}
	var idsB []uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		SELECT coalesced_message_ids FROM task WHERE lane_id = $1 AND status = 'queued'`, mustUUID(t, laneB)).Scan(&idsB); err != nil {
		t.Fatal(err)
	}
	for _, id := range idsB {
		if id.String() == m1 || id.String() == m2 {
			t.Fatal("lane B's queue absorbed lane A's messages — that is agent-level merging")
		}
	}
}

// TestP2SweepInTheDatabase is E5-02·03 against ExpireStale itself.
func TestP2SweepInTheDatabase(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})

	var taskID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM task WHERE session_id = $1 AND status = 'queued' LIMIT 1`, f.sessionID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	// Pretend the daemon claimed it and then went quiet.
	if _, err := f.pool.Exec(ctx, `
		UPDATE task SET status = 'dispatched', dispatched_at = $2, runtime_id = (SELECT id FROM runtime LIMIT 1) WHERE id = $1`,
		taskID, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	f.fake.Advance(contracts.DispatchedTimeout + time.Second)
	if _, err := f.srv.Tasks.ExpireStale(ctx, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	var status string
	var kind *string
	var attempt int
	if err := f.pool.QueryRow(ctx, `SELECT status::text, failure_kind::text, attempt FROM task WHERE id = $1`, taskID).
		Scan(&status, &kind, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || kind == nil || *kind != "timeout" || attempt != 1 {
		t.Fatalf("unclaimed task = %s/%v attempt %d, want failed(timeout) at attempt 1 (E5-02, §4.1 v0.6)", status, kind, attempt)
	}
}

// TestP2SessionGateInTheDatabase is E5-04·05 against the claim query: a paused
// session hands out nothing, and resuming releases the queue in order.
func TestP2SessionGateInTheDatabase(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM runtime LIMIT 1`).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	// Three lanes with one queued task each.
	for _, ag := range []uuid.UUID{f.leadUUID, f.rUUID, f.wUUID} {
		f.post(t, map[string]any{"content": router.MentionLink("x", ag) + " 해줘"})
	}
	if _, err := f.pool.Exec(ctx, `UPDATE session SET limits = '{"max_parallel_lanes": 5}' WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE session SET status = 'paused', paused_reason = 'director' WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	got, err := f.srv.Queue.Claim(ctx, runtimeID.String(), 10, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("claimed %d from a paused session, want 0 (FR-2.3 C3′, E5-04)", len(got))
	}
	if _, err := f.pool.Exec(ctx, `UPDATE session SET status = 'active', paused_reason = NULL WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	got, err = f.srv.Queue.Claim(ctx, runtimeID.String(), 10, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("claimed %d after resume, want 3 in queue order (E5-05)", len(got))
	}
	var prev time.Time
	for _, b := range got {
		var created time.Time
		if err := f.pool.QueryRow(ctx, `SELECT created_at FROM task WHERE id = $1`, b.Task.ID).Scan(&created); err != nil {
			t.Fatal(err)
		}
		if created.Before(prev) {
			t.Fatal("dispatch order is not the queue order")
		}
		prev = created
	}
}

// TestP2BudgetPauseCancelsTheTurn is E5-07 / E6-10 against the database: a
// budget pause must stop the work, not merely stop new work.
func TestP2BudgetPauseCancelsTheTurn(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	var taskID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM task WHERE session_id = $1 AND status = 'queued' LIMIT 1`, f.sessionID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE task SET status = 'running', dispatched_at = $2, started_at = $2, runtime_id = (SELECT id FROM runtime LIMIT 1) WHERE id = $1`,
		taskID, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Sessions.ApplyCompletionEvent(ctx, mustUUID(t, f.sessionID), sessions.Event{Kind: "budget_exhausted"}); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := f.pool.QueryRow(ctx, `SELECT status::text, COALESCE(paused_reason::text, '') FROM task WHERE id = $1`, taskID).
		Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || reason != "budget" {
		t.Fatalf("running task = %s(%s) after a budget pause, want paused(budget) — §8.2.2 cancels the turn", status, reason)
	}
	var cancels int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM daemon_command WHERE task_id = $1 AND type = 'cancel'`, taskID).Scan(&cancels); err != nil {
		t.Fatal(err)
	}
	if cancels != 1 {
		t.Fatalf("cancel commands = %d, want 1 — the daemon has to be told to stop", cancels)
	}
	// A Director pause drains instead: same shape, opposite consequence.
	f2 := newP2Fixture(t)
	f2.post(t, map[string]any{"content": router.MentionLink("Lead", f2.leadUUID) + " 시작"})
	var t2 uuid.UUID
	if err := f2.pool.QueryRow(ctx, `SELECT id FROM task WHERE session_id = $1 AND status = 'queued' LIMIT 1`, f2.sessionID).Scan(&t2); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.pool.Exec(ctx, `UPDATE task SET status = 'running', runtime_id = (SELECT id FROM runtime LIMIT 1) WHERE id = $1`, t2); err != nil {
		t.Fatal(err)
	}
	tx, err := f2.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := f2.srv.Tasks.PauseSessionTasks(ctx, tx, mustUUID(t, f2.sessionID), "director", nil, f2.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var s2 string
	if err := f2.pool.QueryRow(ctx, `SELECT status::text FROM task WHERE id = $1`, t2).Scan(&s2); err != nil {
		t.Fatal(err)
	}
	if s2 != "running" {
		t.Fatalf("running task = %s after a Director pause, want running (drain, E5-06)", s2)
	}
}

// TestP2DerivedStatusInTheDatabase is E5-11…18 against the participant list the
// session screen actually reads.
func TestP2DerivedStatusInTheDatabase(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	statusOf := func(agentID string) string {
		sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
		for _, raw := range sess["participants"].([]any) {
			p := raw.(map[string]any)
			if str(p, "agent_id") == agentID {
				return str(p, "status")
			}
		}
		t.Fatalf("agent %s is not a participant", agentID)
		return ""
	}
	if got := statusOf(f.r); got != "idle" {
		t.Fatalf("fresh agent = %q, want idle", got)
	}

	// A running task reads as working (step 4).
	f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 해줘"})
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'running' WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got != "working" {
		t.Fatalf("agent with a running task = %q, want working", got)
	}

	// blocked lanes are excluded from the derivation (E5-13).
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'completed', finished_at = now() WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'blocked' WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got != "idle" {
		t.Fatalf("agent whose only lane is blocked = %q, want idle (E5-13)", got)
	}

	// An unrunnable failure reads as error (step 3); a retryable one does not
	// (E5-17, E5-18).
	if _, err := f.pool.Exec(ctx, `UPDATE task SET failure_kind = 'auth', status = 'failed' WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got != "error" {
		t.Fatalf("agent whose last task failed on auth = %q, want error (E5-17)", got)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE task SET failure_kind = 'network' WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got == "error" {
		t.Fatal("a retryable failure must not read as error (E5-18)")
	}

	// respond_to: nobody is the kill switch and outranks everything (step 1).
	if _, err := f.pool.Exec(ctx, `UPDATE agent SET respond_to = 'nobody' WHERE id = $1`, f.r); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got != "disabled" {
		t.Fatalf("respond_to nobody = %q, want disabled (E5-15 step 1)", got)
	}
}

// TestP2ServerEventsSurviveConcurrency is R3: two server-side notes for the
// same attempt must both land. `ON CONFLICT DO NOTHING` passed this test's
// predecessor by losing one silently.
func TestP2ServerEventsSurviveConcurrency(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	var taskID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM task WHERE session_id = $1 LIMIT 1`, f.sessionID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	const writers = 8
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if err := tasks.InsertServerEvent(ctx, tx, taskID, 1, "status", "note",
				"concurrent", "ok", map[string]any{"i": i}, t0); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit(ctx)
		}(i)
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent server event: %v", err)
		}
	}
	var n int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task_event WHERE task_id = $1 AND attempt = 1 AND verb = 'note'`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != writers {
		t.Fatalf("server events written = %d, want %d — a lost note is a feed that lies", n, writers)
	}
}

// TestP2ErrorStatusIsNotSticky pins FR-1.3 step 3's INPUT, which is where the
// ladder's order and the PRD's "error must not stay sticky" meet.
//
// The ladder is unchanged — step 3 outranks step 4, exactly as golden E5-15
// pins it. What changed is what LastFailureKind means: the agent's most recent
// task, not the last one that finished. Under the old definition the Director
// could fix the credentials, re-instruct, and watch a new task run while the
// participant list still said `error`.
func TestP2ErrorStatusIsNotSticky(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	statusOf := func(agentID string) string {
		sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
		for _, raw := range sess["participants"].([]any) {
			if p := raw.(map[string]any); str(p, "agent_id") == agentID {
				return str(p, "status")
			}
		}
		t.Fatalf("agent %s is not a participant", agentID)
		return ""
	}

	// An auth failure with nothing else running reads as error (E5-17).
	f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사"})
	var failed uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM task WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE task SET status = 'failed', failure_kind = 'auth', started_at = $2, finished_at = $2 WHERE id = $1`,
		failed, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got != "error" {
		t.Fatalf("agent whose most recent task failed on auth = %q, want error (E5-17)", got)
	}

	// The Director fixes the credentials and re-instructs. A NEW task starts.
	f.fake.Advance(time.Minute)
	f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 다시 해줘", "new_lane": true})
	var fresh uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		SELECT id FROM task WHERE session_id = $1 AND agent_id = $2 AND id <> $3`, f.sessionID, f.r, failed).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE task SET status = 'running', started_at = $2 WHERE id = $1`, fresh, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got != "working" {
		t.Fatalf("agent with a running task after an old auth failure = %q, want working — "+
			"`error` must not stay sticky once the agent is demonstrably running (FR-1.3)", got)
	}

	// The agent page (workspace-scoped, a different query) must agree.
	ag := f.api.must(200, "GET", f.p+"/agents/"+f.r, nil)
	if str(ag, "status") != "working" {
		t.Fatalf("agent page status = %q, want working — the two ladders must read the same input", str(ag, "status"))
	}

	// And if the new task fails on auth too, the error comes back.
	f.fake.Advance(time.Minute)
	if _, err := f.pool.Exec(ctx, `
		UPDATE task SET status = 'failed', failure_kind = 'auth', finished_at = $2 WHERE id = $1`, fresh, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(f.r); got != "error" {
		t.Fatalf("agent whose newest task also failed on auth = %q, want error again", got)
	}
}
