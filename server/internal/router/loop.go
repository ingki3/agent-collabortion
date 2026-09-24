package router

import (
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
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
//
// ID and CauseID are the causal link chain depth is measured along (S-78):
// CauseID is the ID of the hop that woke the turn which wrote this trigger —
// for a mention or a delegation, the hop that created the author's task; for
// a notice that wakes a requester (join, blocked question, re-entry report),
// the requester's OWN cause, so the requester comes back at its own depth.
// Zero means "no cause": a human wrote it, or the cause is not known.
type Hop struct {
	FromAgent uuid.UUID
	ToAgent   uuid.UUID
	At        time.Time
	ID        int64
	CauseID   int64
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

// chainDepth is the depth of `next` in the causal chain a human message
// started (S-78). A hop's depth is its cause's depth + 1; a human hop is 1.
// Depth follows CAUSES, not the row order: Lead → A, Lead → B, Lead → C are
// three hops at the same depth, and the join notice that wakes Lead when they
// end carries Lead's own cause, so Lead is back at depth 1 — Lead → 실무자 →
// 리뷰어 → Lead is 4 (FR-3.5), not "every hop since the person spoke". The
// old count made an F1-shaped session (delegate ×3, join, mention, re-entry,
// delegate, join) pause at its 8th hop.
//
// A human message still resets: a cause that predates the latest human hop
// counts as that human's (depth 1), because the person intervened after it
// (E4-02, E4-06). A hop whose cause is unknown is the human's too when a
// human hop precedes it — the cause is older than the loaded window, or a
// resume erased it, and either way a person is the root.
//
// A history with no human hop at all has no chain to measure and stays at 0:
// FR-3.5 defines the limit as "사람의 메시지에서 시작해 멘션이 연쇄된 깊이",
// and an agent-only history is caught by the other two limits instead
// (E4-07). Zero propagates — a chain that does not start at a person is not
// measured at any of its links.
func chainDepth(history []Hop, next Hop) int {
	depths := make(map[int64]int, len(history)) // by hop ID
	index := make(map[int64]int, len(history))  // hop ID → position in history
	lastHuman := -1
	depthOf := func(h Hop) int {
		if h.Human() {
			return 1
		}
		j, known := index[h.CauseID]
		if h.CauseID == 0 || !known {
			if lastHuman >= 0 {
				return 1
			}
			return 0
		}
		if j < lastHuman {
			return 1
		}
		if d := depths[h.CauseID]; d > 0 {
			return d + 1
		}
		return 0
	}
	for i, h := range history {
		depths[h.ID] = depthOf(h)
		index[h.ID] = i
		if h.Human() {
			lastHuman = i
		}
	}
	return depthOf(next)
}

// MaxChainDepth is the deepest hop a session reached, by the SAME reading
// CheckLoopLimits enforces (each hop judged against the history before it).
// It exists for the §11 observation table (observations.chain_depth): a
// second implementation of the depth rule in SQL would drift from this one
// the next time the rule moves (S-78 moved it once already), and the point of
// the row is to show what the limiter actually saw.
func MaxChainDepth(history []Hop) int {
	deepest := 0
	for i := range history {
		if d := chainDepth(history[:i], history[i]); d > deepest {
			deepest = d
		}
	}
	return deepest
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

// LimitText says which limit tripped in the words the web's PausedBanner uses
// (COMPONENTS §8.4: the internal key stays out of the sentence).
func (v LoopVerdict) LimitText() string {
	switch v.Detail {
	case DetailChainDepth:
		return "주고받기 연쇄가 상한까지 깊어졌습니다"
	case DetailHopsPerHour:
		return "한 시간에 오간 횟수가 상한에 닿았습니다"
	case DetailPairRoundtrips:
		return "두 에이전트가 상한까지 주고받았습니다"
	}
	return "주고받기가 상한에 닿았습니다"
}

// PausedText is the sentence the agent and the Director both read when FR-3.5
// stopped a trigger — ErrLoopLimit's detail and Post's `loop_limit` warning.
// LimitText is its tail; the whole sentence is composed here so the wording
// lock (internal/wording, sinkFuncs) sees every piece of it in one place
// (S-79, PR #213 리뷰 NN3).
func (v LoopVerdict) PausedText() string {
	return "루프 상한에 걸려 미션이 일시정지되었습니다 — " + v.LimitText()
}

// QuestionText is the system HITL's question (pauseForLoop): the same limit
// named, then the ask.
func (v LoopVerdict) QuestionText() string {
	return "루프 상한에 도달해 미션을 일시정지했습니다 — " + v.LimitText() + ". 계속할까요?"
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

// ErrLoopLimit is what a server-originated trigger returns when FR-3.5 stopped
// it (S-76). It is a Problem so the CLI shows the agent the same sentence the
// Director sees on the banner — the delegation did NOT happen, and the agent
// should stop rather than retry.
func ErrLoopLimit(v LoopVerdict) error {
	return apperr.Conflict("loop_limit", v.PausedText())
}
