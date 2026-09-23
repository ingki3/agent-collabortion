package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// S-84 (openapi 0.1.4): a completion condition that names nobody can be
// satisfied by nobody. The Director's "STO 시장 조사" session carried
// `{"type":"agent_approval"}` with no agent_id, and namesActor — correctly —
// never matched a reviewer, so the session could not close and nothing on the
// screen said why. Three things close that hole, all in this file:
//
//   - ValidateReviewers rejects such a tree at createSession/updateSession
//     (422 reviewer_required · reviewer_not_participant), so no NEW session
//     is born in that shape;
//   - buildProgress puts agent_id · agent_name · blocked_reason on every atom
//     of the progress read model, so an OLD session (or one whose reviewer
//     left) shows the structural reason instead of a bare ✗;
//   - updateSession may change completion_condition while the session is
//     active or paused (handlers_sessions_p3.go), which is how such a session
//     is rescued — the re-evaluation rides on ApplyCompletionEvent with the
//     EventConditionChanged kind.

// EventConditionChanged is the ApplyEvent kind updateSession uses after the
// tree was replaced: it satisfies no atom, it only re-reads the tree over the
// atoms already met, so a session whose new tree is already satisfied
// completes and one whose only missing atom is user_approval gets the
// platform's request.
const EventConditionChanged = "condition_changed"

// NextActorDirector · NextActorPlatform are the `next_actor` values for the
// atoms no agent satisfies; the two agent-driven atoms carry the agent's name
// (SCREEN §4.5: "Lead 의 검토 승인 — Lead 차례").
const (
	NextActorDirector = "director"
	NextActorPlatform = "platform"
)

// designated reports which agent the atom names: `agent_id` when given,
// otherwise `who: assignee` resolved against the session's assignee. A
// `who` that is a role name is not resolved here — ApplyCompletionEvent does
// not resolve it either, so reporting it as unresolved is the honest reading
// of what the evaluator will do with it.
func designated(c Condition, assignee *uuid.UUID) *uuid.UUID {
	if c.Agent != nil {
		return c.Agent
	}
	if c.Who == "assignee" {
		return assignee
	}
	return nil
}

// agentDriven is the pair of atoms an agent satisfies — the ones a
// designation matters for.
func agentDriven(typ string) bool {
	return typ == CondArtifactSubmitted || typ == CondAgentApproval
}

// ValidateReviewers is the S-84 half of the creation guard, beside
// ValidateTree (E6-07, which stays tree-only so the E6 golden keeps its
// meaning). `participant` answers whether an agent is in the session —
// participants[] plus the assignee. The field paths point at the atom the
// way `Problem.errors[].field` does elsewhere (`completion_condition/
// conditions/2/agent_id`), so the S6 wizard can mark the right row.
func ValidateReviewers(t Tree, participant func(uuid.UUID) bool) []apperr.FieldError {
	var errs []apperr.FieldError
	for i, c := range t.Conditions {
		if !agentDriven(c.Type) {
			continue
		}
		field := fmt.Sprintf("completion_condition/conditions/%d/agent_id", i)
		if c.Agent == nil {
			if c.Type == CondAgentApproval {
				errs = append(errs, apperr.Field(field, "reviewer_required",
					"「검토 승인」에는 리뷰어를 참여자 중에서 골라 주세요 — 리뷰어가 없으면 아무도 승인할 수 없어 세션이 끝나지 않습니다"))
			}
			continue
		}
		if !participant(*c.Agent) {
			msg := "리뷰어는 이 세션의 참여자 중에서 골라 주세요"
			if c.Type == CondArtifactSubmitted {
				msg = "제출자는 이 세션의 참여자 중에서 골라 주세요"
			}
			errs = append(errs, apperr.Field(field, "reviewer_not_participant", msg))
		}
	}
	return errs
}

// agentFact is what the progress read model needs to know about one agent
// the tree names: its name for the screen, whether it is still in the session
// and whether it has been archived.
type agentFact struct {
	Name        string
	Participant bool
	Archived    bool
}

// completionFacts is everything buildProgress needs beyond the two columns:
// the assignee (for `who: assignee`) and the facts of every agent the tree
// names. It is loaded by loadCompletionFacts and, in tests, built by hand.
type completionFacts struct {
	Assignee *uuid.UUID
	Agents   map[uuid.UUID]agentFact
}

// loadCompletionFacts reads the agents a tree names in one query. An id that
// comes back with no row is simply absent from the map, which buildProgress
// reads as "not a participant" — the same verdict as an agent that exists
// elsewhere, so the response does not say which.
func loadCompletionFacts(ctx context.Context, q db.DBTX, sessionID uuid.UUID, t Tree, assignee *uuid.UUID) (completionFacts, error) {
	f := completionFacts{Assignee: assignee, Agents: map[uuid.UUID]agentFact{}}
	var ids []uuid.UUID
	for _, c := range t.Conditions {
		if id := designated(c, assignee); id != nil && agentDriven(c.Type) {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return f, nil
	}
	rows, err := q.Query(ctx, `
		SELECT a.id, a.name, a.archived_at IS NOT NULL,
		       EXISTS (SELECT 1 FROM room_participant sp WHERE sp.room_id = $1 AND sp.agent_id = a.id AND sp.left_at IS NULL)
		FROM agent a WHERE a.id = ANY($2)`, sessionID, ids)
	if err != nil {
		return f, fmt.Errorf("sessions: completion agents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var af agentFact
		if err := rows.Scan(&id, &af.Name, &af.Archived, &af.Participant); err != nil {
			return f, err
		}
		if assignee != nil && id == *assignee {
			// The assignee counts as a participant even if its
			// session_participant row is gone (openapi createSession:
			// "참여자 = participants[] + assignee").
			af.Participant = true
		}
		f.Agents[id] = af
	}
	return f, rows.Err()
}

// blockedReason is the S-84 verdict for one unmet agent-driven atom: nil when
// the designated agent can act, otherwise why it structurally cannot.
func blockedReason(c Condition, f completionFacts) *gen.CompletionProgressConditionsBlockedReason {
	if !agentDriven(c.Type) {
		return nil
	}
	id := designated(c, f.Assignee)
	if id == nil {
		r := gen.CompletionProgressConditionsBlockedReasonReviewerMissing
		return &r
	}
	af, ok := f.Agents[*id]
	if !ok || !af.Participant {
		r := gen.CompletionProgressConditionsBlockedReasonReviewerNotParticipant
		return &r
	}
	if af.Archived {
		r := gen.CompletionProgressConditionsBlockedReasonAgentArchived
		return &r
	}
	return nil
}

// LoadProgress is the completion read model with the S-84 columns filled:
// the same function serves getSession, submitArtifact/reviewArtifact's
// `completion_progress` and the `session.completion_progress` frame, so the
// REST body and the SSE frame cannot disagree about why an atom is blocked.
func LoadProgress(ctx context.Context, q db.DBTX, sessionID uuid.UUID) (gen.CompletionProgress, error) {
	var tree, met []byte
	var assignee *uuid.UUID
	err := q.QueryRow(ctx, `SELECT completion_condition, completion_met, assignee_agent_id FROM work WHERE room_id = $1`, sessionID).
		Scan(&tree, &met, &assignee)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.CompletionProgress{}, apperr.NotFound("session")
	}
	if err != nil {
		return gen.CompletionProgress{}, err
	}
	return progressOf(ctx, q, sessionID, tree, met, assignee)
}

// progressOf is LoadProgress for a caller that already holds the columns
// (sessions.Load reads them in its one SELECT).
func progressOf(ctx context.Context, q db.DBTX, sessionID uuid.UUID, tree, met []byte, assignee *uuid.UUID) (gen.CompletionProgress, error) {
	facts, err := loadCompletionFacts(ctx, q, sessionID, ParseTree(tree), assignee)
	if err != nil {
		return gen.CompletionProgress{}, err
	}
	return buildProgress(tree, met, facts), nil
}

// buildProgress renders the completion tree for S7's right rail. The met
// flags come from session.completion_met rather than being recomputed: E6-04
// pins that an artifact_submitted flag survives a Director rejection, and a
// recomputation has no way to remember that.
//
// Per atom (openapi CompletionProgress.conditions[], v0.1.4): agent_id and
// agent_name for the two agent-driven types (`who: assignee` resolved),
// blocked_reason when an UNMET agent-driven atom names nobody who can act,
// and next_actor as the screen's "누구 차례" — the agent's name, `director`
// or `platform`. A met atom is neither blocked nor anyone's turn.
func buildProgress(tree, metRaw []byte, f completionFacts) gen.CompletionProgress {
	var p gen.CompletionProgress
	met := map[string]bool{}
	_ = json.Unmarshal(metRaw, &met)
	p.Conditions = make([]progressCond, 0)
	var node any
	if json.Unmarshal(tree, &node) != nil {
		return p
	}
	human := false
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
		typ, _ := m["type"].(string)
		if typ == "user_approval" || typ == "manual" {
			human = true
		}
		p.Total++
		if met[typ] {
			p.Met++
		}
		p.Conditions = append(p.Conditions, describe(atomOf(m), path, met[typ], f))
	}
	walk(node, "")
	p.HumanGate = &human
	// `satisfied` is the tree's verdict, not `met == total`: under OR one atom
	// is enough, and S7 renders "완료로 갑니다" from this field. Leaving it at
	// the zero value made a finished session look unfinished to every reader.
	p.Satisfied = Satisfied(ParseTree(tree), met)
	return p
}

// atomOf reads one atom the way ParseTree does, so the progress walk and the
// evaluator agree on what an atom names.
func atomOf(m map[string]any) Condition {
	c := Condition{}
	c.Type, _ = m["type"].(string)
	if who, ok := m["who"].(string); ok {
		c.Who = who
	}
	if id, ok := m["agent_id"].(string); ok {
		if parsed, err := uuid.Parse(id); err == nil {
			c.Agent = &parsed
		}
	}
	return c
}

// describe fills one progress row. Every S-84 column is written explicitly
// (a null rather than an omitted key) so a reader of the JSON can tell "no
// reason" from "not computed".
func describe(c Condition, path string, met bool, f completionFacts) progressCond {
	row := progressCond{
		Path: path, Type: c.Type, Met: met,
		AgentId:       nullable.NewNullNullable[openapi_types.UUID](),
		AgentName:     nullable.NewNullNullable[string](),
		BlockedReason: nullable.NewNullNullable[gen.CompletionProgressConditionsBlockedReason](),
		NextActor:     nullable.NewNullNullable[string](),
	}
	var name string
	if agentDriven(c.Type) {
		if id := designated(c, f.Assignee); id != nil {
			row.AgentId = nullable.NewNullableWithValue(openapi_types.UUID(*id))
			if af, ok := f.Agents[*id]; ok {
				name = af.Name
				row.AgentName = nullable.NewNullableWithValue(af.Name)
			}
		}
	}
	if met {
		return row
	}
	if r := blockedReason(c, f); r != nil {
		row.BlockedReason = nullable.NewNullableWithValue(*r)
		return row
	}
	switch c.Type {
	case CondArtifactSubmitted, CondAgentApproval:
		if name != "" {
			row.NextActor = nullable.NewNullableWithValue(name)
		}
	case CondUserApproval, CondManual:
		row.NextActor = nullable.NewNullableWithValue(NextActorDirector)
	case CondCriteriaMet:
		row.NextActor = nullable.NewNullableWithValue(NextActorPlatform)
	}
	return row
}
