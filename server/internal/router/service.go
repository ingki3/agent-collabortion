package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

var (
	ErrSessionNotFound = errors.New("router: session not found")
	ErrParentNotFound  = errors.New("router: parent message not found")
)

// ServerSeqBase is the seq range for task_events the server records itself
// (colab-cli.md §4 status events). Daemon seqs are small and monotonic; the
// schema has one (task, attempt, seq) key so server-side events live above it.
const ServerSeqBase = 1 << 30

// Notifier wakes long-polling claims (queue.Notifier).
type Notifier interface{ Notify() }

// Author is who posts: a user (originator) or an agent task (TaskToken).
type Author struct {
	Type    string // user | agent | system
	UserID  *uuid.UUID
	AgentID *uuid.UUID
	TaskID  *uuid.UUID
	Attempt int
}

type Service struct {
	DB       *pgxpool.Pool
	Clock    clock.Clock
	Hub      *realtime.Hub
	Notifier Notifier
}

func New(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub, n Notifier) *Service {
	return &Service{DB: pool, Clock: c, Hub: h, Notifier: n}
}

// Post persists the message, applies the rules and creates or merges tasks.
// One transaction per session (row lock) so two concurrent posts cannot create
// two queued tasks on the same lane.
func (s *Service) Post(ctx context.Context, sessionID uuid.UUID, author Author, in gen.MessageCreate) (*gen.MessagePostResult, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wsID uuid.UUID
	var status string
	var assignee, director *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT workspace_id, status, assignee_agent_id, director_user_id FROM session WHERE id = $1 FOR UPDATE`, sessionID).
		Scan(&wsID, &status, &assignee, &director)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT sp.agent_id, a.name, sp.profile_id FROM session_participant sp JOIN agent a ON a.id = sp.agent_id
		WHERE sp.session_id = $1 ORDER BY sp.joined_at`, sessionID)
	if err != nil {
		return nil, err
	}
	var participants []Participant
	profiles := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var p Participant
		var profile uuid.UUID
		if err := rows.Scan(&p.AgentID, &p.Name, &profile); err != nil {
			rows.Close()
			return nil, err
		}
		participants = append(participants, p)
		profiles[p.AgentID] = profile
	}
	rows.Close()

	// Thread root normalisation: a reply to a reply hangs off the root.
	var parent *uuid.UUID
	if in.ParentId.IsSpecified() && !in.ParentId.IsNull() {
		pid := uuid.UUID(in.ParentId.MustGet())
		var root *uuid.UUID
		var psess uuid.UUID
		err := tx.QueryRow(ctx, `SELECT parent_id, session_id FROM message WHERE id = $1`, pid).Scan(&root, &psess)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && psess != sessionID) {
			return nil, ErrParentNotFound
		}
		if err != nil {
			return nil, err
		}
		if root != nil {
			parent = root
		} else {
			parent = &pid
		}
	}

	var suppress []uuid.UUID
	if in.SuppressAgentIds != nil {
		for _, id := range *in.SuppressAgentIds {
			suppress = append(suppress, uuid.UUID(id))
		}
	}
	dec := Decide(Input{Content: in.Content, AuthorType: author.Type, Participants: participants, AssigneeAgentID: assignee, Suppress: suppress})

	var authorID *uuid.UUID
	switch author.Type {
	case "user":
		authorID = author.UserID
	case "agent":
		authorID = author.AgentID
	}
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, parent_id, content, mentions, source_task_id, kind, state, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'text', 'posted', $8) RETURNING id`,
		sessionID, author.Type, authorID, parent, in.Content, dec.Mentions, author.TaskID, now).Scan(&msgID); err != nil {
		return nil, fmt.Errorf("router: insert message: %w", err)
	}

	result := &gen.MessagePostResult{}
	result.Triggers = make([]struct {
		AgentId       openapi_types.UUID           `json:"agent_id"`
		Coalesced     bool                         `json:"coalesced"`
		DeferredUntil nullable.Nullable[time.Time] `json:"deferred_until,omitempty"`
		LaneId        openapi_types.UUID           `json:"lane_id"`
		TaskId        openapi_types.UUID           `json:"task_id"`
	}, 0)
	result.Warnings = make([]struct {
		AgentId nullable.Nullable[openapi_types.UUID] `json:"agent_id,omitempty"`
		Code    string                                `json:"code"`
		Message string                                `json:"message"`
	}, 0)
	for _, w := range dec.Warnings {
		result.Warnings = append(result.Warnings, struct {
			AgentId nullable.Nullable[openapi_types.UUID] `json:"agent_id,omitempty"`
			Code    string                                `json:"code"`
			Message string                                `json:"message"`
		}{AgentId: tasks.NullUUID(w.AgentID), Code: w.Code, Message: w.Message})
	}

	originator := author.UserID
	if author.Type == "agent" && author.TaskID != nil {
		_ = tx.QueryRow(ctx, `SELECT originator_user_id FROM task WHERE id = $1`, *author.TaskID).Scan(&originator)
	}
	newLane := in.NewLane != nil && *in.NewLane && author.Type == "user"

	for _, tr := range dec.Triggers {
		laneID, err := s.resolveLane(ctx, tx, sessionID, tr.AgentID, profiles[tr.AgentID], newLane)
		if err != nil {
			return nil, err
		}
		// FR-3.4: a queued task on the lane absorbs this message.
		var existing uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM task WHERE lane_id = $1 AND status = 'queued' ORDER BY created_at LIMIT 1 FOR UPDATE`, laneID).Scan(&existing)
		coalesced := err == nil
		var taskID uuid.UUID
		if coalesced {
			taskID = existing
			if _, err := tx.Exec(ctx, `UPDATE task SET coalesced_message_ids = array_append(coalesced_message_ids, $2), updated_at = $3 WHERE id = $1`, taskID, msgID, now); err != nil {
				return nil, err
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx, `
				INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, originator_user_id, status, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, 'queued', $7, $7) RETURNING id`,
				laneID, sessionID, tr.AgentID, profiles[tr.AgentID], msgID, originator, now).Scan(&taskID); err != nil {
				return nil, fmt.Errorf("router: insert task: %w", err)
			}
		} else {
			return nil, err
		}
		result.Triggers = append(result.Triggers, struct {
			AgentId       openapi_types.UUID           `json:"agent_id"`
			Coalesced     bool                         `json:"coalesced"`
			DeferredUntil nullable.Nullable[time.Time] `json:"deferred_until,omitempty"`
			LaneId        openapi_types.UUID           `json:"lane_id"`
			TaskId        openapi_types.UUID           `json:"task_id"`
		}{AgentId: tr.AgentID, Coalesced: coalesced, LaneId: laneID, TaskId: taskID})
		if t, err := tasks.Get(ctx, tx, taskID); err == nil && s.Hub != nil {
			sid := sessionID
			_ = s.Hub.Publish(ctx, tx, wsID, &sid, "task.updated", tasks.ToAPI(t, nil, nil))
		}
	}

	// colab-cli.md §4: the server records CLI calls as status task_events.
	if author.Type == "agent" && author.TaskID != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
			VALUES ($1, $2, (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
			        'status', 'post_message', to_jsonb($4::text), 'ok', $5, $6)`,
			*author.TaskID, author.Attempt, ServerSeqBase,
			msgID.String(),
			map[string]any{"command": "message post", "result_ref": msgID.String()}, now); err != nil {
			return nil, fmt.Errorf("router: status event: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE session SET updated_at = $2 WHERE id = $1`, sessionID, now); err != nil {
		return nil, err
	}
	msg, err := messages.Get(ctx, tx, msgID)
	if err != nil {
		return nil, err
	}
	result.Message = messages.ToAPI(msg)
	if s.Hub != nil {
		sid := sessionID
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "message.created", result.Message)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if len(result.Triggers) > 0 && s.Notifier != nil {
		s.Notifier.Notify()
	}
	return result, nil
}

// resolveLane is lane resolution rule 3/4 in its P1 form: reuse the agent's
// most recent lane in the session unless it failed (or new_lane), else create.
func (s *Service) resolveLane(ctx context.Context, tx pgx.Tx, sessionID, agentID, profileID uuid.UUID, forceNew bool) (uuid.UUID, error) {
	now := s.Clock.Now()
	if !forceNew {
		var id uuid.UUID
		var status string
		err := tx.QueryRow(ctx, `SELECT id, status FROM lane WHERE session_id = $1 AND agent_id = $2 ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, sessionID, agentID).Scan(&id, &status)
		if err == nil && status != "failed" {
			if status == "done" || status == "blocked" {
				if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'queued', reentry_count = reentry_count + 1, finished_at = NULL, updated_at = $2 WHERE id = $1`, id, now); err != nil {
					return uuid.Nil, err
				}
			}
			return id, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'queued', $4, $4) RETURNING id`,
		sessionID, agentID, profileID, now).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("router: insert lane: %w", err)
	}
	return id, nil
}

// SystemPost inserts a system message without routing (session start etc.).
func (s *Service) SystemPost(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, content string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO message (session_id, author_type, author_id, content, kind, created_at) VALUES ($1, 'system', NULL, $2, 'system', $3) RETURNING id`,
		sessionID, strings.TrimSpace(content), s.Clock.Now()).Scan(&id)
	return id, err
}
