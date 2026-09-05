# G3 판정 자료 — P1 수직 슬라이스 통합 보고 (T-I1)

| 항목 | 내용 |
|---|---|
| 작성 | Integrator (T-I1), 2026-09-06 06:23~07:15 KST |
| 대상 | `origin/dev` `32c2f7a` (PR #18 CLI · #20 데몬 · #21 웹 · #22 서버 + 리뷰 반영 #24·#25·#27, **#26 0004 마이그레이션 포함**) |
| 실제 런타임 | Claude Code 2.1.258(로그인됨) · 어댑터 `@agentclientprotocol/claude-agent-acp@0.74.0` · Hermes 0.20.6(감지만, 실행 안 함) · macOS arm64 |
| 에이전트 모델 | `claude-haiku-4-5-20251001` (비용 지침). U1 브라우저 경로만 웹이 고르는 probe `models[0]` |
| 스크립트 | `e2e/p1/` — 재현 명령은 §5. **CI 에서는 실행하지 않는다**(실제 런타임·로그인 필요) |
| 판정 근거 | PLAN.md §3 P1 DoD 5항목, EVAL E11 전부·E12-01·02·07·10·11·E17-01·02, EVAL_USER U1·U13 |

## 1. 판정 요약

| # | PLAN §3 P1 DoD | 판정 | 수치 | 결함 스트림 |
|---|---|---|---|---|
| 1 | E2E 20회: 멘션 → 2초 내 claim → 실행 → 답글 웹 도착, 첫 출력 중앙값 ≤ 10초 | **통과** (우회·수정 후 확인) | claim 중앙값 **6ms**(max 12ms) · 첫 출력(런타임 첫 이벤트) 중앙값 **3.05s** · 답글 도착 중앙값 **3.43s** · 20/20 답글 | — (전제 결함 S-1 은 #26 으로 수정됨) |
| 2 | 데몬 `kill -9` → heartbeat 만료 재큐잉 · 고아 정리 · 폐기 토큰 거부 · 중복 게시 0 | **통과** | kill 후 **186s** 에 `queued attempt 2`, 토큰 `revoked(requeue)`, 폐기 토큰 `colab message post` → **exit 4 token_revoked·저장 0**, 고아 자체 게시 0, `kill9-start`·`kill9-done` 각 **1건**, workdir 보존 | — |
| 3 | 취소 절차가 프로세스 트리를 남기지 않는다 | **부분 통과** | 프로세스 그룹 4 → **0**, pgid 기록 삭제, 취소 후 게시 0. 그러나 (a) 서버 `cancelLane` **501**, (b) 데몬 종료 경로가 `finish outcome=failed(other)` 로 보고 → 서버가 **재큐잉(attempt 2)** — `cancelled` 아님 | **S**(cancelLane 미구현) · **D**(종료 경로 outcome) |
| 4 | 초대 링크로 두 번째 멤버 입장 | **통과** (API) / 브라우저 미측정 | createInvite → 미로그인 signup(invite_token) → `accepted_invite`, 멤버 +1, 초대 `accepted`, 두 번째 사용자 워크스페이스 목록에 포함(S4 건너뜀) | 경미: `invite.url` 이 서버 오리진(:8080) — §4 S-5 |
| 5 | 신규 머신에서 S12 절차로 데몬 페어링 실측 1회 | **통과(API/CLI)** · **실패(웹 S12 화면)** | `daemon pair` → probe 도착 → `ready` **7.7s**(PONG 턴 포함; 온보딩 경로 7.0s) · 서버 SSE `pairing.updated` 정상. **웹 S12 패널은 `대기 중`에 머묾**(페어링 2개 생성 + 갱신 안 됨) → U1 4단계 FAIL, 5~13단계 미도달 | **W** |

**G3 관점 요약**: 기반 다섯(ACP 하네스·큐·실시간·CLI 토큰·고아 정리)은 실제 런타임에서 동작한다(1·2 통과). 다만 첫 통합 시도에서는 **모든 attempt 가 finish 에 실패**하는 S 결함(§4 S-1)이 있어 20회 측정은 로컬 DB 우회 상태에서 했고, 이후 머지된 #26(0004) 을 적용해 우회를 걷어낸 뒤 kill -9 시나리오의 attempt 2 finish(200, `runtime_session_ref` 저장) 로 **수정 후 확인**했다. 사람이 쓰는 경로(U1) 는 S12 화면에서 막힌다(W). 취소는 P1 에 서버 경로가 없고 데몬 종료 경로의 상태 보고가 틀리다(S·D).

## 2. 환경과 전제

- 구성은 `make dev` 와 동일(Postgres 16 docker, `bin/server` :8080, `next dev` :3000 → `/api/v1` 프록시, 데몬은 시나리오가 `bin/daemon pair/run` 으로 기동)하되 백그라운드로 띄우기 위해 `e2e/p1/up.sh` 가 같은 명령을 개별 실행한다.
- **Postgres 격리**: 공용 `colab-pg`(5433) 는 06:32 20회차 도중 다른 세션의 `go test`(server `db` 패키지 `DROP SCHEMA`) 가 스키마를 날려 1차 20회 측정이 19회에서 끊겼다(`e2e/p1/out/a-latency-run1-partial-clobbered.tsv`, 답글 중앙값 3.9s). 이후 **E2E 전용 컨테이너 `colab-pg-e2e`(5435)** 로 옮겨 전부 재측정했다. 기본값을 그렇게 두었다(`PG_PORT`/`PG_CONTAINER` 로 덮어쓰기).
- **우회 상태 측정**(Lead 승인 06:27): §4 S-1 때문에 20회(DoD 1)·취소(DoD 3)는 로컬 DB 에 `ALTER TABLE lane … CHECK(kind OR runtime_kind)` 를 적용한 상태에서 측정했다(`e2e/p1/workaround-0001-check.sh`). 07:03 `origin/dev` 머지(#26 0004) + `make migrate` 후 우회를 걷어냈고(현재 CHECK = `runtime_kind AND session_id`), DoD 2(kill -9) 측정과 그 attempt 2 finish 는 **수정된 서버**로 했다. 20회 전체 재실행은 하지 않았다(Lead 지시).
- 데몬 `run` 은 `--no-turn`(정적 probe) 으로 띄웠다 → 재시작 뒤 `runtime.capabilities.models` 가 빈 배열로 덮인다(S11 표시에 영향, 기본 `run` 은 PONG 턴을 돈다). 결함으로 잡지 않았다.
- `colab` MCP 서버는 토큰 기록 래퍼(`e2e/p1/out/colab-tap.sh`, `daemon.json colab_bin`) 를 거친다 — E11-04 의 "폐기된 토큰"을 얻기 위한 테스트 픽스처. 구현 코드 무수정.
- 한도(rate limit): 06:44 한 차례 걸림(`errorKind=rate_limit`, 데몬이 `rate_limited` 로 분류·`not_before` 재큐잉 — G1 F3 경로 실기 확인). 07:01 리셋 후 재개.

## 3. 항목별 상세

### 3.1 DoD 1 — 20회 수직 슬라이스 (E17-01·02, E11-08, E12-01·07·10)

`e2e/p1/01_vertical_slice.sh` — 가입 → 워크스페이스 → 페어링 코드 → `daemon pair` → probe → Lead(claude_code, haiku) → 세션(none) → 초기 task 완료 대기 → `[@Lead](mention://agent/…) 인사해줘 (i/20)` × 20, 매회 답글까지 대기. 시각은 서버 DB timestamptz 단일 클럭.

| 지표(게시 시각 기준, 초) | 중앙값 | min | p95 | max | 정의 |
|---|---|---|---|---|---|
| claim (E17-01 ≤ 2s) | **0.006** | 0.005 | 0.008 | 0.012 | `task.dispatched_at − trigger message.created_at`. long-poll 이 즉시 깨어난다 |
| 첫 task_event | 0.020 | | | | `runtime.start`(데몬 발행) |
| **첫 출력** (E17-02 ≤ 10s) | **3.051** | 2.949 | 3.374 | 3.711 | 런타임이 낸 첫 `tool`/`message`/`plan` 이벤트(여기선 `tool.use_tool started` = `colab_message_post` 호출) |
| 답글 도착 | **3.425** | 3.317 | 3.758 | 4.209 | 답글 `message.created_at`(웹 타임라인 도착 = SSE `message.created`) |
| `message.say`(턴 종료 합본) | 4.729 | | | | 참고 |

- 20/20 `completed`, 답글 1건씩, `coalesced=false`. 1회차만 4.2s(첫 `session/new`), 이후는 `session/load`(resume) 경로 — `runtime.resume outcome=resumed` 이벤트 확인(E12-07 4필드, seq 단조).
- E12-01: 실기에서 `colab_message_post` 툴 호출마다 `session/request_permission` → `tool.permission allowed` 이벤트, optionId 비하드코딩(harness §11 acpfake 테스트 + 실기 통과).
- E12-10·11: TaskBundle `prompt` 에 `<history>`·`<trigger>`·"Post your reply with `colab message post`" 만, 브리프 [1][2][4][5][8](서버 `queue/bundle.go`) — [3][6][7] 은 P2.
- E11-08: probe 가 `runtime.capabilities` 에 저장됨(claude_code 2.1.258 logged_in, hermes 0.20.6, PONG 턴 시 models 5/43, adapter 0.74.0). `GET /runtimes/{id}` 로 확인.
- 재현: `bash e2e/p1/up.sh && N=20 bash e2e/p1/01_vertical_slice.sh` → `e2e/p1/out/a-latency.tsv`, `a-summary.json`.

### 3.2 DoD 2 — kill -9 (E11-03·04·05·06, E8-04)

`e2e/p1/02_kill9.sh` — Lead 에게 "(1) `kill9-start` 게시 (2) `sleep 210` (3) `kill9-done` 게시" 지시 → `kill9-start` 도착 + `tool.run_shell started` 확인 → 데몬 `kill -9`.

| 단계 | 관측 | EVAL |
|---|---|---|
| kill 직후 | 프로세스 그룹(pgid 79220) 에 `claude` 프로세스 1개 생존(어댑터 `node`·`colab mcp serve` 는 stdin EOF 로 함께 종료) | 고아 전제 |
| 만료 | 마지막 heartbeat +3분 → **186s 뒤** `task queued attempt=2`, `task_attempt(1).outcome=runtime_offline`, `task_token(1).revoked_at` 설정(`requeue`), `daemon_command revoke` 발행 | E11-03, E5-03 |
| 폐기 토큰 | 기록해 둔 attempt-1 토큰으로 `colab message post --body dup-after-revoke` → **exit 4 `token_revoked`**, 메시지 저장 0 | E11-04 |
| 고아의 자체 게시 | sleep 종료(+210s) 뒤 `kill9-done`(attempt 1) 게시 **0** — 401 로 막힘(고아는 그 뒤 스스로 종료) | E11-04 |
| 재시작 | `daemon run` 로그 `orphan …​.1 pgid=79220 alive=false` **claim 전** sweep → attempt 2 claim → 완료 | E11-05 (이번 실측에서는 고아가 이미 죽어 있어 kill 분기는 acpfake 테스트로만 검증됨) |
| 중복 | 세션 메시지: `kill9-start`(22:05:18, attempt 1) · `kill9-done`(22:09:28, attempt 2) **각 1건** — attempt 2 프롬프트의 `posted_message_ids` 를 따름 | E8-04 |
| workdir | `work-a/sessions/<session>/<lane>` 보존, attempt 2 가 재사용 | E11-06 |
| **수정 후 확인** | attempt 2 `finish` → 200, `lane.runtime_session_ref = {runtime_kind, session_id, …}` 저장 — 0004 적용·우회 없음 | §4 S-1 |

재현: `bash e2e/p1/02_kill9.sh`(약 9분) → `e2e/p1/out/b-summary.json`, `b-run.log`, `daemon-a.log`.

### 3.3 DoD 3 — 취소 (E11-07, E10-03, E11-01·02)

`e2e/p1/03_cancel.sh` — Lead 에게 `sleep 120` 지시 → `running` + `tool.run_shell started` → (a) `POST /lanes/{lane}/cancel` → **501 `CancelLane is not part of P1`** → (b) 데몬 SIGTERM(종료 경로 = `Cancel(reason: kill_switch)`).

| 관측 | 판정 |
|---|---|
| pgid 기록 `<root>/.colab/attempts/<task>.1.json` 존재(E11-01) → 종료 후 삭제(E11-02) | 통과 |
| 프로세스 그룹 4개(npm·node 어댑터·claude·colab mcp) → **0**, `claude-agent-acp` 프로세스 0 | E11-07 통과 |
| 취소 후 `cancel-done` 게시 0 | 통과 |
| task_event: `run_shell failed` → `runtime.error failed {detail: "session/prompt: context canceled", failure_kind: other}` → `runtime.cancel started` ; finish `outcome=failed` ; 서버 `queued attempt 2`, 토큰 폐기 사유 `requeue` | **실패** — `cancelled` 여야 한다(daemon-protocol §4.3) |
| 데몬 종료 1초 — session/cancel → 드레인 순서(harness §5) 가 실기에서 밟힌 증거 없음(`runtime.cancel started` 가 `runtime.error` **뒤**) | E10-03 의 "즉시 kill 아님" 미확인 |

원인: `daemon/internal/loop/loop.go` `Run()` 이 ctx 취소 뒤 `runner.Cancel(kill_switch)` 를 부르지만, 같은 ctx 로 도는 `runner.Run(ctx)` 의 `session/prompt` 가 먼저 `context canceled` 로 끝나 `failed(other)` 로 분류·finish 된다(취소 절차와 ctx 취소의 경합). P1 에는 서버 발행 `cancel` 명령 경로가 없어(§4 S-2) 이 종료 경로가 유일한 취소였다. 잔여 `queued attempt 2` 는 재시작 시 다시 실행된다(테스트에서는 DB 에서 `cancelled` 로 정리).

### 3.4 DoD 4 — 초대로 두 번째 멤버 (U13)

`e2e/p1/05_invite_api.sh`: owner `createInvite(role=member)` → 공개 `previewInvite`(워크스페이스명·초대자·역할) → 새 사용자 `signup(invite_token)` → 응답 `accepted_invite.role=member` → 그 사용자의 `GET /workspaces` 에 해당 워크스페이스만 → `listMembers` +1, 초대 `accepted`. 브라우저 S3 경로(`04_u1_browser.sh` U13 단계) 는 U1 4단계 실패로 도달하지 못했다(다음 실행 시 자동 포함).

### 3.5 DoD 5 — S12 페어링 실측 (E11-08)

| 경로 | 결과 |
|---|---|
| API + CLI (`01`): `createPairing` → 화면 2행과 같은 `daemon pair <code> --server http://localhost:8080` → PONG probe → `GET pairing` `ready` | **7.7s** (온보딩 경로 재실측 7.0s). `install_commands` 의 서버 URL 은 `COLAB_SERVER_URL`(:8080) — 데몬이 웹(:3000) 이 아닌 서버로 직접 가야 하므로 맞다(PR #21 N6 해소) |
| 서버 SSE (`06` (1)): 워크스페이스 스트림을 curl 로 구독 → pair → `pairing.updated` 2프레임(`connected`→`ready`) + `runtime.updated` 2프레임, 워크스페이스 필터 정상 | 통과 |
| 웹 S12 (`04` U1-4, `06` (2)): 패널이 **페어링 2개** 발급(`PairingPanel` `useEffect → create()` 가 dev StrictMode 로 두 번), 표시된 코드로 페어링하면 DB 는 `ready` 인데 패널은 **30초·120초 뒤에도 `대기 중`**(SSE/폴링 갱신 미반영, 표시 코드는 불변 → 추적 id 불일치가 아니라 갱신 경로 문제) | **실패 (W)** |

스크린샷: `web/__screenshots__/p1-u1-01~04*.png`, `p1-s12-01-waiting.png`, `p1-s12-02-after-pair.png`.

### 3.6 U1 1~13 단계 (agent-browser, 실서버) — `e2e/p1/04_u1_browser.sh`

| # | 화면 | 보이는 것(기대) | 결과 |
|---|---|---|---|
| 1 | S2 | 이름·이메일·비밀번호 + 가입 버튼, 소셜 로그인 없음 | PASS |
| 2 | S4-1 | 가입 직후 온보딩 자동 진입, 워크스페이스 이름 | PASS (건너뛰기 링크 없음 — U1 명세와 다름, 경미 W) |
| 3 | S4-2 | 설치 명령 2줄 + 복사 + `대기 중` | PASS |
| 4 | S4-2 | 명령 실행 → `대기 중→연결됨→CLI 감지 중→준비 완료` 자동 갱신, "Claude Code 감지됨·로그인됨·모델 N개" | **FAIL** — 데몬은 페어링·probe 완료(서버 `ready`), 화면은 `대기 중` 유지 (§3.5) |
| 5~6 | S4-3 | 템플릿 3장 | N/A(P2) — P1 은 Lead 1개 폼(스크립트는 판정 준비됨) |
| 7~13 | S6·S7 | goal 입력·기본값·S7 시스템 메시지·참여자 칩·답글 | **미도달**(4단계에서 중단; 스크립트는 단계별 판정 구현됨). 실서버 S7 의 첫 시스템 메시지는 `Session started. Goal: …`(영문) — `web/e2e/u1.sh` 가 기다리는 목 문구 `세션 시작 — goal` 과 다르다 |

## 4. 계약 위반·결함 (스트림별)

| ID | 스트림 | 내용 | 근거 | 상태 |
|---|---|---|---|---|
| **S-1** | S | `lane.runtime_session_ref` CHECK 가 `kind` 키 요구, 계약(harness §6·protocol.go `RuntimeSessionRef`) 은 `runtime_kind` → 데몬 finish 500 → 모든 attempt 가 `running` 으로 남고 3분 뒤 재큐잉·3회 후 failed. 서버 테스트는 `RuntimeSessionRef` nil 만 검증 | `server/migrations/0001_init.sql:250`, `server/internal/tasks/service.go:410`; 데몬 로그 `finish …: server: 500 … lane_runtime_session_ref_check` | **수정 머지 #26**(0004 + non-nil 테스트). 본 보고 §3.2 에서 확인 |
| **S-2** | S | `cancelLane` 501 — PLAN §3 P1 DoD "취소 절차" 를 서버 경로로 발동할 수 없다. 서버가 `cancel` 명령을 만드는 코드 없음 | `server/internal/httpapi/unimplemented.go`(CancelLane), `grep CmdCancel server/` 0건; PR #22 501 목록 | 미해결 (T-S1 금지 항목 "lane" 과 PLAN DoD 의 충돌 — Lead 판단 필요) |
| **S-3** | S | claim 이 `runtime_id IS NULL` 인 none 세션을 **워크스페이스 무관**하게 준다 → 다른 워크스페이스의 런타임이 task 를 실행하고 세션이 그 런타임에 고정됨(E11-10 변형). 실측: 마케팅팀 런타임이 e2e-a 세션 task 2건 실행 | `server/internal/queue/postgres.go:58` (`s.runtime_id = $1 OR (s.runtime_id IS NULL AND …)` 에 `s.workspace_id = runtime.workspace_id` 없음); 세션 `d4354413…` runtime_id = 타 워크스페이스 런타임 `d9703227…` | Lead 가 S 에 수정 지시(별도 PR). 측정은 `runtime_id` 명시로 진행 |
| **S-4** | S | 같은 호스트명을 같은 워크스페이스에 두 번 페어링하면 `INSERT runtime` 유일 제약 재시도가 **같은 tx 안**에서 → `25P02 current transaction is aborted` → `daemon pair` 500 | `server/internal/runtimes/runtimes.go:158-170`; `e2e/p1/out/f-run.log`(1차) | 미해결. savepoint 또는 사전 조회 필요. 재설치/재페어링 시나리오(U12·F1) 에 걸림 |
| **S-5** | S(설정) | `invite.url`·`install_commands` 가 `COLAB_SERVER_URL`(:8080) 기준. `make dev` 에서는 웹이 :3000 이라 초대 링크가 UI 없는 서버로 간다(설치 명령은 :8080 이 맞다) | `runtimes.go installCommands`, 초대 URL 생성 | 경미. 배포에서 같은 오리진이면 무해; 웹 URL 설정값 분리 제안 |
| **D-1** | D | 데몬 종료(SIGTERM) 경로: ctx 취소가 취소 절차보다 먼저 `session/prompt` 를 끊어 `failed(other)` 로 finish → 서버 재큐잉. harness §5 순서(session/cancel → 드레인) 미보장 | `daemon/internal/loop/loop.go` `Run()` 말미 `runner.Cancel(kill_switch)` vs `runner.Run(ctx)`; §3.3 이벤트 순서 | 미해결 |
| **W-1** | W | `PairingPanel` 이 마운트마다 `createPairing` 을 **두 번** 호출(StrictMode dev 이중 effect, `Idempotency-Key` 미사용) — 페어링 2개, 화면이 잠시 다른 코드를 보일 수 있음 | `web/components/PairingPanel.tsx:71-73`; `runtime_pairing` 같은 초에 2행(`06` (2) 재현 3/3) | 미해결 |
| **W-2** | W | S12 패널이 서버 `ready` 를 반영하지 않음(SSE `pairing.updated` 는 서버가 보냄, 5초 폴링은 `conn==="open"` 이면 중단) → U1 4단계 실패, F1 최대 이탈 지점 | `PairingPanel.tsx` `onEvent`/폴링 effect; `e2e/p1/out/f-summary.json` `panel_status_after_pair: waiting`, `paired_code_db_status: ready` | 미해결. 원인 후보: 셸 `StreamProvider` 연결이 `open` 인데 이벤트가 구독자에 안 닿음(`useWorkspaceStream` shared 경로) 또는 payload 캐스팅 |
| **W-3** | W | `web/e2e/u1.sh` 2부가 목 문구(`세션 시작 — goal`) 를 기다려 실서버(`Session started. Goal: …`) 에서 실패 예정; S4-1 건너뛰기 링크 없음(U1 2단계) | `web/e2e/u1.sh`, `web/app/onboarding/page.tsx` | 경미 |
| **X-1** | 운영 | 공용 `colab-pg` 를 여러 세션이 공유하면 `server/internal/db` 테스트의 `DROP SCHEMA` 가 실행 중 데이터를 지운다 | 06:32 실측 | E2E 는 전용 컨테이너 사용(`e2e/p1/lib.sh` 기본값) |

계약 문서 자체의 결함은 발견하지 못했다(S-1 은 스키마가 계약보다 먼저 쓰인 불일치, #26 으로 스키마를 계약에 맞춤).

## 5. 재현 명령

```sh
# 전제: docker, go 1.25, node 22, agent-browser, jq, claude 로그인, npx 캐시(어댑터 0.74.0)
bash e2e/p1/up.sh                     # Postgres(colab-pg-e2e:5435) + migrate + bin/* + server :8080 + web :3000
N=20 bash e2e/p1/01_vertical_slice.sh # (a) 20회, 페어링 실측, 데몬 a 기동 → out/a-summary.json
bash e2e/p1/03_cancel.sh              # (c) 취소 → out/c-summary.json (데몬 a 를 SIGTERM 으로 종료)
bash e2e/p1/02_kill9.sh               # (b) kill -9, 약 9분 → out/b-summary.json
bash e2e/p1/05_invite_api.sh          # DoD 4 (API)
bash e2e/p1/06_s12_pairing_realtime.sh# S12: 서버 SSE vs 웹 패널 분리 재현 (에이전트 턴 없음)
bash e2e/p1/04_u1_browser.sh          # (d) U1 1~13 + U13 브라우저 → web/__screenshots__/p1-u1-*.png, out/d-steps.tsv
bash e2e/p1/down.sh                   # 데몬·서버·웹 종료
```

산출물은 `e2e/p1/out/`(git 제외 — 토큰·쿠키 포함). 이번 실행의 요약 JSON 값은 §1·§3 표에 그대로 옮겼다.

## 6. EVAL 제안 행 (Lead 지시)

| 제안 ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E8-13 | attempt 가 `runtime_session_ref` **non-nil** 로 finish | finish | 200, `lane.runtime_session_ref` 저장(계약 키 `runtime_kind`·`session_id`), 다음 attempt 의 TaskBundle.resume 에 실림 | unit + e2e |
| E11-11 | 워크스페이스 A 의 none 세션(`runtime_id` NULL), 워크스페이스 B 의 런타임이 claim | claim | **주지 않음**. 고정은 같은 워크스페이스 런타임만 | unit |
| E11-12 | 같은 호스트명의 데몬을 같은 워크스페이스에 재페어링 | pair | 201, 이름에 접미어(`-2`) — 500 아님 | unit |
| E10-13 | 데몬 프로세스 SIGTERM(정상 종료) 중 running attempt | 종료 | harness §5 순서로 취소, finish `outcome=cancelled`(재큐잉 아님), 프로세스 트리 0 | harness + e2e |
| E17-09 | 웹 S12 패널, 실서버 | `daemon pair` | 패널이 10초 안에 `준비 완료`, 페어링 발급 1건 | e2e(agent-browser) |
