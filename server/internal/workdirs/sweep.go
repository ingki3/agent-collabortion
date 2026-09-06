package workdirs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// Service is the GC scheduler (FR-6.4). The judgement is JudgeGC's; this type
// only reads rows, issues the commands the judgement asked for, and makes sure
// a blocked directory is told about exactly once.
type Service struct {
	DB    *pgxpool.Pool
	Clock clock.Clock
	Hub   *realtime.Hub
	Log   *slog.Logger
}

func NewService(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub, log *slog.Logger) *Service {
	return &Service{DB: pool, Clock: c, Hub: h, Log: log}
}

// SweepResult is what one pass did, for the scheduler's log line.
type SweepResult struct {
	Deleted int
	Blocked int
}

type gcRow struct {
	GCCase
	SessionID    uuid.UUID
	WorkspaceID  uuid.UUID
	RuntimeID    *uuid.UUID
	Director     *uuid.UUID
	NotifiedAt   *time.Time
	KnownReason  string
	CommandOpen  bool
	SessionTitle string
}

// SweepGC is FR-6.4's retention pass.
//
// It looks only at `active` workdirs of sessions that have ENDED. A running
// session's directories are not candidates at any age — retention counts from
// the session's end, and collecting a live checkout deletes the files an agent
// is editing right now (E13-18).
//
// production caller: cmd/server.scheduler (the one-minute purge tick).
func (s *Service) SweepGC(ctx context.Context) (SweepResult, error) {
	now := s.Clock.Now()
	// `retain_until` is the contract's answer to S13's "언제까지" column
	// (openapi Workdir). It is derived, not authoritative — JudgeGC computes
	// the same window from `finished_at` — but a column the screen reads has to
	// exist, and deriving it in one UPDATE keeps it from drifting away from the
	// judgement when a workspace changes `workdir_retention_days`.
	if _, err := s.DB.Exec(ctx, `
		UPDATE workdir w SET retain_until = s.finished_at + make_interval(days => COALESCE(ws.workdir_retention_days, $1)),
		       updated_at = $2
		FROM session s
		LEFT JOIN workspace_settings ws ON ws.workspace_id = s.workspace_id
		WHERE s.id = w.session_id AND w.status = 'active'
		  AND s.status IN ('completed', 'cancelled') AND s.finished_at IS NOT NULL
		  AND w.retain_until IS DISTINCT FROM s.finished_at + make_interval(days => COALESCE(ws.workdir_retention_days, $1))`,
		DefaultRetentionDays, now); err != nil {
		s.warn("workdirs: refresh retain_until", "err", err)
	}
	rows, err := s.DB.Query(ctx, `
		SELECT w.id, w.path_or_ref, w.kind::text, w.session_id, s.workspace_id, s.runtime_id,
		       s.director_user_id, s.title, s.status::text, s.finished_at,
		       COALESCE(s.isolation->>'kind', ''),
		       COALESCE(ws.workdir_retention_days, $1),
		       COALESCE(w.merged, false), w.commits_ahead,
		       COALESCE(w.tree_dirty, w.dirty, false),
		       w.gc_notified_at, COALESCE(w.gc_blocked_reason, ''),
		       EXISTS (SELECT 1 FROM daemon_command c
		                WHERE c.type = 'gc' AND c.consumed_at IS NULL
		                  AND w.id::text = ANY(gc_command_workdir_ids(c.payload)))
		FROM workdir w
		JOIN session s ON s.id = w.session_id
		LEFT JOIN workspace_settings ws ON ws.workspace_id = s.workspace_id
		WHERE w.status = 'active' AND s.status IN ('completed', 'cancelled')`, DefaultRetentionDays)
	if err != nil {
		return SweepResult{}, fmt.Errorf("workdirs: gc sweep: %w", err)
	}
	var cases []gcRow
	for rows.Next() {
		var r gcRow
		var finished *time.Time
		var kind string
		if err := rows.Scan(&r.WorkdirID, &r.Path, &kind, &r.SessionID, &r.WorkspaceID, &r.RuntimeID,
			&r.Director, &r.SessionTitle, &r.SessionStatus, &finished, &r.GCCase.Isolation,
			&r.RetentionDays, &r.Merged, &r.CommitsAhead, &r.TreeDirty,
			&r.NotifiedAt, &r.KnownReason, &r.CommandOpen); err != nil {
			rows.Close()
			return SweepResult{}, err
		}
		if finished != nil {
			r.SinceSessionEnd = now.Sub(*finished)
		}
		if r.GCCase.Isolation == "" {
			// A session row with no isolation kind cannot happen (the column
			// has a CHECK), but the workdir's own kind is the same fact from
			// the daemon's side and is a better guess than "worktree".
			r.GCCase.Isolation = kind
		}
		cases = append(cases, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return SweepResult{}, err
	}

	var out SweepResult
	// Deletions are grouped per (session, runtime): the §4.3 payload is
	// `{session_id, workdirs}`, so one command per session is both what the
	// contract describes and one fewer round trip than one per directory.
	type key struct {
		session uuid.UUID
		runtime uuid.UUID
	}
	batches := map[key][]gcRow{}
	for _, c := range cases {
		v := JudgeGC(c.GCCase)
		switch {
		case v.Delete:
			if c.CommandOpen {
				// The command is already outstanding and is re-sent on every
				// daemon response until the report says it was carried out
				// (§4.3). Queueing another one every minute would fill the
				// table with duplicates of a decision already made.
				continue
			}
			if c.RuntimeID == nil {
				// S-34's shape: directories with no machine to ask. Nothing can
				// be collected, and silence here is exactly the GC bug the log
				// line exists to make findable.
				s.warn("workdirs: gc decided but session has no runtime",
					"workdir", c.WorkdirID, "session", c.SessionID)
				continue
			}
			batches[key{c.SessionID, *c.RuntimeID}] = append(batches[key{c.SessionID, *c.RuntimeID}], c)
		case v.NotifyDirector:
			changed, err := s.recordBlocked(ctx, c, v.Reason, now)
			if err != nil {
				s.warn("workdirs: record gc block", "workdir", c.WorkdirID, "err", err)
				continue
			}
			if changed {
				out.Blocked++
			}
		default:
			// Inside the window, or the session is still live. If a previous
			// pass marked it blocked and the Director has since committed, the
			// mark is cleared so S13 stops showing a stale reason.
			if c.KnownReason != "" {
				if _, err := s.DB.Exec(ctx, `
					UPDATE workdir SET gc_blocked_reason = NULL, gc_notified_at = NULL, updated_at = $2
					WHERE id = $1`, c.WorkdirID, now); err != nil {
					s.warn("workdirs: clear gc block", "workdir", c.WorkdirID, "err", err)
				}
			}
		}
	}

	for k, batch := range batches {
		ids := make([]uuid.UUID, 0, len(batch))
		paths := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.WorkdirID)
			paths = append(paths, c.Path)
		}
		cmd := BuildGCCommand(k.session, ids, paths)
		if err := tokens.QueueCommand(ctx, s.DB, k.runtime, cmd); err != nil {
			s.warn("workdirs: queue gc command", "session", k.session, "err", err)
			continue
		}
		// The rows stay `active` until the daemon's §6 report says
		// `gc: deleted` (ApplyGCReports): the server asked, it did not observe.
		// Claiming the deletion here would make S13 show an empty machine that
		// is still full.
		out.Deleted += len(ids)
	}
	return out, nil
}

// recordBlocked writes FR-6.4's "삭제하지 않고 알린다" and returns whether this
// pass is the one that said it.
//
// The sweep is periodic. Without the `gc_notified_at` guard a single blocked
// directory would notify on every tick and the one item that needed an answer
// would be buried — the same failure E14-10 pins for the offline sweep. The
// guard is on the (workdir, reason) pair rather than the workdir alone, so a
// directory that moves from "미병합 커밋" to "미커밋 변경" is announced again:
// the Director's next action changed.
func (s *Service) recordBlocked(ctx context.Context, c gcRow, reason string, now time.Time) (bool, error) {
	if c.KnownReason == reason && c.NotifiedAt != nil {
		return false, nil
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tag, err := tx.Exec(ctx, `
		UPDATE workdir SET gc_blocked_reason = $2, gc_notified_at = $3, updated_at = $3
		WHERE id = $1 AND (gc_blocked_reason IS DISTINCT FROM $2 OR gc_notified_at IS NULL)`,
		c.WorkdirID, reason, now)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		// Another sweep got there first.
		return false, tx.Commit(ctx)
	}
	if err := s.notifyBlocked(ctx, tx, c, reason, now); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	s.publishWorkdir(ctx, c)
	return true, nil
}

// notifyBlocked puts the refusal where a person will act on it.
//
// TWO CHANNELS, ON PURPOSE. The Director's inbox is where FR-6.4's "알린다"
// lands (`workdir_gc_blocked`, Lead T-S9 ask 1); the activity feed carries the
// same sentence because `task_event` is what the session screen renders, and a
// person looking at the session should not have to go to the inbox to find out
// why a directory is being kept.
//
// The inbox item is issued at most once per unresolved workdir. The sweep is
// periodic, and an item re-created every minute buries the one that needed an
// answer — the same idempotence rule E14-10 pins for the offline sweep.
func (s *Service) notifyBlocked(ctx context.Context, q db.DBTX, c gcRow, reason string, now time.Time) error {
	if c.Director != nil {
		if _, err := q.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
			SELECT m.id, 'workdir_gc_blocked'::inbox_item_type, $1::inbox_severity, $2, $3, $4
			FROM member m
			WHERE m.workspace_id = $5 AND m.user_id = $6
			  AND NOT EXISTS (
			      SELECT 1 FROM inbox_item i
			       WHERE i.member_id = m.id AND i.type = 'workdir_gc_blocked'
			         AND i.ref_id = $3 AND i.read_at IS NULL)`,
			inbox.Severity(inbox.TypeWorkdirGCBlocked), c.SessionID, c.WorkdirID, now,
			c.WorkspaceID, *c.Director); err != nil {
			return fmt.Errorf("workdirs: gc blocked inbox: %w", err)
		}
	}
	var taskID uuid.UUID
	var attempt int
	if err := q.QueryRow(ctx, `
		SELECT id, attempt FROM task WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`,
		c.SessionID).Scan(&taskID, &attempt); err != nil {
		// A session that never dispatched a task has no feed. The inbox item
		// above and the `workdir.updated` frame are then the record.
		s.warn("workdirs: gc blocked with no task to record it on",
			"workdir", c.WorkdirID, "session", c.SessionID, "reason", reason)
		return nil
	}
	tx, ok := q.(pgx.Tx)
	if !ok {
		return nil
	}
	// class `runtime` with `detail`, not `status` with a free-text `note`:
	// `contracts/task_event.schema.json` closes the `status` payload to
	// {command, args, result_ref, rejected_reason} and `detail` is the field the
	// schema actually provides for a sentence (Lead T-S9 ask 3 (i)).
	return tasks.InsertServerEventOnce(ctx, tx, taskID, attempt, "runtime", "report",
		"workdir.gc_blocked:"+c.WorkdirID.String()+":"+reason, "info",
		map[string]any{
			"detail": fmt.Sprintf("%s (%s · 미병합 커밋 %d개)", GCReasonText(reason), c.Path, c.CommitsAhead),
		}, now)
}

func (s *Service) publishWorkdir(ctx context.Context, c gcRow) {
	if s.Hub == nil {
		return
	}
	api, err := Load(ctx, s.DB, c.WorkdirID)
	if err != nil {
		return
	}
	sid := c.SessionID
	_ = s.Hub.Publish(ctx, nil, c.WorkspaceID, &sid, "workdir.updated", api)
}

func (s *Service) warn(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, args...)
		return
	}
	slog.Warn(msg, args...)
}
