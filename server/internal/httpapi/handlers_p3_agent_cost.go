package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/cost"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// ---------------------------------------------------------------------------
// Kill switch (FR-1.9 M8, E10-07 … E10-09)
// ---------------------------------------------------------------------------

// applyKillSwitch carries out `respond_to: nobody`'s four immediate effects.
// It is four different verbs on four different objects, and PlanKillSwitch is
// where that table lives:
//
//	running  → cancelled through §8.2.2 (a kill switch that only stops FUTURE
//	           work leaves the runaway turn running — the case it exists for)
//	queued   → cancelled
//	open HITL→ KEPT (the human's chance to answer is not the agent's to lose)
//	workdirs → preserved (re-enabling must be able to continue)
func (s *Server) applyKillSwitch(ctx context.Context, agentID uuid.UUID) error {
	now := s.Clock.Now()
	return s.inSessionTx(ctx, func(tx pgx.Tx) error {
		var running, queued, waiting, workdirs int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE status IN ('dispatched', 'preparing', 'running')),
			       count(*) FILTER (WHERE status IN ('queued', 'deferred')),
			       count(*) FILTER (WHERE status = 'waiting_human')
			FROM task WHERE agent_id = $1`, agentID).Scan(&running, &queued, &waiting); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE agent_id = $1 AND status <> 'deleted'`, agentID).
			Scan(&workdirs); err != nil {
			return err
		}
		e := tasks.PlanKillSwitch(tasks.KillSwitchState{
			Running: running, Queued: queued, WaitingHuman: waiting, Workdirs: workdirs,
		})
		if e.CancelRunning+e.CancelQueued == 0 {
			return nil
		}
		ids, err := collectTaskIDs(ctx, tx, `
			SELECT id FROM task WHERE agent_id = $1
			  AND status IN ('deferred', 'queued', 'dispatched', 'preparing', 'running')
			ORDER BY created_at`, agentID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := s.Tasks.CancelForSession(ctx, tx, id, "kill_switch", now); err != nil {
				return err
			}
		}
		// waiting_human tasks and their open requests are deliberately NOT
		// touched here (M8 표 3행) — nor are the workdirs.
		return nil
	})
}

// releaseHeldRequeues is the other half of E10-08: the owner re-enables the
// agent, so the answers recorded while it was disabled finally start their
// attempts. Without an explicit held flag there would be nothing to release —
// the answer would sit on a task in waiting_human forever.
func (s *Server) releaseHeldRequeues(ctx context.Context, agentID uuid.UUID) error {
	// E10-08's shape is tasks.PlanKillSwitchAnswer's: the answer parked the
	// task at TaskStatus with requeue_held, and re-enabling releases it to
	// AfterReenableTaskStatus. Reading them from the planner rather than
	// repeating the two literals is what keeps this loop and hitl.PlanRespond
	// from drifting apart (review NN2).
	ks := tasks.PlanKillSwitchAnswer()
	rows, err := s.DB.Query(ctx, `
		SELECT h.id, h.task_id FROM hitl_request h
		JOIN task t ON t.id = h.task_id
		WHERE t.agent_id = $1 AND h.requeue_held AND t.status = $2::task_status`, agentID, ks.TaskStatus)
	if err != nil {
		return err
	}
	type held struct{ hitl, task uuid.UUID }
	var list []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.hitl, &h.task); err != nil {
			rows.Close()
			return err
		}
		list = append(list, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range list {
		// ResumeFromHuman's queued IS AfterReenableTaskStatus; the planner is
		// the statement of that, not a second implementation of it.
		if _, err := s.Tasks.ResumeFromHuman(ctx, h.task, tasks.CauseHitlAnswer); err != nil {
			return err
		}
		if got := s.taskStatusOf(ctx, h.task); got != ks.AfterReenableTaskStatus {
			s.Log.Warn("released hold did not reach the planned status",
				"task", h.task, "got", got, "want", ks.AfterReenableTaskStatus)
		}
		if _, err := s.DB.Exec(ctx, `UPDATE hitl_request SET requeue_held = false WHERE id = $1`, h.hitl); err != nil {
			return err
		}
	}
	if len(list) > 0 {
		s.Queue.Notifier.Notify()
	}
	return nil
}

func collectTaskIDs(ctx context.Context, q pgx.Tx, sql string, args ...any) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---------------------------------------------------------------------------
// Cost (openapi getSessionCost · getWorkspaceCost, FR-7.3)
// ---------------------------------------------------------------------------

const selectUsageRows = `
	SELECT t.id, t.agent_id, COALESCE(t.runtime_id, '00000000-0000-0000-0000-000000000000'::uuid), t.session_id,
	       COALESCE(a.name, ''), COALESCE(a.name, ''), COALESCE(rt.name, ''), COALESCE(s.title, ''),
	       u.cost_usd, u.estimated, u.input_tokens, u.output_tokens, u.cache_read
	FROM task_usage u
	JOIN task t ON t.id = u.task_id
	JOIN session s ON s.id = t.session_id
	LEFT JOIN agent a ON a.id = t.agent_id
	LEFT JOIN runtime rt ON rt.id = t.runtime_id`

func (s *Server) usageRows(ctx context.Context, where string, args ...any) ([]cost.UsageRow, error) {
	rows, err := s.DB.Query(ctx, selectUsageRows+" WHERE "+where+" ORDER BY t.created_at", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cost.UsageRow
	for rows.Next() {
		var r cost.UsageRow
		var taskName string
		if err := rows.Scan(&r.TaskID, &r.AgentID, &r.RuntimeID, &r.SessionID,
			&taskName, &r.AgentName, &r.RuntimeName, &r.SessionName,
			&r.CostUSD, &r.Estimated, &r.InputTokens, &r.OutputTokens, &r.CacheRead); err != nil {
			return nil, err
		}
		// A task has no name of its own; the card shows the agent that ran it.
		r.TaskName = r.AgentName
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Server) GetSessionCost(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	if _, p := s.sessionAccess(r, sessionId); p != nil {
		writeProblem(w, p)
		return
	}
	rows, err := s.usageRows(r.Context(), "t.session_id = $1", sessionId)
	if err != nil {
		writeErr(w, err)
		return
	}
	rep := cost.Rollup(rows)
	var limits []byte
	_ = s.DB.QueryRow(r.Context(), `SELECT limits FROM session WHERE id = $1`, sessionId).Scan(&limits)
	writeJSON(w, http.StatusOK, costReportAPI(rep, budgetOf(limits)))
}

func (s *Server) GetWorkspaceCost(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.GetWorkspaceCostParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	where := "s.workspace_id = $1"
	args := []any{workspaceId}
	if params.From != nil {
		args = append(args, *params.From)
		where += " AND t.created_at >= $2"
	}
	if params.To != nil {
		args = append(args, *params.To)
		where += " AND t.created_at < $" + itoa(len(args))
	}
	rows, err := s.usageRows(r.Context(), where, args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := costReportAPI(cost.Rollup(rows), 0)
	out.From = nullableTime(params.From)
	out.To = nullableTime(params.To)
	writeJSON(w, http.StatusOK, out)
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func costReportAPI(rep cost.Report, budget float64) gen.CostReport {
	buckets := func(bs []cost.Bucket) *[]gen.CostBucket {
		out := make([]gen.CostBucket, 0, len(bs))
		for _, b := range bs {
			in, o, c := b.InputTokens, b.OutputTokens, b.CacheRead
			count := b.TaskCount
			out = append(out, gen.CostBucket{
				Id: b.ID, Name: b.Name, CostUsd: float32(b.CostUSD), Estimated: b.Estimated,
				InputTokens: &in, OutputTokens: &o, CacheRead: &c, TaskCount: &count,
			})
		}
		return &out
	}
	r := gen.CostReport{
		TotalUsd: float32(rep.TotalUSD), Estimated: rep.Estimated,
		InputTokens: rep.InputTokens, OutputTokens: rep.OutputTokens, CacheRead: rep.CacheRead,
		ByTask: buckets(rep.ByTask), ByAgent: buckets(rep.ByAgent),
		BySession: buckets(rep.BySession), ByRuntime: buckets(rep.ByRuntime),
	}
	if budget > 0 {
		b := float32(budget)
		r.BudgetUsd = nullableFloat(&b)
	} else {
		r.BudgetUsd = nullableFloat(nil)
	}
	return r
}

func nullableFloat(v *float32) nullable.Nullable[float32] {
	if v == nil {
		return nullable.NewNullNullable[float32]()
	}
	return nullable.NewNullableWithValue(*v)
}

var _ = context.Background
var _ = time.Now
var _ = apperr.Internal

// taskStatusOf is the one-line read releaseHeldRequeues uses to check its own
// work against tasks.PlanKillSwitchAnswer.
func (s *Server) taskStatusOf(ctx context.Context, taskID uuid.UUID) string {
	var st string
	_ = s.DB.QueryRow(ctx, `SELECT status::text FROM task WHERE id = $1`, taskID).Scan(&st)
	return st
}
