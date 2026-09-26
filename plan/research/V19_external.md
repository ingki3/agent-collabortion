# V19-C · 외부 사례 대조 — 방/스레드 기반 에이전트 협업 설계

| 항목 | 내용 |
|---|---|
| 대상 | PRD **v0.19**(`origin/docs/prd-rooms:PRD.md`, 1556행) — 방(Room)·일(Work)·방 맥락(FR-4)·다른 방 읽기(FR-4.5)·에이전트 다중 참여 |
| 방법 | 공개 문서·공식 개발자 문서·제품 블로그를 WebSearch/WebFetch 로 읽고 대조. **출처 없는 단정은 쓰지 않았고, 확인 못 한 것은 「미확인」으로 적었다.** |
| 범위 밖 | 코드·문서 수정, 커밋, PR **없음**(조사 전용 지시) |
| 작성 | 2026-09-22 |

**한 문단 결론.** 우리 3층 구조(**방 = 대화 공간 / 일 = 끝이 있는 추적 단위 / lane·task = 지금 누가 무엇을**)는 외부에서 독립적으로 수렴한 구조다 — 특히 **Linear 의 `Issue → AgentSession → AgentActivity`** 가 우리 `Work → lane → task_event` 와 거의 1:1로 맞는다. 크로스 읽기(FR-4.5)의 **2중 조건**(사람 originator + 에이전트 참여)은 Slack Agentforce·Atlassian Rovo·M365 Copilot 이 모두 채택한 **1중 조건**(사용자 권한만)보다 **엄격한 쪽**이고, Copilot 의 oversharing 사례가 그 엄격함을 정당화한다. 반대로 외부가 오래 다듬었고 우리에게 없는 것은 **대화 공간이 길어졌을 때의 운영 장치**다 — 요약의 범위·출처, 알림·읽지 않음의 단위, 아카이브의 의미, 방을 가로지르는 지식 저장소. §3 에 7건을 권고로 정리했다.

---

## 0. 읽는 법

- **대응 표기**: 우리 개념 → 외부 개념. `방`=Room, `일`=Work, `lane`=병렬 실행 트랙, `task`=한 번의 실행.
- **미확인**: 공개 문서에서 확인하지 못한 것은 문장 안에 「미확인」으로 표시했다. 추측을 사실처럼 쓰지 않았다.
- 인용부호 안의 영문은 원문 그대로다.

---

## 1. 제품별 한 줄 요약 + 우리 개념과의 대응

### 1.1 요약

| # | 제품 | 한 줄 |
|---|---|---|
| A | **Slack** (채널·스레드·앱/에이전트) | 지속되는 채널 + 스레드가 뼈대. 봇은 **스코프 + 채널 초대 둘 다** 있어야 읽고, 에이전트는 채널과 분리된 **assistant/agent 세션**을 따로 갖는다 |
| B | **Discord** | 서버 → 채널 → 스레드. 권한은 **채널별 오버라이트**로 봇도 멤버와 같은 규칙, 메시지 본문 읽기는 **특권 인텐트**로 승인을 받아야 한다. 스레드는 **비활동 시간으로 자동 아카이브** |
| C | **Microsoft Teams** | 팀 → 채널. 앱·에이전트 권한은 **RSC**(리소스별 동의)로 **그 팀/채팅 인스턴스에만** 준다. Copilot 에이전트는 **사용자의 기존 권한을 그대로 따른다** |
| D | **Linear** | 이슈 = 일. 에이전트는 **멘션·위임으로 `AgentSession` 이 자동 생성**되고, 세션이 6개 상태를 사용자에게 드러낸다. **delegate ≠ assignee** — 사람이 소유권을 유지한다 |
| E | **Atlassian Rovo** (Jira/Confluence) | 에이전트는 **호출한 사람의 권한으로 행동**한다. 자동화 경로에서 신원이 바뀌는 구멍이 커뮤니티에 보고되어 있다 |
| F | **OpenAI Assistants/Threads** → Responses+Conversations | 스레드 = 서버 보관 대화 상태. **2026-08-26 종료**, Thread → Conversation **자동 이관 도구 없음** |
| G | **Anthropic Claude Projects** | 프로젝트 = 지식 + 지시 + 대화 묶음. 팀/엔터프라이즈에서 **프로젝트 단위로 공유**하고 멤버 권한은 보기/편집 2단 |
| H | **Cursor**(Slack 연동 · Cloud Agents) | Slack 스레드에서 `@cursor` → 에이전트가 **스레드 전체를 읽고** PR 을 만든다. **채널 단위 기본 설정**이 개인 기본값을 덮고, 병렬 실행은 **git worktree/원격 VM** |
| I | **Devin** (Cognition) | 세션 = 한 번의 작업 공간. **Knowledge** 가 세션을 가로질러 남는다. 메인 세션이 관리 Devin 들에게 **쪼개 위임하고 결과를 합친다** |
| J | **LangGraph** | `thread_id` + checkpointer = **스레드 범위 단기 상태**, `Store` = **네임스페이스 장기 기억**. 둘을 구조적으로 분리한다 |
| K | **AutoGen** (AgentChat) | GroupChat 은 **모든 참가자가 같은 메시지 맥락을 브로드캐스트로 공유**한다. Swarm 은 handoff 메시지로만 다음 화자를 정한다 |

### 1.2 우리 개념과의 대응표

| 제품 | 방 = ? | 일 = ? | 맥락 공유 = ? | 크로스 읽기 = ? |
|---|---|---|---|---|
| **Slack** | 채널(지속·아카이브만, `completed` 없음) | **없음**(스레드는 대화이지 추적 단위가 아님; Slack 리스트/워크플로가 별도 층) | 채널 히스토리 + 스레드. 봇은 **초대된 채널만** | 봇이 다른 채널을 읽으려면 **그 채널에 초대**되어야 한다. Agentforce 는 "Agents in Slack use your Slack and Salesforce permissions … will never reply with information that you don't have access to" |
| **Discord** | 채널(+서버) | 없음 | 채널 히스토리. 본문 읽기는 특권 인텐트 | 채널별 권한 오버라이트로 통제. 별도 「읽기 요청」 개념 없음 |
| **Teams** | 팀·채널 | Planner/To Do 등 **별도 앱** | 채널 메시지. 앱은 **RSC 로 그 인스턴스만** | Copilot 은 사용자 권한 밖 콘텐츠를 노출하지 않는다("if a user does not have access to a SharePoint site, Teams channel, or mailbox, the agent cannot surface content") |
| **Linear** | **워크스페이스/프로젝트**(대화 공간이 약함) | **Issue** | 이슈 본문 + 코멘트 스레드가 `promptContext` 로 주입 | 스코프(`app:mentionable`, `app:assignable`, `customer:read` 등)로 제어. 이슈 간 자유 읽기 개념은 **미확인** |
| **Rovo** | 없음(Jira/Confluence 전역) | Work item | 사용자 권한 범위의 전체 콘텐츠 | **사용자 권한 1중 조건** — "the agent is acting on that person's behalf and can only return or interact with … information that the user has permission to access" |
| **OpenAI Assistants** | 없음 | 없음(Run = 한 실행) | **Thread** = 서버 보관 메시지 열 | 없음(스레드 간 격리) |
| **Claude Projects** | **프로젝트** | 없음 | 프로젝트 지식 + 지시 + 대화. 팀 공유 | 프로젝트 간 자동 공유 없음(멤버십으로 통제) |
| **Cursor** | Slack **채널**(기본 설정이 붙는 자리) | 한 번의 `@cursor` 요청 → PR | **스레드 전체**("Cloud Agents read the entire thread for context when invoked") | `channels:history` 스코프로 스레드 이전 메시지를 읽는다. 채널을 넘는 읽기는 **미확인** |
| **Devin** | 없음(세션이 최상위) | **세션** | 세션 안 + **Knowledge**(세션 가로지름) | 메인 세션이 관리 Devin 결과를 **모아서** 합친다("compiles the results") — 자식끼리 서로 읽지 않는다 |
| **LangGraph** | (없음) | `thread_id` | checkpointer = 스레드 범위 | `Store` 네임스페이스 = **명시적으로 꺼내 쓰는** 스레드 밖 기억 |
| **AutoGen** | GroupChat | (없음) | **전원 브로드캐스트** — 같은 맥락 | 없음 |

**표에서 읽히는 것 셋.**

1. **"방"과 "일"을 둘 다 1급으로 둔 제품은 드물다.** 채팅 계열(Slack·Discord·Teams)은 방만 있고 일이 없다(별도 앱으로 뺀다). 이슈 계열(Linear·Jira)은 일만 있고 방이 약하다. 에이전트 계열(Devin·OpenAI·Cursor)은 **세션만** 있다. 우리는 셋을 한 제품 안에 넣는다 — 이것이 우리 설계의 고유 지점이자 가장 큰 비용이다.
2. **에이전트의 맥락을 "대화 공간마다 따로" 두는 것은 표준이다.** Slack 은 assistant 세션을 채널과 분리하고, LangGraph 는 `thread_id` 로 격리하며, Devin 은 관리 Devin 마다 "a clean slate, a narrow focus, its own shell". 우리 §3 「Agent Context 는 방마다 따로」는 외부와 일치한다.
3. **크로스 읽기는 대부분 "사용자 권한" 한 겹이다.** 우리만 두 겹(originator + 에이전트 참여)이다. §2·§4 에서 다시 본다.

---

## 2. 우리가 다른 점과 그 이유

### 2.1 의도된 차이 — 「에이전트가 실제 파일을 고치는 노동자」라는 전제에서 나온 것

| # | 우리 | 외부 | 왜 달라도 되는가 |
|---|---|---|---|
| D1 | **방에 `runtime_id`·격리·workdir 가 붙는다**(FR-2.3·FR-6.1) | Slack·Discord·Teams 의 봇에는 그런 개념이 없다. 봇은 상태 없는 응답자다 | 우리 에이전트는 **디스크에 쓴다.** 어느 머신·어느 폴더인지가 대화 공간의 속성이 되어야 위임·리뷰가 성립한다. Cursor 가 같은 문제를 만나 **채널 단위 기본 저장소·브랜치 설정**을 만든 것이 방증이다 |
| D2 | **lane 이 1급**(병렬·DAG·합류) | Linear 의 `AgentSession` 은 병렬을 표현하지만 **합류(join) 개념이 없다**. AutoGen GroupChat 은 순차 화자 선택 | 여러 에이전트가 같은 저장소를 병렬로 고치면 합류 지점이 필요하다. Devin 의 "메인 세션이 결과를 compile" 이 같은 필요를 다른 방식(중앙 집중)으로 푼 것 |
| D3 | **크로스 읽기 2중 조건**(FR-4.5) | Rovo·Copilot·Agentforce 는 **사용자 권한 1중** | 그들 에이전트는 **읽고 답할 뿐**이지만, 우리 에이전트는 읽은 것을 **코드와 아티팩트로 실행**한다. 유출 경로가 대화가 아니라 파일이다. §4 N2 의 Copilot 사례가 1중 조건의 실패를 보여준다 |
| D4 | **에이전트가 일(Work)을 스스로 열 수 없다**(FR-2A.1, 제안만) | Linear 는 에이전트가 `agentSessionCreateOnIssue`·`agentSessionCreateOnComment` 로 **세션을 스스로 연다** | 우리 「일」은 Linear 의 세션이 아니라 **Issue** 에 대응한다(§2.2). Linear 도 **Issue 를 에이전트가 만들게 하지는 않는다**. 즉 차이가 아니라 층이 다른 것이고, §12.1-2 의 열린 질문은 "세션(=우리 lane) 자동 개설" 로 읽으면 이미 우리도 허용하고 있다(멘션 → task) |
| D5 | **방은 완료되지 않는다**(FR-2.4) | Slack 채널과 같다 — 아카이브·삭제만 | 수렴. 외부 근거가 있으니 유지 |

### 2.2 가장 중요한 대응 — 우리 「일」은 스레드가 아니라 **이슈**다

지시가 특히 물은 항목이다. 정리하면:

```
Linear:   Issue        →  AgentSession        →  AgentActivity(thought/action/elicitation/response/error)
우리:     Work(일)      →  lane                →  task_event(class/verb/object_ref/outcome)
Slack:    (없음)        →  assistant/agent 세션  →  agents.sessions.setStatus
```

- **스레드는 「대화의 묶음」이고 일은 「끝 판정이 있는 추적 단위」다.** Linear 도 코멘트 스레드와 `AgentSession` 을 분리했다 — 세션은 스레드 위에 얹히지만, **상태(`pending/active/error/awaitingInput/complete/stale`)를 갖는 쪽은 세션**이다. 우리도 메시지 스레드는 상태가 없고 lane·work 만 상태를 갖는다. **일치한다.**
- **차이 1 — 우리 일은 여러 lane 을 묶는다.** Linear 의 세션은 에이전트 하나의 실행 하나다. 우리 일은 Lead·Backend·Frontend·QA 의 lane 을 한 종료 조건 아래 묶는다. 이것이 우리 고유 층이고, Linear 에서 이에 대응하는 것은 **Issue + sub-issue + delegate** 조합이다.
- **차이 2 — Linear 는 「위임(delegate)」과 「배정(assignee)」을 분리했다.** "Assigning an issue to your app now sets it as the `delegate`, not the `assignee` — so humans maintain ownership while agents act on their behalf." 우리 데이터 모델은 이미 `work.director_user_id`(사람) + `work.assignee_agent_id`(에이전트)로 나뉘어 있는데, **PRD 문장에는 그 의미(사람이 소유권을 유지한다)가 적혀 있지 않다.** §5 에 문장을 제안한다.

### 2.3 놓친 것 — 「방이 오래 산다」가 데려오는 운영 문제

v0.19 는 **세션을 방으로 늘리는 것**에 집중했고, 채팅 제품들이 10년 넘게 다듬어 온 **긴 대화 공간의 운영 장치**를 아직 가져오지 않았다. 아래 넷은 차이가 의도된 것이 아니라 **아직 안 본 것**으로 보인다.

| # | 공백 | 외부는 어떻게 하나 | PRD 자리 |
|---|---|---|---|
| M1 | **요약의 시작점·출처가 없다.** FR-2.5 「여기까지 정리」는 끝점만 있고 시작점이 없다 | Slack AI: "summarize just your unread messages, the last seven days, or a custom date range" + "Clear sources are cited in each summary" | FR-2.5 |
| M2 | **알림·읽지 않음의 단위가 방뿐이다.** FR-8 구독은 전부/HITL만/종료만 3단 | Slack 스레드: 시작했거나·답했거나·멘션되면 알림, **스레드별 팔로우/언팔로우**, 읽지 않은 스레드가 위로 | FR-8 |
| M3 | **아카이브의 의미가 절반만 정의됐다.** FR-2.4 는 "읽기 전용, 새 트리거 없음"까지 | Slack: "Archived channels are closed to new activity, but the message history is retained", 검색 가능(유료 플랜), **되돌릴 수 있다**("you can unarchive a channel. The channel and its members will be restored"). 삭제는 영구 | FR-2.4·FR-2.6 |
| M4 | **방을 가로지르는 지식 저장소가 없다.** FR-4.4 컨텍스트 재사용은 "완료된 세션 첨부"뿐이고, 방이 늘면 같은 것을 방마다 다시 설명해야 한다 | Devin **Knowledge**("a collection of tips, documentation, and instructions that Devin 'knows' across all future sessions"), LangGraph **Store**("Long-term memory is scoped to a namespace and shared across threads"), Claude **Projects** 지식 | FR-4.4 / §10 v2 |

한 가지 더 — **응답성 계약**. Linear 는 "send an activity or update your external URL within 10 seconds to avoid the session being marked as unresponsive" 와 "up to 30 minutes before the session is considered stale. Note that this stale state is recoverable" 를 **사용자에게 보이는 계약**으로 둔다. 우리는 `dispatched` 5분 → `failed(timeout)`, `running` 15초 heartbeat·3분 무응답(FR-7.1) 이 **있다** — 기계 계약은 있고, **「첫 활동까지 얼마나 걸렸나」를 재는 눈금이 없을 뿐이다**. 이건 결함이 아니라 관찰 항목 후보다(§3 G7).

---

## 3. 가져올 것 (7건)

각 항목: **근거(출처) · 우리 어디(PRD 절) · 기대 효과 · 비용 · 권고**.

### G1. 방 요약에 **범위**와 **출처**를 넣는다 — 권고: **지금**

- **근거**: Slack AI — "You can summarize just your unread messages, the last seven days, or a custom date range"; "Clear sources are cited in each summary, allowing you to dive deeper into any highlight." ([Guide to AI features in Slack](https://slack.com/help/articles/25076892548883-Guide-to-AI-features-in-Slack))
- **우리 어디**: **FR-2.5**(방 요약). 부차로 FR-2A.4(일 완료 요약)에도 같은 규칙.
- **기대 효과**: ① 요약이 **재현 가능**해진다(같은 범위 → 같은 입력). ② 요약이 틀렸을 때 사람이 **원 메시지로 내려갈 수 있다**. ③ 우리 FR-4.2 의 "부재와 장애를 구분한다" 철학과 같은 줄에 선다 — 요약도 어디까지 봤는지 말해야 한다.
- **비용**: 낮음. 요약 메시지에 `{from_message_id, to_message_id}` 와 인용 id 목록을 실으면 된다. UI 는 범위 선택 3개(읽지 않음 / 최근 7일 / 직접 고르기).
- **주의**: "읽지 않음" 은 **사람마다 다르다** → 방에 남기는 요약은 「기간」 기준만 허용하고, 「읽지 않음」 기준은 **본인에게만 보이는 미리보기**로 두어야 방 메시지가 사람마다 달라지는 일이 없다.

### G2. **아카이브 = 읽기 전용 + 검색 가능 + 되돌릴 수 있음**을 못 박는다 — 권고: **지금**

- **근거**: Slack — "Archived channels are closed to new activity, but the message history is retained"(검색은 유료 플랜), "If you change your mind, you can unarchive a channel. The channel and its members will be restored", "Deleted channels are permanently removed from a workspace, message history included." ([Archive or delete a channel](https://slack.com/help/articles/213185307-Archive-or-delete-a-channel))
- **우리 어디**: **FR-2.4**(방 상태) · **FR-2.6**(방 삭제).
- **기대 효과**: 「보관」과 「삭제」의 차이가 사용자 머릿속에서 갈라진다. 지금 FR-2.4 는 아카이브 후 **검색에 걸리는지, 되돌릴 수 있는지**를 말하지 않아 R2 화면이 임의로 채울 자리다 — v0.9 의 P7 교훈("PRD가 말하지 않은 것을 파생 문서가 채운다")이 반복될 자리.
- **비용**: 매우 낮음(문장 2개 + 검색 색인에서 archived 를 빼지 않는다는 규칙).

### G3. **크로스 읽기 권한을 「읽는 시점」에 재검사**한다 — 권고: **지금**

- **근거(실패 사례)**: Atlassian Rovo — 자동화 경로에서 "the automation creator/editor connects Rovo using their own identity, the Rovo agent then executes using the permissions of that connected user during rule runs … which seems like it could create indirect permission bypass scenarios" ([Atlassian Community](https://community.atlassian.com/forums/Atlassian-AI-Rovo-discussions/Discussing-governance-for-Rovo-agents-within-Automation-flows/td-p/3239485)), 그리고 "act as the user who triggered the rule" 가 Rovo 검색 층까지 전달되지 않는 보고 ([ROVO-837](https://jira.atlassian.com/browse/ROVO-837)).
- **우리 어디**: **FR-4.5** 권한 규칙 · §9 보안 원칙(본문 1323행).
- **왜 우리 문제인가**: 우리 규칙은 "그 task 를 일으킨 사람 originator 가 그 방의 참여자다"인데, **task 는 오래 산다**(HITL 대기 최대 24시간, 재시도, `rate_limited` 로 `not_before` 까지 대기). 그 사이 originator 가 **대상 방에서 퇴장**하거나 워크스페이스에서 빠질 수 있다. dispatch 시점에 한 번만 검사하면 **퇴장한 사람의 권한으로 읽는** 창이 열린다. 우리 FR-2.2 가 "퇴장한 에이전트의 새 트리거만 막는다"고 이미 시간축을 의식하고 있으므로, 같은 의식을 originator 에도 적용하는 것.
- **기대 효과**: 유출 경로 하나를 닫는다. 감사 때 "언제 기준 권한인가"에 답할 수 있다.
- **비용**: 낮음. `colab room read` 핸들러에서 originator 멤버십을 **호출 시점에** 조회하면 된다(이미 조회해야 하는 값이라 쿼리 추가 없음). e2e 1건(퇴장 뒤 읽기 → 403).

### G4. **위임(delegate) ≠ 배정(assignee)** 을 용어로 못 박는다 — 권고: **지금**(문장만)

- **근거**: Linear — "Assigning an issue to your app now sets it as the `delegate`, not the `assignee` — so humans maintain ownership while agents act on their behalf." ([Linear Developers — Agents](https://linear.app/developers/agents))
- **우리 어디**: **§3 용어표**(Director) · **FR-2A.1**.
- **기대 효과**: 우리 데이터 모델(`work.director_user_id` + `work.assignee_agent_id`)에 이미 있는 구분에 **이유**가 붙는다. 화면(R2)이 "이 일의 담당자"를 에이전트로 표시하고 사람을 지워 버리는 사고를 막는다 — 종료 조건의 승인자는 언제나 사람이기 때문이다(FR-5.4 "승인은 어떤 경우에도 자동 진행하지 않는다").
- **비용**: 0(문장).

### G5. **방 단위 기본 설정 + 메시지 인라인 오버라이드** UX — 권고: **v2.1(R2 화면)**, 인라인 오버라이드는 **안 함**

- **근거**: Cursor Slack — 채널에서 `@Cursor settings` 로 "default settings at the channel level" 을 두고 그것이 "override your personal defaults for that channel"; 메시지 안에서는 "in acme/backend", `branch=dev` 로 그때그때 바꾼다. 저장소 추론은 "recent agent activity" + "Default repository — Fallback when no match is found". ([Cursor Docs — Slack](https://cursor.com/docs/integrations/slack))
- **우리 어디**: **FR-2.3**(방 설정) · §6 UX.
- **기대 효과**: 마법사를 없앤 대가를 메운다. v0.19 는 7단계를 지웠지만 **"그럼 격리·런타임은 언제 정하나"**의 답이 "방 설정에서"뿐인데, Cursor 는 같은 문제를 **채널 안에서 명령 한 줄**로 푼다 — 방을 떠나지 않아도 된다.
- **비용**: 중간. 방 설정 화면(R2)은 이미 계획에 있으므로 추가분은 "방 안에서 설정을 여는 진입점" 정도.
- **인라인 오버라이드(`branch=dev`)는 권고하지 않는다**: 우리 멘션 문법은 이미 링크 형식으로 고정되어 있고(FR-3.2), 본문 파싱을 늘리면 FR-3.3 라우팅 규칙과 충돌할 표면이 생긴다. **필요하면 `colab` CLI 로 에이전트가 하게 두는 편**이 우리 구조와 맞다.

### G6. **lane 스레드 단위 구독·읽지 않음** — 권고: **v2.1(R2·FR-8)**

- **근거**: Slack 스레드 — "you'll be notified of new replies if you started the thread, replied to it, or were mentioned", 스레드별 "Get notified about new replies / Turn off notifications for replies", "Threads with unread replies will appear at the top of the list". ([Use threads to organize discussions](https://slack.com/help/articles/115000769927-Use-threads-to-organize-discussions), [Guide to Slack notifications](https://slack.com/help/articles/360025446073-Guide-to-Slack-notifications))
- **우리 어디**: **FR-8**(인박스·알림) · §10 v2.0 "v2 에서 같이 볼 것"(이미 "방 단위 알림 설정"이 적혀 있다 — **lane 스레드까지 내려야 한다**).
- **왜 필요한가**: 세션 모델에서는 "세션 = 일 하나"라 구독 3단으로 충분했다. **방은 끝나지 않으므로** 한 방에서 일 3개가 동시에 돌면 방 구독 "전부"는 사실상 쓸 수 없고 "HITL만"은 너무 성기다. 중간 눈금이 **일(work)** 과 **lane 스레드**다.
- **기대 효과**: 방이 커져도 Director 가 인박스를 계속 쓴다. 우리 FR-6.2.1 의 `blocked` 질문 카드가 정확히 "그 스레드를 따라가는 사람"에게 간다.
- **비용**: 중간(구독 테이블 + 읽지 않음 커서). **`inbox_item` 에 `work_id`·`lane_id` 를 먼저 넣어 두면**(스키마만) 나중에 마이그레이션 없이 붙는다 — M10(`message.state`)에서 쓴 것과 같은 수법.

### G7. **「첫 활동까지의 시간」과 「무활동 stale」을 관찰 표에 넣는다** — 권고: **지금**(관찰만, 목표치 없음)

- **근거**: Linear — "If you receive a `created` event, you are expected to send an activity or update your external URL within 10 seconds to avoid the session being marked as unresponsive"; "for up to 30 minutes before the session is considered stale. Note that this stale state is recoverable by sending another agent activity." ([Agent Interaction](https://linear.app/developers/agent-interaction), [Best Practices](https://linear.app/developers/agent-best-practices))
- **우리 어디**: **§11 관찰 표**(v0.17 로 이미 5행이 있고 목표치를 걸지 않는 자리) · FR-7.2.
- **왜 지표인가**: 우리에겐 이미 기계 계약이 있다 — `dispatched` 5분 초과 → `failed(timeout)`, `running` 15초 heartbeat·3분 무응답(FR-7.1). 없는 것은 **사람이 체감하는 눈금**이다. "멘션했는데 아무 반응이 없다"는 체감은 3분이 아니라 **수 초**에서 시작하고, 방 모델에서는 사람이 **대화하듯** 던지므로 더 짧아진다.
- **기대 효과**: G8(15분 5명 실사용) 로그에서 "반응 없음" 불만의 실제 분포를 읽을 수 있다. 규칙(예: 첫 활동 지연 시 "생각 중…" 카드)은 그 뒤에 결정한다 — v0.17 의 C2 처리 방식과 같다.
- **비용**: 낮음. `task.started_at` ↔ 첫 `task_event.seq` 의 시각 차 하나. 관찰 op(`getWorkspaceObservations`)에 행 추가.

### 권고 요약

| # | 항목 | 권고 | PRD 절 | 비용 |
|---|---|---|---|---|
| G1 | 요약의 범위·출처 | **지금** | FR-2.5 | 낮음 |
| G2 | 아카이브 = 읽기 전용+검색+해제 | **지금** | FR-2.4·2.6 | 매우 낮음 |
| G3 | 크로스 읽기 권한 **시점** 재검사 | **지금** | FR-4.5 | 낮음 |
| G4 | delegate ≠ assignee 용어 | **지금** | §3·FR-2A.1 | 0 |
| G5 | 방 안에서 방 설정 열기 | **v2.1(R2)** | FR-2.3 | 중간 |
| G6 | 일·lane 단위 구독/읽지 않음 | **v2.1(R2)**, 스키마 칸은 지금 | FR-8 | 중간 |
| G7 | 첫 활동 지연·stale 관찰 | **지금**(관찰만) | §11 | 낮음 |
| — | 인라인 설정 오버라이드(`branch=dev`) | **안 함** | — | — |
| — | 에이전트가 일을 스스로 열기 | **안 함**(§12.1-2 유지) | FR-2A.1 | — |

---

## 4. 하지 말아야 할 것 (외부의 실패·불만)

### N1. 대화 저장소를 **이관 불가능하게** 만들지 말 것

- **사례**: OpenAI Assistants API 가 2026-08-26 종료되면서 "We will not provide an automated tool for migrating Threads to Conversations" — 객체 대응은 있지만(Assistants→Prompts, Threads→Conversations, Runs→Responses) **저장된 대화는 따라오지 않는다**. ([OpenAI Deprecations](https://developers.openai.com/api/docs/deprecations), [OpenAI 커뮤니티 공지](https://community.openai.com/t/assistants-api-beta-deprecation-august-26-2026-sunset/1354666))
- **우리에게**: v0.19 이관(§10 R4)은 **정확히 이 함정 위에 서 있다.** 지금 계획(세션 1 → 방 1 + 일 1, `session_id` 열 이름 유지, `session*` op 을 별칭으로 한 판 유지, 전환 전 DB 덤프)은 **옳다.** §12.1-3("옛 `session_id` 열 이름을 언제 바꿀지")의 답은 **"이관이 실측으로 검증되고 별칭이 제거된 뒤"** 이며, 두 가지를 한 릴리스에 같이 하지 않는 것이 이 사례의 교훈이다.

### N2. 크로스 읽기를 **「사용자 권한」 한 겹으로만** 막지 말 것

- **사례**: M365 Copilot 의 oversharing — 에이전트는 권한을 어기지 않는데도 문제가 된다. "Content shared with 'Everyone' … becomes part of Copilot's queryable surface for any licensed user—even if that user would never have manually located those files"; "The issue is not that Copilot invents secrets, but that it operationalises permission debt already sitting in Microsoft 365." ([Microsoft Tech Community — Mitigate Oversharing](https://techcommunity.microsoft.com/blog/microsoft365copilotblog/mitigate-oversharing-to-govern-microsoft-365-copilot-and-agents/4448744), [배경 정리](https://nhimg.org/community/cybersecurity-beyond-identity/microsoft-365-copilot-oversharing-what-iam-and-data-teams-must-fix/))
- **우리에게**: FR-4.5 의 **2중 조건**(originator 참여 AND 에이전트 참여/참고 방 링크) + **목록에서도 숨김** + **양쪽 방 기록**은 이 실패에 대한 정확한 방어다. **약화하자는 요청이 나오면 이 사례를 근거로 거절할 것.** 특히 "originator 가 워크스페이스 admin 이면 전부 읽게 하자" 같은 편의 요청이 가장 위험하다 — 그것이 Copilot 의 "licensed user" 조건과 같은 모양이다.

### N3. 자동화·체인에서 **권한 주체가 바뀌게** 두지 말 것

- **사례**: Rovo 자동화 — 규칙이 "act as the user who triggered the rule" 로 설정돼도 Rovo 검색 층까지 전달되지 않고, 연결한 사용자 권한으로 실행된 결과가 코멘트·필드·이메일로 흘러나간다는 보고. ([ROVO-837](https://jira.atlassian.com/browse/ROVO-837), [커뮤니티 논의](https://community.atlassian.com/forums/Atlassian-AI-Rovo-discussions/Discussing-governance-for-Rovo-agents-within-Automation-flows/td-p/3239485))
- **우리에게**: 우리 규칙은 본문 505행에 이미 있다 — "권한 판정은 언제나 **체인 최상단의 사람 originator** 기준이다. 에이전트가 다른 에이전트를 호출해도 권한이 상승하지 않는다." **지켜야 할 것은 구현 쪽**이다: 위임으로 생긴 자식 task 의 `originator_user_id` 가 **부모의 값을 그대로 승계**하는지, 재시도·재지시(`restarted_from_task_id`)·HITL 재개에서도 승계되는지. Rovo 의 결함은 정책이 아니라 **한 층에서만 전달된** 문제였다. → §5 에 e2e 문장을 제안한다.

### N4. **비활동 시간으로 스레드를 자동으로 닫지** 말 것

- **사례**: Discord 스레드는 1시간/24시간/3일/7일 비활동으로 자동 아카이브된다. ([Discord 권한·스레드 문서](https://docs.discord.com/developers/topics/permissions), [스레드 아카이브 설정 정리](https://peakbot.pro/blog/how-to-auto-create-thread-for-every-message-discord))
- **우리에게**: 우리 lane 은 **HITL 대기 24시간이 정상 상태**다(FR-5.4 기본 기한 24h, "동시성 슬롯을 점유하지 않는다"). 시간 기반 자동 종료를 도입하면 **가장 중요한 대기를 가장 먼저 죽인다.** 지금처럼 **사람의 응답 기한(`due_at`·`overdue`)** 과 **런타임 무응답(3분 heartbeat)** 을 분리해 두는 것이 맞다. Linear 의 stale 도 **회수 가능**("recoverable by sending another agent activity")하다는 점이 같은 판단이다.

### N5. 봇에게 **「멘션 없이 모든 메시지」를 기본으로** 주지 말 것

- **사례**: Teams RSC — "Conversation owners can consent for an agent to receive all messages in channels and chats without @mentions." ([Get all channel and chat messages](https://learn.microsoft.com/en-us/microsoftteams/platform/bots/how-to/conversations/channel-messages-for-bots-and-agents), [RSC 개요](https://learn.microsoft.com/en-us/microsoftteams/platform/graph-api/rsc/resource-specific-consent)). 반대로 Slack 은 **스코프가 있어도 채널에 초대되어야** 읽는다("The bot must be invited to any channel it should read"), Discord 는 **메시지 본문 자체가 특권 인텐트**로 심사 대상이다. ([Slack scopes](https://docs.slack.dev/reference/scopes/), [Discord — Message Content is Now a Privileged Intent](https://github.com/discord/discord-api-docs/discussions/5412))
- **우리에게**: 우리는 **멘션이 트리거의 유일한 결정적 수단**(§3)이라는 좋은 기본값을 갖고 있다. 그러나 FR-3.3 **규칙 6·7**(멘션 없는 사람 메시지를 assignee 에게, 지연 폴백)은 "멘션 없이도 도는" 경로이고, §12 의 v0.17 행이 이미 **구조적 집중** 위험으로 적어 두었다. **방 모델에서 이 위험은 커진다** — 방은 잡담이 섞이는 공간이라 "멘션 없는 사람 메시지"의 절대량이 세션 때보다 훨씬 많다. 규칙을 바꾸지 말고(우리 판단 유지), **§11 관찰의 규칙 6·7 폴백 비율을 방 단위로도 재라.**

### N6. **작업이 시작된 뒤 요구사항을 계속 덧붙이는 패턴**을 제품이 권장하지 말 것

- **사례**: Cognition 의 Devin 2025 리뷰 — "Devin handles clear upfront scoping well, but not mid-task requirement changes. **It usually performs worse when you keep telling it more after it starts.**" 반대로 잘 되는 것은 "tasks with clear, upfront requirements and verifiable outcomes". ([Devin's 2025 Performance Review](https://cognition.com/blog/devin-annual-performance-review-2025))
- **우리에게**: 방 모델이 **정확히 그 패턴을 유도한다** — 대화하듯 계속 던지게 만드는 것이 v0.19 의 목적이기 때문이다. 방어는 이미 둘 있다: ① FR-3.4 의 기본값 `queue`(어떤 메시지도 진행 중인 턴을 죽이지 않는다), ② §12 의 "그 자리에서 '일로 만들기'를 권하는 안내". **②를 더 강하게 쓸 근거가 이 출처다** — 종료 조건 없이 대화로만 굴리면 에이전트 성능이 실제로 떨어진다. 다만 **자동으로 일을 열지는 말 것**(§12.1-2, D4).

### N7. **스레드 맥락을 읽는 범위를 무제한으로** 열지 말 것

- **사례**: Cursor 는 "Cloud Agents read the entire thread for context when invoked" 로 **스레드 전체**를 읽는다 — 편리하지만 긴 스레드에서는 비용·혼선이 된다. ([Cursor Docs — Slack](https://cursor.com/docs/integrations/slack))
- **우리에게**: 우리 FR-4.1 은 **"최근 N 개 + 관련 스레드"**, 나머지는 CLI 조회다. **이 선택을 유지할 것.** FR-4.5 의 `max_rooms_per_turn`(기본 3)·`max_tokens`(기본 4,000) 상한도 같은 이유로 유지. 「방이 오래 살면서 맥락이 부풀고 비싸진다」(§12)가 이미 같은 판단을 적어 두었다.

---

## 5. PRD 에 넣을 문장 제안 (그대로 붙여 넣을 수 있게)

> 아래 문장은 **제안일 뿐이며 이 워커는 문서를 수정하지 않았다.** 각 블록 머리에 넣을 자리를 적었다. 기존 문장은 바꾸지 않고 **덧붙이는** 형태로 썼다(v0.17 의 `[v0.17]` 꼬리표 관례와 같게 `[V19-C]` 로 표시).

### 5.1 FR-2.4 (방 상태) — 뒤에 덧붙임

```markdown
`[V19-C]` **보관의 뜻.** `archived` 는 **새 활동만 닫고 과거를 남긴다** — 메시지·일·아티팩트·결정 기록은 그대로 보존되고 **검색에도 계속 걸린다**. 보관된 방에서는 새 메시지·새 일·새 트리거가 모두 거부되며, 진행 중인 task 가 있으면 보관을 거부한다(먼저 끝내거나 취소). **보관은 되돌릴 수 있다** — 해제하면 방과 참여자가 그대로 복원된다. 삭제(FR-2.6)만 영구적이다. 이 구분이 없으면 사람이 "지우기 아까운 방"을 삭제한다.
```

### 5.2 FR-2.5 (방 요약) — 문단 교체 제안

```markdown
**FR-2.5 방 요약(선택)** — 방이 길어지면 사람이 「여기까지 정리」를 눌러 그 시점까지의 요약을 방에 남긴다. 자동 요약은 **일**이 끝날 때만 한다(FR-2A.4) — 방은 끝이 없어 자동 요약의 기준 시점이 없다.

`[V19-C]` **범위와 출처.** 요약은 **범위를 사람이 고르고, 그 범위를 요약 메시지에 기록한다.** 범위는 「최근 7일」·「직접 고른 기간」 둘 중 하나이며, 요약 메시지는 `{from_message_id, to_message_id}` 와 **인용한 메시지 id 목록**을 함께 싣는다. 요약이 틀렸을 때 원 메시지로 내려갈 수 있어야 하고, 같은 범위로 다시 만들면 같은 입력을 봐야 한다.
- 「내가 읽지 않은 것만」 기준은 **사람마다 다르므로 방에 남기지 않는다** — 본인에게만 보이는 미리보기로만 제공한다. 방 메시지가 보는 사람에 따라 달라지면 그것은 더 이상 방 맥락이 아니다.
```

### 5.3 FR-4.5 (다른 방 읽기) — 권한 규칙 뒤에 덧붙임

```markdown
`[V19-C]` **권한은 읽는 시점에 다시 검사한다.** 1·2 조건은 task 를 dispatch 할 때가 아니라 **`colab room read`·`room list` 를 호출한 그 순간**에 판정한다. task 는 오래 산다 — HITL 대기(기본 24시간), 재시도, `rate_limited` 의 `not_before` 대기 동안 originator 가 그 방에서 퇴장하거나 워크스페이스에서 빠질 수 있다. dispatch 시점에 한 번만 검사하면 **이미 나간 사람의 권한으로 읽는 창**이 열린다. 판정이 뒤집히면 `403` 이고, 그 사실도 활동에 남긴다.
```

### 5.4 §3 용어표 — `Director` 행 뒤에 덧붙이거나 `Work` 행 보강

```markdown
`[V19-C]` **일의 소유자는 사람이고, 실행자는 에이전트다.** `work.director_user_id`(사람)와 `work.assignee_agent_id`(에이전트)는 같은 자리가 아니다 — 에이전트에게 일을 맡기는 것은 **위임**이지 소유권 이전이 아니다. 종료 조건의 승인자는 언제나 사람이며(FR-5.4 "승인은 어떤 경우에도 자동 진행하지 않는다"), 화면에서 "이 일의 담당" 을 표시할 때 **둘 다** 보여야 한다.
```

### 5.5 §11 관찰 표 — 행 2개 추가

```markdown
| `[V19-C]` 첫 활동 지연 | 멘션·트리거부터 그 task 의 **첫 `task_event`** 까지의 시간 분포(p50·p90) | 목표치 없음 — "멘션했는데 반응이 없다" 는 체감이 몇 초에서 시작하는지 G8 로그로 먼저 읽는다. 기계 계약(`dispatched` 5분, heartbeat 3분, FR-7.1)은 그대로 둔다 |
| `[V19-C]` 무활동 회수 | `running` 인데 일정 시간 새 `task_event` 가 없다가 **다시 활동이 온** 비율 | 목표치 없음 — 무응답으로 죽일지, 회수 가능한 상태로 둘지를 분포로 먼저 본다 |
```

### 5.6 §12 위험 표 — 행 2개 추가

```markdown
| `[V19-C]` **방 모델이 "시작한 뒤 계속 덧붙이는" 사용을 유도해 결과 품질이 떨어진다** | 외부 실측(Cognition, Devin 2025 리뷰)은 "mid-task requirement changes … performs worse when you keep telling it more after it starts" 를 보고한다. 우리는 규칙을 바꾸지 않는다 — FR-3.4 의 `queue` 기본값(어떤 메시지도 진행 중인 턴을 죽이지 않는다)을 유지하고, **종료 조건을 걸 만한 대화에는 그 자리에서 「일로 만들기」를 권한다**(FR-2A.1). 에이전트가 일을 자동으로 열지는 않는다 |
| `[V19-C]` **위임 체인에서 권한 주체가 바뀐다** | 규칙은 이미 "체인 최상단의 사람 originator"(§9)다. 구현이 이를 **모든 층에서** 지키는지 e2e 로 고정한다 — 위임 자식 task, 재시도(`attempt`), 재지시(`restarted_from_task_id`), HITL 재개가 전부 부모의 `originator_user_id` 를 승계하고, 승계된 값으로 FR-4.5 읽기가 판정되는지. 외부 사례(Rovo 자동화)에서 실패한 지점이 "정책은 맞는데 한 층에서만 전달된" 것이었다 |
```

### 5.7 §10 v2.0 표 — R2 행의 「끝났다고 보는 기준」 보강 제안

```markdown
`[V19-C]` R2 에 **알림 단위**를 포함한다: 방 구독 3단(전부/HITL만/종료만)은 방이 끝나지 않는 구조에서 너무 성기다. **일(work)** 과 **lane 스레드**를 구독·읽지 않음의 단위로 내린다(FR-8). 스키마는 지금 넣어 둔다 — `inbox_item` 에 `work_id`·`lane_id` 를 추가하면 나중에 마이그레이션 없이 붙는다(M10 에서 `message.state` 에 쓴 것과 같은 수법).
```

---

## 6. 출처

**Linear**
- [Linear Developers — Getting Started (Agents)](https://linear.app/developers/agents)
- [Linear Developers — Developing the Agent Interaction](https://linear.app/developers/agent-interaction)
- [Linear Developers — Interaction Best Practices](https://linear.app/developers/agent-best-practices)
- [Linear Changelog — Linear for Agents (2025-05-20)](https://linear.app/changelog/2025-05-20-linear-for-agents)

**Slack**
- [Slack Developer Docs — Developing an agent](https://docs.slack.dev/ai/developing-agents/)
- [Slack Developer Docs — Scopes](https://docs.slack.dev/reference/scopes/) · [`channels:history`](https://api.slack.com/scopes/channels:history)
- [Slack Help — Use Agentforce in Slack](https://slack.com/help/articles/36218786859667-Use-Agentforce-in-Slack)
- [Slack Help — Guide to AI features in Slack](https://slack.com/help/articles/25076892548883-Guide-to-AI-features-in-Slack)
- [Slack Help — Use threads to organize discussions](https://slack.com/help/articles/115000769927-Use-threads-to-organize-discussions)
- [Slack Help — Guide to Slack notifications](https://slack.com/help/articles/360025446073-Guide-to-Slack-notifications)
- [Slack Help — Archive or delete a channel](https://slack.com/help/articles/213185307-Archive-or-delete-a-channel)

**Discord**
- [Discord Developer Docs — Permissions](https://docs.discord.com/developers/topics/permissions)
- [Discord API Docs Discussion — Message Content is Now a Privileged Intent](https://github.com/discord/discord-api-docs/discussions/5412)
- [스레드 자동 아카이브 설정 정리(2차 출처)](https://peakbot.pro/blog/how-to-auto-create-thread-for-every-message-discord)

**Microsoft Teams / M365 Copilot**
- [Microsoft Learn — Get all channel and chat messages (bots and agents)](https://learn.microsoft.com/en-us/microsoftteams/platform/bots/how-to/conversations/channel-messages-for-bots-and-agents)
- [Microsoft Learn — Resource-specific consent for apps](https://learn.microsoft.com/en-us/microsoftteams/platform/graph-api/rsc/resource-specific-consent)
- [Microsoft Tech Community — Mitigate Oversharing to Govern Microsoft 365 Copilot and Agents](https://techcommunity.microsoft.com/blog/microsoft365copilotblog/mitigate-oversharing-to-govern-microsoft-365-copilot-and-agents/4448744)
- [M365 Copilot oversharing — what IAM and data teams must fix (2차 출처)](https://nhimg.org/community/cybersecurity-beyond-identity/microsoft-365-copilot-oversharing-what-iam-and-data-teams-must-fix/)

**Atlassian Rovo**
- [Atlassian Support — Rovo agent permissions and governance](https://support.atlassian.com/rovo/docs/rovo-agent-permissions-and-governance/)
- [ROVO-837 — Rovo agent invoked via Jira Automation](https://jira.atlassian.com/browse/ROVO-837)
- [Atlassian Community — Discussing governance for Rovo agents within Automation flows](https://community.atlassian.com/forums/Atlassian-AI-Rovo-discussions/Discussing-governance-for-Rovo-agents-within-Automation-flows/td-p/3239485)

**OpenAI**
- [OpenAI — Deprecations](https://developers.openai.com/api/docs/deprecations)
- [OpenAI Developer Community — Assistants API beta deprecation, Aug 26 2026 sunset](https://community.openai.com/t/assistants-api-beta-deprecation-august-26-2026-sunset/1354666)

**Anthropic**
- [Claude Help Center — What are projects?](https://support.claude.com/en/articles/9517075-what-are-projects)
- [Anthropic — Projects (공지)](https://anthropic.com/news/projects)

**Cursor**
- [Cursor Docs — Slack](https://cursor.com/docs/integrations/slack)
- [Cursor Docs — Cloud Agents](https://cursor.com/docs/cloud-agent)
- [Cursor Changelog 1.1 — Background Agents in Slack](https://cursor.com/changelog/1-1)

**Cognition / Devin**
- [Cognition — Devin's 2025 Performance Review](https://cognition.com/blog/devin-annual-performance-review-2025)
- [Cognition — Devin can now Manage Devins](https://cognition.ai/blog/devin-can-now-manage-devins)
- [Devin Docs — Release Notes 2025](https://docs.devin.ai/release-notes/2025)

**멀티에이전트 프레임워크**
- [LangChain Docs — LangGraph Persistence (threads, checkpointers, Store)](https://docs.langchain.com/oss/python/langgraph/persistence)
- [AutoGen — Swarm](https://microsoft.github.io/autogen/stable//user-guide/agentchat-user-guide/swarm.html)
- [AutoGen — Selector Group Chat](https://microsoft.github.io/autogen/dev//user-guide/agentchat-user-guide/selector-group-chat.html)

---

## 7. 미확인으로 남긴 것

| 항목 | 왜 미확인인가 |
|---|---|
| **CAMEL** 의 멀티에이전트 대화 상태 공유 | 이번 조사에서 1차 문서를 확보하지 못했다. AutoGen·LangGraph 로 같은 축(브로드캐스트 공유 vs 스레드 격리+네임스페이스)을 덮었다고 보고 생략했다 |
| Linear 에서 **이슈 간 맥락 읽기**(우리 FR-4.5 대응물)가 있는지 | `promptContext` 가 "parent issues, projects, labels, comment threads" 를 담는다는 것까지만 확인. 임의의 다른 이슈를 에이전트가 요청해 읽는 경로는 확인하지 못했다 |
| Slack 봇이 **초대되지 않은 채널**을 읽을 수 있는 예외(Enterprise Grid·Discovery API 등) | Agentforce 설명의 "public conversational data" 문구와 "초대된 채널만" 규칙이 어디서 갈리는지 1차 문서로 확정하지 못했다. FR-4.5 대조에는 영향 없음(우리는 더 엄격) |
| Slack 아카이브 채널 검색의 **플랜별 차이** 세부 | 「유료 플랜에서 검색 가능」까지만 확인 |
| Discord 스레드 자동 아카이브의 **현행 최신 옵션** | 1시간/24시간/3일/7일은 2차 출처 기준. 공식 문서에서 현재 값을 재확인하지 못했다 |
| Teams **선언형 에이전트(declarative agent)** 가 채널 단위로 범위를 갖는지 | RSC 가 팀·채팅 인스턴스 단위라는 것까지 확인. 「채널 하나」로 더 좁히는 경로는 확인하지 못했다 |
