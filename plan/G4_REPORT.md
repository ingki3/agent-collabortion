# G4 판정 자료 — P2 중간 게이트: 시나리오 A **Claude Code 단일 런타임** 통합 보고 (T-I2 1부)

| 항목 | 내용 |
|---|---|
| 작성 | Integrator (T-I2 1부). **1판** 2026-09-06 14:30~15:50 KST · **2판**(웹 재실행) 16:10~ KST |
| 대상 | **2판**: `origin/dev` `950b02a` (1판 대상 `9bb4ce9` + T-S4 서버 #75 · 웹 #76). 1판 대상은 서버 P2 #62 · 아티팩트 #65 · CLI #61 · 웹 #67 · 데몬 #54 + 핫픽스 #71 |
| 2판 범위 | **웹 절반만 다시 돌린다**(`11_scenario_a_web.sh` · `12_mock_vs_real.sh` · P1 회귀). API/CLI 경로(§3.1)는 1판 수치를 그대로 둔다 — #75·#76 은 그 경로를 바꾸지 않았고, 재실행은 에이전트 턴을 다시 태운다 |
| 실제 런타임 | Claude Code 2.1.258(로그인됨) · 어댑터 `@agentclientprotocol/claude-agent-acp@0.74.0`(핀) · macOS arm64 |
| 에이전트 모델 | `claude-haiku-4-5-20251001` (비용 지침, `LEAD_MODEL` 로 덮어쓰기) |
| 바이너리 | 1판 **15:21:33** 빌드(dev `9bb4ce9`, 커밋 14:59:24) · 2판 **16:10:49** 빌드(dev `8d9c7a8` = `950b02a` 머지, 커밋 16:0x) — 둘 다 대상 HEAD **이후**. `e2e/p2/up.sh` 가 매 실행마다 `make build` 한다 |
| 스택 | 전용 Postgres `colab-pg-g4`(:5436) · `bin/server` :8090 · `next dev` :3010. **P1 스택(:8080/:3000/:5435)과 포트를 분리**했다 — 다른 워크스페이스의 P1 스택이 동시에 돌고 있어 같은 포트를 잡을 수 없다 |
| 스크립트 | `e2e/p2/` (재현 명령은 §6). **CI 에서 실행하지 않는다**(실제 런타임·로그인 필요) |
| 판정 근거 | PLAN.md §3 P2 DoD·§6.2 G4, plan/P2_TASKS.md §3 T-I2 1부, PRD 시나리오 A(§4), EVAL E1~E6·E15-02, EVAL_USER U2·U4·U5·U15 |

## 1. 판정 요약

| # | G4 항목 (TASK §2) | 판정 | 수치 | 결함 스트림 |
|---|---|---|---|---|
| 1 | Lead 가 깨어난 횟수 = 3 | **통과** | **3** — 세션 시작 1 · J1(Researcher 3) 합류 1 · J2(Writer) 합류 1. 셋 다 `completed`. **구성은 "시작 1 + 합류 2"** 다 — TASK 가 적은 "통보"는 별도 트리거가 아니라 **J2 합류**이고, Writer 의 `@Lead` 멘션은 규칙 8 로 억제돼 task 를 만들지 않는다(합류 전에 도착했을 때). 위임 자체는 Lead 의 **첫 턴 안에서** 일어나므로 깨어남을 만들지 않는다 | — |
| 2 | Researcher lane 3개가 실제로 **동시에** running | **통과** | lane **3**개, 동시 running 최대 겹침 **3** (task `started_at`~`finished_at` 스윕) | — |
| 3 | 합류가 정확히 1회 — 묶음에 3개 결과 + 억제된 자식 메시지 | **통과** | 합류 발화 2건(J1 자식 3 · J2 자식 1), 합류 시스템 메시지 **그룹당 1개**. 합류 턴 프롬프트에 자식 메시지 **3/3** 실림(E1-21). 합류 전 Researcher 의 `@Lead` 멘션이 만든 Lead task **0개**(E1-15) | — |
| 4 | Writer 의 `artifact submit` 201, 다운로드 바이트 = Content-Length | **통과** | `submitArtifact` 201 **1건**, 다운로드 **1439 B = Content-Length 1439**, 본문 일치 | — |
| 5 | 종료 조건 진행률이 `artifact_submitted` 를 반영 | **통과** | `completion_progress` = `met 1 / total 2`, `artifact_submitted.met=true` · `user_approval.met=false` · `satisfied=false` · `human_gate=true`, 세션 `active` 유지(E6-01) | — |
| 6 | `previewTriggers` 가 작성창에 **서버 값**으로 뜬다 | **통과** (2판) | 웹 작성창 칩 = `@Researcher를 트리거합니다 · 명시 멘션(규칙 2) · claude-haiku-4-5-20251001`, 같은 본문의 서버 `previewTriggers` = `Researcher` / 규칙 2 / 프로파일 `default`. 규칙 번호·프로파일·모델은 로컬에서 만들 수 없는 값이다. (1판: S7 이 안 열려 미확인) | — |
| 7 | 웹(agent-browser)으로 U2·U4·U5 여정 한 번 | **부분** (2판) | S6 마법사 7단계 전부 통과하고 **세션이 만들어져 S7 이 열린다**(1판의 S-1·S-2 해소). U2-1 lane 보드는 아직 아니다 — 카드가 3장 동시 running 으로 보이지 않고(**S-9**) 브리프 줄이 없다(**S-10**). 참여자 칩은 e2e 가 식별할 수단이 없다(**W-3**). W9 이후(U15-3·U2-5·U2-6·U5-1)는 미도달 | **S**·**W** |
| 8 | API/CLI 로 한 번 | **통과** | `e2e/p2/10_scenario_a_api.sh` — 체크 **31개 중 PASS 29 · FAIL 2**(FAIL 2 = §4 S-4·S-5). 세션 전체 소요 **93초**, 에이전트 턴 7 | — |

**G4 관점 요약.** 협업 코어의 결정적 부분 — 위임 → lane 3개 병렬 → **합류 정확히 1회** → 종합 → Writer 제출 → 종료 조건 반영 — 은 실제 Claude Code 런타임에서 **끝까지 돈다**. 즉 PLAN §6.2 G4 의 "시나리오 A Claude Code 단일 런타임" 은 **API/CLI 경로에서 통과**다. 반면 **사람이 보는 경로는 통과가 아니다**: 서버가 계약과 다른 모양을 주는 한 필드(`listDecisions`)가 S7 세션 화면 전체를 죽이고(**S-1**), lane 보드·런타임 후보·팀 템플릿이 실서버에서 501 이라(**S-2·S-3**) U2 여정이 성립하지 않는다. 이 넷은 전부 **서버 쪽 좁은 수정**이고 웹 코드와 무관하다.

## 2. 환경과 전제

- 구성은 `make dev` 와 같지만 **포트를 옮겼다**(§1 표). 다른 워크스페이스의 P1 스택이 :8080/:3000 을 잡고 있어 같이 띄울 수 없다. `e2e/p2/lib.sh` 가 `SERVER_URL`·`WEB_URL`·`PG_PORT`·`PG_CONTAINER` 를 덮어쓰고, 나머지는 `e2e/p1/lib.sh` 를 그대로 재사용한다.
- **모든 바이너리는 매 실행마다 다시 빌드**한다(`e2e/p2/up.sh` 의 `make build` + 빌드 시각 출력). 오래된 `bin/daemon` 으로 돌리면 P2 operation 이 501 로 보인다(P1 리뷰어가 07 에서 겪은 함정).
- **픽스처 주의(P1 에서 밟은 것)**: 세션 `goal`·브리프에 이 저장소의 파일·스크립트 이름을 쓰지 않았다. 과제는 저장소 밖의 무해한 주제 — "가상의 스마트 물병 제품 X 의 시장 조사 요약 3항목". `runtime_id` 는 항상 명시하고 워크스페이스 이름은 ASCII 다.
- **claim 탭(테스트 픽스처)** `e2e/p2/fixtures/claimtap.py` — 데몬↔서버 사이의 역방향 프록시로 claim 응답(`TaskBundle`)을 JSONL 로 남긴다. E1-21("합류 묶음 페이로드에 자식 메시지가 실린다")은 **서버가 데몬에 보내는 턴 프롬프트**를 봐야만 증명되는데 그 프롬프트는 디스크에 남지 않기 때문이다(claude_code 는 `_meta.systemPrompt` + `session/prompt` 로 전달). 구현 코드는 무수정. 데몬 config 의 `server` 만 프록시를 가리킨다.
- **에이전트 지시문이 판정의 일부다**(`e2e/p2/fixtures/scenario_a_agents.sh`). 두 가지가 수치를 바꾼다. 둘 다 시나리오 정의이지 플랫폼 결함이 아니지만, 두 번째는 **플랫폼 쪽 함정**을 드러낸다(§5 관찰 1).
  1. Lead 는 종합·마무리에서 아무도 멘션하지 않는다. 멘션하면 규칙 3 재진입으로 lane 이 한 번 더 돌아 Lead 깨어난 횟수가 4가 된다(1차 실행 실측).
  2. 자식(Researcher·Writer)은 `status set done` 을 **턴의 마지막 도구 호출**로 한다. `done` 뒤에 한 줄이라도 더 올리면 규칙 8 억제가 이미 풀려 있어 위임자가 한 번 더 깨어난다(2차 실행 실측).

### 2.1 핫픽스 #71 — 우회 실행과 정식 실행

첫 실행에서 **모든 `claude_code` task 가 `failure_kind=auth`** 로 죽었다: `Failed to authenticate: OAuth session expired and could not be refreshed`. 원인을 격리했다 — `daemon/internal/harness/acp/env.go` 의 허용 목록이 `{PATH, HOME, LANG, TMPDIR}` 뿐이라 macOS 에서 만료된 OAuth 를 **갱신**할 때 필요한 `USER` 가 빠져 있었다.

```
env -i PATH=… HOME=… LANG=… TMPDIR=… claude -p 'PONG'          → 인증 실패
env -i PATH=… HOME=… LANG=… TMPDIR=… USER=… claude -p 'PONG'   → 정상 응답
```

P1 에서는 액세스 토큰이 살아 있어 갱신이 필요 없었으므로 드러나지 않았다. 즉시 보고했고(escalation 15:0x), Lead 가 PR #71(dev `9bb4ce9`)로 `USER` 를 허용 목록에 넣고 CLI 에 `--version` 을 붙였다.

| 실행 | 시각 | 조건 | 결과 |
|---|---|---|---|
| 우회 실행 | 15:00 | 프로파일 `env` 에 `USER` 를 넣어 우회(구현 무수정) | PASS 26 / FAIL 2 — Lead 깨어난 횟수 3, 나머지 §1 과 같음 |
| **정식 실행** | 15:21 | #71 머지 후 재빌드, 우회 **제거**(`PROFILE_ENV={}`) | **PASS 29 / FAIL 2** — §1 의 판정 수치는 전부 이 실행 |

정식 실행에서 `failure_kind=auth` task **0건**, 데몬 로그의 `colab --version failed` **0건**이다. 다만 **`colab_cli` 는 API 에 여전히 실리지 않는다** — 데몬은 이제 값을 만들지만 서버가 버린다(§4 **S-5**).

## 3. 항목별 상세

### 3.1 시나리오 A — API/CLI 경로 (`e2e/p2/10_scenario_a_api.sh`)

`가입 → 워크스페이스 → 페어링 → daemon pair/run → 에이전트 3(Lead·Researcher·Writer, 전부 claude_code) → 세션(격리 none, 종료 조건 artifact_submitted(Writer) AND user_approval, runtime_id 명시) → 자동 트리거` 뒤는 전부 에이전트가 `colab_*` MCP 도구로 한다.

관측된 흐름(정식 실행, 서버 DB 단일 클럭):

| 시각(상대) | 사건 |
|---|---|
| +0s | 세션 생성 → Lead task **1** (`system: Session started. Goal: …`) |
| +9~12s | Lead 가 `colab lane delegate` **3회** → Researcher lane **3개**(각 `delegated_from_task_id` = Lead task 1) + workdir 디렉토리 3개 |
| +12~25s | Researcher lane 3개 **동시 running**(최대 겹침 **3**). 각각 `@Lead` 멘션으로 결과 게시 → **Lead task 0개**(규칙 8 억제, E1-15) |
| +26s | 세 lane 이 `done` → **합류 1회** — 시스템 메시지 `위임한 작업이 모두 끝났습니다.` 1개 + Lead task **2**. 그 턴 프롬프트에 자식 메시지 **3/3** 실림(E1-21) |
| +26~40s | Lead 종합 게시(멘션 없음) → `colab lane delegate` 1회 → Writer lane(J2) |
| +40~70s | Writer 가 파일 작성 → `colab artifact submit` **201** → `@Lead` 게시 → `status set done` |
| +70s | J2 합류 → Lead task **3** → 마무리 게시 + `status set done` |
| +93s | 활성 task 0. `completion_progress` = 1/2 |

체크 31개 중 통과 29. 전체 표는 `e2e/p2/out/a-checks.tsv`, 요약 JSON 은 `e2e/p2/out/scenario-a.json`, 합류 프롬프트 원문은 `e2e/p2/out/a-join-prompt.txt`.

### 3.2 시나리오 A — 웹 경로 (`e2e/p2/11_scenario_a_web.sh`)

**2판**(dev `950b02a`, T-S4 #75·#76 이후). 1판에서는 S6 4단계에서 후보가 0이고 S7 이 첫 렌더에서 죽어 **U2-1 이후 전부 미도달**이었다. 지금은 마법사가 끝까지 가고 세션이 만들어지며 S7 이 열린다.

| # | 판정 대상 | 1판 | 2판 | 근거 |
|---|---|---|---|---|
| W1 | S6 마법사 7단계 · 제목·goal | 통과 | **통과** | `wizard-steps` 7 |
| W2 | 4단계 런타임 후보에 방금 연결한 컴퓨터 | 실패(S-2) | **통과** | 후보 **1**, 오류 문구 없음 |
| W3 | 5단계 참여자 3명 + assignee=Lead | 통과 | **통과** | `participant-option` 3 |
| W4 | 6단계 종료 조건 기본값 | 통과 | **통과** | `☑ 아티팩트 제출 (assignee) … 모두 충족 (AND)` |
| W4b | 마법사 '시작' 이 세션을 만든다 | 미측정 | **통과** | 세션 `bef082c8-…`, S7 로 이동 |
| W5 | lane 보드 카드 3장이 **동시에** running (U2-1) | 미도달(S-1) | **실패** | 화면에서 본 최대 동시 running **1**. **DB 는 3이다** — §아래 |
| W6 | 각 카드에 브리프 한 줄 (U2-1) | 미도달 | **실패** | `lane-brief` **0**. `listLanes` 응답에 `brief` 키가 아예 없다 |
| W7 | Researcher 칩 `working` · Lead 칩 `idle` (U2-3·E5-11) | 미도달 | **실패(테스트 훅)** | 셀렉터가 아무것도 못 잡았다 — `AgentChip` 이 `data-agent-id` 를 달지 않는다 |
| W8 | 작성창 미리보기 칩이 **서버 값**인가 (U4-1·FR-3.6) | 미도달 | **통과** | 칩 `@Researcher를 트리거합니다 · 명시 멘션(규칙 2) · claude-haiku-4-5-20251001` = 서버 `previewTriggers`(agent `Researcher` · rule 2 · profile `default`) |
| W9~W15 | U15-3 `@all` · U2-5 합류 1회 · U2-6·U5-1 진행률 · 아티팩트 행 · HITL | 미도달 | **미도달** | 스크립트가 W9 에서 멈췄다(§아래) |

**W5 는 폴링이 놓친 것이 아니다.** 같은 세션의 서버 DB 를 보면 Researcher task 3개가 실제로 겹쳤다:

| task | `started_at` (UTC) | `finished_at` (UTC) |
|---|---|---|
| `e526d307…` | 07:33:12.729 | 07:33:27.616 |
| `3debeeaa…` | 07:33:13.276 | 07:33:31.560 |
| `ed9ef8f8…` | 07:33:13.984 | 07:33:35.414 |

셋이 동시에 `running` 인 구간은 **07:33:13.98 ~ 07:33:27.62 = 약 13.6초**이고 스윕으로 센 최대 겹침은 **3**이다. 2초 간격 폴링이 13.6초를 놓칠 수 없다. 원인은 **서버가 lane 이 `running` 으로 바뀔 때 `lane.updated` 를 내보내지 않는 것**이다 — `publishLane` 호출처는 delegate·cancel(`handlers_lanes.go`)과 finish(`daemon.go`)뿐이라, 보드는 **초기 로드 상태에서 lane 이 끝날 때만** 갱신된다. 사람 눈에는 "3장이 순서대로 하나씩 도는" 것처럼 보인다(**S-9**).

**W9 에서 멈춘 이유는 제품이 아니라 스크립트다.** `@all` 미리보기 칩(`chip-no-trigger`)을 기다리는 `wait_sel` 이 가드 없이 있어 `set -e` 가 스크립트를 끝냈다. 그 EXIT 트랩이 데몬을 내리는 바람에 **Writer lane 이 `failed`, 그 task 가 `cancelled`** 로 남았다 — 결함이 아니라 teardown 이다. 2판 재실행 전에 브라우저 읽기(`abget`)와 대기(`|| true`)를 전부 실패 허용으로 고쳤다.

### 3.3 목 API vs 실서버 (`e2e/p2/12_mock_vs_real.sh`)

`web/e2e/p2-mock.sh` 를 **BASE_URL 만 바꿔** 실서버에 돌렸다(T-W2 가 그렇게 돈다고 한 방식). 목에는 런타임 1대·에이전트 3명이 미리 있으므로 실서버에도 같은 출발선을 만들어 준다 — 그러지 않으면 첫 행에서 무너져 뒤 30행이 전부 빈 문자열이 되고 진짜 갈린 지점이 안 보인다.

목 **SMOKE PASS(29행)** · 실서버 **SMOKE FAIL** · 갈리는 행 **21**. 원인별로 묶으면:

| 원인 | 갈리는 행 | 스트림 |
|---|---|---|
| `listAgentTemplates`·`applyAgentTemplate` 501 (x-phase **P2**) | agent-templates 3종 · 매핑 status · dev_team 적용 3명 (+ 이어서 프로파일 2행이 빈 경로로 301) | **S-3** |
| `listRuntimeCandidates` 501 (x-phase **P2**) | none 자동 선택 · worktree 자동 선택 불가 · remote URL 후보 | **S-2** |
| `listLanes` 501 (x-phase **P2**) | lane 상태 done · lane tasks 정보 5종 (+ lane cancel 301) | **S-6** |
| P3 operation 을 웹이 이미 쓴다 (`pauseSession`·`resumeSession`·`getSessionCost`·`restartLane`·`listLaneTasks` 는 x-phase **P3**) | pause 200 · resume 200 · paused_detail · cost 200 · lane restart | 결함 아님 — **웹이 P2 범위를 앞서 있음**(기록만) |
| `createRepoCheck` 미구현 | dirty 저장소 ok:false | 결함 아님(x-phase 없음, worktree = P4) |
| **`@all` 링크 형식이 웹과 계약에서 다르다** | preview 규칙 3 | **W-1** |
| **목의 기대값이 EVAL 과 반대** | new_lane 은 항상 새 lane | **W-2** |
| **`agent_disabled` 경고 없음** | 정지된 에이전트는 경고 | **S-7** |

### 3.4 P1 회귀 (`e2e/p2/20_regression_p1.sh`)

`e2e/p1/01~07` 을 이 스택에 README 순서(01 → 03 → 02 → 05 → 06 → 04 → 07)로 전부 돌렸다. `N=20`.

| 스크립트 | 결과 | 수치 |
|---|---|---|
| `01_vertical_slice.sh` | **통과 (19/20)** | claim 중앙값 **0.008s**(max 0.013, E17-01 ≤ 2s) · 첫 출력 중앙값 **3.324s**(E17-02 ≤ 10s) · 답글 도착 중앙값 **3.914s** · 페어링→ready **10.4s**. C-1 회귀 초록: heartbeat 422 **0**, 재큐잉 로그 **0**, SSE `message.delta` **2프레임**. **20회차만 답글 0** — task 는 `completed`(`stop_reason=end_turn`)이고 `message.say` 에 "E2E 수직 슬라이스 인사 왕복이 성공적으로 완료되었습니다!" 가 있는데 `colab_message_post` 를 부르지 않았다. 플랫폼 오류 없음(모델이 20번째에서 왕복이 끝났다고 판단) |
| `03_cancel.sh` | **통과** | 두 취소 경로 모두. 데몬 정상 종료 1s |
| `02_kill9.sh` | **통과** | 재큐잉·토큰 폐기·고아 정리·중복 게시 0, `final_status: completed`, 그 task 의 메시지 **1건** |
| `05_invite_api.sh` | **통과** | 초대 `accepted`, 두 번째 멤버 `member` 로 가입 |
| `06_s12_pairing_realtime.sh` | **통과** | 서버 SSE `pairing.updated` 2프레임 · 화면 1번 열 때 페어링 **1개** · 패널이 **0.3초**에 `ready`(E17-09 ≤ 10s) |
| `04_u1_browser.sh` | **부분** | U1-1~4·6~9·11~12 통과(S12 준비 완료 **11.4s**, S-6 회귀 초록, P2 7단계 마법사 전부 통과). U1-5 는 P2 템플릿(**S-3**), U1-10 은 런타임 후보(**S-2**) — 새 결함이 아니라 §4 의 같은 항목이다. **U1-13 미도달**: 마법사 마지막 단계 전환이 스크립트 페이싱에 걸려 '시작' 이 눌리지 않는다(같은 계정·같은 화면을 손으로 몰면 세션이 만들어진다 — 스크립트 문제이지 제품 문제가 아니다). 도달했더라도 S7 은 **S-1** 로 죽는다 |
| `07_adversarial.sh` | **부분 (81/82)** | 유일한 ✗ = **S-8**(사람 쿠키로 `recordDecision` 201) |

**07 의 chk 수를 정확히 세었다** — T-S3 워커의 "D10 18/18" 은 6개 적다. 한 번의 완전 실행에서 실제로 도는 chk 는 **82개**다:

| 절 | 실행되는 chk | 비고 |
|---|---|---|
| D1 TaskToken 범위 | **2** | 서버가 토큰 평문을 저장하지 않아 짧은 가지로 간다(살아 있는 토큰이 있으면 7) |
| D2 워크스페이스 경계 | 9 | |
| D3 501 표면 | 5 | |
| D4 멱등키 경계 | 6 | |
| D5 미인증 접근 | 4 | |
| D6 SSE 인가 | 2 | |
| D7 데몬 토큰 경계 | 3 | |
| D8 잘못된 입력 | 11 | |
| **D10 아티팩트 경계** | **24** | 파일에 있는 `chk` 문은 26개지만 2개는 "살아 있는 토큰이 없을 때" 가지다. 토큰을 심고 다른 세션·다른 task 가 있는 완전 실행에서 도는 것은 **24** |
| **D11 P2 operation 경계(신설)** | **16** | |
| **합계** | **82** | 이번 실행 PASS **81** · FAIL **1** |

첫 실행의 `"fail": 8` 은 D11 을 처음 넣었을 때의 값이다. 그 8 중 **6개는 내 기대값이 과했던 것**(아직 x-phase P3 인 `listLaneTasks`·`restartLane`·`pauseSession`·`resumeSession`·`getSessionCost` 와 P2 인데 501 인 `listLanes` 가 403/404 대신 501 을 준다), **1개는 응답 위치 차이**(`not_participant` 가 최상위 `code` 가 아니라 `errors[].code` 에 실린다 — CLI 는 그것을 읽어 exit 3 과 대안 안내까지 정상 동작), **1개만 진짜 결함**(S-8)이다. D11 을 고쳐 501 을 허용값에 넣고(구현하는 PR 이 이 줄에서 501 을 빼면 그때부터 경계가 검사된다) `not_participant` 를 `errors[]` 에서도 읽게 한 뒤가 위 표의 81/82 다.

**`out/regression.tsv` 의 07 행은 1차 실행값이다.** 일괄 실행(`20_regression_p1.sh`)이 남긴 tsv 의 `07_adversarial.sh` 행은 D11 을 처음 넣었을 때의 `"fail": 8` 을 가리킨다 — 위 표의 **81/82** 는 D11 을 고친 뒤 07 만 다시 돌린 값(`out/p1/07_adversarial.log`)이다. 두 값이 섞이지 않도록 `20_regression_p1.sh` 는 이제 각 행에 **실행 시각**을 함께 적는다. 2판에서 일괄 실행을 다시 돌리면 tsv 가 새로 쓰인다.

**06 의 `net::ERR_ABORTED` 는 코드 결함이 아니다.** 첫 일괄 실행에서 06 이 `ab open $WEB_URL/login` 에서 `Navigation failed: net::ERR_ABORTED` 로 죽었다. 원인은 **agent-browser 세션 충돌** — 내가 S7 크래시를 좁히려고 열어 둔 디버깅용 세션(`colab-g4-probe*`)이 남아 있었다. `agent-browser close --all` 뒤 같은 스크립트를 같은 포트·같은 BASE_URL 로 다시 돌리면 **통과**한다(패널 ready 0.3s). 포트나 베이스 URL 문제가 아니고 하이드레이션 문제도 아니다 — 같은 실행에서 웹의 다른 화면은 정상 하이드레이트됐다. 재현 조건: 다른 `AGENT_BROWSER_SESSION` 이 살아 있는 상태에서 새 세션으로 `open`. `e2e/p2/20_regression_p1.sh` 가 시작할 때 `agent-browser close --all` 을 하도록 고쳤다.

**T-W2 가 보고한 헤드리스 하이드레이션 문제는 이 워크스페이스에서 재현되지 않았다.** `/signup`·`/login`·`/onboarding`·S12·S6 마법사 전부 헤드리스에서 상호작용까지 정상이다. 다만 **`next dev` 가 도는 중에 `web/.next` 를 지우면** 그 다음 라우트가 `ENOENT … .next/server/app/(app)/sessions/new/page.js` 로 죽는다(실측). 그때는 웹을 내리고 `.next` 를 지운 뒤 다시 띄우면 된다 — S7 크래시(**S-1**)와는 다른 현상이고, S-1 은 `.next` 를 새로 만든 뒤에도 그대로였다.

## 4. 결함 — 스트림 귀속

고치지 않았다. 차단 결함은 즉시 orca 로 보고했다. **해소** 열은 어느 PR 이 닫았는지다 — 2판(dev `950b02a`)에서 실측으로 확인한 것만 해소로 적는다.

| # | 스트림 | 결함 | 근거 | 영향 | 해소 |
|---|---|---|---|---|---|
| **S-1** | S | `listDecisions` 가 `{"items":[]}` — 계약은 `type: array`, 같은 세션의 `listArtifacts` 는 `[]` | `SessionAside.tsx` 의 `decisions.map` 스택 · 계약 · 목 3출처 대조 | **차단**이었다. S7 세션 화면 전체가 클라이언트 예외로 죽었다(빈 세션 포함) | **#75** — 2판에서 S7 정상 렌더 |
| **S-2** | S | `listRuntimeCandidates`(x-phase **P2**) 501 | S6 4단계 오류 `ListRuntimeCandidates is not part of P1` | S6 마법사가 런타임 후보를 못 준다 | **#75** — 2판 후보 1 |
| **S-3** | S | `listAgentTemplates`·`applyAgentTemplate`(**P2**) 501 | `12_mock_vs_real` 3행 | 팀 템플릿 3종·G5 "템플릿 3분" 불가 | **#75** — 2판 `GET agent-templates` 200 |
| **S-4** | S | `workdir` 행 미기록, `lane.workdir_id` 항상 `null` | `select count(*) from workdir` = 0 | openapi `Lane.workdir_id` 는 required. FR-6.1 이 API 로 관측되지 않음 | **#75** — 2판 lane 응답에 `workdir_id` 채워짐 |
| **S-5** | S | probe 최상위 `colab_cli` 를 서버가 저장·노출하지 않는다 | `runtimes.Probe()` 가 4필드만 UPDATE · `GET /runtimes/{id}` → `colab_cli: null` | `daemon-protocol.md` §3 의 S11/S12 경고가 불가능 | **#75**(2판 재확인 예정) |
| **S-6** | S | `listLanes`(**P2**) 501 | 직접 확인 · `12_mock_vs_real` | S7 lane 보드가 항상 빈 화면 | **#75** — 2판 lane 5건 반환 |
| **S-7** | S | `previewTriggers` 에 `agent_disabled` 경고 없음 | 정지 후 preview → `warnings: []` | U11-6·E10-07 | **#75**(2판 재확인 예정) |
| **S-8** | S | `recordDecision` 이 사람 쿠키로 **201**, 저장된 행의 `source` 가 `hitl` | `POST …/decisions` + 쿠키 → 201 | 워크스페이스 멤버 누구나 사람이 HITL 로 답한 것처럼 결정 기록을 위조 | **#75**(2판 회귀 07 로 재확인 예정) |
| **S-9** | **S** | **lane 이 `running` 으로 바뀔 때 `lane.updated` 를 내보내지 않는다** | 계약: `openapi.yaml` 실시간 이벤트에 `lane.updated`. 실서버: DB 는 Researcher 3개가 07:33:13.98~27.62(**13.6초**) 동안 동시에 `running`(스윕 최대 겹침 3)인데 화면 최대 동시 running 은 **1**. 코드: `publishLane` 호출처가 delegate·cancel·finish 뿐 | **U2-1·U2-4 가 성립하지 않는다.** 사람 눈에는 3-way 병렬이 순차 실행으로 보인다 — 시나리오 A 의 핵심 장면이 화면에서 사라진다 | 미해소 |
| **S-10** | **S** | **`listLanes` 응답에 `brief` 가 없다** | 계약: openapi `Lane.brief`. 실서버: 모든 lane 이 `brief` 키 자체 없음(lane 테이블에 컬럼이 없고 `delegateLane` 의 brief 를 저장하지 않는다). 목: brief 있음 | U2-1 "각 카드에 브리프 한 줄" 불가. 카드만 보고 "이 lane 이 무슨 일을 하는지" 알 수 없다 | 미해소 |
| **W-1** | W | 웹이 `[@all](mention://all)` 을 만드는데 PRD FR-3.2 는 `mention://all/all` | 실측 3출처: 계약(PRD §FR-3.2) · 실서버(`mention://all/all` → suppressed=true·트리거 0 / `mention://all` → suppressed=false·규칙 6 으로 Lead 1개) · 목(`content.includes` 로만 판정해 차이를 가림) | **E1-05·U15-3 위반** — `@all` 이 "기록만" 이 아니라 Lead 를 깨운다 | **#76**(2판 W9 재실행에서 확인 예정) |
| **W-2** | W | `web/e2e/p2-mock.sh` 의 `new_lane` 기대값(`resolution==1`)이 EVAL E2-07(→ **4**)과 반대 | 실서버 4 · 목 1 · EVAL 4 | 스모크가 초록인 채로 웹이 잘못된 해소 번호를 믿을 수 있다 | **#76** |
| **W-3** | **W** | **`AgentChip` 이 `data-agent-id` 를 달지 않는다** | 화면에는 칩이 있고 페이지는 서버 `participants[].status` 를 그대로 넘긴다. e2e 가 "어느 에이전트의 칩인가"를 고를 수단이 없다 | 제품 결함이 아니라 **테스트 훅 결함**. U2-3(E5-11 파생 상태)을 자동으로 판정할 수 없다 | 미해소 |
| **D-1** | D | 데몬 env 허용 목록에 `USER` 가 없어 만료된 OAuth 갱신 실패 | §2.1 | 해소 전 **모든 claude_code task 가 `failed(auth)`** — G4 전면 차단 | **#71** |
| **C-1** | C | `colab --version` 이 exit 2 | `daemon-protocol.md` §3 은 데몬이 `colab --version` 을 실행한다고 못박는다 | 해소 전 모든 probe 가 `colab_cli.present=false` | **#71** |

## 5. 관찰 (결함으로 올리지는 않은 것)

1. **규칙 8 억제가 lane 상태에 묶여 있어, 자식이 `status set done` 뒤에 한 줄만 더 올려도 위임자가 두 번 깨어난다.** 2차 실행 실측: Writer 가 `done` → J2 합류 발화 → 그 뒤 도착한 Writer 의 `@Lead` 메시지가 억제 해제 상태라 Lead task 를 하나 더 만들었다(총 4). E1-17("억제 기간은 합류 발화 전까지")대로의 동작이라 **결함으로 올리지 않았다.** 다만 "자식이 마지막 인사를 남기는" 흔한 순서에서 FR-6.5 의 "정확히 한 번" 이 깨지므로, 억제 기준을 lane 상태가 아니라 **자식 턴의 종료**로 두는 편이 안전하다 — G5 에서 결정할 값.
2. **턴 프롬프트의 마지막 줄이 불필요한 왕복을 만든다.** `Respond to the trigger. Post your reply with colab message post; mention the person or agent you are answering when a reply is expected.` 때문에 Lead 가 종합 메시지에 `@Researcher` 를 붙여 lane 이 한 번 더 돌았다(1차 실행). 지시문으로 눌렀지만, 합류 턴에서는 이 줄이 역효과다.
3. `colab_message_post` 의 `mention` 인자와 본문의 멘션 링크가 **둘 다** 반영돼 같은 에이전트가 `mentions` 에 두 번 들어간 메시지가 있었다. 트리거는 1개(E1-03 대로)라 무해했다.
4. **웹은 P3 operation 을 이미 부른다**(pause·resume·cost·restart·lane tasks). x-phase 기준으로는 앞서 있는 것이고 실서버에서는 501 이다. G5 이전에 "웹이 어느 단계까지 켜져 있어야 하는가" 를 맞춰야 `12_mock_vs_real` 의 초록/빨강이 의미를 갖는다.

## 6. 재현

```bash
# 스택 (전용 포트·전용 Postgres, 매번 재빌드)
bash e2e/p2/up.sh

# 시나리오 A — API/CLI 경로 (에이전트 턴 7, 약 100초)
bash e2e/p2/10_scenario_a_api.sh          # out/a-checks.tsv · out/scenario-a.json · out/a-join-prompt.txt

# 시나리오 A — 웹 경로 (agent-browser, U2·U4·U5)
bash e2e/p2/11_scenario_a_web.sh          # out/w-steps.tsv · web/__screenshots__/p2-a-*.png

# 목 API vs 실서버 (에이전트 턴 0~1)
bash e2e/p2/12_mock_vs_real.sh            # out/mock-vs-real.tsv · out/mock-vs-real.json

# P1 회귀 01~07 (01 은 N 회, 기본 20)
N=20 bash e2e/p2/20_regression_p1.sh      # out/p1/*.log · out/regression.tsv

bash e2e/p2/down.sh
```

`e2e/p1/04_u1_browser.sh` 의 U1-7~12 는 **P2 7단계 마법사**에 맞춰 갱신했다 — P1 때의 `session-defaults` 요약 한 줄이 사라진 것은 결함이 아니라 T-W2 의 화면 변경이다. `e2e/p1/07_adversarial.sh` 에는 **D11(P2 operation 경계)** 를 추가했다 — lane 목록·task 이력·중단·재지시·미리보기·일시정지·재개·결정·비용의 워크스페이스 경계와, `delegateLane`·`recordDecision` 이 사람 쿠키를 받지 않는지, 비참여 에이전트 위임이 `not_participant` 인지(E15-02). 같은 파일의 데몬 경로에 박혀 있던 `http://localhost:8080` 은 `$SERVER_URL` 로 바꿨다 — 포트가 다른 스택에서 **남의 서버를 찌르고 있었다**.
