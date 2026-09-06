package tasks

import (
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// PausedDetail builds contracts/openapi.yaml's PausedDetail — the shape S5's
// pause banner reads. It is a shared constructor rather than a literal at each
// call site so `reason` and `paused_at` cannot go missing (the contract makes
// both required) and so the key names come from the generated type.
func PausedDetail(reason string, at time.Time) gen.PausedDetail {
	return gen.PausedDetail{Reason: gen.PauseReason(reason), PausedAt: at.UTC()}
}

// WithLoop fills the branch FR-3.5 needs: which of the three limits tripped,
// the count that tripped it, and the agents involved. "loop" on its own does
// not tell the Director which setting to raise.
func WithLoop(d gen.PausedDetail, limit string, count int, agents []uuid.UUID) gen.PausedDetail {
	ids := make([]openapi_types.UUID, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, openapi_types.UUID(a))
	}
	lim := gen.PausedDetailLoopLimit(limit)
	d.Loop = &struct {
		Agents *[]openapi_types.UUID      `json:"agents,omitempty"`
		Count  *int                       `json:"count,omitempty"`
		Limit  *gen.PausedDetailLoopLimit `json:"limit,omitempty"`
	}{Agents: &ids, Count: &count, Limit: &lim}
	return d
}

// WithBudget fills the budget branch (FR-7.3).
func WithBudget(d gen.PausedDetail, limitUSD, spentUSD float32) gen.PausedDetail {
	d.Budget = &struct {
		LimitUsd *float32 `json:"limit_usd,omitempty"`
		SpentUsd *float32 `json:"spent_usd,omitempty"`
	}{LimitUsd: &limitUSD, SpentUsd: &spentUSD}
	return d
}
