// Package brief assembles the stable brief [1]~[8] (PRD §8.4) and delivers
// it to the runtime by ONE transport (harness §10, E12-09):
//
//	acp_meta_system_prompt (claude_code): nothing on disk — the harness puts
//	    the text in _meta.systemPrompt.append on session/new and session/load.
//	instruction_file (hermes): an UNTRACKED <workdir>/COLAB_BRIEF.md holding
//	    the whole brief in a marker block, registered in .git/info/exclude,
//	    with the turn prompt's first line pointing at its absolute path.
//
// THE REPOSITORY'S OWN INSTRUCTION FILE IS NEITHER READ NOR WRITTEN
// (§8.4 M3 v0.16, harness §10 v0.8.6, E13-03~07). Until v0.15 the contract
// said to append a marker block to the tracked AGENTS.md and hide it with
// `git update-index --skip-worktree`. Spike 5 (plan/spikes/SPIKE_05.md) ran
// that 12 times against two real runtimes and it lost data every time: the
// agent edits the file (12/12) and then `git add -A && git commit` answers
// `nothing to commit, working tree clean` and makes NO commit (0/12), while
// `git status` stays clean so nobody can see it; `git switch` and `git merge`
// then abort with "your local changes would be overwritten" and there is no
// command the agent can run to get out. P4's whole scenario is an agent
// committing to `colab/<session>/<agent>`, so the mechanism and the goal were
// in direct conflict.
//
// What replaces it costs one tool call per turn (the agent reads our file)
// and leaves the checkout genuinely clean — spike 5 §6.3 measured commit
// success 4/4, brief-in-commit 0/4, `git status` clean 4/4.
//
// In P1 the server sends TaskBundle.brief.text ready-made; Assemble exists
// for tests (E12-11) and for the server-less smoke path.
package brief

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

// Marker block delimiters (harness §10).
const (
	MarkerStart = "<!-- colab:brief:start -->"
	MarkerEnd   = "<!-- colab:brief:end -->"
	// FileName is OUR file. It is deliberately not AGENTS.md/CLAUDE.md: the
	// repository owns those names (§8.4 M3 v0.16).
	FileName = "COLAB_BRIEF.md"
)

// InstructionFileNames are the repository's own instruction files. They are
// listed here only so the golden mirror can assert we never opened one —
// nothing in this package reads or writes them.
var InstructionFileNames = []string{"AGENTS.md", "CLAUDE.md"}

// Parts are the eight sections. [1]~[5] must be byte-identical between two
// turns of the same session (cache friendliness, E12-11); [6]~[8] may vary.
type Parts struct {
	Identity     string // [1] agent identity + instructions
	Rules        string // [2] workspace rules + mention syntax + colab CLI/MCP conventions
	Coordination string // [3] lead only
	Session      string // [4] goal / acceptance_criteria / exit condition / Director / isolation
	Roster       string // [5] participants
	Context      string // [6] attachments / previous session summary
	DecisionLog  string // [7]
	Precedence   string // [8] instruction precedence; empty → DefaultPrecedence
}

// DefaultPrecedence is [8] when the server gives none.
const DefaultPrecedence = "User instructions > session goal > agent instructions > runtime defaults."

var headers = [8]string{
	"[1] Agent Identity",
	"[2] Workspace Rules",
	"[3] Coordination Protocol",
	"[4] Session",
	"[5] Roster",
	"[6] Context",
	"[7] Decision Log",
	"[8] Instruction Precedence",
}

// Assemble renders the brief in the fixed [1]~[8] order. Empty [3] (non-lead)
// is omitted; every other section is always present so offsets are stable.
func Assemble(p Parts) string {
	if p.Precedence == "" {
		p.Precedence = DefaultPrecedence
	}
	bodies := [8]string{p.Identity, p.Rules, p.Coordination, p.Session, p.Roster, p.Context, p.DecisionLog, p.Precedence}
	var b strings.Builder
	for i, h := range headers {
		if i == 2 && bodies[i] == "" {
			continue
		}
		b.WriteString("## ")
		b.WriteString(h)
		b.WriteString("\n\n")
		b.WriteString(strings.TrimRight(bodies[i], "\n"))
		b.WriteString("\n\n")
	}
	return b.String()
}

// StablePrefix returns the [1]~[5] part of an assembled brief (up to the
// "[6] Context" header) — the bytes that must not change within a session.
func StablePrefix(assembled string) string {
	if i := strings.Index(assembled, "## "+headers[5]); i >= 0 {
		return assembled[:i]
	}
	return assembled
}

// Prepared records what Prepare did so Remove can undo it.
type Prepared struct {
	Transport contracts.BriefTransport
	// Path is OUR brief file ("" for the _meta transport).
	Path string
	// Workdir is the directory Path lives in — Remove needs it to reach the
	// repository after the file is gone.
	Workdir string
	// Excluded is true when the path was registered in .git/info/exclude, so
	// Remove knows to unregister it (E13-06). False for a workdir that is not
	// a git working tree (`none` isolation with a plain folder).
	Excluded bool
	// Overwrote is true when a previous attempt's brief file was replaced
	// (재개·재시도). Appending would hand the agent two briefs and break the
	// [1]~[5] byte identity of E12-11.
	Overwrote bool
}

// PromptPointerPrefix is the fixed opening of the instruction_file turn
// prompt's first line (§8.4 턴 프롬프트 v0.16, E13-06a).
const PromptPointerPrefix = "먼저 "

// PromptPointerSuffix closes it.
const PromptPointerSuffix = " 를 읽어라. 이 세션의 브리프 전문이 그 파일에 있다."

// TurnPromptPointer is the line that goes at the very FRONT of a hermes turn
// prompt. The path is absolute on purpose: spike 5 §6.3 saw the agent's first
// tool call be that read 4/4, and a relative path breaks the moment the
// runtime's cwd is not the workdir.
//
// The daemon builds this, not the server, for the same reason it rewrites the
// CLI wrapper path (harness §10 v0.8.1): the server does not know where this
// machine put the workdir.
func TurnPromptPointer(workdirAbs string) string {
	return PromptPointerPrefix + filepath.Join(workdirAbs, FileName) + PromptPointerSuffix
}

// PrependPointer puts the pointer line in front of the server's turn prompt,
// separated by a blank line. An empty prompt still gets the pointer — a turn
// with no instructions is a bug elsewhere, and dropping the brief on top of
// it would hide which one.
func PrependPointer(workdirAbs, prompt string) string {
	return TurnPromptPointer(workdirAbs) + "\n\n" + prompt
}

// gitOps is the small slice of gitrepo Prepare/Remove use, kept behind a
// variable so the golden mirror can prove the repository's own instruction
// file is never opened without stubbing out git itself.
var (
	isRepo         = gitrepo.IsRepo
	excludeEnsure  = gitrepo.ExcludeEnsure
	excludeRelease = gitrepo.ExcludeRelease
	worktrees      = gitrepo.Worktrees
)

// Prepare delivers the brief for the given transport.
//
//   - acp_meta_system_prompt writes nothing (the text travels in _meta).
//   - instruction_file writes the whole brief, wrapped in the marker block, to
//     <workdir>/COLAB_BRIEF.md and registers that name in .git/info/exclude.
//
// It does not look at AGENTS.md or CLAUDE.md, and it does not create them.
// Under 우회 B the original state of the repository is irrelevant, which is
// the property E13-04 measures: one plan, every row.
func Prepare(workdir string, transport contracts.BriefTransport, text string) (Prepared, error) {
	switch transport {
	case contracts.BriefACPMetaSystemPrompt:
		return Prepared{Transport: transport}, nil
	case contracts.BriefInstructionFile:
		path := filepath.Join(workdir, FileName)
		_, statErr := os.Stat(path)
		existed := statErr == nil
		// Truncating write, never an append: a resumed lane replaces the
		// stale brief (E13-06 "a resumed lane overwrites").
		if err := os.WriteFile(path, []byte(Block(text)), 0o644); err != nil {
			return Prepared{}, err
		}
		p := Prepared{Transport: transport, Path: path, Workdir: workdir, Overwrote: existed}
		if isRepo(workdir) {
			if err := excludeEnsure(workdir, FileName); err != nil {
				// The brief itself is delivered; failing the attempt over the
				// hiding step would trade a dirty `git status` for no work at
				// all. It is loud, not silent.
				return p, fmt.Errorf("brief: register %s in .git/info/exclude: %w", FileName, err)
			}
			p.Excluded = true
		}
		return p, nil
	}
	return Prepared{}, fmt.Errorf("brief: unknown transport %q", transport)
}

// Block renders the marker block.
func Block(text string) string {
	return MarkerStart + "\n" + strings.TrimRight(text, "\n") + "\n" + MarkerEnd + "\n"
}

// HasMarkers reports whether b is wrapped in the marker block.
func HasMarkers(b []byte) bool {
	return bytes.Contains(b, []byte(MarkerStart)) && bytes.Contains(b, []byte(MarkerEnd))
}

// Remove deletes our brief file and unregisters the exclude entry (E13-06).
// There is nothing to restore: we never wrote a file the repository owns.
//
// The exclude entry is repository-WIDE (gitrepo.CommonDir), so it is only
// unregistered once the last sibling worktree's brief file is gone — pulling
// it while a parallel lane is still running would make that lane's brief show
// up as an untracked change in the checkout it is committing from.
func Remove(p Prepared) error {
	if p.Path == "" {
		return nil
	}
	if err := os.Remove(p.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !p.Excluded {
		return nil
	}
	return excludeRelease(p.Workdir, FileName, func() bool { return siblingBriefExists(p.Workdir) })
}

// siblingBriefExists reports whether any OTHER working tree of the same
// repository still holds a brief file.
func siblingBriefExists(workdir string) bool {
	wts, err := worktrees(workdir)
	if err != nil {
		return false
	}
	self, _ := filepath.Abs(workdir)
	for _, w := range wts {
		if abs, _ := filepath.Abs(w.Path); abs == self {
			continue
		}
		if _, err := os.Stat(filepath.Join(w.Path, FileName)); err == nil {
			return true
		}
	}
	return false
}
