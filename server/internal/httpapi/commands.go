package httpapi

import (
	"context"
	"fmt"

	"github.com/ingki3/agent-collabortion/server/internal/router"
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
		if _, err := s.DB.Exec(ctx, `
			INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
			VALUES ($1, $2, (SELECT COALESCE(max(seq) + 1, $3::int) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq >= $3::int),
			        'status', 'error', to_jsonb($4::text), 'info', $5, $6)`,
			*e.TaskID, attempt, router.ServerSeqBase, string(e.Type),
			map[string]any{"command": string(e.Type), "result_ref": fmt.Sprintf("daemon_command:%d", e.ID), "note": "명령 미소비 만료 (24h TTL)"}, now); err != nil {
			s.Log.Warn("record expired command", "err", err, "command", e.ID)
		}
	}
	return len(expired), nil
}
