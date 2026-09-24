package clienttest

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The v0.19 R3 room operations (colab-cli.md v0.8 §2.4a), written from
// openapi.yaml listReadableRooms · readRoom · createWorkProposal.

// OtherRoomID is the one room the fake lets a turn read.
const (
	OtherRoomID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	ProposalID  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

// roomState holds the room knobs and captures.
type roomState struct {
	// RoomReadDenied, when set, is readRoom's 403 room_read_denied
	// denied_reason (originator_not_participant · originator_left ·
	// agent_not_allowed · no_originator).
	RoomReadDenied string
	// RoomReadTruncated is RoomReadResult.truncated.
	RoomReadTruncated bool
	// Proposals are the createWorkProposal bodies as sent.
	Proposals []map[string]any
}

func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case r.Method == "GET" && path == "/cli/rooms":
		items := []map[string]any{}
		room := map[string]any{"id": OtherRoomID, "name": "경쟁사 조사", "description": "지난 분기 조사 방",
			"last_activity_at": time.Now().UTC().Format(time.RFC3339), "agent_is_participant": true, "via_link": false}
		if q := r.URL.Query().Get("q"); q == "" || strings.Contains(room["name"].(string), q) {
			items = append(items, room)
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return true
	case r.Method == "GET" && strings.HasPrefix(path, "/cli/rooms/") && strings.HasSuffix(path, "/read"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/cli/rooms/"), "/read")
		if s.RoomReadDenied != "" || id != OtherRoomID {
			reason := s.RoomReadDenied
			if reason == "" {
				reason = "originator_not_participant"
			}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(403)
			writeBody(w, map[string]any{"type": "https://colab.dev/problems/room-read-denied", "title": "Forbidden", "status": 403,
				"code": "room_read_denied", "detail": "요청자가 그 방의 참여자가 아닙니다", "denied_reason": reason})
			return true
		}
		writeJSON(w, 200, map[string]any{
			"room":     map[string]any{"id": OtherRoomID, "name": "경쟁사 조사", "description": "지난 분기 조사 방"},
			"summary":  "세 곳을 비교했다",
			"messages": []any{map[string]any{"id": "m1", "content": "A 사가 가장 싸다"}}, "decisions": []any{}, "artifacts": []any{},
			"truncated": s.RoomReadTruncated,
		})
		return true
	case r.Method == "POST" && strings.HasPrefix(path, "/rooms/") && strings.HasSuffix(path, "/work-proposals"):
		room := strings.TrimSuffix(strings.TrimPrefix(path, "/rooms/"), "/work-proposals")
		body, ok := decodeBody(s, w, r)
		if !ok {
			return true
		}
		s.Proposals = append(s.Proposals, body)
		if room != SessionID {
			s.problem(w, 403, "outside_task_scope", "Forbidden", "다른 방에는 접근할 수 없습니다")
			return true
		}
		writeJSON(w, 201, map[string]any{
			"id": ProposalID, "room_id": SessionID, "agent": map[string]any{"id": AgentID, "name": AgentName},
			"proposed_by_task_id": TaskID, "goal": body["goal"], "rationale": body["rationale"], "trigger_message_id": nil,
			"status": "open", "decided_by": nil, "decided_at": nil, "reject_reason": nil, "work_id": nil,
			"created_at": time.Now().UTC().Format(time.RFC3339),
		})
		return true
	}
	return false
}

func writeBody(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
