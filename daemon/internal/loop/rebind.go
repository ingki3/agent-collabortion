package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

// RebindDirName is where a rebound session's artifacts land, under the
// daemon's workdir root — NEVER inside the new checkout. The prompt tells the
// agent to apply them (FR-9.2); a diff file sitting in the working tree would
// show up in `git status` and end up in the very commit it is meant to
// produce, which is the same class of bug §8.4 M6 is about.
const RebindDirName = "rebind"

// RebindDir is <workdir_root>/.colab/rebind/<session_id>.
func RebindDir(root, sessionID string) string {
	return filepath.Join(root, ".colab", RebindDirName, safeName(sessionID))
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}

// RebindManifest is written beside the downloaded files so a human — and the
// next attempt's agent, which the prompt points at this directory — can see
// what order the server submitted them in.
type RebindManifest struct {
	SessionID   string           `json:"session_id"`
	PreparedAt  time.Time        `json:"prepared_at"`
	WorkdirRoot string           `json:"workdir_root"`
	Artifacts   []RebindArtifact `json:"artifacts"`
}

type RebindArtifact struct {
	ID    string `json:"id"`
	Order int    `json:"order"`
	URL   string `json:"url"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

// rebindPrepare executes the §4.3 `rebind_prepare {session_id, artifacts[]}`
// command: download the artifacts in submission order and leave them where
// the next attempt can find them.
//
// IT DOES NOT APPLY THEM. FR-9.2 puts the replay in the turn prompt ("이 세션의
// diff 아티팩트를 제출 순서대로 새 workdir에 적용한 뒤 이어가라", E14-06) and
// the reason is not tidiness: a `git apply` the daemon runs can conflict, and
// the daemon has no way to ask anyone what to do about it — the agent does.
//
// It does not create the checkout either. The workdir belongs to a task
// bundle (§4.1 carries `repo_path`/`branch`), and this command carries only a
// session id; the new attempt's `preparing` phase is what makes the worktree,
// and that phase report is also how the server consumes this command (§4.3).
// What the daemon can do without guessing is have the bytes on disk first,
// which is the part that fails slowly and needs the network.
func (d *Daemon) rebindPrepare(ctx context.Context, c contracts.Command) {
	if c.SessionID == "" {
		d.Log("rebind_prepare: no session_id")
		return
	}
	dir := RebindDir(d.Cfg.WorkdirRoot, c.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		d.Log("rebind_prepare %s: %v", c.SessionID, err)
		return
	}
	arts := append([]contracts.ArtifactRef(nil), c.Artifacts...)
	sort.SliceStable(arts, func(i, j int) bool { return arts[i].Order < arts[j].Order })

	m := RebindManifest{SessionID: c.SessionID, PreparedAt: d.Clock.Now().UTC(), WorkdirRoot: d.Cfg.WorkdirRoot}
	for _, a := range arts {
		row := RebindArtifact{ID: a.ID, Order: a.Order, URL: a.URL}
		dest := filepath.Join(dir, rebindFileName(a))
		dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := d.Server.Download(dctx, a.URL, dest)
		cancel()
		if err != nil {
			// One artifact that will not download does not cancel the rest:
			// the agent applies what it has and the manifest says what is
			// missing. A silent partial set would be worse than a loud one.
			row.Error = err.Error()
			d.Log("rebind_prepare %s: artifact %s: %v", c.SessionID, a.ID, err)
		} else {
			row.Path = dest
		}
		m.Artifacts = append(m.Artifacts, row)
	}
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o644)
	}
	ok := 0
	for _, a := range m.Artifacts {
		if a.Error == "" {
			ok++
		}
	}
	d.Log("rebind_prepare %s: %d/%d artifacts in %s", c.SessionID, ok, len(m.Artifacts), dir)
	// §6 report: the rebind directory is disk this runtime is now using, and
	// S13 has to be able to see it. The command itself is consumed by the new
	// attempt's `phase: preparing` (§4.3), not by this report.
	if wds, err := workdir.List(d.Cfg.WorkdirRoot); err == nil && len(wds) > 0 {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := d.Server.Workdirs(rctx, d.Cfg.RuntimeID, api.WorkdirsRequest{Workdirs: wds}); err != nil {
			d.Log("rebind_prepare %s: report: %v", c.SessionID, err)
		}
	}
}

// rebindFileName keeps the submission order visible in the file name and the
// artifact id unique, and preserves the URL's extension when it has one so a
// `.diff` stays a `.diff` for whatever the agent opens it with.
func rebindFileName(a contracts.ArtifactRef) string {
	ext := ""
	if u, err := url.Parse(a.URL); err == nil {
		ext = filepath.Ext(u.Path)
		if len(ext) > 12 || strings.ContainsAny(ext, `/\`) {
			ext = ""
		}
	}
	return fmt.Sprintf("%03d-%s%s", a.Order, safeName(a.ID), ext)
}
