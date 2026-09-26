package sessions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// DeleteUnmergedDetail is deleteRoom's 409 `workdir_unmerged` (FR-2.7). It
// names the next action (merge, or clean up), not the rule — the blocking rows
// ride along in Problem.workdirs and the dialog lists them with the S13 link
// itself.
const DeleteUnmergedDetail = "미병합 커밋이나 미커밋 변경이 남은 작업 폴더가 있어 삭제할 수 없습니다 — 먼저 병합하거나 정리해 주세요"

// gcAllWorkdirs queues one `gc` for every workdir row of the room that is
// not already `deleted` — unlike gcWorkdirs (completion) it takes the
// worktrees too: the caller (DeleteRoom) has just proven none of them holds
// unmerged or uncommitted work, and the room they belonged to is about to not
// exist.
// `retained` rows (an earlier refusal, or runtime_gone) are included: the
// daemon may refuse again, and that receipt is consumed without a feed line
// (daemon-protocol §6 v0.8.1) because there is no room to put it on.
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
