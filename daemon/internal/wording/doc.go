// Package wording holds the lock on the sentences this daemon writes for a
// person to read (COMPONENTS §8.4 "문구 — 사용자의 말로", backlog D-25).
//
// The daemon has exactly one such surface: the `detail` of a task_event it
// composes itself (PRD §7 v0.16 / S-52 — the runtime class's one free-text
// field). The server stores it and the activity feed shows it verbatim, so
// "workdir bundle path … →" and "유효 예산" reached the screen in the
// daemon's own words. The server pinned its half with
// server/internal/wording; this is the daemon's.
//
// There is no code here: wording_test.go walks the daemon's sources with
// go/ast, collects every string literal that reaches one of the sinks below,
// and asserts that
//
//   - the sentence is in the language of the screens (it contains Hangul),
//   - none of the §8.4 old words or the internal nouns appear in it,
//   - spec references and API names stay in comments.
//
// Sinks (see wording_test.go): the `"detail"` key of a task_event payload,
// `detail :=` locals, `Detail:` fields (acp.Failure), `r.fail(kind, detail)`,
// `CancelNote(detail)`, and the functions whose whole body composes such a
// sentence (loop.workdirDetail, workdir.Verify) plus the budget side names.
//
// Deliberately NOT covered:
//
//   - the harness §5 cancel-step lines `§5 <n> <step> …` (acp.cancelStep /
//     emitStep). They are machine-shaped on purpose — the P3 cancel golden
//     (p3_cancel_golden_test.go) and the P3 smoke parse them by position —
//     and changing their shape is a golden change, not a wording fix. The
//     collector does not follow a call to cancelStep, so they are out by
//     construction; this paragraph is the record of why.
//   - text the daemon passes through from the runtime (rpc.Message, stderr
//     first lines): not literals, and the runtime's own words.
//   - the daemon's progress log (D-24): an operator's stderr, not a screen.
//
// The banned-term table is not copied: the English terms come from the §8.4
// table in COMPONENTS.md at test time, and TestSameTableAsServer reads
// server/internal/wording/wording_test.go and fails when the two locks'
// regex lists differ — so the server's table is the one table.
package wording
