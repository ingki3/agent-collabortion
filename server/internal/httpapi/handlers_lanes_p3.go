package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// ListLaneTasks is the S7 lane-card expansion (openapi listLaneTasks, O3):
// the lane's task history in time order, each with its attempts. A retry is a
// second `attempts[]` row of the SAME task; a re-instruction is a new task
// carrying `restarted_from_task_id` — the two are what O3 exists to tell apart.
func (s *Server) ListLaneTasks(w http.ResponseWriter, r *http.Request, laneId gen.LaneId) {
	var sessionID uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT session_id FROM lane WHERE id = $1`, laneId).Scan(&sessionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, apperr.NotFound("lane"))
		} else {
			writeErr(w, err)
		}
		return
	}
	if _, p := s.sessionAccess(r, sessionID); p != nil {
		writeProblem(w, p)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id FROM task WHERE lane_id = $1 ORDER BY created_at`, laneId)
	if err != nil {
		writeErr(w, err)
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeErr(w, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	out := make([]gen.Task, 0, len(ids))
	for _, id := range ids {
		t, err := tasks.Get(r.Context(), s.DB, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		attempts, err := tasks.ListAttempts(r.Context(), s.DB, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		usage, err := tasks.GetUsage(r.Context(), s.DB, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		out = append(out, tasks.ToAPI(t, attempts, usage))
	}
	writeJSON(w, http.StatusOK, out)
}

// RestartLane is FR-3.4 B, "중단하고 다시 지시".
//
// It is not a retry. The in-flight turn is cancelled through the §8.2.2
// procedure and the new instruction is posted as a message, which creates a NEW
// task (attempt 1, restarted_from_task_id) whose prompt carries the instruction
// and no `<resumed>` section — the human changed direction, so telling the
// agent to continue the interrupted work is exactly wrong (E8-06).
func (s *Server) RestartLane(w http.ResponseWriter, r *http.Request, laneId gen.LaneId, params gen.RestartLaneParams) {
	u, wsID, sessionID, p := s.laneControl(r, laneId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if strings.TrimSpace(in.Content) == "" {
		writeProblem(w, apperr.Validation(apperr.Field("content", "required", "새 지시를 적어 주세요")))
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), params.IdempotencyKey.String(), requestHash(r, body),
		func() (int, any, *Problem) {
			return s.restartLane(r.Context(), laneId, wsID, sessionID, u.Id, in.Content)
		})
}

func (s *Server) restartLane(ctx context.Context, laneID, wsID, sessionID, userID uuid.UUID, content string) (int, any, *Problem) {
	now := s.Clock.Now()
	var laneStatus string
	var agentID uuid.UUID
	var agentName string
	if err := s.DB.QueryRow(ctx, `
		SELECT l.status::text, l.agent_id, a.name FROM lane l JOIN agent a ON a.id = l.agent_id WHERE l.id = $1`, laneID).
		Scan(&laneStatus, &agentID, &agentName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, apperr.NotFound("lane")
		}
		return 0, nil, apperr.Internal(err)
	}
	switch laneStatus {
	case "running", "failed", "paused", "queued", "blocked", "waiting_human":
	default:
		return 0, nil, apperr.Conflict("lane_not_restartable", "이 lane은 다시 지시할 수 없습니다 (현재: "+laneStatus+")")
	}

	// 1. Cancel what is in flight, through the same procedure 중단 uses.
	var cancelledTaskID *uuid.UUID
	cur, _, err := s.Tasks.CancelLane(ctx, laneID, userID)
	switch {
	case errors.Is(err, tasks.ErrLaneNotCancellable):
		// Nothing was running — a failed or finished lane being re-instructed
		// (SCREEN §4.5's failure table names this case).
	case err != nil:
		return 0, nil, apperr.As(err)
	default:
		if cur != nil {
			id := cur.ID
			cancelledTaskID = &id
		}
	}

	// 2. The new instruction goes on the timeline as a message. That is what
	// creates the task, so the trigger of the new task is a real message the
	// Director can see — not an invisible server-side row (FR-3.4 B).
	body := content
	if !strings.Contains(body, "mention://agent/") {
		body = router.MentionLink(agentName, agentID) + " " + strings.TrimSpace(content)
	}
	res, err := s.Router.Post(ctx, sessionID, router.Author{UserID: &userID}, gen.MessageCreate{Content: body})
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	// 3. The new task is a re-instruction of the cancelled one, and the lane
	// keeps running (E2-15): PlanAttempt is what says both.
	var newTask *gen.Task
	for _, tr := range res.Triggers {
		if tr.TaskId == (uuid.UUID{}) {
			continue
		}
		t, err := tasks.Get(ctx, s.DB, tr.TaskId)
		if err != nil || t.LaneID != laneID {
			continue
		}
		if cancelledTaskID != nil {
			if _, err := s.DB.Exec(ctx, `UPDATE task SET restarted_from_task_id = $2, updated_at = $3 WHERE id = $1`,
				t.ID, *cancelledTaskID, now); err != nil {
				return 0, nil, apperr.Internal(err)
			}
			t.RestartedFromTaskID = cancelledTaskID
		}
		if _, err := s.DB.Exec(ctx, `UPDATE lane SET status = 'running', finished_at = NULL, updated_at = $2 WHERE id = $1`,
			laneID, now); err != nil {
			return 0, nil, apperr.Internal(err)
		}
		api := tasks.ToAPI(t, nil, nil)
		newTask = &api
		break
	}
	lane, err := lanes.Load(ctx, s.DB, laneID, true)
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	if s.Hub != nil {
		sid := sessionID
		_ = s.Hub.Publish(ctx, s.DB, wsID, &sid, "lane.updated", lane)
	}
	msg, _ := messages.Get(ctx, s.DB, res.Message.Id)
	out := map[string]any{"lane": lane, "cancelled_task_id": cancelledTaskID}
	if msg != nil {
		out["message"] = messages.ToAPI(msg)
	} else {
		out["message"] = res.Message
	}
	out["task"] = newTask
	return http.StatusAccepted, out, nil
}
