package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/lanedone"
	"github.com/ingki3/agent-collabortion/server/internal/lanefocus"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/quiet"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// StatusResult is `colab status set …`'s answer (contracts/openapi.yaml
// setTaskStatus).
type StatusResult struct {
	TaskID uuid.UUID
	LaneID uuid.UUID
	// TurnEndRequired is the server telling the agent to end its turn. It is
	// deliberately NOT called end_turn: ACP's stopReason `end_turn` is the
	// report that a turn ENDED, and one grep must not return both (the P1
	// kind ↔ runtime_kind collision failed every finish for the same reason).
	TurnEndRequired   bool
	QuestionMessageID *uuid.UUID
	// DelegatorAgentID is who `blocked` woke, or nil when there was no
	// delegator and the question went to the Director instead. It is returned
	// so the caller can name the escalation path it took (E7-19).
	DelegatorAgentID *uuid.UUID
}

// SetAgentStatus is FR-7.4's `colab status set working|blocked|done`.
//
// `blocked` and `done` are the two server-side hinges of the delegation model:
// blocked is the ONLY way a child can ask its delegator a question while rule
// 8 suppresses its mentions (FR-6.2.1), and done is what makes a join group
// fire (FR-6.5). Neither can be left to the prompt — §8.3's conventions are
// advisory and a broken one loses the question or the result entirely.
func (s *Service) SetAgentStatus(ctx context.Context, taskID uuid.UUID, attempt int, status, note string) (*StatusResult, error) {
	now := s.Clock.Now()
	if status == "blocked" && note == "" {
		return nil, apperr.Validation(apperr.Field("note", "required",
			"막힘 상태에는 질문을 함께 적어 주세요 — 위임한 쪽이 답할 것이 없습니다"))
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tc, err := lockTaskCtx(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	laneID, sessionID, agentID, wsID := tc.laneID, tc.sessionID, tc.agentID, tc.wsID
	triggerMsg, reentry, director := tc.triggerMsg, tc.reentry, tc.director
	out := &StatusResult{TaskID: taskID, LaneID: laneID}

	if err := s.recordStatusEvent(ctx, tx, taskID, attempt, status, note, now); err != nil {
		return nil, err
	}

	switch status {
	case "working":
		var prev, taskStatus, kind string
		if err := tx.QueryRow(ctx, `SELECT l.status::text, t.status::text, t.kind FROM lane l JOIN task t ON t.lane_id = l.id WHERE t.id = $1`, taskID).
			Scan(&prev, &taskStatus, &kind); err != nil {
			return nil, err
		}
		if kind == cards.KindQuestion {
			// PRD FR-3.8 2: a question's turn does not move its lane. The
			// declaration is on the feed (above) and nothing else changes.
			break
		}
		if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'running', updated_at = $2 WHERE id = $1`, laneID, now); err != nil {
			return nil, err
		}
		publish := prev != "running"
		// PRD FR-3.1.5 · openapi v0.3.8: `working --note` is the lane's
		// 「지금」 (source = agent). Only while this task's turn runs — the
		// end of the turn empties it, and a sentence stored after that would
		// never be emptied. Every declaration is on the feed (above); only the
		// frames coalesce: the last one inside 60 seconds goes out when the
		// window closes (flushFocus).
		if note != "" && (taskStatus == "dispatched" || taskStatus == "preparing" || taskStatus == "running") {
			d, err := lanefocus.Declare(ctx, tx, laneID, note, now)
			if err != nil {
				return nil, err
			}
			switch {
			case d.PublishNow:
				publish = true
			case publish:
				if err := lanefocus.MarkPublished(ctx, tx, laneID, now); err != nil {
					return nil, err
				}
			default:
				s.scheduleFocusFlush(laneID, d.FlushAt)
			}
		}
		if publish {
			s.publishLane(ctx, tx, laneID)
		}
	case "blocked":
		delegator, delegatorName, delegatorTask, err := delegatorOfLane(ctx, tx, laneID)
		if err != nil {
			return nil, err
		}
		plan := PlanBlocked(delegator, note, uuid.New)
		// S-27: the card names WHO is being asked. The web K3 badge reads
		// message.mentions to render `질문 → @위임자` and fell back to a bare
		// `질문` because the server posted the card with no mentions at all.
		//
		// This mention triggers nobody: the card is inserted here rather than
		// through Post, so FR-3.3 never runs on it and the delegator's single
		// wake-up below stays the only trigger (openapi setTaskStatus:
		// "위임자를 즉시 깨운다(합류 아님)", once).
		content, mentions := note, []gen.Mention{}
		if delegator != nil {
			content = note + "\n\n" + MentionLink(delegatorName, *delegator)
			mentions = ParseMentions(content)
		}
		// The card IS the thread root: a status change alone gives the
		// delegator nothing to reply to (리뷰#04-2). lane.blocked_note is only
		// the last-value cache; the history lives in these messages.
		if _, err := tx.Exec(ctx, `
			INSERT INTO message (id, session_id, author_type, author_id, content, mentions, source_task_id, kind, created_at)
			VALUES ($1, $2, 'agent', $3, $4, $5, $6, 'blocked_q', $7)`,
			plan.QuestionCardID, sessionID, agentID, content, mentions, taskID, now); err != nil {
			return nil, fmt.Errorf("router: blocked question card: %w", err)
		}
		if err := messages.Store(ctx, tx, plan.QuestionCardID, messages.StoreOpts{}); err != nil {
			return nil, err
		}
		// The card is a message, so the timeline hears about it now rather than
		// on the delegator's next reload (G4 2판 W10).
		s.publishMessage(ctx, tx, sessionID, plan.QuestionCardID)
		if _, err := tx.Exec(ctx, `
			UPDATE lane SET status = 'blocked', blocked_note = $2, blocked_message_id = $3, finished_at = $4, updated_at = $4
			WHERE id = $1`, laneID, note, plan.QuestionCardID, now); err != nil {
			return nil, err
		}
		s.publishLane(ctx, tx, laneID)
		qid := plan.QuestionCardID
		out.QuestionMessageID, out.TurnEndRequired = &qid, true

		if plan.DelegatorWoken {
			out.DelegatorAgentID = plan.DelegatorAgentID
			// Immediate, not via the join: a question raised at minute 2 must
			// not arrive forty minutes later behind the slowest sibling.
			childName, err := agentDisplayName(ctx, tx, agentID)
			if err != nil {
				return nil, err
			}
			if err := s.wake(ctx, tx, sessionID, wsID, director, agentID, *plan.DelegatorAgentID, taskID, delegatorTask, qid,
				wakeOnBlocked(qid, note, childName, agentID), now); err != nil {
				return nil, err
			}
		} else if director != nil {
			// 리뷰#04-3: a lane the Director created by mentioning an agent has
			// no delegator to wake, so the question goes to the inbox.
			if err := insertInbox(ctx, tx, wsID, *director, inbox.TypeLaneBlocked, inbox.Severity(inbox.TypeLaneBlocked), sessionID, qid, now); err != nil {
				return nil, err
			}
		}
	case "done":
		// T-RF1: the one writer of a finished lane (lanedone.MarkDone — the
		// 작업 카드 gate goes there). The frame goes out before the follow-up,
		// as it always did.
		// AfterDone runs only when this call moves the lane INTO `done`: a
		// lane the turn's end already closed (tasks.Finish → LaneEnded) has
		// had its join/report, and a second `status set done` adds nothing.
		_, err := lanedone.MarkDone(ctx, tx, lanedone.Request{
			LaneID: laneID, TaskID: taskID, Cause: lanedone.AgentDone, Now: now, Hub: s.Hub,
			Publish: func(ctx context.Context, tx pgx.Tx, id uuid.UUID) { s.publishLane(ctx, tx, id) },
			AfterDone: func(ctx context.Context, tx pgx.Tx) error {
				return s.afterLaneDone(ctx, tx, sessionID, wsID, laneID, agentID, taskID, triggerMsg, reentry, director, now)
			},
		})
		switch {
		case errors.Is(err, lanedone.ErrResultCardRequired):
			// NN2 (#393 review) closed: the CARD GATE refused — nothing was
			// written and the turn is alive, so the agent is told to submit the
			// result card, NOT to end its turn (turn_end_required false). The
			// refusal is on the feed like any other.
			_ = tx.Rollback(ctx)
			s.noteRefusedDone(ctx, taskID, attempt, now)
			p := apperr.Conflict("result_card_required", cards.ResultCardRequiredSentence)
			p.Extra = map[string]any{"turn_end_required": false}
			return nil, p
		case errors.Is(err, lanedone.ErrQuestionDone):
			_ = tx.Rollback(ctx)
			return nil, apperr.Forbidden("command_not_allowed", cards.QuestionRefusal("status set done"))
		case err != nil:
			return nil, err
		}
		out.TurnEndRequired = true
	default:
		return nil, apperr.Validation(apperr.Field("status", "invalid", "상태는 working · blocked · done 중 하나여야 합니다"))
	}

	if _, err := tx.Exec(ctx, `UPDATE room SET updated_at = $2 WHERE id = $1`, sessionID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if s.Notifier != nil {
		s.Notifier.Notify()
	}
	return out, nil
}

// taskCtx is what a lane's end needs to know about the task that ended it.
type taskCtx struct {
	laneID, sessionID, agentID, wsID uuid.UUID
	triggerMsg                       *uuid.UUID
	reentry                          int
	director                         *uuid.UUID
	laneStatus                       string
}

// lockTaskCtx reads (and locks: task, then lane — the order SetAgentStatus
// always took) the task and its lane.
func lockTaskCtx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID) (taskCtx, error) {
	var c taskCtx
	err := tx.QueryRow(ctx, `
		SELECT t.lane_id, t.session_id, t.agent_id, s.workspace_id, t.trigger_message_id, l.reentry_count,
		       -- FR-6.2.1 v0.19: the lane's OWN mission's Director, and the room
		       -- owner for a lane outside any mission (V19_R1B_HANDOFF (c)
		       -- router/status.go:61).
		       COALESCE(wk.director_user_id, s.owner_user_id), l.status::text
		FROM task t JOIN lane l ON l.id = t.lane_id JOIN room s ON s.id = t.session_id
		LEFT JOIN work wk ON wk.id = COALESCE(l.work_id, t.work_id)
		WHERE t.id = $1 FOR UPDATE OF t, l`, taskID).
		Scan(&c.laneID, &c.sessionID, &c.agentID, &c.wsID, &c.triggerMsg, &c.reentry, &c.director, &c.laneStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, tasks.ErrNotFound
	}
	return c, err
}

// AfterLaneEnded is tasks.Service.LaneEnded (wired in httpapi.NewServer): the
// follow-up a lane's end owes when the end was NOT `colab status set done`
// (T-FIX-B, PRD FR-6.5 · colab-cli.md §2 「lane 종료 판정은 서버가
// turn_end 와 함께 한다」).
//
//   - `done` — the turn's end wrote it (lanedone.TurnEnd moved it INTO done;
//     a lane the agent had already closed never gets here): afterLaneDone,
//     exactly what `status set done` runs — the join or the re-entry report.
//   - `failed` — a cancel, a last failure or the sweep: the join only
//     (「그룹의 모든 자식 lane이 종료 상태(done 또는 failed)가 되면」). A
//     failed lane is not a finished piece of work; nobody is told it is done.
//
// Its own transaction, after the one that ended the lane committed. The lane
// is re-read under its lock: a `failed` end whose lane is not `failed` (K-16:
// a cancel on a lane already `done` keeps it done — its join already ran)
// does nothing. The join itself is once per group (join_fired_at under the
// delegating task's lock), so a sibling's `status set done` racing this lands
// one bundle.
func (s *Service) AfterLaneEnded(ctx context.Context, e tasks.LaneEnd) error {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, err := lockTaskCtx(ctx, tx, e.TaskID)
	if errors.Is(err, tasks.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch e.Status {
	case lanestate.Done:
		if err := s.afterLaneDone(ctx, tx, c.sessionID, c.wsID, c.laneID, c.agentID, e.TaskID, c.triggerMsg, c.reentry, c.director, now); err != nil {
			return err
		}
	case lanestate.Failed:
		if c.laneStatus != lanestate.Failed {
			return nil
		}
		var delegTask *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT delegated_from_task_id FROM lane WHERE id = $1`, c.laneID).Scan(&delegTask); err != nil {
			return err
		}
		if delegTask == nil {
			return nil
		}
		if _, err := s.maybeFireJoin(ctx, tx, c.sessionID, c.wsID, c.director, c.agentID, e.TaskID, *delegTask, now); err != nil {
			return err
		}
	default:
		return fmt.Errorf("router: lane end %q", e.Status)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.Notifier != nil {
		s.Notifier.Notify()
	}
	return nil
}

// Join recovery window (#396 review NN1). A group becomes a candidate only
// after its last child has been ended for joinRecoverGrace — the lane end's
// own follow-up runs right after the finish commits and normally fires the
// join first, so the sweep only speaks for a follow-up that did not happen —
// and only for joinRecoverLookback: a deploy must not wake delegators for
// groups that ended days ago under the old code (T-RF1-B left such groups
// behind on purpose; they are not a lost follow-up).
//
// #396 re-review NN3 — the window is also the limit of what is recovered: a
// server that stays down longer than joinRecoverLookback after a child's last
// end loses that group's join for good (a hook that errored leaves a
// lane_end.followup_failed row; a dead process leaves nothing). That is the
// price of not waking delegators for days-old groups on a deploy; the
// Director still sees the group ended on the board, and a person's message
// wakes the delegator. Operations: docs in server/README (복구 창 1시간).
const (
	joinRecoverGrace    = 30 * time.Second
	joinRecoverLookback = time.Hour
)

// RecoverJoins is tasks.Service.RecoverJoins (wired in httpapi.NewServer): it
// fires the join (FR-6.5) of every delegation group whose children have all
// ended `done` or `failed` but whose join never fired. That is the state a
// lost lane-end follow-up leaves — LaneEnded returned an error, or the
// process died between the finish's commit and the hook — and nothing else
// would ever fire it: a repeat finish takes the idempotent branch and a late
// `status set done` finds the lane already done (Result.Became is false).
//
// Groups with a `blocked` child are left alone. `blocked` does not ask for
// the join (the delegator was woken at once with the question — FR-6.2.1), and
// the group completes through the answer's re-entry. Firing it here would be
// a new behaviour, not a recovery (TestLaneDonePathTurnEndKeepsBlocked).
//
// Idempotent and race-safe through the same lock the lane-end path takes:
// each group is judged in its own transaction by maybeFireJoin, which locks
// the delegating task FOR UPDATE and returns if join_fired_at is set. A hook
// that runs late, a second server's sweep, or two sweeps in a row land one
// bundle. Lock order is the hook's: the last child's task and lane
// (lockTaskCtx), then the delegating task.
//
// Each recovered join writes lane_end.join_recovered on the delegating task
// (tasks.LaneEndJoinRecovered) — the count of windows that actually opened.
// Returns how many it fired.
func (s *Service) RecoverJoins(ctx context.Context, now time.Time) (int, error) {
	// #396 re-review NN2: start from the window, not from every delegating
	// task. The candidates are the groups that have a child which ENDED inside
	// [now-lookback, now-grace] (lane_finished_delegated, migration 0046);
	// only those are then judged. A pile of old groups whose join_fired_at
	// stays NULL on purpose (T-RF1-B) no longer costs a scan every 10s.
	rows, err := s.DB.Query(ctx, `
		WITH w AS (
		  SELECT DISTINCT c.delegated_from_task_id AS id FROM lane c
		  WHERE c.delegated_from_task_id IS NOT NULL
		    AND c.finished_at BETWEEN $1::timestamptz - $3::interval AND $1::timestamptz - $2::interval)
		SELECT d.id,
		       (SELECT t.id FROM lane c JOIN task t ON t.lane_id = c.id
		         WHERE c.delegated_from_task_id = d.id ORDER BY c.finished_at DESC, t.created_at DESC LIMIT 1)
		FROM w JOIN task d ON d.id = w.id
		LEFT JOIN work wk ON wk.id = d.work_id
		WHERE d.join_fired_at IS NULL
		  AND COALESCE(wk.status::text, 'active') NOT IN ('completed', 'cancelled')
		  AND NOT EXISTS (SELECT 1 FROM lane c WHERE c.delegated_from_task_id = d.id
		                    AND (c.status NOT IN ('done', 'failed') OR c.finished_at IS NULL))
		  AND (SELECT max(c.finished_at) FROM lane c WHERE c.delegated_from_task_id = d.id)
		        BETWEEN $1::timestamptz - $3::interval AND $1::timestamptz - $2::interval
		ORDER BY d.id`, now, joinRecoverGrace.String(), joinRecoverLookback.String())
	if err != nil {
		return 0, fmt.Errorf("router: recover joins: %w", err)
	}
	type cand struct{ deleg, last uuid.UUID }
	var cands []cand
	for rows.Next() {
		var c cand
		var last *uuid.UUID
		if err := rows.Scan(&c.deleg, &last); err != nil {
			rows.Close()
			return 0, err
		}
		if last != nil {
			c.last = *last
			cands = append(cands, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	fired := 0
	for _, c := range cands {
		ok, err := s.recoverJoin(ctx, c.deleg, c.last, now)
		if err != nil {
			return fired, err
		}
		if ok {
			fired++
		}
	}
	if fired > 0 && s.Notifier != nil {
		s.Notifier.Notify()
	}
	return fired, nil
}

// recoverJoin is one group of RecoverJoins, in its own transaction. It reports
// whether THIS call fired the join (false: someone else got there first, or
// the group is no longer complete).
func (s *Service) recoverJoin(ctx context.Context, delegTask, lastChildTask uuid.UUID, now time.Time) (bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, err := lockTaskCtx(ctx, tx, lastChildTask)
	if errors.Is(err, tasks.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// #396 re-review NN1 · NN4: whether THIS call fired is maybeFireJoin's
	// own answer under the delegating task's FOR UPDATE — not "join_fired_at
	// is set afterwards", which a sibling's late hook firing between an early
	// check and the lock also makes true (a false positive in the count and
	// in join_recovered). No early check is needed for correctness either.
	fired, err := s.maybeFireJoin(ctx, tx, c.sessionID, c.wsID, c.director, c.agentID, lastChildTask, delegTask, now)
	if err != nil {
		return false, err
	}
	if !fired {
		return false, nil // someone else fired it, or a child is no longer ended (re-entered)
	}
	var attempt int
	if err := tx.QueryRow(ctx, `SELECT attempt FROM task WHERE id = $1`, delegTask).Scan(&attempt); err != nil {
		return false, err
	}
	if attempt < 1 {
		attempt = 1
	}
	if err := tasks.InsertServerEventOnce(ctx, tx, delegTask, attempt, "runtime", "report", tasks.LaneEndJoinRecovered, "info",
		map[string]any{"detail": "맡긴 서브 미션이 모두 끝났는데 합류가 나가지 않아 서버가 다시 보냈습니다"}, now); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// afterLaneDone is FR-6.5. Two different notifications hang off one event, and
// they are not interchangeable:
//
//	the JOIN fires once per delegation group, when every child has ended;
//	a RE-ENTRY completion tells whoever asked for the re-entry, which is
//	usually not the delegator (scenario B: QA asked, Lead delegated).
func (s *Service) afterLaneDone(ctx context.Context, tx pgx.Tx, sessionID, wsID, laneID, agentID, taskID uuid.UUID, triggerMsg *uuid.UUID, reentry int, director *uuid.UUID, now time.Time) error {
	var delegTask *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT delegated_from_task_id FROM lane WHERE id = $1`, laneID).Scan(&delegTask); err != nil {
		return err
	}
	// S-31: these two questions are independent and used to share one branch.
	// `reentry > 0` returned early, so a re-entered child that ended LAST in
	// its delegation group completed the group without anyone asking whether
	// the group was complete — join_fired_at stayed null and FR-6.5's bundle
	// was lost silently. It is the easiest order to hit, because E3-05 exists
	// to make the delegator answer the question FAST.
	if reentry > 0 || delegTask == nil {
		// The re-entry's author gets told. Leaving it to the agent's prompt
		// means QA never learns Frontend produced a new diff (리뷰#04-5).
		// A lane that is not a delegation has nobody waiting for a bundle
		// either, so it takes the same path.
		if err := s.notifyReentry(ctx, tx, sessionID, wsID, laneID, agentID, taskID, triggerMsg, director, now); err != nil {
			return err
		}
	}
	if delegTask == nil {
		return nil
	}
	// Both notices can land on the same delegator. That is not two turns:
	// wake() coalesces onto the lane's queued task (FR-3.4), so the delegator
	// wakes once with both messages.
	_, err := s.maybeFireJoin(ctx, tx, sessionID, wsID, director, agentID, taskID, *delegTask, now)
	return err
}

// maybeFireJoin fires the join exactly once per group and reports whether THIS
// call fired it (#396 re-review NN4). `blocked` children count
// as ended (FR-6.2.1) — treating them as in progress would let one question
// hold every sibling's result hostage.
//
// `from` is the child whose end completed the group: the join notice is an
// agent→agent hop from that child to the delegator (S-76), and the pair it
// forms with the delegation that created the child is what
// max_pair_roundtrips counts.
func (s *Service) maybeFireJoin(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID, director *uuid.UUID, from, waker, delegTask uuid.UUID, now time.Time) (bool, error) {
	var delegAgent uuid.UUID
	var fired *time.Time
	if err := tx.QueryRow(ctx, `SELECT agent_id, join_fired_at FROM task WHERE id = $1 FOR UPDATE`, delegTask).
		Scan(&delegAgent, &fired); err != nil {
		return false, err
	}
	if fired != nil {
		return false, nil // one bundle per group (E); a re-entry does not re-fire it
	}
	// FR-6.5 「실패한 lane은 실패 사유와 함께 묶음에 포함된다」: the reason is
	// the failure_kind of the lane's newest task.
	rows, err := tx.Query(ctx, `
		SELECT l.status::text, a.name, l.blocked_note,
		       CASE WHEN l.status = 'failed' THEN (SELECT t.failure_kind::text FROM task t WHERE t.lane_id = l.id ORDER BY t.created_at DESC LIMIT 1) END
		FROM lane l JOIN agent a ON a.id = l.agent_id
		WHERE l.delegated_from_task_id = $1 ORDER BY l.created_at`, delegTask)
	if err != nil {
		return false, err
	}
	type child struct {
		status, name string
		note, reason *string
	}
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.status, &c.name, &c.note, &c.reason); err != nil {
			rows.Close()
			return false, err
		}
		children = append(children, c)
	}
	rows.Close()
	unanswered := 0
	for _, c := range children {
		if !lanestate.Terminal(c.status) {
			return false, nil // still running, waiting_human or paused — the group waits
		}
		if c.status == lanestate.Blocked {
			unanswered++
		}
	}
	if len(children) == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE task SET join_fired_at = $2, updated_at = $2 WHERE id = $1`, delegTask, now); err != nil {
		return false, err
	}
	body := "위임한 작업이 모두 끝났습니다.\n"
	for _, c := range children {
		body += "- " + c.name + ": " + c.status
		if c.note != nil && *c.note != "" {
			body += " — 질문: " + *c.note
		}
		if c.reason != nil && *c.reason != "" {
			body += " — 사유: " + *c.reason
		}
		body += "\n"
	}
	if unanswered > 0 {
		// t-8: without this line a delegator that missed the immediate notice
		// just calls `status set done` and the question dies with the group.
		body += fmt.Sprintf("\n답을 기다리는 자식 %d개가 있습니다. 답하기 전에 작업을 종료하지 마세요.\n", unanswered)
	}
	// PRD FR-3.8 4 · FR-6.5 v0.19.15: a card lane's bundle carries its result
	// card — one line here, the whole card in the delegator's <result_cards>.
	resultCards, lines, err := groupResultCards(ctx, tx, delegTask)
	if err != nil {
		return false, err
	}
	if lines != "" {
		body += "\n결과 카드:\n" + lines
	}
	msgID, err := s.SystemPost(ctx, tx, sessionID, body)
	if err != nil {
		return false, err
	}
	woken, err := s.wakeTask(ctx, tx, sessionID, wsID, director, from, delegAgent, waker, delegTask, msgID, "", now)
	if err != nil {
		return false, err
	}
	if err := attachResultCards(ctx, tx, woken, resultCards); err != nil {
		return false, err
	}
	return true, nil
}

// notifyReentry tells whoever caused the work that it is finished. A human
// author gets an inbox item; an agent author gets a task — unless the lane's
// own messages already woke that agent (requesterAlreadyWoken, T-FIX-B).
func (s *Service) notifyReentry(ctx context.Context, tx pgx.Tx, sessionID, wsID, laneID, from, waker uuid.UUID, triggerMsg *uuid.UUID, director *uuid.UUID, now time.Time) error {
	if triggerMsg == nil {
		return nil
	}
	var authorType string
	var authorID, authorTask *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT author_type::text, author_id, source_task_id FROM message WHERE id = $1`, *triggerMsg).
		Scan(&authorType, &authorID, &authorTask)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	switch {
	case authorType == "agent" && authorID != nil:
		requester := uuid.Nil
		if authorTask != nil {
			requester = *authorTask
		}
		if woken, err := requesterAlreadyWoken(ctx, tx, laneID, *authorID, *triggerMsg); err != nil {
			return err
		} else if woken {
			return nil
		}
		prefix := "요청하신 작업이 끝났습니다."
		var resultCards []uuid.UUID
		if c, err := cards.OfLane(ctx, tx, laneID); err != nil {
			return err
		} else if c != nil && c.Status == cards.ResultSubmitted && c.DelegatorID == *authorID {
			// A revise's re-entry ended with a new result: the delegator
			// judges it again (<result_cards>).
			if res := cards.ParseResult(c.Result); res != nil {
				prefix += fmt.Sprintf("\n결과 카드: %s v%d — 기준 %d/%d 충족", c.Label(), c.Version, res.MetCount, len(c.Criteria))
			}
			resultCards = []uuid.UUID{c.ID}
		}
		woken, err := s.wakeTask(ctx, tx, sessionID, wsID, director, from, *authorID, waker, requester, *triggerMsg, prefix, now)
		if err != nil {
			return err
		}
		return attachResultCards(ctx, tx, woken, resultCards)
	case authorType == "user" && authorID != nil:
		return insertInbox(ctx, tx, wsID, *authorID, inbox.TypeMention, inbox.Severity(inbox.TypeMention), sessionID, *triggerMsg, now)
	case director != nil:
		return insertInbox(ctx, tx, wsID, *director, inbox.TypeMention, inbox.Severity(inbox.TypeMention), sessionID, *triggerMsg, now)
	}
	return nil
}

// requesterAlreadyWoken is the one rule for "the report would be a duplicate
// wake-up" (T-FIX-B, Lead 결정 B 2026-09-30), used by both ways a lane ends
// (`status set done` and the turn's end — both reach notifyReentry).
//
// The lane's own tasks answering this request — those created at or after the
// request message — already posted a message that (1) names the requester
// among its addressees and (2) woke it: a task of the requester has that
// message as its trigger or merged it (FR-3.4). Then the requester is already
// coming back with the answer in hand, and 「요청하신 작업이 끝났습니다」 would
// wake it a second time for nothing (live copy 09-30: 84 of 92 agent-asked
// turn-end ends, 44 of 49 `status set done` ends).
//
// A message that reached the requester but did NOT wake it does not count,
// and the report goes out: a mention rule 8 or the loop limit suppressed makes
// no task at all, and a trigger T-QUIET held (queued · approval_pending, or
// later cancelled · approval_closed) is not a wake-up either.
func requesterAlreadyWoken(ctx context.Context, tx pgx.Tx, laneID, requester, request uuid.UUID) (bool, error) {
	var woken bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM message req, task own JOIN message m ON m.source_task_id = own.id
		  JOIN task t ON t.agent_id = $2 AND (t.trigger_message_id = m.id OR m.id = ANY (t.coalesced_message_ids))
		  WHERE req.id = $3 AND own.lane_id = $1 AND own.created_at >= req.created_at
		    AND m.addressees @> jsonb_build_array(jsonb_build_object('kind', 'agent', 'id', $2::text))
		    AND NOT `+quiet.HeldTaskSQL+`
		    AND NOT (t.status = 'cancelled' AND t.stop_reason IS NOT DISTINCT FROM '`+quiet.StopApprovedClosed+`'))`,
		laneID, requester, request).Scan(&woken)
	if err != nil {
		return false, fmt.Errorf("router: requester already woken: %w", err)
	}
	return woken, nil
}

// wake creates a task for one agent on its own lane. It is the server's own
// trigger, so it bypasses the mention rules — the point of FR-6.2.1 and FR-6.5
// is that these wake-ups are deterministic rather than prompt-dependent.
//
// It does NOT bypass FR-3.5 (S-76). `from` is the agent whose lane ending (or
// question) causes the wake-up; the notice is a hop from it to `agentID`, and
// it is gated like a mention would be. Without this a delegator that answers
// every join by delegating again is a loop the limiter never sees: no message
// in the cycle carries a mention. On a trip the notice is still posted — the
// timeline says what happened — but no task is made and the session pauses.
//
// `requester` is the task of `agentID` that asked for the work now ending —
// the delegating task, or the one that wrote the mention. The notice's hop
// takes that task's own cause (S-78), so the requester wakes at its OWN chain
// depth: a join is Lead coming back, not Lead one step below its child. Nil
// when nothing is known, which chainDepth reads as "no cause".
//
// `waker` is the task whose status change causes the wake-up (the child that
// asked, ended, or ended the re-entry). The woken task inherits ITS person
// originator (PRD FR-4.5 [V19-B], NN7): this INSERT used to leave the column
// out, so a Lead woken by a join — the turn that most needs another room's
// context — had no originator and every room read was refused. When the waker
// has none either, the requester's is next: it is the same chain of asking.
// Never the room owner or the Director — that is the escalation FR-4.5 exists
// to stop, and a NULL here is answered `no_originator`, not papered over.
func (s *Service) wake(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID, director *uuid.UUID, from, agentID, waker, requester, triggerMsg uuid.UUID, prefix string, now time.Time) error {
	_, err := s.wakeTask(ctx, tx, sessionID, wsID, director, from, agentID, waker, requester, triggerMsg, prefix, now)
	return err
}

// wakeTask is wake, answering the task that will carry the wake-up (the
// lane's queued task it merged into, or the new one; uuid.Nil when nothing was
// woken — not a participant, or the loop limit stopped it). The join and the
// re-entry report hang the result cards on it (<result_cards>, Lead 판정 Q4).
func (s *Service) wakeTask(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID, director *uuid.UUID, from, agentID, waker, requester, triggerMsg uuid.UUID, prefix string, now time.Time) (uuid.UUID, error) {
	var profileID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT profile_id FROM room_participant WHERE room_id = $1 AND agent_id = $2`, sessionID, agentID).Scan(&profileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil // no longer a participant: nothing to wake
	}
	if err != nil {
		return uuid.Nil, err
	}
	msg := triggerMsg
	if prefix != "" {
		id, err := s.SystemPost(ctx, tx, sessionID, prefix)
		if err != nil {
			return uuid.Nil, err
		}
		msg = id
	}
	var cause int64
	if requester != uuid.Nil {
		if _, cause, err = causeOfTask(ctx, tx, requester); err != nil {
			return uuid.Nil, err
		}
	}
	v, err := s.judgeHop(ctx, tx, sessionID, wsID, Hop{FromAgent: from, ToAgent: agentID, At: now, CauseID: cause}, msg, RulePlatform, now)
	if err != nil {
		return uuid.Nil, err
	}
	if !v.Allowed {
		// The wake-up is dropped and the session stops here; the notice
		// message stays on the timeline. Nobody is answered with a Problem —
		// this is the server's own trigger, and the pause's card is the word.
		return uuid.Nil, s.pauseForLoop(ctx, tx, sessionID, wsID, v, now)
	}
	// FR-3.1.1: the wake-up runs for the mission of the work that asked for
	// it — the requester's own lane's — and lands on a lane of that mission
	// (resolveLaneFor's candidates, T-R1b2).
	var requesterWork *uuid.UUID
	if requester != uuid.Nil {
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(l.work_id, t.work_id) FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1`, requester).
			Scan(&requesterWork); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	laneID, _, err := s.resolveLaneFor(ctx, tx, sessionID, Trigger{AgentID: agentID, Rule: 0}, profileID,
		laneOpts{topLevelMent: true, work: requesterWork}, now)
	if err != nil {
		return uuid.Nil, err
	}
	originator, err := inheritedOriginator(ctx, tx, waker, requester)
	if err != nil {
		return uuid.Nil, err
	}
	laneWork, err := bindLaneWork(ctx, tx, laneID, requesterWork)
	if err != nil {
		return uuid.Nil, err
	}
	existing, ok, err := lockQueuedTask(ctx, tx, laneID)
	if err != nil {
		return uuid.Nil, err
	}
	if ok {
		// A queued task that absorbs the notice keeps its own originator; one
		// that had none takes the waker's (the turn it will run is this one).
		// Its mission likewise: kept, else the lane's (T-R4b).
		_, err = tx.Exec(ctx, `
			UPDATE task SET coalesced_message_ids = array_append(coalesced_message_ids, $2),
			                originator_user_id = COALESCE(originator_user_id, $4),
			                work_id = COALESCE(work_id, $5), updated_at = $3
			WHERE id = $1`, existing.ID, msg, now, originator, laneWork)
		if err != nil {
			return uuid.Nil, err
		}
		// A server wake-up is never a question (PRD FR-3.8 2): a queued
		// question it rides on becomes the lane's own kind.
		return existing.ID, promoteQueued(ctx, tx, existing.ID, laneID, false)
	}
	return insertQueuedTask(ctx, tx, newQueuedTask{
		LaneID: laneID, SessionID: sessionID, AgentID: agentID, ProfileID: profileID,
		TriggerMessageID: msg, Originator: originator, Work: laneWork, Now: now,
	})
}

// inheritedOriginator is wake()'s NN7 rule: the waker's person originator,
// else the requester's, else none. uuid.Nil ids are "not known".
func inheritedOriginator(ctx context.Context, tx pgx.Tx, waker, requester uuid.UUID) (*uuid.UUID, error) {
	var out *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT originator_user_id FROM task WHERE id = $1),
		                (SELECT originator_user_id FROM task WHERE id = $2))`, waker, requester).Scan(&out)
	if err != nil {
		return nil, fmt.Errorf("router: wake originator: %w", err)
	}
	return out, nil
}

// wakeOnBlocked is the system message the delegator wakes on (openapi
// setTaskStatus, contracts PR #101). It used to be the first line alone, which
// left the delegator with a notice it could not act on:
//
//   - the card id was missing, so there was nothing to thread the answer onto
//     (E3-05 (3) asks the wake-up to QUOTE the card);
//   - the question body was missing, so the delegator had to go find it while
//     the join message carries it (§4.2) — the two paths disagreed;
//   - and nothing said to mention the child. FR-3.3 rule 4 drops an agent's
//     reply that mentions nobody, so a delegator that answers the way the
//     first line asks is answering into the void: the child never wakes.
func wakeOnBlocked(cardID uuid.UUID, note, childName string, childID uuid.UUID) string {
	return "위임한 작업에서 질문이 왔습니다. 이것은 질문 알림이며 합류가 아닙니다 — 답만 하고 턴을 끝내세요.\n" +
		"- 질문 카드: " + cardID.String() + "\n" +
		"- 질문: " + note + "\n" +
		"답은 그 카드에 스레드 답글(reply_to: " + cardID.String() + ")로 달고, 그 답글에서 자식 " +
		MentionLink(childName, childID) + " 을(를) 멘션하세요 — " +
		"멘션 없는 에이전트 메시지는 아무도 깨우지 않으므로(FR-3.3 규칙 4), 멘션이 없으면 자식이 재진입하지 않습니다."
}

// delegatorOfLane returns the agent that delegated this lane, with its display
// name — the name is what the mention link on the question card shows (FR-3.2)
// — and the delegating task, whose cause the wake-up inherits (S-78).
func delegatorOfLane(ctx context.Context, q pgx.Tx, laneID uuid.UUID) (*uuid.UUID, string, uuid.UUID, error) {
	var agent *uuid.UUID
	var name string
	var task uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT d.agent_id, a.name, d.id FROM lane l
		JOIN task d ON d.id = l.delegated_from_task_id
		JOIN agent a ON a.id = d.agent_id
		WHERE l.id = $1`, laneID).Scan(&agent, &name, &task)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", uuid.Nil, nil
	}
	if err != nil {
		return nil, "", uuid.Nil, err
	}
	return agent, name, task, nil
}

// agentDisplayName reads one agent's display name for a mention link.
func agentDisplayName(ctx context.Context, q pgx.Tx, agentID uuid.UUID) (string, error) {
	var name string
	if err := q.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, agentID).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func insertInbox(ctx context.Context, q pgx.Tx, wsID, userID uuid.UUID, typ, severity string, sessionID, refID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
		SELECT m.id, $1::inbox_item_type, $2::inbox_severity, $3, $4, $5
		FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
		typ, severity, sessionID, refID, now, wsID, userID)
	return err
}

// recordStatusEvent is colab-cli.md §4: every CLI call shows up in the feed.
func (s *Service) recordStatusEvent(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, attempt int, status, note string, now time.Time) error {
	return tasks.InsertServerEvent(ctx, tx, taskID, attempt, "status", "set_status", status, "ok",
		// S-52: closed `status` payload — `--note` is an argument of the command.
		map[string]any{"command": "status set " + status,
			"args": map[string]any{"note": note}}, now)
}

// scheduleFocusFlush publishes the lane when the coalescing window that holds
// a stored-but-unsent 「지금」 closes (openapi setTaskStatus v0.3.8: 60초 안의
// 연속 선언은 마지막 것만 lane.updated). One timer per lane at a time; the
// flush publishes whatever the lane holds then — the LAST declaration.
func (s *Service) scheduleFocusFlush(laneID uuid.UUID, at time.Time) {
	if _, busy := s.focusFlush.LoadOrStore(laneID, true); busy {
		return
	}
	wait := s.Clock.After(at.Sub(s.Clock.Now()))
	go func() {
		<-wait
		s.focusFlush.Delete(laneID)
		s.flushFocus(context.Background(), laneID)
	}()
}

// flushFocus is the timer's work, in its own transaction.
func (s *Service) flushFocus(ctx context.Context, laneID uuid.UUID) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	due, err := lanefocus.TakePending(ctx, tx, laneID, s.Clock.Now())
	if err != nil || !due {
		return
	}
	s.publishLane(ctx, tx, laneID)
	_ = tx.Commit(ctx)
}

// noteRefusedDone writes the refused `status set done` on the feed in its own
// transaction (the caller's rolled back): colab-cli.md §4, every call shows.
func (s *Service) noteRefusedDone(ctx context.Context, taskID uuid.UUID, attempt int, now time.Time) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if attempt < 1 {
		attempt = 1
	}
	if err := tasks.InsertServerEvent(ctx, tx, taskID, attempt, "status", "set_status", "done", "rejected",
		map[string]any{"command": "status set done", "rejected_reason": "result_card_required"}, now); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// groupResultCards is the result cards of one delegation group's card lanes,
// in the lanes' order, and their one-line summaries for the join message.
func groupResultCards(ctx context.Context, tx pgx.Tx, delegTask uuid.UUID) ([]uuid.UUID, string, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.id FROM task_card c JOIN lane l ON l.id = c.lane_id
		WHERE l.delegated_from_task_id = $1 AND c.result IS NOT NULL ORDER BY l.created_at`, delegTask)
	if err != nil {
		return nil, "", err
	}
	ids, err := collectUUIDs(rows)
	if err != nil {
		return nil, "", err
	}
	var lines string
	for _, id := range ids {
		c, err := cards.Get(ctx, tx, id)
		if err != nil {
			return nil, "", err
		}
		res := cards.ParseResult(c.Result)
		if res == nil {
			continue
		}
		auto := ""
		if res.Auto {
			auto = " (자동)"
		}
		lines += fmt.Sprintf("- %s v%d %s: 기준 %d/%d 충족%s\n", c.Label(), c.Version, c.AssigneeName, res.MetCount, len(c.Criteria), auto)
	}
	return ids, lines, nil
}

// attachResultCards hangs result cards on the task a wake-up landed on — its
// turn prompt carries them in <result_cards> (harness v0.9.16).
func attachResultCards(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, ids []uuid.UUID) error {
	if taskID == uuid.Nil || len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE task SET result_card_ids = ARRAY(SELECT DISTINCT unnest(result_card_ids || $2::uuid[])) WHERE id = $1`, taskID, ids)
	return err
}

func collectUUIDs(rows pgx.Rows) ([]uuid.UUID, error) {
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
