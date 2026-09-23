package httpapi

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestR2WorktreeFirstClaimRace (#302 리뷰 NN1 · #304 리뷰 NN1): two computers,
// each with one repository, claim a worktree room that has no computer yet at
// the same moment. Exactly one of them takes it — one pin, one 「이 방은 …에서
// 워크트리로 돕니다」 notice, the isolation's repo_path is the winner's, and
// only the winner is handed the task.
//
// Two guards keep it so: settleWorktree's `FOR UPDATE OF s SKIP LOCKED` (the
// loser does not see the room) and FillWorktree's `runtime_id IS NULL` (the
// loser's write re-reads the committed pin and touches nothing). Either alone
// holds, so this test stays green with one removed — it is the lock on
// removing BOTH, where the second claim overwrote the first's pin and the
// timeline got two notices.
//
// The shape is the #302 review probe: a real server's fixture, the second
// computer a column-for-column copy of the first (jsonb_populate_record, so
// it can run everything the first can), and the claims held right before the
// SettleFill write until the other has read too (or 300ms passed — with the
// row lock in place the loser never reads the room). #304's review found the
// queue-package version of this test green with both guards removed; the
// mutant table in the PR body is this test's.
func TestR2WorktreeFirstClaimRace(t *testing.T) {
	for round := 0; round < 4; round++ {
		t.Run(fmt.Sprintf("round%d", round), func(t *testing.T) { r2WorktreeRaceRound(t) })
	}
}

func r2WorktreeRaceRound(t *testing.T) {
	f := newP2Fixture(t)
	ctx := context.Background()
	room := mustUUID(t, f.sessionID)

	var first uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM runtime WHERE workspace_id = $1`, f.wsID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	var second uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO runtime
		SELECT (jsonb_populate_record(NULL::runtime, to_jsonb(r) ||
		        jsonb_build_object('id', gen_random_uuid(), 'name', 'mac-2', 'daemon_token_hash', NULL))).*
		  FROM runtime r WHERE r.id = $1
		RETURNING id`, first).Scan(&second); err != nil {
		t.Fatal(err)
	}
	repoOf := map[uuid.UUID]string{first: "/Users/x/a", second: "/Users/y/b"}
	nameOf := map[uuid.UUID]string{first: "mac-1", second: "mac-2"}
	for rt, repo := range repoOf {
		if _, err := f.pool.Exec(ctx, `UPDATE runtime SET repos = jsonb_build_array(jsonb_build_object('path', $2::text, 'remote_url', '', 'branch', 'main', 'clean', true)) WHERE id = $1`, rt, repo); err != nil {
			t.Fatal(err)
		}
	}

	// createSession with an assignee already queued the Lead's first turn.
	if _, err := f.pool.Exec(ctx, `UPDATE room SET isolation = '{"kind": "worktree"}'::jsonb, runtime_id = NULL WHERE id = $1`, room); err != nil {
		t.Fatal(err)
	}
	var task string
	var queued int
	if err := f.pool.QueryRow(ctx, `SELECT min(id::text), count(*) FROM task WHERE session_id = $1 AND status = 'queued'`, room).Scan(&task, &queued); err != nil || queued != 1 {
		t.Fatalf("premise: one queued task in the room, got %d (%v)", queued, err)
	}

	var mu sync.Mutex
	arrived := 0
	both := make(chan struct{})
	f.srv.Queue.AfterWorktreePremise = func() {
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
	t.Cleanup(func() { f.srv.Queue.AfterWorktreePremise = nil })

	var wg sync.WaitGroup
	got := map[uuid.UUID][]string{}
	errs := make(chan error, 2)
	for _, rt := range []uuid.UUID{first, second} {
		wg.Add(1)
		go func(rt uuid.UUID) {
			defer wg.Done()
			bundles, err := f.srv.Queue.Claim(ctx, rt.String(), 4, f.fake.Now())
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
	if err := f.pool.QueryRow(ctx, `SELECT runtime_id, COALESCE(isolation->>'repo_path', '') FROM room WHERE id = $1`, room).Scan(&pinned, &repo); err != nil {
		t.Fatal(err)
	}
	if pinned == nil {
		t.Fatal("neither claim pinned the room")
	}
	var notices int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '이 방은 %에서 워크트리로 돕니다%'`, room).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 1 {
		t.Fatalf("worktree notices = %d, want 1 — both claims settled the room (lock and runtime_id IS NULL both gone?)", notices)
	}
	if repo != repoOf[*pinned] {
		t.Fatalf("room pinned to %s but isolation.repo_path = %q, want %q — the loser overwrote half of the winner's settle", nameOf[*pinned], repo, repoOf[*pinned])
	}
	var fixed int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '이 방은 ' || $2 || '의 ' || $3 || ' %'`,
		room, nameOf[*pinned], repoOf[*pinned]).Scan(&fixed); err != nil {
		t.Fatal(err)
	}
	if fixed != 1 {
		t.Fatalf("the one notice does not name the pinned computer %s on %s", nameOf[*pinned], repoOf[*pinned])
	}
	loser := second
	if *pinned == second {
		loser = first
	}
	if len(got[loser]) != 0 {
		t.Fatalf("%s (not pinned) was handed %v", nameOf[loser], got[loser])
	}
	if len(got[*pinned]) != 1 || got[*pinned][0] != task {
		t.Fatalf("%s (pinned) was handed %v, want the task %s", nameOf[*pinned], got[*pinned], task)
	}
}
