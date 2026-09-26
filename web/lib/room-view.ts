/**
 * S7 방 화면 · S22 미션 패널의 **판정** — 순수 함수(SCREEN §4.6 · §4.8 · COMPONENTS §9). 화면(`app/(app)/rooms/[id]/page.tsx`)은 여기서
 * 받은 값을 그린다. 문장은 `lib/wording.ts` 에만 있다 — 여기는 무엇을 보이고 무엇을 세는지만 정한다.
 *
 * 미션 칩은 **거르개이자 선택자**다(§4.6): 고르면 ① 타임라인·보드가 그 미션 것만 남고 ② 우열 미션 칸이 그 미션으로 바뀐다.
 * 그래서 선택(`ChipSel`) 하나에서 두 곳의 값이 모두 나온다 — 연동이 코드 한 곳에서 정해진다.
 */
import type { HitlRequest, Lane, LaneStatus, Message, Room, WorkListItem, WorkStatus } from "@/lib/api/types";

/** 칩 줄의 선택 — `(전체)` · `(미션 없음)` · 미션 하나. */
export type ChipSel = { kind: "all" } | { kind: "none" } | { kind: "work"; id: string };
export const SEL_ALL: ChipSel = { kind: "all" };

/**
 * `?work=` 의 값 → 선택. `:workId` 는 그 칩(S22), `none` 은 `(미션 없음)`. `new`·`from` 은 S21(미션 열기, W3)의 자리라 칩을 바꾸지 않는다.
 */
export function parseSel(param: string | null | undefined): ChipSel {
  if (!param || param === "new" || param === "from") return SEL_ALL;
  if (param === "none") return { kind: "none" };
  return { kind: "work", id: param };
}
export function selParam(sel: ChipSel): string | null {
  return sel.kind === "all" ? null : sel.kind === "none" ? "none" : sel.id;
}
export function sameSel(a: ChipSel, b: ChipSel): boolean {
  return a.kind === b.kind && (a.kind !== "work" || a.id === (b as { id: string }).id);
}

/** 열린 미션 — 칩에 선다. `completed`·`cancelled` 는 「지난 미션 ▾」로 간다(§4.6). */
const OPEN: ReadonlySet<WorkStatus> = new Set<WorkStatus>(["draft", "active", "paused", "completing"]);
export const isOpenWork = (w: Pick<WorkListItem, "status">) => OPEN.has(w.status);

/** 칩의 상태 글리프 — ● 실행 중 / ⏸ 일시정지 / ⏳ 사람 대기(파생: 열린 확인 요청, 상태가 아니다) / ○ 초안. */
export function chipGlyph(w: Pick<WorkListItem, "status" | "waiting_human">): { glyph: string; waiting: boolean } {
  if (w.status === "paused") return { glyph: "⏸︎", waiting: false };
  if (w.waiting_human) return { glyph: "⏳︎", waiting: true };
  if (w.status === "draft") return { glyph: "○", waiting: false };
  if (w.status === "completed") return { glyph: "✓", waiting: false };
  if (w.status === "cancelled") return { glyph: "–", waiting: false };
  return { glyph: "●", waiting: false };
}

export interface ChipRow {
  /** 칩 줄을 그리는가 — 미션이 하나도 없으면(열린 것도 끝난 것도 0) 그리지 않고 「+ 새 미션」만 남긴다. */
  show: boolean;
  /** 보이는 미션 칩(최대 4 — 5개 이상이면 4 + 「미션 N개 ▾」). */
  chips: WorkListItem[];
  /** 접힌 미션 칩. */
  overflow: WorkListItem[];
  /** 「지난 미션 N개 ▾」. */
  past: WorkListItem[];
  /** 「일시정지 N ▾」 — 2개 이상일 때만(§4.6 SCR-A P-2). */
  paused: WorkListItem[];
}

/**
 * 칩 줄(§4.6 · COMPONENTS §9.1). **접기 기준은 미션 칩만 센다**(§8.7 Q8) — 특수 칩·「지난 미션」·「일시정지」·「+ 새 미션」은 세지 않는다.
 * 고른 칩이 접힌 쪽에 있으면 보이는 네 번째 자리와 바꾼다 — 고른 것이 안 보이면 우열이 무엇을 말하는지 모른다.
 */
export function chipRow(works: WorkListItem[], sel: ChipSel): ChipRow {
  const open = works.filter(isOpenWork);
  const past = works.filter((w) => !isOpenWork(w));
  let chips = open;
  let overflow: WorkListItem[] = [];
  if (open.length >= 5) {
    chips = open.slice(0, 4);
    overflow = open.slice(4);
    if (sel.kind === "work") {
      const i = overflow.findIndex((w) => w.id === sel.id);
      if (i >= 0) {
        const picked = overflow[i];
        overflow = [chips[3], ...overflow.filter((_, j) => j !== i)];
        chips = [...chips.slice(0, 3), picked];
      }
    }
  }
  const paused = open.filter((w) => w.status === "paused");
  return { show: works.length > 0, chips, overflow, past, paused: paused.length >= 2 ? paused : [] };
}

/**
 * 메시지·서브 미션 거르기 — `(미션 없음)` 은 `work_id = null` 만(§4.6).
 * **칸이 아예 없으면(`undefined`) 서버가 말하지 않은 것**이지 「미션 없음」이 아니다 — 거르지 않고 서버의 거르기(`work_id`·`no_work`
 * 파라미터)를 믿는다. `null` 과 구분하지 않으면 칸을 안 싣는 서버에서 미션 칩을 고르는 순간 타임라인이 텅 빈다(T-R2-W2 실서버 관측).
 */
export function matchesSel(workId: string | null | undefined, sel: ChipSel): boolean {
  if (sel.kind === "all" || workId === undefined) return true;
  if (sel.kind === "none") return workId == null;
  return workId === sel.id;
}
export const filterMessages = (ms: Message[], sel: ChipSel) => ms.filter((m) => matchesSel(m.work_id, sel));
export const filterLanes = (ls: Lane[], sel: ChipSel) => ls.filter((l) => matchesSel(l.work_id, sel));

/** `(전체)` 에서 우열 미션 칸이 보이는 미션 — 최근 활동한 열린 미션. 열린 미션이 없으면 없다. */
export function recentWork(works: WorkListItem[]): WorkListItem | null {
  const open = works.filter(isOpenWork);
  if (!open.length) return null;
  return [...open].sort((a, b) => (b.last_activity_at ?? "").localeCompare(a.last_activity_at ?? ""))[0];
}

/**
 * 우열 미션 칸의 모드 — 칩 선택과 **연동**한다(§4.6 「미션 칩의 두 가지 일」).
 *  - `picked`: 사람이 고른 미션 — 미션 동작이 켜진다(권한은 따로).
 *  - `recent`: `(전체)` 에서 최근 활동 미션을 **보여 주기만** — 동작은 통째로 비활성(Lead 판정 4).
 *  - `none_view`: `(미션 없음)` — 칸은 남기고 안을 비운다(감추지 않는다).
 *  - `no_works`: 미션이 하나도 없는 방.
 */
export type PanelMode =
  | { kind: "picked"; workId: string }
  | { kind: "recent"; workId: string }
  | { kind: "none_view" }
  | { kind: "no_works" };
export function panelMode(sel: ChipSel, works: WorkListItem[]): PanelMode {
  if (sel.kind === "work") return { kind: "picked", workId: sel.id };
  if (sel.kind === "none") return { kind: "none_view" };
  const r = recentWork(works);
  if (r) return { kind: "recent", workId: r.id };
  return works.length ? { kind: "none_view" } : { kind: "no_works" };
}
/** 미션 동작(일시정지·재개·종료·취소…)이 켜질 수 있는 모드인가 — 사람이 고른 미션일 때만. */
export const panelActionsEnabled = (m: PanelMode) => m.kind === "picked";

/** 세 층 요약(§4.6) — 0 인 층은 생략하되 미션은 「미션 없음」으로 남는다(「미션 없음 · 할 일 1개」). */
export function layerCounts(room: Pick<Room, "counts">): { works: number; lanes: number; tasks: number } {
  const c = room.counts ?? {};
  return { works: c.works_active ?? 0, lanes: c.lanes_active ?? 0, tasks: c.tasks_active ?? 0 };
}

/** 보드의 묶음 순서 — 사람이 할 일 우선(§4.6 SCR-C I). `done`·`failed` 는 기본 접힘. */
export const BOARD_ORDER: readonly LaneStatus[] = ["blocked", "waiting_human", "paused", "running", "queued", "failed", "done"];
export const BOARD_FOLDED: ReadonlySet<LaneStatus> = new Set<LaneStatus>(["done", "failed"]);

/** `paused` 서브 미션은 어느 층의 예산인가(§4.6) — 방 예산이면 방, 매인 미션이 일시정지(예산)면 미션, 그 밖은 할 일. */
export type PausedLayer = "task" | "work" | "room";
export function pausedLayer(lane: Pick<Lane, "status" | "work_id">, room: Pick<Room, "blocked_reason">, works: Pick<WorkListItem, "id" | "status" | "paused_reason">[]): PausedLayer | null {
  if (lane.status !== "paused") return null;
  if (room.blocked_reason === "budget") return "room";
  const w = lane.work_id ? works.find((x) => x.id === lane.work_id) : undefined;
  if (w && w.status === "paused" && w.paused_reason === "budget") return "work";
  return "task";
}

/** 「나에게 필요한 것」의 한 항목 — `key` 로 중복을 없앤다(같은 사건이 좌열 카드·우열 배너·타임라인에 동시에 떠도 1). */
export interface NeedItem {
  key: string;
  /** 누르면 갈 곳 — `data-need` 속성 값. */
  target: string;
}

export interface NeedsInput {
  me: string | null;
  room: Pick<Room, "blocked_reason" | "blocked_detail" | "my_capabilities" | "my_room_role">;
  hitls: Pick<HitlRequest, "id" | "status" | "can_respond" | "message_id">[];
  lanes: Pick<Lane, "id" | "status" | "work_id" | "hitl_request_id" | "blocked_message_id">[];
  works: Pick<WorkListItem, "id" | "director">[];
  /** 우열에 실린 미션의 종료 조건이 막혔고 내가 그 Director 인가(「조건 고치기」). */
  fixCondition?: { workId: string } | null;
}

/**
 * 「나에게 필요한 것 N」(§4.6) — 방 멈춤 해소 · 내가 답할 수 있는 확인 요청(HITL·승인·예산 승인 — 서브 미션 카드의 응답·승인 버튼은
 * 같은 요청을 가리키므로 요청 id 로 합친다) · 내가 중단할 수 있는 서브 미션의 질문 · 조건 고치기. **중복 없이** 센다.
 */
export function needsMe(i: NeedsInput): NeedItem[] {
  const out = new Map<string, NeedItem>();
  const r = i.room;
  if (r.blocked_reason) {
    const mine = r.blocked_reason === "manual" ? (r.my_capabilities ?? []).includes("block") : !!i.me && r.blocked_detail?.approver?.id === i.me;
    if (mine) out.set("room", { key: "room", target: "room-banner" });
  }
  for (const h of i.hitls) {
    if (h.status === "open" && h.can_respond) out.set(`hitl:${h.id}`, { key: `hitl:${h.id}`, target: h.message_id ? `message:${h.message_id}` : `hitl:${h.id}` });
  }
  const steward = r.my_room_role === "owner" || r.my_room_role === "deputy";
  for (const l of i.lanes) {
    if (l.status !== "blocked") continue;
    const w = l.work_id ? i.works.find((x) => x.id === l.work_id) : undefined;
    const mine = w ? !!i.me && w.director?.id === i.me : steward;
    const key = `question:${l.blocked_message_id ?? l.id}`;
    if (mine) out.set(key, { key, target: l.blocked_message_id ? `message:${l.blocked_message_id}` : `lane:${l.id}` });
  }
  if (i.fixCondition) out.set(`fix:${i.fixCondition.workId}`, { key: `fix:${i.fixCondition.workId}`, target: "work-panel" });
  return [...out.values()];
}

/** 방이 새 메시지를 받지 않는 이유 — 보관 · 감사 열람. 둘 다 아니면 null. */
export function postBlockedBy(room: Pick<Room, "status" | "visibility" | "my_room_role" | "my_capabilities">): "archived" | "audit" | null {
  if (room.status === "archived") return "archived";
  if (!(room.my_capabilities ?? []).includes("post") && room.visibility === "invited" && room.my_room_role == null) return "audit";
  return null;
}
/** `invited` 방을 참여하지 않은 ws owner·admin 이 보는 중(§2.4 감사 열람). */
export const isAuditView = (room: Pick<Room, "visibility" | "my_room_role">) => room.visibility === "invited" && room.my_room_role == null;

/**
 * 아티팩트·결정 기록의 **미션별 묶음**(§4.6 (나)) — 선택된 미션 묶음이 맨 위(✓), 「미션 없음」 묶음은 맨 뒤, **묶을 것이 하나면 머리글 없음**.
 * 각 묶음은 최근 5건 + 「더 보기 N」.
 */
export interface Group<T> {
  workId: string | null;
  title: string | null;
  items: T[];
  more: number;
  selected: boolean;
}
export function groupByWork<T extends { work_id?: string | null; created_at: string }>(
  items: T[], works: Pick<WorkListItem, "id" | "title">[], selectedWorkId: string | null, perGroup = 5,
): { groups: Group<T>[]; headers: boolean } {
  const byWork = new Map<string | null, T[]>();
  for (const it of [...items].sort((a, b) => b.created_at.localeCompare(a.created_at))) {
    const k = it.work_id ?? null;
    byWork.set(k, [...(byWork.get(k) ?? []), it]);
  }
  const title = (id: string | null) => (id ? works.find((w) => w.id === id)?.title ?? null : null);
  const keys = [...byWork.keys()].sort((a, b) => {
    if (a === b) return 0;
    if (a === null) return 1;
    if (b === null) return -1;
    if (a === selectedWorkId) return -1;
    if (b === selectedWorkId) return 1;
    return 0;
  });
  const groups = keys.map((k) => {
    const all = byWork.get(k)!;
    return { workId: k, title: title(k), items: all.slice(0, perGroup), more: Math.max(0, all.length - perGroup), selected: k !== null && k === selectedWorkId };
  });
  return { groups, headers: groups.length > 1 };
}
