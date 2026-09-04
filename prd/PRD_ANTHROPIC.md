# PRD — Agent Collaboration Messaging Service (working title: **Colab**)

| 항목 | 내용 |
|---|---|
| 문서 버전 | v0.4 (Draft) — ACP 1차 채택, Buzz 참고 기능 4종 반영 |
| 작성일 | 2026-09-02 |
| 참고 프로젝트 | [multica-ai/multica](https://github.com/multica-ai/multica) (이슈 기반 협업, CLI 런타임 데몬), [block/buzz](https://github.com/block/buzz) (ACP 하네스, 활동 피드, 승인 게이트), Orca (병렬 워크트리) |
| 실행 스택 | **ACP 1차 + CLI 폴백** (Claude Code, Hermes, Antigravity) + 플랫폼 내부 기능은 Anthropic Claude API |

### v0.2 변경 요약

| # | 결정 | 반영 위치 |
|---|---|---|
| 1 | 세션마다 사람 한 명을 **Director**로 지정. HITL 요청의 수신자. | §3, FR-2.1, FR-5 |
| 2 | Lane 격리는 **워크트리 / 컨테이너 둘 다** 지원, 세션 생성 시 선택. | FR-2.1, FR-6.4 |
| 3 | 세션 도중 **에이전트 동적 생성 금지**. 등록된 풀 안에서만 위임. | FR-1.5, §8.3 프로토콜 |
| 4 | 이전 세션 요약의 컨텍스트 재사용 길이 상한은 **설정에서 조정**. | FR-4.4, §6 Settings |
| 5 | 에이전트는 **CLI 런타임 + 모델 설정**이 기본. 런타임별 **멀티 프로파일** 지원. | FR-1.1, FR-1.6 |
| 6·7 | v1은 **CLI 런타임 데몬**으로 시작. 초기 지원: **Claude Code, Antigravity, Hermes**. | §2, §8, §10 |
| v0.3 | 세 런타임의 헤드리스 실행 방식·resume·출력 파싱·MCP·지시 파일을 multica 어댑터 코드와 공식 문서로 확인해 §8.2에 확정. | §8.2, §12, 부록 C |

### v0.4 변경 요약 (block/buzz 분석 반영)

| # | 결정 | 반영 위치 |
|---|---|---|
| A | **ACP(Agent Client Protocol)를 1차 실행 경로로 채택.** 런타임별 플래그 조립은 ACP 어댑터가 없는 런타임(Antigravity)의 폴백으로 강등. | §8.2 전면 재작성, §8.1, §10 |
| B | **턴 진행 중 새 메시지 처리 정책** 추가: `steer`(기본) / `queue` / `interrupt` / `director-interrupt`. | FR-3.4 |
| C | **활동 피드(Activity Feed) 설계** 도입: 동사·목적어·결과 문장, 렌더 클래스, 제자리 갱신, 침묵도 렌더. | FR-7.2 |
| D | **승인 토큰 모델**: HITL 요청은 토큰 해시만 저장하고 서명 응답으로 해소, 재개 지점 명시, 기본 기한 24h, 후속 분기. | FR-5.4 |
| E | **`.agent.md` 이식 가능 정의 파일** + 정의(템플릿)/인스턴스 분리. | FR-1.7, FR-1.8 |
| F | 부수 채택: 호출 권한 게이트 + 형제 에이전트, 빈 확인 메시지 금지 규칙, 메모리 조회 실패와 부재 구분, 고빈도 이벤트 비영속 분리. | FR-1.1, §8.3, FR-4.2, §7 |

---

## 1. 개요

### 1.1 한 줄 정의

**복수의 AI 에이전트를 등록하고 역할을 부여한 뒤, 하나의 공유 컨텍스트(세션) 안에서 서로 채팅·멘션하며 사용자가 준 goal을 병렬로 수행하는 메시징 서비스.** 에이전트의 실제 실행은 사용자 머신의 CLI 런타임(Claude Code 등)이 담당한다.

### 1.2 문제

- 여러 에이전트(리서처, 기획자, 개발자, 리뷰어…)를 쓰려면 각각 별도 터미널/채팅창을 열고 같은 컨텍스트를 반복 설명해야 한다.
- 에이전트 간 결과물 전달은 사람이 복사·붙여넣기로 중계한다. 결정의 근거와 히스토리가 흩어진다.
- 에이전트가 언제 사람의 판단을 필요로 하는지(HITL) 명시적인 채널이 없다. 사용자는 항상 화면을 지켜봐야 한다.
- 작업을 병렬로 돌리고 싶어도 에이전트끼리 서로의 진행 상황을 모른 채 충돌한다.

### 1.3 해결

multica가 "이슈 트래커"를 협업 표면으로 삼았다면, 본 서비스는 **"메시징 세션(채널)"** 을 협업 표면으로 삼는다.

- 사용자는 **세션**을 만들고 **goal**, **종료 조건**, **Director**(결정권자)를 설정한다.
- 세션에 **에이전트**들을 초대한다. 각 에이전트는 이름·역할·instruction·런타임 프로파일을 가진다.
- 에이전트들은 세션 안에서 **채팅하고 서로 @멘션**하며 일을 나눈다. 모든 메시지는 공유 컨텍스트가 된다.
- 에이전트가 사람의 결정이 필요하면 **HITL 요청**을 올리고, Director가 인박스에서 답한다.
- 서로 독립적인 하위 작업은 **병렬 lane**으로 실행되며, 종료 조건이 충족되면 세션이 자동으로 닫힌다.

### 1.4 multica에서 가져오는 것 / 바꾸는 것

| multica | 본 서비스 | 이유 |
|---|---|---|
| 이슈(Issue)가 작업 단위, 댓글이 대화 | **세션(Session)** 이 작업 단위, **메시지/스레드**가 대화 | 목표 하나에 여러 에이전트가 자유롭게 대화하는 구조가 핵심 |
| `[@Name](mention://agent/<uuid>)` 형식의 멘션이 에이전트 트리거 | 동일 방식 채택 (`mention://agent|user|all`) | 이름 충돌 없이 결정적으로 라우팅 가능 |
| 스쿼드 리더가 "코디네이트만 하고 구현하지 않는" 프로토콜 | **Lead 역할** 에이전트에 동일 프로토콜 부여 | 검증된 위임 패턴 |
| 데몬(runtime)이 사용자 머신의 CLI를 구동, 26개 CLI 지원 | **동일한 데몬 모델**. v1은 Claude Code · Antigravity · Hermes 3종 | 사용자가 이미 로그인한 CLI를 그대로 사용, 코드가 머신 밖으로 나가지 않음 |
| 에이전트 1개 = 런타임 1개 + 모델 1개 | 에이전트 1개 = **런타임 프로파일 N개** (세션/lane마다 선택) | 같은 역할을 빠른 모델/깊은 모델로 바꿔 쓰기 |
| 이슈 상태(in_progress → in_review → done)가 리뷰 게이트 | **세션 종료 조건 + Director 승인 게이트** | goal 단위 종료를 명시적으로 정의 |
| 태스크는 이슈당 순차, 스쿼드는 리더 경유 | **DAG 기반 병렬 lane** (Orca 스타일) | 독립 작업의 wall-clock 단축 |
| 인박스는 사람 전용, 에이전트가 사람을 멘션/`blocked` 로 호출 | **HITL Request** 를 1급 객체로 승격, 수신자는 **Director** | Director가 "무엇을 결정해야 하는지" 명확히 알 수 있게 |

---

## 2. 목표와 비목표

### 2.1 목표 (Goals)

1. 사용자가 데몬 설치 후 5분 안에 에이전트 3개를 등록하고 첫 세션을 완료할 수 있다.
2. 에이전트 간 위임·보고가 **전부 세션 메시지로 남아** 사람이 언제든 따라잡을 수 있다.
3. 에이전트가 사람의 결정 없이 진행하면 안 되는 지점을 스스로 식별해 Director에게 HITL 요청을 올린다.
4. 독립적인 하위 작업이 병렬로 실행되어 단일 에이전트 대비 총 소요 시간이 줄어든다.
5. 세션은 사용자가 정의한 종료 조건에 따라 **자동으로 완료**되며, 결과물이 정리된 형태로 남는다.
6. 서로 다른 벤더의 CLI(Claude Code / Antigravity / Hermes)를 같은 세션에서 섞어 쓸 수 있다.

### 2.2 비목표 (Non-goals, v1)

- VCS 통합(PR 자동 생성, 리뷰 코멘트 동기화) — lane이 워크트리를 쓰더라도 커밋/PR은 사람 또는 에이전트가 CLI로 직접. v2.
- 사람 간 메시징(Slack 대체) — 사람은 Director 역할(지시·승인·질문 응답)에 집중.
- 에이전트의 세션 중 동적 생성 — 금지(결정 3).
- 클라우드 호스팅 런타임 — v1은 사용자 머신의 데몬만. 클라우드 런타임은 v2.
- 모바일 앱.

---

## 3. 핵심 개념 (Glossary)

| 용어 | 정의 |
|---|---|
| **Workspace** | 사용자·에이전트·런타임·세션을 담는 최상위 단위. 멤버 권한(owner/admin/member). |
| **Runtime** | 데몬이 설치된 머신. 설치·로그인된 CLI 목록(`claude`, `antigravity`, `hermes`)을 서버에 보고하고 task를 받아 실행한다. multica의 `agent_runtime`에 대응. |
| **Runtime Profile** | 에이전트에 붙는 실행 설정 1벌: `{런타임 종류, 모델, effort/옵션, 환경변수, 인자}`. 에이전트는 프로파일을 여러 개 가질 수 있고 하나가 기본. |
| **Agent** | 이름·역할·instruction·런타임 프로파일들을 가진 등록된 AI 참여자. 워크스페이스 소속, 여러 세션에 참여 가능. |
| **Role** | 역할 태그와 역할 설명. `lead`, `researcher`, `writer`, `engineer`, `reviewer`, `custom`. `lead`는 코디네이션 프로토콜을 추가로 받는다. |
| **Session** | 하나의 **goal**을 달성하기 위한 협업 단위. Director, 참여 에이전트, 종료 조건, 격리 방식, 공유 컨텍스트, 메시지 스트림을 가진다. multica의 Issue에 대응. |
| **Director** | 세션마다 지정되는 **사람 한 명**. HITL 요청의 수신자이자 `user_approval` 종료 조건의 승인자. 세션 생성자가 기본값이며 변경 가능. |
| **Goal** | 세션의 목표 문장 + 성공 기준(acceptance criteria). |
| **Completion Condition** | 세션 종료 조건. 산출물 제출, 리뷰어 승인, Director 승인, 예산/시간 상한 등의 조합. |
| **Message** | 세션 내 발화. 작성자는 user / agent / system. 스레드(parent) 구조 지원. |
| **Mention** | `[@표시명](mention://agent/<id>)` 형식의 링크. 에이전트 트리거의 유일한 결정적 수단. |
| **Task (Run)** | 하나의 트리거(멘션, 배정, 응답)로 생성되어 특정 런타임에 dispatch되는 에이전트 실행 단위. |
| **HITL Request** | 에이전트가 Director에게 올리는 구조화된 요청 (질문 / 선택 / 승인 / 정보 요청). 응답 전까지 해당 task는 `waiting_human`. |
| **Artifact** | 세션에서 생성된 산출물 (문서, 파일, 코드 diff, 표). 종료 조건의 대상. |
| **Shared Context** | 세션 설명·첨부 자료·아티팩트·메시지 히스토리·결정 기록의 집합. 모든 에이전트가 동일한 뷰를 갖는다. |
| **Lane** | 병렬 실행 트랙. 하나의 세션 안에 여러 lane이 동시 진행되며, 각 lane은 세션 설정에 따라 워크트리 또는 컨테이너로 격리된다. |

---

## 4. 사용자 시나리오

### 시나리오 A — 시장 조사 보고서 (비개발)

1. 사용자가 노트북에 데몬 설치 → Claude Code, Hermes가 감지되어 런타임으로 등록.
2. 에이전트 3개 등록: `Lead`(lead, Claude Code + opus), `Researcher`(researcher, 프로파일 2개: Hermes 기본 / Claude Code 대체), `Writer`(writer, Claude Code).
3. 세션 생성. Goal: "국내 B2B SaaS 결제 시장 조사 보고서 10페이지". Director: 본인. 격리: 컨테이너. 종료 조건: `Writer가 artifact 제출` AND `Director 승인`.
4. Lead가 자동 트리거 → 계획을 메시지로 올리고 `@Researcher`에게 3개 조사 항목을 병렬 위임(lane 3개, Researcher의 기본 프로파일로 실행).
5. Researcher 3개 lane이 동시에 조사 → 각각 결과를 스레드에 게시.
6. Lead가 종합 → `@Writer` 초안 요청. Writer가 "타깃 독자가 투자자인지 내부 경영진인지"를 **HITL 질문**으로 올림 → Director 인박스.
7. Director가 "투자자" 선택 → Writer 재개 → 보고서 artifact 제출.
8. 종료 조건 1 충족 → Director에게 승인 요청 → 승인 → 세션 `completed`.

### 시나리오 B — 기능 구현 (개발)

1. 에이전트: `PM`(lead, Claude Code), `Backend`(engineer, Claude Code), `Frontend`(engineer, Antigravity), `QA`(reviewer, Claude Code).
2. 세션 생성. Goal: "회원 탈퇴 기능 구현". 격리: **워크트리** (저장소 `~/dev/app`). 종료 조건: `QA 승인` AND `예산 $20 이하`.
3. PM이 스펙 작성 → Backend/Frontend 병렬 lane 생성 → 각 lane은 별도 git 워크트리에서 작업 → 결과를 메시지로 보고.
4. QA가 두 워크트리의 diff를 리뷰 → Frontend에 수정 요청 멘션 → Frontend 수정 → QA 승인 메시지.
5. QA의 승인이 종료 조건을 충족 → 세션 `completed`. 워크트리 병합은 Director가 CLI로 수행(v1). 예산 초과 시 `paused` + Director에게 HITL 승인 요청.

### 시나리오 C — Director 중간 개입

- 진행 중 Director가 세션에 "범위를 한국 시장으로 좁혀줘"라고 메시지 → 라우팅 규칙에 따라 Lead(또는 배정 에이전트)에게 전달 → Lead가 진행 중인 lane에 수정 지시 멘션.

### 시나리오 D — 프로파일 전환

- Researcher의 Hermes 프로파일이 런타임 오프라인으로 실패 → 데몬이 `runtime_offline`을 보고 → 에이전트에 대체 프로파일(Claude Code)이 있으면 재시도 시 자동 전환, 없으면 `failed` + Director 알림.

---

## 5. 기능 요구사항

### FR-1. Agent 등록 및 관리

multica의 Agent 등록 페이지(`agents-create`)를 참고하되, **역할 정의**와 **런타임 프로파일**을 분리해 둘 다 전면에 둔다.

**FR-1.1 등록 폼 필드**

| 필드 | 필수 | 설명 |
|---|---|---|
| `name` | ✓ | 표시 이름. 멘션 라벨로 사용. 워크스페이스 내 유일. |
| `role` | ✓ | 역할 태그 (`lead`, `researcher`, `writer`, `engineer`, `reviewer`, `custom`). |
| `role_description` | ✓ | 역할 설명 1~3문장. 다른 에이전트의 로스터에 노출되어 "누구에게 위임할지" 판단 근거가 된다. |
| `instructions` | ✓ | 시스템 프롬프트. 페르소나·작업 방식·제약. 매 run마다 주입. |
| `profiles[]` | ✓ (1개 이상) | 런타임 프로파일 목록 (FR-1.6). 하나를 `default`로 지정. |
| `tools` | | 허용 도구/스킬/MCP 서버 목록. 런타임이 지원하는 범위 내에서 적용. |
| `max_concurrent_tasks` | | 동시 실행 가능한 task 수 (기본 3). |
| `avatar` | | 이미지. |
| `respond_to` | | **누가 이 에이전트를 트리거할 수 있는가**: `owner`(기본) / `allowlist` / `workspace` / `nobody`(일시 비활성). FR-1.9. |
| `budget_per_task` | | task 1건당 토큰/비용 상한. |

**FR-1.2 Build with AI** — 자연어 설명("보수적인 재무 분석가")을 입력하면 이름·역할·instruction 초안을 생성. 플랫폼 내부 Claude API 호출(구조화 출력)로 폼 JSON을 받는다. 프로파일은 사용자가 직접 선택.

**FR-1.3 Agent 상태** — `idle` / `working` / `waiting_human` / `error` / `offline`(기본 프로파일의 런타임이 오프라인). 세션 화면의 참여자 목록과 에이전트 페이지에 실시간 표시.

**FR-1.4 템플릿** — 자주 쓰는 팀 구성(리서치 팀, 개발 팀, 콘텐츠 팀)을 프리셋으로 제공. 프리셋은 역할·instruction만 담고, 프로파일은 사용자의 런타임에 맞춰 첫 실행 시 매핑.

**FR-1.5 동적 생성 금지** — 에이전트 생성·수정은 Agents 화면(사람)에서만 가능하다. 에이전트에게 제공되는 툴셋에 에이전트 생성 툴은 없으며, `delegate`의 대상은 세션 `participants`로 제한된다. 로스터에 없는 역할이 필요하면 에이전트는 `ask_human`으로 Director에게 참여자 추가를 요청한다.

> 참고: Buzz는 에이전트가 새 에이전트를 **초안으로 제안**하고 소유자가 폼에서 검토·확정하는 중간 경로를 둔다. 우리는 v1에서 이를 채택하지 않는다 — 위의 `ask_human` 경로가 이미 사람의 승인을 거치는 같은 효과를 내고, 초안 검토 UI를 추가할 만큼 수요가 확인되지 않았다. v2 재검토 대상.

**FR-1.6 런타임 프로파일**

```
profile:
  name: "deep"                 # 에이전트 내 유일
  runtime_kind: claude_code    # claude_code | antigravity | hermes
  runtime_id: <optional>       # 특정 머신 고정, 비우면 해당 kind를 가진 온라인 런타임 중 자동 선택
  model: claude-opus-5         # 런타임이 지원하는 모델 ID
  options: { effort: xhigh }   # 세션이 광고한 능력 범위 내에서만 유효 (§8.2.1)
  env: { ... }                 # 추가 환경변수
  args: [ ... ]                # 추가 CLI 인자
  is_default: true
```

- 세션 참여자 추가 시, 또는 `delegate` 호출 시 프로파일을 지정할 수 있다. 미지정 시 `default`.
- 프로파일의 런타임이 오프라인이면 task는 `queued`로 대기하다가, `fallback_profile`이 있으면 그쪽으로 재시도한다.
- 지원 모델 목록은 데몬이 런타임별로 보고한다(§8.2의 probe 결과).
- `transport`는 프로파일에 두지 않는다. ACP 어댑터 사용 가능 여부는 데몬이 판단하고 실패 시 CLI로 폴백한다(§8.2). 사용된 경로는 활동 피드에 기록된다.

**FR-1.7 이식 가능한 정의 파일 (`.agent.md`)** — 에이전트 정의는 DB 레코드인 동시에 **하나의 파일로 내보내고 가져올 수 있다**. Buzz의 `.persona.md`와 같은 형태로, YAML frontmatter가 메타데이터이고 마크다운 본문이 `instructions`다.

```markdown
---
name: Researcher
role: researcher
role_description: 1차 자료를 찾아 출처와 함께 정리한다. 결론을 내리지 않는다.
profiles:
  - name: default
    runtime_kind: claude_code
    model: claude-opus-5
    options: { effort: high }
  - name: fast
    runtime_kind: hermes
    model: "anthropic:claude-sonnet-5"
tools: [web_search, web_fetch]
respond_to: owner
max_concurrent_tasks: 3
---

너는 리서처다. 주장마다 출처 링크를 남기고, 확인되지 않은 것은
"확인 필요"로 표시한다. 결론과 권고는 Lead에게 맡긴다.
```

- **왜**: 팀이 에이전트 구성을 git으로 버전 관리하고, PR로 리뷰하고, 워크스페이스 간에 공유할 수 있다. UI 폼과 파일은 같은 스키마의 두 표현이다.
- **팀 묶음**: 여러 `.agent.md`와 README를 디렉토리로 묶어 **팀 팩**으로 배포한다. 팩 가져오기는 에이전트를 한꺼번에 등록한다.
- **검증**: 가져오기 시 스키마 검증 후, 참조된 `runtime_kind`가 이 워크스페이스에 없으면 등록은 하되 `offline`으로 표시하고 프로파일 매핑을 요구한다.

**FR-1.8 정의와 인스턴스의 분리** — `.agent.md`(또는 템플릿)는 **정의**이고, 워크스페이스에 등록된 에이전트는 **인스턴스**다. 인스턴스는 정의에서 복사된 필드 외에 자기 것만 갖는다: `workspace_id`, `owner_id`, `status`, `runtime_id` 바인딩, 누적 사용량, `definition_source`와 `definition_version`.

- 정의가 갱신되어도 **실행 중인 task에는 영향을 주지 않는다.** 새 설정은 다음 run부터 적용된다. 진행 중인 에이전트의 설정을 갈아끼우는 동작은 제공하지 않는다.
- 인스턴스는 `definition_version`을 보관해 "이 팩의 v3에서 왔고 현재 v5가 있음"을 표시하고, 사용자가 명시적으로 업데이트를 적용한다.

**FR-1.9 호출 권한 게이트** — `permission`(누가 이 에이전트를 **볼 수 있는가**)과 `respond_to`(누가 이 에이전트를 **트리거할 수 있는가**)를 분리한다.

- `respond_to: owner`(기본)에서는 소유자 본인과, **같은 소유자에게 귀속된 다른 에이전트**(형제 에이전트)의 멘션이 통과한다. 이 형제 규칙이 없으면 에이전트 간 위임이 전부 막힌다.
- 권한 판정은 언제나 **체인 최상단의 사람 originator** 기준이다. 에이전트가 다른 에이전트를 호출해도 권한이 상승하지 않는다.
- `nobody`는 정의를 지우지 않고 트리거만 중단시키는 스위치다. 폭주하는 에이전트를 즉시 멈출 때 쓴다.

**FR-2.1 생성 폼**

| 필드 | 설명 |
|---|---|
| `title` | 세션 제목 |
| `goal` | 목표 문장 (필수) |
| `acceptance_criteria` | 성공 기준 체크리스트 (선택, 종료 판정에 사용) |
| `director_user_id` | **Director** (필수). 기본값 생성자. 워크스페이스 멤버 중 선택. 세션 진행 중 변경 가능(변경은 시스템 메시지로 기록). |
| `participants` | 참여 에이전트 목록 + 각자의 프로파일 + 그중 `assignee`(기본 담당, 보통 lead) |
| `context` | 첨부 자료: 문서, URL, 파일, 이전 세션 링크 |
| `isolation` | lane 격리 방식 (필수): `worktree` (저장소 경로 지정) / `container` (이미지 선택) / `none` (아티팩트 네임스페이스만 분리) |
| `completion` | 종료 조건 (FR-2.2) |
| `limits` | 예산 상한(USD/토큰), 시간 상한, 최대 task 수, 최대 병렬 lane 수 |
| `mid_turn_policy` | 실행 중 새 메시지 처리: `steer`(기본) / `queue` / `interrupt` / `director-interrupt` (FR-3.4) |
| `autonomy` | `supervised`(모든 위임 전 Director 승인) / `guided`(HITL 요청만) / `autonomous`(종료 시만 보고) |

**FR-2.2 종료 조건 (Completion Condition)** — 다음 원자 조건을 AND/OR로 조합. UI는 체크박스 + 조합 방식 선택.

| 조건 타입 | 판정 방식 |
|---|---|
| `artifact_submitted` | 지정된 역할/에이전트가 `submit_artifact` 툴을 호출 |
| `agent_approval` | 지정 에이전트(예: reviewer)가 `approve` 툴 호출 |
| `user_approval` | **Director**가 승인 버튼 클릭 (HITL Request `approval` 타입) |
| `criteria_met` | 판정 에이전트가 acceptance_criteria를 구조화 출력으로 평가해 전부 true. **단독 사용 불가** — 항상 승인 계열과 AND |
| `budget_reached` | 누적 비용이 상한 도달 → 강제 `paused` (완료가 아닌 정지) |
| `time_elapsed` | 시간 상한 도달 → 강제 `paused` |
| `manual` | Director가 직접 종료 |

기본값: `artifact_submitted(assignee) AND user_approval`.

**FR-2.3 세션 상태 머신**

```
draft → active → (paused ⇄ active) → completing → completed
                        ↓
                    cancelled
```

- `completing`: 종료 조건 충족 후 결과 요약(summary) 생성 및 아티팩트 정리 단계.
- `paused`: 예산/시간 상한, 루프 상한, 또는 Director가 일시정지. 진행 중 task는 현재 턴까지 마치고 대기.

**FR-2.4 세션 요약** — 완료 시 플랫폼이 결정 기록·아티팩트·비용·타임라인을 정리한 `session_summary` 메시지를 자동 게시. 길이 상한은 워크스페이스 설정(FR-4.4).

### FR-3. 메시징 및 멘션 라우팅

**FR-3.1 메시지 구조** — 세션은 하나의 메인 타임라인 + 스레드(reply). 작성자 타입 `user | agent | system`. 마크다운 지원. `source_task_id`로 어떤 run에서 나온 메시지인지 추적.

**FR-3.2 멘션 문법** — multica와 동일하게 링크 형식으로 고정.

```
[@Researcher](mention://agent/6f1a…)     에이전트
[@김민수](mention://user/…)               사람 (Director가 아니어도 알림)
[@all](mention://all/all)                  전원
```

UI에서 `@` 입력 시 자동완성이 링크를 삽입한다. 에이전트에게는 로스터에 붙여넣기용 링크를 제공한다.

**FR-3.3 라우팅 규칙** (multica `computeCommentAgentTriggers` 를 세션 모델로 옮김. 위에서부터 우선 적용)

1. `/note` 접두 메시지 → 트리거 없음 (기록만).
2. 에이전트 명시 멘션 → 각 멘션된 에이전트에 task 생성. 같은 에이전트 중복 멘션은 1개로 병합. 세션 참여자가 아닌 에이전트 멘션은 무시하고 경고 표시.
3. `@all` 또는 사람만 멘션 → 암묵적 라우팅 억제, 에이전트 트리거 없음.
4. 에이전트가 작성한 메시지는 **멘션이 있을 때만** 다른 에이전트를 트리거 (암묵 라우팅 없음). 단, 하위 lane 완료 보고는 위임한 에이전트(`delegated_from`)를 깨운다.
5. 사용자가 에이전트 메시지에 답글 → 그 에이전트. 스레드 안의 답글 → 스레드 소유 에이전트.
6. 그 외 사용자 메시지 → 세션 `assignee`.
7. 규칙 5로 비-assignee가 트리거된 경우, assignee 폴백 task를 5분 지연 예약하고 주 에이전트가 응답하면 취소.

**FR-3.4 턴 진행 중 도착한 메시지 처리** — 에이전트가 이미 실행 중일 때 그 에이전트를 향한 새 메시지가 오면 세션 설정 `mid_turn_policy`에 따라 처리한다. Buzz의 `multiple-event-handling` 모델을 채택한다.

| 정책 | 동작 | 적합 |
|---|---|---|
| `steer` **(기본)** | 진행 중인 턴을 취소하고, **이전 지시와 새 메시지를 함께** 다시 프롬프트한다. 새 메시지는 "작업 도중 도착함"으로 명시적으로 프레이밍한다(§8.4의 `prior`/`new` 구간). | Director가 방향을 바꾸는 대화형 세션 |
| `queue` | 현재 턴을 끝까지 두고, 새 메시지는 다음 run으로 병합해 대기시킨다(`coalesced_message_ids`). | 되돌리기 어려운 작업이 진행 중인 lane |
| `interrupt` | 진행 중인 턴을 취소하고 **새 메시지만으로** 다시 시작한다. 이전 지시는 히스토리에만 남는다. | 명백한 오작동 중단 |
| `director-interrupt` | Director의 메시지에만 `interrupt`, 다른 참여자의 메시지는 `queue`. | 자율 진행 중 사람만 끼어들게 할 때 |

- 취소는 §8.2.2의 절차(권한 요청 응답 → `session/cancel` → 드레인)를 따른다. 프로세스를 즉시 죽이지 않는다.
- 런타임이 `steering`을 광고하지 않으면 `steer`는 자동으로 `queue`로 강등되고 그 사실을 활동 피드에 남긴다(§8.2.6).
- 정책과 무관하게, **같은 에이전트에 대한 `queued` 상태의 task는 항상 하나로 병합**한다(multica coalescing). 병합된 메시지 id는 `coalesced_message_ids`에 보존한다.
- lane 하나당 진행 중인 턴은 항상 1개다(FR-6.3). 여러 lane은 동시에 진행된다.

**FR-3.5 루프 방지** — 에이전트 간 상호 멘션은 허용하되, 세션당 `max_agent_to_agent_hops`(기본 20)와 동일 쌍 왕복 횟수 상한(기본 5)을 두고 초과 시 세션 `paused` + Director에게 HITL 알림. 상한은 워크스페이스 설정.

**FR-3.6 트리거 미리보기** — 메시지 전송 전 "이 메시지는 A, B를 트리거합니다 (프로파일: …)" 표시. `suppress_agent_ids`로 개별 억제 가능.

### FR-4. 공유 컨텍스트

**FR-4.1 컨텍스트 구성** — 각 run에 다음이 제공된다. multica처럼 **안정적인 브리프는 파일**(런타임의 프로젝트 지시 파일, 예: Claude Code의 `CLAUDE.md`)로, **턴별 내용은 프롬프트**로 전달한다.

| 구간 | 내용 | 전달 방식 |
|---|---|---|
| 안정 브리프 | 에이전트 identity + instructions, 워크스페이스 규칙, 세션 goal/criteria/종료조건, 참여자 로스터(멘션 링크·역할 설명 포함), 첨부 컨텍스트 요약, 툴/CLI 사용 규약 | 런타임 지시 파일: Claude Code는 `CLAUDE.md`, Antigravity·Hermes는 `AGENTS.md` (§8.4). 세 런타임 모두 시스템 프롬프트 인자 대신 이 파일로만 전달 |
| 결정 기록 | HITL 답변, 확정된 결정 목록 | 브리프 파일 뒤쪽 섹션 |
| 세션 히스토리 | 최근 N개 메시지 + 관련 스레드 (전체는 CLI로 조회) | 턴 프롬프트 |
| 트리거 | 트리거 메시지(들) 원문 인용, 위임 브리프, 프로파일 정보 | 턴 프롬프트 |

에이전트는 `colab session get`, `colab session messages --thread <id> --tail 30` 로 필요한 만큼 읽는다.

**FR-4.2 결정 기록(Decision Log)** — HITL 응답과 에이전트의 `colab decision record` 호출을 세션 단위 목록으로 보존. 새 에이전트가 합류해도 "왜"를 알 수 있게 한다.

- **부재와 장애를 구분한다.** 결정 기록이나 이전 세션 요약을 조회할 때, "확실히 비어 있음"과 "조회 실패"는 다르게 처리한다. 조회가 **실패**하면 브리프에 해당 구간을 아예 넣지 않는다. 빈 구간을 넣으면 에이전트가 "결정된 바 없음"으로 읽고 이미 내려진 결정을 뒤집는다. 실패 사실은 활동 피드에 오류로 남긴다.

**FR-4.3 아티팩트 저장소** — `colab artifact submit --name --type --file`. 버전 관리(같은 이름 재제출 시 v2). 세션 사이드바에 노출. 워크트리 격리 세션에서는 diff/브랜치 참조도 아티팩트 타입으로 등록 가능.

**FR-4.4 컨텍스트 재사용** — 완료된 세션을 새 세션의 컨텍스트로 첨부 가능 (요약 + 아티팩트 링크). 요약 길이 상한은 **워크스페이스 설정** `context_reuse.max_summary_tokens` (기본 2,000) 와 `context_reuse.include_artifacts` (기본: 링크만) 로 조정한다. 세션 생성 시 개별 오버라이드 가능.

### FR-5. HITL (Human-in-the-loop)

multica에서는 에이전트가 사람을 멘션하거나 `blocked`로 상태를 바꾸는 것이 HITL의 전부다. 본 서비스는 이를 1급 객체로 만들고 수신자를 **Director**로 고정한다.

**FR-5.1 HITL Request 타입**

| 타입 | 에이전트 CLI | Director 응답 UI |
|---|---|---|
| `question` | `colab hitl ask --question --context` | 자유 텍스트 |
| `choice` | `colab hitl ask --question --option A --option B` | 라디오/체크 + 기타 |
| `approval` | `colab hitl approve-request --summary --artifact <id>` | 승인 / 거절(사유) |
| `info` | `colab hitl request-info --what --why` | 파일/링크/텍스트 첨부 |

**FR-5.2 동작**

- 요청 생성 시 해당 task는 `waiting_human`, 에이전트 상태는 `waiting_human`. 세션 타임라인에 카드 형태로 게시.
- Director 인박스에 `action_required`로 등록. 이메일/푸시 알림 옵션. 다른 멤버는 카드를 볼 수 있지만 응답 버튼은 Director에게만 활성화.
- Director가 부재일 때를 위해 세션에 `deputy_director_user_id`(선택)를 둘 수 있다. `due_in` 초과 시 deputy에게 위임 알림.
- 응답 시 task가 재개되고 응답은 결정 기록에 저장.
- `due_in` 지정 가능. 기한 초과 시 (a) 대기 유지 (기본) (b) 에이전트가 제안한 기본값으로 진행 — 세션 `autonomy` 설정에 따름.
- 다른 에이전트는 대기 중인 HITL과 무관한 lane을 계속 진행할 수 있다.

**FR-5.3 Director 주도 지시** — Director는 언제든 세션에 메시지를 보내 지시할 수 있다(FR-3.3 라우팅). `supervised` 모드에서는 Lead의 모든 위임 메시지가 Director 승인 후에만 전송된다. Director가 아닌 멤버의 메시지도 라우팅되지만, HITL 응답과 승인은 Director만 가능하다.

**FR-5.4 승인 토큰 모델** — HITL 요청의 저장·해소·재개 방식. Buzz의 승인 게이트 구현을 따른다.

- **요청 = 일시 정지**: 에이전트가 HITL 툴을 호출하면 해당 run은 결과를 반환하는 대신 **일시 정지 상태**(`waiting_human`)로 넘어가며, 랜덤 UUID인 **승인 토큰**이 발급된다. 재개 지점(`resume_at`: lane id + 다음 단계)이 함께 기록된다.
- **토큰은 해시만 저장한다**: DB의 `hitl_request.token_hash`에는 `SHA-256(token)`만 넣는다. 원본 토큰은 요청 카드와 알림 링크에만 실린다. 저장소가 유출되어도 승인을 위조할 수 없다.
- **해소는 서명된 응답**: Director의 승인/거절은 토큰 해시를 키로 하는 응답 레코드로 기록된다. 서버는 (a) 아직 `open` 상태이고 (b) 기한이 지나지 않았으며 (c) 응답자가 `approver_spec`에 부합하는지 검증한 뒤 **멱등하게** 저장한다. 같은 토큰에 대한 두 번째 응답은 오류가 아니라 무시된다.
- **재개**: 응답이 확정되면 `resume_at`부터 run을 재개한다. 처음부터 다시 실행하지 않는다.
- **후속 분기**: 재개된 컨텍스트에는 `approved: true|false`와 사유가 주입되어, 에이전트가 거절 경로를 분기 처리할 수 있다. 거절도 정상 흐름이며 실패가 아니다.
- **`approver_spec`**: `director`(기본) / `any_member` / 특정 사용자 id. **역할 기반 지정(예: "리뷰어 아무나")은 v1에서 지원하지 않으며, 지원하지 않는 spec은 저장 시점에 거부한다** — 조용히 아무나 승인 가능해지는 상태를 만들지 않는다(fail closed).
- **기한**: 기본 24시간. 초과 시 `expired`가 되고 세션 `autonomy` 설정에 따라 (a) 대기 유지 (b) 에이전트가 제안한 기본값으로 진행. deputy director가 있으면 기한의 절반 시점에 위임 알림.
- **"Needs Action" 질의**: 인박스는 "나에게 열려 있는 HITL 요청 + 기한이 임박한 항목"의 합집합 질의로 만든다. 이것이 사람 쪽 진입점의 유일한 정의다.

### FR-6. 병렬 실행 (Lanes)

Orca의 워크트리 병렬 모델을 메시징 세션에 적용한다.

**FR-6.1 Lane** — 세션 안의 독립 실행 트랙. 하나의 lane은 하나의 에이전트 task 체인에 대응. Lead(또는 Director)가 여러 에이전트를 한 메시지에서 멘션하면 각 멘션이 별도 lane이 되어 동시에 시작한다.

**FR-6.2 의존성** — `colab lane delegate --agent --brief --depends-on <lane_id> --profile <name>` 으로 DAG를 구성. 의존 lane이 완료되면 자동 시작. UI에 lane 보드(칸반) 제공: `queued / running / waiting_human / done / failed`.

**FR-6.3 동시성 제한** — 세션 `max_parallel_lanes`(기본 5), 에이전트 `max_concurrent_tasks`, 런타임(데몬) 전역 상한, 워크스페이스 전역 상한. 초과분은 큐 대기.

**FR-6.4 격리** — 세션 생성 시 선택한 `isolation`에 따라 데몬이 lane 작업 공간을 준비한다.

| 방식 | 준비 | 적합 |
|---|---|---|
| `worktree` | 지정 저장소에서 `git worktree add <session>/<lane>` (브랜치 `colab/<session>/<lane>`). lane 종료 후 워크트리는 보존, Director가 정리/병합. | 코드 작업 |
| `container` | 지정 이미지로 컨테이너 생성, 세션 컨텍스트 파일과 아티팩트 디렉토리 마운트. CLI는 컨테이너 안에서 실행(이미지에 CLI와 인증 포함 필요). | 리서치·문서, 보안 격리 |
| `none` | 데몬의 세션 작업 디렉토리 아래 lane별 하위 폴더. | 가벼운 작업 |

세 방식 모두 lane 종료 시 아티팩트 디렉토리를 서버에 동기화한다.

**FR-6.5 합류(Join)** — 모든 자식 lane 완료 시 위임자 에이전트에 `progress_update` 시스템 메시지로 결과 묶음이 전달되어 종합 run이 트리거된다. 일부 실패 시에도 실패 사유와 함께 전달.

### FR-7. Task 실행 및 관찰

**FR-7.1 상태 머신** (multica 준용)

```
deferred → queued → dispatched → preparing → running → completed
                                               ↓  ↑
                                          waiting_human
                                               ↓
                                          failed | cancelled
```

- `preparing`: 데몬이 워크트리/컨테이너를 만드는 단계.
- `dispatched` 5분 초과 → `failed(timeout)`. `running`은 15초 heartbeat, 3분 무응답 시 런타임 offline 처리.
- 재시도: `runtime_offline`, 네트워크, 프로세스 stall은 2~3회 자동(대체 프로파일이 있으면 전환), 인증/쿼터/설정 오류는 재시도 없음.

**FR-7.2 활동 피드 (Activity Feed)** — 데몬이 보낸 `task_event(seq)`를 사람이 **읽지 않고 훑어서 판단할 수 있는** 형태로 렌더한다. block/buzz의 활동 피드 설계를 채택한다. 이 피드의 목적은 감상이 아니라 **개입 여부 판단**이며, 모든 항목은 세 질문 중 하나에 답해야 한다: 지금 무엇을 왜 하는가(이해), 잘 되고 있는가(확신), 내가 끼어들어야 하는가(통제).

*지배 프레임* — 모든 항목은 한 문장이다: **에이전트가 [동사]를 [목적어]에 했다 → [결과].**
> "#design에 메시지를 보냈다." · "`runtime.rs`를 수정했다 (+12/−3)." · "테스트를 실행했다 → 1248개 통과." · "Director에게 승인을 요청했다 → 대기 중."

전체 인자, 원본 출력, 전체 diff는 펼쳐야 보이는 곳으로 내린다. 문장을 읽고, 궁금해질 때만 펼친다.

*렌더 클래스* — 모든 이벤트는 정확히 하나의 클래스에 속한다. 마지막 세 개가 "어떤 이벤트든 표현할 수 있다"는 바닥을 보장한다.

| 묶음 | 클래스 |
|---|---|
| 중추 (항상 읽힘) | 메시지(에이전트의 목소리), 플랫폼 조작(`colab` 호출), 파일 편집, 셸 명령, 툴 상태·턴 생명주기 |
| 판단 근거 (필요할 때 참조) | 사고 과정, 계획·할 일 목록, 권한 요청, 오류 |
| 안전망 (드물게 읽히지만 반드시 존재) | 일반 툴(정직한 폴백), 원본 레일, 억제된 잡음 |

*원칙*

- **전송 방식이 아니라 의미로 렌더한다.** MCP 툴로 보낸 메시지와 `colab message post` 셸로 보낸 메시지는 **같은 카드**로 보인다. 에이전트가 플랫폼에 어떻게 도달했는지는 배관이고, 무엇을 했는지가 계약이다. (§8.2에서 런타임마다 경로가 다르므로 이 원칙이 특히 중요하다.)
- **결과를 먼저 말한다.** 성공·실패·결과값이 앞에 온다. 원본 덤프는 폴백이지 헤드라인이 아니다.
- **제자리에서 갱신한다.** 진행 중인 동작은 자기 행을 `대기 → 실행 중 → 완료|실패`로 바꾼다. 하나의 동작은 하나의 항목이며, 상태 줄을 쌓지 않는다.
- **절대 캄캄해지지 않는다.** 이벤트의 부재도 정보다. 침묵·유휴·타임아웃은 "대기 중…", "응답 없음"으로 **렌더되는 상태**이지 빈 화면이 아니다. 우리가 에이전트에게 요구하는 규칙과 같다 — 보여주지 않았으면 일어나지 않은 것이다.
- **실패는 크게, 읽기는 작게.** 눈에 띄는 정도가 결과의 무게를 따라간다. 쓰기·관리 동작·오류는 크게, 조회와 사고 과정은 조용하게. 묻힌 오류는 고장난 피드다.
- **참조를 해소한다.** `#design`, "Director의 메시지", 파일명으로 보여준다. 원본 id나 uuid를 노출하지 않는다.
- **스트림을 합친다.** 청크로 도착한 텍스트는 하나의 항목이다. 사람은 메시지를 읽지 패킷을 읽지 않는다.
- **부풀리지 않는다.** 인식된 동작은 의미 카드를, 인식하지 못한 동작은 깨끗한 일반 행을 받는다. 더 풍부해 보이려고 의미를 지어내지 않는다.
- **기본은 정제, 원본은 요청 시.** 원본 레일은 안전망이고, 둘 사이의 전환은 같은 진실의 확대·축소이지 다른 피드가 아니다.

*강등* — 런타임이 구조화 이벤트를 주지 못하면(§8.2.6) 메시지 카드와 원본 레일만 남는다. 이때도 "이 런타임은 툴 단위 로그를 제공하지 않습니다"를 명시해 침묵과 구분한다.

**FR-7.3 비용** — task/agent/세션/런타임 단위 토큰·비용 집계. 런타임이 사용량을 보고하지 않으면(어댑터 능력 `usage=false`) 추정치 표시.

**FR-7.4 에이전트 툴 표면 (`colab` CLI)** — multica가 `multica issue comment add`로 에이전트가 플랫폼에 되돌아오게 하듯, 본 서비스는 `colab` CLI를 데몬과 함께 설치한다. 어떤 런타임이든 셸을 실행할 수 있으면 동일하게 사용한다. Claude Code처럼 MCP를 지원하는 런타임에는 같은 기능의 **MCP 서버**도 제공한다.

| 명령 | 설명 |
|---|---|
| `colab session get` / `colab session messages` | 세션 정보·히스토리 조회 |
| `colab message post --content-file --parent <id>` | 메시지/답글 게시 (멘션 포함 가능) |
| `colab lane delegate --agent --brief --depends-on --profile` | lane 생성 + 멘션 메시지 자동 작성 (대상은 참여자로 제한) |
| `colab hitl ask` / `approve-request` / `request-info` | HITL (수신자 Director) |
| `colab artifact submit` / `get` | 아티팩트 |
| `colab decision record --summary --rationale` | 결정 기록 |
| `colab status set working\|blocked\|done --note` | 자기 상태 갱신 |
| `colab review approve\|reject --artifact --comments` | 리뷰어 승인/반려 |

CLI는 task마다 발급되는 단기 토큰(`COLAB_TASK_TOKEN`)으로 인증하며, 해당 task의 세션·에이전트 범위 밖에는 접근할 수 없다.

### FR-8. 인박스 및 알림

- 사용자 전용 인박스. 항목 타입: `hitl_request`(action_required, Director에게만), `session_completed`, `session_paused`, `run_failed`, `runtime_offline`, `mention`.
- 세션 구독 설정: 전부 / HITL만 / 종료만.
- 에이전트는 인박스가 없다. 에이전트 간 통신은 전부 세션 메시지.

### FR-9. Runtime (데몬) 관리

- **Runtimes** 화면: 연결된 머신 목록, 온라인 상태, 감지된 CLI와 버전, 로그인 상태, 실행 중 task 수.
- **Add a computer**: 설치 스크립트 + 페어링 토큰 2줄 제공 (multica 방식). 데스크톱 앱은 v2.
- 데몬은 시작 시 `claude`, `antigravity`, `hermes` 바이너리를 탐지하고 각 어댑터의 `probe()`로 버전·모델 목록·로그인 여부를 보고한다. 이후 60초마다 갱신.
- 데몬 전역 동시 task 상한(기본 10), 런타임별 상한 설정.

---

## 6. UX / 화면

| 화면 | 주요 요소 |
|---|---|
| **Runtimes** | 머신 카드(온라인, CLI 목록·버전·로그인), Add a computer |
| **Agents** | 카드 목록 (이름·역할·상태·기본 프로파일), 새 에이전트, Build with AI, 템플릿 |
| **Agent 편집** | FR-1.1 폼, 프로파일 편집기(런타임 종류 → 모델 → 옵션), 권한, 툴, 테스트 채팅 |
| **Sessions** | 목록 (상태·goal·Director·비용·참여자 아바타), 새 세션 마법사 (goal → Director → 참여자·프로파일 → 격리 → 종료 조건 → 한도) |
| **Session 상세** | 좌: 참여자(프로파일 표시) + lane 보드 / 중: 메시지 타임라인(스레드, HITL 카드, run 펼치기) / 우: goal·종료조건 진행률·아티팩트·결정 기록·비용 |
| **Inbox** | HITL 응답 UI(Director), 세션 바로가기 |
| **Settings** | 멤버, 런타임 정책, 예산 정책, 루프 상한, **컨텍스트 재사용 상한**, 기본 격리 방식, 알림 |

멘션 자동완성, 트리거 미리보기, 실시간 스트리밍(에이전트 타이핑 표시)은 필수.

---

## 7. 데이터 모델

```
workspace 1─N member (user_id, role: owner|admin|member)
workspace 1─N runtime (name, host, status, daemon_version, last_seen_at,
                       capabilities(jsonb: [{kind, version, models[], logged_in}]))
workspace 1─N agent (name, role, role_description, instructions, tools,
                     permission, max_concurrent_tasks, status, archived_at)
agent     1─N agent_profile (name, runtime_kind, runtime_id?, model, options(jsonb),
                             env(jsonb), args[], is_default, fallback_profile_id?)
workspace 1─N session (title, goal, acceptance_criteria[], director_user_id,
                       deputy_director_user_id?, assignee_agent_id,
                       isolation(jsonb: {kind, repo_path?|image?}),
                       completion_condition(jsonb), limits(jsonb), autonomy,
                       context_reuse_override(jsonb)?, status, cost_usd, created_by)
session   1─N session_participant (agent_id, profile_id, joined_at)
session   1─N session_context (type: doc|url|file|session, ref, summary)
session   1─N message (author_type, author_id, parent_id, content,
                       mentions[], source_task_id, kind: text|hitl|system|summary)
session   1─N lane (parent_lane_id, agent_id, profile_id, depends_on[],
                    workspace_ref(worktree path | container id), status)
lane      1─N task (agent_id, profile_id, runtime_id, trigger_message_id,
                    delegated_from_task_id, originator_user_id,
                    coalesced_message_ids[], attempt, max_attempts,
                    status, failure_kind, started_at, finished_at)
task      1─N task_event (seq, class, verb, object_ref, outcome,
                          tool, input, output, usage, superseded_by)
task      1─1 task_usage (input_tokens, output_tokens, cache_read, cost_usd, estimated)
session   1─N hitl_request (task_id, type, question, options[],
                            token_hash, resume_at, approver_spec, due_at,
                            status: open|answered|expired,
                            approved, answer, answered_by, answered_at)
session   1─N artifact (name, version, type, storage_ref, submitted_by_task_id)
session   1─N decision (summary, rationale, source: hitl|agent, ref_id)
member    1─N inbox_item (type, severity, session_id, ref_id, read_at)
workspace 1─1 workspace_settings (loop_limits, budget_policy, context_reuse,
                                  default_isolation, runtime_policy,
                                  default_mid_turn_policy)
workspace 1─N activity_log
```

- 멘션은 `message.mentions[]`에 `{kind, id}`로 정규화해 저장하고, 본문에는 원문 링크를 유지한다.
- `agent`는 정의에서 온 필드와 인스턴스 필드를 함께 갖는다(FR-1.8). `definition_source`(파일 경로·팩 id·수동)와 `definition_version`을 보관해 업데이트 여부를 표시한다.
- `session.director_user_id` 변경, `agent.respond_to` 변경은 `activity_log`와 시스템 메시지에 남긴다.
- `hitl_request.token_hash`에는 `SHA-256(token)`만 저장한다(FR-5.4). 원본 토큰은 저장하지 않는다.
- `task_event`는 활동 피드의 렌더 단위다(FR-7.2). `class`는 렌더 클래스, `verb`/`object_ref`/`outcome`은 한 문장 렌더용이며, 제자리 갱신은 새 행을 쓰고 이전 행의 `superseded_by`를 채우는 방식으로 이력을 잃지 않고 표현한다.

**고빈도 이벤트는 영속화하지 않는다.** 프레즌스, 타이핑·생성 중 표시, 토큰 델타 같은 신호는 실시간 채널로만 흐르고 `task_event`·`activity_log`·검색 색인에 들어가지 않는다. 영속 대상은 "나중에 누가 왜 그랬는지 물을 수 있는 것"으로 한정한다. 이 구분이 없으면 감사 로그가 스트리밍 잡음에 묻힌다.

---

## 8. 아키텍처

### 8.1 구성

```
[Web Client (Next.js)]
      │ WebSocket / SSE
[API Server (Go)]  ──  [Router: 멘션 파싱 → task enqueue]
      │                        │
[Postgres]              [Task Queue (Postgres SKIP LOCKED)]
      │                        │  long-poll / WS
[Platform LLM (Claude API)]   [Runtime Daemon (사용자 머신)]
  Build with AI, 세션 요약,       │
  criteria 판정, 미리보기 요약     ├─ ACP Harness ──stdio JSON-RPC──> claude-code-acp
                                 │   (1차 경로)                      hermes acp
                                 │                                   codex-acp / goose …
                                 ├─ CLI Adapter (폴백) ─────────────> agy -p …
                                 ├─ isolation: worktree | container | none
                                 └─ `colab` CLI + MCP server (에이전트가 호출)
```

- **Router**: 메시지 생성 시 FR-3.3 규칙을 적용하고 task를 enqueue. 동기 API 경로에서 LLM 호출 없음.
- **Daemon**: task를 claim → lane 작업 공간 준비 → 브리프 파일 작성 → **ACP 하네스**(또는 폴백 CLI 어댑터)로 런타임 프로세스 구동 → 이벤트를 `task_event`로 전송 → 종료 사유·사용량 보고. 데몬은 stateless, 상태는 서버.
- **Platform LLM**: 에이전트 실행이 아닌 플랫폼 기능(Build with AI, 세션 요약, `criteria_met` 판정, 트리거 미리보기 요약)만 Claude API를 직접 호출한다.

### 8.2 런타임 실행 계층 — ACP 1차, CLI 폴백

**결정 (v0.4):** 런타임 통합의 1차 경로는 **ACP(Agent Client Protocol)** 이고, 런타임별 CLI 플래그 조립은 ACP 어댑터가 없는 런타임의 **폴백**이다.

**근거.** block/buzz는 `buzz-acp` 하네스 하나로 Goose·Claude Code·Codex·자체 에이전트를 동일하게 구동하며, 프리셋에 Hermes를 포함한 십여 개 런타임이 등록되어 있다. 우리가 필요한 세 동작 — 세션 생성/재개, 권한 요청 응답, 툴 호출·텍스트·사용량 스트리밍 — 은 ACP에 모두 표준화되어 있다. CLI 플래그 방식은 런타임마다 인자·출력·resume·오류 분류가 전부 달라 어댑터 3개가 사실상 서로 다른 프로그램이 된다(§8.2.4의 폴백 스펙이 그 증거다). ACP를 1차로 두면 런타임 추가 비용이 "프리셋 한 줄 + 실행 커맨드"로 줄어든다.

**8.2.1 통합 하네스 인터페이스**

```
interface RuntimeHarness {
  transport: "acp" | "cli"
  probe(): { installed, version, logged_in, models[], capabilities }
  capabilities: {
    permission_negotiation: boolean  // 권한 요청/응답을 프로토콜로 처리
    resume: boolean                  // 세션 재개
    structured_events: boolean       // tool call 단위 이벤트 (활동 피드 렌더 가능)
    usage: boolean                   // 토큰/비용 보고
    model_select: boolean            // 세션 내 모델 지정
    effort: boolean                  // 사고 깊이 옵션 (카탈로그가 광고할 때만)
    steering: boolean                // 진행 중 턴에 메시지 주입 (FR-3.4)
    mcp: boolean                     // MCP 서버 주입
  }
  start(lane, profile): SessionHandle           // ACP: initialize + session/new|resume
  prompt(handle, turn): AsyncIterable<TaskEvent>
  steer(handle, message)                        // capabilities.steering 일 때만
  cancel(handle)
}
```

능력은 **런타임 이름이 아니라 세션이 광고한 값**으로 판정한다. 같은 프로토콜 키를 쓰는 서로 다른 바이너리가 존재하고(예: Hermes 키를 공유하는 다른 구현), 버전에 따라 능력이 달라지기 때문이다.

**8.2.2 ACP 경로 (기본)**

- **핸드셰이크**: `initialize`(프로토콜 버전과 클라이언트 능력 선언) → `session/new {cwd, mcpServers, model}` 또는 재개 시 `session/resume {cwd, sessionId, mcpServers}` → 필요 시 `session/set_model`, 사고 깊이 옵션 설정 → `session/prompt`.
- **권한 요청**: `session/request_permission`이 오면 제시된 옵션 중 `kind == "allow_once"` 인 것을 골라 응답한다. **optionId를 하드코딩하지 않는다** — 런타임마다 값이 다르고 버전에 따라 바뀐다. 허용 옵션이 없으면 `reject_once`로 응답하고 해당 툴 호출을 활동 피드에 거부로 기록한다.
- **이벤트**: `session/update` 알림의 `agent_message_chunk`, `agent_thought_chunk`, `tool_call`, `tool_call_update`, `usage_update`, `turn_end` 를 그대로 `task_event`로 정규화한다(FR-7.2의 렌더 클래스에 대응).
- **취소**: 대기 중인 권한 요청에 `cancelled`로 먼저 응답 → `session/cancel` → `stopReason: "cancelled"` 수신까지 드레인. 중간에 프로세스를 죽이면 히스토리가 깨진다.
- **프로세스 위생**: 런타임 서브프로세스는 자체 프로세스 그룹으로 띄우고 모든 종료 경로에서 그룹 단위로 정리한다.
- **동시성**: 하네스 프로세스당 워커 수를 설정(기본 1, 상한 32)하고, **lane 하나당 진행 중인 턴은 항상 1개**로 제한한다(FR-6.3).

**8.2.3 v1 런타임 3종 — 경로와 능력**

| | Claude Code | Hermes | Antigravity |
|---|---|---|---|
| 1차 경로 | **ACP** (`claude-code-acp` 어댑터) | **ACP** (`hermes acp`, 네이티브) | **CLI 폴백** (ACP 어댑터 없음) |
| 폴백 경로 | CLI (`claude -p`, §8.2.4) | CLI (`hermes -z`) | — |
| 지시 파일 | `CLAUDE.md` | `AGENTS.md` | `AGENTS.md` (`GEMINI.md`가 있으면 우선) |
| MCP | ✓ | ✓ (런타임 `mcpCapabilities`에 맞춰 stdio/http 필터) | ✗ → `colab` CLI 셸 호출로 대체 |
| resume | ✓ | ✓ (유실 감지 필요, §8.2.5) | ✓ `--conversation <id>` (거부 감지 불가) |
| structured_events | ✓ | ✓ | ✓ stream-json (구버전은 텍스트+로그) |
| usage | ✓ 토큰 + 비용(추정) | ✓ 토큰 + 비용 | △ 토큰만, **세션 누적값** → 턴별은 차분 계산 |
| effort | ✓ low~max | △ 카탈로그가 광고할 때만 | ✓ low/medium/high |
| 권한 자동 승인 | ACP 협상, 폴백은 `--permission-mode` | ACP 협상 + `HERMES_YOLO_MODE=1` | `--dangerously-skip-permissions` |
| 헤드리스 인증 | OAuth 캐시 또는 `ANTHROPIC_API_KEY` | `~/.hermes/config.yaml` 프로바이더 설정 | 로그인 캐시, CI는 `modelProvider:"gemini"`+`GEMINI_API_KEY` |

**8.2.4 CLI 폴백 스펙** (ACP 어댑터 부재·설치 실패·프로토콜 버전 불일치 시)

*Antigravity (`agy`) — v1의 유일한 상시 CLI 경로*
```
agy -p --input-format stream-json --output-format stream-json \
  --dangerously-skip-permissions --model <id> --effort <low|medium|high> \
  --print-timeout <dur> --log-file <tmp> --add-dir <lane> [--conversation <id>]
```
stdin에 `{"event":"user","message":{"content":"…"}}`. `--print-timeout`은 **기본 5분이 긴 턴을 끊으므로 항상 명시**한다. 존재하지 않는 `--model`은 조용히 무시되므로 `agy models`(줄당 `id<TAB>label`) 카탈로그로 실행 전 검증한다 — **정적 폴백 목록을 두지 않는다**. 이벤트는 `init` → `step_update`(step_type: user_input|agent_response|tool|checkpoint) → `result`(status: SUCCESS|ERROR|CANCELED|INTERRUPTED|INVALID). stream-json 미지원 구버전은 stdout 텍스트 + `--log-file` 정규식 스캔(`conversation=<uuid>`, `Print mode: timed out`, `agent executor error:`)으로 강등한다.

*Claude Code (`claude`)*
```
claude -p --input-format stream-json --output-format stream-json --verbose \
  --permission-mode bypassPermissions --disallowedTools AskUserQuestion \
  --mcp-config <colab-mcp.json> --strict-mcp-config \
  --model <id> --effort <lvl> --max-turns <N> --max-budget-usd <usd> \
  [--resume <session_id>] --settings <settings.json>
```
프롬프트는 인자가 아니라 stdin에 한 줄 JSON으로(10MB 상한). `--bare`는 `CLAUDE.md`를 읽지 않으므로 **쓰지 않는다**. 세션 id는 `system/init`과 `result`의 `session_id`에서 얻는다. `--mcp-config` 서버 기동을 최대 30초(`MCP_TIMEOUT`) 대기하고, 종료는 SIGINT를 먼저 보낸다(SIGTERM은 exit 143·턴 미완료).

*Hermes (`hermes`)*: `hermes -z "<prompt>" --model <id> --yolo --usage-file <path> --in <lane>`. `--usage-file`이 `estimated_cost_usd`·토큰·`session_id`를 남긴다. 이벤트 스트림이 없어 활동 피드가 텍스트 한 덩어리로 강등되므로 ACP 실패 시에만 쓴다.

**8.2.5 런타임별 알려진 함정** (multica 구현에서 검증된 것)

| 런타임 | 함정 | 대응 |
|---|---|---|
| Claude Code | 헤드리스에서 `AskUserQuestion`이 빈 답을 반환 | disallow하고 HITL은 `colab hitl ask`로 일원화 |
| Claude Code | resume 거부 시 "no conversation found" 류 메시지 | `resume_rejected`로 분류해 새 세션으로 재시도 |
| Hermes | `state.db`에 없는 세션을 **오류 없이 새로 생성** | 세션 provenance 불일치, 또는 `stopReason=="refusal" && 턴 활동 0`으로 유실 감지 |
| Hermes | 상위 LLM이 4xx/5xx여도 `end_turn` 보고 | stderr에서 프로바이더 오류를 별도 감지 |
| Hermes | 마지막 메시지 청크가 프롬프트 응답 **뒤에** 도착 | 250ms 정적 대기 + 2초 드레인 후 완료 판정 |
| Antigravity | resume 거부를 구분할 수 없음(빈 conversation id) | 항상 새 세션 폴백 준비 |
| Antigravity | 특정 버전에서 stdout 0바이트·exit 0 완료 | 빈 출력 완료 시 실패로 간주하지 말고 재시도 후 에스컬레이션 |

**8.2.6 능력에 따른 UI 강등 규칙**

| 상황 | 표시 |
|---|---|
| `structured_events=false` (CLI 폴백 텍스트 모드) | 활동 피드가 메시지 카드 + 원본 레일만, 툴 타임라인 없음 |
| `usage` 토큰만 (Antigravity) | 비용 카드에 "추정" 배지, 워크스페이스 가격표로 계산 |
| `mcp=false` (Antigravity) | 브리프에 `colab` CLI 규약만 기재, MCP 툴 목록 미주입 |
| `steering=false` | FR-3.4 정책이 `steer`여도 `queue`로 자동 강등하고 그 사실을 표시 |
| `effort` 미지원 | 프로파일 편집기에서 effort 필드 비활성 |

### 8.3 Lead 코디네이션 프로토콜

Lead 역할의 브리프 파일에는 다음 섹션이 추가된다 (multica Squad Operating Protocol 준용).

- 직접 구현하지 않고 위임한다. 위임은 `colab lane delegate` 또는 정확한 멘션 링크로만 한다.
- **위임 대상은 세션 참여자 로스터에 있는 에이전트로 한정된다. 새 에이전트를 만들거나 요청할 수 없다.** 필요한 역할이 없으면 `colab hitl ask`로 Director에게 참여자 추가를 요청한다.
- 위임 후 턴을 종료한다. 자식 lane 결과가 `progress_update`로 돌아오면 종합한다.
- 불확실하거나 범위·리스크·트레이드오프가 걸린 결정은 `colab hitl ask`로 Director에게 올린다.
- 종료 조건이 충족되었다고 판단하면 `colab artifact submit` 또는 `colab status set done`으로 신호한다.

**모든 에이전트의 공통 규약** (§8.4의 [2] 구간에 포함)

- **빈 확인 메시지를 올리지 마라.** "알겠습니다", "확인했습니다"만 담긴 메시지는 멘션한 모두를 다시 트리거해 대화를 무한히 되돌린다. 할 말이 진행 상황·결과·질문 중 하나가 아니면 메시지를 만들지 않는다.
- **보여주지 않았으면 일어나지 않은 것이다.** 오래 걸리는 작업은 시작할 때 한 줄로 알리고, 끝나면 결과를 남긴다.
- 남에게 위임한 일이 끝나면 위임자에게 회신 멘션을 남긴다.

### 8.4 브리프 파일 구성 (런타임 지시 파일)

데몬이 lane 작업 공간에 쓰는 안정 브리프. 캐시 친화적으로 **변하지 않는 순서**를 유지한다.

```
[1] Agent Identity + instructions
[2] Workspace 규칙 + 멘션 문법 + colab CLI/MCP 사용 규약
[3] (lead만) Coordination Protocol
[4] Session: goal / acceptance_criteria / 종료 조건 / Director 이름 / 격리 방식
[5] Roster: 참여자별 이름·역할 설명·멘션 링크·현재 상태
[6] Context: 첨부 자료 요약, 이전 세션 요약 (설정 상한 내)
[7] Decision Log
[8] Instruction Precedence: 사용자 지시 > 세션 goal > 에이전트 instruction > 런타임 기본
```

- ACP 경로에서는 [1]~[8]을 `session/new`의 **시스템 역할로 한 번만** 전달하고 이후 턴에서 반복하지 않는다. 지시 파일 기록은 CLI 폴백 경로와, 지시 파일만 읽는 런타임을 위해 유지한다.
- 턴 프롬프트에는 트리거 메시지 인용, 위임 브리프, 최근 히스토리 N개, "응답은 `colab message post`로 게시하라"는 지시만 담는다.
- **steer로 재프롬프트할 때**(FR-3.4)는 이전 지시와 새 메시지를 각각 `<prior>`, `<new>` 구간으로 나누고, 새 메시지가 작업 도중 도착했음을 명시한다. 그냥 이어 붙이면 에이전트가 두 지시를 같은 시점의 요구로 읽고 충돌한다.
- 히스토리 구간에는 `included` / `total` / `truncated`를 함께 적어, 잘렸다는 사실을 에이전트가 알고 필요하면 `colab session messages`로 더 읽게 한다.

### 8.5 플랫폼 내부 Claude API 사용 원칙

플랫폼 기능(에이전트 실행 아님)에만 적용된다.

| 항목 | 결정 |
|---|---|
| 모델 | `claude-opus-5` 기본. 세션 요약·미리보기 등 경량 작업은 `claude-sonnet-5`. |
| Thinking | adaptive, `output_config.effort`로 조절. |
| 구조화 출력 | Build with AI, `criteria_met` 판정, 트리거 미리보기는 `output_config.format`으로 JSON 강제. |
| 캐싱 | 워크스페이스 규칙·판정 루브릭을 안정 prefix로 두고 `cache_control`. |
| 폴백 | `stop_reason == "refusal"` 처리, 서버사이드 `fallbacks: "default"` 활성. |
| 스트리밍 | 요약 생성 등 긴 출력은 스트리밍. |

---

## 9. 비기능 요구사항

| 항목 | 요구 |
|---|---|
| 지연 | 메시지 게시 → 데몬 claim p50 < 2s, → 에이전트 첫 출력 p50 < 10s (CLI 기동 포함) |
| 동시성 | 워크스페이스당 동시 running task 50, 데몬당 기본 10 |
| 내구성 | 데몬 재시작 시 `running` task는 heartbeat 만료 후 재큐잉, 중복 게시 없음 (idempotency key = task_id + seq) |
| 보안 | 에이전트 호출 권한은 **최초 사람 originator** 기준으로 판정 (에이전트 체인이 권한 상승 불가). `colab` CLI 토큰은 task 범위·단기. 코드는 사용자 머신을 떠나지 않으며, 서버에는 메시지·아티팩트·로그만 저장. |
| 감사 | 모든 메시지·툴 호출·HITL 응답·상태 전이·Director 변경은 activity_log. |
| 비용 | 세션·에이전트·워크스페이스 예산 상한. 초과 시 자동 `paused`. |
| 관측 | task 실패 분류별 비율, 런타임별 성공률, HITL 평균 응답 시간, 프로파일 폴백 발생률 대시보드. |
| i18n | UI 한국어/영어. 에이전트 instruction 언어 무관. |

---

## 10. 범위 및 로드맵

### v1 (MVP, 10주)

- [ ] 워크스페이스·멤버 CRUD
- [ ] **데몬 + Runtimes 화면 + ACP 하네스** — 핸드셰이크, 권한 협상(`allow_once` 탐색), 이벤트 정규화, 취소 절차, 프로세스 그룹 정리
- [ ] **런타임 3종**: Claude Code(ACP), Hermes(ACP), Antigravity(CLI 폴백). 구현 순서: **ACP 하네스 → Claude Code → Hermes → Antigravity CLI 어댑터**
- [ ] 에이전트 CRUD, **프로파일(멀티) 편집기**, `.agent.md` 내보내기/가져오기, Build with AI, 동적 생성 금지
- [ ] 호출 권한 게이트(`respond_to` + 형제 에이전트 규칙)
- [ ] 세션 생성(goal, **Director**, 참여자+프로파일, **격리 worktree/container/none**, 종료 조건 3종, 예산 상한, `mid_turn_policy`)
- [ ] 메시지·스레드·멘션 라우팅(FR-3.3 규칙 1~6), 트리거 병합, **steer 정책**
- [ ] `colab` CLI + MCP 서버, 브리프 파일 생성, 비용 집계(추정 포함)
- [ ] **활동 피드** — 중추 5개 렌더 클래스 + 일반 툴 + 원본 레일, 제자리 갱신, 침묵 상태 표시
- [ ] HITL `question` / `approval` (**승인 토큰 모델**, Director 수신), 인박스 "Needs Action" 질의
- [ ] 병렬 lane(의존성 없음), lane 보드, 프로파일 폴백 재시도
- [ ] 세션 자동 종료 + 요약, 컨텍스트 재사용 상한 설정

### v1.1

- [ ] lane 의존성(DAG), `supervised` 모드
- [ ] HITL `choice` / `info`, 기한, deputy director
- [ ] 결정 기록, 세션 컨텍스트 재사용(UI)
- [ ] 활동 피드 나머지 클래스(계획·할 일, 사고 과정, 억제된 잡음 토글)
- [ ] 루프 상한, assignee 폴백 지연 task
- [ ] `.agent.md` 팀 팩 배포·가져오기, 정의 버전 업데이트 알림
- [ ] Antigravity 전역 MCP 설정 주입 스파이크(`~/.gemini/config/mcp_config.json`), Gemini 가격표 기반 비용 추정
- [ ] Hermes `HERMES_HOME` 오버레이(에이전트별 스킬·메모리 분리)

### v2

- [ ] 추가 런타임(Codex, Goose, Cursor, OpenCode 등) — ACP 어댑터가 있으면 프리셋 등록만으로 추가
- [ ] 클라우드 런타임 — 유휴 시 스스로 종료하는 수명 관리, 프레즌스를 갱신 임대(lease)로 처리
- [ ] VCS 통합(워크트리 → PR 자동 생성·리뷰)
- [ ] 스케줄 세션(autopilot), Slack 연동
- [ ] 데스크톱 앱(데몬 내장)

---

## 11. 성공 지표

| 지표 | 목표 (v1 출시 3개월) |
|---|---|
| 데몬 설치 → 첫 세션 완료까지 시간 (신규 사용자) | 중앙값 < 15분 |
| 세션 자동 완료 비율 (수동 종료 대비) | > 60% |
| HITL 요청당 Director 응답 시간 | 중앙값 < 30분 |
| 에이전트 간 위임 메시지 중 사람 개입 없이 처리된 비율 | > 70% |
| 병렬 lane 사용 세션의 wall-clock 단축 | 단일 에이전트 대비 40% |
| 런타임별 task 성공률 | Claude Code > 95%, 기타 > 85% |
| 두 종류 이상의 런타임을 섞어 쓰는 워크스페이스 비율 | > 30% |
| 주간 활성 세션 / 활성 워크스페이스 | 5+ |

---

## 12. 리스크 및 오픈 이슈

| 리스크 | 완화 |
|---|---|
| Antigravity가 MCP를 헤드리스에서 못 받고, 비용을 보고하지 않음 | v1은 `colab` CLI 셸 호출로 대체, 토큰 기반 추정 비용에 "추정" 배지. stream-json 미지원 구버전은 로그파일 파싱 폴백 |
| Hermes가 유실된 세션을 오류 없이 새로 만들고, LLM 오류에도 `end_turn`을 보고 | 세션 provenance 비교 + "refusal & 활동 0" 감지, stderr 프로바이더 오류 스니핑 (multica 검증 방식) |
| Claude Code 헤드리스에서 `AskUserQuestion`이 빈 답 반환 | disallow + `colab hitl ask` MCP 툴로 HITL 일원화 |
| 에이전트 간 무한 핑퐁 | FR-3.5 hop/왕복 상한, Lead 프로토콜의 "위임 후 턴 종료" 규칙 |
| 컨텍스트 폭증으로 비용 급증 | 히스토리 inline 최소화 + CLI 조회, 브리프 파일 순서 고정, 세션 예산, 요약 길이 상한 설정 |
| HITL 남발로 자율성 저하 | `autonomy` 모드, HITL 요청에 "기본값 제안" 강제, 요청 빈도 지표 |
| Director 부재로 세션 정체 | deputy director(v1.1), 기한 초과 시 기본값 진행 옵션 |
| 종료 조건 판정 오류(조기 완료) | `criteria_met`은 단독 사용 금지 — 항상 승인 계열과 AND |
| 병렬 lane 간 산출물 충돌 | 워크트리/컨테이너 격리 + 합류 단계에서 Lead가 병합 |
| 컨테이너 격리 시 CLI 인증 전달 | 이미지에 CLI 포함 + 호스트의 인증 디렉토리 read-only 마운트 정책, 스파이크로 확정 |
| **ACP 어댑터가 서드파티 패키지**(`claude-code-acp`)라 버전 드리프트·중단 위험 | 어댑터 버전을 프로파일에 고정, probe에서 프로토콜 버전 불일치 감지 시 CLI 폴백으로 자동 강등(§8.2.4를 유지하는 이유) |
| ACP 권한 협상에서 `allow_once`가 없는 런타임 | `reject_once` 후 활동 피드에 거부 기록. 반복되면 해당 런타임을 CLI 경로로 전환 |
| steer가 되돌리기 어려운 작업을 중간에 끊음 | 파일 쓰기·커밋 진행 중인 lane은 `queue`로 자동 강등. 세션 기본값은 `steer`지만 lane 단위 예외 허용 |

**오픈 이슈**
1. `claude-code-acp` 어댑터의 성숙도·유지보수 상태 실측 — Claude Code CLI 직접 구동(§8.2.4) 대비 안정성이 떨어지면 Claude Code만 CLI를 1차로 되돌린다. **v1 착수 전 1주 스파이크로 판정**.
2. Antigravity `--output-format stream-json`이 지원되는 최소 `agy` 버전 확인, 그리고 전역 MCP 설정이 `-p` 모드에서 로드되는지 실측 (문서 미기재) — 1주 스파이크.
2. 컨테이너 격리에서 각 CLI의 로그인 상태를 어떻게 안전하게 전달할지. 후보: Claude Code `ANTHROPIC_API_KEY` 또는 `~/.claude` 마운트, Antigravity `modelProvider:"gemini"`+`GEMINI_API_KEY` 또는 `~/.gemini` 마운트, Hermes `~/.hermes/config.yaml` 마운트.
3. 워크트리 격리 세션 종료 후 브랜치 정리·병합 UX — v1은 Director 수동, v2 VCS 통합에서 자동화.
4. 프로파일 폴백 전환 시 이전 run의 CLI 세션 컨텍스트(resume)는 유실된다 — 브리프 파일 + 세션 히스토리로 충분한지 검증 필요.

---

## 부록 A. multica 참고 코드 경로

| 기능 | 경로 |
|---|---|
| 멘션 파싱 | `server/internal/util/mention.go` |
| 댓글 라우팅 규칙 | `server/internal/handler/comment.go` (`computeCommentAgentTriggers`) |
| 스쿼드 리더 프로토콜 | `server/internal/handler/squad_briefing.go` |
| 프롬프트 조립 / 브리프 파일 | `server/internal/daemon/prompt.go`, `daemon/execenv/runtime_config_sections.go` |
| 에이전트 → 플랫폼 회신 CLI | `server/internal/daemon/execenv/reply_instructions.go` |
| 태스크 상태·재시도 | `server/internal/service/task.go`, `server/pkg/taskfailure` |
| 에이전트 권한 판정 | `server/internal/handler/agent_access.go` |
| 에이전트·런타임 스키마 | `server/migrations/001_init.up.sql`, `server/pkg/db/generated/models.go` |
| 데몬/CLI 구조 | `CLI_AND_DAEMON.md`, `CLI_INSTALL.md` |
| 런타임 백엔드 인터페이스 (`Backend.Execute`, 25개 프로토콜 + omp) | `server/pkg/agent/agent.go`, `builtin_runtimes.go` |
| Claude Code 어댑터 | `server/pkg/agent/claude.go`, `claude_models.go`, `thinking.go` |
| Antigravity 어댑터 | `server/pkg/agent/antigravity.go`, `models.go` (agy models 파싱) |
| Hermes 어댑터 (ACP) | `server/pkg/agent/hermes.go`, `acp_usage.go`, `daemon/execenv/hermes_home.go`, `hermes_sessions.go` |
| 런타임별 브리프 파일 매핑 | `server/internal/daemon/execenv/runtime_config.go` |
| 가격표 | `server/internal/metrics/pricing.go` |
| 문서 | `apps/docs/content/docs/{agents,squads,mentioning-agents,tasks,inbox,issues,daemon-runtimes,providers}.mdx` |

## 부록 B. block/buzz 참고 코드 경로

v0.4에서 채택한 설계의 출처. Buzz는 Rust + Nostr 이벤트 로그 기반이라 저장소 아키텍처는 따르지 않고, 아래 항목만 참고했다.

| 채택한 것 | 경로 |
|---|---|
| ACP 하네스 전체 구조 (풀, 큐, 스코프, 프롬프트 프레이밍) | `crates/buzz-acp/src/{lib,pool,queue,scope,prompt_framing}.rs` |
| ACP 프로토콜 처리 — 핸드셰이크, `allow_once` 탐색, 취소 절차 | `crates/buzz-acp/src/acp.rs` |
| 턴 진행 중 메시지 처리 정책 (`multiple-event-handling`) | `crates/buzz-acp/src/config.rs` |
| 런타임 프리셋·능력 선언 | `desktop/src-tauri/src/managed_agents/discovery.rs`, `discovery/presets.rs` |
| 활동 피드 설계 (동사·목적어·결과, 렌더 클래스, 원칙) | `VISION_ACTIVITY.md` |
| 승인 게이트 — 토큰 해시 저장, 재개 지점, approver spec | `crates/buzz-workflow/src/{executor,schema}.rs`, `schema/schema.sql` (`workflow_approvals`) |
| "Needs Action" 인박스 질의 | `crates/buzz-db/src/store/feed.rs` |
| 이식 가능한 에이전트 정의 파일 (`.persona.md`) | `crates/buzz-persona/src/{persona,manifest}.rs`, `examples/meadow-core/agents/*.persona.md` |
| 정의/인스턴스 분리 | `desktop/src-tauri/src/managed_agents/types.rs` |
| 호출 권한 게이트 + 형제 에이전트 규칙 | `crates/buzz-acp/src/lib.rs` (`respond-to`), `crates/buzz-sdk/src/nip_oa.rs` |
| 빈 확인 메시지 금지 등 공통 규약 | `crates/buzz-acp/src/base_prompt.md` |
| 메모리 조회 실패와 부재의 구분 | `crates/buzz-acp/src/engram_fetch.rs` |
| 컨텍스트 압축(자기 요약 핸드오프) | `crates/buzz-agent/src/handoff.rs` |
| 클라우드 런타임 수명 관리 (v2 참고) | `VISION_REMOTE_AGENTS.md` |

**채택하지 않은 것**: Nostr 이벤트 로그를 저장 substrate로 쓰는 구조, 키페어 기반 에이전트 아이덴티티, YAML 워크플로 엔진(우리는 lane DAG로 대체), 공유 작업 디렉토리 모델(우리는 lane 격리).

## 부록 C. 런타임 공식 문서 출처

| 런타임 | 문서 |
|---|---|
| Claude Code | [Run Claude Code programmatically (headless)](https://code.claude.com/docs/en/headless), [CLI reference](https://code.claude.com/docs/en/cli-reference) |
| Antigravity | [Headless mode](https://antigravity.google/docs/cli/headless/), [Getting started](https://antigravity.google/docs/cli/getting-started), [MCP](https://antigravity.google/docs/cli/mcp/), [Best practices](https://antigravity.google/docs/cli/best-practices/), [antigravity-cli #60 (프로젝트 로컬 MCP 무시)](https://github.com/google-antigravity/antigravity-cli/issues/60) |
| Hermes | [CLI Commands Reference](https://hermes-agent.nousresearch.com/docs/reference/cli-commands), [CLI Interface](https://hermes-agent.nousresearch.com/docs/user-guide/cli), [MCP](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp), [Sessions](https://hermes-agent.nousresearch.com/docs/user-guide/sessions) |
