package httpapi

import (
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/migrations"
)

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
func TestConvoSpeech_BackfillMatchesClassify(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))
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
	f.post(t, map[string]any{"content": "/note 메모"})
	f.post(t, map[string]any{"content": "그냥 말"})

	type row struct{ speech, addr, resp, lane string }
	snap := func() map[string]row {
		rows, err := f.pool.Query(ctx, `
			SELECT id::text, COALESCE(speech, ''), addressees::text,
			       COALESCE(responds_to_message_id::text, ''), COALESCE(delegated_lane_id::text, '')
			FROM message WHERE session_id = $1`, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]row{}
		for rows.Next() {
			var id string
			var r row
			if err := rows.Scan(&id, &r.speech, &r.addr, &r.resp, &r.lane); err != nil {
				t.Fatal(err)
			}
			out[id] = r
		}
		return out
	}
	before := snap()
	if len(before) < 8 {
		t.Fatalf("only %d messages — fixture did not write the round", len(before))
	}

	if _, err := f.pool.Exec(ctx, `UPDATE message SET speech = NULL, addressees = '[]', responds_to_message_id = NULL, delegated_lane_id = NULL WHERE session_id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, backfillSQL(t)); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	after := snap()
	for id, b := range before {
		a := after[id]
		if a.speech != b.speech || a.resp != b.resp || a.lane != b.lane || !sameJSON(t, a.addr, b.addr) {
			t.Errorf("message %s: backfill %+v, Store %+v", id, a, b)
		}
	}
}

// backfillSQL is the migration's own backfill statement (from `WITH
// mention_to` to the end), read from the embedded file so the test runs the
// exact SQL that production ran. The file is found by name suffix: its number
// is renamed at PR time.
func backfillSQL(t *testing.T) string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*_message_speech.sql")
	if err != nil || len(names) != 1 {
		t.Fatalf("message_speech migration: %v %v", names, err)
	}
	b, err := migrations.FS.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(b), "WITH mention_to AS")
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
