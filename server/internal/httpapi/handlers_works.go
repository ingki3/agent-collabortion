package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Missions (openapi 0.2.x `works` tag — PRD v0.19 FR-2A · FR-3.1.1 · FR-5.3 ·
// FR-8, T-R1b2): a room holds several missions, each with its own goal,
// Director, completion condition, limits and lifecycle. The work-keyed
// helpers they share with the room gate are in handlers_sessions_p3.go.

// ---------------------------------------------------------------------------
// Scopes — which rows a pause, a resume or a cancel reaches
// ---------------------------------------------------------------------------

// taskScopeSQL names the rows one unit owns: a mission's (`work_id`) or a
// whole room's (`session_id`, the room gate). `extra` narrows it further.
type taskScopeSQL struct {
	col   string
	id    uuid.UUID
	extra string
}

func workScope(id uuid.UUID) taskScopeSQL { return taskScopeSQL{col: "work_id", id: id} }
func roomScope(id uuid.UUID) taskScopeSQL { return taskScopeSQL{col: "session_id", id: id} }

// roomOnlyScope is the room's own rows — those of no mission.
func roomOnlyScope(id uuid.UUID) taskScopeSQL {
	return taskScopeSQL{col: "session_id", id: id, extra: " AND work_id IS NULL"}
}

// lockWork locks a mission's room and then the mission.
func lockWork(ctx context.Context, tx pgx.Tx, workID uuid.UUID) (status string, reason *string, detail []byte, err error) {
	err = tx.QueryRow(ctx, `
		SELECT wk.status::text, wk.paused_reason::text, wk.paused_detail
		FROM work wk JOIN room s ON s.id = wk.room_id WHERE wk.id = $1 FOR UPDATE OF s, wk`, workID).
		Scan(&status, &reason, &detail)
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperr.NotFound("work")
	}
	return
}

// pauseWorkTx is FR-2.3's Director pause on one mission. It DRAINS: the turn
// in flight finishes (E5-06) and the claim's mission gate stops anything new.
// notActive is the caller's 409 — the old session surface and the works API
// each keep their own sentence.
func (s *Server) pauseWorkTx(ctx context.Context, tx pgx.Tx, workID uuid.UUID, notActive func(status string) error, now time.Time) error {
	status, _, _, err := lockWork(ctx, tx, workID)
	if err != nil {
		return err
	}
	if status != "active" {
		return notActive(status)
	}
	raw, _ := json.Marshal(tasks.PausedDetail(sessions.PauseDirector, now))
	if _, err := tx.Exec(ctx, `
		UPDATE work SET status = 'paused', paused_reason = 'director', paused_detail = $2, updated_at = $3
		WHERE id = $1`, workID, raw, now); err != nil {
		return err
	}
	return s.Tasks.PauseWorkTasks(ctx, tx, workID, sessions.PauseDirector, raw, now)
}

// resumeScopeTasks re-queues what a pause of that scope parked (S-46).
func (s *Server) resumeScopeTasks(ctx context.Context, tx pgx.Tx, scope taskScopeSQL, reason, cause string, now time.Time) error {
	var err error
	if scope.col == "work_id" {
		_, err = s.Tasks.ResumeWorkTasks(ctx, tx, scope.id, reason, cause, now)
	} else {
		_, err = s.Tasks.ResumeSessionTasks(ctx, tx, scope.id, reason, cause, now)
	}
	return err
}

// liftParkedLanes is S-44 for one scope (requeuePausedLanes).
func liftParkedLanes(ctx context.Context, tx pgx.Tx, scope taskScopeSQL, now time.Time) error {
	return requeuePausedLanes(ctx, tx, scope.col, scope.id, now)
}

// directorHandedOver moves a mission's Director seat to `to` (changeDirector ·
// changeWorkDirector · removeMember's succession): the row, the room seat
// (a Director is a room participant), the open `director` requests of that
// mission — they follow the role, not the person (openapi changeDirector) —
// and one line on the mission's timeline.
func (s *Server) directorHandedOver(ctx context.Context, tx pgx.Tx, wsID, roomID, workID, to uuid.UUID, line string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE work SET director_user_id = $2, updated_at = $3 WHERE id = $1`, workID, to, now); err != nil {
		return err
	}
	if err := addRoomMember(ctx, tx, roomID, to, now); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT h.id FROM hitl_request h LEFT JOIN task t ON t.id = h.task_id
		WHERE h.session_id = $1 AND h.status = 'open' AND h.approver_spec = 'director'
		  AND COALESCE(h.work_id, t.work_id) = $2`, roomID, workID)
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
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, work_id, recipient_basis)
			SELECT mm.id, $4::inbox_item_type, $5::inbox_severity, $1, $2, $3, $8, 'director'
			FROM member mm WHERE mm.workspace_id = $6 AND mm.user_id = $7
			  AND NOT EXISTS (SELECT 1 FROM inbox_item i WHERE i.member_id = mm.id AND i.ref_id = $2)`,
			roomID, id, now, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest), wsID, to, workID); err != nil {
			return err
		}
	}
	if err := s.systemPostWork(ctx, tx, roomID, workID, line); err != nil {
		return err
	}
	s.publishWork(ctx, tx, wsID, workID, "work.updated")
	return nil
}

// systemPostWork is a system line on a mission's timeline (FR-3.1.1: a
// message belongs to the mission it speaks about).
func (s *Server) systemPostWork(ctx context.Context, tx pgx.Tx, roomID, workID uuid.UUID, line string) error {
	_, err := s.Router.SystemPostWork(ctx, tx, roomID, &workID, line)
	return err
}

// publishWork emits `work.created` · `work.updated` (openapi StreamEvent:
// WorkListItem) for the S7 chip row.
func (s *Server) publishWork(ctx context.Context, q db.DBTX, wsID, workID uuid.UUID, typ string) {
	if s.Hub == nil {
		return
	}
	w, err := sessions.LoadWorkRow(ctx, q, workID)
	if err != nil {
		return
	}
	it, err := w.ListItem(ctx, q)
	if err != nil {
		return
	}
	rid := w.RoomId
	_ = s.Hub.Publish(ctx, q, wsID, &rid, typ, it)
}

// publishWorkClosed emits `work.closed` {work_id, room_id, status,
// summary_message_id?}.
func (s *Server) publishWorkClosed(ctx context.Context, q db.DBTX, wsID, roomID, workID uuid.UUID, status string) {
	if s.Hub == nil {
		return
	}
	var summary *uuid.UUID
	_ = q.QueryRow(ctx, `SELECT summary_message_id FROM work WHERE id = $1`, workID).Scan(&summary)
	rid := roomID
	_ = s.Hub.Publish(ctx, q, wsID, &rid, "work.closed", map[string]any{
		"work_id": workID, "room_id": roomID, "status": status, "summary_message_id": summary,
	})
}

// publishWorkDeleted emits `work.deleted` {work_id, room_id} (openapi 0.2.4):
// the chip row drops the mission; its messages stay in the room.
func (s *Server) publishWorkDeleted(ctx context.Context, q db.DBTX, wsID, roomID, workID uuid.UUID) {
	if s.Hub == nil {
		return
	}
	rid := roomID
	_ = s.Hub.Publish(ctx, q, wsID, &rid, string(gen.StreamEventTypeWorkDeleted), map[string]any{"work_id": workID, "room_id": roomID})
}

// ---------------------------------------------------------------------------
// Gates
// ---------------------------------------------------------------------------

// workGate is user() + the mission + the caller's standing in its room
// (rooms.Decide — SCREEN §2.3). A mission in a room the caller cannot see is
// 404 like a mission that does not exist.
func (s *Server) workGate(r *http.Request, workID uuid.UUID, act rooms.Action) (*gen.User, *rooms.Access, *sessions.WorkRow, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, nil, nil, p
	}
	wk, err := sessions.LoadWorkRow(r.Context(), s.DB, workID)
	if err != nil {
		return nil, nil, nil, apperr.As(err)
	}
	a, err := rooms.LoadAccess(r.Context(), s.DB, wk.RoomId, u.Id)
	if err != nil {
		if pr := apperr.As(err); pr.Status == http.StatusNotFound {
			return nil, nil, nil, apperr.NotFound("work")
		}
		return nil, nil, nil, apperr.As(err)
	}
	if !rooms.Decide(rooms.ActView, a.Standing) {
		return nil, nil, nil, apperr.NotFound("work")
	}
	if !rooms.Decide(act, a.Standing) {
		return nil, nil, nil, rooms.Deny(act, a.Standing)
	}
	if act == rooms.ActView && r.Method == http.MethodGet {
		s.auditViewSoft(r.Context(), a, u.Id)
	}
	return u, a, wk, nil
}

// requireWorkDirector is "그 미션의 director" (openapi works ops), with the
// deputy where the op allows it.
func requireWorkDirector(u *gen.User, wk *sessions.WorkRow, allowDeputy bool) *Problem {
	if u.Id == wk.DirectorUserId {
		return nil
	}
	if allowDeputy && wk.DeputyUserId.IsSpecified() && !wk.DeputyUserId.IsNull() && wk.DeputyUserId.MustGet() == u.Id {
		return nil
	}
	return apperr.Forbidden("director_required", "이 미션의 Director 만 할 수 있습니다")
}

func (s *Server) workOut(ctx context.Context, w http.ResponseWriter, status int, workID, viewer uuid.UUID) {
	out, err := sessions.LoadWork(ctx, s.DB, workID, viewer)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, status, out)
}

// ---------------------------------------------------------------------------
// listWorks · getWork
// ---------------------------------------------------------------------------

func (s *Server) ListWorks(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.ListWorksParams) {
	if _, _, p := s.roomGate(r, roomId, rooms.ActView); p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	var status []string
	if params.Status != nil {
		for _, st := range *params.Status {
			status = append(status, string(st))
		}
	}
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	items, next, err := sessions.ListWorks(r.Context(), s.DB, roomId, status, params.Cursor, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) GetWork(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	if principalOf(r).Task != nil {
		// openapi getWork: "TaskToken(그 task 의 방의 미션만)" — the turn's
		// mission for `colab room get`.
		wk, err := sessions.LoadWorkRow(r.Context(), s.DB, workId)
		if err != nil {
			writeErr(w, err)
			return
		}
		if p := s.taskRoom(r, wk.RoomId); p != nil {
			writeProblem(w, p)
			return
		}
		s.workOut(r.Context(), w, http.StatusOK, workId, uuid.Nil)
		return
	}
	u, _, _, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	s.workOut(r.Context(), w, http.StatusOK, workId, u.Id)
}

// ---------------------------------------------------------------------------
// createWork (FR-2A.1 · FR-3.1.1 사후 귀속)
// ---------------------------------------------------------------------------

// CreateWork opens a mission — 「새 미션」, 「이걸 미션으로」(from_message_id) and
// 「제안에서」(from_proposal_id) all make the same record.
func (s *Server) CreateWork(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.CreateWorkParams) {
	u, a, p := s.roomGate(r, roomId, rooms.ActOpenWork)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.WorkCreate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		var workID uuid.UUID
		err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
			id, err := s.openWork(r.Context(), tx, a, u, in, s.Clock.Now())
			workID = id
			return err
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		s.Queue.Notifier.Notify()
		out, err := sessions.LoadWork(r.Context(), s.DB, workID, u.Id)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, out, nil
	})
}

// WorkRunningStatuses are the missions that count against the room's
// `max_concurrent_works` (FR-2A.5 "동시 실행 상한") — a draft is not running,
// a finished one is not either.
var workRunningStatuses = []string{"active", "paused", "completing"}

// DefaultWorkCondition is FR-2A.1's default completion condition: with an
// assignee, `artifact_submitted(assignee) AND user_approval`; without one,
// `user_approval` alone — an artifact nobody is assigned to submit would never
// arrive and the mission would never close (V19-A P0-3, S-84's lesson).
func DefaultWorkCondition(hasAssignee bool) map[string]any {
	if hasAssignee {
		return map[string]any{"op": "and", "conditions": []any{
			map[string]any{"type": "artifact_submitted", "who": "assignee"},
			map[string]any{"type": "user_approval"},
		}}
	}
	return map[string]any{"op": "and", "conditions": []any{map[string]any{"type": "user_approval"}}}
}

// validateWorkLimits is WorkLimits' schema plus a parseable time limit.
func validateWorkLimits(l *gen.WorkLimits) []apperr.FieldError {
	if l == nil {
		return nil
	}
	var errs []apperr.FieldError
	if v, err := l.BudgetUsd.Get(); err == nil && v < 0 {
		errs = append(errs, apperr.Field("limits/budget_usd", "minimum", "예산은 0 이상이어야 합니다"))
	}
	if v, err := l.BudgetTokens.Get(); err == nil && v < 0 {
		errs = append(errs, apperr.Field("limits/budget_tokens", "minimum", "토큰 상한은 0 이상이어야 합니다"))
	}
	if v, err := l.MaxTasks.Get(); err == nil && v < 1 {
		errs = append(errs, apperr.Field("limits/max_tasks", "minimum", "할 일 상한은 1 이상이어야 합니다"))
	}
	if v, err := l.TimeLimit.Get(); err == nil {
		if d, perr := parseISODuration(v); perr != nil || d <= 0 {
			errs = append(errs, apperr.Field("limits/time_limit", "invalid", "시간 상한은 PT4H 처럼 적어 주세요"))
		}
	}
	return errs
}

// mergeWorkLimits folds a WorkLimits patch into the stored jsonb (a key the
// caller omitted keeps its value; an explicit null clears it).
func mergeWorkLimits(stored []byte, patch *gen.WorkLimits) ([]byte, error) {
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
		if v == nil {
			delete(cur, k)
			continue
		}
		cur[k] = v
	}
	return json.Marshal(cur)
}

// roomAgents is the room's live agent participants (+ the assignee): the set
// a completion condition's reviewers are checked against (S-84, FR-2A.2).
func roomAgents(ctx context.Context, q pgx.Tx, roomID uuid.UUID, assignee *uuid.UUID) (map[uuid.UUID]bool, error) {
	return sessionAgents(ctx, q, roomID, assignee)
}

// openWork is createWork inside the caller's transaction. It is also what
// resolveWorkProposal(accept) runs, so a proposal opens exactly the record
// 「새 미션」 opens.
func (s *Server) openWork(ctx context.Context, tx pgx.Tx, a *rooms.Access, u *gen.User, in gen.WorkCreate, now time.Time) (uuid.UUID, error) {
	roomID := a.RoomID
	goal := strings.TrimSpace(in.Goal)
	var errs []apperr.FieldError
	if goal == "" {
		errs = append(errs, apperr.Field("goal", "required", "목표를 입력해 주세요"))
	}
	title := firstLineOf(goal)
	if in.Title != nil && strings.TrimSpace(*in.Title) != "" {
		title = strings.TrimSpace(*in.Title)
	}
	if len([]rune(title)) > 200 {
		errs = append(errs, apperr.Field("title", "length", "제목은 200자까지 쓸 수 있습니다"))
	}
	if in.Autonomy != nil && *in.Autonomy == gen.Supervised {
		errs = append(errs, apperr.Field("autonomy", "unsupported", "감독 모드는 아직 지원하지 않습니다"))
	}
	errs = append(errs, validateWorkLimits(in.Limits)...)
	if len(errs) > 0 {
		return uuid.Nil, apperr.Validation(errs...)
	}

	// The room, locked: the concurrent-missions count and the gate are read
	// under the same lock the claim's writers take first.
	var roomStatus string
	var blocked *string
	var defaultDirector *uuid.UUID
	var limitsRaw []byte
	var runtimeID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT status::text, blocked_reason::text, default_director_user_id, limits, runtime_id
		FROM room WHERE id = $1 FOR UPDATE`, roomID).Scan(&roomStatus, &blocked, &defaultDirector, &limitsRaw, &runtimeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, apperr.NotFound("room")
		}
		return uuid.Nil, err
	}
	if roomStatus == "archived" {
		return uuid.Nil, apperr.Conflict("room_archived", rooms.RoomArchivedDetail)
	}
	if blocked != nil {
		return uuid.Nil, apperr.Conflict("room_blocked", "이 방은 멈춰 있습니다 — 방을 다시 움직인 뒤 미션을 열어 주세요")
	}
	draft := in.Draft != nil && *in.Draft
	if !draft {
		if p, err := concurrentWorksConflict(ctx, tx, roomID, limitsRaw); err != nil {
			return uuid.Nil, err
		} else if p != nil {
			return uuid.Nil, p
		}
	}

	// FR-2A.1 Director: the request's, else the room's default, else the
	// person opening it.
	director := u.Id
	switch {
	case in.DirectorUserId != nil:
		director = uuid.UUID(*in.DirectorUserId)
	case defaultDirector != nil:
		director = *defaultDirector
	}
	var deputy *uuid.UUID
	if in.DeputyUserId.IsSpecified() && !in.DeputyUserId.IsNull() {
		d := uuid.UUID(in.DeputyUserId.MustGet())
		deputy = &d
	}
	for _, id := range []*uuid.UUID{&director, deputy} {
		if id == nil {
			continue
		}
		if err := s.requireMember(ctx, tx, roomID, *id); err != nil {
			return uuid.Nil, apperr.Validation(apperr.Field("director_user_id", "not_member", "Director 와 deputy 는 워크스페이스 멤버여야 합니다"))
		}
	}
	var assignee *uuid.UUID
	if in.AssigneeAgentId.IsSpecified() && !in.AssigneeAgentId.IsNull() {
		id := uuid.UUID(in.AssigneeAgentId.MustGet())
		var live bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM room_participant WHERE room_id = $1 AND agent_id = $2 AND left_at IS NULL)`, roomID, id).Scan(&live); err != nil {
			return uuid.Nil, err
		}
		if !live {
			return uuid.Nil, apperr.Validation(apperr.Field("assignee_agent_id", "not_participant", "제출자는 이 방의 참여자 중에서 골라야 합니다"))
		}
		assignee = &id
	}
	var cond any = DefaultWorkCondition(assignee != nil)
	if in.CompletionCondition != nil {
		raw, err := json.Marshal(in.CompletionCondition)
		if err != nil {
			return uuid.Nil, unreadable("completion_condition", "invalid", "종료 조건을 다시 골라 주세요", err)
		}
		tree := sessions.ParseTree(raw)
		if err := sessions.ValidateTree(tree); err != nil {
			return uuid.Nil, apperr.Validation(apperr.Field("completion_condition", sessions.TreeErrorCode(err), err.Error()))
		}
		agents, err := roomAgents(ctx, tx, roomID, assignee)
		if err != nil {
			return uuid.Nil, err
		}
		if errs := sessions.ValidateReviewers(tree, func(id uuid.UUID) bool { return agents[id] }); len(errs) > 0 {
			return uuid.Nil, apperr.Validation(errs...)
		}
		cond = json.RawMessage(raw)
	}
	criteria := []string{}
	if in.AcceptanceCriteria != nil {
		criteria = *in.AcceptanceCriteria
	}
	limits := []byte(`{}`)
	if in.Limits != nil {
		l, err := mergeWorkLimits(nil, in.Limits)
		if err != nil {
			return uuid.Nil, err
		}
		limits = l
	}
	var autonomy *string
	if in.Autonomy != nil {
		v := string(*in.Autonomy)
		autonomy = &v
	}
	status := "active"
	var startedAt *time.Time
	if draft {
		status = "draft"
	} else {
		startedAt = &now
	}

	// 「이걸 미션으로」: the message must be this room's and not already in a
	// mission (409 message_has_work).
	var fromMsg, threadRoot *uuid.UUID
	if in.FromMessageId != nil {
		id := uuid.UUID(*in.FromMessageId)
		var parent, owner *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT parent_id, work_id FROM message WHERE id = $1 AND session_id = $2 FOR UPDATE`, id, roomID).Scan(&parent, &owner)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, apperr.Validation(apperr.Field("from_message_id", "not_in_room", "이 방의 메시지가 아닙니다"))
		}
		if err != nil {
			return uuid.Nil, err
		}
		if owner != nil {
			p := apperr.Conflict("message_has_work", "이미 다른 미션에 속한 메시지입니다 — 그 미션에서 이어 가세요")
			p.Extra = map[string]any{"work_id": *owner}
			return uuid.Nil, p
		}
		// The router stores a reply to a reply against the thread's root, but
		// a thread is a tree (message.parent_id): the mission adopts it from
		// the top, whatever depth the chosen message sits at (#294 NN5).
		root := id
		if parent != nil {
			if err := tx.QueryRow(ctx, `
				WITH RECURSIVE up AS (
					SELECT id, parent_id FROM message WHERE id = $1
					UNION ALL
					SELECT m.id, m.parent_id FROM message m JOIN up ON m.id = up.parent_id
				)
				SELECT id FROM up WHERE parent_id IS NULL`, id).Scan(&root); err != nil {
				return uuid.Nil, fmt.Errorf("createWork: thread root: %w", err)
			}
		}
		fromMsg, threadRoot = &id, &root
	}
	var proposalID *uuid.UUID
	if in.FromProposalId != nil {
		id := uuid.UUID(*in.FromProposalId)
		if err := lockOpenProposal(ctx, tx, id, roomID); err != nil {
			return uuid.Nil, err
		}
		proposalID = &id
	}

	var workID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO work (room_id, title, goal, acceptance_criteria, director_user_id, deputy_user_id, assignee_agent_id,
		                  completion_condition, limits, autonomy, status, created_by, created_at, updated_at, started_at, opened_from_message_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::autonomy_level, $11::session_status, $12, $13, $13, $14, $15) RETURNING id`,
		roomID, title, goal, criteria, director, deputy, assignee, cond, limits, autonomy, status, u.Id, now, startedAt, fromMsg).Scan(&workID); err != nil {
		return uuid.Nil, fmt.Errorf("createWork: insert: %w", err)
	}
	for _, id := range []*uuid.UUID{&director, deputy} {
		if id != nil {
			if err := addRoomMember(ctx, tx, roomID, *id, now); err != nil {
				return uuid.Nil, err
			}
		}
	}
	if threadRoot != nil {
		if err := adoptThread(ctx, tx, roomID, *threadRoot, workID); err != nil {
			return uuid.Nil, err
		}
	}
	if proposalID != nil {
		if err := s.acceptProposal(ctx, tx, *proposalID, workID, u.Id, now); err != nil {
			return uuid.Nil, err
		}
	}

	// The timeline says a mission opened; with an assignee (and not a draft)
	// its first task starts the way createSession's did (E16-A step 1).
	who := displayName(ctx, tx, u.Id)
	msgID, err := s.Router.SystemPostWork(ctx, tx, roomID, &workID, who+" 님이 미션을 열었습니다. 목표: "+goal)
	if err != nil {
		return uuid.Nil, err
	}
	if !draft && assignee != nil {
		if err := s.kickOffWork(ctx, tx, roomID, workID, *assignee, msgID, runtimeID, u.Id, now); err != nil {
			return uuid.Nil, err
		}
	}
	wid := workID
	if err := logActivity(ctx, tx, a.WorkspaceID, &roomID, &u.Id, "work.created", "work", &wid,
		map[string]any{"title": title, "from_message_id": fromMsg, "from_proposal_id": proposalID}, now); err != nil {
		return uuid.Nil, err
	}
	s.publishWork(ctx, tx, a.WorkspaceID, workID, "work.created")
	return workID, nil
}

// concurrentWorksConflict is FR-2A.5's ceiling: 409 max_concurrent_works with
// the open missions in `Problem.open_works[]` so the form can offer to close
// one (openapi createWork).
func concurrentWorksConflict(ctx context.Context, q pgx.Tx, roomID uuid.UUID, limitsRaw []byte) (*Problem, error) {
	limit := rooms.DefaultMaxConcurrentWorks
	var l struct {
		MaxConcurrentWorks *int `json:"max_concurrent_works"`
	}
	if len(limitsRaw) > 0 && json.Unmarshal(limitsRaw, &l) == nil && l.MaxConcurrentWorks != nil && *l.MaxConcurrentWorks > 0 {
		limit = *l.MaxConcurrentWorks
	}
	rows, err := q.Query(ctx, `
		SELECT id, title, status::text FROM work WHERE room_id = $1 AND status::text = ANY($2) ORDER BY created_at`,
		roomID, workRunningStatuses)
	if err != nil {
		return nil, err
	}
	open := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var title, status string
		if err := rows.Scan(&id, &title, &status); err != nil {
			rows.Close()
			return nil, err
		}
		open = append(open, map[string]any{"id": id, "title": title, "status": status})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(open) < limit {
		return nil, nil
	}
	p := apperr.Conflict("max_concurrent_works",
		fmt.Sprintf("이 방에서 동시에 열 수 있는 미션은 %d개입니다 — 진행 중인 미션을 끝내거나 취소한 뒤 열어 주세요", limit))
	p.Extra = map[string]any{"open_works": open}
	return p, nil
}

// adoptThread is FR-3.1.1's 사후 귀속 (「이걸 미션으로」): the message and the
// rest of its thread that belong to no mission join this one, ONCE — a reply
// already filed under another mission stays there — and so do the lanes and
// tasks that thread started (the tasks it triggered, their lanes).
func adoptThread(ctx context.Context, tx pgx.Tx, roomID, root, workID uuid.UUID) error {
	// threadOf is every message under the root, at any depth (#294 NN5).
	const threadOf = `
		WITH RECURSIVE th AS (
			SELECT id FROM message WHERE id = $2 AND session_id = $1
			UNION
			SELECT m.id FROM message m JOIN th ON m.parent_id = th.id WHERE m.session_id = $1
		)`
	if _, err := tx.Exec(ctx, threadOf+`
		UPDATE message SET work_id = $3
		WHERE id IN (SELECT id FROM th) AND work_id IS NULL`, roomID, root, workID); err != nil {
		return fmt.Errorf("createWork: adopt thread: %w", err)
	}
	if _, err := tx.Exec(ctx, threadOf+`
		UPDATE task t SET work_id = $3
		WHERE t.session_id = $1 AND t.work_id IS NULL
		  AND t.trigger_message_id IN (SELECT id FROM th)`,
		roomID, root, workID); err != nil {
		return fmt.Errorf("createWork: adopt tasks: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE lane l SET work_id = $2
		WHERE l.session_id = $1 AND l.work_id IS NULL
		  AND EXISTS (SELECT 1 FROM task t WHERE t.lane_id = l.id AND t.work_id = $2)`,
		roomID, workID); err != nil {
		return fmt.Errorf("createWork: adopt lanes: %w", err)
	}
	return nil
}

// kickOffWork is the assignee's first task of a mission that opened with one
// (createSession's E16-A step 1, per mission): a lane bound to the mission,
// its queued task triggered by the opening line, and the human hop the
// chain depth starts from (S-78). A mission adopted from a thread whose
// assignee already has a live lane in it goes on in that lane instead.
func (s *Server) kickOffWork(ctx context.Context, tx pgx.Tx, roomID, workID, assignee, msgID uuid.UUID, runtimeID *uuid.UUID, originator uuid.UUID, now time.Time) error {
	var live bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM lane WHERE work_id = $1 AND agent_id = $2
		               AND status IN ('queued', 'running', 'waiting_human', 'blocked', 'paused'))`, workID, assignee).Scan(&live); err != nil {
		return err
	}
	if live {
		return nil
	}
	var profileID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT profile_id FROM room_participant WHERE room_id = $1 AND agent_id = $2 AND left_at IS NULL`, roomID, assignee).Scan(&profileID); err != nil {
		return fmt.Errorf("createWork: assignee profile: %w", err)
	}
	var laneID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at, work_id)
		VALUES ($1, $2, $3, 'queued', $4, $4, $5) RETURNING id`, roomID, assignee, profileID, now, workID).Scan(&laneID); err != nil {
		return err
	}
	_ = lanes.Publish(ctx, s.Hub, tx, laneID)
	if _, err := tx.Exec(ctx, `
		INSERT INTO task (lane_id, session_id, runtime_id, agent_id, profile_id, trigger_message_id, originator_user_id, status, created_at, updated_at, work_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued', $8, $8, $9)`, laneID, roomID, runtimeID, assignee, profileID, msgID, originator, now, workID); err != nil {
		return err
	}
	return s.Router.RecordHumanHop(ctx, tx, roomID, assignee, msgID, now)
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// ---------------------------------------------------------------------------
// updateWork · deleteWork
// ---------------------------------------------------------------------------

func (s *Server) UpdateWork(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	u, a, wk, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := requireWorkDirector(u, wk, false); p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.WorkUpdate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if errs := validateWorkLimits(in.Limits); len(errs) > 0 {
		writeProblem(w, apperr.Validation(errs...))
		return
	}
	now := s.Clock.Now()
	condChanged := false
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		status, _, _, err := lockWork(r.Context(), tx, workId)
		if err != nil {
			return err
		}
		if status == "completed" || status == "cancelled" {
			return apperr.Conflict("work_closed", "끝난 미션은 고칠 수 없습니다")
		}
		var limitsRaw, condRaw []byte
		var assignee *uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT limits, completion_condition, assignee_agent_id FROM work WHERE id = $1`, workId).
			Scan(&limitsRaw, &condRaw, &assignee); err != nil {
			return err
		}
		set := []string{"updated_at = $2"}
		args := []any{workId, now}
		add := func(col string, v any) {
			args = append(args, v)
			set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
		}
		if in.Title != nil {
			t := strings.TrimSpace(*in.Title)
			if t == "" || len([]rune(t)) > 200 {
				return apperr.Validation(apperr.Field("title", "length", "제목은 1~200자로 입력해 주세요"))
			}
			add("title", t)
		}
		if in.Goal != nil {
			g := strings.TrimSpace(*in.Goal)
			if g == "" {
				return apperr.Validation(apperr.Field("goal", "required", "목표를 입력해 주세요"))
			}
			add("goal", g)
		}
		if in.AcceptanceCriteria != nil {
			add("acceptance_criteria", *in.AcceptanceCriteria)
		}
		if in.Autonomy != nil {
			if *in.Autonomy == gen.Supervised {
				return apperr.Validation(apperr.Field("autonomy", "unsupported", "감독 모드는 아직 지원하지 않습니다"))
			}
			add("autonomy", string(*in.Autonomy))
		}
		if in.Limits != nil {
			merged, err := mergeWorkLimits(limitsRaw, in.Limits)
			if err != nil {
				return err
			}
			add("limits", merged)
		}
		if in.AssigneeAgentId.IsSpecified() {
			if in.AssigneeAgentId.IsNull() {
				add("assignee_agent_id", nil)
				assignee = nil
			} else {
				id := uuid.UUID(in.AssigneeAgentId.MustGet())
				var live bool
				if err := tx.QueryRow(r.Context(), `
					SELECT EXISTS (SELECT 1 FROM room_participant WHERE room_id = $1 AND agent_id = $2 AND left_at IS NULL)`, wk.RoomId, id).Scan(&live); err != nil {
					return err
				}
				if !live {
					return apperr.Validation(apperr.Field("assignee_agent_id", "not_participant", "제출자는 이 방의 참여자 중에서 골라야 합니다"))
				}
				add("assignee_agent_id", id)
				assignee = &id
			}
		}
		if in.DeputyUserId.IsSpecified() {
			if in.DeputyUserId.IsNull() {
				add("deputy_user_id", nil)
			} else {
				d := uuid.UUID(in.DeputyUserId.MustGet())
				if err := s.requireMember(r.Context(), tx, wk.RoomId, d); err != nil {
					return err
				}
				if err := addRoomMember(r.Context(), tx, wk.RoomId, d, now); err != nil {
					return err
				}
				add("deputy_user_id", d)
			}
		}
		if in.CompletionCondition != nil {
			// v0.1.5 S-84 on the mission: `active`·`paused` accept a new tree
			// (the rescue for a condition nobody can satisfy); `completing`
			// does not — the summary is already being written.
			if status == "completing" {
				return apperr.Validation(apperr.Field("completion_condition", "immutable", "끝나는 중인 미션의 종료 조건은 바꿀 수 없습니다"))
			}
			raw, err := json.Marshal(in.CompletionCondition)
			if err != nil {
				return unreadable("completion_condition", "invalid", "종료 조건을 다시 골라 주세요", err)
			}
			tree := sessions.ParseTree(raw)
			if err := sessions.ValidateTree(tree); err != nil {
				return apperr.Validation(apperr.Field("completion_condition", sessions.TreeErrorCode(err), err.Error()))
			}
			agents, err := roomAgents(r.Context(), tx, wk.RoomId, assignee)
			if err != nil {
				return err
			}
			if errs := sessions.ValidateReviewers(tree, func(id uuid.UUID) bool { return agents[id] }); len(errs) > 0 {
				return apperr.Validation(errs...)
			}
			add("completion_condition", raw)
			if status != "draft" {
				condChanged = true
				rid := wk.RoomId
				if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "work.completion_condition_changed", "work", &wk.Id,
					map[string]any{"from": json.RawMessage(condRaw), "to": json.RawMessage(raw), "status": status}, now); err != nil {
					return err
				}
			}
		}
		if len(set) == 1 {
			return nil
		}
		_, err = tx.Exec(r.Context(), `UPDATE work SET `+joinComma(set)+` WHERE id = $1`, args...)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if condChanged {
		if _, err := s.Sessions.ApplyWorkEvent(r.Context(), workId, sessions.Event{
			Kind: sessions.EventConditionChanged, Note: "Director 가 종료 조건을 바꿨습니다",
		}); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.publishWork(r.Context(), s.DB, a.WorkspaceID, workId, "work.updated")
	s.workOut(r.Context(), w, http.StatusOK, workId, u.Id)
}

// DeleteWork is FR-2A.6: the mission goes, the room and its messages stay —
// the messages are the room's, their `work_id` becomes null (FK SET NULL),
// and so do the lanes, tasks, artifacts and decisions the mission held. A
// mission still running is 409 work_active.
func (s *Server) DeleteWork(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	u, a, wk, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	// 그 미션의 director · 방장 · ws owner·admin.
	if u.Id != wk.DirectorUserId && a.RoomRole != rooms.RoleOwner && a.WorkspaceRole != "owner" && a.WorkspaceRole != "admin" {
		writeProblem(w, apperr.Forbidden("director_required", "이 미션의 Director 나 방장·소유자·관리자만 삭제할 수 있습니다"))
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		status, _, _, err := lockWork(r.Context(), tx, workId)
		if err != nil {
			return err
		}
		switch status {
		case "active", "paused", "completing":
			return apperr.Conflict("work_active", "진행 중인 미션은 먼저 끝내거나 취소해 주세요")
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM work WHERE id = $1`, workId); err != nil {
			return fmt.Errorf("deleteWork: %w", err)
		}
		rid := wk.RoomId
		if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "work.deleted", "work", &wk.Id,
			map[string]any{"title": wk.Title, "status": status}, now); err != nil {
			return err
		}
		s.publishRoom(r.Context(), tx, a.WorkspaceID, wk.RoomId)
		s.publishWorkDeleted(r.Context(), tx, a.WorkspaceID, wk.RoomId, workId)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// pause · resume · complete · cancel
// ---------------------------------------------------------------------------

func (s *Server) PauseWork(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	u, a, wk, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := requireWorkDirector(u, wk, false); p != nil {
		writeProblem(w, p)
		return
	}
	if err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		return s.pauseWorkTx(r.Context(), tx, workId, func(status string) error {
			return apperr.Conflict("invalid_transition", "진행 중인 미션만 일시정지할 수 있습니다 (현재 상태: "+apperr.StatusLabel(status)+")")
		}, s.Clock.Now())
	}); err != nil {
		writeErr(w, err)
		return
	}
	s.afterWorkChange(r.Context(), a.WorkspaceID, wk)
	s.workOut(r.Context(), w, http.StatusOK, workId, u.Id)
}

// afterWorkChange publishes a mission change (`work.updated`).
func (s *Server) afterWorkChange(ctx context.Context, wsID uuid.UUID, wk *sessions.WorkRow) {
	s.publishWork(ctx, s.DB, wsID, wk.Id, "work.updated")
}

// ResumeWork is FR-2A.3's 계속 진행 on one mission. A mission paused BY THE
// ROOM (its budget or loop gate — the roomgate mirror) is not this op's to
// lift: the room's owner answers the room's request (409 room_blocked).
func (s *Server) ResumeWork(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	var in gen.ResumeWorkJSONBody
	if r.ContentLength > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	u, a, wk, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var reason *string
	var detail []byte
	if err := s.DB.QueryRow(r.Context(), `SELECT paused_reason::text, paused_detail FROM work WHERE id = $1`, workId).Scan(&reason, &detail); err != nil {
		writeErr(w, err)
		return
	}
	rule := sessions.PlanResume(derefString(reason))
	if p := requireWorkDirector(u, wk, rule.DeputyMayResume); p != nil {
		writeProblem(w, p)
		return
	}
	if wk.Status == gen.WorkStatusPaused && sessions.ProjectPause("paused", reason, roomgate.IsMirror(detail)) == nil && derefString(reason) != sessions.PauseDirector {
		writeProblem(w, apperr.Conflict("room_blocked", "방 전체가 멈춰 있어 이 미션도 멈췄습니다 — 방장이 방을 다시 움직여야 합니다"))
		return
	}
	if errs := validateWorkLimits(in.Limits); len(errs) > 0 {
		writeProblem(w, apperr.Validation(errs...))
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		status, reason, _, err := lockWork(r.Context(), tx, workId)
		if err != nil {
			return err
		}
		if status != "paused" {
			return apperr.Conflict("invalid_transition", "일시정지된 미션만 재개할 수 있습니다 (현재 상태: "+apperr.StatusLabel(status)+")")
		}
		var limitsRaw []byte
		var startedAt *time.Time
		if err := tx.QueryRow(r.Context(), `SELECT limits, started_at FROM work WHERE id = $1`, workId).Scan(&limitsRaw, &startedAt); err != nil {
			return err
		}
		if in.Limits != nil {
			merged, err := mergeWorkLimits(limitsRaw, in.Limits)
			if err != nil {
				return err
			}
			limitsRaw = merged
			if _, err := tx.Exec(r.Context(), `UPDATE work SET limits = $2 WHERE id = $1`, workId, limitsRaw); err != nil {
				return err
			}
		}
		switch derefString(reason) {
		case sessions.PauseBudget:
			// FR-7.3: coming back on the ceiling already spent past re-trips
			// the pause on the next usage report.
			if lim := budgetOf(limitsRaw); lim > 0 {
				spent, err := sessions.WorkSpentUSD(r.Context(), tx, workId)
				if err != nil {
					return err
				}
				if err := sessions.CheckBudgetRaise("limits.budget_usd", lim, spent); err != nil {
					return err
				}
			}
		case sessions.PauseTime:
			if lim, ok := workTimeLimit(limitsRaw); ok && startedAt != nil {
				if elapsed := now.Sub(*startedAt); elapsed >= lim {
					return apperr.Validation(apperr.Field("limits.time_limit", "too_low",
						"이미 "+workDuration(elapsed.Truncate(time.Minute))+"이 지났습니다 — 새 시간 상한은 그보다 길어야 합니다"))
				}
			}
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE work SET status = 'active', paused_reason = NULL, paused_detail = NULL, updated_at = $2 WHERE id = $1`, workId, now); err != nil {
			return err
		}
		cause := tasks.CauseHitlAnswer
		if derefString(reason) == sessions.PauseBudget {
			cause = tasks.CauseBudgetApproved
		}
		scope := workScope(workId)
		if err := s.resumeScopeTasks(r.Context(), tx, scope, derefString(reason), cause, now); err != nil {
			return err
		}
		if err := liftParkedLanes(r.Context(), tx, scope, now); err != nil {
			return err
		}
		if rule.ClosesSystemHitl {
			return s.closeSessionBudgetHitl(r.Context(), tx, scope, u.Id, derefString(reason), now)
		}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.Queue.Notifier.Notify()
	s.afterWorkChange(r.Context(), a.WorkspaceID, wk)
	s.workOut(r.Context(), w, http.StatusOK, workId, u.Id)
}

// CompleteWork is FR-2A.4's manual end: the Director closes the mission, the
// summary lands in the room as a message and the room goes on. Like
// completeSession, a mission with lanes still working needs `confirm`.
func (s *Server) CompleteWork(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	u, a, wk, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := requireWorkDirector(u, wk, false); p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.CompleteWorkJSONBody // openapi 0.2.4: {confirm?}
	if r.ContentLength > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	var running int
	if err := s.DB.QueryRow(r.Context(), `
		SELECT count(*) FROM lane WHERE work_id = $1 AND status IN ('queued', 'running')`, workId).Scan(&running); err != nil {
		writeErr(w, err)
		return
	}
	if running > 0 && (in.Confirm == nil || !*in.Confirm) {
		p := apperr.Conflict("running_lanes", "진행 중인 서브 미션이 있습니다 — 그래도 끝내려면 확인 후 다시 요청해 주세요")
		p.Extra = map[string]any{"running_lane_count": running}
		writeProblem(w, p)
		return
	}
	if _, err := s.Sessions.ApplyWorkEvent(r.Context(), workId, sessions.Event{
		Kind: "director_end", Note: "Director 가 미션을 끝냈습니다",
	}); err != nil {
		writeErr(w, err)
		return
	}
	s.afterWorkChange(r.Context(), a.WorkspaceID, wk)
	s.workOut(r.Context(), w, http.StatusOK, workId, u.Id)
}

// CancelWork is FR-2A.6's cancel: `active`·`paused` → `cancelled`, the
// mission's tasks stop through §8.2.2 and its platform requests close.
func (s *Server) CancelWork(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	u, a, wk, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := requireWorkDirector(u, wk, false); p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		status, _, _, err := lockWork(r.Context(), tx, workId)
		if err != nil {
			return err
		}
		if status != "active" && status != "paused" {
			return apperr.Conflict("invalid_transition", "진행 중이거나 일시정지된 미션만 취소할 수 있습니다 (현재 상태: "+apperr.StatusLabel(status)+")")
		}
		if err := s.cancelWorkTx(r.Context(), tx, wk.RoomId, workId, now); err != nil {
			return err
		}
		if err := s.systemPostWork(r.Context(), tx, wk.RoomId, workId, displayName(r.Context(), tx, u.Id)+" 님이 미션 「"+wk.Title+"」을 취소했습니다."); err != nil {
			return err
		}
		s.publishWorkClosed(r.Context(), tx, a.WorkspaceID, wk.RoomId, workId, "cancelled")
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.afterWorkChange(r.Context(), a.WorkspaceID, wk)
	s.workOut(r.Context(), w, http.StatusOK, workId, u.Id)
}

// ---------------------------------------------------------------------------
// changeWorkDirector · setWorkSubscription
// ---------------------------------------------------------------------------

func (s *Server) ChangeWorkDirector(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	u, a, wk, p := s.workGate(r, workId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	// FR-5.3: the Director hands over; an owner·admin can do it for them
	// (a Director who has gone cannot hand over themselves).
	if u.Id != wk.DirectorUserId && a.WorkspaceRole != "owner" && a.WorkspaceRole != "admin" {
		writeProblem(w, apperr.Forbidden("director_required", "현재 Director 나 소유자·관리자만 교체할 수 있습니다"))
		return
	}
	var in gen.ChangeWorkDirectorJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		if _, _, _, err := lockWork(r.Context(), tx, workId); err != nil {
			return err
		}
		to := uuid.UUID(in.DirectorUserId)
		if err := s.requireMember(r.Context(), tx, wk.RoomId, to); err != nil {
			return err
		}
		if in.DeputyUserId.IsSpecified() {
			var dep *uuid.UUID
			if !in.DeputyUserId.IsNull() {
				d := uuid.UUID(in.DeputyUserId.MustGet())
				if err := s.requireMember(r.Context(), tx, wk.RoomId, d); err != nil {
					return err
				}
				if err := addRoomMember(r.Context(), tx, wk.RoomId, d, now); err != nil {
					return err
				}
				dep = &d
			}
			if _, err := tx.Exec(r.Context(), `UPDATE work SET deputy_user_id = $2 WHERE id = $1`, workId, dep); err != nil {
				return err
			}
		}
		line := displayName(r.Context(), tx, to) + " 님이 이 미션의 Director 가 되었습니다."
		if err := s.directorHandedOver(r.Context(), tx, a.WorkspaceID, wk.RoomId, workId, to, line, now); err != nil {
			return err
		}
		rid := wk.RoomId
		return logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "work.director_changed", "work", &wk.Id,
			map[string]any{"title": wk.Title, "from": wk.DirectorUserId, "to": to}, now)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.publishWork(r.Context(), s.DB, a.WorkspaceID, workId, "work.updated")
	s.workOut(r.Context(), w, http.StatusOK, workId, u.Id)
}

// SetWorkSubscription is FR-8's mission switch for the caller: a level
// overrides the room's subscription, null goes back to it.
func (s *Server) SetWorkSubscription(w http.ResponseWriter, r *http.Request, workId gen.WorkId) {
	u, _, _, p := s.workGate(r, workId, rooms.ActSubscribe)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.SetWorkSubscriptionJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	var err error
	if !in.Level.IsSpecified() || in.Level.IsNull() {
		_, err = s.DB.Exec(r.Context(), `DELETE FROM work_subscription WHERE work_id = $1 AND user_id = $2`, workId, u.Id)
	} else {
		level := in.Level.MustGet()
		switch level {
		case gen.SubscriptionLevelAll, gen.SubscriptionLevelHitlOnly, gen.SubscriptionLevelCompletionOnly:
		default:
			writeProblem(w, apperr.Validation(apperr.Field("level", "enum", "구독은 전부 · 확인 요청만 · 종료만 중 하나입니다")))
			return
		}
		_, err = s.DB.Exec(r.Context(), `
			INSERT INTO work_subscription (work_id, user_id, level, updated_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT (work_id, user_id) DO UPDATE SET level = EXCLUDED.level, updated_at = EXCLUDED.updated_at`,
			workId, u.Id, string(level), s.Clock.Now())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workTimeLimit reads WorkLimits.time_limit.
func workTimeLimit(limits []byte) (time.Duration, bool) {
	var l struct {
		TimeLimit *string `json:"time_limit"`
	}
	if len(limits) == 0 || json.Unmarshal(limits, &l) != nil || l.TimeLimit == nil {
		return 0, false
	}
	d, err := parseISODuration(*l.TimeLimit)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// ---------------------------------------------------------------------------
// FR-2A.3 — the mission time limit (WorkLimits.time_limit)
// ---------------------------------------------------------------------------

// SweepWorkTimeLimits is FR-2A.3's time ceiling on a mission: an `active`
// mission whose `limits.time_limit` has run out since it started is paused
// `time` — its running turns are cancelled (§8.2.2: a time pause does not
// drain, DrainsRunningTurn) — and its Director is asked whether to go on
// (purpose `time`, answered with `time_extension` or by resumeWork). A room
// has no time limit of its own in v1 (T-R1b1 hand-over: there was no path at
// all). Run by the scheduler; returns how many missions it paused.
func (s *Server) SweepWorkTimeLimits(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	rows, err := s.DB.Query(ctx, `
		SELECT wk.id, wk.limits, wk.started_at FROM work wk
		WHERE wk.status = 'active' AND wk.started_at IS NOT NULL AND wk.limits ? 'time_limit'
		  AND wk.limits->>'time_limit' IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("works: time sweep: %w", err)
	}
	type due struct {
		id             uuid.UUID
		limit, elapsed time.Duration
	}
	var list []due
	for rows.Next() {
		var id uuid.UUID
		var limits []byte
		var started time.Time
		if err := rows.Scan(&id, &limits, &started); err != nil {
			rows.Close()
			return 0, err
		}
		lim, ok := workTimeLimit(limits)
		if !ok {
			continue
		}
		if el := now.Sub(started); el >= lim {
			list = append(list, due{id, lim, el})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		paused, err := s.pauseWorkForTime(ctx, d.id, d.limit, d.elapsed, now)
		if err != nil {
			s.Log.Warn("works: pause for time limit", "work", d.id, "err", err)
			continue
		}
		if paused {
			n++
		}
	}
	return n, nil
}

func (s *Server) pauseWorkForTime(ctx context.Context, workID uuid.UUID, limit, elapsed time.Duration, now time.Time) (bool, error) {
	var wk *sessions.WorkRow
	var hitlOut uuid.UUID
	paused := false
	err := s.inSessionTx(ctx, func(tx pgx.Tx) error {
		status, _, _, err := lockWork(ctx, tx, workID)
		if err != nil {
			return err
		}
		if status != "active" {
			return nil // moved between the scan and the lock
		}
		if wk, err = sessions.LoadWorkRow(ctx, tx, workID); err != nil {
			return err
		}
		lim, el := workDuration(limit), workDuration(elapsed.Truncate(time.Minute))
		question := fmt.Sprintf("미션 「%s」이 시간 상한 %s에 도달했습니다 (경과 %s). 계속 진행할까요?", wk.Title, lim, el)
		var hitlID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at, work_id)
			VALUES ($1, NULL, 'system', 'approval', $2, 'director', 'time', $3, $4, $5) RETURNING id`,
			wk.RoomId, question, now.Add(24*time.Hour), now, workID).Scan(&hitlID); err != nil {
			return fmt.Errorf("works: time hitl: %w", err)
		}
		detail := tasks.PausedDetail(sessions.PauseTime, now)
		detail.Time = &struct {
			Elapsed *string `json:"elapsed,omitempty"`
			Limit   *string `json:"limit,omitempty"`
		}{Elapsed: &el, Limit: &lim}
		detail.SystemHitlRequestId = nullUUID(&hitlID)
		raw, _ := json.Marshal(detail)
		if _, err := tx.Exec(ctx, `
			UPDATE work SET status = 'paused', paused_reason = 'time', paused_detail = $2, updated_at = $3 WHERE id = $1`,
			workID, raw, now); err != nil {
			return err
		}
		if err := s.Tasks.PauseWorkTasks(ctx, tx, workID, sessions.PauseTime, raw, now); err != nil {
			return err
		}
		if err := s.attachHitlCard(ctx, tx, wk.WorkspaceID, wk.RoomId, hitlID, messages.HitlCard{
			Type: hitl.KindApproval, Question: question, WorkID: &workID,
		}, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, work_id, recipient_basis)
			SELECT m.id, $4::inbox_item_type, $5::inbox_severity, $1, $2, $3, $8, 'director'
			FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
			wk.RoomId, hitlID, now, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest), wk.WorkspaceID, wk.DirectorUserId, workID); err != nil {
			return err
		}
		if err := sessions.WorkInboxItem(ctx, tx, wk.WorkspaceID, wk.DirectorUserId, inbox.TypeWorkPaused, wk.RoomId, workID, now); err != nil {
			return err
		}
		paused, hitlOut = true, hitlID
		return nil
	})
	if err != nil || !paused {
		return false, err
	}
	s.publishHitl(ctx, wk.WorkspaceID, wk.RoomId, hitlOut, "hitl.created")
	s.afterWorkChange(ctx, wk.WorkspaceID, wk)
	return true, nil
}

// resumeWorkForTime is the answer to a mission's time request (purpose
// `time`, approved with `time_extension`): the ceiling grows by the
// extension from where it stood and the mission comes back as resumeWork
// would bring it — the answer IS the resume, like K-10's budget raise.
func (s *Server) resumeWorkForTime(ctx context.Context, workID uuid.UUID, extension time.Duration, now time.Time) error {
	return s.inSessionTx(ctx, func(tx pgx.Tx) error {
		status, reason, _, err := lockWork(ctx, tx, workID)
		if err != nil {
			return err
		}
		if status != "paused" || derefString(reason) != sessions.PauseTime {
			return nil // resumed by hand in between — the answer stands, nothing to lift
		}
		var limits []byte
		var started *time.Time
		if err := tx.QueryRow(ctx, `SELECT limits, started_at FROM work WHERE id = $1`, workID).Scan(&limits, &started); err != nil {
			return err
		}
		cur, _ := workTimeLimit(limits)
		next := cur + extension
		if started != nil && now.Sub(*started) >= next {
			return apperr.Validation(apperr.Field("time_extension", "too_low",
				"이미 "+workDuration(now.Sub(*started).Truncate(time.Minute))+"이 지났습니다 — 그보다 길게 연장해 주세요"))
		}
		merged, err := mergeWorkLimits(limits, &gen.WorkLimits{TimeLimit: nullable.NewNullableWithValue(workDuration(next))})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE work SET limits = $2, status = 'active', paused_reason = NULL, paused_detail = NULL, updated_at = $3 WHERE id = $1`,
			workID, merged, now); err != nil {
			return err
		}
		scope := workScope(workID)
		if err := s.resumeScopeTasks(ctx, tx, scope, sessions.PauseTime, tasks.CauseHitlAnswer, now); err != nil {
			return err
		}
		return liftParkedLanes(ctx, tx, scope, now)
	})
}

// workDuration renders a mission time as ISO 8601 the way a person writes it
// (PT3H, PT1H30M, P1DT2H) — WorkLimits.time_limit and the time request's
// sentence.
func workDuration(d time.Duration) string {
	if d <= 0 {
		return "PT0S"
	}
	d = d.Truncate(time.Second)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	h, m, sec := d/time.Hour, (d%time.Hour)/time.Minute, (d%time.Minute)/time.Second
	out := "P"
	if days > 0 {
		out += fmt.Sprintf("%dD", days)
	}
	if h > 0 || m > 0 || sec > 0 {
		out += "T"
		if h > 0 {
			out += fmt.Sprintf("%dH", h)
		}
		if m > 0 {
			out += fmt.Sprintf("%dM", m)
		}
		if sec > 0 {
			out += fmt.Sprintf("%dS", sec)
		}
	}
	return out
}
