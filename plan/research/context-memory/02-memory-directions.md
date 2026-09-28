# Colab 에이전트의 맥락 유지·관리·회상 — 키워드 검색을 넘어서

조사일 2026-09-28 · 선행 보고: `scratchpad/graphify-research.md`(2026-09-27)
Colab 근거: `PRD.md` FR-4.1·FR-4.2·FR-3.1.2·FR-4.5·FR-2.5·§8.4, `contracts/harness.md` §6·§10(E12-11), `server/internal/queue/bundle.go`, `server/internal/tasks/resume.go`, 실사용 DB `colab-pg-g6` **읽기 전용 SELECT**(`BEGIN READ ONLY … ROLLBACK`, 2026-09-28 조회 — 선행 보고 이후 행이 늘어 수치가 조금 다르다).

---

## 0. 결론 먼저

**Director 의 판단이 맞다. 그리고 실측을 다시 보면 문제는 "검색"이 아니었다.**

1. **Lead 비용의 주범은 옛 맥락 재독이 아니라 "한 번 호출할 때마다 끌고 다니는 컨텍스트 크기"다.** 게임 제작 방 Lead 는 60 task 에 도구 호출 2,133회, **호출 한 번당 cache_read 중앙값 약 39만 토큰**. `colab_room_messages` 호출은 60 task 통틀어 **12회**뿐이다. 즉 에이전트가 히스토리를 찾아 읽는 게 아니라, **재개(resume)된 런타임 세션 안에 턴 프롬프트가 턴마다 통째로 다시 쌓이고**, 그 위에서 셸 1,351회(브라우저 테스트·파이썬·미디어 처리)를 돌리고 있다(§1).
2. 그래서 근본 처방은 세 층이다.
   - **(가) 컨텍스트 수명주기를 고친다** — 재개 턴엔 "지난 턴 이후 바뀐 것"만 보내고(델타 턴 프롬프트), 런타임 세션 크기에 상한을 두고(압축 임계·세션 교체), Lead 의 무거운 검증 작업을 깨끗한 컨텍스트(서브에이전트/QA lane)로 뺀다. **비용에 직접 듣는 유일한 층.**
   - **(나) "상태"를 일급 객체로 만든다 — 방·미션 상태 원장(Room/Mission State Ledger).** 사실·결정·제약·현재 값·열린 질문·아티팩트 포인터를 **짧은 항목 단위**로 Postgres 에 두고, 에이전트가 도구로 추가·대체·폐기하며, 턴 프롬프트는 원문 메시지 더미 대신 이 원장을 싣는다. **조정 일관성과 회상 정확도의 근본 해법.** (Letta memory block, Mem0 의 항목 연산, Zep 의 유효기간, ACE 의 증분 업데이트를 Colab 모양으로 섞은 것.)
   - **(다) 회상 도구는 그 위의 보조** — 원문(메시지·detail 조각·아티팩트·원장 항목)에 대한 **하이브리드 검색(전문/트라이그램 + pgvector, RRF)** 을 `colab room recall` 한 도구로. 원장이 못 담은 긴 꼬리를 위한 것이다.
3. 그래프 DB(Zep/Graphiti)·외부 메모리 서비스(Mem0·Letta 서버)는 **개념만 빌리고 부품은 들이지 않는다** — 방 권한(FR-4.5)·결정성·단일 Go+Postgres 배포와 맞지 않고, 필요한 기능(유효기간·대체 사슬·출처)은 테이블 몇 개로 된다.

**순서: 0 계측·문항 → 1 수명주기(가) → 2 상태 원장(나) → 3 하이브리드 회상(다) → 4 자동 정리·사람 화면.**

---

## 1. 실측으로 문제를 다시 정의한다

### 1.1 게임 제작 방 — 에이전트별 (task_usage 합, 2026-09-28)

| 에이전트 | task | USD | task 당 중앙 USD | cache_read |
|---|---|---|---|---|
| Designer | 19 | 807.8 | 32.96 | 1.14억 |
| Lead | 60 | 703.6 | 10.00 | **7.50억** |
| Developer | 19 | 673.5 | 28.95 | 1.13억 |
| Writer | 23 | 241.8 | 10.36 | 2.57억 |
| Researcher | 15 | 179.4 | 10.45 | 1.83억 |

방 합계 ≈ **$2,606**, 메시지 218건, content 17.1만 자, detail **91.5만 자**, 결정 18건(1.2만 자).

### 1.2 Lead 는 무엇에 토큰을 썼나

| 항목 | 값 |
|---|---|
| Lead lane / task / 런타임 세션 | 16 lane · 62 task · 15 세션 — **한 lane(7f1d…)에 task 22개가 한 세션으로 이어짐**(lane 재사용 규칙 3 + 재진입마다 resume, PRD 1333행) |
| 도구 호출(시작 이벤트) | 2,133회 / 60 task ≈ **task 당 36회** |
| **호출 한 번당 cache_read** | 평균 35.2만 · **중앙 38.9만 토큰** |
| 도구 종류 | Terminal **1,351** · Read File 288 · message_post 89 · artifact_submit 43 · ToolSearch 39 · decision_record 15 · **room_messages 12** · status_set 8 · artifact_get 3 |
| 셸 명령 분류 | 브라우저/노드 테스트 300 · 파이썬/미디어 244 · 파일 확인(grep/sed/shasum…) 179 · 기타 626 · `colab` CLI 2 |

해석:

- **"히스토리를 다시 읽느라 비싸다"는 가설은 Lead 에 대해선 틀렸다.** 재독 도구(room_messages)는 12회뿐. 비용 = (호출 수 ≈ 36/task) × (호출당 컨텍스트 ≈ 39만).
- 호출당 39만 토큰은 턴 프롬프트 한 벌로 설명되지 않는다. `bundle.go` 는 **재개 턴에도** `<history>`(최근 50) + `<mission_messages>`(그 미션 메시지 전부) + `<room_decisions>` 를 **매번 전부** 싣는다(`PlanAttempt` 의 HistoryIncluded 는 재개 여부와 무관). 이니셜 D 미션은 메시지 115건·content 13.3만 자 + detail 미리보기 4.2만 자 ≈ 17.5만 자 — **한국어 토큰 환산을 1.5~2자/토큰으로 잡으면 턴 프롬프트 한 벌이 9만~12만 토큰**(추정). 재개된 Claude Code 세션은 이전 턴들의 프롬프트·도구 결과를 그대로 품고 있으므로, **같은 히스토리가 턴마다 한 벌씩 더 쌓인다.** 1M 창 모델(`claude-opus-5[1m]`)이라 자동 압축이 늦게(기본 창의 ~83% 근처) 걸려 세션이 수십만 토큰으로 떠 있다. → **가설: Lead 비용의 큰 몫은 "재개 세션 안의 히스토리 중복 + 큰 도구 출력 누적"이다.** 호출당 컨텍스트의 구성(프롬프트 대 도구 결과)은 아직 분해하지 않았다 — 0단계 계측의 첫 항목.
- Lead 가 **조정자가 아니라 검증·제작자**로도 일한다(셸 1,351회, 브라우저 테스트·미디어 처리). 그 결과물이 모두 Lead 의 장수 세션에 쌓인다. Anthropic 이 권하는 "무거운 탐색은 깨끗한 컨텍스트의 하위 에이전트가 하고 요약만 돌려준다"의 정반대 모양이다.
- Designer·Developer 는 cache_read 보다 **비캐시 input 이 큰** 다른 모양(선행 보고: input 0.99억·1.2억) — 캐시 접두가 자주 깨지거나 큰 파일을 새로 읽는 쪽. 이 보고서의 주 대상은 아니지만 0단계에서 같이 분해한다.

### 1.3 그래서 문제는 셋이다

| # | 문제 | 누가 아픈가 | 지금 모습 |
|---|---|---|---|
| P1 | **비용**: 호출마다 끌고 다니는 컨텍스트가 크다 | Lead(그리고 장수 lane 전부) | 재개 세션 안 히스토리 중복, 큰 도구 출력 누적, 압축 늦음 |
| P2 | **상태 부재**: "지금 무엇이 참인가"가 어디에도 없다 | 모든 에이전트·사람 | 사실·결정·현재 파라미터가 91만 자 detail 과 대화에 흩어짐. 결정 기록은 18건뿐이고 "현재 값"(예: `driftFlip` 임계)·"누가 무엇을 맡았나"·"폐기된 안"은 기록 대상이 아니다. 매 턴 원문 더미에서 에이전트가 상태를 **다시 추론** |
| P3 | **회상**: 최근 50 + 이 미션 밖은 닿기 어렵다 | 긴 방, 다른 미션·다른 방을 참고할 때 | `room messages` 는 필터뿐, `room read --query` 는 최근 200개 부분 문자열 |

키워드 검색(`room search`)은 P3 의 일부만 푼다. P1·P2 가 근본이다.

---

## 2. 최신 동향 (2025~2026) — Colab 에 쓸 만한 것만

### 2.1 컨텍스트 엔지니어링 (Anthropic 공개 지침)

- **세 전략: 압축(compaction), 구조화된 노트(NOTES.md 같은 컨텍스트 밖 기록), 하위 에이전트(깨끗한 창에서 깊게 탐색하고 압축된 요약만 반환).** — *Effective context engineering for AI agents*.
- **멀티에이전트 리서치 시스템**: 토큰 사용량이 성능 분산의 80%를 설명, 멀티에이전트는 채팅 대비 ~15배 토큰. 리드는 **계획을 메모리에 저장**해 컨텍스트가 잘려도 잃지 않고, 컨텍스트가 차면 **깨끗한 새 하위 에이전트를 띄우며 인계(handoff) 요약으로 연속성**을 잇는다. 하위 에이전트 산출물은 **리드를 거치지 않고 아티팩트로 저장하고 가벼운 참조만 돌려준다**("telephone game" 방지). 위임 시 목표·출력 형식·도구·경계를 명시.
- **API 의 context editing**(`context-management-2025-06-27` 베타): `clear_tool_uses_20250919`(오래된 도구 결과를 자리표시자로 치환, `trigger`·`keep`·`clear_at_least`·`exclude_tools`), `clear_thinking_20251015`, 서버측 압축 `compact_20260112`, 그리고 **memory tool**(`memory_20250818`, 파일형 메모리 디렉터리를 에이전트가 직접 읽고 씀). 도구 결과를 지우면 캐시 접두가 무효화되므로 `clear_at_least` 로 한 번에 크게 지우라고 권한다.
- **Claude Code** 는 자동 압축을 하며 `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` 로 임계를 **낮출** 수만 있다(기본 ~83%, 올릴 수 없음 — 이슈 #31806, 커뮤니티 보고). Colab 의 claude_code 는 ACP 어댑터를 거치므로 API 의 context editing 을 직접 켤 수는 없고, 데몬이 env·세션 수명으로만 조정할 수 있다(추정 — 어댑터 옵션 미확인).
- **Claude Code 는 초기에 RAG+로컬 벡터 DB 를 썼다가 "에이전트형 검색(grep/glob/read)"이 크게 낫다고 보고 버렸다**(Boris Cherny). 보안·신선도·신뢰성 문제도 이유. → Colab 의 회상 도구도 "벡터 DB 가 알아서 맞춰 주는 RAG 주입"이 아니라 **에이전트가 부르는 도구**여야 하고, 정확 일치(id·이름·수치)를 잘해야 한다.

### 2.2 구조화된 에이전트 메모리

| 계열 | 핵심 아이디어 | Colab 에 가져올 것 | 가져오지 않을 것 |
|---|---|---|---|
| **Letta (MemGPT)** | 컨텍스트 창 안의 **라벨 붙은 memory block**(글자 상한, 읽기/쓰기 권한) + 밖의 archival/recall 메모리. **공유 블록**을 여러 에이전트가 붙이고, **sleep-time 에이전트**가 백그라운드에서 블록을 정리 | 방·미션 단위 **공유 블록 = 상태 원장**, 블록별 상한, 백그라운드 정리자 | Letta 서버 자체(에이전트 런타임을 Letta 가 소유하는 모델 — Colab 은 Claude Code/Hermes 를 그대로 쓴다) |
| **Mem0** | 대화에서 **원자적 사실**을 추출해 저장·검색, 조회당 ~7천 토큰으로 전체 컨텍스트(2.5만+) 대비 절감. 2026 알고리즘은 **ADD-only**(덮어쓰기·삭제 없이 이력 보존, 시간 추론으로 최신을 재정렬) | 항목 단위 사실, **덮어쓰지 말고 대체 사슬로**, 출처 보존 | 외부 서비스·LLM 이 매 메시지마다 추출하는 경로(비결정·반출) |
| **Zep / Graphiti** | **시간 지식그래프** — 사실(엣지)에 `valid_at`/`invalid_at`, 새 사실이 옛 사실을 무효화. LongMemEval 에서 지식 갱신·시간 추론에 강하다고 보고 | **유효기간·"무엇이 무엇을 대체했나"** 를 칼럼으로 | Neo4j/FalkorDB 같은 그래프 저장소 |
| **ACE (Agentic Context Engineering)** | 컨텍스트를 **항목 단위 "플레이북"** 으로 키운다. 요약을 통째로 다시 쓰면 **brevity bias**(세부 탈락)와 **context collapse**(반복 재작성으로 세부 소실)가 생긴다 → **증분 델타 업데이트**가 성능 차의 대부분 | 원장은 **통째 재작성 금지, 항목 추가·대체·폐기만** | — |
| **LangMem / OpenAI 메모리** | 의미·일화·절차 메모리 구분, 핫패스(대화 중 도구로 기록) vs 백그라운드(나중에 추출) | "누가 쓰나"의 두 경로를 둘 다 둔다 | 사용자 개인화 중심 설계 |

벤치마크 한 줄: 공개 수치는 대부분 **제품 회사 자체 측정**이고(Mem0 LoCoMo 92.5·LongMemEval 94.4, HydraDB 90.8 등), 독립 비교(Zep 63.8 vs Mem0 49.0, GPT-4o)와 크게 다르다. **Colab 은 남의 벤치마크가 아니라 자기 방의 문항으로 재야 한다**(§5). 또 한 EMNLP 평가에서 RAG 와 롱컨텍스트가 60% 넘는 질의에서 같은 답을 냈다 — "적게 넣고도 맞히는" 여지는 크다.

### 2.3 의미 검색 (pgvector 하이브리드)

- 표준 모양: **BM25(또는 전문 검색) + 벡터, RRF(k=60)로 합산**, 필요시 재랭커. Postgres 안에서 `pgvector` + `pg_textsearch`(Tiger, BM25) 또는 ParadeDB `pg_search`. 한국어 토큰화는 이들 확장에서 확인하지 못했다 → **트라이그램(pg_trgm)을 어휘 쪽으로 쓰는 것이 안전**(조사 흡수).
- 한국어 임베딩: **BGE-M3**(다국어, 8K 입력, 자체 호스팅 가능, dense+sparse 동시), **Qwen3-Embedding**(다국어 MTEB 상위, 한국어 강함, 큰 모델), Kakao **Kanana-Nano-2.1B-Embedding**(경량). API: OpenAI `text-embedding-3-small` $0.02/M, Voyage `voyage-4-lite` $0.02/M·`voyage-4` $0.06/M(2억 토큰 무료), Gemini Embedding 2 $0.20/M. (Anthropic 은 자체 임베딩 API 가 없고 Voyage 를 권한다.)
- **비용은 무시할 수준**: 게임 제작 방 전체 ≈ 109만 자 ≈ 50만~70만 토큰 → API 로 **$0.01~0.05**. 막는 이유는 비용이 아니라 **데이터 반출**(외부 API)과 **운영 부품**(자체 호스팅이면 추론 서버).

### 2.4 멀티에이전트 공유 상태

- **블랙보드**(중앙 공유 게시판에 요청·중간 결과를 올리고 에이전트가 가져가는 구조)가 2025~26 연구에서 다시 쓰인다. 공유 메모리 연구들이 공통으로 꼽는 요건: **출처(provenance) 표시, 버전, 역할별 쓰기 권한, 일관성 창**. Colab 의 방 타임라인은 이미 블랙보드의 "게시판" 절반이고, 빠진 건 **정리된 상태 면**이다.

---

## 3. 방향별 비교

평가 축: **회상**(옛 사실을 맞게 찾나) · **비용**(Lead 턴당 토큰) · **일관성**(에이전트들이 같은 "현재 사실"을 보나) · E12-11·결정성 · 권한 · Go+PG 구축량 · 런타임 LLM 비용 · 위험.

| 방향 | 회상 | 비용 | 일관성 | E12-11·결정성 | 권한(방 격리·FR-4.5) | 구축(Go+PG) | 런타임 LLM | 위험 |
|---|---|---|---|---|---|---|---|---|
| **A. 키워드 검색**(pg_trgm, 기각안) | 중(정확 일치만) | ✕ | ✕ | ◎ | ◎ | 작음 | 0 | 말이 다르면 못 찾음 — Director 기각 |
| **B. 하이브리드 의미 검색**(pgvector+트라이그램, RRF) | **상**(긴 꼬리·다른 말) | △(찾으러 통째로 읽기만 줄임) | ✕ | ◎ 턴 프롬프트 밖 도구. 임베딩 모델·버전 고정이면 재현 | ◎ SQL `WHERE room_id` + 기존 `rooms.Decide` | 중(확장·청크 표·임베딩 작업자) | 임베딩만(≈0) | 외부 API 반출 / 자체 호스팅 운영, 청크 경계 |
| **C. 상태 원장**(방·미션 항목 메모리, 에이전트가 도구로 유지) | **상**(핵심 사실은 항상 눈앞) | **상**(원문 더미 대신 짧은 원장을 싣는다) | **◎**(한 곳의 "현재 사실") | ○ 원장은 **턴 프롬프트** 쪽(버전 번호로 렌더 결정적). [1]~[5] 불변 | ◎ 방·미션 행, 사람이 보고 고침 | 중(표 1~2개·op 4개·CLI/MCP·렌더) | 에이전트가 턴 중 수백 토큰 | 에이전트가 안 쓰거나 틀리게 씀 → 브리프 규칙·정리자·사람 교정 |
| **C′. 자동 정리자**(백그라운드 LLM 이 원문→원장 항목 제안, sleep-time) | 상 | 상 | ○ | △ 비결정 — 단 **결과는 행**으로 저장돼 이후는 결정적(「여기까지 정리」와 같은 성격) | ○ 방 단위로만 실행 | 중 | 미션 종료·N건마다 Haiku/Sonnet 급, 회당 센트 단위 | 환각 사실 → 출처 필수·"자동" 표식·사람 되돌리기 |
| **D. 시간 지식그래프**(Zep/Graphiti 등) | 상(관계·시간 질의) | 중 | ○ | ✕ 추출 비결정, 외부 부품 | ✕ 그래프 단위 격리 재구현 | 큼(그래프 DB 또는 사이드카) | 메시지마다 추출 | 선행 보고의 graphify 기각 사유와 동일 |
| **E. 외부 메모리 서비스**(Mem0·Letta 서버) | 상 | 중 | ○ | ✕ | ✕(사용자/에이전트 id 모델, 방 originator 권한 모름) | 연동은 작지만 운영 의존 큼 | 서비스 과금 | 반출·락인·권한 불일치 |
| **F. 컨텍스트 수명주기**(재개 델타 프롬프트·압축 임계·세션 교체) | 중(교체 시 원장이 이어 줌) | **◎ 가장 직접** | ○ | ○ 델타는 결정적(마지막 본 메시지 id 기준). [1]~[5] 불변 | 무관 | 작음~중(bundle·resume·데몬 env) | 0 | 델타만 받으면 에이전트가 앞일을 잊을 수 있음 → 원장·포인터로 보완, 압축 요약 품질 |
| **G. 하위 에이전트 격리 / Lead 역할 규율** | 무관 | **상**(Lead 셸 1,351회 결과가 Lead 세션에 안 쌓임) | ○(아티팩트로 전달) | 무관 | 무관 | 작음(브리프 [3] 문구·QA 역할) | 하위 에이전트 몫(대신 작은 창) | 위임 비용·지연, 조정 과소 |
| **H. 롱컨텍스트에 그냥 다 넣기** | 중~상 | ✕✕(지금 모습) | △ | ○ | ◎ | 0 | 최대 | 지금의 $2,606 |

**조합의 효과 지도**: 비용 = F+G(+C 가 턴 프롬프트를 줄임) · 일관성 = C(+C′) · 회상 = C(핵심) + B(긴 꼬리).

---

## 4. 권고 아키텍처 — "원문은 증거, 원장은 상태, 창은 작게"

```
            ┌──────────── 턴 프롬프트 (서버가 결정적으로 렌더) ────────────┐
 [1]~[5]    │ <room_memory v=N>  방·미션 원장(현재 유효 항목, 상한 ~6천 자)   │  cold start: 전체
 바이트 동일 │ <memory_changes since=vK>  (재개 턴: 지난 턴 이후 바뀐 항목만)   │  resume: 델타
 (불변)     │ <history …>  cold: 최근 N / resume: 이 lane 이 마지막 본 뒤의 것  │
            │ <trigger> …                                                   │
            └──────────────────────────────────────────────────────────────┘
 도구:  colab memory note|supersede|retire|get      (상태 원장 — 쓰기)
        colab room recall --query …                 (하이브리드 회상 — 읽기)
        colab room messages --thread …              (원문 전문 — 기존)
 저장:  message / decision / artifact  (L0 증거, 기존)
        memory_item  (L1 상태 원장, 새로)
        recall_chunk (L2 색인: 트라이그램 + pgvector, 새로)
 백그라운드: 정리자(C′) — 미션 종료·N건·큰 detail 때 원장 항목 "제안"
```

### 4.1 L1 상태 원장 (`memory_item`)

| 칸 | 뜻 |
|---|---|
| `id`, `room_id`, `work_id?` | 방 전체 항목이면 `work_id` 없음 |
| `kind` | `fact`(현재 참인 값·사양) · `decision`(기존 decision 과 연결) · `constraint` · `owner`(누가 무엇을 맡음) · `open_question` · `artifact_ref`(최신 산출물 포인터) · `lesson`(해 봤더니 안 된 것 — 예: "시간 임계 driftFlip 0.18/0.30/0.45 는 전부 실패") |
| `key` | 선택. 같은 key 의 새 항목이 옛 항목을 대체(예: `game.drift.flip_rule`) |
| `body` | **≤ 300자** 한 항목(ACE: 통째 재작성 금지, 항목 단위) |
| `status` | `active` · `superseded`(→`superseded_by`) · `retracted` |
| `source_message_ids[]`, `source_artifact_id?` | **출처 필수** — 원문으로 돌아가는 길(회상 정확도의 근거) |
| `author_type/id`, `origin` | agent · human · consolidator(자동, 화면에 표식) |
| `created_at`, `valid_from`, `invalidated_at` | Zep 식 시간 — "그때는 무엇이 참이었나"도 답한다 |
| `room_version` | 방 단위 단조 증가 번호 — 델타 렌더·골든 테스트의 기준 |

- **쓰는 쪽(핫패스)**: 에이전트가 턴 안에서 `colab memory note --kind fact --key … --body … --source <msg>` / `supersede <id>` / `retire <id>`. 브리프 [2] 에 **고정 문장**(표면별 두 벌, E12-11 유지): "결정·확정된 값·맡은 일·포기한 안이 생기면 memory 에 한 줄로 남겨라. 긴 결과는 detail/아티팩트에, 원장엔 결론과 출처만." — `colab decision record` 는 `kind=decision` 항목도 함께 만든다(기존 FR-4.2 그대로 유지).
- **역할**: 모든 역할이 `note` 가능, `supersede/retire` 는 작성자·Lead·사람(K-19 역할 표에 한 줄). 충돌(두 에이전트가 같은 key 를 동시에)은 **나중 커밋이 이기되 둘 다 이력에 남고**, 같은 턴 안의 경합은 `room_version` 낙관적 잠금으로 409 → 에이전트가 다시 읽고 판단.
- **렌더**: 턴 프롬프트의 `<room_memory>` — active 항목만, kind 순·key 순(결정적), 상한 초과 시 오래된 `fact` 부터 "…외 N건 — `colab memory get`" 한 줄(FR-4.1 잘림 고지와 같은 원칙). **[1]~[5] 에 넣지 않는다**(원장은 턴마다 바뀐다).
- **부재≠장애**(FR-4.2): 원장 조회 실패면 구간을 빼고 활동에 오류.
- **사람**: 방 우열(사이드)에 「방 메모」 — 항목·출처 링크·자동 표식, 사람이 고치고 고정(pin)·폐기. FR-2.5 「여기까지 정리」는 이 원장의 한 모양(요약 항목)으로 흡수 가능.
- **다른 방 읽기(FR-4.5)**: `room read` 의 "요약" 자리에 대상 방의 active 원장을 싣는다 — 권한 판정·양쪽 기록·4천 토큰 상한은 그대로. 지금의 "최근 200개 부분 문자열"보다 훨씬 밀도가 높다.

### 4.2 L2 하이브리드 회상 (`colab room recall`)

- 색인 대상: 메시지 content, **detail 을 ~1,500자 청크(문단 경계, 겹침 200자)**, 원장 항목(폐기 포함, 표식), 결정, 아티팩트 이름·텍스트 본문 앞부분.
- 질의: 트라이그램 유사도(어휘) + pgvector 코사인(의미) → **RRF(k=60)** → 상위 k(기본 8). 재랭커는 3단계에서 문항 결과를 보고 결정.
- 출력: 조각 ±300자·메시지 id·작성자·시각·미션·스레드·"원장 항목 여부/폐기 여부" — 전문은 기존 `room messages --thread`.
- 임베딩: **모델·차원·버전을 칼럼에 고정**(재현성). 선택지 — ① API(`voyage-4-lite`/`text-embedding-3-small`, 방 전체 몇 센트) ② 자체 호스팅 BGE-M3(반출 없음, 운영 부품 +1). → Director 질문 Q3.
- 쓰기 경로: 메시지 커밋 후 비동기 작업자(서버 내부 goroutine + `recall_chunk.embedded_at IS NULL` 폴링). 임베딩 전에도 **트라이그램은 커밋 즉시** 찾힌다(신선도).
- 권한: `WHERE room_id = $1` + 기존 `rooms.Decide`; 다른 방 회상은 FR-4.5 판정을 **호출 순간** 다시 하고 기록(V19-C).

### 4.3 L0/창 — 컨텍스트 수명주기 (비용의 본체)

1. **재개 턴 델타 프롬프트**: lane 에 `last_seen_message_id`·`last_seen_memory_version` 을 두고, **resume 이면** `<history>` 는 그 뒤의 메시지만, 원장은 `<memory_changes>` 만 싣는다. **cold start 면** 지금처럼 전체(+원장 전체). `PlanAttempt` 에 입력 두 칸이 늘 뿐이라 골든 표 확장으로 고정 가능.
2. **미션 메시지 전부 → 원장 + 포인터**: `<mission_messages>` 는 원장이 자리 잡은 뒤 "최근 N + 원장 + `recall`" 로 줄인다(2단계 이후, 문항 점수로 판정).
3. **세션 크기 상한**: 데몬이 claude_code 에 `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`(예: 1M 창의 20~25% ≈ 20만~25만 토큰)를 준다, 또는 서버가 lane 누적 컨텍스트(턴 중 usage `used`)가 임계를 넘으면 **다음 task 를 계획적 cold start**(새 세션 + 원장 전체 = Anthropic 의 "handoff 로 새 창")로. 원장이 있어야 이 교체가 안전하다 — **(나)가 (가)의 전제**.
4. **Lead 의 무거운 검증을 격리**: 브리프 [3] 조정 프로토콜에 "브라우저 테스트·미디어 처리·대량 파일 확인은 QA/담당 에이전트에게 위임하거나 런타임의 하위 에이전트(Claude Code Task)로 돌리고 결과는 요약·아티팩트로 받는다" 한 줄. 필요시 Lead 역할의 셸 허용 범위를 줄이는 것은 별도 결정(Q2).
5. (hermes) 같은 원리 — 델타·원장은 서버 쪽이라 표면 무관. 압축은 hermes 자체 회전(provenance `reason: compression`)에 맡긴다.

---

## 5. 단계 계획과 성공 지표

### 0단계 — 계측과 문항 (워커 1, 수일) · **다른 모든 판정의 기준**

- **호출당 컨텍스트 분해**: 데몬이 이미 받는 턴 중 usage/`usage_update {used,size}` 를 호출 단위로 `task_event` 에 남기고(닫힌 스키마 — 기존 `usage` 칸 안), 턴 프롬프트 바이트 수·구간별(history/mission/decisions) 바이트 수를 번들 생성 시 기록. → "재개 세션 안 히스토리 중복" 가설 확인/기각.
- **회상 문항 세트 v1**: 게임 제작 방에서 **30문항**(사람이 정답 메시지 id 를 붙인다). 유형 5종 × 6: ① 결정과 그 근거 ② **현재 값**(갱신된 사실 — 예: 드리프트 판정 규칙의 최신안) ③ 폐기된 안과 이유 ④ 누가 무엇을 맡았나/어느 아티팩트가 최신인가 ⑤ 다른 미션(마리오 카트)에서 가져올 것. 평가: 새 cold-start 에이전트에게 번들 그대로 주고 답 + 인용 id 채점(정답·인용 둘 다).
- **기준선 수치**(이미 있는 것): Lead task 당 중앙 $10.00, 호출당 cache_read 중앙 38.9만, task 당 도구 호출 36. 추가로 턴당 `room_messages --thread` 호출 수, 같은 메시지 전문 재독 수.

### 1단계 — 수명주기 (가) (서버 1 + 데몬 1)

- 재개 델타 `<history>`, 압축 임계 env, Lead 브리프 [3] 위임 문장.
- **지표**: Lead 호출당 cache_read 중앙 **−50% 이상**(38.9만 → ≤ 19만), Lead task 당 중앙 USD **−35% 이상**, 문항 점수 **하락 없음**(±1문항). 재현: 게임 제작 방 과업 2~3개를 acpfake 가 아닌 실기로 A/B(같은 지시, dev 바이너리 before/after — 기존 레시피).

### 2단계 — 상태 원장 (나) (서버 1 + CLI/MCP 1 + 웹 1)

- `memory_item` 표·op 4개(`noteMemory`·`supersedeMemory`·`retireMemory`·`listMemory`)·CLI/MCP·브리프 [2] 문장·턴 렌더·방 메모 화면. 기존 방은 **1회 백필**(정리자 프롬프트로 제안 → 사람 확인).
- **지표**: 문항 v1 정답률 **≥ 80%**(기준선 대비 +20%p 이상 목표 — 기준선은 0단계에서), 유형 ②(현재 값) **≥ 90%**, 인용 정확도 ≥ 80%. 원장 커버리지: 미션 종료 시 결정·확정 값의 ≥ 80% 가 원장에 있음(사람 표본 검수). 턴 프롬프트 바이트 **−40%**(미션 메시지 축소 후).

### 3단계 — 하이브리드 회상 (다) (서버 1 + CLI/MCP 1)

- `recall_chunk`·pgvector·임베딩 작업자·`room recall`·`room read` 내부 교체.
- **지표**: 문항 중 원장 밖 긴 꼬리(유형 ③⑤ + 신규 10문항) **recall@5 ≥ 0.8**, 트라이그램만 대비 +10%p 이상이면 벡터 유지(아니면 벡터를 빼고 단순하게 — **데이터로 결정**). p95 < 150ms. 턴당 `room messages` 전문 재독 −50%.

### 4단계 — 자동 정리자·운영 (선택)

- 미션 종료·50건마다·detail > 1만 자 게시 때 Haiku 급 정리자가 원장 항목 **제안**(출처 필수, `origin=consolidator`, 사람 화면에 표식, 되돌리기). 비용 목표: 방당 월 < $1.
- 지표: 정리자 제안 채택률, 잘못된 자동 항목 신고 수.

---

## 6. Director 에게 물을 것

1. **원장 쓰기 주체**: 에이전트가 턴 중에 직접 쓰는 것(권고)을 기본으로 하고, 자동 정리자는 **제안만**(사람/Lead 확인) 할까, 아니면 자동으로 바로 반영(표식만)할까?
2. **Lead 의 역할 규율**: Lead 가 직접 브라우저 테스트·미디어 처리를 하는 것을 브리프로 "권고"만 할까, 역할 허용 명령(K-19)으로 **막을까**? (게임 제작 방 Lead 셸 1,351회)
3. **임베딩 경로**: 방 내용을 외부 임베딩 API(Voyage/OpenAI, 방당 몇 센트)로 보내도 되나, 아니면 자체 호스팅(BGE-M3, 운영 부품 +1)만 허용하나? (PRD §9 반출 원칙)
4. **재개 세션 교체 정책**: 세션이 일정 크기를 넘으면 계획적 cold start(새 세션 + 원장)로 바꾸는 것을 허용하나? 대가: 런타임이 기억하던 미묘한 작업 맥락 일부 상실.
5. **「여기까지 정리」(FR-2.5)와 결정 기록(FR-4.2)** 을 원장의 모양으로 합칠까(화면 하나), 별도로 둘까?
6. **문항 세트 정답 달기**: 30문항 정답 id 를 누가 붙이나(Director 1시간 vs 에이전트 초안 + Director 검수)?

---

## 7. 확인하지 못한 것 · 추정

- **"재개 세션 안 히스토리 중복이 Lead 비용의 주범"은 가설**이다. 근거는 (a) 재개 턴에도 전체 히스토리를 싣는 코드, (b) 호출당 ~39만 토큰, (c) room_messages 12회뿐. 호출별 컨텍스트 구성(프롬프트 vs 도구 결과)은 분해하지 않았다 — 0단계 첫 항목.
- 턴 프롬프트 한 벌 9만~12만 토큰은 **한국어 1.5~2자/토큰 가정**의 추정. 실측 아님.
- Claude Code 자동 압축의 1M 창 실제 임계, ACP 어댑터 경유 시 `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` 가 먹는지 — 미확인(커뮤니티 이슈·블로그 근거, 공식 문서 대조 안 함).
- 메모리 제품 벤치마크(Mem0 92.5/94.4, HydraDB 90.8, Zep 63.8 vs Mem0 49.0)는 **회사 자체·2차 보도 수치**이고 재현하지 않았다. 설계 근거로만 쓰고 판정 근거로 쓰지 않는다.
- pg_textsearch·ParadeDB 의 한국어 토큰화 지원 — 확인 못 함(그래서 트라이그램 권고).
- 한국어 임베딩 모델 순위(BGE-M3 vs Qwen3-Embedding vs Kanana)는 2차 비교 글 기반 — Colab 문항으로 재야 한다.
- 원장 방식의 "에이전트가 실제로 성실히 쓰는가"는 실기 관측 없음 — 2단계에서 커버리지 지표로 확인.
- 실사용 DB 는 읽기 전용 SELECT 만 했고 행 수가 선행 보고 이후 늘어(194→218 메시지, Lead 51→60 task) 수치가 다르다.

---

## 출처

- Anthropic, [Effective context engineering for AI agents](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)
- Anthropic, [How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
- Claude Docs, [Context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing) · [Cookbook: memory, compaction, tool clearing](https://platform.claude.com/cookbook/tool-use-context-engineering-context-engineering-tools)
- Claude Code 자동 압축: [issue #31806](https://github.com/anthropics/claude-code/issues/31806), [issue #36381](https://github.com/anthropics/claude-code/issues/36381)
- Boris Cherny, [agentic search vs RAG](https://x.com/bcherny/status/2017824286489383315)
- Letta, [Memory blocks](https://docs.letta.com/guides/core-concepts/memory/memory-blocks) · [Shared memory](https://docs.letta.com/guides/core-concepts/memory/shared-memory/) · [Sleep-time agents](https://docs.letta.com/guides/agents/architectures/sleeptime/)
- Mem0, [Research](https://mem0.ai/research) · [Token-efficient memory algorithm](https://mem0.ai/blog/mem0-the-token-efficient-memory-algorithm) · [State of AI Agent Memory 2026](https://mem0.ai/blog/state-of-ai-agent-memory-2026)
- Zep, [A Temporal Knowledge Graph Architecture for Agent Memory (arXiv 2501.13956)](https://arxiv.org/pdf/2501.13956) · [Mem0 vs Zep 비교](https://vectorize.io/articles/mem0-vs-zep)
- ACE, [Agentic Context Engineering (arXiv 2510.04618)](https://arxiv.org/abs/2510.04618)
- 하이브리드 검색: [ParadeDB hybrid search manual](https://www.paradedb.com/blog/hybrid-search-in-postgresql-the-missing-manual), [pg_textsearch](https://www.tigerdata.com/blog/introducing-pg_textsearch-true-bm25-ranking-hybrid-retrieval-postgres), [pgEdge BM25+RRF](https://www.pgedge.com/blog/hybrid-search-in-postgresql-bm25-sparse-vectors-and-reciprocal-rank-fusion)
- 임베딩: [BentoML open-source embedding models 2026](https://www.bentoml.com/blog/a-guide-to-open-source-embedding-models), [Voyage pricing](https://docs.voyageai.com/docs/pricing), [embeddingcost.com](https://embeddingcost.com/)
- 공유 메모리: [Governed Shared Memory for Multi-Agent LLM Systems](https://arxiv.org/html/2606.24535v1), [LLM Multi-Agent Blackboard System](https://arxiv.org/html/2510.01285v1), [Memory in the Age of AI Agents](https://arxiv.org/pdf/2512.13564)
- LongMemEval: [arXiv 2410.10813](https://arxiv.org/pdf/2410.10813)
