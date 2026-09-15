# G2 판정 — 계약 승인 (P0-b → P1)

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G2** — "계약 5종 + 클럭 승인. G1 이후에만" |
| 작성 | Lead 초안, 2026-09-05 |
| 상태 | **Director 확인 대기.** G1과 같은 원칙 — Director 위임("쭉 마무리")에 따라 이 초안으로 P1 fan-out을 시작한다. 계약은 문서·타입이라 Director가 뒤집어도 P1 초기에 고칠 수 있다 |

## 계약 목록

| 파일 | 버전 | 근거 | PR |
|---|---|---|---|
| `contracts/harness.md` | v0.2 | G1 + 스파이크 1·2·3·4a·6·**1b** 실측 | #12, #14 |
| `contracts/daemon-protocol.md` | v0.1 | PRD §8.1·FR-7.1·FR-9.1·FR-9.2·FR-6.4 | #12 |
| `contracts/task_event.schema.json` | v0.1 | FR-7.2 + harness §7 정규화 | #12, #14 |
| `contracts/colab-cli.md` | v0.1 | FR-7.4·FR-3.3·FR-5·FR-6.2.1 | #12 |
| `contracts/clock/` | v0.1 | PLAN §5 시간 의존 | #12 |
| `contracts/protocol.go` | v0.1 | 위 문서의 Go 타입·열거값·타이밍 상수 | #12, #14 |
| `contracts/openapi.yaml` + `openapi.md` | 0.1.0-draft | 스키마 v0·PRD FR-1~9·SCREEN §4 — operation 94, redocly 0/0 | #15 |

**게이트 예산(PLAN §6.2 G2: `blocked` 3 / PR 8)**: 실제 `blocked`(question) 1건(스키마 A~D), PR 5건(#12~#16). 통과.

**토큰 예산 단위 `u`**: P0에서 측정하기로 한 작업 단위당 소비는 이번에 재지 못했다 — worker 세션의 usage를 Orca가 노출하지 않고, 한도 창 사용률(0.59→0.63, 스파이크 1b 21턴)만 관측됐다. **P1에서 worker별 `_claude/rateLimit.utilization` 증분을 기록해 G3에서 정한다.** 그때까지 토큰 예산은 "한도 창 사용률 증분"으로 대신 본다.

## openapi.md §6 미결 Q1~Q12 판정

| # | 판정 | 반영 위치 |
|---|---|---|
| Q1 스키마 v0에 없는 테이블 | **`0002_p1_auth_and_stream.sql`로 추가**(P1 S 작업): `app_user.password_hash`, `user_session`, `workspace_invite`, `runtime_pairing`, `session_subscription`, `member.notification_settings(jsonb)`, `artifact_review`, `idempotency_key`, `stream_event`(SSE 백필 10분 창). 기존 0001은 수정하지 않는다 | T-S1 |
| Q2 `session.isolation.remote_url` | 승인 — jsonb 키 추가. PRD §7 후속 | T-S1, PRD |
| Q3 `budget_policy` 키 | 승인 — `default_session_budget_usd`·`default_task_budget_usd`·`workspace_monthly_budget_usd`·`pricing_overrides` | PRD §7 후속 |
| Q4 `SubscriptionLevel` | 승인 — `all`·`hitl_only`·`completion_only` | — |
| Q5 SSE | **승인** — D1 근거(단방향·재연결 표준·쿠키 인증) 타당. `stream_event` 보존 10분, 넘으면 `resync` | T-S1, T-W1 |
| Q6 SSO | **v1.1.** v1은 이메일/비밀번호만. S1의 "또는 SSO"는 비활성 + v1.1 배지 | SCREEN 후속 |
| Q7 에이전트 수정·삭제 권한 | **소유자 + owner·admin** — SCREEN §8.2 Q7 해소 | SCREEN 후속, PRD FR-1.9 |
| Q8 TaskToken 읽기 범위 | 승인 — 그 task의 세션 안에서 세션·메시지·lane·task·아티팩트·결정 읽기, 그 밖은 403. `GET /cli/context`가 전처리 | `colab-cli.md` v0.2 |
| Q9 `task_event.class` | `TaskEvent` 스키마를 `task_event.schema.json`으로 `$ref` 교체 | contracts 후속 PR |
| Q10 세션 예산 HITL 두 경로 | 승인 — 카드와 배너 둘 다, 서로 닫는다 | — |
| Q11 `completeSession` 실행 중 lane | 승인 — `confirm: true` 없으면 `409 running_lanes`. SCREEN 종료 다이얼로그 문구에 반영 | SCREEN 후속 |
| Q12 재바인딩 후 `runtime_session_ref` | 승인 — 격리 무관하게 비운다(런타임이 달라지므로) | — |

## Orca 운영에서 배운 것 (P1 작업 규칙에 반영)

- 워크트리 브랜치가 **main 기준**으로 만들어진다 → worker는 먼저 `git fetch origin dev && git checkout -b <branch> origin/dev`.
- `gh pr create`가 프롬프트에서 멈춘다 → `GH_PROMPT_DISABLED=1 gh pr create --repo ingki3/agent-collabortion --base dev --head <branch> --body-file …`.
- Claude Code 폴더 신뢰 대화상자 → 띄우기 전 `~/.claude.json` trust 처리.
- 한도 정지는 Orca `dispatched`로는 안 보인다 → heartbeat 30분 없으면 터미널 화면을 읽는다.

## P1 착수 조건

`plan/P1_TASKS.md`의 T-S1·T-D1·T-C1·T-W1을 동시 fan-out. Reviewer Hermes는 Orca 터미널로. Integrator T-I1은 네 PR 머지 후.
