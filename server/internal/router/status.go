package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
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
			"blocked needs the question — the delegator has nothing to answer otherwise"))
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var laneID, sessionID, agentID, wsID uuid.UUID
	var triggerMsg *uuid.UUID
	var reentry int
	var director *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.lane_id, t.session_id, t.agent_id, s.workspace_id, t.trigger_message_id, l.reentry_count, s.director_user_id
		FROM task t JOIN lane l ON l.id = t.lane_id JOIN session s ON s.id = t.session_id
		WHERE t.id = $1 FOR UPDATE OF t, l`, taskID).
		Scan(&laneID, &sessionID, &agentID, &wsID, &triggerMsg, &reentry, &director)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tasks.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out := &StatusResult{TaskID: taskID, LaneID: laneID}

	if err := s.recordStatusEvent(ctx, tx, taskID, attempt, status, note, now); err != nil {
		return nil, err
	}

	switch status {
	case "working":
		if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'running', updated_at = $2 WHERE id = $1`, laneID, now); err != nil {
			return nil, err
		}
	case "blocked":
		delegator, err := delegatorOfLane(ctx, tx, laneID)
		if err != nil {
			return nil, err
		}
		plan := PlanBlocked(delegator, note, uuid.New)
		// The card IS the thread root: a status change alone gives the
		// delegator nothing to reply to (리뷰#04-2). lane.blocked_note is only
		// the last-value cache; the history lives in these messages.
		if _, err := tx.Exec(ctx, `
			INSERT INTO message (id, session_id, author_type, author_id, content, source_task_id, kind, created_at)
			VALUES ($1, $2, 'agent', $3, $4, $5, 'blocked_q', $6)`,
			plan.QuestionCardID, sessionID, agentID, note, taskID, now); err != nil {
			return nil, fmt.Errorf("router: blocked question card: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE lane SET status = 'blocked', blocked_note = $2, blocked_message_id = $3, finished_at = $4, updated_at = $4
			WHERE id = $1`, laneID, note, plan.QuestionCardID, now); err != nil {
			return nil, err
		}
		qid := plan.QuestionCardID
		out.QuestionMessageID, out.TurnEndRequired = &qid, true

		if plan.DelegatorWoken {
			// Immediate, not via the join: a question raised at minute 2 must
			// not arrive forty minutes later behind the slowest sibling.
			if err := s.wake(ctx, tx, sessionID, wsID, *plan.DelegatorAgentID, qid,
				"위임한 작업에서 질문이 왔습니다. 이것은 질문 알림이며 합류가 아닙니다 — 답만 하고 턴을 끝내세요.", now); err != nil {
				return nil, err
			}
		} else if director != nil {
			// 리뷰#04-3: a lane the Director created by mentioning an agent has
			// no delegator to wake, so the question goes to the inbox.
			if err := insertInbox(ctx, tx, wsID, *director, "lane_blocked", "action_required", sessionID, qid, now); err != nil {
				return nil, err
			}
		}
	case "done":
		if _, err := tx.Exec(ctx, `
			UPDATE lane SET status = 'done', finished_at = $2, updated_at = $2 WHERE id = $1`, laneID, now); err != nil {
			return nil, err
		}
		out.TurnEndRequired = true
		if err := s.afterLaneDone(ctx, tx, sessionID, wsID, laneID, agentID, triggerMsg, reentry, director, now); err != nil {
			return nil, err
		}
	default:
		return nil, apperr.Validation(apperr.Field("status", "invalid", "status must be working, blocked or done"))
	}

	if _, err := tx.Exec(ctx, `UPDATE session SET updated_at = $2 WHERE id = $1`, sessionID, now); err != nil {
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

// afterLaneDone is FR-6.5. Two different notifications hang off one event, and
// they are not interchangeable:
//
//	the JOIN fires once per delegation group, when every child has ended;
//	a RE-ENTRY completion tells whoever asked for the re-entry, which is
//	usually not the delegator (scenario B: QA asked, Lead delegated).
func (s *Service) afterLaneDone(ctx context.Context, tx pgx.Tx, sessionID, wsID, laneID, agentID uuid.UUID, triggerMsg *uuid.UUID, reentry int, director *uuid.UUID, now time.Time) error {
	if reentry > 0 {
		// The re-entry's author gets told. Leaving it to the agent's prompt
		// means QA never learns Frontend produced a new diff (리뷰#04-5).
		return s.notifyReentry(ctx, tx, sessionID, wsID, triggerMsg, director, now)
	}
	var delegTask *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT delegated_from_task_id FROM lane WHERE id = $1`, laneID).Scan(&delegTask); err != nil {
		return err
	}
	if delegTask == nil {
		// Not a delegation: nobody is waiting for a bundle.
		return s.notifyReentry(ctx, tx, sessionID, wsID, triggerMsg, director, now)
	}
	return s.maybeFireJoin(ctx, tx, sessionID, wsID, *delegTask, now)
}

// maybeFireJoin fires the join exactly once per group. `blocked` children count
// as ended (FR-6.2.1) — treating them as in progress would let one question
// hold every sibling's result hostage.
func (s *Service) maybeFireJoin(ctx context.Context, tx pgx.Tx, sessionID, wsID, delegTask uuid.UUID, now time.Time) error {
	var delegAgent uuid.UUID
	var fired *time.Time
	if err := tx.QueryRow(ctx, `SELECT agent_id, join_fired_at FROM task WHERE id = $1 FOR UPDATE`, delegTask).
		Scan(&delegAgent, &fired); err != nil {
		return err
	}
	if fired != nil {
		return nil // one bundle per group (E); a re-entry does not re-fire it
	}
	rows, err := tx.Query(ctx, `
		SELECT l.status::text, a.name, l.blocked_note
		FROM lane l JOIN agent a ON a.id = l.agent_id
		WHERE l.delegated_from_task_id = $1 ORDER BY l.created_at`, delegTask)
	if err != nil {
		return err
	}
	type child struct {
		status, name string
		note         *string
	}
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.status, &c.name, &c.note); err != nil {
			rows.Close()
			return err
		}
		children = append(children, c)
	}
	rows.Close()
	unanswered := 0
	for _, c := range children {
		if !lanestate.Terminal(c.status) {
			return nil // still running, waiting_human or paused — the group waits
		}
		if c.status == lanestate.Blocked {
			unanswered++
		}
	}
	if len(children) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE task SET join_fired_at = $2, updated_at = $2 WHERE id = $1`, delegTask, now); err != nil {
		return err
	}
	body := "위임한 작업이 모두 끝났습니다.\n"
	for _, c := range children {
		body += "- " + c.name + ": " + c.status
		if c.note != nil && *c.note != "" {
			body += " — 질문: " + *c.note
		}
		body += "\n"
	}
	if unanswered > 0 {
		// t-8: without this line a delegator that missed the immediate notice
		// just calls `status set done` and the question dies with the group.
		body += fmt.Sprintf("\n답을 기다리는 자식 %d개가 있습니다. 답하기 전에 작업을 종료하지 마세요.\n", unanswered)
	}
	msgID, err := s.SystemPost(ctx, tx, sessionID, body)
	if err != nil {
		return err
	}
	return s.wake(ctx, tx, sessionID, wsID, delegAgent, msgID, "", now)
}

// notifyReentry tells whoever caused the work that it is finished. A human
// author gets an inbox item; an agent author gets a task.
func (s *Service) notifyReentry(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID, triggerMsg *uuid.UUID, director *uuid.UUID, now time.Time) error {
	if triggerMsg == nil {
		return nil
	}
	var authorType string
	var authorID *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT author_type::text, author_id FROM message WHERE id = $1`, *triggerMsg).Scan(&authorType, &authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	switch {
	case authorType == "agent" && authorID != nil:
		return s.wake(ctx, tx, sessionID, wsID, *authorID, *triggerMsg, "요청하신 작업이 끝났습니다.", now)
	case authorType == "user" && authorID != nil:
		return insertInbox(ctx, tx, wsID, *authorID, "mention", "info", sessionID, *triggerMsg, now)
	case director != nil:
		return insertInbox(ctx, tx, wsID, *director, "mention", "info", sessionID, *triggerMsg, now)
	}
	return nil
}

// wake creates a task for one agent on its own lane. It is the server's own
// trigger, so it bypasses the mention rules — the point of FR-6.2.1 and FR-6.5
// is that these wake-ups are deterministic rather than prompt-dependent.
func (s *Service) wake(ctx context.Context, tx pgx.Tx, sessionID, wsID, agentID, triggerMsg uuid.UUID, prefix string, now time.Time) error {
	var profileID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT profile_id FROM session_participant WHERE session_id = $1 AND agent_id = $2`, sessionID, agentID).Scan(&profileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no longer a participant: nothing to wake
	}
	if err != nil {
		return err
	}
	msg := triggerMsg
	if prefix != "" {
		id, err := s.SystemPost(ctx, tx, sessionID, prefix)
		if err != nil {
			return err
		}
		msg = id
	}
	laneID, _, err := s.resolveLaneFor(ctx, tx, sessionID, Trigger{AgentID: agentID, Rule: 0}, profileID,
		laneOpts{topLevelMent: true}, now)
	if err != nil {
		return err
	}
	var existing uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM task WHERE lane_id = $1 AND status = 'queued' ORDER BY created_at LIMIT 1 FOR UPDATE`, laneID).Scan(&existing)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE task SET coalesced_message_ids = array_append(coalesced_message_ids, $2), updated_at = $3 WHERE id = $1`, existing, msg, now)
		return err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6, $6)`, laneID, sessionID, agentID, profileID, msg, now)
	return err
}

func delegatorOfLane(ctx context.Context, q pgx.Tx, laneID uuid.UUID) (*uuid.UUID, error) {
	var agent *uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT d.agent_id FROM lane l JOIN task d ON d.id = l.delegated_from_task_id WHERE l.id = $1`, laneID).Scan(&agent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return agent, nil
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
		map[string]any{"command": "status set " + status, "note": note}, now)
}
