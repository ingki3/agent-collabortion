package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestPostSummaryOnceInsertsOnce is #162 review NN1: the golden table pins
// `SummaryMsgs == 1` through PlanSummary, which is a PURE function — it decides
// whether to ask the model for a summary. It cannot decide who wins when the
// completion path runs twice, and the reviewer's injection (deleting the
// WHERE NOT EXISTS from the INSERT) was caught by nothing.
//
// Two calls is the ordinary case, not a pathological one: the summariser runs
// behind the `completing` transition and a client retry after a timeout calls
// completion again. Two summaries in a timeline are indistinguishable and the
// reader cannot tell which is current.
func TestPostSummaryOnceInsertsOnce(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sessionID, workID := seedSummarySession(ctx, t, pool, now)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, inserted, err := postSummaryOnce(ctx, tx, sessionID, workID, "첫 요약", now); err != nil || !inserted {
		t.Fatalf("first summary: inserted=%v err=%v", inserted, err)
	}
	// The second caller read `alreadyPosted` before the first committed — that
	// is the whole race — so it arrives here with a body of its own.
	if id, inserted, err := postSummaryOnce(ctx, tx, sessionID, workID, "둘째 요약", now); err != nil {
		t.Fatalf("second summary: %v", err)
	} else if inserted {
		t.Errorf("the second call inserted %s — FR-2.4 is 세션당 요약 1개, and the guard that "+
			"enforces it is the INSERT's WHERE NOT EXISTS", id)
	}

	var n int
	var body string
	if err := tx.QueryRow(ctx, `
		SELECT count(*), max(content) FROM message WHERE session_id = $1 AND kind = 'summary'`,
		sessionID).Scan(&n, &body); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("summary messages = %d, want exactly 1 (FR-2.4)", n)
	}
	if body != "첫 요약" {
		t.Errorf("the surviving summary is %q — the FIRST one wins, so a retry cannot rewrite "+
			"what the Director already read", body)
	}
}

// TestPostSummaryOnceAcrossTransactions is the same rule when the two callers
// are two connections, which is what a retry actually looks like.
func TestPostSummaryOnceAcrossTransactions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sessionID, workID := seedSummarySession(ctx, t, pool, now)

	for i, body := range []string{"첫 요약", "둘째 요약"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, inserted, err := postSummaryOnce(ctx, tx, sessionID, workID, body, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if want := i == 0; inserted != want {
			t.Errorf("call %d inserted = %v, want %v", i+1, inserted, want)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'summary'`, sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("summary messages after two completions = %d, want 1 (FR-2.4)", n)
	}
}

func seedSummarySession(ctx context.Context, t *testing.T, pool *pgxpool.Pool, now time.Time) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var wsID, userID, sessionID, workID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ('w', 'w-`+uuid.NewString()[:8]+`') RETURNING id`).Scan(&wsID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name) VALUES ($1, 'u') RETURNING id`,
		uuid.NewString()+"@x.test").Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO room (workspace_id, name, owner_user_id, isolation, created_by, created_at, updated_at)
		VALUES ($1, 's', $2, '{"kind":"none"}', $2, $3, $3) RETURNING id`, wsID, userID, now).Scan(&sessionID); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO work (room_id, title, goal, director_user_id, status, created_by, created_at, updated_at)
		VALUES ($1, 's', 'g', $2, 'completing', $2, $3, $3) RETURNING id`, sessionID, userID, now).Scan(&workID); err != nil {
		t.Fatalf("work: %v", err)
	}
	return sessionID, workID
}

// TestPostSummaryOnceIsPerMission is PRD v0.19 FR-2A.4 on the same guard: the
// room lives on after a mission closes, so the room's SECOND mission gets its
// own summary. A guard keyed by the room (the pre-R1b2 WHERE session_id)
// would silently skip it — the second mission would close with no summary.
func TestPostSummaryOnceIsPerMission(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()
	roomID, first := seedSummarySession(ctx, t, pool, now)
	var second uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO work (room_id, title, goal, director_user_id, status, created_by, created_at, updated_at)
		SELECT room_id, 's2', 'g2', director_user_id, 'completing', created_by, $2, $2 FROM work WHERE id = $1 RETURNING id`,
		first, now).Scan(&second); err != nil {
		t.Fatalf("second work: %v", err)
	}
	for _, w := range []uuid.UUID{first, second, second} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := postSummaryOnce(ctx, tx, roomID, w, "요약", now); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var n1, n2 int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE work_id = $2), count(*) FILTER (WHERE work_id = $3)
		FROM message WHERE session_id = $1 AND kind = 'summary'`, roomID, first, second).Scan(&n1, &n2); err != nil {
		t.Fatal(err)
	}
	if n1 != 1 || n2 != 1 {
		t.Fatalf("summaries first=%d second=%d, want 1 and 1 (one per mission)", n1, n2)
	}
}
