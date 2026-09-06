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

	// LastFailureKind is the failure_kind of the agent's most recent task, or
	// "" when it did not fail.
	LastFailureKind string
	// RetryInFlight covers the server re-queueing that task, including onto an
	// alternate profile (daemon-protocol v0.4 §4.4 fallback). A retry in
	// flight must never read as `error` (E5-18).
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
