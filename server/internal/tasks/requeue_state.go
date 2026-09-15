package tasks

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// RequeuedAttempt reads the state a heartbeat-expiry re-queue leaves behind:
// which attempt is next, which workdir it will bind, and whether the PREVIOUS
// attempt's task token is revoked.
//
// It is a read, not a decision — ExpireStale already did the deciding. It
// exists because FR-9.1's last line of defence is a fact about two rows that no
// single call site returns: the task's new attempt (with the workdir C3 binds
// to the AGENT, so attempt 2 gets the same checkout attempt 1 was in) and the
// old attempt's `task_token.revoked_at`. A crash-recovery test that checked
// only the first would pass while an orphan kept posting.
//
// production caller: the worktree double-write simulator (server/test/sim),
// which is the only thing that needs both halves at once; the server's own
// paths write them.
func RequeuedAttempt(ctx context.Context, q db.DBTX, taskID uuid.UUID) (attempt int, laneID uuid.UUID, workdir, branch string, tokenRevoked bool, err error) {
	var wd, br *string
	err = q.QueryRow(ctx, `
		SELECT t.attempt, t.lane_id, w.path_or_ref, w.branch
		FROM task t
		LEFT JOIN lane l ON l.id = t.lane_id
		LEFT JOIN workdir w ON w.id = l.workdir_id
		WHERE t.id = $1`, taskID).Scan(&attempt, &laneID, &wd, &br)
	if err != nil {
		return 0, uuid.Nil, "", "", false, fmt.Errorf("tasks: requeued attempt: %w", err)
	}
	if wd != nil {
		workdir = *wd
	}
	if br != nil {
		branch = *br
	}
	if attempt > 1 {
		// The token of the attempt that was re-queued. `revoked_at` is what
		// makes an orphan's POST a 401 whether or not daemon recovery ran
		// (FR-9.1, E11-04).
		err = q.QueryRow(ctx, `
			SELECT revoked_at IS NOT NULL FROM task_token
			WHERE task_id = $1 AND attempt = $2`, taskID, attempt-1).Scan(&tokenRevoked)
		if err != nil {
			return attempt, laneID, workdir, branch, false, fmt.Errorf("tasks: requeued attempt token: %w", err)
		}
	}
	return attempt, laneID, workdir, branch, tokenRevoked, nil
}
