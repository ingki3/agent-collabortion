package httpapi

import (
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// A part's files are the part's own (openapi v0.3.7 MessagePartCreate.attachment_ids,
// PRD FR-3.7): the group does not share one list, and each recipient's
// trigger names only that part's files. The rules are the single post's —
// same room, at most 10, a repeat kept once — because a part rides the same
// MessageCreate; this test is what proves that reuse is real and not a second
// copy that drifted.
//
// 회귀 주입: PostGroup 의 msg 에서 AttachmentIds 를 빼면 (per-part) FAIL;
// 부분마다가 아니라 묶음 전체에 한 목록을 붙이면 (not-shared) FAIL;
// 부분 경로만 NormalizeAttachments 를 건너뛰면 (part-other-room) FAIL.
func TestPartAttachmentsAreThePartsOwn(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.rUUID, "R")
	dir := f.dirUserID(t)
	dirLink := router.UserMentionLink("Dir", mustUUID(t, dir))
	wLink := router.MentionLink("W", f.wUUID)

	shot := f.uploadID(t, f.sessionID, "", "sprite.png", "attachment", "application/octet-stream", pngBytes)
	bgm := f.uploadID(t, f.sessionID, "", "loop.mp3", "attachment", "application/octet-stream", mp3Bytes)

	st, out, _ := f.groupPost(t, tok, map[string]any{"parts": []any{
		map[string]any{"to": []string{dirLink}, "content": "시안입니다.", "attachment_ids": []string{shot, shot}},
		map[string]any{"to": []string{wLink}, "content": "루프 먼저 들어 보세요.", "attachment_ids": []string{bgm}},
	}}, "")
	if st != 201 {
		t.Fatalf("postMessageGroup = %d %v", st, out)
	}
	parts := partResults(t, out)
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	// (per-part)(not-shared) each row carries its own list, deduped, and NOT the other's.
	for i, want := range [][]string{{shot}, {bgm}} {
		m := parts[i]["message"].(map[string]any)
		raw, _ := m["attachments"].([]any)
		if len(raw) != len(want) {
			t.Fatalf("(per-part) part %d attachments = %v, want %d", i, raw, len(want))
		}
		got := str(raw[0].(map[string]any), "artifact_id")
		if got != want[0] {
			t.Fatalf("(not-shared) part %d has %s, want %s", i, got, want[0])
		}
	}
	// (not-shared) the row in the database agrees — a reader of either part sees one file.
	for i, want := range []string{shot, bgm} {
		id := str(parts[i]["message"].(map[string]any), "id")
		got := f.api.must(200, "GET", f.p+"/messages/"+id, nil)
		atts := got["attachments"].([]any)
		if len(atts) != 1 || str(atts[0].(map[string]any), "artifact_id") != want {
			t.Fatalf("(not-shared) getMessage part %d = %v", i, atts)
		}
	}
	// (part-other-room) a foreign artifact in ONE part refuses the whole group —
	// the parts are one transaction, so no row may survive a bad part.
	otherRoom := sessionRoom(t, f.api, f.pool, f.p, f.wsID, map[string]any{"title": "B", "goal": "g", "isolation": map[string]any{"kind": "none"}})
	other := f.uploadID(t, str(otherRoom, "id"), "", "x.png", "attachment", "application/octet-stream", pngBytes)
	before := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID)
	st, out, _ = f.groupPost(t, tok, map[string]any{"parts": []any{
		map[string]any{"to": []string{dirLink}, "content": "하나는 남의 방", "attachment_ids": []string{other}},
		map[string]any{"to": []string{wLink}, "content": "이쪽은 멀쩡", "attachment_ids": []string{bgm}},
	}}, "")
	if st != 422 || firstCode(out) != "attachment_not_in_room" {
		t.Fatalf("(part-other-room) = %d %v", st, out)
	}
	if after := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID); after != before {
		t.Fatalf("(part-other-room) rows %d → %d: a refused group left a row", before, after)
	}
}
