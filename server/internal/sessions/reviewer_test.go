package sessions

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// S-84 pure halves: the reviewer guard over a participant set, and the
// per-atom verdict buildProgress writes. The HTTP paths (422 codes, the SSE
// frame, the rescue via updateSession) are in httpapi/p5_reviewer_required_test.go.

func TestValidateReviewers(t *testing.T) {
	in := uuid.New()
	out := uuid.New()
	participant := func(id uuid.UUID) bool { return id == in }
	cases := []struct {
		name string
		tree Tree
		want map[string]string // field → code
	}{
		{"agent_approval without agent_id", Tree{Op: "AND", Conditions: []Condition{{Type: CondArtifactSubmitted, Who: "assignee"}, {Type: CondAgentApproval}}},
			map[string]string{"completion_condition/conditions/1/agent_id": "reviewer_required"}},
		{"agent_approval naming an outsider", Tree{Op: "AND", Conditions: []Condition{{Type: CondAgentApproval, Agent: &out}}},
			map[string]string{"completion_condition/conditions/0/agent_id": "reviewer_not_participant"}},
		{"artifact_submitted naming an outsider", Tree{Op: "AND", Conditions: []Condition{{Type: CondArtifactSubmitted, Agent: &out}, {Type: CondUserApproval}}},
			map[string]string{"completion_condition/conditions/0/agent_id": "reviewer_not_participant"}},
		{"artifact_submitted by role needs no agent_id", Tree{Op: "AND", Conditions: []Condition{{Type: CondArtifactSubmitted, Who: "assignee"}, {Type: CondUserApproval}}}, nil},
		{"a participant reviewer", Tree{Op: "AND", Conditions: []Condition{{Type: CondAgentApproval, Agent: &in}}}, nil},
		{"user_approval · manual · criteria_met name nobody", Tree{Op: "AND", Conditions: []Condition{{Type: CondUserApproval}, {Type: CondManual}, {Type: CondCriteriaMet}}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]string{}
			for _, e := range ValidateReviewers(c.tree, participant) {
				got[e.Field] = e.Code
			}
			if len(got) != len(c.want) {
				t.Fatalf("errors = %v, want %v", got, c.want)
			}
			for f, code := range c.want {
				if got[f] != code {
					t.Fatalf("errors = %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestBuildProgressBlockedReason(t *testing.T) {
	lead, r, gone := uuid.New(), uuid.New(), uuid.New()
	facts := completionFacts{Assignee: &lead, Agents: map[uuid.UUID]agentFact{
		lead: {Name: "Lead", Participant: true},
		r:    {Name: "R", Participant: true, Archived: true},
		gone: {Name: "Gone", Participant: false},
	}}
	tree := func(atoms ...map[string]any) []byte {
		b, _ := json.Marshal(map[string]any{"op": "and", "conditions": atoms})
		return b
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	row := func(t *testing.T, treeRaw, met []byte, i int) map[string]any {
		t.Helper()
		b, _ := json.Marshal(buildProgress(treeRaw, met, facts))
		var p struct {
			Conditions []map[string]any `json:"conditions"`
		}
		_ = json.Unmarshal(b, &p)
		return p.Conditions[i]
	}

	t.Run("no agent_id → reviewer_missing, nothing named", func(t *testing.T) {
		got := row(t, tree(map[string]any{"type": "agent_approval"}), []byte(`{}`), 0)
		if str(got, "blocked_reason") != "reviewer_missing" || got["agent_id"] != nil || got["agent_name"] != nil || got["next_actor"] != nil {
			t.Fatalf("row = %v", got)
		}
	})
	t.Run("not a participant → reviewer_not_participant, still named", func(t *testing.T) {
		got := row(t, tree(map[string]any{"type": "agent_approval", "agent_id": gone.String()}), []byte(`{}`), 0)
		if str(got, "blocked_reason") != "reviewer_not_participant" || str(got, "agent_name") != "Gone" {
			t.Fatalf("row = %v", got)
		}
	})
	t.Run("unknown agent → reviewer_not_participant", func(t *testing.T) {
		got := row(t, tree(map[string]any{"type": "agent_approval", "agent_id": uuid.NewString()}), []byte(`{}`), 0)
		if str(got, "blocked_reason") != "reviewer_not_participant" || got["agent_name"] != nil {
			t.Fatalf("row = %v", got)
		}
	})
	t.Run("archived → agent_archived", func(t *testing.T) {
		got := row(t, tree(map[string]any{"type": "artifact_submitted", "agent_id": r.String()}), []byte(`{}`), 0)
		if str(got, "blocked_reason") != "agent_archived" || str(got, "agent_name") != "R" {
			t.Fatalf("row = %v", got)
		}
	})
	t.Run("who: assignee resolves to the assignee and is that agent's turn", func(t *testing.T) {
		got := row(t, tree(map[string]any{"type": "artifact_submitted", "who": "assignee"}), []byte(`{}`), 0)
		if got["blocked_reason"] != nil || str(got, "agent_id") != lead.String() || str(got, "agent_name") != "Lead" || str(got, "next_actor") != "Lead" {
			t.Fatalf("row = %v", got)
		}
	})
	t.Run("a met atom is neither blocked nor anyone's turn", func(t *testing.T) {
		got := row(t, tree(map[string]any{"type": "agent_approval"}), []byte(`{"agent_approval":true}`), 0)
		if got["met"] != true || got["blocked_reason"] != nil || got["next_actor"] != nil {
			t.Fatalf("row = %v", got)
		}
	})
	t.Run("director · platform atoms", func(t *testing.T) {
		raw := tree(map[string]any{"type": "user_approval"}, map[string]any{"type": "manual"}, map[string]any{"type": "criteria_met"})
		for i, want := range []string{NextActorDirector, NextActorDirector, NextActorPlatform} {
			if got := row(t, raw, []byte(`{}`), i); str(got, "next_actor") != want || got["blocked_reason"] != nil {
				t.Fatalf("row %d = %v, want next_actor %s", i, got, want)
			}
		}
	})
}
