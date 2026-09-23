/**
 * S5 방 목록의 실시간 반영(SCREEN §4.3 「실시간」 · §6) — 순수 함수. 화면(`app/(app)/rooms/page.tsx`)이 SSE 프레임을 여기에 넘긴다.
 *
 * **목록은 실시간으로 재정렬되지 않는다**(§4.3 · §4.4 정렬 고정의 같은 이유 — 목록이 스스로 움직이면 방 20개에서 사람이 어제 본 방을 못 찾는다).
 * 그래서 다시 불러와도(`mergeKeepOrder`) 이미 보이는 카드의 순서는 그대로 두고 새 방만 위에 붙인다. 정렬은 사람이 화면을 다시 열 때 반영된다.
 */
import type { RoomListItem, StreamEvent } from "@/lib/api/types";

/** `room.updated` 가 싣는 칸(계약 SSE 표 — Room 부분: blocked_reason · status · name · description · last_activity_at). 보는 사람 모양 칸은 없다. */
const ROOM_UPDATED_KEYS = ["name", "description", "status", "blocked_reason", "last_activity_at"] as const;

/** 새로 받은 목록을 지금 보이는 순서에 맞춘다 — 있던 카드는 제자리(값만 새것), 없어진 카드는 빠지고, 새 카드는 맨 위. */
export function mergeKeepOrder(cur: RoomListItem[] | null, fresh: RoomListItem[]): RoomListItem[] {
  if (!cur) return fresh;
  const byId = new Map(fresh.map((r) => [r.id, r]));
  const known = new Set(cur.map((r) => r.id));
  const added = fresh.filter((r) => !known.has(r.id));
  const kept = cur.filter((r) => byId.has(r.id)).map((r) => byId.get(r.id)!);
  return [...added, ...kept];
}

export type RoomEventEffect =
  /** 목록을 이 값으로 바꾼다. */
  | { kind: "set"; items: RoomListItem[] }
  /** 목록에 없는 방이거나 이 프레임으로는 셀 수 없는 수(미션·주의 배지)가 바뀌었다 — 다시 불러와 `mergeKeepOrder` 로 합친다. */
  | { kind: "reload" }
  | { kind: "none" };

/** 셀 수 있는 것만 여기서 바꾸고, 나머지(진행 중인 미션 수·주의 배지·새 메시지의 안 읽음)는 다시 불러온다. */
const RELOAD_ON = new Set<string>([
  "work.created", "work.updated", "work.closed", "work.deleted",
  "hitl.created", "hitl.updated", "lane.updated",
  "participant.joined", "participant.left", "message.created",
]);

export function roomEventEffect(items: RoomListItem[], ev: StreamEvent): RoomEventEffect {
  const roomId = (p: { room_id?: string | null; id?: string | null; session_id?: string | null }) => p.room_id ?? p.id ?? p.session_id ?? ev.room_id ?? ev.session_id ?? null;
  switch (ev.type) {
    case "room.updated": {
      const p = ev.payload as Partial<RoomListItem> & { id?: string };
      const id = roomId(p);
      if (!id || !items.some((r) => r.id === id)) return { kind: "reload" };
      const patch: Partial<RoomListItem> = {};
      for (const k of ROOM_UPDATED_KEYS) if (k in p) (patch as Record<string, unknown>)[k] = p[k];
      return { kind: "set", items: items.map((r) => (r.id === id ? { ...r, ...patch } : r)) };
    }
    case "room.unread": {
      const p = ev.payload as { room_id?: string; unread_count?: number };
      if (!p.room_id || typeof p.unread_count !== "number" || !items.some((r) => r.id === p.room_id)) return { kind: "none" };
      return { kind: "set", items: items.map((r) => (r.id === p.room_id ? { ...r, unread_count: p.unread_count! } : r)) };
    }
    case "room.deleted":
    case "session.deleted": {
      // 같은 사건의 두 이름(R4 까지 서버가 둘 다 낸다) — "그 id 를 뺀다" 라 두 번 와도 같다.
      const p = ev.payload as { room_id?: string; session_id?: string };
      const id = p.room_id ?? p.session_id ?? ev.room_id ?? ev.session_id;
      if (!id || !items.some((r) => r.id === id)) return { kind: "none" };
      return { kind: "set", items: items.filter((r) => r.id !== id) };
    }
  }
  if (RELOAD_ON.has(ev.type)) return { kind: "reload" };
  return { kind: "none" };
}
