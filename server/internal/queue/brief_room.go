package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// roomBrief is brief [4] 방 맥락 (harness §10 v0.9.0, PRD FR-4.1): the room
// part, and the mission part when the turn belongs to one.
//
// Everything here must be byte-stable for as long as the turn stays in the
// same room and the same mission (E12-11 v0.9.0) — which is why the mission
// carries the completion condition's SHAPE (what has to happen, and who does
// it) and not its progress: the progress moves during the mission and would
// break the cached prefix on every atom that is met. Progress goes to the turn
// prompt's <mission_progress> (Lead T-R3b 판정 1).
type roomBrief struct {
	Name      string
	About     string // the room's one-line description
	Owner     string
	Isolation string
	Mission   *missionBrief // nil: the turn is outside any mission
}

type missionBrief struct {
	ID       uuid.UUID
	Title    string
	Goal     string
	Criteria []string
	Director string
	// Op is the completion tree's combinator ("AND"/"OR", sessions.ParseTree).
	Op    string
	Atoms []missionAtom
}

// missionAtom is one completion atom as a sentence plus the parts of the
// progress row the turn prompt reads. Label is the stable part.
type missionAtom struct {
	Label string
	Met   bool
	Next  string // who acts next (progress next_actor), "" when met or blocked
	// Blocked is progress blocked_reason: the atom cannot be met as the
	// mission stands (S-84). Said in the turn prompt so the agent does not wait
	// for it.
	Blocked string
}

// renderRoom writes [4]. Outside a mission it is the room part only
// (harness §10 v0.9.0 「미션 밖 턴의 [4] 는 방 부분만」).
func renderRoom(r roomBrief) string {
	var b strings.Builder
	b.WriteString("[4] Room\n")
	fmt.Fprintf(&b, "Room: %s\n", r.Name)
	if r.About != "" {
		fmt.Fprintf(&b, "About: %s\n", r.About)
	}
	fmt.Fprintf(&b, "Owner: %s\nIsolation: %s\n", r.Owner, r.Isolation)
	if m := r.Mission; m != nil {
		fmt.Fprintf(&b, "\nMission this turn belongs to: %s\nGoal: %s\n", m.Title, m.Goal)
		if len(m.Criteria) > 0 {
			b.WriteString("Acceptance criteria:\n")
			for _, c := range m.Criteria {
				fmt.Fprintf(&b, "- %s\n", c)
			}
		}
		if len(m.Atoms) > 0 {
			fmt.Fprintf(&b, "Completion condition (%s):\n", opWords(m.Op, len(m.Atoms)))
			for _, a := range m.Atoms {
				fmt.Fprintf(&b, "- %s\n", a.Label)
			}
		}
		fmt.Fprintf(&b, "Director: %s\n", m.Director)
	}
	b.WriteString("\n")
	return b.String()
}

func opWords(op string, n int) string {
	if n == 1 {
		return "this one"
	}
	if op == "OR" {
		return "any one of these"
	}
	return "all of these"
}

// renderMissionProgress is the turn prompt's <mission_progress>: the part of
// the mission that moves. Empty outside a mission or when the mission has no
// completion atoms.
func renderMissionProgress(m *missionBrief, met, total int, satisfied bool) string {
	if m == nil || len(m.Atoms) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<mission_progress work=%q met=%d total=%d satisfied=%t>\n", m.ID, met, total, satisfied)
	for _, a := range m.Atoms {
		mark := " "
		if a.Met {
			mark = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s", mark, a.Label)
		switch {
		case a.Met:
		case a.Blocked != "":
			fmt.Fprintf(&b, " — cannot be met as the mission stands (%s); the Director has to change the condition", a.Blocked)
		case a.Next != "":
			fmt.Fprintf(&b, " — next: %s", a.Next)
		}
		b.WriteString("\n")
	}
	b.WriteString("</mission_progress>\n\n")
	return b.String()
}

// atomLabel says what one completion atom asks for. name is the resolved
// agent (progress agent_name); who is the tree's own shorthand for a role.
func atomLabel(typ, name, who string) string {
	by := ""
	switch {
	case name != "":
		by = " by " + name
	case who != "" && who != "assignee":
		by = " by the " + who
	case who == "assignee":
		by = " by the assignee"
	}
	switch typ {
	case sessions.CondArtifactSubmitted:
		return "an artifact submitted" + by
	case sessions.CondAgentApproval:
		return "a review approval" + by
	case sessions.CondUserApproval:
		return "the Director's approval"
	case sessions.CondCriteriaMet:
		return "the acceptance criteria met"
	case sessions.CondManual:
		return "the Director marks the mission complete"
	}
	return typ
}

// loadRoomBrief reads [4]'s material. workID nil → room part only.
func loadRoomBrief(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, workID *uuid.UUID, isolation string) (roomBrief, gen.CompletionProgress, error) {
	var r roomBrief
	var about string
	if err := tx.QueryRow(ctx, `
		SELECT s.name, s.description, u.display_name
		FROM room s JOIN app_user u ON u.id = s.owner_user_id WHERE s.id = $1`, roomID).Scan(&r.Name, &about, &r.Owner); err != nil {
		return r, gen.CompletionProgress{}, fmt.Errorf("queue: brief room: %w", err)
	}
	// "한 줄 설명": the description is one line by design (0025 filled it
	// from the old goal's first line); a stray newline must not open a line
	// the agent reads as another field.
	r.About = strings.Join(strings.Fields(about), " ")
	r.Isolation = isolation
	if workID == nil {
		return r, gen.CompletionProgress{}, nil
	}
	m := &missionBrief{ID: *workID}
	var tree []byte
	if err := tx.QueryRow(ctx, `
		SELECT wk.title, wk.goal, COALESCE(wk.acceptance_criteria, '{}'), wk.completion_condition, u.display_name
		FROM work wk JOIN room s ON s.id = wk.room_id
		JOIN app_user u ON u.id = COALESCE(wk.director_user_id, s.owner_user_id)
		WHERE wk.id = $1`, *workID).Scan(&m.Title, &m.Goal, &m.Criteria, &tree, &m.Director); err != nil {
		if isNoRows(err) {
			// The mission was deleted under a queued task (work.deleted,
			// ON DELETE SET NULL has not reached this row yet): the turn is
			// outside any mission now.
			return r, gen.CompletionProgress{}, nil
		}
		return r, gen.CompletionProgress{}, fmt.Errorf("queue: brief mission: %w", err)
	}
	progress, err := sessions.LoadWorkProgress(ctx, tx, *workID)
	if err != nil {
		return r, gen.CompletionProgress{}, err
	}
	m.Op = sessions.ParseTree(tree).Op
	who := atomWhoByPath(tree)
	for _, c := range progress.Conditions {
		name := ""
		if c.AgentName.IsSpecified() && !c.AgentName.IsNull() {
			name = c.AgentName.MustGet()
		}
		a := missionAtom{Label: atomLabel(c.Type, name, who[c.Path]), Met: c.Met}
		if c.NextActor.IsSpecified() && !c.NextActor.IsNull() {
			a.Next = c.NextActor.MustGet()
		}
		if c.BlockedReason.IsSpecified() && !c.BlockedReason.IsNull() {
			a.Blocked = string(c.BlockedReason.MustGet())
		}
		m.Atoms = append(m.Atoms, a)
	}
	r.Mission = m
	return r, progress, nil
}

// atomWhoByPath maps each atom's progress path ("/conditions/0") to its `who`
// — the progress row resolves only `assignee`, and a role name is still
// worth saying.
func atomWhoByPath(tree []byte) map[string]string {
	out := map[string]string{}
	var node any
	if json.Unmarshal(tree, &node) != nil {
		return out
	}
	var walk func(n any, path string)
	walk = func(n any, path string) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		if conds, ok := m["conditions"].([]any); ok {
			for i, c := range conds {
				walk(c, fmt.Sprintf("%s/conditions/%d", path, i))
			}
			return
		}
		if who, ok := m["who"].(string); ok {
			out[path] = who
		}
	}
	walk(node, "")
	return out
}

// roomHistory is the turn prompt's three bundles (PRD FR-4.1 v0.19):
// ① the room's latest recentMessages (the existing <history>), ② the rest of
// the turn's mission — its messages older than ①'s window, so the two
// together are all of it without a line twice (Lead T-R3b 판정 5), ③ the
// room's decisions older than brief [7]'s newest decisionLogLimit, plus the
// latest 「여기까지 정리」 summary.
type roomHistory struct {
	MissionOlder []*messages.Row
	Decisions    []string
	Summary      *messages.Row
	// SummaryInHistory: the latest summary is already one of ①'s lines, so
	// ③ points at it instead of repeating it.
	SummaryInHistory bool
}

// loadRoomHistory reads ② and ③. recent is ① as buildBundle rendered it
// (chronological). Messages from before the agent joined are read like any
// other (FR-2.2: an invited agent sees the room from its start).
func loadRoomHistory(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, workID *uuid.UUID, recent []*messages.Row) (roomHistory, error) {
	var h roomHistory
	if workID != nil {
		// Everything older than ①'s oldest line; with ① empty the room has no
		// messages, so the mission has none either.
		if len(recent) > 0 {
			before := recent[0].ID
			for {
				page, more, _, _, err := messages.List(ctx, tx, roomID, messages.ListOptions{IncludeReplies: true, WorkID: workID, Before: &before, Limit: 200})
				if err != nil {
					return h, err
				}
				h.MissionOlder = append(page, h.MissionOlder...)
				if !more || len(page) == 0 {
					break
				}
				before = page[0].ID
			}
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT summary, COALESCE(rationale, ''), source::text, auto, created_at
		FROM decision WHERE session_id = $1 ORDER BY created_at DESC OFFSET $2`, roomID, decisionLogLimit)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var summary, rationale, source string
		var auto bool
		var at time.Time
		if err := rows.Scan(&summary, &rationale, &source, &auto, &at); err != nil {
			rows.Close()
			return h, err
		}
		h.Decisions = append(h.Decisions, briefDecisionLine(summary, rationale, source, auto, at))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return h, err
	}
	for i, j := 0, len(h.Decisions)-1; i < j; i, j = i+1, j-1 {
		h.Decisions[i], h.Decisions[j] = h.Decisions[j], h.Decisions[i]
	}
	var sumID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM message
		WHERE session_id = $1 AND kind = 'summary' AND summary_range IS NOT NULL
		ORDER BY created_at DESC, id DESC LIMIT 1`, roomID).Scan(&sumID)
	if err != nil && !isNoRows(err) {
		return h, err
	}
	if err == nil {
		for _, m := range recent {
			if m.ID == sumID {
				h.SummaryInHistory = true
			}
		}
		if h.Summary, err = messages.Get(ctx, tx, sumID); err != nil {
			return h, err
		}
	}
	return h, nil
}

// truncationNote is the one line FR-4.1 asks for when ① dropped something
// (Lead T-R3b 판정 3: at the head of the history, not in the brief — [1]~[5]
// are the cached prefix). It says what the other bundles still carry, so the
// agent knows the gap is chatter and not the mission or a decision.
func truncationNote(omitted int, h roomHistory, inMission bool) string {
	if omitted <= 0 {
		return ""
	}
	s := fmt.Sprintf("Older room messages not shown below: %d.", omitted)
	if inMission {
		s += fmt.Sprintf(" This mission's %d among them are in <mission_messages>.", len(h.MissionOlder))
	}
	s += " Every decision is in [7] or <room_decisions>. Read the rest with `colab session messages` if you need it.\n"
	return s
}

// renderRoomHistoryTail writes ② and ③ (① is buildBundle's <history>).
func renderRoomHistoryTail(b *strings.Builder, workID *uuid.UUID, h roomHistory) {
	if workID != nil && len(h.MissionOlder) > 0 {
		fmt.Fprintf(b, "<mission_messages work=%q count=%d note=\"this mission's messages older than <history>\">\n", workID.String(), len(h.MissionOlder))
		for _, m := range h.MissionOlder {
			fmt.Fprintf(b, "[%s] %s %s: %s\n", m.CreatedAt.UTC().Format("01-02 15:04"), m.ID, authorLabel(m), m.Content)
		}
		b.WriteString("</mission_messages>\n\n")
	}
	if len(h.Decisions) > 0 {
		fmt.Fprintf(b, "<room_decisions count=%d note=\"older than the ones in [7]\">\n%s\n</room_decisions>\n\n", len(h.Decisions), strings.Join(h.Decisions, "\n"))
	}
	if s := h.Summary; s != nil {
		if h.SummaryInHistory {
			fmt.Fprintf(b, "<room_summary message=%q>the latest 「여기까지 정리」 is that message in <history> above</room_summary>\n\n", s.ID)
		} else {
			fmt.Fprintf(b, "<room_summary message=%q at=%q>\n%s\n</room_summary>\n\n", s.ID, s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), strings.TrimRight(s.Content, "\n"))
		}
	}
}
