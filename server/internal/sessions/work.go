package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// PRD v0.19 FR-2A.5 — a room holds several missions (T-R1b2). Everything in
// this package that used to be keyed by the session id is keyed by the
// mission (work) id; the old `/sessions/*` surface reaches the same code
// through LegacyWork.

// LegacyJoin is the old session row: a room made by the old path (createSession
// or the 0025 migration) and ITS mission, `room.legacy_work_id`. It replaces
// `JOIN work wk ON wk.room_id = s.id`, which stood on the one-mission-per-room
// index and, once that index is gone, returns one row per mission — an
// arbitrary one under QueryRow, all of them under FOR UPDATE and in lists
// (plan/V19_R1B_HANDOFF.md, 42 places). A room made by createRoom has no
// legacy work and is not a session: the join finds nothing and the old
// endpoints answer 404, as they did before.
const LegacyJoin = `JOIN work wk ON wk.id = s.legacy_work_id`

// LegacyWork is the mission the old `/sessions/{id}` surface stands for. A
// room that is not an old-path session (or does not exist) is 404 session.
func LegacyWork(ctx context.Context, q db.DBTX, roomID uuid.UUID) (uuid.UUID, error) {
	var w *uuid.UUID
	err := q.QueryRow(ctx, `SELECT legacy_work_id FROM room WHERE id = $1`, roomID).Scan(&w)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && w == nil) {
		return uuid.Nil, apperr.NotFound("session")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("sessions: legacy work: %w", err)
	}
	return *w, nil
}

// closedConflict is the 409 for an event on a mission that has ended. The old
// session surface keeps its code and sentence; a mission opened through the
// works API speaks of a mission.
func closedConflict(legacy bool) error {
	if legacy {
		return apperr.Conflict("session_closed", "이미 끝난 미션입니다")
	}
	return apperr.Conflict("work_closed", "이미 끝난 미션입니다")
}

// ClosedConflict is closedConflict for callers outside the package (the
// artifact store gates a submission on the mission it belongs to).
func ClosedConflict(legacy bool) error { return closedConflict(legacy) }

// ---------------------------------------------------------------------------
// The Work read model (openapi Work · WorkListItem)
// ---------------------------------------------------------------------------

// WorkPauseReasons is the contract's WorkPauseReason: what a MISSION pauses
// for. The room's reasons (loop, runtime_offline) are not in it — a room
// blocks, it does not pause a mission (openapi WorkPauseReason).
var workPauseReasons = map[string]bool{PauseBudget: true, PauseTime: true, PauseDirector: true}

// ProjectPause is the Work response's reading of a stored pause (T-R1b1 Q2
// hand-over): a mission parked BY THE ROOM (roomgate's mirror mark, or an
// old-path runtime_offline / loop pause) answers `paused` with
// `paused_reason: null` — the reason is the room's and lives in
// Room.blocked_reason; the stored detail still rides in `paused_detail`. A
// mission paused for its own budget, time or by its Director answers with that
// reason.
func ProjectPause(status string, reason *string, mirrored bool) *gen.WorkPauseReason {
	if status != "paused" || reason == nil || mirrored || !workPauseReasons[*reason] {
		return nil
	}
	r := gen.WorkPauseReason(*reason)
	return &r
}

// workSelect reads a mission with its room's defaults (autonomy and limits are
// the room's unless the mission sets its own — WorkLimits "비우면 방 한도").
const workSelect = `
	SELECT wk.id, wk.room_id, s.workspace_id, wk.title, wk.goal, wk.acceptance_criteria, wk.director_user_id, wk.deputy_user_id,
	       wk.assignee_agent_id, wk.completion_condition, wk.completion_met, wk.limits, COALESCE(wk.autonomy, s.autonomy)::text,
	       wk.status::text, wk.paused_reason::text, wk.paused_detail, wk.cost_usd,
	       (SELECT COALESCE(bool_or(u.estimated), false) FROM task_usage u JOIN task t ON t.id = u.task_id WHERE t.work_id = wk.id),
	       wk.summary_message_id, wk.opened_from_message_id, wk.created_by, wk.created_at, wk.updated_at, wk.started_at, wk.finished_at,
	       (SELECT max(m.created_at) FROM message m WHERE m.work_id = wk.id),
	       EXISTS (SELECT 1 FROM hitl_request h WHERE h.work_id = wk.id AND h.status = 'open'),
	       s.legacy_work_id IS NOT DISTINCT FROM wk.id
	FROM work wk JOIN room s ON s.id = wk.room_id`

// WorkRow is one mission as the handlers need it besides the API shape.
type WorkRow struct {
	gen.Work
	WorkspaceID  uuid.UUID
	Legacy       bool // the room's old-path session mission
	WaitingHuman bool
	metRaw       []byte
	condRaw      []byte
	assignee     *uuid.UUID
}

func scanWork(row pgx.Row) (*WorkRow, error) {
	var w WorkRow
	var (
		deputy, assignee, summaryID, openedFrom *uuid.UUID
		cond, met, limits, detail               []byte
		autonomy, status                        string
		reason                                  *string
		cost                                    float64
		estimated                               bool
		startedAt, finishedAt, last             *time.Time
	)
	err := row.Scan(&w.Id, &w.RoomId, &w.WorkspaceID, &w.Title, &w.Goal, &w.AcceptanceCriteria, &w.DirectorUserId, &deputy,
		&assignee, &cond, &met, &limits, &autonomy, &status, &reason, &detail, &cost, &estimated,
		&summaryID, &openedFrom, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt, &startedAt, &finishedAt, &last,
		&w.WaitingHuman, &w.Legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("work")
	}
	if err != nil {
		return nil, fmt.Errorf("sessions: load work: %w", err)
	}
	if w.AcceptanceCriteria == nil {
		w.AcceptanceCriteria = []string{}
	}
	w.DeputyUserId = tasks.NullUUID(deputy)
	w.AssigneeAgentId = tasks.NullUUID(assignee)
	_ = json.Unmarshal(cond, &w.CompletionCondition)
	_ = json.Unmarshal(limits, &w.Limits)
	w.Autonomy = gen.AutonomyLevel(autonomy)
	w.Status = gen.WorkStatus(status)
	w.PausedReason = nullable.NewNullNullable[gen.WorkPauseReason]()
	if status == "paused" && reason != nil {
		if p := ProjectPause(status, reason, roomgate.IsMirror(detail)); p != nil {
			w.PausedReason = nullable.NewNullableWithValue(*p)
		}
		var d gen.PausedDetail
		if len(detail) > 0 && json.Unmarshal(detail, &d) == nil {
			w.PausedDetail = &d
		} else {
			w.PausedDetail = &gen.PausedDetail{Reason: gen.PauseReason(*reason), PausedAt: w.UpdatedAt}
		}
	}
	w.CostUsd = float32(cost)
	w.CostEstimated = &estimated
	w.SummaryMessageId = tasks.NullUUID(summaryID)
	w.OpenedFromMessageId = tasks.NullUUID(openedFrom)
	w.StartedAt = tasks.NullTime(startedAt)
	w.FinishedAt = tasks.NullTime(finishedAt)
	w.LastActivityAt = tasks.NullTime(last)
	w.metRaw, w.condRaw, w.assignee = met, cond, assignee
	return &w, nil
}

// LoadWorkRow reads one mission (no people, no progress) — for the handlers'
// permission and state checks.
func LoadWorkRow(ctx context.Context, q db.DBTX, workID uuid.UUID) (*WorkRow, error) {
	return scanWork(q.QueryRow(ctx, workSelect+` WHERE wk.id = $1`, workID))
}

// LoadWork is getWork: the mission as `viewer` sees it (my_work_role and
// subscription are theirs).
func LoadWork(ctx context.Context, q db.DBTX, workID uuid.UUID, viewer uuid.UUID) (*gen.Work, error) {
	w, err := LoadWorkRow(ctx, q, workID)
	if err != nil {
		return nil, err
	}
	if w.CompletionProgress, err = progressOf(ctx, q, w.RoomId, w.condRaw, w.metRaw, w.assignee); err != nil {
		return nil, err
	}
	if d, err := auth.LoadUser(ctx, q, w.DirectorUserId); err == nil {
		w.Director = d
	}
	if dep, err := w.DeputyUserId.Get(); err == nil {
		if d, err := auth.LoadUser(ctx, q, dep); err == nil {
			w.Deputy = d
		}
	}
	w.MyWorkRole = gen.WorkMyWorkRoleMember
	switch {
	case viewer == w.DirectorUserId:
		w.MyWorkRole = gen.WorkMyWorkRoleDirector
	case w.DeputyUserId.IsSpecified() && !w.DeputyUserId.IsNull() && w.DeputyUserId.MustGet() == viewer:
		w.MyWorkRole = gen.WorkMyWorkRoleDeputy
	}
	var level *string
	if err := q.QueryRow(ctx, `SELECT level FROM work_subscription WHERE work_id = $1 AND user_id = $2`, workID, viewer).Scan(&level); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if level != nil {
		l := gen.SubscriptionLevel(*level)
		w.Subscription = &l
	}
	out := w.Work
	return &out, nil
}

// ListItem is WorkListItem for one row (the S7 chip row and the past-missions
// list, and the `work.created`/`work.updated` frames).
func (w *WorkRow) ListItem(ctx context.Context, q db.DBTX) (gen.WorkListItem, error) {
	it := gen.WorkListItem{
		Id: w.Id, RoomId: w.RoomId, Title: w.Title, Goal: w.Goal, Status: w.Status, PausedReason: w.PausedReason,
		AssigneeAgentId: w.AssigneeAgentId, CostUsd: w.CostUsd, LastActivityAt: w.LastActivityAt, FinishedAt: w.FinishedAt,
	}
	if !it.LastActivityAt.IsSpecified() {
		it.LastActivityAt = nullable.NewNullNullable[time.Time]()
	}
	waiting := w.WaitingHuman
	it.WaitingHuman = &waiting
	it.BudgetUsd = nullable.NewNullNullable[float32]()
	if w.Limits.BudgetUsd.IsSpecified() && !w.Limits.BudgetUsd.IsNull() {
		it.BudgetUsd = w.Limits.BudgetUsd
	}
	prog, err := progressOf(ctx, q, w.RoomId, w.condRaw, w.metRaw, w.assignee)
	if err != nil {
		return it, err
	}
	it.CompletionProgress.Met, it.CompletionProgress.Total = prog.Met, prog.Total
	if d, err := auth.LoadUser(ctx, q, w.DirectorUserId); err == nil {
		it.Director = *d
	}
	return it, nil
}

// OpenWorkStatuses are the missions that count against the room's
// max_concurrent_works and block a delete (FR-2A.5 · FR-2A.6).
var OpenWorkStatuses = []string{"draft", "active", "paused", "completing"}

// ListWorks is listWorks: newest first; `status` filters (empty = all).
func ListWorks(ctx context.Context, q db.DBTX, roomID uuid.UUID, status []string, cursor *string, limit int) ([]gen.WorkListItem, *string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := []string{"wk.room_id = $1"}
	args := []any{roomID}
	if len(status) > 0 {
		args = append(args, status)
		where = append(where, fmt.Sprintf("wk.status::text = ANY($%d)", len(args)))
	}
	if cursor != nil {
		if cid, err := uuid.Parse(*cursor); err == nil {
			args = append(args, cid)
			where = append(where, fmt.Sprintf("(wk.created_at, wk.id) < (SELECT created_at, id FROM work WHERE id = $%d)", len(args)))
		}
	}
	args = append(args, limit+1)
	rows, err := q.Query(ctx, workSelect+` WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY wk.created_at DESC, wk.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("sessions: list works: %w", err)
	}
	var list []*WorkRow
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		list = append(list, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(list) > limit {
		list = list[:limit]
		c := list[len(list)-1].Id.String()
		next = &c
	}
	out := make([]gen.WorkListItem, 0, len(list))
	for _, w := range list {
		it, err := w.ListItem(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, it)
	}
	return out, next, nil
}
