package tasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
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
	var id uuid.UUID
	var seq int
	if err := tx.QueryRow(ctx, `
		INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
		VALUES ($1, $2,
		        (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
		        $4, $5, to_jsonb($6::text), $7, $8, $9)
		RETURNING id, seq`,
		taskID, attempt, ServerSeqBase, class, verb, objectRef, outcome, payload, now).Scan(&id, &seq); err != nil {
		return fmt.Errorf("tasks: server event: %w", err)
	}
	announceServerEvent(ctx, tx, id, taskID, attempt, seq, class, verb, objectRef, outcome, payload, now)
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
	var id uuid.UUID
	var seq int
	err := tx.QueryRow(ctx, `
		INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
		SELECT $1, $2,
		       (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
		       $4, $5, to_jsonb($6::text), $7, $8, $9
		WHERE NOT EXISTS (
		      SELECT 1 FROM task_event
		       WHERE task_id = $1 AND attempt = $2 AND object_ref = to_jsonb($6::text)
		         AND class = $4 AND verb = $5)
		RETURNING id, seq`,
		taskID, attempt, ServerSeqBase, class, verb, objectRef, outcome, payload, now).Scan(&id, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already there — nothing new to announce
	}
	if err != nil {
		return fmt.Errorf("tasks: server event (once): %w", err)
	}
	announceServerEvent(ctx, tx, id, taskID, attempt, seq, class, verb, objectRef, outcome, payload, now)
	return nil
}

// serverEventHub is where server-written rows are announced. It is set by
// New (the one Service the server wires) because the writers above are
// package functions called from inside other packages' transactions —
// threading a hub through every one of the ~20 call sites would change
// nothing but their signatures.
var serverEventHub atomic.Pointer[realtime.Hub]

// announceServerEvent is the `task_event.appended` frame for a row the server
// wrote (openapi StreamEvent — "S7 활동 피드"). Until v1.1 only the daemon's
// batches (events.Ingest) were announced, so a cancel note, a budget pause or
// the FR-7.2 empty-turn card reached the screen on the next reload and not
// before — the row a person is meant to act on was the one row the live feed
// did not carry. Same shape as events.toAPI; it never fails the write.
func announceServerEvent(ctx context.Context, tx pgx.Tx, id, taskID uuid.UUID, attempt, seq int,
	class, verb, objectRef, outcome string, payload map[string]any, now time.Time) {
	hub := serverEventHub.Load()
	if hub == nil {
		return
	}
	var wsID, sessionID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT s.workspace_id, t.session_id FROM task t JOIN room s ON s.id = t.session_id WHERE t.id = $1`, taskID).
		Scan(&wsID, &sessionID); err != nil {
		slog.Warn("announce server task_event", "err", err, "task", taskID)
		return
	}
	a := attempt
	ev := gen.TaskEvent{Id: id, TaskId: taskID, Attempt: &a, Seq: seq, Class: class, CreatedAt: now,
		Verb: nullable.NewNullableWithValue(verb), Outcome: nullable.NewNullableWithValue(outcome),
		ObjectRef: nullable.NewNullableWithValue(objectRef),
		Sentence:  nullable.NewNullableWithValue(EventSentence(class, verb, objectRef, outcome)),
	}
	if payload != nil {
		ev.Payload = nullable.NewNullableWithValue[map[string]interface{}](payload)
	} else {
		ev.Payload = nullable.NewNullNullable[map[string]interface{}]()
	}
	if err := hub.Publish(ctx, tx, wsID, &sessionID, "task_event.appended", ev); err != nil {
		slog.Warn("publish server task_event", "err", err, "task", taskID)
	}
}

// EventSentence is the FR-7.2 one-line fallback render of a task_event:
// "<class>.<verb> <object> → <outcome>". The daemon's rows (events.List ·
// Ingest) and the server's own (announceServerEvent) use the same one.
func EventSentence(class, verb, objectRef, outcome string) string {
	obj := ""
	if objectRef != "" {
		obj = " " + objectRef
	}
	return fmt.Sprintf("%s.%s%s → %s", class, verb, obj, outcome)
}
