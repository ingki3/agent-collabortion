// Package sessions is FR-2.1 session creation (P1: `none` isolation), detail
// with participants' derived status (FR-1.3) and the S5 list.
package sessions

import (
	"context"
	"encoding/json"
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
	"github.com/ingki3/agent-collabortion/server/internal/agents"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

type Service struct {
	DB     *pgxpool.Pool
	Clock  clock.Clock
	Hub    *realtime.Hub
	Router *router.Service
}

func New(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub, r *router.Service) *Service {
	return &Service{DB: pool, Clock: c, Hub: h, Router: r}
}

// Viewer is who asks: a member (user) or a task token.
type Viewer struct {
	UserID *uuid.UUID
}

// Create validates the S6 submission and creates session, participants and
// the assignee's initial task (E16-A step 1) unless draft.
func (s *Service) Create(ctx context.Context, wsID, userID uuid.UUID, in gen.SessionCreate) (*gen.Session, error) {
	var errs []apperr.FieldError
	if strings.TrimSpace(in.Title) == "" || len(in.Title) > 200 {
		errs = append(errs, apperr.Field("title", "length", "title must be 1–200 characters"))
	}
	if strings.TrimSpace(in.Goal) == "" {
		errs = append(errs, apperr.Field("goal", "required", "goal is required"))
	}
	if len(in.Participants) == 0 {
		errs = append(errs, apperr.Field("participants", "min_items", "at least one participant"))
	}
	switch in.Isolation.Kind {
	case gen.IsolationKindNone:
	case gen.IsolationKindContainer:
		errs = append(errs, apperr.Field("isolation/kind", "unsupported", "container isolation is v1.1"))
	case gen.IsolationKindWorktree:
		if !in.RuntimeId.IsSpecified() || in.RuntimeId.IsNull() {
			errs = append(errs, apperr.Field("runtime_id", "required_for_isolation", "worktree isolation requires a runtime"))
		}
		errs = append(errs, apperr.Field("isolation/kind", "unsupported_in_p1", "worktree isolation (repo check) lands in P4; P1 supports none"))
	default:
		errs = append(errs, apperr.Field("isolation/kind", "enum", "unknown isolation kind"))
	}
	if in.Autonomy != nil && *in.Autonomy == gen.Supervised {
		errs = append(errs, apperr.Field("autonomy", "unsupported", "supervised is v1.1"))
	}
	if in.CompletionCondition != nil {
		if b, err := json.Marshal(in.CompletionCondition); err == nil && strings.Contains(string(b), `"criteria_met"`) && !strings.Contains(string(b), `"conditions"`) {
			errs = append(errs, apperr.Field("completion_condition", "criteria_met_alone", "criteria_met cannot be the only condition"))
		}
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}

	var nRuntimes int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM runtime WHERE workspace_id = $1`, wsID).Scan(&nRuntimes); err != nil {
		return nil, err
	}
	if nRuntimes == 0 {
		return nil, apperr.Conflict("no_runtime", "connect a computer first")
	}
	// An explicit runtime must belong to this workspace (FR-2.1 M10; the claim
	// path relies on it). Unknown and foreign runtimes get the same answer so
	// the response does not reveal whether a runtime exists elsewhere.
	var runtimeID *uuid.UUID
	if in.RuntimeId.IsSpecified() && !in.RuntimeId.IsNull() {
		id := uuid.UUID(in.RuntimeId.MustGet())
		var rws uuid.UUID
		if err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM runtime WHERE id = $1`, id).Scan(&rws); err != nil || rws != wsID {
			return nil, apperr.Validation(apperr.Field("runtime_id", "runtime_not_in_workspace", "runtime not in this workspace"))
		}
		runtimeID = &id
	}

	director := userID
	if in.DirectorUserId != nil {
		director = uuid.UUID(*in.DirectorUserId)
	}
	var deputy *uuid.UUID
	if in.DeputyDirectorUserId.IsSpecified() && !in.DeputyDirectorUserId.IsNull() {
		d := uuid.UUID(in.DeputyDirectorUserId.MustGet())
		deputy = &d
	}
	for _, uid := range []*uuid.UUID{&director, deputy} {
		if uid == nil {
			continue
		}
		var n int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM member WHERE workspace_id = $1 AND user_id = $2`, wsID, *uid).Scan(&n); err != nil || n == 0 {
			return nil, apperr.Validation(apperr.Field("director_user_id", "not_member", "director/deputy must be workspace members"))
		}
	}

	// Participants: agent in workspace, invitable by caller (FR-1.9), profile.
	type part struct {
		agentID, profileID uuid.UUID
	}
	var parts []part
	seen := map[uuid.UUID]bool{}
	for i, p := range in.Participants {
		aid := uuid.UUID(p.AgentId)
		if seen[aid] {
			continue
		}
		seen[aid] = true
		a, err := agents.Load(ctx, s.DB, aid, &userID)
		if err != nil || a.WorkspaceId != wsID {
			return nil, apperr.Validation(apperr.Field(fmt.Sprintf("participants/%d/agent_id", i), "not_found", "agent not in this workspace"))
		}
		if !a.Invitable.Allowed {
			reason := "cannot invite this agent"
			if r, err := a.Invitable.Reason.Get(); err == nil {
				reason = r
			}
			return nil, apperr.Forbidden("not_invitable", a.Name+": "+reason)
		}
		var profileID uuid.UUID
		if p.ProfileId.IsSpecified() && !p.ProfileId.IsNull() {
			profileID = uuid.UUID(p.ProfileId.MustGet())
			found := false
			for _, pr := range a.Profiles {
				if pr.Id == profileID {
					found = true
				}
			}
			if !found {
				return nil, apperr.Validation(apperr.Field(fmt.Sprintf("participants/%d/profile_id", i), "not_found", "profile does not belong to the agent"))
			}
		} else {
			for _, pr := range a.Profiles {
				if pr.IsDefault {
					profileID = pr.Id
				}
			}
			if profileID == uuid.Nil && len(a.Profiles) > 0 {
				profileID = a.Profiles[0].Id
			}
		}
		if profileID == uuid.Nil {
			return nil, apperr.Validation(apperr.Field(fmt.Sprintf("participants/%d", i), "no_profile", "agent has no profile"))
		}
		parts = append(parts, part{aid, profileID})
	}
	assignee := parts[0].agentID
	if in.AssigneeAgentId != nil {
		assignee = uuid.UUID(*in.AssigneeAgentId)
		if !seen[assignee] {
			return nil, apperr.Validation(apperr.Field("assignee_agent_id", "not_participant", "assignee must be a participant"))
		}
	}

	isolation := map[string]any{"kind": string(in.Isolation.Kind)}
	if in.Isolation.RepoPath != nil {
		isolation["repo_path"] = *in.Isolation.RepoPath
	}
	if in.Isolation.RemoteUrl.IsSpecified() && !in.Isolation.RemoteUrl.IsNull() {
		isolation["remote_url"] = in.Isolation.RemoteUrl.MustGet()
	}
	criteria := []string{}
	if in.AcceptanceCriteria != nil {
		criteria = *in.AcceptanceCriteria
	}
	autonomy := "guided"
	if in.Autonomy != nil {
		autonomy = string(*in.Autonomy)
	}
	status := "active"
	draft := in.Draft != nil && *in.Draft
	if draft {
		status = "draft"
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	cols := []string{"workspace_id", "title", "goal", "acceptance_criteria", "director_user_id", "deputy_director_user_id", "assignee_agent_id", "runtime_id", "isolation", "autonomy", "status", "created_by", "created_at", "updated_at", "started_at"}
	var startedAt *time.Time
	if !draft {
		startedAt = &now
	}
	args := []any{wsID, strings.TrimSpace(in.Title), in.Goal, criteria, director, deputy, assignee, runtimeID, isolation, autonomy, status, userID, now, now, startedAt}
	if in.CompletionCondition != nil {
		cols = append(cols, "completion_condition")
		args = append(args, in.CompletionCondition)
	}
	if in.Limits != nil {
		cols = append(cols, "limits")
		args = append(args, in.Limits)
	}
	if in.ContextReuseOverride != nil {
		cols = append(cols, "context_reuse_override")
		args = append(args, in.ContextReuseOverride)
	}
	ph := make([]string, len(args))
	for i := range args {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	var sessionID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO session (`+strings.Join(cols, ", ")+`) VALUES (`+strings.Join(ph, ", ")+`) RETURNING id`, args...).Scan(&sessionID); err != nil {
		return nil, fmt.Errorf("sessions: insert: %w", err)
	}
	for _, p := range parts {
		if _, err := tx.Exec(ctx, `INSERT INTO session_participant (session_id, agent_id, profile_id, joined_at) VALUES ($1, $2, $3, $4)`, sessionID, p.agentID, p.profileID, now); err != nil {
			return nil, err
		}
	}
	if in.Context != nil {
		for _, c := range *in.Context {
			if _, err := tx.Exec(ctx, `INSERT INTO session_context (session_id, type, ref, created_at) VALUES ($1, $2, $3, $4)`, sessionID, string(c.Type), c.Ref, now); err != nil {
				return nil, err
			}
		}
	}
	if !draft {
		// E16-A step 1: the assignee's initial task, triggered by a system message.
		msgID, err := s.Router.SystemPost(ctx, tx, sessionID, "Session started. Goal: "+in.Goal)
		if err != nil {
			return nil, err
		}
		var profileID uuid.UUID
		for _, p := range parts {
			if p.agentID == assignee {
				profileID = p.profileID
			}
		}
		var laneID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'queued', $4, $4) RETURNING id`,
			sessionID, assignee, profileID, now).Scan(&laneID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task (lane_id, session_id, runtime_id, agent_id, profile_id, trigger_message_id, originator_user_id, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued', $8, $8)`, laneID, sessionID, runtimeID, assignee, profileID, msgID, userID, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if !draft && s.Router != nil && s.Router.Notifier != nil {
		s.Router.Notifier.Notify()
	}
	sess, err := s.Get(ctx, sessionID, Viewer{UserID: &userID})
	if err != nil {
		return nil, err
	}
	if s.Hub != nil {
		_ = s.Hub.Publish(ctx, nil, wsID, &sessionID, "session.updated", sess)
	}
	return sess, nil
}

// WorkspaceOf returns the session's workspace (authorization).
func (s *Service) WorkspaceOf(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var ws uuid.UUID
	err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM session WHERE id = $1`, id).Scan(&ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, apperr.NotFound("session")
	}
	return ws, err
}

// Get is getSession.
func (s *Service) Get(ctx context.Context, id uuid.UUID, v Viewer) (*gen.Session, error) {
	return Load(ctx, s.DB, id, v)
}

func Load(ctx context.Context, q db.DBTX, id uuid.UUID, v Viewer) (*gen.Session, error) {
	var out gen.Session
	var (
		deputy, assignee, runtimeID          *uuid.UUID
		isolation, completion, limits, reuse []byte
		autonomy, status                     string
		pausedReason                         *string
		cost                                 float64
		startedAt, finishedAt, lastActivity  *time.Time
		runtimeStatus                        *string
	)
	err := q.QueryRow(ctx, `
		SELECT s.id, s.workspace_id, s.title, s.goal, s.acceptance_criteria, s.director_user_id, s.deputy_director_user_id, s.assignee_agent_id,
		       s.runtime_id, s.isolation, s.completion_condition, s.limits, s.autonomy, s.context_reuse_override, s.status, s.paused_reason,
		       s.cost_usd, s.created_by, s.created_at, s.updated_at, s.started_at, s.finished_at,
		       (SELECT max(created_at) FROM message m WHERE m.session_id = s.id), r.status
		FROM session s LEFT JOIN runtime r ON r.id = s.runtime_id WHERE s.id = $1`, id).Scan(
		&out.Id, &out.WorkspaceId, &out.Title, &out.Goal, &out.AcceptanceCriteria, &out.DirectorUserId, &deputy, &assignee,
		&runtimeID, &isolation, &completion, &limits, &autonomy, &reuse, &status, &pausedReason,
		&cost, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt, &startedAt, &finishedAt, &lastActivity, &runtimeStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("session")
	}
	if err != nil {
		return nil, fmt.Errorf("sessions: load: %w", err)
	}
	if out.AcceptanceCriteria == nil {
		out.AcceptanceCriteria = []string{}
	}
	out.DeputyDirectorUserId = tasks.NullUUID(deputy)
	out.AssigneeAgentId = tasks.NullUUID(assignee)
	out.RuntimeId = tasks.NullUUID(runtimeID)
	_ = json.Unmarshal(isolation, &out.Isolation)
	_ = json.Unmarshal(completion, &out.CompletionCondition)
	_ = json.Unmarshal(limits, &out.Limits)
	if len(reuse) > 0 {
		var cr gen.ContextReusePolicy
		if json.Unmarshal(reuse, &cr) == nil {
			out.ContextReuseOverride = &cr
		}
	}
	out.Autonomy = gen.AutonomyLevel(autonomy)
	out.Status = gen.SessionStatus(status)
	out.PausedReason = nullable.NewNullNullable[gen.PauseReason]()
	if pausedReason != nil {
		out.PausedReason = nullable.NewNullableWithValue(gen.PauseReason(*pausedReason))
		out.PausedDetail = &gen.PausedDetail{Reason: gen.PauseReason(*pausedReason), PausedAt: out.UpdatedAt}
	}
	out.CostUsd = float32(cost)
	f := false
	out.CostEstimated = &f
	out.StartedAt = tasks.NullTime(startedAt)
	out.FinishedAt = tasks.NullTime(finishedAt)
	out.LastActivityAt = tasks.NullTime(lastActivity)
	out.CompletionProgress = progress(completion)
	if d, err := auth.LoadUser(ctx, q, out.DirectorUserId); err == nil {
		out.Director = d
	}
	if deputy != nil {
		if d, err := auth.LoadUser(ctx, q, *deputy); err == nil {
			out.DeputyDirector = d
		}
	}
	if runtimeID != nil {
		if rt, err := runtimes.Load(ctx, q, *runtimeID); err == nil {
			out.Runtime = &rt.Runtime
		}
	}
	out.MyRole = gen.SessionMyRoleMember
	if v.UserID != nil {
		switch {
		case *v.UserID == out.DirectorUserId:
			out.MyRole = gen.SessionMyRoleDirector
		case deputy != nil && *v.UserID == *deputy:
			out.MyRole = gen.SessionMyRoleDeputy
		}
	}
	parts, err := LoadParticipants(ctx, q, id, assignee, runtimeStatus)
	if err != nil {
		return nil, err
	}
	out.Participants = &parts
	ctxs := []gen.SessionContext{}
	rows, err := q.Query(ctx, `SELECT id, type, ref, summary, created_at FROM session_context WHERE session_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c gen.SessionContext
		var typ string
		var summary *string
		if err := rows.Scan(&c.Id, &typ, &c.Ref, &summary, &c.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		c.Type = gen.ContextType(typ)
		c.Summary = tasks.NullString(summary)
		ctxs = append(ctxs, c)
	}
	rows.Close()
	out.Context = &ctxs
	return &out, nil
}

// progress counts atoms of the completion tree; P1 has no satisfaction logic.
func progress(tree []byte) gen.CompletionProgress {
	var p gen.CompletionProgress
	p.Conditions = make([]struct {
		HitlRequestId nullable.Nullable[openapi_types.UUID] `json:"hitl_request_id,omitempty"`
		Met           bool                                  `json:"met"`
		MetAt         nullable.Nullable[time.Time]          `json:"met_at,omitempty"`
		MetBy         nullable.Nullable[string]             `json:"met_by,omitempty"`
		NextActor     nullable.Nullable[string]             `json:"next_actor,omitempty"`
		Path          string                                `json:"path"`
		Type          string                                `json:"type"`
	}, 0)
	var node any
	if json.Unmarshal(tree, &node) != nil {
		return p
	}
	human := false
	var walk func(n any, path string)
	walk = func(n any, path string) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		if conds, ok := m["conditions"].([]any); ok {
			for i, c := range conds {
				walk(c, fmt.Sprintf("%s/conditions/%d", path, i))
			}
			return
		}
		typ, _ := m["type"].(string)
		if typ == "user_approval" || typ == "manual" {
			human = true
		}
		p.Total++
		p.Conditions = append(p.Conditions, struct {
			HitlRequestId nullable.Nullable[openapi_types.UUID] `json:"hitl_request_id,omitempty"`
			Met           bool                                  `json:"met"`
			MetAt         nullable.Nullable[time.Time]          `json:"met_at,omitempty"`
			MetBy         nullable.Nullable[string]             `json:"met_by,omitempty"`
			NextActor     nullable.Nullable[string]             `json:"next_actor,omitempty"`
			Path          string                                `json:"path"`
			Type          string                                `json:"type"`
		}{Path: path, Type: typ})
	}
	walk(node, "")
	p.HumanGate = &human
	return p
}

// LoadParticipants returns session_participant rows with FR-1.3 derived status
// (session-scoped: offline when the session runtime is offline).
func LoadParticipants(ctx context.Context, q db.DBTX, sessionID uuid.UUID, assignee *uuid.UUID, runtimeStatus *string) ([]gen.Participant, error) {
	rows, err := q.Query(ctx, `
		SELECT sp.agent_id, sp.profile_id, sp.joined_at, a.name, a.role, a.role_description, a.avatar_url, a.respond_to, a.archived_at IS NOT NULL,
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.session_id = sp.session_id AND t.status IN ('dispatched','preparing','running')),
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.session_id = sp.session_id AND t.status = 'waiting_human'),
		       (SELECT t.failure_kind::text FROM task t WHERE t.agent_id = a.id AND t.session_id = sp.session_id AND t.status IN ('failed','completed','cancelled') ORDER BY t.finished_at DESC NULLS LAST LIMIT 1)
		FROM session_participant sp JOIN agent a ON a.id = sp.agent_id WHERE sp.session_id = $1 ORDER BY sp.joined_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gen.Participant{}
	for rows.Next() {
		var p gen.Participant
		var profileID uuid.UUID
		var role, respondTo string
		var avatar, lastFailure *string
		var archived, running, waiting bool
		if err := rows.Scan(&p.AgentId, &profileID, &p.JoinedAt, &p.Agent.Name, &role, &p.Agent.RoleDescription, &avatar, &respondTo, &archived, &running, &waiting, &lastFailure); err != nil {
			return nil, err
		}
		p.SessionId = sessionID
		p.Agent.Id = p.AgentId
		p.Agent.Role = gen.AgentRole(role)
		p.Agent.AvatarUrl = tasks.NullString(avatar)
		rt := gen.RespondTo(respondTo)
		p.Agent.RespondTo = &rt
		p.Status = agents.DeriveStatus(respondTo, archived, running, waiting, lastFailure)
		if p.Status != gen.AgentStatusDisabled && runtimeStatus != nil && *runtimeStatus == "offline" {
			p.Status = gen.AgentStatusOffline
		}
		p.IsAssignee = assignee != nil && *assignee == p.AgentId
		link := router.MentionLink(p.Agent.Name, p.AgentId)
		p.MentionLink = &link
		p.StatusNote = nullable.NewNullNullable[string]()
		p.Warnings = &[]string{}
		prof, err := agents.LoadProfile(ctx, q, profileID)
		if err != nil {
			return nil, err
		}
		p.Profile = *prof
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListOptions mirrors listSessions filters (P1: status, director, agent, runtime, q, cursor).
type ListOptions struct {
	Status         []string
	DirectorUserID *uuid.UUID
	AgentID        *uuid.UUID
	RuntimeID      *uuid.UUID
	Query          *string
	Cursor         *string
	Limit          int
}

// List is listSessions (S5), newest activity first.
func (s *Service) List(ctx context.Context, wsID uuid.UUID, o ListOptions) ([]gen.SessionListItem, *string, error) {
	if o.Limit <= 0 || o.Limit > 200 {
		o.Limit = 50
	}
	where := []string{"s.workspace_id = $1"}
	args := []any{wsID}
	if len(o.Status) > 0 {
		args = append(args, o.Status)
		where = append(where, fmt.Sprintf("s.status::text = ANY($%d)", len(args)))
	}
	if o.DirectorUserID != nil {
		args = append(args, *o.DirectorUserID)
		where = append(where, fmt.Sprintf("s.director_user_id = $%d", len(args)))
	}
	if o.AgentID != nil {
		args = append(args, *o.AgentID)
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM session_participant sp WHERE sp.session_id = s.id AND sp.agent_id = $%d)", len(args)))
	}
	if o.RuntimeID != nil {
		args = append(args, *o.RuntimeID)
		where = append(where, fmt.Sprintf("s.runtime_id = $%d", len(args)))
	}
	if o.Query != nil && *o.Query != "" {
		args = append(args, "%"+*o.Query+"%")
		where = append(where, fmt.Sprintf("(s.title ILIKE $%d OR s.goal ILIKE $%d)", len(args), len(args)))
	}
	if o.Cursor != nil {
		if cid, err := uuid.Parse(*o.Cursor); err == nil {
			args = append(args, cid)
			where = append(where, fmt.Sprintf("(s.updated_at, s.id) < (SELECT updated_at, id FROM session WHERE id = $%d)", len(args)))
		}
	}
	args = append(args, o.Limit+1)
	rows, err := s.DB.Query(ctx, `SELECT s.id FROM session s WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY s.updated_at DESC, s.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var next *string
	if len(ids) > o.Limit {
		ids = ids[:o.Limit]
		c := ids[len(ids)-1].String()
		next = &c
	}
	out := []gen.SessionListItem{}
	for _, id := range ids {
		item, err := s.listItem(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, *item)
	}
	return out, next, nil
}

func (s *Service) listItem(ctx context.Context, id uuid.UUID) (*gen.SessionListItem, error) {
	sess, err := Load(ctx, s.DB, id, Viewer{})
	if err != nil {
		return nil, err
	}
	item := &gen.SessionListItem{
		Id: sess.Id, Title: sess.Title, Goal: sess.Goal, Status: sess.Status, PausedReason: sess.PausedReason,
		CostUsd: sess.CostUsd, CostEstimated: sess.CostEstimated, RuntimeId: sess.RuntimeId, LastActivityAt: sess.LastActivityAt, CreatedAt: sess.CreatedAt,
	}
	if sess.Director != nil {
		item.Director = *sess.Director
	}
	item.CompletionProgress.Met = sess.CompletionProgress.Met
	item.CompletionProgress.Total = sess.CompletionProgress.Total
	item.BudgetUsd = nullable.NewNullNullable[float32]()
	if sess.Limits.BudgetUsd.IsSpecified() && !sess.Limits.BudgetUsd.IsNull() {
		item.BudgetUsd = sess.Limits.BudgetUsd
	}
	item.Participants = make([]struct {
		AgentId   openapi_types.UUID        `json:"agent_id"`
		AvatarUrl nullable.Nullable[string] `json:"avatar_url,omitempty"`
		Name      string                    `json:"name"`
	}, 0)
	if sess.Participants != nil {
		for _, p := range *sess.Participants {
			item.Participants = append(item.Participants, struct {
				AgentId   openapi_types.UUID        `json:"agent_id"`
				AvatarUrl nullable.Nullable[string] `json:"avatar_url,omitempty"`
				Name      string                    `json:"name"`
			}{AgentId: p.AgentId, AvatarUrl: p.Agent.AvatarUrl, Name: p.Agent.Name})
		}
	}
	err = s.DB.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM hitl_request h WHERE h.session_id = $1 AND h.status = 'open'),
		       (SELECT count(*) FROM lane l WHERE l.session_id = $1 AND l.status = 'blocked'),
		       (SELECT count(*) FROM lane l WHERE l.session_id = $1 AND l.status = 'failed'),
		       (SELECT count(*) FROM lane l WHERE l.session_id = $1 AND l.status = 'running')`, id).
		Scan(&item.Attention.HitlOpen, &item.Attention.Blocked, &item.Attention.Failed, &item.RunningLaneCount)
	return item, err
}
