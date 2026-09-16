package httpapi

import (
	"testing"
)

// TestS85TreeCodesOnTheWire: updateSession and createSession answer the
// reason-specific completion_condition code (S-85, PR #233 리뷰 NN2), not
// `criteria_met_alone` for everything.
func TestS85TreeCodesOnTheWire(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("user_approval")))
	for _, c := range []struct {
		name string
		tree map[string]any
		code string
	}{
		{"no conditions → required", map[string]any{"op": "and", "conditions": []map[string]any{}}, "required"},
		{"unknown type → unknown_type", and(atom("vibes")), "unknown_type"},
		{"criteria_met alone → criteria_met_alone", and(atom("criteria_met")), "criteria_met_alone"},
	} {
		t.Run("patch: "+c.name, func(t *testing.T) {
			if st, out := f.patchCond(t, f.api, sess, c.tree); st != 422 || fieldCode(out, "completion_condition") != c.code {
				t.Fatalf("= %d %v, want 422 %s", st, out, c.code)
			}
		})
		t.Run("create: "+c.name, func(t *testing.T) {
			if st, out := f.createWith(c.tree, f.lead); st != 422 || fieldCode(out, "completion_condition") != c.code {
				t.Fatalf("= %d %v, want 422 %s", st, out, c.code)
			}
		})
	}
}

// TestS85SessionAgentsAssigneeFallback is PR #233 리뷰 NN1: a session whose
// assignee has NO session_participant row (a participant removed after the
// session was set up, the assignee kept) still counts the assignee as a
// participant for the reviewer guard — designating them as reviewer is 200.
// Deleting the `if assignee != nil` line in sessionAgents turns this into
// `reviewer_not_participant`.
func TestS85SessionAgentsAssigneeFallback(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("user_approval")))
	// The assignee (Lead) loses their participant row; the session still
	// names them as assignee.
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM session_participant WHERE session_id = $1 AND agent_id = $2`, sess, f.leadUUID); err != nil {
		t.Fatal(err)
	}
	if st, out := f.patchCond(t, f.api, sess, and(atom("agent_approval", "agent_id", f.lead))); st != 200 {
		t.Fatalf("assignee as reviewer without a participant row = %d %v, want 200 (S-85 NN1)", st, out)
	}
	// The control: an agent who is neither participant nor assignee is
	// refused — the fallback is for the assignee alone.
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM session_participant WHERE session_id = $1 AND agent_id = $2`, sess, f.rUUID); err != nil {
		t.Fatal(err)
	}
	if st, out := f.patchCond(t, f.api, sess, and(atom("agent_approval", "agent_id", f.r))); st != 422 || fieldCode(out, "completion_condition/conditions/0/agent_id") != "reviewer_not_participant" {
		t.Fatalf("non-participant, non-assignee reviewer = %d %v, want 422 reviewer_not_participant", st, out)
	}
}
