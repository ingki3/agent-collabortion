package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// DelegateInput is `colab lane delegate --agent --brief --depends-on --profile`.
type DelegateInput struct {
	AgentID   uuid.UUID
	Brief     string
	DependsOn []uuid.UUID
	Profile   *string
}

// DelegateResult carries the new lane, the mention message the server wrote on
// the caller's behalf, and the task that will run it.
type DelegateResult struct {
	Lane    *gen.Lane
	Message gen.Message
	Task    *gen.Task
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

	// The target must be a participant. FR-1.9: session participation IS the
	// permission, so an agent that was never invited cannot be pulled in by
	// another agent — the human has to add it (E15-02).
	var profileID uuid.UUID
	var targetName string
	err = tx.QueryRow(ctx, `
		SELECT sp.profile_id, a.name FROM room_participant sp JOIN agent a ON a.id = sp.agent_id
		WHERE sp.room_id = $1 AND sp.agent_id = $2 AND sp.left_at IS NULL`, sessionID, in.AgentID).Scan(&profileID, &targetName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Validation(apperr.Field("agent_id", "not_participant",
			"이 에이전트는 세션 참여자가 아닙니다 — `colab hitl ask`로 Director에게 참여자 추가를 요청하세요"))
	}
	if err != nil {
		return nil, err
	}
	if in.Profile != nil && *in.Profile != "" {
		var pid uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM agent_profile WHERE agent_id = $1 AND name = $2`, in.AgentID, *in.Profile).Scan(&pid)
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
			return nil, apperr.Validation(apperr.Field("depends_on", "not_found", "이 세션의 작업 줄기만 선행 작업으로 지정할 수 있습니다"))
		}
	}

	// The server writes the mention message so the delegation is visible in the
	// timeline exactly like a human's would be.
	content := MentionLink(targetName, in.AgentID) + " " + in.Brief
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
	v, err := s.judgeHop(ctx, tx, sessionID, wsID, Hop{FromAgent: callerAgent, ToAgent: in.AgentID, At: now, CauseID: cause}, msgID, 2, now)
	if err != nil {
		return nil, err
	}
	if !v.Allowed {
		if err := s.pauseForLoop(ctx, tx, sessionID, wsID, v, now); err != nil {
			return nil, err
		}
		// colab-cli.md §4: the refused call is on the feed too, with the reason
		// in the schema's own slot rather than a free-text note (S-52).
		if err := tasks.InsertServerEvent(ctx, tx, callerTask, callerAttempt, "status", "delegate", in.AgentID.String(), "rejected",
			map[string]any{"command": "lane delegate", "args": map[string]any{"brief": in.Brief}, "rejected_reason": "loop_limit"}, now); err != nil {
			return nil, err
		}
		// The mention message is a timeline message; the pause's own frames
		// (session.updated, the HITL card) are published by pauseForLoop.
		if s.Hub != nil {
			_ = messages.Publish(ctx, s.Hub, tx, wsID, sessionID, msgID)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrLoopLimit(v)
	}

	d := lanestate.Resolve(lanestate.Request{
		AgentID: in.AgentID, ViaDelegate: true, DelegatorTaskID: callerTask,
	})
	if in.DependsOn == nil {
		in.DependsOn = []uuid.UUID{}
	}
	var laneID uuid.UUID
	// The brief is stored on the lane, not only inside the mention message:
	// it is the child's turn prompt AND the S7 card's one-line "what is this
	// lane for" (openapi Lane.brief). Reading it back out of the message body
	// is not possible — the server prefixes the mention link.
	if err := tx.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, depends_on, delegated_from_task_id, brief, status, created_at, updated_at, work_id)
		VALUES ($1, $2, $3, $4, $5, $6, 'queued', $7, $7, $8) RETURNING id`,
		sessionID, in.AgentID, profileID, in.DependsOn, d.DelegatedFromTaskID, in.Brief, now, callerWork).Scan(&laneID); err != nil {
		return nil, fmt.Errorf("router: delegate lane: %w", err)
	}

	var originator *uuid.UUID
	_ = tx.QueryRow(ctx, `SELECT originator_user_id FROM task WHERE id = $1`, callerTask).Scan(&originator)
	var taskID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, delegated_from_task_id,
		                  originator_user_id, status, created_at, updated_at, work_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued', $8, $8, $9) RETURNING id`,
		laneID, sessionID, in.AgentID, profileID, msgID, callerTask, originator, now, callerWork).Scan(&taskID); err != nil {
		return nil, fmt.Errorf("router: delegate task: %w", err)
	}
	if err := s.recordStatusEvent(ctx, tx, callerTask, callerAttempt, "delegate", in.Brief, now); err != nil {
		return nil, err
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
	if s.Hub != nil {
		sid := sessionID
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "message.created", out.Message)
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "lane.updated", out.Lane)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if s.Notifier != nil {
		s.Notifier.Notify()
	}
	return out, nil
}
