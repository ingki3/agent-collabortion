# graphify 는 Colab 의 대화 맥락 관리를 대체·보강할 수 있는가

조사일 2026-09-27 · 대상 https://github.com/Graphify-Labs/graphify (기본 브랜치 `v8`, 최신 릴리스 v0.9.69 2026-09-26)
Colab 쪽 근거: `PRD.md` FR-3.1.2·FR-3.1.4·FR-4.1~4.5·§8.4, `contracts/harness.md` §10, `contracts/colab-cli.md`, `contracts/openapi.md` D23~D26, `server/internal/queue/bundle.go`·`brief_room.go`, `server/internal/rooms/read.go`, 실사용 DB `colab-pg-g6` 읽기 전용 조회.

---

## 0. 결론 먼저

**지금은 도입하지 않는다(don't adopt now). 대신 Postgres 검색(`colab room search`)을 먼저 만들고, graphify 는 그 뒤에 "방 지식 지도"용 오프라인 실험으로만 남긴다.**

- graphify 의 강점(tree-sitter 로 **코드**를 LLM 없이 그래프로 만든다)은 Colab 의 문제(한국어 **대화·작업 내용·결정**의 회상)와 잘 맞지 않는다. 대화·문서는 LLM 추출 경로를 타고, 그러면 비결정적·비동기·Python 사이드카가 된다.
- 대화형 메모리 벤치마크(LOCOMO 45.3% 등) 수치는 **공개 저장소에 없는 하네스·SurrealDB 엔진·BGE-m3 임베더**로 잰 것이라 재현·검증할 수 없었다. README 는 "임베딩 없음"이라 말하고 벤치마크는 임베더를 쓴다 — 서로 어긋난다.
- Colab 의 실측 비용은 **턴 사이 히스토리**가 아니라 **턴 안 도구 루프의 누적 컨텍스트(cache_read)** 에 쏠려 있다(§2.4). 어떤 검색 도구도 이 비용을 직접 줄이지는 못한다. 검색이 줄일 수 있는 것은 "에이전트가 옛 맥락을 찾으려 통째로 다시 읽는" 부분뿐이다.
- 그 부분은 `pg_trgm`/전문 검색 한 벌로 **서버 안에서, 권한 모델 그대로, 결정적으로** 해결된다. 가치 대비 비용이 가장 좋다.

---

## 1. graphify 는 무엇인가

| 항목 | 확인한 사실 |
|---|---|
| 정체 | "코드베이스(+문서·SQL·설정·PDF)를 질의 가능한 지식 그래프로" 만드는 **Python 라이브러리 + AI 코딩 도구용 `/graphify` 스킬**. Claude Code·Cursor·Codex·Gemini CLI 등 20여 개 플랫폼 설치기 |
| 성숙도 | 저장소 생성 **2026-04-03**(약 6개월), ★ 121,787 · 포크 11,726 · 열린 이슈 **1,486** · 커밋 1,961 · 기여자 30명 중 1명이 1,115 커밋(사실상 1인 주도). 릴리스는 거의 매일(v0.9.65→0.9.69 가 6일). YC S26 회사(Graphify Labs)이며 상용 플랫폼(app.graphify.com, "always-on, meetings 포함") 조기 접근 중 |
| 라이선스 | Apache-2.0 (+ LICENSE-MIT 파일, README 는 이중 라이선스라 표기) — 상용 사용에 문제없음 |
| 언어·런타임 | Python 3.10+, PyPI `graphifyy`(y 두 개). NetworkX, tree-sitter 37개 문법, 선택 extras(leiden, mcp, video, ollama/openai/anthropic/bedrock/azure) |
| 파이프라인 | `detect → extract → build(NetworkX) → cluster(Leiden) → analyze → report → export` (ARCHITECTURE.md) |
| 그래프 구축 | **코드**: tree-sitter AST, 결정적, LLM 없음. **문서·PDF·이미지·영상**: LLM 의미 추출(파일 청크마다 호출, 잘리면 이등분 재시도 최대 8배). 백엔드는 IDE 세션 모델 또는 Gemini/Kimi/Claude/OpenAI/DeepSeek/Azure/Bedrock/Ollama/`claude-cli` |
| 노드·엣지 | 노드 = 개념·심볼(`{id,label,source_file,source_location}`), 엣지 = `calls/imports/references/conceptually_related_to/semantically_similar_to/rationale_for…` + `EXTRACTED/INFERRED/AMBIGUOUS` 신뢰 태그, 하이퍼엣지 |
| 저장 | **파일 하나(`graphify-out/graph.json`)** + 캐시·manifest. 내보내기: HTML, GraphML, Neo4j Cypher, FalkorDB, Obsidian, wiki. DB 서버 없음 — 질의 시 JSON 을 메모리(NetworkX)에 올린다 |
| 질의 | CLI `graphify query/path/explain`, MCP 서버(stdio 또는 Streamable HTTP, `--api-key` 하나): `query_graph`, `get_node`, `get_neighbors`, `shortest_path`, `god_nodes` 등. `query_graph` 는 **어휘 매칭(IDF 가중 + 트라이그램 전처리)으로 씨앗 노드를 찾고 BFS/DFS 로 부분 그래프를 토큰 예산 안에서 반환**. 임베딩 없음 |
| 증분 | `graphify update` — manifest 로 바뀐 파일만 재추출. 코드는 무료, 문서는 LLM 재호출. `--watch` 는 코드만 자동, 문서는 `needs_update` 플래그만 |
| 다국어 | 질의 토크나이저가 **중국어만 분절**, 그 밖은 `\w+`. 한국어 조사("방을", "결정은")는 트라이그램 부분 매칭에 기대야 한다(실측 안 함) |
| 다중 테넌트 | 없음. 서버 하나 = 그래프 파일 하나(멀티 프로젝트 MCP 는 최대 8 컨텍스트 캐시). 노드 단위 권한·가시성 개념 없음 |
| 기타 | 모든 질의를 `~/.cache/graphify-queries.log` 에 기록(끌 수 있음). 로컬 스킬 `~/.claude/skills/graphify/SKILL.md` 는 **v0.4.23**, 설치된 패키지는 **0.8.49**, 최신은 0.9.69 — 같은 도구의 옛 스킬이다(서브에이전트로 의미 추출, `--update`, `--mcp` 등 동일 개념) |

### 벤치마크 주장과 검증 가능성

`BENCHMARKS.md`(2026-07-05): LOCOMO QA 45.3% / recall@10 0.497(BM25 0.362, mem0 0.048), LongMemEval-S 76%(dense RAG 와 동률), 그래프 구축 LLM 비용 $0, 메모리 적재 ~$1.40.

- 재현 명령(`python memory/runner.py … --adapters graphify_v1_surreal`)의 **`memory/` 디렉터리가 공개 트리에 없다**. SurrealDB 엔진·BGE-m3 임베더도 패키지에 없다. → **검증 불가**.
- 같은 표에서 hybrid RRF(BM25+dense) 43.3%, dense RAG 41.3% — graphify 의 우위는 2~4점이고, LongMemEval 에서는 dense RAG 와 같다. 즉 "대화 회상"에서 **평범한 하이브리드 검색과 큰 차이가 없다**는 것이 그들 자신의 수치다.

---

## 2. Colab 이 지금 맥락을 다루는 방식

### 2.1 한 턴에 에이전트가 받는 것

**안정 브리프 [1]~[8]**(§8.4, harness §10) — claude_code 는 `_meta.systemPrompt.append`, hermes 는 `COLAB_BRIEF.md`.
[1] 정체·지시 [2] 규칙·멘션 문법·허용 CLI/MCP [3] (lead) 조정 프로토콜 [4] 방·미션 goal/종료 조건 [5] 로스터(에이전트+사람) [6] 컨텍스트 [7] 결정 기록(최근 20건) [8] 지시 우선순위.
**[1]~[5] 는 같은 방·같은 미션 안에서 바이트 동일(E12-11)** — 프롬프트 캐시 접두. 턴마다 바뀌는 값은 턴 프롬프트로 뺀다.

**턴 프롬프트**(`bundle.go`): `<rebind>`? → `<resumed>`? → 잘림 한 줄 → `<history included total truncated>` → `<mission_messages>` → `<room_decisions>` → `<mission_progress>` → `<roster_status>` → `<folders>` → `<trigger>` → 응답 지시.

### 2.2 히스토리를 어떻게 자르는가 (FR-4.1 세 묶음)

1. **방 최근 50개**(`tasks.DefaultHistoryLimit = 50`, 스레드 답글 포함). 다른 메시지의 `detail` 은 **첫 400자 + "(작업 내용 N자 — `colab room messages --thread <id>` 로 전문)"**.
2. **그 턴이 속한 미션의 메시지 전부**(①보다 오래된 것, 200개씩 페이지로 끝까지).
3. **방 결정 기록 전부**(20건은 [7], 나머지는 `<room_decisions>`) + 가장 최근 「여기까지 정리」 요약.
- 잘렸으면 한 줄: "…이 미션의 N건은 <mission_messages> 에… 결정은 [7]/<room_decisions> 에… 나머지는 `colab room messages` 로".
- **트리거**는 `detail` 전문(턴 합계 5만 자 상한, 넘으면 히스토리 모양으로 강등). 부분 메시지(D26)는 자기 부분 + 다른 부분 한 줄.
- 부재≠장애: 결정·요약 조회 실패 시 그 구간을 **빼고** 활동에 오류를 남긴다(FR-4.2).

### 2.3 옛 맥락을 에이전트가 찾는 길

| 도구 | 동작 | 검색 능력 |
|---|---|---|
| `colab room messages [--since][--limit][--thread][--work][--group][--top-only]` | 같은 방 메시지 + `detail` 전문 | **없음** — 시간/스레드/미션 필터뿐. 찾으려면 페이지를 넘기며 읽는다 |
| `colab room read --room <id> [--tail N] [--query <말>]` | 다른 방의 요약 + 최근 메시지 + 결정 + 아티팩트 목록, 4,000 토큰·턴당 3방 상한, `detail` 제외, 읽은 사실을 양쪽 방에 기록 | `--query` 는 **최근 200개 안의 소문자 부분 문자열 필터**(`rooms/read.go` fill) |
| `colab room list [--query]` | 읽을 수 있는 방 | 이름·설명 `ILIKE` |
| `colab artifact get <id>` | 아티팩트 본문 | 없음(목록에서 이름으로 고른다) |
| 결정 | 브리프/턴 프롬프트에 전부 실린다 | 필요 없음(이미 다 들어감) |

서버 전체에 `tsvector`/`pg_trgm`/`pgvector` 사용은 없다(`ILIKE` 두 곳뿐).

### 2.4 아픈 곳 — 실측

실사용 DB(`colab-pg-g6`, 읽기 전용):

| 방 | 메시지 | content 합 | detail 합 |
|---|---|---|---|
| 게임 제작 방 | 194 | 13.9만 자 | **79.2만 자** |
| 주식 레포트 작성 방 | 18 | 0.65만 자 | 4.7만 자 |

게임 제작 방 비용(task_usage 합):

| 에이전트 | task | USD | input | cache_read | output |
|---|---|---|---|---|---|
| Developer | 19 | 673.5 | 1.20억 | 1.13억 | 68만 |
| Lead | 51 | 557.7 | 0.4만 | **6.01억** | 198만 |
| Designer | 15 | 542.2 | 0.99억 | 0.71억 | 52만 |
| Writer | 18 | 166.1 | — | 1.66억 | 61만 |
| Researcher | 12 | 129.2 | — | 1.19억 | 72만 |

- 방 하나에 약 **$2,069**. Lead 는 task 당 cache_read 약 1,180만 토큰 — 턴 프롬프트 한 벌(최근 50개 + 미션 메시지 + 결정 ≈ 수만 토큰)로는 설명되지 않는다. **턴 안에서 도구 호출을 거듭하며 같은 컨텍스트를 다시 읽는 누적**이 주범이다. STO 테스트에서도 Lead 최종 검수 1턴 ≈ $8.9(63KB 재독)였다.
- 방이 1.5천 건급으로 커진 사례는 지금 DB 에는 없다(최대 194건). 다만 `detail` 이 content 의 5.7배라 **"작업 내용 전문을 찾아 다시 읽기"** 가 비싼 행동이다.
- 다른 방 읽기의 `--query` 는 최근 200개 밖을 못 본다 — 방이 커지면 "3주 전 그 결정의 근거"를 못 찾는다.
- 요약은 사람이 누르는 「여기까지 정리」와 미션 종료 요약뿐 — 자동 요약이 없어 긴 방의 중간 구간은 `room messages` 페이지 넘기기로만 닿는다.

---

## 3. 적합성 평가 — Colab 필요별

| Colab 필요 | graphify 가 돕나 | 붙인다면 | 대체/보완 | 위험 |
|---|---|---|---|---|
| **턴별 맥락 조립**(브리프·`<history>`) | **아니오** | 넣지 말아야 한다 | — | 그래프 요약을 브리프에 넣으면 LLM 추출이 비결정적이라 **[1]~[5] 바이트 동일(E12-11)이 깨지고** 캐시가 날아간다. 턴 프롬프트 쪽이라도 "어떤 부분 그래프가 들어갔나"가 재현 불가 → 골든 테스트와 충돌 |
| **긴 히스토리 회상**(같은 방) | 부분적 | 서버가 방마다 메시지·detail·결정·아티팩트를 "파일"로 내보내 방별 `graph.json` 을 만들고 MCP `query_graph` 를 `colab room search` 로 감싼다 | 보완 | 메시지마다 LLM 추출 = 새 메시지가 들어올 때마다 비동기 재추출(신선도 지연). 노드가 "개념"이라 원 메시지 id 로 돌아오려면 `source_file`=메시지 id 규약이 필요. 한국어 토큰화 약함. 자신들 수치로도 BM25+dense 대비 +2점 |
| **다른 방 읽기**(FR-4.5) | 약함 | 읽기 권한 판정 뒤, 대상 방의 그래프에서 질의 | 보완 | **권한은 호출 순간 재검사**(V19-C)·읽은 사실 양쪽 기록·4,000 토큰 상한이 모두 Go 서버에 있다. graphify 는 그래프 파일 단위 접근뿐 — 방 단위 그래프를 따로 두고 Go 가 문지기가 되어야 한다. 방 경계를 넘는 "공동체 탐지"는 오히려 유출 경로 |
| **결정·아티팩트 찾기** | 거의 불필요 | — | — | 결정은 이미 전부 턴에 실린다. 아티팩트는 목록+이름이면 충분하고, 코드 아티팩트(diff)라면 graphify 의 AST 경로가 유일하게 강한 곳이지만 Colab 의 diff 는 저장소 전체가 아니다 |
| **에이전트의 CLI/MCP 검색** | 인터페이스는 맞다 | graphify MCP(HTTP) 를 colab MCP 옆에 두거나, 서버가 프록시 | 보완 | 두 번째 MCP 서버 = 역할별 허용 명령(K-19, `--allow`)·task 토큰 범위·hermes 의 `cli_wrapper`(hermes 는 `mcpServers` 를 무시) 전부를 다시 배선해야 한다. graphify HTTP 의 인증은 `--api-key` 하나뿐 |
| **사람의 보기** | 흥미롭지만 우선순위 낮음 | `graph.html`·wiki 를 방 설정에 "방 지식 지도"로 | 보완 | 제품 화면 언어(SCREEN.md 문구 자물쇠)와 별개인 외부 HTML. 사람에게 필요한 건 검색창과 요약이지 힘-유도 그래프가 아니다 |

### 공통 위험

- **LLM 추출 비용**: 게임 제작 방 전체(≈93만 자, 한국어 ≈50만 토큰 가정)를 Sonnet 급으로 한 번 추출하면 대략 $3~6(출력 비율·재시도에 따라). 방 비용($2,069) 대비 작다 — **비용은 막는 이유가 아니다.** 막는 이유는 아래다.
- **지연·신선도**: 메시지가 들어올 때마다 추출하면 턴 직전 메시지가 그래프에 없을 수 있다. 배치로 돌리면 더 늦다. 반면 SQL 검색은 커밋 즉시 보인다.
- **결정성**: 같은 입력에 다른 그래프 — 회귀 주입·골든 대조로 굴러가는 이 프로젝트의 검증 문화(§0-9·§0-12)와 맞지 않는다.
- **다중 테넌트**: 방 가시성·참여자·originator 권한을 graphify 는 모른다. 파일 격리로 흉내 내야 하고 실수 하나가 방 간 유출이다.
- **배포**: Go 단일 바이너리 + Postgres 스택에 Python 3.10+ 사이드카·uv·LLM 키·그래프 파일 디렉터리가 붙는다. 데몬(사용자 머신)에 두면 서버가 통제 못 하고, 서버에 두면 운영 부품이 는다.
- **성숙도**: 6개월, 1인 주도, 열린 이슈 1.5천, 거의 매일 릴리스, 상용 플랫폼으로의 무게 이동 — API 안정성을 기대하기 어렵다(로컬 스킬 0.4.23 ↔ 패키지 0.8.49 ↔ 최신 0.9.69 부터가 어긋나 있다).
- **데이터 경로**: 의미 추출이 외부 LLM 으로 방 내용을 보낸다(PRD §9 보안 원칙 — 서버에 저장되는 범위 — 밖의 새 반출 경로). 질의 로그 기본 켜짐.

---

## 4. 더 단순한 대안과 비교

| 안 | 무엇 | 비용·복잡도 | 기대 가치 | 결정성·권한 |
|---|---|---|---|---|
| **A. Postgres 검색** (`pg_trgm` GIN on `content`·`detail`·decision·artifact 이름, 또는 `tsvector`(simple)+트라이그램) | `colab room search --query <말> [--work][--author][--since]` → 메시지 id·작성자·시각·일치 조각(±200자)·`detail` 길이 | 마이그레이션 1개 + 서버 op 1개 + CLI/MCP 1개. 한국어는 트라이그램이 조사 문제를 흡수 | **가장 큼** — "옛 결정·근거·초안 찾기"를 페이지 넘기기에서 1회 질의로. `room read --query` 도 200개 창 대신 인덱스로 | 결정적, 커밋 즉시 반영, 기존 `rooms.Decide`/FR-4.5 판정 그대로 |
| **B. pgvector 임베딩** | 메시지·detail 청크 임베딩, A 와 RRF 합산 | 임베딩 호출(메시지당 소액) + 확장 설치 + 재색인 | "말은 다른데 뜻이 같은" 회상. graphify 자신의 표에서 hybrid RRF 가 graphify 와 2점 차 | 임베딩 모델 고정 시 재현 가능, 권한은 SQL 로 |
| **C. 주기 요약** | 미션 종료 요약은 이미 있음. 방은 N건마다 자동 「여기까지 정리」 초안 → 사람이 확인 | Platform LLM 호출(§8.5) | 턴 프롬프트의 ①이 잘린 구간을 요약으로 메움 | 요약은 사람이 확정하므로 결정적 입력이 됨 |
| **D. graphify** | 방별 지식 그래프 + MCP | Python 사이드카·LLM 추출·권한 재구현 | 개념 지도·"숨은 연결" — 사람 탐색용 | 비결정적, 권한 없음 |

**가치 순서: A → C → B → D.** A 는 지금 아픈 곳(긴 방·다른 방에서 옛 것을 못 찾음, detail 전문 재독)을 가장 싸게 푼다. C 는 FR-2.5 의 자연스러운 확장이다. B 는 A 로 못 찾는 사례가 쌓이면. D 는 그 뒤에 "사람이 방을 한눈에" 라는 별개 요구가 생길 때.

> 주의: A~D 모두 **턴 안 도구 루프 누적 비용**(§2.4 의 대부분)은 직접 줄이지 않는다. 그쪽은 턴 길이·검수 범위·`detail` 재독 지시(브리프 [2])·모델 선택의 문제다. 검색은 "찾으려고 통째로 읽는" 부분만 줄인다.

---

## 5. 권고와 최소 파일럿

### 권고: **graphify 는 지금 도입하지 않는다.** `colab room search`(안 A)를 먼저 만든다.

이유 요약
1. 맞는 문제에 맞는 도구가 아니다 — graphify 의 무료·결정적 경로는 **코드 AST** 이고, Colab 의 맥락은 한국어 대화·작업 내용이라 LLM 추출 경로를 탄다.
2. E12-11 캐시 접두·골든 테스트 문화와 비결정적 그래프가 충돌한다.
3. 방 권한(FR-4.5 호출 순간 재검사, 존재 숨김, 양쪽 기록)을 그래프 도구 밖에서 다시 만들어야 한다.
4. 대화 회상 벤치마크는 공개 코드로 재현 불가하고, 그 수치로도 hybrid 검색과 차이가 작다.
5. 6개월·1인 주도·잦은 릴리스 — 운영 의존으로 삼기엔 이르다.

### 파일럿 1 (권장) — `colab room search`

- **범위**: 같은 방 메시지 `content`+`detail`, 결정 `summary`+`rationale`, 아티팩트 이름. 다른 방은 기존 `room read --query` 의 내부만 인덱스 조회로 바꿈(계약상 출력·상한 불변).
- **자리**: 마이그레이션 `CREATE EXTENSION pg_trgm` + GIN(`content gin_trgm_ops`, `detail gin_trgm_ops`) · openapi 새 op `searchRoom`(TaskToken·SessionCookie, D27) · colab-cli `room search --query <말> [--work] [--limit 10]` + MCP `colab_room_search`(역할 표 §2.5 모든 역할) · 브리프 [2] 한 줄(고정 문장 — E12-11 유지) · 잘림 한 줄에 "`room search` 로 찾아라" 추가.
- **출력**: 메시지 id·작성자·시각·스레드·일치 조각·`detail` N자 → 전문은 `room messages --thread`.
- **성공 지표**: (1) 게임 제작 방에서 "옛 결정의 근거 찾기" 질문 10개 골든 — 상위 5 안에 정답 메시지 ≥ 8/10. (2) 같은 과업을 재현한 A/B 에서 `room messages` 호출 수·해당 턴 cache_read 감소. (3) p95 < 100ms(수만 행 기준).
- **대략 비용**: 서버·계약·CLI 합쳐 워커 2~3개 규모(서버 1, CLI+MCP 1, 문구·브리프 1). 런타임 비용 0(LLM 없음).

### 파일럿 2 (선택, A 이후) — graphify 오프라인 실험

도입이 아니라 **측정**만 한다. 실사용 DB 에서 게임 제작 방을 방 단위로 내보내(메시지 1건 = md 파일 1개, 파일명 = 메시지 id) `graphify extract --backend claude` 한 번, 같은 10문항으로 `query_graph` 대 `room search` 의 정답 적중과 반환 토큰을 비교. 비용 ≈ $5 내외 + 반나절. graphify 가 A 보다 **확연히** 낫고(예: +3문항 이상) 사람용 "방 지식 지도" 요구가 생길 때만 서버 측 방별 그래프(Go 가 권한 문지기, HTTP MCP 는 내부망)를 다시 검토한다.

---

## 6. 확인하지 못한 것

- graphify 대화 메모리 벤치마크(LOCOMO·LongMemEval) — 하네스(`memory/`)·SurrealDB 엔진이 공개 트리에 없어 재현 불가.
- 한국어 질의 품질 — 토크나이저 코드만 읽었고 실측하지 않았다.
- 대화 코퍼스에서의 실제 추출 비용·시간 — 추정치(한국어 문자→토큰 환산 가정)이며 돌려보지 않았다.
- "1.5천 건 넘는 방" — 현재 실사용 DB 최대는 194건. 그 규모의 실측은 없다.
- Lead cache_read 의 구성(턴 프롬프트 대 도구 결과 비중) — 합계만 보았고 턴별 분해는 하지 않았다.
- GitHub 별 수(12만)의 의미 — 6개월 만의 수치라 과열 가능성이 있으나 판단 근거 없음.
