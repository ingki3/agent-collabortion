package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Session-level control (openapi updateSession · pauseSession · resumeSession ·
// cancelSession · changeDirector · add/update/removeParticipant), FR-2.1·2.3.

// sessionControl is "the Director, and for the three policy pauses also the
// deputy" (SCREEN §4.5). It is not sessionDirector: that one deliberately
// excludes the deputy because ending a session is not undoable, and resuming a
// budget pause is (FR-3.4 t-3's reasoning, applied to the pause table).
func (s *Server) sessionControl(r *http.Request, sessionID uuid.UUID, allowDeputy bool) (*gen.User, uuid.UUID, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, uuid.Nil, p
	}
	var wsID, director uuid.UUID
	var deputy *uuid.UUID
	err := s.DB.QueryRow(r.Context(), `
		SELECT workspace_id, director_user_id, deputy_director_user_id FROM session WHERE id = $1`, sessionID).
		Scan(&wsID, &director, &deputy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uuid.Nil, apperr.NotFound("session")
	}
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	m, err := s.Auth.Member(r.Context(), wsID, u.Id)
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	if m == nil {
		return nil, uuid.Nil, apperr.NotFound("session")
	}
	if u.Id == director {
		return u, wsID, nil
	}
	if allowDeputy && deputy != nil && u.Id == *deputy {
		return u, wsID, nil
	}
	return nil, uuid.Nil, apperr.Forbidden("director_required", "Director 권한이 필요합니다")
}

func (s *Server) sessionOut(ctx context.Context, w http.ResponseWriter, sessionID uuid.UUID, u *gen.User) {
	out, err := s.Sessions.Get(ctx, sessionID, sessions.Viewer{UserID: &u.Id})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// publishSession emits `session.updated` (S5·S7's banner reads status,
// paused_reason and paused_detail from it).
func (s *Server) publishSession(ctx context.Context, wsID, sessionID uuid.UUID, u *gen.User) {
	if s.Hub == nil {
		return
	}
	out, err := s.Sessions.Get(ctx, sessionID, sessions.Viewer{UserID: &u.Id})
	if err != nil {
		return
	}
	sid := sessionID
	_ = s.Hub.Publish(ctx, s.DB, wsID, &sid, "session.updated", out)
}

// ---------------------------------------------------------------------------
// pause · resume · cancel
// ---------------------------------------------------------------------------

func (s *Server) PauseSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, wsID, p := s.sessionControl(r, sessionId, false)
	if p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(r.Context(), `SELECT status::text FROM session WHERE id = $1 FOR UPDATE`, sessionId).Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			return apperr.Conflict("invalid_transition", "active 세션만 일시정지할 수 있습니다 (현재: "+status+")")
		}
		detail := tasks.PausedDetail(sessions.PauseDirector, now)
		raw, _ := json.Marshal(detail)
		if _, err := tx.Exec(r.Context(), `
			UPDATE session SET status = 'paused', paused_reason = 'director', paused_detail = $2, updated_at = $3
			WHERE id = $1`, sessionId, raw, now); err != nil {
			return err
		}
		// FR-2.3: a Director pause DRAINS. PauseSessionTasks reads
		// tasks.PlanDispatch, which is where "director → drain, budget → cancel"
		// lives, so the running turn is left alone here (E5-06) and the claim
		// query's `s.status = 'active'` guard stops anything new (C3′).
		return s.Tasks.PauseSessionTasks(r.Context(), tx, sessionId, sessions.PauseDirector, raw, now)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.publishSession(r.Context(), wsID, sessionId, u)
	s.sessionOut(r.Context(), w, sessionId, u)
}

func (s *Server) ResumeSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	var in gen.ResumeSessionJSONBody
	if r.ContentLength > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	var reason *string
	if err := s.DB.QueryRow(r.Context(), `SELECT paused_reason::text FROM session WHERE id = $1`, sessionId).Scan(&reason); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, apperr.NotFound("session"))
		} else {
			writeErr(w, err)
		}
		return
	}
	rule := sessions.PlanResume(derefString(reason))
	u, wsID, p := s.sessionControl(r, sessionId, rule.DeputyMayResume)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if !rule.Resumable {
		writeProblem(w, apperr.Conflict("runtime_offline", rule.Hint))
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var status string
		var limitsRaw []byte
		if err := tx.QueryRow(r.Context(), `SELECT status::text, limits FROM session WHERE id = $1 FOR UPDATE`, sessionId).
			Scan(&status, &limitsRaw); err != nil {
			return err
		}
		// S-49: the same reading the K-10 approval uses. `cost_usd` alone lags
		// the per-task rollup, so this path used to accept a "raise" the
		// session had already spent past.
		spent, err := sessions.SpentUSD(r.Context(), tx, sessionId)
		if err != nil {
			return err
		}
		if status != "paused" {
			return apperr.Conflict("invalid_transition", "paused 세션만 재개할 수 있습니다 (현재: "+status+")")
		}
		if in.Limits != nil {
			merged, err := mergeLimits(limitsRaw, in.Limits)
			if err != nil {
				return err
			}
			limitsRaw = merged
			if _, err := tx.Exec(r.Context(), `UPDATE session SET limits = $2 WHERE id = $1`, sessionId, limitsRaw); err != nil {
				return err
			}
		}
		if rule.RequiresHigherLimit {
			// Resuming a budget pause on the old limit re-trips it on the very
			// next usage report, and the Director sees the banner again with
			// nothing changed (FR-7.3).
			if lim := budgetOf(limitsRaw); lim > 0 && lim <= spent {
				return sessions.BudgetTooLowError("limits.budget_usd", spent)
			}
		}
		if rule.ResetLoopCounters {
			reset := true
			if in.ResetLoopCounters != nil {
				reset = *in.ResetLoopCounters
			}
			if reset {
				// FR-3.5: without the reset the next message re-trips the same
				// counter and the session pauses again immediately.
				if _, err := tx.Exec(r.Context(), `DELETE FROM session_hop WHERE session_id = $1`, sessionId); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE session SET status = 'active', paused_reason = NULL, paused_detail = NULL, updated_at = $2
			WHERE id = $1`, sessionId, now); err != nil {
			return err
		}
		// S-46: the pause PARKED the turns it cancelled (§8.2.2 — a budget or
		// time pause does not drain), so resuming the session has to put those
		// tasks back in the queue. Without this the session returned to
		// `active` with every task it had stopped still `paused`, and no
		// endpoint anywhere moved them again: FR-2.3's 재개 lost the work
		// instead of continuing it. The re-queue is a new attempt on the same
		// lane and workdir, resume tried first (E9-02).
		//
		// It runs BEFORE the lane sweep below, so a lane whose only task was
		// parked is caught by that sweep's `EXISTS ... status = 'queued'`.
		resumeCause := tasks.CauseHitlAnswer
		if derefString(reason) == sessions.PauseBudget {
			// The budget HITL this resume answers is the session-scoped one
			// (task_id NULL, openapi resumeSession), so the attempt starts for
			// the same reason an approved per-task raise does.
			resumeCause = tasks.CauseBudgetApproved
		}
		if _, err := s.Tasks.ResumeSessionTasks(r.Context(), tx, sessionId, derefString(reason), resumeCause, now); err != nil {
			return err
		}
		// S-44: the pause parked the lanes it stopped (tasks.pauseLocked), and
		// since the claim query refuses a paused lane the resume has to lift
		// that too — C3′ is "재개 시 큐 순서대로 dispatch", and a lane left at
		// paused never dispatches again. Only lanes that still hold a QUEUED
		// task come back: a lane whose only task is `paused(budget)` and stayed
		// parked above (its own budget request is open or was refused) has
		// nothing to hand out, and saying `queued` there would be a card that
		// claims work is waiting when the task is still parked.
		if _, err := tx.Exec(r.Context(), `
			UPDATE lane l SET status = 'queued', finished_at = NULL, updated_at = $2
			WHERE l.session_id = $1 AND l.status = 'paused'
			  AND EXISTS (SELECT 1 FROM task t WHERE t.lane_id = l.id AND t.status = 'queued')`,
			sessionId, now); err != nil {
			return err
		}
		if rule.ClosesSystemHitl {
			// openapi resumeSession: resuming IS the answer to the system HITL
			// the pause issued. Closing it with `answered(approved)` keeps the
			// Director's inbox honest — the request really was decided.
			if err := s.closeSessionBudgetHitl(r.Context(), tx, sessionId, u.Id, derefString(reason), now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.Queue.Notifier.Notify()
	s.publishSession(r.Context(), wsID, sessionId, u)
	s.sessionOut(r.Context(), w, sessionId, u)
}

// closeSessionBudgetHitl answers the platform's own pause request. It is
// `answered` rather than `cancelled` (K-4's state) because a person did decide:
// they pressed 재개.
func (s *Server) closeSessionBudgetHitl(ctx context.Context, tx pgx.Tx, sessionID, userID uuid.UUID, purpose string, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT id, question FROM hitl_request
		WHERE session_id = $1 AND status = 'open' AND source = 'system' AND task_id IS NULL AND purpose = $2`,
		sessionID, purpose)
	if err != nil {
		return err
	}
	type row struct {
		id uuid.UUID
		q  string
	}
	var open []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.q); err != nil {
			rows.Close()
			return err
		}
		open = append(open, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range open {
		if _, err := tx.Exec(ctx, `
			UPDATE hitl_request SET status = 'answered', approved = true, answered_by = $2, answered_at = $3
			WHERE id = $1`, o.id, userID, now); err != nil {
			return err
		}
		if _, err := insertDecision(ctx, tx, sessionID, "세션 재개 승인: "+o.q, "", "hitl", &o.id, false, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE inbox_item SET read_at = COALESCE(read_at, $2) WHERE ref_id = $1`, o.id, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) CancelSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, wsID, p := s.sessionControl(r, sessionId, false)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		if p := decodeJSON(w, r, &in); p != nil {
			writeProblem(w, p)
			return
		}
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var status string
		var pauseReason *string
		if err := tx.QueryRow(r.Context(), `SELECT status::text, paused_reason::text FROM session WHERE id = $1 FOR UPDATE`, sessionId).
			Scan(&status, &pauseReason); err != nil {
			return err
		}
		if status != "active" && status != "paused" {
			return apperr.Conflict("invalid_transition", "active·paused 세션만 취소할 수 있습니다 (현재: "+status+")")
		}
		offline := status == "paused" && derefString(pauseReason) == runtimes.PauseReasonOffline
		if offline {
			// E14-07: the Director chose "종료" over rebinding. The state is
			// `cancelled`, never `completed` — the goal was never met, and
			// filing a machine outage in the success column would also trigger
			// FR-2.4's summary of a job that was never finished. The artifacts
			// are recovered by having been on the server all along (FR-9.2
			// "아티팩트만 회수한다"), which is why nothing here fetches them.
			var artifacts int
			if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM artifact WHERE session_id = $1`, sessionId).Scan(&artifacts); err != nil {
				return err
			}
			end := runtimes.PlanOfflineEnd(artifacts)
			if end.SessionState != "cancelled" || end.CompletionConditionsMet {
				return apperr.Internal(fmt.Errorf("runtimes: offline end plan = %+v", end))
			}
			// The dead machine's directories are unreachable. Leaving them
			// `active` makes the GC sweep ask a runtime that will never answer,
			// forever.
			if _, err := tx.Exec(r.Context(), `
				UPDATE workdir SET status = 'retained', gc_blocked_reason = 'runtime_gone', updated_at = $2
				WHERE session_id = $1 AND status = 'active'`, sessionId, now); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `
				INSERT INTO decision (session_id, summary, rationale, source, created_at)
				VALUES ($1, $2, $3, 'hitl', $4)`,
				sessionId, "런타임이 돌아오지 않아 세션을 종료했습니다",
				fmt.Sprintf("재바인딩 대신 종료를 선택했습니다 — 아티팩트 %d개는 서버에 남아 있습니다 (FR-9.2, E14-07)", end.ArtifactsRecovered),
				now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE session SET status = 'cancelled', paused_reason = NULL, paused_detail = NULL,
			       finished_at = $2, updated_at = $2 WHERE id = $1`, sessionId, now); err != nil {
			return err
		}
		return s.cancelSessionWork(r.Context(), tx, sessionId, now)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.publishSession(r.Context(), wsID, sessionId, u)
	s.sessionOut(r.Context(), w, sessionId, u)
}

// cancelSessionWork ends every task the session still holds: queued ones at
// once, in-flight ones through the §8.2.2 procedure (a `cancel` command the
// daemon carries out — never an immediate kill).
func (s *Server) cancelSessionWork(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT id FROM task WHERE session_id = $1
		  AND status IN ('deferred', 'queued', 'dispatched', 'preparing', 'running', 'waiting_human', 'paused')
		ORDER BY created_at`, sessionID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Tasks.CancelForSession(ctx, tx, id, "session_cancelled", now); err != nil {
			return err
		}
	}
	// K-4: the platform's own open requests lose their premise when the session
	// ends. They are closed `cancelled`, not answered — nobody decided them.
	return closeOrphanSystemHitl(ctx, tx, sessionID, now)
}

// closeOrphanSystemHitl is K-4 (Lead decision, 2026-09-06): a platform-issued
// request whose condition is no longer true is closed `cancelled` and taken out
// of the inbox. No decision is recorded — a decision that nobody made is the
// worst thing to leave in the log — and the web renders the card as 취소됨.
func closeOrphanSystemHitl(ctx context.Context, q pgx.Tx, sessionID uuid.UUID, now time.Time) error {
	rows, err := q.Query(ctx, `
		UPDATE hitl_request SET status = 'cancelled'
		WHERE session_id = $1 AND status = 'open' AND source = 'system'
		RETURNING id`, sessionID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := q.Exec(ctx, `DELETE FROM inbox_item WHERE ref_id = $1 AND type = $2::inbox_item_type`,
			id, inbox.TypeHitlRequest); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// updateSession · changeDirector · participants
// ---------------------------------------------------------------------------

func (s *Server) UpdateSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, wsID, p := s.sessionControl(r, sessionId, false)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.SessionUpdate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var status string
		var limitsRaw []byte
		if err := tx.QueryRow(r.Context(), `SELECT status::text, limits FROM session WHERE id = $1 FOR UPDATE`, sessionId).
			Scan(&status, &limitsRaw); err != nil {
			return err
		}
		draft := status == "draft"
		// The runtime and the isolation are fixed once work has started: the
		// workdirs are bound to both, and changing either would orphan them
		// (openapi updateSession, SCREEN §4.5).
		if !draft {
			var errs []apperr.FieldError
			if in.Isolation != nil {
				errs = append(errs, apperr.Field("isolation", "immutable", "격리는 draft에서만 바꿀 수 있습니다"))
			}
			if in.RuntimeId.IsSpecified() {
				errs = append(errs, apperr.Field("runtime_id", "immutable", "런타임은 draft에서만 바꿀 수 있습니다"))
			}
			if in.CompletionCondition != nil {
				errs = append(errs, apperr.Field("completion_condition", "immutable", "종료 조건은 draft에서만 바꿀 수 있습니다"))
			}
			if len(errs) > 0 {
				return apperr.Validation(errs...)
			}
		}
		set := []string{"updated_at = $2"}
		args := []any{sessionId, now}
		add := func(col string, v any) {
			args = append(args, v)
			set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
		}
		if in.Title != nil {
			add("title", *in.Title)
		}
		if in.Goal != nil {
			add("goal", *in.Goal)
		}
		if in.AcceptanceCriteria != nil {
			add("acceptance_criteria", *in.AcceptanceCriteria)
		}
		if in.Autonomy != nil {
			add("autonomy", string(*in.Autonomy))
		}
		if in.Limits != nil {
			merged, err := mergeLimits(limitsRaw, in.Limits)
			if err != nil {
				return err
			}
			add("limits", merged)
		}
		// S-32: an explicit null is an UNSET, and only a nullable type can tell
		// it from an omitted key. `deputy_director_user_id: null` clears the
		// deputy; leaving the key out keeps whoever is there.
		if in.DeputyDirectorUserId.IsSpecified() {
			if in.DeputyDirectorUserId.IsNull() {
				add("deputy_director_user_id", nil)
			} else {
				v, err := in.DeputyDirectorUserId.Get()
				if err != nil {
					return apperr.Validation(apperr.Field("deputy_director_user_id", "invalid", err.Error()))
				}
				if err := s.requireMember(r.Context(), tx, sessionId, v); err != nil {
					return err
				}
				add("deputy_director_user_id", v)
			}
		}
		if draft {
			if in.Isolation != nil {
				raw, _ := json.Marshal(in.Isolation)
				add("isolation", raw)
			}
			if in.CompletionCondition != nil {
				raw, _ := json.Marshal(in.CompletionCondition)
				add("completion_condition", raw)
			}
			if in.RuntimeId.IsSpecified() {
				if in.RuntimeId.IsNull() {
					add("runtime_id", nil)
				} else if v, err := in.RuntimeId.Get(); err == nil {
					add("runtime_id", v)
				}
			}
		}
		if len(set) == 1 {
			return nil
		}
		_, err := tx.Exec(r.Context(), `UPDATE session SET `+joinComma(set)+` WHERE id = $1`, args...)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.publishSession(r.Context(), wsID, sessionId, u)
	s.sessionOut(r.Context(), w, sessionId, u)
}

func (s *Server) ChangeDirector(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var wsID, director uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT workspace_id, director_user_id FROM session WHERE id = $1`, sessionId).
		Scan(&wsID, &director); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, apperr.NotFound("session"))
		} else {
			writeErr(w, err)
		}
		return
	}
	m, err := s.Auth.Member(r.Context(), wsID, u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	// t-5: the current Director hands over, and an owner/admin can do it for
	// them — a Director who leaves the company cannot hand over themselves.
	if m == nil || (u.Id != director && m.Role != "owner" && m.Role != "admin") {
		writeProblem(w, apperr.Forbidden("director_required", "현재 Director 또는 owner·admin만 교체할 수 있습니다"))
		return
	}
	var in struct {
		DirectorUserID uuid.UUID `json:"director_user_id"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err = s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		if err := s.requireMember(r.Context(), tx, sessionId, in.DirectorUserID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE session SET director_user_id = $2, updated_at = $3 WHERE id = $1`,
			sessionId, in.DirectorUserID, now); err != nil {
			return err
		}
		// The open `director` requests follow the role, not the person: an
		// approver_spec pointing at a Director who left is a request nobody can
		// answer (openapi changeDirector).
		rows, err := tx.Query(r.Context(), `
			SELECT id FROM hitl_request WHERE session_id = $1 AND status = 'open' AND approver_spec = 'director'`, sessionId)
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if _, err := tx.Exec(r.Context(), `
				INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
				SELECT mm.id, $4::inbox_item_type, $5::inbox_severity, $1, $2, $3
				FROM member mm WHERE mm.workspace_id = $6 AND mm.user_id = $7
				  AND NOT EXISTS (SELECT 1 FROM inbox_item i WHERE i.member_id = mm.id AND i.ref_id = $2)`,
				sessionId, id, now, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest), wsID, in.DirectorUserID); err != nil {
				return err
			}
		}
		if _, err := s.Router.SystemPost(r.Context(), tx, sessionId, "Director가 교체되었습니다."); err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `
			INSERT INTO activity_log (workspace_id, actor_user_id, action, target_type, target_id, created_at)
			VALUES ($1, $2, 'session.director_changed', 'session', $3, $4)`, wsID, u.Id, sessionId, now)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.publishSession(r.Context(), wsID, sessionId, u)
	s.sessionOut(r.Context(), w, sessionId, u)
}

func (s *Server) requireMember(ctx context.Context, q pgx.Tx, sessionID, userID uuid.UUID) error {
	var ok bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM member m JOIN session s ON s.workspace_id = m.workspace_id
		               WHERE s.id = $1 AND m.user_id = $2)`, sessionID, userID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return apperr.Validation(apperr.Field("user_id", "not_member", "워크스페이스 멤버가 아닙니다"))
	}
	return nil
}

func (s *Server) inSessionTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// mergeLimits folds a SessionLimits patch into the stored jsonb. A key the
// caller omitted keeps its value; an explicit null clears it (S-32).
func mergeLimits(stored []byte, patch *gen.SessionLimits) ([]byte, error) {
	cur := map[string]any{}
	if len(stored) > 0 {
		_ = json.Unmarshal(stored, &cur)
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	next := map[string]any{}
	if err := json.Unmarshal(raw, &next); err != nil {
		return nil, apperr.Internal(err)
	}
	for k, v := range next {
		cur[k] = v
	}
	out, err := json.Marshal(cur)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

func budgetOf(limits []byte) float64 {
	var l struct {
		BudgetUSD *float64 `json:"budget_usd"`
	}
	if len(limits) == 0 {
		return 0
	}
	_ = json.Unmarshal(limits, &l)
	if l.BudgetUSD == nil {
		return 0
	}
	return *l.BudgetUSD
}

var _ = contracts.FailCancelled
var _ = hitl.StatusCancelled
