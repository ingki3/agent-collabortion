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
		SELECT l.session_id, s.workspace_id,
		       -- The lane's OWN mission decides (V19_R1B_HANDOFF (c)); a lane
		       -- outside any mission answers to the room's owner and deputy
		       -- (FR-2A.1), like router.status does.
		       COALESCE(wk.director_user_id, s.owner_user_id),
		       CASE WHEN wk.id IS NULL THEN s.deputy_owner_user_id ELSE wk.deputy_user_id END
		FROM lane l JOIN room s ON s.id = l.session_id LEFT JOIN work wk ON wk.id = l.work_id WHERE l.id = $1`, laneID).Scan(&sessionID, &wsID, &director, &deputy)
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
		return nil, uuid.Nil, uuid.Nil, apperr.Forbidden("director_required", "서브 미션은 이 미션의 Director 나 deputy 만 중단할 수 있습니다")
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
				writeProblem(w, apperr.Validation(apperr.Field("status", "enum", "알 수 없는 서브 미션 상태입니다: "+string(st))))
				return
			}
			statuses = append(statuses, string(st))
		}
	}
	// The same gate the cancel handler applies (laneControl), per lane, so the
	// board never shows a button that 403s (FR-5.3 last bullet): the lane's
	// mission's Director·deputy, or the room's owner·deputy for a lane
	// outside any mission (review #291 NN4 — a room with no mission is not
	// a server error).
	var canControl func(work *uuid.UUID) bool
	if u != nil {
		type seat struct {
			director uuid.UUID
			deputy   *uuid.UUID
		}
		seats := map[uuid.UUID]seat{}
		rows, err := s.DB.Query(r.Context(), `SELECT id, director_user_id, deputy_user_id FROM work WHERE room_id = $1`, sessionId)
		if err != nil {
			writeErr(w, err)
			return
		}
		for rows.Next() {
			var id uuid.UUID
			var st seat
			if err := rows.Scan(&id, &st.director, &st.deputy); err != nil {
				rows.Close()
				writeErr(w, err)
				return
			}
			seats[id] = st
		}
		rows.Close()
		var room seat
		if err := s.DB.QueryRow(r.Context(), `SELECT owner_user_id, deputy_owner_user_id FROM room WHERE id = $1`, sessionId).
			Scan(&room.director, &room.deputy); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, err)
			return
		}
		canControl = func(work *uuid.UUID) bool {
			st := room
			if work != nil {
				st = seats[*work]
			}
			return tasks.MayCancel(u.Id, st.director, st.deputy).ButtonEnabled
		}
	}
	out, err := lanes.List(r.Context(), s.DB, sessionId, statuses, canControl)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := lanes.FillMySubscription(r.Context(), s.DB, out, u.Id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// CancelLane is POST /lanes/{laneId}/cancel (FR-3.4 "중단", E10-04).
// Director/deputy only; the lane's CURRENT task must be cancellable (K-16,
// openapi 0.1.6 — a `done` lane whose turn is still running counts; nothing
// left to stop is 409); a running attempt gets the daemon `cancel` command
// and ends when its finish arrives, a queued task is cancelled at once. 202
// with the lane; completion is `lane.updated` (openapi cancelLane).
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
		// K-16 (openapi 0.1.6, PR #260 리뷰 NN3): the judgement is the current
		// task, so a `done` lane whose turn still runs IS cancellable and the
		// old sentence ("진행 중이거나 대기 중인 작업 줄기만…") named the wrong
		// condition. The sentence is mirrored letter-for-letter by
		// web/lib/mock/wording.ts (server-wording.test.ts); the web copy is
		// synced by the Lead from this PR's body.
		writeProblem(w, apperr.Conflict("lane_not_cancellable", "중단할 수 있는 진행 중 턴이 없습니다"))
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
		writeProblem(w, apperr.Forbidden("agent_only", "위임은 에이전트만 할 수 있습니다 — 사람은 글쓰기 칸의 「새 서브 미션으로 보내기」를 쓰세요"))
		return
	}
	if pr.Task.SessionID != sessionId {
		writeProblem(w, apperr.Forbidden("outside_task_scope", "다른 방에는 위임할 수 없습니다"))
		return
	}
	if p := s.commandAllowed(r, gen.LaneDelegate); p != nil {
		writeProblem(w, p)
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
		writeProblem(w, apperr.Validation(apperr.Field("brief", "required", "지시문을 적어 주세요 — 위임받는 에이전트가 그 글로 시작합니다")))
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
