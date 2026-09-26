package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
)

// Mission and room control helpers (FR-2.1·2.3) shared by the work and room
// handlers. The old session-level operations that first used them are gone
// (openapi v0.3.0, D22).

// closeSessionBudgetHitl answers the platform's own pause request. It is
// `answered` rather than `cancelled` (K-4's state) because a person did decide:
// they pressed 재개.
//
// scope is the unit the pause stopped: a mission's own requests (work_id), or
// every request of the room when the room's gate is lifted.
func (s *Server) closeSessionBudgetHitl(ctx context.Context, tx pgx.Tx, scope taskScopeSQL, userID uuid.UUID, purpose string, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT id, question, session_id, work_id FROM hitl_request
		WHERE `+scope.col+` = $1 AND status = 'open' AND source = 'system' AND task_id IS NULL AND purpose = $2`,
		scope.id, purpose)
	if err != nil {
		return err
	}
	type row struct {
		id, room uuid.UUID
		q        string
		work     *uuid.UUID
	}
	var open []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.q, &rr.room, &rr.work); err != nil {
			rows.Close()
			return err
		}
		open = append(open, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range open {
		if _, err := tx.Exec(ctx, `
			UPDATE hitl_request SET status = 'answered', approved = true, answered_by = $2, answered_at = $3
			WHERE id = $1`, o.id, userID, now); err != nil {
			return err
		}
		if _, err := insertDecision(ctx, tx, o.room, "미션 재개 승인: "+o.q, "", "hitl", &o.id, false, now, o.work); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE inbox_item SET read_at = COALESCE(read_at, $2) WHERE ref_id = $1`, o.id, now); err != nil {
			return err
		}
		// T-APPROVAL: resuming from the mission panel answers the card too.
		s.publishHitlVia(ctx, tx, uuid.Nil, o.room, o.id, "hitl.updated")
	}
	return nil
}

func (s *Server) cancelWorkTx(ctx context.Context, tx pgx.Tx, roomID, workID uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE work SET status = 'cancelled', paused_reason = NULL, paused_detail = NULL,
		       finished_at = $2, updated_at = $2 WHERE id = $1`, workID, now); err != nil {
		return err
	}
	if err := s.cancelScopeTasks(ctx, tx, workScope(workID), now); err != nil {
		return err
	}
	var others int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM work WHERE room_id = $1 AND id <> $2 AND status IN ('draft', 'active', 'paused', 'completing')`,
		roomID, workID).Scan(&others); err != nil {
		return err
	}
	if err := s.closeOrphanSystemHitl(ctx, tx, workScope(workID), now); err != nil {
		return err
	}
	if others == 0 {
		if err := s.closeOrphanSystemHitl(ctx, tx, roomOnlyScope(roomID), now); err != nil {
			return err
		}
		return s.liftOfflineGate(ctx, tx, roomID, now)
	}
	return nil
}

// liftOfflineGate is FR-9.2 v0.19's second way out of a lost computer: the
// last open mission was cancelled, so the room has nothing left waiting for
// that machine. The work outside a mission goes with the missions (「열린
// 미션 전부 취소」 — the room stops spending on the lost computer), the gate
// comes down and the room_paused card is resolved. The room itself stays,
// still pinned to the machine: a new mission waits for it or for a rebind.
func (s *Server) liftOfflineGate(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, now time.Time) error {
	room, err := roomgate.Lock(ctx, tx, roomID)
	if err != nil {
		return err
	}
	if room.BlockedReason == nil || *room.BlockedReason != roomgate.ReasonRuntimeOffline {
		return nil
	}
	if err := s.cancelScopeTasks(ctx, tx, roomOnlyScope(roomID), now); err != nil {
		return err
	}
	// E14-07: giving up on the lost computer is `cancelled`, never
	// `completed` — the artifacts are recovered by having been on the server
	// all along (FR-9.2 「아티팩트만 회수한다」). The dead machine's folders are
	// unreachable: left `active`, the GC sweep would ask a runtime that never
	// answers, forever (a live report from it lifts the stamp again —
	// workdirs.Upsert). This was cancelSession's 「종료」 before openapi v0.3.0;
	// cancelling the room's last open mission is that choice now.
	var artifacts int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE session_id = $1`, roomID).Scan(&artifacts); err != nil {
		return err
	}
	end := runtimes.PlanOfflineEnd(artifacts)
	if end.SessionState != "cancelled" || end.CompletionConditionsMet {
		return apperr.Internal(fmt.Errorf("runtimes: offline end plan = %+v", end))
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workdir SET status = 'retained', gc_blocked_reason = 'runtime_gone', updated_at = $2
		WHERE session_id = $1 AND status = 'active'`, roomID, now); err != nil {
		return err
	}
	// FR-9.2, E14-07 — decision.summary/rationale are public (openapi
	// Decision) and S7 draws them, so they speak the screens' language.
	if _, err := insertDecision(ctx, tx, roomID, "컴퓨터가 돌아오지 않아 미션을 종료했습니다",
		fmt.Sprintf("다른 컴퓨터로 옮기는 대신 종료를 선택했습니다 — 아티팩트 %d개는 서버에 남아 있습니다", end.ArtifactsRecovered),
		"hitl", nil, false, now, nil); err != nil {
		return err
	}
	if _, err := roomgate.Unblock(ctx, tx, roomID, roomgate.ReasonRuntimeOffline, now); err != nil {
		return err
	}
	var runtimeID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT runtime_id FROM room WHERE id = $1`, roomID).Scan(&runtimeID); err != nil {
		return err
	}
	if runtimeID != nil {
		if err := roomgate.ResolveCards(ctx, tx, inbox.TypeRoomPaused, roomID, *runtimeID, now); err != nil {
			return err
		}
	}
	roomgate.PublishUpdated(ctx, s.Hub, tx, roomID)
	return nil
}

// cancelScopeTasks ends every task the scope still holds: queued ones at
// once, in-flight ones through the §8.2.2 procedure (a `cancel` command the
// daemon carries out — never an immediate kill).
func (s *Server) cancelScopeTasks(ctx context.Context, tx pgx.Tx, scope taskScopeSQL, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT id FROM task WHERE `+scope.col+` = $1`+scope.extra+`
		  AND status IN ('deferred', 'queued', 'dispatched', 'preparing', 'running', 'waiting_human', 'paused')
		ORDER BY created_at`, scope.id)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Tasks.CancelForSession(ctx, tx, id, "session_cancelled", now); err != nil {
			return err
		}
	}
	return nil
}

// closeOrphanSystemHitl is K-4 (Lead decision, 2026-09-06): a platform-issued
// request whose condition is no longer true is closed `cancelled` and taken out
// of the inbox. No decision is recorded — a decision that nobody made is the
// worst thing to leave in the log — and the web renders the card as 취소됨.
//
// K-4: the platform's own open requests lose their premise when the mission
// ends. They are closed `cancelled`, not answered — nobody decided them.
func (s *Server) closeOrphanSystemHitl(ctx context.Context, q pgx.Tx, scope taskScopeSQL, now time.Time) error {
	rows, err := q.Query(ctx, `
		UPDATE hitl_request SET status = 'cancelled'
		WHERE `+scope.col+` = $1 AND status = 'open' AND source = 'system'`+scope.extra+`
		RETURNING id`, scope.id)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := q.Exec(ctx, `DELETE FROM inbox_item WHERE ref_id = $1 AND type = $2::inbox_item_type`,
			id, inbox.TypeHitlRequest); err != nil {
			return err
		}
		// T-APPROVAL: the card turns into 「취소됨」 live, not on reload.
		s.publishHitlVia(ctx, q, uuid.Nil, uuid.Nil, id, "hitl.updated")
	}
	return nil
}

func (s *Server) requireMember(ctx context.Context, q pgx.Tx, sessionID, userID uuid.UUID) error {
	var ok bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM member m JOIN room s ON s.workspace_id = m.workspace_id
		               WHERE s.id = $1 AND m.user_id = $2)`, sessionID, userID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return apperr.Validation(apperr.Field("user_id", "not_member", "워크스페이스 멤버가 아닙니다"))
	}
	return nil
}

// addRoomMember keeps room_participant's human rows in step with the old
// session's people (0025 seeds Director·deputy as `member`): a person who
// becomes Director or deputy is a member of the room from then on.
func addRoomMember(ctx context.Context, tx pgx.Tx, roomID, userID uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO room_participant (room_id, user_id, role, joined_at) VALUES ($1, $2, 'member', $3)
		ON CONFLICT (room_id, user_id) WHERE user_id IS NOT NULL
		DO UPDATE SET left_at = NULL, joined_at = EXCLUDED.joined_at WHERE room_participant.left_at IS NOT NULL`, roomID, userID, now)
	return err
}

func (s *Server) inSessionTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// mergeLimits folds a limits patch (a struct or map that marshals to a JSON
// object) into the stored jsonb. A key the caller omitted keeps its value; an
// explicit null clears it (S-32).
func mergeLimits(stored []byte, patch any) ([]byte, error) {
	cur := map[string]any{}
	if len(stored) > 0 {
		_ = json.Unmarshal(stored, &cur)
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	next := map[string]any{}
	if err := json.Unmarshal(raw, &next); err != nil {
		return nil, apperr.Internal(err)
	}
	for k, v := range next {
		cur[k] = v
	}
	out, err := json.Marshal(cur)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

func budgetOf(limits []byte) float64 {
	var l struct {
		BudgetUSD *float64 `json:"budget_usd"`
	}
	if len(limits) == 0 {
		return 0
	}
	_ = json.Unmarshal(limits, &l)
	if l.BudgetUSD == nil {
		return 0
	}
	return *l.BudgetUSD
}

var _ = contracts.FailCancelled
var _ = hitl.StatusCancelled

// sessionAgents is "참여자 = the room's agents + assignee" (S-84) for a room that
// already exists: the set the reviewer guard checks against when a
// mission's completion condition changes.
func sessionAgents(ctx context.Context, q pgx.Tx, sessionID uuid.UUID, assignee *uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := q.Query(ctx, `SELECT agent_id FROM room_participant WHERE room_id = $1 AND agent_id IS NOT NULL AND left_at IS NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	if assignee != nil {
		out[*assignee] = true
	}
	return out, rows.Err()
}
