package client

import "encoding/json"

// P3 wire types — contracts/openapi.yaml createHitlRequest (x-colab-cli
// `hitl ask | hitl approve-request | hitl request-info`, x-phase P3).
//
// The request body is openapi's `HitlCreate` oneOf, discriminated by `type`.
// Rather than four Go types the CLI sends one struct with omitempty fields:
// each command fills exactly the members its variant declares, so the wire
// body is the variant and nothing else.

// HITL types — openapi HitlType enum (FR-5.1).
const (
	HitlQuestion = "question"
	HitlChoice   = "choice"
	HitlApproval = "approval"
	HitlInfo     = "info"
)

// HitlCreate — POST /tasks/{T}/hitl body.
//
//   - question : question + proposed_default (+ context)
//   - choice   : question + options (>= 2) + proposed_default (+ context)
//   - approval : summary (+ artifact_id) — no default, it never auto-proceeds
//   - info     : what (+ why)
//
// `approver_spec` and `due_in` are left to the server's defaults (director,
// PT24H): colab-cli.md §2.4 gives the agent no flag for either, and an
// approver_spec outside `director` · `any_member` · uuid is a 422 by design
// (fail closed, E7-16).
type HitlCreate struct {
	Type            string   `json:"type"`
	Question        string   `json:"question,omitempty"`
	Context         string   `json:"context,omitempty"`
	ProposedDefault string   `json:"proposed_default,omitempty"`
	Options         []string `json:"options,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	ArtifactID      string   `json:"artifact_id,omitempty"`
	What            string   `json:"what,omitempty"`
	Why             string   `json:"why,omitempty"`
}

// HitlCreateResult — createHitlRequest 201 {hitl_request, turn_end_required,
// message_id?}.
//
// `turn_end_required` keeps the contract's full name here too — it is an
// instruction ("end your turn"), not ACP's `end_turn` stopReason, which is a
// statement that a turn already ended (colab-cli.md §2.4, Lead ruling on
// PR #59).
type HitlCreateResult struct {
	HitlRequest     json.RawMessage `json:"hitl_request"`
	TurnEndRequired bool            `json:"turn_end_required"`
	MessageID       *string         `json:"message_id,omitempty"`
	Raw             json.RawMessage `json:"-"`
}
