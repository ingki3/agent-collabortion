package httpapi

// T-RF1: taskOriginator (router) is shared by Post and Delegate. A trigger an
// agent writes — a mention or a delegation — carries the writing task's
// person originator to the task it makes (PRD FR-4.5 [V19-B]).
//
// 회귀 주입: taskOriginator 가 nil 을 돌려주게 → (mention)·(delegate) FAIL.

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

func TestAgentTriggerCarriesOriginator(t *testing.T) {
	f := newP2Fixture(t)
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.runTask(t, leadTask)
	origin := func(task uuid.UUID) *uuid.UUID {
		t.Helper()
		var o *uuid.UUID
		if err := f.pool.QueryRow(t.Context(), `SELECT originator_user_id FROM task WHERE id = $1`, task).Scan(&o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	want := origin(leadTask)
	if want == nil {
		t.Fatal("setup: the Director's mention gives Lead's task an originator")
	}

	author := router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadTask, Attempt: 1}
	res, err := f.srv.Router.Post(t.Context(), mustUUID(t, f.sessionID), author,
		gen.MessageCreate{Content: router.MentionLink("W", f.wUUID) + " 확인"})
	if err != nil {
		t.Fatal(err)
	}
	if got := origin(uuid.UUID(res.Triggers[0].TaskId)); got == nil || *got != *want {
		t.Fatalf("(mention) W's task originator = %v, want Lead's %v", got, *want)
	}

	del, err := f.srv.Router.Delegate(t.Context(), leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "조사"})
	if err != nil {
		t.Fatal(err)
	}
	if got := origin(uuid.UUID(del.Task.Id)); got == nil || *got != *want {
		t.Fatalf("(delegate) R's task originator = %v, want Lead's %v", got, *want)
	}
}
