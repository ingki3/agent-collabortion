package httpapi

// T-HUMANMENTION (openapi v0.3.5 CliContext.humans[], colab-cli v0.9.4, PRD
// FR-3.2 · FR-3.3 rule 3): a Writer turn mentioned the Director and the CLI
// refused it — `/cli/context` carried only agents. The context now carries the
// room's people with the link the web composer inserts; a person's mention in
// an agent's post wakes no agent (no task, no lane), addresses that person
// (speech addressees kind user), and files a `mention` inbox item for them —
// only while they are in the room.

import (
	"strings"
	"testing"
)

// humansOf is CliContext.humans as name → mention_link.
func humansOf(t *testing.T, cc map[string]any) map[string]string {
	t.Helper()
	raw, ok := cc["humans"].([]any)
	if !ok {
		t.Fatalf("cli/context has no humans[]: %v", cc)
	}
	out := map[string]string{}
	for _, h := range raw {
		m := h.(map[string]any)
		out[str(m, "name")] = str(m, "mention_link")
		if str(m, "user_id") == "" {
			t.Fatalf("human without user_id: %v", m)
		}
	}
	return out
}

// 회귀 주입: GetCliContext 의 `out.Humans = &humans` 를 지우면 (humans) FAIL;
// HumanRoster 의 `sp.left_at IS NULL` 을 지우면 (left) FAIL.
func TestCliContextCarriesTheRoomsPeople(t *testing.T) {
	f := newRoomsFixture(t)
	f.api.must(201, "POST", f.roomPath(f.sessionID)+"/participants", map[string]any{"user_id": f.memberUserID})
	tok, _ := f.agentToken(t, f.sessionID, f.wUUID, "W")
	c := &client{t: t, srv: f.api.srv, bearer: tok}

	cc := c.must(200, "GET", f.p+"/cli/context", nil)
	hs := humansOf(t, cc)
	want := "[@Mem](mention://user/" + f.memberUserID + ")"
	if hs["Mem"] != want {
		t.Fatalf("humans = %v, want Mem → %s (the web composer's link: display name + user id)", hs, want)
	}
	if _, ok := hs["Dir"]; !ok {
		t.Fatalf("humans = %v, want the room's owner Dir too", hs)
	}
	if _, ok := hs["Oth"]; ok {
		t.Fatalf("humans = %v: Oth is a workspace member, not in this room", hs)
	}
	for _, p := range cc["participants"].([]any) {
		if strings.Contains(str(p.(map[string]any), "mention_link"), "mention://user/") {
			t.Fatalf("participants carries a person: %v", p)
		}
	}

	// A person who left is not mentionable.
	if _, err := f.pool.Exec(t.Context(), `UPDATE room_participant SET left_at = now() WHERE room_id = $1 AND user_id = $2`, f.sessionID, f.memberUserID); err != nil {
		t.Fatal(err)
	}
	if hs := humansOf(t, c.must(200, "GET", f.p+"/cli/context", nil)); hs["Mem"] != "" {
		t.Fatalf("humans after Mem left = %v", hs)
	}
}

// An agent's post that mentions only a person: no trigger, no new task or
// lane (FR-3.3 rule 3 — rule 4 would also hold it), addressed to that person,
// and one `mention` inbox item for them that quotes the message.
//
// 회귀 주입: PostWithTrigger 의 notifyMentionedPeople 호출을 지우면 (inbox)
// FAIL; Decide 의 규칙 3(`if otherMentioned`)을 끄면 사람 게시가 assignee 를
// 깨워 (person post) FAIL — 에이전트 게시는 규칙 4 가 따로 막는다. /note ·
// 자기 멘션 · 방 밖 · 나간 사람 가드를 하나씩 끄면 마지막 count FAIL.
func TestAgentMentionOfAPersonWakesNoOneAndNotifiesThem(t *testing.T) {
	f := newRoomsFixture(t)
	ctx := t.Context()
	f.api.must(201, "POST", f.roomPath(f.sessionID)+"/participants", map[string]any{"user_id": f.memberUserID})
	tok, _ := f.agentToken(t, f.sessionID, f.wUUID, "W")
	c := &client{t: t, srv: f.api.srv, bearer: tok}
	link := humansOf(t, c.must(200, "GET", f.p+"/cli/context", nil))["Mem"]

	tasksBefore := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1`, f.sessionID)
	lanesBefore := f.count(t, `SELECT count(*) FROM lane WHERE session_id = $1`, f.sessionID)
	out := f.agentPost(t, tok, map[string]any{"content": link + " 초안 검토 부탁드립니다"})
	if tr := out["triggers"].([]any); len(tr) != 0 {
		t.Fatalf("a person's mention triggered %v", tr)
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1`, f.sessionID); n != tasksBefore {
		t.Fatalf("tasks %d → %d: a person's mention made work", tasksBefore, n)
	}
	if n := f.count(t, `SELECT count(*) FROM lane WHERE session_id = $1`, f.sessionID); n != lanesBefore {
		t.Fatalf("lanes %d → %d: a person's mention made a lane", lanesBefore, n)
	}
	msg := out["message"].(map[string]any)
	to := msg["addressees"].([]any)
	found := false
	for _, a := range to {
		am := a.(map[string]any)
		if str(am, "kind") == "user" && str(am, "id") == f.memberUserID {
			found = true
		}
	}
	if !found {
		t.Fatalf("addressees = %v, want Mem (kind user)", to)
	}

	// (inbox) Mem is told, with the message quoted and the author named.
	inb := f.member.must(200, "GET", f.p+"/inbox?session_id="+f.sessionID, nil)
	var mention map[string]any
	for _, it := range items(inb) {
		m := it.(map[string]any)
		if str(m, "type") == "mention" {
			if mention != nil {
				t.Fatalf("two mention items for one message: %v", items(inb))
			}
			mention = m
		}
	}
	if mention == nil {
		t.Fatalf("Mem's inbox has no mention item: %v", items(inb))
	}
	if str(mention, "ref_id") != str(msg, "id") || str(mention, "severity") != "info" {
		t.Fatalf("mention item = %v", mention)
	}
	card := mention["card"].(map[string]any)
	if str(card, "body") != "@Mem 초안 검토 부탁드립니다" || str(card, "agent_name") != "W" {
		t.Fatalf("mention card = %v", card)
	}
	// Nobody else is told: the owner and the outsider get no mention item.
	for who, cl := range map[string]*client{"Dir": f.api, "Oth": f.other} {
		for _, it := range items(cl.must(200, "GET", f.p+"/inbox?session_id="+f.sessionID, nil)) {
			if str(it.(map[string]any), "type") == "mention" {
				t.Fatalf("%s got a mention item: %v", who, it)
			}
		}
	}

	// A person's post that mentions only a person also wakes nobody (rule 3,
	// not rule 6 → assignee), and tells the one mentioned.
	out = f.post(t, map[string]any{"content": link + " 확인해 주세요"})
	if tr := out["triggers"].([]any); len(tr) != 0 {
		t.Fatalf("a person's post mentioning a person triggered %v", tr)
	}
	if n := f.count(t, `SELECT count(*) FROM inbox_item i JOIN member m ON m.id = i.member_id WHERE m.user_id = $1 AND i.type = 'mention'`, f.memberUserID); n != 2 {
		t.Fatalf("Mem's mention items = %d, want 2", n)
	}

	// Not told: /note, a self-mention, a person outside the room, one who left.
	f.post(t, map[string]any{"content": "/note " + link + " 기록만"})
	f.post(t, map[string]any{"content": "[@Dir](mention://user/" + str(f.api.must(200, "GET", f.p+"/me", nil)["user"].(map[string]any), "id") + ") 나 자신"})
	f.agentPost(t, tok, map[string]any{"content": "[@Oth](mention://user/" + f.otherUserID + ") 밖의 사람"})
	if _, err := f.pool.Exec(ctx, `UPDATE room_participant SET left_at = now() WHERE room_id = $1 AND user_id = $2`, f.sessionID, f.memberUserID); err != nil {
		t.Fatal(err)
	}
	f.agentPost(t, tok, map[string]any{"content": link + " 나간 뒤"})
	if n := f.count(t, `SELECT count(*) FROM inbox_item WHERE type = 'mention'`); n != 2 {
		t.Fatalf("mention items = %d, want still 2 (note · self · outsider · left are not told)", n)
	}
}
