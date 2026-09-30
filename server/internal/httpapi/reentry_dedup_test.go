package httpapi

// T-FIX-B (Lead 결정 B, 2026-09-30): the re-entry report to an AGENT requester
// is skipped when the ended lane's own messages already woke that agent — one
// rule (router.requesterAlreadyWoken) for both ways a lane ends. A message
// that reached the requester without waking it (no mention → rule 4; a trigger
// T-QUIET held) does not count: the report goes out.
//
// 회귀 주입: notifyReentry 가 requesterAlreadyWoken 을 무시하면 (woken-*) FAIL;
// 판정이 「깨웠다」 대신 「addressees 에 있다」만 보면 (unmentioned-*) FAIL;
// 보류(approval_pending) 제외를 지우면 (held) FAIL.

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// rPosts: R's running task posts content as an agent message.
func (f *p2Fixture) rPosts(t *testing.T, rTask uuid.UUID, content string) *gen.MessagePostResult {
	t.Helper()
	author := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1}
	res, err := f.srv.Router.Post(t.Context(), mustUUID(t, f.sessionID), author, gen.MessageCreate{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *p2Fixture) reentryNotices(t *testing.T) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND author_type = 'system'
		AND content = '요청하신 작업이 끝났습니다.'`, f.sessionID)
}

func TestReentryReportDedup(t *testing.T) {
	// endBy ends R's lane one of the two ways.
	ends := map[string]func(f *p2Fixture, t *testing.T, rTask uuid.UUID){
		"turn-end": func(f *p2Fixture, t *testing.T, rTask uuid.UUID) { f.finishCompleted(t, rTask) },
		"status-done": func(f *p2Fixture, t *testing.T, rTask uuid.UUID) {
			if _, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "done", ""); err != nil {
				t.Fatal(err)
			}
			f.finishCompleted(t, rTask)
		},
	}
	for name, end := range ends {
		// (woken) R answers Lead with a mention — Lead is woken by it; the
		// lane's end adds no 「요청하신 작업이 끝났습니다」.
		t.Run("woken-"+name, func(t *testing.T) {
			f := newP2Fixture(t)
			rTask, _ := f.agentTriggered(t)
			res := f.rPosts(t, rTask, router.MentionLink("Lead", f.leadUUID)+" 결과입니다")
			if len(res.Triggers) != 1 {
				t.Fatalf("setup: R's mention must wake Lead, triggers = %v", res.Triggers)
			}
			end(f, t, rTask)
			if n := f.reentryNotices(t); n != 0 {
				t.Fatalf("reentry notices = %d, want 0 — Lead was already woken by R's answer", n)
			}
			if n := f.leadQueued(t); n != 1 {
				t.Fatalf("Lead queued = %d, want 1 (the answer's own trigger)", n)
			}
		})
		// (unmentioned) R answers with no mention: addressed to Lead (a
		// report) but rule 4 wakes nobody — the lane's end tells Lead.
		t.Run("unmentioned-"+name, func(t *testing.T) {
			f := newP2Fixture(t)
			rTask, _ := f.agentTriggered(t)
			if res := f.rPosts(t, rTask, "결과입니다"); len(res.Triggers) != 0 {
				t.Fatalf("setup: an unmentioned agent message wakes nobody, triggers = %v", res.Triggers)
			} else if n := f.count(t, `SELECT count(*) FROM message WHERE id = $1
				AND addressees @> jsonb_build_array(jsonb_build_object('kind', 'agent', 'id', $2::text))`,
				uuid.UUID(res.Message.Id), f.leadUUID.String()); n != 1 {
				t.Fatal("setup: the answer must be addressed to Lead (a report) — the boundary is 'reached, not woken'")
			}
			end(f, t, rTask)
			if n := f.reentryNotices(t); n != 1 {
				t.Fatalf("reentry notices = %d, want 1", n)
			}
			if n := f.leadQueued(t); n != 1 {
				t.Fatalf("Lead queued = %d, want 1 (the report)", n)
			}
		})
	}
	// (held) R's mention of Lead made a task, but T-QUIET holds it
	// (approval_pending): that is not a wake-up, the report goes out.
	t.Run("held", func(t *testing.T) {
		f := newP2Fixture(t)
		rTask, _ := f.agentTriggered(t)
		res := f.rPosts(t, rTask, router.MentionLink("Lead", f.leadUUID)+" 결과입니다")
		if len(res.Triggers) != 1 {
			t.Fatalf("setup: triggers = %v", res.Triggers)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE task SET queued_reason = 'approval_pending' WHERE id = $1`, uuid.UUID(res.Triggers[0].TaskId)); err != nil {
			t.Fatal(err)
		}
		f.finishCompleted(t, rTask)
		if n := f.reentryNotices(t); n != 1 {
			t.Fatalf("reentry notices = %d, want 1 — a held trigger did not wake Lead", n)
		}
	})
	// (earlier) a mention of Lead from BEFORE the request (an older turn of
	// the lane) is not an answer to it: the report goes out.
	t.Run("before-request", func(t *testing.T) {
		f := newP2Fixture(t)
		rTask, rLane := f.agentTriggered(t)
		// Backdate R's task: its messages are "older than the request".
		res := f.rPosts(t, rTask, router.MentionLink("Lead", f.leadUUID)+" 이전 이야기")
		if len(res.Triggers) != 1 {
			t.Fatalf("setup: triggers = %v", res.Triggers)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE task SET created_at = created_at - interval '1 hour' WHERE lane_id = $1`, rLane); err != nil {
			t.Fatal(err)
		}
		f.finishCompleted(t, rTask)
		if n := f.reentryNotices(t); n != 1 {
			t.Fatalf("reentry notices = %d, want 1", n)
		}
	})
}
