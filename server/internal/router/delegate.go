package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/quiet"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// DelegateInput is `colab card delegate --file <card.json> --depends-on
// --profile` (PRD FR-3.8 1, openapi v0.3.10 delegateLane — the body is the
// delegation card; the old agent_id·brief body is gone).
type DelegateInput struct {
	Card      cards.Draft
	DependsOn []uuid.UUID
	Profile   *string
}

// DelegateResult carries the new lane, the delegation card bubble the server
// wrote on the caller's behalf, the task that will run it, and the card.
type DelegateResult struct {
	Lane    *gen.Lane
	Message gen.Message
	Task    *gen.Task
	Card    gen.TaskCard
}

// CardInvalid is 422 card_invalid with every broken rule (errors[] — the
// agent reads them and submits again). A card whose only fault is naming its
// own author is 422 self_delegation (openapi v0.3.10 error codes).
func CardInvalid(errs []apperr.FieldError) *apperr.Problem {
	p := apperr.Validation(errs...)
	p.Code, p.Detail = "card_invalid", "위임 카드를 고쳐 다시 내세요 — 아래 칸마다 사유가 있습니다"
	if len(errs) == 1 && errs[0].Code == "self_delegation" {
		p.Code, p.Detail = "self_delegation", errs[0].Message
	}
	return p
}

// Delegate is lane rule 2: a delegation is ALWAYS a new lane, even when the
// same agent already has one running (E2-02, E2-03). That is what makes
// scenario A — the same Researcher working three items in parallel — possible.
//
// The new lane's delegated_from_task_id is the caller's task, and that column
// is the join-group key: FR-6.5 bundles exactly the children of one delegating
// task, so a delegator that splits its work into two rounds gets two joins
// instead of one that never fires.
func (s *Service) Delegate(ctx context.Context, callerTask uuid.UUID, in DelegateInput) (*DelegateResult, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var sessionID, wsID, callerAgent uuid.UUID
	var callerName string
	var callerAttempt int
	// FR-3.1.1: a delegation belongs to the delegator's mission — the child
	// lane, its task and the mention message all carry it.
	var callerWork *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.session_id, s.workspace_id, t.agent_id, a.name, t.attempt, COALESCE(l.work_id, t.work_id)
		FROM task t JOIN room s ON s.id = t.session_id JOIN lane l ON l.id = t.lane_id JOIN agent a ON a.id = t.agent_id
		WHERE t.id = $1`, callerTask).Scan(&sessionID, &wsID, &callerAgent, &callerName, &callerAttempt, &callerWork)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tasks.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// The room row is the lock (V19_R1B_HANDOFF (b) router/delegate.go:65) —
	// "the room's mission" would be every mission once there are several.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM room WHERE id = $1 FOR UPDATE`, sessionID); err != nil {
		return nil, err
	}

	d := in.Card
	errs := cards.CheckDraft(d, callerAgent)
	// The target must be a participant. FR-1.9: session participation IS the
	// permission, so an agent that was never invited cannot be pulled in by
	// another agent — the human has to add it (E15-02).
	var profileID uuid.UUID
	var targetName string
	if d.AssigneeID != uuid.Nil && d.AssigneeID != callerAgent {
		err = tx.QueryRow(ctx, `
			SELECT sp.profile_id, a.name FROM room_participant sp JOIN agent a ON a.id = sp.agent_id
			WHERE sp.room_id = $1 AND sp.agent_id = $2 AND sp.left_at IS NULL`, sessionID, d.AssigneeID).Scan(&profileID, &targetName)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Validation(apperr.Field("card.agent_id", "not_participant",
				"이 에이전트는 방 참여자가 아닙니다 — `colab hitl ask`로 Director에게 참여자 추가를 요청하세요"))
		}
		if err != nil {
			return nil, err
		}
	}
	// FR-3.8 1 「참고 자료 — 같은 방의 id 만, 존재 검사」.
	bad, err := cards.CheckRefsExist(ctx, tx, sessionID, d.Refs)
	if err != nil {
		return nil, err
	}
	for _, f := range bad {
		errs = append(errs, apperr.Field(f, "not_found", "이 방에 없는 참고 자료입니다 — 같은 방의 아티팩트·결정·메시지 id 만 가리킬 수 있습니다"))
	}
	if len(errs) > 0 {
		return nil, CardInvalid(errs)
	}
	if in.Profile != nil && *in.Profile != "" {
		var pid uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM agent_profile WHERE agent_id = $1 AND name = $2`, d.AssigneeID, *in.Profile).Scan(&pid)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Validation(apperr.Field("profile", "not_found", "이 에이전트에는 그 프로파일이 없습니다"))
		}
		if err != nil {
			return nil, err
		}
		profileID = pid
	}
	// depends_on must name lanes of THIS session, or the DAG can reach across
	// sessions and never resolve.
	for _, dep := range in.DependsOn {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM lane WHERE id = $1 AND session_id = $2`, dep, sessionID).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, apperr.Validation(apperr.Field("depends_on", "not_found", "이 방의 서브 미션만 선행 작업으로 지정할 수 있습니다"))
		}
	}

	// The card's number (미션 안 1부터 — the room row lock above serialises
	// two delegations, the unique index backs it) and its parent: a card task
	// delegating again makes a sub-card (FR-3.8 1 「상위 카드」).
	number, err := cards.NextNumber(ctx, tx, sessionID, callerWork)
	if err != nil {
		return nil, err
	}
	var parentCard *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT card_id FROM task WHERE id = $1 AND kind = 'card'`, callerTask).Scan(&parentCard); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// The server writes the delegation card bubble so the delegation is
	// visible in the timeline exactly like a human's would be: the mention of
	// the assignee, then the card in people's words (the web renders the
	// card itself from card_id).
	content := cards.DelegationBubble(MentionLink(targetName, d.AssigneeID), cards.Label(number), 1, d, nil)
	dec := Decision{Mentions: ParseMentions(content)}
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, mentions, source_task_id, kind, created_at, work_id)
		VALUES ($1, 'agent', $2, $3, $4, $5, 'text', $6, $7) RETURNING id`,
		sessionID, callerAgent, content, dec.Mentions, callerTask, now, callerWork).Scan(&msgID); err != nil {
		return nil, fmt.Errorf("router: delegate message: %w", err)
	}

	// FR-3.5 (S-76): the delegation is an agent→agent hop like any other and
	// is gated BEFORE the lane exists. Skipping it let a delegation storm run
	// past every limit — a delegator that re-delegates on each join notice is
	// a loop with no mention in it, and this was the only path the limiter did
	// not see. On a trip the mention message stays in the timeline (E4-01: the
	// message is posted, the task is not), the session pauses with the limit
	// named, and the caller gets a Problem instead of a lane: a 201 with no
	// task would tell the agent its delegation is pending when it is not.
	cause, _, err := causeOfTask(ctx, tx, callerTask)
	if err != nil {
		return nil, err
	}
	v, err := s.judgeHop(ctx, tx, sessionID, wsID, Hop{FromAgent: callerAgent, ToAgent: d.AssigneeID, At: now, CauseID: cause}, msgID, 2, now)
	if err != nil {
		return nil, err
	}
	if !v.Allowed {
		if err := s.pauseForLoop(ctx, tx, sessionID, wsID, v, now); err != nil {
			return nil, err
		}
		// colab-cli.md §4: the refused call is on the feed too, with the reason
		// in the schema's own slot rather than a free-text note (S-52).
		if err := tasks.InsertServerEvent(ctx, tx, callerTask, callerAttempt, "status", "delegate", d.AssigneeID.String(), "rejected",
			map[string]any{"command": "card delegate", "args": map[string]any{"goal": d.Goal}, "rejected_reason": "loop_limit"}, now); err != nil {
			return nil, err
		}
		// The mention message is a timeline message; the pause's own frames
		// (room.updated, the HITL card) are published by pauseForLoop.
		// No lane (and no card) is created, so this is an ordinary
		// agent→agent mention and must not read as 「위임」 (openapi v0.3.2 D24).
		if err := messages.Store(ctx, tx, msgID, messages.StoreOpts{}); err != nil {
			return nil, err
		}
		if s.Hub != nil {
			_ = messages.Publish(ctx, s.Hub, tx, wsID, sessionID, msgID)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrLoopLimit(v)
	}

	res := lanestate.Resolve(lanestate.Request{
		AgentID: d.AssigneeID, ViaDelegate: true, DelegatorTaskID: callerTask,
	})
	if in.DependsOn == nil {
		in.DependsOn = []uuid.UUID{}
	}
	var laneID uuid.UUID
	// The lane's brief is the card's goal (openapi delegateLane v0.3.10
	// `lane.brief` = 목표): the S7 card's one line "what is this lane for".
	if err := tx.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, depends_on, delegated_from_task_id, brief, status, created_at, updated_at, work_id)
		VALUES ($1, $2, $3, $4, $5, $6, 'queued', $7, $7, $8) RETURNING id`,
		sessionID, d.AssigneeID, profileID, in.DependsOn, res.DelegatedFromTaskID, strings.TrimSpace(d.Goal), now, callerWork).Scan(&laneID); err != nil {
		return nil, fmt.Errorf("router: delegate lane: %w", err)
	}
	cardID, err := cards.Create(ctx, tx, cards.New{
		RoomID: sessionID, WorkID: callerWork, Number: number, DelegatorID: callerAgent, DelegatorTaskID: callerTask,
		LaneID: laneID, ParentCardID: parentCard, Draft: d, DelegateMessageID: msgID, Now: now,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE message SET card_id = $2, card_role = 'delegation', card_version = 1 WHERE id = $1`, msgID, cardID); err != nil {
		return nil, err
	}
	// openapi v0.3.2 (D24): the speech is `delegate` because THIS code path is
	// the delegation — not because the body happens to end with the lane's
	// brief, which is what a screen reconstructing it afterwards had to guess.
	// It runs after the lane INSERT so `delegated_lane_id` has its referent.
	if err := messages.Store(ctx, tx, msgID, messages.StoreOpts{
		DelegatedLaneID: &laneID, DelegateTargetID: &d.AssigneeID, DelegateTargetName: targetName,
	}); err != nil {
		return nil, err
	}

	originator, _, err := taskOriginator(ctx, tx, callerTask)
	if err != nil {
		return nil, err
	}
	taskID, err := insertQueuedTask(ctx, tx, newQueuedTask{
		LaneID: laneID, SessionID: sessionID, AgentID: d.AssigneeID, ProfileID: profileID,
		TriggerMessageID: msgID, DelegatedFrom: &callerTask, Originator: originator, Work: callerWork, Now: now,
		Kind: cards.KindCard, CardID: &cardID,
	})
	if err != nil {
		return nil, fmt.Errorf("router: delegate task: %w", err)
	}
	if d.BudgetUSD != nil {
		// FR-3.8 1 「예산 — 그 lane task 의 예산(FR-7 task 예산과 같은 칸)」.
		if _, err := tx.Exec(ctx, `UPDATE task SET budget_override = $2 WHERE id = $1`, taskID, *d.BudgetUSD); err != nil {
			return nil, err
		}
	}
	if err := s.recordStatusEvent(ctx, tx, callerTask, callerAttempt, "delegate", d.Goal, now); err != nil {
		return nil, err
	}
	// T-QUIET (FR-2A.2.3): a delegation is an agent's trigger like a mention
	// — in a mission waiting for approval the lane is made and its task held
	// (queued_reason approval_pending); the CLI/MCP result says whom it did
	// not wake from that reason (harness v0.9.15).
	heldNow := false
	if hold, closed, err := holdsFor(ctx, tx, callerWork); err != nil {
		return nil, err
	} else if hold {
		if heldNow, err = quiet.Hold(ctx, tx, taskID); err != nil {
			return nil, err
		}
	} else if closed {
		if err := quiet.CancelClosed(ctx, tx, taskID, now); err != nil {
			return nil, err
		}
	}

	msg, err := messages.Get(ctx, tx, msgID)
	if err != nil {
		return nil, err
	}
	out := &DelegateResult{Message: messages.ToAPI(msg)}
	if out.Lane, err = lanes.Load(ctx, tx, laneID, false); err != nil {
		return nil, err
	}
	t, err := tasks.Get(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	api := tasks.ToAPI(t, nil, nil)
	out.Task = &api
	cr, err := cards.Get(ctx, tx, cardID)
	if err != nil {
		return nil, err
	}
	if out.Card, err = cards.ToAPI(ctx, tx, cr, cards.Judge{Agent: &callerAgent}, false); err != nil {
		return nil, err
	}
	if s.Hub != nil {
		sid := sessionID
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "message.created", out.Message)
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "lane.updated", out.Lane)
	}
	cards.Publish(ctx, s.Hub, tx, cardID, "card.created")
	if heldNow {
		s.publishQuiet(ctx, tx, wsID, sessionID, *callerWork)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if s.Notifier != nil {
		s.Notifier.Notify()
	}
	return out, nil
}
