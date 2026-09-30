package httpapi

// T-PARTS (PRD FR-3.1.4, openapi v0.3.6 postMessageGroup · D26, harness
// v0.9.11): an agent's one post that says different things to different
// recipients. One row per part, same group_id; every row goes through the
// postMessage path, so routing · lane · speech · mission are decided per part
// by the existing rules, and a part is addressed by its `to` alone.

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
)

func (f *p2Fixture) groupPost(t *testing.T, tok string, body map[string]any, key string) (int, map[string]any, http.Header) {
	t.Helper()
	f.fake.Advance(time.Minute)
	c := &client{t: t, srv: f.api.srv, bearer: tok}
	if key == "" {
		key = uuid.NewString()
	}
	return c.do("POST", f.p+"/rooms/"+f.sessionID+"/message-groups", body, "Idempotency-Key", key)
}

func part(to []string, content string) map[string]any {
	return map[string]any{"to": to, "content": content}
}

func partResults(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["parts"].([]any)
	if !ok {
		t.Fatalf("no parts in %v", out)
	}
	res := make([]map[string]any, len(raw))
	for i, r := range raw {
		res[i] = r.(map[string]any)
	}
	return res
}

func firstCode(out map[string]any) string {
	if errs, ok := out["errors"].([]any); ok && len(errs) > 0 {
		return str(errs[0].(map[string]any), "code")
	}
	return ""
}

func (f *p2Fixture) dirUserID(t *testing.T) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(t.Context(), `SELECT u.id::text FROM app_user u WHERE u.display_name = 'Dir'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Lead, woken by the Director, answers in three parts: a report to the
// Director, a request to R, a request to W. Each part is one row with the
// group columns; R and W each get one task whose trigger is THEIR part;
// the Director's part is a report (↩ the Director's instruction) and wakes
// nobody but files a mention inbox item; the body's mention of W inside R's
// part wakes nobody (only `to` routes).
//
// 회귀 주입: Decide 의 `in.Mentions` 우선을 지우면(본문 파싱) R 부분 본문의 W 멘션이
// W 를 두 번 깨우려다 (body mention) FAIL; postRow 의 group 칸 INSERT 를 빼면
// (group columns) FAIL; PostGroup 의 part 별 To 대신 전부 합친 멘션을 쓰면
// (per-part trigger) FAIL.
func TestPartsRoutePerPart(t *testing.T) {
	f := newP2Fixture(t)
	// R is woken by the Director's mention (the fixture's own first
	// task for Lead is the room's kickoff, whose trigger is a system line).
	tok, _ := f.agentToken(t, f.sessionID, f.rUUID, "R")
	dir := f.dirUserID(t)
	dirLink := router.UserMentionLink("Dir", mustUUID(t, dir))
	rLink, wLink := router.MentionLink("Lead", f.leadUUID), router.MentionLink("W", f.wUUID)
	tasksBefore := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1`, f.sessionID)

	st, out, _ := f.groupPost(t, tok, map[string]any{"parts": []any{
		part([]string{dirLink}, "v9 올렸습니다. 커브 원인은 횡가속도 상한이었습니다."),
		part([]string{rLink}, "커브 자료를 검토해 주세요 — "+wLink+" 가 쓸 겁니다"),
		part([]string{wLink}, "FX 코드를 합쳐 주세요"),
	}}, "")
	if st != 201 {
		t.Fatalf("postMessageGroup = %d %v", st, out)
	}
	gid := str(out, "group_id")
	parts := partResults(t, out)
	if len(parts) != 3 {
		t.Fatalf("parts = %d", len(parts))
	}
	// (group columns)
	for i, p := range parts {
		m := p["message"].(map[string]any)
		if str(m, "group_id") != gid || int(m["group_index"].(float64)) != i || int(m["group_size"].(float64)) != 3 {
			t.Fatalf("part %d group = %v/%v/%v, want %s/%d/3", i, m["group_id"], m["group_index"], m["group_size"], gid, i)
		}
	}
	// (per-part trigger)
	if n := len(parts[0]["triggers"].([]any)); n != 0 {
		t.Fatalf("the Director's part triggered %v", parts[0]["triggers"])
	}
	rT, wT := parts[1]["triggers"].([]any), parts[2]["triggers"].([]any)
	if len(rT) != 1 || str(rT[0].(map[string]any), "agent_id") != f.lead {
		t.Fatalf("Lead's part triggers = %v, want Lead only (body mention of W must not route)", rT)
	}
	if len(wT) != 1 || str(wT[0].(map[string]any), "agent_id") != f.w {
		t.Fatalf("W's part triggers = %v, want W", wT)
	}
	// Each task answers its own part and no other: Lead's may coalesce into
	// the kickoff task already queued on its lane (FR-3.4), W's is new.
	ids := []string{}
	for _, p := range parts {
		ids = append(ids, str(p["message"].(map[string]any), "id"))
	}
	for i, tr := range map[int][]any{1: rT, 2: wT} {
		var got []uuid.UUID
		if err := f.pool.QueryRow(t.Context(), `SELECT coalesced_message_ids FROM task WHERE id = $1`, str(tr[0].(map[string]any), "task_id")).Scan(&got); err != nil {
			t.Fatal(err)
		}
		has := map[string]bool{}
		for _, g := range got {
			has[g.String()] = true
		}
		for j, id := range ids {
			if (j == i) != has[id] {
				t.Fatalf("part %d's task answers %v; want its own part %s and no other part", i, got, ids[i])
			}
		}
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1`, f.sessionID); n > tasksBefore+2 {
		t.Fatalf("tasks %d → %d: more than one per agent part", tasksBefore, n)
	}
	// speech per part: report → Dir (↩), request → R, request → W.
	m0 := parts[0]["message"].(map[string]any)
	if str(m0, "speech") != "report" || str(m0, "responds_to_message_id") == "" {
		t.Fatalf("the Director's part = %s / responds %v, want report with ↩", str(m0, "speech"), m0["responds_to_message_id"])
	}
	a0 := m0["addressees"].([]any)
	if len(a0) != 1 || str(a0[0].(map[string]any), "id") != dir {
		t.Fatalf("the Director's part addressees = %v", a0)
	}
	for i, want := range map[int]string{1: f.lead, 2: f.w} {
		m := parts[i]["message"].(map[string]any)
		a := m["addressees"].([]any)
		// v0.19.15 FR-3.8 2: an agent's part to another agent is a question.
		if str(m, "speech") != "question" || len(a) != 1 || str(a[0].(map[string]any), "id") != want {
			t.Fatalf("part %d = %s → %v, want question → %s only", i, str(m, "speech"), a, want)
		}
	}
	// people: the Director's part files one mention inbox item for Dir (the
	// same path a body mention takes), quoting that part.
	if n := f.count(t, `SELECT count(*) FROM inbox_item WHERE type = 'mention' AND quote_message_id = $1`, str(m0, "id")); n != 1 {
		t.Fatalf("mention inbox items for the Director's part = %d, want 1", n)
	}
	// SSE: one message.created per part, in group_index order, persisted
	// back to back (nothing of this post between them).
	rows, err := f.pool.Query(t.Context(), `SELECT payload->>'group_index' FROM stream_event WHERE type = 'message.created' AND payload->>'group_id' = $1 ORDER BY id`, gid)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for rows.Next() {
		var gi string
		_ = rows.Scan(&gi)
		order = append(order, gi)
	}
	rows.Close()
	if strings.Join(order, ",") != "0,1,2" {
		t.Fatalf("message.created order = %v, want 0,1,2", order)
	}
	// listMessages ?group — the three rows in group_index order.
	lst := f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages?group="+gid, nil)
	its := items(lst)
	if len(its) != 3 {
		t.Fatalf("?group = %d rows", len(its))
	}
	for i, it := range its {
		if int(it.(map[string]any)["group_index"].(float64)) != i {
			t.Fatalf("?group order = %v", its)
		}
	}
	// getMessage carries the group too.
	g := f.api.must(200, "GET", f.p+"/messages/"+str(m0, "id"), nil)
	if str(g, "group_id") != gid {
		t.Fatalf("getMessage group_id = %v", g["group_id"])
	}
	// An ordinary message says null, not absent.
	plain := f.post(t, map[string]any{"content": "/note 보통 메시지"})
	pm := plain["message"].(map[string]any)
	if v, ok := pm["group_id"]; !ok || v != nil {
		t.Fatalf("ordinary message group_id = %v (present %v), want null", v, ok)
	}
}

// The four 422s, and that none of them leaves a row. A person's session is
// 403. 회귀 주입: ValidateParts 의 부분 수 검사를 끄면 (count) FAIL;
// ResolvePartTargets 의 owner 검사를 끄면 (duplicate) FAIL; isNote 검사를 끄면
// (note) FAIL; 참여자 검사를 끄면 (unknown) FAIL.
func TestPartsValidation(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	rLink, wLink := router.MentionLink("R", f.rUUID), router.MentionLink("W", f.wUUID)
	stranger := router.MentionLink("X", uuid.New())
	before := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID)
	six := []any{}
	for i := 0; i < 7; i++ {
		six = append(six, part([]string{"[@all](mention://all/all)"}, "x"))
	}
	cases := []struct {
		name, code string
		parts      []any
	}{
		{"count one", "parts_count", []any{part([]string{rLink}, "하나")}},
		{"count seven", "parts_count", six},
		{"duplicate", "parts_duplicate_agent", []any{part([]string{rLink}, "a"), part([]string{wLink, rLink}, "b")}},
		{"note", "parts_note", []any{part([]string{rLink}, "/note 메모"), part([]string{wLink}, "b")}},
		{"unknown", "unknown_mention", []any{part([]string{rLink}, "a"), part([]string{stranger}, "b")}},
		{"not a link", "unknown_mention", []any{part([]string{rLink}, "a"), part([]string{"@W"}, "b")}},
		// `to` is one mention link and nothing else — the link with a word
		// after it is a body, not a recipient (review #374a NN1).
		{"link plus words", "unknown_mention", []any{part([]string{rLink}, "a"), part([]string{wLink + " 에게"}, "b")}},
		{"empty to", "unknown_mention", []any{part([]string{rLink}, "a"), part([]string{}, "b")}},
	}
	for _, c := range cases {
		st, out, _ := f.groupPost(t, tok, map[string]any{"parts": c.parts}, "")
		if st != 422 || firstCode(out) != c.code {
			t.Errorf("%s: %d %s, want 422 %s (%v)", c.name, st, firstCode(out), c.code, out)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID); n != before {
		t.Fatalf("a refused group left %d rows", n-before)
	}
	// A person may not post parts.
	st, _, _ := f.api.do("POST", f.p+"/rooms/"+f.sessionID+"/message-groups",
		map[string]any{"parts": []any{part([]string{rLink}, "a"), part([]string{wLink}, "b")}}, "Idempotency-Key", uuid.NewString())
	if st != 403 {
		t.Fatalf("a person's postMessageGroup = %d, want 403", st)
	}
}

// Atomicity: a part that fails while being written — a trigger refuses the
// second row after the first row, its lane and task are in — rolls back every
// part. And the same key replays.
//
// 회귀 주입: PostGroup 을 부분마다 트랜잭션으로 나누면(부분 0 커밋) (atomic) FAIL;
// 핸들러에서 idempotentSeq 를 빼면 (replay) FAIL.
func TestPartsAtomicAndIdempotent(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	rLink, wLink := router.MentionLink("R", f.rUUID), router.MentionLink("W", f.wUUID)
	before := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID)
	tasksBefore := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1`, f.sessionID)

	// (atomic) part 1's insert fails inside the transaction after part 0's
	// row, lane and task were written: a trigger on message refuses it.
	if _, err := f.pool.Exec(t.Context(), `
		CREATE FUNCTION t_parts_boom() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.group_index = 1 THEN RAISE EXCEPTION 'boom'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER t_parts_boom BEFORE INSERT ON message FOR EACH ROW EXECUTE FUNCTION t_parts_boom();`); err != nil {
		t.Fatal(err)
	}
	st, out, _ := f.groupPost(t, tok, map[string]any{"parts": []any{part([]string{rLink}, "a"), part([]string{wLink}, "b")}}, "")
	if st < 500 {
		t.Fatalf("a failing part = %d %v, want 5xx", st, out)
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID); n != before {
		t.Fatalf("(atomic) %d rows survived a failed group", n-before)
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1`, f.sessionID); n != tasksBefore {
		t.Fatalf("(atomic) %d tasks survived a failed group", n-tasksBefore)
	}
	if _, err := f.pool.Exec(t.Context(), `DROP TRIGGER t_parts_boom ON message; DROP FUNCTION t_parts_boom();`); err != nil {
		t.Fatal(err)
	}

	// (commit) a commit that fails is the one way this path could answer 201
	// with no row (review #374a NN2): the error must reach the caller as 5xx,
	// and nothing may survive.
	restore := router.SetCommitGroupFailForTest(func() error { return errors.New("commit boom") })
	st, out, _ = f.groupPost(t, tok, map[string]any{"parts": []any{part([]string{rLink}, "c"), part([]string{wLink}, "d")}}, "")
	restore()
	if st < 500 {
		t.Fatalf("a failing commit = %d %v, want 5xx", st, out)
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID); n != before {
		t.Fatalf("(commit) %d rows survived a failed commit", n-before)
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1`, f.sessionID); n != tasksBefore {
		t.Fatalf("(commit) %d tasks survived a failed commit", n-tasksBefore)
	}

	// (replay) the same key twice → one group, same body, Replayed header.
	key := uuid.NewString()
	body := map[string]any{"parts": []any{part([]string{rLink}, "a"), part([]string{wLink}, "b")}}
	st1, out1, _ := f.groupPost(t, tok, body, key)
	st2, out2, hdr := f.groupPost(t, tok, body, key)
	if st1 != 201 || st2 != 201 || str(out1, "group_id") != str(out2, "group_id") || hdr.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("(replay) %d/%d %s/%s replayed=%q", st1, st2, str(out1, "group_id"), str(out2, "group_id"), hdr.Get("Idempotent-Replayed"))
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID); n != before+2 {
		t.Fatalf("(replay) rows = +%d, want +2", n-before)
	}
	// Same key, other body → 422 idempotency_key_reused.
	st3, out3, _ := f.groupPost(t, tok, map[string]any{"parts": []any{part([]string{rLink}, "c"), part([]string{wLink}, "d")}}, key)
	if st3 != 422 || str(out3, "code") != "idempotency_key_reused" {
		t.Fatalf("reused key = %d %v", st3, out3)
	}
}

// FR-3.5: a part whose trigger trips a loop limit is posted, its task is not
// made and the room pauses — the same as postMessage. 회귀 주입: postRow 의
// CheckLoopLimits 를 건너뛰면 FAIL.
func TestPartsLoopLimit(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO workspace_settings (workspace_id, loop_limits) VALUES ($1, '{"max_hops_per_hour": 1}')
		ON CONFLICT (workspace_id) DO UPDATE SET loop_limits = EXCLUDED.loop_limits`, f.wsID); err != nil {
		t.Fatal(err)
	}
	rLink, wLink := router.MentionLink("R", f.rUUID), router.MentionLink("W", f.wUUID)
	st, out, _ := f.groupPost(t, tok, map[string]any{"parts": []any{part([]string{rLink}, "a"), part([]string{wLink}, "b")}}, "")
	if st != 201 {
		t.Fatalf("= %d %v", st, out)
	}
	limited := 0
	for _, p := range partResults(t, out) {
		for _, w := range p["warnings"].([]any) {
			if str(w.(map[string]any), "code") == "loop_limit" {
				limited++
			}
		}
	}
	if limited == 0 {
		t.Fatalf("no part reported loop_limit at hops_per_hour=1: %v", out)
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE group_id = $1`, str(out, "group_id")); n != 2 {
		t.Fatalf("rows = %d, want both parts posted", n)
	}
}

// harness v0.9.11: the woken agent's <trigger> carries ITS part only, marked
// `group`, and one line naming the other parts' recipient and kind — never
// their body. Brief [2] has the fixed parts line, and [1]~[5] are the same
// bytes as a turn woken by an ordinary message (E12-11).
//
// 회귀 주입: bundle 의 group 속성을 빼면 (attr) FAIL; otherPartsLine 이 본문을
// 실으면 (no body) FAIL; 그 줄을 빼면 (line) FAIL; Section2 의 PartsRule 을 빼면
// (brief) FAIL.
func TestPartsTurnPrompt(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.wUUID, "W")
	dirLink := router.UserMentionLink("Dir", mustUUID(t, f.dirUserID(t)))
	rLink, wLink := router.MentionLink("R", f.rUUID), router.MentionLink("Lead", f.leadUUID)
	secretToDir := "디렉터에게만 하는 보고 문장"
	secretToW := "Lead 에게만 하는 FX 지시"
	st, out, _ := f.groupPost(t, tok, map[string]any{"parts": []any{
		part([]string{dirLink}, secretToDir),
		part([]string{rLink}, "R 에게 하는 자료 요청"),
		part([]string{wLink}, secretToW),
	}}, "")
	if st != 201 {
		t.Fatalf("= %d %v", st, out)
	}
	gid := str(out, "group_id")
	parts := partResults(t, out)
	rTask := mustUUID(t, str(parts[1]["triggers"].([]any)[0].(map[string]any), "task_id"))
	b := f.claimBundle(t, rTask)
	i := strings.Index(b.Prompt, "<trigger>\n")
	if i < 0 {
		t.Fatalf("no <trigger>:\n%s", b.Prompt)
	}
	trig := b.Prompt[i:strings.Index(b.Prompt, "</trigger>")]
	if !strings.Contains(trig, `group="`+gid+`"`) {
		t.Errorf("(attr) <message> has no group attribute:\n%s", trig)
	}
	if !strings.Contains(trig, "R 에게 하는 자료 요청") {
		t.Errorf("R's own part is missing:\n%s", trig)
	}
	if strings.Contains(trig, secretToDir) || strings.Contains(trig, secretToW) {
		t.Errorf("(no body) the trigger carries another part's body:\n%s", trig)
	}
	wantLine := "Other parts of the same message: → Dir (report) · → @Lead (question). Read them whole with the `colab_room_messages` tool's `group: \"" + gid + "\"`."
	if !strings.Contains(trig, wantLine) {
		t.Errorf("(line) want %q in:\n%s", wantLine, trig)
	}
	if !strings.Contains(b.Brief.Text, "send one message in parts: the `parts` argument of `colab_message_post` — one part per recipient.") {
		t.Errorf("(brief) [2] has no parts line")
	}

	// E12-11: an ordinary trigger for R in the same room → the same [1]~[5].
	// (claimBundle closed the posting task; a fresh token posts it.)
	tok2, _ := f.agentToken(t, f.sessionID, f.wUUID, "W")
	out2 := f.agentPost(t, tok2, map[string]any{"content": rLink + " 하나 더"})
	b2 := f.claimBundle(t, f.triggerTask(t, out2, f.rUUID))
	if p1, p2 := stablePrefix(b.Brief.Text), stablePrefix(b2.Brief.Text); p1 != p2 {
		t.Errorf("[1]~[5] changed between a part turn and a plain turn (E12-11):\n--- 1\n%s\n--- 2\n%s", p1, p2)
	}
}
