//go:build p3golden

// Golden table for resume, retry and re-instruction (EVAL E8) — PRD FR-5.4
// (재개 모델), FR-7.1 (재시도는 처음부터 다시 하지 않는다, M5), FR-3.4 (재지시 = 새
// task, B), PRD §8.4 (턴 프롬프트·`<resumed>`), contracts/harness.md §6 and
// contracts/daemon-protocol.md §4.1·§4.4.
//
// Rows covered here: E8-01·02·06·07·08·09·10·11·12·13 (the brief's list).
// E8-03 is a daemon-side provenance check (harness §6, T-D5) and E8-04·05 are
// the partial-execution simulator, which lives in server/test/sim.
//
// WHAT THIS FILE PINS THAT IS EASY TO GET BACKWARDS
//
//   - Retry and re-instruction are DIFFERENT records. Retry keeps the task and
//     increments `attempt`; re-instruction creates a NEW task with attempt 1
//     and `restarted_from_task_id`. Folding them into one row loses the
//     trigger history and makes `<resumed>` say "you were interrupted" about
//     an instruction the human deliberately replaced (FR-3.4 B).
//   - `<resumed>` is for resume/retry ONLY. A re-instruction prompt carries
//     the new instruction and nothing else (§8.4, E8-06).
//   - Resume is attempted FIRST and cold start is the fallback, not a policy
//     choice: the same code path serves HITL answers and every retry (FR-5.4).
//   - A runtime_kind change empties `resume` — a Claude Code session id means
//     nothing to Hermes (daemon-protocol §4.4, E8-08).
//
// HOW THIS FILE FAILS TODAY. `queue.buildBundle` already builds a `<resumed>`
// section and `posted_message_ids` for attempt ≥ 2, and `tasks` already stores
// `runtime_session_ref` on finish (E8-13 is a G3 regression row). Those rows
// may pass through the adapter once wired. The re-instruction path, the
// no-alternate-profile hold, the history cap and the cold-start prompt are P3
// and fail until T-S5 lands them.
package tasks

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// caseNameP3 mirrors the P2 golden helper; the tags/packages differ so the two
// never collide, and the suffix keeps that obvious.
func caseNameP3(eval, name string) string { return caseName(eval, name) }

var (
	p3AgentW  = uuid.MustParse("a0000000-0000-4000-8000-000000000003")
	p3LaneW1  = uuid.MustParse("d0000000-0000-4000-8000-000000000001")
	p3TaskOld = uuid.MustParse("c0000000-0000-4000-8000-000000000001")
	p3MsgA    = uuid.MustParse("e0000000-0000-4000-8000-00000000000a")
	p3MsgB    = uuid.MustParse("e0000000-0000-4000-8000-00000000000b")
	p3Trigger = uuid.MustParse("e0000000-0000-4000-8000-000000000001")
)

// ---------------------------------------------------------------------------
// What the implementation must expose.
// ---------------------------------------------------------------------------

// resumeCase describes the state a new attempt starts from.
type resumeCase struct {
	// SessionRef is the lane's `runtime_session_ref` (harness §6). Empty means
	// the lane has none, so only a cold start is possible.
	SessionRef string
	// RefRuntimeKind is the runtime that produced SessionRef.
	RefRuntimeKind string
	// ProfileRuntimeKind is the runtime the next attempt will use. Different
	// from RefRuntimeKind means a profile fallback happened (E8-08).
	ProfileRuntimeKind string

	// Cause is why this attempt exists:
	//   "hitl_answer" | "retry_network" | "retry_auth" | "requeue_heartbeat"
	//   | "restart"   (the human's "중단하고 다시 지시", FR-3.4)
	Cause string

	// ResumeRejected is the daemon's `resume_rejected` report (harness §6).
	ResumeRejected bool

	// PostedMessageIDs are the messages the previous attempt already posted.
	PostedMessageIDs []uuid.UUID

	// PrevWorkdir is the workdir of the previous attempt; the new attempt must
	// reuse it, never create a second one.
	PrevWorkdir string
	// PrevPhase is how far the previous attempt got ("preparing" for E8-11).
	PrevPhase string

	// HistoryTotal / HistoryLimit drive E8-12.
	HistoryTotal int
	HistoryLimit int

	// NewInstruction is the content of a re-instruction (E8-06).
	NewInstruction string

	// AlternateProfile reports whether the same machine has another profile
	// for this agent (E8-08 vs E8-09).
	AlternateProfile bool
}

// attemptPlan is the server's decision for the next attempt: which task row it
// belongs to, what the bundle carries, and what the prompt contains.
type attemptPlan struct {
	// TaskID is the task this attempt runs under. Same as before for a retry,
	// a NEW id for a re-instruction (FR-3.4 B).
	TaskID  uuid.UUID
	Attempt int
	// RestartedFromTaskID is set only for a re-instruction.
	RestartedFromTaskID uuid.UUID
	// TriggerMessageID must be unchanged for a retry (E8-07) and the new
	// message for a re-instruction.
	TriggerMessageID uuid.UUID

	// TaskStatus is where the row sits after planning.
	TaskStatus string
	LaneStatus string

	// Workdir and WorkdirCreated: the same directory, not a new one.
	Workdir        string
	WorkdirCreated bool

	// ResumeRef is the bundle's `resume` (daemon-protocol §4.1). Empty means a
	// cold start.
	ResumeRef string
	// ResumeAttempted reports whether the runtime session was tried BEFORE a
	// cold start (FR-5.4 order).
	ResumeAttempted bool
	// ColdStart is the fallback path.
	ColdStart bool

	// Prompt sections. `<resumed>` must be absent for a re-instruction.
	HasResumedSection bool
	PostedMessageIDs  []uuid.UUID
	// WorkdirCheckInstruction is §8.4's "workdir의 현재 상태를 먼저 확인하라".
	WorkdirCheckInstruction bool
	// ColdStartSections lists what a cold-start prompt rebuilds from
	// (FR-5.4 2: brief + history + decision log).
	ColdStartSections []string
	// PromptContains is what the prompt body must carry for a re-instruction.
	PromptContains string

	// HistoryIncluded / HistoryTotal / HistoryTruncated are §8.4's history
	// header (E8-12).
	HistoryIncluded  int
	HistoryTotal     int
	HistoryTruncated bool

	// Retries is how many automatic retries the failure class permits.
	Retries int
	// DirectorNotified is E8-09's "Dir 알림".
	DirectorNotified bool
	// HandedToAnotherMachine must never be true (E8-09) — the session is
	// pinned to its runtime (FR-2.1 M10).
	HandedToAnotherMachine bool
	// AgentStatus is the FR-1.3 derived status after the failure (E8-10).
	AgentStatus string
}

// planAttempt is wired by T-S5. See the P3a hand-off report, "required API".
var planAttempt func(c resumeCase) attemptPlan

func mustPlan(t *testing.T, c resumeCase) attemptPlan {
	t.Helper()
	if planAttempt == nil {
		t.Fatalf("unimplemented: the shared resume/retry path (FR-5.4 '응답 = 새 attempt', " +
			"FR-7.1 M5). T-S5 must wire `planAttempt` — see the P3a hand-off report 'required API'")
	}
	return planAttempt(c)
}

func idsEqual(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// E8-01, E8-02 — resume first, cold start as the fallback
// ---------------------------------------------------------------------------

func TestResumeFirstGolden(t *testing.T) {
	base := resumeCase{
		SessionRef: "sess-abc", RefRuntimeKind: "claude_code", ProfileRuntimeKind: "claude_code",
		Cause: "hitl_answer", PrevWorkdir: "/w/lane-1",
		PostedMessageIDs: []uuid.UUID{p3MsgA},
	}

	t.Run(caseNameP3("E8-01", "an_answered_hitl_resumes_the_runtime_session_first"), func(t *testing.T) {
		p := mustPlan(t, base)
		if !p.ResumeAttempted {
			t.Fatal("step 1 of FR-5.4 is the runtime session resume — a cold start without trying " +
				"resume throws away the context the runtime still holds")
		}
		if p.ResumeRef != "sess-abc" {
			t.Errorf("bundle resume = %q, want the lane's runtime_session_ref (harness §6)", p.ResumeRef)
		}
		if p.ColdStart {
			t.Error("resume was available and not rejected — this must not be a cold start")
		}
		if !p.HasResumedSection {
			t.Error("the resume prompt carries a <resumed> section (§8.4)")
		}
		if p.Workdir != "/w/lane-1" || p.WorkdirCreated {
			t.Errorf("workdir = %q created = %t, want the SAME workdir reused (FR-7.1 M5 1)",
				p.Workdir, p.WorkdirCreated)
		}
	})

	t.Run(caseNameP3("E8-02", "a_rejected_resume_falls_back_to_a_cold_start_with_the_full_context"), func(t *testing.T) {
		c := base
		c.ResumeRejected = true
		p := mustPlan(t, c)

		if !p.ColdStart {
			t.Fatal("resume_rejected is not a failure — it switches to a cold start (harness §6)")
		}
		for _, want := range []string{"brief", "history", "decision_log"} {
			if !hasString(p.ColdStartSections, want) {
				t.Errorf("cold-start prompt sections = %v, missing %q (FR-5.4 step 2)",
					p.ColdStartSections, want)
			}
		}
		if !p.WorkdirCheckInstruction {
			t.Error("the cold-start prompt must say the previous attempt was interrupted and the " +
				"agent should inspect the workdir first (FR-5.4 step 2, §8.4) — without it the " +
				"agent redoes edits that are already applied")
		}
		if p.Workdir != "/w/lane-1" || p.WorkdirCreated {
			t.Errorf("workdir = %q created = %t — a cold start still reuses the workdir",
				p.Workdir, p.WorkdirCreated)
		}
	})

	t.Run(caseNameP3("E8-02", "a_lane_without_a_session_ref_cold_starts_without_pretending_to_resume"), func(t *testing.T) {
		c := base
		c.SessionRef, c.RefRuntimeKind = "", ""
		p := mustPlan(t, c)
		if !p.ColdStart {
			t.Error("no runtime_session_ref means there is nothing to resume")
		}
		if p.ResumeRef != "" {
			t.Errorf("bundle resume = %q, want empty (daemon-protocol §4.1 `resume: null`)", p.ResumeRef)
		}
	})
}

// ---------------------------------------------------------------------------
// E8-06, E8-07 — re-instruction is a new task; retry is the same task
// ---------------------------------------------------------------------------

func TestRestartVersusRetryGolden(t *testing.T) {
	t.Run(caseNameP3("E8-06", "re_instruction_creates_a_new_task_with_no_resumed_section"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "sess-abc", RefRuntimeKind: "claude_code", ProfileRuntimeKind: "claude_code",
			Cause: "restart", PrevWorkdir: "/w/lane-1",
			NewInstruction:   "방향을 바꿔서 경쟁사 분석부터 해줘",
			PostedMessageIDs: []uuid.UUID{p3MsgA, p3MsgB},
		})

		if p.TaskID == p3TaskOld {
			t.Fatal("re-instruction is a NEW task — reusing the row overwrites trigger_message_id " +
				"and loses the history of what was asked (FR-3.4 B)")
		}
		if p.Attempt != 1 {
			t.Errorf("attempt = %d, want 1 — a new task starts its own attempt count (FR-3.4)", p.Attempt)
		}
		if p.RestartedFromTaskID != p3TaskOld {
			t.Errorf("restarted_from_task_id = %s, want the cancelled task %s", p.RestartedFromTaskID, p3TaskOld)
		}
		if p.HasResumedSection {
			t.Fatal("a re-instruction prompt carries NO <resumed> section: the human changed " +
				"direction, so telling the agent to continue the interrupted work is exactly wrong (§8.4)")
		}
		if len(p.PostedMessageIDs) != 0 {
			t.Errorf("posted_message_ids = %v, want empty — that list belongs to <resumed> (daemon-protocol §4.1)",
				p.PostedMessageIDs)
		}
		if p.PromptContains != "방향을 바꿔서 경쟁사 분석부터 해줘" {
			t.Errorf("prompt = %q, want only the new instruction (§8.4)", p.PromptContains)
		}
		if p.LaneStatus != "running" {
			t.Errorf("lane = %q, want running — the lane survives the restart (FR-3.4, E2-15)", p.LaneStatus)
		}
	})

	t.Run(caseNameP3("E8-07", "a_network_retry_keeps_the_task_the_trigger_and_bumps_the_attempt"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "sess-abc", RefRuntimeKind: "claude_code", ProfileRuntimeKind: "claude_code",
			Cause: "retry_network", PrevWorkdir: "/w/lane-1",
			PostedMessageIDs: []uuid.UUID{p3MsgA},
		})

		if p.TaskID != p3TaskOld {
			t.Errorf("task = %s, want the SAME task %s — a retry is not a new instruction (FR-3.4 표)",
				p.TaskID, p3TaskOld)
		}
		if p.Attempt != 2 {
			t.Errorf("attempt = %d, want 2", p.Attempt)
		}
		if p.RestartedFromTaskID != uuid.Nil {
			t.Errorf("restarted_from_task_id = %s, want empty for a retry", p.RestartedFromTaskID)
		}
		if p.TriggerMessageID != p3Trigger {
			t.Errorf("trigger_message_id = %s, want unchanged %s (E8-07)", p.TriggerMessageID, p3Trigger)
		}
		if !p.HasResumedSection {
			t.Error("a retry prompt DOES carry <resumed> (§8.4, FR-7.1 M5 3)")
		}
		if !idsEqual(p.PostedMessageIDs, []uuid.UUID{p3MsgA}) {
			t.Errorf("posted_message_ids = %v, want [%s] — the list is what stops a duplicate post",
				p.PostedMessageIDs, p3MsgA)
		}
	})
}

// ---------------------------------------------------------------------------
// E8-08, E8-09, E8-10 — profile fallback and the failure classes
// ---------------------------------------------------------------------------

func TestProfileFallbackGolden(t *testing.T) {
	t.Run(caseNameP3("E8-08", "a_runtime_kind_change_keeps_the_workdir_and_empties_the_resume_ref"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "sess-hermes-1", RefRuntimeKind: "hermes", ProfileRuntimeKind: "claude_code",
			Cause: "retry_network", PrevWorkdir: "/w/lane-1", AlternateProfile: true,
		})
		if p.Workdir != "/w/lane-1" || p.WorkdirCreated {
			t.Errorf("workdir = %q created = %t, want the same workdir kept across the switch (E8-08)",
				p.Workdir, p.WorkdirCreated)
		}
		if p.ResumeRef != "" {
			t.Errorf("resume = %q, want EMPTY — a Hermes session id cannot be loaded by Claude Code "+
				"(daemon-protocol §4.4 v0.4)", p.ResumeRef)
		}
		if !p.ColdStart {
			t.Error("with no usable session ref the new runtime cold starts")
		}
		if p.HandedToAnotherMachine {
			t.Error("the fallback is to another PROFILE on the same machine, never another machine")
		}
	})

	t.Run(caseNameP3("E8-09", "with_no_alternate_profile_the_task_waits_and_the_director_is_told"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "sess-hermes-1", RefRuntimeKind: "hermes", ProfileRuntimeKind: "hermes",
			Cause: "retry_network", PrevWorkdir: "/w/lane-1", AlternateProfile: false,
		})
		if p.HandedToAnotherMachine {
			t.Fatal("the session is pinned to its runtime (FR-2.1 M10) — handing the task to another " +
				"machine invalidates both runtime_session_ref and the workdir (E8-09)")
		}
		if p.TaskStatus != "queued" {
			t.Errorf("task = %q, want queued (waiting for the profile to come back)", p.TaskStatus)
		}
		if !p.DirectorNotified {
			t.Error("the Director is notified — a task parked in queued with nobody told is invisible")
		}
	})

	t.Run(caseNameP3("E8-10", "an_auth_failure_is_not_retried_and_shows_the_agent_as_error"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "sess-abc", RefRuntimeKind: "claude_code", ProfileRuntimeKind: "claude_code",
			Cause: "retry_auth", PrevWorkdir: "/w/lane-1", AlternateProfile: true,
		})
		if p.Retries != 0 {
			t.Errorf("retries = %d, want 0 — auth·quota·config are never retried (FR-7.1, harness §8)",
				p.Retries)
		}
		if p.TaskStatus != "failed" {
			t.Errorf("task = %q, want failed", p.TaskStatus)
		}
		if p.AgentStatus != "error" {
			t.Errorf("agent status = %q, want error (FR-1.3 step 3, E5-17)", p.AgentStatus)
		}
	})
}

// ---------------------------------------------------------------------------
// E8-11 — resuming a `preparing` attempt reuses the workdir
// ---------------------------------------------------------------------------

func TestPreparingResumeGolden(t *testing.T) {
	t.Run(caseNameP3("E8-11", "resuming_from_preparing_reuses_the_existing_workdir"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "", ProfileRuntimeKind: "claude_code",
			Cause: "requeue_heartbeat", PrevWorkdir: "/w/lane-1", PrevPhase: "preparing",
		})
		if p.WorkdirCreated {
			t.Error("the previous attempt already prepared the workdir — creating a second one " +
				"leaves an orphan directory and, under worktree isolation, a second checkout (E8-11)")
		}
		if p.Workdir != "/w/lane-1" {
			t.Errorf("workdir = %q, want the prepared one", p.Workdir)
		}
	})
}

// ---------------------------------------------------------------------------
// E8-12 — the history section declares its own truncation
// ---------------------------------------------------------------------------

func TestHistoryTruncationGolden(t *testing.T) {
	t.Run(caseNameP3("E8-12", "a_truncated_history_says_included_total_and_truncated"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "sess-abc", RefRuntimeKind: "claude_code", ProfileRuntimeKind: "claude_code",
			Cause: "hitl_answer", PrevWorkdir: "/w/lane-1",
			HistoryTotal: 200, HistoryLimit: 50,
		})
		if p.HistoryIncluded != 50 {
			t.Errorf("included = %d, want 50 (the cap)", p.HistoryIncluded)
		}
		if p.HistoryTotal != 200 {
			t.Errorf("total = %d, want 200 — the agent has to know how much it cannot see",
				p.HistoryTotal)
		}
		if !p.HistoryTruncated {
			t.Error("truncated = false with 50 of 200 — the flag is what tells the agent to read " +
				"more with `colab session messages` (§8.4)")
		}
	})

	t.Run(caseNameP3("E8-12", "an_untruncated_history_is_not_flagged"), func(t *testing.T) {
		p := mustPlan(t, resumeCase{
			SessionRef: "sess-abc", RefRuntimeKind: "claude_code", ProfileRuntimeKind: "claude_code",
			Cause: "hitl_answer", PrevWorkdir: "/w/lane-1",
			HistoryTotal: 12, HistoryLimit: 50,
		})
		if p.HistoryTruncated {
			t.Error("12 of 12 is not truncated — a always-true flag makes the field meaningless")
		}
		if p.HistoryIncluded != 12 || p.HistoryTotal != 12 {
			t.Errorf("included/total = %d/%d, want 12/12", p.HistoryIncluded, p.HistoryTotal)
		}
	})
}

// ---------------------------------------------------------------------------
// E8-13 — the finish report's runtime_session_ref round-trips (G3 S-1)
//
// This is a REGRESSION row promoted from a G3 defect: the attempt reported a
// session ref, the server dropped it, and every later attempt cold started
// while claiming resume was supported.
// ---------------------------------------------------------------------------

type sessionRefRoundTrip struct {
	FinishStatus int
	// Stored is what landed on lane.runtime_session_ref.
	StoredRuntimeKind string
	StoredSessionID   string
	// NextBundleResume is what the NEXT attempt's TaskBundle carries.
	NextBundleResumeSessionID string
}

var finishWithSessionRef func(runtimeKind, sessionID string) sessionRefRoundTrip

func TestSessionRefRoundTripGolden(t *testing.T) {
	t.Run(caseNameP3("E8-13", "a_reported_session_ref_is_stored_and_handed_to_the_next_attempt"), func(t *testing.T) {
		if finishWithSessionRef == nil {
			t.Fatalf("unimplemented: runtime_session_ref round trip (daemon-protocol §4.4, harness §6). " +
				"T-S5 must wire `finishWithSessionRef` — see the P3a hand-off report")
		}
		r := finishWithSessionRef("claude_code", "sess-xyz")
		if r.FinishStatus != 200 {
			t.Errorf("finish → %d, want 200", r.FinishStatus)
		}
		if r.StoredRuntimeKind != "claude_code" || r.StoredSessionID != "sess-xyz" {
			t.Errorf("stored ref = {%q, %q}, want the contract keys runtime_kind and session_id "+
				"(harness §6)", r.StoredRuntimeKind, r.StoredSessionID)
		}
		if r.NextBundleResumeSessionID != "sess-xyz" {
			t.Errorf("next bundle resume = %q, want sess-xyz — storing it and not handing it back "+
				"makes every attempt a silent cold start (G3 S-1)", r.NextBundleResumeSessionID)
		}
	})
}

// fixtures the wiring PR consumes.
var _ = []uuid.UUID{p3AgentW, p3LaneW1}
var _ = time.Minute
