package brief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
)

func parts(ctx, log string) Parts {
	return Parts{Identity: "Lead: coordinates", Rules: "@mention; colab message post", Coordination: "lead protocol", Session: "goal: ship", Roster: "@Lead @Dev", Context: ctx, DecisionLog: log}
}

// E12-11 — order fixed, [1]~[5] byte-identical across turns.
func TestAssembleOrderAndStablePrefix(t *testing.T) {
	a := Assemble(parts("turn1 ctx", "d1"))
	b := Assemble(parts("turn2 ctx (attachments changed)", "d1\nd2"))
	if StablePrefix(a) != StablePrefix(b) {
		t.Fatal("[1]~[5] differ between turns")
	}
	if a == b {
		t.Fatal("[6]~[8] should differ")
	}
	last := -1
	for _, h := range headers {
		i := strings.Index(a, "## "+h)
		if i < 0 || i < last {
			t.Fatalf("header %q out of order (at %d after %d)", h, i, last)
		}
		last = i
	}
	if !strings.Contains(a, DefaultPrecedence) {
		t.Fatal("default precedence missing")
	}
	nonLead := Assemble(Parts{Identity: "x"})
	if strings.Contains(nonLead, "[3]") {
		t.Fatal("[3] rendered for non-lead")
	}
}

func TestPrepareMetaWritesNothing(t *testing.T) {
	dir := t.TempDir()
	p, err := Prepare(dir, contracts.BriefACPMetaSystemPrompt, "brief")
	if err != nil || p.Path != "" {
		t.Fatalf("%+v %v", p, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("files written: %v", entries)
	}
}

// CHANGED FOR §8.4 v0.16 (spike 5, PR #153). The two tests that stood here
// measured the v0.15 mechanism: a marker block APPENDED to the repository's
// own AGENTS.md, and a Remove that stripped the block while keeping the
// original text. Both behaviours are now defects — the contract says the
// repository's instruction file is neither read nor written, and our brief is
// a separate untracked file that is simply deleted (E13-03~07). P2_TASKS
// §0-14: ordinary unit expectations move with the fix; the golden table that
// pins the new behaviour is p4_brief_golden_test.go.
func TestPrepareInstructionFileWritesOurOwnFile(t *testing.T) {
	dir := t.TempDir()
	p, err := Prepare(dir, contracts.BriefInstructionFile, "brief v1")
	if err != nil || p.Overwrote {
		t.Fatalf("%+v %v", p, err)
	}
	if filepath.Base(p.Path) != "COLAB_BRIEF.md" {
		t.Fatalf("brief written to %q, want COLAB_BRIEF.md (harness §10 v0.8.6)", p.Path)
	}
	got, _ := os.ReadFile(filepath.Join(dir, FileName))
	if string(got) != MarkerStart+"\nbrief v1\n"+MarkerEnd+"\n" {
		t.Fatalf("content %q", got)
	}
	// A resumed lane REPLACES the stale brief — exactly one block, no v1.
	p2, _ := Prepare(dir, contracts.BriefInstructionFile, "brief v2")
	if !p2.Overwrote {
		t.Error("the second Prepare did not report an overwrite")
	}
	got, _ = os.ReadFile(p2.Path)
	if strings.Count(string(got), MarkerStart) != 1 || !strings.Contains(string(got), "brief v2") || strings.Contains(string(got), "brief v1") {
		t.Fatalf("content %q", got)
	}
	if err := Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Path); !os.IsNotExist(err) {
		t.Fatal("our brief file survived the lane")
	}
}

// §8.4 M3 v0.16 / E13-03·04·05: the repository's own instruction files are
// not opened, not created, and not restored over.
func TestPrepareLeavesTheRepositoryInstructionFileAlone(t *testing.T) {
	for _, name := range InstructionFileNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			original := "# Project rules\nkeep me\n"
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := Prepare(dir, contracts.BriefInstructionFile, "brief")
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(path); string(got) != original {
				t.Fatalf("%s changed to %q", name, got)
			}
			// The agent edits it as part of its actual work.
			edited := original + "agent added\n"
			if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Remove(p); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(path); string(got) != edited {
				t.Fatalf("Remove ran over the agent's edit: %q", got)
			}
			if _, err := os.Stat(p.Path); !os.IsNotExist(err) {
				t.Fatal("our brief file survived the lane")
			}
		})
	}
}

// E13-04: the file is not created when the repository has none.
func TestPrepareNeverCreatesTheRepositoryInstructionFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Prepare(dir, contracts.BriefInstructionFile, "brief"); err != nil {
		t.Fatal(err)
	}
	for _, name := range InstructionFileNames {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was created", name)
		}
	}
}

// E13-06a: the pointer line names the ABSOLUTE path.
func TestTurnPromptPointer(t *testing.T) {
	line := TurnPromptPointer("/srv/wd/S1/R")
	if !strings.Contains(line, "/srv/wd/S1/R/COLAB_BRIEF.md") {
		t.Fatalf("pointer %q", line)
	}
	full := PrependPointer("/srv/wd/S1/R", "turn body")
	if !strings.HasPrefix(full, line) || !strings.HasSuffix(full, "turn body") {
		t.Fatalf("prepended prompt %q", full)
	}
}

// PRD §8.4 / harness §10 — [6] context, [7] decision log and [8] precedence
// come from the server as TaskBundle.brief.text. The daemon DELIVERS that
// text; it never composes, reorders or completes it — a brief the daemon
// improvised would differ from the one the server thinks the agent read.
func TestPrepareDeliversServerTextVerbatim(t *testing.T) {
	dir := t.TempDir()
	// Deliberately missing [8]: the daemon must NOT fill it in on this path
	// (DefaultPrecedence belongs to Assemble, which the server-fed path
	// never calls).
	text := "## [1] Agent Identity\n\nWriter\n\n## [6] Context\n\nprev session: chose Postgres\n\n## [7] Decision Log\n\nD-3 approved 2026-09-02\n"
	p, err := Prepare(dir, contracts.BriefInstructionFile, text)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p.Path)
	if err != nil {
		t.Fatal(err)
	}
	inner, ok := between(string(b), MarkerStart, MarkerEnd)
	if !ok {
		t.Fatalf("no marker block in %q", b)
	}
	if strings.TrimSpace(inner) != strings.TrimSpace(text) {
		t.Fatalf("brief was rewritten:\n got %q\nwant %q", inner, text)
	}
	if strings.Contains(string(b), DefaultPrecedence) {
		t.Fatal("the daemon invented an [8] section the server did not send")
	}
	// The _meta transport keeps the same text and writes nothing at all.
	m, err := Prepare(dir, contracts.BriefACPMetaSystemPrompt, text)
	if err != nil || m.Path != "" {
		t.Fatalf("meta transport wrote %q (%v)", m.Path, err)
	}
}

func between(s, start, end string) (string, bool) {
	i := strings.Index(s, start)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}
