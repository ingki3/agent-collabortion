package httpapi

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/agents"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
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
