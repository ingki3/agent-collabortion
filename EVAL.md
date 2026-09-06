# EVAL — 테스트 시나리오와 정확한 예상 동작

| 항목 | 내용 |
|---|---|
| 문서 버전 | v0.10 — E13-18·E13-19·E14-10·E6-12 추가(P4a 골든 작성 중 드러난 "한쪽만 구현해도 통과하는" 구멍 4건, PR #152); E13-03~06 을 스파이크 5(PR #153) 판정대로 정정(`skip-worktree` 폐기 → 미추적 `COLAB_BRIEF.md`) + E13-06a. v0.9 — E9-01·E9-10 실기 도달 노트(G6 2판, K-8 해소). v0.8 — E9-10(사후 발견 예산 강제, S-44 Lead 결정 A). v0.7 — E8-05a(같은 seq 재전송 = 멱등 replay, PR #120 제안). v0.6 — E7-20·E7-21·E9-08·E9-09·E10-14 추가(P3a 골든 작성 중 드러난 "한쪽만 구현해도 통과하는" 구멍 5건, PR #108). v0.5는 E1-22(합류 뒤 자식 멘션의 기상 한도 = FR-3.4 병합, K-5). v0.4는 E5-02가 재큐잉하지 않는다는 것을 명시(계약이 반대로 적고 있었다, `daemon-protocol.md` v0.6에서 정정). v0.3은 P2a 골든 테이블 작성 중 드러난 공백 4행 추가(E1-21 억제와 합류 전달은 한 쌍, E2-14 새 lane 토글 자동 해제, E2-15 중단 후 재지시의 lane, E4-10 재개 시 카운터). v0.2는 P1 통합(G3)에서 드러난 결함 5건을 회귀 행으로 승격(E8-13·E10-13·E11-11·E11-12·E17-09) |
| 작성일 | 2026-09-05 |
| 근거 | `PRD.md` v0.11 (FR 번호로 인용), `PLAN.md` v0.5 (게이트 G1~G9, §5 테스트 전략) |
| 목적 | PRD의 규칙을 **입력 → 정확한 출력** 쌍으로 옮긴다. 이 문서의 한 행이 테스트 하나다. "됐다"를 사람마다 다르게 읽지 않게 하는 것이 PLAN의 DoD 원칙이고, 이 문서가 그 DoD의 실체다 |
| 사용 | P2a에서 Reviewer 에이전트가 §E1~E6을 골든 테스트로 옮긴다. P3 시뮬레이터는 §E8. 게이트 판정은 해당 절의 전 행 통과가 조건이다 |

## 0. 읽는 법

각 행은 네 칸이다.

| 칸 | 뜻 |
|---|---|
| **전제** | 테스트 시작 시 상태. 세션·에이전트·lane·task의 값 |
| **자극** | 한 가지 입력. 메시지 게시, CLI 호출, 이벤트 도착, 시간 경과, 버튼 |
| **예상** | 정확한 결과. 상태 값, 생성된 레코드 수, 깨어난 에이전트, **그리고 일어나지 않아야 하는 것**. 모호한 표현("적절히", "알림")은 쓰지 않는다 |
| **검증** | `unit`(결정적, 골든 테이블) / `harness`(가짜 ACP 서버) / `e2e`(실제 런타임) / `sim`(부분 실행 시뮬레이터) / `manual`(사람 실측) |

공통 고정값: 세션 `S`, Director `Dir`, 에이전트 `Lead`(assignee)·`R`(Researcher)·`W`(Writer)·`QA`. 루프 상한은 기본값(8 / 60 / 5). HITL `due_in` 기본 24h. 표기 `T(x)`는 에이전트 x에 생성된 task, `L(x,n)`은 x의 n번째 lane.

---

## E1. 라우팅 규칙 1~8 (FR-3.3) — G4

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E1-01 | S active, 참여자 Lead·R·W | Dir가 `/note 회의록 정리` 게시 | task 0개. 메시지는 타임라인에 저장. 활동 피드에 "기록만" 표시 없음(일반 메시지와 같은 렌더) | unit |
| E1-02 | 〃 | Dir가 `@R 시장 규모 조사해줘` 게시 | T(R) 1개. `trigger_message_id` = 이 메시지. Lead·W 트리거 없음 | unit |
| E1-03 | 〃 | Dir가 `@R @R 조사` 게시 (중복 멘션) | T(R) **1개** | unit |
| E1-04 | 〃, `X`는 워크스페이스 에이전트지만 S 비참여 | Dir가 `@X 도와줘` 게시 | task 0개. 메시지 게시됨. 작성자에게 **경고 표시**("X는 이 세션 참여자가 아님"). X 트리거 없음 | unit |
| E1-05 | 〃 | Dir가 `@all 진행 상황 공유` 게시 | task 0개. 암묵 라우팅(규칙 6) **억제됨** — Lead에게도 가지 않음 | unit |
| E1-06 | 〃, 멤버 `M2` 존재 | Dir가 `@M2 확인 부탁` 게시 (사람만 멘션) | task 0개. Lead 트리거 없음 | unit |
| E1-07 | 〃 | R(에이전트)이 멘션 없이 `조사 결과입니다…` 게시 | task 0개 (규칙 4: 에이전트 메시지는 멘션 있을 때만) | unit |
| E1-08 | 〃 | R이 `@W 초안 부탁` 게시 | T(W) 1개 | unit |
| E1-09 | 〃, R의 메시지 `m1`이 T(R)에서 게시됨 | Dir가 `m1`에 **답글** (멘션 없음) | T(R) 1개 (규칙 5: 답글 → 그 에이전트). Lead 트리거 없음 | unit |
| E1-10 | 〃, 스레드 루트 `m2`는 W 소유 | Dir가 스레드 안에서 답글 (멘션 없음) | T(W) 1개 | unit |
| E1-11 | 〃 | Dir가 최상위에 멘션 없이 `이제 시작하자` 게시 | T(Lead) 1개 (규칙 6: assignee) | unit |
| E1-12 | 〃 | Dir가 R의 메시지에 답글 (규칙 5로 R 트리거) | T(R) 1개 **+ T(Lead) 1개 `deferred`, 5분 후 예약**(규칙 7 폴백) | unit |
| E1-13 | E1-12 직후, 4분 경과 | R이 응답 메시지 게시 | 예약된 T(Lead) **`cancelled`**. Lead 실행 없음 | unit |
| E1-14 | E1-12 직후 | 5분 경과, R 응답 없음 | T(Lead) `deferred → queued`. Lead 실행됨 | unit |
| E1-15 | Lead가 `colab lane delegate --agent R`로 L(R,1) 생성. 합류 그룹 `J1` 미발화 | R(L(R,1))이 `@Lead 완료했습니다` 게시 | **T(Lead) 0개**(규칙 8). 메시지는 게시됨. 합류 묶음 `J1`의 페이로드에 이 메시지 포함 | unit |
| E1-16 | 〃 | R이 `@Lead @QA 확인 부탁` 게시 | T(Lead) **0개**, T(QA) **1개** (억제 범위는 위임자 한 명) | unit |
| E1-17 | 〃, **`J1` 이미 발화됨**, L(R,1) 재진입 중 | R이 `@Lead 추가 질문` 게시 | T(Lead) **1개** (억제 기간은 합류 발화 전까지) | unit |
| E1-18 | E1-15 상황에서 R이 위임자에게 물어야 함 | R이 `colab status set blocked --note "범위?"` | §E3-05 경로. 멘션 억제와 무관하게 Lead 즉시 깨어남 | unit |
| E1-19 | 규칙 우선순위 | Dir가 `/note @R 이거 봐줘` 게시 | task 0개 (규칙 1이 규칙 2보다 먼저) | unit |
| E1-20 | 규칙 우선순위 | Dir가 `@all @R 시작` 게시 | 규칙 2가 먼저: T(R) 1개. `@all`은 암묵 라우팅만 억제 | unit |
| E1-21 | 규칙 8 억제 중. 자식 C가 위임자 L만 멘션하고 다른 참여자 없음 | C가 `@L 끝냈습니다` 게시 | T(L) **0개**(억제). 그러나 그 메시지는 **합류 묶음 `J1`의 페이로드에 실린다** — 억제와 전달은 한 쌍이다. 억제만 검증하면 메시지가 조용히 사라지는 구현이 통과한다 | unit |
| E1-22 | **`J1` 발화 후**, L(R,1)이 이어서 작업 중. Lead task 없음 | R이 `@Lead 추가로 발견한 것` 두 번 연속 게시 | 첫 게시로 T(Lead) **1개** `queued`(규칙 8 억제 해제). 두 번째 게시는 **새 task 없이** 그 task에 병합 — `coalesced_message_ids` = [m_a, m_b](FR-3.4, K-5). Lead 기상은 자식 발언 수가 아니라 Lead가 task를 마치는 횟수에 묶인다 | unit |

---

## E2. lane 해소와 병합 (FR-3.3 lane 규칙, FR-3.4, FR-6.1) — G4

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E2-01 | L(R,1)의 task가 게시한 `m1`이 스레드 루트 | Dir가 `m1`에 답글 | T(R)의 lane = **L(R,1)** (해소 규칙 1). 새 lane 없음 | unit |
| E2-02 | R에 lane 없음 | Lead가 `colab lane delegate --agent R --brief "A"` | **새 lane** L(R,1) (규칙 2). `delegated_from_task_id` = Lead의 현재 task | unit |
| E2-03 | L(R,1) `running` | Lead가 `colab lane delegate --agent R --brief "B"` | **새 lane** L(R,2). L(R,1)과 무관. 격리 `none`이면 workdir 2개 | unit |
| E2-04 | L(R,1) `done`, 다른 lane 없음 | Dir가 최상위에 `@R 보완해줘` | lane = **L(R,1)**, `done → running`, `reentry_count` 0→1 (규칙 3). 새 lane 없음 | unit |
| E2-05 | L(R,1) `blocked` | Dir가 최상위에 `@R` | L(R,1) `blocked → running`, `reentry_count`+1 | unit |
| E2-06 | L(R,1) `done`, L(R,2) `done`, L(R,2)가 더 최근 | Dir가 최상위에 `@R` | lane = **L(R,2)** (가장 최근) | unit |
| E2-07 | L(R,1) `done` | Dir가 **"새 lane으로 보내기" 토글 ON**으로 `@R` | **새 lane** L(R,2) (규칙 3 건너뜀 → 4). 전송 버튼 옆에 토글 상태 표시됨 | unit + manual |
| E2-08 | R에 lane 없음 | Dir가 최상위에 `@R` | 새 lane L(R,1) (규칙 4). `delegated_from_task_id` **비어 있음** | unit |
| E2-09 | L(R,1) `running`(턴 진행 중) | Dir가 `@R 범위를 좁혀줘` | 진행 중 턴 **취소되지 않음**. T(R) `queued`, lane = L(R,1). 프로세스 계속 실행 (FR-3.4 불변식) | harness |
| E2-10 | L(R,1) `running`, `queued` task `t1` 존재 | Dir가 L(R,1)에 두 번째 메시지 | `queued` task **여전히 1개**. `t1.coalesced_message_ids` = [m_a, m_b] 도착 순서. 다음 run 프롬프트에 두 메시지 순서대로 인용 | unit |
| E2-11 | L(R,1) `running`, L(R,2) `running`, 각각 `queued` 1개 | — | 병합 **lane 단위**: L(R,1) 큐와 L(R,2) 큐 서로 섞이지 않음. R 단위 병합 아님 | unit |
| E2-12 | 격리 `worktree`, R에 L(R,1)·L(R,2) | 둘 다 `queued` | workdir **1개**(`colab/S/R` 브랜치). L(R,2)는 L(R,1) 종료 후 실행 (순차, FR-6.3). 동시 실행 0 | unit |
| E2-13 | 격리 `none`, 같은 상황 | 〃 | workdir **2개**, 동시 실행 2 | unit |
| E2-14 | E2-07 직후("새 lane으로 보내기" 토글 ON으로 한 번 전송함) | Dir가 이어서 최상위에 `@R` 게시 | 토글이 **전송 후 자동 해제**돼 있어 규칙 3이 적용된다: 새 lane 없이 **가장 최근 lane 재사용**. 해제되지 않으면 이후 모든 멘션이 lane을 새로 만들어 규칙 3이 사실상 죽는다 | unit + manual |
| E2-15 | L(R,1) `running`, Dir가 "중단하고 다시 지시" | 새 지시 전송 | 새 task는 **같은 lane L(R,1)** 에 남는다(lane은 `running` 유지). 새 lane을 만들지 않는다. `restarted_from_task_id`가 이전 task를 가리킨다 | unit |

---

## E3. 합류와 `blocked` 경로 (FR-6.5, FR-6.2, FR-6.2.1) — G5

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E3-01 | Lead task `t0`가 R에 lane 3개 위임(그룹 J1) | L1·L2 `done`, L3 `running` | Lead 트리거 **0회**. 합류 대기 | unit |
| E3-02 | 〃 | L3 `done` | Lead에 `progress_update` 시스템 메시지 **정확히 1회**. 페이로드에 lane 3개 결과. T(Lead) 1개 | unit |
| E3-03 | 〃, L2가 `failed` | L3 `done` | 합류 **발화**(실패해도 기다렸다가 묶음). 페이로드에 L2 실패 사유 포함 | unit |
| E3-04 | 〃, L2 `waiting_human` | L1·L3 `done` | 합류 **대기**(waiting_human은 종료 아님). Lead 트리거 0 | unit |
| E3-05 | 〃, L2의 R이 `colab status set blocked --note "범위?"` | — | (1) L2 `blocked`, workdir 보존, 프로세스 종료 (2) 서버가 L2 스레드에 **질문 카드 메시지** 게시, `lane.blocked_message_id` 설정 (3) Lead에 **즉시** 시스템 메시지 — 카드 인용, 프롬프트에 "질문 알림이며 합류가 아니다" 문구. 이 시점 형제 L1·L3 상태 무관 | unit |
| E3-06 | E3-05 후, L1·L3 `done` | — | 합류 **발화**(blocked = 종료 취급). 페이로드에 L2 질문 재포함. 합류 프롬프트에 **"답을 기다리는 자식 1개"** 명시 | unit |
| E3-07 | E3-05 후 | Lead가 질문 카드에 답글 | 해소 규칙 1 → L2, `blocked → running`, `reentry_count`+1. 턴 프롬프트가 L2의 `runtime_session_ref`로 resume 시도 | unit + harness |
| E3-08 | Dir가 직접 만든 L(R,1) (`delegated_from_task_id` 없음) | R이 `status set blocked` | 깨울 위임자 없음 → **Dir 인박스** `lane_blocked` `action_required`. 에이전트 트리거 0 | unit |
| E3-09 | E3-08 후 | Dir가 카드에 답글 | 규칙 1로 L(R,1) 재진입 | unit |
| E3-10 | Lead가 두 번에 나눠 위임: J1(2개), J2(1개) | J1 둘 다 done, J2 running | **J1 합류 발화**, J2 대기. 합류는 그룹 단위 | unit |
| E3-11 | J1 이미 발화, L1 재진입 후 완료. 재진입 트리거 작성자 = QA | L1 `done` | 서버가 **QA를 멘션한 시스템 메시지** 게시 → T(QA). 새 합류 그룹 **생성 안 됨**. Lead 트리거 0 | unit |
| E3-12 | 〃, 재진입 트리거 작성자 = Dir | L1 `done` | Dir **인박스 알림**. 에이전트 트리거 0 | unit |
| E3-13 | 〃, 재진입 트리거 작성자 = Lead(위임자 본인) | L1 `done` | Lead 멘션 시스템 메시지 → T(Lead) 1개 (합류 발화 후라 규칙 8 억제 해제) | unit |
| E3-14 | 동시성: 세션 `max_parallel_lanes`=2, L1·L2 `running`, L3 `queued` | L1 `waiting_human` | L3 **dispatch됨** (waiting_human은 슬롯 미점유) | unit |
| E3-15 | 〃 | L1 `blocked` | L3 dispatch됨 | unit |
| E3-16 | R `max_concurrent_tasks`=1, L(R,1) `running` | L(R,2) `queued` | L(R,2) 대기. L(R,1) `done` 후 dispatch | unit |

---

## E4. 루프 상한 (FR-3.5) — G5

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E4-01 | Dir 메시지 → Lead → R → W → QA → Lead → R → W → QA (깊이 8) | QA가 `@Lead` 게시 (깊이 9) | 세션 **`paused`**, 사유 `loop(chain_depth)`. Dir에게 HITL 알림(`source: system`). 깊이 9 task **생성 안 됨** | unit |
| E4-02 | 깊이 7에서 | Dir가 메시지 게시 | 깊이 **0으로 리셋**. 이후 8단계 더 가능 | unit |
| E4-03 | Lead ↔ R이 서로 멘션 5왕복(10 트리거), 사이에 아무도 안 낌 | R이 `@Lead` (6번째) | 세션 `paused(loop:pair_roundtrips)`. task 생성 안 됨 | unit |
| E4-04 | Lead ↔ R 4왕복 | W가 `@Lead` 게시 (제3자 개입) | pair 카운터 **리셋**. 이후 Lead ↔ R 5왕복 다시 허용 | unit |
| E4-05 | Lead ↔ R 4왕복 | Dir가 메시지 (사람 개입) | pair 카운터 리셋 | unit |
| E4-06 | 1시간 창에 에이전트 간 트리거 60회 | 61번째 에이전트 간 트리거 | `paused(loop:hops_per_hour)`. **사람 메시지는 카운트 안 함** | unit |
| E4-07 | 〃, 다음 시간 창 | 재개 후 트리거 | 카운터 새 창 | unit |
| E4-08 | 내용 기반 억제 없음 확인 | R이 `@QA 리뷰 부탁해` (짧고 물음표·숫자 없음) | T(QA) **1개**. 억제 없음 | unit |
| E4-09 | 워크스페이스 설정 `max_pair_roundtrips`=2 | 3번째 왕복 | `paused`. 설정값이 적용됨 | unit |
| E4-10 | `paused(loop:pair_roundtrips)` 세션, 같은 시간 창에 hops 59회 누적 | Dir가 재개 | `chain_depth`·`pair_roundtrips` **0으로 리셋**(재개는 사람의 개입), `hops_per_hour`는 **59 유지**(시간 창이 지나야 준다). 다음 에이전트 간 트리거 2회째에 `paused(loop:hops_per_hour)` — 재개를 반복해 상한을 무한히 우회할 수 없다 | unit |

---

## E5. 상태 머신 (FR-2.3, FR-7.1, FR-6.2, FR-1.3) — G4

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E5-01 | task `waiting_human` | API로 `running` 전이 시도 | **거부**. 유일한 출구는 `queued` | unit |
| E5-02 | task `dispatched`, 데몬 claim 없음 | 5분 경과 | `failed(timeout)`. **재큐잉하지 않는다** — attempt를 늘리지 않고 task가 거기서 끝난다(PRD FR-7.1의 재시도 목록에 `timeout`이 없다). 그 attempt의 task token **폐기**(좀비 데몬의 뒤늦은 보고 차단) | unit + clock |
| E5-03 | task `running`, heartbeat 15초 | 3분 무응답 | 런타임 `offline`, task 재큐잉(`queued`, `attempt`+1), 토큰 폐기(§E11) | unit + clock |
| E5-04 | 세션 `paused`, `queued` task 3개 | — | dispatch **0**. 데몬 claim 요청에 빈 응답 | unit |
| E5-05 | E5-04 | Dir가 재개 | 3개가 **큐 순서대로** dispatch | unit |
| E5-06 | 세션 `active`, Dir가 일시정지, L1 `running` | — | L1의 턴 **계속 실행**(드레인). `turn_end` 후 새 dispatch 없음. 세션 `paused(director)` | harness |
| E5-07 | 세션 예산 초과로 `paused(budget)`, L1 `running` | — | L1 턴 **취소**(§8.2.2 절차). task `paused(budget)` | harness |
| E5-08 | 세션 `completing` | — | 요약 생성 중. 새 task dispatch 없음. 완료 시 `completed` + `session_summary` 메시지 1개 | unit |
| E5-09 | lane `done` | 재진입 | `done → running` 허용 | unit |
| E5-10 | lane `failed` | 재진입 시도(멘션) | 규칙 3은 `done`·`blocked`만 재진입. `failed`는 **새 lane** | unit |
| E5-11 | 에이전트 파생 상태: R에 `running` 1 + `waiting_human` 1 | 조회 | `working` (순서 4 > 5) | unit |
| E5-12 | R에 `waiting_human` 1, running 0 | 조회 | `waiting_human` | unit |
| E5-13 | R에 `blocked` lane만 | 조회 | **`idle`** (blocked는 파생 제외) | unit |
| E5-14 | R에 `paused(budget)` task만 | 조회 | `idle` | unit |
| E5-15 | R `respond_to: nobody`, running 1 | 조회 | `disabled` (순서 1) | unit |
| E5-16 | 세션 런타임 offline, R running 1 | 조회 | `offline` (순서 2) | unit |
| E5-17 | R의 마지막 task가 인증 오류로 실패 | 조회 | `error`. **재시도 없음**(FR-7.1) | unit |
| E5-18 | R의 마지막 task가 네트워크 오류로 실패, 재시도 중 | 조회 | `working` 또는 `idle` — `error` **아님** | unit |

---

## E6. 종료 조건 (FR-2.2, FR-2.4) — G5

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E6-01 | 조건 `artifact_submitted(W) AND user_approval` (기본값) | W가 `colab artifact submit` | `artifact_submitted` 충족. **플랫폼이** Dir에게 `approval` HITL 발행(`source: system`, `task_id` 비움). 세션 `active` 유지 | unit |
| E6-02 | E6-01 | R이 artifact submit (지정 에이전트 아님) | 조건 **미충족**. HITL 발행 없음 | unit |
| E6-03 | E6-01 후 | Dir 승인 | 세션 `active → completing → completed`. `session_summary` 1개. `container`/`none` workdir 즉시 삭제 | unit + e2e |
| E6-04 | E6-01 후 | Dir 거절(사유) | 세션 `active` 유지. `artifact_submitted` 플래그 **유지**. 거절 사유가 결정 기록에 저장. 에이전트 트리거 없음(사람이 다음 지시) | unit |
| E6-05 | 조건 `agent_approval(QA)` 단독 | QA가 `colab review approve` | 세션 `completing → completed`. **사람 게이트 없이** 종료. 세션 생성 시 "사람 승인 없이 완료됩니다" 표시됐음 | unit + manual |
| E6-06 | 〃 | R이 `colab review approve` | 미충족 (지정 에이전트 아님). CLI가 오류 반환 | unit |
| E6-07 | 조건 `criteria_met` 단독 | 세션 생성 시도 | **폼에서 거부** — 단독 사용 불가 | unit |
| E6-08 | 조건 `manual` | Dir가 종료 버튼 | `completing → completed` | unit |
| E6-09 | 조건 `artifact_submitted(W) OR agent_approval(QA)` | QA approve | 충족 → `completing`. W 제출 불필요 | unit |
| E6-10 | 세션 예산 소진 | — | **`paused(budget)`**, `completed` 아님. Dir에게 "계속?" HITL(`source: system`) | unit |
| E6-11 | 요약 생성 중 Platform LLM `stop_reason == "refusal"` | — | 활동 피드에 오류(`stop_details.category`). 세션 **`completed`**(요약 없이). `completing`에 머물지 않음 | unit |
| E6-12 | 요약 생성 중 Platform LLM **transport 오류**(5xx·타임아웃 — `stop_reason` 자체가 없음) | — | E6-11 과 같다: 피드 오류 + 세션 **`completed`**(요약 없이). `refusal` 문자열만 분기한 구현은 세션을 `completing` 에 영구히 가둔다(**v0.10**, P4a 골든 제안) | unit |

---

## E7. HITL (FR-5.1, FR-5.2, FR-5.4) — G6

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E7-01 | W `running` | W가 `colab hitl ask --question "독자?" --default "투자자"` | CLI 반환: "등록됨, 턴을 끝내라". task에 `pending_hitl` 플래그. 상태 **아직 `running`** | harness |
| E7-02 | E7-01 후 W가 지시 무시하고 메시지 2개 더 게시 | — | 메시지 2개 **저장됨**(이미 일어난 일). 상태 여전히 `running` | harness |
| E7-03 | E7-01 후 | `turn_end` 도착 | task `waiting_human`. 프로세스 종료. HITL 카드 타임라인 게시. Dir 인박스 `action_required`. 동시성 슬롯 해제. workdir 보존 | harness |
| E7-04 | E7-01 후 같은 턴 | W가 두 번째 `hitl ask` | CLI **거부**("이미 대기 중"). 활동 피드에 기록. 첫 요청 유지 | harness |
| E7-05 | `question` 타입, `--default` 없음 | CLI 호출 | **거부** — question·choice에 default 필수 | unit |
| E7-06 | `approval` 타입 | `colab hitl approve-request --summary` (default 없음) | 정상 등록 | unit |
| E7-07 | HITL open | Dir 응답 "경영진" | `answered`. 결정 기록 1건. task `queued`(재큐잉) → dispatch → 새 attempt. 응답이 턴 프롬프트에 "질문/답변" 형식으로 포함 | unit + harness |
| E7-08 | HITL open | Dir 두 번째 응답 | **무시**(오류 아님). 첫 응답 유지 | unit |
| E7-09 | HITL open, `approver_spec: director`, 발행 후 11h | deputy 응답 시도 | **거부**. UI 버튼 비활성 + "HH:MM부터" 표시 | unit + manual |
| E7-10 | 〃, 발행 후 12h 1분 | deputy 응답 | **수락**. deputy에게 위임 알림이 12h 시점에 발송됐음 | unit + clock |
| E7-11 | HITL open, 일반 멤버 M2 | M2 응답 시도 | 거부. 카드는 보이되 버튼 비활성 | unit |
| E7-12 | `question`, `autonomy: autonomous`, 24h 경과 | — | `auto_answered`, `proposed_default`로 재큐잉. 결정 기록에 "자동" 표시 | unit + clock |
| E7-13 | `question`, `autonomy: guided`, 24h 경과 | — | `open` 유지 + `overdue` 플래그. 인박스 맨 위 | unit + clock |
| E7-14 | `approval`, `autonomy: autonomous`, 24h 경과 | — | **`open` + `overdue`**. 자동 승인 **없음**, 자동 거절 **없음** | unit + clock |
| E7-15 | `approval` `overdue` 상태 | Dir 응답 | 수락됨(늦어도 답 가능) | unit |
| E7-16 | `approver_spec: "role:reviewer"` | 등록 시도 | **저장 시점 거부**(fail closed) | unit |
| E7-17 | `approval` 거절 | 재개 | 턴 프롬프트에 `approved: false` + 사유. task `queued` → 실행. `failed` **아님** | harness |
| E7-18 | W `waiting_human`, R `running` 다른 lane | — | R 계속 진행. 세션 `active` | e2e |
| E7-19 | Dir가 답해야 할 질문을 R이 `status set blocked`로 올림 | — | 위임자 Lead가 깨어남(잘못된 경로지만 규칙대로). 브리프에 구분 지시 존재 — 프롬프트 검증은 `manual` | unit |
| E7-20 | `question`, `--default` 있음, task당 열린 HITL 0 | `choice` 타입으로 default 없이 등록 | **거부**(422) — FR-5.1 은 question·choice 둘 다 default 필수. E7-05 가 question 만 적어 한쪽만 구현해도 통과하던 구멍 | unit |
| E7-21 | `info` 타입, `autonomy: autonomous`, 24h 경과 | — | `open` + `overdue`. 자동 진행 **없음**(FR-5.4 표는 `approval · info` 를 한 줄로 묶는다) | unit + clock |

---

## E8. 재개·재시도·중복 (FR-5.4, FR-7.1, §8.4) — G6

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E8-01 | L(W,1) `runtime_session_ref` 있음(Claude Code), HITL 답변 | 재개 | 데몬이 **resume 먼저**. 성공 시 이전 대화 컨텍스트 유지(스파이크 4a 기준 ≥ 90%). 프롬프트에 `<resumed>` 구간 | harness + e2e |
| E8-02 | resume이 런타임에서 거부됨 | 재개 | 데몬 `resume_rejected` 보고 → **콜드 스타트**: 브리프 + 히스토리 + 결정 기록. 프롬프트에 "이전 시도가 중단됨, workdir 현재 상태를 먼저 확인하라" | harness |
| E8-03 | Hermes, `state.db`에 세션 없음 | resume | Hermes는 오류 없이 새 세션 생성 → 데몬이 **provenance 불일치**로 유실 감지 → 콜드 스타트로 전환. 유실 감지 100% | harness |
| E8-04 | task attempt 1이 메시지 `m1`·`m2` 게시 + 파일 2개 편집 후 데몬 kill | 재큐잉 → attempt 2 | (1) 같은 workdir (2) 프롬프트에 **이미 게시한 메시지 [m1, m2]** 목록 (3) 에이전트가 같은 내용 재게시 → `task_id + seq` 멱등키로 **중복 0** (4) 파일 편집 중복 적용 0 (프롬프트 지시 + 검증) | sim |
| E8-05 | sim 100회 반복 | — | 중복 메시지 **0건**, 중복 편집 **0건**. §11 "< 1%"를 CI에서 0으로 | sim |
| E8-05a | 데몬(또는 CLI)이 같은 `task_id + seq` 를 **재전송**(네트워크 타임아웃 재시도) | 같은 Idempotency-Key 로 두 번째 POST | `Idempotent-Replayed` 응답, 새 메시지 행 **0** — 멱등키가 막는 유일한 경우(colab-cli §1). 재개 후 재게시는 여기 해당 없음(posted_message_ids 가 1차) | sim |
| E8-06 | "중단하고 다시 지시" | 새 메시지 전송 | **새 task**, `attempt` 1, `restarted_from_task_id` = 이전 task. 프롬프트에 `<resumed>` **없음**, 새 지시만. lane `running` 유지 | unit + harness |
| E8-07 | 재시도(네트워크 오류) | — | **같은 task**, `attempt` 2, 같은 `trigger_message_id` | unit |
| E8-08 | 재시도 중 같은 머신에 대체 프로파일(Claude Code) 있음 | Hermes 프로파일 실패 | Claude Code로 전환. **workdir 유지**, 아티팩트 디렉토리 유지. `runtime_session_ref`는 새로(런타임이 달라짐) | e2e |
| E8-09 | 같은 머신에 대안 없음 | 프로파일 실패 | task `queued` 대기 + Dir 알림. **다른 머신으로 넘기지 않음** | unit |
| E8-10 | 인증 오류로 실패 | — | 재시도 **0회**. `failed`. 에이전트 `error` | unit |
| E8-11 | `preparing` 재개 | — | 기존 workdir **재사용**. 새 워크트리 생성 0 | harness |
| E8-12 | 히스토리 200개, 프롬프트 상한 50개 | 턴 프롬프트 구성 | 히스토리 구간에 `included: 50 / total: 200 / truncated: true` 명시 | unit |
| E8-13 | attempt가 `runtime_session_ref` **non-nil**로 finish (G3 S-1) | finish | 200, `lane.runtime_session_ref`에 계약 키(`runtime_kind`·`session_id`) 저장, 다음 attempt의 TaskBundle.resume에 같은 값 | unit + e2e |

---

## E9. 예산 (FR-7.3) — G6

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E9-01 | R `budget_per_task` $1, 턴 중 `usage_update` 누적 $1.01 | — | 데몬이 §8.2.2 취소 절차. task **`paused(budget)`**, `failed` 아님. lane `paused`. Dir에게 HITL(`source: system`, **`task_id` 채움**) | harness | **v0.9 노트**: 실측(`estimated:false`) 분기는 harness v0.8.5(#145) 로 claude_code 가 `result.total_cost_usd` 를 실어 실기 도달(G6 2판 50_ arm A); hermes 는 추정 분기(E9-05)만.
| E9-02 | E9-01 | Dir가 $3으로 상향 승인 | `task.budget_override` = 3. R의 `budget_per_task` **여전히 $1**. task `queued` → 같은 lane·workdir로 재개(resume 우선). 새 트리거 불필요 | unit + harness |
| E9-03 | E9-01 | Dir 거절 | task `failed(budget)`? — **아니다**: `paused(budget)` 유지, Dir가 "중단" 버튼으로 명시 종료해야 `cancelled` | unit |
| E9-04 | 세션 잔여 예산 $0.5, 턴 중 $0.6 도달 | — | 세션 `paused(budget)`, 진행 중 턴 **취소**. `queued` dispatch 0 | harness |
| E9-05 | 런타임 `usage=false`(추정), 추정치 100% 도달 | — | **하드 컷 없음**. 세션 `paused` + Dir 알림. 턴은 계속(드레인) | harness |
| E9-06 | CLI 폴백 경로, Claude Code | 실행 | `--max-budget-usd` 함께 전달(이중 강제) | harness |
| E9-07 | 비용 표시 | 조회 | task/agent/세션/런타임 4단위 집계. 추정치에는 "추정" 배지 | unit |
| E9-08 | `task.budget_override` = $3 승인 뒤, 턴 중 누적 $1.50 | — | 취소 **없음**, task `running` 유지 — override 를 저장만 하고 강제 시점에 읽지 않으면 재개 즉시 다시 `paused(budget)` 가 된다 | harness |
| E9-09 | 세션 비용 조회, 전 행 실측(`estimated:false`) | 조회 | `estimated: false` — 항상 true 를 돌려주는 구현을 잡는다 | unit |
| E9-10 | 턴이 끝난 뒤(finish 롤업) 초과를 발견 — 데몬이 턴 중 usage 를 못 준 경우 | finish | completed task 는 그대로. 세션 잔여 초과 → 세션 `paused(budget)` + 시스템 HITL(`task_id` 비움), task 상한만 초과 → 그 lane `paused(budget)` + 시스템 HITL(`task_id` = 초과한 task) → 다음 task dispatch 0. 승인 시 lane 재개 + `budget_override` 승계, 거절 시 유지. `completed → paused` 전이는 없다(E5) | unit + e2e | **v0.9 노트**: 세션 범위 **사후 실측** 분기는 오늘의 런타임 조합으로 실기 도달 불가(claude_code 는 finish 전 heartbeat 로 실측을 먼저 보내 턴 중 강제가 먼저 잡는다; G6 2판 §9.4) — 서버 유닛이 지킨다. task 범위 사후 분기는 실기 도달.

---

## E10. 취소·킬 스위치 (FR-3.4, FR-1.9, §8.2.2) — G6

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E10-01 | L1 `running`, 마지막 이벤트 = 파일 편집 시작(완료 이벤트 없음) | Dir "중단" | 취소 **보류**. 편집 완료 이벤트 후 취소. 최대 30초 | harness |
| E10-02 | 〃, 30초 넘게 완료 안 옴 | — | 그대로 취소. 활동 피드에 "30초 초과로 강제 취소" 기록 | harness |
| E10-03 | L1 `running`, 권한 요청 대기 중 | 취소 | 순서: 권한 요청 응답 → `session/cancel` → 드레인. 프로세스 **즉시 kill 아님**. 프로세스 트리 잔존 0 | harness |
| E10-04 | Dir "중단" | — | lane **`failed(cancelled)`**. 활동 피드 "사람이 중단함". 새 task 없음 | unit |
| E10-05 | 일반 멤버 M2 | 취소 버튼 | **비활성**. API 호출 시 403 | unit |
| E10-06 | deputy, 발행 직후(기한 절반 전) | 취소 버튼 | **활성**, 즉시 동작 (승인과 달리 시점 제한 없음) | unit |
| E10-07 | R `respond_to → nobody`, R에 running 1 · queued 2 · waiting_human 1(HITL open) | — | running **취소**(피드 "소유자가 정지시킴"), queued 2개 **`cancelled`**, HITL **`open` 유지**, workdir 보존. R 상태 `disabled` | unit + harness |
| E10-08 | E10-07 후 | Dir가 HITL 답변 | `answered` 기록. **재큐잉 보류**. R을 `owner`로 되돌리면 그때 `queued` | unit |
| E10-09 | R `nobody` | 새 세션에 R 초대 시도 | 거부 | unit |
| E10-10 | 세션 내 상호 트리거 | R(`respond_to: owner`, 소유자 M1)이 M2가 만든 세션에 참여 중 | Dir·다른 참여자가 R 멘션 → **정상 트리거**(세션 참여 = 허용) | unit |
| E10-11 | 세션 밖 | M2가 R을 자기 세션에 초대 | 거부(owner만 초대 가능) | unit |
| E10-12 | 권한 originator | M2(권한 낮음)가 시작한 체인에서 Lead가 R 멘션 | 판정은 **M2 기준**. 에이전트 경유로 권한 상승 없음 | unit |
| E10-13 | 데몬 프로세스 SIGTERM(정상 종료) 중 `running` attempt (G3 D-1) | 종료 | harness §5 순서(권한 응답 → `session/cancel` → 드레인)로 취소, finish `outcome=cancelled` — **재큐잉 아님**, 프로세스 트리 0 | harness + e2e |
| E10-14 | Dir 가 취소, 마지막 이벤트가 **셸 명령** 시작(완료 없음) | 중단 | 편집과 같은 30초 보류(FR-3.4·harness §5 "파일 편집 또는 셸 명령") | harness |

---

## E11. 데몬·고아·토큰 (FR-9.1, §8.2.2) — G3

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E11-01 | 데몬이 런타임 프로세스 spawn | — | pgid + task_id가 디스크 파일에 기록됨 | unit |
| E11-02 | 정상 종료 | — | 기록 삭제 | unit |
| E11-03 | task `running`, 데몬 `kill -9`, 런타임 프로세스 생존 | 3분 경과 | 서버: heartbeat 만료 → task `queued`(attempt 2), **`COLAB_TASK_TOKEN` 폐기** | unit + clock |
| E11-04 | E11-03, 고아가 `colab message post` 시도 | — | 서버 **401/403 거부**. 메시지 저장 0 | e2e |
| E11-05 | E11-03, 데몬 재시작 | — | claim **전에** 기록된 pgid 확인 → 고아 프로세스 그룹 종료 → 그 다음 claim. 같은 workdir에 프로세스 2개 동시 존재 시간 = 0 | e2e |
| E11-06 | E11-05 후 재개 | — | 고아가 남긴 workdir 변경 **삭제 안 됨**. 프롬프트 "현재 상태 먼저 확인" | e2e |
| E11-07 | 취소 정상 경로 | 취소 | 프로세스 그룹 전체 종료. `ps` 상 자식 프로세스 0 | e2e |
| E11-08 | 데몬 페어링 | `probe()` | CLI 목록·버전·모델, remote URL·브랜치·클린 여부(worktree용)가 서버 DB에 저장. S11에 표시 | e2e |
| E11-09 | 재큐잉된 task를 다른 데몬이 claim 시도 | — | 세션 `runtime_id` 고정 → **거부**. 같은 런타임만 claim | unit |
| E11-10 | `none` 격리, `runtime_id` 비움 | 첫 task dispatch | 선택된 머신으로 **고정**. 이후 모든 lane 같은 머신 | unit |
| E11-11 | 워크스페이스 A의 none 세션(`runtime_id` NULL), 워크스페이스 B의 런타임이 claim (G3 S-3, **구현됨 PR #28**) | claim | **주지 않음**. 고정은 같은 워크스페이스 런타임만. 다른 워크스페이스 runtime_id로 세션 생성 → 422 | unit |
| E11-12 | 같은 호스트명의 데몬을 같은 워크스페이스에 재페어링 (G3 S-4) | pair | 201, 이름 접미어(`-2`) — 500 아님 | unit |

---

## E12. 하네스 (§8.2, §8.2.5, FR-1.6) — G3 · G5

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E12-01 | ACP `session/request_permission`, 옵션에 `kind: allow_once` 존재 | — | 데몬이 그 optionId로 응답. **optionId 하드코딩 없음**(옵션 값이 달라도 동작) | harness |
| E12-02 | 옵션에 `allow_once` 없음 | — | `reject_once` 응답. 활동 피드에 "거부" 기록. 턴 계속 | harness |
| E12-03 | 같은 런타임에서 `allow_once` 부재 반복(≥ 3회) | — | 해당 런타임 CLI 경로 전환 권고 이벤트 | harness |
| E12-04 | Hermes, 마지막 청크가 응답 뒤에 도착 | `turn_end` 판정 | 250ms 정적 대기 후 종료 판정. 청크 유실 0 | harness |
| E12-05 | Hermes, LLM 오류에도 `end_turn` 보고 | — | `stopReason=="refusal" && 턴 활동 0` → 실패로 판정, 재시도 경로. stderr 프로바이더 오류 스니핑 기록 | harness |
| E12-06 | 능력 광고: 런타임이 `usage=false` | — | 비용 "추정" 배지. 하드 컷 없음(E9-05) | unit |
| E12-07 | `session/update` 이벤트 | — | `task_event`로 정규화: class·verb·object_ref·outcome 4필드 전부 채워짐. `seq` 단조 증가 | harness |
| E12-08 | `initialize` 프로토콜 버전 불일치 | — | 실패를 `error`(설정)로 분류, 재시도 없음 | harness |
| E12-09 | 브리프 전달: 스파이크 3 결과가 "시스템 프롬프트 필드 있음" | 실행 | 경로 **하나만** 사용. 지시 파일과 중복 주입 0 | harness |
| E12-10 | 턴 프롬프트 | — | 트리거 메시지 인용 + 위임 브리프 + 히스토리 N + "`colab message post`로 게시하라" 지시. 그 외 없음 | unit |
| E12-11 | 브리프 파일 구성 | — | [1]~[8] 순서 고정. 같은 세션의 두 턴 사이에 [1]~[5] 바이트 동일(캐시 친화) | unit |

---

## E13. worktree·브리프 오염·GC (FR-6.4, §8.4) — G7

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E13-01 | 격리 `worktree`, 저장소 `~/dev/app` | 세션 생성 | 데몬이 `repo_path` 존재·클린·기본 브랜치 검증. 실패 시 **폼에서 차단** | e2e |
| E13-02 | 〃 | R의 첫 task | `git worktree add`, 브랜치 `colab/<S>/R`. 에이전트당 1개 | e2e |
| E13-03 | 저장소가 `AGENTS.md`를 **추적 중**, hermes(`instruction_file`) | lane 시작 | `AGENTS.md` **바이트 무변경**(읽지도 쓰지도 않음). 브리프는 `<workdir>/COLAB_BRIEF.md`(마커 구간)에. `.git/info/exclude` 등록. `skip-worktree` 비트 **0**. `git status` **진짜 클린**(v0.10 정정 — 스파이크 5) | e2e |
| E13-04 | 저장소에 `AGENTS.md` 없음 | lane 시작 | E13-03 과 같다 — 원본 상태와 무관하게 같은 경로·같은 숨기기. `AGENTS.md` 는 생성하지 않는다 | e2e |
| E13-05 | E13-03, 에이전트가 작업 중 `AGENTS.md`를 정당하게 수정하고 **커밋** | lane 종료 | 커밋이 **평범하게 성공**(`1 file changed`). 커밋에 브리프 **섞이지 않음**. lane 종료 뒤 `AGENTS.md` = 에이전트 커밋본, `COLAB_BRIEF.md` 없음 | e2e |
| E13-06 | E13-03 | lane 종료 | `COLAB_BRIEF.md` 삭제, exclude 등록 해제. `git status` 클린 | e2e |
| E13-06a | hermes 턴 프롬프트 | — | 맨 앞 한 줄이 `<workdir 절대 경로>/COLAB_BRIEF.md` 를 읽으라는 지시. 에이전트의 첫 도구 호출이 그 파일 read(실측 4/4) | unit + e2e |
| E13-07 | `.gitignore` | — | 데몬이 **건드리지 않음**(diff 0) | e2e |
| E13-08 | QA가 Frontend diff 리뷰 | — | QA workdir에서 Frontend 워크트리 경로 접근 **불가**(노출 안 됨). 리뷰 대상은 아티팩트만 | e2e |
| E13-09 | 세션 `completed`, `worktree`, 14일 미경과 | GC 실행 | 삭제 **0** | unit + clock |
| E13-10 | 14일 경과, 브랜치 병합됨 + 트리 클린 | GC | 워크트리 **삭제**, 브랜치 **보존** | e2e + clock |
| E13-11 | 14일 경과, 커밋 0 + 트리 클린 | GC | 삭제 | e2e + clock |
| E13-12 | 14일 경과, **미병합 커밋** 있음 | GC | 삭제 **안 함** + Dir 알림 | e2e + clock |
| E13-13 | 14일 경과, 커밋 0, **미커밋 변경** 있음 (diff만 제출한 시나리오 B) | GC | 삭제 **안 함** + Dir 알림 | e2e + clock |
| E13-14 | `container`/`none`, 세션 `completed` | — | workdir **즉시 삭제**. 아티팩트는 이미 서버에 | e2e |
| E13-15 | 세션 `cancelled` | — | 〃 | e2e |
| E13-16 | 디스크 사용량 ≥ `workdir_disk_quota_gb` | 새 세션 생성 시도 | **차단** + Dir에게 정리 요청 | unit |
| E13-17 | 격리 `worktree` | 마법사 | 런타임 후보가 **저장소 있는 머신만** 필터됨 | manual |
| E13-18 | 세션 **`active`**(또는 `paused`) 인 workdir, 생성 후 14일 경과 | GC | 삭제 **0** — 보존 기한은 세션 종료 시각 기준. `created_at` 기준 구현은 실행 중 체크아웃을 지운다(**v0.10**, P4a 골든 제안) | unit + clock |
| E13-19 | `workdir_disk_quota_gb` **미설정(null)** | 새 세션 생성 | 차단 **안 함**(무제한). null 을 0 으로 읽으면 쿼터를 안 켠 워크스페이스가 세션을 못 만든다(**v0.10**) | unit |

---

## E14. 런타임 오프라인·재바인딩 (FR-9.2) — G7

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E14-01 | 런타임 offline 6일 23h | — | 세션 `active`(queued 대기). 알림 없음 | unit + clock |
| E14-02 | 7일 경과 | — | 세션 **`paused(runtime_offline)`** + Dir 알림. 선택지: 재바인딩 / 종료 | unit + clock |
| E14-03 | `isolation: none` | Dir 재바인딩 → 온라인 런타임 B | 세션 `runtime_id` = B. `queued` task가 B에서 dispatch. 대화·아티팩트·결정 기록 온전 | e2e |
| E14-04 | `isolation: worktree`, 원 저장소 remote `git@x:app.git`, B의 `repo_path`는 다르지만 remote 같음 | 재바인딩 후보 조회 | B **후보에 포함**(remote URL 판정) | unit |
| E14-05 | B의 remote가 다름, `repo_path` 문자열은 같음 | 〃 | B **후보 제외** | unit |
| E14-06 | `worktree` 재바인딩 완료 | 첫 task 프롬프트 | **"이 세션의 diff 아티팩트를 제출 순서대로 새 workdir에 적용한 뒤 이어가라"** 포함. 아티팩트 목록 순서 = 제출 순서. 콜드 스타트 | e2e |
| E14-07 | Dir 종료 선택 | — | 세션 `cancelled`. 아티팩트 회수 | unit |
| E14-08 | 활성 세션이 걸린 런타임 | 삭제 시도 | **차단** + "먼저 재바인딩/종료" 요구 | unit |
| E14-09 | 런타임 복귀(7일 안) | — | 세션 그대로 `active`, queued 진행 | e2e |
| E14-10 | 이미 `paused(runtime_offline)` 인 세션 | 오프라인 스윕 다음 tick | 알림 **0**(멱등). "offline > grace" 만 보는 구현은 tick 마다 같은 인박스 항목을 쌓는다(**v0.10**) | unit + clock |

---

## E15. 권한·테스트 채팅·동적 생성·기타 (FR-1.5, FR-1.8.1, FR-2.1, FR-3.6, FR-4.4) — G5

| ID | 전제 | 자극 | 예상 | 검증 |
|---|---|---|---|---|
| E15-01 | 에이전트 툴셋 | 조회 | 에이전트 생성 툴 **없음** | unit |
| E15-02 | Lead가 `colab lane delegate --agent X`(X 비참여) | — | CLI 거부. 대안 안내: `hitl ask`로 Dir에게 참여자 추가 요청 | unit |
| E15-03 | 테스트 채팅 시작 | — | `session` 레코드 **0개**. `test_chat` 채널 생성. 임시 workdir. `COLAB_TASK_TOKEN` **미발급** | unit |
| E15-04 | 테스트 채팅 중 에이전트가 `colab message post` 시도 | — | 토큰 없음 → 실패. 순수 응답만 | e2e |
| E15-05 | 테스트 채팅 종료 | — | 임시 workdir 삭제. 워크스페이스 비용에만 합산. 세션 목록 변화 0 | unit |
| E15-06 | 트리거 미리보기 | Dir가 `@R @W` 입력 | "R, W를 트리거합니다 (프로파일: …)" 표시. R의 ×로 억제 → 전송 시 `suppress_agent_ids=[R]` → T(W)만 | unit + manual |
| E15-07 | Director 교체 | 현재 Dir가 M2로 변경 | 허용. 시스템 메시지 + `activity_log` 기록. 열린 HITL의 approver가 M2로 | unit |
| E15-08 | 〃 | 일반 멤버 M3가 변경 시도 | 거부 (현재 Dir·owner·admin만) | unit |
| E15-09 | 컨텍스트 재사용, `max_summary_tokens` 2000, 이전 세션 요약 5000 토큰 | 새 세션 생성 | 주입된 요약 ≤ 2000 토큰. 아티팩트는 링크만 | unit |
| E15-10 | 정의 갱신(템플릿 v3→v5), R 인스턴스 `definition_version` 3, running task 있음 | — | running task **영향 없음**. R에 "v5 있음" 표시. 사용자가 명시 적용해야 갱신 | unit |
| E15-11 | 세션 생성 `runtime_id` 비움, 격리 `worktree` | 제출 | **폼 거부**(worktree·container는 필수) | unit |
| E15-12 | 팀 템플릿에서 시나리오 A 팀 생성 | Dir 클릭 | 에이전트 3개 생성, 프로파일이 감지된 런타임에 자동 매핑. **3분 이내** | manual |

---

## E16. E2E 시나리오 (PRD §3) — 게이트 판정

각 시나리오는 위 단위 항목의 조합이다. 실제 런타임으로 야간 실행(비용 상한).

### E16-A 시장 조사 (격리 `none`) — G4(단일 런타임) · G5(Hermes 포함)

| 단계 | 자극 | 정확한 예상 |
|---|---|---|
| 1 | 세션 생성: Lead(assignee)·R(Hermes 기본/Claude Code 대체)·W, `none`, 조건 `artifact_submitted(W) AND user_approval` | 세션 `active`. T(Lead) 1개 자동(assignee 초기 트리거) |
| 2 | Lead 실행 | Lead가 계획 메시지 1개 + `lane delegate` 3회 → L(R,1..3), 그룹 J1. workdir 3개 |
| 3 | R 3개 lane 병렬 | 동시 실행 3. 각각 스레드에 결과 게시. **Lead 트리거 0** |
| 4 | 3개 모두 `done` | 합류 **정확히 1회** → T(Lead). Lead 깨어난 횟수 누계 = 2(초기 + 합류) |
| 5 | Lead가 `@W 초안` | T(W) 1개, L(W,1) |
| 6 | W가 `hitl ask --default "투자자"` + 턴 종료 | task `waiting_human`, 인박스 1건, 프로세스 0 |
| 7 | Dir "투자자" 선택 | 새 attempt, resume 시도, W가 `artifact submit` |
| 8 | 조건 충족 | 플랫폼이 `approval` HITL → Dir 승인 → `completed` + 요약 1개 |
| 판정 | Lead 트리거 총 **3회 이하**(초기·합류·W 완료 통보는 Dir 작성이 아니므로 W→Lead 멘션이 있을 때만), 합류 1회, 중복 메시지 0, workdir 3개 삭제됨 |

### E16-B 기능 구현 (격리 `worktree`) — G7

| 단계 | 자극 | 정확한 예상 |
|---|---|---|
| 1 | 세션: PM·Backend·Frontend·QA, `worktree ~/dev/app`, 조건 `agent_approval(QA)`, 예산 $20 | "사람 승인 없이 완료됩니다" 표시. 저장소 검증 통과 |
| 2 | PM 스펙 → `@Backend @Frontend` 위임 | lane 2개, 워크트리 2개(`colab/S/backend`, `colab/S/frontend`), `CLAUDE.md` 오염 0 |
| 3 | 각자 `artifact submit --type diff` | 아티팩트 2개. PM 합류 1회 |
| 4 | PM이 `@QA 리뷰` | T(QA). QA는 아티팩트만 접근 |
| 5 | QA가 Frontend diff 스레드에 수정 요청 | 해소 규칙 1 → **L(Frontend,1) 재진입** `done → running`, 같은 워크트리, resume. 새 lane 0 |
| 6 | Frontend 새 diff 제출, `done` | 서버가 **QA 멘션** 시스템 메시지 → T(QA). PM 트리거 0 |
| 7 | QA `review approve` | `completing → completed`. 사람 승인 없음 |
| 8 | 종료 후 | 워크트리 보존(14일). `git status` 클린. 미커밋 변경 있으면 GC 미삭제 |
| 판정 | 워크트리 2개(4개 아님), 재진입 lane 재사용, QA 통보 서버 발, 예산 초과 시 `paused` |

### E16-C Director 개입 — G6

| 자극 | 정확한 예상 |
|---|---|
| R `running` 중 Dir가 `@R 한국 시장으로 좁혀줘` | 턴 **계속**. task `queued`(L(R,1)). 프로세스 kill 0 |
| Dir "중단하고 다시 지시" → 전송 | 턴 취소(§8.2.2), **새 task** `restarted_from_task_id`, 프롬프트 새 지시만 |
| Dir "중단" | lane `failed(cancelled)`, 피드 "사람이 중단함" |

### E16-D 프로파일 전환 — G5

| 자극 | 정확한 예상 |
|---|---|
| R의 Hermes 실패(네트워크) | 같은 머신 Claude Code로 재시도. workdir·아티팩트 유지 |
| 대안 없음 | `queued` + Dir 알림. 다른 머신 이동 0 |

---

## E17. 성능·지표 (§9, §11) — G3 · G9

| ID | 조건 | 예상 | 검증 |
|---|---|---|---|
| E17-01 | 멘션 게시 → 데몬 claim | ≤ 2초 | e2e |
| E17-02 | 첫 출력 (`none`, 단일 task), **E2E 20회 반복** | 중앙값 ≤ 10초 | e2e |
| E17-03 | 동시 task 50 (부하) | 큐 정체 없음, heartbeat 누락 0 | e2e (P5) |
| E17-04 | 재개 후 중복률 (sim) | 0% | sim |
| E17-05 | resume 성공률 (Claude Code) | ≥ 90% | e2e |
| E17-06 | `blocked` 질문 → 위임자 깨어남 | ≤ 5분(실제로는 즉시) | e2e |
| E17-07 | F1: 설치 → 첫 세션 완료, 신규 사용자 5명 | 중앙값 < 15분 | manual |
| E17-08 | 대시보드 | §11 지표 10개 전부 표시 | manual |
| E17-09 | 웹 S12 패널, 실서버, `daemon pair` 실행 (G3 W-1·W-2) | 페어링 | 페어링 발급 **1건**(StrictMode 이중 effect에도), 패널이 10초 안에 `준비 완료` | e2e(agent-browser) |

---

## 부록. 게이트 ↔ 절 매핑

| 게이트 | 통과 조건이 되는 절 |
|---|---|
| G3 | E11 전부, E12-01·02·07·10·11, E17-01·02 |
| G4 | E1, E2, E5 전부 (골든 테스트), E16-A 단일 런타임 |
| G5 | E3, E4, E6, E15 전부, E12 나머지, E16-A Hermes, E16-D |
| G6 | E7, E8, E9, E10 전부, E16-C |
| G7 | E13, E14 전부, E16-B |
| G9 | E17 전부, E16 4종 CI 초록 |

**이 문서를 바꾸는 규칙**: PRD가 바뀌면 해당 행을 바꾼다. 테스트가 통과하지 않는다고 행을 바꾸지 않는다 — 그것은 PRD 결함이거나 구현 결함이고, 둘 중 어느 쪽인지 Director가 판정한다(PLAN §10.3 "테스트 파일은 구현 PR에서 못 건드린다"의 문서판).
