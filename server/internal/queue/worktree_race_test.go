package queue

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestWorktreeFirstClaimRace (#302 리뷰 NN1): two computers, each with one
// repository, claim a worktree room that has no computer yet at the same
// moment. Exactly one of them takes it — one pin, one 「이 방은 …에서
// 워크트리로 돕니다」 notice, the isolation's repo_path is the winner's, and
// only the winner is handed the task.
//
// Two guards keep it so: settleWorktree's `FOR UPDATE OF s SKIP LOCKED` (the
// loser does not see the room) and FillWorktree's `runtime_id IS NULL` (the
// loser's write re-reads the committed pin and touches nothing). Either alone
// holds, so this test stays green with one removed — it is the lock on
// removing BOTH, where the second claim overwrote the first's pin and the
// timeline got two notices (review probe: 잠금 X · IS NULL X → FAIL).
//
// afterWorktreePremise holds each claim after its read until the other has
// read too (or 300ms passed — with the row lock in place the loser never
// reads the room), so the two transactions overlap every round.
func TestWorktreeFirstClaimRace(t *testing.T) {
	for round := 0; round < 3; round++ {
		t.Run(fmt.Sprintf("round%d", round), func(t *testing.T) { worktreeRaceRound(t) })
	}
}

func worktreeRaceRound(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	other := testdb.AddRuntime(t, q.DB, s.WorkspaceID, "mac-2", t0)
	repoOf := map[uuid.UUID]string{s.RuntimeID: "/Users/x/a", other: "/Users/y/b"}
	nameOf := map[uuid.UUID]string{s.RuntimeID: "mac-1", other: "mac-2"}
	for rt, repo := range repoOf {
		if _, err := q.DB.Exec(ctx, `UPDATE runtime SET repos = jsonb_build_array(jsonb_build_object('path', $2::text, 'remote_url', '', 'branch', 'main', 'clean', true)) WHERE id = $1`, rt, repo); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.DB.Exec(ctx, `UPDATE room SET isolation = '{"kind": "worktree"}'::jsonb, runtime_id = NULL WHERE id = $1`, s.SessionID); err != nil {
		t.Fatal(err)
	}
	task := testdb.AddTask(t, q.DB, s, s.SessionID, t0)

	var mu sync.Mutex
	arrived := 0
	both := make(chan struct{})
	afterWorktreePremise = func() {
		mu.Lock()
		arrived++
		if arrived == 2 {
			close(both)
		}
		mu.Unlock()
		select {
		case <-both:
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Cleanup(func() { afterWorktreePremise = nil })

	var wg sync.WaitGroup
	got := map[uuid.UUID][]string{}
	errs := make(chan error, 2)
	for _, rt := range []uuid.UUID{s.RuntimeID, other} {
		wg.Add(1)
		go func(rt uuid.UUID) {
			defer wg.Done()
			bundles, err := q.Claim(ctx, rt.String(), 4, c.Now())
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			for _, b := range bundles {
				got[rt] = append(got[rt], b.Task.ID)
			}
			mu.Unlock()
		}(rt)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	var pinned *uuid.UUID
	var repo string
	if err := q.DB.QueryRow(ctx, `SELECT runtime_id, COALESCE(isolation->>'repo_path', '') FROM room WHERE id = $1`, s.SessionID).Scan(&pinned, &repo); err != nil {
		t.Fatal(err)
	}
	if pinned == nil {
		t.Fatal("neither claim pinned the room")
	}
	if repo != repoOf[*pinned] {
		t.Fatalf("room pinned to %s but isolation.repo_path = %q, want %q — the loser overwrote half of the winner's settle", nameOf[*pinned], repo, repoOf[*pinned])
	}
	var notices int
	if err := q.DB.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '이 방은 %에서 워크트리로 돕니다%'`, s.SessionID).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 1 {
		t.Fatalf("worktree notices = %d, want 1 — both claims settled the room (lock and runtime_id IS NULL both gone?)", notices)
	}
	var fixed int
	if err := q.DB.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '이 방은 ' || $2 || '의 ' || $3 || ' %'`,
		s.SessionID, nameOf[*pinned], repoOf[*pinned]).Scan(&fixed); err != nil {
		t.Fatal(err)
	}
	if fixed != 1 {
		t.Fatalf("the one notice does not name the pinned computer %s on %s", nameOf[*pinned], repoOf[*pinned])
	}
	loser := other
	if *pinned == other {
		loser = s.RuntimeID
	}
	if len(got[loser]) != 0 {
		t.Fatalf("%s (not pinned) was handed %v", nameOf[loser], got[loser])
	}
	if len(got[*pinned]) != 1 || got[*pinned][0] != task.String() {
		t.Fatalf("%s (pinned) was handed %v, want the task %s", nameOf[*pinned], got[*pinned], task)
	}
}
