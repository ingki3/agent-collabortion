package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var seqMu sync.Mutex

// NextSeq returns the next client_seq for (task, attempt). Every CLI
// invocation is a fresh process, so the counter is persisted under the state
// dir (COLAB_STATE_DIR → $XDG_STATE_HOME/colab → ~/.local/state/colab).
// COLAB_CLIENT_SEQ / Config.ClientSeq forces the value (retries, tests).
func (c *Client) NextSeq(taskID string, attempt int) (int, error) {
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
	f := filepath.Join(dir, fmt.Sprintf("seq-%s-%d", sanitize(taskID), attempt))
	n := 0
	if raw, err := os.ReadFile(f); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	n++
	tmp := f + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(n)), 0o600); err != nil {
		return 0, &Error{Exit: ExitUnreachable, Code: "state_dir", Title: "cannot persist client_seq", Detail: err.Error()}
	}
	if err := os.Rename(tmp, f); err != nil {
		return 0, &Error{Exit: ExitUnreachable, Code: "state_dir", Title: "cannot persist client_seq", Detail: err.Error()}
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
