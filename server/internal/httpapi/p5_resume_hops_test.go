package httpapi

import (
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// S-80 (PR #216 리뷰 (7)): resumeSession used to DELETE session_hop, which
// reset max_hops_per_hour too — PRD FR-3.5 says the hourly limit does NOT
// reset on a human intervention, so repeated resumes emptied all three layers.
// Now a resume records a HUMAN hop: chain_depth and pair_roundtrips restart
// below the person, the hourly window keeps counting, and the audit rows stay.
func TestS80ResumeKeepsHourlyHops(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_pair_roundtrips": 1, "max_hops_per_hour": 8},
	})
	now := time.Now()
	for i := 0; i < 3; i++ {
		for _, pair := range [][2]string{{f.lead, f.r}, {f.r, f.lead}} {
			if _, err := f.pool.Exec(ctx, `
				INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, created_at)
				VALUES ($1, $2, $3, 2, $4)`, f.sessionID, pair[0], pair[1], now); err != nil {
				t.Fatal(err)
			}
		}
	}
	author := router.Author{Type: "agent", AgentID: &f.rUUID}
	if _, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID), author, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 또",
	}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status::text FROM session WHERE id = $1`, f.sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "paused" {
		t.Fatalf("session = %s, want paused(loop) before the resume", status)
	}
	var before int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session_hop WHERE session_id = $1`, f.sessionID).Scan(&before); err != nil {
		t.Fatal(err)
	}

	// Director resumes with the default reset.
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/resume", map[string]any{})

	var after, human int
	if err := f.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE from_agent_id IS NULL) FROM session_hop WHERE session_id = $1`, f.sessionID).
		Scan(&after, &human); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("session_hop rows = %d after resume, want %d (kept) + 1 human hop — a wipe erases the hourly window and the audit trail (S-80)", after, before)
	}
	if human < 1 {
		t.Fatal("resume must record a human hop (from_agent_id NULL) — that is what resets chain_depth and pair_roundtrips")
	}

	// The pair counter did reset: the same agent→Lead mention now goes through…
	out, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID), author, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 다시",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Triggers) != 1 {
		t.Fatalf("triggers after resume = %d, want 1 — the human hop must reset pair_roundtrips (FR-3.5 재개 카운터)", len(out.Triggers))
	}
	// …but the hourly window still counts the old hops: with max_hops_per_hour 8
	// and 6 seeded + 1 blocked + 1 allowed, the next agent hop is the 9th → limit.
	out, err = f.srv.Router.Post(ctx, mustUUID(t, f.sessionID), router.Author{Type: "agent", AgentID: &f.leadUUID}, gen.MessageCreate{
		Content: router.MentionLink("R", f.rUUID) + " 한 번 더",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Triggers) != 0 || len(out.Warnings) != 1 || out.Warnings[0].Code != "loop_limit" {
		t.Fatalf("hourly limit after resume: triggers=%d warnings=%v — max_hops_per_hour must NOT reset on resume (PRD FR-3.5)", len(out.Triggers), out.Warnings)
	}
}
