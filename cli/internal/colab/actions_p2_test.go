package colab_test

// Contract tests for the P2 commands (contracts/colab-cli.md v0.4 §2.2·2.3):
// each one pins the request the CLI builds and how it reads the response,
// against the openapi shapes served by clienttest. The server's own handlers
// are still 501 (T-S2) — the contract is the reference, not the server.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	return client.ExitCode(err)
}

// ───────────────────────────── lane delegate ─────────────────────────────

// colab-cli.md §2.3: always a new lane; the CLI resolves --agent (a name) to
// the roster's agent_id and the server posts the mention message.
func TestLaneDelegate(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.LaneDelegate(context.Background(), newClient(t, s), colab.LaneDelegateArgs{
		Agent: "@Reviewer", Brief: "check the numbers",
		DependsOn: []string{"lane-a,lane-b", " lane-c "}, Profile: "careful",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Delegations) != 1 {
		t.Fatalf("delegations = %d", len(s.Delegations))
	}
	body := s.Delegations[0].Body
	if body["agent_id"] != clienttest.ReviewerID {
		t.Fatalf("agent_id = %v, want the roster id for @Reviewer", body["agent_id"])
	}
	if body["brief"] != "check the numbers" || body["profile"] != "careful" {
		t.Fatalf("body = %v", body)
	}
	dep, _ := body["depends_on"].([]any)
	if len(dep) != 3 || dep[0] != "lane-a" || dep[2] != "lane-c" {
		t.Fatalf("depends_on = %v (repeatable + comma-separated, trimmed)", dep)
	}
	// The Idempotency-Key is optional and must be absent unless asked for.
	if s.Delegations[0].Key != "" {
		t.Fatalf("Idempotency-Key = %q, want none by default", s.Delegations[0].Key)
	}
	if res.LaneID == "" || res.AgentID != clienttest.ReviewerID || res.AgentName != "Reviewer" {
		t.Fatalf("res = %+v", res)
	}
	if res.MessageID == "" || res.Message == nil ||
		!strings.Contains(res.Message.Content, clienttest.ReviewerID) {
		t.Fatalf("auto-posted message = %+v", res.Message)
	}
}

// E15-02: delegating to a non-participant is refused by the CLI itself
// (exit 3) and names the alternative route, and no request is sent.
func TestLaneDelegateNonParticipantExit3(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.LaneDelegate(context.Background(), newClient(t, s), colab.LaneDelegateArgs{
		Agent: "Nobody", Brief: "do a thing"})
	if got := exitOf(t, err); got != client.ExitRefused {
		t.Fatalf("exit = %d, want 3", got)
	}
	e := client.AsError(err)
	if e.Code != "not_participant" {
		t.Fatalf("code = %q", e.Code)
	}
	if !strings.Contains(e.Detail, "hitl ask") || !strings.Contains(e.Detail, "Director") {
		t.Fatalf("detail must point at `colab hitl ask` to the Director, got %q", e.Detail)
	}
	if !strings.Contains(e.Detail, "Reviewer") {
		t.Fatalf("detail should list the roster, got %q", e.Detail)
	}
	if len(s.Delegations) != 0 {
		t.Fatalf("nothing may be sent for a non-participant, got %d", len(s.Delegations))
	}
}

func TestLaneDelegateArgErrors(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	for name, a := range map[string]colab.LaneDelegateArgs{
		"no agent": {Brief: "x"},
		"no brief": {Agent: "Reviewer"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := colab.LaneDelegate(context.Background(), c, a)
			if got := exitOf(t, err); got != client.ExitUsage {
				t.Fatalf("exit = %d, want 2", got)
			}
		})
	}
}

func TestLaneDelegateSendsIdempotencyKeyWhenGiven(t *testing.T) {
	s := clienttest.New(t)
	key := "00000000-0000-4000-8000-00000000abcd"
	if _, err := colab.LaneDelegate(context.Background(), newClient(t, s), colab.LaneDelegateArgs{
		Agent: "Reviewer", Brief: "b", IdempotencyKey: key}); err != nil {
		t.Fatal(err)
	}
	if s.Delegations[0].Key != key {
		t.Fatalf("Idempotency-Key = %q, want %q", s.Delegations[0].Key, key)
	}
}

// ───────────────────────────── status set ─────────────────────────────

// E3-05: `blocked` posts the question card and the reply's
// turn_end_required is surfaced verbatim — the agent ends its turn on it.
func TestStatusSetBlockedSurfacesTurnEndRequired(t *testing.T) {
	s := clienttest.New(t)
	s.BlockedQuestionID = "aaaa1111-0000-4000-8000-000000000001"
	res, err := colab.StatusSet(context.Background(), newClient(t, s), colab.StatusSetArgs{
		Status: "blocked", Note: "what is the scope?"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TurnEndRequired {
		t.Fatal("turn_end_required must be true for blocked (FR-6.2.1)")
	}
	if res.QuestionMessageID == nil || *res.QuestionMessageID != s.BlockedQuestionID {
		t.Fatalf("question_message_id = %v", res.QuestionMessageID)
	}
	if len(s.StatusCalls) != 1 || s.StatusCalls[0].TaskID != clienttest.TaskID {
		t.Fatalf("status calls = %+v", s.StatusCalls)
	}
	if b := s.StatusCalls[0].Body; b["status"] != "blocked" || b["note"] != "what is the scope?" {
		t.Fatalf("body = %v", b)
	}
	// The JSON the agent parses carries the contract's one name for the flag
	// and no `end_turn` alias (colab-cli.md v0.4 §2.4).
	raw := string(colab.MarshalIndent(res))
	if !strings.Contains(raw, `"turn_end_required": true`) {
		t.Fatalf("json = %s", raw)
	}
	if strings.Contains(raw, `"end_turn"`) {
		t.Fatalf("the flag must have exactly one name; json = %s", raw)
	}
}

// The CLI reports the server's flag rather than deciding it locally.
func TestStatusSetReportsServerFlagNotItsOwnGuess(t *testing.T) {
	s := clienttest.New(t)
	s.TurnEndOnWorking = true // a server that asks for a turn end on `working`
	res, err := colab.StatusSet(context.Background(), newClient(t, s), colab.StatusSetArgs{Status: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TurnEndRequired {
		t.Fatal("turn_end_required must mirror the server, not the status word")
	}
	if res.QuestionMessageID != nil {
		t.Fatalf("question_message_id = %v, want null for working", res.QuestionMessageID)
	}
}

func TestStatusSetDone(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.StatusSet(context.Background(), newClient(t, s), colab.StatusSetArgs{
		Status: "done", Note: "table posted"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TurnEndRequired || res.Status != "done" {
		t.Fatalf("res = %+v", res)
	}
	var lane map[string]any
	if err := json.Unmarshal(res.Lane, &lane); err != nil || lane["status"] != "done" {
		t.Fatalf("lane = %s (%v)", res.Lane, err)
	}
}

// `blocked` without a note is an argument error, caught before the request.
func TestStatusSetBlockedNeedsNote(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.StatusSet(context.Background(), newClient(t, s), colab.StatusSetArgs{Status: "blocked"})
	if got := exitOf(t, err); got != client.ExitUsage {
		t.Fatalf("exit = %d, want 2", got)
	}
	if len(s.StatusCalls) != 0 {
		t.Fatal("no request may be sent")
	}
}

func TestStatusSetUnknownStatus(t *testing.T) {
	s := clienttest.New(t)
	for _, status := range []string{"", "finished", "Blocked "} {
		_, err := colab.StatusSet(context.Background(), newClient(t, s), colab.StatusSetArgs{Status: status, Note: "n"})
		if status == "Blocked " { // trimmed + lowercased, so this one is valid
			if err != nil {
				t.Fatalf("%q: %v", status, err)
			}
			continue
		}
		if got := exitOf(t, err); got != client.ExitUsage {
			t.Fatalf("%q: exit = %d, want 2", status, got)
		}
	}
}

// ───────────────────────────── decision record ─────────────────────────────

// colab-cli.md v0.4 §2.2: the record is summary + rationale and nothing else.
func TestDecisionRecord(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.DecisionRecord(context.Background(), newClient(t, s), colab.DecisionRecordArgs{
		Summary: "use Postgres", Rationale: "already operated in-house"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Decisions) != 1 {
		t.Fatalf("decisions = %d", len(s.Decisions))
	}
	body := s.Decisions[0]
	if body["summary"] != "use Postgres" || body["rationale"] != "already operated in-house" {
		t.Fatalf("body = %v", body)
	}
	if len(body) != 2 {
		t.Fatalf("body must be exactly {summary, rationale}, got %v", body)
	}
	if res.DecisionID == "" {
		t.Fatalf("res = %+v", res)
	}
	var d map[string]any
	if err := json.Unmarshal(res.Decision, &d); err != nil || d["source"] != "agent" || d["ref_id"] != clienttest.TaskID {
		t.Fatalf("decision = %s (%v)", res.Decision, err)
	}
}

func TestDecisionRecordNeedsSummary(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.DecisionRecord(context.Background(), newClient(t, s), colab.DecisionRecordArgs{Rationale: "why"})
	if got := exitOf(t, err); got != client.ExitUsage {
		t.Fatalf("exit = %d, want 2", got)
	}
	if len(s.Decisions) != 0 {
		t.Fatal("no request may be sent")
	}
}

// ───────────────────────────── artifact submit ─────────────────────────────

// colab-cli.md v0.4 §2.3: multipart {name, type, file, description}; --name
// defaults to the file's base name.
func TestArtifactSubmit(t *testing.T) {
	s := clienttest.New(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte("# hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := colab.ArtifactSubmit(context.Background(), newClient(t, s), colab.ArtifactSubmitArgs{
		Type: "report", File: path, Description: "first cut"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Submissions) != 1 {
		t.Fatalf("submissions = %d", len(s.Submissions))
	}
	sub := s.Submissions[0]
	if sub.Fields["name"] != "report.md" {
		t.Fatalf("name = %q, want the file's base name", sub.Fields["name"])
	}
	if sub.Fields["type"] != "report" || sub.Fields["description"] != "first cut" {
		t.Fatalf("fields = %v", sub.Fields)
	}
	if string(sub.Data) != "# hi\n" || sub.FileName != "report.md" {
		t.Fatalf("file part = %q / %q", sub.Data, sub.FileName)
	}
	if sub.ContentType != "text/plain" {
		t.Fatalf("file part Content-Type = %q (openapi allows octet-stream · text/plain · json)", sub.ContentType)
	}
	if sub.Key != "" {
		t.Fatalf("Idempotency-Key = %q, want none by default", sub.Key)
	}
	if res.ArtifactID != clienttest.ArtifactID || res.Name != "report.md" || res.SizeBytes != 5 {
		t.Fatalf("res = %+v", res)
	}
	var prog map[string]any
	if err := json.Unmarshal(res.CompletionProgress, &prog); err != nil || prog["total"] != float64(2) {
		t.Fatalf("completion_progress = %s (%v)", res.CompletionProgress, err)
	}
}

// An explicit --name wins, and re-submitting it is version+1 (FR-4.3).
func TestArtifactSubmitNamedVersioning(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	path := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := colab.ArtifactSubmitArgs{Name: "findings", Type: "doc", File: path}
	for want := 1; want <= 2; want++ {
		res, err := colab.ArtifactSubmit(context.Background(), c, a)
		if err != nil {
			t.Fatal(err)
		}
		var art map[string]any
		if err := json.Unmarshal(res.Artifact, &art); err != nil {
			t.Fatal(err)
		}
		if art["version"] != float64(want) {
			t.Fatalf("version = %v, want %d", art["version"], want)
		}
	}
	if ct := s.Submissions[0].ContentType; ct != "application/json" {
		t.Fatalf(".json part Content-Type = %q", ct)
	}
}

func TestArtifactSubmitArgErrors(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]colab.ArtifactSubmitArgs{
		"no type":      {File: file},
		"no file":      {Type: "doc"},
		"missing file": {Type: "doc", File: filepath.Join(dir, "nope.txt")},
		"a directory":  {Type: "doc", File: dir},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := colab.ArtifactSubmit(context.Background(), c, a); exitOf(t, err) != client.ExitUsage {
				t.Fatalf("exit = %d, want 2", client.ExitCode(err))
			}
		})
	}
	if len(s.Submissions) != 0 {
		t.Fatal("no request may be sent for an argument error")
	}
}

// ───────────────────────────── artifact get ─────────────────────────────

// colab-cli.md §2.1: metadata alone, and the body only when --out is given.
func TestArtifactGetMetaOnly(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID})
	if err != nil {
		t.Fatal(err)
	}
	if res.ArtifactID != clienttest.ArtifactID || res.SavedTo != "" || res.SizeBytes != nil {
		t.Fatalf("res = %+v", res)
	}
	var art map[string]any
	if err := json.Unmarshal(res.Artifact, &art); err != nil || art["name"] != clienttest.ArtifactName {
		t.Fatalf("artifact = %s (%v)", res.Artifact, err)
	}
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/content") {
			t.Fatal("the body must not be downloaded without --out")
		}
	}
}

func TestArtifactGetOutFile(t *testing.T) {
	s := clienttest.New(t)
	dest := filepath.Join(t.TempDir(), "saved.md")
	res, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dest})
	if err != nil {
		t.Fatal(err)
	}
	if res.SavedTo != dest || res.SizeBytes == nil || *res.SizeBytes != len(clienttest.ArtifactBody) {
		t.Fatalf("res = %+v", res)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != clienttest.ArtifactBody {
		t.Fatalf("saved = %q (%v)", got, err)
	}
}

// --out may be a directory: the Content-Disposition filename is used.
func TestArtifactGetOutDirectory(t *testing.T) {
	s := clienttest.New(t)
	dir := t.TempDir()
	res, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dir})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, clienttest.ArtifactName)
	if res.SavedTo != want {
		t.Fatalf("saved_to = %q, want %q", res.SavedTo, want)
	}
	if got, err := os.ReadFile(want); err != nil || string(got) != clienttest.ArtifactBody {
		t.Fatalf("saved = %q (%v)", got, err)
	}
}

func TestArtifactGetNeedsID(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.ArtifactGet(context.Background(), newClient(t, s), colab.ArtifactGetArgs{})
	if got := exitOf(t, err); got != client.ExitUsage {
		t.Fatalf("exit = %d, want 2", got)
	}
}

// A cross-session artifact is the server's 404 → exit 3.
func TestArtifactGetUnknownIsRefused(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: "99999999-9999-4999-8999-999999999999"})
	if got := exitOf(t, err); got != client.ExitRefused {
		t.Fatalf("exit = %d, want 3", got)
	}
}

// ───────────────────────────── review ─────────────────────────────

func TestReviewApprove(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.ReviewApprove(context.Background(), newClient(t, s), colab.ReviewArgs{
		Artifact: clienttest.ArtifactID, Note: "numbers check out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Reviews) != 1 || s.Reviews[0].ArtifactID != clienttest.ArtifactID {
		t.Fatalf("reviews = %+v", s.Reviews)
	}
	// --note and --reason both land in `comments` (colab-cli.md v0.4 §2.3).
	if b := s.Reviews[0].Body; b["verdict"] != "approve" || b["comments"] != "numbers check out" {
		t.Fatalf("body = %v", b)
	}
	if res.Verdict != "approve" || res.Message != nil {
		t.Fatalf("res = %+v", res)
	}
	var prog map[string]any
	if err := json.Unmarshal(res.CompletionProgress, &prog); err != nil || prog["satisfied"] != true {
		t.Fatalf("completion_progress = %s (%v)", res.CompletionProgress, err)
	}
}

func TestReviewReject(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.ReviewReject(context.Background(), newClient(t, s), colab.ReviewArgs{
		Artifact: clienttest.ArtifactID, Reason: "source 2 is stale"})
	if err != nil {
		t.Fatal(err)
	}
	if b := s.Reviews[0].Body; b["verdict"] != "reject" || b["comments"] != "source 2 is stale" {
		t.Fatalf("body = %v", b)
	}
	if res.Message == nil || res.Message.Content != "source 2 is stale" {
		t.Fatalf("reject must surface the posted reply, got %+v", res.Message)
	}
}

// E6-06: an agent the completion condition did not designate gets exit 3
// with the CLI-side code `not_reviewer`, and nothing is stored.
func TestReviewNotDesignatedReviewerExit3(t *testing.T) {
	s := clienttest.New(t)
	s.NotDesignatedReviewer = true
	_, err := colab.ReviewApprove(context.Background(), newClient(t, s),
		colab.ReviewArgs{Artifact: clienttest.ArtifactID})
	if got := exitOf(t, err); got != client.ExitRefused {
		t.Fatalf("exit = %d, want 3", got)
	}
	e := client.AsError(err)
	if e.Code != colab.CodeNotReviewer {
		t.Fatalf("code = %q, want %q (colab-cli.md §2.3)", e.Code, colab.CodeNotReviewer)
	}
	if e.Problem == nil || e.Problem.Code != colab.ServerNotDesignatedReviewer {
		t.Fatalf("the server's own problem must survive, got %+v", e.Problem)
	}
	if len(s.Reviews) != 0 {
		t.Fatal("nothing may be stored")
	}
}

// The artifact id is part of the path, so it cannot be omitted; reject also
// requires a reason.
func TestReviewArgErrors(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	if _, err := colab.ReviewApprove(context.Background(), c, colab.ReviewArgs{Note: "ok"}); exitOf(t, err) != client.ExitUsage {
		t.Fatal("approve without --artifact must be exit 2")
	}
	if _, err := colab.ReviewReject(context.Background(), c, colab.ReviewArgs{Artifact: clienttest.ArtifactID}); exitOf(t, err) != client.ExitUsage {
		t.Fatal("reject without --reason must be exit 2")
	}
	if _, err := colab.ReviewReject(context.Background(), c, colab.ReviewArgs{Reason: "no"}); exitOf(t, err) != client.ExitUsage {
		t.Fatal("reject without --artifact must be exit 2")
	}
	if len(s.Reviews) != 0 {
		t.Fatal("no request may be sent")
	}
}

// ───────────────────── exit-code convention across P2 ─────────────────────

// colab-cli.md §2: the P1 exit codes hold for every P2 command too —
// 4 for a missing or revoked token, 5 when the server cannot be reached.
func TestP2ExitCodeConvention(t *testing.T) {
	dir := t.TempDir()
	run := map[string]func(*testing.T, *client.Client) error{
		"lane delegate": func(t *testing.T, c *client.Client) error {
			_, err := colab.LaneDelegate(context.Background(), c, colab.LaneDelegateArgs{Agent: "Reviewer", Brief: "b"})
			return err
		},
		"status set": func(t *testing.T, c *client.Client) error {
			_, err := colab.StatusSet(context.Background(), c, colab.StatusSetArgs{Status: "done"})
			return err
		},
		"decision record": func(t *testing.T, c *client.Client) error {
			_, err := colab.DecisionRecord(context.Background(), c, colab.DecisionRecordArgs{Summary: "s"})
			return err
		},
		"artifact submit": func(t *testing.T, c *client.Client) error {
			p := filepath.Join(dir, "f.txt")
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := colab.ArtifactSubmit(context.Background(), c, colab.ArtifactSubmitArgs{Type: "doc", File: p})
			return err
		},
		"artifact get": func(t *testing.T, c *client.Client) error {
			_, err := colab.ArtifactGet(context.Background(), c, colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID})
			return err
		},
		"review approve": func(t *testing.T, c *client.Client) error {
			_, err := colab.ReviewApprove(context.Background(), c, colab.ReviewArgs{Artifact: clienttest.ArtifactID})
			return err
		},
		"review reject": func(t *testing.T, c *client.Client) error {
			_, err := colab.ReviewReject(context.Background(), c, colab.ReviewArgs{Artifact: clienttest.ArtifactID, Reason: "r"})
			return err
		},
	}
	for name, call := range run {
		t.Run(name, func(t *testing.T) {
			// E15-04 — test chat: no token at all → exit 4.
			t.Run("no token", func(t *testing.T) {
				s := clienttest.New(t)
				env := s.Env(t.TempDir())
				delete(env, "COLAB_TASK_TOKEN")
				err := call(t, client.New(client.FromEnv(clienttest.Getenv(env))))
				if got := exitOf(t, err); got != client.ExitNoToken {
					t.Fatalf("exit = %d, want 4", got)
				}
			})
			// E11-04 — the token was revoked by a requeue → 401 → exit 4.
			t.Run("revoked token", func(t *testing.T) {
				s := clienttest.New(t)
				s.Revoked = true
				err := call(t, newClient(t, s))
				if got := exitOf(t, err); got != client.ExitNoToken {
					t.Fatalf("exit = %d, want 4", got)
				}
				if e := client.AsError(err); e.Code != "token_revoked" {
					t.Fatalf("code = %q", e.Code)
				}
			})
			// 5xx → exit 5.
			t.Run("server error", func(t *testing.T) {
				s := clienttest.New(t)
				s.Fail = 503
				err := call(t, newClient(t, s))
				if got := exitOf(t, err); got != client.ExitUnreachable {
					t.Fatalf("exit = %d, want 5", got)
				}
			})
			// 403 → exit 3.
			t.Run("refused", func(t *testing.T) {
				s := clienttest.New(t)
				s.Fail, s.FailCode = 403, "forbidden"
				err := call(t, newClient(t, s))
				if got := exitOf(t, err); got != client.ExitRefused {
					t.Fatalf("exit = %d, want 3", got)
				}
			})
		})
	}
}

// The P2 write commands must not consume the message-post seq space: the
// server folds X-Colab-Client-Seq into last_seq = max(client_seq), so a
// second consumer would make `message post` re-use an Idempotency-Key.
func TestP2DoesNotConsumeClientSeq(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	ctx := context.Background()
	if _, err := colab.LaneDelegate(ctx, c, colab.LaneDelegateArgs{Agent: "Reviewer", Brief: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := colab.DecisionRecord(ctx, c, colab.DecisionRecordArgs{Summary: "s"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Requests {
		if v := r.Header.Get(client.HeaderClientSeq); v != "" {
			t.Fatalf("%s %s sent %s: %q — only `message post` may", r.Method, r.URL.Path, client.HeaderClientSeq, v)
		}
	}
	res, err := colab.MessagePost(ctx, c, colab.MessagePostArgs{Body: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IdempotencyKey != clienttest.Key(1) {
		t.Fatalf("first post key = %q, want seq 1 (%q)", res.IdempotencyKey, clienttest.Key(1))
	}
}

// C-1: /cli/context is fetched when a command needs it and cached for the
// life of the process — at most one round trip, never one per command
// (colab-cli.md v0.4 §1).
func TestCliContextFetchedAtMostOncePerProcess(t *testing.T) {
	s := clienttest.New(t)
	// No COLAB_SESSION_ID / COLAB_TASK_ID, so every command must resolve its
	// path parameter from /cli/context.
	env := s.Env(t.TempDir())
	delete(env, "COLAB_SESSION_ID")
	delete(env, "COLAB_TASK_ID")
	c := client.New(client.FromEnv(clienttest.Getenv(env)))
	ctx := context.Background()
	if _, err := colab.LaneDelegate(ctx, c, colab.LaneDelegateArgs{Agent: "Reviewer", Brief: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := colab.StatusSet(ctx, c, colab.StatusSetArgs{Status: "working"}); err != nil {
		t.Fatal(err)
	}
	if _, err := colab.DecisionRecord(ctx, c, colab.DecisionRecordArgs{Summary: "s"}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range s.Requests {
		if r.URL.Path == "/api/v1/cli/context" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("/cli/context called %d times, want exactly 1 for the process", n)
	}
}

// A command that needs nothing from the context must not fetch it at all.
func TestArtifactGetDoesNotFetchCliContext(t *testing.T) {
	s := clienttest.New(t)
	if _, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID}); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Requests {
		if r.URL.Path == "/api/v1/cli/context" {
			t.Fatal("artifact get needs no context value; it must not round trip")
		}
	}
}
