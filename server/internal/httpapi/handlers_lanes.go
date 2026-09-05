package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// laneControl resolves the caller for a lane control operation (cancelLane):
// a logged-in member of the lane's workspace who is the session's Director or
// deputy. Non-members get 404 (the lane is not revealed), other members 403.
func (s *Server) laneControl(r *http.Request, laneID uuid.UUID) (*gen.User, uuid.UUID, uuid.UUID, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, uuid.Nil, uuid.Nil, p
	}
	var sessionID, wsID, director uuid.UUID
	var deputy *uuid.UUID
	err := s.DB.QueryRow(r.Context(), `
		SELECT l.session_id, s.workspace_id, s.director_user_id, s.deputy_director_user_id
		FROM lane l JOIN session s ON s.id = l.session_id WHERE l.id = $1`, laneID).Scan(&sessionID, &wsID, &director, &deputy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uuid.Nil, uuid.Nil, apperr.NotFound("lane")
	}
	if err != nil {
		return nil, uuid.Nil, uuid.Nil, apperr.Internal(err)
	}
	m, err := s.Auth.Member(r.Context(), wsID, u.Id)
	if err != nil {
		return nil, uuid.Nil, uuid.Nil, apperr.Internal(err)
	}
	if m == nil {
		return nil, uuid.Nil, uuid.Nil, apperr.NotFound("lane")
	}
	if u.Id != director && (deputy == nil || u.Id != *deputy) {
		return nil, uuid.Nil, uuid.Nil, apperr.Forbidden("director_required", "only the session's Director or deputy can cancel a lane (FR-3.4)")
	}
	return u, wsID, sessionID, nil
}

// CancelLane is POST /lanes/{laneId}/cancel (FR-3.4 "중단", E10-04). P1
// minimal: Director/deputy only; the lane must be running or queued (else
// 409); a running attempt gets the daemon `cancel` command and ends when its
// finish arrives, a queued task is cancelled at once. 202 with the lane;
// completion is `lane.updated` (openapi cancelLane).
func (s *Server) CancelLane(w http.ResponseWriter, r *http.Request, laneId gen.LaneId) {
	u, wsID, sessionID, p := s.laneControl(r, laneId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	_, _, err := s.Tasks.CancelLane(r.Context(), laneId, u.Id)
	switch {
	case errors.Is(err, tasks.ErrLaneNotFound):
		writeProblem(w, apperr.NotFound("lane"))
		return
	case errors.Is(err, tasks.ErrLaneNotCancellable):
		writeProblem(w, apperr.Conflict("lane_not_cancellable", "lane is not running or queued"))
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	lane, err := lanes.Load(r.Context(), s.DB, laneId, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.publishLane(r, wsID, sessionID, lane)
	writeJSON(w, http.StatusAccepted, lane)
}

// publishLane emits `lane.updated` for S7.
func (s *Server) publishLane(r *http.Request, wsID, sessionID uuid.UUID, lane *gen.Lane) {
	if s.Hub == nil {
		return
	}
	sid := sessionID
	if err := s.Hub.Publish(r.Context(), s.DB, wsID, &sid, "lane.updated", lane); err != nil {
		s.Log.Warn("publish lane.updated", "err", err, "lane", lane.Id)
	}
}
