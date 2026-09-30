package clienttest

// Card half of the fake server — openapi v0.3.10 tag `cards`:
// listRoomCards · getCard · submitCardResult · acceptCard · reviseCard.

import (
	"net/http"
	"strings"
)

// CardID is the one card the fake knows (C-1).
const CardID = "c4c4c4c4-c4c4-4c4c-8c4c-c4c4c4c4c4c4"

// CardCall is one captured card write.
type CardCall struct {
	Op   string // result · accept · revise
	Card string
	Key  string
	Body map[string]any
}

type cardState struct {
	// NotCardTask makes getCliContext answer a normal task (no card_id);
	// by default the fake's task is a card task (task_kind card, card_id
	// CardID) so every command of the gate table reaches the server.
	NotCardTask bool
	// DowngradeNotice is submitCardResult's notice.
	DowngradeNotice string
	CardCalls       []CardCall
	CardLists       []string // the work_id query of each listRoomCards
}

func (s *Server) cardJSON(status string) map[string]any {
	return map[string]any{"id": CardID, "label": "C-1", "number": 1, "version": 1, "status": status,
		"room_id": RoomID, "work_id": WorkID, "goal": "초안을 쓴다", "versions": []any{}}
}

// handleCards serves the card paths; the caller holds s.mu.
func (s *Server) handleCards(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case r.Method == "GET" && path == "/rooms/"+SessionID+"/cards":
		s.CardLists = append(s.CardLists, r.URL.Query().Get("work_id"))
		writeJSON(w, 200, map[string]any{"work_id": WorkID, "total": 1, "pending_judgement": 0, "items": []any{
			map[string]any{"id": CardID, "label": "C-1", "number": 1, "version": 1, "goal": "초안을 쓴다", "status": "in_progress",
				"assignee": map[string]any{"agent_id": ReviewerID, "name": ReviewerName}, "met": nil, "total_criteria": 1,
				"parent_card_id": nil, "cost_usd": nil, "lane_id": LaneID},
		}})
		return true
	case strings.HasPrefix(path, "/cards/"):
		rest := strings.TrimPrefix(path, "/cards/")
		id, op, _ := strings.Cut(rest, "/")
		if id != CardID {
			s.problem(w, 404, "not_found", "Not found", "작업 카드를 찾을 수 없습니다")
			return true
		}
		if r.Method == "GET" && op == "" {
			writeJSON(w, 200, s.cardJSON("in_progress"))
			return true
		}
		if r.Method != "POST" {
			return false
		}
		body, ok := decodeBody(s, w, r)
		if !ok {
			return true
		}
		s.CardCalls = append(s.CardCalls, CardCall{Op: op, Card: id, Key: r.Header.Get("Idempotency-Key"), Body: body})
		switch op {
		case "result":
			v, _ := body["verdicts"].([]any)
			if len(v) == 0 {
				writeJSON(w, 422, map[string]any{"type": "about:blank", "title": "Result card incomplete", "status": 422, "code": "result_card_incomplete",
					"errors": []map[string]any{{"field": "verdicts", "code": "missing_criteria", "message": "기준 1 의 판정이 없습니다"}}})
				return true
			}
			out := map[string]any{"card": s.cardJSON("result_submitted")}
			if s.DowngradeNotice != "" {
				out["notice"] = s.DowngradeNotice
			}
			writeJSON(w, 200, out)
		case "accept":
			writeJSON(w, 200, s.cardJSON("accepted"))
		case "revise":
			writeJSON(w, 200, s.cardJSON("in_progress"))
		default:
			return false
		}
		return true
	}
	return false
}
