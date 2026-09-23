/**
 * v0.19 「나머지 화면」의 말(PRD §10 R2 M6, T-R2-W4a) — S9 에이전트 목록 · S11 연결된 컴퓨터 · S13 작업 폴더 · S14 설정(방 기본값·다른 방 읽기·
 * 알림 구독 3층) · S17 컴퓨터 바꾸기. 화면은 이 표를 그리기만 한다 — 문구 자물쇠(`lib/wording.test.ts`)가 옛말·내부 용어를 잰다.
 *
 * **새 문구만** 둔다. 옛 「세션」 문구의 방 전환은 R1.5(문구 자물쇠 + 서버 Go 리터럴 한 PR)의 몫이다 — 여기서 옛 문장을 고치면 그 PR 과 충돌한다.
 */
import type { components } from "@/lib/api/schema";

type S = components["schemas"];
export type RoomSubscriptionLevel = S["RoomSubscriptionLevel"];
export type SubscriptionLevel = S["SubscriptionLevel"];

// ── S9 에이전트 목록(§4.15) ──────────────────────────────────────────────────
export const AGENT_ROOMS = {
  /** 「참여 중인 방 N」 — 누르면 펼친다. */
  count: (n: number) => `참여 중인 방 ${n}`,
  none: "참여 중인 방 없음",
  /** 볼 수 없는 방은 개수에만 — 이름을 보이면 invited 방의 존재가 샌다(§2.4), 개수를 속이면 "왜 3인데 2개만" 이 된다. */
  hidden: (n: number) => `+ 볼 수 없는 방 ${n}`,
  /** 방을 가로지르는 전역 상한(§12.1-5) — 한 방 사람이 "내 방 에이전트가 왜 느린가" 를 여기서만 안다. */
  concurrent: (max: number, used: number) => `동시 ${max}개 중 ${used}개 사용 중`,
  list_label: "참여 중인 방 목록",
  filter_room: "참여 방 필터",
  filter_room_all: "참여 방 전체",
} as const;

// ── S11 연결된 컴퓨터 · S13 작업 폴더 · S17 컴퓨터 바꾸기(§4.16) ─────────────────
export const COMPUTER_ROOMS = {
  /** 유예를 넘겨 멈춘 방 수(FR-9.2 의 단위가 방이다). */
  stopped: (n: number) => `이 컴퓨터에 묶인 방 ${n}개가 멈췄습니다`,
  bound: (n: number) => `묶인 방 ${n}개`,
  /** S13 열 이름 — 작업 폴더는 방×에이전트 단위다(FR-6.1). */
  using_room: "쓰는 중인 방",
  /** S17 `?room=` — 여러 방이 한 컴퓨터에 걸렸으면 방마다 따로 결정한다. */
  rebind_for_room: (name: string) => `${name} 방의 컴퓨터를 바꿉니다`,
} as const;

// ── S14 설정(§4.17) ──────────────────────────────────────────────────────────
export const ROOM_DEFAULTS_TAB = {
  label: "방 기본값",
  desc: "새로 만드는 방에 걸리는 격리·공개 범위·자율성·한도",
  /** 탭 머리 — 방 만들기가 이름 한 칸이 되면서 이 값이 모든 방의 실제 값이 된다(FR-2.1). */
  head: "이 값이 새로 만드는 모든 방의 기본값입니다",
  isolation: "격리 방식",
  isolation_none_note: "격리 없음이면 에이전트들이 같은 폴더를 함께 고칩니다",
  visibility: "공개 범위",
  autonomy: "자율성",
  budget: "방 예산 상한 (USD)",
  max_works: "동시에 열 수 있는 미션",
  max_lanes: "동시에 도는 서브 미션",
  none: "없음",
  impact: {
    isolation: "새 방의 첫 실행 전까지만 바꿀 수 있습니다 — 이미 돈 방의 격리는 그대로입니다",
    visibility: "「초대된 사람만」이면 초대받지 않은 멤버에게는 방이 목록에도 검색에도 나오지 않습니다",
    autonomy: "새 방의 에이전트가 사람 확인을 얼마나 자주 받는지 정합니다",
    budget: "넘으면 그 방 전체가 멈추고 방장에게 계속할지 묻습니다 — 비우면 상한 없음",
    max_works: "넘으면 새 미션을 열 수 없고 열린 미션 목록이 뜹니다",
    max_lanes: "넘는 서브 미션은 줄을 서서 기다립니다",
  },
} as const;
/** 공개 범위 이름 — 방 화면 우열(`ROOM_PANEL.visibility`)과 같은 말. */
export const VISIBILITY_LABEL: Record<S["RoomVisibility"], string> = { workspace: "워크스페이스 전체", invited: "초대된 사람만" };
export const ROOM_DEFAULTS_DEFAULTS = { visibility: "workspace", isolation_kind: "none", autonomy: "guided", max_concurrent_works: 3, max_parallel_lanes: 5 } as const;

/** 컨텍스트 탭 — 다른 방 읽기 상한(FR-4.5). 「컨텍스트 재사용 상한」(FR-4.4)은 버렸다(§12.1-7) — 이 탭에서 뺀다. */
export const ROOM_READ = {
  tab_desc: "에이전트가 다른 방의 대화를 한 턴에 얼마나 읽을지",
  head: "다른 방 읽기",
  max_rooms: "한 턴에 읽을 수 있는 방",
  max_tokens: "읽어 오는 최대 토큰",
  impact_rooms: "낮추면 에이전트가 참고 방 여럿을 한 번에 보지 못하고 나눠 읽습니다",
  impact_tokens: "높이면 읽은 쪽 방의 턴 비용이 오릅니다 — 넘는 분량은 요약으로 잘립니다",
} as const;
export const ROOM_READ_DEFAULTS = { max_rooms_per_turn: 3, max_tokens: 4000 } as const;

/** 알림 구독 3층(FR-8 · §4.17 표) — 방 · 미션 · 서브 미션. 안 읽음 배지는 방에 하나뿐이다(§12.1-6). */
export const SUBSCRIPTIONS = {
  head: "구독 단위",
  desc: "방 · 미션 · 서브 미션 셋으로 나눠 받습니다. 미션 설정은 방 설정을 덮어씁니다. 안 읽음 표시는 방에 하나뿐입니다.",
  room_pick: "방 고르기",
  room_pick_none: "방을 고르세요",
  room_level: "방",
  work_level: "미션",
  lane_level: "서브 미션",
  follow_room: "방 설정을 따름",
  lane_on: "켜기",
  lane_off: "끄기",
  lane_follow: "따로 정하지 않음",
  no_works: "열린 미션이 없습니다",
  no_lanes: "서브 미션이 없습니다",
  saved: "저장됨",
} as const;
export const ROOM_SUB_LABEL: Record<RoomSubscriptionLevel, string> = { all: "전부", my_works: "내가 참여한 미션만", hitl_only: "사람 확인만", off: "끄기" };
export const WORK_SUB_LABEL: Record<SubscriptionLevel, string> = { all: "전부", hitl_only: "사람 확인만", completion_only: "종료만" };

/** 한 에이전트 카드의 방 줄 — 볼 수 있는 방 목록 + 못 보는 방 수. 합계는 둘의 합이다(개수를 속이지 않는다). */
export function agentRoomsView(a: { rooms?: { id: string; name: string }[]; room_count?: number; hidden_room_count?: number }) {
  const rooms = a.rooms ?? [];
  const hidden = a.hidden_room_count ?? 0;
  const visible = Math.max(a.room_count ?? rooms.length, rooms.length);
  return { rooms, hidden, total: visible + hidden };
}
