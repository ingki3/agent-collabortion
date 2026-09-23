package workdirs

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestDisposableNow is FR-6.4 v0.19's clock for a `none`·`container`
// directory, as a table.
func TestDisposableNow(t *testing.T) {
	day := 24 * time.Hour
	for _, tc := range []struct {
		name                 string
		openMission, outside bool
		sinceLastUse         time.Duration
		retention            int
		want                 bool
	}{
		{"its mission is still open (lane done)", true, false, 30 * day, 14, false},
		{"its mission closed", false, false, 0, 14, true},
		{"outside any mission, inside retention", false, true, 13 * day, 14, false},
		{"outside any mission, past retention", false, true, 14 * day, 14, true},
		{"an open mission holds it even with an old outside lane", true, true, 30 * day, 14, false},
		{"no workspace setting → the default window", false, true, 13 * day, -1, false},
	} {
		if got := disposableNow(tc.openMission, tc.outside, tc.sinceLastUse, tc.retention); got != tc.want {
			t.Errorf("%s: disposable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSweepGCNoneFollowsTheMission pins the same clock against real rows —
// the priority item of V19_R1B_HANDOFF: GC deletes files, so WHEN it does
// is checked on the sweep itself. A `none` directory whose lane is done while
// its mission is open stays (R1a deleted it the moment no lane was live); it
// goes as soon as the mission closes; a directory of a run outside any
// mission waits for `workdir_retention_days` from its last use.
func TestSweepGCNoneFollowsTheMission(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	if _, err := pool.Exec(ctx, `UPDATE room SET runtime_id = $2 WHERE id = $1`, seed.SessionID, seed.RuntimeID); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, clock.NewFake(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var work uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM work WHERE room_id = $1`, seed.SessionID).Scan(&work); err != nil {
		t.Fatal(err)
	}
	dir := func(path string, mission *uuid.UUID, lastUse time.Time) uuid.UUID {
		t.Helper()
		agent := seed.AgentID
		wd, err := Record(ctx, pool, Report{Kind: "dir", Path: path, SessionID: seed.SessionID, AgentID: &agent}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE workdir SET last_used_at = $2 WHERE id = $1`, wd, lastUse); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO lane (session_id, agent_id, profile_id, workdir_id, status, work_id)
			VALUES ($1, $2, $3, $4, 'done', $5)`, seed.SessionID, seed.AgentID, seed.ProfileID, wd, mission); err != nil {
			t.Fatal(err)
		}
		return wd
	}
	collected := func(wd uuid.UUID) bool {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM daemon_command WHERE type = 'gc' AND $1::text = ANY(gc_command_workdir_ids(payload))`, wd).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	sweep := func() {
		t.Helper()
		if _, err := svc.SweepGC(ctx); err != nil {
			t.Fatal(err)
		}
	}

	inMission := dir("/Users/x/.colab/w/in-mission", &work, now.Add(-30*24*time.Hour))
	outsideFresh := dir("/Users/x/.colab/w/outside-fresh", nil, now.Add(-time.Hour))
	outsideOld := dir("/Users/x/.colab/w/outside-old", nil, now.Add(-15*24*time.Hour))
	sweep()
	if collected(inMission) {
		t.Error("a done lane's folder was collected while its mission is open — the mission can send the agent back into it")
	}
	if collected(outsideFresh) {
		t.Error("a folder outside any mission was collected an hour after its last use — it keeps workdir_retention_days")
	}
	if !collected(outsideOld) {
		t.Error("a folder outside any mission 15 days idle was not collected (retention 14 days)")
	}

	if _, err := pool.Exec(ctx, `UPDATE work SET status = 'completed', finished_at = $2 WHERE id = $1`, work, now); err != nil {
		t.Fatal(err)
	}
	sweep()
	if !collected(inMission) {
		t.Error("the mission closed and its `none` folder was not collected at once (FR-6.4: 매인 미션이 닫히면 즉시)")
	}
	if collected(outsideFresh) {
		t.Error("closing a mission collected a folder outside it")
	}
}

// TestSweepGCOneRowPerDirectory is V19_R1B_HANDOFF 우선 처리 sweep.go:118: the
// sweep joined "the room's mission" (`JOIN work ON work.room_id = room.id`),
// so once a room holds two missions every directory came back twice — and a
// GC that deletes files queued each twice. The test lifts R1b1's
// work_room_single for its own database (R1b2 lifts it for real).
func TestSweepGCOneRowPerDirectory(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	for _, q := range []string{
		`UPDATE room SET runtime_id = '` + seed.RuntimeID.String() + `' WHERE id = '` + seed.SessionID.String() + `'`,
		`DROP INDEX work_room_single`,
		`INSERT INTO work (room_id, title, goal, director_user_id, status, created_by)
		 VALUES ('` + seed.SessionID.String() + `', 'M2', 'g', '` + seed.UserID.String() + `', 'active', '` + seed.UserID.String() + `')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	agent := seed.AgentID
	wd, err := Record(ctx, pool, Report{Kind: "dir", Path: "/Users/x/.colab/w/old", SessionID: seed.SessionID, AgentID: &agent}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workdir SET last_used_at = $2 WHERE id = $1`, wd, now.Add(-20*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, clock.NewFake(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := svc.SweepGC(ctx); err != nil {
		t.Fatal(err)
	}
	var mentions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_command c, jsonb_array_elements(c.payload->'workdirs') e
		WHERE c.type = 'gc' AND e->>'id' = $1::text`, wd).Scan(&mentions); err != nil {
		t.Fatal(err)
	}
	if mentions != 1 {
		t.Fatalf("the directory appears %d times in gc commands, want 1 — one row per directory however many missions its room has", mentions)
	}
}
