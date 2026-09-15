package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// DispatchOutcome is what a daemon claim gets under a given session state.
type DispatchOutcome struct {
	Dispatched      int
	Order           []uuid.UUID
	RunningTurnKept bool // an in-flight turn was allowed to drain
	RunningTurnKill bool // an in-flight turn was cancelled
	TaskStatus      Status
	SessionState    string
	PauseReason     string
	SummaryMessages int
}

// PlanDispatch is the FR-2.3 session gate (C3′). The session, not the task, is
// the thing that decides whether a queued task may leave the queue: a paused
// session that keeps dispatching spends exactly the budget the pause was
// supposed to stop.
//
// The two pauses differ in what they do to a turn already running (FR-2.3,
// §8.2.2):
//
//	director → drain. The human asked for a stop, not for lost work.
//	budget   → cancel. Letting the turn finish defeats the limit (E5-07).
//
// production caller: tasks.Service.PauseSessionTasks (gate.go), reached from
// httpapi.PauseSession, httpapi.applyBudgetPause, sessions.completion and
// router's loop guard — RunningTurnKill is what decides whether a turn already
// in flight is drained or cancelled.
func PlanDispatch(sessionState, pauseReason string, queued []uuid.UUID, running bool) DispatchOutcome {
	o := DispatchOutcome{SessionState: sessionState, PauseReason: pauseReason, Order: []uuid.UUID{}}
	switch sessionState {
	case "active":
		o.Order = append(o.Order, queued...)
		o.Dispatched = len(o.Order)
	case "paused":
		if running {
			switch pauseReason {
			case "budget", "time":
				o.RunningTurnKill = true
				o.TaskStatus = Paused
			default:
				o.RunningTurnKept = true
			}
		}
	case "completing":
		// The summary is the last thing the session does, and it posts exactly
		// one session_summary message (FR-2.4) before the state settles.
		o.SessionState, o.SummaryMessages = "completed", 1
	}
	return o
}

// ---------------------------------------------------------------------------
// Production side of the gate.
// ---------------------------------------------------------------------------

// PauseSessionTasks carries out PlanDispatch's verdict for a session that has
// just been paused. It is the reason PlanDispatch exists as a function rather
// than a comment: FR-2.3 makes the two pauses behave differently towards work
// already in flight, and getting that backwards is invisible until a budget
// pause quietly lets the turn it was meant to stop run to completion.
//
//	director / loop → drain. The turn finishes; nothing new dispatches.
//	budget / time   → cancel (§8.2.2) and park the task at paused(<reason>),
//	                  so Director approval can resume it rather than re-run it.
//
// The "nothing new dispatches" half is enforced by the claim query's
// `s.status = 'active'` guard (queue/postgres.go), not here.
//
// Production call sites for PlanDispatch: this function, called from
// router.pauseForLoop (FR-3.5) and sessions.ApplyCompletionEvent
// (budget_exhausted, E6-10).
// `detail` is the marshalled contract PausedDetail (openapi PausedDetail,
// migration 0006) or nil. It is bytes rather than a string because the column
// is jsonb: passing prose here used to fail the UPDATE with "invalid input
// syntax for type json", which no caller had hit only because the two existing
// ones passed an empty string or never reached the pause branch.
func (s *Service) PauseSessionTasks(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, reason string, detail []byte, now time.Time) error {
	queued, err := collectIDs(tx.Query(ctx, `
		SELECT id FROM task WHERE session_id = $1 AND status = 'queued' ORDER BY created_at`, sessionID))
	if err != nil {
		return err
	}
	running, err := collectIDs(tx.Query(ctx, `
		SELECT id FROM task WHERE session_id = $1 AND status IN ('dispatched', 'preparing', 'running')
		ORDER BY created_at FOR UPDATE SKIP LOCKED`, sessionID))
	if err != nil {
		return err
	}
	o := PlanDispatch("paused", reason, queued, len(running) > 0)
	if !o.RunningTurnKill {
		return nil
	}
	for _, id := range running {
		t, err := lockTask(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.pauseLocked(ctx, tx, t, reason, detail, o.TaskStatus, now); err != nil {
			return err
		}
	}
	return nil
}

// pauseLocked parks one in-flight task and asks its daemon to stop. The daemon
// command is what actually ends the turn; the row is moved first so a claim
// racing this cannot hand the task out again.
func (s *Service) pauseLocked(ctx context.Context, tx pgx.Tx, t *Row, reason string, detail []byte, target Status, now time.Time) error {
	if _, err := Transition(t.Status, target); err != nil {
		return err
	}
	var det []byte
	if len(detail) > 0 {
		det = detail
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task SET status = $2, paused_reason = $3::pause_reason, paused_detail = $4, updated_at = $5 WHERE id = $1`,
		t.ID, string(target), reason, det, now); err != nil {
		return fmt.Errorf("tasks: pause task: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'paused', updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
		return err
	}
	if t.RuntimeID != nil {
		if requested, err := cancelRequested(ctx, tx, t.ID, t.Attempt); err != nil {
			return err
		} else if !requested {
			if err := tokens.QueueCommand(ctx, tx, *t.RuntimeID, cancelCommandFor(t, reason)); err != nil {
				return err
			}
		}
	}
	t.Status = target
	t.PausedReason = &reason
	s.publish(ctx, tx, t)
	return nil
}
