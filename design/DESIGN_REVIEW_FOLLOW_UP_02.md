# 디자인 리뷰 #02 후속 조치

| 항목 | 내용 |
|---|---|
| 대상 리뷰 | `DESIGN_REVIEW_02.md` (2026-09-04) |
| 수정 대상 | `agent-collaboration.pen` → 와이어프레임 **v0.3** |
| 함께 수정 | `PRD.md` v0.9 → **v0.10**, `SCREEN.md` v0.2 → **v0.3** |
| 작성일 | 2026-09-04 |
| 결론 | 리뷰가 지적한 결함 6건(N1~N6)·PRD 공백 4건(C1′~C4′)·소소한 것 10건(m1~m10)을 **전건 반영**했다. 반대한 항목은 없고, C1′에 `error` 정의를 덧붙였다(§3). N6의 lane 보드 접기는 리뷰 분류대로 다음 라운드에 둔다. **캔버스 변경은 앱 메모리에만 있으므로 ⌘S가 필요하다**(§6). |

---

## 1. 검증 — 리뷰 주장 대조

리뷰 #02의 지적을 반영 전에 캔버스·문서에서 하나씩 확인했다. **전부 사실이었다.**

| 리뷰 | 확인 방법 | 결과 |
|---|---|---|
| N1 task 이력 트리거 열 11~30px | `ctx.bounds` | 12·30·49·11px — 정확 |
| N2 칩 279·304px > 268 | `ctx.bounds` + `problems` | @Frontend 279, @QA 304, partially clipped |
| N3 S7-P 잔재 3건 | 노드 값 읽기 | Backend `$s-run`/working, 칩 "실행 중 → 큐잉", Lead 답글에 HITL 메타 — **셋 다 존재**. 마지막은 `F("Meta",5)`가 엉뚱한 노드를 잡은 내 스크립트 버그 |
| N4 심각도 글리프 !/! | 인스턴스 descendants | action_required·attention 모두 "!" |
| N5 idle = `$s-done` | 인스턴스 descendants | S7 칩·S9 배지 전부 초록. S10 "기본"도 ● 초록 |
| N6 좌열 넘침·각주 클립 | `ctx.bounds` | Lane Board 1073px, Legend fully clipped |
| C1′ FR-1.3 vs FR-5.2 | grep | 286행 "집계하지 않는다" vs 556행 "에이전트 상태도 waiting_human" — **진짜 모순** |
| C2′·C3′ PRD 부재 | grep | `budget_override`·paused 중 dispatch 규칙 모두 없음 |
| C4′ 순서 | grep | PRD §6·SCREEN §4.4 모두 런타임 → 격리 |
| m3·m5·m7·m10 | 노드 값·grep | 전부 사실 |

---

## 2. 반영 내역

### 2.1 PRD v0.10

| 리뷰 | 조치 |
|---|---|
| **C1′** | FR-1.3을 **task 상태에서 파생되는 계산값**으로 재정의. 우선순위 표: `disabled` > `offline` > `error` > `working`(running task 있음) > `waiting_human`(waiting_human task 있음) > `idle`. `blocked`·`paused` lane은 파생에서 제외. FR-5.2의 "에이전트 상태도 waiting_human"을 "파생 규칙에 따라 계산된다"로 정합. 리뷰 권고 (a) 채택 |
| C1′ 추가 | **`error`를 "실행 자체가 불가능한 오류"(인증·쿼터·설정 — FR-7.1이 재시도하지 않는 분류)로 좁혔다.** 리뷰 파생 규칙에는 `error` 조건이 없었고, task 실패를 `error`로 올리면 "다시 지시"까지 끈적하게 남는다 — §3 |
| **C2′** | FR-7.3에 "task 예산 승인은 그 task에만 적용, `task.budget_override`에 저장, 에이전트 `budget_per_task`는 불변" 추가. 스키마에 `budget_override` 필드 |
| **C3′** | FR-2.3에 "세션 `paused` 동안 `queued` task는 dispatch되지 않고 재개 시 순서대로" 추가 |
| **C4′** | §6 마법사 순서를 **goal → Director → 격리 → 런타임 → 참여자 → 종료 조건 → 한도**로 변경 + 근거 |
| **m10** | v0.10 변경 요약 표 추가. 리뷰 #01 반영분(FR-1.3 문장)의 요약 누락도 C1′에 흡수시켜 기록 |

### 2.2 SCREEN v0.3

| 리뷰 | 조치 |
|---|---|
| C4′ | §4.4 순서 문장·표(3·4·5단계 재배치) + "v0.3에서 바꿨다" 인용 블록. 4단계 런타임에 "worktree면 remote URL 일치 머신만, 자동 선택 비활성" 명시 |
| N1 | §4.5 task 이력 표 아래 "정보 5종의 정의, 열 배치는 폭에 맞춘다 — 좌열은 2줄 행" 추가 |
| C1′ | §4.5 참여자 인용 블록을 파생 규칙으로 교체 |
| N4·N5·m4 | §5 상태 배지 규칙에 심각도 글리프 `! ▲ i`, `idle` 회색, `-text` 변형 **예외 없이** 추가 |
| m10 | §8.4 변경 이력(v0.2 → v0.3) 신설 — 캔버스가 문서보다 먼저 옳았던 경우 2건(C4, N1) 기록 |

### 2.3 캔버스 v0.3

**토큰·컴포넌트 (전 화면 자동 반영)**

| 리뷰 | 조치 |
|---|---|
| m4 | `$s-block-text` #6D28D9, `$s-run-text` #1D4ED8, `$s-fail-text` #B91C1C 추가. `$s-block`·`$s-run`을 텍스트에 쓰던 5곳 교체 — 이제 6색 전부 `-text` 변형이 있다 |
| N2 | Agent Chip 컴포넌트의 `Role` 텍스트를 `fixed-width` + `fill_container`로, `Text` 프레임 `fill_container`, 루트 폭 268. 인스턴스 12개 `fill_container`. 긴 부가 텍스트가 **둘째 줄로 줄바꿈** — 검증: 4개 칩 모두 w=268, clip 없음 |
| N5 | `idle` 배지 7개 → 글리프 ○ + `$ink-3`. 칩 상태 점 9개(idle) → `$ink-3` |
| N4 | `attention` 배지 3개 → ▲. S10 "기본" → ★ |

**S7 (기준 화면)**

| 리뷰 | 조치 |
|---|---|
| **N1** | task 이력 4행을 **2줄 행**으로 재구성: 1줄 "#2 재지시 · cancelled · $0.18"(굵게, cancelled는 빨강), 2줄 "Alex 중단·재지시 10:12 · resume ✓". 첫 행 "resume – (첫 실행)"은 주황. 검증: 각 줄 232px, 렌더 확인 |
| m3 | #3 트리거 "QA 스레드 답글 10:35" → **"Lead 답글 10:21"** (타임라인 10:35 시스템 메시지가 말하는 재진입 원인과 일치) |
| C1′ | @Lead 칩 → `waiting_human`(⏳ 주황) "lead · Claude Code · waiting_human — Director 승인 대기". lane 보드의 waiting_human 카드와 이제 일치 |
| N6 | 각주를 **참여자 아래·lane 보드 위**로 이동 (3개 S7 변형 모두). lane 보드는 여전히 스크롤 — 접기는 다음 라운드 |
| 각주 문구 | "칩 상태는 task에서 파생 (FR-1.3): working > waiting_human > idle. lane의 blocked·paused는 lane 카드가 말한다." |

**S7-P · S7-D**

| 리뷰 | 조치 |
|---|---|
| **N3** | @Backend 칩 → `idle` 회색 "lane #3 예산 대기". 작성창 칩 "@Backend · **세션 일시정지** → 큐잉". Lead 답글 메타 → "10:21 · 답글 → lane #2"(S7과 동일). HITL 카드 메타는 "Backend #3 · Frontend #3 때문에 발행"으로 정정 |
| N1 | S7-D의 task 이력도 2줄 행으로 (S7에서 복사된 별도 노드) |
| m9 | S7-D 툴팁을 Header 밖 Main의 absolute 자식(y=68)으로 이동 — 헤더 경계에 잘리지 않음 |

**S6 (마법사 3종)**

| 리뷰 | 조치 |
|---|---|
| **C4′** | 스테퍼 7개 라벨을 새 순서로 교체(3종 모두). S6 원본은 6단계(종료 조건) 활성 유지 |
| C4′ | 구 "S6-3"을 **S6-4 (4단계 런타임)** 로 개명. 제목 "4. 런타임", 설명에 "3단계에서 worktree를 골랐으므로 acme/app.git 저장소가 있는 머신만 후보" |
| C4′ | **되돌림 주의문 삭제.** "자동 선택" 라디오는 비활성(opacity 0.45) + "worktree 격리에서는 쓸 수 없습니다 — none 격리에서만 활성" |
| C4′ | 요약 패널: 격리 행을 런타임 앞으로(3종). S6-4 요약은 goal·Director·격리만(4단계 전이므로) |

**S5 · S8 · S13 · 범례**

| 리뷰 | 조치 |
|---|---|
| m5 | S5 1행 `active · 1 running` |
| m6 | S8 approval 항목 세션을 "온보딩 이메일 시퀀스 v2", 작성자 @Writer lane #2로 — `session_paused`와 다른 세션 |
| N6 | S8 정렬 각주를 목록 **위**로 이동 |
| m7 | S13 3·4행 경로 `s19/backend`·`s21/backend` |
| m8 | S13 경로 열 380 → 340으로 상태 열에 여유 |
| m1 | 범례 제목 "v0.3 — PRD v0.10 · SCREEN.md v0.3" |
| **m2** | 범례 색 견본을 타원 → **Badge 인스턴스 10개**로 교체(running·waiting_human·blocked·paused[soft]·done / failed[solid]·idle·queued·disabled·cancelled). 인박스 심각도 행(! ▲ i) 추가. 배치 안내에 S6-4 반영 |

---

## 3. 리뷰 원안에 덧붙인 것

**C1′ — `error`의 정의.** 리뷰의 파생 규칙 (a)는 "running → working, waiting_human → waiting_human, 아니면 idle"이라 `error`·`offline`·`disabled`가 언제 뜨는지 없었다. `offline`·`disabled`는 원인이 자명하지만 `error`는 아니다. task 하나가 `failed(timeout)`했다고 에이전트를 `error`로 두면 사람이 "다시 지시"할 때까지 에이전트 페이지에 빨간 배지가 남는데, 그 사이 다른 lane은 멀쩡히 돌 수 있다. 그래서 **`error` = 이 에이전트는 지금 실행할 수 없다**(인증·쿼터·설정 — FR-7.1이 재시도하지 않는 분류)로 좁혔고, task 실패는 lane 카드의 몫으로 남겼다. 우선순위 표에서 `error`는 `working`보다 위다 — 실행 불가면 running task도 없어야 하므로 모순되지 않는다.

**m4 — 변형 추가 쪽 선택.** 리뷰는 "-text 변형 추가 또는 규칙에 예외" 중 택일이라 했다. 변형을 만들었다. 변수 3개는 싸고, "대비 4.5:1 이상이면 원색 허용"은 다음 사람이 잊는다.

---

## 4. 보류 — 다음 라운드

| 항목 | 이유 |
|---|---|
| N6 lane 보드 접기 (7장 → 상태별 칸반) | 리뷰 분류대로. 카드 컴포넌트화(후속 #01 §4)와 함께 |
| §6.2 컴포넌트화 (Lane Card·HITL Card·Inbox Item·Message Card…) | **여전히 다음 라운드 1순위.** 이번에 task 이력을 S7·S7-D에서 따로 두 번 고쳤다 — 같은 카드가 3벌(S7·S7-P·S7-D)이라 생기는 비용 |
| O7~O10 (후속 #01 보류 항목) | 컴포넌트화 이후 |

---

## 5. 검증

| 대상 | 방법 | 결과 |
|---|---|---|
| S7 task 이력 | 노드 스크린샷 + bounds | 2줄 행 4개, 각 232px, 렌더 정상 |
| S7 참여자 칩 | bounds | 4개 모두 268px, clip 없음, 부가 텍스트 둘째 줄 |
| S7-P 참여자·작성창 | 노드 스크린샷 | Backend idle 회색, "세션 일시정지 → 큐잉" |
| S6 스테퍼 | 노드 스크린샷 | goal ✓ Director ✓ 격리 ✓ 런타임 ✓ 참여자 ✓ [종료 조건] 한도 |
| S6-4 패널·요약 | 노드 스크린샷 | "4. 런타임", 주의문 없음, 자동 선택 비활성, 요약 3행 |
| 범례·심각도 | 노드 스크린샷 | 배지 10개 글리프 + soft/solid, ! ▲ i |
| S5·S8·S13 값 | 노드 읽기 | `1 running`, 세션명 변경, `s19/backend` |
| 클리핑 전수 | `ctx.problems` (10개 프레임, 스크롤 영역 제외) | S7-D 툴팁·S8 각주 2건 → 수정 후 0 |

**도구 특성 재확인**: `Copy`·재구성 직후 `ctx.problems`가 "fully clipped"를 보고하고 최상위 스크린샷이 비어 보이는 현상이 이번에도 반복됐다(task 이력 재구성 직후). `placeholder` 토글로 재계산 후 자식 노드를 찍으면 정상. 후속 #01 §2.1과 같은 결론이다.

---

## 6. 저장 — 반드시

캔버스 변경은 **앱 메모리에만** 있다. `execute` API에 저장 함수가 없고 앱의 자동저장 리비전도 디스크에 없다(이전 턴에서 확인). **pen.dev에서 ⌘S를 눌러야** 파일이 갱신된다. 저장 전 파일은 v0.2(18:52)다.

---

## 7. 다음 리뷰에서 봐 주었으면 하는 것

1. **C1′ 파생 규칙의 `error` 정의** — "실행 불가"로 좁힌 것이 맞는지. task 실패를 에이전트 층에 전혀 안 올리는 것이 정보 손실인지.
2. **S6-4 자동 선택 비활성 처리** — worktree일 때 라디오를 숨기지 않고 비활성 + 사유로 둔 것이 §1 원칙 4(숨기지 않고 비활성)에 맞는지, 아니면 worktree에서는 아예 안 보이는 게 나은지.
3. **범례를 Badge 인스턴스로 바꾼 것** — 이제 범례가 컴포넌트를 참조하므로 컴포넌트를 고치면 범례도 바뀐다. 의도한 것이지만 "범례 = 규칙 문서"로 볼 때 부작용이 있는지.
