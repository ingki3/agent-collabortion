# Colab — agent collaboration messaging

여러 AI 에이전트(Claude Code, Hermes — 사용자 머신의 CLI 런타임)가 공유 세션에서 @멘션으로 협업하고, 병렬 lane으로 일하며, 사람 Director가 HITL로 개입하는 메시징 서비스.

## 문서 (SSOT)

| 문서 | 내용 |
|---|---|
| [`PRD.md`](PRD.md) | 제품 요구사항 v0.11 — 라우팅 규칙, lane, HITL, 하네스, 스키마, 로드맵 |
| [`SCREEN.md`](SCREEN.md) | 화면 설계 v0.3 — S1~S17 |
| [`COMPONENTS.md`](COMPONENTS.md) | 재사용 컴포넌트 카탈로그 v0.4 (`agent-collaboration.pen`) |
| [`PLAN.md`](PLAN.md) | 개발 계획 v0.5 — 단계·게이트·컷·에이전트 오케스트레이션 |
| [`EVAL.md`](EVAL.md) | 테스트 시나리오 ↔ 정확한 예상 동작 v0.1 — 규칙 중심, 게이트별 통과 조건, 골든 테스트의 원본 |
| [`EVAL_USER.md`](EVAL_USER.md) | 사용자 여정 중심 테스트 케이스 v0.1 — 페르소나 3명, 여정 16개, 실측 프로토콜(G8) |

이전 버전과 리뷰: `prd/` `screen/` `design/` `plan/`.

## 구조

```
contracts/   스트림 간 계약 (OpenAPI, task_event, 데몬 프로토콜, colab CLI, 클럭) — Director 승인 PR로만 변경
server/      Go — API, Router, 상태 머신, 큐, 실시간, 인박스, GC        (스트림 S)
daemon/      Go — 페어링, task claim, workdir, ACP 하네스, heartbeat, 고아 정리 (스트림 D)
cli/         Go — `colab` CLI + MCP 서버                                  (스트림 C)
web/         Next.js — S1~S17, 컴포넌트 8종, 활동 피드                     (스트림 W)
```

Go 워크스페이스(`go.work`)로 묶여 있다. 각 스트림은 별도 모듈이고 `contracts`만 공유한다.

## 시작

```sh
make web-install   # 최초 1회
make dev           # Postgres(Docker) + server :8080 + web :3000
make test          # go vet/test 전부 + web typecheck
```

`curl localhost:8080/healthz` → `{"ok":true,"contracts":"..."}`

## 브랜치 흐름

```
feature/* ──PR──▶ dev ──PR──▶ main
```

- `dev`·`main` 직접 푸시 금지(ruleset, bypass 없음). 삭제·force push 금지.
- `main`으로 가는 PR은 head가 `dev`일 때만 통과(`branch-flow` 필수 체크).
- PR 머지 조건(PLAN.md §10.4): Reviewer 승인 + CI 초록 + (계약·테스트 변경 시) Director 승인.

## 개발 방식

사람 팀이 아니라 **에이전트 오케스트레이션**으로 만든다(PLAN.md §10). Director 한 명, Lead 에이전트, 스트림별 에이전트 4개, Reviewer(Hermes), Integrator. 작업 단위는 반나절 이하, PR 하나. 일정은 주가 아니라 **게이트 9개**로 잰다.
