// Package sessions is FR-2.1 session creation (P1: `none` isolation), detail
// with participants' derived status (FR-1.3) and the S5 list.
package sessions

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/llm"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

type Service struct {
	DB     *pgxpool.Pool
	Clock  clock.Clock
	Hub    *realtime.Hub
	Router *router.Service
	// Tasks carries out a pause's consequence for work already in flight
	// (FR-2.3 drain vs cancel).
	Tasks *tasks.Service
	// LLM is the platform's own Claude client (PRD §8.5) — the session
	// summary's writer. Nil is a supported state: with no API key configured
	// the summary is composed from rows, as it was in P2 (see summarise).
	LLM llm.Client
	Log *slog.Logger
}

func New(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub, r *router.Service) *Service {
	return &Service{DB: pool, Clock: c, Hub: h, Router: r}
}

// WithTasks wires the task service in after construction.
func (s *Service) WithTasks(t *tasks.Service) *Service { s.Tasks = t; return s }

// WithLLM wires the platform LLM client (§8.5) and the logger it reports cache
// and refusal outcomes through.
func (s *Service) WithLLM(c llm.Client, log *slog.Logger) *Service {
	if c != nil {
		s.LLM = c
	}
	s.Log = log
	return s
}

// Progress is the completion read model on its own. submitArtifact and
// reviewArtifact answer with it beside the thing that just happened (openapi),
// so the caller learns whether its submission actually moved the mission
// without a second round trip.
func (s *Service) Progress(ctx context.Context, sessionID uuid.UUID) (gen.CompletionProgress, error) {
	return LoadProgress(ctx, s.DB, sessionID)
}

// progressCond is the element type of gen.CompletionProgress.Conditions — the
// generator emits it as an anonymous struct, so name it once here. agent_id ·
// agent_name · blocked_reason (openapi 0.1.4, S-84) are filled by
// buildProgress (reviewer.go).
type progressCond = struct {
	AgentId       nullable.Nullable[openapi_types.UUID]                            `json:"agent_id,omitempty"`
	AgentName     nullable.Nullable[string]                                        `json:"agent_name,omitempty"`
	BlockedReason nullable.Nullable[gen.CompletionProgressConditionsBlockedReason] `json:"blocked_reason,omitempty"`
	HitlRequestId nullable.Nullable[openapi_types.UUID]                            `json:"hitl_request_id,omitempty"`
	Met           bool                                                             `json:"met"`
	MetAt         nullable.Nullable[time.Time]                                     `json:"met_at,omitempty"`
	MetBy         nullable.Nullable[string]                                        `json:"met_by,omitempty"`
	NextActor     nullable.Nullable[string]                                        `json:"next_actor,omitempty"`
	Path          string                                                           `json:"path"`
	Type          string                                                           `json:"type"`
}

// AgentStatuses derives FR-1.3's status for the room's current agent
// participants (or just agentID when set), scoped to the room: offline when
// the room's runtime is offline, working when a turn of that agent in THIS
// room is running.
//
// It is the one derivation listRoomParticipants and `participant.updated`
// both read (R4: the old session roster that carried it is gone). FR-1.3's
// status is computed, not stored, so a second implementation would be a
// second opinion, and the poll and the live frame would disagree about
// whether an agent is working.
func AgentStatuses(ctx context.Context, q db.DBTX, roomID uuid.UUID, agentID *uuid.UUID) (map[uuid.UUID]gen.AgentStatus, error) {
	var runtimeStatus *string
	err := q.QueryRow(ctx, `SELECT r.status::text FROM room s LEFT JOIN runtime r ON r.id = s.runtime_id WHERE s.id = $1`, roomID).Scan(&runtimeStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("room")
	}
	if err != nil {
		return nil, err
	}
	only := ""
	args := []any{roomID}
	if agentID != nil {
		args = append(args, *agentID)
		only = " AND sp.agent_id = $2"
	}
	rows, err := q.Query(ctx, `
		SELECT a.id, a.respond_to, a.archived_at IS NOT NULL,
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.session_id = sp.room_id AND t.status IN ('dispatched','preparing','running')),
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.session_id = sp.room_id AND t.status = 'waiting_human'),
		       `+tasks.LastFailureKindSQL("AND t.session_id = sp.room_id")+`,
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.session_id = sp.room_id AND t.status IN ('queued','deferred') AND t.attempt > 1),
		       EXISTS (SELECT 1 FROM lane l WHERE l.agent_id = a.id AND l.session_id = sp.room_id AND l.status = 'blocked'),
		       EXISTS (SELECT 1 FROM task t WHERE t.agent_id = a.id AND t.session_id = sp.room_id AND t.status = 'paused' AND t.paused_reason = 'budget')
		FROM room_participant sp JOIN agent a ON a.id = sp.agent_id WHERE sp.room_id = $1 AND sp.left_at IS NULL`+only, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]gen.AgentStatus{}
	for rows.Next() {
		var id uuid.UUID
		var respondTo string
		var lastFailure *string
		var archived, running, waiting, retrying, blocked, pausedBudget bool
		if err := rows.Scan(&id, &respondTo, &archived, &running, &waiting, &lastFailure, &retrying, &blocked, &pausedBudget); err != nil {
			return nil, err
		}
		// One ladder, one implementation (FR-1.3). The offline step is
		// room-scoped — it is the ROOM's runtime that decides whether a turn
		// could run — so it is an input here rather than a second pass over
		// the answer.
		//
		// blocked lanes and paused(budget) tasks are read but deliberately
		// ignored by the ladder (E5-13, E5-14): both processes have already
		// ended and the lane card says why. Selecting them keeps that decision
		// visible instead of hiding it in a missing column.
		out[id] = gen.AgentStatus(tasks.DeriveAgentStatus(tasks.Derived{
			RespondTo: respondTo, Archived: archived,
			RuntimeOffline:  runtimeStatus != nil && *runtimeStatus == "offline",
			Running:         boolCount(running),
			WaitingHuman:    boolCount(waiting),
			Blocked:         boolCount(blocked),
			PausedBudget:    boolCount(pausedBudget),
			LastFailureKind: derefStr(lastFailure),
			RetryInFlight:   retrying,
		}))
	}
	return out, rows.Err()
}

// RosterEntry is one row of the CLI context's `participants` (openapi
// CliContext): who a turn can mention or delegate to.
type RosterEntry struct {
	AgentID uuid.UUID
	Name    string
	Role    string
}

// Roster lists the room's current agent participants in join order.
func Roster(ctx context.Context, q db.DBTX, roomID uuid.UUID) ([]RosterEntry, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.name, a.role::text FROM room_participant sp JOIN agent a ON a.id = sp.agent_id
		WHERE sp.room_id = $1 AND sp.left_at IS NULL ORDER BY sp.joined_at, sp.id`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RosterEntry{}
	for rows.Next() {
		var e RosterEntry
		if err := rows.Scan(&e.AgentID, &e.Name, &e.Role); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DecisionAPI maps one decision row to the contract's Decision. It is the one
// mapping: listDecisions, recordDecision's 201 and the `decision.created`
// frame all read it, so the web sees the same object however it arrived.
func DecisionAPI(sessionID uuid.UUID, d DecisionRow) gen.Decision {
	out := gen.Decision{
		Id: d.ID, SessionId: sessionID, Summary: d.Summary,
		Source: gen.DecisionSource(d.Source), CreatedAt: d.CreatedAt,
		Auto: &d.Auto,
	}
	if d.Rationale != nil {
		out.Rationale = nullable.NewNullableWithValue(*d.Rationale)
	} else {
		out.Rationale = nullable.NewNullNullable[string]()
	}
	if d.RefID != nil {
		out.RefId = nullable.NewNullableWithValue(openapi_types.UUID(*d.RefID))
	} else {
		out.RefId = nullable.NewNullNullable[openapi_types.UUID]()
	}
	if d.WorkID != nil {
		// v0.2.0: the decision's mission (none = the room's own).
		out.WorkId = nullable.NewNullableWithValue(openapi_types.UUID(*d.WorkID))
	}
	return out
}
