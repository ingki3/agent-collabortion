# V19-B · PRD v0.19(방+일) 구현·이관 타당성 검증

| 항목 | 값 |
|---|---|
| 대상 | `git show origin/docs/prd-rooms:PRD.md`(PR #269) §3.1 · FR-2 · FR-2A · FR-4.5 · §7 · §10 v2.0 R0~R4 · §12.1 |
| 코드 기준 | `feat/server-workdir-id` @ `ef31678` (migrations 0001~0024, openapi 0.1.6, daemon-protocol v0.8.3, harness v0.8.11, colab-cli v0.6) |
| 방식 | **읽기 전용.** 코드·문서 수정 0 · 커밋 0 · PR 0. 근거는 파일:줄 |
| 결론 한 줄 | **타당하다. 다만 R0~R4 표는 단계가 4개 모자라고, PRD 에 구현이 못 푸는 구멍이 5개 있다**(방 pause 상태 · 일 밖 라우팅의 assignee/Director · workdir GC 기준점 · FR-4.5 originator · 문구 전환 시점). 아래 §6 이 그 수정문이다 |

---

## 1. 충격 표 — "세션"에 매달린 자리 전수

분류: **방** = 지속되는 대화 공간으로 간다 / **일** = goal·종료 조건과 함께 간다 / **분기** = 하나가 둘로 갈라진다 / **그대로**.

### 1.1 DB — 표와 열 (`server/migrations/`)

| 자리 | 파일:줄 | 판정 | 근거 |
|---|---|---|---|
| `session` 표 본체 | `0001_init.sql:172-203` | **분기** | `workspace_id·runtime_id·isolation` → 방 / `title·goal·acceptance_criteria·director_user_id·deputy·assignee_agent_id·completion_condition·status·paused_reason·paused_detail·cost_usd·started_at·finished_at` → 일 / `limits·autonomy·context_reuse_override` → **둘 다**(FR-2A.3 "작은 쪽") |
| `session_participant` | `0001:207-213` PK `(session_id, agent_id)` | **방** + 확장 | PRD §7 `room_participant` 는 사람·에이전트 **같은 표**. `agent_id`·`profile_id` NOT NULL 을 풀고 `user_id`·`left_at` 추가, PK 교체 |
| `session_context` | `0001:215-224` | **방** | `room_context(type: doc\|url\|file\|room)` — `room` 타입이 FR-4.5 참고 방 링크와 겹친다(§6 NN2) |
| `session_hop` | `0006_p2_routing.sql:35-46`, `0022`(cause_hop_id) | **방** | FR-3.3·3.5 단위가 방(§3.1 표 1행). 이름만 `room_hop` |
| `lane` | `0001:232-255` | **방** + `work_id?` | §7 `lane(work_id?, …)`. `lane_session_agent_recent` 인덱스(해소 규칙 3)는 방 단위로 커진다 |
| `task` | `0001:281-318` | **방** + `work_id?` | `task.session_id` = 방 id. `originator_user_id` 는 FR-4.5 의 입력이 된다(§4 위험 4) |
| `workdir` | `0001:257-277`, UNIQUE `(session_id, path_or_ref)` `0009:28` | **방** | FR-6.1 "작업 폴더는 방×에이전트". 유니크 키는 `(room_id, path)` 로 그대로 읽힌다. **GC 기준점이 깨진다** — §4 위험 1 |
| `message` | `0001:355-373` | **방** + `work_id?` | FR-2A.6 "메시지는 방의 것" |
| `hitl_request` | `0001:385-414` | **방** + `work_id?` | FR-2A.1 "일에 속하지 않은 task 의 HITL 은 방장에게". `approver_spec: director` 의 해소처가 갈라진다 |
| `artifact` UNIQUE `(session_id,name,version)` | `0001:416-425`, `0007:27` | **방** | 방 단위 유니크 → 다른 일에서 같은 이름을 내면 v2 가 된다. **의도인지 결정 필요**(§6 NN4) |
| `decision` | `0001:428-438` | **방** + `work_id?` | FR-4.2 "새 에이전트가 합류해도 왜를 안다" = 방 단위가 맞다 |
| `inbox_item.session_id` (nullable) | `0001:444-456` | **분기** | 카드가 방을 열지 일을 열지 갈라진다. `inbox_item_type` 7종 중 `session_completed`·`session_paused` 는 **일**, 나머지는 방 |
| `activity_log.session_id` (SET NULL) | `0001:458-472` | **방** | FR-2.6 "`activity_log` 에 `room.deleted` 한 줄만" |
| `session_subscription` PK `(session_id,user_id)` | `0002:84-92` | **방** | §5 대응표 "SSE 구독 범위 → 방" |
| `stream_event.session_id` (FK 없음) | `0002:132-142` | **방** | 백필 창이 방 단위로 넓어진다 |
| `task_token.session_id` | `0002:148-162` | **방** | 토큰 범위 = 그 방. FR-4.5 는 **다른 방**을 열어야 하므로 범위 검사에 예외가 생긴다 |
| `daemon_command.session_id` | `0003:26` | **방** | `gc`·`rebind_prepare` 페이로드. 방 단위 |
| ENUM `session_status` | `0001` | **일** | 값 집합(`draft…completed\|cancelled`)이 일의 것. 이름만 남기고 재사용 가능 |
| ENUM `pause_reason` | `0001` | **분기** | 일의 pause + **방의 pause**(FR-2A.3) 둘 다 필요. 방에는 상태가 없다 — §4 위험 2 |
| **신규** | — | — | `room_status(active\|archived)` · `work` 표 · `room_link` 표 · 각 자식의 `work_id` |

세는 자리(비테스트 Go): `FROM session` 55 · `JOIN session` 38 · `UPDATE session` 22 · `INSERT INTO session` 7 = **약 122 SQL 접점**.

### 1.2 계약 op (`contracts/openapi.yaml`, 97 op)

| 판정 | op |
|---|---|
| **방** (26) | `listSessions` `createSession` `getSession` `updateSession` `deleteSession` `listParticipants` `addParticipant` `updateParticipant` `removeParticipant` `rebindSession` `setSessionSubscription` `listMessages` `postMessage` `previewTriggers` `getMessage` `listLanes` `delegateLane` `getLane` `listLaneTasks` `restartLane` `cancelLane` `listSessionTasks` `listArtifacts` `submitArtifact` `listDecisions` `recordDecision` |
| **일** (6) | `startSession` `pauseSession` `resumeSession` `completeSession` `cancelSession` `changeDirector` |
| **분기** (7) | `getSessionCost`(방 비용 ∧ 일 비용 — FR-2A.3 이 둘 다 요구) · `listHitlRequests`·`createHitlRequest`(`work_id?` + 수신자 Director↔방장) · `listInbox`·`getInboxSummary`(카드가 방·일) · `streamEvents`(`?session_id=` → `room_id`) · `getCliContext`(방 + **그 턴의 일**) |
| **그대로** (58) | auth · workspace · members · invites · settings · runtimes · pairing · agents · profiles · templates · testchat · `getArtifact`/`download`/`review` · `getHitlRequest`/`respond` · `getTask`/`listTaskEvents`/`setTaskStatus` · workdirs · `getWorkspaceCost`/`Metrics`/`Observations`(쿼리 안이 바뀐다, 시그니처는 그대로) |
| **신규** | `listRooms`(FR-4.5 `--query`) · `readRoom`(요약+메시지+결정+산출물) · `createRoomLink`/`deleteRoomLink` · `listWorks`/`createWork`/`getWork`/`updateWork`/`proposeWork`(FR-2A.1 3경로) · `archiveRoom` |
| 스키마 | `Session`(`openapi.yaml:4106-4149`) → `Room`+`Work` 로 쪼갠다. `SessionCreate`(`:4190-4224`, 필수 `title·goal·isolation·participants`) → `RoomCreate{name, description?}` **한 칸**(FR-2.1). `SessionListItem`(`:4150`) 의 `goal·completion_progress·director` 는 일에서 온다 |
| 닫힌 enum | `ColabCommand`(`:3222`, 13개) → `room_list`·`room_read` 두 값. **R3 이 아니라 R0 의 일**(§3) |
| SSE 표 | `:4815-4841` — `session.updated`→`room.updated`+`work.updated`, `session.deleted`, `session.completion_progress`(→일), `message.delta/agent.typing/cost.updated` 의 `{session_id}` |

### 1.3 데몬 번들 (`contracts/protocol.go`, `contracts/daemon-protocol.md`)

| 자리 | 파일:줄 | 판정 |
|---|---|---|
| `BundleTask.SessionID` | `protocol.go:180` | **방** → `room_id` + `work_id?` 추가(R0 이 명시) |
| `BundleTask.AllowedCommands` | `protocol.go:192` | **그대로** — 값 집합만 늘어난다 |
| §4.1 claim 조건 "이 런타임에 **고정된 세션**" | `daemon-protocol.md:65` | **방**(FR-2.3 `runtime_id` 는 방 설정) |
| §4.3 `gc {session_id, workdirs}` · `rebind_prepare {session_id,…}` | `:139-140` | **방** |
| §4.3 rebind 경로 `<workdir_root>/.colab/rebind/<session_id>/` | `:140` | **방** — 디스크 경로가 바뀐다(이관 시 기존 디렉터리 이름 유지 가능, id 가 같으므로) |
| §4.4 유효 예산 `min(task 상한, 세션 잔여)` | `:5`(v0.7.1) | **분기 → 3층** `min(방 잔여, 일 잔여, task 상한)`(FR-2A.3) |
| §6 workdir 보고 `session_id` 필수 | `:216-221` | **방** |
| §4.5 테스트 채팅 `session_id` 빈 문자열 | `:189` | **그대로** |
| `COLAB_SESSION_ID` env | `daemon/internal/harness/acp/env.go:57`, `toolwrap.go:40` | **방** → `COLAB_ROOM_ID` + `COLAB_WORK_ID`(빈 값 허용) |
| 브리프 §8.4 `[4] Session` | `queue/bundle.go:248` | **분기** — `[4] Room: 설명` + `[4b] Work: goal/criteria/종료조건/Director` (PRD R3 은 "[1]" 이라 적었다 — 오기, §6 NN1) |

### 1.4 CLI 명령 (`contracts/colab-cli.md` v0.6)

| 명령 | 판정 |
|---|---|
| `session get` · `session messages` | **방** → `room get`·`room messages`, 옛 이름은 별칭(FR-4.1 이 이미 그렇게 적었다) |
| `message post` · `status set` · `decision record` · `lane delegate` · `artifact submit/get` · `review approve/reject` | **방**(경로의 `{S}` 가 방 id) — 본문 무변경 |
| `hitl ask` · `approve-request` · `request-info` | **분기** — 일 안이면 Director, 일 밖이면 방장(FR-2A.1). CLI 표면은 그대로, 서버 해소만 바뀐다 |
| **신규** `room list` · `room read` · `work propose` | FR-4.5 · FR-2A.1 3행 |
| §2.5 역할별 표 | **그대로 + 2행** — `room_list`·`room_read` 는 전 역할 ✓(읽기), `work_propose` 는 lead·custom(에이전트가 스스로 열지 못한다 → 제안만) |
| `COLAB_SESSION_ID` 기본값 | `cli/internal/client/client.go:44`, `ops.go:33` | **방** |
| MCP 툴 이름 | `colab-cli.md §3` — `colab_room_list`·`colab_room_read` 추가 |

### 1.5 웹 라우트 (`web/`)

| 자리 | 판정 |
|---|---|
| `app/(app)/sessions/page.tsx`(185줄, S5) | **방** → `/rooms` 목록. 카드가 goal·진행률 대신 **마지막 활동·진행 중인 일 N** |
| `app/(app)/sessions/new/page.tsx`(570줄, 7단계 마법사) | **삭제**(FR-2.1 "마법사는 없앤다") → 이름 한 칸 모달. R2 의 가장 큰 삭제 |
| `app/(app)/sessions/[id]/page.tsx`(843줄, S7) | **분기** — 방 화면(메시지·lane·참여자) + 상단 「진행 중인 일」 칩 + 일 패널 |
| `components/SessionAside.tsx` · `SessionActions.tsx` · `PausedBanner.tsx` · `ConditionEditor.tsx`/`ConditionRow.tsx` · `FixConditionDialog.tsx` | **일** |
| `components/LaneBoard/LaneCard` · `Composer` · `MessageCard` · `ActivityFeed` · `HitlCard` | **방** |
| `SessionCardMenu.tsx` · `DeleteSessionDialog.tsx` | **분기** — 방 삭제(FR-2.6, 진행 중 일 있으면 거부) ≠ 일 삭제(FR-2A.6) |
| `lib/wording.ts`(287줄) · `lib/mock/wording.ts`(324줄) | **분기** — "세션" 521 자리 |
| `lib/session-label.ts` · `lib/completion.ts` | 일 |
| `lib/mock/handlers.ts`·`store.ts`(5504줄 mock) | 방+일 둘 다 |
| **신규** | 방 설정 탭 · 참여자 초대(사람·에이전트 한 표) · 「이걸 일로」 · 다른 방 읽기 흔적 행 · 방 목록 정렬·검색(§12.1-4) |

### 1.6 골든 표 · e2e

| 자리 | 판정 | 비고 |
|---|---|---|
| `router/golden_test.go`·`loop_golden_test.go`·`rules_test.go`(E1·E2·E4) | **방** | 순수 함수 — 입력 이름만 바뀐다. 값 무변경 |
| `sessions/completion_golden_test.go`·`summary_golden_test.go`·`budget_golden_test.go`(E6·E9) | **일** | `budget_golden` 은 **3층 min 으로 행이 늘어난다** |
| `tasks/state_golden_test.go`·`resume_golden_test.go`·`cancel_golden_test.go`(E8·E10) | **방**(lane·runtime_session_ref 는 방 단위) | |
| `hitl/hitl_golden_test.go`(E7) | **분기** | 수신자 Director↔방장 행 추가 |
| `workdirs/gc_golden_test.go`(E13) | **방** + **기준점 재정의** | §4 위험 1 |
| `runtimes/offline_golden_test.go`(E14) | **방** | rebind 는 방 |
| `daemon/internal/{workdir,brief}`·`worktreesim` 골든 | **방** | 브랜치 이름 `colab/<room>/<agent>` |
| `web/lib/mock/p3-golden.test.ts`·`p4-golden.test.ts` | 방+일 | |
| e2e p1(13) p2(14) p3(23) p4(11) p5(28) | **전부 손댄다** | 세션 생성이 모든 스크립트의 1단계. CI 10 스크립트(`e2e/p5/ci.sh:20`)가 판정선 |

### 1.7 판단이 갈리는 자리 — 요청된 9건

| # | 자리 | 코드 | 판정과 근거 |
|---|---|---|---|
| 1 | **FR-3 라우팅 `session_hop`·`chain_depth`** | `router/service.go:515-521`(LIMIT 200) · `loop.go:111-128` | **방.** §3.1 표 1행이 못 박았다. 단 두 가지가 딸려 온다: (a) 방이 길어지면 `LIMIT 200` 창 안에 사람 hop 이 없을 수 있고, 그러면 `chainDepth` 가 **0 을 돌려 깊이 상한이 꺼진다**(§4 위험 3). (b) `hops_per_hour 60` 은 방 단위로 여러 일을 합산하므로 **실질 상한이 일당 20** 으로 줄어든다 — 방 설정으로 올릴 수 있어야 한다 |
| 2 | **FR-6 합류 `delegated_from_task_id`** | `router/status.go:173-273` | **방.** 합류 그룹 키는 task id 라 단위와 무관하게 성립한다. 옮길 것 없음. 다만 `maybeFireJoin` → `wake` 가 만드는 task 는 **일을 모른다** — 위임자 lane 의 `work_id` 를 물려받게 해야 요약·비용이 맞는다 |
| 3 | **FR-7 재시도** | `tasks/fallback.go`·`resume.go`·`gate.go` | **방.** `lane.runtime_session_ref` 가 재개의 유일한 근거이고 lane 은 방의 것(§7). `fallback.go:112` 의 인박스 수신자가 `session.director_user_id` 라 **일 밖 task 의 재시도 실패 알림이 갈 곳이 없다** → 방장으로 |
| 4 | **예산 강제(min 3층)** | `sessions/budget.go:99-140`(`TaskCeiling`/`EffectiveTaskLimit`) · `queue/bundle.go:314-332`(`sessionRemainingBudget`) · `httpapi/budget.go` | 지금은 **2층**(task, 세션). **3층으로 늘린다**: `min(방 잔여, 일 잔여, task 상한(override 우선))`. `EffectiveTaskLimit(limit, override, sessionRemaining)` 에 인자 하나를 더하는 것이 전부이고 순수 함수라 골든이 바로 잡는다. **주의**: `BudgetOutcome.Scope` 가 `"task"\|"session"` 2값 — `"room"\|"work"\|"task"` 3값이 되고, 문장("…의 예산을 넘었습니다")이 셋으로 갈라진다(자물쇠 대상) |
| 5 | **workdir 유니크 키 `(session_id, path)`** | `0009:28` · `workdirs/workdirs.go:155-196` | **방.** 기계적. 그러나 두 가지가 딸려 온다: (a) 브랜치 이름 `colab/<slug>/<agent>`(`workdirs/gc.go:148`, `daemon/internal/workdir/worktree.go:60`)가 **방 수명 내내 하나** — 일이 바뀌어도 같은 브랜치에 커밋이 쌓인다. 시나리오 B 의 "병합은 Director 가"가 일 단위로 성립하지 않는다. (b) **GC 기준점 소실** — §4 위험 1 |
| 6 | **SSE 구독 범위** | `realtime/realtime.go:60-98`(`Subscribe(ws, sessions[])`) · `openapi:2919-2924` | **방.** 기계적. 다만 `Event.SessionID` 한 칸으로는 "이 일만 보기"가 안 된다 — S7 이 일 칩으로 필터하려면 페이로드에 `work_id` 를 넣거나 클라이언트가 걸러야 한다. **후자 권장**(구독 키를 둘로 만들면 백필 쿼리가 두 배) |
| 7 | **인박스 항목** | `inbox/inbox.go:11-23`(8종) · `0001:444` | **분기.** `hitl_request`·`lane_blocked`·`run_failed`·`mention`·`workdir_gc_blocked` → 방 / `session_completed`·`session_paused` → **일**(이름도 `work_*` 로) / `runtime_offline` → 방. 카드의 동작 `open_session` 은 `open_room`+`open_work` 둘로 |
| 8 | **§11 지표 10개** | `metrics/metrics.go:140-161`, SQL `:192-330` | **분기.** 1(첫 완료까지 시간, `sqlF1:198`) · 2(자동 완료율, `:206`) · 5(병렬 단축, `:227-233`) · 10(주간 활성 세션) → **일**이 분모. 3·4·6·7·8·9 → task/hitl 기반이라 그대로. **PRD §11 은 v0.19 에서 한 글자도 안 바뀌었다** — G9 가 못 박은 10개의 분모를 말로 정해야 한다(§6 NN5) |
| 9 | **관찰 5행** | `observations/observations.go:157-270` | **방.** 데이터 소스(`session_hop`·`lane.delegated_from_task_id`·`task_event`)가 전부 방 단위다. 단 "트리거 사슬 깊이 = **세션이 도달한 최대 chain_depth**"(§11)가 방 단위가 되면 **오래 산 방의 전 기간 최댓값**이 되어 해석이 달라진다 — "일당 최대"로 바꾸는 것이 맞다 |

---

## 2. 이관(마이그레이션) 설계 초안

### 2.1 핵심 선택 — `session` 표를 **개명**한다 (복사하지 않는다)

`session_id` 열을 가진 자식 표가 14개, 외래키 11개다. Postgres 의 `ALTER TABLE … RENAME TO` 는 **OID 를 유지**하므로 외래키·인덱스·CHECK 가 전부 따라온다. 행을 새 표로 복사하면 그 11개를 전부 다시 걸어야 하고, 이관 중 어느 한 자식이라도 빠지면 조용히 고아가 된다.

> **그래서 이관은 "행 이동"이 아니라 "열 이동"이다.** 방은 제자리에 남고, goal 쪽 열만 새 `work` 로 나간다. `§12.1-3` 의 "session_id 이름은 유지" 결정과 정확히 같은 선택이며, 이 선택을 하는 한 **자식 표의 데이터는 한 행도 움직이지 않는다.**

### 2.2 `0025_v19_rooms.sql` — SQL 수준 개요

```sql
-- (1) 개명. FK·인덱스·CHECK 가 따라온다. 열 이름 session_id 는 그대로(§12.1-3).
ALTER TABLE session              RENAME TO room;
ALTER TABLE session_participant  RENAME TO room_participant;
ALTER TABLE session_context      RENAME TO room_context;
ALTER TABLE session_hop          RENAME TO room_hop;
ALTER TABLE session_subscription RENAME TO room_subscription;

-- (2) 방 고유 칸
CREATE TYPE room_status AS ENUM ('active','archived');
ALTER TABLE room
  ADD COLUMN name                     text,
  ADD COLUMN description              text NOT NULL DEFAULT '',
  ADD COLUMN owner_user_id            uuid REFERENCES app_user(id),
  ADD COLUMN default_director_user_id uuid REFERENCES app_user(id),
  ADD COLUMN rstatus                  room_status NOT NULL DEFAULT 'active';
UPDATE room SET name  = title,                                  -- §10 이관 규칙
                description = split_part(goal, E'\n', 1),
                owner_user_id = created_by,
                default_director_user_id = director_user_id,
                rstatus = CASE WHEN status IN ('completed','cancelled')
                               THEN 'archived' ELSE 'active' END;
ALTER TABLE room ALTER COLUMN name SET NOT NULL,
                 ALTER COLUMN owner_user_id SET NOT NULL;

-- (3) 일. status 는 session_status ENUM 을 그대로 쓴다(값 집합이 일의 것).
CREATE TABLE work (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  room_id uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
  title text NOT NULL, goal text NOT NULL,
  acceptance_criteria text[] NOT NULL DEFAULT '{}',
  director_user_id uuid NOT NULL REFERENCES app_user(id),
  deputy_director_user_id uuid REFERENCES app_user(id),
  assignee_agent_id uuid REFERENCES agent(id),
  completion_condition jsonb NOT NULL,
  completion_met jsonb NOT NULL DEFAULT '{}',      -- 0006:70 과 같은 모양
  limits jsonb NOT NULL DEFAULT '{}', autonomy autonomy_level,
  context_reuse_override jsonb,
  status session_status NOT NULL DEFAULT 'draft',
  paused_reason pause_reason, paused_detail jsonb,
  cost_usd numeric(12,4) NOT NULL DEFAULT 0,
  created_by uuid NOT NULL REFERENCES app_user(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz, finished_at timestamptz,
  CHECK ((status = 'paused') = (paused_reason IS NOT NULL))
);
INSERT INTO work (room_id, title, goal, acceptance_criteria, director_user_id,
                  deputy_director_user_id, assignee_agent_id, completion_condition,
                  completion_met, limits, autonomy, context_reuse_override,
                  status, paused_reason, paused_detail, cost_usd, created_by,
                  created_at, updated_at, started_at, finished_at)
SELECT id, title, goal, acceptance_criteria, director_user_id, deputy_director_user_id,
       assignee_agent_id, completion_condition, completion_met, limits, autonomy,
       context_reuse_override, status, paused_reason, paused_detail, cost_usd,
       created_by, created_at, updated_at, started_at, finished_at
FROM room;                                                     -- 세션 1 → 일 1
CREATE INDEX work_room_status ON work (room_id, status);

-- (4) 자식에 work_id (nullable — 일 밖 실행이 있다)
ALTER TABLE message      ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE lane         ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE task         ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE hitl_request ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE artifact     ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE decision     ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE inbox_item   ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
-- 이관분은 전부 그 방의 유일한 일에 붙는다
UPDATE message c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;  -- 6개 표 반복
CREATE INDEX lane_work ON lane (work_id) WHERE work_id IS NOT NULL;
CREATE INDEX task_work ON task (work_id) WHERE work_id IS NOT NULL;

-- (5) 참여자 — 사람·에이전트 한 표 (PRD §7 "같은 코드가 처리해야 갈라지지 않는다")
ALTER TABLE room_participant
  ALTER COLUMN agent_id   DROP NOT NULL,
  ALTER COLUMN profile_id DROP NOT NULL,
  ADD COLUMN user_id uuid REFERENCES app_user(id),
  ADD COLUMN left_at timestamptz,
  ADD CONSTRAINT room_participant_one_side CHECK ((agent_id IS NULL) <> (user_id IS NULL));
ALTER TABLE room_participant DROP CONSTRAINT session_participant_pkey;
ALTER TABLE room_participant ADD COLUMN id uuid PRIMARY KEY DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX room_participant_agent ON room_participant (session_id, agent_id) WHERE agent_id IS NOT NULL;
CREATE UNIQUE INDEX room_participant_user  ON room_participant (session_id, user_id)  WHERE user_id  IS NOT NULL;
-- 사람 시드: Director · deputy · 생성자 (방 멤버가 0명이면 초대 화면이 빈다)
INSERT INTO room_participant (session_id, user_id, joined_at)
SELECT r.id, u, min(r.created_at) FROM room r,
     LATERAL (VALUES (r.director_user_id),(r.deputy_director_user_id),(r.created_by)) v(u)
WHERE u IS NOT NULL GROUP BY r.id, u;

-- (6) 참고 방 링크 (FR-4.5)
CREATE TABLE room_link (
  room_id uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
  target_room_id uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
  created_by uuid NOT NULL REFERENCES app_user(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (room_id, target_room_id), CHECK (room_id <> target_room_id));

-- (7) 다른 방 읽기 기록 (FR-4.5 "양쪽 방에 남는다") — activity_log 두 줄로 충분하다.
--     별도 표를 만들지 않는다: 조회는 항상 "이 방에서 있었던 일" 이고 그게 activity_log 다.
```

`0026_v19_room_cleanup.sql`(R1 말미, 읽는 자리가 전부 옮겨간 뒤):

```sql
ALTER TABLE room DROP COLUMN title, DROP COLUMN goal, DROP COLUMN acceptance_criteria,
  DROP COLUMN director_user_id, DROP COLUMN deputy_director_user_id, DROP COLUMN assignee_agent_id,
  DROP COLUMN completion_condition, DROP COLUMN completion_met, DROP COLUMN status,
  DROP COLUMN paused_reason, DROP COLUMN paused_detail, DROP COLUMN cost_usd,
  DROP COLUMN started_at, DROP COLUMN finished_at;
ALTER TABLE room DROP CONSTRAINT session_paused_detail_check;   -- 0006:22, 드롭한 열을 가리킨다
ALTER TABLE room DROP CONSTRAINT session_status_check;          -- 0001:200 (status)=(paused_reason)
ALTER TABLE room RENAME COLUMN rstatus TO status;
-- work 쪽에 같은 두 CHECK 를 건다(위 (3) 에 이미 하나, paused_detail 것은 여기서)
ALTER TABLE work ADD CONSTRAINT work_paused_detail_check
  CHECK (paused_detail IS NULL OR paused_reason IS NOT NULL);
```

### 2.3 `session_id` 를 방 id 로 읽는 선택의 대가

| 대가 | 크기 | 완화 |
|---|---|---|
| **외래키** | 없음 — 개명으로 전부 따라온다 | — |
| **인덱스** | 없음 — 이름만 옛 것(`lane_session_status` 등 12개) | R4 에서 같이 개명(`ALTER INDEX`, 순간) |
| **계약 문장** | 큼. `Message.session_id`·`Lane.session_id`·`Task.session_id`·`HitlRequest.session_id`·`Artifact.session_id`·`Decision.session_id`·`InboxItem.session_id`·`Participant.session_id`·`Workdir.session_id`·`TaskToken.session_id` 10곳이 **"session 이라 쓰고 room 을 뜻한다"** 가 된다 | **권장: 계약은 즉시 `room_id` 로 바꾸고 `session_id` 를 한 판 중복으로 남긴다.** DB 열 이름만 유지한다. 그러면 "이름과 뜻이 다른" 구간이 서버 SQL 안에만 있고 계약·CLI·웹·데몬은 처음부터 바른 말을 쓴다 |
| **`COLAB_SESSION_ID`** | 사용자 머신에 설치된 CLI·래퍼가 읽는다 | 데몬이 **둘 다** export(`COLAB_ROOM_ID` 신규, `COLAB_SESSION_ID` 유지) — R3~R4 |
| **골든/테스트 이름** | `*_session_*` 함수·픽스처 수백 | 기계적 치환. 자물쇠(§4 위험 4)가 잡는다 |

### 2.4 되돌릴 수 없는 지점

| # | 지점 | 언제부터 되돌릴 수 없나 |
|---|---|---|
| 1 | `0026` 의 `DROP COLUMN` | 즉시. **전환 전 `pg_dump` 가 유일한 복구 수단**(§10 명시) |
| 2 | 한 방에 **두 번째 일**이 열리는 순간 | 방→세션 역변환의 기준이 사라진다(§3.1 명시). R2 배포 시점 |
| 3 | `room_participant` 에 **사람 행**이 들어간 순간 | PK 교체를 되돌리면 사람 행이 갈 곳이 없다 |
| 4 | `session*` 별칭 제거(R4) | **사용자 머신의 CLI 가 낡아 있으면 그 자리에서 죽는다.** `install.sh` 가 서버 커밋을 고정하지만(S-64) 이미 설치된 것은 갱신되지 않는다 → 별칭 제거 전에 "낡은 경로 호출 0" 을 접근 로그로 확인해야 한다 |
| 5 | 워크트리 브랜치 `colab/<room>/<agent>` 로 커밋이 쌓이기 시작 | 일 단위로 브랜치를 나누려면 그때는 손으로 쪼개야 한다 |

### 2.5 검증 스크립트 (행 수 대조) 안

`server/scripts/verify_0025.sql` — 이관 직후 **전부 0 이어야 한다**:

```sql
\set ON_ERROR_STOP on
-- A. 방 수 = 옛 세션 수, 일 수 = 방 수  (전환 전 덤프의 숫자를 :pre_sessions 로 넘긴다)
SELECT 'rooms<>pre'     AS chk, count(*) - :pre_sessions FROM room
UNION ALL SELECT 'works<>rooms',  (SELECT count(*) FROM work) - (SELECT count(*) FROM room)
-- B. 자식 표 행 수 불변 (덤프 숫자와 대조)
UNION ALL SELECT 'messages<>pre', (SELECT count(*) FROM message) - :pre_messages
UNION ALL SELECT 'lanes<>pre',    (SELECT count(*) FROM lane)    - :pre_lanes
UNION ALL SELECT 'tasks<>pre',    (SELECT count(*) FROM task)    - :pre_tasks
-- C. 이관분에 work_id 구멍이 없다 (신규 일 밖 실행이 생기기 전에만 유효)
UNION ALL SELECT 'msg work_id null',  (SELECT count(*) FROM message      WHERE work_id IS NULL)
UNION ALL SELECT 'lane work_id null', (SELECT count(*) FROM lane         WHERE work_id IS NULL)
UNION ALL SELECT 'task work_id null', (SELECT count(*) FROM task         WHERE work_id IS NULL)
UNION ALL SELECT 'hitl work_id null', (SELECT count(*) FROM hitl_request WHERE work_id IS NULL)
UNION ALL SELECT 'art  work_id null', (SELECT count(*) FROM artifact     WHERE work_id IS NULL)
UNION ALL SELECT 'dec  work_id null', (SELECT count(*) FROM decision     WHERE work_id IS NULL)
-- D. 고아 없음: 모든 자식의 session_id 가 살아 있는 방을 가리킨다
UNION ALL SELECT 'orphan work', (SELECT count(*) FROM work w LEFT JOIN room r ON r.id=w.room_id WHERE r.id IS NULL)
-- E. 값 보존: 일의 goal/상태/비용이 방에서 그대로 왔다
UNION ALL SELECT 'goal drift', (SELECT count(*) FROM work w JOIN room r ON r.id=w.room_id
                                 WHERE w.goal IS DISTINCT FROM r.goal
                                    OR w.status IS DISTINCT FROM r.status
                                    OR w.cost_usd IS DISTINCT FROM r.cost_usd)
-- F. 방 이름 빈칸 0, 참여자 양쪽 NULL 0
UNION ALL SELECT 'room name empty', (SELECT count(*) FROM room WHERE name = '')
UNION ALL SELECT 'participant both', (SELECT count(*) FROM room_participant
                                       WHERE (agent_id IS NULL) = (user_id IS NULL))
-- G. 완료 세션은 보관된 방이 됐다 (§10 이관 규칙)
UNION ALL SELECT 'completed not archived',
  (SELECT count(*) FROM room r JOIN work w ON w.room_id=r.id
    WHERE w.status IN ('completed','cancelled') AND r.rstatus <> 'archived');
```

실행 절차: `pg_dump -Fc` → 위 숫자 채취 → `migrate up` → `verify_0025.sql` → 모두 0 이면 커밋, 하나라도 아니면 덤프 복원. **e2e 에 `87_migrate_0025.sh` 로 넣어 매 CI 에서 돈다**(빈 DB + 시드 세션 3개 → 검증 스크립트 0행).

---

## 3. R0~R4 검증

### 3.1 순서는 맞다 — 다만 R1 이 너무 크다

"계약 → 서버 → 화면 → 에이전트 표면 → 이관" 은 v1 에서 검증된 순서이고 그대로 쓸 수 있다. 문제는 **R1 한 단계에 (a) 스키마 이관 (b) 라우팅·상한·HITL 단위 이동 (c) FR-4.5 권한·기록 세 가지**가 들어 있다는 것이다. 이 세 가지는 서로 독립이고, (a) 가 되돌릴 수 없는 유일한 단계다. **R1 을 R1a/R1b/R1c 로 쪼갠다**(§5).

### 3.2 각 단계가 혼자서 초록일 수 있는가

| 단계 | 혼자 초록? | 근거 / 조건 |
|---|---|---|
| **R0 계약** | ✅ | lint + 생성물 재생성. 데몬 번들의 `room_id` 는 `omitempty` 라 낡은 데몬이 무시한다. **조건**: `session*` op 을 별칭으로 남기고 `ColabCommand` enum 을 **이때** 늘린다(R3 이 아니다 — enum 은 계약이고, `server/internal/roles/roles_test.go` 가 `colab-cli.md §2.5` 표를 파싱해 대조하므로 표와 enum 은 같은 PR 에서만 초록이다) |
| **R1a 스키마** | ✅ | `0025` 는 열만 더한다. 읽는 코드는 `room` 의 옛 열을 그대로 읽으므로 **한 줄도 안 고치고 초록**. 호환 뷰 `CREATE VIEW session AS SELECT … FROM room` 은 **쓰기가 안 되므로**(29개 write 자리) 뷰 대신 `ALTER TABLE … RENAME` + 서버 SQL 의 `session` → `room` 일괄 치환이 맞다. 치환은 기계적이고 같은 PR 에서 끝난다 |
| **R1b 단위 이동** | ⚠️ **조건부** | 여기서 기존 e2e 가 깨진다. 깨지는 이유는 두 가지고 **성격이 다르다**: ① **모양**(응답 JSON 이 `Room`+`Work` 로 갈라진다) — 별칭 op 이 `room ⋈ work` 를 옛 `Session` 모양으로 합성해 주면 막힌다(한 방에 일이 하나뿐인 R1 에서는 1:1 이라 가능). ② **문장**(문구 자물쇠가 "세션" 을 글자 단위로 못 박았다: `server/internal/wording/wording_test.go` 624줄, `web/lib/wording.ts` 287줄, `web/lib/mock/server-wording/*.test.ts` 5개) — 이건 별칭으로 못 막는다. **그래서 R1 과 R2 사이에 문구 전환 라운드가 필요하다**(§3.3 누락 ①) |
| **R1c FR-4.5** | ✅ | 새 op 두 개 + 권한 함수. 부르는 곳이 없어도(CLI 는 R3) 유닛·e2e 로 판정 가능 |
| **R2 화면** | ✅ | 웹 단독. 목(`web/lib/mock/`)이 서버와 독립이라 서버가 먼저 머지돼 있으면 된다 |
| **R3 에이전트 표면** | ✅ | CLI·데몬. `COLAB_SESSION_ID` 를 유지한 채 `COLAB_ROOM_ID` 를 더하면 낡은 래퍼도 산다 |
| **R4 이관·정리** | ⚠️ | "실사용 워크스페이스에서 데이터 손실 0" 은 **R1a 가 이미 한 일**이다(§6 NN6). R4 의 진짜 일은 별칭 제거이고, 그건 §2.4-4 의 조건(낡은 CLI 호출 0)을 먼저 확인해야 한다 |

### 3.3 빠진 단계

| # | 빠진 것 | 왜 필요한가 | 어디에 넣나 |
|---|---|---|---|
| ① | **문구 전환 라운드** (서버 문장 · 웹 문구 · 목 문구 · 자물쇠 어휘표) | "세션" 이 사용자에게 보이는 자리 = 서버 156(리터럴 95) + 웹 521. 자물쇠가 셋을 글자 단위로 묶어 놨다(`§8.4` 표, `a-server-table.test.ts`~`e-session-started.test.ts`). R1b 와 R2 어느 쪽에 넣어도 그 PR 이 두 배가 된다 | **R1.5** — 독립 PR 3개(서버·웹·목), T-S13/T-W10 과 같은 모양 |
| ② | **데몬 번들 `room_id`·`work_id` 소비** | R0 이 **계약에 넣는 것**까지만 말한다. 데몬이 읽어 `COLAB_ROOM_ID` 로 내보내고 브리프에 싣는 것은 별개 작업 | **R3** 에 명시 |
| ③ | **브리프 [4] 재구성** | R3 이 "브리프 [1]" 이라 적었지만 §8.4 의 [1] 은 Agent Identity 다. 방 설명 + 그 턴의 일은 **[4]** 자리(`queue/bundle.go:248`). 그리고 [1]~[5] 는 **바이트 동일 규칙**(E12-11, 캐시 프리픽스)이 걸려 있어 "그 턴의 일"을 [4] 에 넣으면 **턴마다 프리픽스가 달라진다** — 일이 바뀔 때만 달라지므로 실무상 괜찮지만, 규칙을 명시적으로 고쳐야 한다 | **R0**(계약 문장) + **R3**(구현) |
| ④ | **MCP 툴 2개 + 역할표 2행** | `colab_room_list`·`colab_room_read`. `roles.AllowedCommands`(`server/internal/roles/roles.go:22`)의 `all` 배열과 `denied` 맵, 데몬의 `--allow` 목록, 웹 `RoleCommands.tsx` 가 같은 표를 읽는다 | **R0**(enum·표) + **R3**(툴) + **R2**(화면 행) |
| ⑤ | **인박스 항목 재분류** | `inbox_item_type` 8종 중 2종이 일로 간다. 카드 동작 `open_session` → `open_room`/`open_work`. 계약 enum·웹 `InboxItemCard.tsx`·서버 `inbox.go` 세 곳 | **R0**(enum) + **R1b** + **R2** |
| ⑥ | **SSE 이벤트 표 전환** | 26종 중 6종이 이름·페이로드를 바꾼다. `STREAM_EVENT_TYPES` 에 없으면 웹이 **조용히 버린다**(T-W13 교훈) | **R0** + **R2** |
| ⑦ | **대시보드 지표 분모 결정** | §11 10개 중 4개(1·2·5·10)의 분모가 일이다. PRD §11 은 v0.19 에서 안 바뀌었다 | **R0 전에 Director 결정** — §12.1 에 5번 항목으로 |
| ⑧ | **이관 검증 스크립트 + e2e** | §10 위험 표가 "스크립트로 대조" 라 했지만 R0~R4 표에 자리가 없다 | **R1a** 의 DoD |
| ⑨ | **골든 표 태그 해제 CI** | `p3golden`·`p4golden` 뒤에 12개 파일이 있어 `go test ./...` 가 **컴파일조차 안 한다**. R1 이 초록인 채로 골든이 썩는다 | **R1a** 의 DoD(§4 위험 4) |

---

## 4. 위험 top 5

### 위험 1 — **작업 폴더 GC 가 영구히 멈춘다** (확실, 조용함)

- **무엇이 깨지나.** `workdirs/sweep.go:69-76` 의 `retain_until` 은 `session.finished_at` 과 `session.status IN ('completed','cancelled')` 에서 나온다. **방은 완료되지 않는다**(FR-2.4). workdir 은 방×에이전트(FR-6.1)라 일에 매달 수도 없다. 결과: 이관 직후 **모든 workdir 의 `retain_until` 이 NULL 로 고정**되고, `workdir_gc` 인덱스(`WHERE status='retained'`)에 아무것도 안 들어오며, 워크트리가 무한히 쌓인다. 남는 브레이크는 `workdir_disk_quota_gb` 하나뿐인데 그건 **초과 시 경고**이지 삭제가 아니다.
- **어떻게 먼저 잡나.** ① `workdirs/gc_golden_test.go`(E13, `p4golden` 태그)에 **"방은 보관되지 않았고 일이 끝났다"** 행을 R1a 전에 먼저 쓴다 — 기대값을 Director 가 고른다(후보: 방 보관 시 / 마지막 사용 후 N일 유휴 시 / 그 에이전트의 모든 일이 끝난 뒤 N일). ② `e2e/p4/64_gc.sh` 에 "일 완료 + 방 active" 시나리오 한 줄. ③ 스키마 가드: `0025` 에 `workdir.retain_until` 을 채우는 경로가 남아 있는지 확인하는 유닛(sweep 의 UPDATE 가 0행을 돌려주면 실패).

### 위험 2 — **방 단위 상한을 강제할 상태가 없다** (설계 구멍)

- **무엇이 깨지나.** FR-2A.3 이 "방의 상한을 넘기면 **그 방의 모든 일과 일 밖 task 가 멈춘다**" 고 한다. 그런데 FR-2.4 의 방 상태는 `active ⇄ archived` 뿐이다. 강제 지점은 claim 쿼리의 단 한 줄 — `queue/postgres.go:101 AND s.status = 'active'` — 이고 `tasks/gate.go:41 PlanDispatch(sessionState, pauseReason, …)` 가 그 위에 얹혀 있다. 방에 `paused` 가 없으면 이 게이트가 갈 곳이 없어, **방 예산이 바닥나도 dispatch 가 계속된다**(= pause 가 막으려던 지출이 그대로 난다). 같은 구멍이 FR-3.5 루프 상한에도 있다: 루프는 방 단위로 세는데(`router/service.go:653 pauseForLoop`) 멈추는 것은 일이라, 방에 일이 3개면 **어느 일을 멈출지 PRD 가 말하지 않는다**.
- **어떻게 먼저 잡나.** ① §12.1 에 Director 결정 항목을 세운다 — 권장안: **방에 `paused_reason`·`paused_detail` 은 두되 `status` 는 안 늘린다**(`room.blocked_reason` 같은 별도 칸). 그러면 `PlanDispatch` 가 `(roomBlocked, workState, pauseReason)` 3인자가 되고 `active|archived` 불변식이 산다. ② `sessions/budget_golden_test.go`(E9)에 **"방 잔여 0, 일 잔여 충분"**, **"일 잔여 0, 방 잔여 충분"**, **"일 밖 task + 방 잔여 0"** 3행을 R1b 전에 쓴다. ③ `router/loop_golden_test.go` 에 "일 2개 있는 방에서 루프 상한" 행.

### 위험 3 — **오래 사는 방에서 깊이 상한이 조용히 꺼진다** (실측 가능)

- **무엇이 깨지나.** `router/service.go:515-521` 이 hop 을 `ORDER BY id DESC LIMIT 200` 으로 읽고, `loop.go` 의 `chainDepth` 는 **창 안에 사람 hop 이 하나도 없으면 0 을 돌려준다**(loop.go:126-128 의 명시적 선택). 세션은 짧아서 200 hop 안에 사람이 반드시 있었다. **방은 몇 주를 산다** — 에이전트 hop 200개가 연속으로 쌓이면 `max_chain_depth=8` 가 꺼지고 `hops_per_hour=60` 만 남는다. 반대 방향 위험도 있다: `hops_per_hour` 를 방 단위로 합산하면 일이 3개 도는 방에서 **일당 실질 상한이 20** 이 되어 정상 위임이 막힌다.
- **어떻게 먼저 잡나.** ① `router/loop_golden_test.go` 에 **"창 밖에만 사람 hop"** 행 하나(입력만 200+ 로 만들면 순수 함수가 바로 잡는다). ② `loadHops` 를 `LIMIT 200` 에서 **"마지막 사람 hop 이후 전부 + 그 hop"** 으로 바꾸는 쿼리 — 인덱스 `room_hop_session(session_id, id)` 가 이미 있다. ③ `hops_per_hour` 를 방 설정으로 노출(FR-2.3 `limits` 에 이미 자리가 있다).

### 위험 4 — **골든 표가 빌드 태그 뒤에서 조용히 썩는다** (이미 존재, 전환이 증폭)

- **무엇이 깨지나.** `//go:build p3golden` 4개 + `p4golden` 8개 = **12개 파일이 `go test ./...` 에서 컴파일조차 되지 않는다**(E7·E8·E9·E10·E13·E14 전부). R1 이 `session` → `room`/`work` 로 바꾸면 이 파일들은 **컴파일 에러인 채로 CI 초록**이 된다. 발견은 다음 골든 라운드(몇 주 뒤)다. 같은 구조가 `web/lib/mock/p3-golden.test.ts`·`p4-golden.test.ts` 에도 있다(그쪽은 태그가 없어 도는 대신, 목이 구현과 **같은 오답을 공유**하면 못 잡는다 — T-C5 교훈).
- **어떻게 먼저 잡나.** ① R1a 의 DoD 를 `go vet -tags p3golden ./... && go vet -tags p4golden ./... && go build -tags p3golden,p4golden ./...` 로 고정하고 **CI 에 올린다**(이 한 줄이 12개 파일을 감시한다). ② R0 에 "골든 태그 전수" 표 한 장 — 어떤 태그가 어떤 EVAL 행을 덮는가. ③ 웹 목은 **서버 소스를 직접 대조**하는 기존 패턴(`server-wording/a-server-table.test.ts`)을 방·일 어휘에도 적용.

### 위험 5 — **FR-4.5 권한의 입력이 합류 경로에서 비어 있다** (확실, 조용함)

- **무엇이 깨지나.** FR-4.5 규칙 1 은 "그 task 를 일으킨 **사람 originator** 가 그 방의 참여자" 다. 입력은 `task.originator_user_id`. 그런데 전파가 **두 경로에만** 있다: 메시지 게시(`router/service.go:206-208, 316`)와 위임(`router/delegate.go:174-181`). **`router/status.go:371` 의 `wake()`** — 합류 통보·blocked 질문 기상·재진입 통보로 만드는 task — 는 `originator_user_id` 를 **아예 INSERT 하지 않는다**(NULL). 즉 **합류로 깨어난 Lead, 질문에 답하러 깨어난 위임자는 originator 가 없어 모든 방 읽기가 403** 이 된다. 하필 맥락이 가장 필요한 자리다. 같은 NULL 이 `tasks/fallback.go` 의 프로파일 폴백 재시도에도 있다.
- **어떻게 먼저 잡나.** ① 유닛 한 줄: `wake()` 로 만든 task 의 `originator_user_id` 가 **깨우는 쪽 task 의 것과 같은가**(`status.go` 는 이미 `delegTask` 를 알고 있어 한 번의 SELECT 로 끝난다). ② FR-4.5 골든에 "originator NULL" 행 — 기대값을 정한다(권장: **방장을 originator 로 대체하지 말 것** — 권한이 올라간다. 대신 전파를 고치고, 그래도 NULL 이면 403 + 사람이 읽는 사유). ③ e2e: 위임 → 합류 → 합류로 깨어난 Lead 가 `colab room read` (1턴).

---

## 5. 규모 추정

기준선(비슷한 과거 라운드의 실측): PR #246 서버 관찰+명령 **27파일 1,988+/22−** · PR #260 서버 2라운드 11건 **41파일 1,747+/127−** · PR #259 웹 2라운드 **90파일 1,299+/457−** · PR #253 e2e **24파일 599+/48−** · PR #263 계약+서버 한 건 **9파일 641+/15−**.

| 단계 | PR | 규모 | 변경 파일 | 병렬 |
|---|---|---|---|---|
| **R0 계약** | 1 (Director 승인) | **대** | `openapi.yaml`(+~700줄: 새 op 9 · 스키마 분리 · enum 2값 · SSE 표) · `daemon-protocol.md` · `colab-cli.md` · `harness.md` · `protocol.go` · `openapi.md` · 생성물 2(`api.gen.go` 8,949줄 · `schema.d.ts`) ≈ **12파일 / +1,400** | ✗ (단독 선행) |
| **R1a 스키마·개명** | 1 | **대** | `0025`·`0026`·`verify_0025.sql`·`embed.go` + 서버 SQL 122접점 ≈ **35파일 / +900 −400** | ✗ (R1b·R1c 의 전제) |
| **R1b 단위 이동** | **3** (T-S: 라우팅·상한 / 완료·요약·HITL / 인박스·SSE·지표) | 각 **중** | 각 ≈ 15~20파일 / +600. 합 ≈ **50파일 / +1,800** | **◐ 부분 병렬** — 라우팅과 완료는 `router/`↔`sessions/` 로 갈리지만 `tasks/gate.go`·`queue/postgres.go` 를 공유한다. 첫 PR 이 게이트를 잡고 나머지 둘 병렬 |
| **R1c FR-4.5** | 1 | **중** | 새 패키지 `rooms/`(권한·읽기·기록) + `httpapi` 2핸들러 + `activity_log` 2줄 + 골든 ≈ **14파일 / +700** | ✅ R1b 와 병렬 |
| **R1.5 문구** | **3** (서버·웹·목) | 서버 **중** / 웹 **대** / 목 **소** | 서버 ≈ 20파일(자물쇠 어휘표 포함) · 웹 ≈ 60파일(`세션` 521자리) · 목 ≈ 6파일 ≈ **86파일 / +900 −900** | ✅ 셋 병렬(자물쇠 어휘표만 먼저 합의) |
| **R2 화면** | **3** (방 목록·방 화면 / 일 패널·열기·종료 / 설정·초대·다른 방 흔적) | 각 **대** | 마법사 570줄 삭제 · S7 843줄 재구성 · 새 컴포넌트 ~10 · 목 5,504줄 갱신 ≈ **110파일 / +2,600 −1,400** | **◐** — 첫 PR 이 라우트·레이아웃을 잡고 나머지 둘 병렬 |
| **R3 에이전트 표면** | **2** (CLI+MCP / 데몬) | 각 **중** | CLI: 새 명령 3 · `--allow` 2값 · env ≈ 12파일 / +500. 데몬: 번들 소비 · env · 브리프 · 래퍼 ≈ 14파일 / +400 | ✅ 병렬(계약이 R0 에 있으므로) |
| **R4 이관·정리** | 2 (별칭 제거 / 문서) | **중** + **소** | 별칭 제거 ≈ 20파일 / −800. 문서(PRD·SCREEN·COMPONENTS·PLAN·EVAL) ≈ 8파일 | ✗ (마지막) |
| **e2e** | **5** (단계마다 1) | 각 **중** | p1~p5 87개 스크립트가 전부 세션 생성으로 시작 ≈ **90파일 / +1,200 −600** | ◐ 각 단계 뒤에 붙는다 |

**합계 ≈ 21 PR · 약 430 파일 · +10,000/−3,300 줄.** 직렬 최소 경로는 **R0 → R1a → (R1b·R1c 병렬) → R1.5 → R2 → R3 → R4** 로 **7 게이트**. 이전 라운드 속도(V11 이 PR #246~#265, 20 PR)와 같은 규모이고, 가장 무거운 단일 PR 은 **R1a**(되돌릴 수 없어서)와 **R2 첫 PR**(마법사 삭제)이다.

---

## 6. PRD 에 고칠 문장 제안

아래는 **그대로 붙여 넣을 수정문**이다. 위치는 PR #269 `PRD.md` 기준.

### NN1 — §10 v2.0 표 R3 행 (브리프 번호 오기)

> **지금**: `R3 에이전트 표면 | colab room * 명령·MCP 툴, 브리프 [1] 에 방 설명 + 그 턴의 일, 역할별 허용 명령에 room_read 편입 | …`
>
> **고칠 것**:
> `| R3 에이전트 표면 | colab room * 명령·MCP 툴(colab_room_list·colab_room_read), 브리프 **[4]** 를 「Room: 이름·설명」 + 「Work: goal·criteria·종료 조건·Director」 두 구간으로(§8.4 의 [1] 은 Agent Identity 다), 번들 room_id·work_id 소비와 COLAB_ROOM_ID·COLAB_WORK_ID export | 실기 1턴에서 다른 방 읽기가 권한대로 되고 기록이 남는다 |`
>
> 덧붙여 §8.4 브리프 구성 블록의 `[4] Session: …` 줄을:
> `[4] Room: 이름 + 설명  /  Work(그 턴이 속한 일이 있으면): goal / acceptance_criteria / 종료 조건 / Director 이름 / 격리 방식 — 일 밖 턴에서는 Work 줄을 통째로 비운다`
>
> **왜**: §8.4 가 [1] 을 Agent Identity 로 못 박았고 구현도 그렇다(`server/internal/queue/bundle.go:230, 248`). 그리고 [1]~[5] 의 **바이트 동일 규칙**(E12-11, 캐시 프리픽스)이 [4] 에 걸려 있으므로 "그 턴의 일"이 들어가면 일이 바뀔 때 프리픽스가 달라진다 — 그 사실을 §8.4 에 한 줄 적어야 한다.

### NN2 — FR-2.4 방 상태 (방 단위 상한을 강제할 자리)

> **덧붙일 것** (FR-2.4 끝):
> `방이 자기 상한(FR-2A.3)을 넘기면 상태는 그대로 active 이고, 방에 blocked_reason(budget|time)·blocked_detail 이 서고 그 방의 모든 일과 일 밖 task 의 dispatch 가 멈춘다. 방장에게 승인 HITL 이 가고, 승인이 blocked_reason 을 지운다. 방에 paused 상태를 두지 않는 이유: active ⇄ archived 는 사람이 보는 방의 수명이고, 지출 정지는 수명이 아니라 게이트이기 때문이다.`
>
> **왜**: FR-2A.3 이 요구하는 "방의 모든 일이 멈춘다" 를 강제할 자리가 FR-2.4 에 없다. 구현의 게이트는 `queue/postgres.go:101` 한 줄이고, 방 상태가 `active|archived` 뿐이면 그 줄이 갈 곳이 없다. **§12.1 에 Director 결정 항목으로 올려도 된다** — 다만 R0 계약(`Room` 스키마)이 이 칸을 알아야 하므로 R0 전에 정해야 한다.

### NN3 — FR-3.5 / §3.1 표 (루프 상한이 방 단위가 될 때)

> **덧붙일 것** (§3.1 "FR 이 매달리는 단위" 표의 첫 행 근거 칸 뒤):
> `방은 오래 살므로 두 가지를 같이 정한다. ① 깊이(max_chain_depth)는 마지막 사람 hop 이후만 센다 — 조회 창을 "마지막 사람 hop 이후 전부" 로 하고, 창 안에 사람 hop 이 없다고 해서 깊이 판정을 끄지 않는다. ② 시간당 hop 상한(max_hops_per_hour)은 방 전체 합이므로 방 설정(FR-2.3 limits)으로 올릴 수 있어야 한다 — 일이 여럿 도는 방에서는 워크스페이스 기본값 60 이 일당 20 으로 줄어든다.`
>
> **왜**: `router/service.go:515-521` 이 hop 을 200행 창으로 읽고 `loop.go:126-128` 이 "창 안에 사람 hop 이 없으면 깊이 0" 을 **의도적으로** 선택했다. 세션에서는 안전했지만 방에서는 상한이 조용히 꺼진다.

### NN4 — FR-2A.5 / FR-4.3 (아티팩트 이름의 범위)

> **덧붙일 것** (FR-2A.5 끝):
> `아티팩트 이름의 유일성·버전은 방 단위다(같은 이름을 다시 내면 일이 달라도 v2 가 된다). 일마다 따로 세지 않는 이유: 산출물은 방의 것이고(FR-2A.6 의 메시지와 같은 이유), 워크트리 격리에서 기본 이름이 <agent>.diff 라 일을 바꿔도 같은 브랜치의 다음 판이기 때문이다. 일별로 나누고 싶으면 이름에 일을 적는다.`
>
> **왜**: `artifact` 의 UNIQUE 가 `(session_id, name, version)`(`0001:425`)이고 이관 뒤 그건 **방 단위**가 된다. E16-B(같은 이름 재제출 = version+1)가 이 전제 위에 있어, 명시하지 않으면 R1 에서 "일마다 v1 부터" 로 바꾸는 구현이 나온다.

### NN5 — §11 (지표의 분모)

> **덧붙일 것** (§11 표 바로 아래):
> `[v0.19] 위 10개 중 네 개의 분모는 **일**이다 — 「데몬 설치 → 첫 **일** 완료까지 시간」, 「**일** 자동 완료 비율」, 「병렬 lane 사용 **일**의 wall-clock 단축」, 「주간 활성 **방** / 활성 워크스페이스」(마지막 것만 방이다: 사람이 계속 쓰는가를 보는 지표라서). 나머지 여섯은 task·HITL·attempt 기반이라 단위 전환과 무관하다. 관찰 표의 「트리거 사슬 깊이」는 방의 전 기간 최댓값이 아니라 **일마다의 최댓값**을 분포로 본다 — 방은 끝이 없어 전 기간 최댓값은 계속 커지기만 한다.`
>
> **왜**: §11 은 v0.19 에서 한 글자도 안 바뀌었는데 지표 1·2·5·10 의 SQL 이 전부 `FROM session WHERE status='completed'`(`metrics/metrics.go:198, 206, 227`) 다. G9 가 못 박은 10개라 분모를 말로 정해 두지 않으면 R1b 가 임의로 고른다.

### NN6 — §10 v2.0 표 R1·R4 (이관이 일어나는 단계)

> **지금**: R1 "…이관 마이그레이션…", R4 "옛 세션 → 방+일 변환 실측, session* 별칭 제거, 문서 정리 | 실사용 워크스페이스에서 데이터 손실 0"
>
> **고칠 것**:
> `| R1 서버 | 방·일 테이블(0025~)과 **이관 마이그레이션 + 검증 스크립트**(행 수 대조, e2e 에 포함), 라우팅·상한·HITL 의 단위 이동, FR-4.5 읽기 권한·기록 | 기존 e2e 전부 초록(세션 = 방+일 하나로 읽힌 채) + 새 e2e(방 2개·일 2개·다른 방 읽기) + **빈 DB→시드→이관→검증 0행** |`
> `| R4 정리 | **실사용 워크스페이스에 R1 이관 적용**(전환 전 pg_dump → 적용 → 검증 스크립트), session* 별칭 제거, 문서 정리 | 데이터 손실 0 + **낡은 경로(/sessions/*) 호출 0 을 접근 로그로 확인한 뒤** 별칭 제거 |`
>
> 그리고 **이관(R4) 규칙** 문단의 머리를 `**이관(R1 이 만들고 R4 가 적용한다) 규칙**` 으로.
>
> **왜**: 변환 SQL 은 R1 의 마이그레이션이고 R4 는 그것을 실데이터에 돌리는 단계다. 지금 문장은 R4 가 변환을 "만든다"고 읽혀 R1 이 스키마만 만들고 이관을 미루게 된다. 별칭 제거의 전제(설치된 CLI)도 적어야 한다 — `install.sh` 는 서버 커밋을 고정하지만 이미 설치된 CLI 는 갱신되지 않는다.

### NN7 — FR-4.5 권한 (originator 가 없을 때)

> **덧붙일 것** (FR-4.5 "권한" 항목 2번 뒤):
> `originator 가 없는 task(합류 통보·질문 기상·재진입 통보·폴백 재시도로 깨어난 턴)는 자기를 깨운 task 의 originator 를 물려받는다. 그래도 없으면 다른 방을 읽을 수 없고(403), 에이전트에게는 "이 턴은 사람의 요청에서 시작하지 않아 다른 방을 읽을 수 없습니다" 가 간다. 방장으로 대체하지 않는다 — 그러면 에이전트를 거쳐 권한이 오른다.`
>
> **왜**: 입력인 `task.originator_user_id` 가 **합류 경로에서 NULL** 이다(`router/status.go:371` 의 INSERT 에 그 열이 없다). 명시하지 않으면 "originator 없으면 방장" 이라는 편한 구현이 나오고, 그게 바로 FR-4.5 가 막으려던 권한 상승이다.

### NN8 — FR-6.1 / FR-6.4 (브랜치 이름과 작업 폴더 정리)

> **덧붙일 것** (FR-2.3 `isolation` 행 뒤 또는 FR-6.1):
> `worktree 격리의 브랜치는 colab/<방 slug>/<에이전트 slug> 이고 방이 사는 동안 하나다 — 일이 바뀌어도 같은 브랜치의 다음 커밋이다. 병합 단위는 브랜치가 아니라 일이 낸 diff 아티팩트다(FR-4.3). 작업 폴더의 보존 기한(FR-6.4)은 방의 끝이 아니라 **그 방×에이전트가 마지막으로 쓰인 시각 + workdir_retention_days** 로 센다 — 방은 끝나지 않으므로 세션 완료를 기준점으로 쓸 수 없다.`
>
> **왜**: `workdirs/sweep.go:69-76` 의 `retain_until` 이 `session.finished_at` 에서 나온다. 방이 완료되지 않으면 이 UPDATE 가 영원히 0행이고 GC 가 멈춘다(§4 위험 1). `workdir.last_used_at` 열은 이미 있다.

### NN9 — §12.1 (열린 결정에 두 항목 추가)

> **덧붙일 것**:
> `5. **방 단위 상한을 무엇이 강제하는가** — 방에 paused 상태를 두지 않는다면(NN2) 게이트가 될 칸을 정해야 한다. 지금 제안은 room.blocked_reason. R0 계약의 Room 스키마가 이 칸을 알아야 하므로 R0 전에 정한다.`
> `6. **§11 지표 10개의 분모**(NN5) — 1·2·5 는 일, 10 은 방으로 제안. G9 가 못 박은 표라 바꾸려면 근거가 남아야 한다.`

---

## 부록 — 세는 데 쓴 명령

```
grep -rn 'FROM session\b'   server --include='*.go' | grep -v _test | wc -l   # 55
grep -rn 'JOIN session\b'   server --include='*.go' | grep -v _test | wc -l   # 38
grep -rn 'UPDATE session'   server --include='*.go' | grep -v _test | wc -l   # 22
grep -rn 'INSERT INTO session' server --include='*.go' | grep -v _test | wc -l #  7
grep -rn '세션' server/internal --include='*.go' | grep -v _test | wc -l        # 156 (리터럴 95)
grep -rn '세션' web --include='*.tsx' --include='*.ts' | grep -v node_modules | wc -l  # 521
grep -rh '//go:build' --include='*_test.go' server daemon | sort | uniq -c     # p3golden 4 · p4golden 8
grep -n 'operationId:' contracts/openapi.yaml | wc -l                          # 97
```
