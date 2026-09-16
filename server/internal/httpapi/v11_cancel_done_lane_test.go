package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// TestV11CancelAfterStatusDone is K-16 on the wire (openapi 0.1.6 cancelLane,
// Lane.actions): an agent calls `colab status set done` and its turn keeps
// running. The lane card must still offer 중단, the cancel must be accepted
// (202, not `409 lane_not_cancellable`), and when the daemon reports the
// stop the task is `cancelled` while the lane stays `done`.
func TestV11CancelAfterStatusDone(t *testing.T) {
	f := newG4Fixture(t)
	taskID := f.runningTask(t, "R", f.rUUID)
	var laneID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id FROM task WHERE id = $1`, taskID).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	tok, err := f.srv.Tokens.Issue(t.Context(), f.pool, tokens.Scope{
		TaskID: taskID, Attempt: 1, LaneID: laneID, SessionID: mustUUID(t, f.sessionID), AgentID: f.rUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := &client{t: t, srv: f.api.srv, bearer: tok}
	agent.must(200, "POST", f.p+"/tasks/"+taskID.String()+"/status", map[string]any{"status": "done"})

	lane := f.laneCard(t, laneID)
	if str(lane, "status") != "done" {
		t.Fatalf("lane after status set done = %q, want done", str(lane, "status"))
	}
	if !hasAction(lane, "cancel") {
		t.Fatalf("Lane.actions = %v, want cancel — the turn's process is still running (K-16)", lane["actions"])
	}
	if got := str(lane["current_task"].(map[string]any), "status"); got != "running" {
		t.Fatalf("current_task = %q, want running", got)
	}

	f.api.must(202, "POST", f.p+"/lanes/"+laneID.String()+"/cancel", map[string]any{})
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID.String()+"/attempts/1/finish", contracts.Finish{
		Outcome: "cancelled", StopReason: "cancelled",
	})
	if st := f.taskStatus(t, taskID); st != "cancelled" {
		t.Fatalf("task = %q, want cancelled", st)
	}
	lane = f.laneCard(t, laneID)
	if str(lane, "status") != "done" {
		t.Fatalf("lane = %q after the stop, want done — the output was submitted (K-16)", str(lane, "status"))
	}
	if hasAction(lane, "cancel") {
		t.Fatalf("Lane.actions = %v after the stop, want no cancel", lane["actions"])
	}
	if st, out, _ := f.api.do("POST", f.p+"/lanes/"+laneID.String()+"/cancel", map[string]any{}); st != 409 || str(out, "code") != "lane_not_cancellable" {
		t.Fatalf("cancel with nothing running = %d %v, want 409 lane_not_cancellable", st, out)
	}
}

// A `done` lane whose turn ended normally offers nothing — the contract's
// 409 is still the answer when there is no process to stop.
func TestV11DoneLaneWithFinishedTurnIsNotCancellable(t *testing.T) {
	f := newG4Fixture(t)
	taskID := f.runningTask(t, "R", f.rUUID)
	var laneID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id FROM task WHERE id = $1`, taskID).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID.String()+"/attempts/1/finish", contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
	})
	lane := f.laneCard(t, laneID)
	if str(lane, "status") != "done" || hasAction(lane, "cancel") {
		t.Fatalf("lane = %q actions %v, want done with no cancel", str(lane, "status"), lane["actions"])
	}
	if st, out, _ := f.api.do("POST", f.p+"/lanes/"+laneID.String()+"/cancel", map[string]any{}); st != 409 || str(out, "code") != "lane_not_cancellable" {
		t.Fatalf("= %d %v, want 409 lane_not_cancellable", st, out)
	}
}

// laneCard is the S7 card for one lane, read through listLanes (getLane is
// not served).
func (f *p2Fixture) laneCard(t *testing.T, laneID uuid.UUID) map[string]any {
	t.Helper()
	for _, raw := range f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil) {
		if l, _ := raw.(map[string]any); str(l, "id") == laneID.String() {
			return l
		}
	}
	t.Fatalf("lane %s not in listLanes", laneID)
	return nil
}

func hasAction(lane map[string]any, want string) bool {
	acts, _ := lane["actions"].([]any)
	for _, a := range acts {
		if a == want {
			return true
		}
	}
	return false
}
