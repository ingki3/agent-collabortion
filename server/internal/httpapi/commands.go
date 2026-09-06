package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// ExpireCommands is the scheduler sweep for daemon commands (daemon-protocol
// §4.3 v0.2): revoke older than HeartbeatExpiry stops being re-sent, and any
// command unconsumed for 24h is dropped with a "명령 미소비 만료" status event
// on the task's feed. Returns the number of TTL-expired commands.
func (s *Server) ExpireCommands(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	expired, err := tokens.ExpireCommands(ctx, s.DB, now)
	if err != nil {
		return 0, err
	}
	for _, e := range expired {
		if e.TaskID == nil {
			continue
		}
		attempt := 1
		if e.Attempt != nil {
			attempt = *e.Attempt
		} else {
			_ = s.DB.QueryRow(ctx, `SELECT attempt FROM task WHERE id = $1`, *e.TaskID).Scan(&attempt)
		}
		// The fourth server-side writer. It used to compute seq the same racy
		// way as the other three and swallow the conflict into a Log.Warn, so
		// the note the Director needed disappeared twice over.
		if err := s.writeServerEvent(ctx, *e.TaskID, attempt, "status", "error", string(e.Type), "info",
			// S-52: closed `status` payload — the sentence goes under `args`.
			map[string]any{"command": string(e.Type), "result_ref": fmt.Sprintf("daemon_command:%d", e.ID),
				"args": map[string]any{"note": "명령 미소비 만료 (24h TTL)"}},
			now); err != nil {
			s.Log.Warn("record expired command", "err", err, "command", e.ID)
		}
	}
	return len(expired), nil
}

// writeServerEvent runs tasks.InsertServerEvent in its own transaction — the
// advisory lock it takes is transaction-scoped, so it needs one.
func (s *Server) writeServerEvent(ctx context.Context, taskID uuid.UUID, attempt int,
	class, verb, objectRef, outcome string, payload map[string]any, now time.Time) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := tasks.InsertServerEvent(ctx, tx, taskID, attempt, class, verb, objectRef, outcome, payload, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
