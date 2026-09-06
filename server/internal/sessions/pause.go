package sessions

// Session pause reasons and what it takes to leave them (FR-2.3, FR-7.3,
// FR-3.5, FR-9.2, openapi resumeSession).
//
// Five reasons, five different exits. Treating `paused` as one state is how a
// runtime_offline session gets "resumed" into a machine that is still gone and
// immediately pauses again, and how a budget pause is lifted without raising
// the budget it paused for.

// pause_reason (0001_init.sql).
const (
	PauseBudget         = "budget"
	PauseTime           = "time"
	PauseLoop           = "loop"
	PauseRuntimeOffline = "runtime_offline"
	PauseDirector       = "director"
)

// ResumeRule is how one paused session comes back.
type ResumeRule struct {
	// Resumable is false for runtime_offline: nothing this endpoint does makes
	// the machine reachable, so the honest answer is 409 and a pointer at
	// rebindSession or cancelSession (openapi resumeSession).
	Resumable bool
	// DeputyMayResume mirrors SCREEN §4.5's paused-resolution table: the three
	// policy pauses (budget · time · loop) are the ones a deputy may lift,
	// because they are the ones that block work while the Director is away.
	DeputyMayResume bool
	// RequiresHigherLimit: resuming a budget pause without raising the limit
	// re-trips it on the next usage report (422 unless the new limit clears
	// what is already spent).
	RequiresHigherLimit bool
	// ResetLoopCounters defaults true for a loop pause: resuming without
	// resetting means the next message re-trips the same counter (FR-3.5).
	ResetLoopCounters bool
	// ClosesSystemHitl: the budget/time/loop pauses issue a system HITL, and
	// resuming IS the answer to it (openapi resumeSession last line).
	ClosesSystemHitl bool
	// Hint is the 409 detail when Resumable is false.
	Hint string
}

// PlanResume is the per-reason rule.
//
// production caller: httpapi.ResumeSession (handlers_sessions_p3.go).
func PlanResume(reason string) ResumeRule {
	switch reason {
	case PauseBudget:
		return ResumeRule{Resumable: true, DeputyMayResume: true, RequiresHigherLimit: true, ClosesSystemHitl: true}
	case PauseTime:
		return ResumeRule{Resumable: true, DeputyMayResume: true, ClosesSystemHitl: true}
	case PauseLoop:
		return ResumeRule{Resumable: true, DeputyMayResume: true, ResetLoopCounters: true, ClosesSystemHitl: true}
	case PauseDirector:
		return ResumeRule{Resumable: true}
	case PauseRuntimeOffline:
		return ResumeRule{
			Hint: "런타임이 오프라인입니다 — 다시 연결하거나 다른 머신으로 재바인딩(rebindSession)하거나 세션을 취소하세요",
		}
	}
	return ResumeRule{Resumable: true}
}

// DrainsRunningTurn is FR-2.3's other half, stated per reason so the two
// halves cannot drift: a Director pause lets the turn finish (the human asked
// for a stop, not for lost work), a budget or time pause cancels it (letting it
// finish spends exactly what the pause exists to stop — E5-07).
//
// tasks.PlanDispatch is what production reads; this function is the same rule
// named from the session side, and both are checked by the E5 golden.
func DrainsRunningTurn(reason string) bool {
	switch reason {
	case PauseBudget, PauseTime:
		return false
	}
	return true
}
