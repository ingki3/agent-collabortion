package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// In-turn budget enforcement (FR-7.3 M9 "턴 중 강제").
//
// The daemon reports the turn's running usage on every heartbeat
// (daemon-protocol §4.2). Before P3 the server threw that number away and only
// priced the turn at `finish`, which means a task could not be stopped until
// it had already spent past its limit — the "턴 중" half of FR-7.3 did not
// exist. This is where it does.

// budgetState is everything PlanBudget reads, loaded once.
//
// PRD v0.19 FR-2A.3: three ceilings apply to one task — its own (the agent's
// budget_per_task or an approved raise), its MISSION's remainder and its
// ROOM's remainder — and the tightest wins. The old `session` budget is the
// room's: `limits` has always been stored on the room row (0025), so a
// session budget crossing is a room budget crossing. A mission's own
// budget (work.limits.budget_usd) is new in v0.19.
type budgetState struct {
	TaskID, SessionID, WorkspaceID, LaneID, AgentID uuid.UUID
	AgentBudgetPerTask                              *float64
	TaskOverride                                    *float64
	TaskSpentUSD                                    float64
	Estimated                                       bool
	TaskStatus                                      string
	Attempt                                         int
	AgentName                                       string

	// The room: its limit and what every task in it has spent. `SessionLimitUSD`
	// keeps its old name because the old session budget IS this number.
	SessionLimitUSD float64
	SessionSpentUSD float64
	RoomBlocked     bool
	RoomOwner       uuid.UUID

	// The task's mission (nil = a run outside any mission, FR-2A.1).
	WorkID       *uuid.UUID
	WorkLimitUSD float64
	WorkSpentUSD float64
	WorkStatus   string
	// Director is who a task- or mission-scope request goes to: the mission's
	// Director, or the room owner for a run outside any mission.
	Director uuid.UUID
}

func (s *Server) loadBudgetState(ctx context.Context, q pgx.Tx, taskID uuid.UUID) (*budgetState, error) {
	var b budgetState
	var limits, workLimits []byte
	var blocked *string
	err := q.QueryRow(ctx, `
		SELECT t.id, t.session_id, s.workspace_id, t.lane_id, t.agent_id, t.attempt,
		       a.budget_per_task,
		       -- tasks.LaneBudgetOverride, inlined so the whole state is one
		       -- round trip: an approved raise carries along the lane it was
		       -- granted on, because the post-turn pause approves a task that
		       -- has already finished (S-44).
		       COALESCE(t.budget_override,
		                (SELECT tt.budget_override FROM task tt
		                  WHERE tt.lane_id = t.lane_id AND tt.budget_override IS NOT NULL
		                  ORDER BY tt.created_at DESC, tt.id DESC LIMIT 1)),
		       s.limits, t.status::text, s.blocked_reason::text, s.owner_user_id, a.name,
		       t.work_id, wk.limits, COALESCE(wk.status::text, 'active'), COALESCE(wk.director_user_id, s.owner_user_id),
		       COALESCE(u.cost_usd, 0), COALESCE(u.estimated, false),
		       COALESCE((SELECT sum(uu.cost_usd) FROM task_usage uu JOIN task tt ON tt.id = uu.task_id
		                 WHERE tt.session_id = t.session_id), 0),
		       COALESCE((SELECT sum(uu.cost_usd) FROM task_usage uu JOIN task tt ON tt.id = uu.task_id
		                 WHERE tt.work_id = t.work_id), 0)
		FROM task t
		JOIN room s ON s.id = t.session_id
		-- The task's OWN mission (V19_R1B_HANDOFF (a) budget.go:65): joining
		-- "the room's mission" counts the room's spend once per mission once a
		-- room has several, and a doubled spend refuses turns silently.
		LEFT JOIN work wk ON wk.id = t.work_id
		JOIN agent a ON a.id = t.agent_id
		LEFT JOIN task_usage u ON u.task_id = t.id
		WHERE t.id = $1`, taskID).
		Scan(&b.TaskID, &b.SessionID, &b.WorkspaceID, &b.LaneID, &b.AgentID, &b.Attempt,
			&b.AgentBudgetPerTask, &b.TaskOverride, &limits, &b.TaskStatus, &blocked, &b.RoomOwner, &b.AgentName,
			&b.WorkID, &workLimits, &b.WorkStatus, &b.Director,
			&b.TaskSpentUSD, &b.Estimated, &b.SessionSpentUSD, &b.WorkSpentUSD)
	if err != nil {
		return nil, err
	}
	b.SessionLimitUSD = budgetOf(limits)
	if b.WorkID != nil {
		b.WorkLimitUSD = budgetOf(workLimits)
	}
	b.RoomBlocked = blocked != nil
	return &b, nil
}

// remainderOf is D-16's "잔여" of one ceiling for THIS task: the limit less
// what the OTHER tasks under it have spent. Its own spend is left in, because
// that is the number its limit is compared against. Zero means no ceiling —
// never "nothing left", or a room without a budget would pin every task to
// zero (daemon-protocol v0.7.1 §4.4).
func (b *budgetState) remainderOf(limit, spent float64) float64 {
	if limit <= 0 {
		return 0
	}
	rem := limit - (spent - b.TaskSpentUSD)
	if rem < 0 {
		// Spent through by the others: the ceiling's own check (mission ·
		// room, below the task check) is what stops it.
		return 0
	}
	return rem
}

// sessionRemaining is the ROOM's remainder for this task (the old session
// budget, see budgetState).
func (b *budgetState) sessionRemaining() float64 {
	return b.remainderOf(b.SessionLimitUSD, b.SessionSpentUSD)
}

// workRemaining is the task's MISSION's remainder; zero outside a mission or
// when the mission carries no budget of its own (WorkLimits: "비우면 방
// 한도를 따른다").
func (b *budgetState) workRemaining() float64 {
	if b.WorkID == nil {
		return 0
	}
	return b.remainderOf(b.WorkLimitUSD, b.WorkSpentUSD)
}

// remainder is FR-2A.3's min(미션 잔여, 방 잔여) — the half of the task's
// effective ceiling that is not the task's own — and which of the two bound.
func (b *budgetState) remainder() (float64, string) {
	w, r := b.workRemaining(), b.sessionRemaining()
	switch {
	case w > 0 && (r <= 0 || w <= r):
		return w, scopeWork
	case r > 0:
		return r, scopeRoom
	}
	return 0, ""
}

// Budget scopes after FR-2A.3's min() has been applied.
const (
	scopeTask = "task"
	scopeWork = "work"
	scopeRoom = "room"
)

// EnforceBudget checks the task, the mission and the room limit and applies
// PlanBudget's verdict. It returns true when it paused something, so the caller
// can tell the daemon (the cancel command rides the same response).
//
// production callers: daemonHeartbeat (the `usage` field, §4.2) and
// daemonFinish through Server.finishAndEnforce, right after tasks.Finish has
// committed the attempt AND rolled the usage up (§4.4).
//
// S-44: this comment used to claim the second caller existed when it did not,
// and the enforcement point it named was the one that matters most — a daemon
// that reports usage only at `finish` (measured in T-I3: budget_per_task
// $0.002, turn $0.0599, task `completed`, session `active`, zero HITL) never
// went through the heartbeat branch at all, so nothing was ever enforced. Both
// call sites are named here so the next audit can grep them.
func (s *Server) enforceBudgetFor(ctx context.Context, taskID uuid.UUID) (bool, error) {
	now := s.Clock.Now()
	paused := false
	err := s.inSessionTx(ctx, func(tx pgx.Tx) error {
		b, err := s.loadBudgetState(ctx, tx, taskID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if b.RoomBlocked || b.WorkStatus != "active" {
			// Already stopped (room gate or mission pause); nothing to decide.
			return nil
		}
		// The task limit is checked first: it is the tighter one, and stopping
		// the mission or the room for a single runaway task would stop the
		// other lanes that are inside their own budgets (FR-7.3). Its ceiling
		// is already min(task, 미션 잔여, 방 잔여) — when a remainder is what
		// bound, PlanBudget answers `session` and the scope is that remainder's.
		rem, remScope := b.remainder()
		o := sessions.PlanBudget(sessions.BudgetInput{
			Scope: "task", TaskID: b.TaskID,
			TaskLimitUSD: derefFloat(b.AgentBudgetPerTask), OverrideUSD: derefFloat(b.TaskOverride),
			SessionRemainingUSD: rem,
			SpentUSD:            b.TaskSpentUSD, Estimated: b.Estimated,
		})
		scope := scopeTask
		if o.Exceeded && o.Scope != scopeTask {
			scope = remScope
		}
		if !o.Exceeded && b.WorkLimitUSD > 0 {
			o = sessions.PlanBudget(sessions.BudgetInput{
				Scope: "session", TaskID: b.TaskID,
				SessionLimitUSD: b.WorkLimitUSD, SpentUSD: b.WorkSpentUSD, Estimated: b.Estimated,
			})
			scope = scopeWork
		}
		if !o.Exceeded {
			o = sessions.PlanBudget(sessions.BudgetInput{
				Scope: "session", TaskID: b.TaskID,
				SessionLimitUSD: b.SessionLimitUSD, SpentUSD: b.SessionSpentUSD, Estimated: b.Estimated,
			})
			scope = scopeRoom
		}
		if !o.Exceeded {
			return nil
		}
		paused = true
		return s.applyBudgetPause(ctx, tx, b, o, scope, rem, now)
	})
	return paused, err
}

// applyBudgetPause carries out PlanBudget's verdict for the scope that
// crossed. What STOPS depends on it (FR-2A.3):
//
//   - task: the task (or, after a finished turn, its lane) — as before;
//   - work: the task's mission is `paused(budget)` and its Director is asked;
//   - room: the room's gate goes up (`blocked_reason: budget`), its active
//     missions are parked as the room's (roomgate), and the room owner is
//     asked — with the absent-owner hand-over (approver_spec room_owner).
//
// An ESTIMATED overrun of a task budget stops the room too: E9-05 has no
// per-task drain, and the old session it paused is the room (budgetState).
func (s *Server) applyBudgetPause(ctx context.Context, tx pgx.Tx, b *budgetState, o sessions.BudgetOutcome, scope string, rem float64, now time.Time) error {
	// The pair quoted everywhere below — paused_detail, the feed note and the
	// question — is the pair that CROSSED. S-48: it used to be picked by
	// `HitlTaskID == uuid.Nil`, which is the request's scope, not the limit's.
	// The two only agree on the measured path; an estimated overrun of a TASK
	// budget pauses the session (E9-05) and so leaves HitlTaskID empty, and the
	// old test then quoted the SESSION's numbers — for a session that often has
	// no budget at all, i.e. "세션이 예산 $0.00를 넘었습니다".
	spent, limit := b.TaskSpentUSD, sessions.EffectiveTaskLimit(derefFloat(b.AgentBudgetPerTask), derefFloat(b.TaskOverride), rem)
	switch scope {
	case scopeWork:
		spent, limit = b.WorkSpentUSD, b.WorkLimitUSD
	case scopeRoom:
		spent, limit = b.SessionSpentUSD, b.SessionLimitUSD
	}
	detail := tasks.WithBudget(tasks.PausedDetail(sessions.PauseBudget, now), float32(limit), float32(spent))
	raw, _ := json.Marshal(detail)

	// Which unit stops when PlanBudget says the "session" pauses.
	stopUnit := ""
	if o.SessionState == "paused" {
		stopUnit = scopeRoom
		if scope == scopeWork && b.WorkID != nil {
			stopUnit = scopeWork
		}
	}
	switch stopUnit {
	case scopeWork:
		tag, err := tx.Exec(ctx, `
			UPDATE work SET status = 'paused', paused_reason = 'budget', paused_detail = $2, updated_at = $3
			WHERE id = $1 AND status = 'active'`, *b.WorkID, raw, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			// FR-8 받은 요청 work_paused (T-R1b2): the mission stopped, and its
			// Director is the one who can let it go on.
			if err := sessions.WorkInboxItem(ctx, tx, b.WorkspaceID, b.Director, inbox.TypeWorkPaused, b.SessionID, *b.WorkID, now); err != nil {
				return err
			}
			s.publishWork(ctx, tx, b.WorkspaceID, *b.WorkID, "work.updated")
		}
	case scopeRoom:
		if _, err := roomgate.Lock(ctx, tx, b.SessionID); err != nil {
			return err
		}
		bs, bc := float32(limit), float32(spent)
		_, err := roomgate.Block(ctx, tx, b.SessionID, roomgate.ReasonBudget, gen.BlockedDetail{
			BudgetUsd: nullable.NewNullableWithValue(bs), CostUsd: nullable.NewNullableWithValue(bc),
		}, &detail, now)
		if errors.Is(err, roomgate.ErrAlreadyBlocked) {
			return nil // stopped already (another reason or a racing heartbeat)
		}
		if err != nil {
			return err
		}
		roomgate.PublishUpdated(ctx, s.Hub, tx, b.SessionID)
	}
	if o.TurnDrained {
		// FR-7.3's last bullet: an ESTIMATED cost never hard-cuts. The running
		// turn finishes; only new dispatch stops (the claim's room gate and
		// mission check do that). Killing real work on our own guess is the
		// failure the rule names (E9-05).
		// S-52: verb `pause` is in no enum, and a budget pause is not a colab
		// CLI call — `status` requires a `command` and there is none to name.
		// It is the server reporting its own decision, so it takes rule 2's
		// shape: class=runtime · verb=report · `detail`. The numbers stay in
		// the sentence because `runtime` has no slot for them either, and a
		// pause the Director cannot see the arithmetic of is not actionable.
		if err := tasks.InsertServerEvent(ctx, tx, b.TaskID, b.Attempt, "runtime", "report", "budget", "info",
			map[string]any{
				"detail": fmt.Sprintf("추정 비용이 한도를 넘어 새 할 일을 멈췄습니다 — 진행 중인 턴은 끝까지 둡니다 "+
					"(추정 사용 $%.4f / 한도 $%.4f)", spent, limit),
			}, now); err != nil {
			return err
		}
		// E9-05's other half — "세션 `paused` + Dir 알림" — is the HITL below.
		// S-44 filed a bare `session_paused` inbox card here because the
		// estimated path raised no request; S-48 gives it the same
		// `purpose: budget` approval the measured path raises, and that
		// request files its own inbox item. Filing both would put two cards
		// for one pause in the Director's inbox.
	} else if o.CancelCommandIssued {
		// §8.2.2 through the daemon: PauseSessionTasks (room scope),
		// PauseWorkTasks (mission scope) or a single task pause, all of which
		// queue the `cancel` command rather than signalling anything.
		switch {
		case stopUnit == scopeRoom:
			if err := s.Tasks.PauseSessionTasks(ctx, tx, b.SessionID, sessions.PauseBudget, raw, now); err != nil {
				return err
			}
		case stopUnit == scopeWork:
			if err := s.Tasks.PauseWorkTasks(ctx, tx, *b.WorkID, sessions.PauseBudget, raw, now); err != nil {
				return err
			}
		case tasks.Terminal(tasks.Status(b.TaskStatus)):
			// S-44, the post-turn case: the overrun was found after `finish`,
			// so there is no turn to cancel and no task to park — `completed`
			// has no edge to `paused` (E5), and inventing one would contradict
			// a completion the session has already acted on. The next task on
			// this lane is what a per-task budget can still stop, so the LANE
			// takes the pause and the HITL below still names the task that
			// crossed the line (FR-7.3 s-13).
			if err := s.Tasks.PauseLaneForBudget(ctx, tx, b.LaneID, now); err != nil {
				return err
			}
		default:
			if err := s.Tasks.PauseTaskForBudget(ctx, tx, b.TaskID, raw, now); err != nil {
				return err
			}
		}
	}
	if !o.HitlIssued {
		return nil
	}
	// The platform asks whether to continue. `purpose` is what tells this
	// request apart from the completion approval and the loop pause — all
	// three are `source: system` + `approval` (0012, E9-01).
	//
	// Who is asked follows what stopped: the ROOM's owner for the room gate
	// (FR-2A.3, absent-owner hand-over), the mission's Director otherwise —
	// or the room owner for a run outside any mission (FR-2A.1).
	question := fmt.Sprintf("%s의 작업이 예산 $%.2f를 넘었습니다 (현재 $%.2f). 계속할까요?", b.AgentName, limit, spent)
	switch scope {
	case scopeRoom:
		question = fmt.Sprintf("방이 예산 $%.2f를 넘었습니다 (현재 $%.2f). 계속할까요?", limit, spent)
	case scopeWork:
		question = fmt.Sprintf("미션이 예산 $%.2f를 넘었습니다 (현재 $%.2f). 계속할까요?", limit, spent)
	}
	if o.TurnDrained {
		// The Director has to be told that the number is OURS, or "$0.06 spent
		// of $0.05" reads as a measurement and the raise they pick is based on
		// a precision the price table does not have (FR-7.3, E9-05).
		question += " (추정 비용 · 진행 중인 턴은 끝까지 둡니다)"
	}
	var taskRef, workRef *uuid.UUID
	if o.HitlTaskID != uuid.Nil {
		// FR-7.3 s-13: a TASK budget HITL must name its task, or there is
		// nothing to resume. A room or mission one leaves it empty — it is
		// answered by lifting that stop (K-10).
		id := o.HitlTaskID
		taskRef = &id
	}
	spec, approver := hitl.SpecDirector, b.Director
	switch {
	case stopUnit == scopeRoom:
		spec, approver = hitl.SpecRoomOwner, b.RoomOwner
	case stopUnit == scopeWork:
		workRef = b.WorkID
	case b.WorkID == nil:
		spec = hitl.SpecRoomOwner
	default:
		workRef = b.WorkID
	}
	var hitlID uuid.UUID
	// `hitl_request_one_open_per_task` (0001) is partial — `WHERE task_id IS
	// NOT NULL` — so ON CONFLICT alone does not dedupe a room- or
	// mission-scoped request, and S-48 makes the estimated path raise exactly
	// those on a signal that repeats every heartbeat. enforceBudgetFor's
	// "already stopped" guard already stops the second heartbeat, but that
	// guard is one caller's; the uniqueness belongs here.
	err := tx.QueryRow(ctx, `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at, work_id)
		SELECT $1, $2, 'system', $3, $4, $8, $5, $6, $7, $9
		WHERE NOT EXISTS (
		      SELECT 1 FROM hitl_request h
		       WHERE h.session_id = $1 AND h.status = 'open' AND h.source = 'system'
		         AND h.purpose = $5 AND h.task_id IS NOT DISTINCT FROM $2
		         AND h.work_id IS NOT DISTINCT FROM $9)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		b.SessionID, taskRef, o.HitlType, question, o.HitlPurpose, now.Add(hitl.DefaultDueIn), now, spec, workRef).Scan(&hitlID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // a request for this task is already open
	}
	if err != nil {
		return err
	}
	// S-45: the timeline card. The request row and the inbox card were the
	// whole of this path, so the Director saw the pause in the inbox and the
	// session's own timeline showed nothing to answer (SCREEN §4.5). The card
	// is posted AFTER the insert on purpose — the ON CONFLICT above is what
	// decides whether there is a request at all, and posting first would leave
	// a card for a request that was never created.
	//
	// This covers the post-turn path S-44 added too: it reaches the same
	// insert, with taskRef naming the task that crossed the line.
	if err := s.attachHitlCard(ctx, tx, b.WorkspaceID, b.SessionID, hitlID, messages.HitlCard{
		Type: o.HitlType, Question: question, SourceTaskID: taskRef, WorkID: workRef,
	}, now); err != nil {
		return err
	}
	if stopUnit == scopeRoom {
		// FR-8 v0.19: the room gate went up — ONE `room_paused` card for the
		// room owner's chain, the approval being that card's action (the same
		// card a loop stop files, router.pauseForLoop). A `hitl_request` card
		// here was a second kind of card for the same stop (#311 ①).
		return roomgate.FileInbox(ctx, tx, roomgate.Item{
			Type: inbox.TypeRoomPaused, WorkspaceID: b.WorkspaceID, RoomID: b.SessionID, HitlID: hitlID,
			Created: now, Due: now.Add(hitl.DefaultDueIn),
		})
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, work_id)
		SELECT m.id, $4::inbox_item_type, $5::inbox_severity, $1, $2, $3, $8
		FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
		b.SessionID, hitlID, now, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest), b.WorkspaceID, approver, workRef)
	return err
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

var _ = contracts.Usage{}

// attachHitlCard posts the timeline card for a system-issued request and links
// it back (openapi HitlRequest.message_id). Filling the column is what makes
// the card findable from the request — S7's card and the inbox item lead to
// the same row, and a NULL there reads as "this request has no card".
func (s *Server) attachHitlCard(ctx context.Context, tx pgx.Tx, wsID, sessionID, hitlID uuid.UUID, card messages.HitlCard, now time.Time) error {
	msgID, err := messages.PostHitlCard(ctx, s.Hub, tx, wsID, sessionID, card, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE hitl_request SET message_id = $2 WHERE id = $1`, hitlID, msgID)
	return err
}
