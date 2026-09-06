// Package hitl will own the HITL request lifecycle (PRD FR-5.1·5.2·5.4):
// registration by an agent, the `turn_end` transition to `waiting_human`, the
// approver/deputy window, deadline handling and the response that re-queues a
// new attempt.
//
// Right now the package holds only the P3a golden table
// (hitl_golden_test.go, build tag `p3golden`), written by the Reviewer before
// the implementation exists (PLAN §10.3). This file carries no logic; it is
// here so the package has at least one file outside the build tag — without it
// `go test ./internal/hitl` fails with "build constraints exclude all Go
// files" rather than reporting the golden rows.
//
// T-S5 adds the implementation here and drops the tag from the slices it
// lands.
package hitl
