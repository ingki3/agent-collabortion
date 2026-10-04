# 07 — 오픈소스 메모리 관리 도구 추가 조사 (2단계 착수 전, 코드 수준)

작성 2026-10-05 · Director 요청("오픈소스 메모리 관리 도구 가운데 참고할 내용 정리") · 선행 `03-oss-survey.md`(§1.11~1.14 가 같은 네 도구를 더 넓게 다뤘다 — 이 문서는 **실제 소스 코드를 더 깊이** 읽은 보강) · 적용 대상 `PRD.md` FR-4.6(미션 상태 원장)

## 1. Letta(구 MemGPT) — `letta-ai/letta` ★25,028, 활발(월 1~2회 릴리스)

- **블록 상한은 "조용한 자르기"가 아니라 즉시 거부**: `Block.value` 가 `CORE_MEMORY_BLOCK_CHAR_LIMIT`(기본 5,000자)를 pydantic validator 로 강제하고, 넘으면 `ValueError`.
- **`memory_replace` 는 `old_str` 이 유일하지 않으면 거부**하고 몇 줄에 중복되는지 알려준다 — 모호한 참조로 엉뚱한 항목을 덮어쓰는 것을 원천 차단.
- 멀티 에이전트 공유는 같은 `block_id` 를 여러 에이전트에 붙이는 것뿐이고, 공식 문서가 **"last write wins, 이전 내용 유실, 히스토리 없음"** 이라고 명시한다. 신뢰도·감쇠 없음.
- **Colab 적용**: ① 우리 `content` 필드는 이미 `maxLength: 300` 으로 거부지 자르기가 아니다(openapi 검증) — 같은 원칙이므로 유지. ② "old_str 유일성" 문제는 우리가 `supersede`/`retire` 를 **id 로만** 부르게 설계해 애초에 비껴간다(모호한 텍스트 매칭이 없다). ③ "last write wins" 공유는 우리가 피하는 것 — 원장은 대체가 아니라 **추가**(새 행 + 옛 행 `superseded_by`)라 이력이 남는다.

## 2. Mem0 — `mem0ai/mem0` ★66,562, 매우 활발

- 과거엔 LLM 이 새 사실과 기존 사실을 비교해 `ADD/UPDATE/DELETE/NONE` 을 판정하는 2단계 머지가 있었다. **현재 main(v3)은 이 로직을 제거하고 단일 호출 ADD 만 한다** — Mem0 팀이 공식 마이그레이션 가이드에 "모델이 기존 상태와 diff 하는 데 용량을 쓰는 것보다 입력 이해에 쓰는 쪽이 벤치마크상 더 정확했다" 고 직접 적었다.
- **Colab 적용**: 이건 우리에게 **경고**다 — "쓸 때마다 LLM 이 기존 원장과 비교해 자동으로 대체·삭제를 판단"하는 설계는 Mem0 가 실측 끝에 버린 길이다. 우리 설계가 **에이전트가 명시적으로 `supersede` 를 부르는 것**(자동 판정 없음)은 이미 이 함정을 비껴간다.

## 3. Zep/Graphiti — `getzep/graphiti` ★31,430

- `EntityEdge` 가 이중 시간(`valid_at`·`invalid_at`·`created_at`·`expired_at`)을 갖고, 모순된 옛 사실은 **삭제하지 않고 무효화**한다(`resolve_edge_contradictions`) — 더 늦게 도착했지만 날짜상 더 오래된 정보가 와도 대칭적으로 처리해 잘못된 덮어쓰기를 막는다.
- 리졸브 단계가 "같은 사실인가"(`duplicate_facts`)와 "모순/대체 관계인가"(`contradicted_facts`)를 **한 번의 LLM 호출에서 동시에 분리해 반환**한다.
- 멀티 에이전트는 `group_id` 네임스페이스로 공유 그래프를 지원한다고 문서에 있으나, **동시 쓰기 경합이나 "누가 어떤 권위로 말했는가"는 다루지 않는다** — 화자는 그냥 엔티티 노드다.
- **Colab 적용**: 우리 `supersede` 요구에 가장 가깝다. `invalidated_at`(우리 필드) + `status: superseded`(우리 enum)가 Zep 의 `invalid_at`/`expired_at` 과 같은 역할이다. **"동시 쓰기 권위" 문제는 Zep 도 안 풀었다** — 아래 §5 참고.

## 4. Cognee — `topoteretes/cognee` ★31,352

- 마케팅 자료는 "모순 탐지"·"GC" 를 내세우지만, 실제 코드(`deduplicate_nodes_and_edges.py`)는 **순수 exact-ID 중복 제거뿐**이고, 의미적 모순 판단은 머지 안 된 PR(#3896)·해커톤 이슈(#3629) 수준이다. "last-accessed GC" 도 미병합 PR(#3511).
- **Colab 적용**: 반면교사. 그래프를 경계 없이 계속 쌓으면 이렇게 된다는 사례이지 베낄 게 없다.

## 5. 부가 — 감쇠(decay) 패턴: MemoryBank(arXiv 2305.10250)

에빙하우스 망각곡선 `R(t) = e^(-t/S)` 로 유지율을 계산하고, 참조될 때마다 강도 S 를 올려 곡선을 리셋한다 — "참조 없으면 가중치가 서서히 빠지고, 참조될 때마다 되살아난다"는 우리 `lesson` kind 의 반감기 아이디어와 직접 대응되는 **유일한 수학적 선례**다(학술 구현, 프로덕션 검증은 없음).

## 6. 종합

**채택**(이미 FR-4.6 설계에 반영됨 또는 반영할 것):
1. 블록·항목 상한은 **자르기가 아니라 거부**(Letta) — `content maxLength` 로 이미 반영.
2. **id 기반 supersede/retire**(Letta old_str 유일성 문제를 애초에 피함) — 이미 반영.
3. **삭제 없이 무효화, 새 행으로 쌓기**(Zep 이중 시간) — 이미 반영(`status`·`superseded_by`·`invalidated_at`).
4. **쓸 때마다 자동 LLM 판정을 하지 않는다**(Mem0 가 스스로 버린 설계) — 이미 반영(에이전트가 명시적으로 `note`/`supersede` 호출, 서버는 자동 추출·자동 병합을 하지 않는다).
5. `lesson` 의 30일 반감기·2건 이상 승격 — MemoryBank 가 유일한 선례, 프로덕션 검증은 우리가 처음 하는 셈이라는 점을 인지하고 간다.

**네 도구 전부가 못 푼 것(=우리가 새로 풀어야 하는 것)**: "여러 에이전트가 한 원장에 동시에 쓰고, 누구 주장이 더 신뢰받는가." Letta 는 last-write-wins, Mem0 는 네임스페이스 분리일 뿐 공유가 아님, Zep 은 화자에 권위를 안 둠, Cognee 는 ACL 격리일 뿐. **우리 해법**: 자동 판정을 하지 않고(위 4번), 쓰기 권한을 `kind` 로 역할 분리하며(`plan`·`progress`는 Lead·사람만 — 가장 자주 경합할 만한 칸을 애초에 한 명만 쓰게 한다), `fact`/`lesson`같이 여러 에이전트가 쓸 수 있는 칸은 "대체"가 아니라 "추가 + 출처 보존"이라 경합이 **데이터 손실로 이어지지 않는다**(최신 active 가 여러 개 공존 가능 — 화면·턴 프롬프트가 둘 다 보여 주고 사람·Lead 가 고른다). 이건 네 도구 중 누구도 쓰지 않은 절충이다.

## 출처

- Letta: `letta-ai/letta` — `letta/schemas/block.py`(`CORE_MEMORY_BLOCK_CHAR_LIMIT`), text-editor 툴셋(`memory_replace`/`memory_insert`/`memory_rethink`), archival consolidation 이슈 #3116.
- Mem0: `mem0ai/mem0` — `FACT_RETRIEVAL_PROMPT`, 옛 `DEFAULT_UPDATE_MEMORY_PROMPT`/`get_update_memory_messages()`, `oss-v2-to-v3` 마이그레이션 가이드(ADD-only 전환 사유 명시).
- Zep/Graphiti: `getzep/graphiti` — `EntityEdge`(`valid_at`/`invalid_at`/`created_at`/`expired_at`), `resolve_extracted_edges()`→`resolve_edge()`(`dedupe_edges.py`, `EdgeDuplicate`), `resolve_edge_contradictions()`.
- Cognee: `topoteretes/cognee` — `deduplicate_nodes_and_edges.py`, PR #3896(모순 탐지, 미병합), 이슈 #3629, PR #3511(GC, 미병합).
- MemoryBank: arXiv:2305.10250.
