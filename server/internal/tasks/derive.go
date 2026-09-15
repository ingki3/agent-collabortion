package tasks

// FR-1.3 derived agent status. The value is never stored: an agent holds many
// lanes (FR-6.1), so its status is computed from its tasks every time it is
// read. This is the pure ladder; the SQL in sessions.LoadParticipants and
// agents.Load only gathers the counts it reads.
const (
	StatusIdle         = "idle"
	StatusWorking      = "working"
	StatusWaitingHuman = "waiting_human"
	StatusError        = "error"
	StatusOffline      = "offline"
	StatusDisabled     = "disabled"
)

// Derived is the agent-scoped snapshot the ladder reads.
type Derived struct {
	RespondTo string // owner | allowlist | workspace | nobody
	Archived  bool
	// RuntimeOffline is the SESSION's runtime, not the agent's: FR-1.3 step 2
	// is about the machine that would run the turn.
	RuntimeOffline bool

	Running      int
	WaitingHuman int
	// Blocked and PausedBudget are counted but deliberately ignored: those
	// processes have already ended and the lane card explains them (FR-1.3,
	// 1st bullet) — E5-13, E5-14.
	Blocked      int
	PausedBudget int

	// LastFailureKind is the failure_kind of the agent's MOST RECENT task, or
	// "" when that task did not fail. Not "the last task that finished" — see
	// LastFailureKindSQL for why the difference decides whether `error` gets
	// stuck on an agent that is demonstrably running.
	LastFailureKind string
	// RetryInFlight covers the server re-queueing that task, including onto an
	// alternate profile (daemon-protocol v0.4 §4.4 fallback). A retry in
	// flight must never read as `error` (E5-18). With LastFailureKind defined
	// as "the most recent task's failure" this is belt-and-braces — a requeue
	// puts the same row back to `queued`, so it is no longer failed — but
	// production supplies it truthfully rather than hard-coding false.
	RetryInFlight bool
}

// Unrunnable is FR-1.3 step 3: the failure classes FR-7.1 never retries. Any
// other class means "it can run, it just did not this time".
func Unrunnable(failureKind string) bool {
	switch failureKind {
	case "auth", "quota", "config":
		return true
	}
	return false
}

// DeriveAgentStatus walks the six-step priority ladder. It is an ORDER, not a
// set of independent rules: step 1 outranks step 4, step 2 outranks step 4 and
// step 3 outranks both working and waiting_human (E5-11 … E5-18).
//
// production callers: sessions.LoadParticipants (S7 participant list) and
// agents.Load (agent page). There is no second ladder — the SQL in both only
// gathers the counts this function reads.
//
// Step 3 sits above step 4 on purpose. PRD FR-1.3's table is an ORDER — "위에서
// 부터 첫 번째로 맞는 것이 상태다" — and golden E5-15 pins that order with a
// synthetic {auth, Running: 1}. The PRD's other rule, that `error` must not stay
// sticky, is satisfied by the INPUT rather than by reordering the ladder:
// LastFailureKind is the agent's most recent task's failure, so a new task
// starting clears it (see LastFailureKindSQL, the single definition both
// production queries use). Production therefore never produces E5-15's
// synthetic input naturally.
func DeriveAgentStatus(in Derived) string {
	switch {
	case in.RespondTo == "nobody" || in.Archived:
		return StatusDisabled
	case in.RuntimeOffline:
		return StatusOffline
	case Unrunnable(in.LastFailureKind) && !in.RetryInFlight:
		return StatusError
	case in.Running > 0:
		return StatusWorking
	case in.WaitingHuman > 0:
		return StatusWaitingHuman
	}
	return StatusIdle
}
