package httpapi

import (
	"context"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// sessionRoom is what createSession did, built through the room and mission
// API (openapi v0.3.0 removed createSession — D22): a room named after the
// title, its computer · isolation · limits (updateRoom), the agents
// (addRoomParticipant) and ONE mission with the goal, the assignee, the
// criteria and the completion condition (createWork — which starts the
// assignee's first task as createSession did, E16-A step 1).
//
// The mission is then marked as the room's legacy mission, the mark
// createSession (and the 0025 migration) left: a post without `work_id` is
// filed under it (router.legacySessionWork). The tests that call this were
// written against that rule; the rooms made by createRoom alone — "no
// mission" for a plain post — are covered by the r1b* tests.
//
// body takes the old SessionCreate keys the tests use: title, goal,
// isolation, runtime_id, participants [{agent_id, profile_id?}],
// assignee_agent_id, completion_condition, acceptance_criteria, limits,
// autonomy, draft. It returns the room (as getRoom) with two more keys:
// `work_id` and `work` (the mission as createWork answered it).
func sessionRoom(t *testing.T, c *client, q db.DBTX, p, wsID string, body map[string]any) map[string]any {
	t.Helper()
	title, _ := body["title"].(string)
	if title == "" {
		title = "S"
	}
	room := c.must(201, "POST", p+"/workspaces/"+wsID+"/rooms", map[string]any{"name": title})
	roomID := str(room, "id")
	patch := map[string]any{}
	for _, k := range []string{"isolation", "runtime_id", "limits", "autonomy"} {
		if v, ok := body[k]; ok {
			patch[k] = v
		}
	}
	if len(patch) > 0 {
		c.must(200, "PATCH", p+"/rooms/"+roomID, patch)
	}
	if parts, ok := body["participants"]; ok {
		for _, raw := range toMaps(parts) {
			add := map[string]any{"agent_id": raw["agent_id"]}
			if v, ok := raw["profile_id"]; ok {
				add["profile_id"] = v
			}
			c.must(201, "POST", p+"/rooms/"+roomID+"/participants", add)
		}
	}
	wk := map[string]any{}
	for _, k := range []string{"goal", "title", "assignee_agent_id", "completion_condition", "acceptance_criteria", "draft"} {
		if v, ok := body[k]; ok {
			wk[k] = v
		}
	}
	if _, ok := body["assignee_agent_id"]; !ok {
		// createSession's default: the first participant is the assignee.
		if parts := toMaps(body["participants"]); len(parts) > 0 {
			wk["assignee_agent_id"] = parts[0]["agent_id"]
		}
	}
	if a, ok := body["assignee_agent_id"]; ok && a != nil {
		// createSession's assignee was a participant by definition
		// ("참여자 = participants[] + assignee").
		if !hasAgent(toMaps(body["participants"]), a) {
			c.must(201, "POST", p+"/rooms/"+roomID+"/participants", map[string]any{"agent_id": a})
		}
	}
	work := c.must(201, "POST", p+"/rooms/"+roomID+"/works", wk)
	workID := str(work, "id")
	if _, err := q.Exec(context.Background(), `UPDATE room SET legacy_work_id = $2 WHERE id = $1`, roomID, workID); err != nil {
		t.Fatal(err)
	}
	out := c.must(200, "GET", p+"/rooms/"+roomID, nil)
	out["work_id"] = workID
	out["work"] = work
	return out
}

func toMaps(v any) []map[string]any {
	var out []map[string]any
	switch l := v.(type) {
	case []map[string]any:
		out = l
	case []any:
		for _, e := range l {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func hasAgent(parts []map[string]any, id any) bool {
	for _, p := range parts {
		if p["agent_id"] == id {
			return true
		}
	}
	return false
}

// tryWork is createSession's validation reached where it lives now: a fresh
// room with these agents, then ONE createWork call whose status and body are
// the answer. (createSession validated the completion tree and its reviewers
// against "participants[] + assignee"; createWork validates the same tree
// against the room's agents + the assignee — openWork · roomAgents.)
func tryWork(t *testing.T, c *client, p, wsID string, agents []string, work map[string]any) (int, map[string]any) {
	t.Helper()
	room := c.must(201, "POST", p+"/workspaces/"+wsID+"/rooms", map[string]any{"name": "V"})
	for _, a := range agents {
		c.must(201, "POST", p+"/rooms/"+str(room, "id")+"/participants", map[string]any{"agent_id": a})
	}
	if _, ok := work["goal"]; !ok {
		work["goal"] = "g"
	}
	st, out, _ := c.do("POST", p+"/rooms/"+str(room, "id")+"/works", work)
	return st, out
}
