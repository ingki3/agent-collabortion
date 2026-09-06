//go:build p4golden

// Golden table for runtime offline grace and session rebinding (EVAL E14, 9
// rows) — PRD FR-9.2 (런타임이 영구히 사라졌을 때, 리뷰 지적 F), FR-9 (probe 가
// repo_path·remote URL·브랜치·클린 여부를 보고한다), contracts/openapi.yaml
// `rebindSession` · `listRuntimeCandidates` · `deleteRuntime`
// (`runtime_has_active_sessions`) and contracts/daemon-protocol.md §4.3
// `rebind_prepare`.
//
// WHAT THIS FILE PINS THAT IS EASY TO GET BACKWARDS
//
//   - The grace period is a THRESHOLD, not a mood. At 6일 23시간 the session is
//     still `active` and nobody is told (E14-01); at 7일 it is
//     `paused(runtime_offline)` and the Director is (E14-02). An
//     implementation that notifies early trains people to ignore the alert;
//     one that notifies late leaves the session silently queued forever, which
//     is the failure FR-9.2 was written to close.
//   - "같은 저장소" is decided by REMOTE URL, never by path (FR-9.2, F).
//     `repo_path` equality is the tempting comparison and it is wrong in both
//     directions: E14-04 has different paths and the SAME repository, E14-05
//     has the same path string and a DIFFERENT one.
//   - Rebinding a `worktree` session is not "carry on". The commits are on the
//     dead machine's branch and push is a non-goal (§2.2), so the first prompt
//     after the rebind MUST tell the agent to re-apply the session's diff
//     artifacts IN SUBMISSION ORDER (E14-06). Without that line the rebind is
//     "start over" wearing a recovery label.
//   - The rebind prompt is a COLD START. The runtime session lives on the dead
//     machine; carrying `runtime_session_ref` over would make the daemon
//     resume a session id the new machine has never seen.
//   - Ending the session is `cancelled`, not `completed` (E14-07). A session
//     that never met its completion conditions must not be filed as success.
//   - A runtime with an active session cannot be deleted (E14-08). The 409 is
//     the last thing standing between a stray click and an unrecoverable
//     session.
//
// HOW THIS FILE FAILS TODAY. `runtimes.Candidates` already answers
// listRuntimeCandidates with the remote-URL rule (candidates.go), so E14-04·05
// may pass through the adapter as a regression guard. Nothing sweeps the grace
// period, nothing rebinds, nothing builds the artifact-replay prompt, and
// `deleteRuntime` has no active-session check. Those hooks are nil until T-S9
// wires them.
//
// TIME. Every row states elapsed time and reads it from the injected clock
// (contracts/clock, EVAL 검증 칸 "unit + clock"). A test that sleeps would take
// seven days.
package runtimes

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func caseNameP4(eval, name string) string {
	out := make([]byte, 0, len(eval))
	for i := 0; i < len(eval); i++ {
		if eval[i] == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, eval[i])
	}
	return string(out) + "_" + name
}

var (
	p4RuntimeA = uuid.MustParse("44444444-0000-4000-8000-00000000000a")
	p4RuntimeB = uuid.MustParse("44444444-0000-4000-8000-00000000000b")
	p4Session  = uuid.MustParse("11111111-0000-4000-8000-000000000001")
	p4Lane     = uuid.MustParse("55555555-0000-4000-8000-000000000001")
	p4ArtA     = uuid.MustParse("66666666-0000-4000-8000-000000000001")
	p4ArtB     = uuid.MustParse("66666666-0000-4000-8000-000000000002")
)

const p4Day = 24 * time.Hour

// ---------------------------------------------------------------------------
// 1. The grace period — E14-01, E14-02, E14-09
// ---------------------------------------------------------------------------

// offlineCase is one sweep of the offline check.
type offlineCase struct {
	RuntimeID uuid.UUID
	// OfflineFor is how long the runtime has been offline at sweep time. Zero
	// means it is online.
	OfflineFor time.Duration
	// Grace is the workspace's `runtime_offline_grace` (default P7D).
	Grace time.Duration
	// QueuedTasks is how many tasks are waiting on this runtime.
	QueuedTasks int
	// SessionState before the sweep.
	SessionState string
}

// offlineOutcome is what the sweep did.
type offlineOutcome struct {
	SessionState string
	PauseReason  string
	// DirectorNotified is the inbox item (`runtime_offline`, FR-8).
	DirectorNotified bool
	// Choices are what the notification offers. FR-9.2 names exactly two.
	Choices []string
	// Dispatched counts tasks handed out during the sweep. A paused session
	// dispatches nothing (E5-04).
	Dispatched int
	// GraceEndsAt is the contract's Runtime.grace_ends_at, so S11 can show
	// "언제까지" instead of a bare "오프라인".
	GraceEndsAt time.Time
}

var sweepOffline func(c offlineCase) offlineOutcome

func mustSweep(t *testing.T, c offlineCase) offlineOutcome {
	t.Helper()
	if sweepOffline == nil {
		t.Fatalf("unimplemented: runtime offline grace sweep (FR-9.2, E14-01·02·09). T-S9 must " +
			"wire `sweepOffline` — see the P4a hand-off report 'required API'")
	}
	return sweepOffline(c)
}

func TestRuntimeOfflineGraceGolden(t *testing.T) {
	t.Run(caseNameP4("E14-01", "just_under_the_grace_period_the_session_stays_active_and_silent"), func(t *testing.T) {
		o := mustSweep(t, offlineCase{
			RuntimeID: p4RuntimeA, OfflineFor: 7*p4Day - time.Hour, Grace: 7 * p4Day,
			QueuedTasks: 2, SessionState: "active",
		})
		if o.SessionState != "active" {
			t.Errorf("session = %q, want active — a laptop closed for a weekend is normal; pausing "+
				"early makes the alert meaningless (E14-01)", o.SessionState)
		}
		if o.DirectorNotified {
			t.Error("알림 없음 (E14-01): the queued tasks simply wait")
		}
		if o.Dispatched != 0 {
			t.Errorf("dispatched = %d, want 0 — there is no machine to dispatch to", o.Dispatched)
		}
	})

	t.Run(caseNameP4("E14-02", "at_the_grace_period_the_session_pauses_with_runtime_offline_and_two_choices"), func(t *testing.T) {
		o := mustSweep(t, offlineCase{
			RuntimeID: p4RuntimeA, OfflineFor: 7 * p4Day, Grace: 7 * p4Day,
			QueuedTasks: 2, SessionState: "active",
		})
		if o.SessionState != "paused" || o.PauseReason != "runtime_offline" {
			t.Errorf("session = %q(%q), want paused(runtime_offline) — the session is pinned to "+
				"runtime_id (C4), so without the pause it queues forever and nobody is told "+
				"(FR-9.2 '두 리뷰 모두 놓친 경로')", o.SessionState, o.PauseReason)
		}
		if !o.DirectorNotified {
			t.Error("Dir 알림 (E14-02) — the pause is only half the fix; the Director has to choose")
		}
		if len(o.Choices) != 2 {
			t.Fatalf("choices = %v, want exactly 2 (재바인딩 / 종료, FR-9.2)", o.Choices)
		}
		joined := strings.Join(o.Choices, ",")
		if !strings.Contains(joined, "rebind") || !strings.Contains(joined, "cancel") {
			t.Errorf("choices = %v, want a rebind and an end option", o.Choices)
		}
		if o.GraceEndsAt.IsZero() {
			t.Error("grace_ends_at must be set so S11 shows when the window closed (openapi " +
				"Runtime.grace_ends_at)")
		}
	})

	t.Run(caseNameP4("E14-09", "a_runtime_that_returns_within_the_window_resumes_normally"), func(t *testing.T) {
		o := mustSweep(t, offlineCase{
			RuntimeID: p4RuntimeA, OfflineFor: 0, Grace: 7 * p4Day,
			QueuedTasks: 2, SessionState: "active",
		})
		if o.SessionState != "active" {
			t.Errorf("session = %q, want active — the machine came back inside the window, so "+
				"nothing happened at all (E14-09)", o.SessionState)
		}
		if o.Dispatched != 2 {
			t.Errorf("dispatched = %d, want 2 — the queued tasks proceed (E14-09)", o.Dispatched)
		}
	})

	// EVAL 제안 행 (E14-10): the sweep must be idempotent. It runs on a timer,
	// so a second pass over an already-paused session must not notify again —
	// an implementation that keys off "offline > grace" alone re-notifies every
	// tick and the Director's inbox fills with the same item.
	t.Run(caseNameP4("E14-10", "sweeping_an_already_paused_session_does_not_notify_twice"), func(t *testing.T) {
		o := mustSweep(t, offlineCase{
			RuntimeID: p4RuntimeA, OfflineFor: 9 * p4Day, Grace: 7 * p4Day,
			QueuedTasks: 2, SessionState: "paused",
		})
		if o.DirectorNotified {
			t.Error("the sweep is periodic; notifying on every pass buries the one item that " +
				"needed an answer [EVAL 제안 행]")
		}
		if o.SessionState != "paused" || o.PauseReason != "runtime_offline" {
			t.Errorf("session = %q(%q), want it left alone", o.SessionState, o.PauseReason)
		}
	})
}

// ---------------------------------------------------------------------------
// 2. Candidate selection — E14-04, E14-05
// ---------------------------------------------------------------------------

// repo is one entry of the daemon's probe `repos` report (FR-9).
type repo struct {
	Path      string
	RemoteURL string
}

// candidateCase asks "may this runtime take the session?".
type candidateCase struct {
	Isolation string // worktree | none | container
	// SessionRemote is the original repository's remote URL.
	SessionRemote string
	// SessionRepoPath is the original path — present ONLY so a path-comparing
	// implementation has something to be wrong with.
	SessionRepoPath string

	Online bool
	Repos  []repo
}

type candidateVerdict struct {
	Eligible bool
	// Reason is shown next to a disabled row (openapi RuntimeCandidate.reason).
	Reason string
	// MatchedRepoPath is the repository the candidate would use.
	MatchedRepoPath string
}

var judgeCandidate func(c candidateCase) candidateVerdict

func mustJudgeCandidate(t *testing.T, c candidateCase) candidateVerdict {
	t.Helper()
	if judgeCandidate == nil {
		t.Fatalf("unimplemented: rebind candidate rule (FR-9.2 F, E14-03·04·05, openapi " +
			"listRuntimeCandidates). T-S9 must wire `judgeCandidate` — see the P4a hand-off " +
			"report 'required API'")
	}
	return judgeCandidate(c)
}

func TestRebindCandidateGolden(t *testing.T) {
	t.Run(caseNameP4("E14-04", "a_different_path_with_the_same_remote_url_is_a_candidate"), func(t *testing.T) {
		v := mustJudgeCandidate(t, candidateCase{
			Isolation: "worktree", SessionRemote: "git@x:app.git", SessionRepoPath: "/Users/a/dev/app",
			Online: true,
			Repos:  []repo{{Path: "/home/b/src/app", RemoteURL: "git@x:app.git"}},
		})
		if !v.Eligible {
			t.Fatalf("eligible = false (%q) — the same repository cloned to a different path is "+
				"THE normal case across two machines; a path comparison rejects every real "+
				"rebind (FR-9.2 F, E14-04)", v.Reason)
		}
		if v.MatchedRepoPath != "/home/b/src/app" {
			t.Errorf("matched repo = %q, want the candidate's own path", v.MatchedRepoPath)
		}
	})

	t.Run(caseNameP4("E14-05", "the_same_path_string_with_a_different_remote_is_not_a_candidate"), func(t *testing.T) {
		v := mustJudgeCandidate(t, candidateCase{
			Isolation: "worktree", SessionRemote: "git@x:app.git", SessionRepoPath: "/Users/a/dev/app",
			Online: true,
			Repos:  []repo{{Path: "/Users/a/dev/app", RemoteURL: "git@y:other.git"}},
		})
		if v.Eligible {
			t.Fatal("`/Users/a/dev/app` on another machine is a DIFFERENT repository. Rebinding " +
				"here would apply the session's diff artifacts to somebody else's code " +
				"(FR-9.2 F, E14-05)")
		}
		if v.Reason == "" {
			t.Error("S17 draws the row disabled WITH the reason; a silently missing machine looks " +
				"like a bug to the person who is staring at it")
		}
	})

	t.Run(caseNameP4("E14-03", "isolation_none_accepts_any_online_runtime"), func(t *testing.T) {
		v := mustJudgeCandidate(t, candidateCase{Isolation: "none", Online: true})
		if !v.Eligible {
			t.Errorf("eligible = false (%q) — `none` has no repository to match, so any online "+
				"machine can take the session (FR-9.2)", v.Reason)
		}
	})

	t.Run(caseNameP4("E14-03", "an_offline_runtime_is_never_a_candidate"), func(t *testing.T) {
		v := mustJudgeCandidate(t, candidateCase{
			Isolation: "worktree", SessionRemote: "git@x:app.git", Online: false,
			Repos: []repo{{Path: "/home/b/src/app", RemoteURL: "git@x:app.git"}},
		})
		if v.Eligible {
			t.Error("rebinding onto a second dead machine repeats the outage the Director is " +
				"trying to escape (E14-02 → E14-03)")
		}
	})
}

// ---------------------------------------------------------------------------
// 3. The rebind itself — E14-03, E14-06
// ---------------------------------------------------------------------------

// artifact is one submitted diff, in submission order.
type artifact struct {
	ID    uuid.UUID
	Order int
	Kind  string // diff | doc | …
}

type rebindCase struct {
	Isolation string
	// TargetRuntime is the runtime the Director picked, and TargetEligible is
	// whether the candidate rule above accepts it.
	TargetRuntime  uuid.UUID
	TargetEligible bool
	// AcknowledgeLoss is the contract's flag; worktree requires it.
	AcknowledgeLoss bool
	// SessionState before the rebind.
	SessionState string
	// Artifacts submitted so far, in submission order.
	Artifacts []artifact
	// RunningLaneSessionRef is the dead machine's runtime session id.
	RunningLaneSessionRef string
}

type rebindResult struct {
	HTTPStatus int
	// RuntimeID after the rebind.
	RuntimeID    uuid.UUID
	SessionState string
	// QueuedDispatched counts tasks that went out on the NEW runtime.
	QueuedDispatched int
	// The conversation must survive: FR-9.2 "아티팩트·메시지·결정 기록은
	// 서버에 있으므로 대화 컨텍스트는 온전히 남는다".
	MessagesKept  int
	ArtifactsKept int
	DecisionsKept int

	// --- the first prompt after the rebind ---
	// PromptIsColdStart: the runtime session ref is dropped.
	PromptIsColdStart bool
	// CarriedSessionRef must be empty.
	CarriedSessionRef string
	// PromptArtifactOrder is the artifact ids in the order the prompt lists
	// them: submission order (E14-06).
	PromptArtifactOrder []uuid.UUID
	// PromptSaysApplyArtifacts is E14-06's sentence.
	PromptSaysApplyArtifacts bool
	// PrepareCommandIssued is daemon-protocol §4.3 `rebind_prepare`.
	PrepareCommandIssued bool
	// PrepareCommandApplies must be false — the daemon downloads, the PROMPT
	// applies (§4.3: "아티팩트 순서 적용은 프롬프트가 지시한다. 데몬은 다운로드만").
	PrepareCommandApplies bool
}

var rebindSession func(c rebindCase) rebindResult

func mustRebind(t *testing.T, c rebindCase) rebindResult {
	t.Helper()
	if rebindSession == nil {
		t.Fatalf("unimplemented: session rebinding (FR-9.2, E14-03·06, openapi rebindSession, " +
			"daemon-protocol §4.3 rebind_prepare). T-S9 must wire `rebindSession` — see the " +
			"P4a hand-off report 'required API'")
	}
	return rebindSession(c)
}

func TestRebindGolden(t *testing.T) {
	t.Run(caseNameP4("E14-03", "isolation_none_moves_the_session_and_keeps_the_whole_conversation"), func(t *testing.T) {
		r := mustRebind(t, rebindCase{
			Isolation: "none", TargetRuntime: p4RuntimeB, TargetEligible: true, SessionState: "paused",
		})
		if r.RuntimeID != p4RuntimeB {
			t.Errorf("runtime = %s, want the chosen runtime B (E14-03)", r.RuntimeID)
		}
		if r.SessionState != "active" {
			t.Errorf("session = %q, want active — the rebind IS the resume (openapi "+
				"rebindSession '…`active`로 되돌린다')", r.SessionState)
		}
		if r.QueuedDispatched < 1 {
			t.Errorf("dispatched = %d, want the queued task to run on B (E14-03)", r.QueuedDispatched)
		}
		if r.MessagesKept == 0 || r.DecisionsKept == 0 {
			t.Errorf("messages/decisions kept = %d/%d — 대화·아티팩트·결정 기록 온전 (E14-03). "+
				"They live on the server; a rebind that clears them is destroying data it never "+
				"had to touch", r.MessagesKept, r.DecisionsKept)
		}
	})

	t.Run(caseNameP4("E14-06", "a_worktree_rebind_prompts_for_diff_replay_in_submission_order"), func(t *testing.T) {
		r := mustRebind(t, rebindCase{
			Isolation: "worktree", TargetRuntime: p4RuntimeB, TargetEligible: true,
			AcknowledgeLoss:       true,
			SessionState:          "paused",
			RunningLaneSessionRef: "sess-on-dead-machine",
			Artifacts: []artifact{
				{ID: p4ArtA, Order: 1, Kind: "diff"},
				{ID: p4ArtB, Order: 2, Kind: "diff"},
			},
		})

		if !r.PromptSaysApplyArtifacts {
			t.Fatal("E14-06's sentence is missing: \"이 세션의 diff 아티팩트를 제출 순서대로 새 " +
				"workdir에 적용한 뒤 이어가라\". Without it the rebind is not recovery — the " +
				"commits are on the dead machine and push is a non-goal (§2.2, FR-9.2 F)")
		}
		if len(r.PromptArtifactOrder) != 2 ||
			r.PromptArtifactOrder[0] != p4ArtA || r.PromptArtifactOrder[1] != p4ArtB {
			t.Errorf("prompt artifact order = %v, want [%s %s] — 아티팩트 목록 순서 = 제출 순서. "+
				"Diffs applied out of order conflict (E14-06)", r.PromptArtifactOrder, p4ArtA, p4ArtB)
		}
		if !r.PromptIsColdStart || r.CarriedSessionRef != "" {
			t.Errorf("cold_start = %t carried ref = %q — the runtime session lives on the machine "+
				"that is gone; resuming an id the new daemon never issued fails at session/load "+
				"(openapi rebindSession '진행 중이던 lane의 runtime_session_ref는 비워 콜드 스타트')",
				r.PromptIsColdStart, r.CarriedSessionRef)
		}
		if !r.PrepareCommandIssued {
			t.Error("`rebind_prepare` tells the daemon to make the new workdir and fetch the " +
				"artifacts (daemon-protocol §4.3)")
		}
		if r.PrepareCommandApplies {
			t.Error("the daemon DOWNLOADS only — applying is the agent's job under the prompt's " +
				"instruction (§4.3), and a daemon that patches silently hides conflicts from " +
				"the agent that has to resolve them")
		}
	})

	t.Run(caseNameP4("E14-06", "a_worktree_rebind_without_acknowledge_loss_is_refused"), func(t *testing.T) {
		r := mustRebind(t, rebindCase{
			Isolation: "worktree", TargetRuntime: p4RuntimeB, TargetEligible: true,
			AcknowledgeLoss: false, SessionState: "paused",
		})
		if r.HTTPStatus != 422 {
			t.Errorf("status = %d, want 422 — worktree 격리에서 유실 경고를 확인해야 한다 "+
				"(openapi rebindSession `acknowledge_loss`)", r.HTTPStatus)
		}
	})

	t.Run(caseNameP4("E14-03", "rebinding_a_session_that_is_not_paused_runtime_offline_is_refused"), func(t *testing.T) {
		r := mustRebind(t, rebindCase{
			Isolation: "none", TargetRuntime: p4RuntimeB, TargetEligible: true, SessionState: "active",
		})
		if r.HTTPStatus != 409 {
			t.Errorf("status = %d, want 409 — rebinding a live session moves work away from a "+
				"machine that is still running it (openapi rebindSession)", r.HTTPStatus)
		}
	})

	t.Run(caseNameP4("E14-05", "rebinding_onto_a_non_candidate_is_refused"), func(t *testing.T) {
		r := mustRebind(t, rebindCase{
			Isolation: "worktree", TargetRuntime: p4RuntimeB, TargetEligible: false,
			AcknowledgeLoss: true, SessionState: "paused",
		})
		if r.HTTPStatus != 422 {
			t.Errorf("status = %d, want 422 — the candidate rule is enforced at the REBIND, not "+
				"only in the picker; a direct API call must not bypass E14-05", r.HTTPStatus)
		}
	})
}

// ---------------------------------------------------------------------------
// 4. Ending instead of rebinding — E14-07
// ---------------------------------------------------------------------------

type endResult struct {
	SessionState string
	// ArtifactsRecovered is FR-9.2's "아티팩트만 회수한다".
	ArtifactsRecovered int
	// SummaryMsgs: ending here is not completion, so there is no FR-2.4
	// summary of a job that was never finished.
	CompletionConditionsMet bool
}

var endOfflineSession func(artifacts int) endResult

func TestOfflineSessionEndGolden(t *testing.T) {
	t.Run(caseNameP4("E14-07", "choosing_to_end_cancels_the_session_and_recovers_artifacts"), func(t *testing.T) {
		if endOfflineSession == nil {
			t.Fatalf("unimplemented: end-instead-of-rebind (FR-9.2, E14-07). T-S9 must wire " +
				"`endOfflineSession` — see the P4a hand-off report 'required API'")
		}
		r := endOfflineSession(2)
		if r.SessionState != "cancelled" {
			t.Errorf("session = %q, want cancelled — the goal was never met, and filing it as "+
				"`completed` puts a machine outage in the success column (E14-07)", r.SessionState)
		}
		if r.ArtifactsRecovered != 2 {
			t.Errorf("artifacts = %d, want 2 — 아티팩트만 회수한다 (E14-07)", r.ArtifactsRecovered)
		}
		if r.CompletionConditionsMet {
			t.Error("no completion condition was satisfied by giving up (FR-2.2)")
		}
	})
}

// ---------------------------------------------------------------------------
// 5. Deleting a runtime that still holds work — E14-08
// ---------------------------------------------------------------------------

type deleteRuntimeCase struct {
	// ActiveSessions is how many non-terminal sessions are pinned to it.
	// FR-9.2's "활성 세션" includes the `paused(runtime_offline)` ones — those
	// are exactly the sessions waiting for the choice.
	ActiveSessions int
	// PausedOfflineSessions is a subset shape: paused, not finished.
	PausedOfflineSessions int
	CompletedSessions     int
}

type deleteRuntimeResult struct {
	Deleted    bool
	HTTPStatus int
	Code       string
	// BlockingSessions is `Problem.sessions[]` — the list the UI turns into
	// links so the Director can act.
	BlockingSessions int
	// Message must ask for the rebind-or-end choice (FR-9.2).
	AsksRebindOrEnd bool
}

var deleteRuntime func(c deleteRuntimeCase) deleteRuntimeResult

func TestRuntimeDeletionGolden(t *testing.T) {
	must := func(t *testing.T) {
		t.Helper()
		if deleteRuntime == nil {
			t.Fatalf("unimplemented: runtime deletion guard (FR-9.2 마지막 줄, E14-08, openapi " +
				"deleteRuntime `runtime_has_active_sessions`). T-S9 must wire `deleteRuntime` — " +
				"see the P4a hand-off report 'required API'")
		}
	}

	t.Run(caseNameP4("E14-08", "a_runtime_with_an_active_session_cannot_be_deleted"), func(t *testing.T) {
		must(t)
		r := deleteRuntime(deleteRuntimeCase{ActiveSessions: 1})
		if r.Deleted {
			t.Fatal("deleting the runtime strands the session with no machine and no rebind " +
				"target — this 409 is the last guard before an unrecoverable click (E14-08)")
		}
		if r.HTTPStatus != 409 || r.Code != "runtime_has_active_sessions" {
			t.Errorf("status/code = %d/%q, want 409/runtime_has_active_sessions (openapi "+
				"deleteRuntime)", r.HTTPStatus, r.Code)
		}
		if r.BlockingSessions != 1 {
			t.Errorf("Problem.sessions = %d, want 1 — \"먼저 재바인딩/종료\" is only actionable "+
				"if the person is told WHICH session", r.BlockingSessions)
		}
		if !r.AsksRebindOrEnd {
			t.Error("the refusal must name the two ways out (FR-9.2)")
		}
	})

	t.Run(caseNameP4("E14-08", "a_paused_runtime_offline_session_still_blocks_the_delete"), func(t *testing.T) {
		must(t)
		r := deleteRuntime(deleteRuntimeCase{ActiveSessions: 0, PausedOfflineSessions: 1})
		if r.Deleted {
			t.Fatal("`paused(runtime_offline)` is the state a session sits in WHILE it waits for " +
				"the Director's choice — treating it as inactive deletes exactly the sessions " +
				"E14-02 just parked (E14-08)")
		}
		if r.HTTPStatus != 409 {
			t.Errorf("status = %d, want 409", r.HTTPStatus)
		}
	})

	t.Run(caseNameP4("E14-08", "a_runtime_with_only_finished_sessions_deletes"), func(t *testing.T) {
		must(t)
		r := deleteRuntime(deleteRuntimeCase{CompletedSessions: 3})
		if !r.Deleted || r.HTTPStatus != 204 {
			t.Errorf("deleted = %t status = %d, want 204 — a guard that never lets go makes "+
				"retiring a laptop impossible", r.Deleted, r.HTTPStatus)
		}
	})
}

// fixtures the wiring PR consumes.
var _ = []uuid.UUID{p4Session, p4Lane, p4RuntimeA}
