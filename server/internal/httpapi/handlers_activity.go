package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/events"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
)

// ListActivityLog is S15's audit list (openapi listActivityLog, FR-4.5 · §9):
// owner·admin only, newest first, no realtime ("감사 화면은 흐르면 읽을 수
// 없다"). The cursor is the row id — activity_log is append-only and its
// bigserial is the order.
//
// Masking: with the workspace's `task_event_masking` on, free-text payload
// values are cut to a head (the same "요약만" rule task events follow) and the
// entry says so with `masked: true`; ids, counts and names stay — they are
// what an audit is read for.
func (s *Server) ListActivityLog(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.ListActivityLogParams) {
	if _, p := s.admin(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	args := []any{workspaceId}
	where := []string{"l.workspace_id = $1"}
	addArg := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if params.RoomId != nil {
		// A deleted room's lines have session_id NULL and the id in the
		// payload (room.deleted) — both are "this room".
		args = append(args, *params.RoomId)
		where = append(where, fmt.Sprintf("(l.session_id = $%d OR l.payload->>'room_id' = ($%d::uuid)::text)", len(args), len(args)))
	}
	if params.Action != nil && *params.Action != "" {
		addArg("l.action = $%d", *params.Action)
	}
	if params.ActorId != nil {
		addArg("l.actor_id = $%d", *params.ActorId)
	}
	if params.Since != nil {
		addArg("l.created_at >= $%d", *params.Since)
	}
	if params.Until != nil {
		addArg("l.created_at < $%d", *params.Until)
	}
	if params.Cursor != nil && *params.Cursor != "" {
		c, err := strconv.ParseInt(*params.Cursor, 10, 64)
		if err != nil {
			writeProblem(w, apperr.Validation(apperr.Field("cursor", "invalid", "목록 위치 표시가 올바르지 않습니다 — 처음부터 다시 불러와 주세요")))
			return
		}
		addArg("l.id < $%d", c)
	}
	args = append(args, limit+1)
	rows, err := s.DB.Query(r.Context(), `
		SELECT l.id, l.created_at, l.actor_type::text, l.actor_id,
		       COALESCE(u.display_name, a.name, ''), l.action, COALESCE(l.object_type, ''), l.object_id,
		       l.session_id, rm.name, l.payload
		FROM activity_log l
		LEFT JOIN app_user u ON l.actor_type = 'user' AND u.id = l.actor_id
		LEFT JOIN agent a ON l.actor_type = 'agent' AND a.id = l.actor_id
		LEFT JOIN room rm ON rm.id = l.session_id
		WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY l.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	masking, err := events.Masking(r.Context(), s.DB, workspaceId)
	if err != nil {
		writeErr(w, err)
		return
	}
	items := []gen.ActivityLogEntry{}
	var lastID int64
	more := false
	for rows.Next() {
		var e gen.ActivityLogEntry
		var id int64
		var actorType, actorName, objType string
		var actorID, objID, roomID *uuid.UUID
		var roomName *string
		var payload []byte
		if err := rows.Scan(&id, &e.At, &actorType, &actorID, &actorName, &e.Action, &objType, &objID, &roomID, &roomName, &payload); err != nil {
			writeErr(w, err)
			return
		}
		if len(items) == limit {
			more = true
			break
		}
		lastID = id
		e.Id = strconv.FormatInt(id, 10)
		e.Actor.Kind = gen.ActivityLogEntryActorKind(actorType)
		e.Actor.Id = nullable.NewNullNullable[openapi_types.UUID]()
		if actorID != nil {
			e.Actor.Id = nullable.NewNullableWithValue(openapi_types.UUID(*actorID))
		}
		e.Actor.Name = actorName
		if actorType == "system" {
			e.Actor.Name = "Colab"
		}
		e.ObjectRef = objType
		if objID != nil {
			e.ObjectRef = objType + ":" + objID.String()
		}
		e.Payload = map[string]any{}
		_ = json.Unmarshal(payload, &e.Payload)
		if masking {
			maskActivity(e.Payload)
		}
		e.Room = nullable.NewNullNullable[struct {
			Id   openapi_types.UUID `json:"id"`
			Name string             `json:"name"`
		}]()
		if roomID != nil && roomName != nil {
			e.Room = nullable.NewNullableWithValue(struct {
				Id   openapi_types.UUID `json:"id"`
				Name string             `json:"name"`
			}{Id: *roomID, Name: *roomName})
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	var next *string
	if more {
		c := strconv.FormatInt(lastID, 10)
		next = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// activityHeadRunes is how much of a free-text payload value survives masking.
const activityHeadRunes = 80

func maskActivity(p map[string]any) {
	masked := false
	for k, v := range p {
		str, ok := v.(string)
		if !ok {
			continue
		}
		if _, err := uuid.Parse(str); err == nil {
			continue
		}
		if r := []rune(str); len(r) > activityHeadRunes {
			p[k] = string(r[:activityHeadRunes]) + "…"
			masked = true
		}
	}
	if masked {
		p["masked"] = true
	}
}

// SetLaneSubscription is the per-thread notification switch (openapi
// setLaneSubscription, FR-8): a room participant turns one sub-mission on or
// off for themselves. The row stays when switched off — deleting it would
// mean "follow the room", which for an `all` room is "on" again.
func (s *Server) SetLaneSubscription(w http.ResponseWriter, r *http.Request, laneId gen.LaneId) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var roomID uuid.UUID
	err := s.DB.QueryRow(r.Context(), `SELECT session_id FROM lane WHERE id = $1`, laneId).Scan(&roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, apperr.NotFound("lane"))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	a, err := rooms.LoadAccess(r.Context(), s.DB, roomID, u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !rooms.Decide(rooms.ActView, a.Standing) {
		// A lane of a room the caller cannot see does not exist for them.
		writeProblem(w, apperr.NotFound("lane"))
		return
	}
	if !rooms.Decide(rooms.ActSubscribe, a.Standing) {
		writeProblem(w, rooms.Deny(rooms.ActSubscribe, a.Standing))
		return
	}
	var in gen.SetLaneSubscriptionJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if _, err := s.DB.Exec(r.Context(), `
		INSERT INTO lane_subscription (lane_id, user_id, enabled, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (lane_id, user_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`,
		laneId, u.Id, in.Enabled, s.Clock.Now()); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
