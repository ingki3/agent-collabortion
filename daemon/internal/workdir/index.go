// index.go is the daemon's one piece of durable bookkeeping about a workdir:
// WHICH SESSION AND WHICH AGENT it belongs to (daemon-protocol §6 v0.7.3).
//
// Why a file on disk and not the directory name. The §6 report has to carry
// the session's **uuid** and, under `worktree` isolation, the **agent_id** —
// without that pair the server cannot find the row and skips it silently, so
// the git facts that FR-6.4 M4 judges (미병합·미커밋) never arrive and GC
// deletes work (T-I4 차단 ②, measured: three checkouts reported,
// `merged=false · commits_ahead=0 · dirty=false · bytes=0` stored). The
// directory name cannot supply either id: the SERVER names the path and it
// names it with SLUGS (`<session-slug>/<agent-slug>`, daemon-protocol §4.1),
// and the daemon has never had a uuid ↔ path map — §4.3 makes the server
// carry `{id, path}` in the `gc` command for exactly that reason.
//
// So the daemon writes down what the bundle told it, once per preparation,
// and reads it back when it reports. The file lives under
// `<root>/.colab/workdirs/` — beside the orphan records of §5, never inside
// the checkout, which would put it in the user's `git status`.
package workdir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Record is one prepared workdir's identity, as the bundle stated it.
type Record struct {
	// Kind is the isolation the bundle asked for ("dir" or "worktree").
	Kind string `json:"kind"`
	// Path is the absolute path on this machine.
	Path string `json:"path"`
	// SessionID is the session UUID (§6: "슬러그·디렉터리 이름이 아니다").
	SessionID string `json:"session_id"`
	// AgentID is the agent UUID. Mandatory for `worktree` (§6) — that
	// isolation binds one workdir per AGENT, so the row is not identified
	// without it.
	AgentID string `json:"agent_id,omitempty"`
	// AgentName is kept for humans reading the file; nothing is matched on it.
	AgentName string `json:"agent_name,omitempty"`
	// LaneID identifies a `dir` workdir (one per lane). A `worktree` belongs
	// to the agent and outlives any one lane, so it is left empty there
	// rather than pinned to whichever lane happened to prepare it.
	LaneID string `json:"lane_id,omitempty"`
	// Branch is the checkout's branch at preparation time (worktree only).
	Branch string `json:"branch,omitempty"`
	// UpdatedAt is when this record was last written.
	UpdatedAt time.Time `json:"updated_at"`
}

// IndexDir is where the records live.
func IndexDir(root string) string { return filepath.Join(root, ".colab", "workdirs") }

// key names a record file after the path it describes: the daemon looks a
// record up by path (that is the only handle the server's `gc` command and
// the disk scan both have), and a hash keeps the name flat and filesystem-safe.
func key(path string) string {
	sum := sha256.Sum256([]byte(absClean(path)))
	return hex.EncodeToString(sum[:])[:16]
}

func absClean(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// RecordWorkdir writes (or overwrites) one record. It is called on every
// preparation, not only the first, so a record lost to a half-written disk
// heals on the next attempt.
func RecordWorkdir(root string, rec Record) error {
	if root == "" || rec.Path == "" {
		return errors.New("workdir: index needs a root and a path")
	}
	rec.Path = absClean(rec.Path)
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}
	dir := IndexDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	blob, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	// Write-then-rename: a probe reading the index while an attempt prepares
	// must never see half a record.
	tmp := filepath.Join(dir, key(rec.Path)+".tmp")
	if err := os.WriteFile(tmp, append(blob, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, key(rec.Path)+".json"))
}

// LookupWorkdir returns the record for one path.
func LookupWorkdir(root, path string) (Record, bool) {
	if root == "" || path == "" {
		return Record{}, false
	}
	blob, err := os.ReadFile(filepath.Join(IndexDir(root), key(path)+".json"))
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if json.Unmarshal(blob, &rec) != nil || rec.Path == "" {
		return Record{}, false
	}
	return rec, true
}

// LoadIndex reads every record, keyed by absolute path.
func LoadIndex(root string) map[string]Record {
	out := map[string]Record{}
	if root == "" {
		return out
	}
	entries, err := os.ReadDir(IndexDir(root))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(IndexDir(root), e.Name()))
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(blob, &rec) != nil || rec.Path == "" {
			continue
		}
		out[absClean(rec.Path)] = rec
	}
	return out
}

// ForgetWorkdir drops the record of a workdir the daemon has collected. It is
// best effort: a leftover record only ever describes a path the disk scan no
// longer finds, and those are skipped.
func ForgetWorkdir(root, path string) {
	if root == "" || path == "" {
		return
	}
	_ = os.Remove(filepath.Join(IndexDir(root), key(path)+".json"))
}

// apply copies a record's identity onto a report row. The row's own values
// win only where the record has nothing — the record is what the SERVER told
// the daemon, and a directory name is a guess.
func (i *Info) apply(rec Record) {
	if rec.Kind != "" {
		i.Kind = rec.Kind
	}
	if rec.SessionID != "" {
		i.SessionID = rec.SessionID
	}
	if rec.AgentID != "" {
		i.AgentID = rec.AgentID
	}
	if rec.LaneID != "" {
		i.LaneID = rec.LaneID
	}
}

// Describe builds one §6 report row for a path the server named (a `gc`
// command carries paths, §4.3). Everything the row needs is either in the
// index or measurable on disk — including `git` and `bytes`, which §6 v0.7.3
// requires on EVERY report because they are the only input GC judges from.
func Describe(root, path, sessionID string) Info {
	abs := absClean(path)
	info := Info{Kind: "dir", Path: abs, SessionID: sessionID}
	if rec, ok := LookupWorkdir(root, abs); ok {
		info.apply(rec)
		if sessionID != "" {
			// The command's session_id is the server's own; keep it.
			info.SessionID = sessionID
		}
	}
	if IsWorktree(abs) {
		info.Kind = "worktree"
		info.Git = Git(abs)
	}
	info.Bytes, info.LastUsedAt = DiskUsage(abs)
	return info
}
