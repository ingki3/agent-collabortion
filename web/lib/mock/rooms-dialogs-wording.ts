/**
 * 목이 흉내 내는 **서버 문장** — 방의 다이얼로그·설정 op(T-R2-W3). 규칙은 `./wording.ts` 의 `SERVER` 와 같다: 문장은 여기서만,
 * `server-wording/j-room-dialogs.test.ts` 가 각 항목을 `server/<at>` 소스와 **글자 단위로** 대조한다.
 *
 * `./wording.ts` 와 나눈 이유: 같은 시기에 S7 재작성(T-R2-W2)이 `SERVER` 끝에 행을 붙인다 — 한 표 끝을 두 PR 이 늘리면 충돌한다.
 * 이미 `SERVER` 에 있는 문장(`room_archived` · `room_owner_required` …)은 거기서 가져다 쓴다 — 두 벌로 적지 않는다.
 * `%d` 자리는 앞부분까지(`text`)와 뒷부분(`tail`)을 따로 적고 목이 수를 끼운다.
 */
import type { ServerSentence } from "./wording";

export const RD_SERVER = {
  // ── 참여자 (internal/httpapi/handlers_room_participants.go) ──
  one_of: { text: "사람이나 에이전트 중 하나만 골라 주세요", at: "internal/httpapi/handlers_room_participants.go" },
  invite_not_member: { text: "워크스페이스 멤버만 초대할 수 있습니다", at: "internal/httpapi/handlers_room_participants.go" },
  already_person: { text: "이미 이 방에 있는 사람입니다", at: "internal/httpapi/handlers_room_participants.go" },
  already_agent: { text: "이미 참여 중인 에이전트입니다", at: "internal/httpapi/handlers_room_participants.go" },
  agent_not_here: { text: "이 워크스페이스의 에이전트가 아닙니다", at: "internal/httpapi/handlers_room_participants.go" },
  profile_not_of_agent: { text: "이 에이전트의 프로파일이 아닙니다", at: "internal/httpapi/handlers_room_participants.go" },
  profile_agent_only: { text: "프로파일은 에이전트에게만 있습니다", at: "internal/httpapi/handlers_room_participants.go" },
  is_owner: { text: "방장은 먼저 다른 참여자에게 방장을 넘긴 뒤 나갈 수 있습니다", at: "internal/httpapi/handlers_room_participants.go" },
  is_director: { text: "이 방에서 진행 중인 미션의 Director 입니다 — 먼저 Director 를 넘겨 주세요", at: "internal/httpapi/handlers_room_participants.go" },
  not_participant_of_target: { text: "참여 중인 방만 참고 방으로 연결할 수 있습니다", at: "internal/httpapi/handlers_room_participants.go" },
  link_self: { text: "자기 방은 참고 방으로 연결할 수 없습니다", at: "internal/httpapi/handlers_room_participants.go" },
  already_linked: { text: "이미 참고 방으로 연결되어 있습니다", at: "internal/httpapi/handlers_room_participants.go" },
  // 에이전트 초대 권한(FR-1.9) — `tasks.MayTrigger` 가 누른 사람에 대해 판정한다.
  trig_nobody: { text: "이 에이전트는 응답 대상이 「아무도 아님」이라 초대할 수 없습니다", at: "internal/tasks/cancel.go" },
  trig_allowlist: { text: "이 에이전트의 허용 목록에 없는 사람입니다", at: "internal/tasks/cancel.go" },
  trig_owner: { text: "이 에이전트는 만든 사람만 초대할 수 있습니다", at: "internal/tasks/cancel.go" },
  // 시스템 메시지 조각 — 이름을 앞뒤에 이어 붙인다(서버와 같은 이음).
  sys_invited_mid: { text: " 님이 ", at: "internal/httpapi/handlers_room_participants.go" },
  sys_invited_tail: { text: " 님을 방에 초대했습니다.", at: "internal/httpapi/handlers_room_participants.go" },
  // 「… 방에 참여했습니다.」 는 옛 참여자 추가와 글자까지 같아졌다(R1.5) — 한 벌만 둔다: W.participant_joined.
  sys_left: { text: " 방에서 나갔습니다.", at: "internal/httpapi/handlers_room_participants.go" },
  sys_removed: { text: " 방에서 내보냈습니다.", at: "internal/httpapi/handlers_room_participants.go" },
  sys_link_src: { text: " 방을 참고 방으로 연결했습니다 — 이 방의 에이전트가 그 방을 읽을 수 있습니다.", at: "internal/httpapi/handlers_room_participants.go" },
  sys_link_dst_head: { text: " 님이 이 방을 ", at: "internal/httpapi/handlers_room_participants.go" },
  sys_link_dst_tail: { text: " 방의 참고 방으로 연결했습니다.", at: "internal/httpapi/handlers_room_participants.go" },
  sys_unlink_src_head: { text: " 님이 참고 방 ", at: "internal/httpapi/handlers_room_participants.go" },
  sys_unlink_src_tail: { text: " 연결을 풀었습니다.", at: "internal/httpapi/handlers_room_participants.go" },
  sys_unlink_dst_tail: { text: " 방에서 이 방으로의 참고 연결을 풀었습니다.", at: "internal/httpapi/handlers_room_participants.go" },

  // ── 방 권한 (internal/rooms/authz.go) — 미션 열기·제안의 403 ──
  open_not_participant: { text: "이 방의 참여자만 할 수 있습니다 — 방장에게 초대를 요청해 주세요", at: "internal/rooms/authz.go" },

  // ── 방 설정 (internal/httpapi/handlers_rooms.go) ──
  supervised_unsupported: { text: "감독 모드는 아직 지원하지 않습니다", at: "internal/httpapi/handlers_rooms.go" },
  repo_required: { text: "워크트리 격리에는 그 컴퓨터의 저장소 경로가 필요합니다", at: "internal/httpapi/handlers_rooms.go" },
  container_unsupported: { text: "컨테이너 격리는 아직 지원하지 않습니다", at: "internal/httpapi/handlers_rooms.go" },
  min_1: { text: "1 이상이어야 합니다", at: "internal/httpapi/handlers_rooms.go" },
  min_0: { text: "0 이상이어야 합니다", at: "internal/httpapi/handlers_rooms.go" },
  runtime_pinned: { text: "이 방은 이미 첫 실행을 시작해 컴퓨터와 격리 방식을 바꿀 수 없습니다 — 작업 폴더가 그 컴퓨터에 묶여 있습니다", at: "internal/httpapi/handlers_rooms.go" },
  runtime_not_in_workspace: { text: "이 워크스페이스에 연결된 컴퓨터가 아닙니다", at: "internal/httpapi/handlers_rooms.go" },
  default_director_not_member: { text: "워크스페이스 멤버가 아닙니다", at: "internal/httpapi/handlers_rooms.go" },
  deputy_is_owner: { text: "방장은 부방장을 겸할 수 없습니다", at: "internal/httpapi/handlers_rooms.go" },
  seat_not_participant: { text: "이 방의 참여자에게만 맡길 수 있습니다 — 먼저 방에 초대해 주세요", at: "internal/httpapi/handlers_rooms.go" },
  sys_settings: { text: " 님이 방 설정을 바꿨습니다.", at: "internal/httpapi/handlers_rooms.go" },
  // FR-2.1.2 — 「〈사람〉 님이 방 이름을 〈옛〉에서 〈새〉(으)로 바꿨습니다.」 조각(조사는 josaRo).
  sys_renamed_mid: { text: " 님이 방 이름을 ", at: "internal/httpapi/handlers_rooms.go" },
  sys_renamed_from: { text: "에서 ", at: "internal/httpapi/handlers_rooms.go" },
  sys_renamed_tail: { text: " 바꿨습니다.", at: "internal/httpapi/handlers_rooms.go" },
  sys_description: { text: " 님이 방 설명을 바꿨습니다.", at: "internal/httpapi/handlers_rooms.go" },
  sys_vis_invited: { text: " 님이 이 방을 초대된 사람만 볼 수 있게 바꿨습니다.", at: "internal/httpapi/handlers_rooms.go" },
  sys_vis_workspace: { text: " 님이 이 방을 워크스페이스 멤버 모두가 볼 수 있게 바꿨습니다.", at: "internal/httpapi/handlers_rooms.go" },
  sys_owner_mid: { text: " 님이 방장을 ", at: "internal/httpapi/handlers_rooms.go" },
  sys_owner_tail: { text: " 님에게 넘겼습니다.", at: "internal/httpapi/handlers_rooms.go" },
  sys_deputy_cleared: { text: " 님이 부방장 자리를 비웠습니다.", at: "internal/httpapi/handlers_rooms.go" },
  sys_deputy_set: { text: " 님을 부방장으로 정했습니다.", at: "internal/httpapi/handlers_rooms.go" },

  // ── 미션 열기 (internal/httpapi/handlers_works.go) ──
  goal_required: { text: "목표를 입력해 주세요", at: "internal/httpapi/handlers_works.go" },
  title_200: { text: "제목은 200자까지 쓸 수 있습니다", at: "internal/httpapi/handlers_works.go" },
  work_budget_min: { text: "예산은 0 이상이어야 합니다", at: "internal/httpapi/handlers_works.go" },
  work_time_invalid: { text: "시간 상한은 PT4H 처럼 적어 주세요", at: "internal/httpapi/handlers_works.go" },
  room_blocked_open: { text: "이 방은 멈춰 있습니다 — 방을 다시 움직인 뒤 미션을 열어 주세요", at: "internal/httpapi/handlers_works.go" },
  director_not_member: { text: "Director 와 deputy 는 워크스페이스 멤버여야 합니다", at: "internal/httpapi/handlers_works.go" },
  assignee_not_participant: { text: "제출자는 이 방의 참여자 중에서 골라야 합니다", at: "internal/httpapi/handlers_works.go" },
  max_concurrent: { text: "이 방에서 동시에 열 수 있는 미션은 ", at: "internal/httpapi/handlers_works.go" },
  max_concurrent_tail: { text: "개입니다 — 진행 중인 미션을 끝내거나 취소한 뒤 열어 주세요", at: "internal/httpapi/handlers_works.go" },
  message_has_work: { text: "이미 다른 미션에 속한 메시지입니다 — 그 미션에서 이어 가세요", at: "internal/httpapi/handlers_works.go" },
  sys_work_opened: { text: " 님이 미션을 열었습니다. 목표: ", at: "internal/httpapi/handlers_works.go" },

  // ── 미션 제안 (internal/httpapi/handlers_work_proposals.go) ──
  already_resolved: { text: "이미 처리된 제안입니다", at: "internal/httpapi/handlers_work_proposals.go" },
  action_enum: { text: "열기(accept) 또는 거절(reject) 중 하나를 골라 주세요", at: "internal/httpapi/handlers_work_proposals.go" },
  sys_rejected_mid: { text: " 의 미션 제안 「", at: "internal/httpapi/handlers_work_proposals.go" },
  sys_rejected_tail: { text: "」을 거절했습니다.", at: "internal/httpapi/handlers_work_proposals.go" },
  sys_reject_reason: { text: " 사유: ", at: "internal/httpapi/handlers_work_proposals.go" },

  // ── 맥락 읽기 기록 (internal/httpapi/handlers_rooms_read.go) ──
  read_direction: { text: "읽음 · 읽힘 · 거부 중 하나를 골라 주세요", at: "internal/httpapi/handlers_rooms_read.go" },
  read_room_not_found: { text: "방을 찾을 수 없습니다", at: "internal/httpapi/handlers_rooms_read.go" },
} as const satisfies Record<string, ServerSentence>;

export type RdKey = keyof typeof RD_SERVER;
export const RW: { readonly [K in RdKey]: (typeof RD_SERVER)[K]["text"] } = Object.fromEntries(
  Object.entries(RD_SERVER).map(([k, v]) => [k, v.text]),
) as { readonly [K in RdKey]: (typeof RD_SERVER)[K]["text"] };
