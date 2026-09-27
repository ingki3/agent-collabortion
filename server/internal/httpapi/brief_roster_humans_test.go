package httpapi

// T-HUMANMENTION R1 (harness v0.9.10): brief [5] Roster carries the room's
// PEOPLE after the agents, with the `mention://user/<id>` link, so an agent can
// read from its own brief how to call the Director — it could not, which is the
// other half of the #362 defect. [2] carries the fixed line that says people
// use the same link form and that a person's mention wakes no agent.
//
// The E12-11 half is the reason these are one test: [1]~[5] must be
// byte-identical between two turns of the same room and mission, and the
// people lines must move ONLY when the room's people do.

import (
	"strings"
	"testing"
)

// rosterLines is brief [5]'s lines, header dropped.
func rosterLines(t *testing.T, brief string) []string {
	t.Helper()
	five := section(brief, 5)
	if five == "" {
		t.Fatalf("brief has no [5]:\n%s", brief)
	}
	var out []string
	for _, l := range strings.Split(five, "\n") {
		if strings.HasPrefix(l, "- ") {
			out = append(out, l)
		}
	}
	return out
}

// 회귀 주입: bundle.go 의 briefHumans 루프를 지우면 (people) FAIL; ORDER BY
// CASE 를 지우면 (order) FAIL; `rp.left_at IS NULL` 을 지우면 (left) FAIL;
// 사람 줄을 <roster_status> 에도 쓰면 (status) FAIL; 사람 줄에 미션 Director
// 표시를 넣으면 (E12-11 two missions) FAIL.
func TestBriefRosterCarriesTheRoomsPeople(t *testing.T) {
	f := newRoomsFixture(t)
	ctx := t.Context()
	wA := legacyWork(t, f.p2Fixture)
	// Mem is a deputy, Oth a plain member; Dir owns the room. The display
	// names are set so ROLE order and NAME order disagree — the deputy sorts
	// last by name — or an ordering by name alone would pass this test
	// (실측: 처음 쓴 Dir·Mem·Oth 는 두 순서가 같아 주입이 초록이었다).
	f.api.must(201, "POST", f.roomPath(f.sessionID)+"/participants", map[string]any{"user_id": f.memberUserID})
	f.api.must(201, "POST", f.roomPath(f.sessionID)+"/participants", map[string]any{"user_id": f.otherUserID})
	if _, err := f.pool.Exec(ctx, `UPDATE room_participant SET role = 'deputy' WHERE room_id = $1 AND user_id = $2`, f.sessionID, f.memberUserID); err != nil {
		t.Fatal(err)
	}
	// Dir(owner) → Zoe(deputy) → Ann(member): by name it would be Ann, Dir, Zoe.
	for id, name := range map[string]string{f.memberUserID: "Zoe", f.otherUserID: "Ann"} {
		if _, err := f.pool.Exec(ctx, `UPDATE app_user SET display_name = $2 WHERE id = $1`, id, name); err != nil {
			t.Fatal(err)
		}
	}

	b1 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	lines := rosterLines(t, b1.Brief.Text)

	// (people) three people, each as the contract's line: name, room role,
	// the user mention link — in role order, not name order.
	want := []string{
		"- Dir (person · room owner) — mention: [@Dir](mention://user/" + f.ownerUserID(t) + ")",
		"- Zoe (person · room deputy) — mention: [@Zoe](mention://user/" + f.memberUserID + ")",
		"- Ann (person · member) — mention: [@Ann](mention://user/" + f.otherUserID + ")",
	}
	people := lines[len(lines)-len(want):]
	for i, w := range want {
		if people[i] != w {
			t.Errorf("(people) roster people line %d =\n  %q\nwant\n  %q", i, people[i], w)
		}
	}
	// (order) and they come AFTER every agent line — the agents are the
	// roster's subject, the people the addition.
	for _, l := range lines[:len(lines)-len(want)] {
		if strings.Contains(l, "mention://user/") {
			t.Errorf("(order) a person's line sits among the agents: %q", l)
		}
	}
	for _, name := range []string{"Lead", "R", "W"} {
		if !strings.Contains(section(b1.Brief.Text, 5), "- "+name) {
			t.Errorf("(order) [5] lost agent %s:\n%s", name, section(b1.Brief.Text, 5))
		}
	}

	// (status) <roster_status> stays agents only: a person is neither working
	// nor idle, and a per-turn value must not name them.
	status := b1.Prompt[strings.Index(b1.Prompt, "<roster_status>"):]
	status = status[:strings.Index(status, "</roster_status>")]
	for _, who := range []string{"Dir", "Zoe", "Ann"} {
		if strings.Contains(status, who) {
			t.Errorf("(status) <roster_status> names the person %s:\n%s", who, status)
		}
	}

	// [2]'s fixed line is in the brief, per surface (claude_code → mcp here).
	two := section(b1.Brief.Text, 2)
	for _, w := range []string{"mention://user/", "[5]", "wakes no agent", "`colab_message_post`"} {
		if !strings.Contains(two, w) {
			t.Errorf("[2] lacks %q:\n%s", w, two)
		}
	}
	if strings.Contains(two, "`colab message post") {
		t.Errorf("[2] on the mcp surface names a shell command:\n%s", two)
	}

	// E12-11: another turn of the same room and mission is byte-identical,
	// and so is a turn of a DIFFERENT agent's [5] (the roster is the room's).
	b2 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	if p1, p2 := stablePrefix(b1.Brief.Text), stablePrefix(b2.Brief.Text); p1 != p2 {
		t.Errorf("[1]~[5] changed between two turns of one mission:\n--- 1\n%s\n--- 2\n%s", p1, p2)
	}
	bW := f.claimBundle(t, f.mentionTask(t, f.wUUID, "W", wA))
	if a, b := section(b1.Brief.Text, 5), section(bW.Brief.Text, 5); !samePeople(a, b) {
		t.Errorf("[5] people differ between two agents of one room:\n--- R\n%s\n--- W\n%s", a, b)
	}

	// (left) a person who leaves drops out — and that is the ONLY kind of
	// change: the roster moved because the room's people did.
	if _, err := f.pool.Exec(ctx, `UPDATE room_participant SET left_at = now() WHERE room_id = $1 AND user_id = $2`, f.sessionID, f.otherUserID); err != nil {
		t.Fatal(err)
	}
	b3 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	five3 := section(b3.Brief.Text, 5)
	if strings.Contains(five3, "Ann") {
		t.Errorf("(left) [5] still carries the person who left:\n%s", five3)
	}
	if !strings.Contains(five3, "- Zoe (person · room deputy)") {
		t.Errorf("(left) [5] lost Zoe:\n%s", five3)
	}
}

// samePeople compares two [5] sections by their people lines only (the agent
// lines carry "(you)" for the claiming agent, which is by design).
func samePeople(a, b string) bool {
	keep := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, "mention://user/") {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	return keep(a) == keep(b) && keep(a) != ""
}

// ownerUserID is the fixture account that owns the room (Dir).
func (f *roomsFixture) ownerUserID(t *testing.T) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT user_id::text FROM room_participant
		WHERE room_id = $1 AND role = 'owner' AND user_id IS NOT NULL`, f.sessionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
