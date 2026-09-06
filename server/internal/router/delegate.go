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
	err = tx.QueryRow(ctx, `
		SELECT t.session_id, s.workspace_id, t.agent_id, a.name, t.attempt
		FROM task t JOIN session s ON s.id = t.session_id JOIN agent a ON a.id = t.agent_id
		WHERE t.id = $1`, callerTask).Scan(&sessionID, &wsID, &callerAgent, &callerName, &callerAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tasks.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM session WHERE id = $1 FOR UPDATE`, sessionID); err != nil {
		return nil, err
	}

	// The target must be a participant. FR-1.9: session participation IS the
	// permission, so an agent that was never invited cannot be pulled in by
	// another agent — the human has to add it (E15-02).
	var profileID uuid.UUID
	var targetName string
	err = tx.QueryRow(ctx, `
		SELECT sp.profile_id, a.name FROM session_participant sp JOIN agent a ON a.id = sp.agent_id
		WHERE sp.session_id = $1 AND sp.agent_id = $2`, sessionID, in.AgentID).Scan(&profileID, &targetName)
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
			return nil, apperr.Validation(apperr.Field("profile", "not_found", "no such profile on this agent"))
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
			return nil, apperr.Validation(apperr.Field("depends_on", "not_found", "depends_on must name lanes of this session"))
		}
	}

	// The server writes the mention message so the delegation is visible in the
	// timeline exactly like a human's would be.
	content := MentionLink(targetName, in.AgentID) + " " + in.Brief
	dec := Decision{Mentions: ParseMentions(content)}
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, mentions, source_task_id, kind, created_at)
		VALUES ($1, 'agent', $2, $3, $4, $5, 'text', $6) RETURNING id`,
		sessionID, callerAgent, content, dec.Mentions, callerTask, now).Scan(&msgID); err != nil {
		return nil, fmt.Errorf("router: delegate message: %w", err)
	}

	d := lanestate.Resolve(lanestate.Request{
		AgentID: in.AgentID, ViaDelegate: true, DelegatorTaskID: callerTask,
	})
	if in.DependsOn == nil {
		in.DependsOn = []uuid.UUID{}
	}
	var laneID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, depends_on, delegated_from_task_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6, $6) RETURNING id`,
		sessionID, in.AgentID, profileID, in.DependsOn, d.DelegatedFromTaskID, now).Scan(&laneID); err != nil {
		return nil, fmt.Errorf("router: delegate lane: %w", err)
	}

	var originator *uuid.UUID
	_ = tx.QueryRow(ctx, `SELECT originator_user_id FROM task WHERE id = $1`, callerTask).Scan(&originator)
	var taskID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, delegated_from_task_id,
		                  originator_user_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued', $8, $8) RETURNING id`,
		laneID, sessionID, in.AgentID, profileID, msgID, callerTask, originator, now).Scan(&taskID); err != nil {
		return nil, fmt.Errorf("router: delegate task: %w", err)
	}
	// The delegation is an agent→agent hop like any other, so it counts toward
	// FR-3.5. Skipping it would let a delegation storm run past the limits.
	if err := s.recordHop(ctx, tx, sessionID, Hop{FromAgent: callerAgent, ToAgent: in.AgentID, At: now}, msgID, 2, true); err != nil {
		return nil, err
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
