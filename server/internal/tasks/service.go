package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

var (
	ErrNotFound     = errors.New("tasks: task not found")
	ErrStaleAttempt = errors.New("tasks: attempt is not the current one")
	// ErrInvalidSessionRef — finish carried a runtime_session_ref without the
	// keys harness.md §6 requires (runtime_kind, session_id). Rejected before the
	// lane CHECK (0004) can turn it into a 500.
	ErrInvalidSessionRef = errors.New("tasks: runtime_session_ref needs runtime_kind and session_id")
	ErrLaneNotFound      = errors.New("tasks: lane not found")
	// ErrLaneNotCancellable — cancelLane on a lane that is not running/queued
	// (openapi cancelLane: already-terminal lanes answer 409).
	ErrLaneNotCancellable = errors.New("tasks: lane is not running or queued")
)

type Service struct {
	DB     *pgxpool.Pool
	Clock  clock.Clock
	Tokens *tokens.Service
	Hub    *realtime.Hub
}

func New(pool *pgxpool.Pool, c clock.Clock, t *tokens.Service, h *realtime.Hub) *Service {
	return &Service{DB: pool, Clock: c, Tokens: t, Hub: h}
}

// Row is one task row (columns the API and the queue need).
type Row struct {
	ID                  uuid.UUID
	LaneID              uuid.UUID
	SessionID           uuid.UUID
	WorkspaceID         uuid.UUID
	RuntimeID           *uuid.UUID
	AgentID             uuid.UUID
	ProfileID           uuid.UUID
	TriggerMessageID    *uuid.UUID
	DelegatedFromTaskID *uuid.UUID
	RestartedFromTaskID *uuid.UUID
	OriginatorUserID    *uuid.UUID
	CoalescedMessageIDs []uuid.UUID
	Attempt             int
	MaxAttempts         int
	PendingHitl         bool
	BudgetOverride      *float64
	Status              Status
	PausedReason        *string
	FailureKind         *string
	NotBefore           *time.Time
	StopReason          *string
	HeartbeatAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DispatchedAt        *time.Time
	StartedAt           *time.Time
	FinishedAt          *time.Time
}

const selectTask = `
	SELECT t.id, t.lane_id, t.session_id, s.workspace_id, t.runtime_id, t.agent_id, t.profile_id,
	       t.trigger_message_id, t.delegated_from_task_id, t.restarted_from_task_id, t.originator_user_id,
	       t.coalesced_message_ids, t.attempt, t.max_attempts, t.pending_hitl, t.budget_override,
	       t.status, t.paused_reason, t.failure_kind, t.not_before, t.stop_reason, t.heartbeat_at,
	       t.created_at, t.updated_at, t.dispatched_at, t.started_at, t.finished_at
	FROM task t JOIN session s ON s.id = t.session_id`

func scanTask(row pgx.Row) (*Row, error) {
	var t Row
	var status, pausedReason, failureKind *string
	err := row.Scan(&t.ID, &t.LaneID, &t.SessionID, &t.WorkspaceID, &t.RuntimeID, &t.AgentID, &t.ProfileID,
		&t.TriggerMessageID, &t.DelegatedFromTaskID, &t.RestartedFromTaskID, &t.OriginatorUserID,
		&t.CoalescedMessageIDs, &t.Attempt, &t.MaxAttempts, &t.PendingHitl, &t.BudgetOverride,
		&status, &pausedReason, &failureKind, &t.NotBefore, &t.StopReason, &t.HeartbeatAt,
		&t.CreatedAt, &t.UpdatedAt, &t.DispatchedAt, &t.StartedAt, &t.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tasks: scan: %w", err)
	}
	t.Status = Status(*status)
	t.PausedReason = pausedReason
	t.FailureKind = failureKind
	if t.CoalescedMessageIDs == nil {
		t.CoalescedMessageIDs = []uuid.UUID{}
	}
	return &t, nil
}

// Get loads a task (no lock).
func Get(ctx context.Context, q db.DBTX, id uuid.UUID) (*Row, error) {
	return scanTask(q.QueryRow(ctx, selectTask+` WHERE t.id = $1`, id))
}

func lockTask(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Row, error) {
	return scanTask(tx.QueryRow(ctx, selectTask+` WHERE t.id = $1 FOR UPDATE OF t`, id))
}

// Attempt is one task_attempt row (Task.attempts[]).
type Attempt struct {
	Attempt      int
	RuntimeID    *uuid.UUID
	DispatchedAt *time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	Outcome      *string
	FailureKind  *string
	Resumed      *bool
	StopReason   *string
}

func ListAttempts(ctx context.Context, q db.DBTX, taskID uuid.UUID) ([]Attempt, error) {
	rows, err := q.Query(ctx, `
		SELECT attempt, runtime_id, dispatched_at, started_at, finished_at, outcome, failure_kind, resumed, stop_reason
		FROM task_attempt WHERE task_id = $1 ORDER BY attempt`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.Attempt, &a.RuntimeID, &a.DispatchedAt, &a.StartedAt, &a.FinishedAt, &a.Outcome, &a.FailureKind, &a.Resumed, &a.StopReason); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Usage is the task_usage row.
type Usage struct {
	InputTokens, OutputTokens, CacheRead int64
	CostUSD                              float64
	Estimated                            bool
	UpdatedAt                            time.Time
}

func GetUsage(ctx context.Context, q db.DBTX, taskID uuid.UUID) (*Usage, error) {
	var u Usage
	err := q.QueryRow(ctx, `SELECT input_tokens, output_tokens, cache_read, cost_usd, estimated, updated_at FROM task_usage WHERE task_id = $1`, taskID).
		Scan(&u.InputTokens, &u.OutputTokens, &u.CacheRead, &u.CostUSD, &u.Estimated, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &u, err
}

// MarkDispatched is the claim transition: queued → dispatched with token issue.
// Called by the queue inside its claim transaction.
func (s *Service) MarkDispatched(ctx context.Context, tx pgx.Tx, t *Row, runtimeID uuid.UUID, now time.Time) (string, error) {
	if _, err := Transition(t.Status, Dispatched); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task SET status = 'dispatched', runtime_id = $2, dispatched_at = $3, heartbeat_at = NULL, updated_at = $3
		WHERE id = $1`, t.ID, runtimeID, now); err != nil {
		return "", fmt.Errorf("tasks: dispatch: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_attempt (task_id, attempt, runtime_id, dispatched_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (task_id, attempt) DO UPDATE SET runtime_id = EXCLUDED.runtime_id, dispatched_at = EXCLUDED.dispatched_at`,
		t.ID, t.Attempt, runtimeID, now); err != nil {
		return "", fmt.Errorf("tasks: attempt row: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'running', updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
		return "", err
	}
	token, err := s.Tokens.Issue(ctx, tx, tokens.Scope{
		TaskID: t.ID, Attempt: t.Attempt, LaneID: t.LaneID, SessionID: t.SessionID, AgentID: t.AgentID, RuntimeID: &runtimeID,
	})
	if err != nil {
		return "", err
	}
	t.Status, t.RuntimeID, t.DispatchedAt = Dispatched, &runtimeID, &now
	s.publish(ctx, tx, t)
	return token, nil
}

// Phase records the daemon's preparing / running report (daemon-protocol §4.2).
func (s *Service) Phase(ctx context.Context, taskID uuid.UUID, attempt int, phase string) error {
	now := s.Clock.Now()
	return s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if t.Attempt != attempt {
			return ErrStaleAttempt
		}
		var to Status
		switch phase {
		case "preparing":
			to = Preparing
		case "running":
			to = Running
		default:
			return fmt.Errorf("tasks: unknown phase %q", phase)
		}
		if t.Status == to {
			return nil // idempotent repeat
		}
		if _, err := Transition(t.Status, to); err != nil {
			return err
		}
		started := t.StartedAt
		if to == Running && started == nil {
			started = &now
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task SET status = $2, heartbeat_at = $3, started_at = $4, updated_at = $3 WHERE id = $1`,
			t.ID, string(to), now, started); err != nil {
			return fmt.Errorf("tasks: phase: %w", err)
		}
		if to == Running {
			_, _ = tx.Exec(ctx, `UPDATE task_attempt SET started_at = COALESCE(started_at, $3) WHERE task_id = $1 AND attempt = $2`, t.ID, t.Attempt, now)
		}
		t.Status, t.HeartbeatAt, t.StartedAt = to, &now, started
		s.publish(ctx, tx, t)
		return nil
	})
}

// Heartbeat refreshes heartbeat_at (every 15s while running).
func (s *Service) Heartbeat(ctx context.Context, taskID uuid.UUID, attempt int, now time.Time) error {
	tag, err := s.DB.Exec(ctx, `
		UPDATE task SET heartbeat_at = $3 WHERE id = $1 AND attempt = $2 AND status IN ('preparing', 'running')`,
		taskID, attempt, now)
	if err != nil {
		return fmt.Errorf("tasks: heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleAttempt
	}
	return nil
}

// Requeue ends the current attempt with reason and either queues attempt+1
// (retryable kinds with attempts left) or fails the task. The attempt's token
// is revoked either way (daemon-protocol §5, §7).
func (s *Service) Requeue(ctx context.Context, taskID uuid.UUID, reason contracts.FailureKind, notBefore *time.Time, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		return s.requeueLocked(ctx, tx, t, reason, notBefore, now)
	})
}

func (s *Service) requeueLocked(ctx context.Context, tx pgx.Tx, t *Row, reason contracts.FailureKind, notBefore *time.Time, now time.Time) error {
	if Terminal(t.Status) {
		return nil
	}
	// E10-04: once a person cancelled the lane, no attempt is requeued — a
	// daemon that dies or reports failed after the cancel command still ends
	// the task as cancelled (no new task).
	if requested, err := cancelRequested(ctx, tx, t.ID, t.Attempt); err != nil {
		return err
	} else if requested {
		return s.cancelLocked(ctx, tx, t, string(reason), now)
	}
	if err := s.Tokens.Revoke(ctx, tx, t.ID, t.Attempt, "requeue"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_attempt (task_id, attempt, runtime_id, finished_at, outcome, failure_kind)
		VALUES ($1, $2, $3, $4, $5, $6::failure_kind)
		ON CONFLICT (task_id, attempt) DO UPDATE SET finished_at = EXCLUDED.finished_at, outcome = EXCLUDED.outcome, failure_kind = EXCLUDED.failure_kind`,
		t.ID, t.Attempt, t.RuntimeID, now, string(reason), string(reason)); err != nil {
		return fmt.Errorf("tasks: record attempt: %w", err)
	}
	if reason.Retryable() && t.Attempt < t.MaxAttempts {
		if _, err := Transition(t.Status, Queued); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task SET status = 'queued', attempt = attempt + 1, failure_kind = NULL, not_before = $2,
			       runtime_id = NULL, heartbeat_at = NULL, dispatched_at = NULL, started_at = NULL, updated_at = $3
			WHERE id = $1`, t.ID, notBefore, now); err != nil {
			return fmt.Errorf("tasks: requeue: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'queued', updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
			return err
		}
		t.Status, t.Attempt, t.FailureKind, t.NotBefore, t.RuntimeID = Queued, t.Attempt+1, nil, notBefore, nil
	} else {
		if _, err := Transition(t.Status, Failed); err != nil {
			return err
		}
		fk := string(reason)
		if _, err := tx.Exec(ctx, `
			UPDATE task SET status = 'failed', failure_kind = $2, finished_at = $3, heartbeat_at = NULL, updated_at = $3 WHERE id = $1`,
			t.ID, fk, now); err != nil {
			return fmt.Errorf("tasks: fail: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'failed', finished_at = $2, updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
			return err
		}
		t.Status, t.FailureKind, t.FinishedAt = Failed, &fk, &now
	}
	s.publish(ctx, tx, t)
	return nil
}

// ExpireStale is the scheduler sweep (daemon-protocol §7):
//   - dispatched/preparing with no running report 5 minutes after dispatch → timeout (E5-02, §4.1)
//   - running with no heartbeat for 3 minutes → runtime_offline,
//     runtime marked offline (E5-03, E11-03; §4.2 v0.2 — preparing is not a heartbeat subject)
//   - runtimes silent for 3 minutes → offline
func (s *Service) ExpireStale(ctx context.Context, now time.Time) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		// §4.1: dispatched and preparing are bounded by 5 minutes from dispatch;
		// preparing is not a heartbeat subject (§4.2 v0.2, N5).
		ids, err := collectIDs(tx.Query(ctx, `
			SELECT id FROM task WHERE status IN ('dispatched', 'preparing') AND dispatched_at < $1 FOR UPDATE SKIP LOCKED`,
			now.Add(-contracts.DispatchedTimeout)))
		if err != nil {
			return err
		}
		for _, id := range ids {
			t, err := lockTask(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := s.requeueLocked(ctx, tx, t, contracts.FailTimeout, nil, now); err != nil {
				return err
			}
			n++
		}
		ids, err = collectIDs(tx.Query(ctx, `
			SELECT id FROM task WHERE status = 'running'
			  AND COALESCE(heartbeat_at, started_at, dispatched_at) < $1 FOR UPDATE SKIP LOCKED`,
			now.Add(-contracts.HeartbeatExpiry)))
		if err != nil {
			return err
		}
		for _, id := range ids {
			t, err := lockTask(ctx, tx, id)
			if err != nil {
				return err
			}
			rt := t.RuntimeID
			if err := s.requeueLocked(ctx, tx, t, contracts.FailRuntimeOffline, nil, now); err != nil {
				return err
			}
			if rt != nil {
				if _, err := tx.Exec(ctx, `
					UPDATE runtime SET status = 'offline', offline_since = COALESCE(offline_since, $2), updated_at = $2
					WHERE id = $1 AND (last_seen_at IS NULL OR last_seen_at < $3)`, *rt, now, now.Add(-contracts.HeartbeatExpiry)); err != nil {
					return err
				}
			}
			n++
		}
		_, err = tx.Exec(ctx, `
			UPDATE runtime SET status = 'offline', offline_since = COALESCE(offline_since, $1), updated_at = $1
			WHERE status = 'online' AND (last_seen_at IS NULL OR last_seen_at < $2)`, now, now.Add(-contracts.HeartbeatExpiry))
		return err
	})
	return n, err
}

// Finish applies the daemon's end-of-attempt report (daemon-protocol §4.4).
// Idempotent per attempt: a repeat returns the recorded status. The returned
// status is the task's final state (server decides — §4.4).
func (s *Service) Finish(ctx context.Context, taskID uuid.UUID, attempt int, f contracts.Finish) (Status, error) {
	now := s.Clock.Now()
	var final Status
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if attempt != t.Attempt {
			var outcome *string
			if err := tx.QueryRow(ctx, `SELECT outcome FROM task_attempt WHERE task_id = $1 AND attempt = $2`, t.ID, attempt).Scan(&outcome); err == nil && outcome != nil {
				final = Status(*outcome)
				return nil
			}
			return ErrStaleAttempt
		}
		var finished *time.Time
		var outcome *string
		_ = tx.QueryRow(ctx, `SELECT finished_at, outcome FROM task_attempt WHERE task_id = $1 AND attempt = $2`, t.ID, attempt).Scan(&finished, &outcome)
		if finished != nil || Terminal(t.Status) {
			final = t.Status
			return nil
		}
		resumed := f.ResumeOutcome == "resumed"
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_attempt (task_id, attempt, runtime_id, finished_at, outcome, resumed, stop_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (task_id, attempt) DO UPDATE SET finished_at = EXCLUDED.finished_at, outcome = EXCLUDED.outcome,
			  resumed = EXCLUDED.resumed, stop_reason = EXCLUDED.stop_reason`,
			t.ID, attempt, t.RuntimeID, now, f.Outcome, resumed, f.StopReason); err != nil {
			return fmt.Errorf("tasks: finish attempt: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_usage (task_id, input_tokens, output_tokens, cache_read, cost_usd, estimated, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (task_id) DO UPDATE SET input_tokens = EXCLUDED.input_tokens, output_tokens = EXCLUDED.output_tokens,
			  cache_read = EXCLUDED.cache_read, cost_usd = EXCLUDED.cost_usd, estimated = EXCLUDED.estimated, updated_at = EXCLUDED.updated_at`,
			t.ID, f.Usage.InputTokens, f.Usage.OutputTokens, f.Usage.CacheReadTokens, f.Usage.CostUSD, f.Usage.Estimated, now); err != nil {
			return fmt.Errorf("tasks: usage: %w", err)
		}
		// harness.md §6: the ref is stored verbatim (contracts.RuntimeSessionRef →
		// jsonb with the contract keys) — it is the only basis for the next
		// attempt's TaskBundle.resume. The lane CHECK (0004) requires
		// runtime_kind + session_id; reject earlier with a typed error.
		if f.RuntimeSessionRef != nil {
			if f.RuntimeSessionRef.RuntimeKind == "" || f.RuntimeSessionRef.SessionID == "" {
				return ErrInvalidSessionRef
			}
			if _, err := tx.Exec(ctx, `UPDATE lane SET runtime_session_ref = $2, updated_at = $3 WHERE id = $1`, t.LaneID, f.RuntimeSessionRef, now); err != nil {
				return fmt.Errorf("tasks: runtime_session_ref: %w", err)
			}
		}
		decided := f.Outcome
		if decided != "completed" {
			// A cancel command was issued for this attempt (cancelLane): the
			// daemon's failed/paused report after it is the cancel taking
			// effect, not a retryable failure (E10-04: no requeue).
			if requested, err := cancelRequested(ctx, tx, t.ID, attempt); err != nil {
				return err
			} else if requested {
				decided = "cancelled"
			}
		}
		switch decided {
		case "completed":
			if _, err := Transition(t.Status, Completed); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE task SET status = 'completed', finished_at = $2, stop_reason = $3, heartbeat_at = NULL, updated_at = $2 WHERE id = $1`,
				t.ID, now, f.StopReason); err != nil {
				return err
			}
			if err := s.Tokens.Revoke(ctx, tx, t.ID, attempt, "completed"); err != nil {
				return err
			}
			// lane: another queued task on this lane keeps it queued, else done
			if _, err := tx.Exec(ctx, `
				UPDATE lane SET status = CASE WHEN EXISTS (SELECT 1 FROM task WHERE lane_id = $1 AND status = 'queued') THEN 'queued'::lane_status ELSE 'done'::lane_status END,
				  finished_at = $2, updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
				return err
			}
			t.Status, t.FinishedAt = Completed, &now
		case "cancelled":
			if err := s.cancelLocked(ctx, tx, t, f.StopReason, now); err != nil {
				return err
			}
			final = t.Status
			return nil
		case "paused_budget":
			if _, err := Transition(t.Status, Paused); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE task SET status = 'paused', paused_reason = 'budget', finished_at = NULL, heartbeat_at = NULL, updated_at = $2 WHERE id = $1`,
				t.ID, now); err != nil {
				return err
			}
			if err := s.Tokens.Revoke(ctx, tx, t.ID, attempt, "paused"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'paused', updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
				return err
			}
			t.Status = Paused
		default: // failed
			kind := f.FailureKind
			if kind == "" {
				kind = contracts.FailOther
			}
			if err := s.requeueLocked(ctx, tx, t, kind, f.NotBefore, now); err != nil {
				return err
			}
			final = t.Status
			return nil
		}
		s.publish(ctx, tx, t)
		final = t.Status
		return nil
	})
	return final, err
}

func (s *Service) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tasks: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) publish(ctx context.Context, q db.DBTX, t *Row) {
	if s.Hub == nil {
		return
	}
	sid := t.SessionID
	_ = s.Hub.Publish(ctx, q, t.WorkspaceID, &sid, "task.updated", ToAPI(t, nil, nil))
}

func collectIDs(rows pgx.Rows, err error) ([]uuid.UUID, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CancelLane is cancelLane (openapi, FR-3.4 "중단", E10-04). The lane must be
// running or queued. Its current task is cancelled at once when nothing holds
// it yet (queued/deferred); a dispatched/preparing/running attempt gets a
// daemon `cancel` command {after_current_tool: true, reason: director}
// (daemon-protocol §4.3) and ends when the daemon's finish arrives — the
// command is consumed by that finish and re-sent on every response until
// then. The feed records "사람이 중단함" either way. Returns the task and
// whether it was cancelled immediately.
func (s *Service) CancelLane(ctx context.Context, laneID, byUserID uuid.UUID) (*Row, bool, error) {
	now := s.Clock.Now()
	var out *Row
	immediate := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var laneStatus string
		err := tx.QueryRow(ctx, `SELECT status FROM lane WHERE id = $1 FOR UPDATE`, laneID).Scan(&laneStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLaneNotFound
		}
		if err != nil {
			return fmt.Errorf("tasks: lock lane: %w", err)
		}
		if laneStatus != "running" && laneStatus != "queued" {
			return ErrLaneNotCancellable
		}
		var taskID uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT id FROM task WHERE lane_id = $1 AND status IN ('deferred', 'queued', 'dispatched', 'preparing', 'running')
			ORDER BY created_at DESC LIMIT 1`, laneID).Scan(&taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLaneNotCancellable
		}
		if err != nil {
			return fmt.Errorf("tasks: current task: %w", err)
		}
		t, err := lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		// A second "중단" while the first cancel is still pending: the command
		// already rides every response (§4.3); nothing to add.
		if requested, err := cancelRequested(ctx, tx, t.ID, t.Attempt); err != nil {
			return err
		} else if requested {
			out = t
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
			VALUES ($1, $2, (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
			        'status', 'cancel', to_jsonb($4::text), 'ok', $5, $6)`,
			t.ID, t.Attempt, serverSeqBase, "director",
			map[string]any{"command": "lane cancel", "note": "사람이 중단함", "requested_by": byUserID.String(), "reason": "director"}, now); err != nil {
			return fmt.Errorf("tasks: cancel feed event: %w", err)
		}
		switch t.Status {
		case Dispatched, Preparing, Running:
			if t.RuntimeID != nil {
				if err := tokens.QueueCommand(ctx, tx, *t.RuntimeID, contracts.Command{
					Type: contracts.CmdCancel, TaskID: t.ID.String(), Attempt: t.Attempt, AfterCurrentTool: true, Reason: "director",
				}); err != nil {
					return err
				}
				out = t
				return nil
			}
		}
		if err := s.cancelLocked(ctx, tx, t, "director", now); err != nil {
			return err
		}
		immediate = true
		out = t
		return nil
	})
	return out, immediate, err
}

// serverSeqBase mirrors router.ServerSeqBase (the router package imports
// tasks, so the constant is repeated here): server-recorded status events
// live above the daemon's seq range.
const serverSeqBase = 1 << 30

// cancelRequested reports whether a cancel command was issued for (task, attempt).
func cancelRequested(ctx context.Context, q db.DBTX, taskID uuid.UUID, attempt int) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM daemon_command WHERE task_id = $1 AND attempt = $2 AND type = 'cancel')`, taskID, attempt).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("tasks: cancel requested: %w", err)
	}
	return ok, nil
}

// cancelLocked ends the task as cancelled (failure_kind cancelled — openapi
// cancelLane), records the attempt, revokes its token and marks the lane
// failed(cancelled). No requeue.
func (s *Service) cancelLocked(ctx context.Context, tx pgx.Tx, t *Row, stopReason string, now time.Time) error {
	if _, err := Transition(t.Status, Cancelled); err != nil {
		return err
	}
	var stop *string
	if stopReason != "" {
		stop = &stopReason
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task SET status = 'cancelled', failure_kind = 'cancelled', paused_reason = NULL, finished_at = $2, stop_reason = $3,
		       heartbeat_at = NULL, updated_at = $2 WHERE id = $1`, t.ID, now, stop); err != nil {
		return fmt.Errorf("tasks: cancel: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_attempt (task_id, attempt, runtime_id, finished_at, outcome, failure_kind, stop_reason)
		VALUES ($1, $2, $3, $4, 'cancelled', 'cancelled', $5)
		ON CONFLICT (task_id, attempt) DO UPDATE SET finished_at = COALESCE(task_attempt.finished_at, EXCLUDED.finished_at),
		  outcome = 'cancelled', failure_kind = 'cancelled', stop_reason = COALESCE(task_attempt.stop_reason, EXCLUDED.stop_reason)`,
		t.ID, t.Attempt, t.RuntimeID, now, stop); err != nil {
		return fmt.Errorf("tasks: cancel attempt: %w", err)
	}
	if err := s.Tokens.Revoke(ctx, tx, t.ID, t.Attempt, "cancelled"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE lane SET status = 'failed', finished_at = $2, updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
		return err
	}
	fk := string(contracts.FailCancelled)
	t.Status, t.FailureKind, t.FinishedAt, t.StopReason, t.PausedReason = Cancelled, &fk, &now, stop, nil
	s.publish(ctx, tx, t)
	return nil
}
