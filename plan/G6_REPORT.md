# G6 판정 자료 — HITL 왕복 · 중복 0 · 예산 · deputy · 시나리오 C·D (T-I3)

| 항목 | 내용 |
|---|---|
| 게이트 | `PLAN.md` §6.2 **G6** — "시나리오 C·D + **중복 0**". **컷 3**: 미통과면 P4 를 열지 않고 P3 를 마감한다 |
| 앞 게이트 | **G5 통과**(`plan/G5_DECISION.md`) — 시나리오 A 8단계 + Hermes + 템플릿 3분. 재측정 PASS 162 · FAIL 0 |
| 작성 | Integrator (T-I3), 2026-09-07 |
| 스택 | 1차 측정 dev `957ffd3`(서버 #124 · 웹 #130 머지 뒤), **`48_` 재측정은 계약 #133 · CLI #134 · 서버 #136 을 머지한 `5ed5dfc`**(빌드 2026-09-07 00:50:46 KST). `bin/server`·`bin/daemon`·`bin/colab` 빌드 시각 **2026-09-06 23:51:18 KST**. server `:8100` · web `:3020`(`next build` + `next start`) · Postgres `colab-pg-g6 :5442` — 다른 워커 스택(P1 `:8080/:5435` · P2 `:8090/:5436` · G5 `:5437` · 스파이크 4c `:8095/:5441`)과 포트·컨테이너·workdir 를 분리했다(§0-13) |
| 런타임 | Claude Code CLI 2.1.258 + 어댑터 `@agentclientprotocol/claude-agent-acp` **0.74.0**(핀) · **Hermes 0.20.6**. 모델은 비용 때문에 haiku(`claude-haiku-4-5-20251001`) |
| 재현 | `bash e2e/p3/up.sh` 뒤 `48_`~`53_` + `fixtures/g_user_approval_card.sh` — 순서·비용·함정은 `e2e/p3/README.md` |
| 총계 | **PASS 259 · FAIL 10** — 48_ **76/0**(핫픽스 뒤 재측정) · 49_ 33/0 · 50_ 47/7 · 51_ 37/0 · 52_ 40/0 · 53_ 22/0 · (g) 4/3. 여기에 (g) 가 재실행한 `e2e/p2/33_` 이 **41/0**(별도 집계) |
| 결론 | **G6 미충족 — 남은 차단 결함 4건.** HITL 왕복·재개·중복 0·취소·deputy·시나리오 C·D 는 **전부 섰다**(FAIL 0 다섯 스크립트). 측정 중 드러난 **K-7·C-4 는 이 PR 이 열려 있는 동안 닫혔고**(계약 #133 · CLI #134) `48_` 재측정이 **76/0** 으로 그것을 확인한다. 남은 것은 **예산 하나**다: 강제가 실기에서 한 번도 발동하지 않고(**D-17 · K-8**; S-44 는 #136 으로 닫혔으나 재측정 대기), 초과를 사람이 **웹에서 풀 수 없다**(**S-45 · W-6**). 판정은 §7 |

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
| **D-17** | 데몬 | `acp.Runner.recordUsage` 가 `session/prompt` **응답에서만** 호출된다(`runner.go:955`) → 턴 중 heartbeat 의 `usage` 는 언제나 0 → 서버 가드(`daemon.go:442`)가 거짓이라 `enforceBudgetFor` 미호출 | §2.2 | **차단** |
| ~~**S-44**~~ | 서버 | `enforceBudgetFor` 는 heartbeat 한 곳에서만 호출된다 — `tasks.Finish` 에서 부르지 않아 **사후 강제도 없다**(`budget.go:88` 주석은 부른다고 적었다) | §2.2 | **해결 — 서버 #136**(50_ 재측정 대기) |
| **K-8** | 계약·교차 | ACP 경로는 `cost_usd` 를 주지 않아 **런타임이 만든 `task_usage` 행이 72/72 전부 `estimated: true`** 다(claude_code·hermes 모두). 그리고 `RecordTurnUsage` 는 `estimated: true` 보고의 금액을 **0 으로 떨어뜨린다**(harness v0.7.1) → 추정 경로도 강제에 도달하지 못한다. **D-17 만 고쳐도 예산은 여전히 발동하지 않는다** | §2.2 | **차단** |
| **S-45** | 서버 | 시스템 발행 HITL 3곳(`budget.go:188` · `sessions/complete.go:216` · `router/service.go:500`)이 `kind='hitl'` 타임라인 메시지를 만들지 않고 `message_id` 가 NULL 이다 → S7 에 카드가 **아예 없다**(SCREEN §4.5 위반). 에이전트 발행 경로만 게시한다(`handlers_hitl_p3.go:203`) | §2.3 · §6 | **차단** |
| **W-6** | 웹 | 인박스의 `hitl_request` 항목이 `HitlBody` 를 `budgetOverride` 없이 그린다(`InboxItemCard.tsx:159`) — 상향 입력칸은 `item.type === "session_paused"` 조건이라 붙지 않는다. task 범위 예산 초과는 세션을 멈추지 않으므로(E9-01) 이 항목은 영영 `session_paused` 가 아니다 → **웹에서 금액을 정할 자리가 없다** | §2.3 | **차단** |

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

## 7. 판정 (컷 3)

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
