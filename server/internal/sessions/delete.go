package sessions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// deleteSession (openapi 0.1.3, FR-2.7) — a finished session is removed for
// good. The contract is explicit that this is a PHYSICAL delete: messages,
// lanes, tasks, activity, HITL, artifacts (bytes included), decisions, inbox
// items and cost rows go with it, and the workspace's cost and metrics
// aggregates no longer count it. What remains is ONE activity_log line
// (`session.deleted`) and the SSE frame that tells S5 to drop the card.

// DeleteActiveDetail is the contract's own sentence for the 409
// `session_active` (openapi deleteSession: "진행 중인 세션은 먼저 종료하세요") —
// S5 shows it as the reason the option is disabled. TestDeleteSentencesMatchContract
// reads it back out of openapi.yaml.
const DeleteActiveDetail = "진행 중인 세션은 먼저 종료하세요"

// DeleteUnmergedDetail is the 409 `workdir_unmerged`. It names the next action
// (merge, or clean up), not the rule — the blocking rows ride along in
// Problem.workdirs and S5's dialog lists them with the S13 link itself. The
// sentence is the one the web's mock (PR #219 MOCK_ONLY) already shows, so
// moving it to the server-sourced table changes nothing on screen.
const DeleteUnmergedDetail = "미병합 커밋이나 미커밋 변경이 남은 작업 폴더가 있어 삭제할 수 없습니다 — 먼저 병합하거나 정리해 주세요"

// DeleteForbiddenDetail is the 403 for a member who is neither the Director
// nor owner·admin (same sentence as the web's mock, PR #219).
const DeleteForbiddenDetail = "Director 나 소유자·관리자만 삭제할 수 있습니다"

// deletableStatuses is "끝난 세션만 — draft·completed·cancelled". `completing`
// is NOT finished: the summary and the approval request are still being
// produced, and deleting under them is how a half-written summary ends up
// posted to a session that no longer exists.
var deletableStatuses = map[string]bool{"draft": true, "completed": true, "cancelled": true}

// CanDelete is the status rule on its own, so the table above is testable
// without a database and the handler cannot drift from it.
func CanDelete(status string) bool { return deletableStatuses[status] }

// Delete carries out deleteSession for a caller the handler has already
// authorised (Director, or owner·admin of the workspace). actor is that
// person; it goes into the activity_log line.
//
// Order inside the one transaction, and why:
//
//  1. lock the session row and check the status (409 session_active);
//  2. list the unmerged/dirty worktrees (409 workdir_unmerged with the rows);
//  3. queue `gc` for every workdir that is left — BEFORE the rows go, because
//     BuildGCCommand needs their paths and the daemon has no id→path map
//     (daemon-protocol §4.3 v0.7, §6 v0.8.1);
//  4. delete the activity_log rows of the session by hand — that FK is
//     ON DELETE SET NULL (0001), and the contract says the activity goes too;
//  5. DELETE FROM session — every other child is ON DELETE CASCADE, and the
//     artifact trigger (0008) unlinks each large object in the same
//     transaction;
//  6. write the one `session.deleted` line (session_id NULL: the FK target is
//     gone; the id lives in object_id and the payload);
//  7. publish SSE `session.deleted {session_id}` through the same tx so the
//     frame is persisted for backfill and rolls back with the delete.
func (s *Service) Delete(ctx context.Context, sessionID, actor uuid.UUID) error {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wsID uuid.UUID
	var runtimeID *uuid.UUID
	var title, status string
	err = tx.QueryRow(ctx, `
		SELECT workspace_id, runtime_id, title, status::text FROM session WHERE id = $1 FOR UPDATE`, sessionID).
		Scan(&wsID, &runtimeID, &title, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("session")
	}
	if err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete: %w", err))
	}
	if !CanDelete(status) {
		return apperr.Conflict("session_active", DeleteActiveDetail)
	}

	blocking, err := workdirs.UnmergedWorktrees(ctx, tx, sessionID)
	if err != nil {
		return apperr.Internal(err)
	}
	if len(blocking) > 0 {
		p := apperr.Conflict("workdir_unmerged", DeleteUnmergedDetail)
		p.Extra = map[string]any{"workdirs": blocking}
		return p
	}

	if err := s.gcAllWorkdirs(ctx, tx, sessionID, runtimeID); err != nil {
		return apperr.Internal(err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM activity_log WHERE session_id = $1`, sessionID); err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete activity: %w", err))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM session WHERE id = $1`, sessionID); err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete session: %w", err))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_log (workspace_id, session_id, actor_type, actor_id, action, object_type, object_id, payload, created_at)
		VALUES ($1, NULL, 'user', $2, 'session.deleted', 'session', $3,
		        jsonb_build_object('title', $4::text, 'session_id', $3::uuid, 'actor', $2::uuid), $5)`,
		wsID, actor, sessionID, title, now); err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete activity line: %w", err))
	}
	if s.Hub != nil {
		sid := sessionID
		if err := s.Hub.Publish(ctx, tx, wsID, &sid, "session.deleted", map[string]any{"session_id": sessionID}); err != nil {
			return apperr.Internal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// gcAllWorkdirs queues one `gc` for every workdir row of the session that is
// not already `deleted` — unlike gcWorkdirs (completion) it takes the
// worktrees too: the caller has just proven none of them holds unmerged or
// uncommitted work, and the session they belonged to is about to not exist.
// `retained` rows (an earlier refusal, or runtime_gone) are included: the
// daemon may refuse again, and that receipt is consumed without a feed line
// (daemon-protocol §6 v0.8.1) because there is no session to put it on.
func (s *Service) gcAllWorkdirs(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, runtimeID *uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT id, path_or_ref FROM workdir WHERE session_id = $1 AND status <> 'deleted' ORDER BY created_at`, sessionID)
	if err != nil {
		return fmt.Errorf("sessions: delete gc workdirs: %w", err)
	}
	var ids []uuid.UUID
	var paths []string
	for rows.Next() {
		var id uuid.UUID
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		paths = append(paths, path)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if runtimeID == nil {
		// No machine to hand the deletion to. The rows are about to cascade
		// away with the session, so the directories — if any exist — are
		// stranded; the log line is the only record (same shape as
		// gcWorkdirs' S-34 note).
		slog.Warn("sessions: deleting a session that has workdirs but no runtime — gc not issued",
			"session", sessionID, "workdirs", len(ids))
		return nil
	}
	cmd, skipped := workdirs.BuildGCCommand(sessionID, ids, paths)
	if len(skipped) > 0 {
		slog.Warn("sessions: delete gc skipped workdirs with a relative path (S-65) — not sent to the daemon",
			"session", sessionID, "workdirs", skipped)
	}
	if len(cmd.Workdirs) == 0 {
		return nil
	}
	return tokens.QueueCommand(ctx, tx, *runtimeID, cmd)
}
