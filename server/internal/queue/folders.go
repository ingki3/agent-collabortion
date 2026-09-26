package queue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// bundleWorkdir is the bundle's `workdir` block as planned by the server
// (daemon-protocol v0.10.0 §4.1·§6.1).
type bundleWorkdir struct {
	ID         uuid.UUID
	Kind       string // worktree | dir
	Path       string
	SharedPath string
	Branch     string
	// Created is true when this bundle is the first to name the folder.
	Created bool
}

// planBundleWorkdir names this task's folder — for every isolation kind, the
// server's to name (daemon-protocol v0.10.0 §4.1). The row is made HERE,
// before the bundle leaves, so `workdir.id` rides from the first attempt.
//
//   - no `workdir_root` → errNoWorkdirRoot, for `none` as for `worktree`
//     (§4.1 v0.10.0 claim rule; S-55: never a relative path).
//   - `none`: the lane's own row when it has one (D6 A — an old
//     `sessions/<room>/<lane>` folder keeps serving its lane), else the
//     (room, mission, agent) row, made on first use (D3 A).
//   - `worktree`: the agent's checkout in this room (C3), else a new one at
//     `rooms/<room>/_worktrees/<agent>` on branch
//     `colab/<Slug(room)>-<room_id8>/<Slug(agent)>`.
//   - a task of a mission also gets the mission's `_shared` (§4.1
//     `shared_path`, D4 A · D7 C — outside the repository under worktree).
func planBundleWorkdir(ctx context.Context, tx pgx.Tx, t *tasks.Row, missionID *uuid.UUID, runtimeID uuid.UUID, isolationKind, roomName, workTitle, agentName string, now time.Time) (bundleWorkdir, error) {
	var root *string
	if err := tx.QueryRow(ctx, `SELECT workdir_root FROM runtime WHERE id = $1`, runtimeID).Scan(&root); err != nil && !isNoRows(err) {
		return bundleWorkdir{}, fmt.Errorf("queue: bundle workdir_root: %w", err)
	}
	r := deref(root)
	if r == "" || !filepath.IsAbs(r) {
		return bundleWorkdir{}, errNoWorkdirRoot
	}
	// missionID is the turn's mission as loadRoomBrief resolved it: nil for
	// a turn outside any mission, for a mission deleted under a queued task,
	// and for a work_id that names another room's mission (#323 NN1) — no
	// folder of another room is ever planned or listed.
	workID := missionID
	owner := workdirs.Owner{
		RoomID: t.SessionID, RoomName: roomName,
		WorkID: workID, WorkTitle: workTitle,
		AgentID: t.AgentID, AgentName: agentName, Role: workdirs.RoleAgent,
	}
	var out bundleWorkdir
	if isolationKind == "worktree" {
		wt, err := planWorktreeCheckout(ctx, tx, t, runtimeID, r, owner, now)
		if err != nil {
			return bundleWorkdir{}, err
		}
		out = wt
	} else {
		out.Kind = "dir"
		row, found, err := workdirs.LaneRow(ctx, tx, t.LaneID)
		if err != nil {
			return bundleWorkdir{}, err
		}
		if !found {
			lane := t.LaneID
			row, err = workdirs.EnsureDirRow(ctx, tx, runtimeID, r, owner, &lane, now)
			if errors.Is(err, workdirs.ErrNoRoot) {
				return bundleWorkdir{}, errNoWorkdirRoot
			}
			if err != nil {
				return bundleWorkdir{}, err
			}
			if err := workdirs.BindLane(ctx, tx, t.LaneID, row.ID, now); err != nil {
				return bundleWorkdir{}, err
			}
		}
		out.ID, out.Path, out.Created = row.ID, row.Path, row.Created
	}
	if workID != nil {
		shared := owner
		shared.AgentID, shared.AgentName, shared.Role = uuid.Nil, "", workdirs.RoleShared
		row, err := workdirs.EnsureDirRow(ctx, tx, runtimeID, r, shared, nil, now)
		if err != nil {
			return bundleWorkdir{}, fmt.Errorf("queue: shared folder: %w", err)
		}
		out.SharedPath = row.Path
	}
	return out, nil
}

// planWorktreeCheckout is the `worktree` half of planBundleWorkdir.
func planWorktreeCheckout(ctx context.Context, tx pgx.Tx, t *tasks.Row, runtimeID uuid.UUID, root string, owner workdirs.Owner, now time.Time) (bundleWorkdir, error) {
	// FR-6.4/C3: ONE worktree per agent, reused across that agent's lanes.
	// The existing path is looked up by agent, not by lane, so a second lane
	// of the same agent gets the same checkout back rather than a second
	// worktree of the same branch (E13-02, E16-B's "워크트리 2개").
	//
	// E13-08 is the same query read the other way: the bundle names only
	// what THIS agent owns.
	existing := ""
	paths, err := workdirs.BundleWorkdirPaths(ctx, tx, t.SessionID, t.AgentID)
	if err != nil {
		return bundleWorkdir{}, err
	}
	for _, p := range paths {
		// S-62 (PR #173 리뷰 NN1): a row written BEFORE migration 0019 can
		// hold a RELATIVE path, and the daemon absolutises it against its
		// own CWD. Only an absolute row is reusable; a relative one is put
		// on the feed and the checkout is planned afresh.
		if filepath.IsAbs(p) {
			if existing == "" {
				existing = p
			}
			continue
		}
		if err := noteRelativeWorkdirRow(ctx, tx, t, p, now); err != nil {
			return bundleWorkdir{}, err
		}
	}
	out := bundleWorkdir{Kind: "worktree"}
	if existing != "" {
		// D6 A: an existing checkout keeps its stored path AND branch — an
		// old `worktrees/<mission slug>/<agent>` one included (FINDING-1 is
		// fixed for new checkouts only).
		var branch *string
		_ = tx.QueryRow(ctx, `SELECT branch FROM workdir WHERE session_id = $1 AND path_or_ref = $2`, t.SessionID, existing).Scan(&branch)
		out.Path = existing
		out.Branch = deref(branch)
		if out.Branch == "" {
			out.Branch = workdirs.WorktreeBranch(owner.RoomName, owner.RoomID, owner.AgentName)
		}
	} else {
		p, err := workdirs.PlanNewWorktree(ctx, tx, runtimeID, root, owner)
		if err != nil {
			return bundleWorkdir{}, err
		}
		if p == "" {
			return bundleWorkdir{}, errNoWorkdirRoot
		}
		out.Path, out.Created = p, true
		out.Branch = workdirs.WorktreeBranch(owner.RoomName, owner.RoomID, owner.AgentName)
	}
	var branch *string
	if out.Created {
		br := out.Branch
		branch = &br
	}
	id, err := workdirs.EnsureBundleRow(ctx, tx, workdirs.BundleRow{
		SessionID: t.SessionID, AgentID: t.AgentID, LaneID: t.LaneID,
		Kind: "worktree", Path: out.Path, Branch: branch,
	}, now)
	if err != nil {
		return bundleWorkdir{}, fmt.Errorf("queue: bundle workdir row: %w", err)
	}
	out.ID = id
	return out, nil
}

// Folder lines of harness v0.9.7 `<folders>`.
const (
	foldersYou    = "you: %s  (your working folder — write here)\n"
	foldersShared = "shared: %s  (everyone on this mission reads and writes)\n"
	foldersPeer   = "- %s: %s  (read only)\n"
	foldersLanes  = "Your other lanes on this mission work in this same folder at the same time: put your lane label `%s` in the name of every new file you create.\n"
)

// renderFolders is harness v0.9.7's `<folders>` block. Peers are listed only
// when their row exists — a path that is not there sends an agent in circles.
func renderFolders(ctx context.Context, tx pgx.Tx, t *tasks.Row, missionID *uuid.UUID, worktree bool, wd bundleWorkdir, surf Surface) (string, error) {
	var b strings.Builder
	b.WriteString("<folders>\n")
	fmt.Fprintf(&b, foldersYou, wd.Path)
	if wd.SharedPath != "" && missionID != nil {
		fmt.Fprintf(&b, foldersShared, wd.SharedPath)
		// E13-08 (계약, 테스트 73): under `worktree` isolation a bundle names
		// NO other agent's checkout — a peer's checkout is a dirty working
		// copy of the user's repository, and a reviewer who reads it reviews
		// something nobody submitted. So the mission's peers are listed for
		// `none` (folders the server made outside the repository) and the
		// shared folder — which D7 C puts outside the repository too — is
		// where a worktree mission meets.
		if !worktree {
			peers, err := workdirs.MissionPeers(ctx, tx, t.SessionID, *missionID, t.AgentID, false)
			if err != nil {
				return "", err
			}
			for _, p := range peers {
				fmt.Fprintf(&b, foldersPeer, p.Name, p.Path)
			}
		}
	}
	if !worktree && wd.ID != uuid.Nil {
		// D3 A: another lane of this agent writes in this very folder.
		shares, err := workdirs.SharesFolder(ctx, tx, wd.ID, t.LaneID)
		if err != nil {
			return "", err
		}
		if shares {
			fmt.Fprintf(&b, foldersLanes, t.LaneID.String()[:8])
		}
	}
	b.WriteString(surf.FoldersLast + "\n")
	b.WriteString("</folders>\n\n")
	return b.String(), nil
}
