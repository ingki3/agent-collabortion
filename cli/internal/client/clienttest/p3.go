package clienttest

// P3 half of the fake server — createHitlRequest (POST /tasks/{T}/hitl),
// the one operation behind `hitl ask` · `approve-request` · `request-info`.
// Shapes follow contracts/openapi.yaml; the real handler is still 501 while
// T-S5 fills it in, so the contract is the reference (same rule as p2.go).

import (
	"fmt"
	"net/http"
	"strings"
)

// HitlCall is one captured createHitlRequest request.
type HitlCall struct {
	TaskID string
	Key    string
	Body   map[string]any
}

// p3State is embedded in p2State (and so in Server); the Server mutex guards
// it.
type p3State struct {
	// HitlCalls is every createHitlRequest the fake accepted or refused.
	HitlCalls []HitlCall
	// OpenHitlID, when set, makes the next createHitlRequest answer
	// 409 hitl_already_open naming it (E7-04). The fake sets it itself after
	// a successful registration, so a second call in the same test conflicts
	// the way the server would.
	OpenHitlID string
	// HitlMessageID is the timeline card id returned with the request; empty
	// omits the field (the contract allows null).
	HitlMessageID string
	// HitlPurpose overrides HitlRequest.purpose. The server fixes it per
	// origin (agent-issued is `agent`, PR #110); tests that care set it.
	HitlPurpose string
}

// HitlID is the request id the fake mints for the first registration.
const HitlID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

func (s *Server) handleP3(w http.ResponseWriter, r *http.Request, path string) bool {
	if r.Method != "POST" || !strings.HasPrefix(path, "/tasks/") || !strings.HasSuffix(path, "/hitl") {
		return false
	}
	taskID := strings.TrimSuffix(strings.TrimPrefix(path, "/tasks/"), "/hitl")
	body, ok := decodeBody(s, w, r)
	if !ok {
		return true
	}
	s.HitlCalls = append(s.HitlCalls, HitlCall{TaskID: taskID, Key: r.Header.Get("Idempotency-Key"), Body: body})
	if taskID != TaskID {
		s.problem(w, 403, "forbidden", "Forbidden", "token scope is another task")
		return true
	}
	// One open request per task (E7-04). Checked before validation: the
	// server refuses the second request whatever it says.
	if s.OpenHitlID != "" {
		s.problem(w, 409, "hitl_already_open", "이미 대기 중인 요청이 있다",
			"이 task 에는 이미 열린 HITL 요청 "+s.OpenHitlID+" 이 있다. 첫 요청이 유지된다 — 답을 기다려라.")
		return true
	}
	typ, _ := body["type"].(string)
	def, _ := body["proposed_default"].(string)
	question, _ := body["question"].(string)
	switch typ {
	case "question", "choice":
		// The server's own guard; the CLI normally refuses first
		// (E7-05 · E7-20).
		if def == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "proposed_default is required for "+typ)
			return true
		}
		if question == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "question is required for "+typ)
			return true
		}
		if typ == "choice" {
			opts, _ := body["options"].([]any)
			if len(opts) < 2 {
				s.problem(w, 422, "validation_failed", "Validation failed", "options needs at least 2 items")
				return true
			}
		}
	case "approval":
		if sum, _ := body["summary"].(string); sum == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "summary is required for approval")
			return true
		}
		question, _ = body["summary"].(string) // stored in `question` (openapi)
	case "info":
		if what, _ := body["what"].(string); what == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "what is required for info")
			return true
		}
		question, _ = body["what"].(string) // stored in `question` (openapi)
	default:
		s.problem(w, 422, "validation_failed", "Validation failed", "unknown hitl type "+typ)
		return true
	}

	s.Seq++
	id := HitlID
	if s.Seq > 1 {
		id = fmt.Sprintf("eeeeeeee-0000-4000-8000-%012d", s.Seq)
	}
	s.OpenHitlID = id
	purpose := s.HitlPurpose
	if purpose == "" {
		purpose = "agent" // source=agent is always `agent` (PR #110)
	}
	opts := body["options"]
	if opts == nil {
		opts = []any{}
	}
	req := map[string]any{
		"id": id, "session_id": SessionID, "task_id": taskID, "lane_id": LaneID,
		"agent":  map[string]any{"id": AgentID, "name": AgentName},
		"source": "agent", "type": typ, "question": question,
		"context": body["context"], "options": opts, "proposed_default": nil,
		"artifact_id": body["artifact_id"], "purpose": purpose,
		"approver_spec": "director", "due_at": "2026-09-08T00:00:00Z", "overdue": false,
		"status": "open", "approved": nil, "answer": nil, "answered_by": nil, "answered_at": nil,
		"can_respond": false, "can_respond_from": nil, "created_at": "2026-09-07T00:00:00Z",
	}
	if def != "" {
		req["proposed_default"] = def
	}
	if s.HitlMessageID != "" {
		req["message_id"] = s.HitlMessageID
	}
	out := map[string]any{"hitl_request": req, "turn_end_required": true}
	if s.HitlMessageID != "" {
		out["message_id"] = s.HitlMessageID
	}
	writeJSON(w, 201, out)
	return true
}
