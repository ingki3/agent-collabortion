# openapi.yaml — 설계 결정 · PRD 대응표 · 미결

| 항목 | 내용 |
|---|---|
| 대상 | `contracts/openapi.yaml` (OpenAPI 3.1, `info.version 0.1.0-draft`) |
| 단계 | PLAN.md §3 P0-b "OpenAPI 초안 — P1~P3에서 쓸 리소스 전부" (S+W) |
| 근거 | `server/migrations/0001_init.sql`(리소스·필드·ENUM SSOT), `PRD.md` v0.12 FR-1~FR-9 · §7 · §8.1 · §9, `SCREEN.md` v0.3 §2 · §4 · §6, `EVAL.md` v0.1 |
| 검증 | `npx -y @redocly/cli lint contracts/openapi.yaml` → **오류 0 · 경고 0**(`recommended`). `--extends recommended-strict`도 0/0 |
| 크기 | operation **94** (태그 15) · 스키마 106 · `x-colab-cli` 표시 operation 13 |

---

## 1. 공통 규약

| 규약 | 결정 | 근거 |
|---|---|---|
| 인증 | `UserSession`(쿠키 `colab_session`, 전역 기본) + `TaskToken`(bearer, `COLAB_TASK_TOKEN`). CLI 전용 operation만 `TaskToken`을 받고 `x-colab-cli`가 붙는다 | FR-7.4 "task 범위·단기 토큰", FR-9.1 "재큐잉 시 폐기 → `401 token_revoked`" |
| 권한 | 각 operation `description` **첫 줄** `권한: …`. 판정은 originator(체인 최상단 사람) 기준 | PRD §9 보안, FR-1.9, FR-5.3 표, SCREEN §2.3 매트릭스 |
| 오류 | 전부 RFC 9457 `application/problem+json`. 확장 `code`(snake_case 식별자) · `errors[]`(필드 검증) · `can_respond_from`(deputy 시점) · `sessions[]`(차단 사유 세션) | SCREEN §7 "권한 없음은 비활성 + 사유" — `detail`이 툴팁 문구 |
| 상태 코드 | `422` 검증 · `409` 전이 불가/유일성/차단 · `403` 권한 · `410` 만료된 토큰류 · `202` 데몬이 마저 처리하는 비동기 동작(취소·workdir 삭제·테스트 채팅 턴) · `504` 동기 데몬 왕복 지연 | |
| 페이지네이션 | 커서(`cursor`·`limit` → `next_cursor`, `Page` 봉투). 메시지 타임라인만 `before`/`after` 양방향(`MessagePage`) — 뒤로 스크롤과 재연결 백필이 둘 다 필요 | SCREEN §6 |
| 멱등키 | `Idempotency-Key`(UUID) **필수**: `postMessage` · `restartLane` · `respondHitlRequest`. 그 밖의 생성 POST는 선택. 재생 응답에 `Idempotent-Replayed: true`. CLI는 `task_id + seq`에서 키를 만든다 | PRD §9 내구성, FR-5.4 "응답 기록은 멱등" |
| 실시간 | **SSE 하나**(`GET /workspaces/{id}/stream`). 이벤트 25종은 `StreamEvent.type`에 열거 | §2 D1 |
| 확장 | `x-prd`(구현하는 PRD 항목) · `x-screen`(부르는 화면) · `x-phase`(처음 필요한 단계) · `x-colab-cli`(CLI 명령) | Lead 지시 + 스트림 병렬 작업 시 우선순위 판단용 |

## 2. 설계 결정

**D1. WS가 아니라 SSE.** 클라이언트→서버 동작이 전부 REST라 양방향 채널이 필요 없다. SSE는 `Last-Event-ID` 재연결·백필이 표준이고(SCREEN §6 "끊긴 채로 낡은 화면을 보여주지 않는다"), 프록시·인증(쿠키)이 HTTP 그대로다. 보존 창(10분)을 넘긴 커서면 첫 프레임 `resync`로 REST 재조회를 시킨다. 고빈도 이벤트(`message.delta` · `agent.typing` · `test_chat.delta`)는 `ephemeral`로 표시하고 백필하지 않는다(PRD §7 "고빈도 이벤트는 영속화하지 않는다").

**D2. CLI는 `GET /cli/context`로 자기 범위를 푼다.** 토큰 하나만 받은 CLI가 `session get`을 하려면 세션 id가 필요하다. 토큰 파싱 규칙을 CLI에 박는 대신 서버가 task·lane·세션·에이전트·참여자 로스터·억제 중인 위임자(규칙 8)·열린 HITL 여부를 돌려준다. 나머지 CLI operation은 웹과 **같은 리소스 경로**를 쓴다 — 별도 `/cli/*` 표면을 두면 계약이 두 벌이 된다.

**D3. 라우팅 진입점은 `postMessage` 하나.** 사람 메시지·에이전트 메시지·답글 모두 이 operation이 FR-3.3 규칙 1~8과 lane 해소 4규칙을 적용한다. 응답 `triggers[]`가 실제 생성/병합된 task를 돌려주고, 세션이 루프 상한으로 `paused`되면 `session_paused`로 알린다(E4-01). 사람의 "새 lane" 수단은 `new_lane` 토글(t-2), 에이전트의 위임은 `delegateLane`(규칙 2 — 항상 새 lane, 참여자 제한 E15-02). `previewTriggers`는 같은 본문으로 게시 없이 결과만 준다(FR-3.6, LLM 호출 없음 §8.1).

**D4. 취소는 lane 리소스의 동작 두 개.** `restartLane`("중단하고 다시 지시")은 메시지를 게시하므로 멱등키 필수이고 **새 task**(`restarted_from_task_id`, E8-06)를 만든다. `cancelLane`("중단")은 `failed(cancelled)`. 둘 다 §8.2.2 절차(30초 보류)를 데몬이 수행하므로 `202`. 권한은 Director·deputy(즉시, t-3). `paused(budget)` task의 명시 종료(E9-03)와 `failed` lane의 "다시 지시"(SCREEN §4.5 실패 분류 표)도 이 둘로 모은다 — 일반 "재시도" 버튼은 두지 않는다.

**D5. HITL 등록은 타입별 `oneOf`, 응답은 스키마 하나.** `HitlCreate`는 `question`·`choice`·`approval`·`info`의 CLI 플래그를 그대로 필드로 옮겼다(`--default` → `proposed_default` 필수, E7-05). `choice`·`info`는 v1.1이지만 SCREEN §2.3 C4("같은 카드에 입력부만 교체")대로 스키마를 지금 둔다. `HitlResponse`는 타입에 맞는 필드만 쓰며 예산 상향은 `budget_override_usd` → `task.budget_override`(C2′, E9-02). **두 번째 응답은 `200 ignored: true`** — 오류가 아니다(FR-5.4, E7-08). deputy 시점 제한은 `403 deputy_not_yet` + `can_respond_from`(E7-09).

**D6. `paused` 해소는 두 경로.** task 단위 `paused(budget)`은 그 task의 시스템 HITL(`task_id` 채움, s-13)에 응답한다. 세션 단위(예산·시간·루프·Director)는 `resumeSession`이며 사유별 본문(`limits` 상향 · `reset_loop_counters`)을 받고, 열려 있던 세션 단위 시스템 HITL(`task_id` 없음)을 함께 닫는다. `runtime_offline`은 `rebindSession` 또는 `cancelSession`만 허용(`409`). `Session.paused_detail`이 SCREEN §4.5 O6의 배너 5종 데이터와 호출자가 지금 할 수 있는 `resolve_actions`를 준다.

**D7. 에이전트 상태는 저장하지 않고 두 곳에서 파생.** `Agent.status`(워크스페이스 전체 task 기준)와 `Participant.status`(그 세션의 런타임·task 기준, `offline`은 세션 런타임에 따라 달라짐). 순서는 FR-1.3(E5-11~18). 칩 둘째 줄은 `status_note`로 분리(N2).

**D8. 런타임 후보는 S6과 S17이 같은 operation.** `listRuntimeCandidates`가 격리 방식으로 후보를 자르고(`worktree`는 remote URL 일치만, E14-04·05) 비후보도 `eligible: false` + 사유로 돌려준다(화면이 비활성+사유). `session_id`를 주면 세션에서 격리·저장소를 읽는다.

**D9. 저장소 검증은 동기 데몬 왕복.** `checkRepo`는 폼이 다음 단계를 막아야 하므로(E13-01) 동기이고, 데몬 지연은 `504`, 오프라인은 `409 runtime_offline`. 결과의 `remote_url`을 서버가 `session.isolation.remote_url`에 보관해 재바인딩 판정 키로 쓴다(스키마 v0 jsonb에 키 추가 — §4 미결).

**D10. 아티팩트는 multipart, 리뷰는 아티팩트의 동작.** `submitArtifact`가 `artifact_submitted` 판정과 `user_approval` 시스템 HITL 발행을 유발하고 응답에 `completion_progress`를 실어 CLI/화면이 즉시 안다(E6-01). `reviewArtifact`는 지정 리뷰어가 아니면 저장하지 않고 `403 not_designated_reviewer`(E6-06); `reject`는 제출 lane 스레드에 답글을 게시해 해소 규칙 1로 재진입시킨다(E16-B 5단계).

**D11. 종료 조건은 재귀 스키마.** `CompletionCondition = oneOf(CompletionGroup{op, conditions[]} | CompletionAtom{type, who?, agent_id?})` — 스키마 v0 기본값 JSON과 정확히 같은 모양. 진행률(`CompletionProgress`)은 조건별 `path`로 트리 위치를 가리키고 `human_gate=false`면 "사람 승인 없이 완료됩니다"(m-m).

**D12. 설정 8탭은 operation 셋.** 멤버 탭 = `members`/`invites`, 알림 탭(개인) = `/me/notification-settings`, 나머지 6탭 = `PATCH /workspaces/{id}/settings` 하나(`task_event_masking`만 owner). `workspace_settings` 컬럼과 1:1.

**D13. 세션 생성은 즉시 `active`.** 마법사 제출 = 생성 + 시작(assignee 초기 task, E16-A 1단계). `draft: true`로 저장만 할 수 있고 `startSession`이 같은 검증을 다시 거친다. 런타임 0개면 `409 no_runtime`(SCREEN §2.1 — PRD보다 엄격한 화면 결정을 API도 따른다).

**D14. `failure_kind`는 마이그레이션 ENUM 그대로.** SCREEN §4.5는 `agent_error`·`budget_exceeded`를 언급하지만 스키마 v0에 없다. `budget_exceeded`는 PRD대로 `paused`이고, `agent_error`는 `auth`·`quota`·`config`(재시도 없음 → 에이전트 `error`)의 화면 묶음으로 본다. API는 ENUM 9값만 낸다.

**D15. uuid를 화면에 노출하지 않기 위한 해소 필드.** `Message.author`, `Lane.agent_name`·`waiting_for`, `InboxItem.card`, `TaskEvent.sentence`(서버가 만든 한 문장 렌더 폴백). FR-7.2 "참조를 해소한다".

**D16. 이 문서에 넣지 않은 것.** 데몬 엔드포인트(claim·heartbeat·event push·토큰 폐기 통보·페어링 토큰 교환) → `daemon-protocol.md`. `task_event.class` 집합 → `task_event.schema.json`(여기서는 `class: string`). `COLAB_TASK_TOKEN` 형식 → `colab-cli.md`. Build with AI(FR-1.2)·`.agent.md` 가져오기/내보내기(FR-1.7)·`container` 격리·`supervised` autonomy·비공개 세션은 v1.1이라 operation 없음(스키마 ENUM에는 값이 있다). `activity_log` 조회 API는 P5 대시보드에서 추가.

## 3. PRD FR → operation 대응표

| PRD | operation | 비고 |
|---|---|---|
| FR-1.1 등록 폼 | `createAgent` · `updateAgent` · `getAgent` · `listAgents` | `AgentCreate.profiles` ≥ 1 |
| FR-1.2 Build with AI | — | v1.1 |
| FR-1.3 상태(파생) | `listAgents`(status) · `listParticipants`(status·status_note) | 저장 안 함 |
| FR-1.4 템플릿 | `listAgentTemplates` · `applyAgentTemplate` | 매핑 결과 `mapped/unmapped` |
| FR-1.5 동적 생성 금지 | `createAgent`(UserSession만) · `delegateLane`(참여자 제한 `422 not_participant`) · `addParticipant` | |
| FR-1.6 프로파일 | `createAgentProfile` · `updateAgentProfile` · `deleteAgentProfile` | 능력 범위 검증 `422` |
| FR-1.7 `.agent.md` | — | v1.1. `definition_source/version`은 `Agent`에 있음 |
| FR-1.8 정의/인스턴스 | `Agent.definition_update_available` · `AgentUpdate.apply_definition_update` | E15-10 |
| FR-1.8.1 테스트 채팅 | `createTestChat` · `getTestChat` · `postTestChatTurn` · `closeTestChat` · SSE `test_chat.*` | 세션 아님, 토큰 없음 |
| FR-1.9 호출 권한 · 킬 스위치 | `Agent.invitable` · `createSession`/`addParticipant` 검사 · `updateAgent(respond_to: nobody)` | E10-07~12 |
| FR-2.1 생성 폼 · 검증 | `createSession` · `checkRepo` · `listRuntimeCandidates` · `updateSession` · `changeDirector` | E15-11 |
| FR-2.2 종료 조건 | `CompletionCondition` · `CompletionProgress` · `submitArtifact` · `reviewArtifact` · `respondHitlRequest(user_approval)` · `completeSession(manual)` | E6-01~09 |
| FR-2.3 상태 머신 | `startSession` · `pauseSession` · `resumeSession` · `completeSession` · `cancelSession` · `Session.paused_detail` | E5-04~08 |
| FR-2.4 요약 | `Message.kind = summary` · SSE `message.created` | 생성은 P4 서버 내부 |
| FR-3.1 · 3.2 메시지 · 멘션 | `listMessages` · `getMessage` · `Message.mentions` · `Participant.mention_link` | |
| FR-3.3 라우팅 8규칙 · lane 해소 | `postMessage`(triggers) · `previewTriggers`(rule · lane.resolution) · `MessageCreate.new_lane` | E1 · E2 |
| FR-3.4 턴 중 메시지 · 취소 | `postMessage`(`coalesced`) · `restartLane` · `cancelLane` · `Lane.actions` | E2-09·10, E10 |
| FR-3.5 루프 상한 | `updateWorkspaceSettings(loop_limits)` · `MessagePostResult.session_paused` · `PausedDetail.loop` · `resumeSession(reset_loop_counters)` | E4 |
| FR-3.6 트리거 미리보기 | `previewTriggers` · `MessageCreate.suppress_agent_ids` | E15-06 |
| FR-4.1 컨텍스트 | `getSession`(context) · `listMessages(thread, limit)` · `listDecisions` | CLI `session get/messages` |
| FR-4.2 결정 기록 | `listDecisions` · `recordDecision` · `respondHitlRequest`(decision_id) | 빈 목록 `200 []` vs 장애 `5xx` |
| FR-4.3 아티팩트 | `submitArtifact` · `listArtifacts` · `getArtifact` · `downloadArtifact` | 버전 자동 증가 |
| FR-4.4 컨텍스트 재사용 | `SessionCreate.context(type: session)` · `context_reuse_override` · `updateWorkspaceSettings(context_reuse)` | E15-09 |
| FR-5.1 HITL 타입 | `createHitlRequest`(`HitlCreate` oneOf) | E7-05·06 |
| FR-5.2 동작 | `createHitlRequest`(`turn_end_required`) · `listHitlRequests` · `getHitlRequest` · `listInbox` | E7-01~04 |
| FR-5.3 사람 역할 | `Session.my_role` · 각 operation 권한 첫 줄 | |
| FR-5.4 응답 · deputy · 기한 | `respondHitlRequest`(멱등 · `ignored` · `403 deputy_not_yet`) · `HitlRequest.overdue/can_respond_from` | E7-07~17 |
| FR-6.1 lane/workdir 분리 | `Lane.workdir_id/workdir_ref` · `listRuntimeWorkdirs` | 경로 비노출 |
| FR-6.2 lane 상태 · DAG | `listLanes` · `getLane` · `delegateLane(depends_on)` · `setTaskStatus` | E3 |
| FR-6.2.1 blocked 경로 | `setTaskStatus(blocked)` → `blocked_q` 카드 · `Lane.blocked_message_id` · 인박스 `lane_blocked` | E3-05~09 |
| FR-6.3 동시성 | `Lane.queue_position` · `updateRuntime(max_concurrent_tasks)` · `runtime_policy` | |
| FR-6.4 격리 · GC | `listRuntimeWorkdirs` · `deleteWorkdir(force)` · `updateWorkspaceSettings(workdir_*)` · SSE `workdir.updated` | E13-09~16 |
| FR-6.5 합류 | `Lane.join_group` · 서버 내부(시스템 메시지) | E3-01~13 |
| FR-7.1 task 상태 머신 | `getTask` · `listLaneTasks`(attempts · resumed) · `listSessionTasks` | E5, E8 |
| FR-7.2 활동 피드 | `listTaskEvents`(superseded · masked · structured) · SSE `task_event.*` | class 집합은 별도 계약 |
| FR-7.3 비용 · 예산 | `getSessionCost` · `getWorkspaceCost` · `respondHitlRequest(budget_override_usd)` · `resumeSession(limits)` · `Task.budget_override` | E9 |
| FR-7.4 colab CLI | `x-colab-cli` 13개 + `getCliContext` | §5 표 |
| FR-8 인박스 | `listInbox`(정렬 · delegated · card · actions) · `getInboxSummary` · `markInboxRead` · `markAllInboxRead` · `setSessionSubscription` · `updateNotificationSettings` | 7타입 ENUM |
| FR-9 런타임 관리 | `listRuntimes` · `getRuntime` · `createPairing` · `getPairing` · `updateRuntime` · `deleteRuntime` | probe 결과 = `capabilities`·`repos` |
| FR-9.1 고아 · 토큰 폐기 | `401 token_revoked` · `getCliContext` | 폐기 통보는 daemon-protocol |
| FR-9.2 오프라인 · 재바인딩 | `Runtime.grace_ends_at` · `listRuntimeCandidates(session_id)` · `rebindSession(acknowledge_loss)` · `deleteRuntime 409` | E14 |

## 4. 화면 → operation

| 화면 | operation |
|---|---|
| S1 · S2 · S3 | `login` · `signup` · `previewInvite` · `acceptInvite` · `getMe` |
| S4 | `createWorkspace` · `createPairing`/`getPairing` · `listAgentTemplates`/`applyAgentTemplate` · `getOnboardingStatus` |
| S5 | `listSessions` · SSE `session.updated`/`cost.updated` |
| S6 | `listMembers` · `listRuntimeCandidates` · `checkRepo` · `listAgents(invitable)` · `createSession` |
| S7 | `getSession` · `listParticipants` · `listLanes` · `listMessages` · `postMessage` · `previewTriggers` · `listTaskEvents` · `listArtifacts` · `listDecisions` · `getSessionCost` · `pause/resume/complete/cancelSession` · `changeDirector` · `add/update/removeParticipant` · `restartLane` · `cancelLane` · `listLaneTasks` · `respondHitlRequest` · `updateSession` · `setSessionSubscription` |
| S7-P · S7-D 변형 | `Lane.actions` · `HitlRequest.can_respond(_from)` · `PausedDetail.resolve_actions` · `Session.my_role` |
| S8 | `listInbox` · `getInboxSummary` · `markInboxRead` · `markAllInboxRead` · 인라인: `respondHitlRequest` · `postMessage(parent_id)` · `resumeSession` · `restartLane` · `rebindSession` |
| S9 · S10 | `listAgents` · `createAgent` · `updateAgent` · `archiveAgent` · `*AgentProfile` · `listRuntimes(capabilities.models)` · 테스트 채팅 4종 |
| S11 · S12 · S13 | `listRuntimes` · `getRuntime` · `updateRuntime` · `deleteRuntime` · `createPairing` · `getPairing` · `listRuntimeWorkdirs` · `deleteWorkdir` |
| S14 | `listMembers` · `updateMemberRole` · `removeMember` · `listInvites` · `createInvite` · `revokeInvite` · `getWorkspaceSettings` · `updateWorkspaceSettings` · `get/updateNotificationSettings` |
| S17 | `listRuntimeCandidates(session_id)` · `rebindSession` · `cancelSession` |

## 5. colab CLI → operation (`x-colab-cli`)

| 명령 | operation | 비고 |
|---|---|---|
| (전처리) | `GET /cli/context` | 토큰 → task·lane·세션·로스터 |
| `session get` | `GET /sessions/{id}` · `GET /sessions/{id}/decisions` | |
| `session messages --thread --tail` | `GET /sessions/{id}/messages?thread=&limit=` | |
| `message post --content-file --parent` | `POST /sessions/{id}/messages` | 멱등키 = `task_id + seq` |
| `lane delegate --agent --brief --depends-on --profile` | `POST /sessions/{id}/lanes` | 참여자만 |
| `hitl ask --default` / `approve-request` / `request-info` | `POST /sessions/{id}/hitl-requests` | `turn_end_required: true` |
| `artifact submit --name --type --file` | `POST /sessions/{id}/artifacts` (multipart) | |
| `artifact get` | `GET /artifacts/{id}` · `…/content` | |
| `decision record --summary --rationale` | `POST /sessions/{id}/decisions` | |
| `status set working\|blocked\|done --note` | `POST /tasks/{id}/status` | blocked·done → `turn_end_required` |
| `review approve\|reject --artifact --comments` | `POST /artifacts/{id}/review` | 지정 리뷰어만 |

## 6. 미결 (Lead · Director 판정 필요)

| # | 항목 | 이 초안의 가정 | 필요한 결정 |
|---|---|---|---|
| Q1 | **스키마 v0에 없는 테이블** | 초안이 전제하는 저장소: 사용자 자격(비밀번호 해시)·사용자 세션, `workspace_invite`, `runtime_pairing`, 세션 구독·알림 설정, `artifact_review`, 멱등키 저장, SSE 백필용 이벤트 로그, 런타임별 상한(`runtime_policy.per_runtime`으로 대체 가능) | 마이그레이션 `0002`로 추가할지, 일부(구독·리뷰)를 jsonb/`decision`으로 흡수할지 |
| Q2 | `session.isolation.remote_url` | `checkRepo` 결과를 jsonb에 키로 추가 보관 | PRD §7 `isolation(jsonb: {kind, repo_path?\|image?})`에 키 추가 승인 |
| Q3 | `budget_policy` jsonb 키 | `default_session_budget_usd` · `default_task_budget_usd` · `workspace_monthly_budget_usd` · `pricing_overrides` | PRD가 키를 정하지 않음 — 확정 필요 |
| Q4 | `SubscriptionLevel` 값 | `all` · `hitl_only` · `completion_only` | FR-8 문구를 ENUM으로 옮긴 것. 명칭 확정 |
| Q5 | 실시간 = SSE | D1 | WS를 원하면 이벤트 종류는 그대로 두고 전송만 바꾼다 |
| Q6 | SSO | `login`은 이메일/비밀번호만 | S1 "또는 SSO" — v1 범위 여부 |
| Q7 | 에이전트 수정·삭제 권한 | 소유자 + owner·admin (SCREEN §2.3 m3) | PRD 미정 Q7 |
| Q8 | `TaskToken`에 `GET /sessions/{id}` 등 읽기 허용 범위 | 그 task의 세션 안에서 세션·메시지·lane·task·아티팩트·결정 읽기 허용, 워크스페이스·에이전트·설정·인박스는 불가 | `colab-cli.md` 토큰 범위와 맞춰야 함 |
| Q9 | `task_event.class` 집합 · `object_ref` 형식 | `string` / 열린 object | `task_event.schema.json` 확정 후 `$ref`로 교체 |
| Q10 | 세션 단위 예산 HITL 응답 경로 | `respondHitlRequest`(HITL 카드)와 `resumeSession`(배너) 둘 다 허용, 서로 닫아줌 | 한 경로로 줄일지 |
| Q11 | `completeSession` 진행 중 lane 처리 | `confirm: true` 없으면 `409 running_lanes` | 확인 다이얼로그 문구와 일치시킬 것 |
| Q12 | 재바인딩 후 `runtime_session_ref` | 전부 비움(콜드 스타트) | `none` 격리에서도 비우는가(런타임이 달라지므로 비운다고 가정) |

## 7. 린트 결과

```
npx -y @redocly/cli lint contracts/openapi.yaml
→ validated · 오류 0 · 경고 0 (recommended)
npx -y @redocly/cli lint contracts/openapi.yaml --extends recommended-strict
→ 오류 0 · 경고 0
```

기록할 경고 없음. 스키마의 `null` 허용은 3.1 방식(`type: [string, 'null']`)으로만 썼고 `nullable`은 쓰지 않았다.
