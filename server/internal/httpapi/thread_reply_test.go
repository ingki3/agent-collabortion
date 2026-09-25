package httpapi

// T-THREAD (daemon-protocol v0.9.2, harness v0.9.3): a question asked in a
// thread is answered in that thread. The server's half is the bundle —
// `task.thread_root_id` and `<message thread="…">` — read off a real claim,
// plus the routing an agent's thread reply now goes through.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// threadRoot posts a top-level Director message that triggers nobody (a
// /note), so the thread under it starts without a task in the queue.
func (f *p2Fixture) threadRoot(t *testing.T, content string) string {
	t.Helper()
	return msgID(f.post(t, map[string]any{"content": "/note " + content}))
}

func (f *p2Fixture) triggerTask(t *testing.T, out map[string]any, agent uuid.UUID) uuid.UUID {
	t.Helper()
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent.String() {
			return mustUUID(t, str(tr, "task_id"))
		}
	}
	t.Fatalf("no task for %s: %v", agent, out["triggers"])
	return uuid.Nil
}

// (a) A thread reply that triggers → the bundle names the thread root and the
// prompt's <message> carries it, with the closing thread line. A reply to a
// reply is normalised to the root.
func TestThreadReplyBundleNamesThreadRoot(t *testing.T) {
	f := newP2Fixture(t)
	root := f.threadRoot(t, "미션 요약")
	if root == "" {
		t.Fatal("no message_id for the root")
	}
	// The stored parent of a reply-to-a-reply is the root already (router);
	// the bundle must say the root either way.
	first := msgID(f.post(t, map[string]any{"content": "/note 첫 답글", "parent_id": root}))
	out := f.post(t, map[string]any{
		"content":   router.MentionLink("Lead", f.leadUUID) + " 종료 된거지?",
		"parent_id": first,
	})
	mid := msgID(out)
	taskID := f.triggerTask(t, out, f.leadUUID)

	b := f.claimBundle(t, taskID)
	if b.Task.ThreadRootID != root {
		t.Fatalf("task.thread_root_id = %q, want the thread root %s", b.Task.ThreadRootID, root)
	}
	wantAttr := `<message id="` + mid + `" author="Dir" at="`
	i := strings.Index(b.Prompt, wantAttr)
	if i < 0 {
		t.Fatalf("prompt has no trigger message %s:\n%s", mid, b.Prompt)
	}
	line := b.Prompt[i : i+strings.Index(b.Prompt[i:], "\n")]
	if !strings.HasSuffix(line, ` thread="`+root+`">`) {
		t.Fatalf("trigger <message> = %q, want thread=%q", line, root)
	}
	// claude_code (tool_surface mcp): the line in tool words (harness v0.9.6).
	if !strings.Contains(b.Prompt, queue.ThreadReplyInstructionMCP) || strings.Contains(b.Prompt, queue.ThreadReplyInstruction) {
		t.Fatalf("threaded turn prompt lacks the mcp thread-reply instruction:\n%s", b.Prompt)
	}
}

// (b) A top-level trigger → neither the bundle field nor the attribute nor the
// closing thread line.
func TestTopLevelTriggerHasNoThread(t *testing.T) {
	f := newP2Fixture(t)
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작해 주세요"})
	b := f.claimBundle(t, f.triggerTask(t, out, f.leadUUID))
	if b.Task.ThreadRootID != "" {
		t.Fatalf("task.thread_root_id = %q, want empty for a top-level trigger", b.Task.ThreadRootID)
	}
	if strings.Contains(b.Prompt, " thread=") {
		t.Fatalf("top-level trigger rendered a thread attribute:\n%s", b.Prompt)
	}
	if strings.Contains(b.Prompt, queue.ThreadReplyInstruction) || strings.Contains(b.Prompt, queue.ThreadReplyInstructionMCP) {
		t.Fatalf("top-level turn got the thread-reply instruction")
	}
}

// Coalesced triggers: the thread is the LATEST message's. A top-level message
// after a thread reply sends the answer to the main timeline, and the reverse
// sends it to the thread.
func TestCoalescedTriggerThreadIsLatest(t *testing.T) {
	for _, tc := range []struct {
		name         string
		threadedLast bool
	}{{"thread-then-top", false}, {"top-then-thread", true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newP2Fixture(t)
			// Coalescing is per lane, so the thread has to root in Lead's
			// lane (해소 규칙 1) — the same lane a top-level mention reuses
			// (규칙 3). Lead's summary is that root, as in the STO room.
			tok, first := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
			lead := &client{t: t, srv: f.api.srv, bearer: tok}
			f.fake.Advance(time.Minute)
			root := msgID(lead.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/messages",
				map[string]any{"content": "미션 요약입니다"}, "Idempotency-Key", uuid.NewString()))
			f.endTurn(t, first)
			mention := router.MentionLink("Lead", f.leadUUID)
			threaded := map[string]any{"content": mention + " 스레드 질문", "parent_id": root}
			top := map[string]any{"content": mention + " 메인 질문"}
			var firstOut map[string]any
			if tc.threadedLast {
				firstOut = f.post(t, top)
				f.post(t, threaded)
			} else {
				firstOut = f.post(t, threaded)
				f.post(t, top)
			}
			taskID := f.triggerTask(t, firstOut, f.leadUUID)
			var n int
			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM task WHERE agent_id = $1 AND status = 'queued'`, f.leadUUID).Scan(&n); err != nil || n != 1 {
				t.Fatalf("queued Lead tasks = %d (%v), want the two posts coalesced into 1", n, err)
			}
			b := f.claimBundle(t, taskID)
			want := ""
			if tc.threadedLast {
				want = root
			}
			if b.Task.ThreadRootID != want {
				t.Fatalf("thread_root_id = %q, want %q (the latest trigger's thread)", b.Task.ThreadRootID, want)
			}
			// Both messages are quoted; only the threaded one has the attribute.
			if c := strings.Count(b.Prompt, ` thread="`+root+`"`); c != 1 {
				t.Fatalf("thread attributes = %d, want 1:\n%s", c, b.Prompt)
			}
		})
	}
}

// Side effect check (T-THREAD 4): an agent's reply now lands in a thread. The
// routing of a thread reply must not change who it wakes — rule 4 (agent
// messages trigger only on a mention) and rule 8 (delegator suppression) read
// no thread position, and a mention in a thread wakes the same agent a
// top-level mention would.
func TestAgentThreadReplyRoutesLikeTopLevel(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	lead := &client{t: t, srv: f.api.srv, bearer: tok}
	root := f.threadRoot(t, "Director 의 질문")
	postAs := func(body map[string]any) map[string]any {
		f.fake.Advance(time.Minute)
		return lead.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/messages", body, "Idempotency-Key", uuid.NewString())
	}

	// No mention in a thread: rule 4 — nothing, not even the thread owner.
	if got := postedAgents(postAs(map[string]any{"content": "끝났습니다", "parent_id": root})); len(got) != 0 {
		t.Fatalf("agent thread reply without a mention triggered %v, want nothing (rule 4)", got)
	}
	// A mention in a thread wakes exactly the mentioned agent.
	out := postAs(map[string]any{"content": router.MentionLink("R", f.rUUID) + " 확인 부탁", "parent_id": root})
	if got := postedAgents(out); len(got) != 1 || !got[f.r] {
		t.Fatalf("agent thread mention triggered %v, want only R", got)
	}
	// The same message at the top level wakes the same set.
	f.pool.Exec(t.Context(), `UPDATE task SET status = 'completed' WHERE agent_id = $1`, f.rUUID)
	if got := postedAgents(postAs(map[string]any{"content": router.MentionLink("R", f.rUUID) + " 확인 부탁"})); len(got) != 1 || !got[f.r] {
		t.Fatalf("top-level mention triggered %v, want only R", got)
	}
	// And R's task, started from the thread, names the thread.
	f.pool.Exec(t.Context(), `UPDATE task SET status = 'completed' WHERE agent_id = $1`, f.rUUID)
	out = postAs(map[string]any{"content": router.MentionLink("R", f.rUUID) + " 하나 더", "parent_id": root})
	b := f.claimBundle(t, f.triggerTask(t, out, f.rUUID))
	if b.Task.ThreadRootID != root {
		t.Fatalf("R's bundle thread_root_id = %q, want %s", b.Task.ThreadRootID, root)
	}
}

// laneAgent is the agent a lane belongs to.
func (f *p2Fixture) laneAgent(t *testing.T, laneID string) uuid.UUID {
	t.Helper()
	var a uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT agent_id FROM lane WHERE id = $1`, laneID).Scan(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

func triggerOf(t *testing.T, out map[string]any, agent uuid.UUID) map[string]any {
	t.Helper()
	for _, raw := range out["triggers"].([]any) {
		if tr := raw.(map[string]any); str(tr, "agent_id") == agent.String() {
			return tr
		}
	}
	t.Fatalf("no trigger for %s: %v", agent, out["triggers"])
	return nil
}

// Lane rule 1 is about the root lane's OWN agent (Lead 판정, T-THREAD). A
// thread rooted in Lead's lane hands R's trigger to R's lane — resolved as
// the same mention at the top level would be (rule 3) — never to Lead's.
//
// 회귀 주입: router thread.laneFor 의 `&& th.RootLaneAgent == tr.AgentID` 를
// 지우면 (a)(b) FAIL. (c) 는 Lead lane 이 하나라 규칙 1·3 이 같은 lane 을
// 줘서 규칙 1 제거를 못 잡는다 — 경쟁 lane 이 있는 (c') 가 잡는다.
func TestThreadMentionOfAnotherAgentKeepsItsOwnLane(t *testing.T) {
	f := newP2Fixture(t)
	// R already has a lane from a top-level mention.
	rOut := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사해 주세요"})
	rLane := str(triggerOf(t, rOut, f.rUUID), "lane_id")
	f.endTurn(t, mustUUID(t, str(triggerOf(t, rOut, f.rUUID), "task_id")))

	// Lead's summary — the root, out of Lead's lane L.
	tok, leadTask := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	lead := &client{t: t, srv: f.api.srv, bearer: tok}
	postAs := func(body map[string]any) map[string]any {
		f.fake.Advance(time.Minute)
		return lead.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/messages", body, "Idempotency-Key", uuid.NewString())
	}
	root := msgID(postAs(map[string]any{"content": "미션 요약입니다"}))
	var leadLane string
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id::text FROM task WHERE id = $1`, leadTask).Scan(&leadLane); err != nil {
		t.Fatal(err)
	}

	// (b)'s premise: Lead has a QUEUED task on L (the Director asked again —
	// it joins the Lead task still in the queue; the token stays valid).
	queuedLead := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 하나 더", "parent_id": root})
	if got := str(triggerOf(t, queuedLead, f.leadUUID), "lane_id"); got != leadLane {
		t.Fatalf("(c) Director's thread reply to Lead's root went to lane %s, want Lead's root lane %s (rule 1)", got, leadLane)
	}
	leadQueued := str(triggerOf(t, queuedLead, f.leadUUID), "task_id")

	// Preview first — it must say what the post will do.
	prev := f.preview(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 해줘", "parent_id": root})
	previewed := false
	for _, raw := range prev["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") != f.r {
			continue
		}
		previewed = true
		lane := tr["lane"].(map[string]any)
		if int(lane["resolution"].(float64)) != 3 || str(lane, "lane_id") != rLane {
			t.Fatalf("preview of a thread mention of R = %v, want rule 3 on R's lane %s", lane, rLane)
		}
	}
	if !previewed {
		t.Fatalf("preview named no trigger for R: %v", prev["triggers"])
	}

	// (a) Lead's thread reply mentioning R — R's lane, not L.
	out := postAs(map[string]any{"content": router.MentionLink("R", f.rUUID) + " 해줘", "parent_id": root})
	tr := triggerOf(t, out, f.rUUID)
	if got := f.laneAgent(t, str(tr, "lane_id")); got != f.rUUID {
		t.Fatalf("(a) R's trigger landed on a lane of %s, want R's own (Lead=%s)", got, f.lead)
	}
	if str(tr, "lane_id") != rLane {
		t.Fatalf("(a) R's trigger lane = %s, want R's most recent lane %s (rule 3)", str(tr, "lane_id"), rLane)
	}
	// (b) and it is R's own task, not merged into Lead's queued one.
	if tr["coalesced"] == true || str(tr, "task_id") == leadQueued {
		t.Fatalf("(b) R's trigger merged into Lead's queued task %s: %v", leadQueued, tr)
	}
	var agent uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT agent_id FROM task WHERE id = $1`, str(tr, "task_id")).Scan(&agent); err != nil || agent != f.rUUID {
		t.Fatalf("(b) task %s belongs to %s (%v), want R", str(tr, "task_id"), agent, err)
	}

	// The Director's thread reply naming R goes the same way (the same defect
	// on the human path).
	f.pool.Exec(t.Context(), `UPDATE task SET status = 'completed' WHERE agent_id = $1`, f.rUUID)
	hum := triggerOf(t, f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 사람이 부탁", "parent_id": root}), f.rUUID)
	if f.laneAgent(t, str(hum, "lane_id")) != f.rUUID {
		t.Fatalf("Director's thread mention of R landed on another agent's lane")
	}
}

// (c') Rule 1 against a competing lane (#331 리뷰 NN1). With one Lead lane,
// rule 1 and rule 3 name the same lane and (c) above cannot tell them apart.
// Here the root lane L is failed and a top-level mention gave Lead a second
// lane L2 — the most recent one, which rule 3 would pick. A Director's @Lead
// reply in the root's thread still goes to L: the thread's own lane.
//
// 회귀 주입: router thread.laneFor 의 `case th.RootLaneAgent == tr.AgentID`
// 가 (uuid.Nil, …) 을 내면(규칙 1 제거) 규칙 3 이 L2 를 줘서 FAIL.
func TestThreadReplyToRootLaneAgentBeatsNewerLane(t *testing.T) {
	f := newP2Fixture(t)
	tok, leadTask := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	lead := &client{t: t, srv: f.api.srv, bearer: tok}
	f.fake.Advance(time.Minute)
	root := msgID(lead.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/messages",
		map[string]any{"content": "미션 요약입니다"}, "Idempotency-Key", uuid.NewString()))
	var rootLane string
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id::text FROM task WHERE id = $1`, leadTask).Scan(&rootLane); err != nil {
		t.Fatal(err)
	}
	f.endTurn(t, leadTask)
	if _, err := f.pool.Exec(t.Context(), `UPDATE lane SET status = 'failed' WHERE id = $1`, rootLane); err != nil {
		t.Fatal(err)
	}

	// A top-level mention: L is failed, so rule 4 opens L2.
	f.fake.Advance(time.Minute)
	top := triggerOf(t, f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 새 일"}), f.leadUUID)
	newLane := str(top, "lane_id")
	if newLane == rootLane || f.laneAgent(t, newLane) != f.leadUUID {
		t.Fatalf("premise: the top-level mention should open a second Lead lane, got %s (root lane %s)", newLane, rootLane)
	}

	body := map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 요약 질문", "parent_id": root}
	prev := f.preview(t, body)
	for _, raw := range prev["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") != f.lead {
			continue
		}
		if lane := tr["lane"].(map[string]any); int(lane["resolution"].(float64)) != 1 || str(lane, "lane_id") != rootLane {
			t.Fatalf("(c') preview of @Lead in the root thread = %v, want rule 1 on the root lane %s (not rule 3 → %s)", lane, rootLane, newLane)
		}
	}
	f.fake.Advance(time.Minute)
	tr := triggerOf(t, f.post(t, body), f.leadUUID)
	if got := str(tr, "lane_id"); got != rootLane {
		t.Fatalf("(c') @Lead in the root thread went to lane %s, want the root lane %s (rule 1; %s is rule 3's pick)", got, rootLane, newLane)
	}
}

// A reply to a reply (depth 2) as the trigger (#331 리뷰 NN2): the thread is
// the ROOT's, never the message replied to. Two layers hold that: the router
// stores a reply-to-a-reply against the root, and the bundle walks
// message.parent_id to the top — parent_id is a tree (#294 NN5), so a stored
// depth-2 row must still name the root.
//
// 회귀 주입: router threadPremise 가 루트 대신 답한 메시지를 parent 로 저장하면
// (1) FAIL, queue threadRootOf 가 올라가지 않고 parent_id 를 그대로 내면 (2) FAIL.
func TestDepthTwoReplyTriggerNamesRoot(t *testing.T) {
	f := newP2Fixture(t)
	root := f.threadRoot(t, "미션 요약")
	first := msgID(f.post(t, map[string]any{"content": "/note 첫 답글", "parent_id": root}))
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 답글의 답글", "parent_id": first})
	mid := msgID(out)
	taskID := f.triggerTask(t, out, f.leadUUID)

	// (1) As posted: stored against the root.
	var parent string
	if err := f.pool.QueryRow(t.Context(), `SELECT parent_id::text FROM message WHERE id = $1`, mid).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent != root {
		t.Fatalf("(1) a reply to reply %s was stored with parent %s, want the root %s", first, parent, root)
	}

	// (2) A depth-2 row — the tree the schema allows — still names the root.
	if _, err := f.pool.Exec(t.Context(), `UPDATE message SET parent_id = $2 WHERE id = $1`, mid, first); err != nil {
		t.Fatal(err)
	}
	b := f.claimBundle(t, taskID)
	if b.Task.ThreadRootID != root {
		t.Fatalf("(2) depth-2 trigger: task.thread_root_id = %q, want the root %s (not the parent %s)", b.Task.ThreadRootID, root, first)
	}
	i := strings.Index(b.Prompt, `<message id="`+mid+`"`)
	if i < 0 {
		t.Fatalf("prompt has no trigger message %s:\n%s", mid, b.Prompt)
	}
	if line := b.Prompt[i : i+strings.Index(b.Prompt[i:], "\n")]; !strings.HasSuffix(line, ` thread="`+root+`">`) {
		t.Fatalf("(2) depth-2 trigger <message> = %q, want thread=%q (not %q)", line, root, first)
	}
}
