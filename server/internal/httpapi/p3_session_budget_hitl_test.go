package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

// K-10 — approving a SESSION-scoped budget request resumes the session.
//
// The two budget requests are answered in two different places and the
// contract only described one of them. A TASK-scoped request (`task_id` set)
// is answered by respondHitlRequest, which stores the raise and re-queues that
// task (E9-02). A SESSION-scoped one (`task_id` empty, FR-7.3 s-13) was
// answered by respondHitlRequest too — it marked the request `answered` and
// then stopped: the session stayed `paused(budget)`, its parked tasks stayed
// `paused`, and the Director had to go and call resumeSession as a separate
// step, re-typing the amount they had just approved because nothing the
// answer stored was read there.
//
// K-9 — and the inbox card now carries the request's `purpose`, so the web can
// tell a budget pause from a completion approval without a second GET per card
// (#139 NN1).

// openSessionBudgetHitl is the id of the session-scoped budget request.
func (f *p2Fixture) openSessionBudgetHitl(t *testing.T) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id::text FROM hitl_request
		WHERE session_id = $1 AND source = 'system' AND purpose = 'budget'
		  AND task_id IS NULL AND status = 'open'`, f.sessionID).Scan(&id); err != nil {
		t.Fatalf("no open session-scoped budget request: %v", err)
	}
	return id
}

func TestP3SessionBudgetApprovalResumesTheSession(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	parked := f.overrunSession(t, f.rUUID, "R", 1.25)
	if st, reason := f.pausedTask(t, parked); st != "paused" || reason != "budget" {
		t.Fatalf("premise: task = %s(%s), want paused(budget) — the pause cancels the turn (§8.2.2)", st, reason)
	}
	hitlID := f.openSessionBudgetHitl(t)

	// A raise no higher than what is already spent is refused here, exactly as
	// resumeSession refuses it: it would re-trip the pause on the next usage
	// report with nothing changed.
	f.api.must(422, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 1}, "Idempotency-Key", uuid.NewString())
	// And so is an approval with no amount at all — the approval IS the
	// resume, so it has to carry the limit the resume needs.
	f.api.must(422, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())

	out := f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 5}, "Idempotency-Key", uuid.NewString())
	if req, _ := out["hitl_request"].(map[string]any); str(req, "status") != "answered" {
		t.Fatalf("hitl status = %q, want answered", str(req, "status"))
	}
	if out["decision_id"] == nil {
		t.Fatal("no decision record (FR-5.2: exactly one per answer)")
	}

	// One action: the session is back, the raise is the session's new ceiling,
	// and the task the pause parked is queued again.
	var status, reason string
	var limits []byte
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, ''), limits FROM session WHERE id = $1`, f.sessionID).
		Scan(&status, &reason, &limits); err != nil {
		t.Fatal(err)
	}
	if status != "active" || reason != "" {
		t.Fatalf("session = %s(%s) after the approval, want active — the Director must not have to "+
			"call resumeSession as a second step (K-10)", status, reason)
	}
	if got := budgetOf(limits); got != 5 {
		t.Fatalf("limits.budget_usd = %v, want 5 — 세션 잔여 상한 = 승인 금액 (K-10)", got)
	}
	if st, _ := f.pausedTask(t, parked); st != "queued" {
		t.Fatalf("parked task = %q after the approval, want queued (S-46's re-queue, same lane·workdir)", st)
	}
	if !has(f.claimed(t), parked) {
		t.Fatal("the queue did not hand the re-queued task out — a lane left `paused` never dispatches again (S-44)")
	}
}

// TestP3SessionBudgetRejectionKeepsThePause is E9-03's shape at session scope,
// and the contract's own clause: "거절은 paused 유지".
func TestP3SessionBudgetRejectionKeepsThePause(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	parked := f.overrunSession(t, f.rUUID, "R", 1.25)
	hitlID := f.openSessionBudgetHitl(t)

	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": false, "reason": "이번 주 예산은 여기까지"}, "Idempotency-Key", uuid.NewString())

	var status, reason string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, '') FROM session WHERE id = $1`, f.sessionID).
		Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || reason != "budget" {
		t.Fatalf("session = %s(%s) after a rejection, want paused(budget) — a 'no' is not a resume", status, reason)
	}
	if st, _ := f.pausedTask(t, parked); st != "paused" {
		t.Fatalf("parked task = %q after a rejection, want paused — the work waits for 중단, it is "+
			"not thrown away (E9-03)", st)
	}
}

// TestP3InboxCardCarriesHitlPurpose is K-9: the card the web draws says which
// pause this is. `source: system` + `approval` is shared by the budget pause,
// the completion approval and the loop pause (0012), so without `purpose` the
// inbox had to fetch every approval request one at a time to find out.
func TestP3InboxCardCarriesHitlPurpose(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	f.overrunSession(t, f.rUUID, "R", 1.25)
	hitlID := f.openSessionBudgetHitl(t)

	items := f.api.must(200, "GET", f.p+"/inbox?session_id="+f.sessionID, nil)
	found := false
	for _, raw := range items["items"].([]any) {
		row := raw.(map[string]any)
		card, _ := row["card"].(map[string]any)
		if card == nil {
			t.Fatalf("inbox item %s has no card", str(row, "id"))
		}
		if str(row, "ref_id") == hitlID {
			found = true
			if got := card["purpose"]; got != "budget" {
				t.Fatalf("card.purpose = %v, want budget (K-9)", got)
			}
		} else if str(row, "type") != "hitl_request" {
			// A non-HITL item carries no purpose: the column is joined only
			// for `hitl_request`, so a null there says "not a request".
			if got := card["purpose"]; got != nil {
				t.Fatalf("card.purpose = %v on a %s item, want null (K-9)", got, str(row, "type"))
			}
		}
	}
	if !found {
		t.Fatalf("the budget request %s is not in the Director's inbox", hitlID)
	}
}
