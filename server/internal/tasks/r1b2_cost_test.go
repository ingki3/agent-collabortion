package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestRollUpCostIsPerMission is V19_R1B_HANDOFF (a) for the finish roll-up:
// `UPDATE work SET cost_usd = <room total> WHERE room_id = …` wrote the room's
// whole spend into EVERY mission of the room, so a second mission showed its
// neighbour's cost (and the old session's budget banner doubled). Each
// mission's cost is its own tasks'; a task outside any mission counts toward
// the room only.
func TestRollUpCostIsPerMission(t *testing.T) {
	s, c, seed := newService(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	w1 := func() uuid.UUID {
		var id uuid.UUID
		if err := s.DB.QueryRow(ctx, `SELECT legacy_work_id FROM room WHERE id = $1`, seed.SessionID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}()
	var w2 uuid.UUID
	if err := s.DB.QueryRow(ctx, `
		INSERT INTO work (room_id, title, goal, director_user_id, status, created_by) VALUES ($1, 'M2', 'g', $2, 'active', $2) RETURNING id`,
		seed.SessionID, seed.UserID).Scan(&w2); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		work *uuid.UUID
		cost float64
	}{{&w1, 1.0}, {&w2, 0.25}, {nil, 0.5}} {
		task := testdb.AddTask(t, s.DB, seed, seed.SessionID, c.Now())
		exec(`UPDATE task SET work_id = $2 WHERE id = $1`, task, x.work)
		exec(`INSERT INTO task_usage (task_id, cost_usd) VALUES ($1, $2)`, task, x.cost)
	}
	if err := s.rollUpCost(ctx, seed.WorkspaceID, seed.SessionID, c.Now()); err != nil {
		t.Fatal(err)
	}
	var c1, c2 float64
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT cost_usd FROM work WHERE id = $1)::float8, (SELECT cost_usd FROM work WHERE id = $2)::float8`, w1, w2).Scan(&c1, &c2); err != nil {
		t.Fatal(err)
	}
	if c1 != 1.0 || c2 != 0.25 {
		t.Fatalf("mission costs = %v / %v, want 1.00 / 0.25 (each its own; the room total is 1.75)", c1, c2)
	}
}
