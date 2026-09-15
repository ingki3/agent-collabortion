package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var seqMu sync.Mutex

// NextSeq returns the next client seq for the task (colab-cli.md §1 v0.2):
// the seq is task-scoped and does NOT reset per attempt, so the derived
// Idempotency-Key UUIDv5(task:<task_id>:<seq>) is unique across attempts.
//
// Every CLI invocation is a fresh process, so the counter is persisted as
// "<attempt> <seq>" under the state dir (COLAB_STATE_DIR →
// $XDG_STATE_HOME/colab → ~/.local/state/colab). Within one attempt the file
// is authoritative and no round trip is needed. On an attempt boundary
// (no file, or the file was written by another attempt — possibly on another
// host) the seq restarts from CliContext.last_seq + 1 via GET /cli/context.
// COLAB_CLIENT_SEQ / Config.ClientSeq forces the value (retries, tests).
func (c *Client) NextSeq(ctx context.Context, taskID string, attempt int) (int, error) {
	if c.cfg.ClientSeq > 0 {
		return c.cfg.ClientSeq, nil
	}
	dir, err := c.stateDir()
	if err != nil {
		return 0, err
	}
	seqMu.Lock()
	defer seqMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, &Error{Exit: ExitUnreachable, Code: "state_dir", Title: "cannot create state dir", Detail: err.Error()}
	}
	f := filepath.Join(dir, "seq-"+sanitize(taskID))
	n := -1
	if raw, err := os.ReadFile(f); err == nil {
		var a, s int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d %d", &a, &s); err == nil && a == attempt {
			n = s + 1
		}
	}
	if n < 0 {
		// Attempt boundary (or first post ever): continue after the server's
		// last_seq so attempt 2 never re-uses attempt 1's keys (E8-04).
		cc, err := c.Context(ctx)
		if err != nil {
			return 0, err
		}
		n = cc.LastSeq + 1
	}
	tmp := f + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d %d", attempt, n)), 0o600); err != nil {
		return 0, &Error{Exit: ExitUnreachable, Code: "state_dir", Title: "cannot persist client seq", Detail: err.Error()}
	}
	if err := os.Rename(tmp, f); err != nil {
		return 0, &Error{Exit: ExitUnreachable, Code: "state_dir", Title: "cannot persist client seq", Detail: err.Error()}
	}
	return n, nil
}

func (c *Client) stateDir() (string, error) {
	if c.cfg.StateDir != "" {
		return c.cfg.StateDir, nil
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "colab"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", &Error{Exit: ExitUnreachable, Code: "state_dir", Title: "no home dir", Detail: err.Error()}
	}
	return filepath.Join(home, ".local", "state", "colab"), nil
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}
