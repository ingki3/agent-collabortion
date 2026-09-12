// Package wording holds the lock on the sentences this server writes for a
// person to read (COMPONENTS §8.4 "문구 — 사용자의 말로", backlog S-67).
//
// The web fixed its own strings in PR #188 and pinned them with
// web/lib/wording.test.ts. Everything the server composes for the same screens
// — Problem.detail / Problem.title / errors[].message, system messages posted
// into a session, HITL cards, inbox items, and the human columns of a
// task_event (payload.detail · args.note, S-52) — is the other half of that
// surface, and without a lock of its own it drifts back to "runtime" and
// "lane" one hotfix at a time.
//
// There is no code here: wording_test.go walks the module's sources with
// go/ast, collects every string literal that reaches one of those sinks, and
// asserts that
//
//   - the sentence is in the language of the screens (it contains Hangul),
//   - none of the §8.4 old words or the internal nouns (lane · task · attempt ·
//     workdir · runtime · HITL · …) appear in it,
//   - spec references (FR-x.y · E1-02 · §) and API operation names stay in
//     comments, not in what a user reads.
//
// Rules of thumb for a new sentence (the §8.4 table wins over this list):
//
//	runtime      → 컴퓨터          lane        → 작업 줄기
//	task         → 할 일           attempt     → 실행
//	workdir      → 작업 폴더        HITL        → 확인 요청 / 사람 확인
//	inbox        → 받은 요청        rebind      → 다른 컴퓨터로 옮기기
//	owner/admin  → 소유자·관리자    Director · deputy stay (§8.4 exception)
//
// Say what the person does next, not which rule fired.
package wording
