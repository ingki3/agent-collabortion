package lanedone_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/lanedone"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestMarkDoneTable pins MarkDone's decision per cause and starting state,
// including whether the follow-up and the publish hook run.
//
// 회귀 주입: runsFollowUp 의 TurnEnd 를 false 로 → (TurnEnd·idle) FAIL
// (T-FIX-B); AgentDone 을 false 로 → (AgentDone·*) FAIL; Became 대신 Done 으로
// 판정하면 (*·done) FAIL(후속이 두 번); TurnEnd CASE 의 queued 가지를
// 지우면 (TurnEnd·queued) FAIL; `status <> 'blocked'` 를 지우면
// (TurnEnd·blocked) FAIL; Publish 호출을 지우면 모든 행 FAIL.
func TestMarkDoneTable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)

	rows := []struct {
		name      string
		cause     lanedone.Cause
		start     string // lane status before
		queued    bool   // a second, queued task on the lane
		want      string
		wantAfter bool
	}{
		{"AgentDone·running", lanedone.AgentDone, "running", false, "done", true},
		{"AgentDone·queued", lanedone.AgentDone, "running", true, "done", true},
		{"AgentDone·blocked", lanedone.AgentDone, "blocked", false, "done", true},
		{"AgentDone·done", lanedone.AgentDone, "done", false, "done", false},
		{"TurnEnd·idle", lanedone.TurnEnd, "running", false, "done", true},
		{"TurnEnd·queued", lanedone.TurnEnd, "running", true, "queued", false},
		{"TurnEnd·blocked", lanedone.TurnEnd, "blocked", false, "blocked", false},
		// status set done, then the turn's end: the lane is already done —
		// the follow-up ran with the first, not again (T-FIX-B 멱등).
		{"TurnEnd·done", lanedone.TurnEnd, "done", false, "done", false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			task := testdb.AddTask(t, pool, seed, seed.SessionID, now)
			var lane uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT lane_id FROM task WHERE id = $1`, task).Scan(&lane); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE task SET status = 'running' WHERE id = $1`, task); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE lane SET status = $2 WHERE id = $1`, lane, r.start); err != nil {
				t.Fatal(err)
			}
			if r.queued {
				if _, err := pool.Exec(ctx, `
					INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, status, created_at, updated_at)
					SELECT lane_id, session_id, agent_id, profile_id, trigger_message_id, 'queued', $2, $2 FROM task WHERE id = $1`,
					task, now); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			var published, after int
			res, err := lanedone.MarkDone(ctx, tx, lanedone.Request{
				LaneID: lane, Cause: r.cause, Now: now.Add(time.Minute),
				Publish:   func(context.Context, pgx.Tx, uuid.UUID) { published++ },
				AfterDone: func(context.Context, pgx.Tx) error { after++; return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if err := tx.QueryRow(ctx, `SELECT status::text FROM lane WHERE id = $1`, lane).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != r.want || res.Status != r.want {
				t.Fatalf("lane = %s (result %s), want %s", got, res.Status, r.want)
			}
			if published != 1 {
				t.Fatalf("publish ran %d times, want 1", published)
			}
			if (after == 1) != r.wantAfter || after > 1 {
				t.Fatalf("AfterDone ran %d times, want %v", after, r.wantAfter)
			}
		})
	}
}
