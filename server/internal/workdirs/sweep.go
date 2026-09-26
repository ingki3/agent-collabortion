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
	// QuotaNotices is how many `workdir_quota` items this pass wrote.
	QuotaNotices int
}

// liveLaneSQL is the GC gate: some lane using workdir `w` is still alive.
// Worktree directories are shared by an agent's lanes in a room, container/
// none ones belong to one lane; either way the lanes pointing at the row are
// the ones that could touch it next.
//
// A lane of a mission that has ended is not alive whatever its own status
// says: completeSession/cancelSession cancel the tasks but leave the lanes
// where they were (a never-claimed lane stays `queued`), and counting those
// would keep every finished session's directory forever — the old gate read
// the session status for exactly this reason. The lane's mission is its OWN
// work_id (T-R1b1: the r1b1_room_gate migration filled the column for every lane written before R1b1,
// and R1b1 writes it; the R1a fallback "the room's one work" is gone — with
// several missions in a room it would read another mission's end as this
// lane's). A lane outside any mission is alive on its own status.
const liveLaneSQL = `EXISTS (SELECT 1 FROM lane l WHERE l.workdir_id = w.id
	AND l.status IN ('queued', 'running', 'waiting_human', 'blocked', 'paused')
	AND NOT EXISTS (SELECT 1 FROM work lw
	                WHERE lw.id = l.work_id AND lw.status IN ('completed', 'cancelled')))`

// disposableNow is FR-6.4 v0.19's clock for a `container`·`none` directory
// (one lane each): it goes the moment the mission its lane is tied to CLOSES,
// and a lane outside any mission keeps it for `workdir_retention_days` after
// its last use. A lane that is done while its mission is still open keeps its
// folder — the mission can send the agent back into it (lane rule 3 re-entry),
// and deleting it under an open mission is what R1a's "no live lane" gate did.
//
// production caller: SweepGC.
func disposableNow(openMission, outsideMission bool, sinceLastUse time.Duration, retentionDays int) bool {
	if openMission {
		return false
	}
	if outsideMission {
		days := retentionDays
		if days < 0 {
			days = DefaultRetentionDays
		}
		return sinceLastUse >= time.Duration(days)*24*time.Hour
	}
	return true
}

// missionFolderDisposable is daemon-protocol v0.10.0 §6.1's GC clock for a
// mission folder — an agent row or the `_shared` row, found by the row's own
// `work_id` rather than through lanes (a `_shared` row has none). D8 B
// (Director 2026-09-26): an open mission keeps its folders at any age, and a
// closed one keeps them until `last_used_at + workdir_retention_days` — the
// close itself deletes nothing (the close dialog tells the Director to submit
// what must stay as an artifact).
//
// production caller: SweepGC.
func missionFolderDisposable(missionOpen bool, sinceLastUse time.Duration, retentionDays int) bool {
	if missionOpen {
		return false
	}
	days := retentionDays
	if days < 0 {
		days = DefaultRetentionDays
	}
	return sinceLastUse >= time.Duration(days)*24*time.Hour
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
// It looks only at `active` workdirs that no live lane is using. A directory
// some lane still runs, waits, or is blocked in is not a candidate at any age —
// collecting a live checkout deletes the files an agent is editing right now
// (E13-18).
//
// The reference point is the directory's own last use, not the session's end
// (PRD v0.19 NN8, T-R1a): a room never completes, so `finished_at` would stop
// GC forever, and a room-wide "no open mission" gate would keep an idle
// agent's folder alive for as long as anyone else in the room works (Lead
// answer, T-R1a Q2). JudgeGC is unchanged; the sweep feeds it
// SessionStatus = "completed" for a directory with no live lane (the only rows
// it selects) and SinceSessionEnd = now − COALESCE(last_used_at, created_at).
//
// production caller: cmd/server.scheduler (the one-minute purge tick).
func (s *Service) SweepGC(ctx context.Context) (SweepResult, error) {
	now := s.Clock.Now()
	// `retain_until` is the contract's answer to S13's "언제까지" column
	// (openapi Workdir). It is derived, not authoritative — JudgeGC computes
	// the same window from the last use — but a column the screen reads has
	// to exist, and deriving it in one UPDATE keeps it from drifting away from
	// the judgement when a workspace changes `workdir_retention_days`.
	if _, err := s.DB.Exec(ctx, `
		UPDATE workdir w SET retain_until = COALESCE(w.last_used_at, w.created_at) + make_interval(days => COALESCE(ws.workdir_retention_days, $1)),
		       updated_at = $2
		FROM room s
		LEFT JOIN workspace_settings ws ON ws.workspace_id = s.workspace_id
		WHERE s.id = w.session_id AND w.status = 'active'
		  AND NOT `+liveLaneSQL+`
		  AND w.retain_until IS DISTINCT FROM COALESCE(w.last_used_at, w.created_at) + make_interval(days => COALESCE(ws.workdir_retention_days, $1))`,
		DefaultRetentionDays, now); err != nil {
		s.warn("workdirs: refresh retain_until", "err", err)
	}
	// V19_R1B_HANDOFF 우선 처리 (sweep.go:118): one row per DIRECTORY. The
	// old `JOIN work ON work.room_id = room.id` returned a directory once per
	// mission of its room — a GC that deletes files must never judge (and
	// command) the same directory twice. Who is told and what the card is
	// titled come from the mission of the directory's latest lane, else the
	// room's owner and name (FR-6.4: 방장 for a directory outside missions).
	rows, err := s.DB.Query(ctx, `
		SELECT w.id, w.path_or_ref, w.kind::text, w.session_id, s.workspace_id, s.runtime_id,
		       COALESCE(lm.director_user_id, s.owner_user_id), COALESCE(lm.title, s.name),
		       'completed', COALESCE(w.last_used_at, w.created_at),
		       COALESCE(s.isolation->>'kind', ''),
		       COALESCE(ws.workdir_retention_days, $1),
		       COALESCE(w.merged, false), w.commits_ahead,
		       COALESCE(w.tree_dirty, w.dirty, false),
		       w.gc_notified_at, COALESCE(w.gc_blocked_reason, ''),
		       EXISTS (SELECT 1 FROM daemon_command c
		                WHERE c.type = 'gc' AND c.consumed_at IS NULL
		                  AND w.id::text = ANY(gc_command_workdir_ids(c.payload))),
		       EXISTS (SELECT 1 FROM lane l JOIN work lw ON lw.id = l.work_id
		                WHERE l.workdir_id = w.id AND lw.status NOT IN ('completed', 'cancelled')),
		       EXISTS (SELECT 1 FROM lane l WHERE l.workdir_id = w.id AND l.work_id IS NULL),
		       w.work_id IS NOT NULL,
		       EXISTS (SELECT 1 FROM work mw WHERE mw.id = w.work_id AND mw.status NOT IN ('completed', 'cancelled'))
		FROM workdir w
		JOIN room s ON s.id = w.session_id
		LEFT JOIN LATERAL (
		       SELECT lw.director_user_id, lw.title FROM lane l JOIN work lw ON lw.id = l.work_id
		        WHERE l.workdir_id = w.id ORDER BY l.updated_at DESC, l.id LIMIT 1) lm ON true
		LEFT JOIN workspace_settings ws ON ws.workspace_id = s.workspace_id
		WHERE w.status = 'active' AND NOT `+liveLaneSQL, DefaultRetentionDays)
	if err != nil {
		return SweepResult{}, fmt.Errorf("workdirs: gc sweep: %w", err)
	}
	var cases []gcRow
	for rows.Next() {
		var r gcRow
		var finished *time.Time
		var kind string
		var openMission, outsideMission, missionFolder, missionOpen bool
		if err := rows.Scan(&r.WorkdirID, &r.Path, &kind, &r.SessionID, &r.WorkspaceID, &r.RuntimeID,
			&r.Director, &r.SessionTitle, &r.SessionStatus, &finished, &r.GCCase.Isolation,
			&r.RetentionDays, &r.Merged, &r.CommitsAhead, &r.TreeDirty,
			&r.NotifiedAt, &r.KnownReason, &r.CommandOpen, &openMission, &outsideMission,
			&missionFolder, &missionOpen); err != nil {
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
		if missionFolder && kind != "worktree" {
			// §6.1 GC table: a mission folder — under `none`, and the
			// `_shared` of a `worktree` room, which is outside the repository
			// and has no commits to protect — follows D8 B, whatever the
			// room's isolation says.
			r.GCCase.Isolation = "none"
			if !missionFolderDisposable(missionOpen, r.SinceSessionEnd, r.RetentionDays) {
				r.SessionStatus = "active"
			}
		} else if r.GCCase.Isolation != "worktree" && !disposableNow(openMission, outsideMission, r.SinceSessionEnd, r.RetentionDays) {
			// JudgeGC deletes a `none`/`container` directory the moment it is
			// fed "ended"; FR-6.4 v0.19 says WHEN that is (disposableNow).
			r.SessionStatus = "active"
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
		cmd, skipped := BuildGCCommand(k.session, ids, paths)
		if len(skipped) > 0 {
			// S-65: a relative row (pre-0019) is never handed to the daemon —
			// see BuildGCCommand. It stays `active` and is named here every
			// pass; the directory is still on the machine and the person who
			// removes it is the one who can see the path.
			s.warn("workdirs: gc skipped workdirs with a relative path (S-65) — not sent to the daemon",
				"session", k.session, "workdirs", skipped)
		}
		if len(cmd.Workdirs) == 0 {
			continue
		}
		if err := tokens.QueueCommand(ctx, s.DB, k.runtime, cmd); err != nil {
			s.warn("workdirs: queue gc command", "session", k.session, "err", err)
			continue
		}
		// The rows stay `active` until the daemon's §6 report says
		// `gc: deleted` (ApplyGCReports): the server asked, it did not observe.
		// Claiming the deletion here would make S13 show an empty machine that
		// is still full.
		out.Deleted += len(cmd.Workdirs)
	}
	if n, err := s.notifyQuota(ctx, now); err != nil {
		s.warn("workdirs: quota notice", "err", err)
	} else {
		out.QuotaNotices = n
	}
	return out, nil
}

// notifyQuota is FR-6.4's disk quota as a `workdir_quota` item (openapi
// InboxItemType v0.2.0): a workspace whose folders hold `workdir_disk_quota_gb`
// or more tells its owners — the people who can raise the quota or clear S13.
// One unread item per owner at a time: the sweep runs every minute, and the
// rule E14-10 pins for the offline sweep holds here too. Returns how many
// items this pass wrote.
func (s *Service) notifyQuota(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.DB.Exec(ctx, `
		WITH used AS (
		  SELECT r.workspace_id, sum(w.disk_bytes) AS bytes
		  FROM workdir w JOIN room r ON r.id = w.session_id
		  WHERE w.status <> 'deleted' GROUP BY r.workspace_id)
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, recipient_basis, created_at)
		SELECT m.id, 'workdir_quota'::inbox_item_type, $1::inbox_severity, NULL, NULL, 'workspace_owner', $2
		FROM workspace_settings ws
		JOIN used u ON u.workspace_id = ws.workspace_id
		JOIN member m ON m.workspace_id = ws.workspace_id AND m.role = 'owner'
		WHERE ws.workdir_disk_quota_gb IS NOT NULL AND ws.workdir_disk_quota_gb > 0
		  AND u.bytes >= ws.workdir_disk_quota_gb::bigint * $3
		  AND NOT EXISTS (SELECT 1 FROM inbox_item i
		                  WHERE i.member_id = m.id AND i.type = 'workdir_quota' AND i.read_at IS NULL)`,
		inbox.Severity(inbox.TypeWorkdirQuota), now, gib)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
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
