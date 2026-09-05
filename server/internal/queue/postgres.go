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
//     runtime is fixed to the first claimer (E11-10)
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

	rows, err := tx.Query(ctx, `
		SELECT t.id FROM task t JOIN session s ON s.id = t.session_id
		WHERE t.status = 'queued'
		  AND s.status = 'active'
		  AND (s.runtime_id = $1 OR (s.runtime_id IS NULL AND s.isolation->>'kind' = 'none'))
		  AND (t.not_before IS NULL OR t.not_before <= $2)
		  AND NOT EXISTS (SELECT 1 FROM task r WHERE r.lane_id = t.lane_id AND r.status IN ('dispatched', 'preparing', 'running'))
		ORDER BY t.created_at
		LIMIT $3
		FOR UPDATE OF t SKIP LOCKED`, rt, now, capacity)
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
		// E11-10: fix the session to the first runtime that claims it.
		if _, err := tx.Exec(ctx, `UPDATE session SET runtime_id = $2, updated_at = $3 WHERE id = $1 AND runtime_id IS NULL`, t.SessionID, rt, now); err != nil {
			return nil, err
		}
		token, err := p.Tasks.MarkDispatched(ctx, tx, t, rt, now)
		if err != nil {
			return nil, err
		}
		b, err := buildBundle(ctx, tx, t, token)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, *b)
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

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
