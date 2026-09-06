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
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
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

	participants, profiles, err := loadParticipants(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}

	th, err := threadPremise(ctx, tx, sessionID, in.ParentId)
	if err != nil {
		return nil, err
	}
	parent := th.Parent

	// Rule 8 premise: the author's own lane and the join group it belongs to.
	authorDelegator, joinFired, err := delegatorPremise(ctx, tx, author.TaskID)
	if err != nil {
		return nil, err
	}

	var suppress []uuid.UUID
	if in.SuppressAgentIds != nil {
		for _, id := range *in.SuppressAgentIds {
			suppress = append(suppress, uuid.UUID(id))
		}
	}
	dec := Decide(Input{
		Content: in.Content, AuthorType: author.Type, Participants: participants,
		AuthorAgentID: author.AgentID, AssigneeAgentID: assignee, Suppress: suppress,
		ReplyToAgentID: th.ReplyTo, ThreadOwnerAgentID: th.ThreadOwner,
		AuthorLaneDelegatorID: authorDelegator, JoinGroupFired: joinFired,
	})

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
	primaryTasks := map[uuid.UUID]uuid.UUID{}
	// The "새 lane으로 보내기" toggle is per message and never sticks: it lives
	// in this request body, so the next message starts from rule 3 again
	// (E2-14). A persisted toggle would silently kill rule 3 for the session.
	newLane := in.NewLane != nil && *in.NewLane && author.Type == "user"

	// FR-3.5: the three loop limits are checked once per post, against the
	// session's trigger history. A trigger that trips a limit is not created
	// and the SESSION pauses (E4-01) — recording the hop anyway keeps the next
	// decision correct.
	limits, err := s.loopLimits(ctx, tx, wsID)
	if err != nil {
		return nil, err
	}
	history, err := s.loadHops(ctx, tx, sessionID, now)
	if err != nil {
		return nil, err
	}

	for _, tr := range dec.Triggers {
		next := Hop{ToAgent: tr.AgentID, At: now}
		if author.Type == "agent" && author.AgentID != nil {
			next.FromAgent = *author.AgentID
		}
		v := CheckLoopLimits(history, next, limits, now)
		if err := s.recordHop(ctx, tx, sessionID, next, msgID, tr.Rule, v.Allowed); err != nil {
			return nil, err
		}
		history = append(history, next)
		if !v.Allowed {
			if err := s.pauseForLoop(ctx, tx, sessionID, wsID, director, v, now); err != nil {
				return nil, err
			}
			result.Warnings = append(result.Warnings, struct {
				AgentId nullable.Nullable[openapi_types.UUID] `json:"agent_id,omitempty"`
				Code    string                                `json:"code"`
				Message string                                `json:"message"`
			}{AgentId: tasks.NullUUID(&tr.AgentID), Code: "loop_limit",
				Message: "루프 상한(" + v.Detail + ")에 걸려 세션이 일시정지되었습니다"})
			continue
		}

		laneID, laneNew, err := s.resolveLaneFor(ctx, tx, sessionID, tr, profiles[tr.AgentID], laneOpts{
			threadRootLane: th.RootLane,
			topLevelMent:   tr.Rule == 2 && parent == nil,
			forceNewLane:   newLane,
		}, now)
		if err != nil {
			return nil, err
		}
		_ = laneNew
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
		primaryTasks[tr.AgentID] = taskID
	}

	// Rule 7: rule 5 woke somebody other than the assignee, so the assignee
	// gets a deferred task five minutes out (E1-12).
	if fb := PlanFallback(dec, assignee, now); fb != nil {
		var primary uuid.UUID
		for _, tr := range dec.Triggers {
			if tr.Rule == 5 {
				primary = primaryTasks[tr.AgentID]
			}
		}
		if primary != uuid.Nil {
			laneID, taskID, ok, err := s.scheduleFallback(ctx, tx, sessionID, *fb, primary, profiles[fb.AgentID], msgID, originator, now)
			if err != nil {
				return nil, err
			}
			if ok {
				result.Triggers = append(result.Triggers, struct {
					AgentId       openapi_types.UUID           `json:"agent_id"`
					Coalesced     bool                         `json:"coalesced"`
					DeferredUntil nullable.Nullable[time.Time] `json:"deferred_until,omitempty"`
					LaneId        openapi_types.UUID           `json:"lane_id"`
					TaskId        openapi_types.UUID           `json:"task_id"`
				}{AgentId: fb.AgentID, LaneId: laneID, TaskId: taskID,
					DeferredUntil: nullable.NewNullableWithValue(fb.DueAt)})
			}
		}
	}

	// E1-13: the primary agent answered inside the window, so the deferred
	// assignee task must never run.
	if author.Type == "agent" && author.TaskID != nil {
		if err := s.cancelFallbacksFor(ctx, tx, *author.TaskID, now); err != nil {
			return nil, err
		}
	}

	// colab-cli.md §4: the server records CLI calls as status task_events.
	if author.Type == "agent" && author.TaskID != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
			VALUES ($1, $2, (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
			        'status', 'post_message', to_jsonb($4::text), 'ok', $5, $6)
			ON CONFLICT (task_id, attempt, seq) DO NOTHING`,
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

// laneOpts carries the premises the four lane rules read that the trigger
// itself does not know.
type laneOpts struct {
	threadRootLane uuid.UUID
	topLevelMent   bool
	forceNewLane   bool
}

// resolveLaneFor applies PRD FR-3.3's lane resolution (rules 1–4) to one
// trigger. The decision itself is lanestate.Resolve — this only loads the
// candidates and writes the result.
func (s *Service) resolveLaneFor(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, tr Trigger, profileID uuid.UUID, o laneOpts, now time.Time) (uuid.UUID, bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, agent_id, status::text, reentry_count, GREATEST(created_at, updated_at)
		FROM lane WHERE session_id = $1 AND agent_id = $2 ORDER BY created_at`, sessionID, tr.AgentID)
	if err != nil {
		return uuid.Nil, false, err
	}
	var existing []lanestate.Candidate
	for rows.Next() {
		var c lanestate.Candidate
		if err := rows.Scan(&c.ID, &c.AgentID, &c.Status, &c.ReentryCount, &c.LastUsed); err != nil {
			rows.Close()
			return uuid.Nil, false, err
		}
		existing = append(existing, c)
	}
	rows.Close()

	d := lanestate.Resolve(lanestate.Request{
		AgentID: tr.AgentID, Existing: existing,
		ThreadRootLaneID: o.threadRootLane,
		TopLevelMention:  o.topLevelMent, ForceNewLane: o.forceNewLane,
	})
	if !d.Created {
		// Lock the row we are about to reuse so two concurrent posts cannot
		// both re-enter it.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM lane WHERE id = $1 FOR UPDATE`, d.LaneID); err != nil {
			return uuid.Nil, false, err
		}
		if d.Reentry {
			// FR-6.2 allows done/blocked → running. The lane becomes `running`
			// when the task is dispatched (tasks.MarkDispatched); until then it
			// is honestly `queued`, because no turn is in flight.
			if _, err := tx.Exec(ctx, `
				UPDATE lane SET status = 'queued', reentry_count = reentry_count + 1, finished_at = NULL, updated_at = $2
				WHERE id = $1`, d.LaneID, now); err != nil {
				return uuid.Nil, false, err
			}
		}
		return d.LaneID, false, nil
	}
	var id uuid.UUID
	var deleg *uuid.UUID
	if d.DelegatedFromTaskID != uuid.Nil {
		deleg = &d.DelegatedFromTaskID
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, delegated_from_task_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'queued', $5, $5) RETURNING id`,
		sessionID, tr.AgentID, profileID, deleg, now).Scan(&id); err != nil {
		return uuid.Nil, false, fmt.Errorf("router: insert lane: %w", err)
	}
	return id, true, nil
}

// loopLimits reads workspace_settings.loop_limits, falling back to the FR-3.5
// defaults for keys the row does not carry (E4-09 overrides one of them).
func (s *Service) loopLimits(ctx context.Context, tx pgx.Tx, wsID uuid.UUID) (Limits, error) {
	lim := DefaultLimits()
	var raw map[string]int
	err := tx.QueryRow(ctx, `SELECT loop_limits FROM workspace_settings WHERE workspace_id = $1`, wsID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return lim, nil
	}
	if err != nil {
		return lim, err
	}
	if v, ok := raw["max_chain_depth"]; ok {
		lim.MaxChainDepth = v
	}
	if v, ok := raw["max_hops_per_hour"]; ok {
		lim.MaxHopsPerHour = v
	}
	if v, ok := raw["max_pair_roundtrips"]; ok {
		lim.MaxPairRoundtrips = v
	}
	return lim, nil
}

// loadHops reads the trigger history the limits reason over. The rolling hour
// bounds hops_per_hour, but chain depth and pair roundtrips walk backwards to
// the last human message, so the window alone is not enough — 200 rows covers
// both without loading a long session.
func (s *Service) loadHops(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, now time.Time) ([]Hop, error) {
	rows, err := tx.Query(ctx, `
		SELECT from_agent_id, to_agent_id, created_at FROM (
			SELECT from_agent_id, to_agent_id, created_at, id
			FROM session_hop WHERE session_id = $1 ORDER BY id DESC LIMIT 200
		) h ORDER BY h.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hop
	for rows.Next() {
		var h Hop
		var from *uuid.UUID
		if err := rows.Scan(&from, &h.ToAgent, &h.At); err != nil {
			return nil, err
		}
		if from != nil {
			h.FromAgent = *from
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Service) recordHop(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, h Hop, msgID uuid.UUID, rule int, allowed bool) error {
	var from *uuid.UUID
	if !h.Human() {
		f := h.FromAgent
		from = &f
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, message_id, rule, allowed, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, sessionID, from, h.ToAgent, msgID, rule, allowed, h.At)
	return err
}

// pauseForLoop is FR-3.5's consequence: the session pauses with a reason that
// NAMES the limit, and the Director gets a system-issued HITL.
func (s *Service) pauseForLoop(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID, director *uuid.UUID, v LoopVerdict, now time.Time) error {
	var status string
	if err := tx.QueryRow(ctx, `SELECT status::text FROM session WHERE id = $1`, sessionID).Scan(&status); err != nil {
		return err
	}
	if status == "paused" {
		return nil // already stopped; one pause per session, not one per trigger
	}
	if _, err := tx.Exec(ctx, `
		UPDATE session SET status = 'paused', paused_reason = 'loop', pause_detail = $2, updated_at = $3
		WHERE id = $1`, sessionID, v.Detail, now); err != nil {
		return err
	}
	var hitlID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, proposed_default, approver_spec, due_at, created_at)
		VALUES ($1, NULL, 'system', 'approval', $2, NULL, 'director', $3, $4) RETURNING id`,
		sessionID, "루프 상한("+v.Detail+")에 도달해 세션을 일시정지했습니다. 계속할까요?",
		now.Add(24*time.Hour), now).Scan(&hitlID); err != nil {
		return fmt.Errorf("router: loop hitl: %w", err)
	}
	if director != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
			SELECT m.id, 'session_paused', 'action_required', $1, $2, $3
			FROM member m WHERE m.workspace_id = $4 AND m.user_id = $5`,
			sessionID, hitlID, now, wsID, *director); err != nil {
			return err
		}
	}
	if s.Hub != nil {
		sid := sessionID
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "session.updated", map[string]any{
			"id": sessionID, "status": "paused", "paused_reason": "loop", "pause_detail": v.Detail,
		})
	}
	return nil
}

// scheduleFallback inserts rule 7's deferred assignee task. It is `deferred`
// with not_before = +5m, so the queue cannot hand it out early and the sweep
// promotes it when the window closes.
func (s *Service) scheduleFallback(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, fb Fallback, primary, profileID, msgID uuid.UUID, originator *uuid.UUID, now time.Time) (uuid.UUID, uuid.UUID, bool, error) {
	// One pending fallback per lane is enough; a second reply inside the same
	// window must not stack two assignee wake-ups.
	var dup int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM task WHERE fallback_for_task_id = $1 AND status = 'deferred'`, primary).Scan(&dup); err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	if dup > 0 {
		return uuid.Nil, uuid.Nil, false, nil
	}
	laneID, _, err := s.resolveLaneFor(ctx, tx, sessionID, Trigger{AgentID: fb.AgentID, Rule: 7}, profileID,
		laneOpts{topLevelMent: true}, now)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	var taskID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, originator_user_id,
		                  status, not_before, fallback_for_task_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'deferred', $7, $8, $9, $9) RETURNING id`,
		laneID, sessionID, fb.AgentID, profileID, msgID, originator, fb.DueAt, primary, now).Scan(&taskID); err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("router: fallback task: %w", err)
	}
	return laneID, taskID, true, nil
}

// cancelFallbacksFor is E1-13: the primary agent spoke, so the deferred
// assignee task it was covering for is cancelled rather than run.
func (s *Service) cancelFallbacksFor(ctx context.Context, tx pgx.Tx, primary uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE task SET status = 'cancelled', finished_at = $2, updated_at = $2
		WHERE fallback_for_task_id = $1 AND status = 'deferred'`, primary, now)
	return err
}

// SystemPost inserts a system message without routing (session start etc.).
func (s *Service) SystemPost(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, content string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO message (session_id, author_type, author_id, content, kind, created_at) VALUES ($1, 'system', NULL, $2, 'system', $3) RETURNING id`,
		sessionID, strings.TrimSpace(content), s.Clock.Now()).Scan(&id)
	return id, err
}

// ---------------------------------------------------------------------------
// Premises. Post and Preview must read exactly the same facts, or the preview
// (FR-3.6) promises triggers the post does not create — which is the bug the
// web's local calculation had. They share these three loaders.
// ---------------------------------------------------------------------------

func loadParticipants(ctx context.Context, q db.DBTX, sessionID uuid.UUID) ([]Participant, map[uuid.UUID]uuid.UUID, error) {
	rows, err := q.Query(ctx, `
		SELECT sp.agent_id, a.name, sp.profile_id FROM session_participant sp JOIN agent a ON a.id = sp.agent_id
		WHERE sp.session_id = $1 ORDER BY sp.joined_at`, sessionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var participants []Participant
	profiles := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var p Participant
		var profile uuid.UUID
		if err := rows.Scan(&p.AgentID, &p.Name, &profile); err != nil {
			return nil, nil, err
		}
		participants = append(participants, p)
		profiles[p.AgentID] = profile
	}
	return participants, profiles, rows.Err()
}

// thread is the FR-3.3 rule 5 / lane rule 1 position of a message.
type thread struct {
	// Parent is the normalised thread ROOT: a reply to a reply hangs off the
	// root, not off the message the human clicked.
	Parent *uuid.UUID
	// ReplyTo owns the message actually replied to; ThreadOwner owns the root.
	ReplyTo     *uuid.UUID
	ThreadOwner *uuid.UUID
	// RootLane is the lane whose task produced the root (lane rule 1).
	RootLane uuid.UUID
}

func threadPremise(ctx context.Context, q db.DBTX, sessionID uuid.UUID, parentID nullable.Nullable[openapi_types.UUID]) (thread, error) {
	var th thread
	if !parentID.IsSpecified() || parentID.IsNull() {
		return th, nil
	}
	pid := uuid.UUID(parentID.MustGet())
	var root *uuid.UUID
	var psess uuid.UUID
	var pType string
	var pAuthor *uuid.UUID
	err := q.QueryRow(ctx, `SELECT parent_id, session_id, author_type::text, author_id FROM message WHERE id = $1`, pid).
		Scan(&root, &psess, &pType, &pAuthor)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && psess != sessionID) {
		return th, ErrParentNotFound
	}
	if err != nil {
		return th, err
	}
	if pType == "agent" {
		th.ReplyTo = pAuthor
	}
	if root != nil {
		th.Parent = root
	} else {
		th.Parent = &pid
	}
	var rType string
	var rAuthor, rTask *uuid.UUID
	if err := q.QueryRow(ctx, `SELECT author_type::text, author_id, source_task_id FROM message WHERE id = $1`, *th.Parent).
		Scan(&rType, &rAuthor, &rTask); err != nil {
		return th, err
	}
	if rType == "agent" {
		th.ThreadOwner = rAuthor
	}
	// Lane rule 1: the thread root came out of a task, so the reply goes to
	// that task's lane and keeps the same workdir (scenario B).
	if rTask != nil {
		var lid uuid.UUID
		err := q.QueryRow(ctx, `SELECT lane_id FROM task WHERE id = $1`, *rTask).Scan(&lid)
		if err == nil {
			th.RootLane = lid
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return th, err
		}
	}
	return th, nil
}

// delegatorPremise answers rule 8: who delegated the author's lane, and has
// that lane's join group already fired? The suppression lasts only until it
// has (E1-17) — otherwise a re-entered child could never speak to its
// delegator again.
func delegatorPremise(ctx context.Context, q db.DBTX, taskID *uuid.UUID) (*uuid.UUID, bool, error) {
	if taskID == nil {
		return nil, false, nil
	}
	var deleg *uuid.UUID
	var firedAt *time.Time
	err := q.QueryRow(ctx, `
		SELECT d.agent_id, d.join_fired_at
		FROM task t JOIN lane l ON l.id = t.lane_id
		JOIN task d ON d.id = l.delegated_from_task_id
		WHERE t.id = $1`, *taskID).Scan(&deleg, &firedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return deleg, firedAt != nil, nil
}
