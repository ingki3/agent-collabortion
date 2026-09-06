package httpapi

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

func (s *Server) Signup(w http.ResponseWriter, r *http.Request) {
	var in gen.SignupJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	res, token, err := s.Auth.Signup(r.Context(), in.DisplayName, string(in.Email), in.Password, in.InviteToken)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var in gen.LoginJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	res, token, err := s.Auth.Login(r.Context(), string(in.Email), in.Password, in.InviteToken)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	if p.User == nil {
		writeProblem(w, apperr.Unauthorized("unauthorized", "login required"))
		return
	}
	if err := s.Auth.Logout(r.Context(), p.SessionToken); err != nil {
		writeErr(w, err)
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	me, err := s.Auth.Me(r.Context(), u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, me)
}

func (s *Server) PreviewInvite(w http.ResponseWriter, r *http.Request, inviteToken gen.InviteToken) {
	res, err := s.Auth.PreviewInvite(r.Context(), inviteToken)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) AcceptInvite(w http.ResponseWriter, r *http.Request, inviteToken gen.InviteToken) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	m, err := s.Auth.AcceptInvite(r.Context(), inviteToken, u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Auth.ListWorkspaces(r.Context(), u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreateWorkspace(w http.ResponseWriter, r *http.Request, params gen.CreateWorkspaceParams) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.CreateWorkspaceJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		ws, err := s.Auth.CreateWorkspace(r.Context(), u.Id, in.Name, in.Slug)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, ws, nil
	})
}

func optKey(k *openapi_types.UUID) string {
	if k == nil {
		return ""
	}
	return k.String()
}

func (s *Server) GetWorkspace(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	ws, err := s.Auth.GetWorkspace(r.Context(), workspaceId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (s *Server) ListMembers(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.ListMembersParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	items, next, err := s.Auth.ListMembers(r.Context(), workspaceId, params.Cursor, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) ListInvites(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId) {
	if _, p := s.admin(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Auth.ListInvites(r.Context(), workspaceId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreateInvite(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.CreateInviteParams) {
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
	var in gen.CreateInviteJSONBody
	if len(body) > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	var email *string
	if in.Email.IsSpecified() && !in.Email.IsNull() {
		e := string(in.Email.MustGet())
		email = &e
	}
	role := "member"
	if in.Role != nil {
		role = string(*in.Role)
	}
	hours := 168
	if in.ExpiresInHours != nil {
		hours = *in.ExpiresInHours
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		inv, err := s.Auth.CreateInvite(r.Context(), workspaceId, u.Id, email, role, hours)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, inv, nil
	})
}

func (s *Server) RevokeInvite(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, inviteId openapi_types.UUID) {
	if _, p := s.admin(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	if err := s.Auth.RevokeInvite(r.Context(), workspaceId, inviteId); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
