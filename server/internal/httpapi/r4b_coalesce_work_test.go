package httpapi

// T-R4b (#327 review (6)): a message filed under a mission that merges into
// a queued task born mission-less must bring the mission along — the task
// is what the turn reads (brief [4], <mission_progress>, the budget,
// COLAB_WORK_ID, the reply's work_id).

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
)

func TestR4bCoalescedTaskTakesLaneMission(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	post := func(body map[string]any) map[string]any {
		f.fake.Advance(time.Minute)
		return f.api.must(201, "POST", f.p+"/rooms/"+rid+"/messages", body, "Idempotency-Key", uuid.NewString())
	}
	trigger := func(out map[string]any) (task, lane string, coalesced bool) {
		tr, _ := out["triggers"].([]any)
		if len(tr) != 1 {
			t.Fatalf("triggers = %v, want 1", out["triggers"])
		}
		m := tr[0].(map[string]any)
		return str(m, "task_id"), str(m, "lane_id"), m["coalesced"] == true
	}
	workOf := func(table, id string) string {
		var w *string
		if err := f.pool.QueryRow(t.Context(), `SELECT work_id::text FROM `+table+` WHERE id = $1`, id).Scan(&w); err != nil {
			t.Fatal(err)
		}
		if w == nil {
			return "-"
		}
		return *w
	}

	// No assignee: opening M starts no task of its own.
	m := str(f.openWork(t, f.api, rid, map[string]any{"goal": "미션 M"}), "id")

	// ① mission-less → R's lane L and queued T1, both without a mission.
	t1, l1, _ := trigger(post(map[string]any{"content": router.MentionLink("R", f.rUUID) + " 미션 없이"}))
	if workOf("task", t1) != "-" || workOf("lane", l1) != "-" {
		t.Fatalf("① T1/L = %s/%s, want no mission", workOf("task", t1), workOf("lane", l1))
	}
	// ② the same agent under M → rule 3's same lane, merged into T1.
	t2, l2, coalesced := trigger(post(map[string]any{"content": router.MentionLink("R", f.rUUID) + " 미션으로", "work_id": m}))
	if !coalesced || t2 != t1 || l2 != l1 {
		t.Fatalf("② = task %s lane %s coalesced %v, want merged into %s on %s", t2, l2, coalesced, t1, l1)
	}
	if got := workOf("lane", l1); got != m {
		t.Fatalf("② lane mission = %s, want %s", got, m)
	}
	if got := workOf("task", t1); got != m {
		t.Fatalf("② merged task mission = %s, want %s — the turn would run as 미션 없음", got, m)
	}

	// The other way round: a mission task absorbing a mission-less post
	// keeps its mission. A mission-less post only reaches unbound lanes
	// (resolveLaneFor), so the lane is unbound by hand — the task alone
	// carries M, and the merge must not write the post's "none" over it.
	lead := f.leadUUID
	t3, l3, _ := trigger(post(map[string]any{"content": router.MentionLink("Lead", lead) + " 미션으로", "work_id": m}))
	f.exec(t, `UPDATE lane SET work_id = NULL WHERE id = $1`, l3)
	t4, _, coalesced := trigger(post(map[string]any{"content": router.MentionLink("Lead", lead) + " 미션 없이"}))
	if !coalesced || t4 != t3 {
		t.Fatalf("reverse = task %s coalesced %v, want merged into %s", t4, coalesced, t3)
	}
	if got := workOf("task", t3); got != m {
		t.Fatalf("reverse merged task mission = %s, want %s kept", got, m)
	}
}
