package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ServerSeqBase is the seq range for task_events the server records itself
// (colab-cli.md §4). Daemon seqs are small and monotonic, so server-side events
// live above them and the two never interleave.
const ServerSeqBase = 1 << 30

// InsertServerEvent appends one server-recorded event to a task's feed.
//
// Why the advisory lock. seq is `max(seq) + 1` over (task, attempt), and the
// unique index is (task_id, attempt, seq) — so two server-side writers for the
// same attempt can read the same max and collide. `ON CONFLICT DO NOTHING`
// makes that quiet instead of fatal, which is not a fix: the second note simply
// vanishes, and the feed is the thing a human reads to decide whether to
// intervene ("보여주지 않았으면 일어나지 않은 것이다", FR-7.2).
//
// The lock serialises exactly the read-then-write, is scoped to one attempt,
// and is released at commit. It is cheaper than locking the task row, which the
// daemon's hot path (heartbeat, finish) already contends for — a feed note must
// not queue behind a running turn's bookkeeping.
//
// With the lock held, a duplicate seq is a real bug, so there is no ON CONFLICT
// clause here: it would hide one.
func InsertServerEvent(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, attempt int,
	class, verb, objectRef, outcome string, payload map[string]any, now time.Time) error {
	checkServerEvent(class, verb, objectRef, outcome, payload)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		taskID.String()+":"+fmt.Sprint(attempt)); err != nil {
		return fmt.Errorf("tasks: server event lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
		VALUES ($1, $2,
		        (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
		        $4, $5, to_jsonb($6::text), $7, $8, $9)`,
		taskID, attempt, ServerSeqBase, class, verb, objectRef, outcome, payload, now); err != nil {
		return fmt.Errorf("tasks: server event: %w", err)
	}
	return nil
}

// InsertServerEventOnce is InsertServerEvent for notes that must appear at most
// once per attempt (a drift warning repeated by every 15s heartbeat would bury
// the feed). The guard runs under the same lock, so "check then insert" cannot
// interleave with another writer.
func InsertServerEventOnce(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, attempt int,
	class, verb, objectRef, outcome string, payload map[string]any, now time.Time) error {
	checkServerEvent(class, verb, objectRef, outcome, payload)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		taskID.String()+":"+fmt.Sprint(attempt)); err != nil {
		return fmt.Errorf("tasks: server event lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
		SELECT $1, $2,
		       (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
		       $4, $5, to_jsonb($6::text), $7, $8, $9
		WHERE NOT EXISTS (
		      SELECT 1 FROM task_event
		       WHERE task_id = $1 AND attempt = $2 AND object_ref = to_jsonb($6::text)
		         AND class = $4 AND verb = $5)`,
		taskID, attempt, ServerSeqBase, class, verb, objectRef, outcome, payload, now); err != nil {
		return fmt.Errorf("tasks: server event (once): %w", err)
	}
	return nil
}
