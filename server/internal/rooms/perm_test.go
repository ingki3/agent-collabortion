package rooms

import (
	"fmt"
	"testing"
)

// TestJudgeTable is every combination of the four facts (2 × 3 × 2 × 2 = 24),
// each row written out rather than derived — a table that computes its own
// expectation proves only that it agrees with itself.
func TestJudgeTable(t *testing.T) {
	const (
		N = NotMember
		M = Member
		L = Left
	)
	allow := func(viaLink bool) Verdict { return Verdict{Allowed: true, ViaLink: viaLink} }
	deny := func(r Reason) Verdict { return Verdict{Reason: r} }
	left := Verdict{Reason: OriginatorLeft, RevealRoom: true}

	rows := []struct {
		has        bool
		orig       Membership
		agent, lnk bool
		want       Verdict
	}{
		// no originator: nothing else matters, and nothing about the target leaks.
		{false, N, false, false, deny(NoOriginator)},
		{false, N, false, true, deny(NoOriginator)},
		{false, N, true, false, deny(NoOriginator)},
		{false, N, true, true, deny(NoOriginator)},
		{false, M, false, false, deny(NoOriginator)},
		{false, M, false, true, deny(NoOriginator)},
		{false, M, true, false, deny(NoOriginator)},
		{false, M, true, true, deny(NoOriginator)},
		{false, L, false, false, deny(NoOriginator)},
		{false, L, false, true, deny(NoOriginator)},
		{false, L, true, false, deny(NoOriginator)},
		{false, L, true, true, deny(NoOriginator)},
		// originator never a participant (or the room does not exist): hidden,
		// whatever the agent's own standing.
		{true, N, false, false, deny(OriginatorNotParticipant)},
		{true, N, false, true, deny(OriginatorNotParticipant)},
		{true, N, true, false, deny(OriginatorNotParticipant)},
		{true, N, true, true, deny(OriginatorNotParticipant)},
		// originator a participant: condition 2 decides.
		{true, M, false, false, deny(AgentNotAllowed)},
		{true, M, false, true, allow(true)},
		{true, M, true, false, allow(false)},
		{true, M, true, true, allow(false)},
		// originator left: named only when leaving is what blocked it.
		{true, L, false, false, deny(OriginatorNotParticipant)},
		{true, L, false, true, left},
		{true, L, true, false, left},
		{true, L, true, true, left},
	}
	if len(rows) != 24 {
		t.Fatalf("table has %d rows, want all 24 combinations", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		key := fmt.Sprint(r.has, r.orig, r.agent, r.lnk)
		if seen[key] {
			t.Fatalf("duplicate row %s", key)
		}
		seen[key] = true
		got := Judge(Facts{HasOriginator: r.has, Originator: r.orig, AgentParticipant: r.agent, Linked: r.lnk})
		if got != r.want {
			t.Errorf("Judge(has=%v orig=%v agent=%v link=%v) = %+v, want %+v", r.has, r.orig, r.agent, r.lnk, got, r.want)
		}
	}
}

// TestJudgeNeverRevealsWithoutReason pins the hiding rule across the table:
// only originator_left may name the room.
func TestJudgeNeverRevealsWithoutReason(t *testing.T) {
	for _, has := range []bool{false, true} {
		for _, o := range []Membership{NotMember, Member, Left} {
			for _, a := range []bool{false, true} {
				for _, l := range []bool{false, true} {
					v := Judge(Facts{HasOriginator: has, Originator: o, AgentParticipant: a, Linked: l})
					if v.RevealRoom != (v.Reason == OriginatorLeft) {
						t.Errorf("%+v: RevealRoom must be exactly originator_left", v)
					}
					if v.Allowed != (v.Reason == "") {
						t.Errorf("%+v: an allowed verdict has no reason and a denied one has one", v)
					}
				}
			}
		}
	}
}
