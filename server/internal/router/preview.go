package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
)

// Preview answers FR-3.6: "이 메시지는 A, B를 트리거합니다 (프로파일: …)". It
// runs the SAME premises and the SAME rules as Post — it just writes nothing.
//
// The web computed this locally in P1, which meant two implementations of
// FR-3.3 drifting apart; the preview is only useful if it is the post's own
// answer.
func (s *Service) Preview(ctx context.Context, sessionID uuid.UUID, author Author, in gen.MessageCreate) (*gen.TriggerPreview, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wsID uuid.UUID
	var status string
	var assignee *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT workspace_id, status::text, assignee_agent_id FROM session WHERE id = $1`, sessionID).
		Scan(&wsID, &status, &assignee)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	participants, profiles, err := loadParticipants(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	th, err := threadPremise(ctx, tx, sessionID, in.ParentId)
	if err != nil {
		return nil, err
	}
	authorDelegator, joinFired, err := delegatorPremise(ctx, tx, author.TaskID)
	if err != nil {
		return nil, err
	}
	var suppress []uuid.UUID
	if in.SuppressAgentIds != nil {
		for _, id := range *in.SuppressAgentIds {
			suppress = append(suppress, uuid.UUID(id))
		}
	}
	dec := Decide(Input{
		Content: in.Content, AuthorType: author.Type, Participants: participants,
		AuthorAgentID: author.AgentID, AssigneeAgentID: assignee, Suppress: suppress,
		ReplyToAgentID: th.ReplyTo, ThreadOwnerAgentID: th.ThreadOwner,
		AuthorLaneDelegatorID: authorDelegator, JoinGroupFired: joinFired,
	})

	out := &gen.TriggerPreview{Triggers: []gen.TriggerTarget{}}
	out.NoteOnly = isNote(in.Content)
	names := map[uuid.UUID]string{}
	for _, p := range participants {
		names[p.AgentID] = p.Name
	}
	// Rule 3 fired when mentions exist but nothing was triggered by them and
	// the author is a human — the composer shows "암묵 라우팅 억제됨".
	suppressed := !out.NoteOnly && len(dec.Triggers) == 0 && len(dec.Mentions) > 0
	out.ImplicitRoutingSuppressed = &suppressed

	for _, w := range dec.Warnings {
		out.Warnings = append(out.Warnings, struct {
			AgentId nullable.Nullable[openapi_types.UUID] `json:"agent_id,omitempty"`
			Code    string                                `json:"code"`
			Message string                                `json:"message"`
		}{AgentId: nullUUID(w.AgentID), Code: w.Code, Message: w.Message})
	}
	if out.Warnings == nil {
		out.Warnings = []struct {
			AgentId nullable.Nullable[openapi_types.UUID] `json:"agent_id,omitempty"`
			Code    string                                `json:"code"`
			Message string                                `json:"message"`
		}{}
	}
	// The delegator the author may not wake is worth showing: the message is
	// posted, it just carries in the join bundle instead (E1-15).
	if authorDelegator != nil && !joinFired && mentionsAgent(dec, *authorDelegator) {
		out.Warnings = append(out.Warnings, struct {
			AgentId nullable.Nullable[openapi_types.UUID] `json:"agent_id,omitempty"`
			Code    string                                `json:"code"`
			Message string                                `json:"message"`
		}{AgentId: nullUUID(authorDelegator), Code: "suppressed_delegator",
			Message: names[*authorDelegator] + "은(는) 위임자이므로 합류 묶음으로 한 번에 전달됩니다"})
	}

	newLane := in.NewLane != nil && *in.NewLane && author.Type == "user"
	for _, tr := range dec.Triggers {
		t := gen.TriggerTarget{AgentId: tr.AgentID, AgentName: names[tr.AgentID], Rule: tr.Rule}
		d, busy, err := s.previewLane(ctx, tx, sessionID, tr, laneOpts{
			threadRootLane: th.RootLane,
			topLevelMent:   tr.Rule == 2 && th.Parent == nil,
			forceNewLane:   newLane,
		})
		if err != nil {
			return nil, err
		}
		t.Lane.Resolution = d.Rule
		reentry := d.Reentry
		t.Lane.Reentry = &reentry
		if d.Created {
			t.Lane.LaneId = nullable.NewNullNullable[openapi_types.UUID]()
		} else {
			t.Lane.LaneId = nullable.NewNullableWithValue(openapi_types.UUID(d.LaneID))
		}
		t.WillQueue = busy
		if pr, err := previewProfile(ctx, tx, profiles[tr.AgentID]); err != nil {
			return nil, err
		} else if pr != nil {
			t.Profile = pr
		}
		out.Triggers = append(out.Triggers, t)
	}
	// Rule 7's deferred assignee task is part of what the composer promises.
	if fb := PlanFallback(dec, assignee, s.Clock.Now()); fb != nil {
		t := gen.TriggerTarget{AgentId: fb.AgentID, AgentName: names[fb.AgentID], Rule: 7}
		t.Lane.LaneId = nullable.NewNullNullable[openapi_types.UUID]()
		t.DeferredUntil = nullable.NewNullableWithValue(fb.DueAt)
		if pr, err := previewProfile(ctx, tx, profiles[fb.AgentID]); err != nil {
			return nil, err
		} else if pr != nil {
			t.Profile = pr
		}
		out.Triggers = append(out.Triggers, t)
	}
	return out, nil
}

// previewLane resolves the lane without writing, and reports whether the
// resulting lane already has work in flight (so the trigger will queue/merge
// rather than start, FR-3.4).
func (s *Service) previewLane(ctx context.Context, q pgx.Tx, sessionID uuid.UUID, tr Trigger, o laneOpts) (lanestate.Decision, bool, error) {
	rows, err := q.Query(ctx, `
		SELECT id, agent_id, status::text, reentry_count, GREATEST(created_at, updated_at)
		FROM lane WHERE session_id = $1 AND agent_id = $2 ORDER BY created_at`, sessionID, tr.AgentID)
	if err != nil {
		return lanestate.Decision{}, false, err
	}
	var existing []lanestate.Candidate
	for rows.Next() {
		var c lanestate.Candidate
		if err := rows.Scan(&c.ID, &c.AgentID, &c.Status, &c.ReentryCount, &c.LastUsed); err != nil {
			rows.Close()
			return lanestate.Decision{}, false, err
		}
		existing = append(existing, c)
	}
	rows.Close()
	d := lanestate.Resolve(lanestate.Request{
		AgentID: tr.AgentID, Existing: existing,
		ThreadRootLaneID: o.threadRootLane,
		TopLevelMention:  o.topLevelMent, ForceNewLane: o.forceNewLane,
	})
	if d.Created {
		return d, false, nil
	}
	var busy int
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM task WHERE lane_id = $1 AND status IN ('queued', 'dispatched', 'preparing', 'running')`,
		d.LaneID).Scan(&busy); err != nil {
		return d, false, err
	}
	return d, busy > 0, nil
}

func previewProfile(ctx context.Context, q pgx.Tx, profileID uuid.UUID) (*struct {
	Id          *openapi_types.UUID `json:"id,omitempty"`
	Model       *string             `json:"model,omitempty"`
	Name        *string             `json:"name,omitempty"`
	RuntimeKind *gen.RuntimeKind    `json:"runtime_kind,omitempty"`
}, error) {
	if profileID == uuid.Nil {
		return nil, nil
	}
	var name, kind string
	var model *string
	err := q.QueryRow(ctx, `SELECT name, runtime_kind::text, model FROM agent_profile WHERE id = $1`, profileID).
		Scan(&name, &kind, &model)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("router: preview profile: %w", err)
	}
	id := openapi_types.UUID(profileID)
	rk := gen.RuntimeKind(kind)
	return &struct {
		Id          *openapi_types.UUID `json:"id,omitempty"`
		Model       *string             `json:"model,omitempty"`
		Name        *string             `json:"name,omitempty"`
		RuntimeKind *gen.RuntimeKind    `json:"runtime_kind,omitempty"`
	}{Id: &id, Model: model, Name: &name, RuntimeKind: &rk}, nil
}

func mentionsAgent(d Decision, id uuid.UUID) bool {
	for _, m := range d.Mentions {
		if m.Kind == gen.MentionKindAgent && m.Id == id.String() {
			return true
		}
	}
	return false
}

func nullUUID(id *uuid.UUID) nullable.Nullable[openapi_types.UUID] {
	if id == nil {
		return nullable.NewNullNullable[openapi_types.UUID]()
	}
	return nullable.NewNullableWithValue(openapi_types.UUID(*id))
}
