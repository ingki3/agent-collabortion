package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// FR-1.4 팀 템플릿 3종 — 리서치 팀 · 개발 팀 · 콘텐츠 팀. "프리셋은 역할·
// instruction만 담고, 프로파일은 사용자의 런타임에 맞춰 첫 실행 시 매핑"(PRD
// FR-1.4), so a template carries no model: `prefer` is only the runtime_kind
// the mapping tries first.
//
// The strings are the same three teams the web mock serves (web/lib/mock/
// store.ts TEMPLATES) — the mock and the server must not describe different
// teams, or S9 changes meaning when COLAB_MOCK_API flips.

type templateAgent struct {
	Key             string
	Name            string
	Role            string
	RoleDescription string
	Instructions    string
	Prefer          string // runtime_kind tried first
}

type templateDef struct {
	Key         string
	Name        string
	Description string
	Version     string
	Agents      []templateAgent
}

var templates = []templateDef{
	{
		Key: "research_team", Name: "리서치 팀", Version: "1",
		Description: "조사 → 정리 → 검토. Lead 가 위임하고 Writer 가 보고서를 제출합니다.",
		Agents: []templateAgent{
			{Key: "lead", Name: "Lead", Role: "lead", RoleDescription: "goal 을 쪼개 위임하고 결과를 종합한다",
				Instructions: "너는 리서치 팀의 Lead 다. goal 을 조사 단위로 쪼개 참여자에게 위임하고, 결과를 종합해 Writer 에게 넘긴다.", Prefer: "claude_code"},
			{Key: "researcher", Name: "Researcher", Role: "researcher", RoleDescription: "자료를 찾아 근거와 함께 정리한다",
				Instructions: "너는 조사 담당이다. 출처를 반드시 남기고, 확인되지 않은 것은 확인되지 않았다고 쓴다.", Prefer: "claude_code"},
			{Key: "writer", Name: "Writer", Role: "writer", RoleDescription: "조사 결과를 읽는 사람 기준으로 다시 쓴다",
				Instructions: "너는 작성 담당이다. 조사 결과를 독자 기준으로 다시 쓰고 아티팩트로 제출한다.", Prefer: "claude_code"},
		},
	},
	{
		Key: "dev_team", Name: "개발 팀", Version: "1",
		Description: "설계 → 구현 → 리뷰. worktree 격리와 diff 아티팩트에 맞춘 구성입니다.",
		Agents: []templateAgent{
			{Key: "lead", Name: "Lead", Role: "lead", RoleDescription: "작업을 쪼개 위임하고 통합한다",
				Instructions: "너는 개발 팀의 Lead 다. 변경을 독립적인 단위로 쪼개 위임하고 충돌을 조정한다.", Prefer: "claude_code"},
			{Key: "engineer", Name: "Engineer", Role: "engineer", RoleDescription: "코드를 쓰고 diff 를 제출한다",
				Instructions: "너는 구현 담당이다. 작은 단위로 커밋하고 diff 를 아티팩트로 제출한다. 테스트 없이 제출하지 않는다.", Prefer: "claude_code"},
			{Key: "reviewer", Name: "Reviewer", Role: "reviewer", RoleDescription: "제출된 diff 를 검토하고 승인·반려한다",
				Instructions: "너는 리뷰 담당이다. 결함을 찾고 근거와 함께 승인 또는 반려한다. 반려에는 반드시 사유를 쓴다.", Prefer: "hermes"},
		},
	},
	{
		Key: "content_team", Name: "콘텐츠 팀", Version: "1",
		Description: "기획 → 초안 → 교정. 문서·마케팅 산출물에 맞춘 구성입니다.",
		Agents: []templateAgent{
			{Key: "lead", Name: "Lead", Role: "lead", RoleDescription: "주제를 쪼개 위임하고 톤을 맞춘다",
				Instructions: "너는 콘텐츠 팀의 Lead 다. 주제를 쪼개 위임하고 전체 톤을 맞춘다.", Prefer: "claude_code"},
			{Key: "writer", Name: "Writer", Role: "writer", RoleDescription: "초안을 쓴다",
				Instructions: "너는 초안 담당이다. 독자와 목적을 먼저 확인하고, 모르면 묻는다.", Prefer: "claude_code"},
			{Key: "reviewer", Name: "Editor", Role: "reviewer", RoleDescription: "사실과 문장을 교정한다",
				Instructions: "너는 교정 담당이다. 사실 오류를 먼저 잡고 그 다음 문장을 고친다.", Prefer: "claude_code"},
		},
	},
}

func findTemplate(key string) *templateDef {
	for i := range templates {
		if templates[i].Key == key {
			return &templates[i]
		}
	}
	return nil
}

// runtimeModels is the workspace's advertised (runtime_kind → models) from the
// probes of its **online** runtimes (FR-1.4 "이 워크스페이스의 온라인 런타임
// 능력으로 계산한"). runtimeID narrows it to one machine — applyTemplate's
// `runtime_id` — because that is the machine the session will be pinned to.
// A capability that is not logged in advertises nothing usable.
func (s *Service) runtimeModels(ctx context.Context, wsID uuid.UUID, runtimeID *uuid.UUID) (map[string][]string, []string, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT capabilities FROM runtime
		WHERE workspace_id = $1 AND ($2::uuid IS NULL OR id = $2) AND ($2::uuid IS NOT NULL OR status = 'online')
		ORDER BY created_at`, wsID, runtimeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	var order []string
	for rows.Next() {
		var caps []gen.RuntimeCapability
		if err := rows.Scan(&caps); err != nil {
			return nil, nil, err
		}
		for _, c := range caps {
			if !c.LoggedIn || c.Models == nil || len(*c.Models) == 0 {
				continue
			}
			k := string(c.Kind)
			if _, seen := out[k]; !seen {
				order = append(order, k)
			}
			for _, m := range *c.Models {
				if !contains(out[k], m) {
					out[k] = append(out[k], m)
				}
			}
		}
	}
	return out, order, rows.Err()
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// mapping picks the profile a template agent would get. It prefers the
// template's runtime_kind and falls back to whatever the workspace actually
// advertises — a Reviewer that wanted Hermes is still worth creating on a
// machine that only has Claude Code, with the substitution said out loud.
func mapping(models map[string][]string, order []string, prefer string) (kind, model, reason string) {
	if ms := models[prefer]; len(ms) > 0 {
		return prefer, ms[0], ""
	}
	for _, k := range order {
		if ms := models[k]; len(ms) > 0 {
			return k, ms[0], prefer + " 가 없어 " + k + " 로 매핑했습니다"
		}
	}
	return "", "", "감지된 런타임이 없습니다 — 먼저 컴퓨터를 연결하세요"
}

// ListTemplates is GET /workspaces/{id}/agent-templates: the three teams with
// this workspace's mapping result per agent (openapi `type: array`).
func (s *Service) ListTemplates(ctx context.Context, wsID uuid.UUID) ([]gen.AgentTemplate, error) {
	models, order, err := s.runtimeModels(ctx, wsID, nil)
	if err != nil {
		return nil, err
	}
	out := make([]gen.AgentTemplate, 0, len(templates))
	for _, t := range templates {
		version := t.Version
		g := gen.AgentTemplate{Key: gen.AgentTemplateKey(t.Key), Name: t.Name, Description: t.Description, Version: &version}
		for _, a := range t.Agents {
			kind, model, reason := mapping(models, order, a.Prefer)
			row := struct {
				Key string `json:"key"`

				// Mapping 이 워크스페이스 런타임으로의 프로파일 자동 매핑 결과.
				Mapping struct {
					Model  *string                   `json:"model,omitempty"`
					Reason nullable.Nullable[string] `json:"reason,omitempty"`

					// RuntimeKind `runtime_kind` (FR-1.6)
					RuntimeKind *gen.RuntimeKind                     `json:"runtime_kind,omitempty"`
					Status      gen.AgentTemplateAgentsMappingStatus `json:"status"`
				} `json:"mapping"`
				Name string `json:"name"`

				// Role `agent_role` (FR-1.1)
				Role            gen.AgentRole `json:"role"`
				RoleDescription string        `json:"role_description"`
			}{Key: a.Key, Name: a.Name, Role: gen.AgentRole(a.Role), RoleDescription: a.RoleDescription}
			row.Mapping.Reason = nullable.NewNullNullable[string]()
			if reason != "" {
				row.Mapping.Reason = nullable.NewNullableWithValue(reason)
			}
			if kind == "" {
				row.Mapping.Status = gen.Unmapped
			} else {
				rk := gen.RuntimeKind(kind)
				m := model
				row.Mapping.Status, row.Mapping.RuntimeKind, row.Mapping.Model = gen.Mapped, &rk, &m
			}
			g.Agents = append(g.Agents, row)
		}
		out = append(out, g)
	}
	return out, nil
}

// TemplateResult is applyAgentTemplate's 201 body.
type TemplateResult struct {
	Agents   []gen.Agent     `json:"agents"`
	Unmapped []TemplateUnmap `json:"unmapped"`
}

type TemplateUnmap struct {
	AgentId openapi_types.UUID `json:"agent_id"`
	Reason  string             `json:"reason"`
}

// ApplyTemplate creates the template's agents with the caller as owner
// (openapi applyAgentTemplate). An agent whose profile cannot be mapped is
// **still created** — the contract says so, and the alternative is a half team
// the user must finish by hand without being told which half is missing. It
// comes back in `unmapped[]` with the reason instead.
//
// This is the P2 DoD "3분 이내" path, so it is one transaction: either the
// whole team exists or none of it does.
func (s *Service) ApplyTemplate(ctx context.Context, wsID, ownerID uuid.UUID, key string, runtimeID *uuid.UUID, overrides map[string]string) (*TemplateResult, error) {
	t := findTemplate(key)
	if t == nil {
		return nil, apperr.NotFound("agent_template")
	}
	if runtimeID != nil {
		var owner uuid.UUID
		err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM runtime WHERE id = $1`, *runtimeID).Scan(&owner)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != wsID) {
			return nil, apperr.Validation(apperr.Field("runtime_id", "not_found", "no such runtime in this workspace"))
		}
		if err != nil {
			return nil, err
		}
	}
	models, order, err := s.runtimeModels(ctx, wsID, runtimeID)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	created := make([]uuid.UUID, 0, len(t.Agents))
	unmapped := []TemplateUnmap{}
	for _, a := range t.Agents {
		name := a.Name
		if overrides != nil {
			if v, ok := overrides[a.Key]; ok && strings.TrimSpace(v) != "" {
				name = v
			}
		}
		id, err := insertTemplateAgent(ctx, tx, wsID, ownerID, name, a, t, now)
		if err != nil {
			return nil, err
		}
		created = append(created, id)
		kind, model, reason := mapping(models, order, a.Prefer)
		if kind == "" {
			unmapped = append(unmapped, TemplateUnmap{AgentId: openapi_types.UUID(id), Reason: reason})
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_profile (agent_id, name, runtime_kind, model, is_default, created_at, updated_at)
			VALUES ($1, 'default', $2, $3, true, $4, $4)`, id, kind, model, now); err != nil {
			return nil, fmt.Errorf("agents: template profile: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Read back through Get so the response is the same Agent shape createAgent
	// returns (derived status, profiles, the caller's invite permission).
	out := &TemplateResult{Agents: make([]gen.Agent, 0, len(created)), Unmapped: unmapped}
	for _, id := range created {
		a, err := s.Get(ctx, id, &ownerID)
		if err != nil {
			return nil, err
		}
		out.Agents = append(out.Agents, *a)
	}
	return out, nil
}

// insertTemplateAgent inserts one agent, sidestepping a name collision with a
// numeric suffix (the contract says `name_overrides` avoids collisions, but a
// second "리서치 팀" must not simply fail after creating half a team). The
// INSERT runs inside a SAVEPOINT: a 23505 aborts the enclosing transaction
// otherwise — the same 25P02 trap as runtime pairing (G3 S-4).
func insertTemplateAgent(ctx context.Context, tx pgx.Tx, wsID, ownerID uuid.UUID, name string, a templateAgent, t *templateDef, now time.Time) (uuid.UUID, error) {
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s %d", name, i+1)
		}
		sp, err := tx.Begin(ctx) // pgx nested tx = SAVEPOINT
		if err != nil {
			return uuid.Nil, err
		}
		var id uuid.UUID
		err = sp.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, role, role_description, instructions, tools, owner_id,
			                   definition_source, definition_version, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, '[]'::jsonb, $6, $7, $8, $9, $9) RETURNING id`,
			wsID, candidate, a.Role, a.RoleDescription, a.Instructions, ownerID, t.Key, t.Version, now).Scan(&id)
		if isUnique(err) && i < 20 {
			_ = sp.Rollback(ctx)
			continue
		}
		if err != nil {
			_ = sp.Rollback(ctx)
			return uuid.Nil, fmt.Errorf("agents: template insert: %w", err)
		}
		if err := sp.Commit(ctx); err != nil {
			return uuid.Nil, err
		}
		return id, nil
	}
}
