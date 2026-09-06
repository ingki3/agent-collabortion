//go:build p2golden

// Golden table for FR-3.5 loop prevention (EVAL E4, 9 rows).
//
// Three independent limits, each with its own reset rule:
//
//	max_chain_depth     8   depth of a mention chain started by a human; a human
//	                        message resets it to 0
//	max_hops_per_hour  60   agent→agent triggers in a rolling hour; human
//	                        messages are not counted at all
//	max_pair_roundtrips 5   consecutive back-and-forth between the SAME two
//	                        agents; a third party or a human resets it
//
// Exceeding any limit pauses the SESSION with a reason that names the limit,
// and the offending task is never created. Nothing here is content-based:
// PRD FR-3.5 says server-side suppression is structural only, so E4-08 pins
// that a short, question-free message is still a normal trigger.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// What the implementation must expose.
// ---------------------------------------------------------------------------

// loopLimits mirrors the workspace settings (FR-3.5, adjustable per workspace).
type loopLimits struct {
	MaxChainDepth     int
	MaxHopsPerHour    int
	MaxPairRoundtrips int
}

func defaultLimits() loopLimits {
	return loopLimits{MaxChainDepth: 8, MaxHopsPerHour: 60, MaxPairRoundtrips: 5}
}

// hop is one message in the history the limiter reasons over.
type hop struct {
	fromAgent uuid.UUID // uuid.Nil ⇒ written by a human
	toAgent   uuid.UUID
	at        time.Time
}

func humanHop(to uuid.UUID, at time.Time) hop { return hop{toAgent: to, at: at} }

// loopVerdict is the limiter's answer for the NEXT trigger.
type loopVerdict struct {
	Allowed      bool
	TaskCreated  bool
	SessionState string // "active" | "paused"
	PauseReason  string // "loop" (pause_reason enum) — Detail names the limit
	Detail       string // chain_depth | hops_per_hour | pair_roundtrips
	HitlToDir    bool   // FR-3.5: Director gets a HITL notice, source: system

	ChainDepth     int
	HopsThisWindow int
	PairRoundtrips int
}

// checkLoop is wired by T-S2. Signature the report asks for:
//
//	router.CheckLoopLimits(ctx, sessionID, history []hop, next hop, limits) (loopVerdict, error)
var checkLoop func(history []hop, next hop, lim loopLimits, now time.Time) loopVerdict

func mustLoop(t *testing.T, history []hop, next hop, lim loopLimits, now time.Time) loopVerdict {
	t.Helper()
	if checkLoop == nil {
		t.Fatalf("unimplemented: FR-3.5 loop limits. T-S2 must wire `checkLoop` " +
			"(see /tmp/p2a-report.md 'required API')")
	}
	return checkLoop(history, next, lim, now)
}

// ---------------------------------------------------------------------------
// E4-01, E4-02 — chain depth
// ---------------------------------------------------------------------------

// chain builds Dir → Lead → R → W → QA → Lead → … of the requested depth.
// Depth 1 is the first agent trigger caused by the human message.
func chain(depth int, t0 time.Time) []hop {
	ring := []uuid.UUID{agLead, agR, agW, agQA}
	out := []hop{humanHop(agLead, t0)} // depth 1
	for i := 1; i < depth; i++ {
		out = append(out, hop{
			fromAgent: ring[(i-1)%len(ring)],
			toAgent:   ring[i%len(ring)],
			at:        t0.Add(time.Duration(i) * time.Minute),
		})
	}
	return out
}

func TestLoopChainDepthGolden(t *testing.T) {
	t0 := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

	t.Run(caseName("E4-01", "depth_9_pauses_session_and_creates_no_task"), func(t *testing.T) {
		h := chain(8, t0)
		next := hop{fromAgent: agQA, toAgent: agLead, at: t0.Add(9 * time.Minute)}
		v := mustLoop(t, h, next, defaultLimits(), next.at)

		if v.Allowed || v.TaskCreated {
			t.Errorf("depth 9 exceeds max_chain_depth 8 — must not create a task (allowed=%v created=%v)",
				v.Allowed, v.TaskCreated)
		}
		if v.SessionState != "paused" {
			t.Errorf("session = %q, want paused", v.SessionState)
		}
		if v.PauseReason != "loop" {
			t.Errorf("pause_reason = %q, want loop (pause_reason enum, 0001_init.sql)", v.PauseReason)
		}
		if v.Detail != "chain_depth" {
			t.Errorf("detail = %q, want chain_depth — the three limits must be distinguishable", v.Detail)
		}
		if !v.HitlToDir {
			t.Error("FR-3.5: exceeding a limit notifies the Director (HITL, source: system)")
		}
	})

	t.Run(caseName("E4-02", "human_message_resets_depth_to_zero"), func(t *testing.T) {
		h := chain(7, t0)
		// A human speaks: depth resets, so the next agent hop is depth 1 again.
		h = append(h, humanHop(agLead, t0.Add(8*time.Minute)))
		next := hop{fromAgent: agLead, toAgent: agR, at: t0.Add(9 * time.Minute)}
		v := mustLoop(t, h, next, defaultLimits(), next.at)

		if !v.Allowed || !v.TaskCreated {
			t.Error("a human message resets chain depth to 0 — the next hop must be allowed")
		}
		if v.ChainDepth > 2 {
			t.Errorf("chain_depth = %d after a human message, want the count restarted (≤2)", v.ChainDepth)
		}
		if v.SessionState != "active" {
			t.Errorf("session = %q, want active", v.SessionState)
		}
	})
}

// ---------------------------------------------------------------------------
// E4-03 … E4-05, E4-09 — pair roundtrips
// ---------------------------------------------------------------------------

// pairPingPong builds n consecutive Lead↔R roundtrips (2n triggers).
func pairPingPong(n int, t0 time.Time) []hop {
	out := []hop{}
	for i := 0; i < n; i++ {
		out = append(out,
			hop{fromAgent: agLead, toAgent: agR, at: t0.Add(time.Duration(2*i) * time.Minute)},
			hop{fromAgent: agR, toAgent: agLead, at: t0.Add(time.Duration(2*i+1) * time.Minute)},
		)
	}
	return out
}

func TestLoopPairRoundtripsGolden(t *testing.T) {
	t0 := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

	t.Run(caseName("E4-03", "sixth_roundtrip_pauses_session"), func(t *testing.T) {
		h := pairPingPong(5, t0)
		next := hop{fromAgent: agR, toAgent: agLead, at: t0.Add(11 * time.Minute)}
		v := mustLoop(t, h, next, defaultLimits(), next.at)

		if v.Allowed || v.TaskCreated {
			t.Error("the 6th consecutive Lead↔R roundtrip exceeds max_pair_roundtrips 5")
		}
		if v.SessionState != "paused" || v.Detail != "pair_roundtrips" {
			t.Errorf("session=%q detail=%q, want paused/pair_roundtrips", v.SessionState, v.Detail)
		}
	})

	t.Run(caseName("E4-04", "third_party_agent_resets_the_pair_counter"), func(t *testing.T) {
		h := pairPingPong(4, t0)
		// W steps in — the Lead↔R pair is no longer "consecutive".
		h = append(h, hop{fromAgent: agW, toAgent: agLead, at: t0.Add(9 * time.Minute)})
		next := hop{fromAgent: agLead, toAgent: agR, at: t0.Add(10 * time.Minute)}
		v := mustLoop(t, h, next, defaultLimits(), next.at)

		if !v.Allowed || !v.TaskCreated {
			t.Error("a third agent resets the pair counter — the next Lead↔R hop is allowed")
		}
		if v.PairRoundtrips > 1 {
			t.Errorf("pair_roundtrips = %d after a third party, want the count restarted", v.PairRoundtrips)
		}
	})

	t.Run(caseName("E4-05", "human_message_resets_the_pair_counter"), func(t *testing.T) {
		h := pairPingPong(4, t0)
		h = append(h, humanHop(agLead, t0.Add(9*time.Minute)))
		next := hop{fromAgent: agLead, toAgent: agR, at: t0.Add(10 * time.Minute)}
		v := mustLoop(t, h, next, defaultLimits(), next.at)

		if !v.Allowed || !v.TaskCreated {
			t.Error("a human message resets the pair counter too")
		}
		if v.PairRoundtrips > 1 {
			t.Errorf("pair_roundtrips = %d after a human message, want the count restarted", v.PairRoundtrips)
		}
	})

	t.Run(caseName("E4-09", "workspace_setting_overrides_the_default"), func(t *testing.T) {
		lim := defaultLimits()
		lim.MaxPairRoundtrips = 2
		h := pairPingPong(2, t0)
		next := hop{fromAgent: agR, toAgent: agLead, at: t0.Add(5 * time.Minute)}
		v := mustLoop(t, h, next, lim, next.at)

		if v.Allowed || v.TaskCreated {
			t.Error("max_pair_roundtrips=2 must pause on the 3rd roundtrip, not the 6th")
		}
		if v.SessionState != "paused" || v.Detail != "pair_roundtrips" {
			t.Errorf("session=%q detail=%q, want paused/pair_roundtrips", v.SessionState, v.Detail)
		}
	})
}

// ---------------------------------------------------------------------------
// E4-06, E4-07 — hops per hour (rolling window, humans excluded)
// ---------------------------------------------------------------------------

// agentHops builds n agent→agent triggers inside one hour, alternating so the
// pair-roundtrip limit is NOT what trips: three agents rotate.
func agentHops(n int, t0 time.Time) []hop {
	ring := []uuid.UUID{agLead, agR, agW}
	out := []hop{}
	for i := 0; i < n; i++ {
		out = append(out, hop{
			fromAgent: ring[i%len(ring)],
			toAgent:   ring[(i+1)%len(ring)],
			at:        t0.Add(time.Duration(i) * 30 * time.Second),
		})
	}
	return out
}

func TestLoopHopsPerHourGolden(t *testing.T) {
	t0 := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

	t.Run(caseName("E4-06", "sixty_first_agent_hop_in_an_hour_pauses"), func(t *testing.T) {
		h := agentHops(60, t0)
		// Humans do not count toward the window (FR-3.5) — adding some must not
		// change the verdict.
		h = append(h, humanHop(agLead, t0.Add(31*time.Minute)))
		next := hop{fromAgent: agW, toAgent: agLead, at: t0.Add(35 * time.Minute)}
		v := mustLoop(t, h, next, defaultLimits(), next.at)

		if v.Allowed || v.TaskCreated {
			t.Error("the 61st agent→agent trigger in one hour exceeds max_hops_per_hour 60")
		}
		if v.SessionState != "paused" || v.Detail != "hops_per_hour" {
			t.Errorf("session=%q detail=%q, want paused/hops_per_hour", v.SessionState, v.Detail)
		}
		if v.HopsThisWindow != 60 {
			t.Errorf("hops_this_window = %d, want 60 — human messages must not be counted", v.HopsThisWindow)
		}
	})

	t.Run(caseName("E4-07", "next_window_starts_a_fresh_count"), func(t *testing.T) {
		h := agentHops(60, t0)
		// Same history, but the next trigger lands beyond the rolling hour.
		next := hop{fromAgent: agW, toAgent: agLead, at: t0.Add(90 * time.Minute)}
		v := mustLoop(t, h, next, defaultLimits(), next.at)

		if !v.Allowed || !v.TaskCreated {
			t.Error("the hour window rolls — a trigger 90m later must be allowed")
		}
		if v.HopsThisWindow > 1 {
			t.Errorf("hops_this_window = %d in the new window, want the old hops excluded", v.HopsThisWindow)
		}
	})
}

// ---------------------------------------------------------------------------
// E4-08 — no content-based suppression
// ---------------------------------------------------------------------------

func TestLoopNoContentHeuristicGolden(t *testing.T) {
	t0 := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

	t.Run(caseName("E4-08", "short_message_without_question_still_triggers"), func(t *testing.T) {
		// PRD FR-3.5: "서버측 억제는 구조적 규칙으로만 한다." A terse message with
		// no question mark and no digits is a perfectly normal trigger; the
		// "empty acknowledgement" rule lives in the prompt (§8.3), not here.
		next := hop{fromAgent: agR, toAgent: agQA, at: t0}
		v := mustLoop(t, nil, next, defaultLimits(), t0)
		if !v.Allowed || !v.TaskCreated {
			t.Error("no content heuristic may suppress a trigger — limits are structural only")
		}

		// And the routing layer agrees: the same message routes by rule 2.
		d := Decide(Input{
			Content:      MentionLink("QA", agQA) + " 리뷰 부탁해",
			AuthorType:   "agent",
			Participants: sessionRoster(),
		})
		if len(d.Triggers) != 1 || d.Triggers[0].AgentID != agQA {
			t.Errorf("router triggers = %v, want exactly QA", d.Triggers)
		}
	})
}
