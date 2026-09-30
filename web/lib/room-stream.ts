/**
 * 방 화면(S7 `app/(app)/rooms/[id]/page.tsx`)의 실시간 프레임 처리 — 유형별 작은 리듀서(T-RF2). React 없이 테스트한다(`room-stream.test.ts`).
 *
 * 나눔: ① `roomEvent()` 가 봉투의 `payload`(계약상 `Record<string, unknown>`)를 유형별 모양으로 **한 곳에서** 읽는다 — 화면에 캐스팅이 없다.
 *       ② 아래 리듀서들이 상태 조각 하나씩을 바꾼다(순수 함수 · 바뀔 것이 없으면 **같은 참조**를 돌려준다 — React 가 다시 그리지 않게).
 *       ③ 화면의 `onEvent` 스위치는 리듀서를 setState 에 걸고, 부수 효과(다시 읽기 · 이동 · 높이 쥐기)만 직접 한다.
 *
 * **이벤트 종류를 하나 더할 때 고칠 곳**
 *  1. 계약(`contracts/openapi.yaml` StreamEvent.type enum → `npm run gen:api`) — 계약 PR 몫.
 *  2. `lib/realtime/stream.ts` 의 `STREAM_EVENT_TYPES` — 목록에 없는 타입은 EventSource 가 **조용히 버린다**.
 *  3. 이 파일: `RoomEventPayloads` 에 payload 모양 한 줄 + (상태를 바꾸면) 리듀서 하나 + 테스트.
 *  4. `page.tsx` `onEvent` 의 `switch (e.type)` 에 case 하나(리듀서를 setState 에 건다).
 *  (진행 메모 `message.delta` 계열은 `lib/progress-memo` `memoEffect` 가 따로 받는다. 작업 카드 `card.*` 는 `lib/cards` 의 리듀서 — 여기서 다시 내보낸다.)
 */
import { matchesSel, type ChipSel } from "./room-view";
import type { Artifact, Decision, HitlRequest, Lane, Message, Room, StreamEvent, Task, TaskCard, TaskEvent, Work, WorkListItem } from "./api/types";
export { boardOnCard, cardsOnUpserted } from "./cards";

/** 방 화면이 읽는 이벤트의 payload 모양(계약 SSE 표). 여기 없는 타입은 `roomEvent()` 가 `{ type, payload: unknown }` 으로 넘긴다. */
export interface RoomEventPayloads {
  "message.created": Message;
  "message.updated": Message;
  "task_event.appended": TaskEvent;
  "task.updated": Task;
  "task_event.superseded": { task_id: string; event_id: string; superseded_by: string };
  "lane.updated": Lane;
  "hitl.created": HitlRequest;
  "hitl.updated": HitlRequest;
  "artifact.created": Artifact;
  "decision.created": Decision;
  "work.created": WorkPatch;
  "work.updated": WorkPatch;
  "work.closed": { work_id: string; status: WorkListItem["status"] };
  "work.deleted": { work_id: string };
  "work.completion_progress": { work_id: string; completion_progress?: Work["completion_progress"] };
  "room.updated": Partial<Room>;
  "room.deleted": { room_id?: string; session_id?: string };
  "room_read.recorded": { direction?: string };
  "cost.updated": CostPayload;
  "agent.typing": { agent_id: string; typing: boolean };
  /** v0.3.10 — `TaskCard`(`versions` 없음). 리듀서는 `lib/cards`(`cardsOnUpserted` · `boardOnCard`). */
  "card.created": TaskCard;
  "card.updated": TaskCard;
}
export type WorkPatch = Partial<WorkListItem> & { id: string };
export interface CostPayload { room_cost_usd?: number; cost_usd?: number; work_id?: string; work_cost_usd?: number; estimated?: boolean }

export type RoomEvent =
  | { [K in keyof RoomEventPayloads]: { type: K; payload: RoomEventPayloads[K]; roomId: string | null | undefined } }[keyof RoomEventPayloads]
  | { type: Exclude<StreamEvent["type"], keyof RoomEventPayloads>; payload: unknown; roomId: string | null | undefined };

/** 봉투 → 유형별 모양. payload 캐스팅은 여기 한 번뿐이다(계약 스키마가 payload 를 유형별로 좁히지 않는다). */
export function roomEvent(ev: StreamEvent): RoomEvent {
  return { type: ev.type, payload: ev.payload as unknown, roomId: ev.room_id } as RoomEvent;
}

const byTime = (a: Message, b: Message) => (a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0);

// ── 메시지 ──
/** 새 메시지 — 뿌리 메시지이고 지금 칩 선택에 맞으면 시각순으로 끼운다(이미 있으면 그대로). 답글이면 뿌리의 답글 수만 올린다. */
export function messagesOnCreated(ms: Message[], m: Message, sel: ChipSel): Message[] {
  if (m.parent_id) {
    const root = m.parent_id;
    return ms.map((x) => (x.id === root ? { ...x, reply_count: (x.reply_count ?? 0) + 1 } : x));
  }
  if (!matchesSel(m.work_id, sel)) return ms;
  return ms.some((x) => x.id === m.id) ? ms : [...ms, m].sort(byTime);
}
/** 새 답글 — 이미 펼쳐 둔(읽어 둔) 스레드에만 붙는다. */
export function repliesOnCreated(r: Record<string, Message[]>, m: Message): Record<string, Message[]> {
  const root = m.parent_id;
  if (!root || !r[root]) return r;
  return r[root].some((x) => x.id === m.id) ? r : { ...r, [root]: [...r[root], m].sort(byTime) };
}
export function messagesOnUpdated(ms: Message[], m: Message): Message[] {
  return ms.map((x) => (x.id === m.id ? { ...x, ...m } : x));
}

// ── 턴 기록(작업 과정) — task id → 읽어 둔 기록. 안 읽은 task 의 프레임은 버린다(펼칠 때 REST 로 읽는다). ──
type EventsCache<E> = Record<string, E>;
type EventsEntry = { events: TaskEvent[]; task?: Pick<Task, "status" | "attempt"> | null };
export function eventsOnAppended<E extends EventsEntry>(c: EventsCache<E>, te: TaskEvent): EventsCache<E> {
  const cur = c[te.task_id];
  if (!cur || cur.events.some((e) => e.id === te.id)) return c;
  return { ...c, [te.task_id]: { ...cur, events: [...cur.events, te] } };
}
/** 「진행 중…」 판정(T-FEED) — task 가 끝나면 그 피드의 짝 없는 started 줄이 「결과 없음」으로 바뀐다. */
export function eventsOnTask<E extends EventsEntry>(c: EventsCache<E>, t: Pick<Task, "id" | "status" | "attempt">): EventsCache<E> {
  const cur = c[t.id];
  if (!cur) return c;
  return { ...c, [t.id]: { ...cur, task: { status: t.status, attempt: t.attempt } } };
}
export function eventsOnSuperseded<E extends EventsEntry>(c: EventsCache<E>, p: RoomEventPayloads["task_event.superseded"]): EventsCache<E> {
  const cur = c[p.task_id];
  if (!cur) return c;
  return { ...c, [p.task_id]: { ...cur, events: cur.events.map((e) => (e.id === p.event_id ? { ...e, superseded_by: p.superseded_by } : e)) } };
}

// ── 서브 미션 · 확인 요청 · 아티팩트 · 결정 ──
export function lanesOnUpdated(cur: Lane[], l: Lane): Lane[] {
  return cur.some((x) => x.id === l.id) ? cur.map((x) => (x.id === l.id ? { ...x, ...l } : x)) : [...cur, l];
}
/** 새것이 맨 앞(목록 순서 = 최근순). null(아직 못 읽음)이면 그것 하나로 시작한다. */
export function prependById<T extends { id: string }>(cur: T[] | null, x: T): T[] {
  return cur ? [x, ...cur.filter((y) => y.id !== x.id)] : [x];
}

// ── 미션 ──
export function worksOnUpserted(cur: WorkListItem[], w: WorkPatch): WorkListItem[] {
  return cur.some((x) => x.id === w.id) ? cur.map((x) => (x.id === w.id ? { ...x, ...w } : x)) : [...cur, w as WorkListItem];
}
export function worksOnClosed(cur: WorkListItem[], p: RoomEventPayloads["work.closed"]): WorkListItem[] {
  return cur.map((x) => (x.id === p.work_id ? { ...x, status: p.status } : x));
}
export function worksOnDeleted(cur: WorkListItem[], p: RoomEventPayloads["work.deleted"]): WorkListItem[] {
  return cur.filter((x) => x.id !== p.work_id);
}
type Progress = Work["completion_progress"];
/** 우열 미션 칸의 진행률 — 그 미션이 실려 있을 때만. */
export function workOnProgress(w: Work | null, workId: string, prog: Progress): Work | null {
  return w && w.id === workId ? { ...w, completion_progress: prog } : w;
}
/** 칩 줄의 진행률은 met/total 만 싣는다. */
export function worksOnProgress(cur: WorkListItem[], workId: string, prog: Progress): WorkListItem[] {
  return cur.map((x) => (x.id === workId ? { ...x, completion_progress: { met: prog.met, total: prog.total } } : x));
}

// ── 방 ──
/** `room.updated` 가 싣는 칸(계약 SSE 표 — Room 부분). 보는 사람 모양 칸(my_capabilities 등)은 없다 — 그것은 다시 읽어 맞춘다. */
export const ROOM_UPDATED_KEYS = ["name", "description", "status", "visibility", "blocked_reason", "blocked_detail", "last_activity_at"] as const;
export function roomOnUpdated(r: Room | null, p: Partial<Room>): Room | null {
  if (!r) return r;
  const patch: Partial<Room> = {};
  for (const k of ROOM_UPDATED_KEYS) if (k in p) (patch as Record<string, unknown>)[k] = p[k];
  return { ...r, ...patch };
}
/** `room.deleted` 가 이 방 것인가 — payload 의 room_id(옛 session_id) · 없으면 봉투의 room_id. */
export function isRoomDeleted(roomId: string, p: RoomEventPayloads["room.deleted"], envelopeRoomId: string | null | undefined): boolean {
  return (p.room_id ?? p.session_id ?? envelopeRoomId) === roomId;
}
export type Reads = { out: number; in: number };
export function readsOnRecorded(r: Reads | null, p: RoomEventPayloads["room_read.recorded"]): Reads {
  const cur = r ?? { out: 0, in: 0 };
  return p.direction === "read_by" || p.direction === "in" ? { ...cur, in: cur.in + 1 } : { ...cur, out: cur.out + 1 };
}

// ── 비용 ──
/** 방 비용(`room_cost_usd`, 옛 `cost_usd`) — 없으면 방은 그대로. */
export function roomOnCost(r: Room | null, p: CostPayload): Room | null {
  const roomCost = p.room_cost_usd ?? p.cost_usd;
  if (typeof roomCost !== "number" || !r) return r;
  return { ...r, cost_usd: roomCost, cost_estimated: p.estimated ?? r.cost_estimated };
}
/** 미션 비용 — `work_id` 와 `work_cost_usd` 가 둘 다 있을 때만. */
export function workCostOf(p: CostPayload): { workId: string; usd: number } | null {
  return p.work_id && typeof p.work_cost_usd === "number" ? { workId: p.work_id, usd: p.work_cost_usd } : null;
}

// ── 입력 중 ──
export function typingOn(t: Record<string, boolean>, p: RoomEventPayloads["agent.typing"]): Record<string, boolean> {
  return { ...t, [p.agent_id]: p.typing };
}

// ── 작업 카드(v0.3.10) ──
/** `card.*` 가 이 방·이 화면의 것인가 — 봉투의 room_id 가 없을 때 payload 의 room_id 로. */
export function isRoomCard(roomId: string, card: Pick<TaskCard, "room_id">): boolean {
  return card.room_id === roomId;
}
