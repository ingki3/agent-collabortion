package clienttest

// P2 half of the fake server — the openapi.yaml operations tagged
// x-colab-cli with x-phase P2: delegateLane · setTaskStatus ·
// recordDecision · submitArtifact · getArtifact · downloadArtifact ·
// reviewArtifact. Shapes follow the contract, not the (still 501) server.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// ArtifactID is the artifact the fake serves for get/download/review.
	ArtifactID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	// ArtifactBody is that artifact's stored content.
	ArtifactBody = "# findings\n3 competitors\n"
	// ArtifactName is its name (used for Content-Disposition).
	ArtifactName = "findings.md"
)

// Delegation is one captured delegateLane request.
type Delegation struct {
	Key  string
	Body map[string]any
}

// StatusCall is one captured setTaskStatus request.
type StatusCall struct {
	TaskID string
	Body   map[string]any
}

// Submission is one captured submitArtifact multipart request.
type Submission struct {
	Key         string
	Fields      map[string]string // name · type · description
	FileName    string
	ContentType string // the `file` part's Content-Type
	Data        []byte
}

// ReviewCall is one captured reviewArtifact request.
type ReviewCall struct {
	ArtifactID string
	Key        string
	Body       map[string]any
}

// p2State is embedded in Server; the mutex there guards it.
type p2State struct {
	// NotDesignatedReviewer makes reviewArtifact answer 403
	// not_designated_reviewer without storing anything (E6-06).
	NotDesignatedReviewer bool
	// BlockedQuestionID is the question card setTaskStatus reports for
	// `blocked` (E3-05). Empty means the server posted none.
	BlockedQuestionID string
	// TurnEndOnWorking forces turn_end_required on `working` too, so a test
	// can prove the CLI reports the server's value rather than its own guess.
	TurnEndOnWorking bool
	// NewLaneID is the lane delegateLane creates.
	NewLaneID string

	Delegations []Delegation
	StatusCalls []StatusCall
	Decisions   []map[string]any
	Submissions []Submission
	Reviews     []ReviewCall
}

// handleP2 serves the P2 paths. It returns false when path is not one of
// them, so the P1 switch can carry on. The caller holds s.mu.
func (s *Server) handleP2(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case r.Method == "POST" && strings.HasSuffix(path, "/lanes") && strings.HasPrefix(path, "/sessions/"):
		if path != "/sessions/"+SessionID+"/lanes" {
			s.problem(w, 403, "forbidden", "Forbidden", "token scope is another session")
			return true
		}
		body, ok := decodeBody(s, w, r)
		if !ok {
			return true
		}
		agentID, _ := body["agent_id"].(string)
		if agentID == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "agent_id required")
			return true
		}
		if !isParticipant(agentID) {
			// The server's own guard; the CLI normally refuses first (E15-02).
			s.problem(w, 422, "not_participant", "Not a participant",
				"target agent is not a session participant — ask the Director to add them (hitl ask)")
			return true
		}
		s.Delegations = append(s.Delegations, Delegation{Key: r.Header.Get("Idempotency-Key"), Body: body})
		laneID := s.NewLaneID
		if laneID == "" {
			laneID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		}
		s.Seq++
		msgID := fmt.Sprintf("dddddddd-0000-4000-8000-%012d", s.Seq)
		brief, _ := body["brief"].(string)
		writeJSON(w, 201, map[string]any{
			"lane": map[string]any{
				"id": laneID, "session_id": SessionID, "agent_id": agentID, "status": "queued",
				"delegated_from_task_id": TaskID, "reentry_count": 0,
			},
			"message": map[string]any{
				"id": msgID, "session_id": SessionID, "author_type": "agent", "author_id": AgentID,
				"parent_id": nil, "content": mentionOf(agentID) + " " + brief, "kind": "chat",
				"state": "posted", "source_task_id": TaskID, "lane_id": LaneID,
				"created_at": time.Now().UTC().Format(time.RFC3339),
			},
			"task": map[string]any{"id": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "lane_id": laneID, "attempt": 1},
		})
		return true

	case r.Method == "POST" && strings.HasPrefix(path, "/tasks/") && strings.HasSuffix(path, "/status"):
		taskID := strings.TrimSuffix(strings.TrimPrefix(path, "/tasks/"), "/status")
		if taskID != TaskID {
			s.problem(w, 403, "forbidden", "Forbidden", "token scope is another task")
			return true
		}
		body, ok := decodeBody(s, w, r)
		if !ok {
			return true
		}
		status, _ := body["status"].(string)
		note, _ := body["note"].(string)
		switch status {
		case "working", "blocked", "done":
		default:
			s.problem(w, 422, "validation_failed", "Validation failed", "status must be working|blocked|done")
			return true
		}
		if status == "blocked" && strings.TrimSpace(note) == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "note is required for blocked")
			return true
		}
		s.StatusCalls = append(s.StatusCalls, StatusCall{TaskID: taskID, Body: body})
		lane := map[string]any{"id": LaneID, "session_id": SessionID, "agent_id": AgentID, "status": "running"}
		out := map[string]any{
			"task":                map[string]any{"id": taskID, "lane_id": LaneID, "attempt": s.Attempt, "status": "running"},
			"lane":                lane,
			"turn_end_required":   status != "working" || s.TurnEndOnWorking,
			"question_message_id": nil,
		}
		if status == "blocked" {
			lane["status"] = "blocked"
			qid := s.BlockedQuestionID
			if qid == "" {
				qid = "ffffffff-ffff-4fff-8fff-ffffffffffff"
			}
			lane["blocked_message_id"] = qid
			out["question_message_id"] = qid
		}
		if status == "done" {
			lane["status"] = "done"
		}
		writeJSON(w, 200, out)
		return true

	case r.Method == "POST" && strings.HasSuffix(path, "/decisions") && strings.HasPrefix(path, "/sessions/"):
		if path != "/sessions/"+SessionID+"/decisions" {
			s.problem(w, 403, "forbidden", "Forbidden", "token scope is another session")
			return true
		}
		body, ok := decodeBody(s, w, r)
		if !ok {
			return true
		}
		summary, _ := body["summary"].(string)
		if strings.TrimSpace(summary) == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "summary required")
			return true
		}
		s.Decisions = append(s.Decisions, body)
		rationale := any(nil)
		if v, ok := body["rationale"]; ok {
			rationale = v
		}
		writeJSON(w, 201, map[string]any{
			"id": fmt.Sprintf("11111111-0000-4000-8000-%012d", len(s.Decisions)), "session_id": SessionID,
			"summary": summary, "rationale": rationale, "source": "agent", "ref_id": TaskID,
			"auto": false, "created_at": time.Now().UTC().Format(time.RFC3339),
		})
		return true

	case r.Method == "POST" && strings.HasSuffix(path, "/artifacts") && strings.HasPrefix(path, "/sessions/"):
		if path != "/sessions/"+SessionID+"/artifacts" {
			s.problem(w, 403, "forbidden", "Forbidden", "token scope is another session")
			return true
		}
		sub, err := readMultipart(r)
		if err != nil {
			s.problem(w, 422, "validation_failed", "Validation failed", err.Error())
			return true
		}
		sub.Key = r.Header.Get("Idempotency-Key")
		if sub.Fields["name"] == "" || sub.Fields["type"] == "" {
			s.problem(w, 422, "validation_failed", "Validation failed", "name and type are required")
			return true
		}
		s.Submissions = append(s.Submissions, sub)
		version := 0
		for _, p := range s.Submissions {
			if p.Fields["name"] == sub.Fields["name"] {
				version++
			}
		}
		writeJSON(w, 201, map[string]any{
			"artifact": map[string]any{
				"id": ArtifactID, "session_id": SessionID, "name": sub.Fields["name"], "version": version,
				"type": sub.Fields["type"], "storage_ref": "s3://fake/" + sub.Fields["name"],
				"size_bytes": len(sub.Data), "content_type": sub.ContentType,
				"submitted_by_task_id": TaskID, "description": sub.Fields["description"],
				"latest": true, "created_at": time.Now().UTC().Format(time.RFC3339),
			},
			"completion_progress": map[string]any{"met": 1, "total": 2, "satisfied": false,
				"human_gate": true, "conditions": []any{}},
		})
		return true

	case r.Method == "GET" && path == "/artifacts/"+ArtifactID:
		writeJSON(w, 200, map[string]any{
			"id": ArtifactID, "session_id": SessionID, "name": ArtifactName, "version": 1,
			"type": "report", "storage_ref": "s3://fake/" + ArtifactName, "size_bytes": len(ArtifactBody),
			"content_type": "text/plain", "submitted_by_task_id": TaskID,
			"submitted_by": map[string]any{"agent_id": AgentID, "agent_name": AgentName},
			"latest":       true, "created_at": time.Now().UTC().Format(time.RFC3339),
		})
		return true

	case r.Method == "GET" && path == "/artifacts/"+ArtifactID+"/content":
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `attachment; filename="`+ArtifactName+`"`)
		w.WriteHeader(200)
		w.Write([]byte(ArtifactBody))
		return true

	case r.Method == "POST" && strings.HasPrefix(path, "/artifacts/") && strings.HasSuffix(path, "/review"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/artifacts/"), "/review")
		body, ok := decodeBody(s, w, r)
		if !ok {
			return true
		}
		verdict, _ := body["verdict"].(string)
		comments, _ := body["comments"].(string)
		if verdict != "approve" && verdict != "reject" {
			s.problem(w, 422, "validation_failed", "Validation failed", "verdict must be approve|reject")
			return true
		}
		if s.NotDesignatedReviewer {
			// E6-06: refused *and not stored*.
			s.problem(w, 403, "not_designated_reviewer", "Not the designated reviewer",
				"the agent_approval completion condition designates another agent")
			return true
		}
		s.Reviews = append(s.Reviews, ReviewCall{ArtifactID: id, Key: r.Header.Get("Idempotency-Key"), Body: body})
		out := map[string]any{
			"review": map[string]any{
				"artifact_id": id, "verdict": verdict, "comments": comments,
				"reviewer_agent_id": AgentID, "reviewer_task_id": TaskID,
				"decision_id": "22222222-0000-4000-8000-000000000001",
				"reviewed_at": time.Now().UTC().Format(time.RFC3339),
			},
			"completion_progress": map[string]any{"met": 2, "total": 2, "satisfied": verdict == "approve",
				"human_gate": false, "conditions": []any{}},
		}
		if verdict == "reject" {
			s.Seq++
			out["message"] = map[string]any{
				"id": fmt.Sprintf("33333333-0000-4000-8000-%012d", s.Seq), "session_id": SessionID,
				"author_type": "agent", "author_id": AgentID, "parent_id": nil, "content": comments,
				"kind": "chat", "state": "posted", "created_at": time.Now().UTC().Format(time.RFC3339),
			}
		}
		writeJSON(w, 200, out)
		return true
	}
	return false
}

// decodeBody reads a JSON request body, answering 422 when it is unparseable.
func decodeBody(s *Server, w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.problem(w, 422, "validation_failed", "Validation failed", "unparseable body: "+err.Error())
		return nil, false
	}
	return body, true
}

// readMultipart parses submitArtifact's multipart/form-data body into the
// contract's four parts.
func readMultipart(r *http.Request) (Submission, error) {
	sub := Submission{Fields: map[string]string{}}
	mr, err := r.MultipartReader()
	if err != nil {
		return sub, err
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sub, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return sub, err
		}
		if part.FormName() == "file" {
			sub.FileName, sub.ContentType, sub.Data = part.FileName(), part.Header.Get("Content-Type"), data
			continue
		}
		sub.Fields[part.FormName()] = string(data)
	}
	if sub.FileName == "" && sub.Data == nil {
		return sub, fmt.Errorf("file part is required")
	}
	return sub, nil
}

func isParticipant(agentID string) bool {
	return agentID == AgentID || agentID == ReviewerID || agentID == DelegatorID
}

func mentionOf(agentID string) string {
	switch agentID {
	case ReviewerID:
		return mention(ReviewerName, ReviewerID)
	case DelegatorID:
		return mention(Delegator, DelegatorID)
	}
	return mention(AgentName, AgentID)
}
