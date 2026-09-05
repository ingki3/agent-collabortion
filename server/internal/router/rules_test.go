package router

import (
	"testing"

	"github.com/google/uuid"
)

// EVAL E1 golden rows for the P1 rules (2 and 6).
func TestDecide(t *testing.T) {
	lead, r, w, x := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	roster := []Participant{{lead, "Lead"}, {r, "R"}, {w, "W"}}
	m := func(name string, id uuid.UUID) string { return MentionLink(name, id) }

	cases := []struct {
		name     string
		in       Input
		triggers []Trigger
		warnings []string
	}{
		{"E1-02 explicit mention → one task, rule 2",
			Input{Content: m("R", r) + " 시장 규모 조사해줘", AuthorType: "user", Participants: roster, AssigneeAgentID: &lead},
			[]Trigger{{r, 2}}, nil},
		{"E1-03 duplicate mention merges",
			Input{Content: m("R", r) + " " + m("R", r) + " 조사", AuthorType: "user", Participants: roster, AssigneeAgentID: &lead},
			[]Trigger{{r, 2}}, nil},
		{"E1-04 non-participant → no task, warning, no assignee fallback",
			Input{Content: m("X", x) + " 도와줘", AuthorType: "user", Participants: roster, AssigneeAgentID: &lead},
			[]Trigger{}, []string{"not_participant"}},
		{"E1-11 no mention → assignee, rule 6",
			Input{Content: "이제 시작하자", AuthorType: "user", Participants: roster, AssigneeAgentID: &lead},
			[]Trigger{{lead, 6}}, nil},
		{"two distinct mentions → two tasks in order",
			Input{Content: m("R", r) + " and " + m("W", w), AuthorType: "user", Participants: roster, AssigneeAgentID: &lead},
			[]Trigger{{r, 2}, {w, 2}}, nil},
		{"agent author without mention → nothing (rule 6 is user-only)",
			Input{Content: "조사 결과입니다", AuthorType: "agent", Participants: roster, AssigneeAgentID: &lead},
			[]Trigger{}, nil},
		{"agent author with mention → rule 2",
			Input{Content: m("W", w) + " 초안 부탁", AuthorType: "agent", Participants: roster, AssigneeAgentID: &lead},
			[]Trigger{{w, 2}}, nil},
		{"suppress_agent_ids removes a trigger (FR-3.6)",
			Input{Content: m("R", r) + " " + m("W", w), AuthorType: "user", Participants: roster, AssigneeAgentID: &lead, Suppress: []uuid.UUID{w}},
			[]Trigger{{r, 2}}, nil},
		{"no assignee → nothing",
			Input{Content: "hello", AuthorType: "user", Participants: roster},
			[]Trigger{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.in)
			if len(d.Triggers) != len(tc.triggers) {
				t.Fatalf("triggers = %+v, want %+v", d.Triggers, tc.triggers)
			}
			for i := range tc.triggers {
				if d.Triggers[i] != tc.triggers[i] {
					t.Fatalf("trigger[%d] = %+v, want %+v", i, d.Triggers[i], tc.triggers[i])
				}
			}
			if len(d.Warnings) != len(tc.warnings) {
				t.Fatalf("warnings = %+v, want codes %v", d.Warnings, tc.warnings)
			}
			for i, code := range tc.warnings {
				if d.Warnings[i].Code != code {
					t.Fatalf("warning[%d] = %s, want %s", i, d.Warnings[i].Code, code)
				}
			}
		})
	}
}

func TestParseMentions(t *testing.T) {
	id := uuid.New()
	ms := ParseMentions("hi " + MentionLink("Lead", id) + " and [@김민수](mention://user/u1) and [@all](mention://all/all)")
	if len(ms) != 3 || ms[0].Kind != "agent" || ms[0].Id != id.String() || *ms[0].DisplayName != "Lead" || ms[1].Kind != "user" || ms[2].Kind != "all" {
		t.Fatalf("unexpected mentions: %+v", ms)
	}
	if len(ParseMentions("@Lead plain text")) != 0 {
		t.Fatal("plain @ must not be a mention (FR-3.2 link form only)")
	}
}
