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
// What is deliberately NOT covered — text the server writes for an agent to
// read, not a person (PR #192 review NN7). It is in Korean too, so nothing
// about its shape keeps it out of the lock; the boundary is a decision:
//
//   - the turn brief (queue/bundle.go — briefContext · briefDecisionLog · the
//     team template instructions in agents/templates.go),
//   - wake-up prompts posted as system messages for a delegator
//     (router/status.go wake · wakeOnBlocked) and the rebind cold-start prompt
//     (runtimes/offline.go), which quote card ids, reply_to and FR numbers
//     because the agent needs them,
//   - the previous-session summary handed to the next turn (sessions/summary.go
//     ReuseSection, brief section [6]: "…읽어라").
//
// These stay out because the lock's sinks are named fields and helpers, and
// none of that code uses one. That is the whole mechanism: a brief builder
// that starts assigning to Note · Detail · Content, or calls SystemPost with
// a literal, becomes subject to the lock — and then either the text is for a
// person after all, or the field should not be named like one. The same goes
// for the daemon-facing Problems of the daemonAPI files (wording_test.go):
// their list is pinned by TestScope; growing it is a reviewed change.
//
// Rules of thumb for a new sentence (the §8.4 table wins over this list):
//
//	runtime      → 컴퓨터          lane        → 작업 줄기
//	GC           → 작업 폴더 정리   (daemon-protocol §6 v0.7.4 fixes the gc refusal sentence)
//	task         → 할 일           attempt     → 실행
//	workdir      → 작업 폴더        HITL        → 확인 요청 / 사람 확인
//	inbox        → 받은 요청        rebind      → 다른 컴퓨터로 옮기기
//	owner/admin  → 소유자·관리자    Director · deputy stay (§8.4 exception)
//
// Say what the person does next, not which rule fired.
package wording
