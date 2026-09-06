package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestLoad needs no database: the embedded set must parse and start at 0001.
func TestLoad(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != 1 || ms[0].Name != "init" {
		t.Fatalf("unexpected first migration: %+v", ms)
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].Version != ms[i-1].Version+1 {
			t.Fatalf("migration versions must be contiguous: %04d follows %04d", ms[i].Version, ms[i-1].Version)
		}
	}
}

// prdTables is PRD.md §7 plus test_chat (FR-1.8.1) plus app_user (FK target,
// agreed with Lead) plus the runner's own bookkeeping table.
var prdTables = []string{
	"workspace", "member", "app_user", "agent", "agent_profile", "runtime", "workdir",
	"session", "session_participant", "session_context", "lane", "task", "task_event",
	"task_usage", "message", "hitl_request", "inbox_item", "artifact", "decision",
	"activity_log", "workspace_settings", "test_chat",
	"schema_migrations",
	// 0002_p1_auth_and_stream.sql (G2 Q1 + daemon-protocol bookkeeping)
	"user_session", "workspace_invite", "runtime_pairing", "session_subscription", "artifact_review",
	"idempotency_key", "stream_event", "task_token", "daemon_command", "task_attempt",
	// 0006_p2_routing.sql (FR-3.5 루프 상한이 읽는 트리거 이력)
	"session_hop",
}

// prdEnums pins every state set to the exact PRD labels (task item 2).
var prdEnums = map[string][]string{
	"session_status":  {"draft", "active", "paused", "completing", "completed", "cancelled"},
	"task_status":     {"deferred", "queued", "dispatched", "preparing", "running", "waiting_human", "paused", "completed", "failed", "cancelled"},
	"lane_status":     {"queued", "running", "waiting_human", "blocked", "paused", "done", "failed"},
	"hitl_status":     {"open", "answered", "auto_answered", "cancelled"},
	"hitl_type":       {"question", "choice", "approval", "info"},
	"hitl_source":     {"agent", "system"},
	"respond_to":      {"owner", "allowlist", "workspace", "nobody"},
	"isolation_kind":  {"worktree", "container", "none"},
	"inbox_item_type": {"hitl_request", "lane_blocked", "session_completed", "session_paused", "run_failed", "runtime_offline", "mention"},
	"inbox_severity":  {"action_required", "attention", "info"},
	"pause_reason":    {"budget", "time", "loop", "runtime_offline", "director"},
	"agent_status":    {"idle", "working", "waiting_human", "error", "offline", "disabled"},
	"message_kind":    {"text", "hitl", "blocked_q", "system", "summary"},
	"message_state":   {"posted", "pending_approval"},
	"member_role":     {"owner", "admin", "member"},
	"autonomy_level":  {"supervised", "guided", "autonomous"},
}

// prdIndexes are the queue/feed indexes required by the P0-a task (item 3).
var prdIndexes = []string{
	"task_queue",                 // task(status, runtime_id, created_at) — SKIP LOCKED claim
	"task_heartbeat_running",     // task(heartbeat_at) WHERE status = 'running'
	"message_session_created",    // message(session_id, created_at)
	"task_event_task_id_seq_key", // UNIQUE (task_id, seq)
}

func testURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("COLAB_TEST_DB_URL")
	if url == "" {
		t.Skip("COLAB_TEST_DB_URL not set; skipping integration test")
	}
	return url
}

// TestMigrate runs only with COLAB_TEST_DB_URL set. It DROPS schema public on
// that database first — point it at a throwaway database (make db / CI service).
func TestMigrate(t *testing.T) {
	url := testURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Start from an empty schema so the test is meaningful on a reused database.
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	all, _ := Load()
	n, err := MigratePool(ctx, pool)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if n != len(all) {
		t.Fatalf("first migrate applied %d, want %d", n, len(all))
	}

	// Every PRD §7 table exists.
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		have[name] = true
	}
	rows.Close()
	var missing, extra []string
	for _, want := range prdTables {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	want := map[string]bool{}
	for _, w := range prdTables {
		want[w] = true
	}
	for name := range have {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("tables missing=%v extra=%v", missing, extra)
	}

	// Enum labels match the PRD exactly (order included).
	for typ, labels := range prdEnums {
		got, err := enumLabels(ctx, pool, typ)
		if err != nil {
			t.Fatalf("enum %s: %v", typ, err)
		}
		if strings.Join(got, ",") != strings.Join(labels, ",") {
			t.Errorf("enum %s = %v, want %v", typ, got, labels)
		}
	}

	// Required indexes exist.
	for _, idx := range prdIndexes {
		var one int
		if err := pool.QueryRow(ctx, `SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, idx).Scan(&one); err != nil {
			t.Errorf("index %s missing: %v", idx, err)
		}
	}

	// Idempotent: second run applies nothing and version is unchanged.
	n, err = MigratePool(ctx, pool)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if n != 0 {
		t.Fatalf("second migrate applied %d, want 0", n)
	}
	v, err := Version(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if v != all[len(all)-1].Version {
		t.Fatalf("version = %d, want %d", v, all[len(all)-1].Version)
	}

	// Migrate(url) — the entry point main uses — is also a no-op now.
	if n, err := Migrate(ctx, url); err != nil || n != 0 {
		t.Fatalf("Migrate(url) = %d, %v; want 0, nil", n, err)
	}
}

func enumLabels(ctx context.Context, pool *pgxpool.Pool, typ string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.enumlabel FROM pg_enum e JOIN pg_type t ON t.oid = e.enumtypid
		WHERE t.typname = $1 ORDER BY e.enumsortorder`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
