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
	st, err := f.setStatus(ctx, leadTask, 1, "blocked", "어디까지 할까요?")
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
	del, err := f.srv.Router.Delegate(ctx, leadTask, testCard(f.rUUID, "A 조사"))
	if err != nil {
		t.Fatal(err)
	}
	st2, err := f.setStatus(ctx, mustUUID(t, del.Task.Id.String()), 1, "blocked", "범위는요?")
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
	del, err := f.srv.Router.Delegate(ctx, leadTask, testCard(f.rUUID, "A 조사"))
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
	// PRD FR-3.8 2 (v0.19.15): an agent's mention of another agent with no
	// card asks a question (task kind question) — no longer a request.
	if *req.Message.Speech != gen.MessageSpeechQuestion {
		t.Fatalf("agent→other agent speech = %s, want question", *req.Message.Speech)
	}

	// 질문: blocked card. 답: a thread reply to it.
	st, err := f.setStatus(ctx, rTask, 1, "blocked", "범위가 어디까지인가요?")
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
		req.Message.Id.String():       "question",
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
	if _, err := f.setStatus(ctx, leadTask, 1, "blocked", "어디까지 할까요?"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'running', blocked_message_id = NULL WHERE id = (SELECT lane_id FROM task WHERE id = $1)`, leadTask); err != nil {
		t.Fatal(err)
	}
	del, err := f.srv.Router.Delegate(ctx, leadTask, testCard(f.rUUID, "A 조사"))
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
	// (v0.19.8) 보고를 받은 뒤의 말 — 멘션 없음(한 단·두 단 윗선) · 사람만 멘션 · 에이전트
	//      멘션 · 윗선 요청자가 에이전트. 윗선 없음은 reportChain 의 멘션 없는 줄이 본다.
	upstream := upstreamRound(t, f, sessionID)
	if _, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{Content: "혼잣말 — 멘션 없음"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{
		Content: router.MentionLink("W", f.wUUID) + " 도와줘",
	}); err != nil {
		t.Fatal(err)
	}
	st, err := f.setStatus(ctx, rTask, 1, "blocked", "질문?")
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
	cardEra := questionEraRows(t, f, sessionID)
	if len(before) < 8 {
		t.Fatalf("only %d messages — fixture did not write the round", len(before))
	}
	seen := map[string]bool{}
	for _, r := range before {
		seen[r.speech] = true
	}
	// v0.19.15: an agent no longer writes `request` (a mention asks a
	// question; work goes on a card), so the fixture has none.
	for _, want := range []string{"question", "answer", "report", "delegate", "chat", "note", "instruct"} {
		if !seen[want] {
			t.Fatalf("fixture never produced %q — the parity test would not compare that row: %v", want, seen)
		}
	}
	// B6: the chain rows exist and Store decided them as the rule says —
	// otherwise the parity below would compare nothing new.
	for id, want := range map[uuid.UUID]string{reqByLead[0]: "question", reqByLead[1]: "chat", againByR: "answer", wReport: "answer"} {
		if got := before[id.String()].speech; got != want {
			t.Fatalf("chain message %s: Store = %q, want %q", id, got, want)
		}
	}
	for id, want := range upstream {
		if got := before[id.String()].speech; got != want {
			t.Fatalf("upstream message %s: Store = %q, want %q", id, got, want)
		}
	}
	if got := before[wReport.String()].resp; got != "" {
		t.Fatalf("W's answer responds_to = %q, want none (an answer carries no ↩)", got)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE message SET speech = NULL, addressees = '[]', responds_to_message_id = NULL, delegated_lane_id = NULL WHERE session_id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, backfillSQL(t)); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	after := speechSnapshot(t, ctx, f, sessionID)
	// v0.19.15 FR-3.8 2: question/answer rows are decided at write time from
	// the task kind (question task), which the 0037-era backfill does not
	// know — it is frozen SQL over old rows that have no question task. The
	// parity holds for every other row.
	for id, b := range before {
		if cardEra[id] {
			continue
		}
		a := after[id]
		if a.speech != b.speech || a.resp != b.resp || a.lane != b.lane || !sameJSON(t, a.addr, b.addr) {
			t.Errorf("message %s: backfill %+v, Store %+v", id, a, b)
		}
	}
	// B6: the rechain runs on the live DB whose rows already hold speech
	// (0035's answer, some of them the wrong 「보고」). Its input is the
	// premises only, so a second run — over filled rows, not NULLs — lands on
	// the same answer (멱등).
	// v0.19.8: the live rows 0037 left — the no-mention line to the upstream
	// requester stored as 「요청 → 보고한 쪽」 — must be rewritten too.
	wrongly := []uuid.UUID{reqByLead[0], reqByLead[1], wReport}
	for id := range upstream {
		wrongly = append(wrongly, id)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE message SET speech = CASE WHEN id = $2 THEN 'request' ELSE 'report' END, addressees = '[]', responds_to_message_id = NULL WHERE id = ANY($1)`, wrongly, wReport); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, backfillSQL(t)); err != nil {
		t.Fatalf("backfill (second run): %v", err)
	}
	again := speechSnapshot(t, ctx, f, sessionID)
	for id, b := range before {
		if cardEra[id] {
			continue
		}
		a := again[id]
		if a.speech != b.speech || a.resp != b.resp || a.lane != b.lane || !sameJSON(t, a.addr, b.addr) {
			t.Errorf("message %s: second backfill %+v, Store %+v", id, a, b)
		}
	}
}

// reportChain is T-AGENTFIX B6's round (실측 게임 제작 방 14:05·14:30): Lead's
// turn is woken by R's report; in it Lead gives R a new order with a mention
// (a request to R — B6 kept) and says one bare line (PRD v0.19.8: 윗선 요청자
// 에게 보고 — here the upstream turn was woken by a system line, so it is not
// found and the line is chat). R's turn woken by the request answers with a
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
	// 에이전트를 멘션한 말은 요청(B6 유지) — 받는 쪽은 멘션된 R.
	// v0.19.15 FR-3.8 2: a mention of an agent with no card is a question.
	if m := withMention.Message; *m.Speech != gen.MessageSpeechQuestion {
		t.Errorf("Lead's order with an agent mention in a turn woken by a report: speech = %s, want question (%q)", *m.Speech, m.Content)
	} else if to := *m.Addressees; len(to) != 1 || to[0].Name != "R" {
		t.Errorf("addressees = %+v, want [R]", to)
	} else if v, err := m.RespondsToMessageId.Get(); err == nil {
		t.Errorf("a question has no responds_to, got %v", v)
	}
	// 멘션 없는 말은 윗선 요청자에게 보고(v0.19.8) — 여기서는 R 의 보고가 답한 위임을 쓴
	// Lead 턴이 미션 시작 줄(시스템)로 깨어났으므로 윗선을 못 찾는다 → 대화, 방 전체.
	if m := bare.Message; *m.Speech != gen.MessageSpeechChat || len(*m.Addressees) != 0 {
		t.Errorf("bare line, no upstream (system): speech = %s to %+v, want chat to the room", *m.Speech, *m.Addressees)
	}
	rTask := turnWokenBy(t, f, f.rUUID, withMention.Message.Id)
	r := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1}
	again, err := f.srv.Router.Post(ctx, sessionID, r, gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " 2장 끝"})
	if err != nil {
		t.Fatal(err)
	}
	if *again.Message.Speech != gen.MessageSpeechAnswer {
		t.Errorf("R's reply in the question turn = %s, want answer", *again.Message.Speech)
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
	// v0.19.15: the chip in R's report asked W a question (FR-3.8 2), so
	// what W says back to R in that question turn is an answer, no ↩.
	if *res.Message.Speech != gen.MessageSpeechAnswer {
		t.Errorf("W woken by a mention chip in R's report answers R: speech = %s, want answer", *res.Message.Speech)
	}
	if to := *res.Message.Addressees; len(to) != 1 || to[0].Name != "R" {
		t.Errorf("addressees = %+v, want [R]", to)
	}
	if v, err := res.Message.RespondsToMessageId.Get(); err == nil {
		t.Errorf("responds_to = %v, want none", v)
	}
	return res.Message.Id
}

// upstreamRound is PRD v0.19.8 FR-3.1.3 「보고를 받은 뒤의 말 — 누구에게
// 하는가」 on the real write paths (Director 지적 2026-09-27: Lead, woken by
// Developer's report, told Simplist 「v9 올렸습니다」 with no mention and it
// read 「요청 → @Developer」).
//
//	Dir ─지시 h1→ Lead(turn1) ─위임 d1→ R ─보고 rep1→ Lead(turn2)
//	  turn2: 멘션 없음            → 보고 → Dir, responds_to h1   (한 단)
//	  turn2: @Dir 만              → 보고 → Dir, responds_to h1   (사람만 멘션)
//	  turn2: @W 검토 q1           → 요청 → W                      (B6 유지)
//	W(turn, q1) ─보고 rep2→ Lead(turn3)
//	  turn3: 멘션 없음            → 보고 → Dir, responds_to h1   (두 단: rep2→q1→turn2→rep1→d1→turn1→h1)
//	R(turn, d1) @W 표 qW → W ─보고 repW→ R(turnR2)
//	  turnR2: 멘션 없음           → 보고 → Lead, responds_to d1  (윗선 요청자가 에이전트)
//	  turnR2: @Dir 만             → 보고 → Dir, responds_to 없음 (윗선 지시는 Lead 의 것)
//
// Returns the ids the parity test must see decided that way.
func upstreamRound(t *testing.T, f *p2Fixture, sessionID uuid.UUID) map[uuid.UUID]string {
	t.Helper()
	ctx := t.Context()
	var dirID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM app_user WHERE display_name = 'Dir'`).Scan(&dirID); err != nil {
		t.Fatal(err)
	}
	h1 := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " v9 만들어 주세요"})
	h1ID := mustUUID(t, str(h1["message"].(map[string]any), "id"))
	turn1 := turnWokenBy(t, f, f.leadUUID, h1ID)
	d1, err := f.srv.Router.Delegate(ctx, turn1, testCard(f.rUUID, "v9 빌드"))
	if err != nil {
		t.Fatal(err)
	}
	rTask := mustUUID(t, d1.Task.Id.String())
	rAuthor := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1}
	rep1, err := f.srv.Router.Post(ctx, sessionID, rAuthor, gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " v9 빌드 끝"})
	if err != nil {
		t.Fatal(err)
	}
	turn2 := turnWokenBy(t, f, f.leadUUID, rep1.Message.Id)
	lead2 := router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &turn2, Attempt: 1}
	postAs := func(a router.Author, content string) gen.Message {
		t.Helper()
		res, err := f.srv.Router.Post(ctx, sessionID, a, gen.MessageCreate{Content: content})
		if err != nil {
			t.Fatal(err)
		}
		return res.Message
	}
	type want struct {
		speech gen.MessageSpeech
		to     uuid.UUID
		resp   *uuid.UUID
	}
	check := func(label string, m gen.Message, w want) {
		t.Helper()
		if *m.Speech != w.speech {
			t.Errorf("%s: speech = %s, want %s (%q)", label, *m.Speech, w.speech, m.Content)
		}
		if to := *m.Addressees; len(to) != 1 || func() bool { id, err := to[0].Id.Get(); return err != nil || id != w.to }() {
			t.Errorf("%s: addressees = %+v, want [%s]", label, to, w.to)
		}
		got, err := m.RespondsToMessageId.Get()
		switch {
		case w.resp == nil && err == nil:
			t.Errorf("%s: responds_to = %v, want none", label, got)
		case w.resp != nil && (err != nil || got != *w.resp):
			t.Errorf("%s: responds_to = %v, want %s", label, got, *w.resp)
		}
	}
	bare1 := postAs(lead2, "Dir 님, v9 올렸습니다")
	check("한 단 · 멘션 없음", bare1, want{gen.MessageSpeechReport, dirID, &h1ID})
	human := postAs(lead2, router.UserMentionLink("Dir", dirID)+" v9 확인 부탁드립니다")
	check("사람만 멘션", human, want{gen.MessageSpeechReport, dirID, &h1ID})
	q1 := postAs(lead2, router.MentionLink("W", f.wUUID)+" v9 검토해 주세요")
	check("에이전트 멘션(B6 → v0.19.15 질문)", q1, want{gen.MessageSpeechQuestion, f.wUUID, nil})

	wTask := turnWokenBy(t, f, f.wUUID, q1.Id)
	w := router.Author{Type: "agent", AgentID: &f.wUUID, TaskID: &wTask, Attempt: 1}
	rep2 := postAs(w, router.MentionLink("Lead", f.leadUUID)+" 검토 끝, 문제 없음")
	check("W 의 답(질문 턴)", rep2, want{gen.MessageSpeechAnswer, f.leadUUID, nil})
	turn3 := turnWokenBy(t, f, f.leadUUID, rep2.Id)
	lead3 := router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &turn3, Attempt: 1}
	bare2 := postAs(lead3, "검토까지 끝났습니다")
	check("두 단 · 멘션 없음", bare2, want{gen.MessageSpeechReport, dirID, &h1ID})

	qW := postAs(rAuthor, router.MentionLink("W", f.wUUID)+" 표 하나 부탁")
	wTask2 := turnWokenBy(t, f, f.wUUID, qW.Id)
	w2 := router.Author{Type: "agent", AgentID: &f.wUUID, TaskID: &wTask2, Attempt: 1}
	repW := postAs(w2, router.MentionLink("R", f.rUUID)+" 표 여기")
	turnR2 := turnWokenBy(t, f, f.rUUID, repW.Id)
	r2 := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &turnR2, Attempt: 1}
	bareR := postAs(r2, "표까지 넣어 v9 마무리")
	d1ID := d1.Message.Id
	check("윗선 요청자가 에이전트", bareR, want{gen.MessageSpeechReport, f.leadUUID, &d1ID})
	// 사람만 멘션했는데 윗선 지시(d1)는 Lead 의 것 → Dir 에게 보고, responds_to 없음.
	humanOther := postAs(r2, router.UserMentionLink("Dir", dirID)+" 표 넣은 버전 공유드립니다")
	check("사람만 멘션 · 윗선은 다른 쪽", humanOther, want{gen.MessageSpeechReport, dirID, nil})

	// (#370 리뷰 B1) `@all` — 보고 트리거로 깨어난 turn2 안에서 세 갈래를 다 지난다.
	allOnly := postAs(lead2, "[@all](mention://all/all) v9 나왔습니다, 각자 확인해 주세요")
	if *allOnly.Speech != gen.MessageSpeechChat {
		t.Errorf("`@all` 만: speech = %s, want chat (모두에게 한 말은 보고가 아니다)", *allOnly.Speech)
	}
	if to := *allOnly.Addressees; len(to) != 1 || to[0].Kind != gen.MessageAddresseesKindAll {
		t.Errorf("`@all` 만: addressees = %+v, want [all]", to)
	}
	if v, err := allOnly.RespondsToMessageId.Get(); err == nil {
		t.Errorf("`@all` 만: responds_to = %v, want none", v)
	}
	allHuman := postAs(lead2, "[@all](mention://all/all) "+router.UserMentionLink("Dir", dirID)+" v9 올렸습니다")
	check("`@all` + 사람 — `@all` 은 받는 쪽에서 뺀다", allHuman, want{gen.MessageSpeechReport, dirID, &h1ID})
	allAgent := postAs(lead2, "[@all](mention://all/all) "+router.MentionLink("W", f.wUUID)+" 한 번 봐 주세요")
	if *allAgent.Speech != gen.MessageSpeechQuestion {
		t.Errorf("`@all` + 에이전트: speech = %s, want question", *allAgent.Speech)
	}
	if to := *allAgent.Addressees; len(to) != 2 {
		t.Errorf("`@all` + 에이전트: addressees = %+v, want [all W]", to)
	}

	// (#370 리뷰 NN2) 윗선이 **시스템 메시지**에서 멈추는 두 단 모양 — 실사용 사본의
	// 70e7af27 이 이것이다(Lead 턴이 미션 시작 줄로 깨어났다). turn1 자리에 시스템 줄로
	// 깨운 턴을 두고 같은 사슬을 한 번 더 만든다.
	var sysMsg uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM message WHERE session_id = $1 AND kind = 'system' ORDER BY created_at LIMIT 1`, sessionID).Scan(&sysMsg); err != nil {
		t.Fatal(err)
	}
	sysTurn := turnWokenBy(t, f, f.leadUUID, sysMsg)
	dSys, err := f.srv.Router.Delegate(ctx, sysTurn, testCard(f.rUUID, "시스템이 깨운 턴의 위임"))
	if err != nil {
		t.Fatal(err)
	}
	rSysTask := mustUUID(t, dSys.Task.Id.String())
	rSys := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rSysTask, Attempt: 1}
	repSys := postAs(rSys, router.MentionLink("Lead", f.leadUUID)+" 그 건 끝냈습니다")
	leadSys := router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: func() *uuid.UUID { id := turnWokenBy(t, f, f.leadUUID, repSys.Id); return &id }(), Attempt: 1}
	// 한 단 위(dSys 를 쓴 턴)의 트리거가 시스템이라 윗선이 없다 → 대화.
	sysStop := postAs(leadSys, "정리해서 올립니다")
	if *sysStop.Speech != gen.MessageSpeechChat || len(*sysStop.Addressees) != 0 {
		t.Errorf("윗선이 시스템에서 멈춤: speech = %s to %+v, want chat to the room", *sysStop.Speech, *sysStop.Addressees)
	}

	// (#370 리뷰 NN2) 윗선이 **자기 자신**이면 멈춘다 — 사슬이 자기가 쓴 말로 돌아오는
	// 재위임 모양. q1(Lead 가 쓴 요청)으로 깨운 Lead 턴에서 위임한다.
	selfTurn := turnWokenBy(t, f, f.leadUUID, q1.Id)
	dSelf, err := f.srv.Router.Delegate(ctx, selfTurn, testCard(f.rUUID, "자기 말로 돌아오는 위임"))
	if err != nil {
		t.Fatal(err)
	}
	rSelfTask := mustUUID(t, dSelf.Task.Id.String())
	repSelf := postAs(router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rSelfTask, Attempt: 1},
		router.MentionLink("Lead", f.leadUUID)+" 그것도 끝냈습니다")
	leadSelf := router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: func() *uuid.UUID { id := turnWokenBy(t, f, f.leadUUID, repSelf.Id); return &id }(), Attempt: 1}
	selfStop := postAs(leadSelf, "이쪽도 마무리했습니다")
	if *selfStop.Speech != gen.MessageSpeechChat || len(*selfStop.Addressees) != 0 {
		t.Errorf("윗선이 자기 자신이면 멈춘다: speech = %s to %+v, want chat to the room", *selfStop.Speech, *selfStop.Addressees)
	}

	return map[uuid.UUID]string{bare1.Id: "report", human.Id: "report", q1.Id: "question", bare2.Id: "report",
		bareR.Id: "report", humanOther.Id: "report",
		allOnly.Id: "chat", allHuman.Id: "report", allAgent.Id: "question",
		sysStop.Id: "chat", selfStop.Id: "chat"}
}

// TestConvoSpeech_UpstreamStopsAtAHumanWrittenRespondsTo is review #370 NN3:
// walkUpstream only follows a `responds_to` written by an AGENT — the 윗선
// 지시 is 「그 보고가 답한 **위임·요청**을 쓴 task 의 트리거」, and a human
// never posts from a task. Store's own writes cannot produce a report whose
// responds_to is a human message (the addressee IS that message's author), so
// the row is tampered here the way an older migration could have left it.
func TestConvoSpeech_UpstreamStopsAtAHumanWrittenRespondsTo(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	h1 := mustUUID(t, str(post["message"].(map[string]any), "id"))
	turn1 := turnWokenBy(t, f, f.leadUUID, h1)
	del, err := f.srv.Router.Delegate(ctx, turn1, testCard(f.rUUID, "A"))
	if err != nil {
		t.Fatal(err)
	}
	rTask := mustUUID(t, del.Task.Id.String())
	rep, err := f.srv.Router.Post(ctx, sessionID, router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " 끝"})
	if err != nil {
		t.Fatal(err)
	}
	// 그 보고가 「사람이 쓴 메시지」에 답한 것으로 바꿔 둔다. 사람 메시지에 task 까지
	// 달아 두는 것은 실제로는 없는 모양이지만(사람은 턴에서 쓰지 않는다), 그렇게 남은
	// 행에서도 걸음이 사람 메시지를 타고 올라가면 안 된다 — 윗선 지시는 「그 보고가 답한
	// **에이전트의** 위임·요청을 쓴 task 의 트리거」다. 이 방어선을 잠그는 행이다.
	if _, err := f.pool.Exec(ctx, `UPDATE message SET responds_to_message_id = $2 WHERE id = $1`, rep.Message.Id, h1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE message SET source_task_id = $2 WHERE id = $1`, h1, turn1); err != nil {
		t.Fatal(err)
	}
	leadTurn := turnWokenBy(t, f, f.leadUUID, rep.Message.Id)
	bare, err := f.srv.Router.Post(ctx, sessionID, router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadTurn, Attempt: 1},
		gen.MessageCreate{Content: "정리해서 올립니다"})
	if err != nil {
		t.Fatal(err)
	}
	if *bare.Message.Speech != gen.MessageSpeechChat || len(*bare.Message.Addressees) != 0 {
		t.Fatalf("사람이 쓴 responds_to 는 따라가지 않는다: speech = %s to %+v, want chat to the room",
			*bare.Message.Speech, *bare.Message.Addressees)
	}
	// 파리티는 여기서 비교하지 않는다. Go 는 트리거의 **저장된** `responds_to` 를 읽고,
	// 0039 는 같은 자리를 자기가 다시 계산한 값으로 읽는다(마이그레이션의 일이 재계산이다).
	// 손댄 행에서는 두 입력이 서로 다르므로 — 0039 는 진짜 윗선(Dir 의 지시)을 찾는다 —
	// 같은 답을 요구하는 것이 규칙이 아니라 픽스처를 비교하는 꼴이 된다. SQL 쪽의 같은
	// 조건은 S8 주입으로도 초록인데, 그 갈래가 재계산 입력에서는 닿을 수 없기 때문이다:
	// 트리거 보고의 `resp` 는 늘 그 보고의 트리거이고, 그 트리거 작성자가 사람이면 그
	// 보고의 받는 쪽도 그 사람이라 다음 턴이 이 분기(작성자 ∈ 받는 쪽, kind=agent)에
	// 들어오지 않는다. 조건은 Go 와 같은 모양을 지키는 방어선으로 둔다.
}

// turnWokenBy is a new turn of agent's latest lane whose trigger is msg — the
// task row messages.Store reads the requester from.
func turnWokenBy(t *testing.T, f *p2Fixture, agent, msg uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, trigger_message_id, originator_user_id, created_at, updated_at, kind)
		SELECT lane_id, session_id, agent_id, profile_id, 'completed', $2, originator_user_id, $3, $3,
		       -- v0.19.15: the turn a question woke is a question task (the
		       -- router makes it so when the mention is routed; here the
		       -- turn is stood in by hand, so read the message's speech).
		       CASE WHEN (SELECT speech FROM message WHERE id = $2) = 'question'
		                 AND (SELECT author_type FROM message WHERE id = $2) = 'agent' THEN 'question' ELSE 'normal' END
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
// the exact SQL that production ran. Since T-SPEECHFIX that is the upstream
// migration — a full recomputation of the same table plus PRD v0.19.8 「보고를
// 받은 뒤의 말」(멘션으로 가르고, 멘션이 없으면 윗선 요청자에게). Files are
// found by name suffix: their numbers are renamed at PR time.
func backfillSQL(t *testing.T) string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*_message_speech_upstream.sql")
	if err != nil || len(names) != 1 {
		t.Fatalf("message_speech_upstream migration: %v %v", names, err)
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

// questionEraRows is the set of messages PRD FR-3.8 2 decides from the
// question task: an agent's question, what a question turn wrote, and what
// a turn an answer woke wrote (its upstream walks through the answer).
func questionEraRows(t *testing.T, f *p2Fixture, sessionID uuid.UUID) map[string]bool {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `
		SELECT m.id::text FROM message m LEFT JOIN task k ON k.id = m.source_task_id
		LEFT JOIN message tm ON tm.id = k.trigger_message_id
		WHERE m.session_id = $1 AND (m.speech IN ('question', 'answer') AND m.author_type = 'agent' OR k.kind = 'question'
		      OR tm.speech = 'answer' AND tm.author_type = 'agent')`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	return out
}
