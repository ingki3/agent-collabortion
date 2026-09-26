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

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
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

// ApplyWorkEvent folds one event into a mission's completion state and
// carries out the consequence: issuing the platform's user_approval request,
// pausing on a budget limit, or running the completing → completed step.
//
// Everything is keyed by the MISSION (PRD v0.19 FR-2A.4 — a room holds
// several): its row is the one locked and written, its tasks are the ones
// paused or cancelled, its summary is posted once per mission and its
// directories are the ones collected. The room is locked too (room first, the
// one lock order every writer of both keeps) because the summary and the
// timeline card are the room's messages.
func (s *Service) ApplyWorkEvent(ctx context.Context, workID uuid.UUID, ev Event) (*Outcome, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wsID, sessionID uuid.UUID
	var status string
	var raw, metRaw, limitsRaw, workLimitsRaw []byte
	var cost float64
	var assignee, director *uuid.UUID
	var legacy bool
	err = tx.QueryRow(ctx, `
		SELECT s.workspace_id, s.id, wk.status::text, wk.completion_condition, wk.completion_met, s.limits, wk.limits,
		       wk.cost_usd, wk.assignee_agent_id, wk.director_user_id, s.legacy_work_id IS NOT DISTINCT FROM wk.id
		FROM work wk JOIN room s ON s.id = wk.room_id WHERE wk.id = $1 FOR UPDATE OF s, wk`, workID).
		Scan(&wsID, &sessionID, &status, &raw, &metRaw, &limitsRaw, &workLimitsRaw, &cost, &assignee, &director, &legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("work")
	}
	if err != nil {
		return nil, err
	}
	if status == "completed" || status == "cancelled" {
		return nil, closedConflict(legacy)
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
	if _, err := tx.Exec(ctx, `UPDATE work SET completion_met = $2, updated_at = $3 WHERE id = $1`, workID, met, now); err != nil {
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
			INSERT INTO decision (session_id, summary, rationale, source, ref_id, created_at, work_id)
			VALUES ($1, $2, $3, $4::decision_source, $5, $6, $7) RETURNING id`,
			sessionID, summary, out.RejectReason, source, ev.Ref, now, workID).Scan(&out.DecisionID); err != nil {
			return nil, fmt.Errorf("sessions: reject decision: %w", err)
		}
		s.publishDecision(ctx, tx, wsID, sessionID, out.DecisionID)
	}

	switch out.SessionState {
	case "paused":
		detail := tasks.PausedDetail(out.PauseReason, now)
		if out.PauseReason == "budget" {
			// The mission's own ceiling when it has one (FR-2A.3), else the
			// room's — the banner names the limit that was reached.
			lim := budgetLimit(workLimitsRaw)
			if lim <= 0 {
				lim = budgetLimit(limitsRaw)
			}
			detail = tasks.WithBudget(detail, float32(lim), float32(cost))
		}
		if _, err := tx.Exec(ctx, `
			UPDATE work SET status = 'paused', paused_reason = $2, paused_detail = $3, updated_at = $4 WHERE id = $1`,
			workID, out.PauseReason, detail, now); err != nil {
			return nil, err
		}
		// FR-2.3 / §8.2.2: a budget pause CANCELS the turn in flight. Letting it
		// finish spends exactly the money the pause exists to stop. Only this
		// mission's turns — the room's other missions run on (FR-2A.3).
		if s.Tasks != nil {
			if err := s.Tasks.PauseWorkTasks(ctx, tx, workID, out.PauseReason, nil, now); err != nil {
				return nil, err
			}
		}
		if director != nil {
			if err := s.WorkInbox(ctx, tx, wsID, *director, TypeWorkPaused, sessionID, workID, now); err != nil {
				return nil, err
			}
		}
	case "completed":
		// active → completing → completed. The intermediate state is real: no
		// new task dispatches while the summary runs (FR-2.3, E5-08).
		if _, err := tx.Exec(ctx, `
			UPDATE work SET status = 'completing', updated_at = $2 WHERE id = $1`, workID, now); err != nil {
			return nil, err
		}
		// FR-2.4 with a model on the path (P4, §8.5). `alreadyPosted` is read
		// FIRST because it changes what a success means: the summariser runs
		// behind the completing transition and a retry after a timeout is the
		// normal way it is called twice — two summaries in a timeline are
		// indistinguishable and the reader cannot tell which is current.
		var alreadyPosted bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM message WHERE work_id = $1 AND kind = 'summary' AND summary_range IS NULL)`,
			workID).Scan(&alreadyPosted); err != nil {
			return nil, fmt.Errorf("sessions: summary presence: %w", err)
		}
		summary := s.summarise(ctx, tx, workID, alreadyPosted)
		if summary.Post {
			msgID, inserted, err := postSummaryOnce(ctx, tx, sessionID, workID, summary.Body, now)
			if err != nil {
				return nil, err
			}
			if inserted {
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
			s.recordSummaryOrigin(ctx, tx, workID, summary.GeneratedBy, now)
		}
		if summary.FeedError {
			// E6-11: the failure is recorded and the session still completes.
			// The category is what tells an operator whether to change the
			// prompt or the model, so it is the object_ref rather than a
			// generic "요약 실패".
			s.recordSummaryFailure(ctx, tx, workID, summary.ErrorCategory, now)
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM message WHERE work_id = $1 AND kind = 'summary' AND summary_range IS NULL`,
			workID).Scan(&out.SummaryMsgs); err != nil {
			return nil, fmt.Errorf("sessions: summary count: %w", err)
		}
		// FR-2A.4: the mission remembers the message that closed it.
		if _, err := tx.Exec(ctx, `
			UPDATE work SET status = 'completed', paused_reason = NULL, paused_detail = NULL,
			       finished_at = $2, updated_at = $2,
			       summary_message_id = (SELECT id FROM message WHERE work_id = $1 AND kind = 'summary' AND summary_range IS NULL
			                             ORDER BY created_at DESC, id DESC LIMIT 1)
			WHERE id = $1`, workID, now); err != nil {
			return nil, err
		}
		if s.Hub != nil {
			sid := sessionID
			var summaryID *uuid.UUID
			_ = tx.QueryRow(ctx, `SELECT summary_message_id FROM work WHERE id = $1`, workID).Scan(&summaryID)
			_ = s.Hub.Publish(ctx, tx, wsID, &sid, "work.closed", map[string]any{
				"work_id": workID, "room_id": sessionID, "status": "completed", "summary_message_id": summaryID,
			})
		}
		// Queued work is moot once the mission is over; leaving it queued means
		// a resumed daemon picks it up after the fact. The room's other
		// missions — and its talk outside any mission — keep theirs.
		if _, err := tx.Exec(ctx, `
			UPDATE task SET status = 'cancelled', finished_at = $2, updated_at = $2
			WHERE work_id = $1 AND status IN ('queued', 'deferred')`, workID, now); err != nil {
			return nil, err
		}
		if director != nil {
			if err := s.WorkInbox(ctx, tx, wsID, *director, TypeWorkCompleted, sessionID, workID, now); err != nil {
				return nil, err
			}
		}
		if err := s.gcWorkdirs(ctx, tx, sessionID, workID, now); err != nil {
			return nil, err
		}
	}

	approvalAsk := out.HitlIssued && ev.Kind != "budget_exhausted"
	if approvalAsk {
		// S-84: a condition change re-reads the tree; if the platform's
		// user_approval request is already open from before the change, the
		// Director has one card to answer, not two. T-APPROVAL: the same for
		// every other re-read — the release of a held request, and a second
		// submission while the first request is still open (which is not a
		// reason to hold either: the Director already has the card).
		var open bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM hitl_request WHERE work_id = $1 AND source = 'system' AND purpose = $2 AND status = 'open')`,
			workID, CondUserApproval).Scan(&open); err != nil {
			return nil, fmt.Errorf("sessions: open approval: %w", err)
		}
		out.HitlIssued = !open
	}
	held := false
	if approvalAsk && out.HitlIssued {
		// T-APPROVAL (Director 2026-09-25): every other atom is met, but the
		// mission still has work running or waiting — the assignee submitted
		// v1 and said it keeps integrating. Asking "종료 조건이 모두
		// 충족되었습니다. 승인하시겠습니까?" now asks the Director to close
		// work that is still moving. The request is HELD; the last task's end
		// (ReleaseHeldApproval) re-reads the tree and opens it then.
		// Other atoms (agent_approval, criteria_met) are untouched: only the
		// platform's user_approval request waits.
		busy, err := missionBusy(ctx, tx, workID)
		if err != nil {
			return nil, err
		}
		if busy {
			held, out.HitlIssued, out.ApprovalHeld = true, false, true
		}
	}
	// The mark is what tells a held request from one the Director turned down
	// (E6-04: a rejection re-asks nothing) — only a mark set here is released
	// later. It is rewritten ONLY by an event that actually re-read the
	// approval (`approvalAsk`) or by the mission completing:
	//
	//   · held        → set (the request waits for the mission's work)
	//   · asked, open → cleared (the Director has the card)
	//   · completed   → cleared (there is nothing left to ask)
	//
	// Every other event leaves it alone. T-APPROVAL (E): a second submission
	// while a hold stands asks nothing (`advanced` is false — the atom was
	// already met), and if it rewrote the mark it would ERASE the hold; the
	// mission would then wait forever for a request nobody would open.
	if approvalAsk || out.SessionState == "completed" {
		if _, err := tx.Exec(ctx, `
			UPDATE work SET approval_held_at = CASE WHEN $2 THEN COALESCE(approval_held_at, $3) END WHERE id = $1`,
			workID, held, now); err != nil {
			return nil, fmt.Errorf("sessions: approval hold: %w", err)
		}
	}
	var hitlID uuid.UUID
	var question string
	if out.HitlIssued {
		// FR-2.2: user_approval and the budget question are issued BY THE
		// PLATFORM, so task_id stays empty and source is `system` (§7).
		//
		// T-APPROVAL (E): the INSERT is the last of three guards against a
		// second open approval for one mission. The other two are the
		// `advanced` test in ApplyEvent (nothing changed → nothing to ask) and
		// the open-request read above; this one is the partial unique index
		// `hitl_request_one_open_user_approval_per_work`, which is what holds
		// when two submissions land at the same instant.
		question = "종료 조건이 모두 충족되었습니다. 승인하시겠습니까?"
		// 0012: `purpose` is what tells the three platform-issued approvals
		// apart afterwards. They share source=system, type=approval and an
		// empty task_id, so respondHitlRequest would otherwise have to read the
		// question text to know whether answering it completes the session.
		purpose := CondUserApproval
		if ev.Kind == "budget_exhausted" {
			question, purpose = "예산 상한에 도달했습니다. 계속 진행할까요?", "budget"
		}
		// The INSERT is skipped when the index says one is already open
		// (`ON CONFLICT DO NOTHING` + no row back): another writer got there
		// between the read above and here, and the Director has the card they
		// need. One question, answered once.
		err := tx.QueryRow(ctx, `
			INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at, work_id)
			VALUES ($1, NULL, 'system', 'approval', $2, 'director', $3, $4, $5, $6)
			ON CONFLICT DO NOTHING
			RETURNING id`,
			sessionID, question, purpose, now.Add(24*time.Hour), now, workID).Scan(&hitlID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			out.HitlIssued = false
		case err != nil:
			return nil, fmt.Errorf("sessions: approval hitl: %w", err)
		}
	}
	if out.HitlIssued && hitlID != uuid.Nil {
		// S-45: the timeline card. This request is what E6-01's "⬜ Director
		// 승인" checkbox waits on, and until now it existed only as an inbox
		// item — the session timeline the Director is already looking at showed
		// nothing (SCREEN §4.5). `source_task_id` stays empty: the platform
		// issued it, no task did.
		//
		// T-APPROVAL: AttachHitlCard also publishes `hitl.created` — without it
		// S7 had the card (message.created) and not the request, and drew a
		// plain system line with no buttons.
		if _, err := messages.AttachHitlCard(ctx, s.Hub, tx, wsID, sessionID, hitlID, messages.HitlCard{
			Type: "approval", Question: question, WorkID: &workID,
		}, now); err != nil {
			return nil, fmt.Errorf("sessions: approval hitl card: %w", err)
		}
		out.HitlTaskID = uuid.Nil
		if director != nil {
			if err := s.inbox(ctx, tx, wsID, *director, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest), sessionID, hitlID, &workID, now); err != nil {
				return nil, err
			}
		}
	}

	// FR-2.2's progress bar is the point of this call: every event that folds
	// into completion_met changes what S7 shows, and the contract declares
	// `work.completion_progress` for exactly that. It published nowhere
	// before, so an artifact submission moved the bar only on reload (W13).
	if s.Hub != nil {
		metRaw, _ := json.Marshal(met)
		var heldNow bool
		if err := tx.QueryRow(ctx, `SELECT approval_held_at IS NOT NULL FROM work WHERE id = $1`, workID).Scan(&heldNow); err != nil {
			return nil, err
		}
		prog, err := progressOf(ctx, tx, sessionID, raw, metRaw, assignee, heldNow)
		if err != nil {
			return nil, err
		}
		sid := sessionID
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "work.completion_progress", map[string]any{
			"work_id":             workID,
			"completion_progress": prog,
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
//
// PRD v0.19 FR-6.4: a room holds several missions, so a mission's end
// collects only the directories of ITS lanes — a directory another open
// mission's lane (or a lane outside any mission) still points at stays for
// the sweep to judge on its own clock (workdirs.disposableNow).
//
// daemon-protocol v0.10.0 §6.1 (D8 B): a folder of the new layout — one with
// a `work_id`, the agents' rows and `_shared` — is NOT collected by the close.
// It stays for `last_used_at + workdir_retention_days` and the sweep collects
// it (workdirs.missionFolderDisposable). Only old-layout lane folders
// (`sessions/<room>/<lane>`, no `work_id`) keep the close-time rule.
func (s *Service) gcWorkdirs(ctx context.Context, tx pgx.Tx, sessionID, workID uuid.UUID, now time.Time) error {
	var kind string
	var runtimeID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(isolation->>'kind', ''), runtime_id FROM room WHERE id = $1`, sessionID).
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
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM workdir w WHERE w.session_id = $1 AND w.status <> 'deleted' AND `+missionDirSQL, sessionID, workID).Scan(&orphans)
		if orphans > 0 {
			slog.Warn("sessions: session has workdirs but no runtime — gc not issued",
				"session", sessionID, "workdirs", orphans, "isolation", kind)
		}
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT w.id, w.path_or_ref FROM workdir w WHERE w.session_id = $1 AND w.status <> 'deleted' AND `+missionDirSQL+`
		ORDER BY w.created_at`, sessionID, workID)
	if err != nil {
		return fmt.Errorf("sessions: gc workdirs: %w", err)
	}
	var ids []uuid.UUID
	var paths []string
	for rows.Next() {
		var id uuid.UUID
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		paths = append(paths, path)
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
	// workdirs.BuildGCCommand is the one builder (S-65): a relative stored
	// path never reaches the daemon, from any of the three gc paths.
	//
	// The rows stay `active` until the daemon's §6 report says `gc: deleted`
	// (workdirs.ApplyGCReports): the server asked, it did not observe.
	// Claiming the deletion here would make S13 show an empty machine that is
	// still full.
	cmd, skipped := workdirs.BuildGCCommand(sessionID, ids, paths)
	if len(skipped) > 0 {
		slog.Warn("sessions: gc skipped workdirs with a relative path (S-65) — not sent to the daemon",
			"session", sessionID, "workdirs", skipped)
	}
	if len(cmd.Workdirs) == 0 {
		return nil
	}
	return tokens.QueueCommand(ctx, tx, *runtimeID, cmd)
}

// missionDirSQL is "workdir w belongs to mission $2 alone": no lane outside
// that mission — in another mission still open, or outside any mission —
// points at it. A directory no lane points at was the session's (1:1 rooms)
// and goes with it as before.
//
// A §6.1 `_room` folder is never one of them (Lead 판정 2026-09-26, 리뷰 345a
// NN3): its row is made with `work_id` NULL and keeps it, and a lane that
// started outside a mission and was later bound to one (D6 lane reuse, which
// stays unrestricted so a running lane's cwd does not move) would otherwise
// make its `_room` folder look like this mission's — the close would delete
// the folder the agent's other mission-less turns use. The row's own
// `work_id` decides, so `_room` keeps the retention rule (workdirs.SweepGC).
var missionDirSQL = `w.work_id IS NULL AND NOT ` + workdirs.RoomFolderSQL + ` AND NOT EXISTS (
	SELECT 1 FROM lane l LEFT JOIN work lw ON lw.id = l.work_id
	 WHERE (l.workdir_id = w.id OR l.id = w.lane_id)
	   AND l.work_id IS DISTINCT FROM $2
	   AND (l.work_id IS NULL OR lw.status NOT IN ('completed', 'cancelled')))`

// publishDecision sends `decision.created` for a row just inserted. FR-4.2's
// log exists so a reader can find out WHY — a decision that only appears after
// a reload is one the person watching the session never sees being made.
func (s *Service) publishDecision(ctx context.Context, q db.DBTX, wsID, sessionID, decisionID uuid.UUID) {
	if s.Hub == nil || decisionID == uuid.Nil {
		return
	}
	var d DecisionRow
	if err := q.QueryRow(ctx, `
		SELECT `+DecisionColumns+`
		FROM decision WHERE id = $1`, decisionID).Scan(d.Dest()...); err != nil {
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
// postSummaryOnce is FR-2.4's "정확히 1개", enforced where it can actually be
// enforced: in the INSERT.
//
// The `alreadyPosted` read above decides what to ASK the model for; it cannot
// decide who WINS, because two callers can both read false. The summariser runs
// behind the `completing` transition and a retry after a timeout is the normal
// way it is called twice, so the guard is the WHERE NOT EXISTS — two summaries
// in a timeline are indistinguishable and a reader cannot tell which is
// current.
//
// It is a function rather than four inline lines so a test can call the same
// statement production calls, twice, and watch it insert once (#162 review NN1:
// removing the NOT EXISTS was the injection nothing caught).
//
// production caller: sessions.Service.ApplyWorkEvent (complete.go).
//
// "Exactly one" is per MISSION (FR-2A.4): a room whose first mission already
// left its summary still gets one for the second.
func postSummaryOnce(ctx context.Context, tx pgx.Tx, sessionID, workID uuid.UUID, body string, now time.Time) (uuid.UUID, bool, error) {
	var msgID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, created_at, work_id)
		SELECT $1, 'system', NULL, $2, 'summary', $3, $4
		WHERE NOT EXISTS (SELECT 1 FROM message WHERE work_id = $4 AND kind = 'summary' AND summary_range IS NULL)
		RETURNING id`, sessionID, body, now, workID).Scan(&msgID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Somebody else got there first. Not an error: the session has its one
		// summary, which is the whole rule.
		return uuid.Nil, false, nil
	case err != nil:
		return uuid.Nil, false, fmt.Errorf("sessions: summary message: %w", err)
	}
	if err := messages.Store(ctx, tx, msgID, messages.StoreOpts{}); err != nil {
		return uuid.Nil, false, err
	}
	return msgID, true, nil
}

func (s *Service) recordSummaryFailure(ctx context.Context, tx pgx.Tx, workID uuid.UUID, category string, now time.Time) {
	if category == "" {
		category = "unknown"
	}
	var taskID uuid.UUID
	var attempt int
	if err := tx.QueryRow(ctx, `
		SELECT id, attempt FROM task WHERE work_id = $1 ORDER BY created_at DESC LIMIT 1`,
		workID).Scan(&taskID, &attempt); err != nil {
		s.logWarn("sessions: summary failed with no task to record it on",
			"work", workID, "category", category)
		return
	}
	// class `runtime` with `detail`, not `status` with a free-text `note`:
	// `contracts/task_event.schema.json` closes the `status` payload to
	// {command, args, result_ref, rejected_reason}, and `detail` is the field
	// the schema provides for a sentence.
	if err := tasks.InsertServerEventOnce(ctx, tx, taskID, attempt, "runtime", "error",
		"summary.failed", "failed",
		map[string]any{
			"detail":      "미션 요약을 만들지 못했습니다 — " + category,
			"stop_reason": category,
		}, now); err != nil {
		s.logWarn("sessions: record summary failure", "work", workID, "err", err)
	}
}

// recordSummaryOrigin notes who wrote the summary that was just posted.
//
// A row-composed fallback and a model-written summary read very differently,
// and only one of them is what FR-2.4 promises. Saying which on the feed is
// what stops a reader from judging the platform's summarising by an assembly
// it did without a model.
func (s *Service) recordSummaryOrigin(ctx context.Context, tx pgx.Tx, workID uuid.UUID, by string, now time.Time) {
	detail := "미션 요약을 플랫폼 모델이 작성했습니다"
	if by == GeneratedByFallback {
		detail = "플랫폼 모델 키가 없어 기록을 이어 붙인 요약입니다"
	}
	var taskID uuid.UUID
	var attempt int
	if err := tx.QueryRow(ctx, `
		SELECT id, attempt FROM task WHERE work_id = $1 ORDER BY created_at DESC LIMIT 1`,
		workID).Scan(&taskID, &attempt); err != nil {
		return
	}
	if err := tasks.InsertServerEventOnce(ctx, tx, taskID, attempt, "runtime", "report",
		"summary.generated_by:"+by, "info", map[string]any{"detail": detail}, now); err != nil {
		s.logWarn("sessions: record summary origin", "work", workID, "err", err)
	}
}

func (s *Service) inbox(ctx context.Context, tx pgx.Tx, wsID, userID uuid.UUID, typ, severity string, sessionID, refID uuid.UUID, workID *uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, work_id, recipient_basis)
		SELECT m.id, $1::inbox_item_type, $2::inbox_severity, $3, $4, $5, $8, $9
		FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
		typ, severity, sessionID, refID, now, wsID, userID, workID, basisFor(workID))
	return err
}

// The v0.2.0 mission item types (openapi InboxItemType, PRD v0.19 FR-8).
const (
	TypeWorkPaused    = inbox.TypeWorkPaused
	TypeWorkCompleted = inbox.TypeWorkCompleted
)

// WorkInbox puts a mission item (work_paused · work_completed) in the
// Director's inbox: ref_id is the mission, so the card opens
// /rooms/:id?work=:workId (PRD §9 미션 행).
func (s *Service) WorkInbox(ctx context.Context, tx pgx.Tx, wsID, userID uuid.UUID, typ string, roomID, workID uuid.UUID, now time.Time) error {
	return WorkInboxItem(ctx, tx, wsID, userID, typ, roomID, workID, now)
}

// WorkInboxItem is WorkInbox for callers outside the package.
func WorkInboxItem(ctx context.Context, q db.DBTX, wsID, userID uuid.UUID, typ string, roomID, workID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, work_id, recipient_basis)
		SELECT m.id, $1::inbox_item_type, $2::inbox_severity, $3, $4, $5, $4, 'director'
		FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
		typ, inbox.Severity(typ), roomID, workID, now, wsID, userID)
	if err != nil {
		return fmt.Errorf("sessions: %s inbox: %w", typ, err)
	}
	return nil
}

// basisFor is InboxItem.recipient_basis for the Director-addressed items this
// package writes: a mission's item reaches the person as its Director.
func basisFor(workID *uuid.UUID) *string {
	if workID == nil {
		return nil
	}
	b := inbox.BasisDirector
	return &b
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
	// The decision belongs to the mission of the task that recorded it
	// (FR-3.1.1 — a task runs for its lane's mission; T-R1b2 fills the column
	// R1b1 left empty).
	err := s.DB.QueryRow(ctx, `
		INSERT INTO decision (session_id, summary, rationale, source, ref_id, created_at, work_id)
		VALUES ($1, $2, $3, $4::decision_source, $5, $6,
		        (SELECT COALESCE(l.work_id, t.work_id) FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $5)) RETURNING id`,
		sessionID, summary, rat, source, refID, s.Clock.Now()).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("sessions: record decision: %w", err)
	}
	var wsID uuid.UUID
	if err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM room WHERE id = $1`, sessionID).Scan(&wsID); err == nil {
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
	WorkID    *uuid.UUID
	// Auto is `decision.auto` — the decision came from an auto_answered
	// request (openapi Decision.auto, E7-12 「자동」).
	Auto bool
}

// DecisionColumns is the SELECT list DecisionRow.Dest scans, in its order.
// listDecisions, the `decision.created` frame and readRoom all read with the
// pair, so a column added to the row cannot reach one of them and not the
// others (decision.auto was in the table and in none of the three).
const DecisionColumns = `id, summary, rationale, source::text, ref_id, created_at, work_id, auto`

// Dest is the Scan destinations for DecisionColumns.
func (d *DecisionRow) Dest() []any {
	return []any{&d.ID, &d.Summary, &d.Rationale, &d.Source, &d.RefID, &d.CreatedAt, &d.WorkID, &d.Auto}
}

func (s *Service) ListDecisions(ctx context.Context, sessionID uuid.UUID) ([]DecisionRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+DecisionColumns+`
		FROM decision WHERE session_id = $1 ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DecisionRow{}
	for rows.Next() {
		var d DecisionRow
		if err := rows.Scan(d.Dest()...); err != nil {
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
