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

	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
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
	// TaskID is the task whose end this is — its kind decides the CARD GATE
	// (card: a result card first) and the question mode (question: the lane
	// goes back to how it was). uuid.Nil = a caller that predates cards
	// (tests of the plain path): no gate.
	TaskID uuid.UUID
	Cause  Cause
	Now    time.Time
	// Hub publishes what the gate writes (the follow-up's system line, the
	// automatic result card, card.updated). nil in tests without one.
	Hub *realtime.Hub

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
	// Gate is what the CARD GATE did ("" = nothing): GateFollowUp (the turn
	// ended without a result card — a result_card_missing task was queued),
	// GateAuto (the second follow-up passed too — the automatic result card
	// was written and the lane ended), GateQuestion (a question turn — the
	// lane went back to its status before the question).
	Gate string
	// FollowUpTaskID is the task GateFollowUp queued.
	FollowUpTaskID uuid.UUID
}

// What the gate did (Result.Gate).
const (
	GateFollowUp = "follow_up"
	GateAuto     = "auto"
	GateQuestion = "question"
)

// ErrResultCardRequired is `status set done` on a card task whose card has no
// result for this version (openapi setTaskStatus v0.3.10: 409
// result_card_required). Nothing is written; the turn goes on.
var ErrResultCardRequired = errors.New("lanedone: result card required")

// ErrQuestionDone is `status set done` in a question task — the handler
// refuses it first (command gate, 403); this is the server's own backstop.
var ErrQuestionDone = errors.New("lanedone: a question task does not end the lane")

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
	// 「작업 카드」 goes here, and only here (PRD FR-3.8 3, T-CARD-S): a
	// lane made by a delegation card ends only with a result card.
	//
	//   - `status set done` on a card task with no result for the card's
	//     current version: nothing is written — 409 result_card_required, the
	//     turn is alive and the agent reads it (router.SetAgentStatus answers
	//     turn_end_required false then, #393 NN2).
	//   - the turn ENDS with no result (the agent cannot see a refusal
	//     anymore): the lane is not done. A `result_card_missing` card task is
	//     queued on the same lane (the same runtime session resumes), at most
	//     twice per version; after that the server writes the automatic result
	//     card (every criterion unmet) and the lane ends — the join must never
	//     be stuck on a card nobody reports (FR-6.5).
	//   - a question task (FR-3.8 2) never ends or opens the lane: its turn's
	//     end puts the lane back where it was before the question, and nothing
	//     after-done runs (it is not a re-entry).
	//
	// Lock order is the callers': the task row, then the lane (below), then
	// the card — submitCardResult and reviseCard take the same order, so a
	// result racing the turn's end, or a revise racing a follow-up, lands one
	// way or the other, never both.
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

	gate := ""
	if req.TaskID != uuid.Nil {
		var kind string
		var cardID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT kind, card_id FROM task WHERE id = $1`, req.TaskID).Scan(&kind, &cardID); err != nil {
			return Result{}, fmt.Errorf("lanedone: task kind: %w", err)
		}
		switch {
		case kind == cards.KindQuestion:
			if req.Cause == AgentDone {
				return Result{Status: prev, Prev: prev}, ErrQuestionDone
			}
			st, err := EndQuestion(ctx, tx, req.TaskID, req.LaneID, req.Now)
			if err != nil {
				return Result{}, err
			}
			if req.Publish != nil {
				req.Publish(ctx, tx, req.LaneID)
			}
			return Result{Status: st, Prev: prev, Gate: GateQuestion}, nil
		case kind == cards.KindCard && cardID != nil && !(req.Cause == TurnEnd && prev == lanestate.Blocked):
			c, err := cards.Lock(ctx, tx, *cardID)
			if errors.Is(err, cards.ErrNotFound) {
				break
			}
			if err != nil {
				return Result{}, err
			}
			if c.Status != cards.InProgress {
				break // a result is in (or the card is closed): the lane ends as before
			}
			if req.Cause == AgentDone {
				return Result{Status: prev, Prev: prev}, ErrResultCardRequired
			}
			var queued bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM task WHERE lane_id = $1 AND status = 'queued' AND kind <> 'question')`, req.LaneID).Scan(&queued); err != nil {
				return Result{}, err
			}
			if queued {
				// Another task already waits on the lane (a revise, a person's
				// word — insertQueuedTask makes it a card task while the card
				// is open): it is the next card turn; no follow-up of our own.
				break
			}
			if c.FollowUps < cards.MaxFollowUps {
				id, err := queueFollowUp(ctx, tx, req.TaskID, c, req.Hub, req.Now)
				if err != nil {
					return Result{}, err
				}
				var status string
				if err := tx.QueryRow(ctx, `UPDATE lane SET status = 'queued', finished_at = NULL, updated_at = $2 WHERE id = $1 RETURNING status::text`,
					req.LaneID, req.Now).Scan(&status); err != nil {
					return Result{}, err
				}
				if req.Publish != nil {
					req.Publish(ctx, tx, req.LaneID)
				}
				cards.Publish(ctx, req.Hub, tx, c.ID, "card.updated")
				return Result{Status: status, Prev: prev, Gate: GateFollowUp, FollowUpTaskID: id}, nil
			}
			// Two follow-ups passed without a result: the automatic result
			// card, then the lane ends like any other.
			msgID, err := cards.StoreResult(ctx, tx, c, cards.Auto(len(c.Criteria)), &req.TaskID, req.Now)
			if err != nil {
				return Result{}, err
			}
			publishMessage(ctx, req.Hub, tx, c.RoomID, msgID)
			cards.Publish(ctx, req.Hub, tx, c.ID, "card.updated")
			gate = GateAuto
		}
	}

	var status string
	switch req.Cause {
	case AgentDone:
		err = tx.QueryRow(ctx, `
			UPDATE lane SET status = 'done', finished_at = $2, updated_at = $2 WHERE id = $1
			RETURNING status::text`, req.LaneID, req.Now).Scan(&status)
	case TurnEnd:
		// Another queued task on this lane keeps it queued, else done (a
		// queued QUESTION does not count — it will not move the lane, and the
		// lane's end must still run its join, PRD FR-3.8 2). A lane
		// the agent put in `blocked` keeps that status: the turn ending is
		// exactly what `colab status set blocked` asked for, and overwriting
		// it with `done` loses the question the delegator has yet to answer
		// (FR-6.2.1).
		err = tx.QueryRow(ctx, `
			UPDATE lane SET status = CASE WHEN EXISTS (SELECT 1 FROM task WHERE lane_id = $1 AND status = 'queued' AND kind <> 'question') THEN 'queued'::lane_status ELSE 'done'::lane_status END,
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
	res := Result{Status: status, Prev: prev, Gate: gate}
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

// queueFollowUp is the gate's `result_card_missing` card task: same lane,
// same agent and profile, same mission and originator as the task that ended,
// hanging off a system line that says what happened (the turn prompt renders
// the trigger from the reason instead — harness v0.9.16).
func queueFollowUp(ctx context.Context, tx pgx.Tx, ended uuid.UUID, c *cards.Row, hub *realtime.Hub, now time.Time) (uuid.UUID, error) {
	if _, err := tx.Exec(ctx, `UPDATE task_card SET follow_ups = follow_ups + 1, updated_at = $2 WHERE id = $1`, c.ID, now); err != nil {
		return uuid.Nil, fmt.Errorf("lanedone: follow-up count: %w", err)
	}
	c.FollowUps++
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, created_at, work_id)
		VALUES ($1, 'system', NULL, $2, 'system', $3, $4) RETURNING id`,
		c.RoomID, cards.FollowUpMessage(c, c.FollowUps), now, c.WorkID).Scan(&msgID); err != nil {
		return uuid.Nil, fmt.Errorf("lanedone: follow-up line: %w", err)
	}
	if err := messages.Store(ctx, tx, msgID, messages.StoreOpts{}); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, delegated_from_task_id, originator_user_id,
		                  coalesced_message_ids, status, created_at, updated_at, work_id, kind, card_id, trigger_reason)
		SELECT lane_id, session_id, agent_id, profile_id, $2, delegated_from_task_id, originator_user_id,
		       ARRAY[$2]::uuid[], 'queued', $3, $3, work_id, 'card', $4, 'result_card_missing'
		FROM task WHERE id = $1 RETURNING id`, ended, msgID, now, c.ID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("lanedone: follow-up task: %w", err)
	}
	publishMessage(ctx, hub, tx, c.RoomID, msgID)
	return id, nil
}

func publishMessage(ctx context.Context, hub *realtime.Hub, tx pgx.Tx, roomID, msgID uuid.UUID) {
	if hub == nil {
		return
	}
	var ws uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT workspace_id FROM room WHERE id = $1`, roomID).Scan(&ws); err != nil {
		return
	}
	_ = messages.Publish(ctx, hub, tx, ws, roomID, msgID)
}

// EndQuestion is a question task's end, whichever way it ended (PRD FR-3.8 2
// 「질문은 lane 과 카드를 바꾸지 않는다 — 끝난 lane 은 끝난 채, 도는 lane 은
// 도는 채」). A question never moved the lane: routing does not re-enter it
// (no reentry_count, no `queued`), dispatch does not make it `running`
// (tasks.MarkDispatched), and its failure or cancel does not fail it. So a
// lane that existed is left exactly as it is, and nothing after-done runs (a
// question is not a re-entry). The one lane a question does touch is one it
// made itself (the asked agent had none): that lane was born `queued` and has
// nothing else to wait for, so it ends `done` — as the answer's lane would
// have. The one change a question turn may make is its own `status set
// blocked` (the question table allows it) — that is the agent's word, kept.
func EndQuestion(ctx context.Context, tx pgx.Tx, taskID, laneID uuid.UUID, now time.Time) (string, error) {
	var cur string
	var others bool
	if err := tx.QueryRow(ctx, `
		SELECT l.status::text, EXISTS (SELECT 1 FROM task x WHERE x.lane_id = l.id AND x.id <> $2 AND x.kind <> 'question')
		FROM lane l WHERE l.id = $1`, laneID, taskID).Scan(&cur, &others); err != nil {
		return "", fmt.Errorf("lanedone: question lane: %w", err)
	}
	if st, ok := PlanQuestionEnd(cur, others); ok {
		if _, err := tx.Exec(ctx, `UPDATE lane SET status = $2::lane_status, finished_at = $3, updated_at = $3 WHERE id = $1`, laneID, st, now); err != nil {
			return "", fmt.Errorf("lanedone: question lane end: %w", err)
		}
		return st, nil
	}
	return cur, nil
}

// PlanQuestionEnd is EndQuestion's decision: only a lane the question made
// (no other task has ever run on it) and still `queued` changes — to `done`.
func PlanQuestionEnd(cur string, otherTasks bool) (string, bool) {
	if !otherTasks && cur == lanestate.Queued {
		return lanestate.Done, true
	}
	return cur, false
}
