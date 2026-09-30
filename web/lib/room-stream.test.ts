/**
 * lib/room-stream — 방 화면 실시간 리듀서(T-RF2). 옛 page.tsx `onEvent` 안의 식을 그대로 옮긴 것이라, 여기 단언은 옛 동작의 기록이다.
 * 회귀 주입(PR 표): messagesOnCreated 의 matchesSel 거르기를 빼면 (칩 거르기) FAIL; 같은 id 가드를 빼면 (중복) FAIL;
 * ROOM_UPDATED_KEYS 에 my_capabilities 를 넣으면 (보는 사람 칸) FAIL; readsOnRecorded 의 "in" 을 빼면 (읽음 방향) FAIL;
 * STREAM_EVENT_TYPES 에서 card.updated 를 빼면 (card.*) FAIL.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  ROOM_UPDATED_KEYS, eventsOnAppended, eventsOnSuperseded, eventsOnTask, isRoomDeleted, lanesOnUpdated, messagesOnCreated, messagesOnUpdated, prependById,
  readsOnRecorded, repliesOnCreated, roomEvent, roomOnCost, roomOnUpdated, typingOn, workCostOf, workOnProgress, worksOnClosed, worksOnDeleted, worksOnProgress,
  worksOnUpserted, boardOnCard, cardsOnUpserted, isRoomCard,
} from "./room-stream";
import { STREAM_EVENT_TYPES } from "./realtime/stream";
import type { Lane, Message, Room, Task, TaskEvent, Work, WorkListItem } from "./api/types";

const msg = (id: string, at: string, extra: Partial<Message> = {}): Message => ({ id, created_at: at, parent_id: null, work_id: null, reply_count: 0, content: id, ...extra }) as Message;
const te = (id: string, task: string): TaskEvent => ({ id, task_id: task }) as TaskEvent;

describe("roomEvent — payload 를 유형별 모양으로 한 곳에서 읽는다", () => {
  it("type · payload · 봉투 room_id 를 그대로 싣는다", () => {
    const m = msg("m1", "t1");
    const e = roomEvent({ id: "e", type: "message.created", at: "t", room_id: "r1", payload: m as unknown as Record<string, unknown> });
    expect(e).toEqual({ type: "message.created", payload: m, roomId: "r1" });
  });
  it("리듀서가 받는 이벤트 종류는 전부 구독 목록(STREAM_EVENT_TYPES)에 있다 — 없으면 EventSource 가 조용히 버린다", () => {
    const src = readFileSync(join(__dirname, "room-stream.ts"), "utf8");
    const block = src.slice(src.indexOf("export interface RoomEventPayloads"), src.indexOf("export type WorkPatch"));
    const keys = [...block.matchAll(/^\s+"([a-z_.]+)":/gm)].map((m) => m[1]);
    expect(keys.length).toBeGreaterThanOrEqual(20);
    for (const k of keys) expect(STREAM_EVENT_TYPES, k).toContain(k);
  });
  it("방 화면 onEvent 에는 payload 캐스팅이 없다(한 곳 = roomEvent)", () => {
    const page = readFileSync(join(__dirname, "..", "app/(app)/rooms/[id]/page.tsx"), "utf8");
    const on = page.slice(page.indexOf("const onEvent = useCallback"), page.indexOf("const conn = useWorkspaceStream"));
    expect(on).toContain("roomEvent(ev)");
    expect(on).not.toMatch(/ev\.payload as|as unknown as/);
  });
});

describe("메시지", () => {
  const sel = { kind: "all" } as const;
  it("새 뿌리 메시지는 시각순으로 끼우고, 같은 id 는 두 번 넣지 않는다", () => {
    const a = msg("a", "2026-01-01T00:00:01Z");
    const c = msg("c", "2026-01-01T00:00:03Z");
    const b = msg("b", "2026-01-01T00:00:02Z");
    const out = messagesOnCreated([a, c], b, sel);
    expect(out.map((x) => x.id)).toEqual(["a", "b", "c"]);
    const same = [a, b, c];
    expect(messagesOnCreated(same, b, sel)).toBe(same);
  });
  it("칩 선택에 안 맞는 메시지는 버린다(미션 칩 거르기)", () => {
    const cur = [msg("a", "t1")];
    expect(messagesOnCreated(cur, msg("w", "t2", { work_id: "W2" }), { kind: "work", id: "W1" })).toBe(cur);
    expect(messagesOnCreated(cur, msg("w", "t2", { work_id: "W1" }), { kind: "work", id: "W1" }).map((x) => x.id)).toEqual(["a", "w"]);
    expect(messagesOnCreated(cur, msg("n", "t2", { work_id: "W1" }), { kind: "none" })).toBe(cur);
  });
  it("답글은 뿌리의 답글 수만 올리고, 스레드는 읽어 둔 것에만 붙는다", () => {
    const root = msg("r", "t1", { reply_count: 2 });
    const reply = msg("x", "t2", { parent_id: "r" });
    expect(messagesOnCreated([root], reply, sel)[0].reply_count).toBe(3);
    const none = {};
    expect(repliesOnCreated(none, reply)).toBe(none);
    const opened = { r: [msg("y", "t3", { parent_id: "r" })] };
    expect(repliesOnCreated(opened, reply).r.map((m) => m.id)).toEqual(["x", "y"]);
    const withIt = { r: [reply] };
    expect(repliesOnCreated(withIt, reply)).toBe(withIt);
  });
  it("수정은 같은 id 에 덮는다", () => {
    expect(messagesOnUpdated([msg("a", "t1")], { ...msg("a", "t1"), content: "고침" })[0].content).toBe("고침");
  });
});

describe("턴 기록", () => {
  type Entry = { events: TaskEvent[]; structured: boolean; loading: boolean; task?: Pick<Task, "status" | "attempt"> | null };
  const cache: Record<string, Entry> = { t1: { events: [te("e1", "t1")], structured: true, loading: false } };
  it("읽어 둔 task 에만 붙이고 같은 id 는 버린다", () => {
    expect(eventsOnAppended(cache, te("e2", "t1")).t1.events.map((e) => e.id)).toEqual(["e1", "e2"]);
    expect(eventsOnAppended(cache, te("e1", "t1"))).toBe(cache);
    expect(eventsOnAppended(cache, te("e9", "t9"))).toBe(cache);
  });
  it("task 상태 · 대체(superseded)", () => {
    expect(eventsOnTask(cache, { id: "t1", status: "completed", attempt: 2 }).t1.task).toEqual({ status: "completed", attempt: 2 });
    expect(eventsOnTask(cache, { id: "tx", status: "completed", attempt: 1 })).toBe(cache);
    expect(eventsOnSuperseded(cache, { task_id: "t1", event_id: "e1", superseded_by: "e5" }).t1.events[0].superseded_by).toBe("e5");
    expect(eventsOnSuperseded(cache, { task_id: "tx", event_id: "e1", superseded_by: "e5" })).toBe(cache);
  });
});

describe("서브 미션 · 목록", () => {
  it("lane 은 있으면 덮고 없으면 뒤에 붙는다", () => {
    const a = { id: "l1", status: "running" } as Lane;
    expect(lanesOnUpdated([a], { ...a, status: "done" } as Lane)[0].status).toBe("done");
    expect(lanesOnUpdated([a], { id: "l2" } as Lane).map((l) => l.id)).toEqual(["l1", "l2"]);
  });
  it("아티팩트·결정은 새것이 맨 앞, 같은 id 는 하나로 · 못 읽은(null) 목록이면 그것 하나", () => {
    expect(prependById([{ id: "a" }, { id: "b" }], { id: "b" }).map((x) => x.id)).toEqual(["b", "a"]);
    expect(prependById(null, { id: "z" })).toEqual([{ id: "z" }]);
  });
});

describe("미션", () => {
  const w1 = { id: "w1", title: "하나", status: "active", completion_progress: { met: 0, total: 2 } } as unknown as WorkListItem;
  it("생성·갱신·닫힘·삭제", () => {
    expect(worksOnUpserted([w1], { id: "w1", title: "고침" })[0].title).toBe("고침");
    expect(worksOnUpserted([w1], { id: "w2", title: "둘" }).map((w) => w.id)).toEqual(["w1", "w2"]);
    expect(worksOnClosed([w1], { work_id: "w1", status: "completed" })[0].status).toBe("completed");
    expect(worksOnDeleted([w1], { work_id: "w1" })).toEqual([]);
  });
  it("진행률 — 우열 칸은 실린 미션만, 칩 줄은 met/total 만", () => {
    const prog = { met: 1, total: 2, satisfied: false, human_gate: true, conditions: [] } as unknown as Work["completion_progress"];
    const w = { id: "w1", completion_progress: { met: 0 } } as unknown as Work;
    expect(workOnProgress(w, "w1", prog)!.completion_progress).toBe(prog);
    expect(workOnProgress(w, "w9", prog)).toBe(w);
    expect(workOnProgress(null, "w1", prog)).toBeNull();
    expect(worksOnProgress([w1], "w1", prog)[0].completion_progress).toEqual({ met: 1, total: 2 });
  });
});

describe("방", () => {
  const room = { id: "r1", name: "방", cost_usd: 1, cost_estimated: false, my_capabilities: ["post"] } as unknown as Room;
  it("room.updated 는 계약 표의 칸만 — 보는 사람 모양 칸(my_capabilities)은 덮지 않는다", () => {
    expect(ROOM_UPDATED_KEYS).not.toContain("my_capabilities");
    const out = roomOnUpdated(room, { name: "새 이름", my_capabilities: [] } as Partial<Room>)!;
    expect(out.name).toBe("새 이름");
    expect(out.my_capabilities).toEqual(["post"]);
    expect(roomOnUpdated(null, { name: "x" })).toBeNull();
  });
  it("room.deleted 는 payload room_id → 옛 session_id → 봉투 순으로 이 방인지 가린다", () => {
    expect(isRoomDeleted("r1", { room_id: "r1" }, "r9")).toBe(true);
    expect(isRoomDeleted("r1", { session_id: "r1" }, "r9")).toBe(true);
    expect(isRoomDeleted("r1", {}, "r1")).toBe(true);
    expect(isRoomDeleted("r1", { room_id: "r2" }, "r1")).toBe(false);
  });
  it("읽음 — read_by·in 은 들어온 쪽, 그 밖은 나간 쪽", () => {
    expect(readsOnRecorded(null, { direction: "read_by" })).toEqual({ out: 0, in: 1 });
    expect(readsOnRecorded({ out: 1, in: 1 }, { direction: "in" })).toEqual({ out: 1, in: 2 });
    expect(readsOnRecorded(null, {})).toEqual({ out: 1, in: 0 });
  });
  it("비용 — 방은 room_cost_usd(없으면 옛 cost_usd), 미션은 work_id+work_cost_usd 둘 다 있을 때만", () => {
    expect(roomOnCost(room, { room_cost_usd: 3, estimated: true })).toMatchObject({ cost_usd: 3, cost_estimated: true });
    expect(roomOnCost(room, { cost_usd: 4 })).toMatchObject({ cost_usd: 4, cost_estimated: false });
    expect(roomOnCost(room, { work_id: "w1", work_cost_usd: 2 })).toBe(room);
    expect(workCostOf({ work_id: "w1", work_cost_usd: 2 })).toEqual({ workId: "w1", usd: 2 });
    expect(workCostOf({ work_id: "w1" })).toBeNull();
  });
  it("입력 중", () => {
    expect(typingOn({ a: true }, { agent_id: "b", typing: true })).toEqual({ a: true, b: true });
  });
});

describe("작업 카드(v0.3.10) — card.created · card.updated", () => {
  const c = { id: "c1", room_id: "r1", work_id: "w1", number: 1, label: "C-1", version: 1, status: "in_progress", criteria: [], goal: "g", assignee: { agent_id: "a", name: "A" }, lane_id: "l1", parent_card_id: null, updated_at: "t1", actions: [] } as never;
  it("구독 목록에 둘 다 있고 payload 모양이 표에 있다(TaskCard) — 리듀서는 lib/cards 를 다시 내보낸다", () => {
    expect(STREAM_EVENT_TYPES).toContain("card.created");
    expect(STREAM_EVENT_TYPES).toContain("card.updated");
    const e = roomEvent({ id: "e", type: "card.updated", at: "t", room_id: "r1", payload: c });
    expect(e.type).toBe("card.updated");
    expect(isRoomCard("r1", e.payload as never)).toBe(true);
    expect(isRoomCard("r2", e.payload as never)).toBe(false);
    const cache = cardsOnUpserted({}, c);
    expect(Object.keys(cache)).toEqual(["c1"]);
    expect(boardOnCard({ work_id: "w1", items: [], total: 0, pending_judgement: 0 }, c)!.total).toBe(1);
  });
  it("방 화면 onEvent 가 card.* 를 캐시·분담표 리듀서에 건다", () => {
    const page = readFileSync(join(__dirname, "..", "app/(app)/rooms/[id]/page.tsx"), "utf8");
    const on = page.slice(page.indexOf("const onEvent = useCallback"), page.indexOf("const conn = useWorkspaceStream"));
    expect(on).toMatch(/case "card\.created":\s*case "card\.updated":/);
    expect(on).toContain("cardsOnUpserted(cur, c)");
    expect(on).toContain("boardOnCard(");
  });
});
