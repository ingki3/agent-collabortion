package clienttest

// Mission ledger half of the fake server — openapi v0.3.12 tag `memory`:
// listMemory · noteMemory · supersedeMemory · retireMemory.

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MemoryID is the one ledger item the fake knows (an active fact).
const MemoryID = "e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1"

// OtherWorkID is a second mission of the room (memory get --work).
const OtherWorkID = "d2d2d2d2-d2d2-4d2d-8d2d-d2d2d2d2d2d2"

// RetiredMemoryID is an item that is no longer active — supersede/retire on
// it is 409 memory_not_active.
const RetiredMemoryID = "e2e2e2e2-e2e2-4e2e-8e2e-e2e2e2e2e2e2"

// MemoryCall is one captured ledger request.
type MemoryCall struct {
	Op    string // note · supersede · retire · list
	Path  string // the API path without the prefix
	Key   string // Idempotency-Key as sent ("" when absent)
	Query string // list: the raw query
	Body  map[string]any
}

type memoryState struct {
	// MemoryForbidPlan makes noteMemory answer kind plan|progress with
	// 403 memory_kind_forbidden, as the server does for a non-lead caller.
	MemoryForbidPlan bool
	MemoryCalls      []MemoryCall
	memorySeq        int
}

func memoryItem(id, kind, content, status string) map[string]any {
	return map[string]any{"id": id, "work_id": WorkID, "kind": kind, "content": content,
		"certainty": nil, "outcome": nil, "support_count": 0, "promoted": true, "status": status,
		"supersedes": nil, "superseded_by": nil, "invalidated_at": nil, "source_message_ids": []any{},
		"created_by": map[string]any{"kind": "agent", "id": AgentID, "name": AgentName},
		"created_at": time.Now().UTC().Format(time.RFC3339)}
}

// handleMemory serves the ledger paths; the caller holds s.mu.
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case strings.HasPrefix(path, "/works/") && strings.HasSuffix(path, "/memory"):
		work := strings.TrimSuffix(strings.TrimPrefix(path, "/works/"), "/memory")
		switch r.Method {
		case "GET":
			s.MemoryCalls = append(s.MemoryCalls, MemoryCall{Op: "list", Path: path, Query: r.URL.RawQuery})
			if work != WorkID && work != OtherWorkID {
				s.problem(w, 404, "not_found", "Not found", "미션을 찾을 수 없습니다")
				return true
			}
			item := memoryItem(MemoryID, "fact", "경쟁사는 셋이다", "active")
			item["work_id"] = work
			writeJSON(w, 200, []any{item})
			return true
		case "POST":
			body, ok := decodeBody(s, w, r)
			if !ok {
				return true
			}
			s.MemoryCalls = append(s.MemoryCalls, MemoryCall{Op: "note", Path: path, Key: r.Header.Get("Idempotency-Key"), Body: body})
			kind, _ := body["kind"].(string)
			if s.MemoryForbidPlan && (kind == "plan" || kind == "progress") {
				s.problem(w, 403, "memory_kind_forbidden", "Forbidden", "plan·progress 는 lead 만 씁니다")
				return true
			}
			s.memorySeq++
			content, _ := body["content"].(string)
			item := memoryItem(fmt.Sprintf("e0e0e0e0-0000-4000-8000-%012d", s.memorySeq), kind, content, "active")
			item["work_id"] = work
			item["certainty"], item["outcome"] = body["certainty"], body["outcome"]
			writeJSON(w, 201, item)
			return true
		}
		return false
	case strings.HasPrefix(path, "/memory/") && r.Method == "POST":
		id, op, _ := strings.Cut(strings.TrimPrefix(path, "/memory/"), "/")
		if op != "supersede" && op != "retire" {
			return false
		}
		body, ok := decodeBody(s, w, r)
		if !ok {
			return true
		}
		s.MemoryCalls = append(s.MemoryCalls, MemoryCall{Op: op, Path: path, Key: r.Header.Get("Idempotency-Key"), Body: body})
		switch id {
		case MemoryID:
		case RetiredMemoryID:
			s.problem(w, 409, "memory_not_active", "Conflict", "이 항목은 이미 대체되었거나 철회되었습니다")
			return true
		default:
			s.problem(w, 404, "not_found", "Not found", "원장 항목을 찾을 수 없습니다")
			return true
		}
		if op == "retire" {
			item := memoryItem(MemoryID, "fact", "경쟁사는 셋이다", "retired")
			item["invalidated_at"] = time.Now().UTC().Format(time.RFC3339)
			// openapi v0.3.13 MemoryItem.retire_reason — the server echoes the trimmed reason.
			reason, _ := body["reason"].(string)
			item["retire_reason"] = strings.TrimSpace(reason)
			writeJSON(w, 200, item)
			return true
		}
		s.memorySeq++
		content, _ := body["content"].(string)
		nid := fmt.Sprintf("e0e0e0e0-0000-4000-8000-%012d", s.memorySeq)
		item := memoryItem(nid, "fact", content, "active")
		item["supersedes"] = MemoryID
		old := memoryItem(MemoryID, "fact", "경쟁사는 셋이다", "superseded")
		old["superseded_by"] = nid
		writeJSON(w, 201, map[string]any{"item": item, "superseded": old})
		return true
	}
	return false
}
