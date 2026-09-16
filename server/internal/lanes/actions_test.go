package lanes

import (
	"testing"

	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// S-83: the server only ever emitted `cancel`, so 「다시 지시」·「응답하러 가기」·
// 「계속 진행 승인」 were disabled on every real lane card. This pins the rule the
// web mock (lib/mock/handlers.ts laneActions) and the S7 card share.
func TestLaneActions(t *testing.T) {
	none := nullable.NewNullNullable[gen.FailureKind]()
	offline := nullable.NewNullableWithValue(gen.FailureKindRuntimeOffline)
	stall := nullable.NewNullableWithValue(gen.FailureKindStall)
	cases := []struct {
		name        string
		status      gen.LaneStatus
		failure     nullable.Nullable[gen.FailureKind]
		cancellable bool // tasks.Cancellable(current task) — K-16
		control     bool
		want        []gen.LaneActions
	}{
		{"running director", gen.LaneStatusRunning, none, true, true, []gen.LaneActions{gen.LaneActionsRestart, gen.LaneActionsCancel}},
		{"running member", gen.LaneStatusRunning, none, true, false, []gen.LaneActions{}},
		{"queued director", gen.LaneStatusQueued, none, true, true, []gen.LaneActions{gen.LaneActionsCancel}},
		{"blocked member — navigation is for everyone", gen.LaneStatusBlocked, none, false, false, []gen.LaneActions{gen.LaneActionsOpenQuestion}},
		{"waiting_human director", gen.LaneStatusWaitingHuman, none, false, true, []gen.LaneActions{gen.LaneActionsRespondHitl}},
		{"paused director", gen.LaneStatusPaused, none, false, true, []gen.LaneActions{gen.LaneActionsApproveBudget, gen.LaneActionsCancel}},
		{"failed stall director → restart", gen.LaneStatusFailed, stall, false, true, []gen.LaneActions{gen.LaneActionsRestart}},
		{"failed runtime_offline director → rebind, no restart", gen.LaneStatusFailed, offline, false, true, []gen.LaneActions{}},
		{"done director, turn finished", gen.LaneStatusDone, none, false, true, []gen.LaneActions{}},
		// K-16 (openapi 0.1.6): `status set done` was called while the turn's
		// process is still alive — the Director can stop it.
		{"done director, turn still running → cancel", gen.LaneStatusDone, none, true, true, []gen.LaneActions{gen.LaneActionsCancel}},
		{"done member, turn still running → nothing (FR-3.4 t-3)", gen.LaneStatusDone, none, true, false, []gen.LaneActions{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := laneActions(c.status, c.failure, c.cancellable, c.control)
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}
