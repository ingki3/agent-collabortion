package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// HITL responses (openapi respondHitlRequest). The operation as a whole is P3,
// but ONE of its branches is P2 (contracts PR #101): the approval the PLATFORM
// issues for a `user_approval` completion condition — E6-03 (승인 → completing
// → completed + 요약) and E6-04 (거절 → active 유지 + 사유 결정 기록).
//
// That branch had to exist. `sessions.ApplyCompletionEvent` already implements
// `director_approve`, but nothing called it, so there was no HTTP entrance in
// P2 that could satisfy the `user_approval` atom at all — G5 had to measure
// E6-03 through `completeSession`, which satisfies `manual` instead and so
// proves a different rule (S-25).
//
// Everything else stays 501 and says so: an agent-issued question, the
// re-queue of the asking task, `budget_override_usd`, and the deputy window
// are all P3, and answering them here would half-implement FR-5.4.

func (s *Server) RespondHitlRequest(w http.ResponseWriter, r *http.Request, hitlRequestId gen.HitlRequestId, params gen.RespondHitlRequestParams) {
	row, err := loadHitlRow(r.Context(), s.DB, hitlRequestId)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Membership first: a non-member must not learn that this id exists.
	if _, p := s.sessionAccess(r, row.SessionID); p != nil {
		writeProblem(w, p)
		return
	}
	if !row.isCompletionApproval() {
		notImplemented(w, r, "RespondHitlRequest (P2 answers only the platform-issued user_approval request)")
		return
	}
	// approver_spec `director`. The deputy half-window (M7, E7-09·10) is P3 —
	// refusing is the fail-closed answer while it is unimplemented.
	u, _, p := s.sessionDirector(r, row.SessionID)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.HitlResponse
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if in.Approved == nil {
		writeProblem(w, apperr.Validation(apperr.Field("approved", "required",
			"an approval request is answered with approved: true or false")))
		return
	}
	reason := ""
	if in.Reason != nil {
		reason = strings.TrimSpace(*in.Reason)
	}
	if !*in.Approved && reason == "" {
		// E6-04 stores the reason in the decision log; a rejection without one
		// leaves the log saying a decision was made and not why.
		writeProblem(w, apperr.Validation(apperr.Field("reason", "required",
			"a rejection records its reason in the decision log (E6-04)")))
		return
	}
	if in.BudgetOverrideUsd != nil || in.TimeExtension != nil {
		notImplemented(w, r, "RespondHitlRequest budget_override_usd / time_extension")
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), params.IdempotencyKey.String(), requestHash(r, body),
		func() (int, any, *Problem) {
			return s.answerCompletionApproval(r.Context(), hitlRequestId, u.Id, *in.Approved, reason)
		})
}

// answerCompletionApproval records the answer and then folds it into the
// session's completion state.
//
// The order matters and neither is free: the answer is written first because
// it is the record of what a person did, and an unapplied answer leaves the
// Director able to end the session by hand. Applying first and failing to
// record would leave the request open in the inbox on a session that is
// already over, and the next response would come back 409.
func (s *Server) answerCompletionApproval(ctx context.Context, hitlID, userID uuid.UUID, approved bool, reason string) (int, any, *Problem) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var status string
	var sessionID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status::text, session_id FROM hitl_request WHERE id = $1 FOR UPDATE`, hitlID).
		Scan(&status, &sessionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, apperr.NotFound("hitl_request")
		}
		return 0, nil, apperr.Internal(err)
	}
	if status != string(gen.HitlStatusOpen) {
		// E7-08: a second response is not an error. It is ignored, and the
		// caller gets the answer that stands.
		_ = tx.Rollback(ctx)
		out, err := s.hitlAPI(ctx, s.DB, hitlID, &userID)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusOK, map[string]any{"hitl_request": out, "ignored": true, "decision_id": nil}, nil
	}
	var answer *string
	if reason != "" {
		answer = &reason
	}
	if _, err := tx.Exec(ctx, `
		UPDATE hitl_request SET status = 'answered', approved = $2, answer = $3, answered_by = $4, answered_at = $5
		WHERE id = $1`, hitlID, approved, answer, userID, now); err != nil {
		return 0, nil, apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, apperr.Internal(err)
	}

	kind := "director_approve"
	if !approved {
		kind = "director_reject"
	}
	ref := hitlID
	outcome, err := s.Sessions.ApplyCompletionEvent(ctx, sessionID, sessions.Event{
		Kind: kind, Actor: userID, Note: reason, Ref: &ref,
	})
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	out, err := s.hitlAPI(ctx, s.DB, hitlID, &userID)
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	resp := map[string]any{"hitl_request": out, "ignored": false, "decision_id": nil}
	if outcome.DecisionID != uuid.Nil {
		resp["decision_id"] = outcome.DecisionID
	}
	return http.StatusOK, resp, nil
}

// hitlRow is the stored request, before it becomes a contract object.
type hitlRow struct {
	ID, SessionID   uuid.UUID
	TaskID, LaneID  *uuid.UUID
	AgentID         *uuid.UUID
	AgentName       *string
	Source, Type    string
	Question        string
	Options         []string
	ProposedDefault *string
	ApproverSpec    string
	Purpose         *string
	DueAt           time.Time
	Overdue         bool
	Status          string
	Approved        *bool
	Answer          *string
	AnsweredBy      *uuid.UUID
	AnsweredAt      *time.Time
	CreatedAt       time.Time
	Director        uuid.UUID
}

// isCompletionApproval is the P2 branch's gate: the platform's own approval for
// a `user_approval` completion condition. `source = system` and
// `type = approval` alone are not enough — the budget and loop pauses issue the
// same pair — which is why 0012 stores `purpose`.
func (h *hitlRow) isCompletionApproval() bool {
	return h.Source == string(gen.HitlSourceSystem) && h.Type == string(gen.HitlTypeApproval) &&
		h.Purpose != nil && *h.Purpose == sessions.CondUserApproval
}

func loadHitlRow(ctx context.Context, q db.DBTX, id uuid.UUID) (*hitlRow, error) {
	var h hitlRow
	err := q.QueryRow(ctx, `
		SELECT h.id, h.session_id, h.task_id, t.lane_id, t.agent_id, a.name,
		       h.source::text, h.type::text, h.question, h.options, h.proposed_default,
		       h.approver_spec, h.purpose, h.due_at, h.overdue, h.status::text,
		       h.approved, h.answer, h.answered_by, h.answered_at, h.created_at, s.director_user_id
		FROM hitl_request h
		JOIN session s ON s.id = h.session_id
		LEFT JOIN task t ON t.id = h.task_id
		LEFT JOIN agent a ON a.id = t.agent_id
		WHERE h.id = $1`, id).
		Scan(&h.ID, &h.SessionID, &h.TaskID, &h.LaneID, &h.AgentID, &h.AgentName,
			&h.Source, &h.Type, &h.Question, &h.Options, &h.ProposedDefault,
			&h.ApproverSpec, &h.Purpose, &h.DueAt, &h.Overdue, &h.Status,
			&h.Approved, &h.Answer, &h.AnsweredBy, &h.AnsweredAt, &h.CreatedAt, &h.Director)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("hitl_request")
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// hitlAPI renders one request as the contract's HitlRequest.
func (s *Server) hitlAPI(ctx context.Context, q db.DBTX, id uuid.UUID, viewer *uuid.UUID) (*gen.HitlRequest, error) {
	h, err := loadHitlRow(ctx, q, id)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	out := gen.HitlRequest{
		Id: h.ID, SessionId: h.SessionID, Question: h.Question, ApproverSpec: h.ApproverSpec,
		DueAt: h.DueAt, CreatedAt: h.CreatedAt,
		Overdue: h.Overdue || now.After(h.DueAt),
		Source:  gen.HitlSource(h.Source), Type: gen.HitlType(h.Type),
		Status:  gen.HitlStatus(h.Status),
		Options: h.Options,
	}
	if out.Options == nil {
		out.Options = []string{}
	}
	out.TaskId = tasks.NullUUID(h.TaskID)
	out.LaneId = tasks.NullUUID(h.LaneID)
	out.AnsweredBy = tasks.NullUUID(h.AnsweredBy)
	out.AnsweredAt = tasks.NullTime(h.AnsweredAt)
	out.Answer = tasks.NullString(h.Answer)
	out.ProposedDefault = tasks.NullString(h.ProposedDefault)
	if h.Approved != nil {
		out.Approved = nullable.NewNullableWithValue(*h.Approved)
	} else {
		out.Approved = nullable.NewNullNullable[bool]()
	}
	if h.Purpose != nil {
		out.Purpose = nullable.NewNullableWithValue(gen.HitlRequestPurpose(*h.Purpose))
	}
	if h.AgentID != nil && h.AgentName != nil {
		out.Agent = &struct {
			Id   *uuid.UUID `json:"id,omitempty"`
			Name *string    `json:"name,omitempty"`
		}{Id: h.AgentID, Name: h.AgentName}
	}
	// can_respond is what greys the button out (SCREEN S8). It is the same
	// judgement the handler makes, so it never says "you can" to someone the
	// handler will refuse.
	out.CanRespond = h.Status == string(gen.HitlStatusOpen) && viewer != nil &&
		h.isCompletionApproval() && h.ApproverSpec == "director" && *viewer == h.Director
	return &out, nil
}
