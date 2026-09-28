// Package quiet is PRD v0.19.13 FR-2A.2.3 (T-QUIET): a mission whose only
// unmet completion condition is the Director's approval does not let its
// agents make new work for each other.
//
// The judgement "the mission is waiting for approval" is made in ONE place —
// sessions.ApplyWorkEvent, the function that already reads the completion
// tree and decides whether to ask for approval — and written to
// `work.approval_quiet`. Everyone else only reads that column:
//
//   - the router (a message or a delegation written by an agent) holds the
//     trigger it makes: the task is queued with `queued_reason:
//     approval_pending` and the claim passes it by;
//   - the claim and FR-2A.2.1's "work still running" test leave held tasks
//     out, so the turn that is running when the mission goes quiet is the
//     last one, and the approval request opens when it ends.
//
// Every transition happens under the room row lock (the router, the
// delegation and ApplyWorkEvent all take it first), which is what makes the
// race between "the Director approves" and "an agent posts" safe: one of the
// two sees the other's result.
package quiet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// ReasonApprovalPending is the `task.queued_reason` of a held trigger
// (openapi v0.3.9 QueuedReason.approval_pending).
const ReasonApprovalPending = "approval_pending"

// States of work.approval_quiet.
const (
	// StateNone: the mission is not waiting for approval.
	StateNone = ""
	// StateQuiet: waiting for approval — an agent's triggers are held.
	StateQuiet = "quiet"
	// StateReleased: a person re-opened the work (a change request or a new
	// instruction). Agents work together again until one of them reports to
	// a person, or the work settles with the approval request still open
	// (Lead 판정 2026-09-28).
	StateReleased = "released"
)

// HeldTaskSQL is "task t is a held trigger" — the one predicate the claim,
// FR-2A.2.1's busy test and the progress count share.
const HeldTaskSQL = `(t.status = 'queued' AND t.queued_reason IS NOT DISTINCT FROM 'approval_pending')`

// ApprovedClosedNote is the feed sentence on a held trigger the approval
// cancelled (PRD FR-2A.2.3 ① 「일이 승인되어 닫혔습니다」).
const ApprovedClosedNote = "일이 승인되어 닫혔습니다"

// EndedClosedNote is the same cancel when the mission closed another way —
// the Director ended it by hand (completeWork) — so the feed does not claim
// an approval nobody gave.
const EndedClosedNote = "일이 끝나 닫혔습니다"

// StopApprovedClosed is the task's stop_reason for the same cancel.
const StopApprovedClosed = "approval_closed"

// NoticeFormat is harness v0.9.15's post-result line — the agent reads it in
// the CLI stdout / MCP response of the post that was held. %s is the held
// recipient's name without the @.
const NoticeFormat = "This mission is waiting for the Director's approval, so @%s was not woken. If work remains after approval, tell the Director."

// Notice is NoticeFormat for the held recipients of one post — one line,
// several names as "A, @B" so the contract's "@X" shape holds for each.
func Notice(names ...string) string {
	clean := make([]string, 0, len(names))
	for _, n := range names {
		clean = append(clean, strings.TrimPrefix(n, "@"))
	}
	return fmt.Sprintf(NoticeFormat, strings.Join(clean, ", @"))
}

// WarningCode is MessagePostResult.warnings[].code for a held trigger (Lead
// 판정 2026-09-28 — colab-cli §2.2 열거에 더한다).
const WarningCode = "approval_pending"

// State reads the mission's quiet state. A closed or missing mission is
// StateNone: nothing is waiting for its approval any more.
func State(ctx context.Context, q db.DBTX, workID uuid.UUID) (string, error) {
	var st *string
	err := q.QueryRow(ctx, `SELECT CASE WHEN status = 'active' THEN approval_quiet END FROM work WHERE id = $1`, workID).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return StateNone, nil
	}
	if err != nil {
		return "", fmt.Errorf("quiet: state: %w", err)
	}
	if st == nil {
		return StateNone, nil
	}
	return *st, nil
}

// Enter puts the mission into StateQuiet. It reports whether the state
// changed.
//
// The turns already RUNNING are not touched (FR-2A.2.3 「지금 도는 턴은 끊지
// 않는다」). Triggers already QUEUED that an agent's message made — first
// attempt, woken by an agent-written message, nothing a person wrote merged
// in — are held now: they are exactly what the rule holds from here on, and
// letting them run would keep FR-2A.2.1's hold from ever releasing.
func Enter(ctx context.Context, q db.DBTX, workID uuid.UUID, now time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE work SET approval_quiet = 'quiet', approval_quiet_at = $2
		 WHERE id = $1 AND status = 'active' AND approval_quiet IS DISTINCT FROM 'quiet'`, workID, now)
	if err != nil {
		return false, fmt.Errorf("quiet: enter: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := q.Exec(ctx, `
		UPDATE task t SET queued_reason = 'approval_pending'
		 WHERE t.work_id = $1 AND t.status = 'queued' AND t.attempt = 1
		   AND NOT t.pending_hitl AND t.restarted_from_task_id IS NULL
		   AND EXISTS (SELECT 1 FROM message m WHERE m.id = t.trigger_message_id AND m.author_type = 'agent')
		   AND NOT EXISTS (SELECT 1 FROM message m WHERE m.id = ANY (t.coalesced_message_ids) AND m.author_type <> 'agent')`,
		workID); err != nil {
		return false, fmt.Errorf("quiet: hold queued: %w", err)
	}
	return true, nil
}

// Release ends the quiet: the mission goes to `to` (StateReleased after a
// person re-opened it, StateNone when a condition other than approval is
// unmet again) and every held trigger of it is let go — its reason is
// cleared and the claim hands it out in arrival order. It returns the lanes
// whose card changed.
func Release(ctx context.Context, q db.DBTX, workID uuid.UUID, to string, now time.Time) ([]uuid.UUID, error) {
	var next *string
	if to != StateNone {
		next = &to
	}
	if _, err := q.Exec(ctx, `
		UPDATE work SET approval_quiet = $2, approval_quiet_at = CASE WHEN $2::text IS NULL THEN NULL ELSE $3::timestamptz END
		 WHERE id = $1 AND approval_quiet IS DISTINCT FROM $2::text`, workID, next, now); err != nil {
		return nil, fmt.Errorf("quiet: release: %w", err)
	}
	rows, err := q.Query(ctx, `
		UPDATE task t SET queued_reason = NULL, updated_at = $2
		 WHERE t.work_id = $1 AND `+HeldTaskSQL+`
		RETURNING t.lane_id`, workID, now)
	if err != nil {
		return nil, fmt.Errorf("quiet: release held: %w", err)
	}
	return distinctLanes(rows)
}

// Hold marks a queued task as a held trigger. Only a queued task is touched —
// a task that coalesced into one already running or waiting is not a new
// trigger. It reports whether the task is held now.
func Hold(ctx context.Context, q db.DBTX, taskID uuid.UUID) (bool, error) {
	var held bool
	err := q.QueryRow(ctx, `
		UPDATE task SET queued_reason = 'approval_pending'
		 WHERE id = $1 AND status = 'queued'
		RETURNING true`, taskID).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("quiet: hold: %w", err)
	}
	return held, nil
}

// IsHeld reports whether the task is a held trigger.
func IsHeld(ctx context.Context, q db.DBTX, taskID uuid.UUID) (bool, error) {
	var held bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM task t WHERE t.id = $1 AND `+HeldTaskSQL+`)`, taskID).Scan(&held); err != nil {
		return false, fmt.Errorf("quiet: is held: %w", err)
	}
	return held, nil
}

// Count is CompletionProgress.paused_agent_triggers.
func Count(ctx context.Context, q db.DBTX, workID uuid.UUID) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM task t WHERE t.work_id = $1 AND `+HeldTaskSQL, workID).Scan(&n); err != nil {
		return 0, fmt.Errorf("quiet: count: %w", err)
	}
	return n, nil
}

// CancelHeld is ① — the Director approved and the mission closed: every held
// trigger is cancelled with the reason on its feed. The caller's bulk cancel
// of the mission's other queued work runs after this and finds nothing of
// these left. It returns the lanes whose card changed.
func CancelHeld(ctx context.Context, tx pgx.Tx, workID uuid.UUID, note string, now time.Time) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT t.id, t.attempt, t.lane_id FROM task t WHERE t.work_id = $1 AND `+HeldTaskSQL+` ORDER BY t.created_at FOR UPDATE`, workID)
	if err != nil {
		return nil, fmt.Errorf("quiet: held tasks: %w", err)
	}
	type held struct {
		id, lane uuid.UUID
		attempt  int
	}
	var hs []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.attempt, &h.lane); err != nil {
			rows.Close()
			return nil, err
		}
		hs = append(hs, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	var lanes []uuid.UUID
	for _, h := range hs {
		if err := cancelOne(ctx, tx, h.id, h.attempt, h.lane, note, now); err != nil {
			return nil, err
		}
		if !seen[h.lane] {
			seen[h.lane] = true
			lanes = append(lanes, h.lane)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE work SET approval_quiet = NULL, approval_quiet_at = NULL WHERE id = $1`, workID); err != nil {
		return nil, fmt.Errorf("quiet: clear: %w", err)
	}
	return lanes, nil
}

// CancelClosed is the race side of ①: an agent's trigger made after the
// mission closed (the approval landed first under the room lock). It is
// cancelled as the held ones were — no claim would ever hand it out.
func CancelClosed(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, now time.Time) error {
	var attempt int
	var lane uuid.UUID
	err := tx.QueryRow(ctx, `SELECT attempt, lane_id FROM task WHERE id = $1 AND status = 'queued' FOR UPDATE`, taskID).Scan(&attempt, &lane)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("quiet: closed task: %w", err)
	}
	return cancelOne(ctx, tx, taskID, attempt, lane, ApprovedClosedNote, now)
}

func cancelOne(ctx context.Context, tx pgx.Tx, id uuid.UUID, attempt int, lane uuid.UUID, note string, now time.Time) error {
	if err := tasks.InsertServerEvent(ctx, tx, id, attempt, "status", "cancel", StopApprovedClosed, "ok",
		map[string]any{"command": "cancel", "args": map[string]any{
			"note": note, "reason": StopApprovedClosed,
		}}, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task SET status = 'cancelled', failure_kind = 'cancelled', finished_at = $2, stop_reason = $3, updated_at = $2
		 WHERE id = $1`, id, now, StopApprovedClosed); err != nil {
		return fmt.Errorf("quiet: cancel held: %w", err)
	}
	// The lane was waiting only for this trigger; nothing failed, the work
	// it would have done is no longer wanted. A lane with another live task
	// keeps its status.
	if _, err := tx.Exec(ctx, `
		UPDATE lane l SET status = 'done', finished_at = $2, updated_at = $2
		 WHERE l.id = $1 AND l.status = 'queued'
		   AND NOT EXISTS (SELECT 1 FROM task t WHERE t.lane_id = l.id
		                     AND t.status IN ('queued','deferred','dispatched','preparing','running','waiting_human','paused'))`,
		lane, now); err != nil {
		return fmt.Errorf("quiet: close held lane: %w", err)
	}
	return nil
}

func distinctLanes(rows pgx.Rows) ([]uuid.UUID, error) {
	defer rows.Close()
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for rows.Next() {
		var l uuid.UUID
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out, rows.Err()
}
