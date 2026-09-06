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

func TestPrepareInstructionFileMarkerBlock(t *testing.T) {
	dir := t.TempDir()
	p, err := Prepare(dir, contracts.BriefInstructionFile, "brief v1")
	if err != nil || !p.Created {
		t.Fatalf("%+v %v", p, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, FileName))
	if string(got) != MarkerStart+"\nbrief v1\n"+MarkerEnd+"\n" {
		t.Fatalf("content %q", got)
	}
	// re-prepare replaces the block, exactly one block (no duplicate injection)
	p2, _ := Prepare(dir, contracts.BriefInstructionFile, "brief v2")
	got, _ = os.ReadFile(p2.Path)
	if strings.Count(string(got), MarkerStart) != 1 || !strings.Contains(string(got), "brief v2") || strings.Contains(string(got), "brief v1") {
		t.Fatalf("content %q", got)
	}
	if err := Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Path); !os.IsNotExist(err) {
		t.Fatal("created file not removed at lane end")
	}
}

func TestPrepareAppendsToExistingAndRemoveKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	os.WriteFile(path, []byte("# Project rules\nkeep me\n"), 0o644)
	p, err := Prepare(dir, contracts.BriefInstructionFile, "brief")
	if err != nil || p.Created {
		t.Fatalf("%+v %v", p, err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), "# Project rules\nkeep me\n") || !strings.Contains(string(got), MarkerStart) {
		t.Fatalf("content %q", got)
	}
	// the agent edits the file legitimately; Remove keeps that edit
	os.WriteFile(path, append(got, []byte("agent added\n")...), 0o644)
	if err := Remove(p); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "# Project rules\nkeep me\nagent added\n" {
		t.Fatalf("after remove %q", got)
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
