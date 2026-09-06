package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
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
	u, p := s.sessionAccess(r, row.SessionID)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if u == nil {
		// A task token identifies an agent attempt, and FR-5.3 gives the
		// response right to people. An agent answering its own question is the
		// loop HITL exists to break.
		writeProblem(w, apperr.Forbidden("task_token_scope", "a HITL request is answered by a person (FR-5.3)"))
		return
	}
	sess, serr := loadHitlSession(r.Context(), s.DB, row.SessionID)
	if serr != nil {
		writeErr(w, serr)
		return
	}
	now := s.Clock.Now()
	az := hitl.Authorize(hitl.AuthzInput{
		Spec: row.ApproverSpec, Director: sess.Director, Deputy: derefUUID(sess.Deputy),
		Responder: u.Id, IsMember: true,
		Elapsed: now.Sub(row.CreatedAt), DueIn: row.DueAt.Sub(row.CreatedAt),
	})
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
	if p := validateHitlResponse(row, in); p != nil {
		writeProblem(w, p)
		return
	}
	disabled, derr := s.agentDisabledFor(r.Context(), row.TaskID)
	if derr != nil {
		writeErr(w, derr)
		return
	}
	plan := hitl.PlanRespond(hitl.RespondInput{
		Kind: row.Type, Status: row.Status, Authz: az,
		Approved: in.Approved, Answer: derefString(in.Answer), Reason: derefString(in.Reason),
		AgentDisabled: disabled,
	})
	if !plan.Accepted && plan.ErrorCode == hitl.CodeForbidden {
		writeProblem(w, forbiddenRespond(row, plan, now))
		return
	}
	if in.TimeExtension != nil {
		// The `time` purpose is issued by the session time limit, which is not
		// part of T-S5 (P3_TASKS §2 names budget). Refusing is the honest
		// answer: storing an extension nothing reads would look implemented.
		notImplemented(w, r, "RespondHitlRequest time_extension (session time limit is not in the P3 server slice)")
		return
	}
	if in.BudgetOverrideUsd != nil && (row.Purpose == nil || *row.Purpose != hitl.PurposeBudget) {
		writeProblem(w, apperr.Validation(apperr.Field("budget_override_usd", "not_applicable",
			"budget_override_usd applies to the budget HITL only (FR-7.3 C2′)")))
		return
	}
	if isSessionBudgetApproval(row, in) {
		// K-10: this answer RESUMES the session, so it carries the new limit —
		// and it is refused here for the same reason resumeSession refuses it,
		// because coming back on a limit the session has already spent
		// re-trips the pause on the next usage report and the Director sees
		// the identical card again, having changed nothing (FR-7.3, openapi
		// resumeSession `limits.budget_usd`).
		//
		// The demand is made only while the session is actually stopped for
		// budget. The same request can be answered after someone has already
		// resumed the session by hand, and asking for a raise then would
		// refuse an answer that has nothing left to lift.
		var status string
		var reason *string
		var spent float64
		// The spend compared against is the one ENFORCEMENT reads — the live
		// sum over task_usage — and not only `session.cost_usd`, which is
		// written by the finish roll-up. An estimated overrun is found on a
		// HEARTBEAT (S-48), before any finish, so the stored column is still
		// $0.00 there and a $0.01 raise would pass this guard and re-trip the
		// pause on the very next heartbeat.
		if err := s.DB.QueryRow(r.Context(), `
			SELECT s.status::text, s.paused_reason::text FROM session s WHERE s.id = $1`, row.SessionID).
			Scan(&status, &reason); err != nil {
			writeErr(w, err)
			return
		}
		// S-49: one definition of "이미 쓴 돈" for both budget-raise handlers.
		spent, err := sessions.SpentUSD(r.Context(), s.DB, row.SessionID)
		if err != nil {
			writeErr(w, err)
			return
		}
		if status == "paused" && derefString(reason) == sessions.PauseBudget {
			switch {
			case in.BudgetOverrideUsd == nil:
				writeProblem(w, apperr.Validation(apperr.Field("budget_override_usd", "required",
					"세션이 예산으로 멈춰 있습니다 — 승인은 새 세션 상한을 함께 받습니다 (승인이 곧 재개, K-10)")))
				return
			case float64(*in.BudgetOverrideUsd) <= spent:
				writeErr(w, sessions.BudgetTooLowError("budget_override_usd", spent))
				return
			}
		}
	}
	if row.isCompletionApproval() {
		// The P2 branch (contracts PR #101): the platform's own approval for a
		// `user_approval` completion condition — E6-03·E6-04. It folds into the
		// session's completion state instead of re-queueing a task.
		s.idempotent(r.Context(), w, "user:"+u.Id.String(), params.IdempotencyKey.String(), requestHash(r, body),
			func() (int, any, *Problem) {
				return s.answerCompletionApproval(r.Context(), hitlRequestId, u.Id, *in.Approved, derefString(in.Reason))
			})
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), params.IdempotencyKey.String(), requestHash(r, body),
		func() (int, any, *Problem) {
			return s.answerAgentHitl(r.Context(), row, sess, u.Id, in, plan)
		})
}

// validateHitlResponse is the per-type body rule (openapi HitlResponse: "타입에
// 맞는 필드만 쓴다"). A question answered with `approved` and an approval
// answered with `answer` are both requests the resume prompt cannot render.
func validateHitlResponse(row *hitlRow, in gen.HitlResponse) *Problem {
	switch row.Type {
	case hitl.KindApproval:
		if in.Approved == nil {
			return apperr.Validation(apperr.Field("approved", "required",
				"an approval request is answered with approved: true or false"))
		}
		if !*in.Approved && strings.TrimSpace(derefString(in.Reason)) == "" {
			// E6-04: the rejection reason is the decision record. Without it
			// the log says a decision was made and not why.
			return apperr.Validation(apperr.Field("reason", "required",
				"a rejection records its reason in the decision log (E6-04)"))
		}
	default:
		if strings.TrimSpace(derefString(in.Answer)) == "" {
			return apperr.Validation(apperr.Field("answer", "required",
				"a question, choice or info request is answered with `answer`"))
		}
	}
	return nil
}

// forbiddenRespond builds the 403 the greyed-out button reads. The deputy gets
// the instant they become eligible; a plain member gets null, because a time
// there would promise a right that never arrives (E7-11, openapi
// Problem.can_respond_from).
func forbiddenRespond(row *hitlRow, plan hitl.RespondPlan, now time.Time) *Problem {
	if plan.CanRespondFrom == nil {
		p := apperr.Forbidden("not_approver", "이 요청에 응답할 권한이 없습니다 (FR-5.3)")
		p.Extra = map[string]any{"can_respond_from": nil}
		return p
	}
	at := row.CreatedAt.Add(*plan.CanRespondFrom).UTC()
	p := apperr.Forbidden("deputy_not_yet",
		fmt.Sprintf("Director 응답 대기 중 · %s부터 승인 가능", at.Format("15:04")))
	p.Extra = map[string]any{"can_respond_from": at}
	return p
}

// agentDisabledFor is FR-1.9 M8's premise: the owner switched `respond_to` to
// `nobody` while the request was open (E10-08).
func (s *Server) agentDisabledFor(ctx context.Context, taskID *uuid.UUID) (bool, error) {
	if taskID == nil {
		return false, nil
	}
	var disabled bool
	err := s.DB.QueryRow(ctx, `
		SELECT a.respond_to = 'nobody' OR a.archived_at IS NOT NULL
		FROM task t JOIN agent a ON a.id = t.agent_id WHERE t.id = $1`, *taskID).Scan(&disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return disabled, err
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// answerAgentHitl records a response to an agent-issued request (or to the
// platform's budget request) and starts the next attempt.
//
// The order is the same one answerCompletionApproval explains: the answer is
// written first because it is the record of what a person did, and only then
// is the task moved. An unrecorded answer that re-queued the task would leave
// the agent resuming with a question it can no longer read.
func (s *Server) answerAgentHitl(ctx context.Context, row *hitlRow, sess *hitlSession, userID uuid.UUID, in gen.HitlResponse, plan hitl.RespondPlan) (int, any, *Problem) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var status string
	if err := tx.QueryRow(ctx, `SELECT status::text FROM hitl_request WHERE id = $1 FOR UPDATE`, row.ID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, apperr.NotFound("hitl_request")
		}
		return 0, nil, apperr.Internal(err)
	}
	if status != string(gen.HitlStatusOpen) {
		// E7-08: not an error. The caller gets the answer that stands.
		_ = tx.Rollback(ctx)
		out, err := s.hitlAPI(ctx, s.DB, row.ID, &userID)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusOK, map[string]any{"hitl_request": out, "ignored": true, "decision_id": nil}, nil
	}

	answer := strings.TrimSpace(derefString(in.Answer))
	reason := strings.TrimSpace(derefString(in.Reason))
	stored := answer
	if stored == "" {
		stored = reason
	}
	var override *float64
	if in.BudgetOverrideUsd != nil && in.Approved != nil && *in.Approved {
		v := float64(*in.BudgetOverrideUsd)
		override = &v
	}
	if _, err := tx.Exec(ctx, `
		UPDATE hitl_request SET status = 'answered', approved = $2, answer = NULLIF($3, ''), answered_by = $4,
		       answered_at = $5, budget_override_usd = $6, requeue_held = $7
		WHERE id = $1`, row.ID, in.Approved, stored, userID, now, override, plan.RequeueHeld); err != nil {
		return 0, nil, apperr.Internal(err)
	}
	// FR-5.2: exactly one decision record per answer. `auto` stays false — a
	// person answered (E7-12 is the other half).
	decisionID, err := insertDecision(ctx, tx, row.SessionID, hitlDecisionSummary(row, in, stored), reason, "hitl", &row.ID, false, now)
	if err != nil {
		return 0, nil, apperr.Internal(err)
	}
	// The inbox item is resolved by the response, not by reading it
	// (openapi markInboxRead).
	if _, err := tx.Exec(ctx, `UPDATE inbox_item SET read_at = COALESCE(read_at, $2) WHERE ref_id = $1`, row.ID, now); err != nil {
		return 0, nil, apperr.Internal(err)
	}
	if override != nil && row.TaskID != nil {
		// FR-7.3 C2′: the raise is scoped to THIS task. The agent's
		// budget_per_task is untouched, or one click re-prices every future
		// session (E9-02).
		if _, err := tx.Exec(ctx, `UPDATE task SET budget_override = $2, updated_at = $3 WHERE id = $1`, *row.TaskID, *override, now); err != nil {
			return 0, nil, apperr.Internal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, apperr.Internal(err)
	}

	// The re-queue is a separate transaction on purpose: it locks the task
	// row, and holding both locks in this order while the budget path also
	// locks task-then-hitl is how the two deadlock.
	var taskOut *gen.Task
	resume := plan.TaskStatus == "queued"
	cause := tasks.CauseHitlAnswer
	if row.Purpose != nil && *row.Purpose == hitl.PurposeBudget {
		// E9-03: a REJECTED raise does not resume anything — the task stays
		// parked at paused(budget) until a person presses 중단. Only the
		// approval is a trigger (E9-02), and PlanBudgetAnswer is what says
		// which of the two this is.
		cause = tasks.CauseBudgetApproved
		br := sessions.PlanBudgetAnswer(sessions.BudgetAnswerInput{
			Approved: in.Approved != nil && *in.Approved,
			TaskID:   derefUUID(row.TaskID), LaneID: derefUUID(row.LaneID),
		})
		resume = resume && br.TaskStatus == "queued"
	}
	if row.TaskID != nil && resume {
		if _, err := s.Tasks.ResumeFromHuman(ctx, *row.TaskID, cause); err != nil {
			return 0, nil, apperr.As(err)
		}
		t, err := tasks.Get(ctx, s.DB, *row.TaskID)
		if err == nil {
			api := tasks.ToAPI(t, nil, nil)
			taskOut = &api
			// S-44: ResumeFromHuman is a no-op for a task that is already
			// terminal, and the post-turn budget pause names exactly such a
			// task — the overrun was found after the turn ended. What the
			// approval has to lift there is the LANE gate, or the raise buys
			// nothing and the lane never dispatches again. The override the
			// answer just stored carries along this lane
			// (tasks.LaneBudgetOverride), so the next task starts with the
			// limit the Director actually approved.
			if cause == tasks.CauseBudgetApproved && tasks.Terminal(t.Status) {
				if err := s.Tasks.ResumeLaneForBudget(ctx, t.LaneID, now); err != nil {
					return 0, nil, apperr.As(err)
				}
			}
		}
		s.Queue.Notifier.Notify()
	}
	if isSessionBudgetApproval(row, in) && in.BudgetOverrideUsd != nil {
		// K-10: a SESSION-scoped budget request (task_id NULL) is answered by
		// resuming the session. Before this the answer marked the request
		// `answered` and stopped: the session stayed `paused(budget)`, its
		// parked tasks stayed `paused`, and the Director had to go and call
		// resumeSession as a second step — with the raise they had just
		// approved not stored anywhere the resume would read, so that second
		// step was refused as `limits.budget_usd too_low` unless they typed
		// the number a second time.
		if err := s.resumeSessionForBudget(ctx, row.SessionID, float64(*in.BudgetOverrideUsd), now); err != nil {
			return 0, nil, apperr.As(err)
		}
		s.Queue.Notifier.Notify()
		// publishSession renders the session for a viewer; the responder is
		// the one person we know is looking at it.
		s.publishSession(ctx, sess.WorkspaceID, row.SessionID, &gen.User{Id: userID})
	}
	s.publishHitl(ctx, sess.WorkspaceID, row.SessionID, row.ID, "hitl.updated")
	out, err := s.hitlAPI(ctx, s.DB, row.ID, &userID)
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	resp := map[string]any{"hitl_request": out, "ignored": false, "decision_id": decisionID}
	if taskOut != nil {
		resp["task"] = taskOut
	}
	return http.StatusOK, resp, nil
}

// isSessionBudgetApproval is K-10's gate: an APPROVED, session-scoped
// (`task_id` empty) budget request. The three parts each carry weight —
// `purpose` because `source: system` + `approval` is shared by the completion
// approval and the loop pause (0012), `task_id` empty because a task-scoped
// raise resumes that task and leaves the session alone (E9-02), and `approved`
// because a rejection keeps the session `paused` (E9-03, contract: "거절은
// paused 유지").
func isSessionBudgetApproval(row *hitlRow, in gen.HitlResponse) bool {
	return row.Purpose != nil && *row.Purpose == hitl.PurposeBudget && row.TaskID == nil &&
		in.Approved != nil && *in.Approved
}

// resumeSessionForBudget is the session half of the approval: the same four
// steps ResumeSession runs for a `budget` pause, minus the ones that belong to
// another pause reason (loop counters) and minus closing the system request —
// answerAgentHitl has already answered it, which is the whole point of K-10.
//
// It is a separate transaction from the answer for the reason answerAgentHitl
// gives about the re-queue: this one locks the session and then its tasks,
// while the budget answer path locks task-then-hitl, and holding both orders
// at once is how the two deadlock.
func (s *Server) resumeSessionForBudget(ctx context.Context, sessionID uuid.UUID, raise float64, now time.Time) error {
	return s.inSessionTx(ctx, func(tx pgx.Tx) error {
		var status string
		var reason *string
		var limitsRaw []byte
		if err := tx.QueryRow(ctx, `
			SELECT status::text, paused_reason::text, limits FROM session WHERE id = $1 FOR UPDATE`, sessionID).
			Scan(&status, &reason, &limitsRaw); err != nil {
			return err
		}
		if status != "paused" || derefString(reason) != sessions.PauseBudget {
			// The pause was already lifted (a concurrent resumeSession), or
			// the session stopped for another reason after the request was
			// raised. Either way this answer is not the one that resumes it —
			// and forcing `active` here would undo that other pause.
			return nil
		}
		// The contract's "세션 잔여 상한 = 승인 금액": the raise IS the new
		// session budget, not an addition to it, which is what makes
		// `budget_override_usd` mean the same thing here as it does for a task
		// (FR-7.3 C2′ — an override REPLACES the limit).
		budget := float32(raise)
		merged, err := mergeLimits(limitsRaw, &gen.SessionLimits{BudgetUsd: nullable.NewNullableWithValue(budget)})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE session SET limits = $2, status = 'active', paused_reason = NULL, paused_detail = NULL, updated_at = $3
			WHERE id = $1`, sessionID, merged, now); err != nil {
			return err
		}
		// S-46's re-queue, reused: the pause PARKED the turns it cancelled, so
		// a session that comes back `active` with its tasks still `paused` has
		// resumed nothing.
		if _, err := s.Tasks.ResumeSessionTasks(ctx, tx, sessionID, sessions.PauseBudget, tasks.CauseBudgetApproved, now); err != nil {
			return err
		}
		// S-44's lane gate: the claim query refuses a paused lane, so a lane
		// left `paused` never dispatches again. Only lanes that hold a queued
		// task come back — one whose only task stayed parked has nothing to
		// hand out.
		_, err = tx.Exec(ctx, `
			UPDATE lane l SET status = 'queued', finished_at = NULL, updated_at = $2
			WHERE l.session_id = $1 AND l.status = 'paused'
			  AND EXISTS (SELECT 1 FROM task t WHERE t.lane_id = l.id AND t.status = 'queued')`,
			sessionID, now)
		return err
	})
}

// hitlDecisionSummary is the one line the decision log carries.
func hitlDecisionSummary(row *hitlRow, in gen.HitlResponse, stored string) string {
	switch row.Type {
	case hitl.KindApproval:
		if in.Approved != nil && *in.Approved {
			return "승인: " + row.Question
		}
		return "거절: " + row.Question
	default:
		return row.Question + " → " + stored
	}
}

// insertDecision writes one decision row. `auto` separates a human answer from
// an expiry that proceeded with the agent's proposal (openapi Decision.auto,
// E7-12); it is a column rather than a convention because the two read
// identically in the log otherwise.
func insertDecision(ctx context.Context, q db.DBTX, sessionID uuid.UUID, summary, rationale, source string, refID *uuid.UUID, auto bool, now time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	var rat *string
	if rationale != "" {
		rat = &rationale
	}
	err := q.QueryRow(ctx, `
		INSERT INTO decision (session_id, summary, rationale, source, ref_id, auto, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		sessionID, summary, rat, source, refID, auto, now).Scan(&id)
	return id, err
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
	// The inbox item is resolved by the RESPONSE, not by reading it (openapi
	// markInboxRead). Leaving it unread keeps an action_required row — and the
	// nav badge — on a request that has already been decided (S-33).
	if _, err := tx.Exec(ctx, `UPDATE inbox_item SET read_at = COALESCE(read_at, $2) WHERE ref_id = $1`, hitlID, now); err != nil {
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
	Context         *string
	Options         []string
	ProposedDefault *string
	ApproverSpec    string
	Purpose         *string
	ArtifactID      *uuid.UUID
	BudgetOverride  *float64
	MessageID       *uuid.UUID
	RequeueHeld     bool
	DueAt           time.Time
	Overdue         bool
	Status          string
	Approved        *bool
	Answer          *string
	AnsweredBy      *uuid.UUID
	AnsweredAt      *time.Time
	CreatedAt       time.Time
	Director        uuid.UUID
	Deputy          *uuid.UUID
	TaskStatus      *string
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
		       h.source::text, h.type::text, h.question, h.context, h.options, h.proposed_default,
		       h.approver_spec, h.purpose, h.artifact_id, h.budget_override_usd, h.message_id, h.requeue_held,
		       h.due_at, h.overdue, h.status::text,
		       h.approved, h.answer, h.answered_by, h.answered_at, h.created_at,
		       s.director_user_id, s.deputy_director_user_id, t.status::text
		FROM hitl_request h
		JOIN session s ON s.id = h.session_id
		LEFT JOIN task t ON t.id = h.task_id
		LEFT JOIN agent a ON a.id = t.agent_id
		WHERE h.id = $1`, id).
		Scan(&h.ID, &h.SessionID, &h.TaskID, &h.LaneID, &h.AgentID, &h.AgentName,
			&h.Source, &h.Type, &h.Question, &h.Context, &h.Options, &h.ProposedDefault,
			&h.ApproverSpec, &h.Purpose, &h.ArtifactID, &h.BudgetOverride, &h.MessageID, &h.RequeueHeld,
			&h.DueAt, &h.Overdue, &h.Status,
			&h.Approved, &h.Answer, &h.AnsweredBy, &h.AnsweredAt, &h.CreatedAt,
			&h.Director, &h.Deputy, &h.TaskStatus)
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
	out.Context = tasks.NullString(h.Context)
	out.ArtifactId = tasks.NullUUID(h.ArtifactID)
	out.MessageId = tasks.NullUUID(h.MessageID)
	if h.BudgetOverride != nil {
		out.BudgetOverrideUsd = nullable.NewNullableWithValue(float32(*h.BudgetOverride))
	} else {
		out.BudgetOverrideUsd = nullable.NewNullNullable[float32]()
	}
	// can_respond / can_respond_from are the SAME judgement the handler makes
	// (hitl.Authorize), so the button never says "you can" to someone the
	// handler will refuse, and the greyed-out tooltip carries the instant the
	// deputy becomes eligible (E7-09, E7-11).
	out.CanRespondFrom = nullable.NewNullNullable[time.Time]()
	if h.Status == string(gen.HitlStatusOpen) && viewer != nil {
		az := hitl.Authorize(hitl.AuthzInput{
			Spec: h.ApproverSpec, Director: h.Director, Deputy: derefUUID(h.Deputy),
			Responder: *viewer, IsMember: true,
			Elapsed: now.Sub(h.CreatedAt), DueIn: h.DueAt.Sub(h.CreatedAt),
		})
		out.CanRespond = az.Allowed
		if az.CanRespondFrom != nil {
			out.CanRespondFrom = nullable.NewNullableWithValue(h.CreatedAt.Add(*az.CanRespondFrom).UTC())
		}
	}
	return &out, nil
}
