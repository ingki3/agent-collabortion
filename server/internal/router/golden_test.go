// Golden tables for the router: PRD FR-3.3 rules 1–8 (E1) and the lane
// resolution + coalescing rules (E2). Written by the Reviewer BEFORE the
// implementation (PLAN §10.1, P2a) so T-S2 codes against a table it did not
// author.
//
// Every case name carries its EVAL row id, e.g.
//
//	TestRouterGolden/E1_15_rule8_suppresses_only_delegator
//
// The axes of the table are PLAN.md:124:
//
//	(message kind × author × mentions × thread position × lane state)
//	  → (triggers, lane, coalesced?)
//
// HOW THIS FILE FAILS TODAY. Rules 2 and 6 are implemented (P1, T-S1), so
// their rows pass through the adapter below. Rules 1, 3, 4, 5, 7, 8 and every
// lane rule are P2 — those rows fail, and that is the point of the build tag.
// T-S2 drops `-tags p2golden` for the parts it lands.
package router

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Fixed identities. Stable UUIDs keep failure output readable.
// ---------------------------------------------------------------------------

var (
	agLead = uuid.MustParse("a0000000-0000-4000-8000-000000000001")
	agR    = uuid.MustParse("a0000000-0000-4000-8000-000000000002")
	agW    = uuid.MustParse("a0000000-0000-4000-8000-000000000003")
	agQA   = uuid.MustParse("a0000000-0000-4000-8000-000000000004")
	agX    = uuid.MustParse("a0000000-0000-4000-8000-000000000005") // workspace agent, NOT a participant
	usrDir = uuid.MustParse("b0000000-0000-4000-8000-000000000001")
	usrM2  = uuid.MustParse("b0000000-0000-4000-8000-000000000002")
)

func agentName(id uuid.UUID) string {
	switch id {
	case agLead:
		return "Lead"
	case agR:
		return "R"
	case agW:
		return "W"
	case agQA:
		return "QA"
	case agX:
		return "X"
	}
	return id.String()
}

// sessionRoster is the E1/E2 premise: "S active, 참여자 Lead·R·W" (+QA where
// the row needs a third agent to prove rule 8 scoping).
func sessionRoster() []Participant {
	return []Participant{
		{AgentID: agLead, Name: "Lead"},
		{AgentID: agR, Name: "R"},
		{AgentID: agW, Name: "W"},
		{AgentID: agQA, Name: "QA"},
	}
}

func userLink(name string, id uuid.UUID) string {
	return "[@" + name + "](mention://user/" + id.String() + ")"
}

func allLink() string { return "[@all](mention://all/all)" }

// ---------------------------------------------------------------------------
// The golden row.
// ---------------------------------------------------------------------------

// routeCase is one row of the FR-3.3 table. Zero values mean "not part of the
// premise": no thread, no lanes, no delegator.
type routeCase struct {
	eval    string // EVAL row id, e.g. "E1-15"
	name    string // snake_case case name
	content string

	// author
	authorKind string    // "user" | "agent"
	authorUser uuid.UUID // set when authorKind == "user"
	authorAgt  uuid.UUID // set when authorKind == "agent"

	// thread position (FR-3.3 rule 5)
	replyToAgent uuid.UUID // the agent that owns the message being replied to
	threadOwner  uuid.UUID // the agent that owns the thread root

	// lane / delegation state (FR-3.3 rule 8)
	authorLaneDelegator uuid.UUID // the delegator of the author's lane
	joinGroupFired      bool      // has the author's join group already fired?

	assignee *uuid.UUID
	suppress []uuid.UUID

	want routeWant
}

type routeWant struct {
	// triggers is the exact expected set, agent → rule that fired.
	triggers map[uuid.UUID]int
	// warnings is the expected warning codes (order independent).
	warnings []string
	// fallback is rule 7's deferred assignee task, when one is expected.
	fallback *fallbackWant
}

type fallbackWant struct {
	agent uuid.UUID
	delay time.Duration
}

func trig(pairs ...any) map[uuid.UUID]int {
	m := map[uuid.UUID]int{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i].(uuid.UUID)] = pairs[i+1].(int)
	}
	return m
}

// ---------------------------------------------------------------------------
// E1 — routing rules 1..8 (EVAL E1-01 … E1-20)
// ---------------------------------------------------------------------------

func routerGoldenTable() []routeCase {
	lead := agLead
	return []routeCase{
		{
			eval: "E1-01", name: "rule1_note_prefix_triggers_nothing",
			content:    "/note 회의록 정리",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			// Rule 1 wins over rule 6: a /note message is stored, never routed.
			want: routeWant{triggers: trig()},
		},
		{
			eval: "E1-02", name: "rule2_single_agent_mention",
			content:    MentionLink("R", agR) + " 시장 규모 조사해줘",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			want: routeWant{triggers: trig(agR, 2)},
		},
		{
			eval: "E1-03", name: "rule2_duplicate_mention_merges_to_one",
			content:    MentionLink("R", agR) + " " + MentionLink("R", agR) + " 조사",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			want: routeWant{triggers: trig(agR, 2)},
		},
		{
			eval: "E1-04", name: "rule2_non_participant_warns_and_does_not_trigger",
			content:    MentionLink("X", agX) + " 도와줘",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			// Posted, warned, never triggered — and rule 6 must NOT rescue it.
			want: routeWant{triggers: trig(), warnings: []string{"not_participant"}},
		},
		{
			eval: "E1-05", name: "rule3_at_all_suppresses_implicit_routing",
			content:    allLink() + " 진행 상황 공유",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			// @all suppresses rule 6; it does NOT fan out to every participant.
			want: routeWant{triggers: trig()},
		},
		{
			eval: "E1-06", name: "rule3_human_only_mention_suppresses_implicit_routing",
			content:    userLink("M2", usrM2) + " 확인 부탁",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			want: routeWant{triggers: trig()},
		},
		{
			eval: "E1-07", name: "rule4_agent_message_without_mention_triggers_nothing",
			content:    "조사 결과입니다…",
			authorKind: "agent", authorAgt: agR, assignee: &lead,
			// No implicit routing for agent authors — the join (FR-6.5) wakes Lead.
			want: routeWant{triggers: trig()},
		},
		{
			eval: "E1-08", name: "rule4_agent_message_with_mention_triggers",
			content:    MentionLink("W", agW) + " 초안 부탁",
			authorKind: "agent", authorAgt: agR, assignee: &lead,
			want: routeWant{triggers: trig(agW, 2)},
		},
		{
			eval: "E1-09", name: "rule5_reply_to_agent_message",
			content:    "고마워요, 조금 더 좁혀주세요",
			authorKind: "user", authorUser: usrDir, replyToAgent: agR, assignee: &lead,
			// Rule 5 routes to R; Lead is not triggered by rule 6.
			want: routeWant{triggers: trig(agR, 5)},
		},
		{
			eval: "E1-10", name: "rule5_reply_inside_thread_routes_to_thread_owner",
			content:    "여기 수정 부탁해요",
			authorKind: "user", authorUser: usrDir, threadOwner: agW, assignee: &lead,
			want: routeWant{triggers: trig(agW, 5)},
		},
		{
			eval: "E1-11", name: "rule6_plain_user_message_goes_to_assignee",
			content:    "이제 시작하자",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			want: routeWant{triggers: trig(agLead, 6)},
		},
		{
			eval: "E1-19", name: "priority_rule1_beats_rule2",
			content:    "/note " + MentionLink("R", agR) + " 이거 봐줘",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			// The /note prefix is checked before mentions are honoured.
			want: routeWant{triggers: trig()},
		},
		{
			eval: "E1-20", name: "priority_rule2_beats_rule3_at_all",
			content:    allLink() + " " + MentionLink("R", agR) + " 시작",
			authorKind: "user", authorUser: usrDir, assignee: &lead,
			// @all only suppresses IMPLICIT routing; an explicit mention still fires.
			want: routeWant{triggers: trig(agR, 2)},
		},

		// --- rule 8: a child lane mentioning its own delegator ----------------
		{
			eval: "E1-15", name: "rule8_child_does_not_trigger_its_delegator",
			content:    MentionLink("Lead", agLead) + " 완료했습니다",
			authorKind: "agent", authorAgt: agR,
			authorLaneDelegator: agLead, joinGroupFired: false,
			assignee: &lead,
			// Posted, carried by the join bundle, but no task for Lead.
			want: routeWant{triggers: trig()},
		},
		{
			eval: "E1-16", name: "rule8_suppresses_only_delegator_not_third_party",
			content:    MentionLink("Lead", agLead) + " " + MentionLink("QA", agQA) + " 확인 부탁",
			authorKind: "agent", authorAgt: agR,
			authorLaneDelegator: agLead, joinGroupFired: false,
			assignee: &lead,
			// Suppression scope is exactly one agent: the delegator.
			want: routeWant{triggers: trig(agQA, 2)},
		},
		{
			eval: "E1-17", name: "rule8_suppression_ends_once_join_group_fired",
			content:    MentionLink("Lead", agLead) + " 추가 질문",
			authorKind: "agent", authorAgt: agR,
			authorLaneDelegator: agLead, joinGroupFired: true,
			assignee: &lead,
			// After the join fires there is nothing left to bundle, so the
			// mention behaves normally (PRD FR-3.3 rule 8, 3rd bullet).
			want: routeWant{triggers: trig(agLead, 2)},
		},
	}
}

// TestRouterGolden drives the FR-3.3 table through the router's pure decision
// function. Rows for rules 2 and 6 pass against the P1 implementation; the
// rest are the specification T-S2 has to satisfy.
func TestRouterGolden(t *testing.T) {
	for _, c := range routerGoldenTable() {
		t.Run(caseName(c.eval, c.name), func(t *testing.T) {
			got := decideForCase(c)

			gotTrig := map[uuid.UUID]int{}
			for _, tr := range got.Triggers {
				if prev, dup := gotTrig[tr.AgentID]; dup {
					t.Errorf("agent %s triggered twice (rules %d and %d) — rule 2 merges duplicates",
						agentName(tr.AgentID), prev, tr.Rule)
				}
				gotTrig[tr.AgentID] = tr.Rule
			}
			if len(gotTrig) != len(c.want.triggers) {
				t.Errorf("trigger count = %d, want %d\n got: %s\nwant: %s",
					len(gotTrig), len(c.want.triggers), fmtTrig(gotTrig), fmtTrig(c.want.triggers))
			}
			for ag, wantRule := range c.want.triggers {
				gotRule, ok := gotTrig[ag]
				if !ok {
					t.Errorf("missing trigger for %s (want rule %d)", agentName(ag), wantRule)
					continue
				}
				if gotRule != wantRule {
					t.Errorf("%s triggered by rule %d, want rule %d", agentName(ag), gotRule, wantRule)
				}
			}
			for ag := range gotTrig {
				if _, ok := c.want.triggers[ag]; !ok {
					t.Errorf("unexpected trigger for %s (rule %d)", agentName(ag), gotTrig[ag])
				}
			}

			gotWarn := map[string]bool{}
			for _, w := range got.Warnings {
				gotWarn[w.Code] = true
			}
			for _, code := range c.want.warnings {
				if !gotWarn[code] {
					t.Errorf("missing warning %q (got %v)", code, keys(gotWarn))
				}
			}
			if len(gotWarn) != len(c.want.warnings) {
				t.Errorf("warning count = %d %v, want %d %v",
					len(gotWarn), keys(gotWarn), len(c.want.warnings), c.want.warnings)
			}
		})
	}
}

// decideForCase adapts a golden row to the router's input. It is an ADAPTER,
// not an implementation: every routing decision stays inside router.Decide.
// The fields Input cannot yet carry (thread position, author agent, lane
// delegator, join state) are listed in /tmp/p2a-report.md as required API.
// T-S2 landed them on Input, so the adapter now passes them through; not one
// expectation in the table above changed.
func decideForCase(c routeCase) Decision {
	in := Input{
		Content:         c.content,
		AuthorType:      c.authorKind,
		Participants:    sessionRoster(),
		AssigneeAgentID: c.assignee,
		Suppress:        c.suppress,
		JoinGroupFired:  c.joinGroupFired,
	}
	if c.authorAgt != uuid.Nil {
		in.AuthorAgentID = &c.authorAgt
	}
	if c.replyToAgent != uuid.Nil {
		in.ReplyToAgentID = &c.replyToAgent
	}
	if c.threadOwner != uuid.Nil {
		in.ThreadOwnerAgentID = &c.threadOwner
	}
	if c.authorLaneDelegator != uuid.Nil {
		in.AuthorLaneDelegatorID = &c.authorLaneDelegator
	}
	return Decide(in)
}

// ---------------------------------------------------------------------------
// E1-12 … E1-14 — rule 7, the 5-minute assignee fallback.
//
// Rule 7 is not a pure mention decision: it schedules a deferred task and
// cancels it when the primary agent answers. It needs the injected clock
// (daemon-protocol.md:163 — every time-dependent path goes through
// contracts/clock), so it cannot ride on Decide.
// ---------------------------------------------------------------------------

// fallbackPlan is the minimum the implementation must expose for E1-12..14.
// T-S2 provides it; until then the hook is nil and these rows fail loudly.
type fallbackPlan struct {
	// Scheduled is the deferred assignee task rule 7 creates, if any.
	Scheduled *fallbackWant
	// CancelledOnReply reports whether the primary agent's reply cancelled it.
	CancelledOnReply bool
	// PromotedAfter reports whether the delay elapsing promoted it to queued.
	PromotedAfter bool
}

// planFallback is wired by T-S2 to the real implementation. See the report:
// router.PlanFallback(ctx, sessionID, triggeredAgentID, assigneeID, now) …
var planFallback func(c routeCase, elapsed time.Duration, primaryReplied bool) fallbackPlan

func TestRouterFallbackGolden(t *testing.T) {
	lead := agLead
	base := routeCase{
		eval: "E1-12", content: "조금 더 좁혀주세요",
		authorKind: "user", authorUser: usrDir, replyToAgent: agR, assignee: &lead,
	}

	t.Run(caseName("E1-12", "rule7_schedules_deferred_assignee_fallback"), func(t *testing.T) {
		p := mustFallback(t, base, 0, false)
		if p.Scheduled == nil {
			t.Fatal("rule 7 must schedule a deferred assignee task alongside the rule 5 trigger")
		}
		if p.Scheduled.agent != agLead {
			t.Errorf("fallback agent = %s, want Lead (the assignee)", agentName(p.Scheduled.agent))
		}
		if p.Scheduled.delay != 5*time.Minute {
			t.Errorf("fallback delay = %s, want 5m (FR-3.3 rule 7)", p.Scheduled.delay)
		}
	})

	t.Run(caseName("E1-13", "rule7_primary_reply_within_5m_cancels_fallback"), func(t *testing.T) {
		p := mustFallback(t, base, 4*time.Minute, true)
		if !p.CancelledOnReply {
			t.Error("R replied at 4m — the deferred Lead task must be cancelled, not run")
		}
		if p.PromotedAfter {
			t.Error("a cancelled fallback must never be promoted to queued")
		}
	})

	t.Run(caseName("E1-14", "rule7_no_reply_promotes_fallback_at_5m"), func(t *testing.T) {
		p := mustFallback(t, base, 5*time.Minute, false)
		if !p.PromotedAfter {
			t.Error("no reply by 5m — the deferred Lead task must go deferred → queued")
		}
		if p.CancelledOnReply {
			t.Error("nothing replied, so nothing may be cancelled")
		}
	})
}

func mustFallback(t *testing.T, c routeCase, elapsed time.Duration, replied bool) fallbackPlan {
	t.Helper()
	if planFallback == nil {
		t.Fatalf("unimplemented: rule 7 fallback. T-S2 must provide a way to plan/cancel " +
			"the deferred assignee task and wire it to `planFallback` in this file " +
			"(see /tmp/p2a-report.md 'required API')")
	}
	return planFallback(c, elapsed, replied)
}

// ---------------------------------------------------------------------------
// E1-18 — blocked is the escape hatch from rule 8 suppression.
// ---------------------------------------------------------------------------

// blockedEffect is what `colab status set blocked` must produce (FR-6.2.1).
type blockedEffect struct {
	DelegatorWoken   bool
	QuestionCardID   uuid.UUID
	LaneBlockedMsgID uuid.UUID
	TurnEndRequired  bool
}

var setBlocked func(laneDelegator uuid.UUID, note string) blockedEffect

func TestBlockedWakesDelegatorGolden(t *testing.T) {
	t.Run(caseName("E1-18", "rule8_blocked_wakes_delegator_despite_suppression"), func(t *testing.T) {
		if setBlocked == nil {
			t.Fatalf("unimplemented: `colab status set blocked` server path (FR-6.2.1). " +
				"T-S2 must wire `setBlocked` (see /tmp/p2a-report.md)")
		}
		e := setBlocked(agLead, "범위?")
		if !e.DelegatorWoken {
			t.Error("blocked must wake the delegator immediately — mention suppression (rule 8) does not apply")
		}
		if e.QuestionCardID == uuid.Nil {
			t.Error("blocked must post a question card")
		}
		if !e.TurnEndRequired {
			t.Error("blocked returns turn_end_required: true (contracts/colab-cli.md §2.2)")
		}
	})
}

// ---------------------------------------------------------------------------
// E2 — lane resolution (4 rules) and coalescing (FR-3.4)
// ---------------------------------------------------------------------------

type laneState struct {
	id       uuid.UUID
	agent    uuid.UUID
	status   string // queued|running|waiting_human|blocked|paused|done|failed
	lastUsed time.Time
}

// laneCase is one row of the lane resolution table (PRD FR-3.3 "lane 해소 규칙").
type laneCase struct {
	eval, name string

	agent     uuid.UUID
	existing  []laneState
	isolation string // none | worktree | container

	// how the task arrived
	viaDelegate  bool      // `colab lane delegate` → always a new lane (rule 2)
	threadRoot   uuid.UUID // lane id that owns the thread root → rule 1
	topLevelMent bool      // top-level mention → rule 3
	forceNewLane bool      // "새 lane으로 보내기" toggle → skip rule 3

	delegatorTaskID uuid.UUID

	want laneWant
}

type laneWant struct {
	rule       int       // which lane rule decided
	laneID     uuid.UUID // uuid.Nil means "a brand new lane"
	newLane    bool
	reentry    bool   // done|blocked → running, reentry_count+1
	fromStatus string // the status it re-entered from
	delegated  bool   // delegated_from_task_id must be set
}

// laneResult is what the implementation must return for a resolution.
type laneResult struct {
	Rule            int
	LaneID          uuid.UUID
	Created         bool
	ReentryCount    int
	Status          string
	DelegatedFromID uuid.UUID
}

// resolveLane is wired by T-S2. The P1 router has an unexported resolveLane
// with a different shape (session, agent, profile, forceNew) that cannot
// express rules 1 and 2 — see the report.
var resolveLane func(laneCase) laneResult

func laneGoldenTable() []laneCase {
	t0 := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	l1 := uuid.MustParse("c0000000-0000-4000-8000-000000000001")
	l2 := uuid.MustParse("c0000000-0000-4000-8000-000000000002")
	delegTask := uuid.MustParse("d0000000-0000-4000-8000-000000000001")

	return []laneCase{
		{
			eval: "E2-01", name: "rule1_thread_reply_goes_to_the_threads_lane",
			agent: agR, isolation: "none",
			existing:   []laneState{{id: l1, agent: agR, status: "running", lastUsed: t0}},
			threadRoot: l1,
			want:       laneWant{rule: 1, laneID: l1, newLane: false},
		},
		{
			eval: "E2-02", name: "rule2_delegate_always_creates_a_lane",
			agent: agR, isolation: "none",
			viaDelegate: true, delegatorTaskID: delegTask,
			want: laneWant{rule: 2, newLane: true, delegated: true},
		},
		{
			eval: "E2-03", name: "rule2_delegate_creates_a_second_lane_while_first_runs",
			agent: agR, isolation: "none",
			existing:    []laneState{{id: l1, agent: agR, status: "running", lastUsed: t0}},
			viaDelegate: true, delegatorTaskID: delegTask,
			// Same agent, second delegation → a second, independent lane.
			want: laneWant{rule: 2, newLane: true, delegated: true},
		},
		{
			eval: "E2-04", name: "rule3_top_level_mention_reenters_done_lane",
			agent: agR, isolation: "none",
			existing:     []laneState{{id: l1, agent: agR, status: "done", lastUsed: t0}},
			topLevelMent: true,
			want:         laneWant{rule: 3, laneID: l1, newLane: false, reentry: true, fromStatus: "done"},
		},
		{
			eval: "E2-05", name: "rule3_top_level_mention_reenters_blocked_lane",
			agent: agR, isolation: "none",
			existing:     []laneState{{id: l1, agent: agR, status: "blocked", lastUsed: t0}},
			topLevelMent: true,
			want:         laneWant{rule: 3, laneID: l1, newLane: false, reentry: true, fromStatus: "blocked"},
		},
		{
			eval: "E2-06", name: "rule3_reuses_the_most_recent_lane",
			agent: agR, isolation: "none",
			existing: []laneState{
				{id: l1, agent: agR, status: "done", lastUsed: t0},
				{id: l2, agent: agR, status: "done", lastUsed: t0.Add(time.Hour)},
			},
			topLevelMent: true,
			want:         laneWant{rule: 3, laneID: l2, newLane: false, reentry: true, fromStatus: "done"},
		},
		{
			eval: "E2-07", name: "rule3_skipped_when_new_lane_toggle_is_on",
			agent: agR, isolation: "none",
			existing:     []laneState{{id: l1, agent: agR, status: "done", lastUsed: t0}},
			topLevelMent: true, forceNewLane: true,
			// The toggle is the human's only way to fan out (PRD t-2).
			want: laneWant{rule: 4, newLane: true},
		},
		{
			eval: "E2-08", name: "rule4_first_mention_with_no_lane_creates_one_undelegated",
			agent: agR, isolation: "none",
			topLevelMent: true,
			// delegated_from_task_id must stay empty: a human mention is not a delegation.
			want: laneWant{rule: 4, newLane: true, delegated: false},
		},
	}
}

func TestLaneResolutionGolden(t *testing.T) {
	for _, c := range laneGoldenTable() {
		t.Run(caseName(c.eval, c.name), func(t *testing.T) {
			if resolveLane == nil {
				t.Fatalf("unimplemented: lane resolution rules 1–4. T-S2 must expose a pure " +
					"resolver and wire `resolveLane` (see /tmp/p2a-report.md 'required API')")
			}
			got := resolveLane(c)

			if got.Rule != c.want.rule {
				t.Errorf("decided by lane rule %d, want rule %d", got.Rule, c.want.rule)
			}
			if got.Created != c.want.newLane {
				t.Errorf("created new lane = %v, want %v", got.Created, c.want.newLane)
			}
			if !c.want.newLane && got.LaneID != c.want.laneID {
				t.Errorf("lane = %s, want %s", short(got.LaneID), short(c.want.laneID))
			}
			if c.want.newLane {
				for _, ex := range c.existing {
					if got.LaneID == ex.id {
						t.Errorf("expected a NEW lane but got the existing %s", short(ex.id))
					}
				}
			}
			if c.want.reentry {
				if got.ReentryCount != 1 {
					t.Errorf("reentry_count = %d, want 1 (%s → running)", got.ReentryCount, c.want.fromStatus)
				}
				if got.Status != "running" {
					t.Errorf("status = %q, want running after re-entry", got.Status)
				}
			}
			if c.want.delegated && got.DelegatedFromID == uuid.Nil {
				t.Error("delegated_from_task_id must be set for `lane delegate` (FR-6.5 join group)")
			}
			if !c.want.delegated && got.DelegatedFromID != uuid.Nil {
				t.Errorf("delegated_from_task_id = %s, want empty — this task was not delegated",
					short(got.DelegatedFromID))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// E2-09 … E2-13 — FR-3.4: nothing cancels a running turn; queued tasks coalesce
// PER LANE; isolation decides workdir count, not lane count.
// ---------------------------------------------------------------------------

// arrivalResult is what the implementation must report when a message lands on
// a lane that is already busy.
type arrivalResult struct {
	CancelledRunningTurn bool
	QueuedTaskCount      int
	CoalescedMessageIDs  []uuid.UUID
	LaneOfQueuedTask     uuid.UUID
}

var messageArrives func(lane uuid.UUID, laneStatus string, msgIDs []uuid.UUID) arrivalResult

// workdirLayout is FR-6.1: lane and workdir are different things.
type workdirLayout struct {
	WorkdirCount   int
	ConcurrentRuns int
}

var layoutFor func(isolation string, lanes []laneState) workdirLayout

func TestCoalescingGolden(t *testing.T) {
	l1 := uuid.MustParse("c0000000-0000-4000-8000-000000000001")
	l2 := uuid.MustParse("c0000000-0000-4000-8000-000000000002")
	mA := uuid.MustParse("e0000000-0000-4000-8000-00000000000a")
	mB := uuid.MustParse("e0000000-0000-4000-8000-00000000000b")

	t.Run(caseName("E2-09", "running_turn_is_never_cancelled_by_a_new_message"), func(t *testing.T) {
		r := mustArrival(t, l1, "running", []uuid.UUID{mA})
		if r.CancelledRunningTurn {
			t.Error("FR-3.4 invariant: no message cancels a running turn — it queues")
		}
		if r.QueuedTaskCount != 1 {
			t.Errorf("queued tasks = %d, want 1", r.QueuedTaskCount)
		}
		if r.LaneOfQueuedTask != l1 {
			t.Errorf("queued on lane %s, want %s", short(r.LaneOfQueuedTask), short(l1))
		}
	})

	t.Run(caseName("E2-10", "second_message_coalesces_into_the_queued_task_in_order"), func(t *testing.T) {
		r := mustArrival(t, l1, "running", []uuid.UUID{mA, mB})
		if r.QueuedTaskCount != 1 {
			t.Errorf("queued tasks = %d, want 1 — the second message merges", r.QueuedTaskCount)
		}
		if len(r.CoalescedMessageIDs) != 2 {
			t.Fatalf("coalesced_message_ids = %v, want both messages", r.CoalescedMessageIDs)
		}
		if r.CoalescedMessageIDs[0] != mA || r.CoalescedMessageIDs[1] != mB {
			t.Errorf("coalesced order = %v, want arrival order [%s %s]",
				r.CoalescedMessageIDs, short(mA), short(mB))
		}
	})

	t.Run(caseName("E2-11", "coalescing_unit_is_the_lane_not_the_agent"), func(t *testing.T) {
		r1 := mustArrival(t, l1, "running", []uuid.UUID{mA})
		r2 := mustArrival(t, l2, "running", []uuid.UUID{mB})
		if r1.LaneOfQueuedTask == r2.LaneOfQueuedTask {
			t.Fatal("two lanes of the same agent must keep separate queues (PRD FR-3.4 'C')")
		}
		for _, id := range r1.CoalescedMessageIDs {
			if id == mB {
				t.Error("lane 1's queue absorbed lane 2's message — that is agent-level merging")
			}
		}
	})

	t.Run(caseName("E2-12", "worktree_isolation_shares_one_workdir_and_runs_sequentially"), func(t *testing.T) {
		l := mustLayout(t, "worktree", []laneState{
			{id: l1, agent: agR, status: "queued"}, {id: l2, agent: agR, status: "queued"},
		})
		if l.WorkdirCount != 1 {
			t.Errorf("workdirs = %d, want 1 (one per agent under worktree, FR-6.1)", l.WorkdirCount)
		}
		if l.ConcurrentRuns != 1 {
			t.Errorf("concurrent runs = %d, want 1 (sequential, FR-6.3)", l.ConcurrentRuns)
		}
	})

	t.Run(caseName("E2-13", "none_isolation_gives_each_lane_its_own_workdir"), func(t *testing.T) {
		l := mustLayout(t, "none", []laneState{
			{id: l1, agent: agR, status: "queued"}, {id: l2, agent: agR, status: "queued"},
		})
		if l.WorkdirCount != 2 {
			t.Errorf("workdirs = %d, want 2 (one per lane under none, FR-6.1)", l.WorkdirCount)
		}
		if l.ConcurrentRuns != 2 {
			t.Errorf("concurrent runs = %d, want 2", l.ConcurrentRuns)
		}
	})
}

func mustArrival(t *testing.T, lane uuid.UUID, status string, msgs []uuid.UUID) arrivalResult {
	t.Helper()
	if messageArrives == nil {
		t.Fatalf("unimplemented: per-lane coalescing (FR-3.4). T-S2 must wire `messageArrives` " +
			"(see /tmp/p2a-report.md 'required API')")
	}
	return messageArrives(lane, status, msgs)
}

func mustLayout(t *testing.T, isolation string, lanes []laneState) workdirLayout {
	t.Helper()
	if layoutFor == nil {
		t.Fatalf("unimplemented: lane/workdir binding (FR-6.1). T-S2 must wire `layoutFor` " +
			"(see /tmp/p2a-report.md 'required API')")
	}
	return layoutFor(isolation, lanes)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func caseName(eval, name string) string {
	return fmt.Sprintf("%s_%s", underscore(eval), name)
}

func underscore(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

func short(id uuid.UUID) string {
	if id == uuid.Nil {
		return "<nil>"
	}
	return id.String()[:8]
}

func fmtTrig(m map[uuid.UUID]int) string {
	if len(m) == 0 {
		return "{}"
	}
	s := "{"
	for ag, rule := range m {
		s += fmt.Sprintf(" %s:rule%d", agentName(ag), rule)
	}
	return s + " }"
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
