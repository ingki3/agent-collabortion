// Mission ledger commands — contracts/colab-cli.md v0.9.12 §2.3 (PRD FR-4.6
// · openapi v0.3.12 tag `memory`): memory note · supersede · retire · get.
// The CLI (cmd/colab) and the MCP server call these, so a tool's arguments
// and result are exactly the command's.
package colab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// The ledger enums, openapi MemoryKind · MemoryCertainty · MemoryOutcome and
// listMemory's status query (TestMemoryEnumsMatchOpenapi reads the contract).
var (
	MemoryKinds       = []string{"fact", "assignment", "open_question", "lesson", "plan", "progress"}
	MemoryCertainties = []string{"given", "to_verify", "derived", "guess"}
	MemoryOutcomes    = []string{"dead_end", "corrected", "useful"}
	MemoryStatuses    = []string{"active", "superseded", "retired", "all"}
)

// ErrCodeNoMission is the exit 3 of a ledger command in a turn that belongs
// to no mission (no COLAB_WORK_ID, no --work) — nothing is sent.
const ErrCodeNoMission = "no_mission"

// NoMissionSentence is that refusal's sentence.
const NoMissionSentence = "이 턴은 미션에 매여 있지 않습니다 — 미션 상태 원장은 미션 안에서만 씁니다(COLAB_WORK_ID 없음)"

// ErrCodeMemoryKindForbidden is the server's 403 code for kind plan ·
// progress written by a non-lead; the CLI uses the same code when it already
// knows the role (colab-cli v0.9.12 §2.3), so the agent reads one code
// whichever layer refused.
const ErrCodeMemoryKindForbidden = "memory_kind_forbidden"

// MemoryKindForbiddenSentence is the CLI's refusal of kind plan|progress.
func MemoryKindForbiddenSentence(role, kind string) string {
	return fmt.Sprintf("이 역할(%s)은 원장에 %s 를 쓸 수 없습니다 — plan·progress 는 lead 만 씁니다", role, kind)
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// workOf is the mission a ledger command addresses: the override, else the
// turn's COLAB_WORK_ID; neither is exit 3 no_mission.
func workOf(c *client.Client, override string) (string, error) {
	w := strings.TrimSpace(override)
	if w == "" {
		w = c.WorkID()
	}
	if w == "" {
		return "", &client.Error{Exit: client.ExitRefused, Code: ErrCodeNoMission, Title: "not in a mission", Detail: NoMissionSentence}
	}
	return w, nil
}

// ───────────────────────────── memory note ─────────────────────────────

// MemoryNoteArgs — `colab memory note --kind <k> --content <t> [--certainty
// <c>] [--outcome <o>] [--source <msg_id>,...]` / colab_memory_note (openapi
// MemoryItemInput).
type MemoryNoteArgs struct {
	Kind             string   `json:"kind"`
	Content          string   `json:"content"`
	Certainty        string   `json:"certainty,omitempty"`
	Outcome          string   `json:"outcome,omitempty"`
	SourceMessageIDs []string `json:"source_message_ids,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// MemoryNote — POST /works/{W}/memory, W = COLAB_WORK_ID. Argument errors
// (kind outside the enum, --certainty on a non-fact, --outcome on a
// non-lesson) are exit 2 before anything is sent; kind plan|progress with a
// known non-lead role is exit 3 memory_kind_forbidden before the POST.
func MemoryNote(ctx context.Context, c *client.Client, a MemoryNoteArgs) (json.RawMessage, error) {
	if !oneOf(a.Kind, MemoryKinds) {
		return nil, client.Usage("--kind must be one of %s (got %q)", strings.Join(MemoryKinds, " · "), a.Kind)
	}
	if strings.TrimSpace(a.Content) == "" {
		return nil, client.Usage("--content is required")
	}
	if a.Certainty != "" {
		if a.Kind != "fact" {
			return nil, client.Usage("--certainty is only for --kind fact (got --kind %s)", a.Kind)
		}
		if !oneOf(a.Certainty, MemoryCertainties) {
			return nil, client.Usage("--certainty must be one of %s (got %q)", strings.Join(MemoryCertainties, " · "), a.Certainty)
		}
	}
	if a.Outcome != "" {
		if a.Kind != "lesson" {
			return nil, client.Usage("--outcome is only for --kind lesson (got --kind %s)", a.Kind)
		}
		if !oneOf(a.Outcome, MemoryOutcomes) {
			return nil, client.Usage("--outcome must be one of %s (got %q)", strings.Join(MemoryOutcomes, " · "), a.Outcome)
		}
	}
	w, err := workOf(c, "")
	if err != nil {
		return nil, err
	}
	if err := c.Allow(ctx, client.CmdMemoryNote); err != nil {
		return nil, err
	}
	if a.Kind == "plan" || a.Kind == "progress" {
		role, err := c.KnownRole(ctx)
		if err != nil {
			return nil, err
		}
		if role != "" && role != "lead" {
			return nil, &client.Error{Exit: client.ExitRefused, Code: ErrCodeMemoryKindForbidden,
				Title: "memory kind not allowed for this role", Detail: MemoryKindForbiddenSentence(role, a.Kind),
				Extra: map[string]any{"role": role, "kind": a.Kind}}
		}
	}
	body := map[string]any{"kind": a.Kind, "content": a.Content, "source_message_ids": nonNil(splitList(a.SourceMessageIDs))}
	if a.Certainty != "" {
		body["certainty"] = a.Certainty
	}
	if a.Outcome != "" {
		body["outcome"] = a.Outcome
	}
	return c.MemoryCall(ctx, http.MethodPost, "/works/"+url.PathEscape(w)+"/memory", nil, body, a.IdempotencyKey)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ───────────────────────────── memory supersede ─────────────────────────

// MemorySupersedeArgs — `colab memory supersede <id> --content <t> [--source
// ...]` / colab_memory_supersede (supersedeMemory's requestBody). kind is the
// target's and is not an argument; certainty is in the tool's generated
// schema (the requestBody has it) but the CLI takes no flag for it — the
// contract's command line inherits it from the target.
type MemorySupersedeArgs struct {
	Memory           string   `json:"memory"`
	Content          string   `json:"content"`
	Certainty        string   `json:"certainty,omitempty"`
	SourceMessageIDs []string `json:"source_message_ids,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// MemorySupersede — POST /memory/{id}/supersede → 201 {item, superseded}. A
// target no longer active is the server's 409 memory_not_active → exit 3.
func MemorySupersede(ctx context.Context, c *client.Client, a MemorySupersedeArgs) (json.RawMessage, error) {
	if strings.TrimSpace(a.Memory) == "" {
		return nil, client.Usage("the ledger item id is required: colab memory supersede <id> --content <text>")
	}
	if strings.TrimSpace(a.Content) == "" {
		return nil, client.Usage("--content is required")
	}
	if a.Certainty != "" && !oneOf(a.Certainty, MemoryCertainties) {
		return nil, client.Usage("certainty must be one of %s (got %q)", strings.Join(MemoryCertainties, " · "), a.Certainty)
	}
	if err := c.Allow(ctx, client.CmdMemorySupersede); err != nil {
		return nil, err
	}
	body := map[string]any{"content": a.Content, "source_message_ids": nonNil(splitList(a.SourceMessageIDs))}
	if a.Certainty != "" {
		body["certainty"] = a.Certainty
	}
	return c.MemoryCall(ctx, http.MethodPost, "/memory/"+url.PathEscape(strings.TrimSpace(a.Memory))+"/supersede", nil, body, a.IdempotencyKey)
}

// ───────────────────────────── memory retire ─────────────────────────────

// MemoryRetireArgs — `colab memory retire <id> --reason <t>` /
// colab_memory_retire. retireMemory takes no Idempotency-Key (openapi).
type MemoryRetireArgs struct {
	Memory string `json:"memory"`
	Reason string `json:"reason"`
}

// MemoryRetire — POST /memory/{id}/retire → 200 MemoryItem.
func MemoryRetire(ctx context.Context, c *client.Client, a MemoryRetireArgs) (json.RawMessage, error) {
	if strings.TrimSpace(a.Memory) == "" {
		return nil, client.Usage("the ledger item id is required: colab memory retire <id> --reason <text>")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return nil, client.Usage("--reason is required")
	}
	if err := c.Allow(ctx, client.CmdMemoryRetire); err != nil {
		return nil, err
	}
	return c.MemoryCall(ctx, http.MethodPost, "/memory/"+url.PathEscape(strings.TrimSpace(a.Memory))+"/retire", nil, map[string]any{"reason": a.Reason}, "")
}

// ───────────────────────────── memory get ─────────────────────────────

// MemoryGetArgs — `colab memory get [--kind <k>] [--work <id>] [--status
// active|superseded|retired|all]` / colab_memory_get.
type MemoryGetArgs struct {
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
	Work   string `json:"work,omitempty"`
}

// MemoryGetResult wraps listMemory's array: an MCP tool's structuredContent
// is an object, and the CLI prints the same JSON as the tool.
type MemoryGetResult struct {
	WorkID string          `json:"work_id"`
	Status string          `json:"status"`
	Items  json.RawMessage `json:"items"`
}

// MemoryGet — GET /works/{W}/memory?status=<s>[&kind=<k>], W = --work or
// COLAB_WORK_ID, status default active (sent explicitly, as the contract's
// row writes the query).
func MemoryGet(ctx context.Context, c *client.Client, a MemoryGetArgs) (*MemoryGetResult, error) {
	if a.Kind != "" && !oneOf(a.Kind, MemoryKinds) {
		return nil, client.Usage("--kind must be one of %s (got %q)", strings.Join(MemoryKinds, " · "), a.Kind)
	}
	status := a.Status
	if status == "" {
		status = "active"
	}
	if !oneOf(status, MemoryStatuses) {
		return nil, client.Usage("--status must be one of %s (got %q)", strings.Join(MemoryStatuses, " · "), status)
	}
	w, err := workOf(c, a.Work)
	if err != nil {
		return nil, err
	}
	if err := c.Allow(ctx, client.CmdMemoryGet); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("status", status)
	if a.Kind != "" {
		q.Set("kind", a.Kind)
	}
	raw, err := c.MemoryCall(ctx, http.MethodGet, "/works/"+url.PathEscape(w)+"/memory", q, nil, "")
	if err != nil {
		return nil, err
	}
	var probe []json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, &client.Error{Exit: client.ExitUnreachable, Code: "bad_response", Title: "unparseable server response", Detail: err.Error()}
	}
	return &MemoryGetResult{WorkID: w, Status: status, Items: raw}, nil
}
