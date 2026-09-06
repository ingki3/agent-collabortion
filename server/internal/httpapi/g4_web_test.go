// G4 웹 여정 2판이 격리한 서버 결함 2건의 DB 통합 테스트.
//
//	1 lane.brief — delegateLane 이 받은 brief 가 lane 에 남고 읽기 모델에 실린다
//	2 lane.updated — lane.status 가 바뀌면 SSE 프레임이 나간다 (W5: claim → running)
package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// TestLaneBriefSurvivesDelegate — 결함 1. 계약 Lane.brief 는 "위임 요약"이고
// delegateLane 의 brief 는 자식 턴의 프롬프트가 되는 바로 그 문자열인데, lane
// 테이블에 칸이 없어 서버가 버렸다(모든 lane 이 brief:null). 0010 이 칸을 만들고
// delegate 가 적는다. 사람이 만든 lane 은 여전히 null 이 맞다.
func TestLaneBriefSurvivesDelegate(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	// Lead's own lane comes from a human mention: no delegation, no brief.
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))

	const brief = "A 항목을 조사해서 근거 3개와 함께 요약해 주세요"
	res, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: brief})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Lane.Brief.IsSpecified() || res.Lane.Brief.IsNull() {
		t.Fatalf("delegate response lane.brief = null, want %q", brief)
	}
	if got := res.Lane.Brief.MustGet(); got != brief {
		t.Fatalf("delegate response lane.brief = %q, want %q", got, brief)
	}

	// The read model S7 actually renders is listLanes.
	byID := map[string]map[string]any{}
	for _, raw := range f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil) {
		l := raw.(map[string]any)
		byID[str(l, "id")] = l
	}
	delegated := byID[res.Lane.Id.String()]
	if delegated == nil {
		t.Fatalf("listLanes did not return the delegated lane %s", res.Lane.Id)
	}
	if got, _ := delegated["brief"].(string); got != brief {
		t.Fatalf("listLanes brief = %v, want %q", delegated["brief"], brief)
	}
	// getLane is still 501 (not in the P1/P2 operation set), so listLanes and
	// the delegate response are the two places the value is observable today.
	// Both read lanes.Load, which is where the column is now mapped.

	// A lane a human made by mentioning an agent has no brief, and null is the
	// honest answer — not the mention message's text.
	human := 0
	for _, l := range byID {
		if l["delegated_from_task_id"] != nil {
			continue
		}
		human++
		if l["brief"] != nil {
			t.Fatalf("non-delegated lane %s has brief %v, want null", str(l, "id"), l["brief"])
		}
	}
	if human == 0 {
		t.Fatal("fixture produced no human-made lane; the null half of the case is untested")
	}
}

// TestClaimPublishesLaneRunning — 결함 2 (G4 2판 W5). The daemon's claim moves
// the lane to `running` (tasks.MarkDispatched), and before this fix nothing
// published it: S7 loaded the board once and then saw nothing until a lane
// ENDED, so three Researchers running in parallel for fourteen seconds were
// never on screen. The assertion is on the real SSE stream, not the
// stream_event table, because the board reads the stream.
func TestClaimPublishesLaneRunning(t *testing.T) {
	f := newG4Fixture(t)

	// A queued task to claim. The lane is `queued` until the claim lands.
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	laneID := str(post["triggers"].([]any)[0].(map[string]any), "lane_id")

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+f.sessionID)
	defer stop()

	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})

	got := waitForLane(t, frames, laneID)
	if got != "running" {
		t.Fatalf("lane.updated after claim carried status %q, want running", got)
	}
	// Exactly one frame: the claim moves the lane once, and a board that gets
	// the same card twice per transition is a publish that leaked into a loop.
	if extra := drainLane(frames, laneID, 300*time.Millisecond); extra != 0 {
		t.Fatalf("claim produced %d extra lane.updated frames for the same lane, want 1 in total", extra)
	}
}

// streamFrame is one decoded SSE `data:` line.
type streamFrame struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// openStream opens the SSE endpoint and returns its frames. It blocks until the
// server has sent the `: connected` comment, so an event published right after
// this returns cannot be missed.
func openStream(t *testing.T, c *client, path string) (<-chan streamFrame, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", c.srv.URL+path, nil)
	req.Header.Set("Cookie", c.cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		cancel()
		res.Body.Close()
		t.Fatalf("stream status = %d, want 200", res.StatusCode)
	}
	out := make(chan streamFrame, 64)
	connected := make(chan struct{})
	go func() {
		defer close(out)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		open := false
		for sc.Scan() {
			line := sc.Text()
			if !open && strings.HasPrefix(line, ": connected") {
				open = true
				close(connected)
				continue
			}
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var fr streamFrame
			if err := json.Unmarshal([]byte(data), &fr); err != nil {
				continue
			}
			select {
			case out <- fr:
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("SSE stream never opened")
	}
	return out, cancel
}

// waitForLane returns the status of the first `lane.updated` frame about
// laneID. Frames for other lanes and other types are skipped — the session's
// stream also carries task.updated and message.created.
func waitForLane(t *testing.T, frames <-chan streamFrame, laneID string) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case fr, ok := <-frames:
			if !ok {
				t.Fatal("stream closed before lane.updated arrived")
			}
			if fr.Type != "lane.updated" {
				continue
			}
			var lane struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(fr.Payload, &lane); err != nil {
				t.Fatalf("lane.updated payload: %v", err)
			}
			if lane.ID != laneID {
				continue
			}
			return lane.Status
		case <-deadline:
			t.Fatalf("no lane.updated for lane %s within 10s — the board would still show it queued", laneID)
			return ""
		}
	}
}

// drainLane counts further `lane.updated` frames about laneID within d.
func drainLane(frames <-chan streamFrame, laneID string, d time.Duration) int {
	n := 0
	deadline := time.After(d)
	for {
		select {
		case fr, ok := <-frames:
			if !ok {
				return n
			}
			if fr.Type != "lane.updated" {
				continue
			}
			var lane struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(fr.Payload, &lane); err == nil && lane.ID == laneID {
				n++
			}
		case <-deadline:
			return n
		}
	}
}
