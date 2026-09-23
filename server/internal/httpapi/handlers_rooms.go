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

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// Rooms (openapi 0.2.0 — PRD v0.19 FR-2 · FR-5.3 · FR-8, T-R1b3): the room
// itself, its settings, archive, delete, the unread marker, 「여기까지 정리」,
// the owner and the deputy. The roster and the reference links are in
// handlers_room_participants.go.
//
// Every handler asks rooms.Require for its one action — the SCREEN §2.3 table
// lives in rooms.Decide and nowhere else.

// roomGate is user() + rooms.Require: the caller and their standing, or the
// Problem to answer (401 · 404 invisible · 403 role · 409 room_archived).
func (s *Server) roomGate(r *http.Request, roomID uuid.UUID, act rooms.Action) (*gen.User, *rooms.Access, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, nil, p
	}
	a, err := rooms.Require(r.Context(), s.DB, roomID, u.Id, act)
	if err != nil {
		return nil, nil, apperr.As(err)
	}
	return u, a, nil
}

// roomOut answers with the room as the caller sees it (my_room_role,
// my_capabilities and unread are the caller's).
func (s *Server) roomOut(ctx context.Context, w http.ResponseWriter, status int, roomID, userID uuid.UUID) {
	out, err := s.loadRoom(ctx, roomID, userID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) loadRoom(ctx context.Context, roomID, userID uuid.UUID) (*gen.Room, error) {
	a, err := rooms.LoadAccess(ctx, s.DB, roomID, userID)
	if err != nil {
		return nil, err
	}
	return rooms.Load(ctx, s.DB, a, s.Clock.Now())
}

// publishRoom emits `room.updated` — the partial the contract lists (blocked
// reason and detail, status, name, description, last activity). Viewer-shaped
// fields (unread, my role, capabilities) are never in a broadcast frame.
func (s *Server) publishRoom(ctx context.Context, q db.DBTX, wsID, roomID uuid.UUID) {
	if s.Hub == nil {
		return
	}
	var name, desc, status, vis string
	var blocked *string
	var detail []byte
	var last *time.Time
	if err := q.QueryRow(ctx, `
		SELECT name, description, status::text, visibility::text, blocked_reason::text, blocked_detail,
		       (SELECT max(created_at) FROM message m WHERE m.session_id = r.id)
		FROM room r WHERE id = $1`, roomID).Scan(&name, &desc, &status, &vis, &blocked, &detail, &last); err != nil {
		return
	}
	payload := map[string]any{
		"id": roomID, "name": name, "description": desc, "status": status, "visibility": vis,
		"blocked_reason": blocked, "blocked_detail": json.RawMessage(nullJSON(detail)), "last_activity_at": last,
	}
	rid := roomID
	_ = s.Hub.Publish(ctx, q, wsID, &rid, "room.updated", payload)
}

func nullJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// logActivity writes one activity_log line (PRD §7, openapi listActivityLog).
// A room-scoped line keeps the room's name in the payload: the FK is ON
// DELETE SET NULL, and S15 still has to say which room a `room.deleted` was.
func logActivity(ctx context.Context, q db.DBTX, wsID uuid.UUID, roomID *uuid.UUID, actor *uuid.UUID, action, objectType string, objectID *uuid.UUID, payload map[string]any, now time.Time) error {
	actorType := "user"
	if actor == nil {
		actorType = "system"
	}
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := q.Exec(ctx, `
		INSERT INTO activity_log (workspace_id, session_id, actor_type, actor_id, action, object_type, object_id, payload, created_at)
		VALUES ($1, $2, $3::author_type, $4, $5, $6, $7, $8, $9)`,
		wsID, roomID, actorType, actor, action, objectType, objectID, payload, now)
	if err != nil {
		return fmt.Errorf("activity_log %s: %w", action, err)
	}
	return nil
}

// displayName is how a person appears inside a system message.
func displayName(ctx context.Context, q db.DBTX, userID uuid.UUID) string {
	var name string
	if err := q.QueryRow(ctx, `SELECT display_name FROM app_user WHERE id = $1`, userID).Scan(&name); err != nil || name == "" {
		return "알 수 없는 사람"
	}
	return name
}

// ---------------------------------------------------------------------------
// list · create · get · update
// ---------------------------------------------------------------------------

func (s *Server) ListRooms(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.ListRoomsParams) {
	u, m, p := s.member(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	o := rooms.RoomListOptions{Participating: true}
	if params.Q != nil {
		o.Query = *params.Q
	}
	if params.UnreadOnly != nil {
		o.UnreadOnly = *params.UnreadOnly
	}
	if params.Participating != nil {
		o.Participating = *params.Participating
	}
	if params.IncludeArchived != nil {
		o.IncludeArchived = *params.IncludeArchived
	}
	if params.Cursor != nil {
		o.Cursor = *params.Cursor
	}
	if params.Limit != nil {
		o.Limit = *params.Limit
	}
	items, next, err := rooms.List(r.Context(), s.DB, workspaceId, u.Id, m.Role, o)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// CreateRoom is FR-2.1: a name is enough. Everything else is inherited from
// the workspace's room_defaults (visibility · isolation · autonomy · limits);
// with no computer connected the room is still made (201 — the old
// createSession's 409 no_runtime is gone), and the runtime is fixed at the
// first dispatch (FR-2.1.1).
func (s *Server) CreateRoom(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.CreateRoomParams) {
	u, _, p := s.member(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.RoomCreate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		name := strings.TrimSpace(in.Name)
		desc := ""
		if in.Description != nil {
			desc = strings.TrimSpace(*in.Description)
		}
		var errs []apperr.FieldError
		if name == "" || len([]rune(name)) > 200 {
			errs = append(errs, apperr.Field("name", "length", "방 이름은 1~200자로 입력해 주세요"))
		}
		if len([]rune(desc)) > 500 {
			errs = append(errs, apperr.Field("description", "length", "설명은 500자까지 쓸 수 있습니다"))
		}
		if len(errs) > 0 {
			return 0, nil, apperr.Validation(errs...)
		}
		d, err := loadRoomDefaults(r.Context(), s.DB, workspaceId)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		now := s.Clock.Now()
		var roomID uuid.UUID
		err = s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
			if err := tx.QueryRow(r.Context(), `
				INSERT INTO room (workspace_id, name, description, owner_user_id, visibility, isolation, autonomy, limits, created_by, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $4, $9, $9) RETURNING id`,
				workspaceId, name, desc, u.Id, d.visibility, map[string]any{"kind": d.isolation}, d.autonomy, d.limits, now).Scan(&roomID); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO room_participant (room_id, user_id, role, joined_at) VALUES ($1, $2, 'owner', $3)`, roomID, u.Id, now); err != nil {
				return err
			}
			s.publishRoom(r.Context(), tx, workspaceId, roomID)
			return nil
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		out, err := s.loadRoom(r.Context(), roomID, u.Id)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, out, nil
	})
}

// roomDefaults is what a new room inherits (FR-2.1 · openapi RoomDefaults).
type roomDefaults struct {
	visibility, isolation, autonomy string
	limits                          map[string]any
}

// loadRoomDefaults reads workspace_settings.room_defaults over the contract's
// defaults. The isolation default is the old `default_isolation` column
// unless room_defaults names one — S14's two controls must not disagree about
// what a new room gets.
func loadRoomDefaults(ctx context.Context, q db.DBTX, wsID uuid.UUID) (*roomDefaults, error) {
	d := &roomDefaults{visibility: rooms.VisWorkspace, isolation: "none", autonomy: "guided",
		limits: map[string]any{"max_parallel_lanes": rooms.DefaultMaxParallelLanes, "max_concurrent_works": rooms.DefaultMaxConcurrentWorks}}
	var raw []byte
	var iso string
	err := q.QueryRow(ctx, `SELECT room_defaults, default_isolation::text FROM workspace_settings WHERE workspace_id = $1`, wsID).Scan(&raw, &iso)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	d.isolation = iso
	var rd gen.RoomDefaults
	if len(raw) > 0 && json.Unmarshal(raw, &rd) == nil {
		if rd.Visibility != nil {
			d.visibility = string(*rd.Visibility)
		}
		if rd.IsolationKind != nil {
			d.isolation = string(*rd.IsolationKind)
		}
		if rd.Autonomy != nil {
			d.autonomy = string(*rd.Autonomy)
		}
		if rd.Limits != nil {
			var m map[string]any
			if b, err := json.Marshal(rd.Limits); err == nil && json.Unmarshal(b, &m) == nil {
				for k, v := range m {
					d.limits[k] = v
				}
			}
		}
	}
	if d.isolation != "none" {
		// A worktree needs a repository path, which a one-field form cannot
		// ask for (FR-2.1.1: "저장소를 쓰는 방은 첫 dispatch 전에 격리를 고르게
		// 한다"). The room starts `none`; S20 sets the path before the first run.
		d.isolation = "none"
	}
	return d, nil
}

func (s *Server) GetRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	p := principalOf(r)
	if p.Task != nil {
		// openapi getRoom: "TaskToken 이면 그 task 의 방만".
		if p.Task.SessionID != roomId {
			writeProblem(w, apperr.Forbidden("outside_task_scope", "다른 방에는 접근할 수 없습니다"))
			return
		}
		a := &rooms.Access{RoomID: roomId}
		if err := s.DB.QueryRow(r.Context(), `SELECT workspace_id, visibility::text FROM room WHERE id = $1`, roomId).Scan(&a.WorkspaceID, &a.Visibility); err != nil {
			writeProblem(w, apperr.NotFound("room"))
			return
		}
		out, err := rooms.Load(r.Context(), s.DB, a, s.Clock.Now())
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	u, a, pr := s.roomGate(r, roomId, rooms.ActView)
	if pr != nil {
		writeProblem(w, pr)
		return
	}
	if a.AuditViewed() {
		// FR-5.3: a workspace owner·admin reading an invited room they are
		// not in leaves a line — the room's people cannot see who looked
		// otherwise.
		rid := roomId
		if err := logActivity(r.Context(), s.DB, a.WorkspaceID, &rid, &u.Id, "room.audit_viewed", "room", &rid,
			map[string]any{"name": a.Name}, s.Clock.Now()); err != nil {
			writeErr(w, err)
			return
		}
	}
	out, err := rooms.Load(r.Context(), s.DB, a, s.Clock.Now())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// UpdateRoom is S20. runtime_id and isolation are fixed by the first dispatch
// (409 runtime_pinned): the room's working folders are tied to that machine.
// A visibility change and any settings change go to the timeline and to
// activity_log (FR-2.2 last bullet).
func (s *Server) UpdateRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	u, a, p := s.roomGate(r, roomId, rooms.ActConfigure)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.RoomUpdate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	var errs []apperr.FieldError
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len([]rune(*in.Name)) > 200) {
		errs = append(errs, apperr.Field("name", "length", "방 이름은 1~200자로 입력해 주세요"))
	}
	if in.Description != nil && len([]rune(*in.Description)) > 500 {
		errs = append(errs, apperr.Field("description", "length", "설명은 500자까지 쓸 수 있습니다"))
	}
	if in.Visibility != nil && *in.Visibility != gen.RoomVisibilityWorkspace && *in.Visibility != gen.RoomVisibilityInvited {
		errs = append(errs, apperr.Field("visibility", "enum", "알 수 없는 공개 범위입니다"))
	}
	if in.Autonomy != nil && *in.Autonomy == gen.Supervised {
		errs = append(errs, apperr.Field("autonomy", "unsupported", "감독 모드는 아직 지원하지 않습니다"))
	}
	if in.Isolation != nil {
		switch in.Isolation.Kind {
		case gen.IsolationKindNone:
		case gen.IsolationKindWorktree:
			if in.Isolation.RepoPath == nil || strings.TrimSpace(*in.Isolation.RepoPath) == "" {
				errs = append(errs, apperr.Field("isolation/repo_path", "required", "워크트리 격리에는 그 컴퓨터의 저장소 경로가 필요합니다"))
			}
		case gen.IsolationKindContainer:
			errs = append(errs, apperr.Field("isolation/kind", "unsupported", "컨테이너 격리는 아직 지원하지 않습니다"))
		default:
			errs = append(errs, apperr.Field("isolation/kind", "enum", "알 수 없는 격리 방식입니다"))
		}
	}
	if l := in.Limits; l != nil {
		if l.MaxConcurrentWorks != nil && *l.MaxConcurrentWorks < 1 {
			errs = append(errs, apperr.Field("limits/max_concurrent_works", "out_of_range", "1 이상이어야 합니다"))
		}
		if l.MaxParallelLanes != nil && *l.MaxParallelLanes < 1 {
			errs = append(errs, apperr.Field("limits/max_parallel_lanes", "out_of_range", "1 이상이어야 합니다"))
		}
		if l.BudgetUsd.IsSpecified() && !l.BudgetUsd.IsNull() && l.BudgetUsd.MustGet() < 0 {
			errs = append(errs, apperr.Field("limits/budget_usd", "out_of_range", "0 이상이어야 합니다"))
		}
	}
	if len(errs) > 0 {
		writeProblem(w, apperr.Validation(errs...))
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var visibility string
		var runtimeID *uuid.UUID
		var pinned bool
		if err := tx.QueryRow(r.Context(), `
			SELECT visibility::text, runtime_id,
			       EXISTS (SELECT 1 FROM task_attempt ta JOIN task t ON t.id = ta.task_id WHERE t.session_id = r.id)
			FROM room r WHERE id = $1 FOR UPDATE`, roomId).Scan(&visibility, &runtimeID, &pinned); err != nil {
			return err
		}
		sets, args := []string{}, []any{roomId}
		add := func(expr string, v any) {
			args = append(args, v)
			sets = append(sets, fmt.Sprintf(expr, len(args)))
		}
		changed := []string{}
		if in.RuntimeId.IsSpecified() || in.Isolation != nil {
			if pinned {
				return apperr.Conflict("runtime_pinned", "이 방은 이미 첫 실행을 시작해 컴퓨터와 격리 방식을 바꿀 수 없습니다 — 작업 폴더가 그 컴퓨터에 묶여 있습니다")
			}
		}
		if in.RuntimeId.IsSpecified() {
			if in.RuntimeId.IsNull() {
				add("runtime_id = $%d", nil)
			} else {
				id := uuid.UUID(in.RuntimeId.MustGet())
				var rws uuid.UUID
				if err := tx.QueryRow(r.Context(), `SELECT workspace_id FROM runtime WHERE id = $1`, id).Scan(&rws); err != nil || rws != a.WorkspaceID {
					return apperr.Validation(apperr.Field("runtime_id", "runtime_not_in_workspace", "이 워크스페이스에 연결된 컴퓨터가 아닙니다"))
				}
				add("runtime_id = $%d", id)
			}
			changed = append(changed, "runtime_id")
		}
		if in.Isolation != nil {
			iso := map[string]any{"kind": string(in.Isolation.Kind)}
			if in.Isolation.RepoPath != nil {
				iso["repo_path"] = *in.Isolation.RepoPath
			}
			if in.Isolation.RemoteUrl.IsSpecified() && !in.Isolation.RemoteUrl.IsNull() {
				iso["remote_url"] = in.Isolation.RemoteUrl.MustGet()
			}
			add("isolation = $%d", iso)
			changed = append(changed, "isolation")
		}
		if in.Name != nil {
			add("name = $%d", strings.TrimSpace(*in.Name))
			changed = append(changed, "name")
		}
		if in.Description != nil {
			add("description = $%d", strings.TrimSpace(*in.Description))
			changed = append(changed, "description")
		}
		if in.Autonomy != nil {
			add("autonomy = $%d", string(*in.Autonomy))
			changed = append(changed, "autonomy")
		}
		if in.DefaultDirectorUserId.IsSpecified() {
			if in.DefaultDirectorUserId.IsNull() {
				add("default_director_user_id = $%d", nil)
			} else {
				id := uuid.UUID(in.DefaultDirectorUserId.MustGet())
				if err := s.requireMember(r.Context(), tx, roomId, id); err != nil {
					return apperr.Validation(apperr.Field("default_director_user_id", "not_member", "워크스페이스 멤버가 아닙니다"))
				}
				add("default_director_user_id = $%d", id)
			}
			changed = append(changed, "default_director_user_id")
		}
		if l := in.Limits; l != nil {
			patch := map[string]any{}
			var unset []string
			if l.MaxConcurrentWorks != nil {
				patch["max_concurrent_works"] = *l.MaxConcurrentWorks
			}
			if l.MaxParallelLanes != nil {
				patch["max_parallel_lanes"] = *l.MaxParallelLanes
			}
			if l.BudgetUsd.IsSpecified() {
				if l.BudgetUsd.IsNull() {
					unset = append(unset, "budget_usd")
				} else {
					patch["budget_usd"] = l.BudgetUsd.MustGet()
				}
			}
			if l.TimeLimit.IsSpecified() {
				if l.TimeLimit.IsNull() {
					unset = append(unset, "time_limit")
				} else {
					patch["time_limit"] = l.TimeLimit.MustGet()
				}
			}
			args = append(args, patch, unset)
			sets = append(sets, fmt.Sprintf("limits = (limits || $%d::jsonb) - $%d::text[]", len(args)-1, len(args)))
			changed = append(changed, "limits")
		}
		visChanged := in.Visibility != nil && string(*in.Visibility) != visibility
		if visChanged {
			add("visibility = $%d", string(*in.Visibility))
			changed = append(changed, "visibility")
		}
		if len(sets) == 0 {
			return nil
		}
		add("updated_at = $%d", now)
		if _, err := tx.Exec(r.Context(), `UPDATE room SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...); err != nil {
			return err
		}
		rid := roomId
		who := displayName(r.Context(), tx, u.Id)
		if visChanged {
			line := who + " 님이 이 방을 초대된 사람만 볼 수 있게 바꿨습니다."
			if *in.Visibility == gen.RoomVisibilityWorkspace {
				line = who + " 님이 이 방을 워크스페이스 멤버 모두가 볼 수 있게 바꿨습니다."
			}
			if _, err := s.Router.SystemPost(r.Context(), tx, roomId, line); err != nil {
				return err
			}
			if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "room.visibility_changed", "room", &rid,
				map[string]any{"name": a.Name, "from": visibility, "to": string(*in.Visibility)}, now); err != nil {
				return err
			}
		}
		if settings := without(changed, "visibility"); len(settings) > 0 {
			if _, err := s.Router.SystemPost(r.Context(), tx, roomId, who+" 님이 방 설정을 바꿨습니다."); err != nil {
				return err
			}
			if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "room.settings_changed", "room", &rid,
				map[string]any{"name": a.Name, "fields": settings}, now); err != nil {
				return err
			}
		}
		s.publishRoom(r.Context(), tx, a.WorkspaceID, roomId)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.roomOut(r.Context(), w, http.StatusOK, roomId, u.Id)
}

func without(xs []string, drop string) []string {
	out := []string{}
	for _, x := range xs {
		if x != drop {
			out = append(out, x)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// delete · archive · unarchive
// ---------------------------------------------------------------------------

// DeleteRoom is FR-2.6: the owner or a workspace owner·admin, never while a
// mission is in progress (409 works_active) or while a worktree holds work
// nobody merged (409 workdir_unmerged). Everything goes except one
// activity_log line.
func (s *Server) DeleteRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	u, _, p := s.roomGate(r, roomId, rooms.ActDelete)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if err := s.Sessions.DeleteRoom(r.Context(), roomId, u.Id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ArchiveRoom closes new activity and keeps the past (FR-2.4 [V19-C]). A room
// with work still in flight refuses (409 tasks_active) — archiving under a
// running turn would leave its answer nowhere to go.
func (s *Server) ArchiveRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	s.setArchived(w, r, roomId, true)
}

// UnarchiveRoom restores the room with its participants as they were.
func (s *Server) UnarchiveRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	s.setArchived(w, r, roomId, false)
}

func (s *Server) setArchived(w http.ResponseWriter, r *http.Request, roomID uuid.UUID, archive bool) {
	u, a, p := s.roomGate(r, roomID, rooms.ActArchive)
	if p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(r.Context(), `SELECT status::text FROM room WHERE id = $1 FOR UPDATE`, roomID).Scan(&status); err != nil {
			return err
		}
		want := "active"
		if archive {
			want = "archived"
		}
		if status == want {
			return nil // already there — the button's second press is not an error
		}
		if archive {
			var n int
			if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM task WHERE session_id = $1 AND status IN `+activeTaskStatusesSQL, roomID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				pr := apperr.Conflict("tasks_active", "진행 중인 할 일이 있어 보관할 수 없습니다 — 끝나거나 중단된 뒤 보관해 주세요")
				pr.Extra = map[string]any{"tasks_active": n}
				return pr
			}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE room SET status = $2, updated_at = $3 WHERE id = $1`, roomID, want, now); err != nil {
			return err
		}
		who := displayName(r.Context(), tx, u.Id)
		line, action := who+" 님이 이 방을 보관했습니다.", "room.archived"
		if !archive {
			line, action = who+" 님이 이 방의 보관을 해제했습니다.", "room.unarchived"
		}
		if _, err := s.Router.SystemPost(r.Context(), tx, roomID, line); err != nil {
			return err
		}
		rid := roomID
		if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, action, "room", &rid, map[string]any{"name": a.Name}, now); err != nil {
			return err
		}
		s.publishRoom(r.Context(), tx, a.WorkspaceID, roomID)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.roomOut(r.Context(), w, http.StatusOK, roomID, u.Id)
}

// activeTaskStatusesSQL is "a task still in flight" (FR-7.1's non-terminal
// states) — the archive refusal.
const activeTaskStatusesSQL = `('deferred', 'queued', 'dispatched', 'preparing', 'running', 'waiting_human', 'paused')`

// ---------------------------------------------------------------------------
// unread
// ---------------------------------------------------------------------------

// MarkRoomRead moves the caller's marker FORWARD only (FR-8 · §12.1-6): a tab
// that loaded an older page must not resurrect messages another tab already
// cleared. `room.unread` goes to the caller alone — their other tabs and
// devices.
func (s *Server) MarkRoomRead(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	u, a, p := s.roomGate(r, roomId, rooms.ActMarkRead)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.MarkRoomReadJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	var unread int
	var marker uuid.UUID
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var inRoom bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM message WHERE id = $1 AND session_id = $2)`, in.LastReadMessageId, roomId).Scan(&inRoom); err != nil {
			return err
		}
		if !inRoom {
			return apperr.Validation(apperr.Field("last_read_message_id", "not_in_room", "이 방의 메시지가 아닙니다"))
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE room_participant p SET last_read_message_id = $3
			FROM message nm
			WHERE p.id = $1 AND p.room_id = $2 AND nm.id = $3
			  AND (p.last_read_message_id IS NULL OR NOT EXISTS (
			        SELECT 1 FROM message cur WHERE cur.id = p.last_read_message_id
			          AND (cur.created_at > nm.created_at OR (cur.created_at = nm.created_at AND cur.id >= nm.id))))`,
			a.ParticipantID, roomId, in.LastReadMessageId); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `SELECT COALESCE(last_read_message_id, $2) FROM room_participant WHERE id = $1`, a.ParticipantID, in.LastReadMessageId).Scan(&marker); err != nil {
			return err
		}
		n, err := rooms.UnreadCount(r.Context(), tx, roomId, u.Id)
		if err != nil {
			return err
		}
		unread = n
		if s.Hub != nil {
			rid, uid := roomId, u.Id
			return s.Hub.PublishTo(r.Context(), tx, a.WorkspaceID, &rid, &uid, "room.unread",
				map[string]any{"room_id": roomId, "unread_count": n, "last_read_message_id": marker})
		}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room_id": roomId, "unread_count": unread})
}

// ---------------------------------------------------------------------------
// 「여기까지 정리」
// ---------------------------------------------------------------------------

// SummarizeRoom is FR-2.5 [V19-C]: the person picks the range, and the range
// is recorded with the summary (message.summary_range) so the summary can be
// traced back to the messages it read, and the same range reads the same
// input. The body is written by the same path the mission's completion summary
// uses (sessions.SummarizeRange — the platform LLM when configured, rows
// otherwise).
func (s *Server) SummarizeRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.SummarizeRoomParams) {
	u, a, p := s.roomGate(r, roomId, rooms.ActSummarize)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.RoomSummarize
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		byIDs := in.FromMessageId != nil || in.ToMessageId != nil
		if in.Since != nil && byIDs {
			return 0, nil, apperr.Validation(apperr.Field("since", "exclusive", "기간과 메시지 범위는 함께 고를 수 없습니다 — 하나만 골라 주세요"))
		}
		if in.Since == nil && !byIDs {
			return 0, nil, apperr.Validation(apperr.Field("since", "required", "정리할 범위를 골라 주세요"))
		}
		var msg *gen.Message
		err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
			rng := sessions.SummaryRange{Since: in.Since}
			if in.FromMessageId != nil {
				id := uuid.UUID(*in.FromMessageId)
				rng.From = &id
			}
			if in.ToMessageId != nil {
				id := uuid.UUID(*in.ToMessageId)
				rng.To = &id
			}
			m, err := s.Sessions.SummarizeRange(r.Context(), tx, a.WorkspaceID, roomId, u.Id, rng)
			if err != nil {
				return err
			}
			msg = m
			return nil
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusAccepted, msg, nil
	})
}

// ---------------------------------------------------------------------------
// owner · deputy
// ---------------------------------------------------------------------------

// TransferRoomOwner hands the room to another current participant (a person).
// The old owner stays as a member; a new owner who was the deputy stops being
// the deputy — one person cannot hold both seats.
func (s *Server) TransferRoomOwner(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	u, a, p := s.roomGate(r, roomId, rooms.ActTransferOwner)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.TransferRoomOwnerJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	target := uuid.UUID(in.UserId)
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var owner uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT owner_user_id FROM room WHERE id = $1 FOR UPDATE`, roomId).Scan(&owner); err != nil {
			return err
		}
		if owner == target {
			return nil
		}
		if err := requireLiveHuman(r.Context(), tx, roomId, target, "user_id"); err != nil {
			return err
		}
		if err := rooms.SetOwner(r.Context(), tx, roomId, owner, target, now); err != nil {
			return err
		}
		line := displayName(r.Context(), tx, u.Id) + " 님이 방장을 " + displayName(r.Context(), tx, target) + " 님에게 넘겼습니다."
		if _, err := s.Router.SystemPost(r.Context(), tx, roomId, line); err != nil {
			return err
		}
		rid, tid := roomId, target
		if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "room.owner_transferred", "user", &tid,
			map[string]any{"name": a.Name, "from": owner, "to": target}, now); err != nil {
			return err
		}
		s.publishRoom(r.Context(), tx, a.WorkspaceID, roomId)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.roomOut(r.Context(), w, http.StatusOK, roomId, u.Id)
}

// SetRoomDeputy appoints the one deputy or clears the seat (user_id null).
func (s *Server) SetRoomDeputy(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	u, a, p := s.roomGate(r, roomId, rooms.ActSetDeputy)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.SetRoomDeputyJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	var target *uuid.UUID
	if in.UserId.IsSpecified() && !in.UserId.IsNull() {
		t := uuid.UUID(in.UserId.MustGet())
		target = &t
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var owner uuid.UUID
		var cur *uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT owner_user_id, deputy_owner_user_id FROM room WHERE id = $1 FOR UPDATE`, roomId).Scan(&owner, &cur); err != nil {
			return err
		}
		if (cur == nil && target == nil) || (cur != nil && target != nil && *cur == *target) {
			return nil
		}
		if target != nil {
			if *target == owner {
				return apperr.Validation(apperr.Field("user_id", "is_owner", "방장은 부방장을 겸할 수 없습니다"))
			}
			if err := requireLiveHuman(r.Context(), tx, roomId, *target, "user_id"); err != nil {
				return err
			}
		}
		if err := rooms.SetDeputy(r.Context(), tx, roomId, cur, target, now); err != nil {
			return err
		}
		who := displayName(r.Context(), tx, u.Id)
		line := who + " 님이 부방장 자리를 비웠습니다."
		if target != nil {
			line = who + " 님이 " + displayName(r.Context(), tx, *target) + " 님을 부방장으로 정했습니다."
		}
		if _, err := s.Router.SystemPost(r.Context(), tx, roomId, line); err != nil {
			return err
		}
		rid := roomId
		if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "room.deputy_changed", "room", &rid,
			map[string]any{"name": a.Name, "from": cur, "to": target}, now); err != nil {
			return err
		}
		s.publishRoom(r.Context(), tx, a.WorkspaceID, roomId)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.roomOut(r.Context(), w, http.StatusOK, roomId, u.Id)
}

// requireLiveHuman is the 422 not_participant of transferRoomOwner and
// setRoomDeputy: the seat goes to a person who is in the room now.
func requireLiveHuman(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID, field string) error {
	var ok bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM room_participant WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL)`, roomID, userID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return apperr.Validation(apperr.Field(field, "not_participant", "이 방의 참여자에게만 맡길 수 있습니다 — 먼저 방에 초대해 주세요"))
	}
	return nil
}

// onMemberLeft is auth.Service.OnLeave (removeMember's transaction): the
// person leaves every room of the workspace, and each room they owned passes
// to the oldest workspace owner — with the timeline line SCREEN §4.6 system
// message 4 shows and a `room.owner_succeeded` activity entry (§12.1-4).
func (s *Server) onMemberLeft(ctx context.Context, tx pgx.Tx, wsID, userID uuid.UUID, now time.Time) error {
	moved, err := rooms.LeaveWorkspace(ctx, tx, wsID, userID, now)
	if err != nil {
		return err
	}
	for _, m := range moved {
		line := displayName(ctx, tx, m.From) + " 님이 워크스페이스를 떠나 " + displayName(ctx, tx, m.To) + " 님이 방장을 이어받았습니다."
		if _, err := s.Router.SystemPost(ctx, tx, m.RoomID, line); err != nil {
			return err
		}
		rid, to := m.RoomID, m.To
		if err := logActivity(ctx, tx, wsID, &rid, nil, "room.owner_succeeded", "user", &to,
			map[string]any{"name": m.RoomName, "from": m.From, "to": m.To}, now); err != nil {
			return err
		}
		s.publishRoom(ctx, tx, wsID, m.RoomID)
	}
	return nil
}
