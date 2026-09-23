/**
 * S8 받은 요청 v0.19(SCREEN v0.19.2 §4.14, T-R2-W4a) — 순수 규칙과 문구. 화면(`app/(app)/inbox/page.tsx`)·카드(`InboxItemCard`)는
 * 이 표를 그리기만 한다. 문구는 한곳(여기)에서만 나온다 — 문구 자물쇠(`lib/wording.test.ts`)가 옛말·내부 용어를 잰다.
 *
 * 항목이 **방·미션·미션 밖** 세 층으로 갈린다. 카드가 말해야 하는 것은 셋이다:
 *   1) 어느 방 일인가 — 맥락 한 줄(첫 줄 `[심각도] [타입] · 방 이름 · 미션 제목`). **방 이름은 줄이지 않는다**(말줄임은 미션 제목 → 타입 순).
 *   2) 왜 내게 왔나 — 수신자 근거(`recipient_basis`): 「Director 로서」·「방장으로서」…
 *   3) 지금 답할 수 있나 — 위임 시점(FR-2A.3): 부방장·ws owner 는 「14:30부터 답할 수 있습니다」로 먼저 비활성을 보인다.
 */
import type { components } from "@/lib/api/schema";

type S = components["schemas"];
export type InboxItem = S["InboxItem"];
export type RecipientBasis = NonNullable<InboxItem["recipient_basis"]>;
export type BlockedDetail = S["BlockedDetail"];

/** 주격 조사 — 받침이 있으면 「이」, 없으면 「가」, 한글이 아니면 「이(가)」(서버 apperr.Josa 와 같은 규칙). */
export function ga(word: string): string {
  const cp = word.length ? word.codePointAt(word.length - 1)! : 0;
  if (!word.length || cp < 0xac00 || cp > 0xd7a3) return `${word}이(가)`;
  return (cp - 0xac00) % 28 === 0 ? `${word}가` : `${word}이`;
}

// ── 문구 ────────────────────────────────────────────────────────────────────
/** 수신자 근거(§4.14 표) — 층을 함께 적는다: 미션 역할은 영어(Director·deputy), 방 역할은 한국어(방장·부방장). */
export const RECIPIENT_BASIS: Record<RecipientBasis, string> = {
  director: "Director 로서",
  deputy: "deputy 로서",
  room_owner: "방장으로서",
  room_deputy: "부방장으로서",
  workspace_owner: "워크스페이스 소유자로서",
};

export const INBOX_V19 = {
  /** 필터 두 줄(§4.14) — 둘째 줄의 선택 상자. */
  filter_room: "방별 필터",
  filter_work: "미션별 필터",
  all_rooms: "모든 방",
  all_works: "모든 미션",
  no_work: "미션 없음",
  /** 방 이름을 모를 때(계약 0.2.9 `InboxItem.room` 을 서버가 아직 안 채우고 목록에도 없다). 존재를 지어내지 않는다. */
  unknown_room: "(볼 수 없는 방)",
  /** 두 바로가기(PRD §6) — 미션이 없으면 방 하나만. */
  open_room: "방 열기",
  open_work: "미션 열기",
  open_settings: "방 설정 열기",
  open_workdirs: "작업 폴더 열기",
  /** 위임 시점(FR-2A.3) — 비활성 먼저, 그 시각이 지나면 위임됨. */
  from: (hhmm: string) => `${hhmm}부터 답할 수 있습니다`,
  delegated_owner: (owner: string) => `위임됨 · 방장 ${ga(owner)} 아직 답하지 않았습니다`,
  delegated_owner_plain: "위임됨 · 방장이 아직 답하지 않았습니다",
  delegated_director: "위임됨 · 지금부터 응답 가능",
  /** 응답 뒤 한 줄(토스트). */
  room_resumed: "방이 다시 돕니다 — 멈췄던 미션과 대화가 이어집니다",
  isolation_answered: "답을 보냈습니다 — 이 방의 첫 턴이 시작됩니다",
  /** 스크린리더 — 맥락 한 줄의 방 이름. */
  room_aria: (name: string) => `방 ${name}`,
} as const;

/** `isolation_confirm` 카드(§4.14 표 — FR-2.1.1). 본문은 승인 요청이지만 버튼 이름이 결과를 말한다. */
export const ISOLATION_CARD = {
  basis_delegated: (owner: string) => `부방장으로서 — 방장 ${ga(owner)} 기한 절반 동안 답하지 않았습니다`,
  shared: (computer: string) => `${computer}에는 저장소가 있습니다. 에이전트들이 같은 폴더를 함께 고치게 됩니다 — 이 결정은 되돌릴 수 없습니다`,
  shared_plain: "이 컴퓨터에는 저장소가 있습니다. 에이전트들이 같은 폴더를 함께 고치게 됩니다 — 이 결정은 되돌릴 수 없습니다",
  held: "이 방은 아직 첫 턴을 시작하지 않았습니다 — 답할 때까지 에이전트가 일을 시작하지 않습니다",
  proposed: "제안: 워크트리로 나눔",
  split: "워크트리로 나눔",
  keep: "이대로 진행",
  /** 「이대로 진행」은 거절(= `none` 유지)이라 결정 기록에 사유가 필요하다(서버 E6-04) — 사람이 적지 않아도 이 문장이 남는다. */
  keep_reason: "격리 없이 한 폴더를 함께 쓰기로 했습니다",
  /** choice(저장소 고르기, T-S-wt) — 어느 저장소에서 워크트리를 나눌지. */
  choose: "저장소 고르기",
  choose_send: "이 저장소로 나눔",
} as const;

/** `room_paused` 카드(§4.14 — FR-2A.3). 폭발 반경이 방 전체라 action_required 다. */
export const ROOM_PAUSED_CARD = {
  stopped: (n: number) => `이 방의 미션 ${n}개와 대화 전부가 멈췄습니다`,
  stopped_no_works: "이 방의 대화 전부가 멈췄습니다",
  resume: (n: number) => `승인하면 이 방의 미션 ${n}개가 한꺼번에 다시 돕니다`,
  remaining: (usd: string) => ` — 열린 미션 잔여 예산 합계 ${usd}`,
  resume_no_works: "승인하면 이 방의 대화가 다시 돕니다",
  approve: "계속 승인",
  budget_field: "방의 새 예산 상한 (USD)",
  budget_hint: (spent: string) => `지금까지 ${spent} 사용 — 그보다 큰 값을 넣으세요`,
  next: (hhmm: string, role: string, name: string) => `${hhmm}부터 ${role} ${ga(name)} 답할 수 있습니다`,
} as const;

/** `BlockedDetail.next_approver_role` → 배너·카드의 역할명. */
export const NEXT_APPROVER_ROLE: Record<"room_deputy" | "workspace_owner", string> = {
  room_deputy: "부방장",
  workspace_owner: "워크스페이스 소유자",
};

// ── 규칙 ────────────────────────────────────────────────────────────────────
/** 방 층 항목 — 미션이 없고 방장이 답한다. 위임 시점 줄을 이 넷이 쓴다(§4.14 「…는 위임 시점을 적는다」). */
export function needsDelegationLine(item: Pick<InboxItem, "type" | "work_id">): boolean {
  if (item.type === "room_paused" || item.type === "isolation_confirm") return true;
  return (item.type === "hitl_request" || item.type === "lane_blocked") && !item.work_id;
}

/** 위임받은 사람의 근거인가 — 부방장·owner 최고참(방 층), deputy(미션 층). */
export const isDelegatedBasis = (b: InboxItem["recipient_basis"]): boolean =>
  b === "room_deputy" || b === "workspace_owner" || b === "deputy";

/** 방 이름 — 계약 0.2.9 `room.name`, 없으면 방 목록에서 찾은 이름, 그것도 없으면 「(볼 수 없는 방)」. */
export function roomNameOf(item: Pick<InboxItem, "room" | "room_id" | "session_id">, known?: ReadonlyMap<string, string>): string {
  if (item.room?.name) return item.room.name;
  const id = item.room_id ?? item.session_id ?? null;
  return (id && known?.get(id)) || INBOX_V19.unknown_room;
}
export const roomIdOf = (item: Pick<InboxItem, "room" | "room_id" | "session_id">): string | null =>
  item.room?.id ?? item.room_id ?? item.session_id ?? null;

/**
 * 미션 제목 — 서버 `listInbox` 는 `session`(SessionRef) 자리에 **미션**을 싣는다(handlers_inbox.go `wk.title`). 미션이 없는 방 층 항목
 * (`room_paused`·`isolation_confirm`)은 비어 있다. `work_id` 가 없으면 미션 밖 항목이라 제목을 말하지 않는다.
 */
export function workTitleOf(item: Pick<InboxItem, "work_id" | "session">): string | null {
  if (!item.work_id) return null;
  return item.session?.title ?? null;
}

/**
 * 둘째 줄 — 미션 밖 항목의 트리거 메시지 인용(§4.14 「인용을 첫 줄에 넣으면 방 이름이 밀린다」). 한 줄 말줄임은 CSS 가 한다.
 * 미션 안이면 미션 제목이 맥락이라 인용을 따로 내리지 않는다(본문이 이미 말한다).
 */
export function quoteLine(item: Pick<InboxItem, "type" | "work_id" | "card">): string | null {
  if (item.work_id) return null;
  if (item.type !== "mention" && item.type !== "lane_blocked") return null;
  const t = item.card?.body?.trim();
  return t ? `"${t}"` : null;
}

/** 두 바로가기 — 방은 늘, 미션은 `work_id` 가 있을 때만(§4.14 「미션이 없는 항목은 방 바로가기 하나만」). */
export function shortcutsOf(item: Pick<InboxItem, "room" | "room_id" | "session_id" | "work_id">): { room: string | null; work: string | null } {
  const rid = roomIdOf(item);
  if (!rid) return { room: null, work: null };
  return { room: `/rooms/${rid}`, work: item.work_id ? `/rooms/${rid}?work=${item.work_id}` : null };
}

/** 바로가기가 대신하는 동작 — 버튼 줄에서 뺀다(같은 행동이 두 자리에 있으면 사람이 어느 것을 누를지 망설인다). */
export const SHORTCUT_ACTIONS: ReadonlySet<string> = new Set(["open_session", "open_room", "open_work"]);

/**
 * 필터 둘째 줄의 선택지 — 지금 받은 목록에 있는 방·미션만(없는 것을 고르면 빈 화면이 된다). 방 이름은 줄이지 않는다.
 * 미션 선택지는 방을 고르면 그 방 것만.
 */
export function filterOptions(items: readonly InboxItem[], known?: ReadonlyMap<string, string>, roomId?: string) {
  const rooms = new Map<string, string>();
  const works = new Map<string, string>();
  let noWork = false;
  for (const it of items) {
    const rid = roomIdOf(it);
    if (rid) rooms.set(rid, roomNameOf(it, known));
    if (roomId && rid !== roomId) continue;
    if (it.work_id) works.set(it.work_id, workTitleOf(it) ?? it.work_id.slice(0, 8));
    else noWork = true;
  }
  return { rooms: [...rooms.entries()], works: [...works.entries()], noWork };
}

/** 방·미션 거르개(둘째 줄). `work` 가 `"none"` 이면 미션 밖 항목만. */
export function applyScope(items: readonly InboxItem[], room: string, work: string): InboxItem[] {
  return items.filter((it) => {
    if (room && roomIdOf(it) !== room) return false;
    if (work === "none") return !it.work_id;
    if (work && it.work_id !== work) return false;
    return true;
  });
}

/** `$12.40` — 카드의 달러 표기. */
export const usd = (n: number): string => `$${n.toFixed(2)}`;

/**
 * `room_paused` 두 문장(§4.14) — 「미션 N개와 대화 전부가 멈췄습니다」 + 「승인하면 미션 N개가 한꺼번에 다시 돕니다 — 잔여 합계 $X」.
 * 수·금액은 `getRoom` 의 `blocked_detail`(카드당 1회, 드묾 — Lead Q5). 잔여 합계는 0.2.9 `open_works_remaining_usd`, 없으면 말하지 않는다.
 */
export function roomPausedLines(d: Pick<BlockedDetail, "works_stopped" | "open_works_remaining_usd"> | null | undefined): { stopped: string; resume: string } {
  const n = d?.works_stopped ?? 0;
  const rem = d?.open_works_remaining_usd;
  return {
    stopped: n > 0 ? ROOM_PAUSED_CARD.stopped(n) : ROOM_PAUSED_CARD.stopped_no_works,
    resume: n > 0 ? ROOM_PAUSED_CARD.resume(n) + (rem != null ? ROOM_PAUSED_CARD.remaining(usd(rem)) : "") : ROOM_PAUSED_CARD.resume_no_works,
  };
}

/** `HH:MM` — 위임 시각. */
export const hhmm = (iso: string): string => {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

/**
 * 위임 줄(FR-2A.3) — 셋 중 하나:
 *   · 아직 답할 수 없다(`from` 이 미래) → 「14:30부터 답할 수 있습니다」(비활성 먼저)
 *   · 위임받아 답할 수 있다 → 「위임됨 · 방장 〈민호〉가 아직 답하지 않았습니다」(미션 deputy 는 「위임됨 · 지금부터 응답 가능」)
 *   · 위임과 무관(방장·Director 본인) → null
 */
export function delegationLine(
  item: Pick<InboxItem, "recipient_basis" | "delegated">,
  opts: { canRespond: boolean; from?: string | null; ownerName?: string | null; now?: number },
): { text: string; locked: boolean } | null {
  const b = item.recipient_basis ?? null;
  const delegated = isDelegatedBasis(b) || item.delegated === true;
  if (!delegated) return null;
  const now = opts.now ?? Date.now();
  if (!opts.canRespond && opts.from && Date.parse(opts.from) > now) return { text: INBOX_V19.from(hhmm(opts.from)), locked: true };
  if (b === "deputy" || (b == null && item.delegated)) return { text: INBOX_V19.delegated_director, locked: false };
  return { text: opts.ownerName ? INBOX_V19.delegated_owner(opts.ownerName) : INBOX_V19.delegated_owner_plain, locked: false };
}
