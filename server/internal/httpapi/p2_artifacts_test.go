package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/artifacts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// These rows are the artifact half of T-S3 against a real database. The E6
// golden table proves what ApplyEvent decides; what it cannot prove is that
// `colab artifact submit` and `colab review approve` actually reach it, that
// the bytes survive the round trip, or that the boundary answers 403/404/413
// with the exact codes the CLI branches on.

// artifactSession creates a session whose completion tree is exactly the one
// the row under test needs, and returns its id.
func (f *p2Fixture) artifactSession(t *testing.T, tree map[string]any) string {
	t.Helper()
	f.fake.Advance(time.Minute)
	sess := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/sessions", map[string]any{
		"title": "A", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id":    f.lead,
		"completion_condition": tree,
		"participants": []map[string]any{
			{"agent_id": f.lead}, {"agent_id": f.r}, {"agent_id": f.w},
		},
	})
	return str(sess, "id")
}

// agentToken mints a live TaskToken for `agent` in `sessionID` by waking it the
// way a person would and issuing the token the daemon would have received.
func (f *p2Fixture) agentToken(t *testing.T, sessionID string, agent uuid.UUID, name string) (string, uuid.UUID) {
	t.Helper()
	f.fake.Advance(time.Minute)
	out := f.api.must(201, "POST", f.p+"/sessions/"+sessionID+"/messages",
		map[string]any{"content": router.MentionLink(name, agent) + " 부탁합니다"},
		"Idempotency-Key", uuid.NewString())
	var taskID, laneID uuid.UUID
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent.String() {
			taskID, laneID = mustUUID(t, str(tr, "task_id")), mustUUID(t, str(tr, "lane_id"))
		}
	}
	if taskID == uuid.Nil {
		t.Fatalf("no task for %s: %v", name, out["triggers"])
	}
	tok, err := f.srv.Tokens.Issue(t.Context(), f.pool, tokens.Scope{
		TaskID: taskID, Attempt: 1, LaneID: laneID, SessionID: mustUUID(t, sessionID), AgentID: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok, taskID
}

// multipartBody builds a submitArtifact body exactly as the CLI encodes one
// (cli/internal/client/ops_p2.go SubmitArtifact).
func multipartBody(t *testing.T, name, typ, desc string, data []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(mw.WriteField("name", name))
	must(mw.WriteField("type", typ))
	if desc != "" {
		must(mw.WriteField("description", desc))
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	h.Set("Content-Type", "text/plain")
	part, err := mw.CreatePart(h)
	must(err)
	_, err = part.Write(data)
	must(err)
	must(mw.Close())
	return mw.FormDataContentType(), buf.Bytes()
}

// submit posts a multipart body with the given credential (bearer token, or
// the fixture cookie when tok is empty).
func (f *p2Fixture) submit(t *testing.T, sessionID, tok, name, typ string, data []byte) (int, map[string]any) {
	t.Helper()
	// Submission order is the list order (E14-06), and with a frozen fake
	// clock two submits share a created_at and the order is arbitrary.
	f.fake.Advance(time.Second)
	ct, body := multipartBody(t, name, typ, "", data)
	return f.raw(t, "POST", f.p+"/sessions/"+sessionID+"/artifacts", tok, ct, body)
}

func (f *p2Fixture) raw(t *testing.T, method, path, tok, contentType string, body []byte) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, f.api.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if f.api.cookie != "" {
		req.Header.Set("Cookie", f.api.cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return res.StatusCode, out
}

// TestArtifactVersioningAndLimits is FR-4.3: the same name never overwrites,
// and the 50 MB ceiling is a 413 and not a truncated file.
func TestArtifactVersioningAndLimits(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")

	st, out := f.submit(t, sess, tok, "report.md", "doc", []byte("v1 본문"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	first := out["artifact"].(map[string]any)
	if v := int(first["version"].(float64)); v != 1 {
		t.Fatalf("first version = %d, want 1", v)
	}

	st, out = f.submit(t, sess, tok, "report.md", "doc", []byte("v2 본문은 더 길다"))
	if st != 201 {
		t.Fatalf("resubmit = %d: %v", st, out)
	}
	second := out["artifact"].(map[string]any)
	if v := int(second["version"].(float64)); v != 2 {
		t.Fatalf("resubmitting the same name must be v2, got v%d (FR-4.3)", v)
	}
	if second["id"] == first["id"] {
		t.Fatal("v2 must be its own row — v1 is still citable from another lane (FR-6.1)")
	}
	// `latest` is computed at read time and the 201 body is re-read after the
	// insert, so v2 is the latest and v1 is not — S7 renders exactly this.
	if l, _ := second["latest"].(bool); !l {
		t.Fatal("the newest version must come back latest: true")
	}

	// A different name starts its own version line.
	st, out = f.submit(t, sess, tok, "notes.md", "doc", []byte("x"))
	if st != 201 || int(out["artifact"].(map[string]any)["version"].(float64)) != 1 {
		t.Fatalf("a new name starts at v1: %d %v", st, out)
	}

	// listArtifacts: three rows in submission order, two of them latest.
	stl, list := f.rawList(t, f.p+"/sessions/"+sess+"/artifacts", tok)
	if stl != 200 {
		t.Fatalf("list = %d", stl)
	}
	if len(list) != 3 {
		t.Fatalf("list = %d rows, want 3", len(list))
	}
	if list[0]["name"] != "report.md" || int(list[0]["version"].(float64)) != 1 {
		t.Fatalf("list is submission order (E14-06 re-applies in it): %v", list[0])
	}
	stl, latestOnly := f.rawList(t, f.p+"/sessions/"+sess+"/artifacts?latest_only=true", tok)
	if stl != 200 || len(latestOnly) != 2 {
		t.Fatalf("latest_only = %d rows, want 2 (report v2 + notes v1)", len(latestOnly))
	}
	for _, a := range latestOnly {
		if a["name"] == "report.md" && int(a["version"].(float64)) != 2 {
			t.Fatalf("latest_only returned report.md v%v", a["version"])
		}
	}
	stl, typed := f.rawList(t, f.p+"/sessions/"+sess+"/artifacts?type=diff", tok)
	if stl != 200 || len(typed) != 0 {
		t.Fatalf("type filter = %d rows, want 0", len(typed))
	}

	// 413: one byte past the ceiling. The contract's number, not a guess.
	big := bytes.Repeat([]byte("a"), artifacts.MaxBytes+1)
	st, out = f.submit(t, sess, tok, "huge.bin", "file", big)
	if st != 413 {
		t.Fatalf("oversized submit = %d, want 413 (openapi submitArtifact)", st)
	}
	if str(out, "code") != "payload_too_large" {
		t.Fatalf("413 code = %q, want payload_too_large", str(out, "code"))
	}
	// …and nothing was stored.
	stl, list = f.rawList(t, f.p+"/sessions/"+sess+"/artifacts", tok)
	if stl != 200 || len(list) != 3 {
		t.Fatalf("a rejected upload must store nothing: %d rows", len(list))
	}

	// A body with no `file` part is a validation error, not a 500.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "x")
	_ = mw.WriteField("type", "doc")
	_ = mw.Close()
	st, _ = f.raw(t, "POST", f.p+"/sessions/"+sess+"/artifacts", tok, mw.FormDataContentType(), buf.Bytes())
	if st != 422 {
		t.Fatalf("missing file part = %d, want 422", st)
	}
}

// TestArtifactDownloadDeclaresLength is the CLI's truncation guard: without a
// Content-Length the client can only trust chunked termination, and a half
// file then looks complete (cli/README.md, actions_p2.go ArtifactGet).
func TestArtifactDownloadDeclaresLength(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")

	// Big enough that io.Copy runs several windows, so a length that came from
	// counting rather than from the row would show up.
	payload := bytes.Repeat([]byte("본문 조각 "), 40000)
	st, out := f.submit(t, sess, tok, "big.txt", "file", payload)
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	id := str(out["artifact"].(map[string]any), "id")
	if n := int64(out["artifact"].(map[string]any)["size_bytes"].(float64)); n != int64(len(payload)) {
		t.Fatalf("size_bytes = %d, want %d", n, len(payload))
	}

	req, _ := http.NewRequest("GET", f.api.srv.URL+f.p+"/artifacts/"+id+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("download = %d", res.StatusCode)
	}
	declared := res.Header.Get("Content-Length")
	if declared == "" {
		t.Fatal("downloadArtifact must declare Content-Length — the CLI checks the bytes it wrote against it")
	}
	want, err := strconv.ParseInt(declared, 10, 64)
	if err != nil {
		t.Fatalf("Content-Length %q: %v", declared, err)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) != want {
		t.Fatalf("wrote %d bytes, declared %d", len(got), want)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("the downloaded body is not the one submitted")
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment (openapi downloadArtifact)", cd)
	}
}

// TestArtifactBoundaries is D1/D2 for the artifact operations: a TaskToken may
// not reach another session, and a member of another workspace gets 404 rather
// than a hint that the id exists.
func TestArtifactBoundaries(t *testing.T) {
	f := newP2Fixture(t)
	sessA := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	sessB := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tokA, _ := f.agentToken(t, sessA, f.leadUUID, "Lead")
	tokB, _ := f.agentToken(t, sessB, f.leadUUID, "Lead")

	st, out := f.submit(t, sessA, tokA, "a.txt", "file", []byte("a"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	id := str(out["artifact"].(map[string]any), "id")

	// The other session's token cannot submit into A…
	st, _ = f.submit(t, sessA, tokB, "sneak.txt", "file", []byte("x"))
	if st != 403 {
		t.Fatalf("cross-session submit = %d, want 403 outside_task_scope (G2 Q8)", st)
	}
	// …nor read A's artifact.
	stg, outg := f.rawGet(t, f.p+"/artifacts/"+id, tokB)
	if stg != 403 {
		t.Fatalf("cross-session getArtifact = %d, want 403: %v", stg, outg)
	}
	stg, _ = f.rawGet(t, f.p+"/artifacts/"+id+"/content", tokB)
	if stg != 403 {
		t.Fatalf("cross-session download = %d, want 403", stg)
	}

	// A member of another workspace gets 404 — not 403, which would confirm
	// the id (FR-6.1: cross-lane reads go through artifacts and nothing else).
	other := &client{t: t, srv: f.api.srv}
	_, _, hdr := other.do("POST", f.p+"/auth/signup", map[string]any{
		"display_name": "Out", "email": "out@example.com", "password": "password123"})
	other.cookie = hdr.Get("Set-Cookie")
	other.must(201, "POST", f.p+"/workspaces", map[string]any{"name": "Other"})
	if st, _, _ := other.do("GET", f.p+"/artifacts/"+id, nil); st != 404 {
		t.Fatalf("outsider getArtifact = %d, want 404", st)
	}
	if st, _, _ := other.do("GET", f.p+"/sessions/"+sessA+"/artifacts", nil); st != 404 {
		t.Fatalf("outsider listArtifacts = %d, want 404", st)
	}

	// An unknown id is 404 for an insider too.
	if stg, _ := f.rawGet(t, f.p+"/artifacts/"+uuid.NewString(), tokA); stg != 404 {
		t.Fatalf("unknown artifact = %d, want 404", stg)
	}
}

// TestReviewE6_05_06_RealPath is the DoD row: the golden table's E6-05 and
// E6-06 reached through the production handler instead of the hook.
//
//	E6-05  the designated reviewer approves → the session completes with no
//	       human gate, because `agent_approval` stands alone.
//	E6-06  anybody else gets 403 not_designated_reviewer and NOTHING is stored.
func TestReviewE6_05_06_RealPath(t *testing.T) {
	f := newP2Fixture(t)
	// Scenario B's tree: agent_approval alone, designating W (the QA role).
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "agent_approval", "agent_id": f.w},
	}})
	leadTok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	qaTok, _ := f.agentToken(t, sess, f.wUUID, "W")
	rTok, _ := f.agentToken(t, sess, f.rUUID, "R")

	st, out := f.submit(t, sess, leadTok, "draft.md", "doc", []byte("초안"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	id := str(out["artifact"].(map[string]any), "id")

	// E6-06 first: R is not the designated reviewer.
	st, out = f.rawPost(t, f.p+"/artifacts/"+id+"/review", rTok, map[string]any{"verdict": "approve"})
	if st != 403 {
		t.Fatalf("non-designated approve = %d, want 403 (E6-06)", st)
	}
	if code := str(out, "code"); code != "not_designated_reviewer" {
		t.Fatalf("403 code = %q, want not_designated_reviewer — colab-cli.md §2.3 maps that exact string to exit 3", code)
	}
	var reviews int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM artifact_review WHERE artifact_id = $1`, mustUUID(t, id)).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	if reviews != 0 {
		t.Fatalf("a rejected reviewer stored %d rows — E6-06 says nothing is stored", reviews)
	}
	if got := f.sessionStatus(t, sess); got != "active" {
		t.Fatalf("session = %q after a non-designated approve, want active", got)
	}

	// E6-05: the designated reviewer approves and the session is over. No
	// user_approval HITL is invented — the tree has no such atom.
	st, out = f.rawPost(t, f.p+"/artifacts/"+id+"/review", qaTok, map[string]any{"verdict": "approve", "comments": "좋습니다"})
	if st != 200 {
		t.Fatalf("designated approve = %d, want 200: %v", st, out)
	}
	prog := out["completion_progress"].(map[string]any)
	if sat, _ := prog["satisfied"].(bool); !sat {
		t.Fatalf("completion_progress.satisfied = false after the only atom was met: %v", prog)
	}
	if hg, ok := prog["human_gate"].(bool); !ok || hg {
		t.Fatalf("human_gate = %v, want false — agent_approval alone completes without a person (E6-05)", prog["human_gate"])
	}
	if got := f.sessionStatus(t, sess); got != "completed" {
		t.Fatalf("session = %q, want completed (E6-05)", got)
	}
	var hitls int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM hitl_request WHERE session_id = $1`, mustUUID(t, sess)).Scan(&hitls); err != nil {
		t.Fatal(err)
	}
	if hitls != 0 {
		t.Fatalf("%d user_approval HITLs issued — the tree has no such atom (E6-05)", hitls)
	}
	rev := out["review"].(map[string]any)
	if str(rev, "verdict") != "approve" || str(rev, "reviewer_agent_id") != f.w {
		t.Fatalf("stored review = %v", rev)
	}
}

// TestReviewRejectPostsReasonAndDecision is the other half of reviewArtifact:
// a rejection ends nothing, records WHY as `source: agent`, and puts the reason
// back on the submitting lane's thread so it re-enters (E16-B step 5).
func TestReviewRejectPostsReasonAndDecision(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "agent_approval", "agent_id": f.w},
	}})
	leadTok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	qaTok, _ := f.agentToken(t, sess, f.wUUID, "W")

	st, out := f.submit(t, sess, leadTok, "draft.md", "doc", []byte("초안"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	id := str(out["artifact"].(map[string]any), "id")

	// reject without a reason is 422: the reason IS the payload.
	if st, _ := f.rawPost(t, f.p+"/artifacts/"+id+"/review", qaTok, map[string]any{"verdict": "reject"}); st != 422 {
		t.Fatalf("reject without comments = %d, want 422", st)
	}

	st, out = f.rawPost(t, f.p+"/artifacts/"+id+"/review", qaTok,
		map[string]any{"verdict": "reject", "comments": "근거가 없습니다"})
	if st != 200 {
		t.Fatalf("reject = %d: %v", st, out)
	}
	if got := f.sessionStatus(t, sess); got != "active" {
		t.Fatalf("session = %q after a rejection, want active — a reject completes nothing", got)
	}
	msg, ok := out["message"].(map[string]any)
	if !ok {
		t.Fatal("reject must return the reply it posted (openapi reviewArtifact `message`)")
	}
	if !strings.Contains(str(msg, "content"), "근거가 없습니다") {
		t.Fatalf("posted reply = %q, want the reason", str(msg, "content"))
	}
	if str(msg, "parent_id") == "" {
		t.Fatal("the reason goes on the submitting lane's thread as a reply, not as a new root")
	}

	var summary, source string
	var refID *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT summary, source::text, ref_id FROM decision WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`,
		mustUUID(t, sess)).Scan(&summary, &source, &refID); err != nil {
		t.Fatalf("no decision recorded for the rejection: %v", err)
	}
	if source != "agent" {
		t.Fatalf("decision source = %q, want agent — a reviewer agent decided, not a person", source)
	}
	if refID == nil || *refID != mustUUID(t, id) {
		t.Fatalf("decision ref_id = %v, want the artifact %s", refID, id)
	}

	// The review row carries the verdict and points at that decision.
	var verdict string
	var decisionID *uuid.UUID
	if err := f.pool.QueryRow(t.Context(),
		`SELECT verdict::text, decision_id FROM artifact_review WHERE artifact_id = $1`, mustUUID(t, id)).
		Scan(&verdict, &decisionID); err != nil {
		t.Fatal(err)
	}
	if verdict != "reject" || decisionID == nil {
		t.Fatalf("artifact_review = (%q, %v), want (reject, the decision id)", verdict, decisionID)
	}

	// The artifact is still there at its version: a rejection is not a delete.
	if stg, _ := f.rawGet(t, f.p+"/artifacts/"+id, qaTok); stg != 200 {
		t.Fatalf("rejected artifact = %d, want 200 — it is still citable", stg)
	}
}

// TestSubmitByNonDesignatedAgentStoresButDoesNotSatisfy is E6-02, the
// asymmetry that makes `artifact_submitted` different from a file upload: the
// bytes are kept, the condition is not met.
func TestSubmitByNonDesignatedAgentStoresButDoesNotSatisfy(t *testing.T) {
	f := newP2Fixture(t)
	// The tree names Lead (the assignee) as the submitter.
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	rTok, _ := f.agentToken(t, sess, f.rUUID, "R")

	st, out := f.submit(t, sess, rTok, "side.md", "doc", []byte("옆에서 냅니다"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	prog := out["completion_progress"].(map[string]any)
	if met := int(prog["met"].(float64)); met != 0 {
		t.Fatalf("met = %d, want 0 — R is not the designated submitter (E6-02)", met)
	}
	var stored int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM artifact WHERE session_id = $1`, mustUUID(t, sess)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("stored %d artifacts, want 1 — the bytes are kept either way (E6-02)", stored)
	}

	// The designated one submits: now the atom is met, and because the only
	// thing left is user_approval the platform issues that HITL (E6-01).
	leadTok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	st, out = f.submit(t, sess, leadTok, "main.md", "doc", []byte("본편"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	prog = out["completion_progress"].(map[string]any)
	if met := int(prog["met"].(float64)); met != 1 {
		t.Fatalf("met = %d, want 1 (artifact_submitted)", met)
	}
	var hitls int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM hitl_request WHERE session_id = $1 AND source = 'system'`, mustUUID(t, sess)).Scan(&hitls); err != nil {
		t.Fatal(err)
	}
	if hitls != 1 {
		t.Fatalf("system HITLs = %d, want 1 — user_approval is issued BY THE PLATFORM (E6-01)", hitls)
	}
}

// TestSubmitIdempotency is openapi's IdempotencyKeyOptional on submitArtifact:
// a retried upload must not become v2.
func TestSubmitIdempotency(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	key := uuid.NewString()
	// Encoded TWICE, as the CLI does on a retry: multipart.Writer picks a fresh
	// random boundary each time, so the raw bytes differ while the request does
	// not. Hashing the bytes would turn a legitimate retry into 422.
	ct1, body1 := multipartBody(t, "once.md", "doc", "", []byte("한 번"))
	ct2, body2 := multipartBody(t, "once.md", "doc", "", []byte("한 번"))
	if bytes.Equal(body1, body2) {
		t.Fatal("premise: two encodings of the same upload must differ (random boundary)")
	}

	st1, out1 := f.rawKeyed(t, f.p+"/sessions/"+sess+"/artifacts", tok, ct1, body1, key)
	st2, out2 := f.rawKeyed(t, f.p+"/sessions/"+sess+"/artifacts", tok, ct2, body2, key)
	if st1 != 201 || st2 != 201 {
		t.Fatalf("submits = %d, %d", st1, st2)
	}
	if str(out1["artifact"].(map[string]any), "id") != str(out2["artifact"].(map[string]any), "id") {
		t.Fatal("a replayed Idempotency-Key must return the same artifact, not a v2")
	}
	var n int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM artifact WHERE session_id = $1`, mustUUID(t, sess)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("stored %d artifacts for one key, want 1", n)
	}

	// The same key with a DIFFERENT upload is still a 422: the guard survives
	// hashing the parts instead of the bytes.
	ct3, body3 := multipartBody(t, "once.md", "doc", "", []byte("다른 본문"))
	if st, _ := f.rawKeyed(t, f.p+"/sessions/"+sess+"/artifacts", tok, ct3, body3, key); st != 422 {
		t.Fatalf("same key, different upload = %d, want 422 idempotency_key_reused", st)
	}
}

// TestReviewIsAgentOnly: reviewArtifact's only security scheme is TaskToken.
func TestReviewIsAgentOnly(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "agent_approval", "agent_id": f.w},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	st, out := f.submit(t, sess, tok, "d.md", "doc", []byte("d"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	id := str(out["artifact"].(map[string]any), "id")
	if st, _, _ := f.api.do("POST", f.p+"/artifacts/"+id+"/review", map[string]any{"verdict": "approve"}); st != 403 {
		t.Fatalf("a person calling reviewArtifact = %d, want 403 agent_only", st)
	}
}

// helpers ------------------------------------------------------------------

func (f *p2Fixture) sessionStatus(t *testing.T, sessionID string) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM session WHERE id = $1`, mustUUID(t, sessionID)).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *p2Fixture) rawGet(t *testing.T, path, tok string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.api.srv.URL+path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (f *p2Fixture) rawList(t *testing.T, path, tok string) (int, []map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.api.srv.URL+path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (f *p2Fixture) rawPost(t *testing.T, path, tok string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", f.api.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (f *p2Fixture) rawKeyed(t *testing.T, path, tok, contentType string, body []byte, key string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.api.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// TestArtifactBlobIsUnlinkedWithTheRow is review R1: the bytes live in a large
// object, and a large object is NOT owned by the row that points at it. Nothing
// in the server calls lo_unlink, and `artifact` cascades off `session`, so a
// deleted session would leave up to 50 MB per version in pg_largeobject
// forever. Migration 0008's AFTER DELETE trigger is what closes that; drop the
// trigger and both halves of this test go red.
func TestArtifactBlobIsUnlinkedWithTheRow(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")

	blobOf := func(artifactID uuid.UUID) uint32 {
		t.Helper()
		var ref string
		if err := f.pool.QueryRow(ctx, `SELECT storage_ref FROM artifact WHERE id = $1`, artifactID).Scan(&ref); err != nil {
			t.Fatal(err)
		}
		var oid uint32
		if _, err := fmt.Sscanf(ref, "pglo:%d", &oid); err != nil {
			t.Fatalf("storage_ref %q is not a large object ref: %v", ref, err)
		}
		return oid
	}
	blobCount := func(oid uint32) int {
		t.Helper()
		var n int
		if err := f.pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_largeobject_metadata WHERE oid = $1`, oid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// --- the row is deleted directly ---
	st, out := f.submit(t, sess, tok, "direct.txt", "file", []byte("직접 삭제되는 본문"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	direct := mustUUID(t, str(out["artifact"].(map[string]any), "id"))
	oid := blobOf(direct)
	if n := blobCount(oid); n != 1 {
		t.Fatalf("blob %d exists = %d, want 1 — the premise is that the bytes are really there", oid, n)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM artifact WHERE id = $1`, direct); err != nil {
		t.Fatal(err)
	}
	if n := blobCount(oid); n != 0 {
		t.Fatalf("blob %d survived its row (count %d) — nothing else will ever unlink it", oid, n)
	}

	// --- the session is deleted and the row goes with it (ON DELETE CASCADE) ---
	// This is the path that actually leaks: a cascade never runs application
	// code, so a Go-side Remove() would not have covered it.
	st, out = f.submit(t, sess, tok, "cascade.txt", "file", []byte("세션과 함께 사라질 본문"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	cascaded := mustUUID(t, str(out["artifact"].(map[string]any), "id"))
	oid = blobOf(cascaded)
	if n := blobCount(oid); n != 1 {
		t.Fatalf("blob %d exists = %d, want 1", oid, n)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM session WHERE id = $1`, mustUUID(t, sess)); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE id = $1`, cascaded).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("the artifact row survived the session (count %d) — premise broken", rows)
	}
	if n := blobCount(oid); n != 0 {
		t.Fatalf("blob %d survived the session cascade (count %d) — FR-6.4 GC would leak 50 MB per version", oid, n)
	}
}

// TestArtifactRowDeletesWhenTheBlobIsAlreadyGone is review NN1. Migration
// 0008's trigger swallows `undefined_object` so that a missing blob cannot
// block the delete, and until now that promise rested only on the SQLSTATE
// name being the right one. If it is wrong — or a future Postgres raises
// something else for lo_unlink — every session delete that touches an
// already-cleaned artifact starts failing, and the failure surfaces as a
// cascade nobody can complete. This pins the behaviour instead of the code.
func TestArtifactRowDeletesWhenTheBlobIsAlreadyGone(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")

	st, out := f.submit(t, sess, tok, "orphaned.txt", "file", []byte("blob 이 먼저 사라진다"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	id := mustUUID(t, str(out["artifact"].(map[string]any), "id"))

	var ref string
	if err := f.pool.QueryRow(ctx, `SELECT storage_ref FROM artifact WHERE id = $1`, id).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	var oid uint32
	if _, err := fmt.Sscanf(ref, "pglo:%d", &oid); err != nil {
		t.Fatalf("storage_ref %q is not a large object ref: %v", ref, err)
	}

	// The blob goes first — the state a half-finished manual cleanup, a restored
	// dump, or a second GC pass leaves behind.
	if _, err := f.pool.Exec(ctx, `SELECT lo_unlink($1)`, oid); err != nil {
		t.Fatalf("premise: unlinking the blob directly: %v", err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_largeobject_metadata WHERE oid = $1`, oid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("premise: blob %d still exists (count %d)", oid, n)
	}

	// …and the row must still delete. A trigger that re-raises here would make
	// the row undeletable and take the whole session cascade down with it.
	if _, err := f.pool.Exec(ctx, `DELETE FROM artifact WHERE id = $1`, id); err != nil {
		t.Fatalf("deleting a row whose blob is already gone must succeed — 0008's trigger "+
			"has to swallow undefined_object, not re-raise it: %v", err)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE id = $1`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("the row survived its own DELETE (count %d)", rows)
	}
}
