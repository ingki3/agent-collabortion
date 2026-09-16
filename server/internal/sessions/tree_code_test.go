package sessions

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// TestValidateTreeCodes is S-85 (PR #233 리뷰 NN2): each way a completion
// tree can be wrong has its own field code, and every one of them still
// unwraps to ErrInvalidTree. The handler puts TreeErrorCode on the 422 —
// before, every failure went out as `criteria_met_alone`.
func TestValidateTreeCodes(t *testing.T) {
	appr := Condition{Type: CondUserApproval}
	crit := Condition{Type: CondCriteriaMet}
	for _, c := range []struct {
		name string
		tree Tree
		code string
	}{
		{"no conditions", Tree{Op: "AND"}, TreeCodeRequired},
		{"unknown op", Tree{Op: "XOR", Conditions: []Condition{appr}}, TreeCodeInvalidOp},
		{"unknown condition type", Tree{Op: "AND", Conditions: []Condition{{Type: "vibes"}}}, TreeCodeUnknownType},
		{"criteria_met alone", Tree{Op: "AND", Conditions: []Condition{crit}}, TreeCodeCriteriaMetAlone},
		{"criteria_met under OR", Tree{Op: "OR", Conditions: []Condition{crit, appr}}, TreeCodeCriteriaMetAlone},
		{"criteria_met AND approval", Tree{Op: "and", Conditions: []Condition{crit, appr}}, ""},
		{"agent_approval alone (a different role reviews)", Tree{Op: "AND", Conditions: []Condition{{Type: CondAgentApproval, Agent: ptr(uuid.New())}}}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateTree(c.tree)
			if c.code == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want %s, got nil", c.code)
			}
			if !errors.Is(err, ErrInvalidTree) {
				t.Fatalf("%v must unwrap to ErrInvalidTree", err)
			}
			if got := TreeErrorCode(err); got != c.code {
				t.Fatalf("code = %q, want %q (err %v)", got, c.code, err)
			}
		})
	}
	if got := TreeErrorCode(errors.New("something else")); got != "invalid" {
		t.Fatalf("a foreign error gets the generic code, got %q", got)
	}
}

func ptr[T any](v T) *T { return &v }
