# e2e/p5 — P5 스모크·판정 스크립트

`e2e/p4/README.md` 의 규약을 그대로 잇는다(→ p3 → p2 → p1). 다른 점만 적는다.

- **번호는 Lead 가 준다**(P5_TASKS §0: `70_` 부터). 같은 번호를 다시 쓰지 않는다.
  산출물 파일명도 스크립트 번호를 따른다(`out/70-*`).
- **스택은 스크립트마다 격리**(P3_TASKS §0-13). `SERVER_URL`·`PG_PORT`·`PG_CONTAINER` 를
  미리 export 하면 덮어쓸 수 있다. `up.sh` 는 기본으로 **서버만** 띄운다(`WITH_WEB=1` 이면 웹도).
- `out/` 은 `.gitignore` 다 — claim 응답·데몬 토큰·쿠키가 들어 있다.
- **판정 표**는 `lib.sh chk ID EXPECT ACTUAL NOTE` 로 `out/<번호>-checks.tsv` 에 쌓인다
  (p2 의 `chk ID 설명 기대 실제` 와 인자 순서가 다르다 — 이 디렉터리 안에서만 쓴다).

| 스크립트 | 무엇을 재는가 | 스택 |
|---|---|---|
| `79_delete_session.sh` | **deleteSession**(openapi 0.1.3, FR-2.7, T-S17) 서버 쪽 전부 — 데몬 **없이** curl 로: 완료 세션(claim → phase → 아티팩트 lo → finish → complete) → 멤버 403 · active 409 `session_active`(계약 문장) · Director 204 → 404 · 목록·비용·지표 표본에서 빠짐 · 자식 행 0 · large object 0 · `activity_log` `session.deleted` 1행(고아 0) · SSE `session.deleted` · worktree §6 보고(미병합) → cancel → 409 `workdir_unmerged` + `Problem.workdirs[]` → 병합 보고 → 204 → claim 의 gc `{id,path}` → 없는 행 영수증 200·소비·피드 0 · admin 204. 43 판정 | Postgres `colab-pg-s17` :5459 + server :8115 (`SERVER_URL`·`PG_PORT`·`PG_CONTAINER` 를 up.sh 에 export) |
| `70_testchat.sh` | **테스트 채팅**(FR-1.8.1, daemon-protocol v0.8 §4.5) 서버 쪽 전부 — 데몬 **없이** 데몬 역할을 curl 로 흉내: createTestChat → 턴 202/409 → claim 이 주는 §4.5 번들(kind·id·attempt·토큰 없음·[2] 없음·workdir·첫 턴 머리 한 줄) → phase → heartbeat(preview → SSE `test_chat.delta`) → events(message.say 합침, task_event 0) → finish(transport·usage 추정·ref) → SSE `test_chat.turn` → getTestChat(agent 턴·토큰·transport·비용) → 턴 2 의 `resume` → failed(auth) 의 §8.4 문장 → 워크스페이스 `test_chat_usd` → close 의 gc(test_chat_id, session_id 없음)·410·§6 영수증으로 소비 → 진행 중 턴 close 의 cancel+gc. 끝에 **getWorkspaceMetrics**(10개·순서·표본 0 = null·422). 57 판정 | Postgres `colab-pg-s12` :5451 + server :8107 (웹·데몬·모델 호출 0회) |

## 재현

```bash
bash e2e/p5/up.sh                      # colab-pg-s12 :5451 + server :8107 (bin/server 를 다시 빌드)
bash e2e/p5/70_testchat.sh             # out/70-checks.tsv · 70-bundle-{1,2}.json · 70-sse.log · 70-testchat.json · 70-metrics.json
bash e2e/p5/down.sh                    # pid·pgid 로만 종료(§0-10). Postgres 컨테이너는 남긴다

# T-S17 (다른 포트 — 스택은 스크립트마다 격리)
SERVER_URL=http://localhost:8115 PG_PORT=5459 PG_CONTAINER=colab-pg-s17 bash e2e/p5/up.sh
bash e2e/p5/79_delete_session.sh       # out/79-checks.tsv · 79-claim-gc.json · 79-409-unmerged.json · 79-sse.log · 79-cost-{before,after}.json
SERVER_URL=http://localhost:8115 PG_PORT=5459 PG_CONTAINER=colab-pg-s17 bash e2e/p5/down.sh
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

| 스크립트 | 무엇을 재나 | 실기 원본 |
|---|---|---|
| `72_scenario_a.sh` | 시나리오 A 8단계 전부: 위임 3 → lane 3 병렬 → 합류 1회 → Writer **HITL 질문**(default) → Director 답 → resume attempt 2 → 아티팩트 → 플랫폼 `user_approval` → 승인 → completed → 요약 1 | p2 `10_` + p2 `33_` |
| `73_scenario_b.sh` | 시나리오 B: worktree 격리 · 브랜치 · diff 아티팩트 · QA 는 아티팩트만 · 반려 → 기존 lane 재진입(resume) · v2 · QA 재기동 · 사람 승인 없이 completed · §8.4 위생 · 피드 카드 | p4 `61_` |
| `74_scenario_c.sh` | 시나리오 C: 실행 중 메시지(queued) · "중단하고 다시 지시"(<resumed> 없음) · "중단"(피드 "사람이 중단함") · 결정 기록이 콜드 스타트를 넘는다 | p3 `52_` |
| `75_scenario_d.sh` | 시나리오 D: hermes 프로파일 실패 → 같은 머신 claude_code 폴백(workdir·아티팩트 유지) · 대안 없음 → 재큐잉 + 알림 | p3 `53_` |
| `76_perf.sh` | §9: 게시→claim p50 · 첫 출력 p50 (100회) · **동시 task 50**(데몬 5 × cap 10) — claim/완료 p50·p95, API p50·p95, DB 커넥션, 재큐잉 0, 이중 게시 0 | — |
| `77_security.sh` | §9 보안: originator 체인 보존 · task 토큰으로 사람 op 불가 · 토큰 범위(다른 세션·task, finish 뒤·revoke 뒤 401) · 마스킹 · 데몬 토큰 · SSE 격리 | — |
| `78_web_s7.sh` | 웹 S7 한 화면 — agent-browser 가 있으면 DOM + 스크린샷, 없으면(CI) next build + 앱 셸 200 | p2 `11_` |
| `lib_i5.sh` | p4 lib 재사용 + 페이크 런타임 배선(`fake_runtime_setup`·`fake_env`·`create_agent_fake`·`daemon_run_p5`) + `psql` 모드. `up_i5.sh`/`down_i5.sh` 는 CI(`PG_EXTERNAL=1`)도 안다 |
| `fixtures/agent.sh` | 대본. 역할별로 `<trigger>` 를 읽고 `colab` 을 부른다. 흔적은 `out/fake-records/agent-trace.tsv` |

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
