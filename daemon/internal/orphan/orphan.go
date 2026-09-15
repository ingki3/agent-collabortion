// Package orphan implements FR-9.1 / daemon-protocol §5: every spawned
// runtime process group is recorded on disk as
// <workdir_root>/.colab/attempts/<task_id>.<attempt>.json {pgid, started_at};
// the record is deleted on normal exit (E11-01·02), and on daemon start —
// before the first claim — surviving groups are SIGTERM/SIGKILLed (E11-05).
package orphan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Record is the on-disk shape.
type Record struct {
	TaskID    string    `json:"task_id"`
	Attempt   int       `json:"attempt"`
	PGID      int       `json:"pgid"`
	StartedAt time.Time `json:"started_at"`
	Workdir   string    `json:"workdir,omitempty"`
}

// Store manages the records under one workdir root.
type Store struct {
	Root string
	// KillAfter is the SIGTERM → SIGKILL grace for Sweep. Zero → 10s.
	KillAfter time.Duration
}

func (s Store) dir() string { return filepath.Join(s.Root, ".colab", "attempts") }

func (s Store) path(taskID string, attempt int) string {
	return filepath.Join(s.dir(), fmt.Sprintf("%s.%d.json", taskID, attempt))
}

// Record writes the pgid file (E11-01).
func (s Store) Record(r Record) error {
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := s.path(r.TaskID, r.Attempt) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(r.TaskID, r.Attempt))
}

// Remove deletes the record (E11-02). Missing is not an error.
func (s Store) Remove(taskID string, attempt int) error {
	err := os.Remove(s.path(taskID, attempt))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// List reads every record.
func (s Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.dir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir(), e.Name()))
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) != nil || r.PGID <= 0 {
			// unreadable record: drop it so it cannot block claims forever
			_ = os.Remove(filepath.Join(s.dir(), e.Name()))
			continue
		}
		if r.TaskID == "" {
			base := strings.TrimSuffix(e.Name(), ".json")
			if i := strings.LastIndexByte(base, '.'); i > 0 {
				r.TaskID = base[:i]
				r.Attempt, _ = strconv.Atoi(base[i+1:])
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Swept describes one cleaned record.
type Swept struct {
	Record Record
	Alive  bool // the group still existed and was signalled
	Killed bool // needed SIGKILL
}

// Alive reports whether any process in the group exists.
func Alive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

// Sweep terminates every recorded process group that is still alive
// (SIGTERM → KillAfter → SIGKILL) and deletes all records. Must run before
// the first claim (E11-05).
func (s Store) Sweep() ([]Swept, error) {
	recs, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []Swept
	for _, r := range recs {
		sw := Swept{Record: r}
		if Alive(r.PGID) {
			sw.Alive = true
			sw.Killed = Kill(r.PGID, s.grace())
		}
		_ = s.Remove(r.TaskID, r.Attempt)
		out = append(out, sw)
	}
	return out, nil
}

func (s Store) grace() time.Duration {
	if s.KillAfter > 0 {
		return s.KillAfter
	}
	return 10 * time.Second
}

// Kill sends SIGTERM to the group, waits up to grace, then SIGKILL. Returns
// true when SIGKILL was needed.
func Kill(pgid int, grace time.Duration) bool {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !Alive(pgid) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && Alive(pgid) {
		time.Sleep(20 * time.Millisecond)
	}
	return true
}
