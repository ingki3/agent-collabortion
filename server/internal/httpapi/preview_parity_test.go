package httpapi

// T-RF1: the preview mirrors Post's lane resolution (FR-3.6 「미리보기 = 게시의
// 답」). Both now read their candidates through router.laneCandidates; these
// rows pin where the two agree and the one place they do not.
//
// 회귀 주입: resolveLaneFor 가 laneCandidates 에 nil 을 넘기게(미션 필터
// 끄기) → (multi-mission) 의 post 가 다른 미션 lane 을 재사용해 FAIL;
// previewLane 이 laneCandidates 대신 빈 목록을 쓰게 → (one-mission) FAIL.

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
	// under mission 1 makes a NEW lane (T-R1b2 filter) — but the preview,
	// which reads every lane of the agent, promises the mission-2 lane.
	// TODO(T-RF1-P): pinned as it is (Lead's call A); aligning the two is
	// passing the mission to laneCandidates in previewLane.
	t.Run("multi-mission", func(t *testing.T) {
		f := newRoomsFixture(t)
		rid := f.worksRoom(t)
		w1 := f.openWork(t, f.api, rid, map[string]any{"goal": "첫 미션"})
		w2 := f.openWork(t, f.api, rid, map[string]any{"goal": "둘째 미션", "assignee_agent_id": f.r})
		var rLane string
		if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM lane WHERE session_id = $1 AND agent_id = $2`, rid, f.r).Scan(&rLane); err != nil {
			t.Fatal(err)
		}
		if n := f.count(t, `SELECT count(*) FROM lane WHERE id = $1 AND work_id = $2`, rLane, str(w2, "id")); n != 1 {
			t.Fatal("setup: R's lane must be mission 2's")
		}
		body := map[string]any{"content": router.MentionLink("R", mustUUID(t, f.r)) + " 여기도", "work_id": str(w1, "id")}
		lane, rule := previewLaneOf(t, f.api.must(200, "POST", f.p+"/rooms/"+rid+"/messages/preview", body), f.r)
		f.fake.Advance(time.Minute)
		out := f.api.must(201, "POST", f.p+"/rooms/"+rid+"/messages", body, "Idempotency-Key", uuid.NewString())
		got := postLaneOf(t, out, f.r)
		if got == rLane {
			t.Fatalf("post reused mission 2's lane %s for a mission-1 message — the T-R1b2 filter is gone", rLane)
		}
		if lane != rLane || rule != 3 {
			t.Fatalf("preview = lane %v rule %d; pinned current behaviour is mission 2's lane %s by rule 3 (TODO(T-RF1-P))", lane, rule, rLane)
		}
	})
}
