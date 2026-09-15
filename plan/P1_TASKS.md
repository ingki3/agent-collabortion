# P1 작업 분해 — 수직 슬라이스 (→ G3)

| 항목 | 내용 |
|---|---|
| 근거 | `PLAN.md` §3 P1, §10.3 작업 단위 형식, `EVAL.md` E11·E12·E17, `EVAL_USER.md` U1·U13 |
| 전제 | G2 — `contracts/` 6종 머지(`harness.md` v0.2, `daemon-protocol.md`, `task_event.schema.json`, `colab-cli.md`, `clock/`, `protocol.go`, `openapi.yaml`) |
| 목표 (G3) | 웹에서 `@Lead 인사해줘` → 2초 안에 데몬 claim → Claude Code(ACP)가 실행 → `colab message post`로 답글 → 웹 타임라인에 도착. **데몬 `kill -9` 후 재큐잉·고아 정리·폐기 토큰 거부로 중복 게시 0.** 취소 시 프로세스 트리 잔존 0. E2E 20회 첫 출력 중앙값 ≤ 10초 |
| 배정 | S·D·C·W **동시** fan-out(각자 워크트리, PR 하나). Reviewer = Hermes(Orca 터미널), Integrator = 별도 worker(4개 머지 후) |
| 예산 (PLAN §6.2 G3) | `blocked` ≤ 10, PR ≤ 30, PR당 리뷰 반려 ≤ 3 |

**모델(2026-09-06 Director 결정)**: worker는 **Opus 5**로 띄운다 — `orca orchestration worker-start … --agent claude --model opus`. Fable 한도가 먼저 소진돼 작업이 멈추기 때문이다. 실행 중인 worker는 터미널에 `/model opus` → 확인 `1`로 전환한다.

공통 규칙 (모든 작업): **Orca 워크트리 브랜치는 main 기준으로 생긴다 — 작업 시작 전에 반드시 `git fetch origin dev && git checkout -b <feature-branch> origin/dev`로 갈아타라(contracts/·server/·EVAL.md가 없으면 갈아타지 않은 것이다).** PR은 `GH_PROMPT_DISABLED=1 gh pr create --repo ingki3/agent-collabortion --base dev --head <branch> --title … --body-file <file>`(프롬프트에서 멈추는 것 방지). dev에서 feature 브랜치 → PR to dev. **`contracts/` 수정 금지** — 계약 결함은 `orca orchestration ask`로 Lead에게(Director 승인 PR로만 바뀐다). 남의 스트림 디렉토리 수정 금지. 테스트 파일은 자기 스트림 안에서만. `.github/` 수정은 CI 잡 추가에 한해 허용(PR 본문에 명시). 한도(rate limit)에 걸리면 기다리지 말고 지금까지 결과로 PR을 열고 `worker_done --outcome failed`에 리셋 시각을 적어라. 완료는 `worker_done`(PR URL, 테스트 결과, 계약과 다르게 한 것·발견한 계약 결함).

---

## T-S1 · 서버 API 코어 + 큐 + 라우터 2규칙 + 토큰

```
작업: P1 서버 — 수직 슬라이스에 필요한 REST/데몬 API·큐·라우터(규칙 2·6)·task 토큰·실시간
스트림: S
입력: contracts/openapi.yaml(P1 범위 operation — 인증·워크스페이스·멤버·초대·에이전트(프로파일 1개)·런타임·세션 생성(none)·메시지·task·실시간), contracts/daemon-protocol.md 전부, contracts/task_event.schema.json, contracts/protocol.go, contracts/clock, server/migrations/0001_init.sql, PRD FR-3.3 규칙 2·6, FR-7.1, FR-9.1, EVAL E5-01·02·03, E8-04, E11-03·04·09·10, E17-01
출력: PR 하나 (branch feat/server-p1)
  - server/migrations/0002_p1_auth_and_stream.sql (G2 Q1): app_user.password_hash, user_session, workspace_invite, runtime_pairing, session_subscription, member.notification_settings(jsonb), artifact_review, idempotency_key(키·요청 해시·응답·만료), stream_event(SSE 백필 10분 창). session.isolation jsonb에 remote_url 키(Q2). 0001은 수정 금지.
  - server/internal/httpapi: openapi.yaml에서 타입·라우터 생성(oapi-codegen 권장, 생성물 커밋). P1 범위 밖 operation은 501. getCliContext 포함(TaskToken 범위 = G2 Q8).
  - server/internal/auth: 이메일+비밀번호(argon2id), 세션 쿠키 또는 Bearer, 워크스페이스·멤버·초대 링크(S3).
  - server/internal/agents: CRUD, 프로파일 1개.
  - server/internal/runtimes: 페어링 코드 발급(S12) → /v1/daemon/pair, probe 저장, 목록.
  - server/internal/sessions: 생성(none 격리, participants, assignee), get, messages 목록.
  - server/internal/router: FR-3.3 **규칙 2(명시 멘션→task, 중복 병합, 비참여자 경고)와 6(그 외 사용자 메시지→assignee)만**. 테이블 테스트(E1-02·03·04·11). 규칙 1·3·4·5·7·8은 P2.
  - server/internal/queue: daemon-protocol §7 Queue 인터페이스 + Postgres SKIP LOCKED 구현. claim long-poll(≤30s), 세션 runtime_id 고정(E11-10), 다른 런타임 거부(E11-09), paused 세션 제외.
  - server/internal/tasks: 상태 머신 queued→dispatched→preparing→running→completed/failed, ExpireStale(dispatched 5분→timeout, heartbeat 3분→runtime_offline) — **contracts/clock 주입**, Fake 클럭 테스트(E5-02·03).
  - server/internal/tokens: ctk_ 발급(해시 저장)·검증·**재큐잉/취소/완료 시 폐기** + revoke 명령 큐(E11-03·04). 폐기 토큰의 CLI 호출 401 token_revoked.
  - server/internal/events: /events 배치 수신, (task_id, attempt, seq) 멱등, accepted_seq_max, 스키마 검증.
  - server/internal/realtime: WS 또는 SSE 하나 — 메시지·task 상태·task_event를 세션 구독자에게.
  - colab CLI용 3 op: session get / session messages / message post(Idempotency-Key, TaskToken 인증).
  - server/cmd/server 배선, make dev로 기동.
DoD: cd server && go vet ./... && go test ./... (COLAB_TEST_DB_URL로 통합 포함) 통과. 테스트 필수: 라우터 2규칙 테이블, claim 격리 2건, ExpireStale Fake 클럭 2건, 토큰 폐기→401, events 멱등(같은 seq 재전송 → 1건), 메시지 멱등키. CI 초록. PR 본문에 구현한 operation 목록과 501 목록.
금지: 라우터 규칙 2·6 외 구현, lane/합류/HITL/예산(P2·P3), contracts/·daemon/·cli/·web/ 수정, 마이그레이션 변경(필요하면 0002_*.sql 추가 — 기존 파일 수정 금지, PR 본문에 사유).
막히면: orca orchestration ask. openapi.yaml과 daemon-protocol.md가 충돌하면 daemon-protocol이 우선이고 그 사실을 ask로 알려라.
```

## T-D1 · 데몬 — 페어링·claim·하네스 핵심·고아 정리

```
작업: P1 데몬 — 페어링/probe, claim 루프, none workdir, 브리프 조립, ACP 하네스 핵심, heartbeat, 고아 정리
스트림: D
입력: contracts/harness.md v0.2 **전부**, contracts/daemon-protocol.md 전부, contracts/task_event.schema.json, contracts/protocol.go, contracts/clock, daemon/internal/acpprobe(재사용·승격 대상), plan/spikes/SPIKE_01.md §6·SPIKE_01b.md §8, PRD §8.4 브리프 [1]~[8], FR-9.1, EVAL E11-01~07, E12-01·02·04·07·08·09·10·11
출력: PR 하나 (branch feat/daemon-p1)
  - daemon/internal/api: 서버 클라이언트(pair, probe, claim, phase, events 배치+재전송, heartbeat 15s, finish). 명령(cancel/revoke/probe) 처리.
  - daemon/internal/probe: claude·hermes 감지, 버전, 로그인 여부, 어댑터 핀 확인, 능력 광고(harness §9). PONG 1턴.
  - daemon/internal/workdir: none 격리 — 세션 작업 루트 아래 lane 하위 폴더, reuse. worktree는 P4.
  - daemon/internal/brief: [1]~[8] 조립(서버가 TaskBundle.brief.text를 주면 그대로; 데몬은 transport만 처리) — claude_code: _meta.systemPrompt.append; hermes: AGENTS.md 마커(추적 파일 처리는 P4, P1은 none이라 파일 생성만).
  - daemon/internal/harness/acp: acpprobe 클라이언트를 승격. spawn(pgid·허용 목록 env §2.1), initialize(v1 검사→config), session/new·load(+_meta 매번), **set_config_option 모델(new/load 뒤 항상)**, 권한 정책(§4), 취소 절차(§5), 재개(§6, provenance), 이벤트 정규화(§7, 리플레이 버림, model_drift, rate_limit), 오류 분류(§8 우선순위), Hermes 250ms 대기, stall 3분.
  - daemon/internal/orphan: pgid 파일 기록/삭제, 시작 시 claim 전 정리(E11-05), revoke 명령 시 종료.
  - daemon/internal/acpfake: 가짜 ACP 서버(스크립트 응답) — harness §11 계약 테스트 전부.
  - daemon/cmd/daemon: `daemon pair <code> --server <url>`, `daemon run`(claim 루프), `daemon version`. 설정 ~/.colab/daemon.json.
DoD: cd daemon && go vet ./... && go test ./... 통과. acpfake 계약 테스트 harness §11 목록 전부 + §12 P1 추가 항목 (a)(b)(c). 실제 어댑터 스모크는 COLAB_SMOKE=1일 때만(1턴 PONG, claude·hermes). CI 초록. PR 본문에 §11 표 대비 통과 목록.
금지: worktree(P4), 예산 강제(P3), contracts/·server/·cli/·web/ 수정. 서버 없이 테스트하도록 서버 클라이언트는 인터페이스 뒤에(httptest).
막히면: orca orchestration ask. 어댑터 동작이 harness.md와 다르면 계약 결함이다 — 우회 구현하지 말고 ask.
```

## T-C1 · colab CLI + MCP — 3개 명령

```
작업: P1 colab CLI — session get / session messages / message post + MCP 서버
스트림: C
입력: contracts/colab-cli.md 전부, contracts/openapi.yaml의 x-colab-cli operation 3개, contracts/protocol.go, EVAL E11-04, E15-04, E8-04(멱등키)
출력: PR 하나 (branch feat/cli-p1)
  - cli/internal/client: 서버 HTTP 클라이언트(TaskToken Bearer, COLAB_* env 기본값, Idempotency-Key = task:attempt:seq 자동, 종료 코드 0/2/3/4/5, --json).
  - cli/cmd/colab: `session get`, `session messages [--since --limit --thread]`, `message post --body [--reply-to --mention]`, `version`, `mcp serve`.
  - cli/internal/mcp: stdio MCP 서버 — colab_session_get / colab_session_messages / colab_message_post (같은 JSON 인자·반환). 공식 MCP Go SDK 또는 최소 JSON-RPC 구현.
  - 문서: cli/README.md에 에이전트용 사용 예(브리프 [2]에 들어갈 문장 초안).
DoD: cd cli && go vet ./... && go test ./... 통과. httptest 테스트: 토큰 없음→4, 401 token_revoked→4, 멱등키 재전송 동일 응답, 응답 triggered/suppressed 파싱, 종료 코드. MCP 서버는 initialize/tools.list/tools.call 왕복 테스트. CI 초록.
금지: 다른 명령(P2·P3), contracts/·server/·daemon/·web/ 수정.
막히면: orca orchestration ask.
```

## T-W1 · 웹 — 셸·인증·최소 화면·S7 중앙·실시간

```
작업: P1 웹 — S1·S2·S3, 앱 셸(App Nav), S5 최소, S6 최소, S7 중앙 열, S11 최소, S12, 실시간
스트림: W
입력: SCREEN.md §2·§4.1·§4.2(S12 인라인)·§4.3(S5 최소 정의: 상태 배지+제목+새 세션)·§4.4(S6 — P1은 goal 입력 + 기본값 통과만)·§4.5(S7 중앙: 타임라인·작성창 멘션 자동완성·활동 피드 원본 레일)·§4.8(S11 카드 최소·S12 4단계)·§6 실시간·§7 빈 상태, COMPONENTS.md(Badge 사용, Message Card·Agent Chip·App Nav 정의), web/components/Badge, contracts/openapi.yaml, contracts/task_event.schema.json(원본 레일 표시), EVAL_USER U1 1~13단계·U13, EVAL E1-04(비참여자 경고 표시)
출력: PR 하나 (branch feat/web-p1)
  - openapi에서 TS 클라이언트 생성(openapi-typescript + fetch 래퍼), 인증 상태·워크스페이스 컨텍스트.
  - 화면: S1 로그인, S2 회원가입, S3 초대 수락(워크스페이스 있으면 S4 건너뛰고 S5), 앱 셸(좌측 내비: Sessions·Inbox(뱃지 자리)·Agents·Runtimes·Settings(권한 있을 때만)), S5 최소, S6 최소(제목·goal, 나머지 기본값, 런타임 없으면 "먼저 컴퓨터를 연결하세요"), S7 중앙 열(타임라인: Message Card — 작성자·본문·스레드 접기 / 작성창: @ 자동완성, 비참여자 경고 칩 / 활동 피드 원본 레일: task_event를 시간순 텍스트로), S11 최소(카드: 이름·온라인·CLI 목록·마지막 접속), S12(설치 명령 2줄 복사 + 4단계 상태 실시간, 3분 후 문제 해결 안내 펼침).
  - 실시간: 서버 WS/SSE 구독 → 타임라인·피드·S12 상태 갱신. 새로고침 없이.
  - 컴포넌트: Message Card, Agent Chip, App Nav를 COMPONENTS.md대로 React 컴포넌트로(스토리 페이지 /dev/components에 추가).
  - E2E: web/e2e/u1.sh — agent-browser 스크립트로 U1 1~13단계(가입→온보딩 최소→S12→세션 생성→S7). 서버는 make dev, 데몬은 스크립트가 띄우지 않고 "S12 대기" 단계까지 검증 + 데몬 페어링 후 나머지(Integrator가 실행).
DoD: npm run typecheck && npm test && npm run build 통과. 컴포넌트 테스트 3개(Message Card 스레드, Agent Chip 파생 상태 표시, 작성창 비참여자 경고). agent-browser 스크립트가 make dev 서버에 대해 S12 대기 단계까지 통과(스크린샷 web/__screenshots__/u1-*.png). CI 초록.
금지: S7 좌·우열, S8, S9/S10 편집, 설정(P2 이후). contracts/·server/·daemon/·cli/ 수정. 디자인 토큰 값 변경.
막히면: orca orchestration ask. openapi에 화면이 필요한 필드가 없으면 ask(계약 결함).
```

## T-I1 · Integrator — G3 판정 자료

```
작업: P1 통합 — 네 PR 머지 후 E2E와 G3 DoD 검증, 보고서
역할: Integrator (기능 추가 금지, 검증만)
입력: PLAN.md §3 P1 DoD, EVAL E11·E12·E17-01·02, EVAL_USER U1, 위 네 작업의 PR 본문
출력: PR 하나 (branch test/p1-integration) — e2e/p1/: 스크립트(make dev → 데몬 페어링 → 에이전트 Lead 생성 → 세션(none) → "@Lead 인사해줘" → 답글 도착 대기 → 20회 반복 중앙값), kill -9 시나리오(running 중 데몬 kill → 3분 → 재큐잉·고아 종료·폐기 토큰 401·중복 게시 0), 취소 시나리오(프로세스 트리 0), agent-browser로 U1 전체(스크린샷). plan/G3_REPORT.md: DoD 항목별 통과/실패·수치·재현 명령.
DoD: 보고서에 P1 DoD 5항목 각각 판정. 실패 항목은 어느 스트림 결함인지 지목(수정은 해당 스트림 재지시).
금지: 구현 코드 수정.
```

## Reviewer (Hermes)

Orca에 hermes 에이전트 타입이 없으므로 `orca terminal create --command 'hermes chat'`로 띄우고 `terminal send`로 리뷰 프롬프트를 보낸다. 각 PR마다: (1) 계약 준수 — contracts/ 문서와 diff 대조, (2) 테스트 존재·통과, (3) 범위 밖 변경, (4) `gh pr review <n> --comment -b <결과>`. 반려 3회면 Director. Hermes 응답이 Orca 메일로 오지 않으므로 Lead가 터미널을 읽는다.

## 순서

1. 네 작업 동시 fan-out(워크트리 `p1-server` `p1-daemon` `p1-cli` `p1-web`, trust 처리 선행).
2. 각 PR: Hermes 리뷰 → Lead 확인 → 머지. **S가 먼저 머지되어야 Integrator가 시작할 수 있고**, D·C·W는 S 없이도 테스트 가능(httptest·acpfake).
3. 네 개 머지 → T-I1 → `plan/G3_REPORT.md` → G3 판정(Director).
