package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
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
type budgetState struct {
	TaskID, SessionID, WorkspaceID, LaneID, AgentID uuid.UUID
	AgentBudgetPerTask                              *float64
	TaskOverride                                    *float64
	SessionLimitUSD                                 float64
	TaskSpentUSD                                    float64
	SessionSpentUSD                                 float64
	Estimated                                       bool
	TaskStatus                                      string
	SessionStatus                                   string
	Attempt                                         int
	Director                                        uuid.UUID
	AgentName                                       string
}

func (s *Server) loadBudgetState(ctx context.Context, q pgx.Tx, taskID uuid.UUID) (*budgetState, error) {
	var b budgetState
	var limits []byte
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
		       s.limits, t.status::text, s.status::text, s.director_user_id, a.name,
		       COALESCE(u.cost_usd, 0), COALESCE(u.estimated, false),
		       COALESCE((SELECT sum(uu.cost_usd) FROM task_usage uu JOIN task tt ON tt.id = uu.task_id
		                 WHERE tt.session_id = t.session_id), 0)
		FROM task t
		JOIN session s ON s.id = t.session_id
		JOIN agent a ON a.id = t.agent_id
		LEFT JOIN task_usage u ON u.task_id = t.id
		WHERE t.id = $1`, taskID).
		Scan(&b.TaskID, &b.SessionID, &b.WorkspaceID, &b.LaneID, &b.AgentID, &b.Attempt,
			&b.AgentBudgetPerTask, &b.TaskOverride, &limits, &b.TaskStatus, &b.SessionStatus, &b.Director, &b.AgentName,
			&b.TaskSpentUSD, &b.Estimated, &b.SessionSpentUSD)
	if err != nil {
		return nil, err
	}
	b.SessionLimitUSD = budgetOf(limits)
	return &b, nil
}

// sessionRemaining is D-16's "세션 잔여" for THIS task: the session budget less
// what the OTHER tasks have spent. Its own spend is left in, because that is
// the number its limit is compared against. Zero means the session carries no
// budget — never "nothing left", or a session without a limit would pin every
// task to zero (daemon-protocol v0.7.1 §4.4).
func (b *budgetState) sessionRemaining() float64 {
	if b.SessionLimitUSD <= 0 {
		return 0
	}
	rem := b.SessionLimitUSD - (b.SessionSpentUSD - b.TaskSpentUSD)
	if rem < 0 {
		return 0
	}
	return rem
}

// EnforceBudget checks the task and then the session limit and applies
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
		if b.SessionStatus != "active" {
			// Already paused or over; nothing to decide.
			return nil
		}
		// The task limit is checked first: it is the tighter one, and pausing
		// the session for a single runaway task would stop the other lanes
		// that are inside their own budgets (FR-7.3).
		o := sessions.PlanBudget(sessions.BudgetInput{
			Scope: "task", TaskID: b.TaskID,
			TaskLimitUSD: derefFloat(b.AgentBudgetPerTask), OverrideUSD: derefFloat(b.TaskOverride),
			SessionRemainingUSD: b.sessionRemaining(),
			SpentUSD:            b.TaskSpentUSD, Estimated: b.Estimated,
		})
		if !o.Exceeded {
			o = sessions.PlanBudget(sessions.BudgetInput{
				Scope: "session", TaskID: b.TaskID,
				SessionLimitUSD: b.SessionLimitUSD, SpentUSD: b.SessionSpentUSD, Estimated: b.Estimated,
			})
		}
		if !o.Exceeded {
			return nil
		}
		paused = true
		return s.applyBudgetPause(ctx, tx, b, o, now)
	})
	return paused, err
}

func (s *Server) applyBudgetPause(ctx context.Context, tx pgx.Tx, b *budgetState, o sessions.BudgetOutcome, now time.Time) error {
	spent := b.TaskSpentUSD
	limit := sessions.EffectiveTaskLimit(derefFloat(b.AgentBudgetPerTask), derefFloat(b.TaskOverride), b.sessionRemaining())
	if o.HitlTaskID == uuid.Nil {
		spent, limit = b.SessionSpentUSD, b.SessionLimitUSD
	}
	detail := tasks.WithBudget(tasks.PausedDetail(sessions.PauseBudget, now), float32(limit), float32(spent))
	raw, _ := json.Marshal(detail)

	if o.SessionState == "paused" {
		if _, err := tx.Exec(ctx, `
			UPDATE session SET status = 'paused', paused_reason = 'budget', paused_detail = $2, updated_at = $3
			WHERE id = $1`, b.SessionID, raw, now); err != nil {
			return err
		}
	}
	if o.TurnDrained {
		// FR-7.3's last bullet: an ESTIMATED cost never hard-cuts. The running
		// turn finishes; only new dispatch stops (the claim query's
		// `s.status = 'active'` guard does that). Killing real work on our own
		// guess is the failure the rule names (E9-05).
		if err := tasks.InsertServerEvent(ctx, tx, b.TaskID, b.Attempt, "status", "pause", "budget", "info",
			map[string]any{
				"note":      "추정 비용이 한도를 넘어 세션을 일시정지했습니다 — 진행 중인 턴은 끝까지 둡니다",
				"estimated": true, "spent_usd": spent, "limit_usd": limit,
			}, now); err != nil {
			return err
		}
		// E9-05's other half: "세션 `paused` + Dir 알림". The feed event is
		// what the session shows; the Director is somewhere else. PlanBudget
		// has set DirectorNotified since P3 and nothing implemented it, so an
		// estimated overrun paused the session in silence — the one pause that
		// raises no HITL is exactly the one that needs the inbox card (S-44).
		if err := s.notifyDirectorPaused(ctx, tx, b, now); err != nil {
			return err
		}
	} else if o.CancelCommandIssued {
		// §8.2.2 through the daemon: PauseSessionTasks (session scope) or a
		// single task pause, both of which queue the `cancel` command rather
		// than signalling anything.
		switch {
		case o.SessionState == "paused":
			if err := s.Tasks.PauseSessionTasks(ctx, tx, b.SessionID, sessions.PauseBudget, raw, now); err != nil {
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
	// The platform asks the Director whether to continue. `purpose` is what
	// tells this request apart from the completion approval and the loop pause
	// — all three are `source: system` + `approval` (0012, E9-01).
	question := fmt.Sprintf("%s의 작업이 예산 $%.2f를 넘었습니다 (현재 $%.2f). 계속할까요?", b.AgentName, limit, spent)
	if o.HitlTaskID == uuid.Nil {
		question = fmt.Sprintf("세션이 예산 $%.2f를 넘었습니다 (현재 $%.2f). 계속할까요?", limit, spent)
	}
	var taskRef *uuid.UUID
	if o.HitlTaskID != uuid.Nil {
		// FR-7.3 s-13: a TASK budget HITL must name its task, or there is
		// nothing to resume. A SESSION one leaves it empty — it is answered by
		// resuming the session.
		id := o.HitlTaskID
		taskRef = &id
	}
	var hitlID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at)
		VALUES ($1, $2, 'system', $3, $4, 'director', $5, $6, $7)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		b.SessionID, taskRef, o.HitlType, question, o.HitlPurpose, now.Add(hitl.DefaultDueIn), now).Scan(&hitlID)
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
		Type: o.HitlType, Question: question, SourceTaskID: taskRef,
	}, now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
		SELECT m.id, $4::inbox_item_type, $5::inbox_severity, $1, $2, $3
		FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
		b.SessionID, hitlID, now, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest), b.WorkspaceID, b.Director)
	return err
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

var _ = contracts.Usage{}

// notifyDirectorPaused is the inbox card for a pause that issues no HITL: the
// estimated overrun (E9-05). `session_paused` is the item type FR-8 names for
// it, and the card's actions (`approve_continue`) are already wired — nothing
// had ever inserted one. The card's body is the session's own paused_reason,
// which applyBudgetPause has just written, so `ref_id` carries the session.
//
// It is inserted only while the session is still `active` (enforceBudgetFor's
// guard), so the Director gets one card per pause, not one per heartbeat.
func (s *Server) notifyDirectorPaused(ctx context.Context, tx pgx.Tx, b *budgetState, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
		SELECT m.id, $3::inbox_item_type, $4::inbox_severity, $1, $1, $2
		FROM member m WHERE m.workspace_id = $5 AND m.user_id = $6`,
		b.SessionID, now, inbox.TypeSessionPaused, inbox.Severity(inbox.TypeSessionPaused), b.WorkspaceID, b.Director)
	return err
}

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
