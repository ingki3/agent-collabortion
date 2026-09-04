# 캔버스 컴포넌트 카탈로그

| 항목 | 내용 |
|---|---|
| 대상 | `agent-collaboration.pen` 와이어프레임 **v0.4** |
| 근거 | `SCREEN.md` v0.3 §5 공통 컴포넌트, `design/DESIGN_REVIEW_01.md` §6.2 · `design/DESIGN_REVIEW_02.md` §4 (컴포넌트화 요청). 리뷰·후속 조치 이력은 `design/` |
| 작성일 | 2026-09-04 (리뷰 #03 반영으로 갱신) |
| id 신뢰 범위 | **v0.4부터 인스턴스 id는 이 문서와 캔버스만 신뢰한다.** 후속 #01·#02에 적힌 인스턴스 id(`NnWRc`, `e1Lzj` 등)는 자기 교체로 전부 바뀌었으므로 이력으로만 읽는다(K4). 컴포넌트 **정의** id는 바뀌지 않는다 |
| 목적 | 캔버스의 `reusable` 컴포넌트 11개를 **id·오버라이드 키·슬롯·사용처**까지 기록해, 다음 사람이 화면을 추가하거나 카드를 고칠 때 손으로 그리지 않고 인스턴스를 쓰게 한다 |

> **이것은 디자인 시스템이 아니다.** 토큰(색 18개 + 글꼴 1개, `X:` 임포트 제외)과 도메인 컴포넌트만 있다. 기초 컴포넌트(버튼·입력·탭)는 임포트된 `X:` nitro 라이브러리에 있지만 와이어프레임에서는 쓰지 않았다. 하이파이로 갈 때 "nitro를 기초로 쓸지"를 결정한다.

---

## 1. 무엇이 바뀌었나

v0.3까지 lane 카드·HITL 카드·인박스 항목·메시지 카드·조건 행·프로파일 행·배너는 **화면마다 손으로 그린 개별 프레임**이었다. 같은 카드가 S7·S7-P·S7-D에 3벌씩 있어서, 리뷰 #02의 task 이력 수정을 두 번 따로 했다.

v0.4에서 이것들을 `reusable` 컴포넌트로 올리고 **모든 화면을 인스턴스로 재조립**했다. 손으로 그린 카드는 남아 있지 않다(검증: 정규식 전수 탐색 0건). 이제 Lane Card 정의 하나를 고치면 21개 인스턴스에 반영된다.

| 컴포넌트 | id | 인스턴스 | 쓰이는 곳 |
|---|---|---|---|
| Badge | `Tfz9o` | 28 + 컴포넌트 내부 | 전 화면 상태 배지 (v0.2부터) |
| Agent Chip | `MmxsR` | 12 | S7 3종 참여자 |
| App Nav | `D6yb65` | 13 | 앱 셸 화면 전부 |
| **Lane Card** | `x1YCq` | 21 | S7 3종 lane 보드 (7장씩) |
| **Message Card** | `CYRKG` | 18 | S7 3종 타임라인 (6장씩) |
| **HITL Card** | `n9PqY` | 3 | S7 3종 타임라인 |
| **Inbox Item** | `T0qdqP` | 8 | S8 |
| **Condition Row** | `XMNop` | 7 | S6 (5), S7 우열 (2) |
| **Profile Row** | `ZWC8q` | 2 | S10 |
| **Status Banner** | `zPJly` | 3 | S7-P paused, S6 경고, S17 유실 경고 |
| **Activity Feed** | `h6pub` | 3 | Message Card의 슬롯 안 (S7 3종 Lead 메시지) |

캔버스 위치: 기존 3개는 (0,0)~(0,140), 범례는 x=300. 새 8개는 **x=1700부터 시작하는 컴포넌트 선반**에 세 줄로 놓았고 각 정의 위에 이름·id 라벨이 있다(`Component Labels` 프레임). 컴포넌트는 항상 화면(y≥1120) 위에 둔다.

| 줄 | y | 컴포넌트 (왼쪽부터) |
|---|---|---|
| 1 | 60 | Lane Card · Message Card · HITL Card · Activity Feed |
| 2 | 420 | Inbox Item · Condition Row |
| 3 | 600 | Profile Row · Status Banner |

> v0.4 초판은 범례 오른쪽 x=1194에 두었는데 범례가 1317px로 넓어지면서 Lane·Message·HITL Card를 덮었다(리뷰 #03 직후 사용자 지적). 최상위 프레임끼리의 겹침은 자동 검사로 0건 확인.

---

## 2. 컴포넌트별 명세

오버라이드는 인스턴스의 `descendants` 맵에 **노드 id**를 키로 넣는다. 컴포넌트 안의 Badge처럼 중첩된 ref는 `"내부refId/배지노드id"` 경로로 접근한다. 부분을 끄려면 `{enabled:false}`, 켜려면 `{enabled:true}` — 정의에서 꺼둔 슬롯은 인스턴스에서 켜야 보인다.

### 2.1 Lane Card `x1YCq`

lane 보드의 카드. 7개 lane 상태를 하나의 정의로 표현한다. 폭 268(좌열), 인스턴스는 `width:"fill_container"`.

| 부분 | id | 기본 | 오버라이드 |
|---|---|---|---|
| 에이전트 이름 | `R3fKpX` | 켬 | `content` |
| 상태 배지 (Badge ref) | `HF1VS` | 켬 (기본 `queued` ○ 회색 — 오버라이드를 빠뜨려도 "실행 중"으로 오독하지 않게, m4) | `HF1VS/zlCEw` 라벨 · `HF1VS/IOnJ0` 색 · `HF1VS/EnjHf` 글리프+색. soft(paused): `HF1VS: {fill:$bg, stroke:색, strokeWidth:1.5}`. solid(failed): `HF1VS: {fill:색, stroke:색}` + 라벨·글리프 `fill:$bg` |
| 요약 | `PDkU3` | 켬 | `content` |
| 부가 정보 | `bXrz6` | **끔** | `{enabled:true, content, fill}` — 상태색 `-text` 변형 |
| 동작 묶음 | `b7sPL` | **끔** | `{enabled:true}` |
| 버튼 1·라벨 | `mEolH` · `WmIHX` | 켬 | 라벨 `content`. 비활성 표현은 `mEolH: {opacity:0.4}` |
| 버튼 2·라벨 | `jphJo` · `ac2d1` | 켬 | 버튼 하나만 쓰면 `jphJo: {enabled:false}` |
| task 이력 슬롯 | `wG4Pf` | **끔** | `{enabled:true}` 후 `Insert(인스턴스id+"/wG4Pf", …)` 로 2줄 행을 넣는다 (§3) |

상태별 조합:

| 상태 | 배지 | 부가 | 버튼 |
|---|---|---|---|
| `queued` | ○ `$ink-3` | — | — |
| `running` | ● `$s-run` | (deputy 시점: "취소는 즉시 가능") | 중단하고 다시 지시 · 중단 |
| `blocked` | ? `$s-block` | "? @위임자의 답을 기다림" | — |
| `paused` | ⏸ soft | "계속 진행 승인 필요" | 계속 진행 승인 |
| `waiting_human` | ⏳ `$s-wait` | "⏳ Director 승인 대기 · Inbox" | 응답하러 가기 |
| `done` | ✓ `$s-done` | — | — |
| `failed` | ✕ solid | 실패 분류·재시도 소진 | 다시 지시 |

### 2.2 Message Card `CYRKG`

타임라인 메시지. `text`·`system`·`blocked_q`·`answer` kind를 배지와 테두리로 구분한다. 폭 580, 인스턴스 `fill_container`.

| 부분 | id | 기본 | 오버라이드 |
|---|---|---|---|
| kind 배지 (Badge ref) | `YHA5b` | **끔** | `{enabled:true}` + `YHA5b/zlCEw` `YHA5b/IOnJ0` `YHA5b/EnjHf` |
| 작성자 | `J0mWUr` | 켬 | `content` |
| 메타 | `rIri9` | 켬 | `content` |
| 본문 | `YydN1` | 켬 | `content` |
| 슬롯 | `XMTib` | **끔** | `{enabled:true}` 후 `Insert(인스턴스id+"/XMTib", Activity Feed ref)` |

카드 자체 오버라이드: 질문 카드(`blocked_q`)는 인스턴스에 `padding:12, fill:$bg, stroke:$s-block, strokeWidth:1`.

kind별 조합 (K3):

| kind | Kind 배지 | 카드 오버라이드 | 슬롯 |
|---|---|---|---|
| `text` (사람·에이전트 일반) | 끔 | 없음 (투명) | 에이전트 메시지면 Activity Feed |
| `system` | `system` · `$ink-3` | 없음 | — |
| `blocked_q` (질문 카드, 스레드 루트) | `질문 → @위임자` ? `$s-block` | `padding:12, fill:$bg, stroke:$s-block` | — |
| `answer` (질문 답글) | `answer` ↳ `$ink-3` | 없음 | — |
| `summary` (세션 완료) | `summary` ✓ `$s-done` | `fill:$surface` | — (v1 화면에 예시 없음) |

### 2.3 HITL Card `n9PqY`

승인·질문 요청 카드. S7 타임라인과 S8 인박스가 **같은 컴포넌트를 쓰지는 않는다** — 인박스는 세션명·심각도가 더 필요해 Inbox Item이 따로 있다. 실제로 본문 구조도 같지 않다(여기는 `g71PvC` 제안 기본값 슬롯, Inbox Item은 `fDXjQ` 부가 텍스트로 대신). **하이파이에서는 HITL 본문(질문·제안·기한·버튼)을 하위 컴포넌트로 빼서 둘 다 품게 한다** — F2(인박스에서 맥락 없이 답한다)의 원래 의도다. SCREEN §5도 그렇게 고쳤다(리뷰 #03 K2). 폭 580.

| 부분 | id | 기본 | 오버라이드 |
|---|---|---|---|
| kind 배지 | `cHpam` | 켬 | `cHpam/zlCEw` (`HITL · approval` / `HITL · question`) |
| 작성자 | `Lqg7w` | "시스템" | 에이전트 질문이면 에이전트명 |
| 메타 | `i2BcH` | | `content` — 시스템 발행이면 "… 때문에 발행 · source: system" |
| 질문/대상 | `JzPyQ` | | `content` |
| 제안 기본값 | `g71PvC` | **끔** | `{enabled:true, content}` — `question` 타입은 필수(FR-5.1) |
| 기한·권한 | `cmq2b` | | `content` |
| 버튼 1(주)·라벨 | `qw6Mj` · `wE8wV` | 켬 | 라벨. 비활성 `qw6Mj: {opacity:0.4}` |
| ~~버튼 옆 메모~~ | ~~`V55koW`~~ | — | 용도 없어 **삭제**(리뷰 #03 K1). 버튼 옆 짧은 안내는 `W7gS3` 권한 안내로 |
| 버튼 2·라벨 | `r7AS3m` · `X7yhTl` | "거절" | |
| 권한 안내 | `W7gS3` | **끔** | `{enabled:true, content}` — deputy 시점의 "🔒 22:31부터 승인 가능" |

### 2.4 Inbox Item `T0qdqP`

인박스 항목 7종을 하나로. 폭 1172.

| 부분 | id | 기본 | 오버라이드 |
|---|---|---|---|
| 심각도 배지 | `flvU9` | | `flvU9/zlCEw` `flvU9/IOnJ0` `flvU9/EnjHf` — `action_required` **!** · `attention` **▲** · `info` **i** |
| 종류 | `SzQ6z` | | `content` |
| 세션 | `chBBl` | | `content` ("· 세션명") |
| 기한 | `ZHNoQ` | | `content`. overdue면 `fill:$s-fail-text, fontWeight:700` |
| 본문 | `I6r6T` | | `content` |
| 부가 | `fDXjQ` | **끔** | `{enabled:true, content}` |
| 버튼 1(주) | `N1gvMt` · `mYkDk` | 켬 | 라벨 |
| 버튼 2 | `wocsZ` · `JZCMt` | **끔** | `{enabled:true}` + 라벨 |
| 버튼 3 | `bIbO0` · `w4w1Uf` | **끔** | 〃 |

카드 오버라이드: `action_required`는 인스턴스에 `stroke:심각도색, strokeWidth:2`.

타입별 조합 (K3). **심각도 배지의 색은 항목의 원인 상태를 따른다** — 글리프가 심각도, 색이 원인(리뷰 #03 N4). 다음 사람이 `attention`을 빨강으로 통일하려 들지 않도록 규칙으로 둔다.

| 타입 | 심각도 · 글리프 · 색 | 버튼 | 부가 |
|---|---|---|---|
| `hitl_request` approval | action_required ! `$s-wait` | 계속 진행 승인 · 거절 · 세션 열기 | deputy 응답 가능 시점 |
| `hitl_request` question | action_required ! `$s-wait` | 답변 보내기 · 세션 열기 | 에이전트 제안값 + autonomy 결과 |
| `lane_blocked` | action_required ! `$s-block` | 답글 작성 · 세션 열기 | — |
| `session_paused` | attention ▲ `$s-pause` | 계속 진행 승인 · 세션 열기 | 재개 방식 |
| `runtime_offline` | attention ▲ `$s-fail` | 재바인딩 · 세션 종료 | — |
| `run_failed` | attention ▲ `$s-fail` | 다시 지시 · 세션 열기 | — |
| `mention` | info i `$s-run` | 세션 열기 | — |
| `session_completed` | info i `$s-done` | 세션 열기 | — |

### 2.5 Condition Row `XMNop`

종료 조건 한 줄. S6 마법사(테두리 있음)와 S7 우열 진행률(테두리 없음) 둘 다 쓴다. 폭 808.

| 부분 | id | 기본 | 오버라이드 |
|---|---|---|---|
| 체크 | `V8cDma` | ☐ | `content` (☑ / ☐ / —) |
| 이름 | `VLDQ9` | | `content`, S7에서는 `fontSize:12` |
| 설명 | `oKmnM` | | `content`, S7에서는 `fill:$ink-3` |
| 옵션 드롭다운 | `NayQT` · `QphSd` | **끔** | `{enabled:true}` + 라벨 |
| v1.1 배지 | `GzFvX` | **끔** | `{enabled:true}` |

S7 우열용 오버라이드: `stroke:"#00000000", padding:[2,0]`. 선택된 조건은 `stroke:$ink`, 비활성(v1.1)은 `opacity:0.45`.

### 2.6 Profile Row `ZWC8q`

에이전트 편집의 런타임 프로파일 한 줄. 폭 808.

| 부분 | id | 오버라이드 |
|---|---|---|
| 이름 · 런타임 · 모델 · 옵션 · 폴백 | `pPGCI` `T1edb` `epf1b` `luoSl` `Yla1S` | `content` |
| 기본 배지 | `I4fSYw` | **끔** → `{enabled:true}`. 글리프 ★ `$ink` — 상태색을 쓰지 않는다(리뷰 #02 N4, #03 m5) |

기본 프로파일 행은 `stroke:$ink`.

### 2.7 Status Banner `zPJly`

세션 `paused` 사유 배너, 마법사 경고, 재바인딩 유실 경고. 폭 268(우열), 인스턴스 `fill_container`. 정의는 주황(경고)이고 색은 인스턴스에서 바꾼다.

| 부분 | id | 기본 | 오버라이드 |
|---|---|---|---|
| 제목 | `v5FTca` | | `content`, `fill` (`-text` 변형) |
| 설명 | `KAlIg` | | `content` |
| 입력 | `t42cy` · `M7KnB` | **끔** | `{enabled:true}` + 값 — "새 상한: $30" |
| 버튼 1·2 | `kCPXF` → `f0epr`·`atFpz`, `IWssW`·`ynXhu` | **끔** | `kCPXF: {enabled:true}` + 라벨 |
| 권한 각주 | `CDR7n` | **끔** (기본 문구 "권한 각주 — 예: …") | `{enabled:true, content}` — 켜면 반드시 `content`를 넘긴다 |

색 변형 — paused: `fill:#FCE7F3, stroke:$s-pause`, 제목 `$s-pause-text`. 유실 경고: `fill:#FEE2E2, stroke:$s-fail`, 제목 `$s-fail-text`.

### 2.8 Activity Feed `h6pub`

메시지 카드 슬롯에 들어가는 "활동 보기" 펼침. 행 5개 슬롯(기본 4개 켬). 폭 580.

| 부분 | id | 오버라이드 |
|---|---|---|
| 제목 (run · 런타임 · resume) | `dMOdM` | `content` |
| 행 1~5 | `hLPBV` `qGpJs` `Tlj89` `lSHWZ` `AopVY` | 안 쓰는 행 `{enabled:false}` |
| 행 아이콘 1~5 | `BD3Sc` `TlATI` `Jvslm` `hRKb2` `tI1NY` | `content` — **이벤트 종류**다: ● 동작(플랫폼 조작·편집·셸), ○ 턴 생명주기(시작·종료). FR-7.2의 "제자리 갱신(대기 → 실행 중 → 완료)"은 같은 행의 **문장 끝 결과**로 표현하고 아이콘을 바꾸지 않는다(K6) |
| 행 문장 1~5 | `k6xMtp` `P2NA7A` `PanD2` `DDDbU` `he2xa` | `content` — "동사 · 목적어 → 결과" |
| 강등 안내 | `Bxz9s` | **끔** → 구조화 이벤트 없는 런타임에서 `{enabled:true}` (SCREEN §1 원칙 5) |

---

## 3. 슬롯에 내용 넣기 — 인스턴스 안에 Insert

Lane Card의 task 이력, Message Card의 활동 피드처럼 **인스턴스마다 다른 하위 트리**가 필요한 곳은 슬롯 프레임을 두고 인스턴스 경로로 Insert한다.

```js
inst = Insert(laneBoard, {type:"ref", ref:"x1YCq", descendants:{..., "wG4Pf":{enabled:true}}})
Insert(inst + "/wG4Pf", {type:"frame", name:"TR", layout:"vertical", ...})   // 슬롯 안에 자식 추가
Insert(msgInst + "/XMTib", {type:"ref", ref:"h6pub", descendants:{...}})    // 슬롯에 다른 컴포넌트 인스턴스
```

이 방식의 한계: 슬롯 내용은 인스턴스마다 따로 관리된다(S7·S7-D의 task 이력은 여전히 두 벌). 그래도 **카드 껍데기는 하나**라 카드 스타일 변경은 전파된다.

---

## 4. 알려진 도구 특성

- **정의의 `enabled:false` 슬롯이 `fill_container`면 검사기가 "not inside a flexbox layout" 경고를 낸다.** 꺼진 노드를 레이아웃 밖으로 보기 때문이다. 인스턴스에서 켜면 정상이다. 무해하지만 매 execute마다 8줄씩 나온다. 없애려면 슬롯 정의에서 `width`를 빼고 인스턴스에서 `{enabled:true, width:"fill_container"}`로 함께 주면 된다 — 인스턴스 40여 개를 다시 손봐야 해서 보류.
- **`Insert`로 만든 ref는 앱이 레이아웃을 돌리지 않는다 — 스크린샷도 PNG Export도 빈칸이다.** 공간은 차지하고 `ctx.bounds`도 맞지만 텍스트·중첩 배지가 그려지지 않는다. `placeholder` 토글이나 컴포넌트 정의 속성 토글로는 풀리지 않았다. **`Replace(id, Get(id, {depth:30}))` — 자기 자신으로 교체하면 즉시 렌더된다.** 이번 라운드에서 인스턴스 62개 전부에 적용했다. id가 바뀌므로 다른 곳에서 인스턴스 id를 참조하고 있으면 안 된다. 슬롯 자식이 있는 인스턴스는 `depth`를 충분히 줘야 한다 — 얕게 읽으면 하위가 `"..."`로 잘려 Replace가 그것을 그대로 쓴다.
- **정의(reusable) 프레임은 만든 직후 스크린샷에서 텍스트가 빈칸이다.** 정의의 자식을 `Update`로 한 번 건드리면(예: 기본 배지 오버라이드 변경) 그 뒤로는 렌더된다 — 리뷰 #03 반영 때 Lane Card에서 확인. 정의를 `Replace`하면 id가 바뀌어 모든 `ref:`가 끊기므로 **절대 하지 않는다**; 렌더가 필요하면 Update로 재해석을 유도한다.
- 하나의 `execute`에서 `const`로 선언한 헬퍼는 다음 호출에 남지 않는다. 8개 영역을 병렬 호출로 재조립할 때 `lane()`·`msg()`·`item()` 헬퍼를 호출마다 다시 정의했다.

---

## 5. 검증

| 항목 | 방법 | 결과 |
|---|---|---|
| 손으로 그린 카드 잔존 | 정규식 전수 탐색 (`^(Lane \|Msg \|Item \|Cond \|P default\|P fast)`) | 0건 ("Lane Board" 컨테이너 3개만 매칭 — 카드 아님) |
| 인스턴스 수 | ref 전수 집계 | 표 §1과 일치 |
| 슬롯 내용 | `resolveInstances:true`로 트리 읽기 | Lead 메시지 슬롯에 AF ref, Backend lane 슬롯에 History 4행 |
| 레이아웃 문제 | `ctx.problems` 전수 (스크롤 영역 제외) | 0 |
| 레이아웃 문제 (전체) | `ctx.problems` 전수 | S13 상태 열 `V` 1건 15px 클립(스크롤 아님) → 경로 열 320으로 축소해 해소(리뷰 #03 m6·m7) |
| 렌더 | 인스턴스 62개를 `Replace(id, Get(id,{depth:30}))`로 자기 교체 후 스크린샷 | 전부 정상. 슬롯 내용(task 이력 4행, AF 4행)도 교체를 거쳐 보존됨. 정의 프레임만 스크린샷에서 빈칸(§4) |

---

## 6. 다음 라운드에서 할 수 있게 된 것

컴포넌트가 생겨서 후속 #01 §4에 보류했던 항목의 비용이 내려갔다.

| 항목 | 방법 |
|---|---|
| O10 런타임 능력 강등 표시 | Activity Feed의 `Bxz9s` 켜고 행 4개 끄기 — 인스턴스 하나 |
| O9 실시간 연결 끊김 배너 | Status Banner 인스턴스를 앱 셸 상단에 |
| O8 빈 상태 8종 | 별도 컴포넌트 "Empty State" 하나 추가 |
| N6 lane 보드 접기 | Lane Card는 그대로, 보드를 상태별 접이식 그룹 컴포넌트로 |
| O7 참여 에이전트 다이얼로그 | 마법사 5단계(참여자) 행을 컴포넌트로 만든 뒤 재사용 |
