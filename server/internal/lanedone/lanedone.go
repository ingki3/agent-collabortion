// Package lanedone is the ONE place a lane is written `done` because the work
// on it finished (T-RF1).
//
// Two paths end a lane's work, and before T-RF1 each wrote its own UPDATE:
//
//   - the agent's `colab status set done` (router.SetAgentStatus) — the lane
//     is `done` unconditionally;
//   - the daemon's end of turn (tasks.Finish, outcome completed) — the lane is
//     `done` unless another task is queued on it (then `queued`) or the agent
//     put it in `blocked` (kept, FR-6.2.1).
//
// Either way, when the lane actually BECOMES `done` here, the after-done
// follow-up runs: the join (PRD FR-6.5) or the re-entry report to whoever
// asked (T-FIX-B — contracts/colab-cli.md §2 「lane 종료 판정은 서버가
// turn_end 와 함께 한다」). A lane that was already `done` (the agent said
// `status set done`, then its turn ended) runs nothing a second time.
//
// Both now call MarkDone, so the 「작업 카드」 gate (a lane completes only with
// a result card; without one it waits and the server asks for it) has exactly
// one place to go — marked CARD GATE below.
//
// It is a leaf on purpose: tasks cannot import router or lanes (both import
// tasks — the S-52 cycle), so the write lives below all three and the
// callers hand in what only they know (publishing, the follow-up).
//
// Writers of `done` that are NOT "the work finished" stay where they are and
// are listed in census_test.go: a held task cancelled because its mission
// closed (quiet.cancelOne) and a budget pause lifted on a lane with nothing
// queued (tasks.ResumeLaneForBudget). No result card is owed for either.
package lanedone

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
)

// Cause is which path is ending the lane's work.
type Cause int

const (
	// AgentDone is `colab status set done`: the agent declared the work
	// finished. The lane is written `done` whatever it held.
	AgentDone Cause = iota + 1
	// TurnEnd is the daemon reporting the turn completed. Another queued task
	// keeps the lane `queued`; a `blocked` lane is left alone (the question
	// is still open).
	TurnEnd
)

func (c Cause) String() string {
	switch c {
	case AgentDone:
		return "agent_done"
	case TurnEnd:
		return "turn_end"
	}
	return fmt.Sprintf("cause(%d)", int(c))
}

// Request is one MarkDone call.
type Request struct {
	LaneID uuid.UUID
	Cause  Cause
	Now    time.Time

	// Publish emits `lane.updated` right after the write, before AfterDone —
	// the order the board's frames had before T-RF1. nil when the caller
	// publishes the lane itself later (tasks.Finish does, through
	// tasks.publish, together with task.updated).
	Publish func(ctx context.Context, tx pgx.Tx, laneID uuid.UUID)

	// AfterDone is the follow-up the lane's end owes (router.afterLaneDone:
	// the join, FR-6.5, or the re-entry report). It is called in the same
	// transaction, only when this call moved the lane to `done`
	// (Result.Became) and the cause runs one (runsFollowUp). nil = none.
	// tasks.Finish hands in a closure that only RECORDS the end; the
	// follow-up itself runs after the finish commits (tasks.LaneEnded — the
	// finish↔완료 lock-order precedent).
	AfterDone func(ctx context.Context, tx pgx.Tx) error
}

// Result is what the write left.
type Result struct {
	// Status is the lane's status after the call: `done`, or for TurnEnd
	// `queued` (another task waits) or `blocked` (left as it was).
	Status string
	// Prev is the lane's status before the call ("" when the lane is gone).
	Prev string
}

// Done reports whether the lane is now `done`.
func (r Result) Done() bool { return r.Status == lanestate.Done }

// Became reports whether THIS call ended the lane: it is `done` now and was
// not before. The follow-up keys on it, so `status set done` followed by the
// turn's own end runs the join and the report once — the second call finds
// the lane already `done`. Both callers hold the task row (FOR UPDATE) and
// read Prev under the lane's row lock, so a `status set done` racing the
// finish of the same attempt is serialised and exactly one of them sees the
// transition.
func (r Result) Became() bool { return r.Done() && r.Prev != lanestate.Done }

// runsFollowUp is which paths run the after-done follow-up (the join, FR-6.5,
// and the re-entry report — router.afterLaneDone). Both do (T-FIX-B, Director
// 승인 2026-09-30): a delegated child that ends its turn without `colab status
// set done` used to leave its lane `done` with the join never asked — the
// group's last such child left the delegator asleep for good (the S-31 loss).
// What keeps the follow-up to ONE per lane end is not this switch but the
// transition check in MarkDone (res.Became).
func runsFollowUp(c Cause) bool {
	switch c {
	case AgentDone, TurnEnd:
		return true
	}
	return false
}

// MarkDone ends a lane's work. It is the only writer of lane `done` for a
// finished piece of work (census_test.go keeps it that way).
func MarkDone(ctx context.Context, tx pgx.Tx, req Request) (Result, error) {
	// ── CARD GATE ─────────────────────────────────────────────────────────
	// 「작업 카드」 goes here, and only here: before the lane is written done,
	// ask whether the lane's work has a result card. Without one the lane
	// would wait as 「결과 카드 대기」 and the server would queue the
	// follow-up turn that asks for it, instead of completing and running
	// AfterDone. T-RF1 adds no gate: nothing is checked, nothing changes.
	// Both paths now owe AfterDone (T-FIX-B), so a gate that turns the lane
	// to 「결과 카드 대기」 here must also skip it — and router.SetAgentStatus
	// must stop answering TurnEndRequired unconditionally (see there).
	// ──────────────────────────────────────────────────────────────────────

	// The lane's status before the write, under its row lock: the follow-up
	// runs only on the transition into `done` (Result.Became).
	var prev string
	err := tx.QueryRow(ctx, `SELECT status::text FROM lane WHERE id = $1 FOR UPDATE`, req.LaneID).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("lanedone: read %s: %w", req.Cause, err)
	}

	var status string
	switch req.Cause {
	case AgentDone:
		err = tx.QueryRow(ctx, `
			UPDATE lane SET status = 'done', finished_at = $2, updated_at = $2 WHERE id = $1
			RETURNING status::text`, req.LaneID, req.Now).Scan(&status)
	case TurnEnd:
		// Another queued task on this lane keeps it queued, else done. A lane
		// the agent put in `blocked` keeps that status: the turn ending is
		// exactly what `colab status set blocked` asked for, and overwriting
		// it with `done` loses the question the delegator has yet to answer
		// (FR-6.2.1).
		err = tx.QueryRow(ctx, `
			UPDATE lane SET status = CASE WHEN EXISTS (SELECT 1 FROM task WHERE lane_id = $1 AND status = 'queued') THEN 'queued'::lane_status ELSE 'done'::lane_status END,
			  finished_at = $2, updated_at = $2 WHERE id = $1 AND status <> 'blocked'
			RETURNING status::text`, req.LaneID, req.Now).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			// Blocked (or gone): nothing written. Report what is there.
			err = tx.QueryRow(ctx, `SELECT status::text FROM lane WHERE id = $1`, req.LaneID).Scan(&status)
			if errors.Is(err, pgx.ErrNoRows) {
				return Result{}, nil
			}
		}
	default:
		return Result{}, fmt.Errorf("lanedone: unknown cause %v", req.Cause)
	}
	if err != nil {
		return Result{}, fmt.Errorf("lanedone: mark %s: %w", req.Cause, err)
	}
	res := Result{Status: status, Prev: prev}
	if req.Publish != nil {
		req.Publish(ctx, tx, req.LaneID)
	}
	if res.Became() && runsFollowUp(req.Cause) && req.AfterDone != nil {
		if err := req.AfterDone(ctx, tx); err != nil {
			return res, err
		}
	}
	return res, nil
}
