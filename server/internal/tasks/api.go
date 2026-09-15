package tasks

import (
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// ToAPI maps a task row (+ optional attempts and usage) to the contract type.
func ToAPI(t *Row, attempts []Attempt, usage *Usage) gen.Task {
	out := gen.Task{
		Id:                  t.ID,
		LaneId:              t.LaneID,
		SessionId:           t.SessionID,
		RuntimeId:           NullUUID(t.RuntimeID),
		AgentId:             t.AgentID,
		ProfileId:           t.ProfileID,
		TriggerMessageId:    NullUUID(t.TriggerMessageID),
		DelegatedFromTaskId: NullUUID(t.DelegatedFromTaskID),
		RestartedFromTaskId: NullUUID(t.RestartedFromTaskID),
		OriginatorUserId:    NullUUID(t.OriginatorUserID),
		CoalescedMessageIds: t.CoalescedMessageIDs,
		Attempt:             t.Attempt,
		MaxAttempts:         t.MaxAttempts,
		PendingHitl:         t.PendingHitl,
		BudgetOverride:      NullFloat(t.BudgetOverride),
		Status:              gen.TaskStatus(t.Status),
		PausedReason:        nullable.NewNullNullable[gen.PauseReason](),
		FailureKind:         nullable.NewNullNullable[gen.FailureKind](),
		HeartbeatAt:         NullTime(t.HeartbeatAt),
		CreatedAt:           t.CreatedAt,
		UpdatedAt:           t.UpdatedAt,
		DispatchedAt:        NullTime(t.DispatchedAt),
		StartedAt:           NullTime(t.StartedAt),
		FinishedAt:          NullTime(t.FinishedAt),
	}
	if t.PausedReason != nil {
		out.PausedReason = nullable.NewNullableWithValue(gen.PauseReason(*t.PausedReason))
	}
	if t.FailureKind != nil {
		out.FailureKind = nullable.NewNullableWithValue(gen.FailureKind(*t.FailureKind))
	}
	if t.CoalescedMessageIds() == nil {
		out.CoalescedMessageIds = []openapi_types.UUID{}
	}
	if attempts != nil {
		as := make([]gen.TaskAttempt, 0, len(attempts))
		for _, a := range attempts {
			ta := gen.TaskAttempt{
				Attempt:    a.Attempt,
				StartedAt:  NullTime(a.StartedAt),
				FinishedAt: NullTime(a.FinishedAt),
				Resumed:    nullable.NewNullNullable[bool](),
				Outcome:    nullable.NewNullNullable[string](),
			}
			if a.Resumed != nil {
				ta.Resumed = nullable.NewNullableWithValue(*a.Resumed)
			}
			if a.Outcome != nil {
				ta.Outcome = nullable.NewNullableWithValue(*a.Outcome)
			}
			as = append(as, ta)
			if a.Attempt == t.Attempt && a.Resumed != nil {
				out.Resumed = nullable.NewNullableWithValue(*a.Resumed)
			}
		}
		out.Attempts = &as
	}
	if usage != nil {
		out.Usage = &gen.TaskUsage{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheRead: usage.CacheRead,
			CostUsd: float32(usage.CostUSD), Estimated: usage.Estimated, UpdatedAt: &usage.UpdatedAt,
		}
	}
	return out
}

// CoalescedMessageIds exists so ToAPI can be written without a nil check inline.
func (t *Row) CoalescedMessageIds() []uuid.UUID { return t.CoalescedMessageIDs }

// Nullable helpers shared by the API mappers of every package.

func NullUUID(p *uuid.UUID) nullable.Nullable[openapi_types.UUID] {
	if p == nil {
		return nullable.NewNullNullable[openapi_types.UUID]()
	}
	return nullable.NewNullableWithValue(openapi_types.UUID(*p))
}

func NullTime(p *time.Time) nullable.Nullable[time.Time] {
	if p == nil {
		return nullable.NewNullNullable[time.Time]()
	}
	return nullable.NewNullableWithValue(*p)
}

func NullString(p *string) nullable.Nullable[string] {
	if p == nil {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(*p)
}

func NullFloat(p *float64) nullable.Nullable[float32] {
	if p == nil {
		return nullable.NewNullNullable[float32]()
	}
	return nullable.NewNullableWithValue(float32(*p))
}
