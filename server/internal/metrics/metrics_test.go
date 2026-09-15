package metrics

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// PRD §11 · openapi getWorkspaceMetrics (T-S12). A few seeded rows per metric,
// each value checked against the definition sentence by hand; then the empty
// workspace, where every one of the ten must be null · n 0.

var t0 = time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

func TestParseWindow(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":       30 * 24 * time.Hour,
		"P30D":   30 * 24 * time.Hour,
		"P2W":    14 * 24 * time.Hour,
		"PT12H":  12 * time.Hour,
		"P1M":    30 * 24 * time.Hour,
		"P1DT6H": 30 * time.Hour,
	} {
		got, err := ParseWindow(in)
		if err != nil || got != want {
			t.Errorf("ParseWindow(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"30d", "P", "PT", "1 month", "P-3D"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("ParseWindow(%q) accepted", bad)
		}
	}
}

func TestMetricsOrderAndEmptyWorkspace(t *testing.T) {
	pool := testdb.New(t)
	s := testdb.Plant(t, pool, t0)
	ms, err := Compute(context.Background(), pool, s.WorkspaceID, 30*24*time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 10 {
		t.Fatalf("metrics = %d, want 10", len(ms))
	}
	for i, d := range Defs {
		m := ms[i]
		if m.Key != d.Key {
			t.Errorf("metrics[%d].key = %s, want %s (§11 column order)", i, m.Key, d.Key)
		}
		if m.Value != nil || m.N != 0 {
			t.Errorf("%s on an empty workspace = %v n=%d, want null · 0 (0 은 실측처럼 보이지 않는다)", m.Key, deref(m.Value), m.N)
		}
		if m.Label == "" || m.Note == "" || m.Target == 0 || m.TargetOp == "" {
			t.Errorf("%s lacks label/note/target: %+v", m.Key, m)
		}
		if m.Key == "task_success_rate_by_runtime" {
			if len(m.Breakdown) != 2 {
				t.Errorf("breakdown = %+v, want the two v1 runtime kinds", m.Breakdown)
			}
			for _, b := range m.Breakdown {
				if b.Value != nil || b.N != 0 {
					t.Errorf("empty breakdown %s = %v n=%d", b.Kind, deref(b.Value), b.N)
				}
			}
		}
	}
}

func TestMetricsFromSeed(t *testing.T) {
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

	// 1. f1 — the user's first machine came online 100 min ago; their first
	// completed session ended 70 min ago → 30 min.
	exec(`INSERT INTO runtime_pairing (workspace_id, code_hash, status, runtime_id, created_by, expires_at, created_at, connected_at, ready_at)
	      VALUES ($1, 'h1', 'ready', $2, $3, $4, $5, $5, $5)`, s.WorkspaceID, s.RuntimeID, s.UserID, at(0), at(100))
	// S1: completed by its conditions (auto), 2 lanes, 30-minute wall clock.
	s1 := s.SessionID
	exec(`UPDATE session SET status = 'completed', started_at = $2, finished_at = $3, completion_met = '{"artifact_submitted":true}' WHERE id = $1`, s1, at(100), at(70))
	// S2: ended by the Director (manual), 1 lane.
	s2 := testdb.AddSession(t, pool, s, &s.RuntimeID, at(30))
	exec(`UPDATE session SET status = 'completed', started_at = $2, finished_at = $3, completion_met = '{"manual":true}' WHERE id = $1`, s2, at(30), at(10))
	// S3: completed but too old for the window — 3. counts only within it.
	// Its Director is a second user, so 1. (per-user FIRST completed session)
	// is not skewed by a session that ended before the machine was paired.
	s3 := testdb.AddSession(t, pool, s, &s.RuntimeID, at(60*24*40))
	var u2 uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name, created_at) VALUES ('u2@example.com', 'U2', $1) RETURNING id`, t0).Scan(&u2); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE session SET status = 'completed', director_user_id = $3, started_at = $2, finished_at = $2, completion_met = '{"manual":true}' WHERE id = $1`, s3, at(60*24*40), u2)

	// 3. HITL — 20 min and 10 min answered here (T2's below adds a 0), one auto-answered (excluded).
	hitl := func(sess uuid.UUID, created, answered time.Time, status string) {
		exec(`INSERT INTO hitl_request (session_id, source, type, question, approver_spec, due_at, status, answered_at, created_at)
		      VALUES ($1, 'system', 'approval', 'q', 'director', $2, $3, $4, $5)`, sess, created.Add(24*time.Hour), status, answered, created)
	}
	hitl(s1, at(60), at(40), "answered")
	hitl(s1, at(30), at(20), "answered")
	hitl(s2, at(25), at(5), "auto_answered")

	// Lanes and tasks on S1. T0 is the delegator; T1 · T2 were delegated from
	// it; T3 failed. Profile is claude_code (Plant).
	lane := func(sess uuid.UUID) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'done', $4, $4) RETURNING id`,
			sess, s.AgentID, s.ProfileID, at(100)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	l1, l2 := lane(s1), lane(s1)
	task := func(l, sess uuid.UUID, status string, attempt int, from *uuid.UUID, started, finished time.Time) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, attempt, delegated_from_task_id, created_at, updated_at, started_at, finished_at)
		      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $8, $9) RETURNING id`,
			l, sess, s.AgentID, s.ProfileID, status, attempt, from, started, finished).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	t0id := task(l1, s1, "completed", 1, nil, at(100), at(98))
	t1 := task(l1, s1, "completed", 2, &t0id, at(100), at(80)) // 20 min, attempt 2
	t2 := task(l2, s1, "completed", 2, &t0id, at(95), at(75))  // 20 min, attempt 2, HAS a HITL
	t3 := task(l2, s1, "failed", 1, nil, at(74), at(72))
	exec(`INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, due_at, status, answered_at, created_at)
	      VALUES ($1, $2, 'agent', 'approval', 'q', 'director', $3, 'answered', $4, $4)`, s1, t2, at(0), at(76))
	// S2's lane ran a task 10 days ago — the session is "ever active", not this week.
	exec(`INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, created_at, updated_at, started_at, finished_at)
	      VALUES ($1, $2, $3, $4, 'completed', $5, $5, $5, $5)`, lane(s2), s2, s.AgentID, s.ProfileID, at(60*24*10))
	_ = t3

	// 7. duplicates — T1 (attempt 2) posted the same message twice; T2 did not.
	msg := func(sess uuid.UUID, task *uuid.UUID, kind, content string, parent *uuid.UUID, when time.Time) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO message (session_id, author_type, author_id, content, source_task_id, kind, parent_id, created_at)
		      VALUES ($1, 'agent', $2, $3, $4, $5, $6, $7) RETURNING id`, sess, s.AgentID, content, task, kind, parent, when).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	msg(s1, &t1, "text", "결과입니다", nil, at(85))
	msg(s1, &t1, "text", "결과입니다", nil, at(81))
	msg(s1, &t2, "text", "다른 결과", nil, at(76))

	// 8. resume — three attempts with a resume verdict: two resumed, one cold.
	att := func(task uuid.UUID, attempt int, resumed *bool, when time.Time) {
		exec(`INSERT INTO task_attempt (task_id, attempt, runtime_id, finished_at, outcome, resumed) VALUES ($1, $2, $3, $4, 'completed', $5)`,
			task, attempt, s.RuntimeID, when, resumed)
	}
	tr, fa := true, false
	att(t1, 1, nil, at(90)) // no verdict: not a sample
	att(t1, 2, &tr, at(80))
	att(t2, 1, &tr, at(85))
	att(t2, 2, &fa, at(75))

	// 9. blocked — the question card at -50, first reply at -45 → 5 min.
	q := msg(s1, &t1, "blocked_q", "어느 쪽?", nil, at(50))
	msg(s1, nil, "text", "왼쪽", &q, at(45))
	msg(s1, nil, "text", "덧붙임", &q, at(40))

	ms, err := Compute(ctx, pool, s.WorkspaceID, 30*24*time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Metric{}
	for _, m := range ms {
		by[m.Key] = m
	}
	want := []struct {
		key   string
		value float64
		n     int
	}{
		{"f1_minutes", 30, 1},                              // paired at -100, first completed session at -70
		{"auto_complete_rate", 0.5, 2},                     // S1 by its conditions, S2 by the Director; S3 is outside the window
		{"hitl_response_minutes", 10, 3},                   // 20 · 10 · 0 (T2's, answered at creation) → median 10; auto_answered excluded
		{"delegation_autonomous_rate", 0.5, 2},             // T1 clean, T2 had a HITL
		{"parallel_wallclock_reduction", 1 - 30.0/44.0, 1}, // wall 30 vs T0 2 + T1 20 + T2 20 + T3 2
		{"task_success_rate_by_runtime", 4.0 / 5.0, 5},     // T0 · T1 · T2 · S2's task completed, T3 failed
		{"duplicate_after_resume_rate", 0.5, 2},            // T1 posted the same content twice, T2 did not
		{"resume_success_rate", 2.0 / 3.0, 3},              // two resumed, one cold start; the verdict-less attempt is no sample
		{"blocked_response_minutes", 5, 1},                 // card at -50, first reply at -45
		{"weekly_active_sessions", 1, 2},                   // S1 this week; S2 ran 10 days ago
	}
	for _, w := range want {
		m, ok := by[w.key]
		if !ok {
			t.Fatalf("%s missing", w.key)
		}
		if m.N != w.n {
			t.Errorf("%s n = %d, want %d", w.key, m.N, w.n)
		}
		if m.Value == nil || math.Abs(*m.Value-w.value) > 1e-6 {
			t.Errorf("%s = %v, want %.4f", w.key, deref(m.Value), w.value)
		}
	}
	// 6. breakdown: everything ran on claude_code; hermes has no sample.
	bd := by["task_success_rate_by_runtime"].Breakdown
	if len(bd) != 2 || bd[0].Kind != "claude_code" || bd[0].N != 5 || bd[0].Value == nil || math.Abs(*bd[0].Value-0.8) > 1e-6 || bd[0].Target != 0.95 {
		t.Errorf("claude_code breakdown = %+v", bd)
	}
	if bd[1].Kind != "hermes" || bd[1].N != 0 || bd[1].Value != nil || bd[1].Target != 0.85 {
		t.Errorf("hermes breakdown = %+v", bd[1])
	}
	if by["task_success_rate_by_runtime"].Target != 0.85 {
		t.Errorf("overall target = %v, want the lowest kind target 0.85", by["task_success_rate_by_runtime"].Target)
	}

	// A one-hour window sees only what ended inside it.
	ms, err = Compute(ctx, pool, s.WorkspaceID, time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		by[m.Key] = m
	}
	if m := by["auto_complete_rate"]; m.N != 1 || m.Value == nil || *m.Value != 0 {
		t.Errorf("auto_complete_rate over 1h = %v n=%d, want 0 · 1 (only S2, manual)", deref(m.Value), m.N)
	}
	if m := by["weekly_active_sessions"]; m.N != 2 || m.Value == nil || *m.Value != 1 {
		t.Errorf("weekly_active_sessions ignores the window: got %v n=%d", deref(m.Value), m.N)
	}
}

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
