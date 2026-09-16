package sessions

import (
	"errors"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

// TestCheckBudgetRaise is S-49: the too_low rule and its sentence in one
// function, held to the same answer whichever field asks. Both handlers
// (resumeSession `limits.budget_usd`, the K-10 approval `budget_override_usd`)
// call this and nothing else.
func TestCheckBudgetRaise(t *testing.T) {
	for _, c := range []struct {
		name         string
		limit, spent float64
		tooLow       bool
	}{
		{"above spent → ok", 5, 4.99, false},
		{"equal to spent → too_low (the next report re-trips)", 5, 5, true},
		{"below spent → too_low", 4, 5, true},
		{"nothing spent yet, any positive raise → ok", 0.01, 0, false},
	} {
		for _, field := range []string{"limits.budget_usd", "budget_override_usd"} {
			t.Run(c.name+" / "+field, func(t *testing.T) {
				err := CheckBudgetRaise(field, c.limit, c.spent)
				if !c.tooLow {
					if err != nil {
						t.Fatalf("want ok, got %v", err)
					}
					return
				}
				var p *apperr.Problem
				if !errors.As(err, &p) || p.Status != 422 || len(p.Errors) != 1 {
					t.Fatalf("want 422 with one field error, got %v", err)
				}
				fe := p.Errors[0]
				if fe.Field != field || fe.Code != "too_low" {
					t.Fatalf("field error = %+v, want %s/too_low", fe, field)
				}
				want := BudgetTooLowError(field, c.spent).(*apperr.Problem).Errors[0].Message
				if fe.Message != want {
					t.Fatalf("sentence = %q, want the one wording %q", fe.Message, want)
				}
			})
		}
	}
}
