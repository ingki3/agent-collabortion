package httpapi

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roles"
)

// FR-1.9.1 (v1.1, K-19) — the role's command subset flows out through three
// surfaces (Agent.allowed_commands · CliContext.allowed_commands · the daemon
// bundle's task.allowed_commands) and is ENFORCED on every x-colab-cli
// operation's task-token path: a reviewer delegating and a writer approving
// get `403 command_not_allowed` with colab-cli.md §2.5's sentence and a
// `status` feed row (§4), a lead and a custom get past the gate everywhere,
// and a person is never gated by the table.

// cmdOps drives one x-colab-cli operation per ColabCommand with a task token
// and answers the status and body. `artifact` is a submitted artifact's id
// for the get/review rows (any session member may read it).
func (f *p2Fixture) cmdOp(t *testing.T, tok string, taskID uuid.UUID, cmd gen.ColabCommand, artifact string) (int, map[string]any) {
	t.Helper()
	c := &client{t: t, srv: f.api.srv, bearer: tok}
	sess := f.p + "/sessions/" + f.sessionID
	key := func() []string { return []string{"Idempotency-Key", uuid.NewString()} }
	switch cmd {
	case gen.ColabCommandRoomGet:
		st, out, _ := c.do("GET", sess, nil)
		return st, out
	case gen.ColabCommandRoomMessages:
		st, out, _ := c.do("GET", sess+"/messages", nil)
		return st, out
	case gen.ColabCommandMessagePost:
		st, out, _ := c.do("POST", sess+"/messages", map[string]any{"content": "한 마디"}, key()...)
		return st, out
	case gen.ColabCommandStatusSet:
		st, out, _ := c.do("POST", f.p+"/tasks/"+taskID.String()+"/status", map[string]any{"status": "working"})
		return st, out
	case gen.ColabCommandDecisionRecord:
		st, out, _ := c.do("POST", sess+"/decisions", map[string]any{"summary": "결정"}, key()...)
		return st, out
	case gen.ColabCommandLaneDelegate:
		st, out, _ := c.do("POST", sess+"/lanes", map[string]any{"agent_id": f.w, "brief": "초안을 써 주세요"}, key()...)
		return st, out
	case gen.ColabCommandArtifactSubmit:
		return f.submit(t, f.sessionID, tok, "doc-"+uuid.NewString()[:8]+".md", "doc", []byte("# x"))
	case gen.ColabCommandArtifactGet:
		st, out, _ := c.do("GET", f.p+"/artifacts/"+artifact, nil)
		return st, out
	case gen.ColabCommandReviewApprove, gen.ColabCommandReviewReject:
		verdict := "approve"
		if cmd == gen.ColabCommandReviewReject {
			verdict = "reject"
		}
		st, out, _ := c.do("POST", f.p+"/artifacts/"+artifact+"/review", map[string]any{"verdict": verdict, "comments": "확인"}, key()...)
		return st, out
	case gen.ColabCommandHitlAsk:
		st, out, _ := c.do("POST", sess+"/hitl-requests", map[string]any{"type": "question", "question": "q?", "proposed_default": "a"}, key()...)
		return st, out
	case gen.ColabCommandHitlApproveRequest:
		st, out, _ := c.do("POST", sess+"/hitl-requests", map[string]any{"type": "approval", "summary": "끝났습니다"}, key()...)
		return st, out
	case gen.ColabCommandHitlRequestInfo:
		st, out, _ := c.do("POST", sess+"/hitl-requests", map[string]any{"type": "info", "what": "w", "why": "y"}, key()...)
		return st, out
	case gen.ColabCommandRoomList:
		st, out, _ := c.do("GET", f.p+"/cli/rooms", nil)
		return st, out
	case gen.ColabCommandRoomRead:
		// The current room: past the gate it is readRoom's own 422
		// current_room — the operation's business, not the table's.
		st, out, _ := c.do("GET", f.p+"/cli/rooms/"+f.sessionID+"/read", nil)
		return st, out
	case gen.ColabCommandWorkPropose:
		st, out, _ := c.do("POST", f.p+"/rooms/"+f.sessionID+"/work-proposals", map[string]any{"goal": "새 미션", "rationale": "근거"}, key()...)
		return st, out
	}
	t.Fatalf("no driver for %s", cmd)
	return 0, nil
}

// refusedRows is the §4 feed record of refused commands on one attempt.
func (f *p2Fixture) refusedRows(t *testing.T, taskID uuid.UUID) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `
		SELECT verb || ' ' || (object_ref #>> '{}') || ' ' || (payload->>'command')
		FROM task_event WHERE task_id = $1 AND class = 'status' AND outcome = 'rejected'
		  AND payload->>'rejected_reason' = 'command_not_allowed' ORDER BY seq`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestV11CommandNotAllowed(t *testing.T) {
	f := newP2Fixture(t)
	// A custom-role agent joins the session too.
	custom := str(f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "C", "role": "custom", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	}), "id")
	customUUID := mustUUID(t, custom)
	f.api.must(201, "POST", f.p+"/sessions/"+f.sessionID+"/participants", map[string]any{"agent_id": custom})
	// A reviewer-role agent as well (the fixture has lead · researcher · writer).
	reviewer := str(f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "Rev", "role": "reviewer", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	}), "id")
	reviewerUUID := mustUUID(t, reviewer)
	f.api.must(201, "POST", f.p+"/sessions/"+f.sessionID+"/participants", map[string]any{"agent_id": reviewer})

	// Surface 1 — Agent.allowed_commands is the §2.5 row, read-only.
	for _, tc := range []struct {
		id   string
		role gen.AgentRole
	}{{f.lead, gen.Lead}, {f.r, gen.Researcher}, {f.w, gen.Writer}, {reviewer, gen.Reviewer}, {custom, gen.Custom}} {
		a := f.api.must(200, "GET", f.p+"/agents/"+tc.id, nil)
		got, _ := a["allowed_commands"].([]any)
		want := roles.AllowedCommands(tc.role)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("agent %s allowed_commands = %v, want %v", tc.role, got, want)
		}
	}
	// The field is derived: a PATCH that tries to set it is ignored (the
	// generated AgentUpdate has no such field, so it is simply not read).
	f.api.must(200, "PATCH", f.p+"/agents/"+f.w, map[string]any{"allowed_commands": []string{"lane_delegate"}})
	if got, _ := f.api.must(200, "GET", f.p+"/agents/"+f.w, nil)["allowed_commands"].([]any); fmt.Sprint(got) != fmt.Sprint(roles.AllowedCommands(gen.Writer)) {
		t.Errorf("writer allowed_commands after PATCH = %v, want the role's row (derived, read-only)", got)
	}

	// One turn per agent. The tokens are the ones the QUEUE hands out with
	// the bundle (a claim re-issues the attempt's token, so a token minted
	// beforehand is dead by then) — which is also surface 3.
	_, taskLead := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	_, taskW := f.agentToken(t, f.sessionID, f.wUUID, "W")
	_, taskRev := f.agentToken(t, f.sessionID, reviewerUUID, "Rev")
	_, taskC := f.agentToken(t, f.sessionID, customUUID, "C")
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	bundles, err := f.srv.Queue.Claim(t.Context(), runtimeID.String(), 10, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[uuid.UUID]string{}
	for _, b := range bundles {
		var want []string
		switch b.Task.ID {
		case taskRev.String():
			want = roles.AllowedCommandStrings(gen.Reviewer)
		case taskLead.String():
			want = roles.AllowedCommandStrings(gen.Lead)
		case taskW.String():
			want = roles.AllowedCommandStrings(gen.Writer)
		case taskC.String():
			want = roles.AllowedCommandStrings(gen.Custom)
		default:
			continue
		}
		tokens[mustUUID(t, b.Task.ID)] = b.TaskToken
		if fmt.Sprint(b.Task.AllowedCommands) != fmt.Sprint(want) {
			t.Errorf("bundle %s allowed_commands = %v, want %v", b.Task.ID, b.Task.AllowedCommands, want)
		}
	}
	if len(tokens) != 4 {
		t.Fatalf("claimed %d of the 4 turns", len(tokens))
	}
	tokLead, tokW, tokRev, tokC := tokens[taskLead], tokens[taskW], tokens[taskRev], tokens[taskC]
	// An artifact the Lead submitted, for the get/review rows.
	st, out := f.submit(t, f.sessionID, tokLead, "plan.md", "doc", []byte("# plan"))
	if st != 201 {
		t.Fatalf("lead submit = %d %v", st, out)
	}
	artifact := str(out["artifact"].(map[string]any), "id")

	// Surface 2 — CliContext.allowed_commands.
	for _, tc := range []struct {
		tok  string
		role gen.AgentRole
	}{{tokLead, gen.Lead}, {tokW, gen.Writer}, {tokRev, gen.Reviewer}, {tokC, gen.Custom}} {
		c := &client{t: t, srv: f.api.srv, bearer: tc.tok}
		_, ctx, _ := c.do("GET", f.p+"/cli/context", nil)
		got, _ := ctx["allowed_commands"].([]any)
		if fmt.Sprint(got) != fmt.Sprint(roles.AllowedCommands(tc.role)) {
			t.Errorf("cli/context %s allowed_commands = %v", tc.role, got)
		}
	}

	// Enforcement — reviewer: delegate 403 · submit 403 · approve-request 403,
	// and the sentence is §2.5's, with the enum in the `command` slot.
	st, out = f.cmdOp(t, tokRev, taskRev, gen.ColabCommandLaneDelegate, artifact)
	if st != 403 || str(out, "code") != "command_not_allowed" {
		t.Fatalf("reviewer delegate = %d %v, want 403 command_not_allowed", st, out)
	}
	if str(out, "detail") != "이 역할(reviewer)은 lane delegate 를 쓸 수 없습니다" || str(out, "command") != "lane_delegate" || str(out, "role") != "reviewer" {
		t.Errorf("reviewer delegate problem = %v", out)
	}
	if st, out := f.cmdOp(t, tokRev, taskRev, gen.ColabCommandArtifactSubmit, artifact); st != 403 || str(out, "code") != "command_not_allowed" {
		t.Errorf("reviewer submit = %d %v, want 403", st, out)
	}
	if st, out := f.cmdOp(t, tokRev, taskRev, gen.ColabCommandHitlApproveRequest, artifact); st != 403 || str(out, "detail") != "이 역할(reviewer)은 hitl approve-request 를 쓸 수 없습니다" {
		t.Errorf("reviewer approve-request = %d %v, want 403", st, out)
	}
	// v0.8: work propose is not the reviewer's either — and no proposal row
	// is written by the refusal.
	if st, out := f.cmdOp(t, tokRev, taskRev, gen.ColabCommandWorkPropose, artifact); st != 403 || str(out, "detail") != "이 역할(reviewer)은 work propose 를 쓸 수 없습니다" || str(out, "command") != "work_propose" {
		t.Errorf("reviewer work propose = %d %v, want 403", st, out)
	}
	var proposals int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM work_proposal WHERE proposed_by_task_id = $1`, taskRev).Scan(&proposals); err != nil {
		t.Fatal(err)
	}
	if proposals != 0 {
		t.Errorf("refused work propose stored %d proposals", proposals)
	}
	// …and the feed carries each refusal (§4), command as typed.
	if got := f.refusedRows(t, taskRev); fmt.Sprint(got) != "[delegate lane_delegate lane delegate submit_artifact artifact_submit artifact submit hitl hitl_approve_request hitl approve-request hitl work_propose work propose]" {
		t.Errorf("reviewer refused rows = %v", got)
	}
	// No lane was created by the refused delegation.
	var lanes int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM lane WHERE session_id = $1 AND delegated_from_task_id = $2`, f.sessionID, taskRev).Scan(&lanes); err != nil {
		t.Fatal(err)
	}
	if lanes != 0 {
		t.Errorf("refused delegation created %d lanes", lanes)
	}
	// What the reviewer MAY do goes through the gate (whatever the operation
	// then answers is that operation's business, never command_not_allowed).
	for _, cmd := range roles.AllowedCommands(gen.Reviewer) {
		if st, out := f.cmdOp(t, tokRev, taskRev, cmd, artifact); st == 403 && str(out, "code") == "command_not_allowed" {
			t.Errorf("reviewer %s gated: %d %v", cmd, st, out)
		}
	}

	// writer: review approve 403 · reject 403 · delegate 403 · approve-request 403
	// · work propose 403 (v0.8: a mission proposal is the Lead's).
	for _, cmd := range []gen.ColabCommand{gen.ColabCommandReviewApprove, gen.ColabCommandReviewReject, gen.ColabCommandLaneDelegate, gen.ColabCommandHitlApproveRequest, gen.ColabCommandWorkPropose} {
		st, out := f.cmdOp(t, tokW, taskW, cmd, artifact)
		if st != 403 || str(out, "code") != "command_not_allowed" || str(out, "command") != string(cmd) {
			t.Errorf("writer %s = %d %v, want 403 command_not_allowed", cmd, st, out)
		}
	}
	if st, out := f.cmdOp(t, tokW, taskW, gen.ColabCommandReviewApprove, artifact); str(out, "detail") != "이 역할(writer)은 review approve 를 쓸 수 없습니다" {
		t.Errorf("writer approve = %d %v", st, out)
	}
	if got := f.refusedRows(t, taskW); len(got) != 6 || got[0] != "review review_approve review approve" || got[4] != "hitl work_propose work propose" {
		t.Errorf("writer refused rows = %v", got)
	}
	for _, cmd := range roles.AllowedCommands(gen.Writer) {
		if st, out := f.cmdOp(t, tokW, taskW, cmd, artifact); st == 403 && str(out, "code") == "command_not_allowed" {
			t.Errorf("writer %s gated: %d %v", cmd, st, out)
		}
	}

	// lead and custom: everything passes the gate.
	for _, tc := range []struct {
		name string
		tok  string
		task uuid.UUID
	}{{"lead", tokLead, taskLead}, {"custom", tokC, taskC}} {
		for _, cmd := range roles.All() {
			st, out := f.cmdOp(t, tc.tok, tc.task, cmd, artifact)
			if st == 403 && str(out, "code") == "command_not_allowed" {
				t.Errorf("%s %s gated: %d %v", tc.name, cmd, st, out)
			}
			if st >= 500 {
				t.Errorf("%s %s = %d %v", tc.name, cmd, st, out)
			}
		}
		if got := f.refusedRows(t, tc.task); len(got) != 0 {
			t.Errorf("%s refused rows = %v, want none", tc.name, got)
		}
	}

	// A person is not gated: the Director reads the session and posts.
	f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	f.api.must(201, "POST", f.p+"/sessions/"+f.sessionID+"/messages", map[string]any{"content": "사람"}, "Idempotency-Key", uuid.NewString())
}
