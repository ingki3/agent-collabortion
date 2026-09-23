package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// DeleteSession is deleteSession (openapi 0.1.3, FR-2.7, Director request
// 2026-09-14: the S5 card's 「…」 menu).
//
// Permission is "Director 또는 owner·admin" — the Director alone is not
// enough of a gate when the Director has left the company (changeDirector's
// t-5 reasoning), and a plain member deleting someone else's session is what
// the 403 stops. A non-member gets 404, never a hint that the session exists.
// The deputy is NOT included: like completeSession this is not undoable.
//
// Everything after the gate — status, worktrees, gc, the cascade, the one
// activity line and the SSE frame — is sessions.Service.Delete, in one
// transaction. The second call finds no row and answers 404 (the contract:
// 멱등이 아니다).
func (s *Server) DeleteSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var wsID, director uuid.UUID
	err := s.DB.QueryRow(r.Context(), `SELECT s.workspace_id, wk.director_user_id FROM room s JOIN work wk ON wk.room_id = s.id WHERE s.id = $1`, sessionId).
		Scan(&wsID, &director)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, apperr.NotFound("session"))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	m, err := s.Auth.Member(r.Context(), wsID, u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if m == nil {
		writeProblem(w, apperr.NotFound("session"))
		return
	}
	if u.Id != director && m.Role != "owner" && m.Role != "admin" {
		// The code is the web's (PR #219 mock): not `director_required`,
		// because the sentence names two roles and S5 shows it verbatim.
		writeProblem(w, apperr.Forbidden("director_or_admin_required", sessions.DeleteForbiddenDetail))
		return
	}
	if err := s.Sessions.Delete(r.Context(), sessionId, u.Id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
