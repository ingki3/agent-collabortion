// Resume defects found by spike 4c (plan/spikes/SPIKE_04c.md) — D-11, D-12,
// D-13. EVAL E8-01·02·03.
package acp_test

import (
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
)

// D-11 — the cold-start event carries the ORIGINAL rpc code and message.
//
// Spike 4c spent a full batch on the difference between the wording the
// contract quoted ("Session not found") and the one the adapter actually
// sends (-32002 "Resource not found"), and the feed said only "cold_start".
// The next time an adapter changes its mind, the answer is in the event.
func TestColdStartCarriesTheOriginalRPCError(t *testing.T) {
	b := bundle(contracts.RuntimeClaudeCode)
	b.Resume = resumeRef(contracts.RuntimeClaudeCode, "gone", "")
	f := newFixture(t, acpfake.Script{}, b, nil)
	if res := f.run(); res.ResumeOutcome != "cold_start" {
		t.Fatalf("result %+v", res)
	}
	ev := f.sink.find("runtime", "resume", "cold_start")
	if len(ev) != 1 {
		t.Fatalf("resume events %+v", f.sink.find("runtime", "resume", ""))
	}
	got, _ := ev[0].Payload["rpc_error"].(string)
	if !strings.Contains(got, "-32002") || !strings.Contains(strings.ToLower(got), "not found") {
		t.Fatalf("rpc_error = %q, want the adapter's own code and message (D-11)", got)
	}
}

// D-12 — a `sessionProvenance` object with no `acpSessionId` is missing
// provenance, not a mismatched one. Both cold start, but the REASON is what a
// person reads in the feed, and "Hermes handed us a different session" is a
// different (and wrong) story from "Hermes handed us nothing to compare".
func TestEmptyProvenanceObjectIsNoProvenance(t *testing.T) {
	b := bundle(contracts.RuntimeHermes)
	b.Resume = resumeRef(contracts.RuntimeHermes, "old", "old")
	f := newFixture(t, acpfake.Script{
		Kind: "hermes", KnownSessions: []string{"old"},
		LoadProvenance: &acpfake.Provenance{}, // `sessionProvenance: {}`
	}, b, nil)
	res := f.run()
	if res.ResumeOutcome != "cold_start" {
		t.Fatalf("resume outcome = %q, want cold_start (fail closed, §6 a′)", res.ResumeOutcome)
	}
	ev := f.sink.find("runtime", "resume", "cold_start")
	if len(ev) != 1 || ev[0].Payload["resume_reason"] != "no_provenance" {
		t.Fatalf("reason = %+v, want no_provenance (D-12)", ev)
	}
}

// D-13 — an empty `refusal` on the first turn after a resume is a second loss
// signal: cold start once, and the retry does the work.
//
// harness §2.2 keeps `refusal` a normal end (G1 F7) and this does not change
// it: the retry needs a RESUMED turn with ZERO activity, and it happens once.
func TestRefusalRightAfterResumeColdStartsOnce(t *testing.T) {
	b := bundle(contracts.RuntimeHermes)
	b.Resume = resumeRef(contracts.RuntimeHermes, "old", "old")
	f := newFixture(t, acpfake.Script{
		Kind: "hermes", KnownSessions: []string{"old"},
		Turns: []acpfake.Turn{
			{StopReason: "refusal"},                                  // the resumed turn: nothing at all
			{Steps: []acpfake.Step{{Chunk: "picked the work back"}}}, // the cold start
		},
	}, b, nil)
	res := f.run()
	if res.Outcome != "completed" {
		t.Fatalf("outcome = %q, want completed — the cold start did the work (D-13)", res.Outcome)
	}
	if res.ResumeOutcome != "cold_start" {
		t.Fatalf("resume outcome = %q, want cold_start — the attempt no longer claims it resumed", res.ResumeOutcome)
	}
	if res.Text != "picked the work back" {
		t.Fatalf("text = %q — the retry's turn is the one that counts", res.Text)
	}
	if ev := f.sink.find("runtime", "resume", "refusal_retry"); len(ev) != 1 {
		t.Fatalf("no refusal_retry event: %+v", f.sink.find("runtime", "resume", ""))
	}
	cold := f.sink.find("runtime", "resume", "cold_start")
	if len(cold) != 1 || cold[0].Payload["resume_reason"] != "refusal_after_resume" {
		t.Fatalf("cold start event %+v", cold)
	}
}

// D-13 — and if the cold start refuses too, the attempt FAILS. Reporting
// `completed` is what made the work disappear silently (spike 4c §3).
func TestRefusalTwiceFailsTheAttempt(t *testing.T) {
	b := bundle(contracts.RuntimeHermes)
	b.Resume = resumeRef(contracts.RuntimeHermes, "old", "old")
	f := newFixture(t, acpfake.Script{
		Kind: "hermes", KnownSessions: []string{"old"},
		Turns: []acpfake.Turn{{StopReason: "refusal"}}, // the last turn repeats
	}, b, nil)
	res := f.run()
	if res.Outcome != "failed" || res.Failure == nil || res.Failure.Kind != contracts.FailOther {
		t.Fatalf("result %+v — want failed(other) after the second empty refusal (D-13)", res)
	}
	if !strings.Contains(res.Failure.Detail, "refusal") {
		t.Fatalf("detail = %q, want the reason on the event (D-13)", res.Failure.Detail)
	}
	if n := len(f.sink.find("runtime", "resume", "refusal_retry")); n != 1 {
		t.Fatalf("retried %d times, want exactly 1", n)
	}
}

// The control: a refusal that is NOT after a resume stays a normal end
// (harness §2.2, G1 F7). Retrying it would re-run a runtime that declined.
func TestRefusalWithoutResumeIsStillANormalEnd(t *testing.T) {
	f := newFixture(t, acpfake.Script{Turns: []acpfake.Turn{{StopReason: "refusal"}}}, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "completed" || res.StopReason != "refusal" {
		t.Fatalf("result %+v — §2.2 keeps refusal a normal end outside the resume path", res)
	}
	if n := len(f.sink.find("runtime", "resume", "refusal_retry")); n != 0 {
		t.Fatalf("retried %d times on a cold turn", n)
	}
}
