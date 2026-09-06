// Package router applies PRD FR-3.3 to a posted message and turns the result
// into tasks (FR-3.4 per-lane coalescing).
//
// All eight FR-3.3 rules live here, applied top down. Rules 2 and 6 landed in
// P1 (T-S1); rules 1, 3, 4, 5, 7 and 8 are P2 (T-S2). Rule 7 is the only one
// that is not a pure mention decision — it schedules a deferred task — so it
// takes the clock reading from the caller (PlanFallback).
package router

import (
	"regexp"
	"strings"

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
	Content      string
	AuthorType   string // user | agent | system
	Participants []Participant
	// AuthorAgentID is the agent that wrote the message (AuthorType == "agent").
	AuthorAgentID   *uuid.UUID
	AssigneeAgentID *uuid.UUID
	Suppress        []uuid.UUID // MessageCreate.suppress_agent_ids (FR-3.6)

	// Thread position (rule 5). ReplyToAgentID owns the message being replied
	// to; ThreadOwnerAgentID owns the thread root. Both nil for a top-level
	// message.
	ReplyToAgentID     *uuid.UUID
	ThreadOwnerAgentID *uuid.UUID

	// Rule 8. AuthorLaneDelegatorID is the delegator of the lane the author is
	// running in; the suppression lasts only until that lane's join group has
	// fired (PRD FR-3.3 rule 8, 3rd bullet).
	AuthorLaneDelegatorID *uuid.UUID
	JoinGroupFired        bool
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

// Decide applies FR-3.3 rules 1–8 in order. It never touches the clock or the
// database: rule 7's deferred task is planned separately by PlanFallback.
func Decide(in Input) Decision {
	d := Decision{Mentions: ParseMentions(in.Content), Triggers: []Trigger{}, Warnings: []Warning{}}

	// Rule 1: a /note message is stored, never routed. Checked before mentions
	// are honoured, so `/note @R …` triggers nothing (E1-01, E1-19).
	if isNote(in.Content) {
		return d
	}

	participants := map[uuid.UUID]Participant{}
	for _, p := range in.Participants {
		participants[p.AgentID] = p
	}
	suppressed := map[uuid.UUID]bool{}
	for _, id := range in.Suppress {
		suppressed[id] = true
	}
	// Rule 8: while the author's lane belongs to a join group that has not
	// fired yet, a mention of its delegator is posted but not triggered — the
	// join bundle carries it instead (E1-15). The scope is exactly that one
	// agent, so a third party mentioned alongside still fires (E1-16).
	if in.AuthorLaneDelegatorID != nil && !in.JoinGroupFired {
		suppressed[*in.AuthorLaneDelegatorID] = true
	}

	// Rule 2: explicit agent mentions. Duplicate mentions of one agent merge
	// (E1-03); non-participants are posted but warned, never triggered (E1-04).
	seen := map[uuid.UUID]bool{}
	agentMentioned, otherMentioned := false, false
	for _, m := range d.Mentions {
		if m.Kind != gen.MentionKindAgent {
			otherMentioned = true
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

	// Rule 3: @all or a human-only mention suppresses implicit routing. It is
	// only reached when no agent was mentioned, which is what makes rule 2 win
	// over it (E1-20).
	if otherMentioned {
		return d
	}

	// Rule 4: an agent's message without a mention triggers nothing — the join
	// (FR-6.5) is the one path back to the delegator (E1-07).
	if in.AuthorType != "user" {
		return d
	}

	// Rule 5: a reply goes to the agent that owns the message, or the agent
	// that owns the thread root (E1-09, E1-10).
	if ag := replyTarget(in); ag != nil {
		if _, ok := participants[*ag]; ok && !suppressed[*ag] {
			d.Triggers = append(d.Triggers, Trigger{AgentID: *ag, Rule: 5})
			return d
		}
	}

	// Rule 6: any other user message → session assignee (E1-11).
	if in.AssigneeAgentID != nil && !suppressed[*in.AssigneeAgentID] {
		if _, ok := participants[*in.AssigneeAgentID]; ok {
			d.Triggers = append(d.Triggers, Trigger{AgentID: *in.AssigneeAgentID, Rule: 6})
		}
	}
	return d
}

// isNote is rule 1's prefix test. Leading whitespace is tolerated so a pasted
// message is not routed by accident.
func isNote(content string) bool {
	c := strings.TrimLeft(content, " \t\r\n")
	return c == "/note" || strings.HasPrefix(c, "/note ") || strings.HasPrefix(c, "/note\n") || strings.HasPrefix(c, "/note\t")
}

// replyTarget is rule 5's subject: the replied-to agent first, then the thread
// root's owner.
func replyTarget(in Input) *uuid.UUID {
	if in.ReplyToAgentID != nil {
		return in.ReplyToAgentID
	}
	return in.ThreadOwnerAgentID
}
