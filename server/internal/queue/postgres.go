package queue

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
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Postgres implements Queue on the task table (daemon-protocol §4.1 rules).
type Postgres struct {
	DB       *pgxpool.Pool
	Clock    clock.Clock
	Tasks    *tasks.Service
	Notifier *Notifier
}

var _ Queue = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool, c clock.Clock, t *tasks.Service, n *Notifier) *Postgres {
	return &Postgres{DB: pool, Clock: c, Tasks: t, Notifier: n}
}

// Claim hands out up to capacity queued tasks to runtimeID:
//   - only sessions fixed to this runtime (E11-09); a `none` session with no
//     runtime is fixed to the first claimer (E11-10) — but only a runtime of
//     the session's own workspace is ever a candidate (FR-2.1 M10, FR-1.9):
//     a daemon paired to workspace B must never claim, and thereby pin,
//     workspace A's session
//   - session must be active (paused → nothing, E5-04)
//   - not_before must have passed (rate_limited)
//   - one in-flight task per lane (FR-6.3)
//
// Each claimed task moves queued → dispatched and gets a fresh task token.
func (p *Postgres) Claim(ctx context.Context, runtimeID string, capacity int, now time.Time) ([]contracts.TaskBundle, error) {
	rt, err := uuid.Parse(runtimeID)
	if err != nil {
		return nil, fmt.Errorf("queue: runtime id: %w", err)
	}
	if capacity <= 0 {
		return []contracts.TaskBundle{}, nil
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// FR-6.3's four concurrency layers plus the DAG gate.
	//
	// The counting has to happen INSIDE the statement, not just against the
	// tasks already running: one claim asks for up to `capacity` tasks, so a
	// guard that only reads the current `busy` set would hand out ten at once
	// and blow every limit in a single call. The window functions rank the
	// candidates and each limit admits only as many as it has room for.
	//
	// `waiting_human` and `blocked` are absent from every count on purpose
	// (t-1): both processes have already exited, so holding a slot for them
	// stalls the session while nothing runs. The list is hitl.OccupyingStatuses
	// rather than a literal so the rule has ONE definition — the E7-18 golden
	// checks that function, and a status added here would otherwise be a slot
	// nobody's table knows about.
	rows, err := tx.Query(ctx, `
		WITH busy AS (
			SELECT id, session_id, agent_id, lane_id, runtime_id FROM task
			WHERE status::text = ANY($4)
		),
		cand AS (
			SELECT DISTINCT ON (t.lane_id)
			       t.id, t.session_id, t.agent_id, t.lane_id, t.created_at,
			       COALESCE((s.limits->>'max_parallel_lanes')::int, 5) AS lane_cap,
			       -- worktree shares one workdir per agent (FR-6.1), so that
			       -- agent's lanes run one at a time whatever its own cap says.
			       CASE WHEN s.isolation->>'kind' = 'worktree' THEN 1 ELSE a.max_concurrent_tasks END AS agent_cap,
			       COALESCE((cfg.runtime_policy->>'max_concurrent_tasks')::int, 10) AS runtime_cap,
			       (cfg.runtime_policy->>'max_concurrent_tasks_workspace')::int AS workspace_cap,
			       (SELECT count(DISTINCT b.lane_id) FROM busy b WHERE b.session_id = t.session_id) AS busy_lanes,
			       (SELECT count(*) FROM busy b WHERE b.agent_id = t.agent_id) AS busy_agent,
			       (SELECT count(*) FROM busy b WHERE b.runtime_id = r.id) AS busy_runtime,
			       (SELECT count(*) FROM busy b JOIN session bs ON bs.id = b.session_id
			         WHERE bs.workspace_id = r.workspace_id) AS busy_workspace
			  FROM task t
			  JOIN session s ON s.id = t.session_id
			  JOIN lane l ON l.id = t.lane_id
			  JOIN agent a ON a.id = t.agent_id
			  JOIN runtime r ON r.id = $1
			  -- LEFT: a workspace with no settings row falls back to the
			  -- defaults instead of stopping dispatch altogether.
			  LEFT JOIN workspace_settings cfg ON cfg.workspace_id = r.workspace_id
			 WHERE t.status = 'queued'
			   AND s.status = 'active'
			   -- FR-7.3 / S-44: a lane parked at paused does not dispatch. The
			   -- budget pause that follows a finished turn has no task to park
			   -- (the task is completed), so the lane row IS the gate on the
			   -- next task by that agent; the Director's approval lifts it
			   -- (tasks.ResumeLaneForBudget, tasks.ResumeFromHuman).
			   AND l.status <> 'paused'
			   AND s.workspace_id = r.workspace_id
			   AND (s.runtime_id = r.id OR (s.runtime_id IS NULL AND s.isolation->>'kind' = 'none'))
			   AND (t.not_before IS NULL OR t.not_before <= $2)
			   AND NOT EXISTS (SELECT 1 FROM busy b WHERE b.lane_id = t.lane_id)
			   -- FR-6.2: a lane waits for every lane it depends on to end.
			   AND NOT EXISTS (
			         SELECT 1 FROM lane d WHERE d.id = ANY (l.depends_on)
			           AND d.status NOT IN ('done', 'failed', 'blocked'))
			 ORDER BY t.lane_id, t.created_at
		),
		ranked AS (
			SELECT c.*,
			       row_number() OVER (PARTITION BY c.session_id ORDER BY c.created_at, c.id) AS rn_session,
			       row_number() OVER (PARTITION BY c.agent_id   ORDER BY c.created_at, c.id) AS rn_agent,
			       row_number() OVER (                          ORDER BY c.created_at, c.id) AS rn_all
			  FROM cand c
		)
		SELECT t.id FROM task t
		 WHERE t.id IN (
		       SELECT id FROM ranked
		        WHERE busy_lanes + rn_session <= lane_cap
		          AND busy_agent + rn_agent   <= agent_cap
		          AND busy_runtime + rn_all   <= runtime_cap
		          AND (workspace_cap IS NULL OR busy_workspace + rn_all <= workspace_cap)
		        ORDER BY created_at, id
		        LIMIT $3)
		 ORDER BY t.created_at
		 FOR UPDATE OF t SKIP LOCKED`, rt, now, capacity, hitl.OccupyingStatuses())
	if err != nil {
		return nil, fmt.Errorf("queue: select: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	bundles := []contracts.TaskBundle{}
	for _, id := range ids {
		t, err := tasks.Get(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		// E11-10: fix the session to the first runtime that claims it. The
		// workspace guard mirrors the SELECT so the session can never be pinned
		// to a runtime outside its workspace.
		if _, err := tx.Exec(ctx, `UPDATE session SET runtime_id = $2, updated_at = $3
			WHERE id = $1 AND runtime_id IS NULL
			  AND workspace_id = (SELECT workspace_id FROM runtime WHERE id = $2)`, t.SessionID, rt, now); err != nil {
			return nil, err
		}
		// S-55: one task's bundle may be impossible to build without making the
		// whole claim fail — a runtime whose probe has not landed yet would
		// otherwise take every other session's queued task down with it. The
		// savepoint undoes THIS task's dispatch (it stays `queued` and is
		// retried on the next claim, which is right: the probe is seconds
		// away) and the note below is what makes the wait visible.
		if _, err := tx.Exec(ctx, `SAVEPOINT claim_task`); err != nil {
			return nil, err
		}
		token, err := p.Tasks.MarkDispatched(ctx, tx, t, rt, now)
		if err == nil {
			var b *contracts.TaskBundle
			b, err = buildBundle(ctx, tx, t, rt, token)
			if err == nil {
				if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT claim_task`); err != nil {
					return nil, err
				}
				bundles = append(bundles, *b)
				continue
			}
		}
		if !errors.Is(err, errNoWorkdirRoot) {
			return nil, err
		}
		if _, rerr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT claim_task`); rerr != nil {
			return nil, rerr
		}
		if nerr := noteMissingWorkdirRoot(ctx, tx, t, rt, now); nerr != nil {
			return nil, nerr
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return bundles, nil
}

// ClaimWait is the long-poll form (§4.1 wait_ms ≤ 30s): it returns as soon as
// a claim yields tasks, a queued-task notification arrives (then re-claims),
// or the wait elapses. Wall-clock waiting is real time; task timestamps use
// the injected clock.
func (p *Postgres) ClaimWait(ctx context.Context, runtimeID string, capacity int, wait time.Duration) ([]contracts.TaskBundle, error) {
	if wait > contracts.ClaimMaxWait {
		wait = contracts.ClaimMaxWait
	}
	deadline := time.Now().Add(wait)
	for {
		bundles, err := p.Claim(ctx, runtimeID, capacity, p.Clock.Now())
		if err != nil || len(bundles) > 0 || capacity <= 0 {
			return bundles, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return bundles, nil
		}
		poll := remaining
		if poll > time.Second {
			poll = time.Second
		}
		var wake <-chan struct{}
		if p.Notifier != nil {
			wake = p.Notifier.Wait()
		}
		select {
		case <-ctx.Done():
			return bundles, nil
		case <-wake:
		case <-time.After(poll):
		}
	}
}

func (p *Postgres) Heartbeat(ctx context.Context, taskID string, attempt int, now time.Time) error {
	id, err := uuid.Parse(taskID)
	if err != nil {
		return err
	}
	return p.Tasks.Heartbeat(ctx, id, attempt, now)
}

func (p *Postgres) Requeue(ctx context.Context, taskID string, reason contracts.FailureKind, notBefore *time.Time, now time.Time) error {
	id, err := uuid.Parse(taskID)
	if err != nil {
		return err
	}
	return p.Tasks.Requeue(ctx, id, reason, notBefore, now)
}

func (p *Postgres) ExpireStale(ctx context.Context, now time.Time) (int, error) {
	return p.Tasks.ExpireStale(ctx, now)
}

// errNoBundle is returned when the task's joins are missing (data bug).
var errNoBundle = errors.New("queue: bundle rows missing")

// errNoWorkdirRoot is S-55's refusal: a `worktree` lane whose runtime has not
// reported a `workdir_root` (probe §3) has no absolute path to run in, and
// daemon-protocol v0.7.3 §4.1 forbids the relative one that used to be shipped
// in its place.
var errNoWorkdirRoot = errors.New("queue: runtime has no workdir_root (probe §3) — cannot name an absolute workdir (§4.1 v0.7.3)")

// noteMissingWorkdirRoot puts S-55's refusal on the activity feed. A task that
// silently stays queued is the same silence the relative path was: the
// Director needs to see that this machine's daemon has not probed yet.
//
// `Once` per (task, attempt): the claim long-polls, so a plain insert would
// write this line every second until the probe lands.
func noteMissingWorkdirRoot(ctx context.Context, tx pgx.Tx, t *tasks.Row, runtimeID uuid.UUID, now time.Time) error {
	// S-52: a server-written task_event obeys the closed schema. `runtime` is
	// the class for "process/adapter level", and `detail` is its one free-text
	// field.
	return tasks.InsertServerEventOnce(ctx, tx, t.ID, t.Attempt, "runtime", "error", "workdir_root", "failed",
		map[string]any{
			"failure_kind": "config",
			"detail": "이 런타임(" + runtimeID.String() + ")의 probe `workdir_root` 를 아직 받지 못해 " +
				"절대 workdir 경로를 만들 수 없습니다(daemon-protocol §4.1 v0.7.3). 데몬이 probe 를 " +
				"보내면 다음 claim 에서 이 task 가 나갑니다 — 상대 경로로 내보내지 않습니다.",
		}, now)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
