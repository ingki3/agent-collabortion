// Package workdir prepares lane working directories (PRD §5 isolation,
// FR-6.4).
//
//	none/dir  — one sub-folder per LANE under the daemon's workdir root,
//	            reused between attempts (this file).
//	worktree  — one git checkout per AGENT, branch `colab/<session>/<agent>`
//	            (worktree.go, P4).
//
// Orphan-left changes are never deleted (E11-06).
package workdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

// ErrUnsupported is returned for isolation kinds this daemon cannot prepare.
// `container` is the only one left (PRD §5); it is not v1.
var ErrUnsupported = errors.New("workdir: isolation kind not supported")

// Path returns the lane folder for `none` isolation:
// <root>/sessions/<session_id>/<lane_id>.
func Path(root, sessionID, laneID string) string {
	return filepath.Join(root, "sessions", safe(sessionID), safe(laneID))
}

func safe(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// ResolvePath turns a bundle's `workdir.path` into an absolute path on THIS
// machine (daemon-protocol §4.1 v0.7.3).
//
// The contract says the server sends an absolute path, and this is the
// daemon's half of that clause: "path 가 상대면 `<workdir_root>` 기준으로
// 해석한다". Absolutising a relative path against the daemon's own CWD — what
// `filepath.Abs` does and what this code used to do — pointed every
// `worktree` lane at a directory that does not exist, and the runtime died
// with `spawn: fork/exec …/npx: no such file or directory` (T-I4 차단 ①).
// The daemon's CWD is wherever the operator happened to launch it from; it
// has never been a workdir.
//
// THERE IS NO CWD FALLBACK LEFT (PR #172 리뷰 NN1). A relative path with no
// root is a path this daemon cannot place, and the honest answer to that is
// nothing: the empty string travels to `Prepare` ("workdir: empty root") and
// to `Verify` ("no directory to run in"), which name the real fault. The
// fallback that used to live here — `filepath.Abs(p)`, i.e. the CWD — is the
// one line §4.1 forbids, and leaving it as an unreachable branch left the
// T-I4 차단 ① behaviour one config bug away from coming back.
func ResolvePath(root, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, p) // Join cleans
}

// realPath is `filepath.Abs` plus every symlink that actually exists on the
// way — the longest existing ancestor is resolved and the rest is appended,
// so a path that has not been created yet still answers about the right
// directory.
//
// It exists because the two sides of the guards below come from different
// places and only one of them was ever resolved: `repo_path` reaches
// `UnderRoot` as `git rev-parse --show-toplevel`, which git ALWAYS gives
// resolved, while the bundle's `workdir.path` is whatever the server stored.
// On macOS that is enough to disarm the whole check — `/tmp` and `/var` are
// symlinks to `/private/…`, so `<repo>/wt` and the same repository's
// toplevel compare as two unrelated trees and the "never inside the user's
// repository" guard silently passes anything (found by the NN5 fixture,
// T-D10b). Symlinked repository paths are ordinary: `/tmp/...` on macOS, a
// symlinked home, an `/etc/auto_home` mount.
//
// It also makes `Remove` strictly safer: a symlink under the root pointing
// out of it used to read as "inside the root", and `os.RemoveAll` follows the
// last component.
func realPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	rest, cur := "", abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest, cur = filepath.Join(filepath.Base(cur), rest), parent
	}
}

// UnderRoot reports whether p is the workdir root's own subtree. `Remove`
// already refused anything outside it; `worktree` preparation asks the same
// question BEFORE creating a checkout instead of after (§4.1 v0.7.3 데몬
// 방어, D-21(b)).
func UnderRoot(root, p string) bool {
	if root == "" {
		return false
	}
	rootAbs := realPath(root)
	abs := realPath(p)
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// Verify is the last check before a runtime is spawned: the directory the
// process will run in has to exist (§4.1 v0.7.3 데몬 방어, D-21(c)).
//
// The error NAMES THE PATH on purpose. Without this check the missing
// directory surfaced as the runtime's own `exec …/npx: no such file or
// directory` — a message about the adapter binary, for a fault that has
// nothing to do with it, which sent the G7 investigation looking at node
// installs for as long as it took to strace the spawn.
func Verify(path string) error {
	if path == "" {
		return errors.New("workdir: no directory to run in")
	}
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("workdir does not exist: %s", path)
	}
	if err != nil {
		return fmt.Errorf("workdir %s: %w", path, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("workdir is not a directory: %s", path)
	}
	return nil
}

// Prepare resolves and creates the workdir for a bundle. An explicit
// bundle.workdir.path wins; otherwise the lane folder under root. Existing
// folders are reused as-is (reuse=false still never deletes — FR-9.1).
func Prepare(root string, b contracts.TaskBundle) (string, error) {
	switch b.Workdir.Kind {
	case "", "dir", "none":
	case "worktree":
		return PrepareWorktree(root, b)
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupported, b.Workdir.Kind)
	}
	path := ResolvePath(root, b.Workdir.Path)
	if path == "" {
		if root == "" {
			return "", errors.New("workdir: empty root")
		}
		path = Path(root, b.Task.SessionID, b.Task.LaneID)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// §6 v0.7.3: the report needs the session uuid and the lane, and neither
	// survives in the directory name once the server names the path.
	if root != "" {
		if err := RecordWorkdir(root, Record{
			Kind: "dir", Path: abs, SessionID: b.Task.SessionID,
			AgentID: b.Task.AgentID, AgentName: b.Task.AgentName, LaneID: b.Task.LaneID,
		}); err != nil {
			return "", fmt.Errorf("workdir index: %w", err)
		}
	}
	return abs, nil
}

// Info is one workdir as reported to the server (daemon-protocol §6).
type Info struct {
	Kind       string    `json:"kind"`
	Path       string    `json:"path"`
	SessionID  string    `json:"session_id"`
	LaneID     string    `json:"lane_id,omitempty"`
	AgentID    string    `json:"agent_id,omitempty"`
	Bytes      int64     `json:"bytes"`
	LastUsedAt time.Time `json:"last_used_at"`
	// Git is the §6 report's `git` block, set for a checkout: the numbers
	// the SERVER judges 병합·클린 from (E13-10~13). nil for a plain folder.
	Git *contracts.WorkdirGit `json:"git,omitempty"`
	// GC is set only on the report that answers a §4.3 `gc` command.
	GC *GCResult `json:"gc,omitempty"`
}

// GCResult is what the daemon did with a workdir a `gc` command named
// (daemon-protocol §6, Lead decision 2026-09-06).
//
// A refusal has to travel on the wire. Before this, an isolation the daemon
// cannot collect produced one line in the daemon's own log ("command gc
// ignored (P4)") and nothing else: the server kept re-issuing the command,
// the disk kept filling, and the only person who could have acted never saw
// it. Silence is not an answer to a command.
type GCResult struct {
	// Status is "deleted" or "refused".
	Status string `json:"status"`
	// Reason is set on a refusal, e.g. isolation_worktree_p4.
	Reason string `json:"reason,omitempty"`
	// ID echoes the server's workdir row id from the command, so the server
	// can match the answer to what it asked for without matching on paths.
	ID string `json:"id,omitempty"`
}

// GCStatus values (§6).
const (
	GCDeleted = "deleted"
	GCRefused = "refused"
	// GCReasonWorktreeP4 was the P1~P3 refusal: `worktree` isolation had no
	// collector, so every gc naming a checkout was answered `refused`. P4
	// implements it (RemoveWorktree), and the constant stays only so a
	// server row written before this daemon shipped is still recognisable.
	//
	// Deprecated: no code path produces it any more.
	GCReasonWorktreeP4 = "isolation_worktree_p4"
)

// IsWorktree reports whether p looks like a git worktree checkout: a worktree
// has `.git` as a FILE (`gitdir: …`), a plain clone has it as a directory.
// Either way it is not ours to delete with an rm -rf (§6 "`worktree` 삭제는
// `git worktree remove`만, 브랜치는 남긴다").
func IsWorktree(p string) bool { return gitrepo.IsWorktreeCheckout(p) }

// Remove deletes one workdir named by a `gc` command. It refuses anything
// outside root — the command carries a path the server stored, and a bug or a
// tampered row must not turn the daemon into a remote `rm -rf /`.
func Remove(root, p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	rootAbs, _ := filepath.Abs(root)
	if !UnderRoot(root, abs) {
		return fmt.Errorf("workdir: refusing to remove %q: not inside the workdir root %q", abs, rootAbs)
	}
	return os.RemoveAll(abs)
}

// SessionLanes lists the lane folders of one session — the fallback target of
// a `gc` command that names no paths (only `session_id`).
func SessionLanes(root, sessionID string) []Info {
	all, err := List(root)
	if err != nil {
		return nil
	}
	var out []Info
	want := safe(sessionID)
	for _, w := range all {
		if w.SessionID == want {
			out = append(out, w)
		}
	}
	return out
}

// List enumerates lane folders under root with size and mtime.
func List(root string) ([]Info, error) {
	base := filepath.Join(root, "sessions")
	sessions, err := os.ReadDir(base)
	// A root that has only ever run `worktree` sessions has no `sessions/`
	// directory at all; returning early here would hide every checkout.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var out []Info
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		lanes, err := os.ReadDir(filepath.Join(base, s.Name()))
		if err != nil {
			continue
		}
		for _, l := range lanes {
			if !l.IsDir() {
				continue
			}
			p := filepath.Join(base, s.Name(), l.Name())
			size, last := DiskUsage(p)
			out = append(out, Info{Kind: "dir", Path: p, SessionID: s.Name(), LaneID: l.Name(), Bytes: size, LastUsedAt: last})
		}
	}
	// §6 "데몬은 workdir 목록을 probe와 함께 보고한다" — the list is the whole
	// disk footprint, and a `worktree` checkout left out of it is a workdir
	// S13 cannot show and GC can never reach.
	out = append(out, ListWorktrees(root)...)
	return out, nil
}

// DiskUsage sums file sizes under p and returns the newest mtime.
func DiskUsage(p string) (int64, time.Time) {
	var size int64
	var last time.Time
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			size += fi.Size()
			if fi.ModTime().After(last) {
				last = fi.ModTime()
			}
		}
		return nil
	})
	return size, last
}
