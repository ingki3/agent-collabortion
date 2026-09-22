# SCR-B · SCREEN v0.19 구현 타당성 검증

| 항목 | 값 |
|---|---|
| 대상 | `origin/dev` `SCREEN.md` v0.19 (PR #270·#271·#272 머지본, 5286621) · `PRD.md` v0.19 |
| 대조한 코드 | `web/` (app 77파일·컴포넌트 60·테스트 62) · `contracts/openapi.yaml` 0.1.6 (op 97개) · `COMPONENTS.md` §8 |
| 작성 | 2026-09-22 · SCR-B (읽기 전용 — 코드·문서 수정 0, 커밋 0) |
| 표기 | 근거가 원문·실측이면 그대로, 내 판단이면 **[추정]** |

> **세 줄 결론.**
> 1. SCREEN 의 신규 9화면은 **기존 op 의 이름만 바꿔서는 하나도 서지 않는다** — 최소 **신규 op 31개 · 기존 op 확장 12개 · SSE 타입 14종 추가**가 R0 계약의 입력이다. 별칭으로 끝나는 것은 `rebindSession`·`changeDirector`·`getSessionCost` 정도뿐이다.
> 2. 화면을 못 세우는 **저장 자리 공백이 둘** 새로 나왔다 — PRD §7 에 **미션 제안(`work_proposal`)** 표와 **다른 방 읽기 기록(`room_read`)** 표가 없고, **활동 로그 조회 op 이 계약에 아예 없다**(S15 는 v1 승격인데 부를 op 이 없다). SCREEN §8.2 의 공백 아홉에 이 셋이 빠져 있다.
> 3. R1.5 문구 라운드는 **한 PR 에 가둘 수 있다**(모노레포라 `server/`·`web/` 이 같은 커밋에 든다) — 다만 규모가 **웹 화면 문자열 107건 / 웹 테스트 it 블록 90개 / 서버 Go 40파일 / e2e 셸 20파일**이고, `lib/mock/server-wording/*` 자물쇠가 웹 목 문장을 **서버 Go 소스와 글자 단위로 대조**하므로 **서버와 웹을 쪼개면 중간 PR 이 반드시 빨개진다**. 쪼갤 자유가 없다는 뜻이다.

---

## 1. 화면 → 계약 op 표 — **R0 의 입력**

### 1.0 판정 요약

| 분류 | 개수 | 뜻 |
|---|---|---|
| 기존 op 그대로 | 41 | 에이전트·컴퓨터·작업 폴더·HITL 응답·활동 피드 — 방 전환의 영향 밖 |
| **이름만 별칭**(`session*`→`room*`, 요청·응답 그대로) | **4** | `rebindSession` · `getSessionCost` · `setSessionSubscription` · `getMessage` |
| **단위 이동 별칭**(경로·동사는 같고 **대상이 session→work**) | **6** | `pauseSession`·`resumeSession`·`completeSession`·`cancelSession`·`changeDirector`·`startSession` |
| **기존 op 확장**(스키마·파라미터를 더한다) | **12** | 아래 §1.3 |
| **신규 op** | **31** | 아래 §1.2 |

> **「세션 op 을 방 op 으로 개명하면 된다」가 성립하지 않는 이유.** `Session` 한 리소스가 **방의 것**(name·description·visibility·참여자·격리·컴퓨터·누적 비용)과 **미션의 것**(goal·성공기준·종료조건·진행률·Director·미션 비용)으로 갈린다. `listSessions` 의 응답 `SessionListItem` 은 16칸 중 **goal·status·paused_reason·director·completion_progress·cost_usd·budget_usd·cost_estimated 여덟 칸이 미션의 것**이라 방 목록 카드로 쓸 수 없다(SCREEN §4.3 이 "상태 배지·goal·진행률·예산% 가 전부 빠진다"고 적은 그 칸들이다). 그러므로 별칭은 **op 이름**에만 걸고 **스키마는 새로** 만든다.

### 1.1 화면별 전수

| 화면 | 부르는 op | 판정 |
|---|---|---|
| **S5 방 목록** | `listRooms` | **신규**(N1). `listSessions` 별칭 가능하나 응답 스키마 `RoomListItem` 은 새것 |
| | `archiveRoom`·`unarchiveRoom`·`deleteRoom` | **신규 2 + 별칭 1**(N5·N6, `deleteSession` 확장 — 거부 사유가 「진행 중 미션」으로) |
| | `getWorkspaceSettings` | 기존 (컴퓨터 0개 보조 안내) |
| **S25 방 찾기** | `listRooms` 파라미터 확장 | **신규 N1 에 포함** — `q`·`sort`·`unread_only`·`participating`·`include_archived` |
| | 메시지 본문 검색 | **⚠ 계약·PRD 충돌** — SCREEN §4.4 는 「메시지 본문」을 검색 범위로 적었고 PRD §12.1-11 확정은 「이름·설명 부분 일치」다. §6-C1 |
| **S18 방 만들기** | `createRoom` | **신규**(N2). `createSession` 은 `{title,goal,isolation,participants}` 필수라 별칭 불가 |
| | 이름 중복 경고 | 화면이 `listRooms?q=` 로 판정 — **op 불필요** |
| | 런타임 0개 | **계약 변경 필요** — `createSession` 은 런타임 0개에 `409 no_runtime`. `createRoom` 은 그 검사를 **빼야** SCREEN §2.1(v0.3 m5 철회)이 성립한다. R0 에 명시 |
| **S7 방 화면 · 상단** | `getRoom` | **신규**(N3) — `getSession` 별칭, 응답 `Room` 새것 |
| | `listWorks`(칩 줄·「지난 미션 N개」) | **신규**(N15) |
| | `markRoomRead` | **신규**(N9) — 안 읽음 해소 |
| | `summarizeRoom`(「여기까지 정리」) | **신규**(N10) — FR-2.5 의 범위·인용 기록 포함 |
| | `archiveRoom`·`deleteRoom`·`transferRoomOwner` | **신규**(N5·N6·N7) |
| **S7 좌열** | `listParticipants` | **확장**(E1) — 사람 행 추가, `kind: user\|agent`, 방 역할 |
| | `listLanes` | **확장**(E2) — `work_id` 필터 + `Lane.work_id`·`work_title` |
| | `listLaneTasks`·`cancelLane`·`restartLane`·`listTaskEvents` | 기존 그대로 |
| **S7 가운데** | `listMessages` | **확장**(E3) — `?work_id=` · `?around_message_id=`(안 읽음 앵커) |
| | `postMessage` | **확장**(E4) — `work_id?` 본문 칸 |
| | `previewTriggers` | **확장**(E5) — 응답에 `work{id,title}` + `work_source: chosen\|thread\|running_lane\|none`. **FR-3.1.1 4규칙 중 3번(자동 귀속)을 화면이 스스로 못 센다** — 서버가 말해야 한다 |
| | 「이걸 미션으로」 | `createWork` (N16) + `from_message_id` |
| **S7 우열 (가) 미션 칸** | `getWork`·`updateWork` | **신규**(N17·N18) |
| | `pauseWork`·`resumeWork`·`completeWork`·`cancelWork`·`deleteWork`·`changeWorkDirector` | **단위 이동 별칭 6**(기존 session 동사) |
| **S7 우열 (나) 방 칸** | `listArtifacts`·`listDecisions` | **확장**(E6·E7) — 응답에 `work_id`, 「미션 없음」 묶음 |
| | `getRoomCost` | **별칭**(`getSessionCost`) — 단 **미션 비용은 `Work.cost_usd`** 로 따로 온다 |
| | `listRoomReads`(맥락 오간 기록 요약) | **신규**(N14) |
| **S21 미션 열기** | `createWork` | **신규**(N16) — `409 max_concurrent_works` · `422 reviewer_required`(기존 검증 재사용 가능) |
| | 상한 미리 알림 | `listWorks?status=active` 로 화면이 판정 |
| **S22 미션 패널** | `getWork` | **신규**(N17) — 라우트는 있으나 화면은 S7 의 열. **PRD §6 「별도 라우트는 두지 않는다」와 충돌**(§6-P3) |
| **S26 미션 제안 확인** | `listWorkProposals`·`getWorkProposal`·`resolveWorkProposal` | **신규 3**(N19·N20·N21) |
| | (에이전트 쪽) `createWorkProposal` = `colab work propose` | **신규**(N22, R3) |
| | **⚠ 저장 자리 없음** | PRD §7 에 `work_proposal` 표가 없다 — §6-P6 |
| **S19 참여자 초대·퇴장** | `addParticipant` | **확장**(E1b) — `{agent_id? \| user_id?}` oneOf. 지금은 `agent_id` **필수** |
| | `removeParticipant` | **확장**(E1c) — 경로 `{agentId}` → `{participantId}`. 사람 퇴장 거부 규칙(Director·방장) 추가 |
| | `updateParticipant` | **확장**(E1d) — `room_role: member\|deputy` |
| | `setRoomDeputy` | **신규**(N8) 또는 E1d 에 흡수 |
| | `listMembers` | 기존 (사람 탭 후보 목록) |
| **S20 방 설정** | `updateRoom` | **신규**(N4) — `visibility`·`runtime_id`·`isolation`·`limits`·`autonomy`·`default_director_user_id`. `409 runtime_pinned` |
| | `transferRoomOwner`·`setRoomDeputy` | **신규**(N7·N8) |
| | `checkRepo` | 기존 (worktree 저장소 선택) |
| **S24 참고 방 링크** | `listRoomLinks`·`createRoomLink`·`deleteRoomLink` | **신규 3**(N11·N12·N13) |
| | 연결 후보 검색 | `listRooms?participating=true&q=` (N1) |
| **S23 맥락 읽기 기록** | `listRoomReads` | **신규**(N14) — 읽은·읽힌·거부 세 방향 |
| | **⚠ 저장 자리 없음** | PRD §7 에 `room_read` 표가 없다. `activity_log` 뿐이고 그마저 **조회 op 이 없다** — §6-P7 |
| **S8 받은 요청** | `listInbox` | **확장**(E8) — `?work_id`·`?room_id` 필터, 응답에 `work_id`·`lane_id`·`context_line`·`recipient_basis` |
| | `InboxItemType` enum | **확장**(E9) — 신규 5(`work_proposed`·`work_paused`·`room_paused`·`work_completed`·`room_invited`), 개명 2(`session_paused`·`session_completed`) |
| | `respondHitlRequest`·`markInboxRead`·`getInboxSummary` | 기존 그대로 |
| **S9 에이전트 목록** | `listAgents` | **확장**(E10) — `room_count` · `rooms[]`(볼 수 있는 것만) · `hidden_room_count` · `concurrent_used/max` |
| **S11·S13·S17** | `listRuntimes`·`listRuntimeWorkdirs`·`rebindSession`·`listRuntimeCandidates` | **별칭 + 확장**(E11) — `?session_id=`→`?room_id=`, 「묶인 방 N개」, `Workdir.room_id` |
| **S14 설정** | `getWorkspaceSettings`·`updateWorkspaceSettings` | **확장**(E12) — 「방 기본값」 묶음 명시 |
| | `setRoomSubscription`(별칭) + `setWorkSubscription`·`setLaneSubscription` | **신규 2**(N23·N24) — PRD §10 R2 [V19-C] 가 요구한 구독 단위 하향 |
| | `getWorkspaceMetrics` | 기존 — 다만 분모가 미션/방으로 갈린다(§12.1-10 확정) |
| **S15 활동 로그** | `listActivityLog` | **신규**(N25) — **계약에 활동 로그 조회 op 이 하나도 없다**(op 97개 전수 확인). S15 v1 승격이 성립하지 않는다 |

### 1.2 신규 op 초안 — 요청·응답 칸

> 아래 칸은 **화면이 실제로 그리는 것에서 역산**했다. PRD §7 v0.19 스키마(방장·부방장·`visibility`·`blocked_reason`·`last_read_message_id`·`inbox_item.work_id`)를 전제한다 — 그 칸들은 PR #271 에서 이미 들어갔다.

| # | op | 메서드·경로 | 요청 | 응답(주요 칸) |
|---|---|---|---|---|
| **N1** | `listRooms` | `GET /workspaces/{ws}/rooms` | `q` · `sort=last_activity\|name` · `unread_only` · `participating`(기본 true) · `include_archived`(기본 false) · `cursor` · `limit` | `RoomListItem[]`: `id·name·description·status·blocked_reason·unread_count·active_work_count·attention{hitl_open,blocked,failed}·participants[{kind,id,name,avatar_url}]·my_room_role·last_activity_at` |
| **N2** | `createRoom` | `POST /workspaces/{ws}/rooms` | `{name(1..200), description?}` | `Room`. **런타임 0개여도 201** |
| **N3** | `getRoom` | `GET /rooms/{id}` | — | `Room`: N2 + `visibility·owner_user_id·deputy_owner_user_id·runtime_id?·runtime·isolation·limits·autonomy·default_director_user_id·blocked_detail·counts{works_active,lanes_active,tasks_active}·unread_count·my_room_role·my_capabilities[]` |
| **N4** | `updateRoom` | `PATCH /rooms/{id}` | 위 설정 칸들(부분) | `Room`. `409 runtime_pinned`(첫 dispatch 뒤 `runtime_id`·`isolation`), `422` |
| **N5** | `archiveRoom` / `unarchiveRoom` | `POST /rooms/{id}/archive` · `/unarchive` | — | `Room`. `409 room_has_running_tasks` |
| **N6** | `deleteRoom` | `DELETE /rooms/{id}` | — | `204`. `409 work_active`(진행 중 미션 수를 `Problem` 확장 칸에) · `409 workdir_unmerged`(기존 `Problem.workdirs[]` 그대로) |
| **N7** | `transferRoomOwner` | `POST /rooms/{id}/owner` | `{user_id}` | `Room` + 시스템 메시지 |
| **N8** | `setRoomDeputy` | `PUT`·`DELETE /rooms/{id}/deputy` | `{user_id}` | `Room` |
| **N9** | `markRoomRead` | `PUT /rooms/{id}/read` | `{last_read_message_id}` | `{room_id, unread_count: 0}` |
| **N10** | `summarizeRoom` | `POST /rooms/{id}/summary` | `{from_message_id?, to_message_id?, since?}` | `Message(kind: summary)` + `scope{from,to,quoted_message_ids[]}` (FR-2.5 [V19-C]) |
| **N11** | `listRoomLinks` | `GET /rooms/{id}/links` | — | `RoomLink[]{id, target_room{id,name,description,last_activity_at}, created_by, created_at}` |
| **N12** | `createRoomLink` | `POST /rooms/{id}/links` | `{target_room_id}` | `RoomLink`. `403 not_participant_of_target` |
| **N13** | `deleteRoomLink` | `DELETE /rooms/{id}/links/{linkId}` | — | `204` |
| **N14** | `listRoomReads` | `GET /rooms/{id}/reads` | `direction=out\|in\|denied` · `agent_id` · `from`·`to` · `cursor` | `RoomReadEntry[]{at, direction, agent{id,name}, originator_user{id,name}, other_room{id,name}\|null, scope{summary,recent_n}, truncated, task_id?, denied_reason?}` — **`direction=denied` 에서는 `other_room` 을 반드시 `null`** (FR-4.5 존재 숨김) |
| **N15** | `listWorks` | `GET /rooms/{id}/works` | `status[]` · `include_closed` | `WorkListItem[]{id,title,goal,status,paused_reason,director,assignee_agent_id,completion_progress{met,total},cost_usd,budget_usd,last_activity_at}` |
| **N16** | `createWork` | `POST /rooms/{id}/works` | `{goal, title?, acceptance_criteria[], assignee_agent_id?, completion_condition, director_user_id?, deputy_director_user_id?, autonomy?, limits?, from_message_id?}` | `Work`. `409 max_concurrent_works`(열린 미션 목록을 `Problem` 확장에) · `422 reviewer_required`·`reviewer_not_participant`(기존 v0.1.4 검증 재사용) |
| **N17** | `getWork` | `GET /works/{id}` | — | `Work`: `Session` 에서 goal 쪽 칸 전부 + `room_id`·`completion_progress`·`paused_detail`·`my_work_role: director\|deputy\|member` |
| **N18** | `updateWork` | `PATCH /works/{id}` | `goal?·acceptance_criteria?·completion_condition?·limits?·autonomy?·assignee_agent_id?` | `Work` (기존 `updateSession` v0.1.4 의 「조건 고치기」 규칙 그대로) |
| **N19** | `listWorkProposals` | `GET /rooms/{id}/work-proposals` | `status=open\|all` | `WorkProposal[]{id, agent{id,name}, proposed_goal, rationale, trigger_message_id, status, resolved_by, resolved_at, reject_reason}` |
| **N20** | `getWorkProposal` | `GET /work-proposals/{id}` | — | `WorkProposal` |
| **N21** | `resolveWorkProposal` | `POST /work-proposals/{id}/resolve` | `{action: accept\|reject, goal?, reject_reason?}` + 나머지 `createWork` 칸 | `{proposal, work?}`. `409 already_resolved`(다른 사람이 먼저 — S26 빈 상태가 이것을 그린다) |
| **N22** | `createWorkProposal` | `POST /rooms/{id}/work-proposals` (TaskToken) | `{goal, rationale, trigger_message_id}` | `WorkProposal` — R3 `colab work propose` |
| **N23** | `setWorkSubscription` | `PUT /works/{id}/subscription` | `{level: all\|hitl_only\|completion_only}` | `{work_id, level}` |
| **N24** | `setLaneSubscription` | `PUT /lanes/{id}/subscription` | 〃 | `{lane_id, level}` |
| **N25** | `listActivityLog` | `GET /workspaces/{ws}/activity` | `from`·`to`·`actor_kind`·`actor_id`·`room_id`·`kind[]`·`cursor` | `ActivityEntry[]{at, actor{kind,id,name}, action, object_ref, room{id,name}?, payload(마스킹 반영)}`. 권한 owner·admin |
| N26~N31 | 별칭 6 | `pauseWork`·`resumeWork`·`completeWork`·`cancelWork`·`deleteWork`·`changeWorkDirector` | 기존 session 동사와 같음 | `Work` |

### 1.3 기존 op 확장 12

E1 `listParticipants`(사람 행) · E1b `addParticipant`(`user_id`) · E1c `removeParticipant`(경로·거부 규칙) · E1d `updateParticipant`(`room_role`) · E2 `listLanes`(`work_id`) · E3 `listMessages`(`work_id`·앵커) · E4 `postMessage`(`work_id`) · E5 `previewTriggers`(귀속 미션) · E6·E7 `listArtifacts`·`listDecisions`(`work_id`) · E8·E9 `listInbox`·`InboxItemType` · E10 `listAgents`(참여 방) · E11 runtime 계열(`room_id`) · E12 `workspace_settings`(방 기본값).

> **E5 가 가장 중요하다.** FR-3.1.1 의 귀속 4규칙 중 **3번(멘션 대상이 이 방에서 실행 중인 서브 미션을 갖고 그것이 미션에 매여 있으면 그 미션)** 은 서버 상태를 봐야 한다. `Composer.tsx` 주석이 이미 같은 이유로 "로컬 규칙 계산은 없다"를 못박아 두었다(W-6·S-1 의 교훈). 미리보기 칩이 **없으면** 잡담이 말없이 미션에 들어가고 그 미션의 비용·브리프가 오염된다(SCREEN §4.6 이 스스로 적은 위험). → **E5 는 R0 필수, 컷 불가.**

---

## 2. SSE 이벤트 표

### 2.1 현재

`web/lib/realtime/stream.ts` `STREAM_EVENT_TYPES` **26종** = `contracts/openapi.yaml` `StreamEvent.type` enum 26종 (전수 일치 확인).

**버려지는 방식이 조용하다** — `openStream()` 은 `for (const t of STREAM_EVENT_TYPES) es.addEventListener(t, handler)` 로 **목록에 있는 이름만** 듣는다. `es.onmessage` 는 `event:` 필드 **없이** 온 프레임만 받으므로, 서버가 `event: room.updated` 를 보내면 **어디에도 도달하지 않고 사라진다**. 오류도 로그도 없다(v1.1 S5 카드 삭제에서 실제로 겪은 일 — `p5-web-t-w13`).

### 2.2 화면이 실시간으로 받아야 하는 것 대 현재

| 화면이 필요로 하는 것 | 근거 | 현재 타입으로 되나 | 신규 |
|---|---|---|---|
| 미션 칩 줄(열림·닫힘·상태) | §4.6 · §6 | ❌ | **`work.created` · `work.updated` · `work.closed`** |
| 미션 종료 조건 진행률 | §4.6 우열 | 부분 — `session.completion_progress` 가 있으나 `session_id` 축 | **`work.completion_progress`**(별칭·payload `{work_id, completion_progress}`) |
| 서브 미션 보드(미션 라벨 포함) | §4.6 좌열 | `lane.updated` ✅ | payload `Lane.work_id` 확장 |
| 방 멈춤 배너 | §4.6 · §4.3 | ❌ (`session.updated` 는 미션 축) | **`room.updated`**(`blocked_reason`·`blocked_detail`·`last_activity_at`) |
| 방 목록 카드 갱신·삭제 | §4.3 | 부분 | `room.updated` + **`room.deleted`**(`session.deleted` 별칭) |
| **안 읽음** 배지 | §4.3·§4.4·내비 | ❌ | **`room.unread`** `{room_id, unread_count}` — **내가 다른 탭·기기에서 읽었을 때도 내려야** 배지가 두 곳에서 갈린다 |
| 참여자 목록 변화(초대·퇴장) | §4.6 좌열·§4.10 | ❌ — `participant.updated` 는 **상태만**(status·status_note·profile) | **`participant.joined` · `participant.left`** |
| 맥락 읽기 기록(읽힘·거부) | §4.13·§6 | ❌ | **`room_read.recorded`** `{room_id, direction, entry}` |
| 미션 제안 도착·처리 | §4.9 | ❌ | **`work_proposal.created` · `work_proposal.resolved`** |
| 참고 방 링크(반대쪽에서 풀림) | §4.12·§6 | ❌ | **`room_link.updated`** `{room_id, links_version}` |
| 시스템 메시지 5종 | §4.6 | `message.created` ✅ | payload 에 `kind: system` 이미 있음 — 추가 불필요 |
| 방 누적 비용 ↔ 미션 비용 | §4.6 | 부분 — `cost.updated{session_id,…}` | **두 수로 갈린다**: `cost.updated{room_id, room_cost_usd, work_id?, work_cost_usd, estimated}` 로 확장 |
| HITL 수신자 근거 | §4.14 | `hitl.created`·`hitl.updated` ✅ | payload `approver_spec: room_owner` 확장 |
| 받은 요청 항목(방·미션 축) | §4.14 | `inbox.item_created` ✅ | payload `InboxItem.work_id`·`lane_id` 확장 |

### 2.3 신규 SSE 초안 — **14종**

```
room.updated            {room: Room(부분: blocked_reason·blocked_detail·status·last_activity_at·name·description)}
room.deleted            {room_id}                                    ← session.deleted 별칭
room.unread             {room_id, unread_count, last_read_message_id} ← ephemeral 아님(배지의 정본)
work.created            {work: WorkListItem}
work.updated            {work: WorkListItem(부분: status·paused_reason·cost_usd·title·goal)}
work.closed             {work_id, status: completed|cancelled, summary_message_id?}
work.completion_progress {work_id, completion_progress}              ← session.completion_progress 별칭
participant.joined      {room_id, participant: Participant}
participant.left        {room_id, participant_id, kind, left_at}
room_read.recorded      {room_id, entry: RoomReadEntry}
room_link.updated       {room_id, action: linked|unlinked, target_room{id,name}}
work_proposal.created   {room_id, proposal: WorkProposal}
work_proposal.resolved  {room_id, proposal_id, action, work_id?}
activity.appended       (S15 는 실시간 없음 — SCREEN §4.18 이 "흐르면 읽을 수 없다"고 못박았다. **넣지 않는다**)
```
→ 실제 추가는 **13종**(맨 뒤 행은 넣지 않기로 하는 결정의 기록).

### 2.4 구독 범위 파라미터도 바뀐다

`streamEvents` 는 `?session_id=` 로 범위를 좁힌다(`stream.ts` `streamUrl()`·`useStream({sessionIds})`). **`?room_id=` 로 바꾸고 `work_id` 는 넣지 않는다** — 한 방의 미션 3개를 각각 구독하면 방 배너·참여자·안 읽음이 어느 구독에도 안 걸린다. **[추정]** 미션 단위 거르기는 클라이언트에서 `payload.work_id` 로 한다.

> **R0 가 이 목록을 계약에 못박지 않으면 R2 가 통째로 조용히 죽는다.** 서버가 새 이벤트를 발행해도 `STREAM_EVENT_TYPES` 와 openapi enum **양쪽**에 이름이 오르기 전에는 화면에 한 건도 도달하지 않는다. 두 곳이 같은 PR 에 있어야 한다.

---

## 3. 컴포넌트 재사용 표

`web/components/` 60파일 실측 기준.

### 3.1 그대로 쓰는 것 — 11

| 컴포넌트 | 근거 |
|---|---|
| `Badge` + `badge-map.ts` | 글리프·톤 규칙이 층과 무관. `paused` 에 층 표기(§5)는 **호출자가 문자열로** 넣는다 |
| `AgentChip` + `AgentChip.derive.test.tsx` | FR-1.3 파생 규칙 그대로. 「다른 방에서 작업 중」 둘째 줄은 **기존 `status_note` 슬롯** 재사용 |
| `ConditionRow` · `ConditionEditor` · `FixConditionDialog` | 종료 조건은 미션의 것으로 **내려갈 뿐** 구조가 같다 |
| `ActivityFeed` · `ActivityRail` · `LaneTaskHistory` | task 축이라 방 전환의 영향 밖 |
| `lib/markdown.tsx` | 의존성 0 |
| `PageHead` · `DisabledHint` | 문구만 바뀐다 |
| `Icon` · `ThemeSelect` · `ConnectionBanner` | — |

### 3.2 슬롯·prop 만 더하는 것 — 9

| 컴포넌트 | 더할 것 | 규모 **[추정]** |
|---|---|---|
| `LaneCard`(200줄) | **미션 라벨 슬롯 1개**(「미션 〈…〉」/「미션 없음」) + `paused` 층 표기 + `queued` 대기 사유 | 소 (~20줄) |
| `HitlCard`·`HitlBody`(315줄) | **수신자 근거 한 줄**(「Director 로서」/「방장으로서」) | 소 |
| `InboxItemCard`(259줄) | `TYPE_LABEL`·`TONE_BY_TYPE` 에 **5종 추가·2종 개명** + 맥락 한 줄 슬롯 + 방·미션 두 바로가기 | 중 (~60줄) |
| `PausedBanner`(178줄) | **두 크기**(전폭=방 / 우열=미션) + 층 표기 + 「N개가 멈췄습니다」 | 중 |
| `Composer`(377줄) | **미션 선택기** + 미리보기 칩에 귀속 미션. 서버 `TriggerPreview` 확장(E5)에 의존 | 중 (~70줄) |
| `MessageCard`(137줄) | 미션 라벨 + 「…」에 **「이걸 미션으로」**(이미 미션이면 비활성+사유) | 소 |
| `SessionCardMenu`(152줄) → `RoomCardMenu` | 항목 2 → **3**(보관·보관 해제·삭제). 「되돌릴 수 없음」 꼬리표 | 소 |
| `DeleteSessionDialog`(123줄) → `DeleteRoomDialog` | 문구 교체 + 사라지는 것 목록 확장. `409 workdir_unmerged` 경로 그대로 | 소 |
| `RebindDialog`(252줄) | 방 단위 문구 + 「열린 미션 모두 취소」. **diff 적용 순서는 말하지 않는다**(G-4) | 소 |

### 3.3 쪼개거나 크게 고치는 것 — 3

| 지금 | 어떻게 |
|---|---|
| **`SessionAside`(210줄)** | **둘로 쪼갠다** — `WorkPanel`(목표·성공기준·종료조건 진행률·미션 비용·미션 동작·assignee·Director) + `RoomPanel`(산출물·결정 기록·방 누적 비용·맥락 오간 기록·방 설정 요약). 내부 절(`aside__sec`) 단위로 이미 갈려 있어 **분할선이 깨끗하다** — 코드 재사용률 높음 |
| **`ParticipantsDialog`(159줄)** | **절반 재사용**. 지금은 에이전트 전용 목록. 두 구역(지금 있는 사람·에이전트 / 초대하기 + 사람·에이전트 두 탭) + 내보내기 거부 규칙 + 부방장 지정. 실질 **재작성에 가깝다** |
| **`app/(app)/sessions/[id]/page.tsx`(843줄)** | 3열 골격(`s7__cols`·`s7__left`·`s7__center`·`s7__right`·`s7__tabs` 열 전환)은 **그대로 살아남는다**. 바뀌는 것은 상단 머리(칩 줄·액션 분리)와 우열(둘로 쪼갬), 그리고 데이터 로딩 축(session → room + works) |

### 3.4 새로 만들 것 — 12

`WorkChipRow`(칩 줄 + 4개 초과 시 드롭다운 — §8.7 Q8) · `RoomBlockedBanner`(전폭) · `CreateRoomDialog`(S18) · `CreateWorkDialog`(S21) · `WorkProposalDialog`(S26) · `RoomLinksDialog`(S24) · `RoomReadsTable`(S23) · `RoomSettingsForm`(S20, 7묶음) · `RoomSearchBar`(S25) · `UnreadBadge` · `AuditLogTable`(S15) · `TransferOwnerDialog`.

> **「확인 다이얼로그」가 컴포넌트가 아닌 것이 이제 문제가 된다.** COMPONENTS §5 는 이미 "(컴포넌트 없음)"으로 적어 두었는데, v0.19 에서 쓰는 곳이 **방 삭제·보관·참여자 내보내기·방장 넘기기·링크 해제·미션 취소·제안 거절** 일곱으로 늘어난다. `DeleteSessionDialog` 를 일반화하는 것이 가장 싸다 **[추정]**.

### 3.5 판정

**신규 9화면 중 「기존 컴포넌트 조립만으로 서는 것」은 0개다.** 가장 가까운 것이 S22(미션 패널) — `SessionAside` 분할 + `ConditionRow`·`PausedBanner` 재사용으로 거의 다 된다. 가장 먼 것이 **S23·S24·S15** — 화면 골격도 데이터도 op 도 전부 새것이다.

---

## 4. 자물쇠 충격

### 4.1 자물쇠가 지금 재는 것

| 자물쇠 | 재는 것 | 범위 |
|---|---|---|
| `web/lib/wording.test.ts` | §8.4 용어표 **옛말 0건** + 새말 존재 + 내부 용어 누출 | `app`·`components`·`lib` 전부, **`lib/mock`·`lib/api/schema.d.ts`·`app/dev` 제외** |
| `web/lib/mock/server-wording/*.test.ts` (9파일) | **목 문장 ↔ `server/` Go 소스 리터럴 글자 단위 대조** | `server/internal/{apperr,auth,sessions,metrics,httpapi}` |
| `web/app/typography.test.ts` · `contrast.test.ts` · `layout.test.ts` | px 리터럴·대비·레이아웃 | `app`·`components` |

### 4.2 실측 — 자물쇠 풀(POOL)을 그대로 재현해 센 수

자물쇠의 `visibleStrings()` 를 그대로 옮겨 `origin/dev` 트리에 돌린 결과:

```
FILES 77 · POOL 1421 (화면에 닿는 문자열)
  「세션」      84건 / 21파일
  「작업 줄기」 19건 / 12파일
  「아티팩트」   4건 /  4파일
  「산출물」      8건 /  4파일   ← 이미 쓰고 있다(§6-C2 참조)
  「걸린 세션」   0건            ← 이미 「쓰는 중인 세션」으로 바뀜
```

`세션` 상위 파일: `lib/settings.ts`(12) · `lib/wording.ts`(12) · `components/SessionActions.tsx`(8) · `app/(app)/runtimes/page.tsx`(7) · `app/(app)/sessions/new/page.tsx`(7) · `components/SettingsTabs.tsx`(6) · `components/RebindDialog.tsx`(5).

### 4.3 깨지는 테스트 수

| 층 | 수 | 근거(grep 실측) |
|---|---|---|
| 웹 `it`/`test` 블록 **총수** | **634** | `web/**/*.test.ts(x)` 파싱 |
| 그중 **옛말(세션·작업 줄기·아티팩트)을 블록 본문에 가진 것** | **90** (14%) | 32파일 |
| 웹 테스트 파일 총수 / 옛말이 든 파일 | **62 / 36** | |
| 옛말이 든 블록 상위 | `lib/mock/p4-golden.test.ts` 12/28 · `lib/mock/delete-session.test.ts` 8/11 · `components/InboxItemCard.test.tsx` 7/23 · `app/(app)/sessions/page.test.tsx` 6/7 · `components/SessionCardMenu.test.tsx` 6/12 | |
| **서버** 비테스트 `.go` 파일 | **40** (문자열 「세션」 206건 · 「작업 줄기」 17 · 「아티팩트」 40) | |
| **서버** 테스트·골든 파일 | **23** | `apperr_test`·`members_test`·`g5_test`·`budget_golden_test`·`summary_golden_test`·`p4_golden_wire_test` 등 |
| **e2e 셸** 파일 | **20+** — `e2e/p1/07_adversarial.sh` 45건 · `p2/11_scenario_a_web.sh` 15 · `p2/33_approval_completed.sh` 16 · `p2/34_template_3min.sh` 10 | |

### 4.4 R1.5 를 한 PR 에 가둘 수 있는가 — **가둘 수 있고, 사실은 가둬야만 한다**

**가둘 수 있는 이유.** `server/`·`web/`·`e2e/`·`contracts/` 가 **한 저장소**다. `server-wording/_shared.ts` 가 `SERVER_ROOT = web/../server` 를 `readFileSync` 로 직접 읽으므로 서버 Go 리터럴과 웹 목 문장은 **같은 커밋에서만** 초록일 수 있다.

**가둬야만 하는 이유(쪼갤 자유가 없다).**
- 서버 문장만 먼저 고치면 → `server-wording/a·b·d·e·g` 다섯 파일이 즉시 빨강.
- 웹 문장만 먼저 고치면 → 같은 다섯 파일이 반대 방향으로 빨강.
- 화면 문구만 고치고 자물쇠 표를 안 고치면 → `wording.test.ts` 의 `ROWS` 에 **`Sessions → 세션`** 행이 있어 **「세션」이 새말로 등록되어 있다.** 옛말로 뒤집는 순간 그 행 자체가 자기모순이 된다. 표와 문구가 같은 PR 이어야 한다.

**그런데 「한 PR」의 규모가 크다.** 문자열 변경만 대략 — 웹 화면 107건 + 웹 테스트 단언 221건 + 서버 263건 + e2e 150건 **≈ 740 자리**. 컷 순서나 리뷰 단위로 쪼갤 수 없으므로 **커밋으로 쪼개는 것이 유일한 완화책**이다 **[추정]**:
`(1) 자물쇠 표 교체 + 새 행 추가(이때 빨강)` → `(2) 웹 화면 문구` → `(3) 웹 목 + 서버 Go` → `(4) 테스트 단언` → `(5) e2e 셸` → `(6) 스크린샷 재촬영`.

### 4.5 이 라운드가 밟을 지뢰 넷

1. **「세션」은 1:1 치환이 아니다.** 84건 중 문맥에 따라 **방**과 **미션**으로 갈린다(`SessionActions`=미션, `runtimes/page.tsx` 「쓰는 중인 세션」=방). `sed` 로 못 한다 — 84건을 손으로 판정해야 한다.
2. **`runtime_session_ref` 예외.** SCREEN §3.4(c) 가 「세션」을 쓰지 말라 했으므로 자물쇠에 예외를 안 넣으면 `LaneCard` 의 「이전 대화를 이어받음」 설명이 걸린다. **현재 문구는 이미 「세션」을 안 쓴다**(실측) — 예외 행이 필요 없을 수 있다 **[추정]**.
3. **「아티팩트」 치환은 넣으면 안 된다.** PRD §3.2(Director 확정 2026-09-23)가 **「산출물」로 바꾸지 않는다**고 못박았는데 SCREEN §3.4(b)는 치환 대상으로 올렸다. 자물쇠에 넣으면 **계약과 반대로 잠긴다.** → §6-C2.
4. **`lib/mock` 이 `wording.test.ts` 범위 밖이다.** 목 문장 32건의 「세션」은 `wording.test.ts` 로는 안 걸리고 `server-wording` 으로만 걸린다. 둘 다 고쳐야 하고, 고치는 규칙이 다르다(목은 **서버를 따라간다**).

---

## 5. R2 서브 순서(PRD §10) 검증

> 원문: `(a) /rooms 신설 + /sessions/* 리다이렉트 공존 → (b) S5·S7 재작성 → (c) S18~S21 신규 → (d) 마법사 삭제 → (e) R1.5 문구 자물쇠 → (f) 별칭 제거`. DoD: 「각 단계가 혼자 CI 초록. 두 라우트 공존 규칙 = 옛 경로는 새 경로로 **302**」.

### 5.1 단계별 판정

| 단계 | 혼자 CI 초록? | 문제 |
|---|---|---|
| **(a)** 라우트 신설 + 리다이렉트 | ⚠ **조건부** | 아래 R-1·R-2·R-3 |
| **(b)** S5·S7 재작성 | ✅ 가능 | 단 **목(`lib/mock/handlers.ts`·`store.ts`)이 새 op 을 먼저 흉내내야 한다**. 웹 테스트 634개 전부가 목 위에 선다. (b) 앞에 **목 개정 단계가 서브 순서에 없다** |
| **(c)** S18~S21 신규 | ✅ 가능 | `createRoom` 과 마법사 `createSession` 이 같은 테이블에 두 경로로 쓴다 — R1 이 `session = room + work` 로 만들어 두었으면 공존 OK |
| **(d)** 마법사 삭제 | ✅ 가능 | `/sessions/new`(570줄) + `page.test.tsx` 삭제. 이때 비로소 `/sessions/new` 리다이렉트를 켠다(R-2) |
| **(e)** R1.5 문구 | ✅ 가능 | 단 §4.4 대로 **서버까지 같은 PR**. (e)가 R2 안에 있으면 **이 단계는 서버 변경을 포함한다** — 표가 그 사실을 안 적었다 |
| **(f)** 별칭 제거 | ❌ **여기서 하면 안 된다** | R-5 |

### 5.2 라우트 공존 — Next 15.5.4 에서 실제로 어떻게 되나

- **R-1 · 302 가 기본값이 아니다.** `next.config.mjs` 의 `redirects()` 는 `permanent: false` → **307**, `permanent: true` → **308** 을 낸다. **302 를 내려면 `statusCode: 302` 를 써야 하고, 그때 `permanent` 를 함께 주면 빌드가 실패한다.** DoD 가 「302」라고 못박았으므로 **`{ source: '/sessions/:path*', destination: '/rooms/:path*', statusCode: 302 }`** 형태여야 한다. 지금 `next.config.mjs` 에는 `redirects()` 자체가 없다(`rewrites()` 만 있다).
  - **[추정]** 굳이 302 를 고집할 이유는 약하다. 307 은 메서드·본문을 보존하고 캐시되지 않아 **오히려 안전**하다. 「302」를 문서에 못박은 것이 의도인지 관용어인지 확인이 필요하다 — §6-P4.
- **R-2 · (a) 에서 리다이렉트를 켜면 마법사가 (d) 전에 죽는다.** `(d)` 의 근거는 「먼저 지우면 방 만들기 경로가 사라진다」인데, `(a)` 에서 `/sessions/:path*` 를 통째로 302 시키면 `/sessions/new` 가 **(a) 시점에 이미 도달 불가**다. → `(a)` 의 리다이렉트는 **`/sessions/new` 를 제외**해야 하고, 그 제외를 `(d)` 에서 푼다. **서브 순서에 그 문장이 없다.**
- **R-3 · 두 벌 공존의 테스트 비용.** `app/(app)/sessions/page.test.tsx`·`[id]/page.test.tsx`·`new/page.test.tsx` 는 **페이지 컴포넌트를 직접 import** 한다(라우팅을 안 탄다). 따라서 `/rooms` 를 **신설**(복사)하면 두 벌의 테스트가 동시에 돌고 둘 다 초록이어야 한다 — (b)~(d) 구간에서 **웹 테스트 수가 한동안 1.3~1.5배** 가 된다 **[추정]**. 「이동」이 아니라 「신설」인 것은 옳은 선택이다.
- **R-4 · 클라이언트 내비게이션.** 앱 안의 `<Link href="/sessions/...">` 도 `next.config` 의 redirects 를 따른다 **[추정 — 실측 안 함]**. 다만 `AppNav.NAV_ITEMS` 의 `href: "/sessions"` 는 (b) 에서 **직접 고치는 편이 확실하다** — 내비가 매 클릭 302 를 왕복하면 활성 항목 판정(`current` 접두 일치)이 흔들린다.
- **R-5 · (f) 별칭 제거가 R4 와 충돌한다.** §10 **R4** 행은 「`session*` 별칭 제거는 **이미 설치된 CLI 가 갱신된 뒤**(install.sh 는 서버 커밋을 고정하지만 설치본은 스스로 갱신되지 않는다)」라고 적었다. R2 (f) 에서 제거하면 **아직 옛 CLI 를 쓰는 데몬이 그 순간 죽는다.** 같은 일을 두 곳이 다른 조건으로 적었다 — §6-P5.
- **R-6 · `postbuild` 검사.** `scripts/assert-no-dev-routes.mjs` 가 빌드 매니페스트를 훑는다. `/rooms` 추가는 영향 없음(확인).

### 5.3 빠진 단계 — **8개**

| # | 빠진 것 | 왜 필요한가 |
|---|---|---|
| **M1** | **목(`lib/mock/handlers.ts`·`store.ts`) 개정** | (b) 앞. 웹 테스트 634개와 e2e 목 스크립트 5종이 전부 그 위에 선다 |
| **M2** | **SSE 타입 추가**(`STREAM_EVENT_TYPES` + openapi enum) | (b) 앞. 없으면 새 이벤트가 **조용히 버려진다**(§2.1) |
| **M3** | **S22~S26 다섯 화면** | (c) 는 S18~S21 만이다. S23(맥락 읽기 기록)·S24(참고 방 링크)가 없으면 **FR-4.5 가 반쪽**이고, S26 이 없으면 FR-2A.1 의 제안 경로가 끊긴다 |
| **M4** | **안 읽음 구현**(`markRoomRead` 호출 지점 + 배지 + `room.unread`) | **R2 의 DoD 가 「안 읽음이 방 20개에서 동작」을 통과 기준으로 못박았는데 (a)~(f) 에 그 단계가 없다** |
| **M5** | **S8 받은 요청 개정**(항목 5종 추가·2종 개명·맥락 한 줄·수신자 근거) | F2 지표(중앙값 30분)가 여기 걸린다 |
| **M6** | **S9·S11·S13·S14·S17 수정 5화면** | 「참여 중인 방 N」·「묶인 방 N개」·「쓰는 중인 방」·방 기본값·`?room=` |
| **M7** | **S15 활동 로그 승격** + `listActivityLog` op 신설 | FR-4.5 의 「읽힌 쪽 기록」이 갈 곳이 여기뿐이다(SCREEN §4.18 의 승격 사유 그대로) |
| **M8** | **스크린샷 174장 재촬영 + `EVAL_USER.md` 갱신** | 파일명이 옛 화면 번호를 가리킨다(`p4-w5-01-s6-isolation.png` 등). (e) 뒤 |

### 5.4 권고 순서 **[추정]**

```
(a0) 계약 SSE·op 이름 확정(R0 산물)  →  (a) /rooms 신설 + /sessions/new 제외 302
  →  (M1) 목 개정  →  (M2) SSE 타입  →  (b) S5·S7  →  (c) S18~S21
  →  (M3) S22~S26  →  (M4) 안 읽음  →  (M5) S8  →  (M6) 수정 5화면  →  (M7) S15
  →  (d) 마법사 삭제 + /sessions/new 302  →  (e) R1.5 문구(서버 포함, 한 PR)
  →  (M8) 스크린샷·EVAL  →  [R3·R4 뒤로]  (f) 별칭 제거
```

---

## 6. SCREEN·PRD 고칠 문장 — 구현 관점

### 6.1 SCREEN 쪽

| # | 자리 | 무엇이 틀렸나 | 고칠 방향 |
|---|---|---|---|
| **C1** | §4.4 S25 표 | 검색 범위에 「**메시지 본문**」, 정렬에 「**안 읽음 우선**」이 있다. **PRD §12.1-11 확정은 「이름·설명 부분 일치」·「마지막 활동순, 안 읽음은 배지로만(정렬을 바꾸지 않는다)」** 이다 | 두 줄을 확정에 맞춘다. 메시지 본문 검색을 살리려면 **전문 색인이 필요**하고 그것은 v1.1 이다 **[추정]** |
| **C2** | §3.4(b) 「아티팩트 → 산출물」 · §8.3 D-11 | **PRD §3.2 Director 확정(2026-09-23)은 「아티팩트」 유지**다 — 「'산출물'로 바꾸지 않는다」가 원문. SCREEN 은 반대 방향으로 적었고 §4.6·§4.16·§5·§7 본문이 전부 「산출물」을 쓴다(8자리) | **§3.4(b) 에서 그 행을 뺀다.** D-11 은 「PRD 를 고칠 것」이 아니라 **SCREEN 이 따라갈 것**이다. 자물쇠에 넣으면 계약과 반대로 잠긴다(§4.5-3) |
| **C3** | §8.3 D-1·D-2·D-3·D-5·D-6·D-9·D-12 | **PRD #271·#272 에서 이미 반영됐다** — §7 `room` 에 `visibility`·`blocked_reason`·`deputy_owner_user_id`, `room_participant.role: owner\|deputy\|member`·`last_read_message_id`, `inbox_item.work_id·lane_id`, FR-2.2 에 사람 퇴장 규칙, §11 분모(§12.1-10) | 「Lead 판단 요청」에서 내리고 **반영 확인**으로 바꾼다. 남는 것은 **D-4(부분)·D-7·D-8(부분)·D-10·D-11(방향 반대)** |
| **C4** | §8.2 G-1·G-6·G-8 | 전부 「결정이 열려 있다」를 전제로 쓰였는데 §12.1-6·-10·-7 이 **확정됐다**(안 읽음=방 단위 `last_read_message_id` 사람 행만 / 분모 1·2·5 미션·10 방 / 컨텍스트 재사용 **버린다**) | 공백 아홉 중 **셋을 닫는다**. G-1 은 「모델 없음」이 아니라 **「카운트 질의와 인덱스가 없다」**로 다시 쓴다 — `last_read_message_id` 하나로 배지 수를 세려면 `message` 에 방별 단조 순번이나 `created_at` 인덱스가 있어야 하고 방 20개면 20회 질의다 **[추정]** |
| **C5** | §4.4 S25 각주 | 「`room_participant` 에 `last_read_at`/`last_read_message_id` 가 §7 에 **없다**」 — **있다**(PR #271) | 삭제 |
| **C6** | §4.18 S15 | v1 로 승격했는데 **부를 op 이 계약에 없다**(97개 전수 확인 — activity 관련 op 0개). §8.2 공백에도 없다 | **G-10** 으로 새 공백을 열거나 N25 를 §8.1 대조표에 적는다 |
| **C7** | §4.13 S23 | 「`activity_log` 와 방 타임라인 시스템 메시지 둘을 한 자리에 모은다」 — 그러나 **세 묶음(읽은·읽힌·거부)을 질의할 칸이 §7 에 없다.** `activity_log` 는 행 구조가 §7 에 적혀 있지 않고, 「읽힌 쪽 방 id」로 거를 수 있는지도 미정 | 필요한 칸을 SCREEN 이 명시한다(N14 의 응답 칸이 곧 그 요구다) |
| **C8** | §4.9 S26 | 제안을 저장할 표가 §7 에 없다 | 〃 (§6-P6 과 짝) |
| **C9** | §4.6 상단 액션 | 「여기까지 정리」가 `⋯` 안 한 줄뿐인데 §8.1 대조표는 FR-2.5 를 **✅**(「범위 고르기 + 범위 기록」)로 적었다. **범위를 고르는 UI 명세가 §4 어디에도 없다** | §4.6 에 범위 선택 다이얼로그를 적거나 ✅ 를 내린다 |
| **C10** | §4.6 미션 칩 | 글리프에 「⏳ 사람 대기」가 있으나 **미션 상태 머신(FR-2A.4)에는 `waiting_human` 이 없다**(`draft→active→paused⇄active→completing→completed\|cancelled`) | 칩 글리프가 **상태가 아니라 파생**(열린 HITL 존재)임을 적는다 |
| **C11** | §2.1 · §4.5 | 「컴퓨터 없이도 방을 만들 수 있다」가 화면 결정인데, **계약 `createSession` 은 런타임 0개에 `409 no_runtime`** 을 낸다 | `createRoom` 이 그 검사를 **빼야 한다**는 문장을 R0 입력으로 명시 |
| **C12** | §4.6 작성창 규칙 3 | 「멘션 대상이 실행 중인 서브 미션을 갖고…」를 화면이 판정하는 것처럼 읽힌다 | **서버 `previewTriggers` 가 판정한다**(E5)고 적는다 — `Composer.tsx` 가 이미 같은 이유로 로컬 계산을 금지해 두었다 |

### 6.2 PRD 쪽

| # | 자리 | 무엇이 틀렸나 |
|---|---|---|
| **P1** | §7 `hitl_request` | **한 줄 안에서 값 집합이 둘**이다 — 필드는 `approver_spec: director\|any_member\|user_id`, 같은 줄 주석은 `director\|room_owner\|any_member\|user:<id>`. R0 계약이 어느 쪽을 읽어야 하는지 모른다. SCREEN D-4 가 짚은 `room_owner` 는 **주석에만** 들어갔다 |
| **P2** | FR-4.4 본문 | 「§12.1 의 열린 결정」이라 적혀 있으나 **§12.1-7 은 「버린다」로 확정**. 그리고 §7 에 `room_context` 표가, 계약 `Session` 에 `context[]`·`context_reuse_override` 가 남아 있다 — 버린다면 **셋 다 정리 대상**임을 적어야 R0 이 헷갈리지 않는다 |
| **P3** | §6 UX 표 「미션」 행 | 「방 화면 안에서 열린다 — **별도 라우트는 두지 않는다**」 vs SCREEN S22 가 `?work=:workId` 라우트를 둔다(받은 요청·알림·시스템 메시지에서 바로 보내려면 필요). SCREEN 이 각주로 인정했을 뿐 §8.3 에 올리지 않았다 |
| **P4** | §10 R2 서브 순서 DoD | 「옛 경로는 새 경로로 **302**」 — Next 15 의 `redirects()` 기본값은 307/308 이고 302 는 `statusCode: 302` 를 명시해야 한다. 의도라면 그대로, 관용어라면 **307** 로 고치는 편이 안전하다 **[추정]** |
| **P5** | §10 R2 (f) vs R4 | **별칭 제거를 두 곳이 다른 조건으로 적었다** — R2 (f) 는 R2 안에서, R4 는 「이미 설치된 CLI 가 갱신된 뒤」. R2 에서 제거하면 옛 CLI 를 쓰는 데몬이 그 순간 죽는다 |
| **P6** | §7 데이터 모델 | **`work_proposal` 표가 없다.** FR-2A.1 이 「에이전트는 제안만, 사람이 연다」를 v0.19 의 안전장치로 못박았고 §12.1-2 가 확정했으며 S26 이 화면인데, 제안·근거·트리거 메시지·처리 결과를 담을 자리가 없다 |
| **P7** | §7 데이터 모델 | **다른 방 읽기 기록 표가 없다.** FR-4.5 는 「양쪽 방에 남긴다」를 유출 완화책으로 내걸었고 §12 위험표가 그것을 근거로 삼는다. `task_event` 는 **닫힌 스키마**(S-52)라 읽힌 쪽에 붙일 수 없고, `activity_log` 는 §7 에 **행 구조가 적혀 있지 않다**. 거부된 시도(`403`)까지 남기려면 더 그렇다 |
| **P8** | §10 R2 DoD | 「안 읽음이 방 20개에서 동작」이 통과 기준인데 **서브 순서 (a)~(f) 에 안 읽음 단계가 없다**(§5.3 M4) |
| **P9** | §10 R0 행 | 「**방 참여 op**」이라고만 적었다. 현재 `addParticipant` 는 **`agent_id` 필수**라 사람을 넣을 수 없다(스키마 실측). FR-2.2·S19 가 「사람·에이전트 한 표·한 다이얼로그」를 요구하므로 **`{agent_id \| user_id}` oneOf 로 바꾼다**를 R0 행에 적어야 한다 |
| **P10** | §10 표의 **R1.5 행 위치** | 이름은 「R1 과 R2 사이」를 뜻하는데, 표에서는 **R3 다음 행**에 있고, 실제 실행은 **R2 서브 순서 (e)** 다. 셋이 서로 다르다 — 순서를 읽는 사람이 세 번 다르게 읽는다 |
| **P11** | §7 `work` 행 | `status` 의 **값 집합이 적혀 있지 않다**(`room`·`lane`·`task` 는 적혀 있다). FR-2A.4 가 「v0.18 FR-2.3 의 상태 머신을 그대로」라 했으므로 `draft\|active\|paused\|completing\|completed\|cancelled` 를 §7 에 쓴다 |
| **P12** | §7 `inbox_item` 행 | `session_id→room_id` 라는 표기가 §12.1-3 확정(「열 이름은 R4 까지 유지, 방 id 로 읽는다」)과 표기 규칙이 어긋난다 — 다른 자식 표는 `session_id` 를 그대로 뒀다 |

---

## 7. 이 보고서가 R0 에 넘기는 것

1. **§1.2 의 신규 op 31개 + §1.3 의 확장 12개** — 요청·응답 칸 초안 포함. 이름 별칭으로 끝나는 것은 4개뿐이다.
2. **§2.3 의 SSE 신규 13종 + 구독 파라미터 `session_id`→`room_id`** — 계약 enum 과 `STREAM_EVENT_TYPES` 양쪽에 같은 PR 로.
3. **저장 자리 공백 셋** — `work_proposal`(P6) · 다른 방 읽기 기록(P7) · 활동 로그 조회 op(C6). **R0 계약보다 먼저 §7 스키마 결정이 필요한 것은 앞의 둘이다.**
4. **SCREEN 이 스스로 닫아야 할 것 12건(§6.1)** — 특히 **C2(아티팩트)** 는 R1.5 자물쇠를 계약과 반대로 잠그는 자리라 문구 라운드 전에 반드시 정리돼야 한다.
