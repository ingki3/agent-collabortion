package workdirs

// layout.go — daemon-protocol v0.10.0 §6.1 (T-FOLDERS, Director 승인 2026-09-26):
// work folders are laid out room → mission → agent, and the SERVER names every
// path, for every kind.
//
//	<workdir_root>/rooms/<room>/
//	    <mission>/_shared/          미션 공용 (role=shared)
//	    <mission>/<agent>/          에이전트 cwd (role=agent, none 격리)
//	    _room/<agent>/              미션 밖 턴 (none 격리)
//	    _worktrees/<agent>/         worktree 체크아웃 (방×에이전트)
//
// A piece is `PathSlug(name)-<id 앞 8자리>`, fixed when the row is made: the
// path is stored in `workdir.path_or_ref` and never re-derived from a name, so
// renaming a room, a mission or an agent (FR-2.1.2) cannot move a running
// lane's cwd or break `session/load {cwd}` (harness §6).

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// Reserved pieces of §6.1. None of them ends in `-<id>`, so no name piece
// (which always does) can ever be equal to one.
const (
	RoomsDir     = "rooms"
	OutsideDir   = "_room"
	SharedDir    = "_shared"
	WorktreesSub = "_worktrees"
)

// Row roles (openapi Workdir.role v0.3.4).
const (
	RoleAgent  = "agent"
	RoleShared = "shared"
)

// ID piece lengths: 8 normally, 12 when the 8-digit path collides with a
// row of another owner on the same runtime (§6.1 「id 충돌」).
const (
	IDLen     = 8
	IDLenLong = 12
)

// pathSlugMax is §6.1's 40 runes.
const pathSlugMax = 40

// RoomFolderSQL is the SQL shape of a §6.1 `_room` folder —
// `rooms/<room>/_room/<agent>`, the folder of an agent's turns OUTSIDE any
// mission. `w` is the `workdir` row.
//
// Lead 판정 2026-09-26 (PR #345 리뷰 345a NN3): GC judges a folder by the
// ROW's `work_id`, not by the mission its lanes happen to hold. A `_room` row
// is made with `work_id` NULL and keeps it, so when its lane is later bound to
// a mission (a mission message reaching a lane that started outside one, the
// lane reuse of D6 — which is NOT restricted, so a running lane's cwd and its
// runtime resume never move) the folder still follows the `_room` rule:
// `last_used_at + workdir_retention_days`, never the mission's close.
//
// The shape is the discriminator because `work_id IS NULL` alone also covers
// the OLD layout (`sessions/<room>/<lane>`), which keeps its close-time rule
// (D6 A). `_` is a LIKE wildcard, so this is a regex match.
const RoomFolderSQL = `(w.kind <> 'worktree' AND w.path_or_ref ~ '/` + RoomsDir + `/[^/]+/` + OutsideDir + `/[^/]+/?$')`

// PathSlug is §6.1's path slug — Hangul (and every other letter) is kept, so
// a person reading `ls` recognises the room. NFC → lower case → letters
// (\p{L}), digits (\p{N}) and `_` stay, every other run becomes one `-` → trim
// `-` → cut at 40 runes and trim a trailing `-` again → `x` when empty.
//
// The git branch keeps the ASCII Slug (ref compatibility, §6.1).
func PathSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(norm.NFC.String(s)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if utf8.RuneCountInString(out) > pathSlugMax {
		out = strings.TrimRight(string([]rune(out)[:pathSlugMax]), "-")
	}
	if out == "" {
		return "x"
	}
	return out
}

// idPiece is the first n hex digits of the id (dashes left out, so 12 is 12
// digits and not 11 plus a dash).
func idPiece(id uuid.UUID, n int) string {
	h := strings.ReplaceAll(id.String(), "-", "")
	if n <= 0 || n > len(h) {
		n = IDLen
	}
	return h[:n]
}

// Piece is one §6.1 name piece: `PathSlug(name)-<id[:n]>`.
func Piece(name string, id uuid.UUID, n int) string {
	return PathSlug(name) + "-" + idPiece(id, n)
}

// Owner names what one row belongs to — the §6.1 row unit (D3 A). The name
// fields are the CURRENT names, used only when a row is made.
type Owner struct {
	RoomID    uuid.UUID
	RoomName  string
	WorkID    *uuid.UUID // nil = outside any mission (`_room`)
	WorkTitle string
	AgentID   uuid.UUID // uuid.Nil on a shared row
	AgentName string
	Role      string // agent | shared
}

// roomDir is `<root>/rooms/<room>`.
func roomDir(root string, o Owner, n int) string {
	return path.Join(root, RoomsDir, Piece(o.RoomName, o.RoomID, n))
}

// PlanDir is the §6.1 path of a `none` row: the agent's folder (in its
// mission, or `_room` outside any) or the mission's `_shared`. Empty when the
// root is unknown — never a relative path (S-55).
func PlanDir(root string, o Owner, n int) string {
	if root == "" || !filepath.IsAbs(root) {
		return ""
	}
	mission := OutsideDir
	if o.WorkID != nil {
		mission = Piece(o.WorkTitle, *o.WorkID, n)
	}
	if o.Role == RoleShared {
		if o.WorkID == nil {
			return ""
		}
		return path.Join(roomDir(root, o, n), mission, SharedDir)
	}
	return path.Join(roomDir(root, o, n), mission, Piece(o.AgentName, o.AgentID, n))
}

// PlanWorktreePath is the §6.1 checkout of a `worktree` room: one per room ×
// agent, `rooms/<room>/_worktrees/<agent>`.
func PlanWorktreePath(root string, o Owner, n int) string {
	if root == "" || !filepath.IsAbs(root) {
		return ""
	}
	return path.Join(roomDir(root, o, n), WorktreesSub, Piece(o.AgentName, o.AgentID, n))
}

// WorktreeBranch is §6.1's branch of a NEW checkout:
// `colab/<Slug(room)>-<room_id[:8]>/<Slug(agent)>` (Lead 판정 2026-09-26 — a
// Hangul room name is `x` under the ASCII Slug, and two such rooms on one
// repository would share branches without the id). FINDING-1: the material is
// the ROOM name, not the agent's first mission title.
func WorktreeBranch(roomName string, roomID uuid.UUID, agentName string) string {
	return "colab/" + Slug(roomName) + "-" + idPiece(roomID, IDLen) + "/" + Slug(agentName)
}

// pathTaken is §6.1's collision test: the path is already a live row's on
// this runtime, and that row is not this owner's.
func pathTaken(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, p string, o Owner) (bool, error) {
	var taken bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM workdir w JOIN room r ON r.id = w.session_id
		   WHERE r.runtime_id = $1 AND w.path_or_ref = $2
		     AND NOT (w.session_id = $3 AND w.work_id IS NOT DISTINCT FROM $4
		              AND w.agent_id IS NOT DISTINCT FROM $5 AND w.role = $6))`,
		runtimeID, p, o.RoomID, o.WorkID, nilIfZero(o.AgentID), o.Role).Scan(&taken)
	if err != nil {
		return false, fmt.Errorf("workdirs: path collision: %w", err)
	}
	return taken, nil
}

// planFree runs plan with 8-digit pieces and, when that path is another
// owner's on this runtime, with 12 (deterministic: same input, same answer).
func planFree(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, o Owner, plan func(n int) string) (string, error) {
	p := plan(IDLen)
	if p == "" {
		return "", nil
	}
	taken, err := pathTaken(ctx, q, runtimeID, p, o)
	if err != nil || !taken {
		return p, err
	}
	return plan(IDLenLong), nil
}

// ErrNoRoot is the refusal when the runtime has not reported `workdir_root`
// (probe §3): the server cannot name an absolute path (daemon-protocol
// v0.10.0 §4.1 — `none` is refused like `worktree`).
var ErrNoRoot = errors.New("workdirs: runtime has no workdir_root — cannot name an absolute path")

// DirRow is what EnsureDirRow answers: the row the bundle carries.
type DirRow struct {
	ID   uuid.UUID
	Path string
	// Created is true when this call made (or revived) the row.
	Created bool
}

// liveRowSQL is a row the bundle may still name: not collected, not the dead
// machine's after a rebind (U2), and an absolute path (S-62).
const liveRowSQL = `w.status <> 'deleted' AND w.gc_blocked_reason IS DISTINCT FROM 'runtime_gone' AND w.path_or_ref LIKE '/%'`

// LaneRow is the row `lane.workdir_id` points at when it is still usable —
// D6 A: a lane that already has a folder (the old `sessions/<room>/<lane>`
// layout included) keeps it, so a re-entry's cwd does not move.
func LaneRow(ctx context.Context, q db.DBTX, laneID uuid.UUID) (DirRow, bool, error) {
	var r DirRow
	err := q.QueryRow(ctx, `
		SELECT w.id, w.path_or_ref FROM lane l JOIN workdir w ON w.id = l.workdir_id
		 WHERE l.id = $1 AND w.kind <> 'worktree' AND `+liveRowSQL, laneID).Scan(&r.ID, &r.Path)
	if errors.Is(err, pgx.ErrNoRows) {
		return DirRow{}, false, nil
	}
	if err != nil {
		return DirRow{}, false, fmt.Errorf("workdirs: lane row: %w", err)
	}
	return r, true, nil
}

// EnsureDirRow returns the `none` row of this owner — made on the first
// attempt when there is none (daemon-protocol v0.10.0 §4.1: `workdir.id` from
// the first attempt, for `dir` too). Lookup is by owner, not by path: the path
// was fixed when the row was made and a rename must not re-plan it. laneID
// is stored on a new agent row for diagnosis only (openapi Workdir.lane_id).
//
// A `deleted` row of the same path (a `_room` folder GC'd after retention,
// then the agent speaks outside a mission again) is revived rather than
// duplicated: (session_id, path_or_ref) is unique.
func EnsureDirRow(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, root string, o Owner, laneID *uuid.UUID, now time.Time) (DirRow, error) {
	var r DirRow
	err := q.QueryRow(ctx, `
		SELECT w.id, w.path_or_ref FROM workdir w
		 WHERE w.session_id = $1 AND w.work_id IS NOT DISTINCT FROM $2
		   AND w.agent_id IS NOT DISTINCT FROM $3 AND w.role = $4 AND w.kind <> 'worktree'
		   AND `+liveRowSQL+`
		 ORDER BY w.created_at, w.id LIMIT 1`,
		o.RoomID, o.WorkID, nilIfZero(o.AgentID), o.Role).Scan(&r.ID, &r.Path)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DirRow{}, fmt.Errorf("workdirs: dir row: %w", err)
	}
	if root == "" {
		return DirRow{}, ErrNoRoot
	}
	p, err := planFree(ctx, q, runtimeID, o, func(n int) string { return PlanDir(root, o, n) })
	if err != nil {
		return DirRow{}, err
	}
	if p == "" {
		return DirRow{}, ErrNoRoot
	}
	var lane *uuid.UUID
	if o.Role == RoleAgent {
		lane = laneID
	}
	err = q.QueryRow(ctx, `
		INSERT INTO workdir (session_id, work_id, agent_id, lane_id, role, kind, path_or_ref, status, last_used_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'dir', $6, 'active', $7, $7, $7)
		ON CONFLICT (session_id, path_or_ref) DO UPDATE SET
		    status = 'active', gc_blocked_reason = NULL, gc_notified_at = NULL, retain_until = NULL,
		    work_id = EXCLUDED.work_id, agent_id = EXCLUDED.agent_id, role = EXCLUDED.role,
		    lane_id = COALESCE(EXCLUDED.lane_id, workdir.lane_id),
		    last_used_at = EXCLUDED.last_used_at, updated_at = EXCLUDED.updated_at
		RETURNING id, path_or_ref`,
		o.RoomID, o.WorkID, nilIfZero(o.AgentID), lane, o.Role, p, now).Scan(&r.ID, &r.Path)
	r.Created = true
	if err != nil {
		return DirRow{}, fmt.Errorf("workdirs: make dir row: %w", err)
	}
	return r, nil
}

// PlanNewWorktree is the checkout path of a room × agent that has none yet,
// with §6.1's collision rule applied.
func PlanNewWorktree(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, root string, o Owner) (string, error) {
	return planFree(ctx, q, runtimeID, o, func(n int) string { return PlanWorktreePath(root, o, n) })
}

// BindLane points the lane at the row (D3 A: several lanes, one row).
func BindLane(ctx context.Context, q db.DBTX, laneID, workdirID uuid.UUID, now time.Time) error {
	if _, err := q.Exec(ctx, `
		UPDATE lane SET workdir_id = $1, updated_at = $2 WHERE id = $3 AND workdir_id IS DISTINCT FROM $1`,
		workdirID, now, laneID); err != nil {
		return fmt.Errorf("workdirs: bind lane: %w", err)
	}
	return nil
}

// Peer is one `<folders>` peer line (harness v0.9.7).
type Peer struct {
	AgentID uuid.UUID
	Name    string
	Path    string
}

// MissionPeers are the other agents of the mission that have a row — only
// folders that exist (harness v0.9.7: 「행이 실제로 있는 동료만」). `none`:
// the mission's role=agent rows. `worktree`: the checkout rows of agents that
// have a lane in the mission. Ordered by name, then agent id.
func MissionPeers(ctx context.Context, q db.DBTX, roomID, workID, self uuid.UUID, worktree bool) ([]Peer, error) {
	var rows pgx.Rows
	var err error
	if worktree {
		rows, err = q.Query(ctx, `
			SELECT DISTINCT ON (a.name, a.id) a.id, a.name, w.path_or_ref
			  FROM workdir w JOIN agent a ON a.id = w.agent_id
			 WHERE w.session_id = $1 AND w.kind = 'worktree' AND w.agent_id <> $3 AND `+liveRowSQL+`
			   AND EXISTS (SELECT 1 FROM lane l WHERE l.session_id = $1 AND l.work_id = $2 AND l.agent_id = w.agent_id)
			 ORDER BY a.name, a.id, w.created_at`, roomID, workID, self)
	} else {
		rows, err = q.Query(ctx, `
			SELECT DISTINCT ON (a.name, a.id) a.id, a.name, w.path_or_ref
			  FROM workdir w JOIN agent a ON a.id = w.agent_id
			 WHERE w.session_id = $1 AND w.work_id = $2 AND w.role = 'agent' AND w.kind <> 'worktree'
			   AND w.agent_id <> $3 AND `+liveRowSQL+`
			 ORDER BY a.name, a.id, w.created_at`, roomID, workID, self)
	}
	if err != nil {
		return nil, fmt.Errorf("workdirs: mission peers: %w", err)
	}
	defer rows.Close()
	var out []Peer
	for rows.Next() {
		var p Peer
		if err := rows.Scan(&p.AgentID, &p.Name, &p.Path); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SharesFolder is true when another lane of this agent points at the same
// row — harness v0.9.7's 갈래 표지 line (D3 A).
func SharesFolder(ctx context.Context, q db.DBTX, workdirID, laneID uuid.UUID) (bool, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM lane WHERE workdir_id = $1 AND id <> $2`, workdirID, laneID).Scan(&n); err != nil {
		return false, fmt.Errorf("workdirs: shared folder: %w", err)
	}
	return n > 0, nil
}
