package rooms

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// entriesSQL is room_read_log seen from ONE room ($1): what it read (out),
// what read it (in), and its agents' refused attempts (denied). The other
// room of a denied row is kept only for originator_left — the one reason
// allowed to name it (contracts RoomRead: `other_room` null in `denied`).
//
// A room never reads itself (ErrSelf), so an id appears in at most one
// branch and (created_at, id) is a total order for the cursor.
const entriesSQL = `
	WITH x AS (
		SELECT l.*, 'out'::text AS dir, l.target_room_id AS other
		FROM room_read_log l WHERE l.room_id = $1 AND l.allowed
		UNION ALL
		SELECT l.*, 'in'::text, l.room_id
		FROM room_read_log l WHERE l.target_room_id = $1 AND l.allowed
		UNION ALL
		SELECT l.*, 'denied'::text, CASE WHEN l.denied_reason = 'originator_left' THEN l.target_room_id END
		FROM room_read_log l WHERE l.room_id = $1 AND NOT l.allowed
	)
	SELECT x.id, x.created_at, x.dir, x.reader_agent_id, a.name,
	       u.id, u.display_name, u.email, u.avatar_url, u.created_at,
	       o.id, o.name, x.summary_bytes, x.recent_n, x.truncated, x.reader_task_id, x.denied_reason::text
	FROM x
	JOIN agent a ON a.id = x.reader_agent_id
	LEFT JOIN app_user u ON u.id = x.originator_user_id
	LEFT JOIN room o ON o.id = x.other`

type otherRoom = struct {
	Id   openapi_types.UUID `json:"id"`
	Name string             `json:"name"`
}

func scanEntry(row pgx.Row) (gen.RoomRead, error) {
	var e gen.RoomRead
	var dir string
	var uID, oID *uuid.UUID
	var uName, uEmail, uAvatar, oName, reason *string
	var uCreated *time.Time
	var summaryBytes, recentN int
	var taskID *uuid.UUID
	if err := row.Scan(&e.Id, &e.At, &dir, &e.Agent.Id, &e.Agent.Name,
		&uID, &uName, &uEmail, &uAvatar, &uCreated,
		&oID, &oName, &summaryBytes, &recentN, &e.Truncated, &taskID, &reason); err != nil {
		return e, err
	}
	e.Direction = gen.RoomReadDirection(dir)
	e.OriginatorUser = nullable.NewNullNullable[gen.User]()
	if uID != nil {
		u := gen.User{Id: *uID, DisplayName: deref(uName), Email: openapi_types.Email(deref(uEmail)), CreatedAt: *uCreated,
			AvatarUrl: tasks.NullString(uAvatar)}
		e.OriginatorUser = nullable.NewNullableWithValue(u)
	}
	e.OtherRoom = nullable.NewNullNullable[otherRoom]()
	if oID != nil {
		e.OtherRoom = nullable.NewNullableWithValue(otherRoom{Id: *oID, Name: deref(oName)})
	}
	if dir != "denied" {
		hasSummary := summaryBytes > 0
		e.Scope.Summary, e.Scope.RecentN = &hasSummary, &recentN
	}
	e.TaskId = tasks.NullUUID(taskID)
	e.DeniedReason = nullable.NewNullNullable[gen.RoomReadDeniedReason]()
	if reason != nil {
		e.DeniedReason = nullable.NewNullableWithValue(gen.RoomReadDeniedReason(*reason))
	}
	return e, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loadEntry is one log row as room `room` sees it in direction `dir`.
func loadEntry(ctx context.Context, q db.DBTX, id, room uuid.UUID, dir string) (gen.RoomRead, error) {
	e, err := scanEntry(q.QueryRow(ctx, entriesSQL+` WHERE x.id = $2 AND x.dir = $3`, room, id, dir))
	if err != nil {
		return e, fmt.Errorf("rooms: entry: %w", err)
	}
	return e, nil
}

// ListOptions is listRoomReads' query.
type ListOptions struct {
	Direction string
	AgentID   *uuid.UUID
	Since     *time.Time
	Cursor    string
	Limit     int
}

// ErrBadCursor is a cursor this server did not hand out.
var ErrBadCursor = errors.New("rooms: bad cursor")

// ListReads is listRoomReads, newest first. The next cursor is nil at the end.
func ListReads(ctx context.Context, q db.DBTX, room uuid.UUID, o ListOptions) ([]gen.RoomRead, *string, error) {
	if o.Limit <= 0 {
		o.Limit = 50
	}
	where := []string{"true"}
	args := []any{room}
	if o.Direction != "" {
		args = append(args, o.Direction)
		where = append(where, fmt.Sprintf("x.dir = $%d", len(args)))
	}
	if o.AgentID != nil {
		args = append(args, *o.AgentID)
		where = append(where, fmt.Sprintf("x.reader_agent_id = $%d", len(args)))
	}
	if o.Since != nil {
		args = append(args, *o.Since)
		where = append(where, fmt.Sprintf("x.created_at >= $%d", len(args)))
	}
	if o.Cursor != "" {
		at, id, err := decodeCursor(o.Cursor)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, at, id)
		where = append(where, fmt.Sprintf("(x.created_at, x.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, o.Limit+1)
	rows, err := q.Query(ctx, entriesSQL+" WHERE "+strings.Join(where, " AND ")+
		fmt.Sprintf(" ORDER BY x.created_at DESC, x.id DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("rooms: reads: %w", err)
	}
	defer rows.Close()
	out := []gen.RoomRead{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("rooms: reads scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(out) > o.Limit {
		out = out[:o.Limit]
		last := out[len(out)-1]
		c := encodeCursor(last.At, last.Id)
		next = &c
	}
	return out, next, nil
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	at, idText, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	return t, id, nil
}
