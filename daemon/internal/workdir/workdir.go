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
