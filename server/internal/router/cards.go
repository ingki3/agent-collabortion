package router

// cards.go is PRD FR-3.8 3·4 on the router's side: the result card
// (submitCardResult), and the delegator's judgement (acceptCard ·
// reviseCard). The pure rules are internal/cards; the lane's end with or
// without a result is lanedone.MarkDone's CARD GATE.
//
// Lock order is the one every card path shares — task → lane → card:
//   - SubmitResult locks the calling task and its lane (lockTaskCtx), then
//     the card — the same order tasks.Finish → lanedone.MarkDone takes, so a
//     result racing the turn's end lands on one side: either the finish sees
//     the result (the lane ends) or the token is already revoked (401).
//   - Accept/Revise lock the card's lane, then the card. They never lock a
//     running task, so they cannot close a cycle with a finish; and a revise
//     racing a result_card_missing follow-up is serialised on the lane (the
//     follow-up only happens while the card is in_progress, a revise only
//     after its result — whichever takes the lane first decides).

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
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// SubmitResultOut is submitCardResult's 201.
type SubmitResultOut struct {
	Card       gen.TaskCard
	Message    gen.Message
	Downgraded []int
	Notice     *string
}

// SubmitResult is `colab card report` (openapi submitCardResult).
func (s *Service) SubmitResult(ctx context.Context, taskID uuid.UUID, attempt int, cardID uuid.UUID, in cards.ResultIn) (*SubmitResultOut, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tc, err := lockTaskCtx(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	var kind string
	var taskCard *uuid.UUID
	var dispatched *time.Time
	if err := tx.QueryRow(ctx, `SELECT kind, card_id, dispatched_at FROM task WHERE id = $1`, taskID).Scan(&kind, &taskCard, &dispatched); err != nil {
		return nil, err
	}
	if kind != cards.KindCard || taskCard == nil || *taskCard != cardID {
		return nil, apperr.Forbidden("not_card_task", cards.NotCardTaskSentence)
	}
	c, err := cards.Lock(ctx, tx, cardID)
	if errors.Is(err, cards.ErrNotFound) {
		return nil, apperr.NotFound("card")
	}
	if err != nil {
		return nil, err
	}
	// 「현재 판」: a turn dispatched before this version's delegation bubble
	// was working on an older version (a revise came while it still ran).
	if c.DelegateMessageID != nil && dispatched != nil {
		var at time.Time
		if err := tx.QueryRow(ctx, `SELECT created_at FROM message WHERE id = $1`, *c.DelegateMessageID).Scan(&at); err == nil && dispatched.Before(at) {
			return nil, apperr.Forbidden("not_card_task", cards.NotCardTaskSentence)
		}
	}
	if _, ok := cards.Transition(c.Status, cards.ActSubmit, false); !ok || c.Status != cards.InProgress && c.Status != cards.ResultSubmitted {
		return nil, apperr.Conflict("card_not_open", cards.CardNotOpenSentence)
	}
	res, down, errs := cards.CheckResult(in, len(c.Criteria))
	if len(errs) == 0 {
		bad, err := cards.CheckEvidenceExist(ctx, tx, c.RoomID, in)
		if err != nil {
			return nil, err
		}
		for _, f := range bad {
			errs = append(errs, apperr.Field(f, "not_found", "이 방에 없는 근거입니다 — 같은 방의 아티팩트·메시지 id 로 가리키세요"))
		}
	}
	if len(errs) > 0 {
		p := apperr.Validation(errs...)
		p.Code, p.Detail = "result_card_incomplete", "결과 카드를 고쳐 다시 내세요 — 아래 칸마다 사유가 있습니다"
		return nil, p
	}
	fillEvidenceLabels(ctx, tx, &res)
	msgID, err := cards.StoreResult(ctx, tx, c, res, &taskID, now)
	if err != nil {
		return nil, err
	}
	if attempt < 1 {
		attempt = 1
	}
	if err := tasks.InsertServerEvent(ctx, tx, taskID, attempt, "status", "card", c.Label(), "ok",
		map[string]any{"command": "card report", "result_ref": msgID.String()}, now); err != nil {
		return nil, err
	}
	s.publishMessage(ctx, tx, tc.sessionID, msgID)
	cards.Publish(ctx, s.Hub, tx, cardID, "card.updated")
	out, err := s.cardOut(ctx, tx, cardID, cards.Judge{Agent: &tc.agentID}, msgID)
	if err != nil {
		return nil, err
	}
	out.Downgraded, out.Notice = down, cards.DowngradeNotice(down)
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func fillEvidenceLabels(ctx context.Context, tx pgx.Tx, r *cards.Result) {
	for i := range r.Verdicts {
		for j := range r.Verdicts[i].Evidence {
			e := &r.Verdicts[i].Evidence[j]
			var label *string
			switch e.Kind {
			case "artifact":
				_ = tx.QueryRow(ctx, `SELECT name || ' v' || version FROM artifact WHERE id::text = $1`, e.Ref).Scan(&label)
			case "message":
				_ = tx.QueryRow(ctx, `
					SELECT COALESCE(u.display_name, a.name, '시스템') || ' 메시지' FROM message m
					LEFT JOIN app_user u ON m.author_type = 'user' AND u.id = m.author_id
					LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id WHERE m.id::text = $1`, e.Ref).Scan(&label)
			case "commit":
				l := e.Ref
				if len(l) > 7 {
					l = l[:7]
				}
				label = &l
			}
			e.Label = label
		}
	}
}

func (s *Service) cardOut(ctx context.Context, tx pgx.Tx, cardID uuid.UUID, j cards.Judge, msgID uuid.UUID) (*SubmitResultOut, error) {
	c, err := cards.Get(ctx, tx, cardID)
	if err != nil {
		return nil, err
	}
	api, err := cards.ToAPI(ctx, tx, c, j, false)
	if err != nil {
		return nil, err
	}
	m, err := messages.Get(ctx, tx, msgID)
	if err != nil {
		return nil, err
	}
	return &SubmitResultOut{Card: api, Message: messages.ToAPI(m)}, nil
}

// Judgement is who calls accept/revise: an agent's task, or a person.
type Judgement struct {
	TaskID  *uuid.UUID // agent (TaskToken)
	Attempt int
	UserID  *uuid.UUID // person
}

func (s *Service) judgeOf(ctx context.Context, tx pgx.Tx, j Judgement) (cards.Judge, string, uuid.UUID, string, error) {
	if j.TaskID != nil {
		var agent uuid.UUID
		var name string
		if err := tx.QueryRow(ctx, `SELECT t.agent_id, a.name FROM task t JOIN agent a ON a.id = t.agent_id WHERE t.id = $1`, *j.TaskID).Scan(&agent, &name); err != nil {
			return cards.Judge{}, "", uuid.Nil, "", err
		}
		return cards.Judge{Agent: &agent}, "agent", agent, name, nil
	}
	if j.UserID == nil {
		return cards.Judge{}, "", uuid.Nil, "", apperr.Forbidden("not_card_judge", cards.NotCardJudgeSentence)
	}
	var name string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM app_user WHERE id = $1`, *j.UserID).Scan(&name); err != nil {
		return cards.Judge{}, "", uuid.Nil, "", err
	}
	return cards.Judge{Person: j.UserID}, "user", *j.UserID, name, nil
}

// lockCardViaLane locks the card's lane, then the card (the order above).
func lockCardViaLane(ctx context.Context, tx pgx.Tx, cardID uuid.UUID) (*cards.Row, error) {
	var lane uuid.UUID
	err := tx.QueryRow(ctx, `SELECT lane_id FROM task_card WHERE id = $1`, cardID).Scan(&lane)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("card")
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM lane WHERE id = $1 FOR UPDATE`, lane); err != nil {
		return nil, err
	}
	c, err := cards.Lock(ctx, tx, cardID)
	if errors.Is(err, cards.ErrNotFound) {
		return nil, apperr.NotFound("card")
	}
	return c, err
}

// Accept is acceptCard.
func (s *Service) Accept(ctx context.Context, cardID uuid.UUID, j Judgement) (*gen.TaskCard, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, err := lockCardViaLane(ctx, tx, cardID)
	if err != nil {
		return nil, err
	}
	judge, byKind, byID, byName, err := s.judgeOf(ctx, tx, j)
	if err != nil {
		return nil, err
	}
	w, err := cards.JudgesOf(ctx, tx, c)
	if err != nil {
		return nil, err
	}
	if !cards.MayJudge(judge, w) {
		return nil, apperr.Forbidden("not_card_judge", cards.NotCardJudgeSentence)
	}
	if _, ok := cards.Transition(c.Status, cards.ActAccept, judge.Person != nil); !ok {
		return nil, apperr.Conflict("card_not_judgeable", cards.CardNotJudgeableSentence)
	}
	if err := cards.Accept(ctx, tx, c, byKind, byID, byName, now); err != nil {
		return nil, err
	}
	if j.TaskID != nil {
		if err := tasks.InsertServerEvent(ctx, tx, *j.TaskID, max(j.Attempt, 1), "status", "card", c.Label(), "ok",
			map[string]any{"command": "card accept", "args": map[string]any{"card": c.Label()}}, now); err != nil {
			return nil, err
		}
	}
	s.publishResultBubbleUpdate(ctx, tx, c)
	cards.Publish(ctx, s.Hub, tx, cardID, "card.updated")
	c, err = cards.Get(ctx, tx, cardID)
	if err != nil {
		return nil, err
	}
	api, err := cards.ToAPI(ctx, tx, c, judge, false)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &api, nil
}

// publishResultBubbleUpdate is 「결과 카드 말풍선 판정 줄 갱신(message.updated)」
// — the bubble's own row is unchanged (the web reads the judgement from the
// card); the frame tells an open timeline to redraw it.
func (s *Service) publishResultBubbleUpdate(ctx context.Context, tx pgx.Tx, c *cards.Row) {
	res := cards.ParseResult(c.Result)
	if s.Hub == nil || res == nil || res.MessageID == nil {
		return
	}
	id, err := uuid.Parse(*res.MessageID)
	if err != nil {
		return
	}
	m, err := messages.Get(ctx, tx, id)
	if err != nil {
		return
	}
	var ws uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT workspace_id FROM room WHERE id = $1`, c.RoomID).Scan(&ws); err != nil {
		return
	}
	room := c.RoomID
	_ = s.Hub.Publish(ctx, tx, ws, &room, "message.updated", messages.ToAPI(m))
}

// ReviseOut is reviseCard's 200.
type ReviseOut struct {
	Card    gen.TaskCard
	Message gen.Message
	Task    *gen.Task
}

// Patch is TaskCardPatch — nil fields keep the current version's value.
type Patch struct {
	Goal         *string
	Criteria     *[]cards.Criterion
	Boundaries   *string
	Refs         *[]cards.Ref
	OutputFormat **string
	BudgetUSD    **float64
}

// Revise is reviseCard: a new version, a new delegation bubble, and a card
// task re-entering the same lane (FR-3.5's loop limits count it).
func (s *Service) Revise(ctx context.Context, cardID uuid.UUID, j Judgement, reason string, patch Patch) (*ReviseOut, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, err := lockCardViaLane(ctx, tx, cardID)
	if err != nil {
		return nil, err
	}
	judge, byKind, byID, byName, err := s.judgeOf(ctx, tx, j)
	if err != nil {
		return nil, err
	}
	w, err := cards.JudgesOf(ctx, tx, c)
	if err != nil {
		return nil, err
	}
	if !cards.MayJudge(judge, w) {
		return nil, apperr.Forbidden("not_card_judge", cards.NotCardJudgeSentence)
	}
	if _, ok := cards.Transition(c.Status, cards.ActRevise, judge.Person != nil); !ok {
		return nil, apperr.Conflict("card_not_judgeable", cards.CardNotJudgeableSentence)
	}
	d := cards.ApplyPatch(c, patch.Goal, patch.Criteria, patch.Boundaries, patch.Refs, patch.OutputFormat, patch.BudgetUSD)
	errs := cards.CheckDraft(d, c.DelegatorID)
	bad, err := cards.CheckRefsExist(ctx, tx, c.RoomID, d.Refs)
	if err != nil {
		return nil, err
	}
	for _, f := range bad {
		errs = append(errs, apperr.Field(f, "not_found", "이 방에 없는 참고 자료입니다 — 같은 방의 아티팩트·결정·메시지 id 만 가리킬 수 있습니다"))
	}
	if len(errs) > 0 {
		return nil, CardInvalid(errs)
	}
	if err := cards.Revise(ctx, tx, c, d, reason, byKind, byID, byName, now); err != nil {
		return nil, err
	}
	version := c.Version + 1

	// The new version's delegation bubble — by the judge (the delegator, or
	// the person reverting it), mentioning the assignee.
	var wsID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT workspace_id FROM room WHERE id = $1`, c.RoomID).Scan(&wsID); err != nil {
		return nil, err
	}
	content := cards.DelegationBubble(MentionLink(c.AssigneeName, c.AssigneeID), c.Label(), version, d, &reason)
	mentions := ParseMentions(content)
	var msgID uuid.UUID
	authorType := "agent"
	if byKind == "user" {
		authorType = "user"
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, mentions, source_task_id, kind, created_at, work_id,
		                     card_id, card_role, card_version)
		VALUES ($1, $2, $3, $4, $5, $6, 'text', $7, $8, $9, 'delegation', $10) RETURNING id`,
		c.RoomID, authorType, byID, content, mentions, j.TaskID, now, c.WorkID, c.ID, version).Scan(&msgID); err != nil {
		return nil, fmt.Errorf("router: revise bubble: %w", err)
	}
	if err := messages.Store(ctx, tx, msgID, messages.StoreOpts{
		DelegatedLaneID: &c.LaneID, DelegateTargetID: &c.AssigneeID, DelegateTargetName: c.AssigneeName,
	}); err != nil {
		return nil, err
	}
	if err := cards.SetDelegateMessage(ctx, tx, c.ID, msgID); err != nil {
		return nil, err
	}
	s.publishMessage(ctx, tx, c.RoomID, msgID)

	out := &ReviseOut{}
	// FR-3.5: the re-entry is a hop like a mention; over the limit the card
	// changes but no task is made and the room stops (openapi reviseCard).
	var hop Hop
	hop.ToAgent, hop.At = c.AssigneeID, now
	if j.TaskID != nil {
		hop.FromAgent = byID
		if _, hop.CauseID, err = causeOfTask(ctx, tx, *j.TaskID); err != nil {
			return nil, err
		}
	}
	v, err := s.judgeHop(ctx, tx, c.RoomID, wsID, hop, msgID, 2, now)
	if err != nil {
		return nil, err
	}
	if !v.Allowed {
		if err := s.pauseForLoop(ctx, tx, c.RoomID, wsID, v, now); err != nil {
			return nil, err
		}
	} else {
		taskID, err := s.reenterCardLane(ctx, tx, c, msgID, j, now)
		if err != nil {
			return nil, err
		}
		t, err := tasks.Get(ctx, tx, taskID)
		if err != nil {
			return nil, err
		}
		api := tasks.ToAPI(t, nil, nil)
		out.Task = &api
		if s.Hub != nil {
			room := c.RoomID
			_ = s.Hub.Publish(ctx, tx, wsID, &room, "task.updated", api)
		}
	}
	if j.TaskID != nil {
		if err := tasks.InsertServerEvent(ctx, tx, *j.TaskID, max(j.Attempt, 1), "status", "card", c.Label(), "ok",
			map[string]any{"command": "card revise", "args": map[string]any{"card": c.Label(), "reason": reason}}, now); err != nil {
			return nil, err
		}
	}
	s.publishResultBubbleUpdate(ctx, tx, c)
	cards.Publish(ctx, s.Hub, tx, cardID, "card.updated")
	nc, err := cards.Get(ctx, tx, cardID)
	if err != nil {
		return nil, err
	}
	if out.Card, err = cards.ToAPI(ctx, tx, nc, judge, false); err != nil {
		return nil, err
	}
	m, err := messages.Get(ctx, tx, msgID)
	if err != nil {
		return nil, err
	}
	out.Message = messages.ToAPI(m)
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if s.Notifier != nil {
		s.Notifier.Notify()
	}
	return out, nil
}

// reenterCardLane is the revise's card task on the card's own lane — lane
// resolution rule 1 (same lane, reentry_count +1). A queued task already on
// the lane absorbs it (FR-3.4) and becomes this card's task.
func (s *Service) reenterCardLane(ctx context.Context, tx pgx.Tx, c *cards.Row, msgID uuid.UUID, j Judgement, now time.Time) (uuid.UUID, error) {
	var profileID uuid.UUID
	var laneWork *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT profile_id, work_id FROM lane WHERE id = $1`, c.LaneID).Scan(&profileID, &laneWork); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE lane SET status = CASE WHEN status IN ('running', 'queued') THEN status ELSE 'queued'::lane_status END,
		       reentry_count = reentry_count + 1, finished_at = NULL, updated_at = $2 WHERE id = $1`, c.LaneID, now); err != nil {
		return uuid.Nil, err
	}
	s.publishLane(ctx, tx, c.LaneID)
	existing, ok, err := lockQueuedTask(ctx, tx, c.LaneID)
	if err != nil {
		return uuid.Nil, err
	}
	if ok {
		_, err := tx.Exec(ctx, `
			UPDATE task SET coalesced_message_ids = array_append(coalesced_message_ids, $2), kind = 'card', card_id = $3, updated_at = $4
			WHERE id = $1`, existing.ID, msgID, c.ID, now)
		return existing.ID, err
	}
	var originator *uuid.UUID
	if j.TaskID != nil {
		if originator, _, err = taskOriginator(ctx, tx, *j.TaskID); err != nil {
			return uuid.Nil, err
		}
	} else {
		originator = j.UserID
	}
	return insertQueuedTask(ctx, tx, newQueuedTask{
		LaneID: c.LaneID, SessionID: c.RoomID, AgentID: c.AssigneeID, ProfileID: profileID,
		TriggerMessageID: msgID, DelegatedFrom: c.DelegatorTaskID, Originator: originator, Work: laneWork, Now: now,
		Kind: cards.KindCard, CardID: &c.ID,
	})
}
