# G6 판정 자료 — HITL 왕복 · 중복 0 · 예산 · deputy · 시나리오 C·D (T-I3)

| 항목 | 내용 |
|---|---|
| 게이트 | `PLAN.md` §6.2 **G6** — "시나리오 C·D + **중복 0**". **컷 3**: 미통과면 P4 를 열지 않고 P3 를 마감한다 |
| 앞 게이트 | **G5 통과**(`plan/G5_DECISION.md`) — 시나리오 A 8단계 + Hermes + 템플릿 3분. 재측정 PASS 162 · FAIL 0 |
| 작성 | Integrator (T-I3), 2026-09-07 |
| 스택 (2판) | dev **`94c4143`** — 바이너리·`next build` **2026-09-07 01:43:09 KST**, 마이그레이션 15개, **DB 재생성**. server `:8100` · web `:3020` · Postgres `colab-pg-g6 :5442` |
| 스택 (1판) | 1차 측정 dev `957ffd3`(서버 #124 · 웹 #130 머지 뒤), **`48_` 재측정은 계약 #133 · CLI #134 · 서버 #136 을 머지한 `5ed5dfc`**(빌드 2026-09-07 00:50:46 KST). `bin/server`·`bin/daemon`·`bin/colab` 빌드 시각 **2026-09-06 23:51:18 KST**. server `:8100` · web `:3020`(`next build` + `next start`) · Postgres `colab-pg-g6 :5442` — 다른 워커 스택(P1 `:8080/:5435` · P2 `:8090/:5436` · G5 `:5437` · 스파이크 4c `:8095/:5441`)과 포트·컨테이너·workdir 를 분리했다(§0-13) |
| 런타임 | Claude Code CLI 2.1.258 + 어댑터 `@agentclientprotocol/claude-agent-acp` **0.74.0**(핀) · **Hermes 0.20.6**. 모델은 비용 때문에 haiku(`claude-haiku-4-5-20251001`) |
| 재현 | `bash e2e/p3/up.sh` 뒤 `48_`~`53_` + `fixtures/g_user_approval_card.sh` — 순서·비용·함정은 `e2e/p3/README.md` |
| 총계 (2판, `94c4143`) | **PASS 331 · FAIL 3** — 48_ 76/0 · 49_ 33/0 · 50_ **114/3** · 51_ 38/0 · 52_ 40/0 · 53_ 22/0 · (g) 8/0. 여기에 (g) 가 재실행한 `e2e/p2/33_` 이 **41/0**(별도 집계). **§9** 가 2판이다 |
| 총계 (1판, `957ffd3`) | PASS 259 · FAIL 10 — 48_ 76/0(핫픽스 뒤 재측정) · 49_ 33/0 · 50_ 47/7 · 51_ 37/0 · 52_ 40/0 · 53_ 22/0 · (g) 4/3 |
| 결론 | **G6 충족(2판, §9).** 1판의 차단 다섯(K-7·C-4 · S-44 · S-45 · W-6 · D-17 · K-8)은 핫픽스 #133·#134·#136·#139·#142·#145·#147 로 **전부 닫혔고 실기로 확인**했다. `50_` 은 이제 **데몬 대역 우회가 하나도 없다** — 예산 초과·취소·정지·상향·재개가 전부 에이전트가 실제로 쓴 돈으로 일어난다. 남은 것은 **신규 결함 1건(서버)**: 예산으로 `paused` 된 task 의 `finish` 가 500 이라 attempt 기록과 `runtime_session_ref` 가 유실되고, 승인 뒤 재개가 resume 이 아니라 **콜드 스타트**가 된다(§9.5). 판정은 §9.7 — 아래 §1~§8 은 **1판 그대로** 남긴다 |

> **읽는 법.** 각 절은 EVAL 행 번호를 그대로 쓴다. `우회`라고 적힌 것은 정식 경로가 막혀 다른 경로로
> 돌아간 측정이고, 그 자리에는 반드시 **결함 번호**가 붙어 있다. 결함은 `S`(서버) `W`(웹) `D`(데몬)
> `C`(CLI) `K`(계약·교차) 로 스트림을 귀속했다 — 번호는 Lead 가 줬다(§0-11).
> 체크 표 전문은 `e2e/p3/out/*-checks.tsv`, 요약 JSON 은 `e2e/p3/out/4*.json` 이다.

---

## 0. 방법

- 수치는 전부 **서버 DB 의 단일 클럭**에서 센다(`e2e/p3/lib.sh` 의 관측 질의). 화면 판정만
  `agent-browser` 로 실제 DOM 을 읽는다(§0-9).
- 서버가 데몬에 보내는 `TaskBundle` 은 claim 탭(`e2e/p2/fixtures/claimtap.py`)이 기록한다 —
  턴 프롬프트·브리프·`resume` 는 디스크에 남지 않으므로 이것 없이는 "답변이 프롬프트에 들어갔다"를
  증명할 수 없다. T-I3 는 **attempt 별로** 꺼내는 픽스처를 새로 뒀다(`fixtures/prompt_of.py`·`brief_of.py`):
  P2 의 `prompt_of_task.py` 는 attempt 를 가리지 않아 attempt 1·2 가 섞이고, 그러면 재개 판정이 항상 참이 된다.
- 과제는 저장소 밖의 무해한 주제(가상의 실내 화분 자동 급수기 제품 Y · 스마트 물병)다. 세션 goal·지시문에
  이 저장소의 파일·스크립트 이름을 쓰지 않는다(G3_DECISION §2 X-2).
- 프로세스 종료는 pid·pgid·포트만(§0-10). 이 머신에는 다른 워커의 데몬이 함께 떠 있다.

### 0.1 이번에 새로 쓴 자극 세 가지 (전부 계약 위의 경로다)

| 자극 | 무엇을 대신하나 | 왜 필요했나 |
|---|---|---|
| `backdate_hitl`(lib.sh) | 클럭 주입 | 서버 바이너리에 클럭 주입 경로가 없다(`clock.Real{}` 고정). `hitl.Authorize` 가 보는 값은 `Elapsed = now - created_at` 과 `DueIn = due_at - created_at` 뿐이라 **둘을 같은 만큼 과거로 밀면** 기한 길이 24h 는 보존한 채 경과 시간만 옮길 수 있다. 서버·계약 무수정 |
| `fixtures/daemon_heartbeat.sh` | 데몬의 턴 중 usage 보고 | 데몬이 턴 중에 usage 를 **올리지 않아서**(D-17) 예산 강제가 발동하지 않는다. daemon-protocol §4.2 의 와이어 그대로 데몬 토큰으로 heartbeat 를 보낸다 — 서버가 하는 일은 실제 데몬이 보냈을 때와 같다 |
| 에이전트 턴 안의 `curl` | `colab hitl ask` | 1차에서 CLI 가 openapi 에 없는 경로를 불렀다(K-7·C-4). 에이전트가 자기 `COLAB_TASK_TOKEN` 으로 `createHitlRequest` 를 직접 부른다 — 서버가 보는 것은 정식 경로와 **완전히 같다**(`source=agent` · `pending_hitl` · 카드 · 인박스). **핫픽스 뒤 재측정에서는 쓰이지 않았다**(정식 도구가 성공하면 건너뛴다) |

세 자극 모두 **서버·계약·구현을 수정하지 않는다.** 대역으로 채운 부분은 각 절에서 FAIL 로 남겼다.

---

## 1. 총계

| 스크립트 | 항목 | PASS | FAIL | 비고 |
|---|---|---|---|---|
| `48_hitl_roundtrip.sh` | (a) HITL 왕복 | **76** | 0 | 핫픽스(#133·#134) 뒤 재측정. 1차(`957ffd3`)는 69/4 — 네 칸 모두 K-7·C-4 |
| `49_partial_exec_dup0.sh` | (b) 중복 0 | 33 | 0 | |
| `50_budget_pause_override.sh` | (c) 예산 | 47 | **7** | D-17 1 · K-8 3 · S-45 2 · W-6 1 |
| `51_deputy_and_cancel.sh` | (d) deputy·취소 | 37 | 0 | |
| `52_scenario_c.sh` | (e) 시나리오 C | 40 | 0 | |
| `53_scenario_d.sh` | (f) 시나리오 D 재확인 | 22 | 0 | |
| `fixtures/g_user_approval_card.sh` | (g) 완료 승인 카드 | 4 | **3** | S-45 |
| ↳ 그 안에서 재실행한 `e2e/p2/33_` | E6-01·03·04 재확인 | 41 | 0 | 별도 집계 |
| **합계**(33_ 제외) | | **259** | **10** | 결함에 전부 귀속된다(§2) |

---

## 2. 결함 (스트림 귀속 · 번호는 Lead)

| # | 스트림 | 내용 | 근거 | 차단? |
|---|---|---|---|---|
| ~~**K-7**~~ | 계약 | `contracts/colab-cli.md` v0.5 §2.4 는 `colab hitl ask·approve-request·request-info` 의 경로를 `POST /v1/tasks/{T}/hitl` 로 적는데 **openapi 에 그 경로가 없다** — `createHitlRequest` 는 `POST /sessions/{S}/hitl-requests` 뿐이다 | §2.1 | **해결 — 계약 #133** |
| ~~**C-4**~~ | CLI | CLI(PR #126, `cli/internal/client/ops_p3.go:18`)가 colab-cli.md 를 따라 존재하지 않는 경로를 부른다 → 두 도구 표면(MCP·cli_wrapper) 모두 **404**. 목 서버 스모크가 이것을 잡지 못했다(#126 리뷰 NN1 이 예고한 그대로) | §2.1 | **해결 — CLI #134** |
| ~~**D-17**~~ | 데몬 | `acp.Runner.recordUsage` 가 `session/prompt` **응답에서만** 호출된다(`runner.go:955`) → 턴 중 heartbeat 의 `usage` 는 언제나 0 → 서버 가드(`daemon.go:442`)가 거짓이라 `enforceBudgetFor` 미호출 | §2.2 | **해결 — 데몬 #145** (§9.1) |
| ~~**S-44**~~ | 서버 | `enforceBudgetFor` 는 heartbeat 한 곳에서만 호출된다 — `tasks.Finish` 에서 부르지 않아 **사후 강제도 없다**(`budget.go:88` 주석은 부른다고 적었다) | §2.2 | **해결 — 서버 #136**. 다만 그 호출부는 오늘의 런타임에서 실기에 도달하지 않는다(§9.4) |
| ~~**K-8**~~ | 계약·교차 | ACP 경로는 `cost_usd` 를 주지 않아 **런타임이 만든 `task_usage` 행이 72/72 전부 `estimated: true`** 다(claude_code·hermes 모두). 그리고 `RecordTurnUsage` 는 `estimated: true` 보고의 금액을 **0 으로 떨어뜨린다**(harness v0.7.1) → 추정 경로도 강제에 도달하지 못한다. **D-17 만 고쳐도 예산은 여전히 발동하지 않는다** | §2.2 | **해결 — 서버 #147**(S-48). 그리고 #145 로 claude_code 가 실측 비용을 준다 (§9.1) |
| ~~**S-45**~~ | 서버 | 시스템 발행 HITL 3곳(`budget.go:188` · `sessions/complete.go:216` · `router/service.go:500`)이 `kind='hitl'` 타임라인 메시지를 만들지 않고 `message_id` 가 NULL 이다 → S7 에 카드가 **아예 없다**(SCREEN §4.5 위반). 에이전트 발행 경로만 게시한다(`handlers_hitl_p3.go:203`) | §2.3 · §6 | **해결 — 서버 #142** (§9.1) |
| ~~**W-6**~~ | 웹 | 인박스의 `hitl_request` 항목이 `HitlBody` 를 `budgetOverride` 없이 그린다(`InboxItemCard.tsx:159`) — 상향 입력칸은 `item.type === "session_paused"` 조건이라 붙지 않는다. task 범위 예산 초과는 세션을 멈추지 않으므로(E9-01) 이 항목은 영영 `session_paused` 가 아니다 → **웹에서 금액을 정할 자리가 없다** | §2.3 | **해결 — 웹 #139** (§9.1) |

| **신규** | 서버 | **예산으로 `paused` 된 task 의 `finish` 가 500 이다.** `tasks.Finish` 가 `completed` 아닌 outcome 을 `cancelRequested` 때문에 `cancelled` 로 바꾸고, `cancelLocked` 가 `paused_reason` 만 지우고 `paused_detail` 을 남겨 `task_paused_detail_check`(0006)를 깬다 → attempt 기록과 `lane.runtime_session_ref` 유실 → 승인 뒤 재개가 **콜드 스타트**(E9-02 "resume 우선" 미충족) | **§9.5** | Lead 판정 |
| **관찰** | 서버 | 턴 종료와 경합한 취소는 흡수되지 않는다 — `completed` finish 는 `cancelRequested` 를 보지 않아 피드는 "사람이 중단함", 화면은 완료 | **§9.6** | Lead 판정 |

관찰(결함 아님, §4 에 상술): 예비 회차 한 번에서 resume arm 의 "workdir 먼저 확인" 이 어긋났다 — 최종 회차는 2/2 로 지켰다.

### 2.1 K-7 · C-4 — 에이전트가 HITL 을 **열 수 없다**

첫 실서버 회차의 실측이다. `colab_hitl_ask` MCP 툴이 두 번 다 이렇게 돌아왔다.

```
{"error":{"code":"refused","exit":3,"ok":false,"status":404,"title":"Not Found"}}
```

`env | grep -i colab` 로 확인한 attempt 환경은 정상이었고(`COLAB_TASK_TOKEN`·`COLAB_SERVER_URL`·
`COLAB_TASK_ID` 전부 있음), 직접 확인해도 같다.

```
curl -X POST $SERVER/api/v1/tasks/{T}/hitl   → 404
POST /sessions/{S}/hitl-requests             → 201   (openapi createHitlRequest, 서버가 구현한 것)
```

**두 계약이 갈려 있다.** `colab-cli.md` v0.5 §2.4 는 `POST /v1/tasks/{T}/hitl` 로 적고 CLI 가 그대로
구현했고, `openapi.yaml` 에는 그 경로가 없다. 서버(PR #124)는 openapi 만 구현한다. 목 서버
(`e2e/p3/mock_hitl_server.py`)는 **CLI 가 보내는 것을 그대로 받아** 주므로 T-C4 스모크는 초록이었다 —
PR #126 리뷰 NN1 이 "목은 openapi 를 읽지 않는다" 고 적어 둔 그 구멍이다.

이 결함의 값은 404 하나가 아니다. 실측에서 에이전트는 툴이 실패하자 **30여 번의 툴 호출로 저장소를
뒤졌다**(`which colab` → `find ~ -name colab` → `git log --grep=hitl` → `git show …:contracts/colab-cli.md`).
X-2 가 말하는 사고의 변종이다 — goal 에 저장소 이름을 쓰지 않아도, **도구가 실패하면** 에이전트는
workdir 위의 저장소를 뒤지러 간다. 지시문에 "저장소를 뒤지지 마라" 를 넣어 2회차부터 막았다(§8).

**Lead 판정: openapi 가 API SSOT.** `colab-cli.md` 를 openapi 에 맞추는 계약 **#133**, CLI 핫픽스 **#134**.

**재측정(핫픽스 뒤, `5ed5dfc`): 닫혔다.** `48_` 을 다시 돌려 **76/0** 이 나왔고, 이번에는 우회가 한 번도
쓰이지 않았다 — 두 도구 표면이 다 선다.

| 도구 표면 | 1차(`957ffd3`) | 재측정(`5ed5dfc`) |
|---|---|---|
| Claude Code · `mcp` (`colab_hitl_ask`) | 404, 등록 실패 | **등록 성공** — `{"hitl_id":…,"turn_end_required":true}` → `waiting_human` |
| Hermes · `cli_wrapper` (래퍼 `colab hitl ask`) | 404, 등록 0건 | **등록 성공** → `waiting_human` → 답변 → 재개 → 에이전트가 답을 게시(왕복 완주) |

§0.1 의 `curl` 자극은 스크립트에 남아 있지만 **정식 도구가 성공하면 건너뛴다** — 회귀가 나면
`T1`(404 0건)·`H1`(등록 1건)이 먼저 붉어진다.

### 2.2 D-17 · S-44 · K-8 — 예산 강제가 **한 번도 발동한 적이 없다**

1회차 실측(축소 상한 $0.002, 실제 턴 비용 $0.0599):

| 관측 | 값 |
|---|---|
| task 최종 상태 | `completed` |
| 세션 | `active` |
| HITL | **0건** |
| `daemon_command(type=cancel)` | **0건** |
| `task_usage` | cost $0.0599 · **`estimated: true`** · `updated_at` = 턴 종료 시각 |

원인은 셋이 겹쳐 있다.

1. **D-17.** `acp.Runner.recordUsage` 는 `session/prompt` **응답에서만** 호출된다 — 턴은 프롬프트 하나이므로
   호출은 턴당 한 번, 끝에서다. 그래서 heartbeat 의 `r.Usage()` 는 턴 내내 0 이고, 서버의
   `if in.Usage.InputTokens > 0 || …` 가드가 거짓이라 `RecordTurnUsage`·`enforceBudgetFor` 가 아예 불리지 않는다.
   50_ 의 **D1** 이 이것을 직접 잰다: 턴 시작 50초(heartbeat 3회) 뒤 `task_usage` **행 0개**.
2. **S-44.** `enforceBudgetFor` 는 `daemon.go:445` 한 곳에서만 호출된다. `budget.go:88` 의 주석은
   "production callers: daemonHeartbeat … 그리고 tasks.Finish 의 rollup" 이라고 적었지만 **그 호출이 없다**
   (테스트 제외 grep 0건). 그래서 턴이 끝난 뒤에도 강제가 없다.
3. **K-8.** 설령 1·2 를 고쳐도 오늘의 런타임으로는 발동하지 않는다. ACP 어댑터는 `cost_usd` 를 주지
   않으므로 `recordUsage` 가 `Estimated=true, CostUSD=0` 으로 접고(harness v0.7.1), 서버의
   `RecordTurnUsage` 는 `estimated` 보고의 금액을 **0 으로 떨어뜨린다**(가격은 roll-up 이 매긴다).
   즉 heartbeat 로 오는 금액은 **언제나 0** 이다. 실측(G6 DB 전체): `task_usage` 76행 중 72행이
   `estimated: true` 이고, 나머지 4행은 이 스크립트의 **데몬 대역이 `estimated:false` 로 넣은 것**이다 —
   **런타임이 만든 행은 72/72 전부 추정이다**(claude_code 70 · hermes 2).

세 번째가 이 절의 핵심이다. **D-17 만 고치면 "턴 중에 0 을 올리는" 데몬이 될 뿐이다.** 강제가 서려면
(a) 데몬이 턴 중에 값을 올리고, (b) 서버가 추정 보고에도 가격표를 매겨 비교하거나 finish 에서 한 번 더
재는(S-44) 길이 있어야 한다. 어느 쪽을 택할지는 Lead·서버 스트림의 결정이다.

**대역 측정.** `fixtures/daemon_heartbeat.sh` 로 §4.2 와이어 그대로 usage 를 보내면 서버의 강제 경로는
**전부 계약대로 동작한다**(§5 표: E9-01·02·08·03 전부 PASS). 즉 서버의 판정 로직은 옳고, 그 로직에
**숫자가 도달하지 않는 것**이 결함이다.

### 2.3 S-45 · W-6 — 예산을 사람이 **웹에서 풀 수 없다**

시스템이 발행한 예산 HITL 은 `hitl_request` 행과 인박스 항목까지는 정상으로 만들어진다. 그런데,

- **S-45**: 그 행의 `message_id` 가 **NULL** 이고 세션 타임라인에 `kind='hitl'` 메시지가 없다. S7 은
  `kind='hitl'` 메시지를 `HitlCard` 로 그리므로(`sessions/[id]/page.tsx:626`) 카드가 화면에 서지 않는다.
  실측: 50_ 의 W1 — S7 의 `[data-testid="hitl-card"][data-purpose="budget"]` **0개**.
  같은 원인이 (g) 의 완료 승인 카드에도 그대로 걸린다(§6) — 즉 **시스템 발행 HITL 전부**가 화면에 없다.
- **W-6**: 인박스에는 항목이 뜨지만(`inbox-item[data-type=hitl_request]` 1개) 그 카드는 `HitlBody` 를
  `budgetOverride` 없이 그린다. 상향 입력칸(`inbox-budget-input`)은 `item.type === "session_paused"`
  조건인데, **task 범위** 예산 초과는 세션을 멈추지 않으므로(E9-01 이 그렇게 정한다) 이 항목이
  `session_paused` 가 되는 일은 없다. 결과: 승인·거절 버튼만 있고 **금액을 정할 칸이 없다.**

FR-7.3 이 정한 사람의 개입 경로가 웹에 없다 — Lead 판정도 **차단**이다. 뒷단계(override 저장 · 에이전트
상한 불변 · 같은 lane·workdir 재개 · E9-08)는 정식 op(`respondHitlRequest` + `budget_override_usd`)로
우회해 전부 쟀고 **전부 PASS** 다(§5). FAIL 로 남은 것은 "웹에서 낸다" 한 칸이다.

---

## 3. (a) HITL 왕복 — `48_hitl_roundtrip.sh` (E7-01·03·04·06·17·18, E8-01·02)

네 arm 을 **capacity=1 데몬 하나**에 얹었다. capacity 1 은 우연이 아니다 — `waiting_human` 이 동시 실행
슬롯을 잡고 있으면 다른 lane 의 task 가 **영원히 queued 로 남는다**. 그래서 "슬롯 미점유" 가 반증 가능해진다.

| arm | 런타임 · 도구 표면 | 무엇을 재나 |
|---|---|---|
| A1 | claude_code · mcp | 등록 → 턴 종료 → `waiting_human` → **웹 인박스에서 답** → 재개(resume 우선) |
| A2 | claude_code · mcp | 같은 왕복을 **강제 콜드 스타트**로(transcript 삭제, E8-02) |
| A3 | hermes · cli_wrapper | 두 번째 도구 표면 — 등록 + 왕복 |
| A4 | claude_code · mcp | `approval` **거절**(E7-17) |

### 3.1 E7-03 — 턴 종료 · 프로세스 없음 · 슬롯 미점유

| 항목 | 실측 |
|---|---|
| task / lane 상태 | `waiting_human` / `waiting_human` |
| attempt 1 의 프로세스(pgid 기준) | **0** |
| workdir 디렉토리 | 보존됨 |
| 세션 | `active` 유지 |
| 이 턴이 게시한 일반 메시지 | **0건**(등록 뒤 바로 끝냈다) |
| 타임라인 HITL 카드(`message.kind='hitl'`) | 1건 |
| Director 인박스 | `hitl_request` 1건 · `severity=action_required` |
| HITL 행 | `source=agent` · `type=question` · `purpose=agent` · `approver_spec=director` · `proposed_default` 있음 · `task_id` 채워짐 |

**E7-18(슬롯 미점유 · 다른 lane 계속).** Asker 가 `waiting_human` 인 동안 같은 세션의 Peer 를 트리거했다.
**capacity=1 인데 Peer 의 task 가 dispatch 되어 `completed` 로 끝났고**, 그 사이 Asker 는 계속
`waiting_human`, 세션은 `active` 였다. `waiting_human` 은 슬롯을 잡지 않는다.

### 3.2 E8-01 — 웹에서 답 → 재개, 답이 프롬프트에

답변은 **웹 인박스(S8)** 에서 냈다(§0-9 DOM: `inbox-item[data-type=hitl_request]` → `hitl-answer-input`
→ `hitl-answer`). 스크린샷 `web/__screenshots__/p3-48-01-inbox.png`·`p3-48-02-inbox-answer.png`.

| 항목 | 실측 |
|---|---|
| HITL | `answered`, 저장된 답 = 화면에 입력한 값 그대로 |
| 결정 기록 | **1건**, `source=hitl` |
| 인박스 | 그 항목이 닫혔다(읽지 않은 항목 0) |
| 재큐잉 | **같은 task**, `attempt` 2, 세션의 task 수 그대로(재지시가 아니다) |
| resume | `task_attempt.resumed = **true**` — resume 우선(E8-01) |
| 프롬프트(attempt 2) | `<resumed attempt=2>` · `<hitl_answer sections="question_answer">` · `question: …` · `answer: 관리사무소 담당자` |
| 에이전트 행동 | 그 답을 읽고 `ANSWERED: 관리사무소 담당자` 게시 · 같은 workdir 에 파일 |

`<hitl_answer>` 구간은 PR #124 리뷰 R1("사람의 답이 프롬프트에 도달하지 않는다")이 닫은 자리다 —
실기에서 실제로 도달한다.

### 3.3 E8-02 — 강제 콜드 스타트에서도 이어간다

`~/.claude/projects/<cwd 인코딩>/<sessionId>.jsonl` 을 지워 유실을 만들었다(스파이크 4c 와 같은 정식 유도).

| 항목 | 실측 |
|---|---|
| attempt 2 `resumed` | **false**(콜드 스타트) |
| 프롬프트 | `<hitl_answer>` 있음 · `answer: 아파트 관리사무소` 있음 · "inspect the current state of the workdir" 있음 |
| 결과 | **이어갔다** — `ANSWERED: 아파트 관리사무소` 게시, 같은 workdir 에 파일 |

resume 이 붙은 A1 과 콜드 스타트한 A2 의 **결과가 같다.** 스파이크 4c 의 정성 결론이 HITL 왕복에서도 선다.

### 3.4 E7-17 — 거절도 재개다

| 항목 | 실측 |
|---|---|
| HITL | `answered` · `approved=false` |
| task | `failed` **아님** → 재큐잉되어 `completed` |
| 프롬프트 | `approved: false` · `reason: 출처가 없어 이대로는 못 쓴다` · `sections="approval_result"` |
| 에이전트 | 사유를 읽고 `REJECTED: …` 게시 |
| 결정 기록 | 1건 |
| `approval` 의 `proposed_default` | **없음**(E7-06 — 절대 자동 진행되지 않는다) |

### 3.5 두 도구 표면 — 1차 FAIL 4건 → 재측정 0건

1차(`957ffd3`)에서 네 칸이 붉었고 **전부 K-7·C-4** 였다. 계약 #133 · CLI #134 가 머지된 뒤
`5ed5dfc` 로 다시 돌린 결과가 오른쪽 칸이다.

| id | 항목 | 1차(`957ffd3`) | 재측정(`5ed5dfc`) |
|---|---|---|---|
| T1 | MCP 도구 표면이 HITL 을 등록한다 | 404, 등록 실패 | **PASS** — 404 0건 |
| H1 | cli_wrapper 도구 표면이 HITL 을 등록한다 | 등록 0건 | **PASS** — 1건 |
| H1b | 그래서 Hermes 턴이 `waiting_human` 으로 끝난다 | `completed` | **PASS** — `waiting_human` |
| H2~H3 | Hermes(cli_wrapper) HITL 왕복 | 서지 않음 | **PASS** — 답변 → 재개 → 에이전트가 `ANSWERED:` 게시 |

cli_wrapper 경로는 **우회할 수 없었다**: 래퍼는 위생화된 env(`env -i`)에서 돌고 토큰을 나르는 것이
래퍼 파일 자신이라(harness §10) 에이전트가 `COLAB_TASK_TOKEN` 을 볼 수 없다. 그래서 1차에서 이 arm 은
프로브 하나로 남았고, 지금은 **왕복까지 완주한다** — G6 의 (a) 는 두 표면 모두에서 선다.

---

## 4. (b) 중복 0 — `49_partial_exec_dup0.sh` (E8-04)

시뮬레이터 100회는 CI(T-P3a `test/sim`)가 돈다. 여기서 재는 것은 **실기 한 번**이다. 판정기는 스파이크
4c 의 것을 그대로 옮겨 썼다(`fixtures/measure_dup0.py`) — 같은 자를 써야 스파이크 표와 비교가 된다.

두 arm 을 한 데몬에 얹고 **한 번만** SIGKILL 해 3분 만료 창을 공유했다.

- `Bwarm` — warm-up 턴으로 `lane.runtime_session_ref` 를 심어 둔 상태(재개 경로)
- `Bcold` — 그 ref 를 비운 상태. **E8-04 의 실제 모양이다**: `runtime_session_ref` 는 `finish` 에서만
  저장되므로 데몬이 턴 도중 죽으면 그 attempt 의 ref 는 서버에 없다(SPIKE_04c §0.2)

| 런 | warm-up | kill 시점 | attempt 2 재개 판정 | 이어갔나 | 같은 파일 | **재게시** | **중복 편집** | workdir 먼저 |
|---|---|---|---|---|---|---|---|---|
| Bwarm | 있음 | 메시지 2 · 파일 절반 | `resumed` | ✅ | ✅ | **0** | **0** | ✅ |
| Bcold | 없음 | 메시지 2 · 파일 절반 | 콜드 스타트 | ✅ | ✅ | **0** | **0** | ✅ |

**중복 0 은 두 경로 모두에서 성립한다** — G6 이 요구한 핵심 수치다. 두 task 모두 `completed`,
재큐잉은 같은 task 의 `attempt` 2(재지시가 아니다).

프롬프트도 계약대로다. `<resumed attempt=2>` 안에 **이미 게시한 메시지 목록**이 있고,
`posted_message_ids` 는 kill 전 게시분 2건을 담는다. 스파이크 4c §5-1 이 "목록이 UUID 뿐이라 무엇을
게시했는지 알려주지 않는다"를 부족 항목으로 적었는데, **지금은 본문 한 줄이 같이 실린다** — 그
빈칸은 닫혔다:

```
<resumed attempt=2>
Your previous attempt (1) was interrupted: runtime_offline.
Messages you already posted (do not post them again):
- 244daefb-… — STAGE-A1 done
- 4b1adb35-… — STAGE-B1 done
Before continuing, inspect the current state of the workdir (changed files, git status) and continue from there.
</resumed>
```

**관찰(결함 아님).** 이 스크립트를 다듬는 동안 돌린 예비 회차 한 번에서 `Bwarm`(resume 경로)의 attempt 2
가 첫 결정적 툴로 **편집**을 골라 "workdir 먼저 확인" 이 어긋났다. 최종 회차는 두 arm 모두 지켰고,
어긋난 회차에서도 **재게시 0 · 중복 편집 0 · 이어감은 그대로**였다. 런타임이 이전 턴을 기억하고 있으면
"먼저 보라"를 건너뛸 유인이 있다 — 스파이크 4c 의 같은 arm 은 5/5 로 지켰으므로(n=5) 지금은
"드물게 어긋나되 결과는 바뀌지 않는다" 로 읽는다.

---

## 5. (c) 예산 — `50_budget_pause_override.sh` (E9-01·02·03·05·08)

금액은 EVAL 그대로다: `budget_per_task` **$1** → 턴 중 **$1.01** → 상향 **$3** → 재개 뒤 **$1.50**.
자극만 데몬 대역이다(§0.1 · §2.2). 세 세션(A 승인 · B 거절 · C 추정)의 turn-중 heartbeat 를
**한 자리에서** 냈다 — A 의 웹·승인·재개를 먼저 하면 그 사이 B·C 의 턴이 끝나 "턴 중" 이 성립하지 않는다.

### 5.1 E9-01 — 턴 중 초과 → `paused(budget)` + 시스템 HITL

| 항목 | 실측 |
|---|---|
| task | **`paused`** · `paused_reason=budget` (`failed` 아님) |
| lane | `paused` |
| 세션 | `active` 유지(task 범위 초과다) |
| 시스템 HITL | 1건 — `source=system` · `type=approval` · **`purpose=budget`** · **`task_id` 채워짐** |
| Director 인박스 | 1건 |
| 취소 | `daemon_command(type=cancel)` 1건, **`delivered_at` 채워짐**(S-35) — 프로세스 kill 이 아니라 §8.2.2 명령 |
| attempt 1 프로세스 | 0(명령을 받은 데몬이 절차대로 닫았다) |

`purpose=budget` 은 0012 가 넣은 칸이다 — `source: system` + `approval` 만으로는 완료 승인·루프 정지와
갈리지 않는다.

### 5.2 E9-02 · E9-08 — 상향 → 같은 lane·workdir 재개 → 취소 없음

웹에서 낼 자리가 없어(S-45·W-6) 정식 op 로 냈다. 그 뒤는 전부 실기다.

| 항목 | 실측 |
|---|---|
| HITL | `answered` · `approved=true` |
| `task.budget_override` | **3** |
| 에이전트 `budget_per_task` | **1.00 — 불변**(E9-02) |
| 재개 | 같은 task `attempt` 2 · 세션 task 수 1(새 트리거 불필요) · **같은 lane** · 같은 workdir |
| 프롬프트 | `<resumed attempt=2>` |
| **E9-08** | 재개 뒤 누적 **$1.50** 을 보고했는데 **취소 없음** — task `running` 유지, 새 예산 HITL 0, 새 cancel 명령 0 |
| 최종 | 재개한 턴이 `completed` |
| 결정 기록 | 1건 |

E9-08 은 "override 를 저장만 하고 강제 시점에 읽지 않으면 재개 즉시 다시 paused 가 된다"를 잡는 행이다.
읽고 있다.

### 5.3 E9-03 — 거절은 `paused` 유지

`approved:false` + 사유 → task **`paused(budget)` 유지**, `failed`·`cancelled` 아님,
`budget_override` 미저장, `attempt` 그대로 1(재큐잉 없음). 계약대로 "사람이 중단 버튼을 눌러야 끝난다".

### 5.4 E9-05 — 추정치 경로는 **잴 수가 없다** (FAIL 3 · K-8)

| 항목 | 실측 | 판정 |
|---|---|---|
| 추정 heartbeat 에 취소 명령 | 0건 | PASS(하드 컷 금지는 지켜진다) |
| 진행 중 턴이 취소됐나 | 아니오 | PASS |
| 서버가 추정 보고의 금액을 보존하나 | **아니오 — 0 으로 떨어뜨린다** | **FAIL(K-8)** |
| 세션이 `paused(budget)` 로 멈추나 | `active` | **FAIL** |
| "진행 중인 턴은 끝까지" 피드 기록 | 0건 | **FAIL** |

앞의 두 줄이 PASS 인 것은 **금액이 0 이라 아무 판정도 일어나지 않았기 때문**이다 — "하드 컷을 하지
않았다" 가 아니라 "아무 일도 없었다" 이다. 실기 usage 가 100% 추정인 이상(K-8) E9-05 는 대역으로도
서지 않는다. `acpfake` 나 유닛 골든이 이 분기를 지켜야 한다.

### 5.5 D1 — 데몬은 턴 중 usage 를 올리지 않는다 (FAIL 1 · D-17)

> **주의**: (c) 의 수치는 전부 **`957ffd3`**(서버 #136 이전)에서 잰 것이다. #136 이 finish 뒤 강제를
> 넣었으므로(S-44 · E9-10) 아래 D1 과 §5.4 의 판정은 재측정에서 달라질 수 있다 — Lead 지시대로
> `50_` 은 D-17·S-45·W-6 이 닫힌 뒤 **한 번에** 다시 돈다.

턴 시작 50초(heartbeat 3회) 뒤 `task_usage` **행 0개**. 그래서 그 시점까지 아무 강제도 없다(task `running`).
§2.2 참조.

### 5.6 비용 집계

`getSessionCost` 응답 정상, 세션 합계 > 0. 4단위(task·agent·session·runtime) 표시는 P3 웹(T-W3)이 이미
검증했고 여기서는 API 응답만 확인했다.

---

## 6. (d)·(e)·(f)·(g)

### 6.1 (d) deputy 위임 시점과 취소 권한 — `51_deputy_and_cancel.sh` (PASS 37 · FAIL 0)

Director·deputy·일반 멤버 **세 사람**을 초대 링크로 만들고 세션에 deputy 를 지정했다.
시각은 `backdate_hitl` 로 옮겼다(§0.1) — 서버·계약 무수정.

| EVAL | 자극 | 실측 |
|---|---|---|
| E7-11 | 일반 멤버가 응답 | `403` · **`can_respond_from: null`**(생기지 않을 권리에 시각을 약속하지 않는다). HITL 은 `open` 유지 |
| E7-09 | 발행 후 **11h** deputy 응답 | `403` · `code=deputy_not_yet` · `can_respond_from` = 발행 **+12h**. HITL `open` 유지 |
| E7-10 | 발행 후 **12h 2분** deputy 응답 | **수락** — `answered` · `approved=true` · 응답자가 deputy 로 기록 · 결정 기록 1건 |
| E10-05 | 멤버가 취소 | `403`, lane 계속 돈다 |
| E10-06 | deputy 가 취소 | **즉시 202**(시점 제한 없음) → lane `failed` · task `cancelled(failure_kind=cancelled)` · **3초** |
| E10-04 | 그 결과 | 활동 피드 `task_event(status/cancel)` 에 **"사람이 중단함"** · 새 task 0 · 프로세스 잔존 0 |
| — | 기한 기본값 | 24h(FR-5.4) |

**화면(§0-9 DOM).** deputy 에게 11h 시점 카드는 `data-permission="later"`, 승인 버튼 **비활성**,
문구 `🔒 01:36:12부터 응답 가능 — 기한의 절반이 지나면 deputy 에게 위임됩니다(FR-5.2)`.
12h 뒤 같은 카드가 `allowed` + 버튼 **활성**이 되고, 그 버튼으로 실제 승인이 통과했다.
일반 멤버에게는 `never` + "응답 권한이 없습니다"(카드는 보인다). lane 의 "중단" 버튼도
멤버에게 **보이되 비활성**이다(SCREEN §7 — 숨기지 않는다).
스크린샷 `p3-51-01-deputy-locked.png` · `p3-51-02-member-noright.png` · `p3-51-03-deputy-unlocked.png` ·
`p3-51-04-member-cancel-disabled.png`.

### 6.2 (e) 시나리오 C — `52_scenario_c.sh` (PASS 40 · FAIL 0)

세 자극을 **턴이 살아 있는 동안 한 자리에서** 냈다(끝난 lane 에 restart·cancel 은 409 다).

| E16-C 행 | 실측 |
|---|---|
| R `running` 중 Director 메시지 | **턴이 계속 돈다** — task `running`, **같은 pgid 가 살아 있고**, 취소 명령 **0건**, 이벤트가 계속 늘었다(19건). 새 지시는 **같은 lane 의 새 task 로 `queued`**(재지시가 아니다 — `restarted_from_task_id` 없음). 첫 턴은 **스스로** `completed` 로 끝나고 그 뒤 새 지시가 실행됐다(프롬프트 trigger = "한국 시장으로 좁혀줘") |
| "중단하고 다시 지시" | `restartLane` 202 → 이전 task `cancelled` · **새 task `attempt` 1 · `restarted_from_task_id` = 이전 task** · 같은 lane · **재지시 시점 lane 은 `running` 유지**. 프롬프트에 **`<resumed>` 없음**, "이미 게시한 메시지" 목록도 없음, 새 지시만(E8-06) |
| "중단" | `cancelLane` 202 → lane `failed` · task `cancelled(failure_kind=cancelled)` · 피드 "사람이 중단함" · 새 task 0 · 프로세스 잔존 0 · **12초** |

**결정 기록이 콜드 스타트를 넘는가(브리프 [7]).** 에이전트에게 `colab_decision_record` 로 결정을 남기게
하고(`recordDecision` 은 openapi 에서 TaskToken 전용이다 — 사람의 결정은 HITL 응답이 남긴다), 그 lane 의
런타임 transcript 를 지운 뒤 새 지시를 보냈다. 그 attempt 는 **콜드 스타트**였고, 브리프에
**`[7] Decision Log`** 구간과 그 결정("조사 범위를 한국 시장으로 좁힌다")이 실려 있었으며, 턴은 일을 했다.
**결정 기록은 콜드 스타트를 넘어 살아남는다.**

### 6.3 (f) 시나리오 D 재확인 — `53_scenario_d.sh` (PASS 22 · FAIL 0)

G5 에서 `30_scenario_a_hermes.sh` 의 arm C·D 로 통과한 항목을 P3 빌드에서 다시 쟀다.

| E16-D 행 | 실측 |
|---|---|
| hermes 실패 → 같은 머신 claude_code | attempt 1 `failure_kind=other`(재시도 가능) → 재큐잉 → task·lane 프로파일이 `spare`(claude_code)로 전환 → **`completed`**. 세션 런타임 고정(다른 머신 이동 0) |
| workdir 유지 | **경로가 같다**(문자열 동일) · `workdir` 행 1개(새로 만들지 않았다) · 폴백 뒤 `runtime_session_ref.runtime_kind = claude_code`(resume 비움 → 콜드 스타트) |
| **아티팩트 유지** | 폴백한 프로파일이 `product-y-guide.md` 를 제출했고 그 파일이 **같은 workdir 안**에 있다 |
| 대안 없음 | Director 인박스 `run_failed` **1건**, 재큐잉은 있었고(attempt 2건) 프로파일은 그대로, 런타임도 그대로 |

폴백 연결은 **여전히 DB 로 만든다**(G5 **S-24** — `AgentProfileCreate.fallback_profile` 을 서버가 조용히
버리고 `updateAgentProfile` 은 501). 재는 것은 "연결이 있을 때 전환하는가" 이고, 연결을 만드는 정식
경로가 없는 것은 S-24 로 열려 있다.

### 6.4 (g) 시나리오 A 의 마지막 `user_approval` — `fixtures/g_user_approval_card.sh`

`e2e/p2/33_approval_completed.sh` 를 G6 스택으로 재실행하고(E6-01·03·04 재확인), 그 세션의 화면을 봤다.

| 항목 | 실측 |
|---|---|
| 33_ 재실행 | **PASS 41 · FAIL 0** — E6-01(제출 → 플랫폼 승인 HITL) · E6-03(승인 → `completed`) · E6-04(거절 → `active` 유지)가 P3 빌드에서도 그대로 선다 |
| HITL 행 | `source=system` · **`purpose=user_approval`** |
| **S7 타임라인 카드** | **없음** — `[data-testid="hitl-card"]` 0개 · `hitl_request.message_id` NULL · `message(kind='hitl')` **0건** |
| S8 인박스 | 항목은 뜬다 |

**시나리오 A 의 마지막 승인은 "정식 HITL 카드" 로 돌지 않는다.** §2.3 의 **S-45** 와 같은 원인이고,
같은 결함이 예산·루프 정지 카드에도 걸린다. 인박스에서는 답할 수 있으므로 세션이 막히지는 않지만,
SCREEN §4.5 가 정한 중앙 타임라인의 자리가 비어 있다.

---

## 7. 판정 (컷 3) — 1판

> **이 절은 1판(`957ffd3`)의 판정이다. 핫픽스 뒤 재측정한 2판 판정은 §9.7 이고, 그것이 최종이다.**

**G6 미충족.** 근거는 "무엇이 실패했나" 가 아니라 **"어느 경로가 서지 않나"** 다.

| G6 DoD (PLAN §3 P3) | 판정 |
|---|---|
| HITL 왕복 — 턴 종료 · 슬롯 미점유 · 답변 · 새 attempt · resume 기억 / 콜드 스타트 이어감 | **충족**(§3). 1차에서 막혀 있던 입구(K-7·C-4)가 #133·#134 로 닫혔고 재측정 **76/0** — **두 도구 표면 모두** 우회 없이 선다 |
| 중복 0 — 실기 1회 + CI sim 100회 | **충족**(§4). 실기 재게시 0 · 중복 편집 0(두 경로) |
| 예산 — `paused(budget)` → 상향 → 같은 lane·workdir 재개 + `budget_per_task` 불변 | **서버 로직은 충족, 경로는 미충족**(§5) — 강제가 실기에서 발동하지 않고(D-17·K-8; S-44 는 #136 으로 닫혔으나 재측정 대기) 사람이 웹에서 풀 수 없다(S-45·W-6) |
| deputy — 12h 전 비활성 + "HH:MM부터" · 취소 즉시 | **충족**(§6.1) |
| 시나리오 C — Director 메시지가 실행 중 턴을 절대 죽이지 않음 | **충족**(§6.2) |
| 시나리오 D 재확인 | **충족**(§6.3) |
| 시나리오 A 의 `user_approval` 이 정식 HITL 카드로 | **미충족**(§6.4, S-45) |

### 7.1 P4 를 열려면 닫아야 하는 것

| # | 상태 | 재측정 |
|---|---|---|
| ~~K-7 · C-4~~ | **닫힘** — 계약 #133 · CLI #134 | `48_` **76/0** 으로 확인(§3.5) |
| ~~S-44~~ | **닫힘** — 서버 #136(finish 뒤 강제, E9-10) | **재측정 대기** — Lead 지시로 `50_` 은 D-17·S-45·W-6 과 함께 한 번에 돈다 |
| **S-45** | 열림 — 시스템 발행 HITL 의 타임라인 카드 | 닫히면 `50_`·(g) 재실행 |
| **W-6** | 열림 — 인박스(또는 S7 카드)의 예산 상향 입력칸 | 닫히면 `50_` 재실행 |
| **D-17** | 열림 — 데몬이 턴 중 usage 를 올리지 않는다 | 닫히면 `50_` 의 D1 재실행 |
| **K-8** | 열림 — 추정 보고의 금액이 0 으로 떨어져 강제에 도달하지 못한다 | 닫히면 `50_` 의 N1~N3 재실행 |

남은 넷은 **전부 예산 하나에 모여 있다.** S-45·W-6 은 사람이 개입하는 입구라 **컷 3 의 본문**이고,
D-17·K-8 은 FR-7.3 의 "턴 중 강제" 자체다. **D-17 과 K-8 은 같이 봐야 한다** — D-17 만 고치면
턴 중에 0 을 올리는 데몬이 될 뿐이다(§2.2). 넷 다 **핫픽스 라운드 한 번**에 들어갈 크기로 보인다 —
판정은 Lead(`plan/G6_DECISION.md`).

### 7.2 재실행 비용

전 항목 재측정은 에이전트 턴 **34** 남짓(haiku), 벽시계 **40분** 안팎이다(`49_` 의 3분 만료 대기가 최장).
남은 결함이 닫힌 뒤 필요한 것은 **`50_` 과 (g) 둘뿐**이다 — 턴 8 · 12분.

---

## 8. 되먹임 (다음 게이트를 위한 기록)

1. **X-2 의 변종: 도구가 실패하면 에이전트가 저장소를 뒤진다.** goal 에 저장소 이름을 쓰지 않아도,
   `colab_hitl_ask` 가 404 로 실패하자 에이전트는 `find ~ -name colab` → `git log --grep=hitl` →
   `git show …:contracts/colab-cli.md` 로 30여 툴콜을 태웠다. workdir 가 저장소 트리 아래 있는 한
   이 경로는 계속 열려 있다. 지시문에 **"저장소나 다른 디렉토리를 뒤지지 마라"** 와
   **"실패해도 재시도하거나 다른 방법을 찾지 마라"** 를 넣어 막았다 — e2e 지시문의 기본 문구로 굳힐 것을 권한다.
2. **개입은 턴이 살아 있는 동안에만 잰다.** `restartLane`·`cancelLane` 은 끝난 lane 에 409 이고,
   "턴 중 예산 초과" 도 턴이 끝나면 성립하지 않는다. 자극을 한 자리에 모으고 판정을 뒤로 미루는
   구조로 두 스크립트를 다시 짰다(52_ §2, 50_ §4).
3. **`prompt_of_task.py` 는 attempt 를 가리지 않는다.** 같은 task 의 모든 claim 을 이어 붙이므로 재개를
   재는 자리에서는 "답변이 프롬프트에 들어갔다" 가 항상 참이 된다. T-I3 는 attempt 를 받는
   `fixtures/prompt_of.py` 를 따로 뒀다. P2 스크립트를 재사용하는 곳은 확인이 필요하다.
4. **측정 도구의 함정 세 가지**(README 에도 적었다): 비활성 버튼은 `get attr … disabled` 가 아니라 CSS
   `:disabled` 로 봐야 하고(boolean 속성은 빈 문자열이다), 활동 피드는 세션 `message` 가 아니라
   `task_event(class=status, payload.note)` 이며, `chk` 안에서 `case … in pat)` 를 쓰면 bash 3.2 가
   명령 치환을 파싱하지 못한다(`in_set` 헬퍼로 대체).
5. **`plan/spikes/spike04c/measure.py` 의 버그.** psql 출력에 전역 `.strip()` 을 걸어 **마지막 줄의 빈 후행
   칸이 잘린다** — `resumed` 가 NULL 인 attempt 가 마지막 행이면 IndexError 로 죽는다. 스파이크 표는 이
   경로를 타지 않았으므로 과거 수치는 바뀌지 않는다. 사본(`e2e/p3/fixtures/measure_dup0.py`)에서 고쳤고
   원본은 건드리지 않았다.
6. **EVAL 제안(K-1 에 얹을 것)**: E9-01 의 "실측(`estimated:false`) → 취소" 분기는 오늘의 두 런타임으로는
   실기에 도달할 수 없다(K-8). EVAL 에 "실기 검증 불가 · `acpfake`/골든이 지킨다"를 명시하거나,
   추정치에 서버 가격표를 매겨 비교하는 규칙을 계약에 넣어야 행이 살아난다.

---

## 9. 2판 — 핫픽스 뒤 재측정 (2026-09-07)

1판(§1~§8)은 그대로 둔다. 이 절은 **차단 결함 다섯이 전부 닫힌 뒤** 같은 자극을 다시 낸 결과다.

| 항목 | 내용 |
|---|---|
| 스택 | dev **`94c4143`**(#134·#136·#139·#142·#143·#145·#146·#147 머지 뒤). `bin/server`·`bin/daemon`·`bin/colab` 빌드 **2026-09-07 01:43:09 KST**, `next build` 같은 시각, 마이그레이션 15개 적용. 서버 `:8100` · web `:3020` · Postgres `colab-pg-g6 :5442` — **DB 를 드롭하고 다시 만들었다**(1판 데이터 없음) |
| 런타임 | 1판과 같다 — Claude Code CLI 2.1.258 + 어댑터 `0.74.0`(핀) · Hermes 0.20.6 · haiku |
| 재측정 범위 | `48_`~`53_` **전부 다시 돌렸다** + `fixtures/g_user_approval_card.sh`. (Lead 지시는 `50_`·(g) 만이었지만 #142 가 **에이전트 HITL 카드 경로**를, #147 이 **롤업·인박스**를 건드려 나머지도 같은 빌드에서 다시 잰다) |
| 총계 | **PASS 331 · FAIL 3** — 48_ **76/0** · 49_ **33/0** · 50_ **114/3** · 51_ **38/0** · 52_ **40/0** · 53_ **22/0** · (g) **8/0**. 그 안에서 재실행한 `e2e/p2/33_` 은 **41/0**(별도 집계) |
| 결론 | **G6 충족 — 남은 차단 1건은 신규**(§9.5). 1판의 차단 다섯(K-7·C-4·S-44·S-45·W-6·D-17·K-8)은 **전부 닫혔고 실기로 확인**했다. `50_` 은 **대역 우회가 하나도 없다** — 예산 초과·취소·정지·상향·재개가 전부 에이전트가 실제로 쓴 돈으로 일어난다 |

### 9.1 1판의 차단 결함 — 하나씩 실측으로 닫음

| # | 어떻게 닫혔나 | 2판 실측 근거 |
|---|---|---|
| ~~**K-7 · C-4**~~ | 계약 #133 · CLI #134 | `48_` **76/0** — 두 도구 표면(Claude Code `mcp` · Hermes `cli_wrapper`) 모두 우회 없이 등록·왕복. `resumed` a1 `true` · 콜드 스타트 arm `false`(E8-01·E8-02 그대로) |
| ~~**D-17**~~ | 데몬 #145 (harness v0.8.5 — `emitRawSDKMessages` 원시 스트림 dedup 누적 + `OnUsage` 훅) | `50_` **D1·D1b·D1c** — 턴 시작 27초 만에 `task_usage` 1행 · 토큰 451 · `estimated:true`. 런타임 능력 광고도 `usage_midturn: true`(**실측 광고**, §9 v0.8.5) |
| ~~**K-8**~~ | 서버 #147 (`RecordTurnUsage` 가 `repriceEstimates` 를 롤업과 **같은 함수**로 heartbeat 에서도 부른다) | `50_` **D1d** — 그 추정 행에 **$0.006122** 가 매겨져 있다(1판은 0). 그리고 claude_code 가 `result.total_cost_usd` 를 실어 주면서(#145) **`estimated:false` 실측 행이 실기에 생긴다** — 1판의 "런타임이 만든 행 72/72 가 추정" 은 더 이상 사실이 아니다 |
| ~~**S-44**~~ | 서버 #136 (finish 뒤 강제) | 아래 §9.4 — 오늘의 런타임에서는 **finish 이전 heartbeat 이 언제나 먼저 잡아** 그 호출부가 실기에 도달하지 않는다. 유닛(`TestP3BudgetAtFinish*`)이 지킨다. `50_` H6 을 **N/A** 로 남겼다 |
| ~~**S-45**~~ | 서버 #142 (`messages.PostHitlCard` — 네 경로 한 헬퍼) | `50_` **C2g·C2h**(예산 task 범위) · **N4d**(추정·세션 범위) · **M2c**(실측·세션 범위) · **H3b**(hermes) · (g) **G2·G2b·G2c**(완료 승인). 시스템 발행 HITL **네 경로 전부** 타임라인에 카드 한 장 |
| ~~**S-46**~~ | 서버 #142 (`ResumeSessionTasks`) | `50_` **M7** — 세션 정지가 park 한 task 가 승인 한 번에 `queued` 로 돌아온다 |
| ~~**W-6**~~ | 웹 #139 | `50_` **W1b**(S7 카드, scope=task) · **W3**(인박스, task 범위) · **M5**(인박스, 세션 범위). 그리고 **승인을 실제로 웹에서 냈다** — C4·M6 은 DOM 입력 → 클릭의 결과다 |
| ~~**K-9**~~ | 계약 + 서버 #147 | `50_` **C2i** = `budget` · (g) **G3b** = `user_approval` — `GET /inbox` 의 `card.purpose` 가 채워진다 |
| ~~**K-10**~~ | 서버 #147 | `50_` **N9**(이미 쓴 금액 이하 상향 → 4xx) · **N10b/c**(승인 한 번에 세션 `active` + `limits.budget_usd` = 승인 금액) · **M6b/c** · **M8**(멈춰 있던 다음 task 가 다시 dispatch) |

### 9.2 `50_` 을 다시 짠 방식 — 대역이 사라졌다

1판의 `50_` 은 `fixtures/daemon_heartbeat.sh` 로 §4.2 와이어를 흉내 냈다. 2판은 **자극이 전부 실기**다.
바뀐 것은 금액의 눈금 하나다.

> EVAL E9-01 은 `budget_per_task` **$1** · 턴 중 **$1.01** 이다. 그 금액은 **실기 한 턴으로 도달할 수 없다** —
> haiku 한 턴의 실측 비용이 $0.075 안팎이다. 그래서 2판은 **상한을 실기 눈금으로 내리고 EVAL 의 비율을
> 지킨다**: 상한 **L=$0.05** → 초과 → 상향 **3L=$0.15** → 재개 뒤 다시 L 초과(취소 없음).

L 을 고르는 규칙이 하나 더 있다. **서버는 같은 턴을 두 번 본다.**

| 무엇 | 언제 | 값(실측) | 어느 분기 |
|---|---|---|---|
| 원시 SDK 스트림 토큰 × 워크스페이스 가격표 = **추정** | 턴 중, 15초마다 | $0.0061 → $0.025 → $0.032 (한 턴 끝값 $0.044) | `Estimated` → 세션 `paused` + 드레인 (E9-05) |
| `result.total_cost_usd` = **실측** | 턴 끝, `finish` **직전** heartbeat(`OnUsage`) | **$0.0770** | 실측 → 취소 명령 + task `paused(budget)` (E9-01) |

추정이 먼저 넘으면 E9-05 로 가고 E9-01 에 도달하지 못한다. **$0.044 < $0.05 < $0.0770** 이 그 사이다.
같은 눈금 규칙으로 다섯 arm 을 한 워크스페이스에 얹었다(capacity 5, 전부 동시).

| arm | 자극 | 무엇을 재나 |
|---|---|---|
| **A** | claude_code · `budget_per_task` $0.05 | E9-01 실측 초과 → **웹**에서 $0.15 상향 → E9-02 재개 → E9-08 |
| **B** | 같음 | E9-03 거절은 `paused` 유지 |
| **C** | claude_code · `budget_per_task` $0.015 | E9-05 · S-48 **추정치**가 먼저 넘는다 → 하드 컷 없이 세션 정지 + 드레인 → K-10 |
| **D** | claude_code · 에이전트 상한 없음 · 세션 `limits.budget_usd` $0.05 | 세션 범위 **실측** 초과 → **웹** 승인 → K-10 + S-46 |
| **H** | hermes · `budget_per_task` $0.10 | 실측 비용을 주지 않는 런타임의 사후 강제(E9-10) |

### 9.3 (c) 예산 — `50_` **PASS 114 · FAIL 3**

#### E9-01 (arm A) — 실기에서 처음으로 발동한다

| 항목 | 실측 |
|---|---|
| 턴 중 추정 | 행 1 · 토큰 451 · **$0.006122** · `estimated:true` — 이 시점엔 강제 없음(task `running`) |
| 턴 끝 실측 | **$0.076976** · `estimated:false` (상한 $0.05) |
| task / lane / 세션 | **`paused(budget)`** / `paused` / **`active` 유지**(task 범위다) |
| 시스템 HITL | 1건 · `source=system` · `approval` · **`purpose=budget`** · **`task_id` 채움** |
| 타임라인 카드 | **1장**(`kind='hitl'`, `hitl_request.message_id` NOT NULL) — S-45 |
| 인박스 | 1건, `card.purpose = budget` — K-9 |
| 취소 | `daemon_command(type=cancel)` 1건 · `delivered_at` 채움 — 프로세스 kill 아님 |
| attempt 1 프로세스 | 0 |
| **데몬 자신의 §5** | 함께 발동했다 — `runtime/cancel` 이벤트 `실측 비용 $0.0770 가 유효 예산 $0.0500 를 넘었다 — 넘긴 쪽은 task 상한 … paused_budget` |
| 단가 | 데몬 보고 `cost_usd=0.0769764` = DB `task_usage.cost_usd` **0.076976** — 서버는 실측을 **다시 매기지 않는다**(어댑터 list 단가 재계산 없음) |

FR-7.3 M9 의 **데몬 반쪽이 처음 동작한다**(#145 §4): ACP 경로가 늘 추정이던 동안 `budget.go` 의 `Estimated`
가드가 영원히 닫혀 있었다. 이제 서버 명령과 데몬 §5 가 **이중**으로 같은 결론에 이른다.

#### E9-02 · E9-08 — 웹에서 상향, 같은 lane·workdir 재개

| 항목 | 실측 |
|---|---|
| 승인 경로 | **S8 인박스 DOM** — `hitl-budget-input` 에 `0.15` 입력 → `hitl-approve` 클릭. 스크린샷 `p3-50-02`·`p3-50-03` |
| HITL | `answered` · `approved=true` |
| `task.budget_override` | **0.15** |
| 에이전트 `budget_per_task` | **0.05 — 불변**(E9-02) |
| 재개 | 같은 task `attempt` 2 · task 수 1 · **같은 lane** · 같은 workdir · 프롬프트에 `<resumed attempt=2>` |
| **E9-08** | 재개한 턴이 **$0.093628** 을 썼다 — 원래 상한 $0.05 를 **다시 넘었는데** 취소 0 · 새 HITL 0 · task `running` 유지 → `completed`. 강제 시점이 override 를 읽고 있다 |

S7 타임라인 카드에도 상향 입력칸이 붙는다(W1b, `scope=task`) — 두 자리 모두 산다.

#### E9-03 (arm B) — 거절은 `paused` 유지

`approved:false` + 사유 → task **`paused(budget)` 유지** · `failed`·`cancelled` 아님 · `budget_override` 미저장 ·
`attempt` 그대로 1. 1판과 같다.

#### E9-05 · S-48 (arm C) — 1판이 **잴 수 없다**고 적은 자리

1판 §5.4 는 이 다섯 줄 중 셋을 FAIL 로 남겼다(K-8). 2판은 전부 선다.

| 항목 | 1판 | 2판 실측 |
|---|---|---|
| 추정 보고의 금액을 서버가 보존하나 | **0 으로 떨어뜨린다** | **$0.078946** — 가격표로 매긴다 |
| 그 정지를 결정한 값이 추정인가 | (도달 못 함) | **`estimated: true`** — `task_event(status/pause).payload.estimated` |
| 세션이 `paused(budget)` 로 멈추나 | `active` | **`paused`** · `paused_detail.budget = {limit_usd: 0.015, spent_usd: 0.025288}` |
| "진행 중인 턴은 끝까지" 피드 | 0건 | **1건** |
| 시스템 HITL | 없음(알림만) | **1건** · `system`·`approval`·`purpose=budget` · **`task_id` 비움** · 타임라인 카드 1 · 인박스 1 |
| 옛 `session_paused` 인박스 카드 | — | **0** (한 정지에 카드 한 장) |
| 하드 컷 | — | **없다** — 서버 취소 명령 0, 턴은 `end_turn` 으로 제 끝까지 갔다 |
| 멈춘 세션의 dispatch | — | **0** (`dispatched`·`running` 0건) |

턴을 닫은 것은 서버가 아니라 **데몬의 §5** 다(`task_attempt.outcome = paused_budget`). 서버는 드레인을 지켰고,
데몬은 자기 상한을 지켰다 — 계약이 정한 두 반쪽이 각자 제자리에서 동작한다.

**K-10**: 이미 쓴 $0.078946 이하($0.001)의 상향은 **4xx** 로 막힌다. $0.30 승인 한 번에 세션 `active` ·
`limits.budget_usd = 0.30` · `paused_reason` 비움.

#### 세션 범위 · S-46 (arm D)

| 항목 | 실측 |
|---|---|
| 자극 | 세션 `limits.budget_usd` $0.05, 에이전트 상한 없음 → D-16 의 min() 이 **세션 잔여**로 묶는다 |
| 정지 | 세션 `paused(budget)` · `paused_detail.budget.limit_usd = 0.05` · 누적 $0.075111 (**실측**) |
| HITL | `system`·`purpose=budget`·**`task_id` 비움** · 타임라인 카드 1 · 인박스 1 |
| 취소 | 실측이므로 취소 **명령** 1건, 그 task 는 `paused(budget)` 로 park |
| **다음 dispatch 0** | 정지 중에 새 task(멘션)를 만들어도 `queued` 에 머문다 — `task_attempt` **0행**(E5-04) |
| 승인 | **웹 인박스**의 세션 범위 입력칸(`새 세션 상한`)에 $0.15 → 클릭 |
| **K-10 · S-46** | 세션 `active` · `limits.budget_usd = 0.15` · **park 된 task 재큐잉** · 멈춰 있던 다음 task 도 dispatch |

#### hermes (arm H) — 실측 비용을 주지 않는 런타임

| 항목 | 실측 |
|---|---|
| usage | **$0.134196 · `estimated: true`** — ACP 에 `cost_usd` 가 없어 hermes 는 끝까지 추정이다(상한 $0.10) |
| 분기 | 추정이므로 **하드 컷 없음** — 취소 명령 **0**, 완료한 task 는 `completed` 그대로(E5: `completed → paused` 전이 없다) |
| 정지 | 세션 `paused(budget)` + 시스템 HITL(`purpose=budget`) + 타임라인 카드 1 + 인박스 1 + 다음 dispatch 0 |

### 9.4 E9-10 의 실측·사후 분기는 **실기에 도달하지 않는다** (N/A, 결함 아님)

E9-10 은 둘로 갈린다. 세션 잔여 초과 → 세션 `paused` + HITL(`task_id` 비움)은 arm C·D·H 가 **실기로 확인**했다.
남은 절반 — **실측** 초과가 **`finish` 뒤**에 발견되어 lane 이 `paused` 되고 HITL 이 task 를 지목하는 분기 —
는 오늘의 런타임 조합으로 만들 수 없다.

- 실측 금액을 주는 런타임은 claude_code 하나뿐이고(#145),
- 그 값은 `finish` **이전**의 `OnUsage` heartbeat 으로 먼저 서버에 닿는다(#145 §5).
- 그래서 강제는 언제나 **턴-중 호출부**가 먼저 잡고, `finishAndEnforce` 는 `SessionStatus != active` 로 조기 반환한다.

즉 S-44 가 만든 호출부는 **살아 있지만 오늘은 지나가지 않는다**. `50_` 의 **H6 을 `N/A`** 로 두고 근거를
서버 유닛(`TestP3BudgetAtFinishEstimatedNeverCuts` 등)에 넘긴다. **EVAL 제안(K-1 에 얹을 것)**: E9-10 의
실측 분기에 "실기 검증 불가(런타임이 finish 전에 실측을 보낸다) · 유닛이 지킨다"를 명시할 것.

### 9.5 신규 결함 1건 (차단) — 스트림 **S(서버)**

> 번호는 Lead 가 준다(§0-11). 아래는 "신규(서버)" 로만 적는다.

**증상.** 예산으로 `paused` 된 task 의 `finish` 가 **500** 이다. 재현 3/3(arm A·B·D), 두 회차 모두.

```
finish <task>.1: server: 500: {"code":"internal",
 "detail":"tasks: cancel: ERROR: new row for relation \"task\" violates check constraint
           \"task_paused_detail_check\" (SQLSTATE 23514)"}
```

**경로.**

1. `httpapi.applyBudgetPause` 가 task 를 `paused` + `paused_reason='budget'` + `paused_detail={...}` 로 두고
   `daemon_command(type=cancel)` 을 건다.
2. 데몬이 §5 로 attempt 를 닫고 `finish{outcome: "paused_budget"}` 을 보낸다.
3. `tasks.Finish` 는 `completed` 가 아닌 **모든** outcome 을, 그 attempt 에 cancel 명령이 있으면
   `decided = "cancelled"` 로 바꾼다(`service.go` 의 `cancelRequested` 분기).
4. `cancelLocked` 가 `paused_reason = NULL` 로 지우면서 **`paused_detail` 은 남긴다** →
   `task_paused_detail_check`(migration 0006: `paused_detail IS NULL OR paused_reason IS NOT NULL`) 위반 → 500.

**두 가지가 겹쳐 있다.**
- (a) 예산 pause 가 건 취소 명령을 근거로 `paused_budget` 을 **`cancelled` 로 승격**하는 것 자체가
  E9-01 문언("`failed` 가 아니다", 재개 가능해야 한다)과 어긋난다. 그 취소 명령은 **그 pause 자신**이다.
- (b) `cancelLocked` 가 `paused_detail` 을 지우지 않아 `paused → cancelled` 전이가 **어떤 경로로도** 500 이다.

**측정된 영향.**

| 무엇 | 실측 |
|---|---|
| 그 attempt 의 `task_attempt.outcome`·`finished_at`·`stop_reason` | **NULL 로 남는다**(A·B·D 3/3) — 피드·화면에서 "끝나지 않은 attempt" |
| `lane.runtime_session_ref` | **저장되지 않는다** — `Finish` 가 유일한 기록자다 |
| 그래서 승인 뒤 재개는 | **콜드 스타트**다 — `task_attempt(attempt 2).resumed` 가 NULL(붙일 세션 자체가 없다). **E9-02 의 "resume 우선" 미충족** |
| pause 자체 | **살아남는다** — 트랜잭션이 CHECK 로 깨져 `cancelled` 가 커밋되지 않았기 때문이다(우연한 보호) |

**차단인 이유.** E9-02 가 "같은 lane·workdir 로 재개(**resume 우선**)"를 명문으로 요구하고,
정상 프로토콜 경로(`finish`)가 500 을 받는다. 콜드 스타트가 결과적으로 이어가긴 한다(스파이크 4c ·
2판 A 의 attempt 2 는 `completed`) — 그래서 **작업이 유실되지는 않는다**. 판정은 Lead.

**1판에도 있었다.** 1판은 대역 heartbeat 로 턴 중에 취소를 걸었고, 그 뒤 데몬의 `finish` 도 같은 경로를
탔다. 다만 1판은 `task_attempt.outcome`·`resumed` 를 재지 않아 드러나지 않았다. 2판이 그 칸을 새로 잰다
(`50_` **C3e·C3f·C5e**).

**연관.** 백로그 **S-39**("`runtime_session_ref` 가 finish 에서만 저장돼 크래시한 attempt 는 콜드 스타트",
P4·알고 있기)와 증상이 같다. 그러나 여기서는 **크래시가 아니라 정상 경로가 500** 이므로 원인이 다르다 —
S-39 를 P4 로 미룬 근거("실측상 콜드 스타트 성적이 같다")가 이 자리에도 적용되는지는 Lead 판단이다.

**함께 갱신할 것: K-8.** Lead 판정은 "E9-01 실측 분기는 실기 도달 불가, 대역/acpfake 로만" 이었다.
#145 가 `result.total_cost_usd` 를 실어 주면서 **그 전제가 바뀌었다** — 2판은 실측 분기를 실기로 통과한다.
K-8 은 해소로 옮기고, 남는 것은 §9.4 의 "finish **사후** 실측 분기" 하나다.

### 9.6 나머지 스크립트 — 같은 빌드에서 다시

| 스크립트 | 1판 | 2판(`94c4143`) | 비고 |
|---|---|---|---|
| `48_hitl_roundtrip.sh` (a) | 76/0 (`5ed5dfc`) | **76/0** | #142 가 에이전트 HITL 카드 경로를 헬퍼로 옮겼는데 회귀 없음. `resumed` a1 `true` · 콜드 arm `false` |
| `49_partial_exec_dup0.sh` (b) | 33/0 | **33/0** | 실기 재게시 0 · 중복 편집 0 · 3분 만료 대기 그대로 |
| `50_budget_pause_override.sh` (c) | 47/**7** | **114/3** | 대역 제거 + arm 5개로 다시 짰다(§9.2). FAIL 3 은 §9.5 한 결함 |
| `51_deputy_and_cancel.sh` (d) | 37/0 | **38/0** | 첫 회차 34/**3** → 자극이 성립하지 않았다(아래 관찰). 지시문을 고치고 **전제 검사 X1c 를 더해** 38/0. 취소 → lane `failed` **3초** |
| `52_scenario_c.sh` (e) | 40/0 | **40/0** | Director 메시지가 실행 중 턴을 죽이지 않는다 |
| `53_scenario_d.sh` (f) | 22/0 | **22/0** | 프로파일 전환 재확인 |
| `fixtures/g_user_approval_card.sh` (g) | 4/**3** | **8/0** | S-45 가 닫혀 완료 승인도 타임라인 카드로 선다. 그 안의 `e2e/p2/33_` **41/0** |

#### 관찰(신규 후보 · 스트림 S) — 턴 종료와 경합한 취소는 흡수되지 않는다

`51_` 첫 회차의 실측이다. deputy 의 취소가 **202** 로 받아들여지고 서버가 `cancel` 명령을 걸었으며
활동 피드에 **"사람이 중단함"** 까지 남았는데, 그 사이 턴이 `end_turn` 으로 끝나 데몬이
`finish{outcome: "completed"}` 를 보냈다. `tasks.Finish` 는 **`completed` 가 아닌 outcome 에서만**
`cancelRequested` 를 본다 — 그래서 task 는 `completed`, lane 은 `done` 이 됐다.

| 시각 | 무슨 일 |
|---|---|
| 17:19:29.65 | `cancel` 명령 `delivered_at` |
| (그 직전) | `runtime/turn_end` `stop_reason=end_turn` — 턴은 이미 끝나 있었다 |
| 17:19:30.25 | `finish outcome=completed stop=end_turn` → task `completed` · lane `done` |
| — | 피드에는 `status/cancel` **"사람이 중단함"** 이 남아 있다 |

일이 실제로 끝났으니 `completed` 가 옳다고 볼 수도 있다. 다만 **피드가 "사람이 중단함" 이라고 말하는데
화면은 완료**라 사람이 읽는 두 문장이 어긋난다. 결함으로 볼지 문구를 고칠지는 Lead 판정 — 여기서는
자극의 전제(§8 되먹임 2)를 스크립트가 **직접 검사하도록** 고쳤다(`51_` **X1c**: 취소 직전에 그 attempt 가
아직 `turn_end` 를 내지 않았음). 첫 회차에 이 전제가 깨진 것은 `Chapters` 에이전트가 지시문의
"지시를 받으면" 을 "지시를 기다린다" 로 읽고 첫 턴을 15초 만에 끝냈기 때문이다 — 지시문을
"**첫 턴부터 곧바로**" 로 바꿨다(§0-16 문구는 유지).

### 9.7 판정 (컷 3) — 2판

| G6 DoD (PLAN §3 P3) | 1판 | 2판 |
|---|---|---|
| HITL 왕복 — 턴 종료 · 슬롯 미점유 · 답변 · 새 attempt · resume 기억 / 콜드 스타트 이어감 | 충족 | **충족** — `48_` 76/0 |
| 중복 0 — 실기 1회 + CI sim 100회 | 충족 | **충족** — `49_` |
| 예산 — `paused(budget)` → 상향 → 같은 lane·workdir 재개 + `budget_per_task` 불변 | **미충족** | **충족** — `50_` 이 **대역 없이** 전 구간을 통과한다(§9.3). 다만 재개가 resume 이 아니라 콜드 스타트다(§9.5 신규) |
| deputy — 12h 전 비활성 + "HH:MM부터" · 취소 즉시 | 충족 | **충족** — `51_` |
| 시나리오 C — Director 메시지가 실행 중 턴을 절대 죽이지 않음 | 충족 | **충족** — `52_` |
| 시나리오 D 재확인 | 충족 | **충족** — `53_` |
| 시나리오 A 의 `user_approval` 이 정식 HITL 카드로 | **미충족** | **충족** — (g) 8/0 |

**결론: G6 충족.** DoD 일곱 행이 전부 선다. 남은 것은 **신규 결함 1건(서버, §9.5)** 이고,
그것은 "예산 재개가 resume 이 아니라 콜드 스타트가 된다 + `finish` 가 500 을 받는다" 이지 **작업 유실이
아니다**. P4 를 여는 조건으로 볼지 핫픽스 한 번을 더 돌지는 Lead 판정(`plan/G6_DECISION.md`).

### 9.8 되먹임 (2판에서 새로 배운 것)

1. **예산은 EVAL 의 금액이 아니라 런타임의 눈금으로 자극해야 한다.** haiku 한 턴이 $0.075 이므로 EVAL 의
   $1 은 실기에서 영원히 안 넘는다. 1판이 대역을 쓴 진짜 이유가 여기 있다. 상한을 내리고 **비율**을 지키면
   같은 행을 실기로 잰다.
2. **한 턴에 두 번의 강제 신호가 온다** — 턴 중 추정(가격표), 턴 끝 실측(`result.total_cost_usd`). 어느
   분기를 재려는지에 따라 상한을 **그 둘 사이**에 두어야 한다. 이것을 모르면 E9-01 을 재려다 E9-05 를 잰다.
3. **인박스 DOM 은 항목을 `data-item-id` 로 집어야 한다.** 여러 arm 이 같은 인박스에 뜨므로
   `[data-type="hitl_request"]` 만으로는 남의 항목을 누른다.
4. **API 관측은 그 화면의 계정으로 로그인하고 해야 한다.** (g) 가 처음에 `card.purpose` 를 `-` 로 읽은 것은
   `api_ok` 가 lib 기본 쿠키(다른 계정)를 쓰고 있었기 때문이다 — 결함이 아니라 측정 실수였다.
5. **`paused_detail` 은 `{"budget": {...}, "reason": …, "paused_at": …}` 다.** `paused_detail->>'limit_usd'` 는
   언제나 NULL 이다(`->'budget'->>'limit_usd'`).
6. **"지시를 받으면 …" 은 첫 턴을 기다리게 만든다.** `51_` 의 에이전트가 그렇게 읽고 15초 만에 턴을
   끝내 취소 자극이 성립하지 않았다. 개입을 재는 스크립트의 지시문은 **"첫 턴부터 곧바로"** 로 쓰고,
   자극의 전제(턴이 살아 있는가)를 **체크 한 줄로 남겨** 전제가 깨진 회차를 결함으로 오독하지 않게 한다.
