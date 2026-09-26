/** S5 실시간 반영(lib/rooms.ts) — 재정렬하지 않는다 · 셀 수 있는 것만 제자리 · 나머지는 다시 부르기. */
import { describe, expect, it } from "vitest";
import { mergeKeepOrder, roomEventEffect } from "./rooms";
import type { RoomListItem, StreamEvent } from "@/lib/api/types";

const room = (id: string, over: Partial<RoomListItem> = {}): RoomListItem => ({
  id, name: `방 ${id}`, description: "", status: "active", blocked_reason: null, unread_count: 0, active_work_count: 0,
  attention: { hitl_open: 0, blocked: 0, failed: 0 }, participants: [], my_room_role: "owner", last_activity_at: null, ...over,
});
const ev = (type: StreamEvent["type"], payload: Record<string, unknown>, extra: Partial<StreamEvent> = {}): StreamEvent =>
  ({ id: "1", type, at: "2026-09-24T00:00:00Z", workspace_id: "w", session_id: null, ephemeral: false, payload, ...extra }) as StreamEvent;

describe("mergeKeepOrder — 보이는 순서를 지킨다", () => {
  it("있던 카드는 제자리에 새 값으로, 없어진 카드는 빠지고, 새 카드는 맨 위", () => {
    const cur = [room("a"), room("b"), room("c")];
    // 서버는 활동순으로 c 를 맨 위에 올려 보냈다 — 그래도 화면은 a,b 순서를 지킨다.
    const fresh = [room("c", { unread_count: 4 }), room("n"), room("a", { name: "바뀐 이름" })];
    const out = mergeKeepOrder(cur, fresh);
    expect(out.map((r) => r.id)).toEqual(["n", "a", "c"]);
    expect(out[1].name).toBe("바뀐 이름");
    expect(out[2].unread_count).toBe(4);
  });
  it("처음이면 받은 그대로", () => {
    expect(mergeKeepOrder(null, [room("x")]).map((r) => r.id)).toEqual(["x"]);
  });
});

describe("roomEventEffect", () => {
  const items = [room("a"), room("b")];
  it("room.updated — 계약 부분 칸만 제자리에서 고친다(보는 사람 칸은 건드리지 않는다)", () => {
    const eff = roomEventEffect(items, ev("room.updated", { id: "b", blocked_reason: "budget", name: "새 이름", unread_count: 99, my_room_role: null }));
    expect(eff.kind).toBe("set");
    if (eff.kind !== "set") return;
    expect(eff.items.map((r) => r.id)).toEqual(["a", "b"]);
    expect(eff.items[1]).toMatchObject({ blocked_reason: "budget", name: "새 이름", unread_count: 0, my_room_role: "owner" });
  });
  it("room.updated — 목록에 없는 방(방금 생김·방금 초대됨)은 다시 부른다", () => {
    expect(roomEventEffect(items, ev("room.updated", { id: "z", name: "새 방" }))).toEqual({ kind: "reload" });
  });
  it("room.unread — 내 다른 탭에서 읽은 수를 그대로", () => {
    const eff = roomEventEffect([room("a", { unread_count: 5 })], ev("room.unread", { room_id: "a", unread_count: 0, last_read_message_id: "m" }));
    expect(eff).toEqual({ kind: "set", items: [room("a", { unread_count: 0 })] });
  });
  it("room.deleted — 그 방을 뺀다, 두 번 와도 같다(옛 session.deleted 는 v0.3.0 R4 에서 지워졌다)", () => {
    const e1 = roomEventEffect(items, ev("room.deleted", { room_id: "a" }));
    expect(e1.kind === "set" && e1.items.map((r) => r.id)).toEqual(["b"]);
    expect(roomEventEffect([room("b")], ev("room.deleted", { room_id: "a" }))).toEqual({ kind: "none" });
    const e2 = roomEventEffect(items, ev("room.deleted", {}, { room_id: "b" }));
    expect(e2.kind === "set" && e2.items.map((r) => r.id)).toEqual(["a"]);
  });
  it("수가 바뀌는 사건(미션·확인 요청·서브 미션·새 메시지)은 다시 부르고, 모르는 사건은 무시", () => {
    for (const t of ["work.created", "work.closed", "hitl.created", "lane.updated", "message.created"] as const) expect(roomEventEffect(items, ev(t, {}))).toEqual({ kind: "reload" });
    expect(roomEventEffect(items, ev("message.delta", {}))).toEqual({ kind: "none" });
  });
});
