package httpapi

// T-S-toapi: openapi 0.2.0 put the mission on every entity a room shows —
// Message · Lane · Task · Decision (Artifact already carried it) — and the
// database has filled the columns since R1a/R1b, but the response mappers
// never read them: the web's mission chip filtered on fields that were always
// null (T-R2-W2 on a real server). These tests read each field through the
// response, the SSE frame included, never through the table.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

// frame returns the `typ` frame whose payload id is `id`, marshalled the way
// the SSE handler writes it (realtime.Event.MarshalJSON adds the v0.2.0
// envelope keys), decoded back.
func (f *p2Fixture) frame(t *testing.T, typ, id string) map[string]any {
	t.Helper()
	var ev realtime.Event
	var payload []byte
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id, type, created_at, workspace_id, session_id, payload FROM stream_event
		 WHERE type = $1 AND payload->>'id' = $2 ORDER BY id DESC LIMIT 1`, typ, id).
		Scan(&ev.ID, &ev.Type, &ev.At, &ev.WorkspaceID, &ev.SessionID, &payload); err != nil {
		t.Fatalf("%s frame for %s: %v", typ, id, err)
	}
	ev.Payload = payload
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// hasNull is "the key is there and says null" — 「미션 없음」 is an answer,
// not a missing field.
func hasNull(m map[string]any, k string) bool {
	v, ok := m[k]
	return ok && v == nil
}

func TestWorkIDOnMessages(t *testing.T) {
	f := newP2Fixture(t)
	work := f.workID(t).String()

	posted := f.post(t, map[string]any{"content": "@R 조사해 줘"})
	msg := posted["message"].(map[string]any)
	id := str(msg, "id")
	if str(msg, "work_id") != work {
		t.Fatalf("postMessage message.work_id = %v, want the room's mission %s", msg["work_id"], work)
	}

	// listMessages — both a plain page and the chip's own filter.
	for _, q := range []string{"", "?work_id=" + work} {
		var found bool
		for _, raw := range items(f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages"+q, nil)) {
			m := raw.(map[string]any)
			if str(m, "id") == id {
				found = true
				if str(m, "work_id") != work {
					t.Fatalf("listMessages%s work_id = %v, want %s", q, m["work_id"], work)
				}
			}
		}
		if !found {
			t.Fatalf("listMessages%s lost the message", q)
		}
	}

	// getMessage (was 501, T-R2-W3).
	if got := f.api.must(200, "GET", f.p+"/messages/"+id, nil); str(got, "work_id") != work || str(got, "content") != "@R 조사해 줘" {
		t.Fatalf("getMessage = %v", got)
	}

	// message.created — the payload AND the envelope (lifted from the payload).
	fr := f.frame(t, "message.created", id)
	if str(fr["payload"].(map[string]any), "work_id") != work || str(fr, "work_id") != work {
		t.Fatalf("message.created frame work_id payload=%v envelope=%v, want %s", fr["payload"].(map[string]any)["work_id"], fr["work_id"], work)
	}

	// A message of no mission answers null, not a missing key.
	var none uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO message (session_id, author_type, content, kind, created_at)
		VALUES ($1, 'system', 'x', 'system', now()) RETURNING id`, f.sessionID).Scan(&none); err != nil {
		t.Fatal(err)
	}
	if got := f.api.must(200, "GET", f.p+"/messages/"+none.String(), nil); !hasNull(got, "work_id") {
		t.Fatalf("getMessage of no mission work_id = %v (present=%v), want null", got["work_id"], got["work_id"] == nil)
	}
}

func TestWorkIDOnLanesTasksDecisions(t *testing.T) {
	f := newP2Fixture(t)
	work := f.workID(t).String()
	var title string
	if err := f.pool.QueryRow(t.Context(), `SELECT title FROM work WHERE id = $1`, work).Scan(&title); err != nil {
		t.Fatal(err)
	}
	posted := f.post(t, map[string]any{"content": "@R 조사해 줘"})
	taskID := str(posted["triggers"].([]any)[0].(map[string]any), "task_id")
	f.exec(t, `UPDATE task SET queued_reason = 'room_lanes' WHERE id = $1`, taskID)

	// getTask
	task := f.api.must(200, "GET", f.p+"/tasks/"+taskID, nil)
	if str(task, "work_id") != work || str(task, "queued_reason") != "room_lanes" {
		t.Fatalf("getTask work_id=%v queued_reason=%v, want %s · room_lanes", task["work_id"], task["queued_reason"], work)
	}

	// listLanes — the lane's own mission, its title, and why it waits.
	var lane map[string]any
	for _, raw := range f.api.mustList(200, "GET", f.p+"/rooms/"+f.sessionID+"/lanes", nil) {
		l := raw.(map[string]any)
		if cur, ok := l["current_task"].(map[string]any); ok && str(cur, "id") == taskID {
			lane = l
		}
	}
	if lane == nil {
		t.Fatal("listLanes has no lane for the task")
	}
	if str(lane, "work_id") != work || str(lane, "work_title") != title || str(lane, "queued_reason") != "room_lanes" {
		t.Fatalf("lane work_id=%v work_title=%v queued_reason=%v, want %s · %q · room_lanes", lane["work_id"], lane["work_title"], lane["queued_reason"], work, title)
	}
	if cur := lane["current_task"].(map[string]any); str(cur, "work_id") != work {
		t.Fatalf("lane.current_task.work_id = %v", cur["work_id"])
	}

	// lane.updated frame (lanes.Publish reads the same Load).
	var laneID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id FROM task WHERE id = $1`, taskID).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `DELETE FROM stream_event WHERE type = 'lane.updated'`)
	if err := lanes.Publish(t.Context(), f.srv.Hub, f.pool, laneID); err != nil {
		t.Fatal(err)
	}
	fr := f.frame(t, "lane.updated", laneID.String())
	if p := fr["payload"].(map[string]any); str(p, "work_id") != work || str(p, "work_title") != title || str(fr, "work_id") != work {
		t.Fatalf("lane.updated frame payload work_id=%v work_title=%v envelope=%v", p["work_id"], p["work_title"], fr["work_id"])
	}

	// A lane of no mission: null, with no title.
	f.exec(t, `UPDATE lane SET work_id = NULL WHERE id = $1`, laneID)
	for _, raw := range f.api.mustList(200, "GET", f.p+"/rooms/"+f.sessionID+"/lanes", nil) {
		if l := raw.(map[string]any); str(l, "id") == laneID.String() && (!hasNull(l, "work_id") || !hasNull(l, "work_title")) {
			t.Fatalf("lane of no mission work_id=%v work_title=%v, want null · null", l["work_id"], l["work_title"])
		}
	}

	// listDecisions.
	f.exec(t, `INSERT INTO decision (session_id, summary, source, created_at, work_id) VALUES ($1, '결정', 'agent', now(), $2)`, f.sessionID, work)
	decs := f.api.mustList(200, "GET", f.p+"/rooms/"+f.sessionID+"/decisions", nil)
	if len(decs) == 0 || str(decs[len(decs)-1].(map[string]any), "work_id") != work {
		t.Fatalf("listDecisions = %v, want the decision with work_id %s", decs, work)
	}
}

// TestWorkIDOnRoomRead: readRoom (FR-4.5) returns the other room's messages
// and decisions — each with its mission, the same mapping as the room's own
// screen. The decision read had its own SELECT that dropped work_id.
func TestWorkIDOnRoomRead(t *testing.T) {
	f := newRoomReadFixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	var work uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT legacy_work_id FROM room WHERE id = $1`, f.b).Scan(&work); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO decision (session_id, summary, source, created_at, work_id) VALUES ($1, '인프라 결정', 'agent', now(), $2)`, f.b, work)
	f.exec(t, `INSERT INTO message (session_id, author_type, content, kind, created_at, work_id) VALUES ($1, 'system', '인프라 메모', 'system', now(), $2)`, f.b, work)
	f.fake.Advance(time.Second)
	st, out := f.read(t, tok, f.b, "")
	if st != 200 {
		t.Fatalf("read = %d %v", st, out)
	}
	var dec, msg bool
	for _, raw := range out["decisions"].([]any) {
		if d := raw.(map[string]any); str(d, "summary") == "인프라 결정" {
			dec = str(d, "work_id") == work.String()
		}
	}
	for _, raw := range out["messages"].([]any) {
		if m := raw.(map[string]any); str(m, "content") == "인프라 메모" {
			msg = str(m, "work_id") == work.String()
		}
	}
	if !dec || !msg {
		t.Fatalf("readRoom work_id on decision=%v message=%v, want both %s", dec, msg, work)
	}
}

// TestGetMessageAccess: getMessage answers with the room's access — a member
// of a room reads it, a task token reads its own room, and everyone else gets
// the message's 404 (not the room's 403/404: FR-5.3 존재 숨김).
func TestGetMessageAccess(t *testing.T) {
	f := newP2Fixture(t)
	id := str(f.post(t, map[string]any{"content": "hello"})["message"].(map[string]any), "id")

	notFound := func(st int, out map[string]any, who string) {
		t.Helper()
		if st != 404 || str(out, "code") != "not_found" || str(out, "detail") != "메시지를 찾을 수 없습니다" {
			t.Fatalf("%s: getMessage = %d %v, want the message's 404", who, st, out)
		}
	}
	st, out, _ := f.api.do("GET", f.p+"/messages/"+uuid.NewString(), nil)
	notFound(st, out, "missing id")

	// Same room's task token: yes. Another room's: the same 404.
	tok, _ := f.agentToken(t, f.sessionID, f.rUUID, "R")
	if st, out := f.rawGet(t, f.p+"/messages/"+id, tok); st != 200 || str(out, "id") != id {
		t.Fatalf("own room's task token: %d %v", st, out)
	}
	other := str(sessionRoom(t, f.api, f.pool, f.p, f.wsID, map[string]any{
		"title": "T", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": f.lead, "participants": []map[string]any{{"agent_id": f.lead}},
	}), "id")
	otherTok, _ := f.agentToken(t, other, f.leadUUID, "Lead")
	st, out = f.rawGet(t, f.p+"/messages/"+id, otherTok)
	notFound(st, out, "another room's task token")

	// A workspace member who is not invited to an invited room.
	m := f.addMember(t, "m@example.com", "M")
	if st, _, _ := m.do("GET", f.p+"/messages/"+id, nil); st != 200 {
		t.Fatalf("member of a workspace room: %d", st)
	}
	f.exec(t, `UPDATE room SET visibility = 'invited' WHERE id = $1`, f.sessionID)
	st, out, _ = m.do("GET", f.p+"/messages/"+id, nil)
	notFound(st, out, "member outside an invited room")

	// Signed out.
	anon := &client{t: t, srv: f.api.srv}
	if st, _, _ := anon.do("GET", f.p+"/messages/"+id, nil); st != 401 {
		t.Fatalf("signed out: %d, want 401", st)
	}
}
