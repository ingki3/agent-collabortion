package observations

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// PRD §11 관찰 표 · openapi getWorkspaceObservations (T-S19). Each row is
// seeded and its numbers checked against the definition sentence by hand;
// then the empty workspace, where every row must be n 0 with null numbers.

var t0 = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func near(a *float64, want float64) bool {
	return a != nil && math.Abs(*a-want) < 1e-6
}

func TestObservationsOrderAndEmptyWorkspace(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Plant(t, pool, t0)
	rows, err := Compute(context.Background(), pool, s.WorkspaceID, 30*24*time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	for i, d := range Defs {
		r := rows[i]
		if r.Key != d.Key {
			t.Errorf("rows[%d].key = %s, want %s (§11 표 순서)", i, r.Key, d.Key)
		}
		if r.Value != nil || r.Median != nil || r.P95 != nil || r.N != 0 {
			t.Errorf("%s on an empty workspace = value %v median %v p95 %v n=%d, want null · 0", r.Key, deref(r.Value), deref(r.Median), deref(r.P95), r.N)
		}
		if r.Label == "" || r.Note == "" {
			t.Errorf("%s lacks label/note: %+v", r.Key, r)
		}
		if r.Key == "routing_concentration" {
			if len(r.Breakdown) != 9 {
				t.Errorf("breakdown = %+v, want rules 1~8 + platform", r.Breakdown)
			}
			for _, b := range r.Breakdown {
				if b.N != 0 || b.Share != 0 {
					t.Errorf("empty breakdown %s = %+v", b.Kind, b)
				}
			}
		} else if r.Breakdown != nil {
			t.Errorf("%s carries a breakdown", r.Key)
		}
	}
}

// TestObservationsFromSeed seeds one session's hops, lanes and attempts and
// reads every row back. Numbers are worked out by hand from the definitions.
func TestObservationsFromSeed(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Plant(t, pool, t0)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	at := func(m int) time.Time { return t0.Add(-time.Duration(m) * time.Minute) }
	agent := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, role, role_description, instructions, owner_id, created_at, updated_at)
			VALUES ($1, $2, 'researcher', 'd', 'i', $3, $4, $4) RETURNING id`, s.WorkspaceID, name, s.UserID, t0).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b, c := agent("A"), agent("B"), agent("C")
	lead := s.AgentID
	s1 := s.SessionID
	s2 := testdb.AddSession(t, pool, s, nil, at(50))
	// A session outside the window (its hops are 40 days old): counted by no row.
	s3 := testdb.AddSession(t, pool, s, nil, at(60*24*40))

	hop := func(sess uuid.UUID, from *uuid.UUID, to uuid.UUID, rule int, allowed bool, cause *int64, when time.Time) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, allowed, cause_hop_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, sess, from, to, rule, allowed, cause, when).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// S1 — human → Lead (rule 2); Lead delegates to A, B, C (rule 2, cause = h1:
	// siblings, depth 2); A mentions B (rule 2, cause = hA: depth 3); one hop
	// tripped the limit (allowed=false, still history for depth); the assignee
	// fallback fired twice (rules 6 and 7); then a second human message resets.
	h1 := hop(s1, nil, lead, 2, true, nil, at(100))
	hA := hop(s1, &lead, a, 2, true, &h1, at(99))
	_ = hop(s1, &lead, b, 2, true, &h1, at(99))
	_ = hop(s1, &lead, c, 2, true, &h1, at(99))
	hAB := hop(s1, &a, b, 2, true, &hA, at(98))
	_ = hop(s1, &b, lead, 9, true, &hA, at(97)) // join notice (platform) waking A's requester chain: cause = hA → depth 3
	_ = hop(s1, &b, a, 2, false, &hAB, at(96))  // refused (allowed=false): not a task, but the limiter saw depth 4
	_ = hop(s1, &lead, a, 6, true, &h1, at(95))
	_ = hop(s1, &lead, a, 7, true, &h1, at(94))
	h2 := hop(s1, nil, lead, 2, true, nil, at(90))
	_ = hop(s1, &lead, a, 2, true, &h2, at(89))
	// S2 — one human hop, nothing derived.
	_ = hop(s2, nil, lead, 2, true, nil, at(40))
	// S3 — old; a deep chain that must not be counted.
	o1 := hop(s3, nil, lead, 2, true, nil, at(60*24*40))
	o2 := hop(s3, &lead, a, 2, true, &o1, at(60*24*40))
	o3 := hop(s3, &a, b, 2, true, &o2, at(60*24*40))
	_ = hop(s3, &b, c, 2, true, &o3, at(60*24*40))

	// Join groups: T0 (Lead) delegated 3 lanes; T1 delegated 1 lane; an old
	// group of 5 outside the window.
	lane := func(sess uuid.UUID, from *uuid.UUID, when time.Time) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, delegated_from_task_id, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'done', $5, $5) RETURNING id`, sess, s.AgentID, s.ProfileID, from, when).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	task := func(l, sess uuid.UUID, attempt int, when time.Time) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, attempt, created_at, updated_at, started_at, finished_at)
			VALUES ($1, $2, $3, $4, 'completed', $5, $6, $6, $6, $6) RETURNING id`, l, sess, s.AgentID, s.ProfileID, attempt, when).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	l0 := lane(s1, nil, at(100))
	t0id := task(l0, s1, 1, at(100))
	for i := 0; i < 3; i++ {
		lane(s1, &t0id, at(99))
	}
	l1 := lane(s1, &t0id, at(99))
	t1 := task(l1, s1, 1, at(98))
	lane(s1, &t1, at(97))
	lOld := lane(s3, nil, at(60*24*40))
	tOld := task(lOld, s3, 1, at(60*24*40))
	for i := 0; i < 5; i++ {
		lane(s3, &tOld, at(60*24*40))
	}

	// Attempts: four completed in the window, one of them empty (the FR-7.2
	// row); one failed (not counted); one completed but old.
	attempt := func(tid uuid.UUID, n int, outcome string, when time.Time) {
		exec(`INSERT INTO task_attempt (task_id, attempt, outcome, finished_at, started_at) VALUES ($1, $2, $3, $4, $4)`, tid, n, outcome, when)
	}
	t2 := task(l0, s1, 2, at(80))
	attempt(t0id, 1, "completed", at(99))
	attempt(t1, 1, "completed", at(97))
	attempt(t2, 1, "failed", at(85))
	attempt(t2, 2, "completed", at(80))
	t3 := task(l1, s2, 1, at(30))
	attempt(t3, 1, "completed", at(30))
	attempt(tOld, 1, "completed", at(60*24*40))
	// The empty-turn row on t2 attempt 2 (what tasks.Finish writes), plus a
	// same-shaped row on the OLD attempt and a `turn_end` with another
	// object_ref on t3 — neither may count.
	exec(`INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
	      VALUES ($1, 2, 1073741824, 'status', 'turn_end', to_jsonb('empty_turn'::text), 'info', '{"command":"turn_end"}', $2)`, t2, at(80))
	exec(`INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
	      VALUES ($1, 1, 1073741824, 'status', 'turn_end', to_jsonb('empty_turn'::text), 'info', '{"command":"turn_end"}', $2)`, tOld, at(60*24*40))
	exec(`INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
	      VALUES ($1, 1, 1073741824, 'status', 'turn_end', to_jsonb('other'::text), 'info', '{"command":"turn_end"}', $2)`, t3, at(30))

	rows, err := Compute(ctx, pool, s.WorkspaceID, 30*24*time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Row{}
	for _, r := range rows {
		by[r.Key] = r
	}

	// 1. chain_scale — human hops in the window: h1 (derived: hA, hB, hC, hAB,
	// join, rule6, rule7 = 7 allowed agent hops; the refused one is not a
	// task), h2 (1), S2's (0). Sorted 0, 1, 7 → median 1, p95 = 1 + 6·0.9 = 6.4.
	r := by["chain_scale"]
	if r.N != 3 || !near(r.Median, 1) || !near(r.P95, 6.4) || r.Value != nil {
		t.Errorf("chain_scale = n %d median %v p95 %v value %v; want n 3 · 1 · 6.4 · null", r.N, deref(r.Median), deref(r.P95), deref(r.Value))
	}
	// 2. chain_depth — S1: human 1 → Lead's delegations 2 → A→B 3 → the refused
	// B→A 4 (the limiter saw it); S2: 1. S3 is outside the window. Sorted 1, 4 →
	// median 2.5, p95 3.85. The reading is router.MaxChainDepth's own.
	r = by["chain_depth"]
	if r.N != 2 || !near(r.Median, 2.5) || !near(r.P95, 3.85) {
		t.Errorf("chain_depth = n %d median %v p95 %v; want n 2 · 2.5 · 3.85", r.N, deref(r.Median), deref(r.P95))
	}
	// 3. join_breadth — groups T0 (4 lanes) and T1 (1 lane); the old group of 5
	// is outside. Sorted 1, 4 → median 2.5, p95 3.85.
	r = by["join_breadth"]
	if r.N != 2 || !near(r.Median, 2.5) || !near(r.P95, 3.85) {
		t.Errorf("join_breadth = n %d median %v p95 %v; want n 2 · 2.5 · 3.85", r.N, deref(r.Median), deref(r.P95))
	}
	// 4. routing_concentration — allowed hops in the window: rule 2 × 8 (h1,
	// hA, hB, hC, hAB, h2, h2→A, S2's), rule 6 × 1, rule 7 × 1, platform × 1 =
	// 11. Fallback share (6+7) = 2/11.
	r = by["routing_concentration"]
	if r.N != 11 || !near(r.Value, 2.0/11) || r.Median != nil || r.P95 != nil {
		t.Errorf("routing_concentration = n %d value %v median %v; want n 11 · 2/11 · null", r.N, deref(r.Value), deref(r.Median))
	}
	shares := map[string]Breakdown{}
	for _, b := range r.Breakdown {
		shares[b.Kind] = b
	}
	for kind, want := range map[string]int{"1": 0, "2": 8, "3": 0, "4": 0, "5": 0, "6": 1, "7": 1, "8": 0, "platform": 1} {
		got, ok := shares[kind]
		if !ok || got.N != want || math.Abs(got.Share-float64(want)/11) > 1e-6 {
			t.Errorf("breakdown[%s] = %+v, want n %d", kind, got, want)
		}
	}
	if len(r.Breakdown) != 9 || r.Breakdown[0].Kind != "1" || r.Breakdown[8].Kind != "platform" {
		t.Errorf("breakdown order = %+v", r.Breakdown)
	}
	// 5. empty_turn_rate — completed attempts in the window: t0/1, t1/1, t2/2,
	// t3/1 = 4; only t2/2 carries the FR-7.2 row → 0.25. The failed attempt and
	// the old one (with its own empty row) do not count.
	r = by["empty_turn_rate"]
	if r.N != 4 || !near(r.Value, 0.25) || r.Median != nil {
		t.Errorf("empty_turn_rate = n %d value %v; want n 4 · 0.25", r.N, deref(r.Value))
	}

	// A narrower window drops the older hops and attempts: only h2's chain and
	// S2 remain for chain_scale; t3 alone for the empty-turn rate (0).
	rows, err = Compute(ctx, pool, s.WorkspaceID, 45*time.Minute, t0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		switch r.Key {
		case "chain_scale":
			if r.N != 1 || !near(r.Median, 0) {
				t.Errorf("45m chain_scale = n %d median %v; want n 1 · 0 (S2's human hop only)", r.N, deref(r.Median))
			}
		case "empty_turn_rate":
			if r.N != 1 || !near(r.Value, 0) {
				t.Errorf("45m empty_turn_rate = n %d value %v; want n 1 · 0", r.N, deref(r.Value))
			}
		case "join_breadth":
			if r.N != 0 || r.Median != nil {
				t.Errorf("45m join_breadth = n %d median %v; want n 0 · null", r.N, deref(r.Median))
			}
		}
	}
}

// TestChainDepthRowReadsLikeTheLimiter pins the depth row to the router's own
// reading on a history the limiter is known to judge (S-78 shape: siblings at
// one depth, a join notice returning the requester to its own depth).
func TestChainDepthRowReadsLikeTheLimiter(t *testing.T) {
	lead, a, b := uuid.New(), uuid.New(), uuid.New()
	h := []router.Hop{
		{ID: 1, ToAgent: lead},                           // human → Lead: 1
		{ID: 2, FromAgent: lead, ToAgent: a, CauseID: 1}, // Lead → A: 2
		{ID: 3, FromAgent: lead, ToAgent: b, CauseID: 1}, // Lead → B: 2 (sibling)
		{ID: 4, FromAgent: a, ToAgent: b, CauseID: 2},    // A → B: 3
		{ID: 5, FromAgent: b, ToAgent: lead, CauseID: 1}, // join notice wakes Lead at ITS depth's cause: 2
		{ID: 6, FromAgent: lead, ToAgent: a, CauseID: 5}, // Lead re-delegates: 3
	}
	if got := router.MaxChainDepth(h); got != 3 {
		t.Errorf("MaxChainDepth = %d, want 3", got)
	}
	if got := percentile([]float64{1, 4}, 0.95); math.Abs(got-3.85) > 1e-9 {
		t.Errorf("percentile(0.95) = %v, want 3.85 (percentile_cont)", got)
	}
	if got := percentile([]float64{7}, 0.5); got != 7 {
		t.Errorf("percentile of one = %v", got)
	}
}
