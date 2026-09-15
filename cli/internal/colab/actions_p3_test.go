package colab_test

// Contract tests for the P3 HITL commands (contracts/colab-cli.md v0.5.1
// §2.4, openapi.yaml createHitlRequest): the request line, the body each
// command builds, the client-side flag checks that spare the agent a round
// trip, and how the second open request on a task reaches the agent. EVAL
// rows are named on each test; the contract, served by clienttest, is the
// reference.

import (
	"context"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// E7-01: `hitl ask` registers a question and tells the agent to end its turn.
func TestHitlAskQuestion(t *testing.T) {
	s := clienttest.New(t)
	s.HitlMessageID = "99999999-9999-4999-8999-999999999999"
	res, err := colab.HitlAsk(context.Background(), newClient(t, s), colab.HitlAskArgs{
		Question: "독자?", Default: "투자자", Context: "브리프에 없다",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.HitlCalls) != 1 {
		t.Fatalf("hitl calls = %d, want 1", len(s.HitlCalls))
	}
	call := s.HitlCalls[0]
	// The path is the session's (openapi createHitlRequest); the task rides in
	// the token, which is why nothing on this call names it.
	if call.SessionID != clienttest.SessionID {
		t.Fatalf("session = %q, want the env's session %q", call.SessionID, clienttest.SessionID)
	}
	if call.TaskID != clienttest.TaskID {
		t.Fatalf("task = %q, want the token's task", call.TaskID)
	}
	if call.Body["type"] != "question" {
		t.Fatalf("type = %v, want question", call.Body["type"])
	}
	if call.Body["question"] != "독자?" || call.Body["proposed_default"] != "투자자" ||
		call.Body["context"] != "브리프에 없다" {
		t.Fatalf("body = %v", call.Body)
	}
	// A question carries no options: the oneOf variant is the body itself.
	if _, ok := call.Body["options"]; ok {
		t.Fatalf("question body carries options: %v", call.Body)
	}
	// The Idempotency-Key is optional and absent unless asked for.
	if call.Key != "" {
		t.Fatalf("Idempotency-Key = %q, want none by default", call.Key)
	}
	if !res.TurnEndRequired {
		t.Fatal("turn_end_required = false; the contract's 201 is const true")
	}
	if res.Instruction != colab.TurnEndInstruction {
		t.Fatalf("instruction = %q, want %q", res.Instruction, colab.TurnEndInstruction)
	}
	if res.HitlID != clienttest.HitlID || res.Type != "question" {
		t.Fatalf("res = %+v", res)
	}
	if res.MessageID != s.HitlMessageID {
		t.Fatalf("message_id = %q, want the timeline card %q", res.MessageID, s.HitlMessageID)
	}
	// Nothing the server sent is dropped: purpose is a P3 field (PR #110) the
	// CLI does not type, and it still reaches --json.
	if !strings.Contains(string(res.HitlRequest), `"purpose":"agent"`) {
		t.Fatalf("hitl_request lost the server's fields: %s", res.HitlRequest)
	}
}

// E7-05: `question` without --default is refused by the CLI (exit 2) and
// nothing is sent — the agent does not spend a round trip on a 422.
func TestHitlAskQuestionNeedsDefault(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.HitlAsk(context.Background(), newClient(t, s), colab.HitlAskArgs{Question: "독자?"})
	if got := exitOf(t, err); got != client.ExitUsage {
		t.Fatalf("exit = %d, want %d", got, client.ExitUsage)
	}
	if !strings.Contains(err.Error(), "--default") {
		t.Fatalf("error names no flag: %v", err)
	}
	if len(s.HitlCalls) != 0 {
		t.Fatalf("sent %d requests, want none", len(s.HitlCalls))
	}
}

// E7-20: the same rule for `choice`. E7-05 spelled it only for `question`,
// which an implementation could satisfy while leaving choice open.
func TestHitlAskChoiceNeedsDefault(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.HitlAsk(context.Background(), newClient(t, s), colab.HitlAskArgs{
		Question: "어느 쪽?", Choices: []string{"A,B"},
	})
	if got := exitOf(t, err); got != client.ExitUsage {
		t.Fatalf("exit = %d, want %d", got, client.ExitUsage)
	}
	if !strings.Contains(err.Error(), "choice") {
		t.Fatalf("error does not say the type is choice: %v", err)
	}
	if len(s.HitlCalls) != 0 {
		t.Fatalf("sent %d requests, want none", len(s.HitlCalls))
	}
}

// --choices makes it a `choice`: openapi HitlCreateChoice = question +
// options (>= 2) + proposed_default, and the default must be one of them.
func TestHitlAskChoice(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.HitlAsk(context.Background(), newClient(t, s), colab.HitlAskArgs{
		Question: "어느 쪽?", Default: "B", Choices: []string{"A,B", " C "},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := s.HitlCalls[0].Body
	if body["type"] != "choice" || body["proposed_default"] != "B" {
		t.Fatalf("body = %v", body)
	}
	opts, _ := body["options"].([]any)
	if len(opts) != 3 || opts[0] != "A" || opts[2] != "C" {
		t.Fatalf("options = %v (repeatable + comma-separated, trimmed)", opts)
	}
	if res.Type != "choice" || !res.TurnEndRequired {
		t.Fatalf("res = %+v", res)
	}
}

func TestHitlAskChoiceRejectsBadOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    colab.HitlAskArgs
		wantErr string
	}{
		{
			// openapi HitlCreateChoice.options: minItems 2.
			name:    "one option is not a choice",
			args:    colab.HitlAskArgs{Question: "어느 쪽?", Default: "A", Choices: []string{"A"}},
			wantErr: "at least 2",
		},
		{
			// HitlCreateChoice.proposed_default: "options 중 하나".
			name:    "default outside the options",
			args:    colab.HitlAskArgs{Question: "어느 쪽?", Default: "Z", Choices: []string{"A,B"}},
			wantErr: "must be one of",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clienttest.New(t)
			_, err := colab.HitlAsk(context.Background(), newClient(t, s), tc.args)
			if got := exitOf(t, err); got != client.ExitUsage {
				t.Fatalf("exit = %d, want %d", got, client.ExitUsage)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
			if len(s.HitlCalls) != 0 {
				t.Fatalf("sent %d requests, want none", len(s.HitlCalls))
			}
		})
	}
}

// E7-06: `approval` has no default and registering without one is normal.
func TestHitlApproveRequest(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.HitlApproveRequest(context.Background(), newClient(t, s), colab.HitlApproveRequestArgs{
		Summary: "프로덕션에 배포해도 되나", Artifact: clienttest.ArtifactID, IdempotencyKey: "k-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := s.HitlCalls[0].Body
	if body["type"] != "approval" || body["summary"] != "프로덕션에 배포해도 되나" ||
		body["artifact_id"] != clienttest.ArtifactID {
		t.Fatalf("body = %v", body)
	}
	// No default is sent, and none is invented: an approval never
	// auto-proceeds (FR-5.4).
	if _, ok := body["proposed_default"]; ok {
		t.Fatalf("approval body carries proposed_default: %v", body)
	}
	if s.HitlCalls[0].Key != "k-1" {
		t.Fatalf("Idempotency-Key = %q", s.HitlCalls[0].Key)
	}
	if res.Type != "approval" || !res.TurnEndRequired || res.Instruction != colab.TurnEndInstruction {
		t.Fatalf("res = %+v", res)
	}
}

func TestHitlApproveRequestNeedsSummary(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.HitlApproveRequest(context.Background(), newClient(t, s), colab.HitlApproveRequestArgs{})
	if got := exitOf(t, err); got != client.ExitUsage {
		t.Fatalf("exit = %d, want %d", got, client.ExitUsage)
	}
	if len(s.HitlCalls) != 0 {
		t.Fatalf("sent %d requests, want none", len(s.HitlCalls))
	}
}

// `info` (E7-21). The CLI sends the type and lets the server rule on it — it
// does not refuse the type locally (Lead ruling, T-C4).
func TestHitlRequestInfo(t *testing.T) {
	s := clienttest.New(t)
	res, err := colab.HitlRequestInfo(context.Background(), newClient(t, s), colab.HitlRequestInfoArgs{
		What: "결제사 API 키", Why: "샌드박스로는 재현되지 않는다",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := s.HitlCalls[0].Body
	if body["type"] != "info" || body["what"] != "결제사 API 키" ||
		body["why"] != "샌드박스로는 재현되지 않는다" {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["proposed_default"]; ok {
		t.Fatalf("info body carries proposed_default: %v", body)
	}
	if res.Type != "info" || !res.TurnEndRequired {
		t.Fatalf("res = %+v", res)
	}
}

func TestHitlRequestInfoNeedsWhat(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.HitlRequestInfo(context.Background(), newClient(t, s), colab.HitlRequestInfoArgs{Why: "필요해서"})
	if got := exitOf(t, err); got != client.ExitUsage {
		t.Fatalf("exit = %d, want %d", got, client.ExitUsage)
	}
	if len(s.HitlCalls) != 0 {
		t.Fatalf("sent %d requests, want none", len(s.HitlCalls))
	}
}

// E7-04: one open request per task. The second is the server's 409 →
// exit 3, and the server's own wording reaches the agent unchanged, naming
// the request that is already waiting.
func TestHitlSecondRequestIsRefused(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	if _, err := colab.HitlAsk(context.Background(), c, colab.HitlAskArgs{Question: "독자?", Default: "투자자"}); err != nil {
		t.Fatal(err)
	}
	// A different command and a different type: the conflict is per task, not
	// per command.
	_, err := colab.HitlApproveRequest(context.Background(), c, colab.HitlApproveRequestArgs{Summary: "배포?"})
	if got := exitOf(t, err); got != client.ExitRefused {
		t.Fatalf("exit = %d, want %d (409 → 3)", got, client.ExitRefused)
	}
	e := client.AsError(err)
	if e.Code != colab.ServerHitlAlreadyOpen {
		t.Fatalf("code = %q, want the server's %q verbatim", e.Code, colab.ServerHitlAlreadyOpen)
	}
	if !strings.Contains(e.Detail, clienttest.HitlID) {
		t.Fatalf("detail = %q, want the open request id passed through", e.Detail)
	}
	if len(s.HitlCalls) != 2 {
		t.Fatalf("hitl calls = %d — the CLI must not pre-check /cli/context", len(s.HitlCalls))
	}
}

// A revoked token is exit 4 on this path too (FR-9.1, E11-04): the HITL
// commands are the last thing a stopped agent tries.
func TestHitlRevokedToken(t *testing.T) {
	s := clienttest.New(t)
	s.Revoked = true
	_, err := colab.HitlAsk(context.Background(), newClient(t, s), colab.HitlAskArgs{Question: "독자?", Default: "투자자"})
	if got := exitOf(t, err); got != client.ExitNoToken {
		t.Fatalf("exit = %d, want %d", got, client.ExitNoToken)
	}
}

// C-1 for this path: with COLAB_TASK_ID in the environment — what the daemon
// always sets — a HITL registration is exactly one request. Two things ride
// on that: /cli/context is fetched only when a command needs a value from it
// (colab-cli.md v0.5 §1), and the "already open" check belongs to the server,
// not to a context snapshot the CLI may have taken minutes ago (E7-04).
func TestHitlIsOneRequest(t *testing.T) {
	s := clienttest.New(t)
	if _, err := colab.HitlAsk(context.Background(), newClient(t, s),
		colab.HitlAskArgs{Question: "독자?", Default: "투자자"}); err != nil {
		t.Fatal(err)
	}
	want := "/sessions/" + clienttest.SessionID + "/hitl-requests"
	if len(s.Requests) != 1 || !strings.HasSuffix(s.Requests[0].URL.Path, want) {
		paths := make([]string, 0, len(s.Requests))
		for _, r := range s.Requests {
			paths = append(paths, r.URL.Path)
		}
		t.Fatalf("requests = %v, want the POST %s alone", paths, want)
	}
}

// C-4 (found by T-I3): the three HITL commands used to POST
// /v1/tasks/{T}/hitl, a path openapi.yaml has never had — every registration
// 404'd against the real server, and the mock the smoke ran against was
// written from the same wrong table row, so nothing caught it.
//
// This is the regression: it asserts the ONE thing the fake cannot fake — the
// request line — against openapi createHitlRequest, for all three commands.
// Point the client back at /tasks/{T}/hitl and it fails here first.
func TestHitlPathIsSessionScoped(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *client.Client) error
	}{
		{"ask", func(ctx context.Context, c *client.Client) error {
			_, err := colab.HitlAsk(ctx, c, colab.HitlAskArgs{Question: "독자?", Default: "투자자"})
			return err
		}},
		{"approve-request", func(ctx context.Context, c *client.Client) error {
			_, err := colab.HitlApproveRequest(ctx, c, colab.HitlApproveRequestArgs{Summary: "배포?"})
			return err
		}},
		{"request-info", func(ctx context.Context, c *client.Client) error {
			_, err := colab.HitlRequestInfo(ctx, c, colab.HitlRequestInfoArgs{What: "API 키"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clienttest.New(t)
			if err := tc.call(context.Background(), newClient(t, s)); err != nil {
				t.Fatal(err)
			}
			got := s.Requests[len(s.Requests)-1]
			want := "/api/v1/sessions/" + clienttest.SessionID + "/hitl-requests"
			if got.Method != "POST" || got.URL.Path != want {
				t.Fatalf("%s %s, want POST %s (openapi createHitlRequest)", got.Method, got.URL.Path, want)
			}
			if strings.Contains(got.URL.Path, "/tasks/") {
				t.Fatalf("path is task-scoped again: %s (C-4)", got.URL.Path)
			}
		})
	}
}

// --session / the `session` tool argument overrides COLAB_SESSION_ID, the way
// every other session-scoped command's flag does. The task is NOT overridable
// any more: the token names it.
func TestHitlSessionOverride(t *testing.T) {
	s := clienttest.New(t)
	other := "77777777-7777-4777-8777-777777777777"
	_, err := colab.HitlAsk(context.Background(), newClient(t, s), colab.HitlAskArgs{
		Session: other, Question: "독자?", Default: "투자자",
	})
	// The fake's token scopes another session → 403, but the point is the
	// path: the flag reached the request line.
	if err == nil {
		t.Fatal("want the fake's 403 for a foreign session")
	}
	if len(s.HitlCalls) != 1 || s.HitlCalls[0].SessionID != other {
		t.Fatalf("calls = %+v, want one on session %s", s.HitlCalls, other)
	}
}
