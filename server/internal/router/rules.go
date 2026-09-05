// Package router applies PRD FR-3.3 to a posted message and turns the result
// into tasks (FR-3.4 per-lane coalescing).
//
// P1 scope (plan/P1_TASKS.md T-S1): rule 2 (explicit agent mentions → one
// task per distinct participant, non-participants warned) and rule 6 (any
// other user message → session assignee). Rules 1, 3, 4, 5, 7, 8 are P2.
package router

import (
	"regexp"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// mentionRe matches the FR-3.2 link form: [@Name](mention://agent/<id>).
var mentionRe = regexp.MustCompile(`\[@([^\]]*)\]\(mention://(agent|user|all)/([^)\s]+)\)`)

// ParseMentions extracts mentions in document order (duplicates kept).
func ParseMentions(content string) []gen.Mention {
	out := []gen.Mention{}
	for _, m := range mentionRe.FindAllStringSubmatch(content, -1) {
		name := m[1]
		out = append(out, gen.Mention{Kind: gen.MentionKind(m[2]), Id: m[3], DisplayName: &name})
	}
	return out
}

// MentionLink renders the roster link for an agent (FR-3.2).
func MentionLink(name string, id uuid.UUID) string {
	return "[@" + name + "](mention://agent/" + id.String() + ")"
}

// Participant is one session_participant the rules can trigger.
type Participant struct {
	AgentID uuid.UUID
	Name    string
}

// Input is everything Decide needs; it is pure so the table tests need no DB.
type Input struct {
	Content         string
	AuthorType      string // user | agent | system
	Participants    []Participant
	AssigneeAgentID *uuid.UUID
	Suppress        []uuid.UUID // MessageCreate.suppress_agent_ids (FR-3.6)
}

// Trigger is one agent to create/merge a task for, with the rule that fired.
type Trigger struct {
	AgentID uuid.UUID
	Rule    int
}

// Warning mirrors TriggerPreview.warnings[].
type Warning struct {
	Code    string
	Message string
	AgentID *uuid.UUID
}

type Decision struct {
	Mentions []gen.Mention
	Triggers []Trigger
	Warnings []Warning
}

// Decide applies rule 2 then rule 6.
func Decide(in Input) Decision {
	d := Decision{Mentions: ParseMentions(in.Content), Triggers: []Trigger{}, Warnings: []Warning{}}
	participants := map[uuid.UUID]Participant{}
	for _, p := range in.Participants {
		participants[p.AgentID] = p
	}
	suppressed := map[uuid.UUID]bool{}
	for _, id := range in.Suppress {
		suppressed[id] = true
	}

	// Rule 2: explicit agent mentions. Duplicate mentions of one agent merge
	// (E1-03); non-participants are posted but warned, never triggered (E1-04).
	seen := map[uuid.UUID]bool{}
	agentMentioned := false
	for _, m := range d.Mentions {
		if m.Kind != gen.MentionKindAgent {
			continue
		}
		agentMentioned = true
		id, err := uuid.Parse(m.Id)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := participants[id]; !ok {
			name := m.Id
			if m.DisplayName != nil && *m.DisplayName != "" {
				name = *m.DisplayName
			}
			aid := id
			d.Warnings = append(d.Warnings, Warning{Code: "not_participant", Message: name + "은(는) 이 세션 참여자가 아닙니다", AgentID: &aid})
			continue
		}
		if suppressed[id] {
			continue
		}
		d.Triggers = append(d.Triggers, Trigger{AgentID: id, Rule: 2})
	}
	if agentMentioned {
		return d
	}

	// Rule 6: any other user message → session assignee (E1-11).
	if in.AuthorType == "user" && in.AssigneeAgentID != nil && !suppressed[*in.AssigneeAgentID] {
		if _, ok := participants[*in.AssigneeAgentID]; ok {
			d.Triggers = append(d.Triggers, Trigger{AgentID: *in.AssigneeAgentID, Rule: 6})
		}
	}
	return d
}
