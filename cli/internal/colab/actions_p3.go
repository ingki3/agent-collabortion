// P3 commands of contracts/colab-cli.md v0.5.1 §2.4 (openapi.yaml
// createHitlRequest, x-phase P3): hitl ask · hitl approve-request ·
// hitl request-info. All three are one server operation with a different
// `type`; the CLI (cmd/colab) and the MCP server (internal/mcp) call these
// functions, so a tool and its command send the same body and print the same
// JSON.
//
// The operation is session-scoped — POST /sessions/{S}/hitl-requests — and
// the task comes from the TaskToken, not from the path (v0.5.1, C-4: the
// v0.5 table named `/tasks/{T}/hitl`, which openapi never had, so every one
// of these commands 404'd against the real server).
//
// Every one of them means the same thing to the agent: the answer comes from
// a human, later. Register the request and END THE TURN — the server sets
// `pending_hitl` now and moves the task to `waiting_human` when `turn_end`
// arrives (FR-7.1 N4, E7-01·E7-03).
package colab

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// TurnEndInstruction is the one line every successful HITL registration
// prints alongside `turn_end_required: true` (colab-cli.md §2.4, E7-01).
// The flag is the machine-readable half; this is the half a model reads.
const TurnEndInstruction = "등록됨 — 이 턴을 끝내라"

// ServerHitlAlreadyOpen is the server's 409 code for "this task already has
// an open HITL request" (E7-04). It is passed through untouched: the CLI adds
// no wording of its own, so the agent reads the server's reason and the id of
// the request that is already waiting.
const ServerHitlAlreadyOpen = "hitl_already_open"

// HitlResult is what all three commands print. The typed fields are the ones
// an agent acts on; `hitl_request` keeps the server's whole object so --json
// never drops a field the contract adds later.
type HitlResult struct {
	HitlID          string          `json:"hitl_id"`
	Type            string          `json:"type"`
	TurnEndRequired bool            `json:"turn_end_required"`
	Instruction     string          `json:"instruction"`
	MessageID       string          `json:"message_id,omitempty"`
	HitlRequest     json.RawMessage `json:"hitl_request,omitempty"`
}

// HitlAskArgs — `colab hitl ask --question <q> --default <proposed>
// [--choices a,b,c] [--context <text>]` / colab_hitl_ask.
type HitlAskArgs struct {
	Session  string   `json:"session,omitempty"`
	Question string   `json:"question"`
	Default  string   `json:"default"`
	Choices  []string `json:"choices,omitempty"` // >= 2 turns this into a `choice`
	Context  string   `json:"context,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// HitlAsk — POST /sessions/{S}/hitl-requests with `question`, or `choice`
// when --choices is given.
//
// `--default` is required for BOTH types and colab-cli.md v0.5 §2.4 puts the
// check here, client side, as well as on the server.
// The server answers 422 either way (E7-05, E7-20), but a round trip to be
// told the flag is missing costs the agent a turn it did not have to spend,
// and the whole point of the default is that the human can accept the
// agent's proposal instead of composing one.
func HitlAsk(ctx context.Context, c *client.Client, a HitlAskArgs) (*HitlResult, error) {
	if strings.TrimSpace(a.Question) == "" {
		return nil, client.Usage("hitl ask: --question is required")
	}
	opts := splitList(a.Choices)
	typ := client.HitlQuestion
	if len(opts) > 0 {
		typ = client.HitlChoice
	}
	if strings.TrimSpace(a.Default) == "" {
		return nil, client.Usage(
			"hitl ask: --default is required for %s (FR-5.1: the human must be able to accept your proposal — E7-05, E7-20)", typ)
	}
	if typ == client.HitlChoice {
		// openapi HitlCreateChoice.options: minItems 2. One option is not a
		// choice, and the server would 422 it.
		if len(opts) < 2 {
			return nil, client.Usage("hitl ask: --choices needs at least 2 options, got %d (%s)", len(opts), strings.Join(opts, ", "))
		}
		if !contains(opts, a.Default) {
			return nil, client.Usage("hitl ask: --default %q must be one of --choices (%s)", a.Default, strings.Join(opts, ", "))
		}
	}
	return createHitl(ctx, c, a.Session, a.IdempotencyKey, client.HitlCreate{
		Type: typ, Question: a.Question, Context: a.Context,
		ProposedDefault: a.Default, Options: opts,
	})
}

// HitlApproveRequestArgs — `colab hitl approve-request --summary <s>
// [--artifact <id>]` / colab_hitl_approve_request.
type HitlApproveRequestArgs struct {
	Session  string `json:"session,omitempty"`
	Summary  string `json:"summary"`
	Artifact string `json:"artifact,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// HitlApproveRequest — POST /sessions/{S}/hitl-requests `{type: approval}`.
//
// There is deliberately no default here (E7-06): an `approval` never
// auto-proceeds, not even after the 24h due date passes (FR-5.4). Asking for
// a proposed default would suggest otherwise.
func HitlApproveRequest(ctx context.Context, c *client.Client, a HitlApproveRequestArgs) (*HitlResult, error) {
	if strings.TrimSpace(a.Summary) == "" {
		return nil, client.Usage("hitl approve-request: --summary is required (what you are asking approval for)")
	}
	return createHitl(ctx, c, a.Session, a.IdempotencyKey, client.HitlCreate{
		Type: client.HitlApproval, Summary: a.Summary, ArtifactID: strings.TrimSpace(a.Artifact),
	})
}

// HitlRequestInfoArgs — `colab hitl request-info --what <w> [--why <y>]` /
// colab_hitl_request_info. `--question` is accepted as an alias of `--what`.
type HitlRequestInfoArgs struct {
	Session string `json:"session,omitempty"`
	What    string `json:"what"`
	Why     string `json:"why,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// HitlRequestInfo — POST /sessions/{S}/hitl-requests `{type: info}`.
//
// Like `approval` it has no default and never auto-proceeds — FR-5.4 puts
// `approval` and `info` on one row, and E7-21 is the regression: `info` that
// is 24h overdue under `autonomy: autonomous` stays `open` + `overdue`.
//
// The type is v1 as of colab-cli.md v0.5 §2.4 (promoted from the v0.4 table's
// v1.1 marker on this task's question, on the strength of PLAN §3's P3 table
// and EVAL E7-21). The CLI does not refuse it locally: a client-side
// `unsupported_type` would hide a server that supports it.
func HitlRequestInfo(ctx context.Context, c *client.Client, a HitlRequestInfoArgs) (*HitlResult, error) {
	if strings.TrimSpace(a.What) == "" {
		return nil, client.Usage("hitl request-info: --what is required (the information you need)")
	}
	return createHitl(ctx, c, a.Session, a.IdempotencyKey, client.HitlCreate{
		Type: client.HitlInfo, What: a.What, Why: a.Why,
	})
}

// createHitl is the one server call the three commands share: resolve the
// session, POST, and shape the result. A second open request on the same task
// is the server's 409 hitl_already_open → exit 3, forwarded verbatim (E7-04).
//
// The session is COLAB_SESSION_ID when the daemon set it — which it always
// does (harness.md §2.1) — and /cli/context otherwise, so the one-request
// property of C-1 holds on the normal path. The task is not resolved at all
// here: it rides in the TaskToken.
func createHitl(ctx context.Context, c *client.Client, session, key string, body client.HitlCreate) (*HitlResult, error) {
	sid, err := c.SessionID(ctx, session)
	if err != nil {
		return nil, err
	}
	res, err := c.CreateHitlRequest(ctx, sid, body, key)
	if err != nil {
		return nil, err
	}
	out := &HitlResult{
		HitlID:          rawField(res.HitlRequest, "id"),
		Type:            body.Type,
		TurnEndRequired: res.TurnEndRequired,
		Instruction:     TurnEndInstruction,
		HitlRequest:     res.HitlRequest,
	}
	if res.MessageID != nil {
		out.MessageID = *res.MessageID
	}
	return out, nil
}

// contains reports whether v is one of list, ignoring surrounding space the
// way splitList already trimmed the options.
func contains(list []string, v string) bool {
	v = strings.TrimSpace(v)
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
