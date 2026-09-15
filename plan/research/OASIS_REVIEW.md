# 리서치 — camel-ai/oasis 가 Colab 에 시사하는 것 (T-R1)

| 항목 | 내용 |
|---|---|
| 상태 | **제안(미확정)** — Director 요청(2026-09-15). 확정은 Director·Lead |
| 대상 | [camel-ai/oasis](https://github.com/camel-ai/oasis) — 읽은 커밋 `0004f5bfd61194324cb40623fa9b2578daf9aec9` (2026-08-27). 아래 코드 경로는 전부 이 커밋 기준(`https://github.com/camel-ai/oasis/blob/0004f5b/<경로>`) |
| 논문 | [OASIS: Open Agent Social Interaction Simulations with One Million Agents](https://arxiv.org/abs/2411.11581) (arXiv 2411.11581, HTML 본문 [arxiv.org/html/2411.11581](https://arxiv.org/html/2411.11581)) |
| 문서 | [docs.oasis.camel-ai.org](https://docs.oasis.camel-ai.org) = 저장소 `docs/` (Mintlify). 문서와 코드가 어긋난 곳이 있다(§1.6) |
| 우리 | `PRD.md` v0.16 §3·§5·§8·§11·§12, `SCREEN.md`, `contracts/harness.md` §7, `contracts/task_event.schema.json`, `plan/P2_BACKLOG.md` |
| 산출 | 이 문서 + `PRD.md` "v0.17 변경 제안" 절(꼬리표 `[제안 v0.17]`) + `plan/P2_BACKLOG.md` "OASIS 후보" 절. **코드·contracts/ 변경 없음** |

## 0. 먼저 — 목적이 다르다

| | OASIS | Colab |
|---|---|---|
| 무엇을 만드나 | **시뮬레이션·관찰**. LLM 에이전트 수천~100만 명이 가짜 SNS(X·Reddit)에서 행동하게 하고, 정보 확산·양극화·군중 효과 같은 **집단 현상**을 재현·측정한다 | **실제 일을 시키는 협업**. 사람 Director 가 에이전트 몇 명에게 goal 을 주고, 에이전트가 사용자 머신의 CLI 런타임으로 **실제 파일·저장소를 바꾸고 산출물을 낸다** |
| 에이전트는 | 연구 **대상**. 행동이 값싸고(좋아요·팔로우) 되돌릴 수 있고 부작용이 시뮬레이터 DB 안에 갇힌다 | **노동자**. 행동이 비싸고(모델 턴 수십 초·달러) 부작용이 저장소·셸에 남는다(FR-3.4 "되돌리기 어려운 작업 중 취소 보류") |
| 사람은 | 실험자. 시작 전에 개입(`ManualAction`)을 심고 끝난 뒤 DB 를 분석한다. 실행 중에는 없다 | **Director**. 실행 중에 HITL 에 답하고 중단·재지시한다(FR-5, FR-3.4). 사람이 루프 안에 있다 |
| 시간 | 인공. 3분 = 1 timestep, `Clock(k=60)` 배속, 활동 확률로 누가 깨어날지 정한다 | 실시간. 멘션이 트리거이고 heartbeat 15초·stall 3분·HITL 24h 이 벽시계다 |
| 성공 | 실제 세계 곡선과의 RMSE, 집단 지표의 재현 | 세션이 종료 조건을 채우고 사람이 15분 안에 첫 세션을 끝낸다(§11) |
| 규모 | 100만 에이전트 × 24 A100 × 1주 | 워크스페이스 동시 task 50, 데몬당 10(§9) |

그래서 **OASIS 의 핵심 자산(추천 시스템·시간 엔진·대규모 추론기·에이전트 생성기)은 우리에게 필요 없다.** 가져올 수 있는 것은 (1) 에이전트 집단을 **어떻게 재는가**(§2 (b)·§3 C1~C3), (2) 행동을 **어떻게 기록·제한하는가**(C4·C5), (3) 실험을 **어떻게 재현 가능하게 적는가**(C7) 정도다. 나머지는 대응은 되지만 가져올 것이 없다(§3 "안 함").

## 1. OASIS 한 장 요약

### 1.1 목적

논문 초록: "generalizable and scalable social media simulator … capable of modeling up to one million users". 세 현상을 재현한다 — X 에서의 **정보 확산**(Vosoughi 2018 의 scale·depth·max breadth 곡선과 대조), **집단 양극화**(라운드가 거듭될수록 의견이 극단으로 가는가), Reddit 의 **군중 효과**(초기 up/down 조작이 최종 점수를 바꾸는가). 주 발견: "larger agent group scale leads to more enhanced group dynamics and more diverse and helpful agents' opinions" — 196 → 10,196 에이전트에서 의견 다양성이 유의하게 늘었다(논문 §5, Safe-RLHF 척도).

### 1.2 아키텍처 (논문 §3 · 코드 `oasis/`)

```
[AgentGraph]  igraph|neo4j 로 follow 그래프          oasis/social_agent/agent_graph.py
     │
[SocialAgent ×N]  CAMEL ChatAgent 상속, 툴 = 행동     oasis/social_agent/agent.py
     │  Channel (asyncio.Queue + uuid message_id, 0.1s 폴링)   oasis/social_platform/channel.py
[Platform]  단일 코루틴 `running()` 이 큐를 소비해     oasis/social_platform/platform.py
     │      SQLite 에 쓰고 trace 를 남긴다             oasis/social_platform/schema/*.sql
     ├─ RecSys  update_rec_table() 을 step 마다        oasis/social_platform/recsys.py
     └─ Clock   k 배속 · time_step                     oasis/clock/clock.py
[OasisEnv]  reset()/step(actions)/close()             oasis/environment/env.py
```

- **Environment Server = `Platform`**. 사용자·포스트·댓글·관계(follow/like/mute…)·**trace**·추천(rec) 표. `trace` 는 `(user_id, created_at, action, info)` 네 칸에 PK 가 네 칸 전부다([`schema/trace.sql`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_platform/schema/trace.sql)). 모든 행동 함수가 끝에 `_record_trace()` 를 부른다([`platform_utils.py#L188`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_platform/platform_utils.py#L188)). `PRAGMA synchronous = OFF`([`platform.py#L84`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_platform/platform.py#L84)) — 내구성보다 처리량.
- **Agent Module = `SocialAgent(ChatAgent)`**([`agent.py#L55`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_agent/agent.py#L55)). 시스템 프롬프트 = 프로필(`UserInfo.to_system_message`). 한 번 깨어나면 `SocialEnvironment.to_text_prompt()` 가 **refresh(추천 포스트 JSON) + 팔로워 수 + 그룹 메시지**를 한 덩어리 user 메시지로 만들고([`agent_environment.py#L118`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_agent/agent_environment.py#L118)), LLM 이 **function calling 으로 행동 하나**를 고른다(`perform_action_by_llm`, `max_iteration=1` 기본 — 깨어남당 추론 1회). 메모리는 CAMEL `ChatHistoryMemory`; 스크립트 행동(`perform_action_by_data`)도 "Agent N performed X … result Y" 를 SYSTEM 역할로 메모리에 적는다([`agent.py#L286`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_agent/agent.py#L286)). `available_actions` 로 에이전트마다 **툴 목록을 잘라** 준다([`agent.py#L85`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_agent/agent.py#L85)). 여러 모델 엔드포인트를 `scheduling_strategy='random_model'` 로 섞는다.
- **행동 공간 = `ActionType`**([`typing.py#L17`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_platform/typing.py#L17)). enum 32 값(README 는 23, 논문은 21 — 버전에 따라 늘었다): 콘텐츠(`create_post`·`repost`·`quote_post`·`create_comment`), 반응(`like/unlike/dislike/undo_dislike` × post/comment), 관계(`follow/unfollow/mute/unmute`), 탐색(`refresh`·`trend`·`search_posts`·`search_user`), 조사자용(`interview`·`report_post`), 그룹(`create/join/leave/send_to/listen_from_group`), 상거래(`purchase_product`), 제어(`sign_up`·`update_rec_table`·`exit`), 그리고 **`do_nothing`**. 플랫폼별 기본 부분집합(`get_default_twitter_actions` 6개 / `_reddit_` 13개).
- **RecSys**([`recsys.py`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_platform/recsys.py)). X: in-network(팔로우 포스트, 좋아요순) + out-of-network(TwHIN-BERT 임베딩 코사인 × 최신성 × 작성자 팔로워 수, `topk`). Reddit: hot score `h = log10(max(|u−d|,1)) + sign(u−d)·(t−t0)/45000`([`recsys.py#L168`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_platform/recsys.py#L168)). `rec_prob=0.7` 로 개인화 70% / 무작위 30% 를 섞고, `RANDOM` 베이스라인이 따로 있다. `step()` 마다 `update_rec_table()` 을 **먼저** 돈다([`env.py#L152`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/environment/env.py#L152)).
- **Time Engine = `Clock`**([`clock.py`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/clock/clock.py), 33줄). `k` 배속(`Platform` 기본 60)으로 실제 경과 시간을 시뮬레이션 시각으로 변환(`time_transfer`) — Reddit hot score 처럼 **시각에 의존하는 규칙이 배속 아래서도 같은 순서로** 돌게 하려는 것. X 모드는 정수 `time_step` 만 쓴다. 누가 깨어날지는 엔진이 아니라 **실험 스크립트**가 정한다: 에이전트마다 24차원 시간대별 활동 확률 `active_threshold[hour]` 를 두고 timestep 마다 `random() < threshold` 면 `perform_action_by_llm()`([`twitter_simulation_1m.py#L146-L158`](https://github.com/camel-ai/oasis/blob/0004f5b/examples/experiment/twitter_simulation_1M_agents/twitter_simulation_1m.py#L146-L158); 핵심 196명은 고정 0.1). 3분 = 1 timestep(`0.05 * timestep` 시간).
- **Scalable Inferencer**. 별도 모듈이 아니라 (i) `OasisEnv.llm_semaphore = asyncio.Semaphore(128)`([`env.py#L70`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/environment/env.py#L70)) 로 동시 LLM 호출 상한, (ii) `step()` 이 활성 에이전트 전부를 `asyncio.gather` 로 띄우고 **전부 끝나야 다음 timestep**(라운드 배리어), (iii) 모델 쪽은 vLLM 엔드포인트 목록을 무작위 분배 — 100만 실험 설정은 3 호스트 × 24 포트([`twitter_1m.yaml`](https://github.com/camel-ai/oasis/blob/0004f5b/examples/experiment/twitter_simulation_1M_agents/twitter_1m.yaml)). 논문: Reddit 1만 명 실험 4 A100 에서 15분/timestep, 1천 명 0.83분, 100명 0.33분; 100만 명은 24 A100 으로 1주.
- **에이전트 생성기**([`agents_generator.py`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_agent/agents_generator.py)). 실제 사용자 데이터 + 나이·성격·직업 독립변수로 프로필 합성, 핵심 사용자를 0.2 확률로 팔로우하는 scale-free 그래프. `is_controllable` 에이전트는 사람이 콘솔로 조종(`perform_action_by_hci`).

### 1.3 평가 방법 (논문 §4–5)

| 현상 | 지표 | 재는 법 |
|---|---|---|
| 정보 확산(X) | **scale**(시간별 참여자 수), **depth**(전파 그래프 최대 깊이), **max breadth**(한 깊이의 최대 참여자 수) | 실제 곡선 대비 정규화 RMSE(+ 분 단위 RMSE 신뢰구간) |
| 집단 양극화(X) | 라운드 전후 의견이 더 극단/진보/무승부 | **LLM 판정**(GPT-4o-mini) |
| 군중 효과(Reddit) | 포스트 점수(up−down)·댓글의 반대 정도(disagree score) | 반사실(초기 up/down 심기) 대조군 비교 |
| 절제 실험 | 시간대 활동 확률을 균일 1.0 으로 바꾸면 RMSE 15~20% 악화. RecSys 를 빼면 팬 밀집 커뮤니티 외에는 확산이 조기 종료. TwHIN-BERT > MiniLM·BERT | — |

### 1.4 실험 재현 관례 (`examples/experiment/`)

- 스크립트 하나 + **YAML 하나**(`data / simulation / inference` 세 절). 결과는 **SQLite 파일 하나**(`trace` 표가 전부의 원본)이고, `visualization/<실험>/code/analysis_*.py` 가 그 DB 를 읽어 그림을 만든다 — 실행과 분석이 파일 하나로 분리된다.
- `examples/experiment/README.md` 가 **돌리기 전 비용을 문장으로** 적는다: "36 agents × 활성 확률 0.1 × 2 timesteps ≈ 7.2 agent inferences ≈ 14 API requests(GPT-4)". 
- 개입은 `ManualAction(action_type, action_args)` 를 특정 timestep 의 특정 에이전트에 심는 방식([`env_action.py`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/environment/env_action.py); 오정보 쿡북 [`docs/cookbooks/misinformation_spreading.mdx`](https://github.com/camel-ai/oasis/blob/0004f5b/docs/cookbooks/misinformation_spreading.mdx)). `interview` 행동으로 실행 중 에이전트에게 **메모리에 남기지 않고** 질문할 수 있다(`interview_record=False` 기본, [`agent.py#L197`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_agent/agent.py#L197)).

### 1.5 규모 기법 — 한 줄씩

동시 호출 세마포어 128 · 라운드 배리어(`gather`) · 모델 엔드포인트 무작위 분배 · SQLite `synchronous=OFF` · 추천 임베딩 배치 1000 · 한 깨어남 = 추론 1회(`max_iteration=1`) · 활동 확률로 timestep 당 깨어나는 수를 줄인다(100만 중 시간대별 수 %). **병목은 GPU 추론이고 해법은 "덜 깨우고, 깨운 것은 한 번에"** 다.

### 1.6 읽으며 걸린 것 (OASIS 자체의 한계)

- 문서 [`docs/simulation/simulation_settings.mdx`](https://github.com/camel-ai/oasis/blob/0004f5b/docs/simulation/simulation_settings.mdx) 는 `EnvAction(activate_agents, intervention)`·`SingleAction` 을 설명하지만 코드에는 `ManualAction`·`LLMAction` 만 있다(`oasis/environment/env_action.py`). 문서가 낡았다 — 인용은 코드 기준으로 했다.
- `trace` 의 PK 가 `(user_id, created_at, action, info)` 전부라 **같은 초에 같은 행동을 두 번 하면 한 행**이다. 우리 `(task_id, seq)` 멱등키(harness §7)가 더 낫다.
- `Channel.read_from_send_queue` 가 0.1초 폴링. `time_transfer` 는 배속만 하지 timestep 과 벽시계를 잇지 않아(X 모드) 시각 의존 규칙이 플랫폼마다 다르게 돈다.

## 2. Colab 과의 대응표

| OASIS 개념 | 위치 | Colab 대응 | 위치 | 판단 |
|---|---|---|---|---|
| `Platform` + SQLite (환경 서버) | `oasis/social_platform/platform.py` | API 서버 + Postgres, Router | PRD §8.1 | 같은 자리. 우리는 단일 신뢰 지점·내구성 우선(§12 "서명 채택 안 함"), OASIS 는 처리량 우선 |
| `ActionType` 행동 공간(함수 호출 툴) | `typing.py`, `agent_action.py` | `colab` CLI/MCP 도구면 | FR-7.4 | 우리 표면 9 명령이 OASIS 32 행동에 해당. OASIS 는 `available_actions` 로 **에이전트별 부분집합**을 준다 — 우리는 세션 참여 여부·토큰 유무로만 켜고 끈다(harness §2.1) → C5 |
| `trace` 표 (행동 로그) | `schema/trace.sql`, `_record_trace` | `task_event` (class·verb·object_ref·outcome·seq) + `activity_log` | harness §7, `task_event.schema.json`, §9 감사 | 우리가 더 구조적이다. OASIS 에만 있는 것: **`do_nothing` 이 1급 행동** → C3 |
| RecSys (누가 무엇을 보나) | `recsys.py`, `update_rec_table` | 멘션 라우팅 규칙 1~8 + lane 해소 + 합류 | FR-3.3, FR-6.5 | OASIS 는 확률적·학습된 노출, 우리는 결정적 규칙. 대응은 "정보가 누구에게 가는가" 뿐. OASIS 절제 실험(RecSys 제거 → 확산 조기 종료)이 말하는 것: **노출 규칙이 집단 결과를 좌우한다** → 우리 규칙 6·7(assignee 폴백)·합류(위임자 한 명) 의 집중을 재야 한다 → C2 |
| `Clock` 배속·timestep | `clock.py` | 실시간 heartbeat 15s · stall 3분 · dispatched 5분 · HITL 24h · 오프라인 7일 · GC 14일 | FR-7.1, harness §7 stall, FR-9.2, FR-6.4 | 우리는 시간을 압축하지 않는다. e2e 는 이미 서버 클럭 우회로 대신한다(T-I3 실기 우회 "클럭") → 안 함 |
| 활동 확률 `active_threshold[24]` | `twitter_simulation_1m.py#L146` | 없음 — 트리거는 멘션 | FR-3.3 | 대응 없음. 우리 에이전트는 "깨어날 확률"이 아니라 "깨워진 이유"가 있다(`session_hop.rule`) |
| `asyncio.Semaphore(128)` + 라운드 배리어 | `env.py#L70`, `step()` | `max_parallel_lanes` 5 · 에이전트 `max_concurrent_tasks` · 데몬 10 · 워크스페이스 50, SKIP LOCKED 큐 | FR-6.3, §9 | 우리는 배리어 없는 이벤트 구동. 병목도 다르다(GPU ↔ 계정 한도·Director). `rate_limited` → `not_before` 재큐잉(FR-7.1)이 OASIS 의 엔드포인트 분산에 해당 → 안 함 |
| `ChatHistoryMemory` + 스크립트 행동을 메모리에 SYSTEM 으로 기록 | `agent.py#L286` | `runtime_session_ref` resume 우선 + 콜드 스타트 `<resumed>` 프롬프트("이미 게시한 메시지 목록") | FR-4, FR-5.4, FR-7.1 M5, harness §6 | 같은 패턴. 우리는 런타임이 메모리를 갖고 우리는 참조만 가진다. 가져올 것 없음 |
| `max_iteration=1` (깨어남당 추론 1회) | `agent.py#L69` | "위임 후 턴을 종료한다", HITL 호출 = 턴 종료 | §8.3, FR-5.4 | 같은 결정 — 에이전트를 짧게 깨워 상태는 플랫폼이 갖는다 |
| `interview` (메모리 밖 질문) | `agent.py#L197`, `platform.py#L1348` | 테스트 채팅(FR-1.8.1, §4.5 — 세션 아님·토큰 없음) | daemon-protocol §4.5 | 가장 가까운 것은 테스트 채팅. 실행 중 lane 의 런타임 세션에 "부작용 없는 질문"은 Claude Code 에서 보장 못 한다(툴을 부른다) → 안 함 |
| `ManualAction` 개입·`is_controllable` 에이전트 | `env_action.py`, `agents_generator.py#L348` | Director 메시지·「중단하고 다시 지시」·`/note` | FR-3.4, 시나리오 C | 우리는 사람이 항상 안에 있다. 시나리오 e2e 의 acpfake `exec` 스텝이 `ManualAction` 에 해당(T-I5 #206) |
| `AgentGraph` follow 그래프 | `agent_graph.py` | 세션 로스터 + lane DAG(`depends_on`) + `session_hop(from,to,rule,cause_hop_id)` | FR-6.2, `server/migrations/0006`·`0022` | 우리 그래프는 **세션당 인과 그래프**로 이미 DB 에 있다 → C1 이 이걸 쓴다 |
| 확산 지표 scale·depth·max breadth | 논문 §4.1 | `max_chain_depth`(FR-3.5) 는 **상한**만 있고 **분포**는 안 잰다 | FR-3.5, §11 | → C1 |
| 양극화 판정 = LLM 판정자 | 논문 §4.2 | `criteria_met` 판정 = 플랫폼 LLM(단독 사용 금지) | §8.1, §12 | 같은 도구, 우리는 이미 보수적으로 쓴다. "합의 형성" 지표는 리뷰 왕복 수로 결정적으로 잴 수 있다 → C6 |
| `report_post`·`report_threshold=2` 커뮤니티 신고 | `platform.py#L116` | 없음(Director 단일 결정권자) | FR-5 | 안 함 |
| 그룹 채팅 5 행동 | `typing.py#L45-49` | 세션·스레드, v1.1 채널 계층 | §10 v1.1 | 이미 있음 |
| 실험 YAML + DB + 분석 스크립트 분리, README 의 사전 비용 문장 | `examples/experiment/README.md` | `e2e/p5/72_~75_` + `out/`, `plan/G9_REPORT.md` | `e2e/p5/README.md` | → C7 |

## 3. 반영 후보

권고 눈금: **지금** = P5 잔여(G8·G9 전) 에 문서·관찰만으로 넣을 수 있고 계약 변경이 없다 / **v1.1** = 계약·화면 변경이 필요하거나 G9 지표 10개를 흔든다 / **안 함** = 목적이 달라 가져올 것이 없다.

| # | 후보 | 근거(OASIS) | 우리 어디 | 기대 효과 | 비용 | 리스크 | 권고 |
|---|---|---|---|---|---|---|---|
| **C1** | **세션 트리거 사슬 관찰 지표 3개(목표치 없음)** — 사람 메시지 1건이 만든 파생 task 수(**scale**), 도달한 최대 `chain_depth`(**depth**), 합류 그룹의 최대 자식 수(**max breadth**). 워크스페이스 분포(중앙값·p95)로 본다 | 논문 §4.1 정보 확산 지표(Vosoughi 2018 세 지표). "규모가 커질수록 집단 역학이 달라진다"는 주 발견 — 우리도 상한(8·60·5)이 맞는지 **분포**를 봐야 안다 | §11(관찰 행), FR-3.5 상한 조정의 근거. 데이터는 이미 `session_hop(rule, cause_hop_id, chain_depth)`·`lane.delegated_from_task_id` 에 있다(0006·0022) | Director 가 `max_chain_depth` 8·`max_pair_roundtrips` 5 를 감으로 고치지 않고 분포를 보고 고친다. S-76/S-78(F1 형이 깊이 9 로 pause) 같은 사건을 **미리** 본다 | 낮음 — 읽기 SQL 1벌. 화면은 S14 대시보드 표 아래 "관찰" 3행. `getWorkspaceMetrics` 10개(G9)는 건드리지 않고 별도 op 또는 `breakdown` — **Lead 결정** | 지표가 늘면 G9 "10개 그대로 읽힘" 조건과 섞여 보일 수 있다 → 표를 나눈다. 목표치를 걸면 안 된다(관찰용) | **지금**(PRD §11 제안 행. 구현은 Lead 가 P5 잔여 또는 v1.1 로 배치) |
| **C2** | **라우팅 집중 관찰 + §12 리스크 행** — task 를 만든 `session_hop.rule` 분포(특히 규칙 6·7 폴백 비율)와 에이전트별 트리거 점유율(assignee·Lead 로 쏠림) | 논문 §5 절제: RecSys 를 빼면 확산이 조기 종료, TwHIN 이 MiniLM 보다 낫다 — **노출 규칙이 결과를 좌우한다.** 우리 규칙 6·7 은 멘션 없는 사람 메시지를 전부 assignee 에게, 합류는 전부 위임자에게 보낸다 | FR-3.3 규칙 6·7, FR-6.5, §11(관찰 행), §12(리스크 행). 데이터: `session_hop.rule`(0006) | "Director 가 멘션을 안 써서 Lead 가 모든 것을 받고 병렬이 안 생기는" 패턴을 수치로 본다(G8 실측 5명의 로그에서 바로 읽을 수 있다) | 낮음 — SQL 1벌(C1 과 같은 표). §12 한 행 | 편향을 "고치려" 규칙을 바꾸면 결정적 라우팅의 장점(FR-3.5 "내용 기반 억제 안 함")을 잃는다 → 관찰만, 규칙 변경은 별도 결정 | **지금**(§11 관찰 행 + §12 리스크 행 제안) |
| **C3** | **빈 턴(empty turn) 렌더·집계** — 턴이 메시지 0·플랫폼 조작 0·파일 편집 0 으로 `end_turn` 한 경우를 피드에 "아무것도 하지 않고 끝냈다" 카드로 보이고, 세션당 빈 턴 수를 관찰 | `ActionType.DO_NOTHING` 이 1급 행동이고 trace 에 남는다([`typing.py#L42`](https://github.com/camel-ai/oasis/blob/0004f5b/oasis/social_platform/typing.py#L42)) — "하지 않기로 한 것"도 데이터다 | FR-7.2 "절대 캄캄해지지 않는다"·"침묵도 렌더"(턴 **중**의 침묵만 다룬다), D-13(refusal & 활동 0 → 재시도 — **refusal 만**), §11 관찰. 데이터: `task_event` 에 `status`·`tool.edit_file`·`message.say` 가 하나도 없는 attempt | 멘션이 낭비된 턴(프롬프트 규칙 위반 또는 정당한 무응답)을 Director 가 본다. §8.3 "빈 확인 메시지 금지" 의 반대편 실패(아무 말도 안 함)를 잡는다 | 낮음~중 — 서버가 finish 시 판정해 시스템 이벤트 1건(`runtime.turn_end` payload 에 `empty: true` 또는 별도 카드). 웹 렌더 1 클래스 | 정당한 빈 턴(예: 위임자가 질문 알림에 "답할 것 없음")을 실패처럼 보이면 안 된다 — **정보 카드, 오류 아님**. `task_event` 스키마는 닫혀 있어 새 키는 계약 변경(K-계약) | **지금**(PRD FR-7.2 제안 문장 + §11 관찰). 계약 키는 Lead |
| C4 | 행동 로그 어휘 대조 — OASIS 32 행동 ↔ 우리 `verb` 21개. 우리에게 없는 것: `do_nothing`(→C3), `search_user`/`trend`(탐색은 `read`·`search` 로 덮임), `report_post`(없음), `interview`(테스트 채팅) | `typing.py`, harness §7, `task_event.schema.json` verb enum | harness §7 | 어휘 변경 필요 없음 — 대조 결과 우리 verb 가 부족하지 않다. C3 만 남는다 | 0 | — | **안 함**(대조만 기록) |
| C5 | **역할별 행동 부분집합**(`available_actions`) — 예: reviewer 는 `lane delegate` 불가, researcher 는 `review approve` 불가. 지금은 세션 참여자면 9 명령 전부 가능하고 프롬프트 규칙(§8.3)만 막는다 | `agent.py#L85` `available_actions` 로 툴 목록 자체를 자른다 — 프롬프트가 아니라 **표면**으로 막는다(우리 FR-3.5 "서버측 억제는 구조적 규칙으로만" 과 같은 철학) | FR-1.1 호출 권한 게이트, FR-7.4, §8.3, harness §2.1(토큰 없으면 전부 끔 — 전부/전무 두 단계뿐) | reviewer 가 위임을 시작해 lane 이 늘어나는 사고, non-lead 가 `status set done` 으로 세션을 닫는 사고를 **서버 403** 으로 막는다 | 중 — `agent.role → allowed ops` 표(계약), 서버 403 + 활동 피드 거부 카드, MCP 서버는 툴 목록 자체를 줄인다(가능), 골든 행 | 너무 좁히면 "리뷰어가 수정을 직접 못 해서" 왕복이 는다. 기본은 넓게, `custom` 은 전부 허용. S-84(reviewer 필수 422)처럼 역할 의미가 이미 서버에 들어오기 시작했으니 방향은 맞다 | **v1.1**(계약 변경. P2_BACKLOG "OASIS 후보") |
| C6 | **합의 형성 지표** — 아티팩트가 `review approve` 까지 거친 `reject` 왕복 수 중앙값, 세션당 `decision record` 수 | 논문 §4.2 양극화 = 라운드 전후 의견 변화. 우리는 LLM 판정 없이 **리뷰 왕복 수**로 결정적으로 잰다 | §11, FR-2.2 `agent_approval`, FR-4.2 | "리뷰가 승인까지 몇 번 도는가" 는 팀 구성(reviewer 프로파일)의 품질 지표 | 낮음 — SQL. 하지만 §11 표(G9)에 넣으면 11번째 지표 | 표본이 적으면 의미 없음 | **v1.1**(G9 뒤 §11 개정 때) |
| C7 | **e2e 스크립트 머리에 "비용 한 줄"** — 에이전트 턴 수·실기 예상 비용·소요 시간을 각 `e2e/p*/NN_*.sh` 와 README 표에 적는다(페이크 런타임은 $0 명시) | `examples/experiment/README.md` "36 agents × 0.1 × 2 steps ≈ 7.2 inferences ≈ 14 API requests" | `e2e/p5/README.md`, `e2e/README.md` | 실기 대조(`DAEMON_BIN` 스위치) 를 돌리기 전에 얼마가 드는지 안다 — 워커 한도(P2_BACKLOG 운영 "4 worker 동시는 5시간 창을 20~30분에 소진") 와 같은 문제 | 아주 낮음 — 문서 | 없음 | **지금 — 단 PRD 아님**(문서 관례. P2_BACKLOG "OASIS 후보" 에만) |
| C8 | 세션 시작 전 **페이크 런타임 리허설**(acpfake 로 위임 DAG 미리 돌리기) 을 제품 기능(S6 마법사)으로 | OASIS 는 시뮬레이션 자체가 제품; `ManualAction` 을 심어 시나리오를 돌린다 | S6, `daemon/acpfake`, `server/test/sim`, `e2e/p5/72_~75_` | — | 높음 — 우리 DAG 는 미리 정해진 것이 아니라 **Lead 의 LLM 이 턴 중에 만든다.** 페이크로 돌리면 라우팅·합류 배관만 검증되는데 그것은 CI(72_~75_, T-I5 #206)가 이미 매 PR 마다 한다. Director 에게 스크립트를 쓰게 할 수 없다 | 리허설이 "통과" 해도 실기에서 다른 DAG 가 나온다 — 거짓 확신 | **안 함**(제품). 테스트 자산으로는 이미 하고 있다. 변형 "세션 템플릿 저장 시 로스터·권한 스모크(acpfake)" 는 v1.1 후보로만 적어 둔다 |
| C9 | 시간 배속(`Clock(k)`) 을 서버·데몬 공통 주입 클럭으로 | `clock.py` | FR-7.1 타임아웃들, e2e 실기 우회 "클럭"(T-I3) | e2e 에서 24h HITL·7일 오프라인·14일 GC 를 실시간으로 검증 | 이미 서버 클럭 우회가 있고 데몬 stall 3분은 실측이 목적(S-66) | 배속이 stall 판정을 바꾸면 S-66 류를 못 본다 | **안 함**(있는 것으로 충분) |
| C10 | 대규모 동시성 기법(세마포어·라운드 배리어·엔드포인트 분산) 을 §9 동시 50 에 | 논문 §3.5, `env.py#L70` | §9, FR-6.3, `e2e/p5/76_perf.sh`(동시 50 실측) | — | 병목이 다르다: OASIS 는 GPU 처리량, 우리는 계정 한도(`rate_limited`)·Director 응답·워크트리 I/O. 76_ 이 이미 50 을 잰다 | 배리어를 넣으면 lane 독립성(FR-6)이 깨진다 | **안 함** |
| C11 | 추천 시스템 편향 관찰(개인화 vs 무작위 `rec_prob`) | `recsys.py`, `rec_prob=0.7` | FR-3.3 | — | 우리 라우팅은 학습된 것이 없어 "편향" 은 규칙의 **구조적 집중**뿐 → C2 로 흡수 | — | **안 함**(C2 로) |
| C12 | `interview` — 실행 중 에이전트에게 메모리 밖 질문 | `agent.py#L197` | 테스트 채팅 §4.5 | Director 가 "지금 뭘 하고 있니" 를 lane 에 묻기 | ACP 에는 부작용 없는 prompt 가 없다(툴을 부른다). heartbeat `preview` 와 활동 피드가 이미 그 답 | 턴을 끊거나 이중 쓰기 | **안 함** |
| C13 | 에이전트 프로필 대량 생성·scale-free 그래프 | `agents_generator.py` | FR-1.5 동적 생성 금지 | — | 목적 정반대(우리는 등록된 풀 안에서만) | — | **안 함** |
| C14 | `report_post` 커뮤니티 신고·임계값 | `platform.py#L116` | — | — | 결정권자가 한 사람(Director) | — | **안 함** |

### 3.1 "지금" 3건이 PRD 에 들어간 모양

`PRD.md` 에 "### v0.17 변경 제안 (OASIS 리뷰, 미확정)" 절을 두고 다음 자리에 `[제안 v0.17]` 꼬리표 문장을 **덧붙였다**(기존 문장 삭제·재작성 없음).

| 후보 | PRD 자리 | 덧붙인 것 |
|---|---|---|
| C1 | §11 표 아래 | 관찰 행 3개(scale·depth·max breadth) — 목표치 없음, G9 10개와 별도 |
| C2 | §11 표 아래 · §12 리스크 표 | 관찰 행 1개(규칙 6·7 폴백 비율·에이전트별 트리거 점유율) · 리스크 행 "라우팅의 구조적 집중" |
| C3 | FR-7.2 원칙 목록 뒤 · §11 표 아래 | "빈 턴도 렌더한다" 문장 · 관찰 행 1개(빈 턴 비율) |

SCREEN·EVAL 은 건드리지 않았다 — S14 대시보드 탭이 "§11 표를 그대로" 그리므로 관찰 행을 **같은 표에 넣을지 아래 표로 나눌지**는 Lead 가 정한 뒤 SCREEN 을 고쳐야 한다(§4 확인 3).

## 4. 결론 3줄과 확인이 필요한 것

- **가져올 것**: 집단을 재는 눈 — 트리거 사슬의 규모·깊이·폭(C1), 라우팅 집중(C2), 빈 턴(C3). 셋 다 우리 DB(`session_hop`·`task_event`)에 데이터가 이미 있고 관찰만 하므로 계약을 흔들지 않는다. 문서 관례로는 e2e 비용 한 줄(C7).
- **안 가져올 것과 이유**: 추천 시스템·시간 엔진·대규모 추론기·에이전트 생성기·리허설(C8~C14). OASIS 는 에이전트를 **관찰 대상**으로 값싸게 많이 깨우는 도구이고, 우리는 에이전트를 **노동자**로 비싸게 적게 깨우며 사람이 루프 안에 있다. 병목(GPU ↔ 계정 한도·Director)과 부작용(DB 안 ↔ 저장소)이 달라 기법이 옮겨지지 않는다.
- **확인이 필요한 것**: (1) C1·C2·C3 관찰 행을 `getWorkspaceMetrics` 에 얹을지(G9 "10개 그대로" 조건과의 분리) 별도 op 로 할지 — Lead. (2) C3 의 "빈 턴" 판정 위치(서버 finish 시 vs 데몬)와 `task_event` 닫힌 스키마에 키를 더할지 — 계약 결정. (3) C5 역할별 행동 부분집합을 v1.1 에 넣을지 — S-84(reviewer 필수) 와 같은 결의 결정이라 Director 판단. (4) OASIS 문서와 코드가 어긋나 있어(§1.6) 인용은 코드 커밋 `0004f5b` 기준 — 이후 버전에서 이름이 바뀔 수 있다.
