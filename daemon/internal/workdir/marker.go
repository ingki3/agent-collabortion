// marker.go — the workdir's own name tag: `<path>/.colab-workdir.json`, one
// line, the server's `workdir` row id (daemon-protocol v0.8.3 §4.1·§6, K-14).
//
// Why a file INSIDE the workdir, when index.go went to some length to keep
// its records out of the checkout. The index existed to recover the
// (session uuid, agent uuid) pair from a path the server had named with
// SLUGS — the daemon's memory of what the bundle said, keyed by a hash of the
// path and kept under `<root>/.colab/workdirs/`. v0.8.3 makes that recovery
// unnecessary: the bundle carries the row's id, the §6 report echoes it, and
// the server finds the row by id. What the daemon still has to survive is a
// RESTART — the probe's full report has to name every directory on disk, and
// a restarted daemon has no bundle in hand. One line in the directory itself
// answers that, and it goes wherever the directory goes: a checkout the
// operator moves by hand keeps its name tag, where a hash-of-path record
// would have gone stale; a directory `gc` removes takes the tag with it, so
// there is nothing to forget.
//
// Hygiene (COMPONENTS §8.4, E13-03~06): in a checkout the tag is registered in
// `.git/info/exclude` — never `.gitignore`, which belongs to the repository —
// so `git status` stays clean and no commit of the agent's picks it up. The
// entry is repository-wide and permanent (unlike the brief's, it is not
// released at lane end: the tag outlives the lane by design), which is fine —
// it hides exactly one file name that only this daemon writes.
//
// The index is kept for ONE case only: a bundle with no `workdir.id`, i.e. an
// older server, or (Lead T-S21 decision A) the FIRST attempt of a `dir` lane,
// whose row the server cannot make before the daemon has named the path.
// Retire it — index.go and the `RecordWorkdir` calls in Prepare/PrepareWorktree
// — once every paired server sends v0.8.3 bundles and no `dir` lane predates
// T-S21 (first release after v1.1; check `ls <root>/.colab/workdirs/` is empty
// on the fleet).
package workdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

// MarkerName is the name tag's file name inside the workdir.
const MarkerName = ".colab-workdir.json"

// marker is the file's one line. Only `id` is in the contract ("한 줄(id 만)");
// nothing else is read back, and nothing else should be added — the file is
// the daemon's, but it sits in the agent's directory, and every extra byte is
// something an agent may read and reason about.
//
// daemon-protocol v0.10.0 §6 adds `work_id?`·`role?` ("데몬은 번들에서 받은 값을
// `.colab-workdir.json` 표식에 함께 적어 두고 그대로 회신한다") — they are what S13
// groups by and what the §6 report echoes after a restart.
type marker struct {
	ID     string `json:"id"`
	WorkID string `json:"work_id,omitempty"`
	Role   string `json:"role,omitempty"`
}

// WriteMarker writes the tag. Written on every preparation, not only the
// first, so a tag lost to a half-written disk heals on the next attempt and a
// row the server re-keyed (it never does today, but a stale id must not stick)
// follows the bundle. Write-then-rename: a probe listing the directory while
// an attempt prepares must never read half a line.
func WriteMarker(path, id string) error {
	return writeMarker(path, marker{ID: id})
}

// WriteMarkerFor writes the tag with the v0.10.0 fields.
func WriteMarkerFor(path, id, workID, role string) error {
	return writeMarker(path, marker{ID: id, WorkID: workID, Role: role})
}

func writeMarker(path string, m marker) error {
	if path == "" || m.ID == "" {
		return errors.New("workdir: marker needs a path and an id")
	}
	blob, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(path, MarkerName+".tmp")
	if err := os.WriteFile(tmp, append(blob, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(path, MarkerName))
}

// ReadMarker returns the tag's id, or "" when the directory has none (an
// older daemon prepared it, or the bundle had no id).
func ReadMarker(path string) string { return readMarker(path).ID }

func readMarker(path string) marker {
	if path == "" {
		return marker{}
	}
	blob, err := os.ReadFile(filepath.Join(path, MarkerName))
	if err != nil {
		return marker{}
	}
	var m marker
	if json.Unmarshal(blob, &m) != nil {
		return marker{}
	}
	return m
}

// tag is what Prepare and PrepareWorktree do once the directory exists: the
// name tag when the bundle carries an id (v0.8.3), the index record when it
// does not (the fallback the file comment above explains). The two are
// exclusive on purpose — a directory that has a tag has no business in the
// index, and forgetting the record here is what empties the index directory
// on a fleet that has moved to v0.8.3 bundles.
func tag(root, abs, id string, rec Record) error {
	return tagWith(root, abs, id, "", "", rec)
}

// tagWith is tag with the v0.10.0 marker fields (work_id, role).
func tagWith(root, abs, id, workID, role string, rec Record) error {
	if id == "" {
		if root == "" {
			return nil
		}
		if err := RecordWorkdir(root, rec); err != nil {
			return fmt.Errorf("workdir index: %w", err)
		}
		return nil
	}
	if err := WriteMarkerFor(abs, id, workID, role); err != nil {
		return fmt.Errorf("workdir marker: %w", err)
	}
	if gitrepo.IsRepo(abs) {
		// E13-03~06: the tag must not show in `git status` or land in a
		// commit. Loud on failure, like the brief's exclude — a dirty tree
		// trips the server's E13-13 judgement later and the reason would be
		// invisible from the feed.
		if err := gitrepo.ExcludeEnsure(abs, MarkerName); err != nil {
			return fmt.Errorf("workdir marker: register %s in .git/info/exclude: %w", MarkerName, err)
		}
	}
	ForgetWorkdir(root, abs)
	return nil
}
