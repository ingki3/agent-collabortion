package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/router"
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
	// FR-3.4 t-3: the deputy may cancel IMMEDIATELY, unlike approving (FR-5.4
	// M7's half-deadline). Reusing the approval window here would make a
	// runaway agent un-stoppable for twelve hours (E10-06).
	if perm := tasks.MayCancel(u.Id, director, deputy); !perm.Allowed {
		return nil, uuid.Nil, uuid.Nil, apperr.Forbidden("director_required", "only the session's Director or deputy can cancel a lane (FR-3.4)")
	}
	return u, wsID, sessionID, nil
}

// ListLanes is GET /sessions/{sessionId}/lanes — the S7 left-column board
// (FR-6.2). Workspace member or a TaskToken scoped to this session; the
// response is a bare array (openapi listLanes `type: array`).
//
// `actions` is per-caller: only the Director and the deputy may cancel
// (t-3), and a TaskToken never can, so the board an agent reads shows no
// control buttons.
func (s *Server) ListLanes(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.ListLanesParams) {
	u, p := s.sessionAccess(r, sessionId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	statuses := []string{}
	if params.Status != nil {
		for _, st := range *params.Status {
			if !st.Valid() {
				writeProblem(w, apperr.Validation(apperr.Field("status", "enum", "unknown lane status: "+string(st))))
				return
			}
			statuses = append(statuses, string(st))
		}
	}
	canControl := false
	if u != nil {
		var director uuid.UUID
		var deputy *uuid.UUID
		if err := s.DB.QueryRow(r.Context(), `SELECT director_user_id, deputy_director_user_id FROM session WHERE id = $1`, sessionId).
			Scan(&director, &deputy); err != nil {
			writeErr(w, err)
			return
		}
		// The same gate the cancel handler applies, so the board never shows a
		// button that 403s (FR-5.3 last bullet).
		canControl = tasks.MayCancel(u.Id, director, deputy).ButtonEnabled
	}
	out, err := lanes.List(r.Context(), s.DB, sessionId, statuses, canControl)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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

// DelegateLane is `colab lane delegate` (FR-6.2, FR-6.5). Agents only: a human
// parallelises with postMessage's new_lane toggle instead.
func (s *Server) DelegateLane(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.DelegateLaneParams) {
	pr := principalOf(r)
	if pr.Task == nil {
		writeProblem(w, apperr.Forbidden("agent_only", "lane delegate is an agent tool; humans use the composer's new-lane toggle"))
		return
	}
	if pr.Task.SessionID != sessionId {
		writeProblem(w, apperr.Forbidden("outside_task_scope", "task token cannot delegate in another session"))
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.DelegateLaneJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if strings.TrimSpace(in.Brief) == "" {
		writeProblem(w, apperr.Validation(apperr.Field("brief", "required", "brief is required — it becomes the child's turn prompt")))
		return
	}
	dep := []uuid.UUID{}
	if in.DependsOn != nil {
		for _, d := range *in.DependsOn {
			dep = append(dep, uuid.UUID(d))
		}
	}
	var profile *string
	if in.Profile.IsSpecified() && !in.Profile.IsNull() {
		v := in.Profile.MustGet()
		profile = &v
	}
	call := func() (int, any, *Problem) {
		res, err := s.Router.Delegate(r.Context(), pr.Task.TaskID, router.DelegateInput{
			AgentID: uuid.UUID(in.AgentId), Brief: in.Brief, DependsOn: dep, Profile: profile,
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, map[string]any{"lane": res.Lane, "message": res.Message, "task": res.Task}, nil
	}
	if params.IdempotencyKey == nil {
		st, out, p := call()
		if p != nil {
			writeProblem(w, p)
			return
		}
		writeJSON(w, st, out)
		return
	}
	s.idempotent(r.Context(), w, taskScope(pr.Task.TaskID), params.IdempotencyKey.String(), requestHash(r, body), call)
}
