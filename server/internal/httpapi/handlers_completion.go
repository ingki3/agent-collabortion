package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// CompleteSession is FR-2.2's `manual` condition (E6-08): the Director ends the
// session directly, whatever the completion tree says.
func (s *Server) CompleteSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, wsID, p := s.sessionDirector(r, sessionId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	_ = wsID
	var in gen.CompleteSessionJSONBody
	if r.ContentLength > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	var running int
	if err := s.DB.QueryRow(r.Context(), `
		SELECT count(*) FROM lane WHERE session_id = $1 AND status IN ('queued', 'running')`, sessionId).Scan(&running); err != nil {
		writeErr(w, err)
		return
	}
	// Ending a session with work in flight throws that work away, so it takes a
	// second, explicit act — and the count says how much is at stake.
	if running > 0 && (in.Confirm == nil || !*in.Confirm) {
		p := apperr.Conflict("running_lanes", "이 세션에는 진행 중인 lane이 있습니다 — confirm: true로 다시 요청하세요")
		p.Extra = map[string]any{"running_lane_count": running}
		writeProblem(w, p)
		return
	}
	if _, err := s.Sessions.ApplyCompletionEvent(r.Context(), sessionId, sessions.Event{
		Kind: "director_end", Note: "Director ended the session",
	}); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.Sessions.Get(r.Context(), sessionId, sessions.Viewer{UserID: &u.Id})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// RecordDecision is FR-4.2. `colab decision record` writes source: agent with
// the calling task as ref; a member writing from the UI records source: hitl.
// RecordDecision is `colab decision record` (FR-4.2). **Agents only** —
// openapi recordDecision is `security: [TaskToken]` and its description says
// so ("권한: TaskToken(에이전트, source: agent)"). A person's decision is not
// written here: answering a HITL request is what records one, with
// `source: hitl` and the request as `ref_id`. Accepting a cookie here let the
// same session grow two kinds of `hitl` decision — one with a ref_id and one
// without — and gave a plain member a write the contract never granted.
func (s *Server) RecordDecision(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.RecordDecisionParams) {
	pr := principalOf(r)
	if pr.Task == nil {
		if pr.User == nil {
			writeProblem(w, apperr.Unauthorized("unauthorized", "TaskToken required"))
			return
		}
		writeProblem(w, apperr.Forbidden("agent_only",
			"decision record is an agent tool (openapi recordDecision is TaskToken-only); a person's decision is recorded by answering the HITL request"))
		return
	}
	if _, p := s.sessionAccess(r, sessionId); p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.RecordDecisionJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if strings.TrimSpace(in.Summary) == "" {
		writeProblem(w, apperr.Validation(apperr.Field("summary", "required", "summary is required")))
		return
	}
	taskID := pr.Task.TaskID
	source, scope, ref := "agent", taskScope(taskID), &taskID
	rationale := ""
	if in.Rationale != nil {
		rationale = *in.Rationale
	}
	call := func() (int, any, *Problem) {
		id, err := s.Sessions.RecordDecision(r.Context(), sessionId, in.Summary, rationale, source, ref)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		out, err := s.Sessions.ListDecisions(r.Context(), sessionId)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		for _, d := range out {
			if d.ID == id {
				return http.StatusCreated, decisionAPI(sessionId, d), nil
			}
		}
		return http.StatusCreated, map[string]any{"id": id}, nil
	}
	if params.IdempotencyKey == nil {
		st, out, p := call()
		if p != nil {
			writeProblem(w, p)
			return
		}
		writeJSON(w, st, out)
		return
	}
	s.idempotent(r.Context(), w, scope, params.IdempotencyKey.String(), requestHash(r, body), call)
}

func (s *Server) ListDecisions(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	if _, p := s.sessionAccess(r, sessionId); p != nil {
		writeProblem(w, p)
		return
	}
	rows, err := s.Sessions.ListDecisions(r.Context(), sessionId)
	if err != nil {
		writeErr(w, err)
		return
	}
	// The contract's listDecisions response is `type: array` — the same shape
	// listArtifacts already returns. Wrapping it in {"items": …} killed the whole
	// S7 session screen (`props.decisions.map is not a function`, G4 결함 1).
	items := make([]gen.Decision, 0, len(rows))
	for _, d := range rows {
		items = append(items, decisionAPI(sessionId, d))
	}
	writeJSON(w, http.StatusOK, items)
}

func decisionAPI(sessionID uuid.UUID, d sessions.DecisionRow) gen.Decision {
	out := gen.Decision{
		Id: d.ID, SessionId: sessionID, Summary: d.Summary,
		Source: gen.DecisionSource(d.Source), CreatedAt: d.CreatedAt,
	}
	if d.Rationale != nil {
		out.Rationale = nullable.NewNullableWithValue(*d.Rationale)
	} else {
		out.Rationale = nullable.NewNullNullable[string]()
	}
	if d.RefID != nil {
		out.RefId = nullable.NewNullableWithValue(openapi_types.UUID(*d.RefID))
	} else {
		out.RefId = nullable.NewNullNullable[openapi_types.UUID]()
	}
	return out
}
