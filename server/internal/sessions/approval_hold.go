package sessions

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// T-APPROVAL — the platform's user_approval request waits for the mission's
// work to settle (PRD FR-2.3 완료 흐름, openapi v0.3.3 held_reason).

// EventTasksSettled is the ApplyEvent kind ReleaseHeldApproval uses: it
// satisfies no atom, it only re-reads the tree over the atoms already met —
// the same shape as EventConditionChanged — so a mission whose only missing
// atom is user_approval now gets the request it was held from.
const EventTasksSettled = "tasks_settled"

// HeldRunningTasks is the `held_reason` value (openapi v0.3.3).
const HeldRunningTasks = "running_tasks"

// busyTaskSQL is "a task of mission $1 is running or about to": the four
// statuses T-APPROVAL names. `deferred` (a fallback not yet due) and the
// parked ones (`paused`, `waiting_human`) do not hold the request — a
// question waiting on the Director must not wait on itself.
const busyTaskSQL = `EXISTS (SELECT 1 FROM task t WHERE t.work_id = $1
	AND t.status IN ('queued', 'dispatched', 'preparing', 'running'))`

func missionBusy(ctx context.Context, q db.DBTX, workID uuid.UUID) (bool, error) {
	var busy bool
	if err := q.QueryRow(ctx, `SELECT `+busyTaskSQL, workID).Scan(&busy); err != nil {
		return false, fmt.Errorf("sessions: mission busy: %w", err)
	}
	return busy, nil
}

// ReleaseHeldApproval re-reads a held mission once its work may have
// settled. It is cheap when nothing is held (one indexed read) so the task
// layer calls it after every task that ends, whatever the ending — finish,
// cancel or failure.
//
// production callers: tasks.Service.AfterSettle (wired in httpapi.NewServer)
// and ReleaseHeldApprovals (the scheduler's catch-all).
func (s *Service) ReleaseHeldApproval(ctx context.Context, workID uuid.UUID) (bool, error) {
	var due bool
	err := s.DB.QueryRow(ctx, `
		SELECT approval_held_at IS NOT NULL AND status = 'active' AND NOT `+busyTaskSQL+`
		FROM work WHERE id = $1`, workID).Scan(&due)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !due) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sessions: held approval: %w", err)
	}
	out, err := s.ApplyWorkEvent(ctx, workID, Event{Kind: EventTasksSettled})
	if err != nil {
		var p *apperr.Problem
		if errors.As(err, &p) && p.Status == 409 {
			return false, nil // closed between the read and the lock
		}
		return false, err
	}
	return out.HitlIssued, nil
}

// ReleaseHeldApprovals is the scheduler's catch-all for the paths that end a
// task inside someone else's transaction (a room block, the kill switch):
// every held mission whose work has settled gets its request.
func (s *Service) ReleaseHeldApprovals(ctx context.Context) (int, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM work WHERE approval_held_at IS NOT NULL AND status = 'active'`)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		opened, err := s.ReleaseHeldApproval(ctx, id)
		if err != nil {
			return n, err
		}
		if opened {
			n++
		}
	}
	return n, nil
}
