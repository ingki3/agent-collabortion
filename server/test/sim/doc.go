// Package sim holds the partial-execution simulator (EVAL E8-04·E8-05, PLAN §5
// `sim` tier): "N edits + M posts, then a forced kill → re-queue → resume",
// repeated 100 times with zero duplicate messages and zero duplicate edits.
//
// The simulator itself lives in partial_exec_test.go behind the `p3golden`
// build tag, written by the Reviewer before the implementation (PLAN §10.3).
// This file carries no logic — it exists so the package builds without the
// tag, since a directory whose only file is tag-gated fails `go test ./...`
// with "build constraints exclude all Go files" instead of reporting rows.
package sim
