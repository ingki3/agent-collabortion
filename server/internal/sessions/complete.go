package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// ParseTree reads session.completion_condition. The stored shape allows nested
// groups; P2 evaluates a single operator over the atoms it finds, because that
// is the only shape FR-2.2's UI ("체크박스 + 조합 방식 선택") can produce. A
// nested tree is flattened under its top operator rather than rejected, so a
// hand-written condition still evaluates instead of silently never completing.
func ParseTree(raw []byte) Tree {
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return Tree{}
	}
	t := Tree{Op: "AND"}
	var walk func(n any)
	walk = func(n any) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		if conds, ok := m["conditions"].([]any); ok {
			if op, ok := m["op"].(string); ok && t.Op == "AND" {
				t.Op = normOp(op)
			}
			for _, c := range conds {
				walk(c)
			}
			return
		}
		typ, _ := m["type"].(string)
		if typ == "" {
			return
		}
		c := Condition{Type: typ}
		if who, ok := m["who"].(string); ok {
			c.Who = who
		}
		if id, ok := m["agent_id"].(string); ok {
			if parsed, err := uuid.Parse(id); err == nil {
				c.Agent = &parsed
			}
		}
		t.Conditions = append(t.Conditions, c)
	}
	walk(node)
	return t
}

// ApplyCompletionEvent folds one event into a session's completion state and
// carries out the consequence: issuing the platform's user_approval request,
// pausing on a budget limit, or running the completing → completed step.
func (s *Service) ApplyCompletionEvent(ctx context.Context, sessionID uuid.UUID, ev Event) (*Outcome, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wsID uuid.UUID
	var status string
	var raw, metRaw, limitsRaw []byte
	var cost float64
	var assignee, director *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT workspace_id, status::text, completion_condition, completion_met, limits, cost_usd, assignee_agent_id, director_user_id
		FROM session WHERE id = $1 FOR UPDATE`, sessionID).
		Scan(&wsID, &status, &raw, &metRaw, &limitsRaw, &cost, &assignee, &director)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("session")
	}
	if err != nil {
		return nil, err
	}
	if status == "completed" || status == "cancelled" {
		return nil, apperr.Conflict("session_closed", "this session is already closed")
	}
	tree := ParseTree(raw)
	// "who: assignee" is resolved here, not at creation: the assignee can
	// change (changeDirector's sibling operation) and the condition should
	// follow it rather than pin the agent that happened to hold the role.
	for i := range tree.Conditions {
		if tree.Conditions[i].Agent == nil && tree.Conditions[i].Who == "assignee" {
			tree.Conditions[i].Agent = assignee
		}
	}
	st := State{Met: map[string]bool{}}
	_ = json.Unmarshal(metRaw, &st.Met)

	out := ApplyEvent(tree, st, ev)
	if out.CLIError != "" {
		return &out, nil
	}

	met := map[string]bool{}
	for _, a := range out.MetAtoms {
		met[a] = true
	}
	if _, err := tx.Exec(ctx, `UPDATE session SET completion_met = $2, updated_at = $3 WHERE id = $1`, sessionID, met, now); err != nil {
		return nil, err
	}

	if out.DecisionRecorded && out.RejectReason != "" {
		// FR-4.2: WHO decided is part of the record. A Director turning down
		// the completion HITL and a reviewer agent turning down an artifact are
		// both rejections, but the log must not read as if a person did both —
		// `source: agent` is what openapi reviewArtifact promises.
		summary, source := "Director가 완료를 거절했습니다", "hitl"
		if ev.Kind == "review_reject" {
			summary, source = "리뷰어가 아티팩트를 반려했습니다", "agent"
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO decision (session_id, summary, rationale, source, ref_id, created_at)
			VALUES ($1, $2, $3, $4::decision_source, $5, $6) RETURNING id`,
			sessionID, summary, out.RejectReason, source, ev.Ref, now).Scan(&out.DecisionID); err != nil {
			return nil, fmt.Errorf("sessions: reject decision: %w", err)
		}
		s.publishDecision(ctx, tx, wsID, sessionID, out.DecisionID)
	}

	switch out.SessionState {
	case "paused":
		detail := tasks.PausedDetail(out.PauseReason, now)
		if out.PauseReason == "budget" {
			detail = tasks.WithBudget(detail, float32(budgetLimit(limitsRaw)), float32(cost))
		}
		if _, err := tx.Exec(ctx, `
			UPDATE session SET status = 'paused', paused_reason = $2, paused_detail = $3, updated_at = $4 WHERE id = $1`,
			sessionID, out.PauseReason, detail, now); err != nil {
			return nil, err
		}
		// FR-2.3 / §8.2.2: a budget pause CANCELS the turn in flight. Letting it
		// finish spends exactly the money the pause exists to stop.
		if s.Tasks != nil {
			if err := s.Tasks.PauseSessionTasks(ctx, tx, sessionID, out.PauseReason, nil, now); err != nil {
				return nil, err
			}
		}
	case "completed":
		// active → completing → completed. The intermediate state is real: no
		// new task dispatches while the summary runs (FR-2.3, E5-08).
		if _, err := tx.Exec(ctx, `
			UPDATE session SET status = 'completing', updated_at = $2 WHERE id = $1`, sessionID, now); err != nil {
			return nil, err
		}
		// FR-2.4 with a model on the path (P4, §8.5). `alreadyPosted` is read
		// FIRST because it changes what a success means: the summariser runs
		// behind the completing transition and a retry after a timeout is the
		// normal way it is called twice — two summaries in a timeline are
		// indistinguishable and the reader cannot tell which is current.
		var alreadyPosted bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM message WHERE session_id = $1 AND kind = 'summary')`,
			sessionID).Scan(&alreadyPosted); err != nil {
			return nil, fmt.Errorf("sessions: summary presence: %w", err)
		}
		summary := s.summarise(ctx, tx, sessionID, alreadyPosted)
		if summary.Post {
			var msgID uuid.UUID
			// The WHERE NOT EXISTS is the actual "exactly one" guard. The read
			// above decides what to ASK the model for; two ApplyCompletionEvent
			// calls racing on the same session would both pass it, and only the
			// insert can settle which one wins.
			err := tx.QueryRow(ctx, `
				INSERT INTO message (session_id, author_type, author_id, content, kind, created_at)
				SELECT $1, 'system', NULL, $2, 'summary', $3
				WHERE NOT EXISTS (SELECT 1 FROM message WHERE session_id = $1 AND kind = 'summary')
				RETURNING id`, sessionID, summary.Body, now).Scan(&msgID)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				// Somebody else got there first. Not an error: the session has
				// its one summary, which is the whole rule.
			case err != nil:
				return nil, fmt.Errorf("sessions: summary message: %w", err)
			default:
				// FR-2.4's summary is a timeline message like any other (openapi
				// maps it to SSE `message.created`); it was the one insert on this
				// path that never produced a frame.
				_ = messages.Publish(ctx, s.Hub, tx, wsID, sessionID, msgID)
			}
		}
		if summary.Post {
			// Lead T-S9 ask 3 (i): who wrote it. `message` has no payload
			// column and openapi's Message has no field for it, so the feed's
			// `detail` carries it — the same place the failure category goes.
			s.recordSummaryOrigin(ctx, tx, sessionID, summary.GeneratedBy, now)
		}
		if summary.FeedError {
			// E6-11: the failure is recorded and the session still completes.
			// The category is what tells an operator whether to change the
			// prompt or the model, so it is the object_ref rather than a
			// generic "요약 실패".
			s.recordSummaryFailure(ctx, tx, sessionID, summary.ErrorCategory, now)
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'summary'`,
			sessionID).Scan(&out.SummaryMsgs); err != nil {
			return nil, fmt.Errorf("sessions: summary count: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE session SET status = 'completed', paused_reason = NULL, paused_detail = NULL,
			       finished_at = $2, updated_at = $2 WHERE id = $1`, sessionID, now); err != nil {
			return nil, err
		}
		// Queued work is moot once the session is over; leaving it queued means
		// a resumed daemon picks it up after the fact.
		if _, err := tx.Exec(ctx, `
			UPDATE task SET status = 'cancelled', finished_at = $2, updated_at = $2
			WHERE session_id = $1 AND status IN ('queued', 'deferred')`, sessionID, now); err != nil {
			return nil, err
		}
		if director != nil {
			if err := s.inbox(ctx, tx, wsID, *director, inbox.TypeSessionCompleted, inbox.Severity(inbox.TypeSessionCompleted), sessionID, sessionID, now); err != nil {
				return nil, err
			}
		}
		if err := s.gcWorkdirs(ctx, tx, sessionID, now); err != nil {
			return nil, err
		}
	}

	if out.HitlIssued {
		// FR-2.2: user_approval and the budget question are issued BY THE
		// PLATFORM, so task_id stays empty and source is `system` (§7).
		var hitlID uuid.UUID
		question := "종료 조건이 모두 충족되었습니다. 승인하시겠습니까?"
		// 0012: `purpose` is what tells the three platform-issued approvals
		// apart afterwards. They share source=system, type=approval and an
		// empty task_id, so respondHitlRequest would otherwise have to read the
		// question text to know whether answering it completes the session.
		purpose := CondUserApproval
		if ev.Kind == "budget_exhausted" {
			question, purpose = "예산 상한에 도달했습니다. 계속 진행할까요?", "budget"
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at)
			VALUES ($1, NULL, 'system', 'approval', $2, 'director', $3, $4, $5) RETURNING id`,
			sessionID, question, purpose, now.Add(24*time.Hour), now).Scan(&hitlID); err != nil {
			return nil, fmt.Errorf("sessions: approval hitl: %w", err)
		}
		// S-45: the timeline card. This request is what E6-01's "⬜ Director
		// 승인" checkbox waits on, and until now it existed only as an inbox
		// item — the session timeline the Director is already looking at showed
		// nothing (SCREEN §4.5). `source_task_id` stays empty: the platform
		// issued it, no task did.
		msgID, err := messages.PostHitlCard(ctx, s.Hub, tx, wsID, sessionID, messages.HitlCard{
			Type: "approval", Question: question,
		}, now)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE hitl_request SET message_id = $2 WHERE id = $1`, hitlID, msgID); err != nil {
			return nil, fmt.Errorf("sessions: approval hitl card: %w", err)
		}
		out.HitlTaskID = uuid.Nil
		if director != nil {
			if err := s.inbox(ctx, tx, wsID, *director, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest), sessionID, hitlID, now); err != nil {
				return nil, err
			}
		}
	}

	// FR-2.2's progress bar is the point of this call: every event that folds
	// into completion_met changes what S7 shows, and the contract declares
	// `session.completion_progress` for exactly that. It published nowhere
	// before, so an artifact submission moved the bar only on reload (W13).
	if s.Hub != nil {
		metRaw, _ := json.Marshal(met)
		sid := sessionID
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "session.completion_progress", map[string]any{
			"session_id":          sessionID,
			"completion_progress": progress(raw, metRaw),
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// gcWorkdirs is E6-03's last column — "`container`/`none` workdir 즉시 삭제" —
// and daemon-protocol §6's division of labour: the SERVER decides what may go,
// the daemon does the deleting and reports back. The `completed` branch tidied
// the session, its tasks and the inbox but issued no `gc`, so a finished
// session left its directories on the machine indefinitely.
//
// Only `container` and `none` are collected here. A `worktree` workdir is
// shared by all of one agent's lanes (C3) and outlives the session under the
// retention policy — deleting it on completion would take an uncommitted
// branch with it.
func (s *Service) gcWorkdirs(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, now time.Time) error {
	var kind string
	var runtimeID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(isolation->>'kind', ''), runtime_id FROM session WHERE id = $1`, sessionID).
		Scan(&kind, &runtimeID); err != nil {
		return fmt.Errorf("sessions: gc isolation: %w", err)
	}
	if kind != "none" && kind != "container" {
		return nil
	}
	if runtimeID == nil {
		// The session was never dispatched (C4 fixes runtime_id at first
		// dispatch), so no machine holds a directory for it.
		//
		// S-34: this used to return in silence. Under `none` isolation it is
		// unreachable — a session with no runtime has no workdir either — but
		// if it ever IS reached the directories are stranded with nothing in
		// the log, which is exactly the shape of the P4 GC bug this branch
		// would cause. One line, only when there is actually something to
		// collect.
		var orphans int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1 AND status <> 'deleted'`, sessionID).Scan(&orphans)
		if orphans > 0 {
			slog.Warn("sessions: session has workdirs but no runtime — gc not issued",
				"session", sessionID, "workdirs", orphans, "isolation", kind)
		}
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, path_or_ref FROM workdir WHERE session_id = $1 AND status <> 'deleted' ORDER BY created_at`, sessionID)
	if err != nil {
		return fmt.Errorf("sessions: gc workdirs: %w", err)
	}
	ids := []string{}
	targets := []contracts.GCWorkdir{}
	for rows.Next() {
		var id uuid.UUID
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id.String())
		targets = append(targets, contracts.GCWorkdir{ID: id.String(), Path: path})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	// daemon-protocol §4.3 v0.7: the SERVER carries the paths. The daemon has
	// never held a uuid→path map, so a command with ids alone falls back to
	// "every lane workdir of the session" — which is not what the retention
	// rules decided. `workdir_ids` stays for a daemon still on v0.6.
	//
	// The rows stay `active` until the daemon's §6 report says `gc: deleted`
	// (workdirs.ApplyGCReports): the server asked, it did not observe.
	// Claiming the deletion here would make S13 show an empty machine that is
	// still full.
	return tokens.QueueCommand(ctx, tx, *runtimeID, contracts.Command{
		Type: contracts.CmdGC, SessionID: sessionID.String(), WorkdirIDs: ids, Workdirs: targets,
	})
}

// publishDecision sends `decision.created` for a row just inserted. FR-4.2's
// log exists so a reader can find out WHY — a decision that only appears after
// a reload is one the person watching the session never sees being made.
func (s *Service) publishDecision(ctx context.Context, q db.DBTX, wsID, sessionID, decisionID uuid.UUID) {
	if s.Hub == nil || decisionID == uuid.Nil {
		return
	}
	var d DecisionRow
	if err := q.QueryRow(ctx, `
		SELECT id, summary, rationale, source::text, ref_id, created_at
		FROM decision WHERE id = $1`, decisionID).
		Scan(&d.ID, &d.Summary, &d.Rationale, &d.Source, &d.RefID, &d.CreatedAt); err != nil {
		return
	}
	sid := sessionID
	_ = s.Hub.Publish(ctx, q, wsID, &sid, "decision.created", DecisionAPI(sessionID, d))
}

// recordSummaryFailure puts E6-11's activity-feed error on the session.
//
// A session-level event has no task of its own, so it is attached to the
// session's most recent task — the same shape httpapi.recordGCRefusal uses for
// a GC refusal, and for the same reason: `task_event` is the only feed the
// screen renders, and a failure written nowhere is a failure nobody can act on
// ("보여주지 않았으면 일어나지 않은 것이다", FR-7.2). A session that never
// dispatched a task has no feed at all; the log line is then the record.
func (s *Service) recordSummaryFailure(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, category string, now time.Time) {
	if category == "" {
		category = "unknown"
	}
	var taskID uuid.UUID
	var attempt int
	if err := tx.QueryRow(ctx, `
		SELECT id, attempt FROM task WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`,
		sessionID).Scan(&taskID, &attempt); err != nil {
		s.logWarn("sessions: summary failed with no task to record it on",
			"session", sessionID, "category", category)
		return
	}
	// class `runtime` with `detail`, not `status` with a free-text `note`:
	// `contracts/task_event.schema.json` closes the `status` payload to
	// {command, args, result_ref, rejected_reason}, and `detail` is the field
	// the schema provides for a sentence.
	if err := tasks.InsertServerEventOnce(ctx, tx, taskID, attempt, "runtime", "error",
		"summary.failed", "failed",
		map[string]any{
			"detail":      "세션 요약을 만들지 못했습니다 — " + category,
			"stop_reason": category,
		}, now); err != nil {
		s.logWarn("sessions: record summary failure", "session", sessionID, "err", err)
	}
}

// recordSummaryOrigin notes who wrote the summary that was just posted.
//
// A row-composed fallback and a model-written summary read very differently,
// and only one of them is what FR-2.4 promises. Saying which on the feed is
// what stops a reader from judging the platform's summarising by an assembly
// it did without a model.
func (s *Service) recordSummaryOrigin(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, by string, now time.Time) {
	detail := "세션 요약을 플랫폼 LLM 이 작성했습니다 (§8.5)"
	if by == GeneratedByFallback {
		detail = "플랫폼 LLM 키 없음 — 행 조립 요약입니다 (§8.5 폴백)"
	}
	var taskID uuid.UUID
	var attempt int
	if err := tx.QueryRow(ctx, `
		SELECT id, attempt FROM task WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`,
		sessionID).Scan(&taskID, &attempt); err != nil {
		return
	}
	if err := tasks.InsertServerEventOnce(ctx, tx, taskID, attempt, "runtime", "report",
		"summary.generated_by:"+by, "info", map[string]any{"detail": detail}, now); err != nil {
		s.logWarn("sessions: record summary origin", "session", sessionID, "err", err)
	}
}

func (s *Service) inbox(ctx context.Context, tx pgx.Tx, wsID, userID uuid.UUID, typ, severity string, sessionID, refID uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
		SELECT m.id, $1::inbox_item_type, $2::inbox_severity, $3, $4, $5
		FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
		typ, severity, sessionID, refID, now, wsID, userID)
	return err
}

// RecordDecision is FR-4.2. The log exists so an agent joining later can find
// out WHY, which is also why a failed read must never look like an empty one:
// an empty section reads as "nothing was decided" and invites the agent to
// overturn a decision that was in fact made.
func (s *Service) RecordDecision(ctx context.Context, sessionID uuid.UUID, summary, rationale string, source string, refID *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	var rat *string
	if rationale != "" {
		rat = &rationale
	}
	err := s.DB.QueryRow(ctx, `
		INSERT INTO decision (session_id, summary, rationale, source, ref_id, created_at)
		VALUES ($1, $2, $3, $4::decision_source, $5, $6) RETURNING id`,
		sessionID, summary, rat, source, refID, s.Clock.Now()).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("sessions: record decision: %w", err)
	}
	var wsID uuid.UUID
	if err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM session WHERE id = $1`, sessionID).Scan(&wsID); err == nil {
		s.publishDecision(ctx, s.DB, wsID, sessionID, id)
	}
	return id, nil
}

// Decision is one row of the log.
type DecisionRow struct {
	ID        uuid.UUID
	Summary   string
	Rationale *string
	Source    string
	RefID     *uuid.UUID
	CreatedAt time.Time
}

func (s *Service) ListDecisions(ctx context.Context, sessionID uuid.UUID) ([]DecisionRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, summary, rationale, source::text, ref_id, created_at
		FROM decision WHERE session_id = $1 ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DecisionRow{}
	for rows.Next() {
		var d DecisionRow
		if err := rows.Scan(&d.ID, &d.Summary, &d.Rationale, &d.Source, &d.RefID, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// budgetLimit reads session.limits.budget_usd for the pause banner. A missing
// limit yields 0, which the banner renders as "no explicit limit" rather than
// inventing one.
func budgetLimit(raw []byte) float64 {
	var l struct {
		BudgetUsd *float64 `json:"budget_usd"`
	}
	if json.Unmarshal(raw, &l) != nil || l.BudgetUsd == nil {
		return 0
	}
	return *l.BudgetUsd
}
