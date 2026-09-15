package tasks

import "strings"

// LastFailureKindSQL is the ONE definition of FR-1.3 step 3's input, shared by
// the participant list (S7) and the agent page so the two cannot drift.
//
//	LastFailureKind = the agent's MOST RECENT task — whatever its status —
//	                  is `failed` ? its failure_kind : ""
//
// "Most recent", not "most recently finished". The difference is the whole
// point. The old query asked for the last task that ENDED, so after an auth
// failure the Director could fix the credentials, re-instruct, and watch a new
// task run while the agent still read `error` — the sticky error PRD FR-1.3
// warns about ("error 는 '이 에이전트는 지금 실행할 수 없다'만 뜻한다"). Under this
// definition the new task is the most recent one, it is not failed, and the
// error clears by itself. If the new task fails on auth too, it becomes the
// most recent failure and the error comes back — which is correct.
//
// The ladder itself is unchanged: step 3 still outranks step 4 (PRD's table is
// an order, "위에서부터 첫 번째로 맞는 것이 상태다", and golden E5-15 pins that
// order with a synthetic input). This function is what stops production from
// producing that input naturally.
//
// scope is an extra AND-clause on the task lookup ("" for workspace-wide).
func LastFailureKindSQL(scope string) string {
	return strings.Replace(`
		(SELECT CASE WHEN t.status = 'failed' THEN t.failure_kind::text END
		   FROM task t
		  WHERE t.agent_id = a.id %SCOPE%
		  ORDER BY COALESCE(t.started_at, t.dispatched_at, t.created_at) DESC,
		           -- a task still in flight outranks one that ended at the same
		           -- instant: it is the agent's current state
		           (t.finished_at IS NULL) DESC,
		           t.created_at DESC
		  LIMIT 1)`, "%SCOPE%", scope, 1)
}
