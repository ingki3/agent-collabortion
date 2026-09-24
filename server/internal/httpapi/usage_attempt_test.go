package httpapi

import (
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// T-S-usage — a new attempt must not erase what the attempt before it spent.
//
// task_usage was one row per task, and heartbeat and finish both overwrote it.
// The daemon reports the ATTEMPT's running total, so within an attempt that is
// right; but a retry · resume · cold start · profile fallback runs the same
// task again from zero, and attempt 2's total replaced attempt 1's. Measured on
// the live DB (2026-09-24): a Writer task spent $2.733, the room's budget
// paused it, the Director approved, attempt 2 finished at $1.318 — and the
// mission's cost went 11.477 → 10.062. Every budget ceiling then counted less
// than was actually spent.

// attemptNow is the attempt a heartbeat from the task's running turn carries
// in its path (/attempts/{n}/heartbeat) — RecordTurnUsage is keyed by it.
func attemptNow(t *testing.T, q *pgxpool.Pool, taskID uuid.UUID) int {
	t.Helper()
	var n int
	if err := q.QueryRow(t.Context(), `SELECT attempt FROM task WHERE id = $1`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// costWatch reads every surface a room's spend is quoted on and fails the test
// the moment any of them goes DOWN — the defect's signature.
type costWatch struct {
	f    *p2Fixture
	task uuid.UUID
	last map[string]float64
}

func (w *costWatch) read(t *testing.T) map[string]float64 {
	t.Helper()
	ctx := t.Context()
	sid := mustUUID(t, w.f.sessionID)
	got := map[string]float64{}
	spent, err := sessions.SpentUSD(ctx, w.f.pool, sid)
	if err != nil {
		t.Fatal(err)
	}
	got["room spent (budget)"] = spent
	var live, stored float64
	if err := w.f.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT sum(u.cost_usd) FROM task_usage u JOIN task t ON t.id = u.task_id WHERE t.session_id = $1), 0)::float8,
		       COALESCE((SELECT sum(cost_usd) FROM work WHERE room_id = $1), 0)::float8`, sid).Scan(&live, &stored); err != nil {
		t.Fatal(err)
	}
	got["room live sum"], got["work.cost_usd roll-up"] = live, stored
	u, err := tasks.GetUsage(ctx, w.f.pool, w.task)
	if err != nil {
		t.Fatal(err)
	}
	if u != nil {
		got["task usage (getTask)"] = u.CostUSD
	}
	rows, err := w.f.srv.usageRows(ctx, "t.session_id = $1", sid)
	if err != nil {
		t.Fatal(err)
	}
	var report float64
	for _, r := range rows {
		report += r.CostUSD
	}
	got["cost report"] = report
	return got
}

// step records a reading after `what` and refuses any decrease.
func (w *costWatch) step(t *testing.T, what string) map[string]float64 {
	t.Helper()
	got := w.read(t)
	for k, v := range got {
		if prev, ok := w.last[k]; ok && v < prev-1e-9 {
			t.Fatalf("%s: %s went DOWN %.4f → %.4f — a new attempt erased what the previous one spent (T-S-usage)", what, k, prev, v)
		}
	}
	w.last = got
	return got
}

func usdEq(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// heartbeat is RecordTurnUsage + enforcement, in the order daemonHeartbeat runs them.
func (f *p2Fixture) heartbeat(t *testing.T, taskID uuid.UUID, usd float64) {
	t.Helper()
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), taskID, attemptNow(t, f.pool, taskID), contracts.Usage{
		InputTokens: 100, OutputTokens: 100, CostUSD: usd,
	}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.enforceBudgetFor(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
}

func (f *p2Fixture) finishWith(t *testing.T, taskID uuid.UUID, usd float64, resume string) {
	t.Helper()
	if _, err := f.srv.Tasks.Finish(t.Context(), taskID, attemptNow(t, f.pool, taskID), contracts.Finish{
		Outcome: "completed", StopReason: "end_turn", ResumeOutcome: resume,
		Usage: contracts.Usage{InputTokens: 200, OutputTokens: 200, CostUSD: usd},
	}); err != nil {
		t.Fatal(err)
	}
}

// checkFinal is the DoD: $2.7 + $1.3 = $4.0 everywhere, and each attempt kept
// its own row.
func (w *costWatch) checkFinal(t *testing.T, got map[string]float64) {
	t.Helper()
	for k, v := range got {
		if !usdEq(v, 4.0) {
			t.Fatalf("%s = %.4f, want 4.0 — attempt 1's $2.7 plus attempt 2's $1.3 (all: %v)", k, v, got)
		}
	}
	var n int
	if err := w.f.pool.QueryRow(t.Context(), `SELECT count(*) FROM task_usage WHERE task_id = $1`, w.task).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("task_usage rows for the task = %d, want 2 (one per attempt)", n)
	}
	// by_task counts tasks, not attempts.
	rows, err := w.f.srv.usageRows(t.Context(), "t.session_id = $1", mustUUID(t, w.f.sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("cost report rows = %d, want 1 — a task with two attempts is still one task", len(rows))
	}
}

// TestUsageSurvivesBudgetResume is the measured incident: attempt 1 spends
// $2.7 past the room's $2 ceiling, the room pauses and parks the turn, the
// Director raises the ceiling, the task re-queues as attempt 2, and attempt 2
// ends at $1.3. The daemon reports attempt 2 either resuming the runtime
// session or cold-starting it (daemon-protocol §4.4 resume_outcome) — the bill
// is the same either way.
func TestUsageSurvivesBudgetResume(t *testing.T) {
	for _, resume := range []string{"resumed", "cold_start"} {
		t.Run(resume, func(t *testing.T) {
			f := newP2Fixture(t)
			if _, err := f.pool.Exec(t.Context(), `UPDATE room SET limits = '{"budget_usd": 2}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
				t.Fatal(err)
			}
			_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
			w := &costWatch{f: f, task: taskID}
			f.runTask(t, taskID)
			f.heartbeat(t, taskID, 1.0)
			w.step(t, "attempt 1 heartbeat $1.0")
			f.heartbeat(t, taskID, 2.7)
			w.step(t, "attempt 1 heartbeat $2.7")
			if st, reason := f.pausedTask(t, taskID); st != "paused" || reason != "budget" {
				t.Fatalf("premise: task = %s(%s), want paused(budget)", st, reason)
			}

			f.liftRoomBudget(t, 10)
			w.step(t, "budget raise")
			if a := attemptNow(t, f.pool, taskID); a != 2 {
				t.Fatalf("premise: attempt = %d after the raise, want 2", a)
			}

			f.runTask(t, taskID)
			// The moment the old code lost it: attempt 2's first running
			// total replaced attempt 1's $2.7.
			f.heartbeat(t, taskID, 0.5)
			w.step(t, "attempt 2 heartbeat $0.5")
			f.finishWith(t, taskID, 1.3, resume)
			w.checkFinal(t, w.step(t, "attempt 2 finish $1.3"))

			// The ceiling reads the same sum: $4.0 of a $10 room leaves $6.
			st, err := f.srv.loadBudgetStateTx(t, taskID)
			if err != nil {
				t.Fatal(err)
			}
			if !usdEq(st.TaskSpentUSD, 4.0) || !usdEq(st.SessionSpentUSD, 4.0) {
				t.Fatalf("budget state task=%.4f room=%.4f, want 4.0 / 4.0 — enforcement must count every attempt", st.TaskSpentUSD, st.SessionSpentUSD)
			}
		})
	}
}

// TestUsageSurvivesProfileFallback is the third way a task gets a new attempt:
// attempt 1 dies retryably after $2.7, the server re-queues it onto the
// profile's fallback (E8-08), and attempt 2 finishes at $1.3.
func TestUsageSurvivesProfileFallback(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	var primary uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM agent_profile WHERE agent_id = $1 AND is_default`, f.rUUID).Scan(&primary); err != nil {
		t.Fatal(err)
	}
	var spare uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO agent_profile (agent_id, name, runtime_kind, model)
		SELECT agent_id, 'spare', runtime_kind, model FROM agent_profile WHERE id = $1 RETURNING id`, primary).Scan(&spare); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE agent_profile SET fallback_profile_id = $2 WHERE id = $1`, primary, spare); err != nil {
		t.Fatal(err)
	}

	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	w := &costWatch{f: f, task: taskID}
	f.runTask(t, taskID)
	f.heartbeat(t, taskID, 2.7)
	w.step(t, "attempt 1 heartbeat $2.7")

	if err := f.srv.Tasks.Requeue(ctx, taskID, contracts.FailStall, nil, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	w.step(t, "stall re-queue")
	var profile uuid.UUID
	var attempt int
	if err := f.pool.QueryRow(ctx, `SELECT profile_id, attempt FROM task WHERE id = $1`, taskID).Scan(&profile, &attempt); err != nil {
		t.Fatal(err)
	}
	if profile != spare || attempt != 2 {
		t.Fatalf("premise: profile=%s attempt=%d, want the fallback %s on attempt 2", profile, attempt, spare)
	}

	f.runTask(t, taskID)
	f.heartbeat(t, taskID, 0.5)
	w.step(t, "attempt 2 heartbeat $0.5")
	f.finishWith(t, taskID, 1.3, "")
	w.checkFinal(t, w.step(t, "attempt 2 finish $1.3"))
}

// TestUsageSameAttemptOverwrites keeps the other half of the rule: WITHIN an
// attempt the daemon sends a running total, so heartbeats and the finish
// replace one another rather than add up, and an empty finish (a cancel often
// carries nothing) keeps the last heartbeat's number (service.go, S-19 note).
func TestUsageSameAttemptOverwrites(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.heartbeat(t, taskID, 0.4)
	f.heartbeat(t, taskID, 0.9)
	if _, err := f.srv.Tasks.Finish(t.Context(), taskID, 1, contracts.Finish{Outcome: "completed", StopReason: "end_turn"}); err != nil {
		t.Fatal(err)
	}
	u, err := tasks.GetUsage(t.Context(), f.pool, taskID)
	if err != nil || u == nil {
		t.Fatalf("usage = %v, %v", u, err)
	}
	if !usdEq(u.CostUSD, 0.9) {
		t.Fatalf("task cost = %.4f, want 0.9 — heartbeats in one attempt are running totals, and an empty finish is not a zero", u.CostUSD)
	}
}

// loadBudgetStateTx is loadBudgetState in a throwaway transaction.
func (s *Server) loadBudgetStateTx(t *testing.T, taskID uuid.UUID) (*budgetState, error) {
	t.Helper()
	tx, err := s.DB.Begin(t.Context())
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	return s.loadBudgetState(t.Context(), tx, taskID)
}

// TestUsageEstimatesPricedPerAttempt: an ACP runtime reports `estimated: true`
// with tokens only, and the server prices each row from its OWN tokens
// (repriceEstimates). Keyed by task, the re-price of attempt 2 would write its
// number over attempt 1's row too.
func TestUsageEstimatesPricedPerAttempt(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	// 100k + 100k of claude-sonnet-5 = $1.20 (cost.Defaults).
	f.estimatedTurn(t, taskID, "claude-sonnet-5", 100_000, 100_000)
	if err := f.srv.Tasks.Requeue(t.Context(), taskID, contracts.FailStall, nil, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	f.runTask(t, taskID)
	// 50k + 50k = $0.60.
	f.estimatedTurn(t, taskID, "claude-sonnet-5", 50_000, 50_000)
	usd, est := f.storedUsage(t, taskID)
	if !usdEq(usd, 1.80) || !est {
		t.Fatalf("task cost = %.4f (estimated %v), want 1.80 estimated — $1.20 for attempt 1 plus $0.60 for attempt 2", usd, est)
	}
}
