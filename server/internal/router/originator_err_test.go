package router

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestTaskOriginatorErrors is #393 review NN4: a missing task is "not found"
// (no error); a failed read is an error, never folded into not-found.
//
// 회귀 주입: taskOriginator 가 읽기 오류를 (nil, false, nil) 로 삼키면 (broken) FAIL.
func TestTaskOriginatorErrors(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	task := testdb.AddTask(t, pool, seed, seed.SessionID, now)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, found, err := taskOriginator(ctx, tx, task); err != nil || !found {
		t.Fatalf("(found) found=%v err=%v, want true/nil", found, err)
	}
	if o, found, err := taskOriginator(ctx, tx, uuid.New()); err != nil || found || o != nil {
		t.Fatalf("(missing) o=%v found=%v err=%v, want nil/false/nil", o, found, err)
	}
	// (broken) an aborted transaction: the read fails.
	_, _ = tx.Exec(ctx, `SELECT 1/0`)
	if _, found, err := taskOriginator(ctx, tx, task); err == nil || found {
		t.Fatalf("(broken) found=%v err=%v, want an error", found, err)
	}
}
