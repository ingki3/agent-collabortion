package httpapi

// T-RENAME: 방 이름·설명 바꾸기 (PRD v0.19.4 FR-2.1.2) — updateRoom 의
// name·description 칸. 앞뒤 공백을 떼고 잰다, 바뀐 경우에만 남긴다(타임라인
// 시스템 메시지 speech=system · activity_log · room.updated), 같은 이름의 방을
// 막지 않는다, 권한은 방 설정과 같다(방장·부방장·ws owner·admin).

import (
	"strings"
	"testing"
)

type renameProbe struct {
	f    *roomsFixture
	room string
	seen map[string]bool
}

// fresh returns the room's system messages written since the last call, as
// "speech|content". The fake clock does not move, so created_at ties — the
// probe remembers ids instead of relying on order.
func (p renameProbe) fresh(t *testing.T) []string {
	t.Helper()
	rows, err := p.f.pool.Query(t.Context(), `
		SELECT id::text, content, coalesce(speech, '') FROM message
		WHERE session_id = $1 AND author_type = 'system'`, p.room)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, c, sp string
		if err := rows.Scan(&id, &c, &sp); err != nil {
			t.Fatal(err)
		}
		if !p.seen[id] {
			p.seen[id] = true
			out = append(out, sp+"|"+c)
		}
	}
	return out
}

func (p renameProbe) activity(t *testing.T, action string) int {
	return p.f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1 AND action = $2`, p.room, action)
}

func (p renameProbe) roomUpdated(t *testing.T) []string {
	t.Helper()
	rows, err := p.f.pool.Query(t.Context(), `
		SELECT payload->>'name' FROM stream_event
		WHERE session_id = $1 AND type = 'room.updated' ORDER BY id`, p.room)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func fieldErrors(out map[string]any) []string {
	var got []string
	errs, _ := out["errors"].([]any)
	for _, e := range errs {
		m := e.(map[string]any)
		got = append(got, str(m, "field")+":"+str(m, "message"))
	}
	return got
}

func TestRoomRename(t *testing.T) {
	f := newRoomsFixture(t)
	room := f.mkRoom(t, f.member, "설계 방") // Mem 이 방장
	p := renameProbe{f: f, room: str(room, "id"), seen: map[string]bool{}}
	rp := f.roomPath(p.room)
	p.fresh(t)
	evBase := len(p.roomUpdated(t))

	t.Run("이름을 바꾸면 앞뒤 공백을 떼고 저장 · 시스템 메시지(speech=system) · activity_log · room.updated", func(t *testing.T) {
		out := f.member.must(200, "PATCH", rp, map[string]any{"name": "  결제·정산팀  "})
		if str(out, "name") != "결제·정산팀" {
			t.Fatalf("name = %q", str(out, "name"))
		}
		lines := p.fresh(t)
		want := "system|Mem 님이 방 이름을 설계 방에서 결제·정산팀으로 바꿨습니다."
		if len(lines) != 1 || lines[0] != want {
			t.Fatalf("system lines = %q, want [%q]", lines, want)
		}
		if n := p.activity(t, "room.renamed"); n != 1 {
			t.Fatalf("room.renamed activity = %d", n)
		}
		if n := p.activity(t, "room.settings_changed"); n != 0 {
			t.Fatalf("이름만 바꿨는데 「방 설정을 바꿨습니다」 activity = %d", n)
		}
		if ev := p.roomUpdated(t)[evBase:]; len(ev) != 1 || ev[0] != "결제·정산팀" {
			t.Fatalf("room.updated frames = %q", ev)
		}
		var from, to string
		if err := f.pool.QueryRow(t.Context(), `SELECT payload->>'from', payload->>'to' FROM activity_log WHERE session_id = $1 AND action = 'room.renamed'`, p.room).Scan(&from, &to); err != nil || from != "설계 방" || to != "결제·정산팀" {
			t.Fatalf("activity payload from/to = %q/%q (%v)", from, to, err)
		}
	})

	t.Run("같은 이름(공백만 다름)이면 아무것도 남지 않는다", func(t *testing.T) {
		evBefore := len(p.roomUpdated(t))
		f.member.must(200, "PATCH", rp, map[string]any{"name": " 결제·정산팀 ", "description": ""})
		if got := p.fresh(t); len(got) != 0 {
			t.Fatalf("unchanged name posted: %q", got)
		}
		if ev := p.roomUpdated(t); len(ev) != evBefore {
			t.Fatalf("unchanged name published room.updated: %q", ev[evBefore:])
		}
		if n := p.activity(t, "room.renamed"); n != 1 {
			t.Fatalf("room.renamed activity = %d after a no-op", n)
		}
	})

	t.Run("설명을 바꾸면 「방 설명을 바꿨습니다」 · 비워도 된다", func(t *testing.T) {
		out := f.member.must(200, "PATCH", rp, map[string]any{"description": "  결제 흐름 개편  "})
		if str(out, "description") != "결제 흐름 개편" {
			t.Fatalf("description = %q", str(out, "description"))
		}
		f.member.must(200, "PATCH", rp, map[string]any{"description": "   "})
		lines := p.fresh(t)
		want := "system|Mem 님이 방 설명을 바꿨습니다."
		if len(lines) != 2 || lines[0] != want || lines[1] != want {
			t.Fatalf("system lines = %q", lines)
		}
		if n := p.activity(t, "room.description_changed"); n != 2 {
			t.Fatalf("room.description_changed = %d", n)
		}
	})

	t.Run("검증 — 빈 이름·공백만 422, 뗀 뒤 200자는 통과·201자는 422, 설명 501자 422 (기존 errors[] 모양)", func(t *testing.T) {
		for _, bad := range []string{"", "   ", strings.Repeat("가", 201)} {
			st, out, _ := f.member.do("PATCH", rp, map[string]any{"name": bad})
			if st != 422 || str(out, "code") != "validation_failed" {
				t.Fatalf("name %q = %d %v", bad, st, out)
			}
			if got := fieldErrors(out); len(got) != 1 || got[0] != "name:방 이름은 1~200자로 입력해 주세요" {
				t.Fatalf("errors = %q", got)
			}
		}
		st, out, _ := f.member.do("PATCH", rp, map[string]any{"description": strings.Repeat("가", 501)})
		if got := fieldErrors(out); st != 422 || len(got) != 1 || got[0] != "description:설명은 500자까지 쓸 수 있습니다" {
			t.Fatalf("description 501 = %d %q", st, got)
		}
		if got := p.fresh(t); len(got) != 0 {
			t.Fatalf("a refused PATCH posted: %q", got)
		}
		long := strings.Repeat("나", 200)
		if out := f.member.must(200, "PATCH", rp, map[string]any{"name": "  " + long + "  "}); str(out, "name") != long {
			t.Fatalf("200 chars after trim should pass, got %d chars", len([]rune(str(out, "name"))))
		}
		if out := f.member.must(200, "PATCH", rp, map[string]any{"description": " " + strings.Repeat("다", 500) + " "}); len([]rune(str(out, "description"))) != 500 {
			t.Fatal("500-char description after trim should pass")
		}
		f.member.must(200, "PATCH", rp, map[string]any{"name": "결제·정산팀"})
		p.fresh(t)
	})

	t.Run("같은 이름의 방이 있어도 막지 않는다", func(t *testing.T) {
		other := f.mkRoom(t, f.member, "다른 방")
		out := f.member.must(200, "PATCH", f.roomPath(str(other, "id")), map[string]any{"name": "결제·정산팀"})
		if str(out, "name") != "결제·정산팀" {
			t.Fatalf("duplicate name refused: %v", out)
		}
	})

	t.Run("권한 — 참여하지 않은 멤버·일반 참여자는 403, ws admin 은 된다", func(t *testing.T) {
		if st, _, _ := f.other.do("PATCH", rp, map[string]any{"name": "몰래 바꿈"}); st != 403 {
			t.Fatalf("non-participant member PATCH = %d, want 403", st)
		}
		f.member.must(201, "POST", rp+"/participants", map[string]any{"user_id": f.otherUserID})
		if st, _, _ := f.other.do("PATCH", rp, map[string]any{"name": "몰래 바꿈"}); st != 403 {
			t.Fatalf("plain participant PATCH = %d, want 403", st)
		}
		if caps(f.other.must(200, "GET", rp, nil))["configure"] {
			t.Fatal("plain participant got configure capability")
		}
		if got := p.fresh(t); len(got) != 1 || !strings.Contains(got[0], "초대했습니다") {
			t.Fatalf("a refused rename posted: %q", got)
		}
		out := f.admin.must(200, "PATCH", rp, map[string]any{"name": "리서치"})
		if str(out, "name") != "리서치" {
			t.Fatalf("admin rename = %v", out)
		}
		if lines := p.fresh(t); len(lines) != 1 || lines[0] != "system|Adm 님이 방 이름을 결제·정산팀에서 리서치로 바꿨습니다." {
			t.Fatalf("admin rename lines = %q", lines)
		}
	})
}

// TestRoomRenameReachesBrief is FR-2.1.2 「에이전트」: brief [4] carries the new
// name from the next turn on. [1]~[5] byte identity (E12-11) breaks exactly
// once — at the rename — and holds again for the turns after it.
func TestRoomRenameReachesBrief(t *testing.T) {
	f := newP2Fixture(t)
	wA := legacyWork(t, f)
	b1 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	if s := section(b1.Brief.Text, 4); !strings.Contains(s, "\nRoom: ") || strings.Contains(s, "Room: 결제·정산팀\n") {
		t.Fatalf("[4] before rename:\n%s", s)
	}
	f.api.must(200, "PATCH", f.p+"/rooms/"+f.sessionID, map[string]any{"name": "결제·정산팀"})
	b2 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	if s := section(b2.Brief.Text, 4); !strings.Contains(s, "[4] Room\nRoom: 결제·정산팀\n") {
		t.Fatalf("[4] after rename lacks the new name:\n%s", s)
	}
	if stablePrefix(b1.Brief.Text) == stablePrefix(b2.Brief.Text) {
		t.Error("[1]~[5] did not change across the rename — [4] is not reading room.name")
	}
	b3 := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	if p2, p3 := stablePrefix(b2.Brief.Text), stablePrefix(b3.Brief.Text); p2 != p3 {
		t.Errorf("[1]~[5] changed again after the rename (E12-11 breaks only once):\n--- 2\n%s\n--- 3\n%s", p2, p3)
	}
}
