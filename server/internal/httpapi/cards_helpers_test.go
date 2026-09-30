package httpapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// testCard is a minimal valid delegation card (PRD FR-3.8 1) — what the old
// tests' `lane delegate --agent --brief` becomes: the brief is the goal, one
// criterion checked by review, a boundary.
func testCard(agent uuid.UUID, goal string) router.DelegateInput {
	return router.DelegateInput{Card: cards.Draft{
		AssigneeID: agent, Goal: goal,
		Criteria:   []cards.Criterion{{Text: goal + " — 결과를 보고한다", Method: "review"}},
		Boundaries: "맡은 것 밖의 파일은 건드리지 않는다",
	}}
}

// setStatus is Router.SetAgentStatus as the tests before T-CARD-S called it:
// a `done` on a card task whose card is still open first submits a minimal
// result card (every criterion met, with a commit as evidence) — what an
// agent following harness v0.9.16 does — so the plain lane-end paths those
// tests pin (join, re-entry, blocked, loop limits) are exercised unchanged.
// The card gate itself is pinned by cards_gate_test.go, which calls the
// router directly.
func (f *p2Fixture) setStatus(ctx context.Context, taskID uuid.UUID, attempt int, status, note string) (*router.StatusResult, error) {
	if status == "done" {
		if err := f.autoReport(ctx, taskID, attempt); err != nil {
			return nil, err
		}
	}
	return f.srv.Router.SetAgentStatus(ctx, taskID, attempt, status, note)
}

// autoReport submits a minimal result card for a card task whose card is
// open; anything else is left alone.
func (f *p2Fixture) autoReport(ctx context.Context, taskID uuid.UUID, attempt int) error {
	var cardID *uuid.UUID
	var n int
	err := f.pool.QueryRow(ctx, `
		SELECT c.id, jsonb_array_length(c.criteria) FROM task t JOIN task_card c ON c.id = t.card_id
		WHERE t.id = $1 AND t.kind = 'card' AND c.status = 'in_progress'`, taskID).Scan(&cardID, &n)
	if err != nil || cardID == nil {
		return nil
	}
	in := cards.ResultIn{Summary: "했습니다.", Confirmed: []string{"확인"}, Assumed: []string{}}
	for c := 1; c <= n; c++ {
		in.Verdicts = append(in.Verdicts, cards.VerdictIn{Criterion: c, Verdict: "met", Evidence: []cards.Evidence{{Kind: "commit", Ref: "abc1234"}}})
	}
	_, err = f.srv.Router.SubmitResult(ctx, taskID, attempt, *cardID, in)
	return err
}

// report is autoReport for a test that has a *testing.T.
func (f *p2Fixture) report(t *testing.T, taskID uuid.UUID) {
	t.Helper()
	if err := f.autoReport(t.Context(), taskID, currentAttempt(t, f, taskID)); err != nil {
		t.Fatal(err)
	}
}
