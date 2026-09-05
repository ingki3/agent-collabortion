# `colab` CLI / MCP — 에이전트 → 플랫폼

| 항목 | 내용 |
|---|---|
| 버전 | v0.1 (G2 후보) |
| 소유 | C + D. 변경은 Director 승인 PR로만 |
| 근거 | PRD FR-7.4(툴 표면), FR-3.3(라우팅), FR-5.1·5.4(HITL), FR-6.2·6.2.1(lane·blocked), FR-6.5(합류), FR-2.2(종료 조건), FR-1.5(동적 생성 금지), FR-9.1(토큰 폐기), EVAL §E1·E3·E6·E7·E15 |
| 원칙 | 에이전트가 플랫폼에 되돌아오는 **유일한 경로**. 어떤 런타임이든 셸이 있으면 같다. MCP 서버는 같은 명령을 같은 이름의 툴로 노출한다 |

## 1. 인증·환경

| 변수 | 출처 | 의미 |
|---|---|---|
| `COLAB_TASK_TOKEN` | 데몬(`daemon-protocol.md` §4.1 TaskBundle) | `ctk_` + base64url(32B). **attempt 전용.** 서버는 해시만 저장. 범위: `{task_id, attempt, lane_id, session_id, agent_id}` |
| `COLAB_SERVER_URL` | 데몬 | 서버 |
| `COLAB_TASK_ID` `COLAB_LANE_ID` `COLAB_SESSION_ID` `COLAB_AGENT_NAME` | 데몬 | 명령이 인자를 생략할 때의 기본값 |

- 토큰이 폐기됐으면(재큐잉·취소·완료·`waiting_human`) 모든 명령이 **`401 token_revoked`** — 고아 프로세스의 마지막 방어선(FR-9.1, E11-04).
- 테스트 채팅(FR-1.8.1)에는 토큰이 없다 → 모든 명령이 `no token` 오류(E15-04).
- 토큰은 원본 사람 originator의 권한을 넘지 못한다(FR-1.9). 세션 밖 자원은 전부 403.

## 2. 명령

전부 `--json` 출력(에이전트가 파싱). 종료 코드: 0 성공, 2 인자 오류, 3 거부(권한·상태·정책), 4 토큰 없음/폐기, 5 서버 도달 불가.

### 2.1 읽기

| 명령 | HTTP (`openapi.yaml` `x-colab-cli`) | 반환 | 단계 |
|---|---|---|---|
| `colab session get [--session S]` | `GET /v1/sessions/{S}` | goal, acceptance_criteria, completion_condition 진행, 참여자 로스터(이름·역할 설명·**파생 상태**), 격리, Director 이름 | P1 |
| `colab session messages [--since <id>] [--limit N] [--thread <root_id>]` | `GET /v1/sessions/{S}/messages` | 메시지 목록(작성자·본문·스레드·시각). 히스토리가 프롬프트에서 잘렸을 때 더 읽는 용도(§8.4 `truncated`) | P1 |
| `colab artifact get <id> [--out <path>]` | `GET /v1/artifacts/{id}` | 메타 + 본문 다운로드. **크로스 lane 읽기는 이것뿐**(FR-6.1) | P2 |

### 2.2 쓰기 — 메시지·상태

| 명령 | HTTP | 동작 | 단계 |
|---|---|---|---|
| `colab message post --body <text> [--reply-to <msg_id>] [--mention @A,@B]` | `POST /v1/sessions/{S}/messages` (`Idempotency-Key: <task_id>:<attempt>:<client_seq>`) | 메시지 게시. 라우팅은 서버(FR-3.3): 에이전트 메시지는 **멘션 있을 때만** 트리거(규칙 4), 위임자 멘션은 합류 전까지 억제(규칙 8). 응답에 `triggered: [agent…]`와 `suppressed: [agent…]`를 돌려준다 | P1 |
| `colab status set working\|done [--note <text>]` | `POST /v1/tasks/{T}/status` | `done`은 "이 턴의 작업이 끝났다"의 선언 — lane 종료 판정은 서버가 `turn_end`와 함께 한다. `working`은 no-op에 가깝고 피드 기록용 | P2 |
| `colab status set blocked --note "<질문>"` | `POST /v1/tasks/{T}/status` | **FR-6.2.1 경로.** 서버가 (1) lane `blocked` (2) 스레드에 질문 카드 게시(`lane.blocked_message_id`) (3) 위임자 즉시 깨움(없으면 Director 인박스 `lane_blocked`). 반환: `{"turn_end_required": true}` — 에이전트는 **턴을 끝내야 한다** | P2 |
| `colab decision record --title <t> --body <text> [--options a,b] [--chosen a]` | `POST /v1/sessions/{S}/decisions` | 결정 기록(source=agent). 브리프 [7]에 실린다 | P2 |

### 2.3 위임·아티팩트·리뷰

| 명령 | HTTP | 동작 | 단계 |
|---|---|---|---|
| `colab lane delegate --agent <name> --brief <text> [--depends-on <lane_id>] [--profile <name>]` | `POST /v1/sessions/{S}/lanes` | **항상 새 lane**(해소 규칙 2). `delegated_from_task_id` = 현재 task → 합류 그룹. 대상은 세션 참여자만(FR-1.5) — 아니면 `3 not_participant` + "hitl ask로 Director에게 참여자 추가를 요청하라". `--depends-on`은 v1에서 저장만(DAG 실행은 v1.1) | P2 |
| `colab artifact submit --type <report\|diff\|file\|link> --path <p> \| --url <u> [--title <t>]` | `POST /v1/sessions/{S}/artifacts` | 아티팩트 업로드. `diff`는 `git diff` 텍스트(P4). 종료 조건 `artifact_submitted(agent)` 판정 입력(FR-2.2) | P2 (diff: P4) |
| `colab review approve [--artifact <id>] [--note <t>]` / `colab review reject --reason <text> [--artifact <id>]` | `POST /v1/sessions/{S}/reviews` | 종료 조건 `agent_approval(agent)` 판정 입력. 지정 에이전트가 아니면 `3 not_reviewer`(E6-06). `reject`는 결정 기록 + 아티팩트 스레드에 사유 게시 | P2 |

### 2.4 HITL (FR-5.1) — **호출 후 턴을 끝내라**

| 명령 | HTTP | 동작 | 단계 |
|---|---|---|---|
| `colab hitl ask --question <q> --default <proposed> [--context <text>]` | `POST /v1/tasks/{T}/hitl` `{type: question}` | `--default` **필수**(없으면 `2`). task에 `pending_hitl` 세움. 반환 `{"hitl_id", "turn_end_required": true}` | P3 |
| `colab hitl approve-request --summary <s> [--artifact <id>]` | 〃 `{type: approval}` | default 없음. **절대 자동 진행되지 않는다**(FR-5.4) | P3 |
| `colab hitl request-info --what <w> --why <y>` | 〃 `{type: info}` | v1.1(타입만 예약). v1은 `3 unsupported_type` | v1.1 |
| (choice) | 〃 `{type: choice}` | v1.1 | v1.1 |

- **task당 열린 HITL은 하나**: 두 번째 호출은 `3 hitl_already_open`(E7-04).
- "턴을 끝내라"는 반환 필드 `turn_end_required: true`로 표현한다. 브리프 [2]가 같은 말을 한다. 에이전트가 무시하고 계속하면 그동안의 게시·편집은 그대로 기록되고 `turn_end`에 `waiting_human`으로 전이한다(FR-7.1, E7-02).
- Director가 답해야 할 질문은 `hitl ask`, 위임자가 답할 질문은 `status set blocked`. 브리프에 구분을 적는다(E7-19).

## 3. MCP 서버

`colab mcp serve`(stdio). 툴 이름은 명령 경로를 밑줄로: `colab_session_get`, `colab_session_messages`, `colab_message_post`, `colab_status_set`, `colab_decision_record`, `colab_lane_delegate`, `colab_artifact_submit`, `colab_artifact_get`, `colab_review_approve`, `colab_review_reject`, `colab_hitl_ask`, `colab_hitl_approve_request`. 인자·반환은 CLI와 동일한 JSON. 데몬이 `session/new.mcpServers`에 이 서버 하나만 넣는다(`harness.md` §3, `strictMcpConfig`).

## 4. 서버 기록

서버는 모든 CLI 호출을 `task_event {class: status, verb: <명령>, outcome: ok|failed, payload.status}`로 기록한다 — 활동 피드의 "플랫폼 조작" 렌더 클래스. 데몬은 이 이벤트를 만들지 않는다.

## 5. 계약 테스트 (P1~P3 C 작업)

| 테스트 | EVAL |
|---|---|
| 폐기 토큰 → 401, 저장 0 | E11-04 |
| 토큰 없음(테스트 채팅) → 4 | E15-04 |
| `message post` 멱등키 재전송 → 중복 0 | E8-04 |
| 비참여자 `lane delegate` → 3 + 안내 | E15-02 |
| `hitl ask` default 없음 → 2 / 두 번째 → 3 | E7-05, E7-04 |
| `status set blocked` → 질문 카드 + 위임자 즉시 / 위임자 없음 → 인박스 | E3-05, E3-08 |
| `review approve` 비지정 → 3 | E6-06 |
