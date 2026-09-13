package httpapi

// S14 멤버 탭(역할 변경 · 제거) · 알림 탭(개인) — openapi updateMemberRole ·
// removeMember · getNotificationSettings · updateNotificationSettings (S-72, T-S14).
// 판정은 auth.PlanRoleChange · auth.PlanRemoval; 여기는 권한 확인과 본문 읽기뿐이다.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// adminWithRole is s.admin plus the caller's own role — the planners need to
// know whether an owner or an admin is asking (SCREEN §2.3 "owner 강등은 owner 만").
func (s *Server) adminWithRole(r *http.Request, wsID uuid.UUID) (*gen.User, string, *Problem) {
	u, m, p := s.member(r, wsID)
	if p != nil {
		return nil, "", p
	}
	if m.Role != "owner" && m.Role != "admin" {
		return nil, "", apperr.Forbidden("admin_required", "소유자·관리자만 할 수 있습니다")
	}
	return u, m.Role, nil
}

// memberNotFound is the 404 for a member id that is not in this workspace.
// Composed here rather than through apperr.NotFound("member") because
// apperr.NotFoundNouns is mirrored item for item by
// web/lib/mock/server-wording.test.ts and T-S14 may not touch web/ (the same
// reason as testChatNotFound). When the web mirror gains `member: 멤버`, move
// this into the table.
func memberNotFound() *Problem {
	return apperr.New(http.StatusNotFound, "not_found", "멤버를 찾을 수 없습니다")
}

func (s *Server) UpdateMemberRole(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, memberId gen.MemberId) {
	u, role, p := s.adminWithRole(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.UpdateMemberRoleJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	switch in.Role {
	case gen.MemberRoleOwner, gen.MemberRoleAdmin, gen.MemberRoleMember:
	default:
		writeProblem(w, apperr.Validation(apperr.Field("role", "enum", "역할은 소유자 · 관리자 · 멤버 중 하나여야 합니다")))
		return
	}
	m, err := s.Auth.UpdateMemberRole(r.Context(), workspaceId, memberId, u.Id, role, string(in.Role))
	if errors.Is(err, auth.ErrMemberNotFound) {
		writeProblem(w, memberNotFound())
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) RemoveMember(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, memberId gen.MemberId) {
	u, role, p := s.adminWithRole(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	err := s.Auth.RemoveMember(r.Context(), workspaceId, memberId, u.Id, role)
	if errors.Is(err, auth.ErrMemberNotFound) {
		writeProblem(w, memberNotFound())
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetNotificationSettings — 권한: 로그인한 사용자(본인). An agent token is
// refused by s.user (task_token_scope); there is no user id in the path, so
// nobody can read another person's settings.
func (s *Server) GetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Auth.NotificationSettings(r.Context(), u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// UpdateNotificationSettings — 권한: 로그인한 사용자(본인). The body is read as
// a patch: a key left out keeps its stored value (the screen sends all three).
func (s *Server) UpdateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in struct {
		Email               *bool   `json:"email"`
		Push                *bool   `json:"push"`
		DefaultSubscription *string `json:"default_subscription"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Auth.UpdateNotificationSettings(r.Context(), u.Id, auth.NotificationPatch{
		Email: in.Email, Push: in.Push, DefaultSubscription: in.DefaultSubscription,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
