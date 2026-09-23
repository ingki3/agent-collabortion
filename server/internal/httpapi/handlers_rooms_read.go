package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// PRD v0.19 FR-4.5 — another room read on request (T-R1c). The permission,
// the reading and the record live in internal/rooms; these handlers turn a
// task token into a rooms.Reader and the answer into the contract.
//
// The role gate (colab-cli.md §2.5) is not consulted: `room_list` and
// `room_read` are not in ColabCommand yet (R3 adds them, for every role —
// §2.4a). The person-originator rule is the gate here.

func (s *Server) roomReader(r *http.Request) (rooms.Reader, *Problem) {
	sc := principalOf(r).Task
	if sc == nil {
		return rooms.Reader{}, apperr.Unauthorized("unauthorized", "에이전트만 쓸 수 있는 기능입니다")
	}
	return rooms.Reader{TaskID: sc.TaskID, Attempt: sc.Attempt, AgentID: sc.AgentID, RoomID: sc.SessionID}, nil
}

// ListReadableRooms is `colab room list` (openapi listReadableRooms).
func (s *Server) ListReadableRooms(w http.ResponseWriter, r *http.Request, params gen.ListReadableRoomsParams) {
	rd, p := s.roomReader(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	q := ""
	if params.Q != nil {
		q = *params.Q
	}
	list, err := s.Rooms.ListReadable(r.Context(), rd, q)
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]gen.ReadableRoom, 0, len(list))
	for _, x := range list {
		items = append(items, gen.ReadableRoom{
			Id: x.ID, Name: x.Name, Description: x.Description,
			LastActivityAt:     tasks.NullTime(x.LastActivityAt),
			AgentIsParticipant: x.AgentIsParticipant, ViaLink: x.ViaLink,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ReadRoom is `colab room read` (openapi readRoom).
func (s *Server) ReadRoom(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.ReadRoomParams) {
	rd, p := s.roomReader(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	tail := rooms.DefaultTail
	if params.Tail != nil {
		if *params.Tail < 1 || *params.Tail > 100 {
			writeProblem(w, apperr.Validation(apperr.Field("tail", "out_of_range", "최근 메시지는 1~100건까지 읽을 수 있습니다")))
			return
		}
		tail = *params.Tail
	}
	query := ""
	if params.Query != nil {
		query = *params.Query
	}
	res, denial, err := s.Rooms.Read(r.Context(), rd, roomId, tail, query)
	if errors.Is(err, rooms.ErrSelf) {
		writeProblem(w, apperr.Validation(apperr.Field("room_id", "current_room",
			"지금 있는 방입니다 — 다른 방을 읽을 때 쓰는 명령입니다")))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if denial != nil {
		writeProblem(w, roomReadDenied(denial))
		return
	}
	out := gen.RoomReadResult{
		Summary:   nullable.NewNullNullable[string](),
		Messages:  make([]gen.Message, 0, len(res.Messages)),
		Decisions: make([]gen.Decision, 0, len(res.Decisions)),
		Artifacts: make([]gen.Artifact, 0, len(res.Artifacts)),
		Truncated: res.Truncated,
	}
	out.Room.Id, out.Room.Name = res.RoomID, res.Name
	if res.Description != "" {
		desc := res.Description
		out.Room.Description = &desc
	}
	if res.Summary != nil {
		out.Summary = nullable.NewNullableWithValue(*res.Summary)
	}
	for _, m := range res.Messages {
		out.Messages = append(out.Messages, messages.ToAPI(m))
	}
	for _, d := range res.Decisions {
		out.Decisions = append(out.Decisions, sessions.DecisionAPI(res.RoomID, d))
	}
	for _, a := range res.Artifacts {
		out.Artifacts = append(out.Artifacts, artifactAPI(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// roomReadDenied is readRoom's 403 (openapi: `room_read_denied` +
// `denied_reason`). The sentence is for the agent, which relays it; only
// originator_left names the room (FR-4.5 존재 숨김의 예외).
func roomReadDenied(d *rooms.Denial) *Problem {
	var detail string
	switch d.Reason {
	case rooms.NoOriginator:
		detail = "이 턴은 사람의 요청에서 시작하지 않아 다른 방을 읽을 수 없습니다"
	case rooms.OriginatorLeft:
		detail = fmt.Sprintf("요청한 %s 「%s」 방을 떠나 그 방을 읽을 수 없습니다 — 요청자가 그 방의 참여자가 아닙니다",
			apperr.Josa(d.OriginatorName, "이", "가"), d.RoomName)
	case rooms.AgentNotAllowed:
		detail = "이 에이전트가 읽을 수 없는 방입니다"
	default:
		detail = "요청자가 그 방의 참여자가 아닙니다"
	}
	p := apperr.Forbidden("room_read_denied", detail)
	p.Extra = map[string]any{"denied_reason": string(d.Reason)}
	if d.Reason == rooms.OriginatorLeft {
		p.Extra["room_name"] = d.RoomName
	}
	return p
}

// ListRoomReads is S23 (openapi listRoomReads): anyone who may view the room.
func (s *Server) ListRoomReads(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.ListRoomReadsParams) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := s.roomViewer(r, roomId, u.Id); p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	o := rooms.ListOptions{AgentID: params.AgentId, Since: params.Since}
	if params.Direction != nil {
		switch *params.Direction {
		case "out", "in", "denied":
			o.Direction = string(*params.Direction)
		default:
			writeProblem(w, apperr.Validation(apperr.Field("direction", "invalid", "읽음 · 읽힘 · 거부 중 하나를 골라 주세요")))
			return
		}
	}
	if params.Cursor != nil {
		o.Cursor = *params.Cursor
	}
	if params.Limit != nil {
		o.Limit = *params.Limit
	}
	items, next, err := rooms.ListReads(r.Context(), s.DB, roomId, o)
	if errors.Is(err, rooms.ErrBadCursor) {
		writeProblem(w, apperr.Validation(apperr.Field("cursor", "invalid_cursor", "이전 응답의 다음 쪽 표시를 그대로 넣어 주세요")))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	nc := nullable.NewNullNullable[string]()
	if next != nil {
		nc = nullable.NewNullableWithValue(*next)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nc})
}

// roomNotFound is composed here rather than through apperr.NotFound("room")
// for the reason memberNotFound gives: apperr.NotFoundNouns is mirrored item
// for item by web/lib/mock/server-wording.test.ts and this PR does not touch
// web/. When the web mirror gains `room: 방`, move it into the table.
func roomNotFound() *Problem {
	return apperr.New(http.StatusNotFound, "not_found", "방을 찾을 수 없습니다")
}

// roomViewer is S23's gate: whoever may view the room (rooms.Decide ActView
// — participants, ws owner·admin for audit, and every member of a
// workspace-visible room). S23 must be no more open and no more closed than
// the room itself (review #290 R1-1 · #291 NN3), so it asks the one table
// rather than keeping its own. An invisible room is 404 like a missing one.
func (s *Server) roomViewer(r *http.Request, roomID, userID uuid.UUID) *Problem {
	a, err := rooms.LoadAccess(r.Context(), s.DB, roomID, userID)
	if err != nil {
		if pr := apperr.As(err); pr.Status != http.StatusNotFound {
			return pr
		}
		return roomNotFound()
	}
	if !rooms.Decide(rooms.ActView, a.Standing) {
		return roomNotFound()
	}
	return nil
}
