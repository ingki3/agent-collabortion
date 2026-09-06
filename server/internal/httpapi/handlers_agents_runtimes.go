package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/agents"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
)

// ── runtimes ──

func (s *Server) ListRuntimes(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.ListRuntimesParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	var status *string
	if params.Status != nil {
		st := string(*params.Status)
		status = &st
	}
	out, err := s.Runtimes.List(r.Context(), workspaceId, status)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreatePairing(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.CreatePairingParams) {
	u, p := s.admin(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.CreatePairingJSONBody
	if len(body) > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		pr, err := s.Runtimes.CreatePairing(r.Context(), workspaceId, u.Id, in.Name)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, pr, nil
	})
}

func (s *Server) GetPairing(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, pairingId openapi_types.UUID) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	pr, err := s.Runtimes.GetPairing(r.Context(), workspaceId, pairingId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

func (s *Server) GetRuntime(w http.ResponseWriter, r *http.Request, runtimeId gen.RuntimeId) {
	rt, err := s.Runtimes.Get(r.Context(), runtimeId)
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, _, p := s.member(r, rt.WorkspaceId); p != nil {
		if p.Status == http.StatusForbidden {
			p = apperr.NotFound("runtime")
		}
		writeProblem(w, p)
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

// ── agents ──

func (s *Server) ListAgents(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.ListAgentsParams) {
	u, _, p := s.member(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	o := agents.ListOptions{Cursor: params.Cursor}
	if params.Role != nil {
		v := string(*params.Role)
		o.Role = &v
	}
	if params.RuntimeKind != nil {
		v := string(*params.RuntimeKind)
		o.RuntimeKind = &v
	}
	if params.Status != nil {
		v := string(*params.Status)
		o.Status = &v
	}
	if params.OwnerId != nil {
		v := *params.OwnerId
		o.OwnerID = &v
	}
	if params.IncludeArchived != nil {
		o.IncludeArchived = *params.IncludeArchived
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	if params.Limit != nil {
		o.Limit = *params.Limit
	}
	items, next, err := s.Agents.List(r.Context(), workspaceId, &u.Id, o)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) CreateAgent(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.CreateAgentParams) {
	u, _, p := s.member(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.AgentCreate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		a, err := s.Agents.Create(r.Context(), workspaceId, u.Id, in)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, a, nil
	})
}

func (s *Server) agentAccess(r *http.Request, agentId gen.AgentId) (*gen.User, *Problem) {
	wsID, err := s.Agents.WorkspaceOf(r.Context(), agentId)
	if err != nil {
		return nil, apperr.As(err)
	}
	u, _, p := s.member(r, wsID)
	if p != nil && p.Status == http.StatusForbidden {
		return nil, apperr.NotFound("agent")
	}
	return u, p
}

func (s *Server) GetAgent(w http.ResponseWriter, r *http.Request, agentId gen.AgentId) {
	u, p := s.agentAccess(r, agentId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	a, err := s.Agents.Get(r.Context(), agentId, &u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) UpdateAgent(w http.ResponseWriter, r *http.Request, agentId gen.AgentId) {
	u, p := s.agentAccess(r, agentId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	a, err := s.Agents.Get(r.Context(), agentId, &u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	m, _ := s.Auth.Member(r.Context(), a.WorkspaceId, u.Id)
	if a.OwnerId != u.Id && (m == nil || (m.Role != "owner" && m.Role != "admin")) {
		writeProblem(w, apperr.Forbidden("not_agent_owner", "only the agent owner or a workspace owner/admin can edit this agent"))
		return
	}
	var in gen.AgentUpdate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Agents.Update(r.Context(), agentId, u.Id, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// agentEditor is the "권한: 소유자, owner · admin" gate the profile operations
// share with updateAgent. A profile decides which runtime and model an agent
// runs on, so it is an owner-level edit, not a member-level one.
func (s *Server) agentEditor(r *http.Request, agentId gen.AgentId) (*gen.User, *Problem) {
	u, p := s.agentAccess(r, agentId)
	if p != nil {
		return nil, p
	}
	a, err := s.Agents.Get(r.Context(), agentId, &u.Id)
	if err != nil {
		return nil, apperr.As(err)
	}
	m, _ := s.Auth.Member(r.Context(), a.WorkspaceId, u.Id)
	if a.OwnerId != u.Id && (m == nil || (m.Role != "owner" && m.Role != "admin")) {
		return nil, apperr.Forbidden("not_agent_owner", "only the agent owner or a workspace owner/admin can edit this agent")
	}
	return u, nil
}

// CreateAgentProfile is openapi createAgentProfile (x-phase P2). It was 501,
// which meant an agent created without a usable profile — the template mapping
// failure of §6.4 — could never be given one (S-24 · S-30).
func (s *Server) CreateAgentProfile(w http.ResponseWriter, r *http.Request, agentId gen.AgentId) {
	if _, p := s.agentEditor(r, agentId); p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.AgentProfileCreate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Agents.CreateProfile(r.Context(), agentId, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// UpdateAgentProfile is openapi updateAgentProfile (x-phase P2): edit, pick the
// default, and — the reason E8-08 had to reach into the database — set the
// fallback profile.
func (s *Server) UpdateAgentProfile(w http.ResponseWriter, r *http.Request, agentId gen.AgentId, profileId gen.ProfileId) {
	if _, p := s.agentEditor(r, agentId); p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.AgentProfileUpdate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Agents.UpdateProfile(r.Context(), agentId, profileId, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ListRuntimeCandidates is GET /workspaces/{id}/runtime-candidates — S6 4단계
// and S17 rebinding (FR-2.1, FR-9.2 F). Ineligible runtimes are returned too,
// with a reason, so the wizard draws them disabled instead of showing "후보 0".
func (s *Server) ListRuntimeCandidates(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.ListRuntimeCandidatesParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	q := runtimes.CandidateQuery{Isolation: string(params.Isolation), SessionID: params.SessionId}
	if params.RemoteUrl != nil {
		q.RemoteURL = *params.RemoteUrl
	}
	auto, cands, err := s.Runtimes.Candidates(r.Context(), workspaceId, q)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auto_select_allowed": auto, "candidates": cands})
}

// ── agent templates (FR-1.4) ──

// ListAgentTemplates is GET /workspaces/{id}/agent-templates: the three teams
// with per-agent profile mapping computed from this workspace's online
// runtimes. The contract's response is a bare array.
func (s *Server) ListAgentTemplates(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Agents.ListTemplates(r.Context(), workspaceId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ApplyAgentTemplate is POST /workspaces/{id}/agent-templates/{key}/apply —
// the P2 "3분 이내" path. Any workspace member may apply one; the caller
// becomes the agents' owner.
func (s *Server) ApplyAgentTemplate(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, templateKey gen.AgentTemplateKey, params gen.ApplyAgentTemplateParams) {
	u, _, p := s.member(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.ApplyAgentTemplateJSONBody
	if len(body) > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	var runtimeID *uuid.UUID
	if in.RuntimeId.IsSpecified() && !in.RuntimeId.IsNull() {
		v := uuid.UUID(in.RuntimeId.MustGet())
		runtimeID = &v
	}
	overrides := map[string]string{}
	if in.NameOverrides != nil {
		overrides = *in.NameOverrides
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		out, err := s.Agents.ApplyTemplate(r.Context(), workspaceId, u.Id, string(templateKey), runtimeID, overrides)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, out, nil
	})
}
