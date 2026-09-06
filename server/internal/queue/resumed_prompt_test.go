package queue

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// TestResumedSectionCarriesTheWorkdirCheck pins E8-04's only mechanism.
//
// E8-04 (4) — "파일 편집 중복 적용 0" after a daemon kill — has no server-side
// enforcement anywhere: attempt 2 runs in the same workdir, and nothing on
// either side inspects the files. The whole defence is two sentences in the
// `<resumed>` block: the list of messages already posted, and the instruction
// to read the workdir first and not to redo an edit that is already there.
// The p4 sim's 100-round row measures exactly the failure this prevents (an
// agent that appends `<edit-1>` a second time), so if these sentences ever go
// missing the row goes red and nobody can tell why from the diff.
//
// The golden table pins the BOOLEAN (AttemptPlan.WorkdirCheckInstruction). This
// pins the text it renders to.
func TestResumedSectionCarriesTheWorkdirCheck(t *testing.T) {
	m1, m2 := uuid.NewString(), uuid.NewString()
	plan := tasks.AttemptPlan{HasResumedSection: true, WorkdirCheckInstruction: true}

	var b strings.Builder
	renderResumedSection(&b, plan, 2, "runtime_offline", []string{m1, m2}, nil)
	got := b.String()

	for _, want := range []string{
		"<resumed attempt=2>",
		"Your previous attempt (1) was interrupted: runtime_offline.",
		"Messages you already posted (do not post them again):",
		"- " + m1,
		"- " + m2,
		"inspect the current state of the workdir (changed files, `git status`)",
		"do NOT make an edit again if it is already in the file",
		"</resumed>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("`<resumed>` is missing %q — E8-04 (2)·(4) have no other mechanism.\n--- prompt ---\n%s", want, got)
		}
	}
}

// TestResumedSectionIsAbsentForAReInstruction is FR-3.4 (B) / E8-06 seen from
// the renderer: "중단하고 다시 지시" makes a NEW task whose plan has no
// `<resumed>`, and the block must then contribute nothing at all — a leftover
// "continue where you left off" is precisely the bug B was written to stop.
func TestResumedSectionIsAbsentForAReInstruction(t *testing.T) {
	var b strings.Builder
	renderResumedSection(&b, tasks.AttemptPlan{HasResumedSection: false, WorkdirCheckInstruction: false},
		1, "cancelled", []string{uuid.NewString()}, nil)
	if b.String() != "" {
		t.Errorf("a re-instruction rendered a resumed block:\n%s", b.String())
	}
}

// TestResumedSectionColdStartSaysSo keeps FR-5.4 step 2's sentence next to the
// workdir check: a cold turn that does not say it is cold reads to the agent
// like a continuation of a conversation it no longer has.
func TestResumedSectionColdStartSaysSo(t *testing.T) {
	var b strings.Builder
	renderResumedSection(&b, tasks.AttemptPlan{HasResumedSection: true, ColdStart: true,
		WorkdirCheckInstruction: true}, 3, "failed", nil, nil)
	got := b.String()
	if !strings.Contains(got, "starts cold") {
		t.Errorf("cold start not announced:\n%s", got)
	}
	if !strings.Contains(got, "do NOT make an edit again") {
		t.Errorf("cold start dropped the workdir instruction:\n%s", got)
	}
}
