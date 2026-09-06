package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
)

// ResumeFromHuman starts the next attempt of a task a person unblocked: an
// answered HITL, an approved budget raise, a re-enabled agent (FR-5.4 "응답 =
// 새 attempt", FR-7.3 M9, FR-1.9 M8).
//
// It is one function for the three because they are one transition. `running`
// is not reachable from `waiting_human` (FR-7.1 N4) and the process that asked
// the question is gone, so the only honest move is a new attempt on the same
// task, same lane and same workdir — which is exactly what PlanAttempt plans.
func (s *Service) ResumeFromHuman(ctx context.Context, taskID uuid.UUID, cause string) (*Row, error) {
	now := s.Clock.Now()
	var out *Row
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		out = t
		if t.Status != WaitingHuman && t.Status != Paused {
			// Already moving (a second answer, a cancel that got there first).
			// Not an error: the answer is recorded either way (E7-08).
			return nil
		}
		return s.requeueParkedLocked(ctx, tx, t, cause, now)
	})
	return out, err
}

// requeueParkedLocked is the write half of ResumeFromHuman, on a row the caller
// has already locked. It is shared with ResumeSessionTasks because the two are
// the same transition — a person lifted the hold, so the task starts a NEW
// attempt on the SAME lane and workdir with resume tried first (FR-5.4, E9-02).
// Writing the session-scoped resume separately is how one of the two quietly
// stops reusing the workdir.
func (s *Service) requeueParkedLocked(ctx context.Context, tx pgx.Tx, t *Row, cause string, now time.Time) error {
	if _, err := Transition(t.Status, Queued); err != nil {
		return err
	}
	p := PlanAttempt(AttemptInput{
		TaskID: t.ID, Attempt: t.Attempt, MaxAttempts: t.MaxAttempts,
		Cause: cause, PrevWorkdir: "-",
	})
	if _, err := tx.Exec(ctx, `
		UPDATE task SET status = 'queued', attempt = $2, pending_hitl = false, paused_reason = NULL,
		       paused_detail = NULL, failure_kind = NULL, not_before = NULL, runtime_id = NULL,
		       heartbeat_at = NULL, dispatched_at = NULL, started_at = NULL, finished_at = NULL, updated_at = $3
		WHERE id = $1`, t.ID, p.Attempt, now); err != nil {
		return fmt.Errorf("tasks: resume from human: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'queued', finished_at = NULL, updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
		return err
	}
	t.Status, t.Attempt, t.PendingHitl, t.PausedReason = Queued, p.Attempt, false, nil
	s.publish(ctx, tx, t)
	return nil
}

// ResumeSessionTasks re-queues the tasks a SESSION-scoped pause parked
// (PRD FR-2.3, §8.2.2 "드레인 vs 취소 · 재개", EVAL E9-02·E9-03·E9-10).
//
// S-46: a budget or time pause CANCELS the turn in flight, and
// PauseSessionTasks parks every running task at `paused(budget)` with its lane
// `paused`. resumeSession set the session back to `active` and lifted only the
// lanes that still held a QUEUED task — so the work the pause had actually
// stopped stayed `paused` forever, with no endpoint anywhere that moves it.
// The session looked resumed and dispatched nothing (found in T-S6; #136
// lifted the lane gate of the post-turn case, which is a different row).
//
// Two tasks are deliberately left parked:
//
//   - one whose OWN budget request is still open — that request is answered by
//     respondHitlRequest, and re-queueing it here would spend past a limit
//     nobody raised (E9-01);
//   - one whose own budget request was REFUSED — E9-03 says a rejection keeps
//     the task at `paused(budget)` until a person presses 중단, and a session
//     pause/resume cycle must not launder that refusal into a dispatch.
//
// Both are per-TASK requests (hitl_request.task_id = the task, FR-7.3 s-13);
// the session-scoped one carries a NULL task_id and is answered by this very
// resume, so it gates nothing here.
func (s *Service) ResumeSessionTasks(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, reason, cause string, now time.Time) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.id FROM task t
		WHERE t.session_id = $1 AND t.status = 'paused'
		  AND ($2 = '' OR t.paused_reason::text = $2)
		  AND NOT EXISTS (
		        SELECT 1 FROM hitl_request h
		         WHERE h.task_id = t.id AND h.purpose = 'budget'
		           AND (h.status = 'open' OR h.approved IS NOT TRUE))
		ORDER BY t.created_at
		FOR UPDATE`, sessionID, reason)
	if err != nil {
		return nil, fmt.Errorf("tasks: resume session tasks: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The cursor is drained before any write: writing while iterating is the
	// "conn busy" trap (G4 S7).
	resumed := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		t, err := lockTask(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if t.Status != Paused {
			continue // moved between the select and the lock
		}
		if err := s.requeueParkedLocked(ctx, tx, t, cause, now); err != nil {
			return nil, err
		}
		resumed = append(resumed, id)
	}
	return resumed, nil
}

// SetPendingHitl flags the task without moving it. FR-7.1 step 1: registration
// is not a transition — the agent may keep working, and only `turn_end` sends
// the task to waiting_human (E7-01, E7-02).
func (s *Service) SetPendingHitl(ctx context.Context, q pgx.Tx, taskID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE task SET pending_hitl = true, updated_at = $2 WHERE id = $1`, taskID, now)
	if err != nil {
		return fmt.Errorf("tasks: pending_hitl: %w", err)
	}
	return nil
}

// waitingHumanLocked is FR-7.1 step 3, applied when the daemon's end-of-turn
// report arrives for a task holding an open request. PlanTurn is what says the
// four things that means — no process, no slot, the workdir kept, no heartbeat
// — and three of them follow from the status; the fourth is the token, which
// is revoked here because a process that no longer exists must not be able to
// post (FR-9.1).
func (s *Service) waitingHumanLocked(ctx context.Context, tx pgx.Tx, t *Row, attempt int, now time.Time) error {
	p := hitl.PlanTurn(0, true)
	if _, err := Transition(t.Status, WaitingHuman); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task SET status = $2, finished_at = NULL, heartbeat_at = NULL, updated_at = $3 WHERE id = $1`,
		t.ID, p.TaskStatus, now); err != nil {
		return fmt.Errorf("tasks: waiting_human: %w", err)
	}
	if err := s.Tokens.Revoke(ctx, tx, t.ID, attempt, "waiting_human"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE lane SET status = $2, updated_at = $3 WHERE id = $1`, t.LaneID, p.LaneStatus, now); err != nil {
		return err
	}
	t.Status = WaitingHuman
	s.publish(ctx, tx, t)
	return nil
}

// PauseTaskForBudget parks ONE task at paused(budget) and asks its daemon to
// stop through the §8.2.2 procedure. It is the task-scoped sibling of
// PauseSessionTasks: a single agent over its own budget_per_task must not stop
// the lanes that are inside theirs (FR-7.3, E9-01).
func (s *Service) PauseTaskForBudget(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, detail []byte, now time.Time) error {
	t, err := lockTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if Terminal(t.Status) || t.Status == Paused {
		return nil
	}
	return s.pauseLocked(ctx, tx, t, "budget", detail, Paused, now)
}

// RecordTurnUsage stores the running usage the daemon reports on every
// heartbeat (daemon-protocol §4.2). It is an upsert on the task, not an
// increment: the daemon sends the turn's TOTAL, so adding it would multiply
// the bill by the number of heartbeats.
//
// An `estimated: true` report carries a 0 the runtime did not measure
// (harness v0.7.1), so the reported number is dropped — and the row is priced
// from the workspace price table before this returns.
//
// S-48: the pricing used to happen only in the roll-up, which runs at
// `finish`. Every ACP runtime (Claude Code, Hermes) reports `estimated: true`
// — T-I3 measured 72 of 72 runtime-written `task_usage` rows that way — so the
// heartbeat wrote a 0, `enforceBudgetFor` compared that 0 against the limit,
// and the "턴 중 강제" half of FR-7.3 could not fire for any runtime the
// product actually runs. Fixing the daemon's own half (D-17) would only have
// made it report a 0 more often. The pricing is the SAME function the roll-up
// calls, so the heartbeat and the finish cannot drift onto two numbers.
func (s *Service) RecordTurnUsage(ctx context.Context, taskID uuid.UUID, u contracts.Usage, now time.Time) error {
	reported := u.CostUSD
	if u.Estimated {
		reported = 0
	}
	var model *string
	if u.Model != "" {
		m := u.Model
		model = &m
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_usage (task_id, input_tokens, output_tokens, cache_read, cost_usd, estimated, model, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (task_id) DO UPDATE SET input_tokens = EXCLUDED.input_tokens, output_tokens = EXCLUDED.output_tokens,
			  cache_read = EXCLUDED.cache_read, cost_usd = EXCLUDED.cost_usd, estimated = EXCLUDED.estimated,
			  model = COALESCE(EXCLUDED.model, task_usage.model), updated_at = EXCLUDED.updated_at`,
			taskID, u.InputTokens, u.OutputTokens, u.CacheReadTokens, reported, u.Estimated, model, now); err != nil {
			return fmt.Errorf("tasks: turn usage: %w", err)
		}
		if !u.Estimated {
			// A measured cost is already the number; re-pricing it would
			// overwrite a measurement with a guess (repriceEstimates only
			// touches `estimated` rows, but not walking the session at all is
			// cheaper on the 15s heartbeat).
			return nil
		}
		var wsID, sessionID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT s.workspace_id, t.session_id FROM task t JOIN session s ON s.id = t.session_id
			WHERE t.id = $1`, taskID).Scan(&wsID, &sessionID); err != nil {
			return fmt.Errorf("tasks: turn usage session: %w", err)
		}
		return repriceEstimates(ctx, tx, wsID, sessionID, now)
	})
}

// NoteBudgetEnforceFailed puts the loss of a post-finish budget check on the
// FEED (S-47). The check runs in its own transaction after `finish` has
// committed (finishAndEnforce explains why), so when it fails the attempt
// stands and nothing is paused: a task that crossed its per-task limit leaves
// its lane unlocked and the next task is dispatched, to be caught only at that
// task's first usage report. That is one turn of overspend, and until now the
// only trace of it was a Warn line in the server log — the session's own
// timeline said the turn ended normally.
//
// The re-check itself is guaranteed by enforceBudgetFor being idempotent and
// state-driven rather than event-driven: it re-reads task_usage and the
// session status every time, the HITL insert is guarded (one open system
// request per session·purpose·task), and a session already `paused` returns
// early. So the NEXT heartbeat or finish on this session runs the same
// comparison and pauses then — this note exists to say that a turn went by
// unchecked, not to schedule the retry.
func (s *Service) NoteBudgetEnforceFailed(ctx context.Context, taskID uuid.UUID, attempt int, cause error, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		return InsertServerEventOnce(ctx, tx, taskID, attempt, "status", "error", "budget.enforce_failed", "error",
			map[string]any{
				"note":  "턴이 끝난 뒤 예산 재검사가 실패했습니다 — 다음 heartbeat·finish 에서 다시 검사합니다",
				"error": cause.Error(),
			}, now)
	})
}

// PauseLaneForBudget parks the LANE of a task that has ALREADY finished.
//
// S-44: the budget check that runs after `finish` finds the overrun when the
// turn is over. There is no turn to cancel and no task to park — the task is
// `completed`, and completed → paused is not a transition the state machine
// has (E5). What is still ahead is the NEXT task on this lane, and that is
// what the pause has to stop: the agent's per-task budget is what was crossed,
// so the next task by the same agent on the same lane would cross it again.
// The claim query refuses a paused lane (queue/postgres.go), so this one row
// is the whole gate; the Director's approval lifts it (ResumeLaneForBudget).
func (s *Service) PauseLaneForBudget(ctx context.Context, tx pgx.Tx, laneID uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE lane SET status = 'paused', updated_at = $2 WHERE id = $1 AND status <> 'paused'`,
		laneID, now); err != nil {
		return fmt.Errorf("tasks: pause lane for budget: %w", err)
	}
	if s.LanePublish != nil {
		s.LanePublish(ctx, tx, laneID)
	}
	return nil
}

// ResumeLaneForBudget lifts a PauseLaneForBudget gate after the Director
// raises the limit. It is the lane-only half of ResumeFromHuman: the task the
// HITL names is terminal, so there is nothing to re-queue — only the lane's
// refusal to dispatch to lift.
//
// The lane goes back to `queued` when it still holds a queued task and to
// `done` otherwise, which is the same CASE the completed branch of Finish
// writes. `done` is not an end: rule 3 re-entry brings the lane back the next
// time the agent is mentioned (lanestate.Reenter).
func (s *Service) ResumeLaneForBudget(ctx context.Context, laneID uuid.UUID, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE lane SET status = CASE WHEN EXISTS (SELECT 1 FROM task WHERE lane_id = $1 AND status = 'queued')
			                          THEN 'queued'::lane_status ELSE 'done'::lane_status END,
			       updated_at = $2
			WHERE id = $1 AND status = 'paused'`, laneID, now); err != nil {
			return fmt.Errorf("tasks: resume lane for budget: %w", err)
		}
		if s.LanePublish != nil {
			s.LanePublish(ctx, tx, laneID)
		}
		return nil
	})
}

// LaneBudgetOverride is the raise the Director last approved on this lane.
//
// A `task.budget_override` is scoped to one task (FR-7.3 C2′), which is right
// for the in-turn pause — the same task resumes and carries it. It is not
// enough for the post-turn pause: there the approved task is already finished,
// so the raise would apply to nothing and the NEXT task on the lane would trip
// the same limit on its first heartbeat, park the lane again and ask the same
// question. The override therefore carries along the lane it was granted on,
// which is the narrowest scope that makes the answer mean something. The
// AGENT's budget_per_task is still untouched (E9-02), and another lane of the
// same agent is unaffected.
func LaneBudgetOverride(ctx context.Context, q db.DBTX, laneID uuid.UUID) (*float64, error) {
	var v *float64
	err := q.QueryRow(ctx, `
		SELECT t.budget_override FROM task t
		WHERE t.lane_id = $1 AND t.budget_override IS NOT NULL
		ORDER BY t.created_at DESC, t.id DESC LIMIT 1`, laneID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tasks: lane budget override: %w", err)
	}
	return v, nil
}
