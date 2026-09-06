package router

import (
	"time"

	"github.com/google/uuid"
)

// Limits mirrors workspace_settings.loop_limits (PRD FR-3.5).
type Limits struct {
	MaxChainDepth     int
	MaxHopsPerHour    int
	MaxPairRoundtrips int
}

// DefaultLimits are the FR-3.5 defaults, and the values 0001_init.sql writes
// into workspace_settings.loop_limits.
func DefaultLimits() Limits {
	return Limits{MaxChainDepth: 8, MaxHopsPerHour: 60, MaxPairRoundtrips: 5}
}

// HopWindow is the rolling window max_hops_per_hour counts over.
const HopWindow = time.Hour

// Loop-limit detail values. pause_reason has a single `loop` label, so the
// limit that actually tripped is recorded next to it (session.paused_detail's
// `loop` branch, migration 0006 + openapi PausedDetail) — E4-01, E4-03, E4-06 and E4-09 are four different rows and
// the Director cannot act on "loop" alone.
const (
	DetailChainDepth     = "chain_depth"
	DetailHopsPerHour    = "hops_per_hour"
	DetailPairRoundtrips = "pair_roundtrips"
)

// Hop is one trigger in the session's history. FromAgent == uuid.Nil means a
// human wrote it: humans reset the chain and the pair counter and are never
// counted toward max_hops_per_hour.
type Hop struct {
	FromAgent uuid.UUID
	ToAgent   uuid.UUID
	At        time.Time
}

// Human reports whether this hop came from a person.
func (h Hop) Human() bool { return h.FromAgent == uuid.Nil }

// LoopVerdict is the limiter's answer for one prospective trigger.
type LoopVerdict struct {
	Allowed      bool
	TaskCreated  bool
	SessionState string // active | paused
	PauseReason  string // the pause_reason enum label; "loop" when a limit tripped
	Detail       string // which limit: chain_depth | hops_per_hour | pair_roundtrips
	HitlToDir    bool   // FR-3.5: the Director is notified, source: system

	ChainDepth     int
	HopsThisWindow int
	PairRoundtrips int

	// Agents is who the limit is about — the two ends of a pair ping-pong, or
	// the trigger's own pair otherwise. PausedDetail.loop.agents shows them so
	// the Director can see WHO is looping, not just that something is.
	Agents []uuid.UUID
}

// CheckLoopLimits applies the three FR-3.5 limits to the next trigger. It is
// structural only — no content heuristic may suppress a trigger (E4-08),
// because "본문이 짧고 요청 신호가 없으면 억제" silently swallows a normal
// delegation like `@QA 리뷰 부탁해`.
func CheckLoopLimits(history []Hop, next Hop, lim Limits, now time.Time) LoopVerdict {
	v := LoopVerdict{
		Allowed: true, TaskCreated: true, SessionState: "active",
		ChainDepth:     chainDepth(history, next),
		HopsThisWindow: hopsInWindow(history, now),
		PairRoundtrips: pairRoundtrips(history, next),
		Agents:         hopAgents(next),
	}
	// A human message is never limited: it is the thing that RESETS the
	// counters, so it can always land.
	if next.Human() {
		return v
	}
	switch {
	case lim.MaxChainDepth > 0 && v.ChainDepth > lim.MaxChainDepth:
		return exceeded(v, DetailChainDepth)
	case lim.MaxHopsPerHour > 0 && v.HopsThisWindow+1 > lim.MaxHopsPerHour:
		return exceeded(v, DetailHopsPerHour)
	case lim.MaxPairRoundtrips > 0 && v.PairRoundtrips > lim.MaxPairRoundtrips:
		return exceeded(v, DetailPairRoundtrips)
	}
	return v
}

func exceeded(v LoopVerdict, detail string) LoopVerdict {
	v.Allowed, v.TaskCreated = false, false
	v.SessionState, v.PauseReason, v.Detail, v.HitlToDir = "paused", "loop", detail, true
	return v
}

// chainDepth is the depth of the mention chain STARTED BY A HUMAN that `next`
// would extend. A human message resets it to 0, so the scan walks back only to
// the most recent human hop.
//
// A history with no human hop at all has no chain to measure: FR-3.5 defines
// the limit as "사람의 메시지에서 시작해 멘션이 연쇄된 깊이", and an
// agent-only history is caught by the other two limits instead (E4-07).
func chainDepth(history []Hop, next Hop) int {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Human() {
			return len(history) - i + 1
		}
	}
	return 0
}

// hopsInWindow counts agent→agent triggers in the rolling hour. Human messages
// are not counted at all (E4-06).
func hopsInWindow(history []Hop, now time.Time) int {
	cut := now.Add(-HopWindow)
	n := 0
	for _, h := range history {
		if h.Human() || !h.At.After(cut) {
			continue
		}
		n++
	}
	return n
}

// pairRoundtrips counts how many consecutive back-and-forths the same two
// agents have had, `next` included. A third agent or a human breaks the run
// (E4-04, E4-05); two triggers make one roundtrip.
func pairRoundtrips(history []Hop, next Hop) int {
	if next.Human() {
		return 0
	}
	run := 1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Human() || !samePair(history[i], next) {
			break
		}
		run++
	}
	return (run + 1) / 2
}

// hopAgents lists the agents at the two ends of the trigger, humans omitted.
func hopAgents(h Hop) []uuid.UUID {
	out := []uuid.UUID{}
	if !h.Human() {
		out = append(out, h.FromAgent)
	}
	if h.ToAgent != uuid.Nil {
		out = append(out, h.ToAgent)
	}
	return out
}

func samePair(a, b Hop) bool {
	return (a.FromAgent == b.FromAgent && a.ToAgent == b.ToAgent) ||
		(a.FromAgent == b.ToAgent && a.ToAgent == b.FromAgent)
}

// LimitCount is the number that tripped, whichever limit it was — PausedDetail
// carries one `count` field and the banner needs it filled with the right one.
func (v LoopVerdict) LimitCount() int {
	switch v.Detail {
	case DetailChainDepth:
		return v.ChainDepth
	case DetailHopsPerHour:
		return v.HopsThisWindow
	case DetailPairRoundtrips:
		return v.PairRoundtrips
	}
	return 0
}
