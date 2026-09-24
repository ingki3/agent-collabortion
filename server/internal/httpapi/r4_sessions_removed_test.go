package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

// T-R4 (openapi v0.3.0, D22 — Director 승인 2026-09-24): the old /sessions/*
// addresses are gone, `colab room get` reads the room, its mission and its
// roster with the task's own token, and nothing publishes session.* frames.

// TestR4OldSessionPathsAre404 — (a) every old address answers 404: the
// router has no route for them, whoever asks.
func TestR4OldSessionPathsAre404(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	agent := &client{t: t, srv: f.api.srv, bearer: tok}
	sp := f.p + "/sessions/" + f.sessionID
	for _, c := range []struct {
		method, path string
	}{
		{"GET", sp + "/messages"}, {"POST", sp + "/messages"}, {"GET", sp}, {"PATCH", sp}, {"DELETE", sp},
		{"GET", sp + "/participants"}, {"GET", sp + "/lanes"}, {"GET", sp + "/artifacts"}, {"GET", sp + "/decisions"},
		{"GET", sp + "/hitl-requests"}, {"GET", sp + "/cost"}, {"GET", sp + "/tasks"}, {"POST", sp + "/rebind"},
		{"POST", sp + "/pause"}, {"POST", sp + "/resume"}, {"POST", sp + "/complete"}, {"POST", sp + "/cancel"},
		{"GET", f.p + "/workspaces/" + f.wsID + "/sessions"}, {"POST", f.p + "/workspaces/" + f.wsID + "/sessions"},
	} {
		for _, who := range []*client{f.api, agent} {
			if st, _, _ := who.raw(c.method, c.path, map[string]any{}); st != 404 {
				t.Errorf("%s %s = %d, want 404 (removed in openapi v0.3.0)", c.method, c.path, st)
			}
		}
	}
	// The same reads at their new address answer.
	f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages", nil)
	agent.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages", nil)
}

// TestR4TaskTokenRoomGet — (b) getRoom · getWork · listRoomParticipants take
// the TaskToken (`colab room get`) for the task's OWN room only: another
// room is 403 outside_task_scope (the old sessionAccess rule), a room that
// does not exist is 404.
func TestR4TaskTokenRoomGet(t *testing.T) {
	f := newP2Fixture(t)
	other := sessionRoom(t, f.api, f.pool, f.p, f.wsID, map[string]any{
		"title": "다른 방", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": f.lead, "participants": []map[string]any{{"agent_id": f.lead}},
	})
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	agent := &client{t: t, srv: f.api.srv, bearer: tok}

	own := []string{
		f.p + "/rooms/" + f.sessionID,
		f.p + "/works/" + f.missionID,
		f.p + "/rooms/" + f.sessionID + "/participants",
	}
	for _, path := range own {
		agent.must(200, "GET", path, nil)
	}
	room := agent.must(200, "GET", own[0], nil)
	if _, ok := room["my_subscription"]; ok {
		t.Errorf("an agent's getRoom carries my_subscription (a person's field): %v", room["my_subscription"])
	}
	work := agent.must(200, "GET", own[1], nil)
	if str(work, "goal") != "g" || work["completion_progress"] == nil {
		t.Errorf("getWork (token) lacks what `room get` shows: %v", work)
	}
	for _, p := range agentRows(t, agent.must(200, "GET", own[2], nil)) {
		if str(p, "status") == "" {
			t.Errorf("roster row without the derived status: %v", p)
		}
	}

	foreign := []string{
		f.p + "/rooms/" + str(other, "id"),
		f.p + "/works/" + str(other, "work_id"),
		f.p + "/rooms/" + str(other, "id") + "/participants",
	}
	for _, path := range foreign {
		st, out, _ := agent.do("GET", path, nil)
		if st != 403 || str(out, "code") != "outside_task_scope" {
			t.Errorf("GET %s with another room's token = %d %v, want 403 outside_task_scope", path, st, out)
		}
	}
	for _, path := range []string{
		f.p + "/rooms/" + uuid.NewString(),
		f.p + "/works/" + uuid.NewString(),
		f.p + "/rooms/" + uuid.NewString() + "/participants",
	} {
		if st, _, _ := agent.do("GET", path, nil); st != 404 {
			t.Errorf("GET %s (no such room) = %d, want 404", path, st)
		}
	}
}

// TestR4NoSessionFrames — (c) the flows that used to send session.updated ·
// session.deleted · session.completion_progress (pause, resume, the loop and
// budget gates' lift, completion, delete) send none now; the room.* · work.*
// frames carry the same events. The package's TestMain holds the rest of the
// suite to the same rule (realtime.TypeViolations).
func TestR4NoSessionFrames(t *testing.T) {
	f := newP2Fixture(t)
	f.api.must(200, "POST", f.p+"/works/"+f.missionID+"/pause", nil)
	f.api.must(200, "POST", f.p+"/works/"+f.missionID+"/resume", nil)
	f.api.must(200, "POST", f.p+"/rooms/"+f.sessionID+"/block", nil)
	f.api.must(200, "POST", f.p+"/rooms/"+f.sessionID+"/unblock", nil)
	f.api.must(200, "POST", f.p+"/works/"+f.missionID+"/complete", map[string]any{"confirm": true})
	f.api.must(204, "DELETE", f.p+"/rooms/"+f.sessionID, nil)
	var n, rooms, works int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FILTER (WHERE type LIKE 'session.%'),
		       count(*) FILTER (WHERE type IN ('room.updated', 'room.deleted')),
		       count(*) FILTER (WHERE type IN ('work.updated', 'work.completion_progress'))
		FROM stream_event`).Scan(&n, &rooms, &works); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d session.* frame(s) stored — removed in openapi v0.3.0", n)
	}
	if rooms == 0 || works == 0 {
		t.Fatalf("room.* = %d, work.* = %d — the same events must still be sent under their names", rooms, works)
	}
}

// TestR4FrameTypeGuardIsWired proves TestMain's frame check can fail.
func TestR4FrameTypeGuardIsWired(t *testing.T) {
	f := newP2Fixture(t)
	before := len(realtime.TypeViolations())
	if err := f.srv.Hub.Publish(t.Context(), nil, mustUUID(t, f.wsID), nil, "session.updated", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := realtime.TypeViolations(); len(got) != before+1 || got[before] != "session.updated" {
		t.Fatalf("violations = %v, want session.updated recorded", got)
	}
	realtime.TrimTypeViolations(before)
}
