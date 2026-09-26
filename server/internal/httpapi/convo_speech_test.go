package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/migrations"
)

// TestConvoSpeech_EveryWritePathStores walks the five message-writing paths the
// delegation round does NOT reach — the mission summary, a HITL card, the range
// summary (「여기까지 정리」), and the two isolation notices — and demands that
// every row they wrote carries a speech (review #335 NN1).
//
// 회귀 주입: 이 다섯 경로 중 어디서든 `messages.Store` 를 지우면 이 테스트가 FAIL.
// (#335 리뷰는 그 다섯 곳을 지워도 전 스위트가 초록이라는 것을 보였다.)
func TestConvoSpeech_EveryWritePathStores(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	wsID := mustUUID(t, f.wsID)
	now := f.fake.Now()

	// 1·2. 격리 공지 두 줄 — roomgate.AnnounceComputer · FillWorktree.
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := roomgate.AnnounceComputer(ctx, f.pool, f.srv.Hub, sessionID, runtimeID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE room SET isolation = jsonb_build_object('kind', 'worktree'), runtime_id = NULL WHERE id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	filled, err := roomgate.FillWorktree(ctx, tx, f.srv.Hub, sessionID, runtimeID, "/work/colab", now)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !filled {
		t.Fatal("FillWorktree wrote nothing — the fixture's isolation is not a fresh worktree room")
	}

	// 3. HITL 카드 — 사람이 답할 질문 카드(messages.PostHitlCard).
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	f.hitlOn(t, tok, map[string]any{"type": "question", "question": "이대로 제출할까요?", "proposed_default": "네"}, 201)

	// 4. 범위 요약 — 「여기까지 정리」(sessions.SummarizeRange).
	f.api.must(202, "POST", f.p+"/rooms/"+f.sessionID+"/summaries",
		map[string]any{"since": now.Add(-time.Hour).Format(time.RFC3339)})

	// 5. 미션 요약 — 완료 시 한 개(sessions.postSummaryOnce).
	if _, err := f.srv.Sessions.ApplyWorkEvent(ctx, mustUUID(t, f.missionID),
		sessions.Event{Kind: "director_end"}); err != nil {
		t.Fatal(err)
	}

	// 다섯 경로가 실제로 썼는지 먼저 확인한다 — 아무것도 안 썼으면 「0 null」은 공짜다.
	kinds := map[string]int{}
	rows, err := f.pool.Query(ctx, `SELECT kind::text, count(*) FROM message WHERE session_id = $1 GROUP BY 1`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		kinds[k] = n
	}
	rows.Close()
	if kinds["hitl"] < 1 {
		t.Fatalf("no HITL card was written: %v", kinds)
	}
	if kinds["summary"] < 2 {
		t.Fatalf("summary messages = %d, want the range summary AND the mission summary: %v", kinds["summary"], kinds)
	}
	var notices int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system'
		  AND (content LIKE '%워크트리%' OR content LIKE '%돕니다%' OR content LIKE '%컴퓨터%')`, sessionID).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices < 2 {
		t.Fatalf("isolation notices = %d, want both AnnounceComputer and FillWorktree", notices)
	}

	var nullSpeech int
	var sample string
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(min(left(content, 40)), '') FROM message WHERE session_id = $1 AND speech IS NULL`,
		sessionID).Scan(&nullSpeech, &sample); err != nil {
		t.Fatal(err)
	}
	if nullSpeech != 0 {
		t.Fatalf("%d rows were written without a speech (e.g. %q) — a write path skips messages.Store", nullSpeech, sample)
	}
}

// TestConvoSpeech_QuestionCardWaitingFor is PRD FR-3.1.3 표 3행 후반 (review
// #335 NN3): a question card with no mention is addressed to whoever that lane
// waits on. A lane a human made by mentioning an agent has no delegator, so the
// person who started the chain is the one waiting — without this the card's
// header read 「→ 방 전체」.
func TestConvoSpeech_QuestionCardWaitingFor(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	// 사람이 직접 불러 만든 lane — 위임자가 없다.
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))
	st, err := f.srv.Router.SetAgentStatus(ctx, leadTask, 1, "blocked", "어디까지 할까요?")
	if err != nil {
		t.Fatal(err)
	}
	card := f.api.must(200, "GET", f.p+"/messages/"+st.QuestionMessageID.String(), nil)
	if got := str(card, "speech"); got != "question" {
		t.Fatalf("speech = %q, want question", got)
	}
	to := card["addressees"].([]any)
	if len(to) != 1 {
		t.Fatalf("addressees = %v, want the one the lane waits on (Dir)", to)
	}
	a := to[0].(map[string]any)
	if str(a, "kind") != "user" || str(a, "name") != "Dir" {
		t.Errorf("addressee = %v, want the Director who started the chain", a)
	}

	// 위임으로 만든 lane 은 위임자를 멘션하므로 멘션 쪽으로 간다(표 3행 전반).
	del, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A 조사"})
	if err != nil {
		t.Fatal(err)
	}
	st2, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, del.Task.Id.String()), 1, "blocked", "범위는요?")
	if err != nil {
		t.Fatal(err)
	}
	card2 := f.api.must(200, "GET", f.p+"/messages/"+st2.QuestionMessageID.String(), nil)
	to2 := card2["addressees"].([]any)
	if len(to2) != 1 || str(to2[0].(map[string]any), "name") != "Lead" {
		t.Errorf("delegated lane's question addressees = %v, want [Lead] (the delegator it mentions)", to2)
	}
}

// TestConvoSpeech_ToAPIEmptyFallback covers the one branch the backfill leaves
// unreachable (review #335 NN7): a row written before the migration that the
// fill could not classify reads as `chat` with no addressees — the honest
// 「방 전체」, never a guessed label.
func TestConvoSpeech_ToAPIEmptyFallback(t *testing.T) {
	got := messages.ToAPI(&messages.Row{ID: uuid.New(), Speech: "", Addressees: []messages.Addressee{}})
	if got.Speech == nil || *got.Speech != gen.MessageSpeechChat {
		t.Fatalf("speech = %v, want chat for a row the backfill could not classify", got.Speech)
	}
	if got.Addressees == nil || len(*got.Addressees) != 0 {
		t.Fatalf("addressees = %v, want an empty list (「방 전체」)", got.Addressees)
	}
}

// TestConvoSpeech_ServerDecidesAtWrite walks every path that writes a message
// in a delegation round and reads the four D24 fields back through the list,
// getMessage and the row itself. The web and the CLI read only these fields
// (PRD FR-3.1.3), so if a path forgets messages.Store the timeline loses its
// header — this is the test that notices.
func TestConvoSpeech_ServerDecidesAtWrite(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	// 지시: the human mentions Lead.
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시장 조사 부탁"})
	instructID := str(post["message"].(map[string]any), "id")
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))

	// 위임: router.Delegate writes it and knows it.
	del, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A 조사"})
	if err != nil {
		t.Fatal(err)
	}
	if del.Message.Speech == nil || *del.Message.Speech != gen.MessageSpeechDelegate {
		t.Fatalf("delegate result speech = %v, want delegate", del.Message.Speech)
	}
	if v, _ := del.Message.DelegatedLaneId.Get(); v != del.Lane.Id {
		t.Fatalf("delegated_lane_id = %v, want the lane Delegate created %v", v, del.Lane.Id)
	}

	// 보고: R posts back to Lead from the delegated turn.
	rTask := mustUUID(t, del.Task.Id.String())
	rAuthor := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1}
	rep, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 조사 끝났습니다",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Message.Speech == nil || *rep.Message.Speech != gen.MessageSpeechReport {
		t.Fatalf("report speech = %v, want report", rep.Message.Speech)
	}
	if v, _ := rep.Message.RespondsToMessageId.Get(); v != del.Message.Id {
		t.Fatalf("responds_to = %v, want the delegation message %v", v, del.Message.Id)
	}

	// R3: 보고에 다른 에이전트를 같이 불러도 **받는 쪽은 요청자 한 명**(PRD 표 7행).
	// 그 다른 에이전트는 라우팅이 그대로 깨운다 — 화면에서만 보고받은 쪽이 아니다.
	both, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 끝났습니다 " + router.MentionLink("W", f.wUUID) + " 표 확인 부탁",
	})
	if err != nil {
		t.Fatal(err)
	}
	if *both.Message.Speech != gen.MessageSpeechReport {
		t.Fatalf("speech = %s, want report", *both.Message.Speech)
	}
	if to := *both.Message.Addressees; len(to) != 1 || to[0].Name != "Lead" {
		t.Errorf("report addressees = %+v, want only the requester [Lead] (PRD FR-3.1.3 표 7행)", to)
	}
	wokeW := false
	for _, tr := range both.Triggers {
		if tr.AgentId == f.wUUID {
			wokeW = true
		}
	}
	if !wokeW {
		t.Errorf("W was mentioned and must still be triggered (FR-3.3) — only the header changes: %+v", both.Triggers)
	}

	// 요청: R mentions W — not its requester, no lane → request.
	req, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: router.MentionLink("W", f.wUUID) + " 이것 좀 봐 주세요",
	})
	if err != nil {
		t.Fatal(err)
	}
	if *req.Message.Speech != gen.MessageSpeechRequest {
		t.Fatalf("agent→other agent speech = %s, want request", *req.Message.Speech)
	}

	// 질문: blocked card. 답: a thread reply to it.
	st, err := f.srv.Router.SetAgentStatus(ctx, rTask, 1, "blocked", "범위가 어디까지인가요?")
	if err != nil {
		t.Fatal(err)
	}
	ans := f.post(t, map[string]any{"content": "국내만요", "parent_id": st.QuestionMessageID.String()})
	ansID := str(ans["message"].(map[string]any), "id")

	// detail 속 멘션은 받는 쪽이 아니다(D23 과 같은 이유) — 본문엔 멘션이 없으니 방 전체 대화.
	withDetail, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: "표 정리했습니다", Detail: func() *string { s := "참고: " + router.MentionLink("W", f.wUUID) + " 의 초안 표"; return &s }(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if *withDetail.Message.Speech == gen.MessageSpeechRequest {
		t.Fatalf("a mention inside detail made this a request")
	}
	for _, a := range *withDetail.Message.Addressees {
		if a.Name == "W" {
			t.Fatalf("detail mention leaked into addressees: %+v", *withDetail.Message.Addressees)
		}
	}

	// 메모
	note := f.post(t, map[string]any{"content": "/note 기록만"})
	noteID := str(note["message"].(map[string]any), "id")

	// The same fields must come back on every read path.
	list := f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages?limit=100", nil)
	byID := map[string]map[string]any{}
	for _, raw := range list["items"].([]any) {
		m := raw.(map[string]any)
		byID[str(m, "id")] = m
		if _, ok := m["speech"]; !ok {
			t.Errorf("message %s has no speech in the list — every row must carry one", str(m, "id"))
		}
	}
	want := map[string]string{
		instructID:                    "instruct",
		del.Message.Id.String():       "delegate",
		rep.Message.Id.String():       "report",
		req.Message.Id.String():       "request",
		st.QuestionMessageID.String(): "question",
		ansID:                         "answer",
		noteID:                        "note",
	}
	for id, sp := range want {
		one := f.api.must(200, "GET", f.p+"/messages/"+id, nil)
		// The room list carries thread roots; a reply is read through getMessage.
		if m := byID[id]; m != nil {
			if got := str(m, "speech"); got != sp {
				t.Errorf("list %s speech = %q, want %q (content %q)", id, got, sp, str(m, "content"))
			}
		} else if id != ansID {
			t.Fatalf("message %s missing from list", id)
		} else {
			byID[id] = one
		}
		if got := str(one, "speech"); got != sp {
			t.Errorf("getMessage %s speech = %q, want %q", id, got, sp)
		}
	}
	// Addressees: 지시 → Lead; 답 → the asker (R); 메모 → nobody.
	addr := func(id string) []string {
		out := []string{}
		for _, raw := range byID[id]["addressees"].([]any) {
			out = append(out, str(raw.(map[string]any), "name"))
		}
		return out
	}
	if a := addr(instructID); len(a) != 1 || a[0] != "Lead" {
		t.Errorf("instruct addressees = %v, want [Lead]", a)
	}
	if a := addr(ansID); len(a) != 1 || a[0] != "R" {
		t.Errorf("answer addressees = %v, want [R] (the question card's author)", a)
	}
	if a := addr(noteID); len(a) != 0 {
		t.Errorf("note addressees = %v, want none", a)
	}
	// The start line is system.
	var nullSpeech int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND speech IS NULL`, sessionID).Scan(&nullSpeech); err != nil {
		t.Fatal(err)
	}
	if nullSpeech != 0 {
		t.Fatalf("%d messages were written without a speech — a write path skips messages.Store", nullSpeech)
	}
}

// TestConvoSpeech_BackfillMatchesClassify re-runs the migration's backfill
// over rows written by the live paths (after wiping their four fields) and
// checks it lands on the same answer Store did — the SQL and Go copies of the
// FR-3.1.3 table must not drift, or the pre-migration STO room reads
// differently from every new room.
//
// 픽스처는 리뷰 #335 가 가리킨 갈림길을 전부 지난다: 멘션 없는 스레드 답글(R1),
// 추가 멘션이 붙은 답(R2), 다른 에이전트를 같이 부른 보고(R3), `@all`(NN2),
// 멘션 없는 질문 카드(NN3).
func TestConvoSpeech_BackfillMatchesClassify(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))
	// (NN3) 위임 없이 사람이 만든 lane 의 질문 카드 — 멘션이 없다.
	if _, err := f.srv.Router.SetAgentStatus(ctx, leadTask, 1, "blocked", "어디까지 할까요?"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'running', blocked_message_id = NULL WHERE id = (SELECT lane_id FROM task WHERE id = $1)`, leadTask); err != nil {
		t.Fatal(err)
	}
	del, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A 조사"})
	if err != nil {
		t.Fatal(err)
	}
	rTask := mustUUID(t, del.Task.Id.String())
	rAuthor := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1}
	if _, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 완료",
	}); err != nil {
		t.Fatal(err)
	}
	// (R3) 보고 + 다른 에이전트 멘션.
	r3, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 끝 " + router.MentionLink("W", f.wUUID) + " 표 부탁",
	})
	if err != nil {
		t.Fatal(err)
	}
	// (B6) 보고로 깨운 Lead 턴의 말은 요청 — 멘션이 있든 없든. 그 요청으로 깨운 R 턴의
	//      답은 다시 보고(사슬: 보고 → 요청 → 보고).
	reqByLead, againByR := reportChain(t, f, sessionID, r3)
	// (B6 갈림길, review #343 블로커 1) 같은 보고의 본문 멘션 칩으로만 불린 W 는 그
	//      보고의 받는 쪽이 아니다 — 라우팅이 깨운 W 의 턴에서 R 에게 돌려주는 결과는
	//      원래 보고로 남는다(responds_to = 그 보고).
	wReport := chipWokenReport(t, f, sessionID, r3)
	if _, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{Content: "혼잣말 — 멘션 없음"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: router.MentionLink("W", f.wUUID) + " 도와줘",
	}); err != nil {
		t.Fatal(err)
	}
	st, err := f.srv.Router.SetAgentStatus(ctx, rTask, 1, "blocked", "질문?")
	if err != nil {
		t.Fatal(err)
	}
	f.post(t, map[string]any{"content": "답", "parent_id": st.QuestionMessageID.String()})
	// (b) 답에 다른 사람을 더 부른 경우 — 받는 쪽은 질문자 + 멘션 둘 다(표 5행).
	f.post(t, map[string]any{
		"content":   router.MentionLink("W", f.wUUID) + " 국내만요",
		"parent_id": st.QuestionMessageID.String(),
	})
	// (a) 멘션 없는 에이전트 스레드 답글 — 트리거가 있는 턴이라도 요청자에게 한 말이
	//     아니면 보고가 아니다(받는 쪽은 스레드 상대).
	rootID := str(post["message"].(map[string]any), "id")
	if _, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: "스레드에 멘션 없이 답니다", ParentId: nullableUUID(mustUUID(t, rootID)),
	}); err != nil {
		t.Fatal(err)
	}
	// (c) @all — jsonb 모양(Go 와 SQL)이 같아야 한다(NN2).
	f.post(t, map[string]any{"content": "[@all](mention://all/all) 오늘까지 봅시다"})
	f.post(t, map[string]any{"content": "/note 메모"})
	f.post(t, map[string]any{"content": "그냥 말"})

	before := speechSnapshot(t, ctx, f, sessionID)
	if len(before) < 8 {
		t.Fatalf("only %d messages — fixture did not write the round", len(before))
	}
	seen := map[string]bool{}
	for _, r := range before {
		seen[r.speech] = true
	}
	for _, want := range []string{"question", "answer", "report", "delegate", "chat", "note", "instruct", "request"} {
		if !seen[want] {
			t.Fatalf("fixture never produced %q — the parity test would not compare that row: %v", want, seen)
		}
	}
	// B6: the chain rows exist and Store decided them as the rule says —
	// otherwise the parity below would compare nothing new.
	for id, want := range map[uuid.UUID]string{reqByLead[0]: "request", reqByLead[1]: "request", againByR: "report", wReport: "report"} {
		if got := before[id.String()].speech; got != want {
			t.Fatalf("chain message %s: Store = %q, want %q", id, got, want)
		}
	}
	if got := before[wReport.String()].resp; got != r3.Message.Id.String() {
		t.Fatalf("W's report responds_to = %q, want R's report %s", got, r3.Message.Id)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE message SET speech = NULL, addressees = '[]', responds_to_message_id = NULL, delegated_lane_id = NULL WHERE session_id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, backfillSQL(t)); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	after := speechSnapshot(t, ctx, f, sessionID)
	for id, b := range before {
		a := after[id]
		if a.speech != b.speech || a.resp != b.resp || a.lane != b.lane || !sameJSON(t, a.addr, b.addr) {
			t.Errorf("message %s: backfill %+v, Store %+v", id, a, b)
		}
	}
	// B6: the rechain runs on the live DB whose rows already hold speech
	// (0035's answer, some of them the wrong 「보고」). Its input is the
	// premises only, so a second run — over filled rows, not NULLs — lands on
	// the same answer (멱등).
	if _, err := f.pool.Exec(ctx, `UPDATE message SET speech = CASE WHEN id = $2 THEN 'request' ELSE 'report' END, addressees = '[]', responds_to_message_id = NULL WHERE id = ANY($1)`, []uuid.UUID{reqByLead[0], reqByLead[1], wReport}, wReport); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, backfillSQL(t)); err != nil {
		t.Fatalf("backfill (second run): %v", err)
	}
	again := speechSnapshot(t, ctx, f, sessionID)
	for id, b := range before {
		a := again[id]
		if a.speech != b.speech || a.resp != b.resp || a.lane != b.lane || !sameJSON(t, a.addr, b.addr) {
			t.Errorf("message %s: second backfill %+v, Store %+v", id, a, b)
		}
	}
}

// reportChain is T-AGENTFIX B6's round (실측 게임 제작 방 14:05·14:30): Lead's
// turn is woken by R's report; in it Lead gives R a new order — once with a
// mention, once as a bare reply — and both are requests (보고에 대한 보고는
// 없다), addressed to R alone. R's turn woken by that request answers with a
// report again. Returns Lead's two message ids and R's second report id.
func reportChain(t *testing.T, f *p2Fixture, sessionID uuid.UUID, fromReport *gen.MessagePostResult) ([2]uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	// The delegator is suppressed while its lane's join group is open (FR-3.6),
	// so the report does not route to Lead here; in the room it woke Lead at
	// the join. What Store reads is the task's trigger_message_id — set that.
	leadTask := turnWokenBy(t, f, f.leadUUID, fromReport.Message.Id)
	lead := router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadTask, Attempt: 1}
	withMention, err := f.srv.Router.Post(ctx, sessionID, lead, gen.MessageCreate{
		Content: router.MentionLink("R", f.rUUID) + " 좋아요, 이제 2장도 써 주세요",
	})
	if err != nil {
		t.Fatal(err)
	}
	bare, err := f.srv.Router.Post(ctx, sessionID, lead, gen.MessageCreate{Content: "표는 두 줄로 줄여 주세요"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []gen.Message{withMention.Message, bare.Message} {
		if *m.Speech != gen.MessageSpeechRequest {
			t.Errorf("Lead's order in a turn woken by a report: speech = %s, want request (%q)", *m.Speech, m.Content)
		}
		if to := *m.Addressees; len(to) != 1 || to[0].Name != "R" {
			t.Errorf("addressees = %+v, want [R]", to)
		}
		if v, err := m.RespondsToMessageId.Get(); err == nil {
			t.Errorf("a request has no responds_to, got %v", v)
		}
	}
	rTask := turnWokenBy(t, f, f.rUUID, withMention.Message.Id)
	r := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1}
	again, err := f.srv.Router.Post(ctx, sessionID, r, gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " 2장 끝"})
	if err != nil {
		t.Fatal(err)
	}
	if *again.Message.Speech != gen.MessageSpeechReport {
		t.Errorf("R's answer to a request = %s, want report", *again.Message.Speech)
	}
	return [2]uuid.UUID{withMention.Message.Id, bare.Message.Id}, again.Message.Id
}

// chipWokenReport is review #343 블로커 1's counter-example on the real
// routing path: R's report (to Lead) also asks W for a table with a body
// mention chip. Routing (FR-3.3) wakes W with that report as the trigger, but
// W is not the report's addressee — W returning the table to R is W's own
// report, not a request. Returns W's message id.
func chipWokenReport(t *testing.T, f *p2Fixture, sessionID uuid.UUID, report *gen.MessagePostResult) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	if *report.Message.Speech != gen.MessageSpeechReport {
		t.Fatalf("fixture: R's message speech = %s, want report", *report.Message.Speech)
	}
	var wTask uuid.UUID
	for _, tr := range report.Triggers {
		if tr.AgentId == f.wUUID {
			wTask = tr.TaskId
		}
	}
	if wTask == uuid.Nil {
		t.Fatalf("routing did not wake W from R's report: %+v", report.Triggers)
	}
	var trig uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT trigger_message_id FROM task WHERE id = $1`, wTask).Scan(&trig); err != nil {
		t.Fatal(err)
	}
	if trig != report.Message.Id {
		t.Fatalf("W's task trigger = %s, want R's report %s", trig, report.Message.Id)
	}
	w := router.Author{Type: "agent", AgentID: &f.wUUID, TaskID: &wTask, Attempt: 1}
	res, err := f.srv.Router.Post(ctx, sessionID, w, gen.MessageCreate{Content: router.MentionLink("R", f.rUUID) + " 표 여기 있습니다"})
	if err != nil {
		t.Fatal(err)
	}
	if *res.Message.Speech != gen.MessageSpeechReport {
		t.Errorf("W woken by a mention chip in R's report answers R: speech = %s, want report", *res.Message.Speech)
	}
	if to := *res.Message.Addressees; len(to) != 1 || to[0].Name != "R" {
		t.Errorf("addressees = %+v, want [R]", to)
	}
	if v, err := res.Message.RespondsToMessageId.Get(); err != nil || v != report.Message.Id {
		t.Errorf("responds_to = %v (%v), want R's report %s", v, err, report.Message.Id)
	}
	return res.Message.Id
}

// turnWokenBy is a new turn of agent's latest lane whose trigger is msg — the
// task row messages.Store reads the requester from.
func turnWokenBy(t *testing.T, f *p2Fixture, agent, msg uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, trigger_message_id, originator_user_id, created_at, updated_at)
		SELECT lane_id, session_id, agent_id, profile_id, 'completed', $2, originator_user_id, $3, $3
		FROM task WHERE agent_id = $1 ORDER BY created_at DESC LIMIT 1
		RETURNING id`, agent, msg, f.fake.Now()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

type speechRow struct{ speech, addr, resp, lane string }

func speechSnapshot(t *testing.T, ctx context.Context, f *p2Fixture, sessionID uuid.UUID) map[string]speechRow {
	t.Helper()
	rows, err := f.pool.Query(ctx, `
		SELECT id::text, COALESCE(speech, ''), addressees::text,
		       COALESCE(responds_to_message_id::text, ''), COALESCE(delegated_lane_id::text, '')
		FROM message WHERE session_id = $1`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]speechRow{}
	for rows.Next() {
		var id string
		var r speechRow
		if err := rows.Scan(&id, &r.speech, &r.addr, &r.resp, &r.lane); err != nil {
			t.Fatal(err)
		}
		out[id] = r
	}
	return out
}

// backfillSQL is the backfill production last ran over existing rows (every
// statement from the `-- backfill:` marker to the end), read from the embedded file so the test runs
// the exact SQL that production ran. Since T-AGENTFIX B6 that is the rechain
// migration — a full recomputation of the same table plus 「보고에 대한 보고는
// 없다」. Files are found by name suffix: their numbers are renamed at PR time.
func backfillSQL(t *testing.T) string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*_message_speech_rechain.sql")
	if err != nil || len(names) != 1 {
		t.Fatalf("message_speech_rechain migration: %v %v", names, err)
	}
	b, err := migrations.FS.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(b), "-- backfill:")
	if i < 0 {
		t.Fatal("backfill statement not found in the migration")
	}
	return string(b[i:])
}

func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
