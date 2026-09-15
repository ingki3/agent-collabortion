package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FR-7.2 (v1.1, K-18): a turn that ended with `end_turn` having posted no
// message, operated the platform through no `colab` command and edited no
// file leaves an INFORMATION card behind — "아무것도 하지 않고 턴을 끝냈습니다".
// It is not an error: whether the mention was wasted (§8.3) or the silence
// was the right answer is the Director's call, and the card is what they
// judge it from. The observation table's empty_turn_rate counts this row.
//
// The three conditions are read from the attempt's own record:
//
//   - platform operation = any `status` task_event of this attempt that an
//     agent's command produced (colab-cli.md §4). The server's own `status`
//     rows — a cancel, an expired command, this very card — are not the
//     agent doing something and are left out by verb;
//   - file edit = a `tool` event with verb `edit_file` (task_event.schema);
//   - message = a message row this task posted since the attempt was
//     dispatched (message has no attempt column; attempt N's messages are
//     the ones after attempt N's dispatch, which attempt N-1 cannot reach).

// EmptyTurnObjectRef is the card's object_ref; observations.sqlEmptyTurnRate
// matches it verbatim.
const EmptyTurnObjectRef = "empty_turn"

// serverStatusVerbs are the `status` verbs the SERVER writes on its own; a
// row with any other verb is the agent operating the platform.
const serverStatusVerbs = `('cancel', 'error', 'turn_end')`

// emptyTurn reports whether attempt did nothing observable (FR-7.2 v1.1).
func emptyTurn(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, attempt int) (bool, error) {
	var empty bool
	err := tx.QueryRow(ctx, `
		SELECT NOT EXISTS (SELECT 1 FROM task_event e WHERE e.task_id = $1 AND e.attempt = $2
		                     AND e.class = 'status' AND e.verb NOT IN `+serverStatusVerbs+`)
		   AND NOT EXISTS (SELECT 1 FROM task_event e WHERE e.task_id = $1 AND e.attempt = $2
		                     AND e.class = 'tool' AND e.verb = 'edit_file')
		   AND NOT EXISTS (SELECT 1 FROM message m
		                   WHERE m.source_task_id = $1
		                     AND m.created_at >= COALESCE(
		                           (SELECT COALESCE(a.dispatched_at, a.started_at) FROM task_attempt a WHERE a.task_id = $1 AND a.attempt = $2),
		                           (SELECT t.created_at FROM task t WHERE t.id = $1)))`,
		taskID, attempt).Scan(&empty)
	if err != nil {
		return false, fmt.Errorf("tasks: empty turn: %w", err)
	}
	return empty, nil
}

// noteEmptyTurn writes the FR-7.2 card. The shape is the contract's, inside
// the closed `status` payload (S-52): `command` names the event and the
// sentence rides under `args.note`. Once per attempt — a repeated finish
// must not stack cards.
func noteEmptyTurn(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, attempt int, now time.Time) error {
	return InsertServerEventOnce(ctx, tx, taskID, attempt, "status", "turn_end", EmptyTurnObjectRef, "info",
		map[string]any{
			"command": "turn_end",
			"args":    map[string]any{"note": "아무것도 하지 않고 턴을 끝냈습니다"},
		}, now)
}
