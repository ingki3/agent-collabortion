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
		       a.budget_per_task, t.budget_override, s.limits, s.status::text, s.director_user_id, a.name,
		       COALESCE(u.cost_usd, 0), COALESCE(u.estimated, false),
		       COALESCE((SELECT sum(uu.cost_usd) FROM task_usage uu JOIN task tt ON tt.id = uu.task_id
		                 WHERE tt.session_id = t.session_id), 0)
		FROM task t
		JOIN session s ON s.id = t.session_id
		JOIN agent a ON a.id = t.agent_id
		LEFT JOIN task_usage u ON u.task_id = t.id
		WHERE t.id = $1`, taskID).
		Scan(&b.TaskID, &b.SessionID, &b.WorkspaceID, &b.LaneID, &b.AgentID, &b.Attempt,
			&b.AgentBudgetPerTask, &b.TaskOverride, &limits, &b.SessionStatus, &b.Director, &b.AgentName,
			&b.TaskSpentUSD, &b.Estimated, &b.SessionSpentUSD)
	if err != nil {
		return nil, err
	}
	b.SessionLimitUSD = budgetOf(limits)
	return &b, nil
}

// EnforceBudget checks the task and then the session limit and applies
// PlanBudget's verdict. It returns true when it paused something, so the caller
// can tell the daemon (the cancel command rides the same response).
//
// Production entry points: daemonHeartbeat (the `usage` field) and
// tasks.Finish's rollup, via Server.enforceBudgetFor.
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
			SpentUSD: b.TaskSpentUSD, Estimated: b.Estimated,
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
	limit := sessions.EffectiveTaskLimit(derefFloat(b.AgentBudgetPerTask), derefFloat(b.TaskOverride))
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
	} else if o.CancelCommandIssued {
		// §8.2.2 through the daemon: PauseSessionTasks (session scope) or a
		// single task pause, both of which queue the `cancel` command rather
		// than signalling anything.
		if o.SessionState == "paused" {
			if err := s.Tasks.PauseSessionTasks(ctx, tx, b.SessionID, sessions.PauseBudget,
				fmt.Sprintf("세션 예산 $%.2f 초과", limit), now); err != nil {
				return err
			}
		} else if err := s.Tasks.PauseTaskForBudget(ctx, tx, b.TaskID,
			fmt.Sprintf("task 예산 $%.2f 초과", limit), now); err != nil {
			return err
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
