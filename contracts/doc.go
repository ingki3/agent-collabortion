// Package contracts holds the Go types shared by server, daemon and cli:
// protocol messages, task_event, colab CLI token format.
//
// Empty until G2 (PLAN.md §3 P0-b). Implementation PRs must not modify
// this module — see contracts/README.md.
package contracts

// Version is the contract set version. Bumped only by Director-approved PRs.
const Version = "0.0.0-pre-g2"
