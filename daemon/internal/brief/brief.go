// Package brief assembles the stable brief [1]~[8] (PRD §8.4) and delivers
// it to the runtime by ONE transport (harness §10, E12-09):
//
//	acp_meta_system_prompt (claude_code): nothing on disk — the harness puts
//	    the text in _meta.systemPrompt.append on session/new and session/load.
//	instruction_file (hermes): AGENTS.md marker block in the workdir.
//
// In P1 the server sends TaskBundle.brief.text ready-made; Assemble exists
// for tests (E12-11) and for the server-less smoke path. Tracked-file
// handling (skip-worktree / .git/info/exclude, §8.4 M3) is P4 — P1 is `none`
// isolation, so the file is simply created.
package brief

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ingki3/agent-collabortion/contracts"
)

// Marker block delimiters (harness §10).
const (
	MarkerStart = "<!-- colab:brief:start -->"
	MarkerEnd   = "<!-- colab:brief:end -->"
	FileName    = "AGENTS.md"
)

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
	// Path is the instruction file ("" for the _meta transport).
	Path string
	// Created is true when the file did not exist before.
	Created bool
}

// Prepare delivers the brief for the given transport. For
// acp_meta_system_prompt it writes nothing (the text travels in _meta).
func Prepare(workdir string, transport contracts.BriefTransport, text string) (Prepared, error) {
	switch transport {
	case contracts.BriefACPMetaSystemPrompt:
		return Prepared{Transport: transport}, nil
	case contracts.BriefInstructionFile:
		path := filepath.Join(workdir, FileName)
		created, err := writeMarkerBlock(path, text)
		if err != nil {
			return Prepared{}, err
		}
		return Prepared{Transport: transport, Path: path, Created: created}, nil
	}
	return Prepared{}, fmt.Errorf("brief: unknown transport %q", transport)
}

// Block renders the marker block.
func Block(text string) string {
	return MarkerStart + "\n" + strings.TrimRight(text, "\n") + "\n" + MarkerEnd + "\n"
}

func writeMarkerBlock(path, text string) (created bool, err error) {
	orig, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return true, os.WriteFile(path, []byte(Block(text)), 0o644)
	case err != nil:
		return false, err
	}
	stripped := stripBlock(orig)
	var out bytes.Buffer
	out.Write(stripped)
	if len(stripped) > 0 && !bytes.HasSuffix(stripped, []byte("\n")) {
		out.WriteByte('\n')
	}
	if len(stripped) > 0 {
		out.WriteByte('\n')
	}
	out.WriteString(Block(text))
	return false, os.WriteFile(path, out.Bytes(), 0o644)
}

// stripBlock removes an existing marker block (and one trailing newline).
func stripBlock(b []byte) []byte {
	s := bytes.Index(b, []byte(MarkerStart))
	if s < 0 {
		return b
	}
	e := bytes.Index(b[s:], []byte(MarkerEnd))
	if e < 0 {
		return b
	}
	end := s + e + len(MarkerEnd)
	if end < len(b) && b[end] == '\n' {
		end++
	}
	// drop the blank line we added before the block
	if s >= 1 && b[s-1] == '\n' && s >= 2 && b[s-2] == '\n' {
		s--
	}
	return append(append([]byte{}, b[:s]...), b[end:]...)
}

// Remove strips the marker block at lane end; a file we created that is now
// empty is deleted.
func Remove(p Prepared) error {
	if p.Path == "" {
		return nil
	}
	orig, err := os.ReadFile(p.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	rest := stripBlock(orig)
	if p.Created && len(bytes.TrimSpace(rest)) == 0 {
		return os.Remove(p.Path)
	}
	return os.WriteFile(p.Path, rest, 0o644)
}
