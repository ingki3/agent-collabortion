# e2e/p5 — P5 스모크·판정 스크립트

`e2e/p4/README.md` 의 규약을 그대로 잇는다(→ p3 → p2 → p1). 다른 점만 적는다.

- **번호는 Lead 가 준다**(P5_TASKS §0: `70_` 부터). 같은 번호를 다시 쓰지 않는다.
  산출물 파일명도 스크립트 번호를 따른다(`out/70-*`).
- **스택은 스크립트마다 격리**(P3_TASKS §0-13). `SERVER_URL`·`PG_PORT`·`PG_CONTAINER` 를
  미리 export 하면 덮어쓸 수 있다. `up.sh` 는 기본으로 **서버만** 띄운다(`WITH_WEB=1` 이면 웹도).
- `out/` 은 `.gitignore` 다 — claim 응답·데몬 토큰·쿠키가 들어 있다.
- **판정 표**는 `lib.sh chk ID EXPECT ACTUAL NOTE` 로 `out/<번호>-checks.tsv` 에 쌓인다
  (p2 의 `chk ID 설명 기대 실제` 와 인자 순서가 다르다 — 이 디렉터리 안에서만 쓴다).

| 스크립트 | 무엇을 재는가 | 스택 | 비용 한 줄(I-3: 턴 · 실기 $ · 소요) |
|---|---|---|---|
| `79_delete_session.sh` | **deleteSession**(openapi 0.1.3, FR-2.7, T-S17) 서버 쪽 전부 — 데몬 **없이** curl 로: 완료 세션(claim → phase → 아티팩트 lo → finish → complete) → 멤버 403 · active 409 `session_active`(계약 문장) · Director 204 → 404 · 목록·비용·지표 표본에서 빠짐 · 자식 행 0 · large object 0 · `activity_log` `session.deleted` 1행(고아 0) · SSE `session.deleted` · worktree §6 보고(미병합) → cancel → 409 `workdir_unmerged` + `Problem.workdirs[]` → 병합 보고 → 204 → claim 의 gc `{id,path}` → 없는 행 영수증 200·소비·피드 0 · admin 204. 43 판정 | Postgres `colab-pg-s17` :5459 + server :8115 (`SERVER_URL`·`PG_PORT`·`PG_CONTAINER` 를 up.sh 에 export) | 턴 0(curl) · $0 · ≈ 15s |
| `80_reviewer.sh` | **S-84**(openapi 0.1.4, T-S18) 서버 쪽 전부 — 데몬 **없이** curl 로: createSession `agent_approval` 리뷰어 없음 422 `reviewer_required`(errors[].field 가 그 원자) · 참여자 아님 422 `reviewer_not_participant`(`artifact_submitted` 의 agent_id 도) · 리뷰어 지정 201 → 진행률 `agent_id`·`agent_name`·`next_actor`(에이전트 이름/`director`) → Lead 제출 → R 승인(task 토큰) → completed · 옛 모양 세션(DB 로 심음) getSession 200 + `blocked_reason` 3종(reviewer_missing · reviewer_not_participant · agent_archived) → updateSession(active) 422 두 코드 → 리뷰어 지정 200 → SSE `session.completion_progress`+`session.updated` · `activity_log` `session.completion_condition_changed` → R 승인 → completed · 이미 충족된 원자 유지(단독이면 즉시 completed · user_approval 만 남으면 확인 요청 1건, 두 번 바꿔도 1건 → Director 승인 → completed) · 멤버 403 · completed 422 immutable · paused 200(paused 유지). 50 판정 | Postgres `colab-pg-s18` :5460 + server :8116 | 턴 0(curl) · $0 · ≈ 15s |
| `70_testchat.sh` | **테스트 채팅**(FR-1.8.1, daemon-protocol v0.8 §4.5) 서버 쪽 전부 — 데몬 **없이** 데몬 역할을 curl 로 흉내: createTestChat → 턴 202/409 → claim 이 주는 §4.5 번들(kind·id·attempt·토큰 없음·[2] 없음·workdir·첫 턴 머리 한 줄) → phase → heartbeat(preview → SSE `test_chat.delta`) → events(message.say 합침, task_event 0) → finish(transport·usage 추정·ref) → SSE `test_chat.turn` → getTestChat(agent 턴·토큰·transport·비용) → 턴 2 의 `resume` → failed(auth) 의 §8.4 문장 → 워크스페이스 `test_chat_usd` → close 의 gc(test_chat_id, session_id 없음)·410·§6 영수증으로 소비 → 진행 중 턴 close 의 cancel+gc. 끝에 **getWorkspaceMetrics**(10개·순서·표본 0 = null·422). 57 판정 | Postgres `colab-pg-s12` :5451 + server :8107 (웹·데몬·모델 호출 0회) | 턴 0(curl) · $0 · ≈ 15s |

## 재현

```bash
bash e2e/p5/up.sh                      # colab-pg-s12 :5451 + server :8107 (bin/server 를 다시 빌드)
bash e2e/p5/70_testchat.sh             # out/70-checks.tsv · 70-bundle-{1,2}.json · 70-sse.log · 70-testchat.json · 70-metrics.json
bash e2e/p5/down.sh                    # pid·pgid 로만 종료(§0-10). Postgres 컨테이너는 남긴다

# T-S17 (다른 포트 — 스택은 스크립트마다 격리)
SERVER_URL=http://localhost:8115 PG_PORT=5459 PG_CONTAINER=colab-pg-s17 bash e2e/p5/up.sh
bash e2e/p5/79_delete_session.sh       # out/79-checks.tsv · 79-claim-gc.json · 79-409-unmerged.json · 79-sse.log · 79-cost-{before,after}.json
SERVER_URL=http://localhost:8115 PG_PORT=5459 PG_CONTAINER=colab-pg-s17 bash e2e/p5/down.sh

# T-S18 (S-84 리뷰어 필수 · 진행률 blocked_reason · active 에서 종료 조건 수정)
SERVER_URL=http://localhost:8116 PG_PORT=5460 PG_CONTAINER=colab-pg-s18 bash e2e/p5/up.sh
bash e2e/p5/80_reviewer.sh             # out/80-checks.tsv · 80-422-required.json · 80-progress-{new,old}.json · 80-patch-fix.json · 80-sse.log
SERVER_URL=http://localhost:8116 PG_PORT=5460 PG_CONTAINER=colab-pg-s18 bash e2e/p5/down.sh
```

## 이 판에서 밟은 함정

- **bash 3.2 는 `$( … "…\"…\"…" )` 안의 이스케이프한 따옴표에서 인자를 가른다.** `chk ID 200 "$(daemon_api_code … "{\"phase\":…}")" "설명"` 이
  `got` 와 `note` 양쪽에 HTTP 코드를 넣었다(422 로 보였지만 실제 서버 응답은 200). 데몬에 보낼 JSON 은 `jq -nc` 로 **먼저 변수에**
  만들고 그 변수만 넘긴다(70_ 의 `PH1`·`PH2`).
- 70_ 은 데몬을 띄우지 않으므로 **데몬 몫**(§4.5 토큰 없는 환경·`mcpServers` 미탑재·래퍼 미생성·`.colab/testchat/` 아래만 `rm -rf`·
  24h 지난 디렉터리 정리)은 여기서 재지 않는다 — T-D12 의 자리다.
- `task_event 저장 0` 판정(B.5)은 DB 전역 count 다. 전용 스택이라 0 이지만, 다른 스크립트와 DB 를 공유하면 먼저 재라.

## T-I5 — 시나리오 A·B·C·D 를 **CI 에서** · 성능(§9) · 보안(§9) (72_~78_, G9 판정 자료)

70_·71_ 과 같은 디렉터리지만 **lib 가 다르다**: 72_~78_ 은 `lib_i5.sh`(p2 의 `chk ID 설명 기대 실제` 관례 + 페이크 런타임 배선)를,
스택은 `up_i5.sh`/`down_i5.sh`(server :8109 · pg :5453 `colab-pg-i5` · web :3021)를 쓴다. 그리고 하나 더 — **런타임이 페이크다**.

### 왜 페이크인가

G9 조건은 "시나리오 A·B·C·D E2E 가 CI 에서 초록"(PLAN §6.2). CI 러너에는 모델도 로그인도 없다.
데몬 모듈의 **acpfake**(`daemon/acpfake`, 대본 응답 ACP 에이전트)를 `bin/acpfake` 로 빌드해 `hermes` 와
`npx`(claude_code 어댑터 자리) 이름으로 데몬의 PATH 앞에 두면, 데몬은 **자기 코드 그대로** 런타임을 spawn 하고
probe·claim·phase·events·heartbeat·finish 를 전부 탄다(T-D10 실기 스모크 `e2e/p3/58_` 의 레시피).
모델의 답은 대본이다 — `fixtures/agent.sh <role>` 이 acpfake 의 새 `exec` 스텝에서 **실물 서버가 만든 턴
프롬프트**(`ACPFAKE_PROMPT`)를 읽고 진짜 `colab` CLI 로 위임·게시·아티팩트·HITL·리뷰·결정을 한다.
서버·데몬·CLI·DB·웹은 실물이고, 없는 것은 모델 하나다.

- 판정은 실기 판(p2 `10_`·p3 `52_`·`53_`·p4 `61_`)의 것을 **그대로** 옮겼다. 같은 스크립트가 `RUNTIME=real` 로
  실기(claude_code·hermes 로그인)에서도 돈다 — CI 는 fake 만.
- 페이크가 **못 재는 것**: 모델의 판단(지시문을 따르는가), 실기 어댑터의 이벤트 모양(편집 카드는 대본이 붙인다),
  resume 의 실제 신뢰도, 비용. 그것은 G4~G7 의 실기 판정과 ④ S-66 재현(`plan/G9_REPORT.md`)이 맡는다.
- **페이크가 잡은 것**(모델과 무관한 플랫폼 결함): `77_` 의 N/A 두 줄 — 위임↔합류 사이클이 FR-3.5 루프 상한을
  타지 않는다 · 마스킹이 `task_event.payload.title` 을 지우지 않는다. 번호는 Lead 가 준다(§0-11).

### 스택 · 실행

포트: server **:8109** · Postgres **:5453**(`colab-pg-i5`) · web **:3021** — 다른 워커 스택과 겹치지 않는다.
CI 에서는 docker 가 없어 `PG_EXTERNAL=1 PSQL_URL=…` 로 service 컨테이너에 붙고 `psqlq` 가 `psql` 클라이언트로 간다.

```bash
bash e2e/p5/ci.sh                          # 스택 기동 → 72~78 전부 → 표 → 종료 (CI 가 부르는 한 줄)
bash e2e/p5/up_i5.sh && bash e2e/p5/72_scenario_a.sh   # 하나씩
RUNTIME=real bash e2e/p5/72_scenario_a.sh  # 실기
bash e2e/p5/down_i5.sh                     # pid·pgid 로만 종료(§0-10)
```

| 스크립트 | 무엇을 재나 | 실기 원본 | 비용 한 줄(I-3: 페이크 턴 · 실기 $ · 소요) |
|---|---|---|---|
| `72_scenario_a.sh` | 시나리오 A 8단계 전부: 위임 3 → lane 3 병렬 → 합류 1회 → Writer **HITL 질문**(default) → Director 답 → resume attempt 2 → 아티팩트 → 플랫폼 `user_approval` → 승인 → completed → 요약 1 | p2 `10_` + p2 `33_` | ≈ 9턴 · $0 · 10s (실기 haiku ≈ $0.05 · 5분) |
| `73_scenario_b.sh` | 시나리오 B: worktree 격리 · 브랜치 · diff 아티팩트 · QA 는 아티팩트만 · 반려 → 기존 lane 재진입(resume) · v2 · QA 재기동 · 사람 승인 없이 completed · §8.4 위생 · 피드 카드 | p4 `61_` | ≈ 8턴 · $0 · 10s (실기 ≈ $0.05 · 6분) |
| `74_scenario_c.sh` | 시나리오 C: 실행 중 메시지(queued) · "중단하고 다시 지시"(<resumed> 없음) · "중단"(피드 "사람이 중단함") · 결정 기록이 콜드 스타트를 넘는다 | p3 `52_` | ≈ 8턴 · $0 · 40s (실기 ≈ $0.05 · 6분) |
| `75_scenario_d.sh` | 시나리오 D: hermes 프로파일 실패 → 같은 머신 claude_code 폴백(workdir·아티팩트 유지) · 대안 없음 → 재큐잉 + 알림 | p3 `53_` | ≈ 4턴 · $0 · 6s (실기 ≈ $0.02 · 3분) |
| `76_perf.sh` | §9: 게시→claim p50 · 첫 출력 p50 (100회) · **동시 task 50**(데몬 5 × cap 10) — claim/완료 p50·p95, API p50·p95, DB 커넥션, 재큐잉 0, 이중 게시 0 | — | ≈ 250턴 · $0 · 130s (**실기 금지** — haiku ≈ $1~2 · 20분+) |
| `77_security.sh` | §9 보안: originator 체인 보존 · task 토큰으로 사람 op 불가 · 토큰 범위(다른 세션·task, finish 뒤·revoke 뒤 401) · 마스킹 · 데몬 토큰 · SSE 격리 | — | ≈ 12턴 · $0 · 40s (실기 ≈ $0.06 · 5분) |
| `78_web_s7.sh` | 웹 S7 한 화면 — agent-browser 가 있으면 DOM + 스크린샷, 없으면(CI) next build + 앱 셸 200 | p2 `11_` | 0턴 · $0 · 3s |
| `lib_i5.sh` | p4 lib 재사용 + 페이크 런타임 배선(`fake_runtime_setup`·`fake_env`·`create_agent_fake`·`daemon_run_p5`) + `psql` 모드. `up_i5.sh`/`down_i5.sh` 는 CI(`PG_EXTERNAL=1`)도 안다 | — |
| `fixtures/agent.sh` | 대본. 역할별로 `<trigger>` 를 읽고 `colab` 을 부른다. 흔적은 `out/fake-records/agent-trace.tsv` | — |

판정 표는 `out/<번호>-checks.tsv`, 요약은 `out/ci-summary.tsv`, 해석은 `plan/G9_REPORT.md`.

### 이 판(T-I5)에서 밟은 함정

- **확장되는 heredoc 안의 백틱은 명령 치환이다.** 래퍼 스크립트 주석에 `` `hermes acp` `` 를 썼더니 lib 를 source 하는
  순간 진짜 hermes 가 떴다. 래퍼는 파일에 JSON 을 두고 `$(cat …)` 로 읽는다.
- **함수 호출 앞의 임시 대입(`PATH=… func`)은 bash 3.2 에서 함수가 띄운 자식(데몬)까지 가지 않았다.**
  서브셸 + `export` 로 넘긴다(`daemon_pair_p5`·`daemon_run_p5`).
- `${3:-{\}}` 는 `{}` 가 아니다 — jq `--argjson` 이 invalid JSON. `[ -n "$x" ] || x='{}'`.
- 세션 시작 트리거 문장은 §8.4 로 바뀌었다(S-67): "Session started" 가 아니라 "세션을 시작했습니다". 대본은 둘 다 받는다.
- 페이크는 빠르다(턴 0.2s). **동시성을 재려면 턴에 sleep 을 넣어야** 50 슬롯이 채워진다. 그리고 한 에이전트가 50 세션을
  맡으면 **에이전트 상한(기본 3)** 이 먼저 걸린다 — 재는 층(런타임·워크스페이스)에 맞춰 `max_concurrent_tasks` 를 올린다.
- 위임자가 "깨어날 때마다 위임" 하면 합류 통보로 또 깨어나 **무한 사이클**이 된다(70초에 529회) — 루프 상한이 이 경로를
  안 본다(`77_` 보고). 대본은 세션 시작 트리거에서만 위임한다.
- `colab status set done` 은 lane 을 즉시 done 으로 만든다 — 그 뒤에도 도는 턴은 "중단" 이 409 다. 긴 턴 대본은 done 을 부르지 않는다.
- 페이크의 워크디렉터리를 저장소 안(`e2e/p5/out`)에 두지 마라 — `dir` 격리의 §6 git 측정이 이 저장소를 읽는다(T-D12). `/private/tmp/colab-p5-i5`.

## T-I6 — v1.1 첫 라운드 마감: 세 층 역할 게이트(82_) · CI 편입(81_·82_·84_) · 72_ 흔들림 · I-3 비용 한 줄 · 실기 대조

`plan/V11_TASKS.md` T-I6. 판정 수치·CI run URL·열린 결함은 `plan/V11_REPORT.md`.

| 스크립트 | 무엇을 재나 | lib | 비용 한 줄(I-3) |
|---|---|---|---|
| `81_observations_commands.sh` | **서버 층**(T-S19, PR #246): 관찰 표 모양·실값 · 빈 턴 카드 · 역할별 403 `command_not_allowed` — 데몬 없이 curl | `lib.sh` | 턴 0 · $0 · 15s |
| `82_role_gate.sh` | **세 층 한 번에**(acpfake, CI): reviewer 대본이 런타임 안에서 `lane delegate` 를 시도 → (a) MCP argv `--allow` + 그 argv 로 띄운 진짜 colab 의 tools/list 에 delegate 없음 (b) CLI exit 3 — claude_code 는 선에 GET /cli/context 1, hermes(래퍼)는 0 줄, POST /lanes 는 0 (c) 같은 토큰 curl → 403 + rejected 행; lead·custom 은 셋 다 통과; **사람(쿠키)은 비게이트**(NN4: Director·멤버의 403 은 `agent_only`, `command_not_allowed` 0). 같은 세션에서 관찰 5행 n≥1 · Idle(빈 대본) 빈 턴 카드 = empty_turn_rate · 대시보드(headless 78_ 방식) · **I-2 lane.actions** 상태별(running→restart,cancel · queued→cancel · waiting_human→respond_hitl · done→[] · 멤버는 []). `RUNTIME=real` 은 절 R 만(아래) | `lib_i5.sh` | ≈ 9턴 · $0 · 60s (실기 절 R: haiku 1턴 ≈ $0.04 · 60s) |
| `83_allowed_commands_daemon.sh` | **데몬 층 실기**(T-D13, PR #250): 로그 allowed/denied · MCP argv 탭 · 툴 목록 · [2] 인용. **실기 고정 — CI 에 없다** | `lib.sh` | haiku 1턴 · ≈ $0.01 · 60s |
| `84_cli_allowed_commands.sh` | **CLI 층**(T-C7, PR #251 의 `81_cli_allowed_commands.sh` — 서버 81_ 과 번호가 겹쳐 84_ 로): 세 표면(컨텍스트·래퍼 env·MCP)을 실제 바이너리로, 탭 프록시로 선에서 센다 | `lib.sh` | 턴 0 · $0 · 20s |

### CI 편입 · 실기 대조 스위치

`ci.sh` 의 `SCRIPTS` 에 `81_ 82_ 84_` 가 들었다(83_ 은 실기라 로컬만). 상한 15분은 그대로(실측 CI ≈ 6분). `ci.sh` 머리의 표가
**어느 스크립트가 `RUNTIME=real` 로 무엇을 재는지**를 적는다. 판정 표는 두 모양이라(lib_i5 = p2 관례 3열 판정 · lib.sh = 2열 판정)
`ci.sh` 가 둘 다 센다. `lib.sh` 는 CI(`PSQL_URL`)에서 `psql` 클라이언트로 간다(lib_i5 와 같은 분기).

```bash
bash e2e/p5/ci.sh                                   # 72~78 · 81 · 82 · 84 전부(페이크)
bash e2e/p5/up_i5.sh && bash e2e/p5/82_role_gate.sh # 82_ 하나(페이크, 세 층)
RUNTIME=real bash e2e/p5/82_role_gate.sh            # 실기 절 R 만 — claude_code 로그인 필요
bash e2e/p5/83_allowed_commands_daemon.sh           # 실기 데몬 몫(자기 스택 :8118/:5472)
```

### 72_ A2d 흔들림 — 원인과 고침(I-1)

CI(PR #249 run 34989845714 attempt 1)에서 `A2d 동시 3개 (위임 3이 병렬) got=2`. **원인**: 페이크 턴은 0.2s 라 첫 Researcher lane 이
셋째 lane 의 `started_at` 전에 끝나 스윕(`running_overlap`)이 2 를 본다 — 병렬성이 없는 게 아니라 **표본 시점에 3 중 2 만 running** 이다.
**고침**(단언은 그대로): 대본(`fixtures/agent.sh` Researcher)에 **barrier** — 자기 표식을 남기고 형제 표식 3 개가 모일 때까지(상한 30s)
기다린 뒤 2s 더 붙든다. 데몬이 병렬로 안 돌리면 상한이 지나 그냥 진행하고 A2d 가 **제대로** FAIL 이다. 하네스 쪽은 `wait_step`(lib_i5, I-1:
단계별 대기를 판정 행으로)으로 "Researcher task 3 이 동시에 running" 인 순간을 0.3s 폴링으로 잡는다(`A2d0`) — 두 눈으로 같은 것을 본다.
연속 3회 로컬 3/3 · CI 3/3(V11_REPORT).

### 이 판(T-I6)에서 밟은 함정

- **데몬 capacity 로는 queued 를 못 만든다.** 82_ (f) 의 queued lane 을 "capacity 3 이 R·RH·C 로 찼으니 다음은 queued" 로 만들려 했더니
  Idle 이 claim 됐다 — 데몬은 claim 응답의 task 를 `running` 에 등록(`runAttempt` 안, workdir 준비 뒤)하기 **전에** 루프가 돌아
  `free = capacity - len(running)` 을 다시 세고 다음 claim 을 보낸다(결함 보고, V11_REPORT). 세션 `limits.max_parallel_lanes=3` 으로
  **서버 쪽 lane 상한**을 쓰면 결정적이다(claim SQL 의 `lane_cap`).
- **사람의 보통 메시지는 규칙 6 으로 assignee 를 깨운다.** NN4 의 "사람 경로" 메시지를 보통 문장으로 올렸더니 Lead 대본이 또 위임했다.
  라우팅을 원치 않는 사람 메시지는 `/note`(규칙 1).
- **Director 의 POST /lanes·/decisions 는 403 `agent_only`** — 명령 표가 아니라 사람 권한 규칙(사람은 「새 작업 줄기로 보내기」·HITL 카드).
  NN4 는 "`command_not_allowed` 가 아니다" 로 잰다.
- **hermes 의 브리프는 파일이다**(brief_transport=file): 프롬프트 첫 줄이 workdir 의 `COLAB_BRIEF.md` 를 가리킨다. 래퍼 경로·거부 줄은
  거기서 읽는다(턴이 살아 있는 동안).
- **S7 의 빈 턴 문장은 이력 「활동」 토글로만 닿는다**(T-W16 Lead A). 새로 연 페이지는 SSE 로 그 행을 못 봤으니 lane 카드에 한 줄이 없다 —
  Idle lane 의 `lane-tasks-toggle` → `task-activity-toggle` 을 연 뒤 `feed-row-empty-turn`·`lane-empty-turn` 을 센다.
- **탭 포트 겹침(로컬)**: 73_ 의 탭 기본 :8120 · 74_ :8121 · 84_ 는 `SERVER_URL 포트+10`. T-I6 스택(:8120)에서 ci.sh 를 돌리면 73_ 의 탭이
  서버 포트와 겹친다 — `TAP_PORT_72/73/74/82` 를 export 한다. CI(:8109)는 겹치지 않는다.
- 대본이 **토큰을 남기고 턴을 붙드는** 레시피(Gate): 서버 층 (c) 는 finish 뒤 401 이라 살아 있는 토큰이 필요하다. `gate-<task>.token` →
  하네스가 curl → `gate-<task>.go` 로 놓아준다(상한 90s). 합류 통보로 깨어난 턴은 시도하지 않는다(위임↔합류 사이클, 77_ 보고).
