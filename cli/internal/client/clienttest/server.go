// Package clienttest is an in-memory stand-in for the Colab server's
// x-colab-cli operations, used by the CLI/MCP tests. It implements the
// openapi.yaml shapes the P1 commands depend on: TaskToken auth (401
// token_revoked · no bearer), GET /cli/context, GET /sessions/{S},
// GET/POST /sessions/{S}/messages with Idempotency-Key replay.
package clienttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

const (
	Token     = "ctk_dGVzdHRlc3R0ZXN0dGVzdHRlc3R0ZXN0dGVzdHRlc3Q"
	TaskID    = "11111111-1111-4111-8111-111111111111"
	LaneID    = "22222222-2222-4222-8222-222222222222"
	SessionID = "33333333-3333-4333-8333-333333333333"
	AgentID   = "44444444-4444-4444-8444-444444444444"
	AgentName = "Researcher"
	Attempt   = 2

	ReviewerID   = "55555555-5555-4555-8555-555555555555"
	ReviewerName = "Reviewer"
	DelegatorID  = "66666666-6666-4666-8666-666666666666"
	Delegator    = "Lead"
)

// Posted is one stored message plus the response that was returned for it.
type Posted struct {
	Key      string
	Body     map[string]any
	Response []byte
}

// Server is the fake. Mutate the exported fields before the request under test.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	Revoked  bool // every authed call → 401 token_revoked
	Fail     int  // if >0, every call returns this status with a Problem
	FailCode string
	Prefix   string // API prefix the fake mounts (default /api/v1)
	Posted   []Posted
	ByKey    map[string]Posted
	Requests []*http.Request // every request seen (auth header preserved)
	Seq      int
	Messages []map[string]any
}

// New starts the fake server; it is closed on test cleanup.
func New(t interface {
	Cleanup(func())
	Helper()
}) *Server {
	t.Helper()
	s := &Server{ByKey: map[string]Posted{}, Prefix: "/api/v1"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) problem(w http.ResponseWriter, status int, code, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"type":  "https://colab.dev/problems/" + strings.ReplaceAll(code, "_", "-"),
		"title": title, "status": status, "code": code, "detail": detail,
	})
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, r)
	if !strings.HasPrefix(r.URL.Path, s.Prefix+"/") {
		s.problem(w, 404, "not_found", "Not found", "wrong API prefix: "+r.URL.Path)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, s.Prefix)

	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+Token {
		s.problem(w, 401, "unauthorized", "Unauthorized", "missing or invalid task token")
		return
	}
	if s.Revoked {
		s.problem(w, 401, "token_revoked", "Task token revoked", "이 task는 재큐잉되어 토큰이 폐기되었다. 즉시 종료하라.")
		return
	}
	if s.Fail > 0 {
		code := s.FailCode
		if code == "" {
			code = "failed"
		}
		s.problem(w, s.Fail, code, http.StatusText(s.Fail), "forced failure")
		return
	}

	switch {
	case r.Method == "GET" && path == "/cli/context":
		writeJSON(w, 200, map[string]any{
			"task_id": TaskID, "lane_id": LaneID, "session_id": SessionID, "agent_id": AgentID,
			"agent_name": AgentName, "workspace_id": "77777777-7777-4777-8777-777777777777", "attempt": Attempt,
			"delegated_from_task_id": nil, "suppressed_delegator_agent_id": DelegatorID, "open_hitl_request_id": nil,
			"participants": []map[string]any{
				{"agent_id": AgentID, "name": AgentName, "role": "researcher", "mention_link": mention(AgentName, AgentID)},
				{"agent_id": ReviewerID, "name": ReviewerName, "role": "reviewer", "mention_link": mention(ReviewerName, ReviewerID)},
				{"agent_id": DelegatorID, "name": Delegator, "role": "lead", "mention_link": mention(Delegator, DelegatorID)},
			},
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	case r.Method == "GET" && path == "/sessions/"+SessionID:
		writeJSON(w, 200, map[string]any{
			"id": SessionID, "title": "Market research", "goal": "Find 3 competitors",
			"acceptance_criteria": []string{"table of 3", "sources cited"},
			"completion_progress": map[string]any{"met": 1, "total": 2, "satisfied": false, "human_gate": true, "conditions": []any{}},
			"isolation":           "worktree", "my_role": "member", "status": "active",
			"director": map[string]any{"id": "88888888-8888-4888-8888-888888888888", "name": "Dana"},
			"participants": []map[string]any{
				{"agent_id": AgentID, "agent": map[string]any{"name": AgentName, "role_description": "digs"}, "status": "running"},
				{"agent_id": ReviewerID, "agent": map[string]any{"name": ReviewerName, "role_description": "checks"}, "status": "idle"},
			},
		})
	case r.Method == "GET" && strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/messages"):
		if path != "/sessions/"+SessionID+"/messages" {
			s.problem(w, 403, "forbidden", "Forbidden", "token scope is another session")
			return
		}
		items := s.Messages
		q := r.URL.Query()
		if th := q.Get("thread"); th != "" {
			var f []map[string]any
			for _, m := range items {
				if m["id"] == th || m["parent_id"] == th {
					f = append(f, m)
				}
			}
			items = f
		}
		if after := q.Get("after"); after != "" {
			var f []map[string]any
			seen := false
			for _, m := range items {
				if seen {
					f = append(f, m)
				}
				if m["id"] == after {
					seen = true
				}
			}
			items = f
		}
		total := len(items)
		limit := 50
		fmt.Sscanf(q.Get("limit"), "%d", &limit)
		more := false
		if len(items) > limit {
			items = items[:limit]
			more = true
		}
		if items == nil {
			items = []map[string]any{}
		}
		writeJSON(w, 200, map[string]any{"items": items, "before_cursor": nil, "after_cursor": nil,
			"has_more_before": false, "has_more_after": more, "total": total})
	case r.Method == "POST" && path == "/sessions/"+SessionID+"/messages":
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "Idempotency-Key required")
			return
		}
		if p, ok := s.ByKey[key]; ok {
			w.Header().Set("Idempotent-Replayed", "true")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			w.Write(p.Response)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["content"] == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "content required")
			return
		}
		s.Seq++
		id := fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012d", s.Seq)
		content, _ := body["content"].(string)
		msg := map[string]any{
			"id": id, "session_id": SessionID, "author_type": "agent", "author_id": AgentID,
			"author":    map[string]any{"name": AgentName, "role": "researcher"},
			"parent_id": body["parent_id"], "content": content, "mentions": []any{},
			"source_task_id": TaskID, "lane_id": LaneID, "kind": "chat", "state": "posted",
			"reply_count": 0, "created_at": time.Now().UTC().Format(time.RFC3339),
		}
		s.Messages = append(s.Messages, msg)
		triggers := []map[string]any{}
		warnings := []map[string]any{}
		if strings.Contains(content, ReviewerID) {
			triggers = append(triggers, map[string]any{"agent_id": ReviewerID, "task_id": "99999999-9999-4999-8999-999999999999", "lane_id": LaneID, "coalesced": false, "deferred_until": nil})
		}
		if strings.Contains(content, DelegatorID) {
			warnings = append(warnings, map[string]any{"code": "delegator_mention_suppressed", "message": "delegator is suppressed until rejoin (rule 8)", "agent_id": DelegatorID})
		}
		resp, _ := json.Marshal(map[string]any{"message": msg, "triggers": triggers, "warnings": warnings, "session_paused": nil})
		p := Posted{Key: key, Body: body, Response: resp}
		s.Posted = append(s.Posted, p)
		s.ByKey[key] = p
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write(resp)
	default:
		s.problem(w, 404, "not_found", "Not found", path)
	}
}

func mention(name, id string) string { return "[@" + name + "](mention://agent/" + id + ")" }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Env returns the COLAB_* environment an agent process would see, pointing at
// this fake. stateDir isolates the client_seq counter per test.
func (s *Server) Env(stateDir string) map[string]string {
	return map[string]string{
		"COLAB_TASK_TOKEN": Token, "COLAB_SERVER_URL": s.URL,
		"COLAB_TASK_ID": TaskID, "COLAB_LANE_ID": LaneID, "COLAB_SESSION_ID": SessionID,
		"COLAB_AGENT_NAME": AgentName, "COLAB_STATE_DIR": stateDir,
	}
}

// Getenv adapts a map to client.Getenv.
func Getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}
