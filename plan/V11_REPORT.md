# v1.1 첫 라운드 마감 자료 — 세 층 역할 게이트(K-19) · 관찰 표(K-18) · CI 편입 (T-I6)

| 항목 | 내용 |
|---|---|
| 상태 | **초안(2026-09-16, T-I6).** 판정은 Lead. 게이트는 없다(V11_TASKS) — 각 PR 의 CI + 리뷰 + 이 실서버·실기 대조 |
| 빌드 | dev `8e3534a`(#246 서버 T-S19 · #247/#248 웹 T-W16 · #249 계약 문장 · #250 데몬 T-D13 · #251 CLI T-C7 머지본) 위에 이 PR |
| 방법 | 페이크 런타임(`e2e/p5/README.md` "왜 페이크인가"): 서버·데몬·CLI·DB·웹은 실물, 모델 자리만 acpfake 대본. 실기 대조는 §4(claude_code reviewer 1턴)와 T-D13 의 83_ |
| 무엇이 새로 재졌나 | **reviewer 가 `lane delegate` 를 시도하면 세 층(MCP·CLI·서버)이 각각 막는다** 를 데몬이 실제로 띄운 런타임 안에서(82_); 사람 경로 비게이트(NN4); 관찰 5행 실값·빈 턴 카드·대시보드; I-2 lane.actions 상태별; 72_ A2d 흔들림의 원인·고침(I-1); I-3 비용 한 줄 |

## 1. CI 초록 증거

- **run**: https://github.com/ingki3/agent-collabortion/actions/runs/34995413656 (PR #253, head `d53d70a`, merge `b6782fb`) — `e2e` job **success** 3회 연속(attempt 1·2·3, 각 283s·285s·287s = 5m15s 안팎, 상한 15분): 10/10 PASS 모두, **72_ 47/0 × 3**(A2d0·A2d), 82_ 66/0(N/A 1 = agent-browser DOM, CI 에 없음), 81_ 56/0, 84_ 35/0. attempt 1 의 `web` job 은 S14 설정 테스트 `settings-dirty` 로 실패 — 이 PR 이 건드리지 않은 웹 유닛의 **기존 흔들림**(dev run 34988310438 도 같은 파일), attempt 2 초록·로컬 3/3 초록(§6).
- job 안 표(`ci-summary.tsv`, 아티팩트 `e2e-p5-out`) — 로컬(맥, docker Postgres, 같은 `bash e2e/p5/ci.sh`) 10/10 PASS · 245s:

| 스크립트 | 결과 | PASS/FAIL | 시간(로컬) | 재는 것 |
|---|---|---|---|---|
| `72_scenario_a` | PASS | 47/0 | 8s | 시나리오 A(G9 그대로) + **A2d0**: Researcher 3 이 동시에 running 인 순간을 폴링으로(§3) |
| `73_scenario_b` | PASS | 48/0 | 8s | 시나리오 B |
| `74_scenario_c` | PASS | 42/0 | 36s | 시나리오 C |
| `75_scenario_d` | PASS | 22/0 | 6s | 시나리오 D |
| `76_perf` | PASS | 9/0 | 129s | §9 성능 |
| `77_security` | PASS | 50/0 | 37s | §9 보안 |
| `78_web_s7` | PASS | 7/0 | 4s | S7 앱 셸(로컬은 DOM) |
| `81_observations_commands` | PASS | 56/0 | 2s | **서버 층**(T-S19 #246): 관찰 표 모양·실값 · 빈 턴 카드 · 역할별 403 — 데몬 없이 curl. CI 편입(§0 lib.sh 의 `PSQL_URL` 분기) |
| `82_role_gate` | PASS | **70/0** | 13s | **세 층 + 관찰 + lane.actions** — §2 |
| `84_cli_allowed_commands` | PASS | 35/0 | 2s | **CLI 층**(T-C7 #251 의 `81_cli_allowed_commands.sh` → 번호 충돌로 84_) |

`83_allowed_commands_daemon.sh`(T-D13, 실기 고정)는 CI 에 넣지 않았다 — `ci.sh` 머리의 **실기 대조 스위치 표**에 적었다.

## 2. 82_ — reviewer 의 `lane delegate` 를 세 층이 각각 막는다 (70/0)

한 세션, 데몬 capacity 3, 세션 `limits.max_parallel_lanes=3`. 대본 `Gate`(fixtures/agent.sh)가 런타임 안에서 `colab lane delegate` 를 시도하고 토큰을 남긴 채 턴을 붙든다(서버 층은 살아 있는 토큰이 필요 — finish 뒤 401).

| 층 | reviewer(claude_code, MCP 표면) | reviewer(hermes, 래퍼 표면) | lead · custom |
|---|---|---|---|
| (a) MCP | session/new 의 colab MCP argv = `mcp serve --allow <10>`(acpfake record) → **그 argv 로 띄운 진짜 colab** 의 tools/list **10**, `colab_lane_delegate`·`artifact_submit`·`hitl_approve_request` 없음 (R8·R9) | (래퍼 표면 — mcpServers 무시) | argv `--allow <13>` → tools/list **13**, delegate 있음 (L4·L5·C2) |
| (b) CLI | `colab lane delegate` → **exit 3** `command_not_allowed/reviewer/lane_delegate`, 문장 = 서버 §2.5 문장, 선(wire)에 **GET /cli/context 1 · POST /lanes 0**, lane 0, 서버 rejected 0 (R1~R6) | 프롬프트가 이름한 **래퍼 절대 경로**로 → exit 3, `role ""` 문장 괄호 생략, `error.allowed` = 래퍼가 export 한 10, 선에 **0 줄**(컨텍스트조차 없음) (H1~H6) | exit 0, lane 생성 (L1·L2·C1) |
| (c) 서버 | 같은 토큰 curl 직접 POST /lanes → **403** + task_event `status/rejected` 1행(우회분만), lane 0, 403 문장 == CLI 문장 글자 단위 (R11~R14) | 403 + rejected 1 (H8) | **201** · rejected 0 (L6·L7·C3) |
| 브리프 [2] | `_meta.systemPrompt.append` 에 거부 줄 · `colab lane delegate` 0 (R10) | `COLAB_BRIEF.md`(파일 전달)에 거부 줄 · 래퍼 경로 · delegate 0 (H7) | — |
| 번들 | `task.allowed_commands` 10 (claim 탭, R7) | 10 (래퍼 env) | 13 (L3) |

- **(d) 사람(쿠키) 경로는 비게이트**(NN4): Director·멤버의 `/note` 메시지 201 · getSession/listLanes/listMessages 200 · Director 의 POST /lanes·/decisions 는 **403 `agent_only`**(사람 권한 규칙 — 「새 작업 줄기로 보내기」·HITL 카드), `command_not_allowed` 0 · 세션 전체 rejected 행 = 2(R·RH 의 curl 우회분뿐) (D1~D6).
- **(e) 관찰 표**: 5행 §11 순서, 5행 모두 n ≥ 1(chain_scale 4 · chain_depth 1 · join_breadth 2 · routing 8 · empty_turn 8), routing breakdown 9종(규칙 2 = 7 · platform 1), **empty_turn_rate = 3/8 = 0.375** 가 DB 와 같은 수(Idle 의 빈 대본 턴 3 = `status/turn_end/empty_turn` 카드 3, 게이트 턴에는 0) (O1~O5·L14·Q2·Q3). 대시보드: `/settings?tab=dashboard` 200 · 프록시 5행 · agent-browser DOM `observation-row` 5 · `empty_turn_rate` 측정 가능 (W0~W2b, `web/__screenshots__/p5-82-s14-observations.png`). S7: Idle lane 이력 → 「활동」에 `feed-row-empty-turn` ≥ 1 · 카드 `lane-empty-turn` 1 (W3·W3b, `web/__screenshots__/v018/p5-82-s7-empty-turn.png`).
- **(f) I-2 lane.actions 실서버**: running → `restart,cancel` · queued → `cancel` · waiting_human → `respond_hitl` · done → `[]` · **멤버(비제어자)는 running 도 `[]`** (L9~L11·L13·C5·C6·Q1). 웹 목의 규칙(S-83 교훈)과 같다.

## 3. 72_ A2d 흔들림 — 원인 · 고침 · 연속 3회

- **사건**: PR #249 CI run 34989845714 **attempt 1** `A2d 동시 3개 (위임 3이 병렬) got=2 want=3`(attempt 2 는 통과). 나머지 45/1.
- **원인**: 페이크 턴이 0.2s 라 첫 Researcher lane 이 셋째 lane 의 `started_at` 전에 끝난다 — 스윕(`running_overlap`)이 2 를 본다. 병렬성 결함이 아니라 **표본 시점에 3 중 2 만 running**.
- **고침(단언 그대로)**: 대본 Researcher 에 **barrier**(형제 표식 3 개가 모일 때까지 ≤ 30s, 모이면 2s 더 붙듦; 안 모이면 그냥 진행 → A2d 가 제대로 FAIL) + 하네스 `wait_step`(lib_i5, **I-1**: 단계별 대기가 판정 행) 으로 "Researcher task 3 이 동시에 running" 을 0.3s 폴링(`A2d0`).
- **결과**: 로컬 연속 3회 3/3(overlap 3, 6s) · CI 연속 3회 3/3(run 34995413656 attempt 1·2·3, 72_ 47/0 · A2d0 폴링이 3 을 잡았다).

## 4. 실기 대조 1회 — claude_code reviewer 1턴 (`RUNTIME=real bash e2e/p5/82_role_gate.sh`, 9/0)

로컬 claude_code 2.1.258 · adapter 0.74.0 · haiku. 세션 goal 이 (1) 브리프 [2] 의 `- 이 역할은` 줄 인용 (2) **Lead 에게 위임을 시도**하고 결과 보고 (3) DONE 을 시켰다. 실측 $0.0423(캐시 쓰기 포함) · 38s.

- 데몬 로그 `allowed commands: <reviewer 10> (denied: lane_delegate,artifact_submit,hitl_approve_request)` 1줄 (R3).
- raw system/init 툴 목록 = `colab_artifact_get,…,colab_status_set` **10**, `colab_lane_delegate` 없음 (R4).
- 에이전트 메시지(`out/82-real-messages.txt`): `- 이 역할은 위임 · 산출물 제출 · 완료 승인 요청을 쓰지 않는다.` / **`No delegation tool available. The colab commands I have access to do not include a delegation tool (colab_delegate or similar).`** / `DONE`.
- 피드(`out/82-real-feed.txt`): `ToolSearch` 1회(툴을 찾아봤다) → `colab_message_post` 3회. delegate 를 시도한 카드 0 → lane 0 · 서버 rejected 0 (R6·R7). 즉 실기는 (a) MCP 층에서 끝났다 — 툴이 없으니 시도조차 없다. (b) CLI exit 3 는 Bash 가 열린 런타임에서만 생기고, 그 경로는 82_ 페이크(래퍼·컨텍스트 모드)와 84_ 이 잰다.

## 5. I-3 비용 한 줄

`e2e/p5/README.md` 표 3개에 「비용 한 줄」 열, 스크립트 70_~84_ 머리에 `# 비용 한 줄(I-3): 턴 · 실기 $ · 소요`. 실기 금지 표시: 76_(≈ 250턴, haiku $1~2, 20분+).

## 6. 열린 결함 (번호 없음 — Lead 가 준다)

| 층 | 무엇 | 근거 | 영향 · 제안 |
|---|---|---|---|
| 데몬 | **capacity 초과 창**: claim 루프가 `free = capacity - len(d.running)` 을 다시 세는데, claim 응답의 task 는 `runAttempt` 안에서 workdir 준비·래퍼 작성 뒤에야 `d.running` 에 들어간다(`loop.go` claim 루프 vs `d.running[k] = run`). 그 사이 다음 claim 이 `capacity` 만큼 또 나간다 | 82_ 1차 실행 데몬 로그(capacity 3): R·RH·C 가 `phase running` 인 채로 Idle 이 claim·running(01:13:48, `turn outcome` 전) → 4 동시. 그래서 82_ (f) 의 queued 를 세션 `limits.max_parallel_lanes` 로 바꿨다 | 짧은 턴이 많을 때 capacity 가 N+k 로 샌다(S13 capacity 열·E13-16 분모). 제안: `start()` 에서 `d.running` 에 자리(placeholder)를 먼저 잡고 runAttempt 가 채우기 |
| 웹 유닛(기존) | `app/(app)/settings/page.test.tsx` S14 「루프 상한/작업 폴더 탭 … 바꾼 칸만 PATCH」가 `settings-dirty` 를 못 찾고 간헐 실패 | 이 PR run attempt 1(「루프 상한 탭」) · dev run 34988310438(같은 파일 「작업 폴더 탭」). 로컬 3회 21/21 | 웹 코드 무관(이 PR 은 e2e 만). 대기 없이 `getByTestId` 를 쓰는 자리로 보인다 — T-W 몫 |
| e2e(로컬 함정) | 탭 포트 겹침 — 73_ 탭 :8120 · 74_ :8121 · 84_ `SERVER_URL+10` 이 T-I6 서버(:8120)·72_ 탭과 겹칠 수 있다 | README 함정 절 | `TAP_PORT_72/73/74/82` export. CI(:8109)는 무관 |

결함 아님(확인): Director 의 POST /lanes·/decisions 403 은 `agent_only`(설계) · S7 lane 카드의 빈 턴 한 줄은 이력 「활동」을 연 뒤에만(T-W16 Lead A, 설계).

## 7. 산출물

`e2e/p5/82_role_gate.sh` · `84_cli_allowed_commands.sh`(이동) · `ci.sh`(SCRIPTS · 스위치 표 · 두 판정 표 모양) · `lib.sh`(PSQL_URL) · `lib_i5.sh`(`wait_step`) · `fixtures/agent.sh`(Researcher barrier · Gate · Asker) · `72_scenario_a.sh`(A2d0) · README(T-I6 절 · 비용 열) · 스크립트 머리 비용 한 줄 · `web/__screenshots__/p5-82-*.png`·`web/__screenshots__/v018/p5-82-*.png` · 이 문서.
