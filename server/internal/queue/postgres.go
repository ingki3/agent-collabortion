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
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/testchat"
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
//   - the ROOM gate (PRD v0.19 FR-2.4 · §12.1-9): nothing of a room whose
//     `blocked_reason` is set, whose isolation question is pending
//     (FR-2.1.1), or that is archived — in or out of a mission
//   - the task's OWN mission must be active (paused → nothing, E5-04); a task
//     outside any mission answers to the room gate alone (FR-2A.1)
//   - not_before must have passed (rate_limited)
//   - one in-flight task per lane (FR-6.3)
//
// A task the limits hold back gets `queued_reason` (PRD §3.1): which of the
// four layers is full — `agent_global` is the agent's max_concurrent_tasks
// counted across every room (§12.1-5), so the room screen can say "다른 방에서
// 작업 중".
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

	// FR-2.1.1: a room about to make its first run on a computer with a
	// repository while isolation is `none` asks its owner first. The question
	// is raised here, where "first dispatch" is decided, and the room is left
	// out of the candidates below until it is answered — nothing pinned.
	if err := p.askIsolation(ctx, tx, rt, now); err != nil {
		return nil, err
	}
	// T-S-wt: a worktree room with no computer yet (createRoom inherits the
	// kind alone) is settled on its first claim — filled from this computer's
	// one repository and pinned, or asked about, or passed by with a reason.
	// Before the SELECT, so a room filled here is claimed in this same poll.
	if err := p.settleWorktree(ctx, tx, rt, now); err != nil {
		return nil, err
	}

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
	//
	// The statement ranks EVERY candidate and names the layer that holds it
	// back (NULL = admissible); Go admits the first `capacity` admissible ones.
	// The layer is what `queued_reason` records.
	rows, err := tx.Query(ctx, `
		WITH busy AS (
			SELECT id, session_id, agent_id, lane_id, runtime_id FROM task
			WHERE status::text = ANY($3)
		),
		cand AS (
			SELECT DISTINCT ON (t.lane_id)
			       t.id, t.session_id, t.agent_id, t.lane_id, t.created_at, t.queued_reason::text AS prev_reason,
			       COALESCE((s.limits->>'max_parallel_lanes')::int, 5) AS lane_cap,
			       a.max_concurrent_tasks AS agent_cap,
			       -- worktree shares one workdir per agent IN A ROOM (FR-6.1), so
			       -- that agent's lanes in this room run one at a time whatever its
			       -- own cap says.
			       s.isolation->>'kind' = 'worktree' AS serial,
			       COALESCE((cfg.runtime_policy->>'max_concurrent_tasks')::int, 10) AS runtime_cap,
			       (cfg.runtime_policy->>'max_concurrent_tasks_workspace')::int AS workspace_cap,
			       (SELECT count(DISTINCT b.lane_id) FROM busy b WHERE b.session_id = t.session_id) AS busy_lanes,
			       -- §12.1-5: the agent's cap spans every room.
			       (SELECT count(*) FROM busy b WHERE b.agent_id = t.agent_id) AS busy_agent,
			       (SELECT count(*) FROM busy b WHERE b.agent_id = t.agent_id AND b.session_id = t.session_id) AS busy_agent_room,
			       (SELECT count(*) FROM busy b WHERE b.runtime_id = r.id) AS busy_runtime,
			       (SELECT count(*) FROM busy b JOIN room bs ON bs.id = b.session_id
			         WHERE bs.workspace_id = r.workspace_id) AS busy_workspace
			  FROM task t
			  JOIN room s ON s.id = t.session_id
			  -- The task's own mission — never "the room's mission": a room has
			  -- several (V19_R1B_HANDOFF (a) queue/postgres.go:94).
			  LEFT JOIN work wk ON wk.id = t.work_id
			  JOIN lane l ON l.id = t.lane_id
			  JOIN agent a ON a.id = t.agent_id
			  JOIN runtime r ON r.id = $1
			  -- LEFT: a workspace with no settings row falls back to the
			  -- defaults instead of stopping dispatch altogether.
			  LEFT JOIN workspace_settings cfg ON cfg.workspace_id = r.workspace_id
			 WHERE t.status = 'queued'
			   -- FR-2.4: the room gate — the one line §12.1-9 promises.
			   AND s.status = 'active' AND s.blocked_reason IS NULL AND s.isolation_pending IS NULL
			   AND (t.work_id IS NULL OR wk.status = 'active')
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
			       row_number() OVER (PARTITION BY c.session_id             ORDER BY c.created_at, c.id) AS rn_session,
			       row_number() OVER (PARTITION BY c.agent_id               ORDER BY c.created_at, c.id) AS rn_agent,
			       row_number() OVER (PARTITION BY c.agent_id, c.session_id ORDER BY c.created_at, c.id) AS rn_agent_room,
			       row_number() OVER (                                      ORDER BY c.created_at, c.id) AS rn_all
			  FROM cand c
		)
		SELECT id, lane_id, prev_reason,
		       CASE
		         WHEN busy_lanes + rn_session > lane_cap THEN 'room_lanes'
		         WHEN busy_agent + rn_agent > agent_cap THEN 'agent_global'
		         WHEN serial AND busy_agent_room + rn_agent_room > 1 THEN 'serial'
		         WHEN busy_runtime + rn_all > runtime_cap THEN 'runtime'
		         WHEN workspace_cap IS NOT NULL AND busy_workspace + rn_all > workspace_cap THEN 'workspace'
		       END
		  FROM ranked
		 ORDER BY created_at, id`, rt, now, hitl.OccupyingStatuses())
	if err != nil {
		return nil, fmt.Errorf("queue: select: %w", err)
	}
	var admit []uuid.UUID
	var held []heldTask
	for rows.Next() {
		var h heldTask
		var prev, layer *string
		if err := rows.Scan(&h.id, &h.lane, &prev, &layer); err != nil {
			rows.Close()
			return nil, err
		}
		h.prev = deref(prev)
		switch {
		case layer == nil && len(admit) < capacity:
			admit = append(admit, h.id)
			continue
		case layer == nil:
			// Admissible, but the daemon asked for fewer: the machine is
			// what is full.
			h.reason = QueuedRuntime
		default:
			h.reason = QueuedReasonOf(*layer)
		}
		held = append(held, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := p.noteQueuedReasons(ctx, tx, held); err != nil {
		return nil, err
	}

	var ids []uuid.UUID
	if len(admit) > 0 {
		lrows, err := tx.Query(ctx, `
			SELECT id FROM task WHERE id = ANY($1) AND status = 'queued'
			 ORDER BY created_at FOR UPDATE SKIP LOCKED`, admit)
		if err != nil {
			return nil, fmt.Errorf("queue: lock: %w", err)
		}
		for lrows.Next() {
			var id uuid.UUID
			if err := lrows.Scan(&id); err != nil {
				lrows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		lrows.Close()
		if err := lrows.Err(); err != nil {
			return nil, err
		}
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
		pin, err := tx.Exec(ctx, `UPDATE room SET runtime_id = $2, updated_at = $3
			WHERE id = $1 AND runtime_id IS NULL
			  AND workspace_id = (SELECT workspace_id FROM runtime WHERE id = $2)`, t.SessionID, rt, now)
		if err != nil {
			return nil, err
		}
		if pin.RowsAffected() > 0 {
			// FR-2.1.1: the room's computer is fixed for good right now, and
			// the timeline says so — isolation and computer cannot change from
			// here on.
			if err := roomgate.AnnounceComputer(ctx, tx, p.hub(), t.SessionID, rt, now); err != nil {
				return nil, err
			}
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
			b, err = buildBundle(ctx, tx, t, rt, token, now)
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
	// daemon-protocol v0.8 §4.5: test chat turns ride the same claim, taking one
	// `capacity` slot each — but only the slots the session tasks LEFT. They
	// are handed out after the task loop so a person's test chat never displaces
	// real work, and the runtime's own concurrency cap (FR-6.3, the 3rd layer)
	// counts them alongside the tasks in flight. The session-task SQL above is
	// untouched: a test chat is not a session and must not change how sessions
	// dispatch.
	if remaining := capacity - len(bundles); remaining > 0 {
		slots, err := testChatSlots(ctx, tx, rt, remaining)
		if err != nil {
			return nil, err
		}
		chats, err := testchat.ClaimTurns(ctx, tx, rt, slots, now)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, chats...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return bundles, nil
}

// Queued reasons (openapi QueuedReason, PRD §3.1).
const (
	QueuedRoomLanes   = "room_lanes"
	QueuedAgentGlobal = "agent_global"
	QueuedRuntime     = "runtime"
	QueuedWorkspace   = "workspace"
)

// QueuedReasonOf maps the claim's layer to the stored reason. `serial` — a
// worktree agent already busy in the same room — has no value of its own: the
// same agent's other lane in the same room is running, which the room board
// already shows, and none of the four reasons would be true.
func QueuedReasonOf(layer string) string {
	switch layer {
	case QueuedRoomLanes, QueuedAgentGlobal, QueuedRuntime, QueuedWorkspace:
		return layer
	}
	return ""
}

// heldTask is a candidate the claim did not hand out, and why.
type heldTask struct {
	id, lane     uuid.UUID
	prev, reason string
}

// noteQueuedReasons writes `queued_reason` where it changed. The claim
// long-polls every second, so only a CHANGE is written (and published): a
// write per poll per waiting task would be most of the database's traffic.
// Rows another claim holds are skipped rather than waited on — two runtimes
// claiming the same unpinned room would otherwise lock each other's rows in
// opposite orders.
func (p *Postgres) noteQueuedReasons(ctx context.Context, tx pgx.Tx, held []heldTask) error {
	for _, h := range held {
		if h.reason == h.prev {
			continue
		}
		var reason *string
		if h.reason != "" {
			r := h.reason
			reason = &r
		}
		tag, err := tx.Exec(ctx, `
			UPDATE task SET queued_reason = $2::queued_reason
			 WHERE id = (SELECT id FROM task WHERE id = $1 AND status = 'queued' FOR UPDATE SKIP LOCKED)`, h.id, reason)
		if err != nil {
			return fmt.Errorf("queue: queued_reason: %w", err)
		}
		if tag.RowsAffected() > 0 && p.Tasks != nil && p.Tasks.LanePublish != nil {
			// openapi 0.2.0: `lane.updated` carries the lane's first queued
			// task's reason — the card that says "다른 방에서 작업 중".
			p.Tasks.LanePublish(ctx, tx, h.lane)
		}
	}
	return nil
}

// askIsolation raises FR-2.1.1's question for every room this runtime would
// otherwise pin with isolation `none` while it has a repository. One question
// per room: the room row is locked (SKIP LOCKED — another runtime asking at
// the same instant simply wins) and isolation_pending is set in the same
// transaction, so the next poll no longer sees the room.
func (p *Postgres) askIsolation(ctx context.Context, tx pgx.Tx, runtimeID uuid.UUID, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT s.id, COALESCE(r.repos->0->>'path', '')
		  FROM room s
		  JOIN runtime r ON r.id = $1
		 WHERE s.workspace_id = r.workspace_id
		   AND s.runtime_id IS NULL AND s.isolation->>'kind' = 'none'
		   AND s.isolation_pending IS NULL AND s.blocked_reason IS NULL AND s.status = 'active'
		   AND jsonb_typeof(r.repos) = 'array' AND jsonb_array_length(r.repos) > 0
		   AND EXISTS (SELECT 1 FROM task t LEFT JOIN work wk ON wk.id = t.work_id
		                WHERE t.session_id = s.id AND t.status = 'queued'
		                  AND (t.work_id IS NULL OR wk.status = 'active')
		                  AND (t.not_before IS NULL OR t.not_before <= $2))
		 FOR UPDATE OF s SKIP LOCKED`, runtimeID, now)
	if err != nil {
		return fmt.Errorf("queue: isolation premise: %w", err)
	}
	type ask struct {
		room uuid.UUID
		repo string
	}
	var asks []ask
	for rows.Next() {
		var a ask
		if err := rows.Scan(&a.room, &a.repo); err != nil {
			rows.Close()
			return err
		}
		asks = append(asks, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range asks {
		if err := roomgate.AskIsolation(ctx, tx, p.hub(), a.room, runtimeID, a.repo, now); err != nil {
			return err
		}
	}
	return nil
}

// settleWorktree is the first claim of every worktree room of this workspace
// that has no computer yet (roomgate.PlanWorktree). The rooms are locked SKIP
// LOCKED like askIsolation's: two computers polling at once, one settles the
// room and the other no longer sees it unpinned.
func (p *Postgres) settleWorktree(ctx context.Context, tx pgx.Tx, runtimeID uuid.UUID, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT s.id, COALESCE(s.isolation->>'repo_path', ''),
		       COALESCE((SELECT array_agg(e->>'path' ORDER BY n)
		                   FROM jsonb_array_elements(CASE WHEN jsonb_typeof(r.repos) = 'array' THEN r.repos ELSE '[]'::jsonb END)
		                        WITH ORDINALITY AS x(e, n)
		                  WHERE COALESCE(e->>'path', '') <> ''), '{}')
		  FROM room s
		  JOIN runtime r ON r.id = $1
		 WHERE s.workspace_id = r.workspace_id
		   AND s.runtime_id IS NULL AND s.isolation->>'kind' = 'worktree'
		   AND s.isolation_pending IS NULL AND s.blocked_reason IS NULL AND s.status = 'active'
		   AND EXISTS (SELECT 1 FROM task t LEFT JOIN work wk ON wk.id = t.work_id
		                WHERE t.session_id = s.id AND t.status = 'queued'
		                  AND (t.work_id IS NULL OR wk.status = 'active')
		                  AND (t.not_before IS NULL OR t.not_before <= $2))
		 FOR UPDATE OF s SKIP LOCKED`, runtimeID, now)
	if err != nil {
		return fmt.Errorf("queue: worktree premise: %w", err)
	}
	type room struct {
		id    uuid.UUID
		repo  string
		repos []string
	}
	var list []room
	for rows.Next() {
		var r room
		if err := rows.Scan(&r.id, &r.repo, &r.repos); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range list {
		switch settle, repo := roomgate.PlanWorktree(r.repo, r.repos); settle {
		case roomgate.SettleFill:
			if _, err := roomgate.FillWorktree(ctx, tx, p.hub(), r.id, runtimeID, repo, now); err != nil {
				return err
			}
		case roomgate.SettleAsk:
			if err := roomgate.AskRepo(ctx, tx, p.hub(), r.id, runtimeID, r.repos, now); err != nil {
				return err
			}
		default:
			if err := p.noteWaitingForComputer(ctx, tx, r.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// noteWaitingForComputer marks a worktree room's queued tasks `queued_reason:
// runtime` when the claiming computer cannot take the room — without it the
// room sits at `queued` with no reason and the screen has nothing to say. The
// room is worktree with no computer, so the screen reads the pair as 「저장소가
// 있는 컴퓨터를 기다립니다」. Only a change is written, as noteQueuedReasons.
func (p *Postgres) noteWaitingForComputer(ctx context.Context, tx pgx.Tx, roomID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		UPDATE task SET queued_reason = 'runtime'
		 WHERE id IN (SELECT id FROM task WHERE session_id = $1 AND status = 'queued'
		                 AND queued_reason IS DISTINCT FROM 'runtime' FOR UPDATE SKIP LOCKED)
		RETURNING lane_id`, roomID)
	if err != nil {
		return fmt.Errorf("queue: waiting for a computer: %w", err)
	}
	lanes := map[uuid.UUID]bool{}
	for rows.Next() {
		var l uuid.UUID
		if err := rows.Scan(&l); err != nil {
			rows.Close()
			return err
		}
		lanes[l] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if p.Tasks != nil && p.Tasks.LanePublish != nil {
		for l := range lanes {
			p.Tasks.LanePublish(ctx, tx, l)
		}
	}
	return nil
}

func (p *Postgres) hub() *realtime.Hub {
	if p.Tasks == nil {
		return nil
	}
	return p.Tasks.Hub
}

// testChatSlots is how many test chat turns this claim may still hand out:
// the daemon's remaining capacity, bounded by the runtime cap
// (`runtime_policy.max_concurrent_tasks`, default 10) minus everything already
// running or dispatched on this runtime — session tasks, test chat turns, and
// the bundles this very claim just built.
func testChatSlots(ctx context.Context, tx pgx.Tx, runtimeID uuid.UUID, remaining int) (int, error) {
	var runtimeCap, busyTasks int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((cfg.runtime_policy->>'max_concurrent_tasks')::int, 10),
		       (SELECT count(*) FROM task b WHERE b.runtime_id = r.id AND b.status::text = ANY($2))
		FROM runtime r LEFT JOIN workspace_settings cfg ON cfg.workspace_id = r.workspace_id
		WHERE r.id = $1`, runtimeID, hitl.OccupyingStatuses()).Scan(&runtimeCap, &busyTasks); err != nil {
		if isNoRows(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("queue: test chat slots: %w", err)
	}
	busyChats, err := testchat.InFlightOn(ctx, tx, runtimeID)
	if err != nil {
		return 0, fmt.Errorf("queue: test chat slots: %w", err)
	}
	// The tasks claimed a moment ago are already counted in busyTasks (they
	// are `dispatched` inside this transaction), so only the runtime cap's
	// headroom is left to apply.
	slots := runtimeCap - busyTasks - busyChats
	if slots > remaining {
		slots = remaining
	}
	if slots < 0 {
		slots = 0
	}
	return slots, nil
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
			"detail": "이 컴퓨터(" + runtimeID.String() + ")가 작업 폴더의 기준 위치를 아직 알려 주지 않아 " +
				"작업 폴더 경로를 정할 수 없습니다. 컴퓨터가 알려 주면 다음 차례에 이 할 일이 나갑니다 — " +
				"그 전에는 상대 경로로 내보내지 않습니다.",
		}, now)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
