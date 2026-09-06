package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
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
	})
	return out, err
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
func (s *Service) PauseTaskForBudget(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, detail string, now time.Time) error {
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
// (harness v0.7.1), so the number is dropped here exactly as Finish drops it
// and the roll-up prices it from the workspace table instead (S-20).
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
	_, err := s.DB.Exec(ctx, `
		INSERT INTO task_usage (task_id, input_tokens, output_tokens, cache_read, cost_usd, estimated, model, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (task_id) DO UPDATE SET input_tokens = EXCLUDED.input_tokens, output_tokens = EXCLUDED.output_tokens,
		  cache_read = EXCLUDED.cache_read, cost_usd = EXCLUDED.cost_usd, estimated = EXCLUDED.estimated,
		  model = COALESCE(EXCLUDED.model, task_usage.model), updated_at = EXCLUDED.updated_at`,
		taskID, u.InputTokens, u.OutputTokens, u.CacheReadTokens, reported, u.Estimated, model, now)
	if err != nil {
		return fmt.Errorf("tasks: turn usage: %w", err)
	}
	return nil
}
