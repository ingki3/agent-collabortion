package httpapi

// T-RF1: the preview mirrors Post's lane resolution (FR-3.6 「미리보기 = 게시의
// 답」). Both read their candidates through router.laneCandidates with the
// same mission filter (T-RF1-P); these rows compare preview to post in rooms
// with one mission, several, and a closed one.
//
// 회귀 주입: resolveLaneFor 가 laneCandidates 에 nil 을 넘기게(미션 필터
// 끄기) → (multi-mission) 의 post 가 다른 미션 lane 을 재사용해 FAIL;
// previewLane 이 nil 을 넘기게(T-RF1-P 되돌리기) → (multi-mission)·
// (closed-mission) FAIL; previewLane 이 laneCandidates 대신 빈 목록을 쓰게 →
// (one-mission) FAIL.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
)

func previewLaneOf(t *testing.T, pv map[string]any, agent string) (laneID any, resolution int) {
	t.Helper()
	for _, raw := range pv["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent {
			l := tr["lane"].(map[string]any)
			return l["lane_id"], int(l["resolution"].(float64))
		}
	}
	t.Fatalf("preview has no trigger for %s: %v", agent, pv["triggers"])
	return nil, 0
}

func postLaneOf(t *testing.T, out map[string]any, agent string) string {
	t.Helper()
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent {
			return str(tr, "lane_id")
		}
	}
	t.Fatalf("post has no trigger for %s: %v", agent, out["triggers"])
	return ""
}

func TestPreviewLaneParity(t *testing.T) {
	// (one-mission) a room with one mission: the preview names the lane the
	// post then uses — rule 4 for the first mention, rule 3 for the next.
	t.Run("one-mission", func(t *testing.T) {
		f := newP2Fixture(t)
		body := map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사"}
		lane, rule := previewLaneOf(t, f.preview(t, body), f.r)
		if lane != nil || rule != 4 {
			t.Fatalf("first preview = lane %v rule %d, want new lane (rule 4)", lane, rule)
		}
		first := postLaneOf(t, f.post(t, body), f.r)
		body = map[string]any{"content": router.MentionLink("R", f.rUUID) + " 보완"}
		lane, rule = previewLaneOf(t, f.preview(t, body), f.r)
		if lane != first || rule != 3 {
			t.Fatalf("second preview = lane %v rule %d, want %s (rule 3)", lane, rule, first)
		}
		if got := postLaneOf(t, f.post(t, body), f.r); got != first {
			t.Fatalf("second post lane = %s, preview promised %s", got, first)
		}
	})

	// (multi-mission) R's only lane is bound to mission 2; a message filed
	// under mission 1 makes a NEW lane (T-R1b2 filter) — and the preview, which
	// reads the same candidates (T-RF1-P), promises a new lane too. Before
	// T-RF1-P the preview promised mission 2's lane by rule 3.
	t.Run("multi-mission", func(t *testing.T) {
		f := newRoomsFixture(t)
		rid := f.worksRoom(t)
		w1 := f.openWork(t, f.api, rid, map[string]any{"goal": "첫 미션"})
		w2 := f.openWork(t, f.api, rid, map[string]any{"goal": "둘째 미션", "assignee_agent_id": f.r})
		rLane := f.onlyLane(t, rid)
		if n := f.count(t, `SELECT count(*) FROM lane WHERE id = $1 AND work_id = $2`, rLane, str(w2, "id")); n != 1 {
			t.Fatal("setup: R's lane must be mission 2's")
		}
		body := map[string]any{"content": router.MentionLink("R", mustUUID(t, f.r)) + " 여기도", "work_id": str(w1, "id")}
		got := f.parity(t, rid, body)
		if got == rLane {
			t.Fatalf("post reused mission 2's lane %s for a mission-1 message — the T-R1b2 filter is gone", rLane)
		}
		// (multi-mission, same mission) a second mission-1 message: both now
		// name the mission-1 lane the first one made (rule 3).
		body = map[string]any{"content": router.MentionLink("R", mustUUID(t, f.r)) + " 보완", "work_id": str(w1, "id")}
		if again := f.parity(t, rid, body); again != got {
			t.Fatalf("second mission-1 post lane = %s, want %s", again, got)
		}
		// (multi-mission, other mission) a mission-2 message: mission 2's own
		// lane, in both.
		body = map[string]any{"content": router.MentionLink("R", mustUUID(t, f.r)) + " 둘째 쪽", "work_id": str(w2, "id")}
		if l := f.parity(t, rid, body); l != rLane {
			t.Fatalf("mission-2 post lane = %s, want mission 2's %s", l, rLane)
		}
	})

	// (closed-mission) R's lane belongs to a mission that is now completed; a
	// message filed under the room's other (open) mission must not be promised
	// — or given — the closed mission's lane.
	t.Run("closed-mission", func(t *testing.T) {
		f := newRoomsFixture(t)
		rid := f.worksRoom(t)
		w1 := f.openWork(t, f.api, rid, map[string]any{"goal": "첫 미션"})
		w2 := f.openWork(t, f.api, rid, map[string]any{"goal": "둘째 미션", "assignee_agent_id": f.r})
		rLane := f.onlyLane(t, rid)
		// The mission closes (its lane stays, bound to it).
		if _, err := f.pool.Exec(t.Context(), `UPDATE task SET status = 'cancelled', failure_kind = 'cancelled', finished_at = now() WHERE lane_id = $1 AND status NOT IN ('completed','failed','cancelled')`, rLane); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE lane SET status = 'done' WHERE id = $1`, rLane); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE work SET status = 'completed' WHERE id = $1`, str(w2, "id")); err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"content": router.MentionLink("R", mustUUID(t, f.r)) + " 이어서", "work_id": str(w1, "id")}
		if got := f.parity(t, rid, body); got == rLane {
			t.Fatalf("post reused the closed mission's lane %s", rLane)
		}
	})
}

// onlyLane is R's one lane in room rid.
func (f *roomsFixture) onlyLane(t *testing.T, rid string) string {
	t.Helper()
	var l string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM lane WHERE session_id = $1 AND agent_id = $2`, rid, f.r).Scan(&l); err != nil {
		t.Fatal(err)
	}
	return l
}

// parity previews body, then posts it, and fails unless the preview named the
// lane the post used: an existing lane by id, or — lane_id null — a lane the
// post had to create. Returns the post's lane.
func (f *roomsFixture) parity(t *testing.T, rid string, body map[string]any) string {
	t.Helper()
	pvLane, rule := previewLaneOf(t, f.api.must(200, "POST", f.p+"/rooms/"+rid+"/messages/preview", body), f.r)
	before := f.count(t, `SELECT count(*) FROM lane WHERE session_id = $1 AND agent_id = $2`, rid, f.r)
	f.fake.Advance(time.Minute)
	out := f.api.must(201, "POST", f.p+"/rooms/"+rid+"/messages", body, "Idempotency-Key", uuid.NewString())
	got := postLaneOf(t, out, f.r)
	after := f.count(t, `SELECT count(*) FROM lane WHERE session_id = $1 AND agent_id = $2`, rid, f.r)
	created := after > before
	switch {
	case pvLane == nil && !created:
		t.Fatalf("preview promised a new lane (rule %d), post reused %s", rule, got)
	case pvLane != nil && (created || pvLane != got):
		t.Fatalf("preview promised lane %v (rule %d), post used %s (created=%v)", pvLane, rule, got, created)
	}
	return got
}
