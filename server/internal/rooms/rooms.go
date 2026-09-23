package rooms

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/agents"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// ---------------------------------------------------------------------------
// Who is asking — one read per request
// ---------------------------------------------------------------------------

// Access is the caller's standing in one room: Decide's input plus the ids a
// handler needs afterwards.
type Access struct {
	Standing
	RoomID      uuid.UUID
	WorkspaceID uuid.UUID
	UserID      uuid.UUID
	// ParticipantID is the caller's live room_participant row (uuid.Nil if none).
	ParticipantID uuid.UUID
	OwnerUserID   uuid.UUID
	Name          string
}

// LoadAccess reads the room and the caller's two roles in one statement. A
// missing room is 404 exactly like an invisible one (Deny) — the two must
// not be distinguishable from outside.
func LoadAccess(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID) (*Access, error) {
	a := &Access{RoomID: roomID, UserID: userID}
	var wsRole, roomRole *string
	var pid *uuid.UUID
	var status string
	err := q.QueryRow(ctx, `
		SELECT r.workspace_id, r.visibility::text, r.status::text, r.owner_user_id, r.name,
		       m.role::text, p.role::text, p.id
		FROM room r
		LEFT JOIN member m ON m.workspace_id = r.workspace_id AND m.user_id = $2
		LEFT JOIN room_participant p ON p.room_id = r.id AND p.user_id = $2 AND p.left_at IS NULL
		WHERE r.id = $1`, roomID, userID).
		Scan(&a.WorkspaceID, &a.Visibility, &status, &a.OwnerUserID, &a.Name, &wsRole, &roomRole, &pid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("room")
	}
	if err != nil {
		return nil, fmt.Errorf("rooms: access: %w", err)
	}
	if wsRole != nil {
		a.WorkspaceRole = *wsRole
	}
	if roomRole != nil {
		a.RoomRole = *roomRole
	}
	if pid != nil {
		a.ParticipantID = *pid
	}
	a.Archived = status == "archived"
	return a, nil
}

// Require is LoadAccess + Decide: the room's Access when the caller may take
// the action, else the Problem Deny chooses.
func Require(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID, act Action) (*Access, error) {
	a, err := LoadAccess(ctx, q, roomID, userID)
	if err != nil {
		return nil, err
	}
	if !Decide(act, a.Standing) {
		return nil, Deny(act, a.Standing)
	}
	return a, nil
}

// AuditViewed reports whether this read is a workspace owner·admin looking
// into an invited room they are not in — the one kind of reading FR-5.3 asks
// to leave a trace of (`room.audit_viewed`).
func (a *Access) AuditViewed() bool {
	return a.RoomRole == "" && a.Visibility == VisInvited && a.wsAdmin()
}

// ---------------------------------------------------------------------------
// Room
// ---------------------------------------------------------------------------

// Contract defaults for RoomLimits (openapi RoomLimits · 0025's column
// default). A row written before a key existed reads as the default, not 0.
const (
	DefaultMaxConcurrentWorks = 3
	DefaultMaxParallelLanes   = 5
)

// ParseLimits decodes room.limits and fills the two defaulted keys.
func ParseLimits(raw []byte) gen.RoomLimits {
	var l gen.RoomLimits
	_ = json.Unmarshal(raw, &l)
	if l.MaxConcurrentWorks == nil {
		v := DefaultMaxConcurrentWorks
		l.MaxConcurrentWorks = &v
	}
	if l.MaxParallelLanes == nil {
		v := DefaultMaxParallelLanes
		l.MaxParallelLanes = &v
	}
	return l
}

// Statuses that count as "in progress" for the room's 409s and counts.
const (
	openWorkStatuses   = `('active', 'paused', 'completing')`
	liveLaneStatuses   = `('queued', 'running', 'waiting_human', 'blocked', 'paused')`
	activeTaskStatuses = `('deferred', 'queued', 'dispatched', 'preparing', 'running', 'waiting_human', 'paused')`
)

// Load is getRoom for one viewer. The counts, the room's own cost and the
// viewer's unread marker are read here, not stored.
//
// 1:N — every aggregate is keyed by the ROOM (session_id on the child rows),
// never by joining work, so a room with three missions counts each lane once.
func Load(ctx context.Context, q db.DBTX, a *Access, now time.Time) (*gen.Room, error) {
	var out gen.Room
	var (
		deputy, runtimeID, defDirector    *uuid.UUID
		isolation, limits, blockedDetail  []byte
		visibility, status, autonomy      string
		blockedReason                     *string
		cost                              float64
		estimated                         bool
		worksActive, lanesActive, tActive int
		lastActivity                      *time.Time
	)
	err := q.QueryRow(ctx, `
		SELECT r.id, r.workspace_id, r.name, r.description, r.status::text, r.visibility::text, r.owner_user_id,
		       r.deputy_owner_user_id, r.runtime_id, r.isolation, r.limits, r.autonomy::text, r.default_director_user_id,
		       r.blocked_reason::text, r.blocked_detail, r.created_by, r.created_at, r.updated_at,
		       (SELECT count(*) FROM work w WHERE w.room_id = r.id AND w.status IN `+openWorkStatuses+`),
		       (SELECT count(*) FROM lane l WHERE l.session_id = r.id AND l.status IN `+liveLaneStatuses+`),
		       (SELECT count(*) FROM task t WHERE t.session_id = r.id AND t.status IN `+activeTaskStatuses+`),
		       (SELECT COALESCE(sum(u.cost_usd), 0)::float8 FROM task_usage u JOIN task t ON t.id = u.task_id WHERE t.session_id = r.id),
		       (SELECT COALESCE(bool_or(u.estimated), false) FROM task_usage u JOIN task t ON t.id = u.task_id WHERE t.session_id = r.id),
		       (SELECT max(created_at) FROM message m WHERE m.session_id = r.id)
		FROM room r WHERE r.id = $1`, a.RoomID).Scan(
		&out.Id, &out.WorkspaceId, &out.Name, &out.Description, &status, &visibility, &out.OwnerUserId,
		&deputy, &runtimeID, &isolation, &limits, &autonomy, &defDirector,
		&blockedReason, &blockedDetail, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt,
		&worksActive, &lanesActive, &tActive, &cost, &estimated, &lastActivity)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("room")
	}
	if err != nil {
		return nil, fmt.Errorf("rooms: load: %w", err)
	}
	out.Status = gen.RoomStatus(status)
	out.Visibility = gen.RoomVisibility(visibility)
	out.DeputyOwnerUserId = tasks.NullUUID(deputy)
	out.RuntimeId = tasks.NullUUID(runtimeID)
	out.DefaultDirectorUserId = tasks.NullUUID(defDirector)
	_ = json.Unmarshal(isolation, &out.Isolation)
	out.Limits = ParseLimits(limits)
	out.Autonomy = gen.AutonomyLevel(autonomy)
	out.BlockedReason = nullable.NewNullNullable[gen.RoomBlockedReason]()
	if blockedReason != nil {
		out.BlockedReason = nullable.NewNullableWithValue(gen.RoomBlockedReason(*blockedReason))
	}
	out.BlockedDetail = nullable.NewNullNullable[gen.BlockedDetail]()
	if len(blockedDetail) > 0 {
		var d gen.BlockedDetail
		if json.Unmarshal(blockedDetail, &d) == nil {
			if blockedReason != nil {
				if err := fillApprover(ctx, q, a.RoomID, &d, now); err != nil {
					return nil, err
				}
			}
			out.BlockedDetail = nullable.NewNullableWithValue(d)
		}
	}
	out.Counts = &struct {
		LanesActive *int `json:"lanes_active,omitempty"`
		TasksActive *int `json:"tasks_active,omitempty"`
		WorksActive *int `json:"works_active,omitempty"`
	}{LanesActive: &lanesActive, TasksActive: &tActive, WorksActive: &worksActive}
	c := float32(cost)
	out.CostUsd = &c
	out.CostEstimated = &estimated
	out.LastActivityAt = tasks.NullTime(lastActivity)
	if runtimeID != nil {
		if rt, err := runtimes.Load(ctx, q, *runtimeID); err == nil {
			out.Runtime = &rt.Runtime
		}
	}
	out.MyRoomRole = nullable.NewNullNullable[gen.RoomRole]()
	if a.RoomRole != "" {
		out.MyRoomRole = nullable.NewNullableWithValue(gen.RoomRole(a.RoomRole))
		n, err := UnreadCount(ctx, q, a.RoomID, a.UserID)
		if err != nil {
			return nil, err
		}
		out.UnreadCount = n
	}
	capsFacts := a.Standing
	capsFacts.Archived = out.Status == gen.RoomStatusArchived
	caps := make([]gen.RoomMyCapabilities, 0)
	for _, c := range Capabilities(capsFacts) {
		caps = append(caps, gen.RoomMyCapabilities(c))
	}
	out.MyCapabilities = &caps
	return &out, nil
}

// fillApprover is the banner's "지금 답할 수 있는 사람" for a stop an approval
// lifts (T-R1b1): the owner, and from half the open request's deadline the
// deputy or the oldest workspace owner (FR-2A.3). Computed from the open
// request at read time, never stored. A manual stop has no request — the
// people who may lift it are the stewards, and the banner names who stopped
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
	ap, err := roomgate.LoadApprovers(ctx, q, roomID)
	if err != nil {
		return err
	}
	who, next := ap.Now(created, due, now)
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

// unreadPredicate is "a message after my marker that I did not write" for the
// row alias m against the participant alias p and the marker message lr. The
// list and getRoom and markRoomRead all count with it, so the badge the list
// shows is the number markRoomRead then clears.
const unreadPredicate = `m.session_id = p.room_id
	AND m.author_id IS DISTINCT FROM p.user_id
	AND (lr.id IS NULL OR m.created_at > lr.created_at OR (m.created_at = lr.created_at AND m.id > lr.id))`

// UnreadCount is FR-8 · §12.1-6: messages in the room after the person's
// `last_read_message_id` (a room-level marker — missions and sub-missions do
// not have their own badge).
func UnreadCount(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		SELECT count(m.id)
		FROM room_participant p
		LEFT JOIN message lr ON lr.id = p.last_read_message_id
		JOIN message m ON `+unreadPredicate+`
		WHERE p.room_id = $1 AND p.user_id = $2 AND p.left_at IS NULL`, roomID, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("rooms: unread: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// listRooms — one statement
// ---------------------------------------------------------------------------

// RoomListOptions are listRooms' query parameters.
type RoomListOptions struct {
	Query           string
	UnreadOnly      bool
	Participating   bool
	IncludeArchived bool
	Cursor          string
	Limit           int
}

// List is listRooms (S5 · S25). ONE statement — unread counts, attention and
// the roster ride along as lateral subqueries rather than a query per card,
// which is what the session list did (sessions.listItem ran Load per id). The
// order is last activity only (§12.1-11); `unread` never reorders the list.
//
// Invisible rooms are filtered in SQL, not after: a page of 50 that came back
// with 12 after a Go-side filter would page wrongly and leak the count.
func List(ctx context.Context, q db.DBTX, wsID, userID uuid.UUID, wsRole string, o RoomListOptions) ([]gen.RoomListItem, *string, error) {
	if o.Limit <= 0 || o.Limit > 200 {
		o.Limit = 50
	}
	args := []any{wsID, userID}
	where := []string{}
	admin := wsRole == "owner" || wsRole == "admin"
	if !admin {
		where = append(where, "(x.visibility = 'workspace' OR x.my_role IS NOT NULL)")
	}
	if o.Participating {
		where = append(where, "x.my_role IS NOT NULL")
	}
	if !o.IncludeArchived {
		where = append(where, "x.status = 'active'")
	}
	if s := strings.TrimSpace(o.Query); s != "" {
		args = append(args, "%"+escapeLike(s)+"%")
		where = append(where, fmt.Sprintf("(x.name ILIKE $%d OR x.description ILIKE $%d)", len(args), len(args)))
	}
	if o.UnreadOnly {
		where = append(where, "x.unread > 0")
	}
	if o.Cursor != "" {
		at, id, ok := decodeRoomCursor(o.Cursor)
		if !ok {
			return nil, nil, apperr.Validation(apperr.Field("cursor", "invalid", "목록 위치 표시가 올바르지 않습니다 — 처음부터 다시 불러와 주세요"))
		}
		args = append(args, at, id)
		where = append(where, fmt.Sprintf("(x.sort_at, x.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, o.Limit+1)
	rows, err := q.Query(ctx, `
		SELECT x.id, x.name, x.description, x.status, x.blocked_reason, x.my_role, x.unread, x.active_works,
		       x.hitl_open, x.blocked, x.failed, x.last_activity, x.sort_at, x.participants
		FROM (
		  SELECT r.id, r.name, r.description, r.status::text AS status, r.visibility::text AS visibility,
		         r.blocked_reason::text AS blocked_reason, p.role::text AS my_role,
		         COALESCE(ur.n, 0) AS unread, aw.n AS active_works,
		         att.hitl_open, att.blocked, att.failed,
		         la.at AS last_activity, COALESCE(la.at, r.created_at) AS sort_at,
		         ro.list AS participants
		  FROM room r
		  LEFT JOIN room_participant p ON p.room_id = r.id AND p.user_id = $2 AND p.left_at IS NULL
		  LEFT JOIN message lr ON lr.id = p.last_read_message_id
		  LEFT JOIN LATERAL (SELECT count(*) AS n FROM message m WHERE p.id IS NOT NULL AND `+unreadPredicate+`) ur ON true
		  LEFT JOIN LATERAL (SELECT count(*) AS n FROM work w WHERE w.room_id = r.id AND w.status IN `+openWorkStatuses+`) aw ON true
		  LEFT JOIN LATERAL (SELECT max(m.created_at) AS at FROM message m WHERE m.session_id = r.id) la ON true
		  -- "내가 답할 것만": a request whose approver I am, and blocked/failed
		  -- lanes of the open missions I direct (or deputise), plus the room's
		  -- mission-less lanes and room-level requests when I own the room.
		  -- The mission of a row is its work_id (T-R1b2 writes it everywhere;
		  -- a NULL is outside any mission — "any mission of the room" would
		  -- count one room's request once per Director).
		  LEFT JOIN LATERAL (
		    SELECT
		      (SELECT count(*) FROM hitl_request h
		         WHERE h.session_id = r.id AND h.status = 'open'
		           AND (h.approver_spec = ($2::uuid)::text
		                OR (h.approver_spec = 'room_owner' AND r.owner_user_id = $2)
		                OR (h.approver_spec = 'director' AND $2 IN (
		                      SELECT w.director_user_id FROM work w WHERE w.id = COALESCE(h.work_id, r.legacy_work_id)
		                      UNION SELECT w.deputy_user_id FROM work w WHERE w.id = COALESCE(h.work_id, r.legacy_work_id))))) AS hitl_open,
		      (SELECT count(*) FROM lane l WHERE l.session_id = r.id AND l.status = 'blocked' AND `+laneMineSQL+`) AS blocked,
		      (SELECT count(*) FROM lane l WHERE l.session_id = r.id AND l.status = 'failed' AND `+laneMineSQL+`) AS failed
		  ) att ON true
		  LEFT JOIN LATERAL (
		    SELECT COALESCE(json_agg(json_build_object(
		             'kind', CASE WHEN rp.agent_id IS NULL THEN 'user' ELSE 'agent' END,
		             'id', COALESCE(rp.agent_id, rp.user_id),
		             'name', COALESCE(ag.name, u.display_name, u.email),
		             'avatar_url', COALESCE(ag.avatar_url, u.avatar_url))
		           ORDER BY rp.joined_at, rp.id), '[]'::json) AS list
		    FROM room_participant rp
		    LEFT JOIN agent ag ON ag.id = rp.agent_id
		    LEFT JOIN app_user u ON u.id = rp.user_id
		    WHERE rp.room_id = r.id AND rp.left_at IS NULL
		  ) ro ON true
		  WHERE r.workspace_id = $1
		) x `+cond+fmt.Sprintf(`
		ORDER BY x.sort_at DESC, x.id DESC
		LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("rooms: list: %w", err)
	}
	defer rows.Close()
	out := []gen.RoomListItem{}
	var sortKeys []time.Time
	for rows.Next() {
		var lastSort time.Time
		var it gen.RoomListItem
		var status string
		var blocked, myRole *string
		var last *time.Time
		var parts []byte
		if err := rows.Scan(&it.Id, &it.Name, &it.Description, &status, &blocked, &myRole, &it.UnreadCount, &it.ActiveWorkCount,
			&it.Attention.HitlOpen, &it.Attention.Blocked, &it.Attention.Failed, &last, &lastSort, &parts); err != nil {
			return nil, nil, err
		}
		it.Status = gen.RoomStatus(status)
		it.BlockedReason = nullable.NewNullNullable[gen.RoomBlockedReason]()
		if blocked != nil {
			it.BlockedReason = nullable.NewNullableWithValue(gen.RoomBlockedReason(*blocked))
		}
		it.MyRoomRole = nullable.NewNullNullable[gen.RoomRole]()
		if myRole != nil {
			it.MyRoomRole = nullable.NewNullableWithValue(gen.RoomRole(*myRole))
		}
		it.LastActivityAt = tasks.NullTime(last)
		it.Participants = []gen.RoomParticipantRef{}
		if err := json.Unmarshal(parts, &it.Participants); err != nil {
			return nil, nil, fmt.Errorf("rooms: list roster: %w", err)
		}
		out = append(out, it)
		sortKeys = append(sortKeys, lastSort)
		if len(out) == o.Limit+1 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(out) > o.Limit {
		out = out[:o.Limit]
		// The cursor is the LAST RETURNED row's sort key — not the extra row's.
		c := encodeRoomCursor(sortKeys[o.Limit-1], out[o.Limit-1].Id)
		next = &c
	}
	return out, next, nil
}

// laneMineSQL is "this lane's mission is one I direct or deputise, and the
// mission is open — or it has no mission and I own the room".
const laneMineSQL = `(
	EXISTS (SELECT 1 FROM work w WHERE w.id = l.work_id
	          AND w.status IN ` + openWorkStatuses + ` AND $2 IN (w.director_user_id, w.deputy_user_id))
	OR (l.work_id IS NULL AND r.owner_user_id = $2))`

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func encodeRoomCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeRoomCursor(c string) (time.Time, uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	at, idS, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(idS)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return t, id, true
}

// ---------------------------------------------------------------------------
// Roster
// ---------------------------------------------------------------------------

// ParticipantRow is one room_participant row as the handlers reason about it.
type ParticipantRow struct {
	ID        uuid.UUID
	RoomID    uuid.UUID
	AgentID   *uuid.UUID
	ProfileID *uuid.UUID
	UserID    *uuid.UUID
	Role      string
	JoinedAt  time.Time
	LeftAt    *time.Time
}

const participantCols = `id, room_id, agent_id, profile_id, user_id, role::text, joined_at, left_at`

func scanParticipant(row pgx.Row) (*ParticipantRow, error) {
	var p ParticipantRow
	if err := row.Scan(&p.ID, &p.RoomID, &p.AgentID, &p.ProfileID, &p.UserID, &p.Role, &p.JoinedAt, &p.LeftAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetParticipant reads one row of the room (404 participant if it is not in
// this room — a participant id of another room must not be reachable through
// this one's path).
func GetParticipant(ctx context.Context, q db.DBTX, roomID, id uuid.UUID, forUpdate bool) (*ParticipantRow, error) {
	sql := `SELECT ` + participantCols + ` FROM room_participant WHERE room_id = $1 AND id = $2`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	p, err := scanParticipant(q.QueryRow(ctx, sql, roomID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("participant")
	}
	return p, err
}

// ParticipantAPI renders one row (RoomParticipant). viewer is the caller —
// the embedded Agent's `invitable` is answered for them.
func ParticipantAPI(ctx context.Context, q db.DBTX, p *ParticipantRow, viewer uuid.UUID) (gen.RoomParticipant, error) {
	out := gen.RoomParticipant{
		Id: p.ID, RoomId: p.RoomID, RoomRole: gen.RoomRole(p.Role), JoinedAt: p.JoinedAt, LeftAt: tasks.NullTime(p.LeftAt),
	}
	switch {
	case p.AgentID != nil:
		out.Kind = gen.ParticipantKindAgent
		a, err := agents.Load(ctx, q, *p.AgentID, &viewer)
		if err != nil {
			return out, err
		}
		out.Agent = a
		st := a.Status
		out.Status = &st
		if p.ProfileID != nil {
			for i := range a.Profiles {
				if a.Profiles[i].Id == *p.ProfileID {
					pr := a.Profiles[i]
					out.Profile = &pr
				}
			}
		}
	case p.UserID != nil:
		out.Kind = gen.ParticipantKindUser
		u, err := auth.LoadUser(ctx, q, *p.UserID)
		if err != nil {
			return out, err
		}
		out.User = u
	}
	return out, nil
}

// ListParticipants is listRoomParticipants: people and agents in one list,
// in join order.
func ListParticipants(ctx context.Context, q db.DBTX, roomID, viewer uuid.UUID, includeLeft bool) ([]gen.RoomParticipant, error) {
	sql := `SELECT ` + participantCols + ` FROM room_participant WHERE room_id = $1`
	if !includeLeft {
		sql += ` AND left_at IS NULL`
	}
	rows, err := q.Query(ctx, sql+` ORDER BY joined_at, id`, roomID)
	if err != nil {
		return nil, err
	}
	var list []*ParticipantRow
	for rows.Next() {
		p, err := scanParticipant(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]gen.RoomParticipant, 0, len(list))
	for _, p := range list {
		api, err := ParticipantAPI(ctx, q, p, viewer)
		if err != nil {
			return nil, err
		}
		out = append(out, api)
	}
	return out, nil
}

// LeftPayload is the `participant.left` frame (StreamEvent table).
func LeftPayload(p *ParticipantRow, at time.Time) map[string]any {
	kind := "user"
	if p.AgentID != nil {
		kind = "agent"
	}
	return map[string]any{"room_id": p.RoomID, "participant_id": p.ID, "kind": kind, "left_at": at}
}

// ---------------------------------------------------------------------------
// Reference links
// ---------------------------------------------------------------------------

// LinkAPI renders one room_link row (RoomLink).
func LinkAPI(ctx context.Context, q db.DBTX, id uuid.UUID) (*gen.RoomLink, error) {
	var out gen.RoomLink
	var createdBy uuid.UUID
	var desc string
	var last *time.Time
	err := q.QueryRow(ctx, `
		SELECT l.id, l.room_id, l.target_room_id, t.name, t.description,
		       (SELECT max(created_at) FROM message m WHERE m.session_id = t.id), l.created_by, l.created_at
		FROM room_link l JOIN room t ON t.id = l.target_room_id WHERE l.id = $1`, id).
		Scan(&out.Id, &out.RoomId, &out.TargetRoom.Id, &out.TargetRoom.Name, &desc, &last, &createdBy, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("room_link")
	}
	if err != nil {
		return nil, err
	}
	out.TargetRoom.Description = &desc
	out.TargetRoom.LastActivityAt = tasks.NullTime(last)
	u, err := auth.LoadUser(ctx, q, createdBy)
	if err != nil {
		return nil, err
	}
	out.CreatedBy = *u
	return &out, nil
}

// ListLinks is listRoomLinks: the links this room holds, newest first.
func ListLinks(ctx context.Context, q db.DBTX, roomID uuid.UUID) ([]gen.RoomLink, error) {
	rows, err := q.Query(ctx, `SELECT id FROM room_link WHERE room_id = $1 ORDER BY created_at DESC, id`, roomID)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]gen.RoomLink, 0, len(ids))
	for _, id := range ids {
		l, err := LinkAPI(ctx, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Agent.room_count · hidden_room_count
// ---------------------------------------------------------------------------

// FillAgentRoomCounts sets Agent.room_count (rooms the agent is in that the
// viewer can see) and hidden_room_count (the rest — a number, never names)
// for a page of agents in one statement. wsRole is the viewer's member.role:
// an owner·admin sees every room (audit).
func FillAgentRoomCounts(ctx context.Context, q db.DBTX, list []gen.Agent, viewer uuid.UUID, wsRole string) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(list))
	for i, a := range list {
		ids[i] = a.Id
	}
	admin := wsRole == "owner" || wsRole == "admin"
	rows, err := q.Query(ctx, `
		SELECT p.agent_id,
		       count(*) FILTER (WHERE $3 OR r.visibility = 'workspace' OR me.id IS NOT NULL),
		       count(*) FILTER (WHERE NOT ($3 OR r.visibility = 'workspace' OR me.id IS NOT NULL))
		FROM room_participant p
		JOIN room r ON r.id = p.room_id
		LEFT JOIN room_participant me ON me.room_id = r.id AND me.user_id = $2 AND me.left_at IS NULL
		WHERE p.agent_id = ANY($1) AND p.left_at IS NULL
		GROUP BY p.agent_id`, ids, viewer, admin)
	if err != nil {
		return fmt.Errorf("rooms: agent room counts: %w", err)
	}
	defer rows.Close()
	counts := map[uuid.UUID][2]int{}
	for rows.Next() {
		var id uuid.UUID
		var seen, hidden int
		if err := rows.Scan(&id, &seen, &hidden); err != nil {
			return err
		}
		counts[id] = [2]int{seen, hidden}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range list {
		c := counts[list[i].Id]
		seen, hidden := c[0], c[1]
		list[i].RoomCount = &seen
		list[i].HiddenRoomCount = &hidden
	}
	return nil
}
