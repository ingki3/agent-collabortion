// Package agents is FR-1.1 agent CRUD with one profile (P1) and the derived
// status (FR-1.3, never stored) plus the caller's invite permission (FR-1.9).
package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

type Service struct {
	DB    *pgxpool.Pool
	Clock clock.Clock
}

func New(pool *pgxpool.Pool, c clock.Clock) *Service { return &Service{DB: pool, Clock: c} }

var v1RuntimeKinds = []string{"claude_code", "hermes"}

// Create inserts the agent and its profiles; exactly one profile is default
// (the first when none is flagged). Name is unique per workspace (409).
func (s *Service) Create(ctx context.Context, wsID, ownerID uuid.UUID, in gen.AgentCreate) (*gen.Agent, error) {
	var errs []apperr.FieldError
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 40 {
		errs = append(errs, apperr.Field("name", "length", "name must be 1–40 characters"))
	}
	if strings.ContainsAny(in.Name, "[]()@") {
		errs = append(errs, apperr.Field("name", "invalid", "name cannot contain [ ] ( ) @"))
	}
	if strings.TrimSpace(in.RoleDescription) == "" {
		errs = append(errs, apperr.Field("role_description", "required", "role_description is required"))
	}
	if strings.TrimSpace(in.Instructions) == "" {
		errs = append(errs, apperr.Field("instructions", "required", "instructions is required"))
	}
	if len(in.Profiles) == 0 {
		errs = append(errs, apperr.Field("profiles", "min_items", "at least one profile is required"))
	}
	defaults := 0
	for i, p := range in.Profiles {
		if p.IsDefault != nil && *p.IsDefault {
			defaults++
		}
		if !slices.Contains(v1RuntimeKinds, string(p.RuntimeKind)) {
			errs = append(errs, apperr.Field(fmt.Sprintf("profiles/%d/runtime_kind", i), "unsupported", "v1 runtimes are claude_code and hermes"))
		}
		if strings.TrimSpace(p.Model) == "" || strings.TrimSpace(p.Name) == "" {
			errs = append(errs, apperr.Field(fmt.Sprintf("profiles/%d", i), "required", "profile needs name and model"))
		}
	}
	if defaults > 1 {
		errs = append(errs, apperr.Field("profiles", "one_default", "exactly one profile can be is_default"))
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	respondTo := gen.RespondToOwner
	if in.RespondTo != nil {
		respondTo = *in.RespondTo
	}
	allow := []uuid.UUID{}
	if in.RespondToAllowlist != nil {
		for _, id := range *in.RespondToAllowlist {
			allow = append(allow, uuid.UUID(id))
		}
	}
	tools := []string{}
	if in.Tools != nil {
		tools = *in.Tools
	}
	maxConc := 3
	if in.MaxConcurrentTasks != nil {
		maxConc = *in.MaxConcurrentTasks
	}
	var budget *float64
	if in.BudgetPerTask.IsSpecified() && !in.BudgetPerTask.IsNull() {
		v := float64(in.BudgetPerTask.MustGet())
		budget = &v
	}
	var avatar *string
	if in.AvatarUrl.IsSpecified() && !in.AvatarUrl.IsNull() {
		v := in.AvatarUrl.MustGet()
		avatar = &v
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, role, role_description, instructions, tools, owner_id, respond_to, respond_to_allowlist,
		                   avatar_url, budget_per_task, max_concurrent_tasks, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13) RETURNING id`,
		wsID, in.Name, string(in.Role), in.RoleDescription, in.Instructions, tools, ownerID, string(respondTo), allow,
		avatar, budget, maxConc, now).Scan(&id)
	if isUnique(err) {
		return nil, apperr.Conflict("name_taken", "an agent with this name already exists in the workspace")
	}
	if err != nil {
		return nil, fmt.Errorf("agents: insert: %w", err)
	}
	byName := map[string]uuid.UUID{}
	ids := make([]uuid.UUID, len(in.Profiles))
	for i, p := range in.Profiles {
		isDefault := (p.IsDefault != nil && *p.IsDefault) || (defaults == 0 && i == 0)
		var pid uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO agent_profile (agent_id, name, runtime_kind, model, options, env, args, is_default, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9) RETURNING id`,
			id, p.Name, string(p.RuntimeKind), p.Model, profileOptions(p.Options), profileEnv(p.Env), profileArgs(p.Args), isDefault, now).Scan(&pid); err != nil {
			if isUnique(err) {
				return nil, apperr.Validation(apperr.Field(fmt.Sprintf("profiles/%d/name", i), "duplicate", "profile names must be unique"))
			}
			return nil, fmt.Errorf("agents: insert profile: %w", err)
		}
		ids[i], byName[p.Name] = pid, pid
	}
	// S-24: the fallback link is a second pass because a profile may name one
	// that is later in the same array (FR-1.7 `.agent.md` writes them in any
	// order). Before this the INSERT column list simply had no
	// fallback_profile_id, so BOTH contract fields were accepted and dropped:
	// the API said yes and E8-08's alternate profile silently never existed.
	for i, p := range in.Profiles {
		target, err := resolveCreateFallback(p, byName, ids[i], i)
		if err != nil {
			return nil, err
		}
		if target == nil {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_profile SET fallback_profile_id = $2, updated_at = $3 WHERE id = $1`,
			ids[i], *target, now); err != nil {
			return nil, fmt.Errorf("agents: link fallback profile: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id, &ownerID)
}

// resolveCreateFallback picks the profile one AgentProfileCreate points at.
// `fallback_profile_id` names it by id, `fallback_profile` by name inside the
// same array; both must land on a profile of this agent, and never on itself
// (the table's CHECK says the same thing, but a 500 is not an answer).
func resolveCreateFallback(p gen.AgentProfileCreate, byName map[string]uuid.UUID, self uuid.UUID, i int) (*uuid.UUID, error) {
	field := func(name, code, msg string) error {
		return apperr.Validation(apperr.Field(fmt.Sprintf("profiles/%d/%s", i, name), code, msg))
	}
	var target *uuid.UUID
	if p.FallbackProfileId.IsSpecified() && !p.FallbackProfileId.IsNull() {
		id := uuid.UUID(p.FallbackProfileId.MustGet())
		found := false
		for _, v := range byName {
			if v == id {
				found = true
			}
		}
		if !found {
			return nil, field("fallback_profile_id", "not_found", "fallback_profile_id must be another profile of this agent")
		}
		target = &id
	}
	if p.FallbackProfile.IsSpecified() && !p.FallbackProfile.IsNull() {
		name := p.FallbackProfile.MustGet()
		id, ok := byName[name]
		if !ok {
			return nil, field("fallback_profile", "not_found", "no profile named "+name+" in this request")
		}
		target = &id
	}
	if target != nil && *target == self {
		return nil, field("fallback_profile", "self_reference", "a profile cannot fall back to itself")
	}
	return target, nil
}

// CreateProfile is openapi createAgentProfile (FR-1.6, x-phase P2). Without it
// a profile could only ever be created together with its agent, which is what
// left a template-mapped agent with no way to acquire one (S-30).
func (s *Service) CreateProfile(ctx context.Context, agentID uuid.UUID, in gen.AgentProfileCreate) (*gen.AgentProfile, error) {
	var errs []apperr.FieldError
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 40 {
		errs = append(errs, apperr.Field("name", "length", "name must be 1–40 characters"))
	}
	if strings.TrimSpace(in.Model) == "" {
		errs = append(errs, apperr.Field("model", "required", "model is required"))
	}
	if !slices.Contains(v1RuntimeKinds, string(in.RuntimeKind)) {
		errs = append(errs, apperr.Field("runtime_kind", "unsupported", "v1 runtimes are claude_code and hermes"))
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	existing, err := profileNames(ctx, tx, agentID)
	if err != nil {
		return nil, err
	}
	fallback, err := resolveFallback(ctx, tx, agentID, uuid.Nil, in.FallbackProfileId, in.FallbackProfile, existing)
	if err != nil {
		return nil, err
	}
	isDefault := in.IsDefault != nil && *in.IsDefault
	if isDefault {
		if err := clearDefault(ctx, tx, agentID, now); err != nil {
			return nil, err
		}
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_profile (agent_id, name, runtime_kind, model, options, env, args, is_default, fallback_profile_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10) RETURNING id`,
		agentID, in.Name, string(in.RuntimeKind), in.Model, profileOptions(in.Options), profileEnv(in.Env), profileArgs(in.Args),
		isDefault, fallback, now).Scan(&id)
	if isUnique(err) {
		return nil, apperr.Conflict("name_taken", "this agent already has a profile with that name")
	}
	if err != nil {
		return nil, fmt.Errorf("agents: create profile: %w", err)
	}
	out, err := LoadProfile(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateProfile is openapi updateAgentProfile — "프로파일 수정 · 기본 지정 ·
// 폴백 지정" (FR-1.6, x-phase P2).
func (s *Service) UpdateProfile(ctx context.Context, agentID, profileID uuid.UUID, in gen.AgentProfileUpdate) (*gen.AgentProfile, error) {
	var errs []apperr.FieldError
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 40) {
		errs = append(errs, apperr.Field("name", "length", "name must be 1–40 characters"))
	}
	if in.Model != nil && strings.TrimSpace(*in.Model) == "" {
		errs = append(errs, apperr.Field("model", "required", "model cannot be empty"))
	}
	if in.RuntimeKind != nil && !slices.Contains(v1RuntimeKinds, string(*in.RuntimeKind)) {
		errs = append(errs, apperr.Field("runtime_kind", "unsupported", "v1 runtimes are claude_code and hermes"))
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wasDefault bool
	err = tx.QueryRow(ctx, `SELECT is_default FROM agent_profile WHERE id = $1 AND agent_id = $2 FOR UPDATE`, profileID, agentID).Scan(&wasDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("profile")
	}
	if err != nil {
		return nil, err
	}
	set := []string{"updated_at = $2"}
	args := []any{profileID, now}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Name != nil {
		add("name", *in.Name)
	}
	if in.RuntimeKind != nil {
		add("runtime_kind", string(*in.RuntimeKind))
	}
	if in.Model != nil {
		add("model", *in.Model)
	}
	if in.Options != nil {
		add("options", *in.Options)
	}
	if in.Env != nil {
		add("env", *in.Env)
	}
	if in.Args != nil {
		add("args", *in.Args)
	}
	if in.FallbackProfileId.IsSpecified() {
		if in.FallbackProfileId.IsNull() {
			add("fallback_profile_id", nil)
		} else {
			fallback, err := resolveFallback(ctx, tx, agentID, profileID, in.FallbackProfileId, nullable.Nullable[string]{}, nil)
			if err != nil {
				return nil, err
			}
			add("fallback_profile_id", fallback)
		}
	}
	if in.IsDefault != nil {
		if *in.IsDefault {
			if err := clearDefault(ctx, tx, agentID, now); err != nil {
				return nil, err
			}
		} else if wasDefault {
			// The partial unique index tolerates zero defaults; sessions do not
			// — every participant is started on one (session_participant.
			// profile_id). Dropping the only default here would leave the agent
			// unusable with nothing saying so, so the caller names the new
			// default instead.
			return nil, apperr.Conflict("last_default",
				"make another profile the default instead of clearing this one")
		}
		add("is_default", *in.IsDefault)
	}
	_, err = tx.Exec(ctx, "UPDATE agent_profile SET "+strings.Join(set, ", ")+" WHERE id = $1", args...)
	if isUnique(err) {
		return nil, apperr.Conflict("name_taken", "this agent already has a profile with that name")
	}
	if err != nil {
		return nil, fmt.Errorf("agents: update profile: %w", err)
	}
	out, err := LoadProfile(ctx, tx, profileID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveFallback validates a fallback target against the agent's own
// profiles. openapi updateAgentProfile: "`fallback_profile_id`는 같은
// 에이전트의 다른 프로파일이어야 한다(`422`)" — a cross-agent id would make the
// runtime fall back onto a profile nobody in this session may run.
func resolveFallback(ctx context.Context, q db.DBTX, agentID, self uuid.UUID, byID nullable.Nullable[openapi_types.UUID], byNameField nullable.Nullable[string], names map[string]uuid.UUID) (*uuid.UUID, error) {
	var target *uuid.UUID
	if byID.IsSpecified() && !byID.IsNull() {
		id := uuid.UUID(byID.MustGet())
		var owner uuid.UUID
		err := q.QueryRow(ctx, `SELECT agent_id FROM agent_profile WHERE id = $1`, id).Scan(&owner)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != agentID) {
			return nil, apperr.Validation(apperr.Field("fallback_profile_id", "not_found",
				"fallback_profile_id must be another profile of this agent"))
		}
		if err != nil {
			return nil, err
		}
		target = &id
	}
	if byNameField.IsSpecified() && !byNameField.IsNull() {
		name := byNameField.MustGet()
		id, ok := names[name]
		if !ok {
			return nil, apperr.Validation(apperr.Field("fallback_profile", "not_found",
				"this agent has no profile named "+name))
		}
		target = &id
	}
	if target != nil && self != uuid.Nil && *target == self {
		return nil, apperr.Validation(apperr.Field("fallback_profile_id", "self_reference",
			"a profile cannot fall back to itself"))
	}
	return target, nil
}

func profileNames(ctx context.Context, q db.DBTX, agentID uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT id, name FROM agent_profile WHERE agent_id = $1`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}

// clearDefault unsets the agent's current default so the new one can take it.
// The `agent_profile_one_default` partial unique index makes the order matter.
func clearDefault(ctx context.Context, q db.DBTX, agentID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE agent_profile SET is_default = false, updated_at = $2 WHERE agent_id = $1 AND is_default`, agentID, now)
	return err
}

func profileOptions(v *map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return *v
}

func profileEnv(v *map[string]string) map[string]string {
	if v == nil {
		return map[string]string{}
	}
	return *v
}

func profileArgs(v *[]string) []string {
	if v == nil {
		return []string{}
	}
	return *v
}

// Update applies a partial AgentUpdate (P1: identity, instructions, respond_to).
func (s *Service) Update(ctx context.Context, id uuid.UUID, caller uuid.UUID, in gen.AgentUpdate) (*gen.Agent, error) {
	now := s.Clock.Now()
	set := []string{"updated_at = $2"}
	args := []any{id, now}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 40 || strings.ContainsAny(*in.Name, "[]()@") {
			return nil, apperr.Validation(apperr.Field("name", "invalid", "name must be 1–40 characters without [ ] ( ) @"))
		}
		add("name", *in.Name)
	}
	if in.Role != nil {
		add("role", string(*in.Role))
	}
	if in.RoleDescription != nil {
		add("role_description", *in.RoleDescription)
	}
	if in.Instructions != nil {
		add("instructions", *in.Instructions)
	}
	if in.Tools != nil {
		add("tools", *in.Tools)
	}
	if in.MaxConcurrentTasks != nil {
		add("max_concurrent_tasks", *in.MaxConcurrentTasks)
	}
	if in.RespondTo != nil {
		add("respond_to", string(*in.RespondTo))
	}
	if in.RespondToAllowlist != nil {
		ids := []uuid.UUID{}
		for _, x := range *in.RespondToAllowlist {
			ids = append(ids, uuid.UUID(x))
		}
		add("respond_to_allowlist", ids)
	}
	if in.AvatarUrl.IsSpecified() {
		if in.AvatarUrl.IsNull() {
			add("avatar_url", nil)
		} else {
			add("avatar_url", in.AvatarUrl.MustGet())
		}
	}
	if in.BudgetPerTask.IsSpecified() {
		if in.BudgetPerTask.IsNull() {
			add("budget_per_task", nil)
		} else {
			add("budget_per_task", float64(in.BudgetPerTask.MustGet()))
		}
	}
	tag, err := s.DB.Exec(ctx, `UPDATE agent SET `+strings.Join(set, ", ")+` WHERE id = $1`, args...)
	if isUnique(err) {
		return nil, apperr.Conflict("name_taken", "an agent with this name already exists in the workspace")
	}
	if err != nil {
		return nil, fmt.Errorf("agents: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.NotFound("agent")
	}
	if in.RespondTo != nil {
		_, _ = s.DB.Exec(ctx, `INSERT INTO activity_log (workspace_id, actor_type, actor_id, action, object_type, object_id, payload)
			SELECT workspace_id, 'user', $2, 'agent.respond_to_changed', 'agent', id, jsonb_build_object('respond_to', $3::text) FROM agent WHERE id = $1`,
			id, caller, string(*in.RespondTo))
	}
	return s.Get(ctx, id, &caller)
}

// WorkspaceOf returns the agent's workspace (for authorization).
func (s *Service) WorkspaceOf(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var ws uuid.UUID
	err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM agent WHERE id = $1`, id).Scan(&ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, apperr.NotFound("agent")
	}
	return ws, err
}

// ListOptions mirrors listAgents filters.
type ListOptions struct {
	Role            *string
	RuntimeKind     *string
	Status          *string
	OwnerID         *uuid.UUID
	IncludeArchived bool
	Cursor          *string
	Limit           int
}

// List returns a page of agents (cursor = last agent id, ordered by name).
func (s *Service) List(ctx context.Context, wsID uuid.UUID, caller *uuid.UUID, o ListOptions) ([]gen.Agent, *string, error) {
	if o.Limit <= 0 || o.Limit > 200 {
		o.Limit = 50
	}
	where := []string{"a.workspace_id = $1"}
	args := []any{wsID}
	if !o.IncludeArchived {
		where = append(where, "a.archived_at IS NULL")
	}
	if o.Role != nil {
		args = append(args, *o.Role)
		where = append(where, fmt.Sprintf("a.role::text = $%d", len(args)))
	}
	if o.OwnerID != nil {
		args = append(args, *o.OwnerID)
		where = append(where, fmt.Sprintf("a.owner_id = $%d", len(args)))
	}
	if o.RuntimeKind != nil {
		args = append(args, *o.RuntimeKind)
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM agent_profile p WHERE p.agent_id = a.id AND p.is_default AND p.runtime_kind::text = $%d)", len(args)))
	}
	if o.Cursor != nil {
		if cid, err := uuid.Parse(*o.Cursor); err == nil {
			args = append(args, cid)
			where = append(where, fmt.Sprintf("(a.name, a.id) > (SELECT name, id FROM agent WHERE id = $%d)", len(args)))
		}
	}
	args = append(args, o.Limit+1)
	rows, err := s.DB.Query(ctx, `SELECT a.id FROM agent a WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY a.name, a.id LIMIT $%d`, len(args)), args...)
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
	out := []gen.Agent{}
	for _, id := range ids {
		a, err := s.Get(ctx, id, caller)
		if err != nil {
			return nil, nil, err
		}
		if o.Status != nil && string(a.Status) != *o.Status {
			continue
		}
		out = append(out, *a)
	}
	return out, next, nil
}

// Get loads one agent with profiles, derived status and invitable for caller.
func (s *Service) Get(ctx context.Context, id uuid.UUID, caller *uuid.UUID) (*gen.Agent, error) {
	return Load(ctx, s.DB, id, caller)
}

// Load is Get on any DBTX.
func Load(ctx context.Context, q db.DBTX, id uuid.UUID, caller *uuid.UUID) (*gen.Agent, error) {
	var a gen.Agent
	var (
		role, respondTo               string
		allow                         []uuid.UUID
		tools                         []string
		avatar, defSource, defVersion *string
		budget                        *float64
		archived                      *time.Time
		owner                         gen.User
		ownerAvatar                   *string
		hasRunning, hasWaiting        bool
		retrying                      bool
		lastFailure                   *string
	)
	err := q.QueryRow(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.role, a.role_description, a.instructions, a.tools, a.owner_id, a.respond_to, a.respond_to_allowlist,
		       a.avatar_url, a.budget_per_task, a.max_concurrent_tasks, a.definition_source, a.definition_version, a.archived_at, a.created_at, a.updated_at,
		       u.id, u.email, u.display_name, u.avatar_url, u.created_at,
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.status IN ('dispatched','preparing','running')),
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.status = 'waiting_human'),
		       `+tasks.LastFailureKindSQL("")+`,
		       -- FR-1.3 step 3 never fires while the server is re-queueing the
		       -- failed task (attempt >= 2 back in the queue), including onto an
		       -- alternate profile — a retry in flight is not "cannot run" (E5-18).
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.status IN ('queued','deferred') AND t.attempt > 1)
		FROM agent a JOIN app_user u ON u.id = a.owner_id WHERE a.id = $1`, id).Scan(
		&a.Id, &a.WorkspaceId, &a.Name, &role, &a.RoleDescription, &a.Instructions, &tools, &a.OwnerId, &respondTo, &allow,
		&avatar, &budget, &a.MaxConcurrentTasks, &defSource, &defVersion, &archived, &a.CreatedAt, &a.UpdatedAt,
		&owner.Id, &owner.Email, &owner.DisplayName, &ownerAvatar, &owner.CreatedAt,
		&hasRunning, &hasWaiting, &lastFailure, &retrying)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("agent")
	}
	if err != nil {
		return nil, fmt.Errorf("agents: load: %w", err)
	}
	a.Role = gen.AgentRole(role)
	a.RespondTo = gen.RespondTo(respondTo)
	a.Tools = tools
	if a.Tools == nil {
		a.Tools = []string{}
	}
	a.RespondToAllowlist = []openapi_types.UUID{}
	for _, x := range allow {
		a.RespondToAllowlist = append(a.RespondToAllowlist, openapi_types.UUID(x))
	}
	a.AvatarUrl = tasks.NullString(avatar)
	a.BudgetPerTask = tasks.NullFloat(budget)
	a.DefinitionSource = tasks.NullString(defSource)
	a.DefinitionVersion = tasks.NullString(defVersion)
	a.ArchivedAt = tasks.NullTime(archived)
	owner.AvatarUrl = tasks.NullString(ownerAvatar)
	a.Owner = &owner
	a.Status = gen.AgentStatus(tasks.DeriveAgentStatus(tasks.Derived{
		RespondTo: respondTo, Archived: archived != nil,
		Running: boolCount(hasRunning), WaitingHuman: boolCount(hasWaiting),
		LastFailureKind: deref(lastFailure), RetryInFlight: retrying,
	}))
	allowed, reason := Invitable(respondTo, a.OwnerId, allow, caller)
	a.Invitable.Allowed = allowed
	a.Invitable.Reason = nullable.NewNullNullable[string]()
	if reason != "" {
		a.Invitable.Reason = nullable.NewNullableWithValue(reason)
	}
	profiles, err := LoadProfiles(ctx, q, id)
	if err != nil {
		return nil, err
	}
	a.Profiles = profiles
	return &a, nil
}

// LoadProfiles returns the agent's profiles, default first.
func LoadProfiles(ctx context.Context, q db.DBTX, agentID uuid.UUID) ([]gen.AgentProfile, error) {
	rows, err := q.Query(ctx, `
		SELECT id, agent_id, name, runtime_kind, model, options, env, args, is_default, fallback_profile_id, created_at, updated_at
		FROM agent_profile WHERE agent_id = $1 ORDER BY is_default DESC, created_at`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gen.AgentProfile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// LoadProfile returns one profile.
func LoadProfile(ctx context.Context, q db.DBTX, id uuid.UUID) (*gen.AgentProfile, error) {
	p, err := scanProfile(q.QueryRow(ctx, `
		SELECT id, agent_id, name, runtime_kind, model, options, env, args, is_default, fallback_profile_id, created_at, updated_at
		FROM agent_profile WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("profile")
	}
	return p, err
}

func scanProfile(row pgx.Row) (*gen.AgentProfile, error) {
	var p gen.AgentProfile
	var kind string
	var fallback *uuid.UUID
	if err := row.Scan(&p.Id, &p.AgentId, &p.Name, &kind, &p.Model, &p.Options, &p.Env, &p.Args, &p.IsDefault, &fallback, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.RuntimeKind = gen.RuntimeKind(kind)
	p.FallbackProfileId = tasks.NullUUID(fallback)
	if p.Options == nil {
		p.Options = map[string]any{}
	}
	if p.Env == nil {
		p.Env = map[string]string{}
	}
	if p.Args == nil {
		p.Args = []string{}
	}
	return &p, nil
}

// boolCount adapts the EXISTS probes above to the ladder's counts: FR-1.3 only
// asks "is there one", never how many.
func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Invitable is FR-1.9: may caller invite this agent into a session?
func Invitable(respondTo string, owner uuid.UUID, allow []uuid.UUID, caller *uuid.UUID) (bool, string) {
	switch respondTo {
	case "nobody":
		return false, "kill switch: respond_to is nobody"
	case "workspace":
		return true, ""
	case "allowlist":
		if caller != nil && (*caller == owner || slices.Contains(allow, *caller)) {
			return true, ""
		}
		return false, "not on this agent's allowlist"
	default: // owner
		if caller != nil && *caller == owner {
			return true, ""
		}
		return false, "only the owner can invite this agent"
	}
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
