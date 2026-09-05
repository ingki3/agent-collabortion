package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

func laneStatus(t *testing.T, s *Service, laneID uuid.UUID) string {
	t.Helper()
	var st string
	if err := s.DB.QueryRow(context.Background(), `SELECT status::text FROM lane WHERE id = $1`, laneID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func cancelFeedNotes(t *testing.T, s *Service, taskID uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'status' AND verb = 'cancel' AND payload->>'note' = '사람이 중단함'`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// cancelLane on a running attempt (E10-04, daemon-protocol §4.3): the server
// queues cancel{after_current_tool, reason: director} for the holding runtime,
// re-sends it until the attempt's finish arrives, and the daemon's
// finish(cancelled) ends the task cancelled(failure_kind cancelled), the lane
// failed, the token revoked — with no requeue and no new task.
func TestCancelLaneRunning(t *testing.T) {
	s, c, seed := newService(t)
	ctx := context.Background()
	taskID := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	tok := dispatch(t, s, seed, taskID)
	for _, ph := range []string{"preparing", "running"} {
		if err := s.Phase(ctx, taskID, 1, ph); err != nil {
			t.Fatal(err)
		}
	}
	laneID := row(t, s, taskID).LaneID

	got, immediate, err := s.CancelLane(ctx, laneID, seed.UserID)
	if err != nil || immediate {
		t.Fatalf("CancelLane = immediate %v err %v, want pending", immediate, err)
	}
	if got.Status != Running || row(t, s, taskID).Status != Running || laneStatus(t, s, laneID) != "running" {
		t.Fatalf("running attempt must stay running until finish: task %s lane %s", row(t, s, taskID).Status, laneStatus(t, s, laneID))
	}
	cmds, err := tokens.PendingCommands(ctx, s.DB, seed.RuntimeID, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	var cancel *contracts.Command
	for i := range cmds {
		if cmds[i].Type == contracts.CmdCancel {
			cancel = &cmds[i]
		}
	}
	if cancel == nil || cancel.TaskID != taskID.String() || cancel.Attempt != 1 || !cancel.AfterCurrentTool || cancel.Reason != "director" {
		t.Fatalf("pending commands = %+v, want cancel{task, attempt 1, after_current_tool, director}", cmds)
	}
	if n := cancelFeedNotes(t, s, taskID); n != 1 {
		t.Fatalf("feed '사람이 중단함' rows = %d, want 1", n)
	}
	// A repeated "중단" adds neither a second command nor a second feed row.
	if _, _, err := s.CancelLane(ctx, laneID, seed.UserID); err != nil {
		t.Fatal(err)
	}
	cmds, _ = tokens.PendingCommands(ctx, s.DB, seed.RuntimeID, c.Now())
	if len(cmds) != 1 || cancelFeedNotes(t, s, taskID) != 1 {
		t.Fatalf("second cancel must be a no-op: commands %d feed %d", len(cmds), cancelFeedNotes(t, s, taskID))
	}
	// The token is still valid while the daemon drains (harness §5).
	if _, err := s.Tokens.Verify(ctx, s.DB, tok); err != nil {
		t.Fatalf("token before finish: %v", err)
	}

	// daemon: finish outcome=cancelled (the command's §4.3 consumption is the
	// HTTP layer's job; the service decides the final state).
	final, err := s.Finish(ctx, taskID, 1, contracts.Finish{Outcome: "cancelled", StopReason: "cancelled"})
	if err != nil || final != Cancelled {
		t.Fatalf("Finish = %s %v, want cancelled", final, err)
	}
	r := row(t, s, taskID)
	if r.Status != Cancelled || r.FailureKind == nil || *r.FailureKind != "cancelled" || r.Attempt != 1 || r.FinishedAt == nil {
		t.Fatalf("task after finish = %+v, want cancelled/failure_kind cancelled/attempt 1", r)
	}
	if laneStatus(t, s, laneID) != "failed" {
		t.Fatalf("lane = %s, want failed(cancelled)", laneStatus(t, s, laneID))
	}
	if _, err := s.Tokens.Verify(ctx, s.DB, tok); !errors.Is(err, tokens.ErrRevoked) {
		t.Fatalf("token after cancel: %v, want revoked", err)
	}
	var queued int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM task WHERE lane_id = $1 AND status = 'queued'`, laneID).Scan(&queued)
	if queued != 0 {
		t.Fatalf("queued tasks on the lane = %d, want 0 (E10-04: 새 task 없음)", queued)
	}
	// Terminal lane: another cancel is 409.
	if _, _, err := s.CancelLane(ctx, laneID, seed.UserID); !errors.Is(err, ErrLaneNotCancellable) {
		t.Fatalf("cancel on failed lane = %v, want ErrLaneNotCancellable", err)
	}
}

// After the cancel command, a daemon finish(failed) (the D-1 race: ctx
// cancellation beats the cancel procedure) and a heartbeat expiry are both the
// cancel taking effect — the task ends cancelled, never requeued (E10-04).
func TestCancelLaneNoRequeueAfterCancel(t *testing.T) {
	s, c, seed := newService(t)
	ctx := context.Background()

	byFinish := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	byExpiry := testdb.AddTask(t, s.DB, seed, testdb.AddSession(t, s.DB, seed, nil, t0), t0)
	for _, id := range []uuid.UUID{byFinish, byExpiry} {
		dispatch(t, s, seed, id)
		for _, ph := range []string{"preparing", "running"} {
			if err := s.Phase(ctx, id, 1, ph); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := s.CancelLane(ctx, row(t, s, id).LaneID, seed.UserID); err != nil {
			t.Fatal(err)
		}
	}
	final, err := s.Finish(ctx, byFinish, 1, contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther})
	if err != nil || final != Cancelled {
		t.Fatalf("finish(failed) after cancel = %s %v, want cancelled", final, err)
	}
	c.Advance(contracts.HeartbeatExpiry + time.Second)
	if _, err := s.ExpireStale(ctx, c.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{byFinish, byExpiry} {
		r := row(t, s, id)
		if r.Status != Cancelled || r.Attempt != 1 || r.FailureKind == nil || *r.FailureKind != "cancelled" {
			t.Fatalf("task %s = %s attempt %d failure_kind %v, want cancelled/1/cancelled (no requeue)", id, r.Status, r.Attempt, r.FailureKind)
		}
		if laneStatus(t, s, r.LaneID) != "failed" {
			t.Fatalf("lane of %s = %s, want failed", id, laneStatus(t, s, r.LaneID))
		}
	}
}

// A queued lane (nothing claimed yet) is cancelled at once; a completed lane
// answers 409; a lane nobody knows is not found.
func TestCancelLaneQueuedAndTerminal(t *testing.T) {
	s, _, seed := newService(t)
	ctx := context.Background()
	taskID := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	laneID := row(t, s, taskID).LaneID

	got, immediate, err := s.CancelLane(ctx, laneID, seed.UserID)
	if err != nil || !immediate || got.Status != Cancelled {
		t.Fatalf("CancelLane(queued) = %v immediate %v err %v, want cancelled at once", got.Status, immediate, err)
	}
	r := row(t, s, taskID)
	if r.Status != Cancelled || r.FailureKind == nil || *r.FailureKind != "cancelled" || laneStatus(t, s, laneID) != "failed" {
		t.Fatalf("queued cancel → task %s lane %s", r.Status, laneStatus(t, s, laneID))
	}
	if n := cancelFeedNotes(t, s, taskID); n != 1 {
		t.Fatalf("feed rows = %d, want 1", n)
	}
	var cmds int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM daemon_command WHERE task_id = $1`, taskID).Scan(&cmds)
	if cmds != 0 {
		t.Fatalf("queued cancel must not queue daemon commands, got %d", cmds)
	}

	done := testdb.AddTask(t, s.DB, seed, testdb.AddSession(t, s.DB, seed, nil, t0), t0)
	dispatch(t, s, seed, done)
	for _, ph := range []string{"preparing", "running"} {
		if err := s.Phase(ctx, done, 1, ph); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Finish(ctx, done, 1, contracts.Finish{Outcome: "completed"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CancelLane(ctx, row(t, s, done).LaneID, seed.UserID); !errors.Is(err, ErrLaneNotCancellable) {
		t.Fatalf("cancel on done lane = %v, want ErrLaneNotCancellable", err)
	}
	if _, _, err := s.CancelLane(ctx, uuid.New(), seed.UserID); !errors.Is(err, ErrLaneNotFound) {
		t.Fatalf("cancel unknown lane = %v, want ErrLaneNotFound", err)
	}
}
