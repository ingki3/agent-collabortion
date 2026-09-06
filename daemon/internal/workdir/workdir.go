// Package workdir prepares lane working directories. P1 implements `none`
// isolation only (PRD §5 isolation, FR-6.4): every lane of a session gets a
// sub-folder under the daemon's workdir root and is reused between attempts.
// Orphan-left changes are never deleted (E11-06). `worktree` is P4.
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
)

// ErrUnsupported is returned for isolation kinds not implemented in P1.
var ErrUnsupported = errors.New("workdir: isolation kind not supported in P1")

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

// Prepare resolves and creates the workdir for a bundle. An explicit
// bundle.workdir.path wins; otherwise the lane folder under root. Existing
// folders are reused as-is (reuse=false still never deletes — FR-9.1).
func Prepare(root string, b contracts.TaskBundle) (string, error) {
	switch b.Workdir.Kind {
	case "", "dir", "none":
	case "worktree":
		return "", fmt.Errorf("%w: %s (P4)", ErrUnsupported, b.Workdir.Kind)
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupported, b.Workdir.Kind)
	}
	path := b.Workdir.Path
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
	return abs, nil
}

// Info is one workdir as reported to the server (daemon-protocol §6).
type Info struct {
	Kind       string    `json:"kind"`
	Path       string    `json:"path"`
	SessionID  string    `json:"session_id"`
	LaneID     string    `json:"lane_id,omitempty"`
	Bytes      int64     `json:"bytes"`
	LastUsedAt time.Time `json:"last_used_at"`
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
	// GCReasonWorktreeP4 is the one refusal v1 can produce: `worktree`
	// isolation is P4, and removing one is `git worktree remove` plus a
	// branch to keep (§6, E13-10) — not an rm -rf.
	GCReasonWorktreeP4 = "isolation_worktree_p4"
)

// IsWorktree reports whether p looks like a git worktree checkout: a worktree
// has `.git` as a FILE (`gitdir: …`), a plain clone has it as a directory.
// Either way it is not ours to delete with an rm -rf (§6 "`worktree` 삭제는
// `git worktree remove`만, 브랜치는 남긴다" — P4).
func IsWorktree(p string) bool {
	fi, err := os.Stat(filepath.Join(p, ".git"))
	return err == nil && !fi.IsDir()
}

// Remove deletes one workdir named by a `gc` command. It refuses anything
// outside root — the command carries a path the server stored, and a bug or a
// tampered row must not turn the daemon into a remote `rm -rf /`.
func Remove(root, p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if rootAbs == "" || abs == rootAbs {
		return fmt.Errorf("workdir: refusing to remove %q: not inside the workdir root %q", abs, rootAbs)
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
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
