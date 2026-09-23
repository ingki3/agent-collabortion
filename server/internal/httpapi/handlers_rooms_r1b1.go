package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// The room gate's HTTP side (PRD v0.19 FR-2.4 · FR-2A.3 · §12.1-9, T-R1b1).
//
// `blockRoom`/`unblockRoom` are the person's switch (`manual`); the other
// reasons are lifted by the approval their HITL asks for — the budget raise
// (K-10, resumeRoomForBudget), the loop continue (unblockRoomForLoop) — or
// by the old `/sessions/*` resume, which lifts the room block its mirror
// stands for (ResumeSession).

// ---------------------------------------------------------------------------
// blockRoom · unblockRoom — 「이 방 멈춤」(manual)
// ---------------------------------------------------------------------------

// roomManager is the gate for the manual switch: the room owner and deputy,
// and the workspace's owners and admins (FR-2.4). A non-member gets the 404
// every room-scoped op gives, so the id's existence does not leak.
func (s *Server) roomManager(r *http.Request, roomID uuid.UUID) (*gen.User, *roomgate.State, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, nil, p
	}
	st, err := roomgate.Load(r.Context(), s.DB, roomID)
	if errors.Is(err, roomgate.ErrNotFound) {
		return nil, nil, apperr.NotFound("room")
	}
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	m, err := s.Auth.Member(r.Context(), st.WorkspaceID, u.Id)
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	if m == nil {
		return nil, nil, apperr.NotFound("room")
	}
	if !mayManageRoom(st, u.Id, m.Role) {
		return nil, nil, apperr.Forbidden("not_room_manager",
			"방장·부방장이나 워크스페이스 소유자·관리자만 이 방을 멈추거나 풀 수 있습니다")
	}
	return u, st, nil
}

// mayManageRoom is FR-2.4's list of who may put the manual stop up and take
// it down: the same people, so a stop can always be undone by whoever could
// have made it.
func mayManageRoom(st *roomgate.State, userID uuid.UUID, wsRole string) bool {
	if userID == st.Owner || (st.Deputy != nil && userID == *st.Deputy) {
		return true
	}
	return wsRole == "owner" || wsRole == "admin"
}

func (s *Server) BlockRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	u, _, p := s.roomManager(r, roomId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		st, err := roomgate.Lock(r.Context(), tx, roomId)
		if err != nil {
			return err
		}
		if st.BlockedReason != nil {
			return apperr.Conflict("already_blocked", "이미 멈춰 있는 방입니다 — "+blockedReasonText(*st.BlockedReason))
		}
		by, err := auth.LoadUser(r.Context(), tx, u.Id)
		if err != nil {
			return err
		}
		if _, err := roomgate.Block(r.Context(), tx, roomId, roomgate.ReasonManual, gen.BlockedDetail{
			BlockedByUser: nullable.NewNullableWithValue(*by),
		}, nil, now); err != nil {
			return err
		}
		// FR-3.4 「중단」 for every turn in flight: the §8.2.2 cancel command,
		// never a signal from here. Waiting tasks stay queued behind the gate.
		if err := s.Tasks.CancelInFlightInRoom(r.Context(), tx, roomId, u.Id, now); err != nil {
			return err
		}
		if _, err := s.Router.SystemPost(r.Context(), tx, roomId,
			by.DisplayName+"님이 이 방을 멈췄습니다 — 진행 중인 턴을 끝내고 새 실행을 막습니다. 멈춤을 풀면 기다리던 일이 이어집니다."); err != nil {
			return err
		}
		return roomActivity(r.Context(), tx, st.WorkspaceID, roomId, u.Id, "room.blocked", roomgate.ReasonManual, now)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	roomgate.PublishUpdated(r.Context(), s.Hub, s.DB, roomId)
	s.roomOut(r.Context(), w, roomId, u.Id)
}

func (s *Server) UnblockRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	u, _, p := s.roomManager(r, roomId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		st, err := roomgate.Lock(r.Context(), tx, roomId)
		if err != nil {
			return err
		}
		switch {
		case st.BlockedReason == nil:
			return apperr.Conflict("not_blocked", "멈춰 있지 않은 방입니다")
		case *st.BlockedReason != roomgate.ReasonManual:
			// §12.1-9: budget·loop·runtime_offline are lifted by the approval
			// (or re-connecting the computer), not by this switch — lifting
			// them here would spend past a limit nobody raised.
			return apperr.Conflict("not_manual", blockedReasonText(*st.BlockedReason)+
				" — 이 멈춤은 확인 요청에 답하거나 컴퓨터를 다시 연결해야 풀립니다")
		}
		if _, err := roomgate.Unblock(r.Context(), tx, roomId, roomgate.ReasonManual, now); err != nil {
			return err
		}
		by, err := auth.LoadUser(r.Context(), tx, u.Id)
		if err != nil {
			return err
		}
		if _, err := s.Router.SystemPost(r.Context(), tx, roomId, by.DisplayName+"님이 방 멈춤을 풀었습니다."); err != nil {
			return err
		}
		return roomActivity(r.Context(), tx, st.WorkspaceID, roomId, u.Id, "room.unblocked", roomgate.ReasonManual, now)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.Queue.Notifier.Notify()
	roomgate.PublishUpdated(r.Context(), s.Hub, s.DB, roomId)
	s.roomOut(r.Context(), w, roomId, u.Id)
}

// blockedReasonText names a stop for the 409s (SCREEN §4.6's banner words).
func blockedReasonText(reason string) string {
	switch reason {
	case roomgate.ReasonBudget:
		return "예산 상한에 걸려 멈춘 방입니다"
	case roomgate.ReasonLoop:
		return "에이전트끼리 주고받기가 상한에 걸려 멈춘 방입니다"
	case roomgate.ReasonRuntimeOffline:
		return "컴퓨터 연결이 끊겨 멈춘 방입니다"
	case roomgate.ReasonManual:
		return "사람이 멈춘 방입니다"
	}
	return "멈춘 방입니다"
}

// roomActivity is FR-2.2's audit line for the manual switch.
func roomActivity(ctx context.Context, tx pgx.Tx, wsID, roomID, userID uuid.UUID, action, reason string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO activity_log (workspace_id, session_id, actor_type, actor_id, action, object_type, object_id, payload, created_at)
		VALUES ($1, $2, 'user', $3, $4, 'room', $2, jsonb_build_object('reason', $5::text), $6)`,
		wsID, roomID, userID, action, reason, now)
	return err
}

// ---------------------------------------------------------------------------
// Lifting the gate by approval (K-10 · FR-3.5 · the old resume)
// ---------------------------------------------------------------------------

// roomScoped: a budget request that is the ROOM's (its owner answers, no
// mission) rather than one mission's.
func roomScoped(row *hitlRow) bool {
	return row.ApproverSpec == hitl.SpecRoomOwner || row.WorkID == nil
}

// budgetStopOf answers, for a room- or mission-scoped budget request, whether
// the stop it would lift is still up and what has been spent against the
// ceiling a raise must clear (S-49).
func (s *Server) budgetStopOf(ctx context.Context, row *hitlRow) (bool, float64, error) {
	if roomScoped(row) {
		st, err := roomgate.Load(ctx, s.DB, row.SessionID)
		if err != nil {
			return false, 0, err
		}
		spent, err := sessions.SpentUSD(ctx, s.DB, row.SessionID)
		if err != nil {
			return false, 0, err
		}
		return st.BlockedReason != nil && *st.BlockedReason == roomgate.ReasonBudget, spent, nil
	}
	var status string
	var reason *string
	var detail []byte
	if err := s.DB.QueryRow(ctx, `SELECT status::text, paused_reason::text, paused_detail FROM work WHERE id = $1`, *row.WorkID).
		Scan(&status, &reason, &detail); err != nil {
		return false, 0, err
	}
	spent, err := sessions.WorkSpentUSD(ctx, s.DB, *row.WorkID)
	if err != nil {
		return false, 0, err
	}
	return status == "paused" && derefString(reason) == sessions.PauseBudget && !roomgate.IsMirror(detail), spent, nil
}

// resumeRoomForBudget is the room half of the K-10 approval: the raise is the
// room's new ceiling ("세션 잔여 상한 = 승인 금액" — an override REPLACES the
// limit, FR-7.3 C2′), the gate comes down, the missions the block parked come
// back, and the turns the pause parked are re-queued (S-46) on unparked lanes
// (S-44).
//
// It is a separate transaction from the answer for the reason answerAgentHitl
// gives about the re-queue: this one locks the room and then its tasks, while
// the budget answer path locks task-then-hitl, and holding both orders at once
// is how the two deadlock.
func (s *Server) resumeRoomForBudget(ctx context.Context, roomID uuid.UUID, raise float64, now time.Time) error {
	return s.inSessionTx(ctx, func(tx pgx.Tx) error {
		st, err := roomgate.Lock(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if st.BlockedReason == nil || *st.BlockedReason != roomgate.ReasonBudget {
			// Lifted already (a concurrent resume), or stopped for another
			// reason since — this answer is not the one that lifts it, and
			// forcing it open would undo that other stop.
			return nil
		}
		var limitsRaw []byte
		if err := tx.QueryRow(ctx, `SELECT limits FROM room WHERE id = $1`, roomID).Scan(&limitsRaw); err != nil {
			return err
		}
		budget := float32(raise)
		merged, err := mergeLimits(limitsRaw, &gen.SessionLimits{BudgetUsd: nullable.NewNullableWithValue(budget)})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE room SET limits = $2, updated_at = $3 WHERE id = $1`, roomID, merged, now); err != nil {
			return err
		}
		if _, err := roomgate.Unblock(ctx, tx, roomID, roomgate.ReasonBudget, now); err != nil {
			return err
		}
		if _, err := s.Tasks.ResumeSessionTasks(ctx, tx, roomID, sessions.PauseBudget, tasks.CauseBudgetApproved, now); err != nil {
			return err
		}
		if err := requeuePausedLanes(ctx, tx, "session_id", roomID, now); err != nil {
			return err
		}
		roomgate.PublishUpdated(ctx, s.Hub, tx, roomID)
		return nil
	})
}

// resumeWorkForBudget is the mission half: one mission's own budget pause
// (FR-2A.3 "일의 상한") comes off with the raise as that mission's ceiling.
// The room's other missions are not touched — they were never stopped.
func (s *Server) resumeWorkForBudget(ctx context.Context, roomID, workID uuid.UUID, raise float64, now time.Time) error {
	return s.inSessionTx(ctx, func(tx pgx.Tx) error {
		// Room first, then the mission (R1a lock order).
		if _, err := roomgate.Lock(ctx, tx, roomID); err != nil {
			return err
		}
		var status string
		var reason *string
		var detail, limitsRaw []byte
		if err := tx.QueryRow(ctx, `
			SELECT status::text, paused_reason::text, paused_detail, limits FROM work WHERE id = $1 FOR UPDATE`, workID).
			Scan(&status, &reason, &detail, &limitsRaw); err != nil {
			return err
		}
		if status != "paused" || derefString(reason) != sessions.PauseBudget || roomgate.IsMirror(detail) {
			return nil // not this mission's own budget pause (any more)
		}
		cur := map[string]any{}
		_ = json.Unmarshal(limitsRaw, &cur)
		cur["budget_usd"] = raise
		merged, err := json.Marshal(cur)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE work SET limits = $2, status = 'active', paused_reason = NULL, paused_detail = NULL, updated_at = $3
			WHERE id = $1`, workID, merged, now); err != nil {
			return err
		}
		if _, err := s.Tasks.ResumeWorkTasks(ctx, tx, workID, sessions.PauseBudget, tasks.CauseBudgetApproved, now); err != nil {
			return err
		}
		return requeuePausedLanes(ctx, tx, "work_id", workID, now)
	})
}

// unblockRoomForLoop is FR-3.5's resume at the room: the owner's approval
// takes the loop gate down and brings back the missions it parked. The
// chain-depth and pair counters restart below a person (a human hop, S-80 —
// "사람이 개입하면 0"), while hops_per_hour keeps counting so repeated
// approvals cannot empty all three limits at once.
func (s *Server) unblockRoomForLoop(ctx context.Context, roomID uuid.UUID, now time.Time) error {
	return s.inSessionTx(ctx, func(tx pgx.Tx) error {
		st, err := roomgate.Lock(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if st.BlockedReason == nil || *st.BlockedReason != roomgate.ReasonLoop {
			return nil
		}
		if _, err := roomgate.Unblock(ctx, tx, roomID, roomgate.ReasonLoop, now); err != nil {
			return err
		}
		agent, err := loopResetAgent(ctx, tx, roomID, st.BlockedDetail)
		if err != nil {
			return err
		}
		if agent != uuid.Nil {
			if err := s.Router.RecordHumanHop(ctx, tx, roomID, agent, uuid.Nil, now); err != nil {
				return err
			}
		}
		roomgate.PublishUpdated(ctx, s.Hub, tx, roomID)
		return nil
	})
}

// loopResetAgent is who the reset hop points at: the first agent of the loop
// that tripped (the gate's own detail), else the room's only mission's
// assignee. A human hop resets the counters whoever it names — the target
// only has to be an agent of the room.
func loopResetAgent(ctx context.Context, q db.DBTX, roomID uuid.UUID, blockedDetail []byte) (uuid.UUID, error) {
	var d gen.BlockedDetail
	if len(blockedDetail) > 0 && json.Unmarshal(blockedDetail, &d) == nil && d.LoopAgents != nil && len(*d.LoopAgents) > 0 {
		return uuid.UUID((*d.LoopAgents)[0]), nil
	}
	var agent *uuid.UUID
	err := q.QueryRow(ctx, `SELECT assignee_agent_id FROM work WHERE room_id = $1 ORDER BY created_at LIMIT 1`, roomID).Scan(&agent)
	if errors.Is(err, pgx.ErrNoRows) || agent == nil {
		return uuid.Nil, nil
	}
	return *agent, err
}

// requeuePausedLanes is S-44's lane half of a resume: the claim refuses a
// paused lane, so a lane left `paused` never dispatches again. Only lanes that
// hold a queued task come back — one whose only task stayed parked has
// nothing to hand out.
func requeuePausedLanes(ctx context.Context, tx pgx.Tx, col string, id uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE lane l SET status = 'queued', finished_at = NULL, updated_at = $2
		WHERE l.`+col+` = $1 AND l.status = 'paused'
		  AND EXISTS (SELECT 1 FROM task t WHERE t.lane_id = l.id AND t.status = 'queued')`, id, now)
	return err
}

// ---------------------------------------------------------------------------
// The Room the switch answers with (openapi Room)
// ---------------------------------------------------------------------------

func (s *Server) roomOut(ctx context.Context, w http.ResponseWriter, roomID, viewer uuid.UUID) {
	out, err := roomAPI(ctx, s.DB, roomID, viewer, s.Clock.Now())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// roomAPI renders a room (openapi Room) for `viewer`. The gate's banner
// fields that move with time — who may answer now and when the next one may
// (`approver` · `delegate_at`, FR-2A.3) — are computed here from the open
// request, not stored.
func roomAPI(ctx context.Context, q db.DBTX, roomID, viewer uuid.UUID, now time.Time) (*gen.Room, error) {
	var out gen.Room
	var (
		deputy, runtimeID, defaultDirector *uuid.UUID
		isolation, limits, blockedDetail   []byte
		status, visibility, autonomy       string
		blocked                            *string
	)
	err := q.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, status::text, visibility::text, owner_user_id, deputy_owner_user_id,
		       runtime_id, isolation, limits, autonomy::text, default_director_user_id, blocked_reason::text, blocked_detail,
		       created_by, created_at, updated_at
		FROM room WHERE id = $1`, roomID).Scan(
		&out.Id, &out.WorkspaceId, &out.Name, &out.Description, &status, &visibility, &out.OwnerUserId, &deputy,
		&runtimeID, &isolation, &limits, &autonomy, &defaultDirector, &blocked, &blockedDetail,
		&out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("room")
	}
	if err != nil {
		return nil, err
	}
	out.Status = gen.RoomStatus(status)
	out.Visibility = gen.RoomVisibility(visibility)
	out.Autonomy = gen.AutonomyLevel(autonomy)
	out.DeputyOwnerUserId = tasks.NullUUID(deputy)
	out.RuntimeId = tasks.NullUUID(runtimeID)
	out.DefaultDirectorUserId = tasks.NullUUID(defaultDirector)
	_ = json.Unmarshal(isolation, &out.Isolation)
	_ = json.Unmarshal(limits, &out.Limits)
	out.BlockedReason = nullable.NewNullNullable[gen.RoomBlockedReason]()
	out.BlockedDetail = nullable.NewNullNullable[gen.BlockedDetail]()
	if blocked != nil {
		out.BlockedReason = nullable.NewNullableWithValue(gen.RoomBlockedReason(*blocked))
		var d gen.BlockedDetail
		_ = json.Unmarshal(blockedDetail, &d)
		if err := fillApprover(ctx, q, roomID, &d, now); err != nil {
			return nil, err
		}
		out.BlockedDetail = nullable.NewNullableWithValue(d)
	}
	out.MyRoomRole = nullable.NewNullNullable[gen.RoomRole]()
	var role *string
	var lastRead *uuid.UUID
	err = q.QueryRow(ctx, `SELECT role::text, last_read_message_id FROM room_participant WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`, roomID, viewer).
		Scan(&role, &lastRead)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if role != nil {
		out.MyRoomRole = nullable.NewNullableWithValue(gen.RoomRole(*role))
	}
	// §12.1-6: unread is per room, counted from the viewer's read mark.
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM message m WHERE m.session_id = $1
		  AND ($2::uuid IS NULL OR m.created_at > (SELECT created_at FROM message WHERE id = $2))`, roomID, lastRead).
		Scan(&out.UnreadCount); err != nil {
		return nil, err
	}
	return &out, nil
}

// fillApprover is the banner's "지금 답할 수 있는 사람" for a stop an approval
// lifts: the owner, and from half the open request's deadline the deputy or
// the oldest workspace owner (FR-2A.3). A manual stop has no request — the
// people who may lift it are the managers, and the banner names who stopped
// it instead.
func fillApprover(ctx context.Context, q db.DBTX, roomID uuid.UUID, d *gen.BlockedDetail, now time.Time) error {
	var created, due time.Time
	err := q.QueryRow(ctx, `
		SELECT created_at, due_at FROM hitl_request
		WHERE session_id = $1 AND status = 'open' AND source = 'system' AND task_id IS NULL
		  AND approver_spec = 'room_owner' AND purpose IN ('budget', 'loop')
		ORDER BY created_at DESC LIMIT 1`, roomID).Scan(&created, &due)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	a, err := roomgate.LoadApprovers(ctx, q, roomID)
	if err != nil {
		return err
	}
	who, next := a.Now(created, due, now)
	if u, err := auth.LoadUser(ctx, q, who); err == nil {
		d.Approver = nullable.NewNullableWithValue(*u)
	}
	if next != nil {
		d.DelegateAt = nullable.NewNullableWithValue(next.UTC())
	} else {
		d.DelegateAt = nullable.NewNullNullable[time.Time]()
	}
	return nil
}
