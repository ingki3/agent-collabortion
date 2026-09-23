package httpapi

import (
	"errors"
	"net/http"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
)

// GetMessage is getMessage (S7·S8 — the inbox `mention` · `lane_blocked`
// card opens the message it points at). 501 until T-S-toapi (T-R2-W3 found it).
//
// Access is the room's: the message's room goes through sessionAccess, the
// same rooms.Decide(ActView) listMessages uses, and a task token reads its
// own room only. Every "you may not see it" answer is the message's 404 —
// a message in a room the caller cannot open does not exist for them
// (FR-5.3 존재 숨김), and 403 outside_task_scope would say that it does.
func (s *Server) GetMessage(w http.ResponseWriter, r *http.Request, messageId gen.MessageId) {
	pr := principalOf(r)
	if pr.Task == nil && pr.User == nil {
		writeProblem(w, apperr.Unauthorized("unauthorized", "로그인이 필요합니다"))
		return
	}
	m, err := messages.Get(r.Context(), s.DB, messageId)
	if errors.Is(err, messages.ErrNotFound) {
		writeProblem(w, messageNotFound())
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, p := s.sessionAccess(r, m.SessionID); p != nil {
		if p.Status == http.StatusNotFound || p.Status == http.StatusForbidden {
			p = messageNotFound()
		}
		writeProblem(w, p)
		return
	}
	writeJSON(w, http.StatusOK, messages.ToAPI(m))
}

// messageNotFound is composed here rather than through
// apperr.NotFound("message") for the reason memberNotFound gives:
// apperr.NotFoundNouns is mirrored item for item by
// web/lib/mock/server-wording.test.ts, and this task may not touch web/.
func messageNotFound() *Problem {
	return apperr.New(http.StatusNotFound, "not_found", "메시지를 찾을 수 없습니다")
}
