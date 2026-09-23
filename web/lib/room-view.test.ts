/**
 * S7 방 화면의 판정(T-R2-W2) — 칩 줄 접기(§8.7 Q8) · 거르기 · 우열 모드 연동 · 「나에게 필요한 것」 중복 없음 · 예산 층 · 미션별 묶음.
 */
import { describe, expect, it } from "vitest";
import {
  BOARD_FOLDED, BOARD_ORDER, chipGlyph, chipRow, filterLanes, filterMessages, groupByWork, needsMe, panelActionsEnabled, panelMode, parseSel,
  pausedLayer, postBlockedBy, recentWork, selParam, type NeedsInput,
} from "./room-view";
import { LANE_GROUP_ORDER } from "@/components/LaneBoard";
import type { Lane, Message, WorkListItem } from "@/lib/api/types";

const w = (id: string, status: WorkListItem["status"] = "active", extra: Partial<WorkListItem> = {}): WorkListItem => ({
  id, room_id: "r1", title: `미션 ${id}`, goal: "g", status, paused_reason: status === "paused" ? "budget" : null, waiting_human: false,
  director: { id: "u1", email: "", display_name: "서연", avatar_url: null, created_at: "" }, assignee_agent_id: null,
  completion_progress: { met: 0, total: 1 }, cost_usd: 0, budget_usd: null, last_activity_at: null, finished_at: null, ...extra,
});
const msg = (id: string, work_id: string | null) => ({ id, work_id, created_at: id }) as unknown as Message;
const lane = (id: string, work_id: string | null, status: Lane["status"] = "running", extra: Partial<Lane> = {}) =>
  ({ id, work_id, status, agent_id: "a1", hitl_request_id: null, blocked_message_id: null, ...extra }) as unknown as Lane;

describe("선택(?work=)", () => {
  it("미션 id 는 그 칩, none 은 (미션 없음), new·from(S21 자리)·없음은 (전체)", () => {
    expect(parseSel("w1")).toEqual({ kind: "work", id: "w1" });
    expect(parseSel("none")).toEqual({ kind: "none" });
    for (const v of [null, "", "new", "from"]) expect(parseSel(v)).toEqual({ kind: "all" });
    expect(selParam(parseSel("w1"))).toBe("w1");
    expect(selParam({ kind: "all" })).toBeNull();
  });
});

describe("칩 줄(§4.6 · COMPONENTS §9.1)", () => {
  it("미션이 하나도 없으면 칩 줄을 그리지 않는다", () => {
    expect(chipRow([], { kind: "all" }).show).toBe(false);
  });
  it("열린 미션이 0 이고 끝난 미션이 있으면 칩 줄을 그리고 끝난 것은 「지난 미션」으로(SCR-A P-4)", () => {
    const r = chipRow([w("a", "completed"), w("b", "cancelled")], { kind: "all" });
    expect(r.show).toBe(true);
    expect(r.chips).toHaveLength(0);
    expect(r.past.map((x) => x.id)).toEqual(["a", "b"]);
  });
  it("접기 기준은 미션 칩만 센다 — 4개까지는 다 보이고 5개부터 4 + 「미션 N개 ▾」", () => {
    expect(chipRow(["1", "2", "3", "4"].map((i) => w(i)), { kind: "all" }).overflow).toHaveLength(0);
    const r = chipRow(["1", "2", "3", "4", "5", "6"].map((i) => w(i)), { kind: "all" });
    expect(r.chips.map((x) => x.id)).toEqual(["1", "2", "3", "4"]);
    expect(r.overflow.map((x) => x.id)).toEqual(["5", "6"]);
    // 끝난 미션·일시정지는 접기 셈에 들지 않는다.
    expect(chipRow([w("1"), w("2"), w("3"), w("4"), w("x", "completed")], { kind: "all" }).overflow).toHaveLength(0);
  });
  it("고른 칩이 접힌 쪽이면 네 번째 자리와 바꾼다 — 고른 것이 안 보이면 우열이 무엇을 말하는지 모른다", () => {
    const r = chipRow(["1", "2", "3", "4", "5", "6"].map((i) => w(i)), { kind: "work", id: "6" });
    expect(r.chips.map((x) => x.id)).toEqual(["1", "2", "3", "6"]);
    expect(r.overflow.map((x) => x.id)).toEqual(["4", "5"]);
  });
  it("일시정지 미션이 2개 이상일 때만 「일시정지 N ▾」(SCR-A P-2)", () => {
    expect(chipRow([w("1", "paused"), w("2")], { kind: "all" }).paused).toHaveLength(0);
    expect(chipRow([w("1", "paused"), w("2", "paused")], { kind: "all" }).paused).toHaveLength(2);
  });
  it("⏳ 는 상태가 아니라 파생(열린 확인 요청) — 일시정지가 먼저 이긴다", () => {
    expect(chipGlyph(w("1", "active", { waiting_human: true }))).toEqual({ glyph: "⏳︎", waiting: true });
    expect(chipGlyph(w("1", "paused", { waiting_human: true })).glyph).toBe("⏸︎");
    expect(chipGlyph(w("1")).glyph).toBe("●");
  });
});

describe("거르기 — 칩 하나가 타임라인·보드·우열을 함께 바꾼다", () => {
  const ms = [msg("1", "w1"), msg("2", null), msg("3", "w2")];
  const ls = [lane("a", "w1"), lane("b", null), lane("c", "w2")];
  it("(전체)는 거르지 않고, (미션 없음)은 work_id = null 만, 미션 칩은 그 미션만", () => {
    expect(filterMessages(ms, { kind: "all" }).map((m) => m.id)).toEqual(["1", "2", "3"]);
    expect(filterMessages(ms, { kind: "none" }).map((m) => m.id)).toEqual(["2"]);
    expect(filterMessages(ms, { kind: "work", id: "w2" }).map((m) => m.id)).toEqual(["3"]);
    expect(filterLanes(ls, { kind: "work", id: "w1" }).map((l) => l.id)).toEqual(["a"]);
    expect(filterLanes(ls, { kind: "none" }).map((l) => l.id)).toEqual(["b"]);
  });
  it("우열 모드 — 고른 미션만 동작이 켜지고, (전체)는 최근 활동 미션을 보여 주기만, (미션 없음)은 칸을 비운다", () => {
    const works = [w("w1", "active", { last_activity_at: "2026-09-20T00:00:00Z" }), w("w2", "active", { last_activity_at: "2026-09-22T00:00:00Z" })];
    expect(panelMode({ kind: "work", id: "w1" }, works)).toEqual({ kind: "picked", workId: "w1" });
    expect(panelMode({ kind: "all" }, works)).toEqual({ kind: "recent", workId: "w2" });
    expect(recentWork(works)?.id).toBe("w2");
    expect(panelMode({ kind: "none" }, works)).toEqual({ kind: "none_view" });
    expect(panelMode({ kind: "all" }, [])).toEqual({ kind: "no_works" });
    expect(panelActionsEnabled({ kind: "picked", workId: "w1" })).toBe(true);
    expect(panelActionsEnabled({ kind: "recent", workId: "w2" })).toBe(false);
    expect(panelActionsEnabled({ kind: "none_view" })).toBe(false);
  });
});

describe("보드 — 사람이 할 일 우선, done·failed 접힘(SCR-C I)", () => {
  it("묶음 순서는 blocked → waiting_human → paused → running → queued → failed → done 이고 LaneBoard 와 같다", () => {
    expect(BOARD_ORDER).toEqual(["blocked", "waiting_human", "paused", "running", "queued", "failed", "done"]);
    expect(LANE_GROUP_ORDER).toEqual(BOARD_ORDER);
    expect([...BOARD_FOLDED].sort()).toEqual(["done", "failed"]);
  });
  it("paused 는 어느 층의 예산인가 — 방 예산 멈춤 > 매인 미션의 예산 일시정지 > 할 일", () => {
    const works = [w("w1", "paused"), w("w2")];
    expect(pausedLayer(lane("x", "w1", "paused"), { blocked_reason: null }, works)).toBe("work");
    expect(pausedLayer(lane("x", "w2", "paused"), { blocked_reason: null }, works)).toBe("task");
    expect(pausedLayer(lane("x", null, "paused"), { blocked_reason: "budget" }, works)).toBe("room");
    expect(pausedLayer(lane("x", "w1", "running"), { blocked_reason: null }, works)).toBeNull();
  });
});

describe("「나에게 필요한 것」 — 중복 없이(§4.6)", () => {
  const base: NeedsInput = {
    me: "u1",
    room: { blocked_reason: null, blocked_detail: null, my_capabilities: [], my_room_role: "member" },
    hitls: [], lanes: [], works: [w("w1")],
  };
  it("같은 확인 요청이 카드·배너·타임라인에 동시에 떠도 1 — 요청 id 로 합친다", () => {
    const n = needsMe({
      ...base,
      hitls: [{ id: "h1", status: "open", can_respond: true, message_id: "m1" }, { id: "h1", status: "open", can_respond: true, message_id: "m1" }],
      lanes: [lane("l1", "w1", "waiting_human", { hitl_request_id: "h1" })],
    });
    expect(n.map((x) => x.key)).toEqual(["hitl:h1"]);
    expect(n[0].target).toBe("message:m1");
  });
  it("내가 답할 수 없는 요청·닫힌 요청은 세지 않는다", () => {
    expect(needsMe({ ...base, hitls: [{ id: "h1", status: "open", can_respond: false, message_id: null }, { id: "h2", status: "answered", can_respond: true, message_id: null }] })).toEqual([]);
  });
  it("방 멈춤 — manual 은 풀 권한(block)이 있으면, 그 밖은 내가 지금의 승인자면 센다", () => {
    expect(needsMe({ ...base, room: { ...base.room, blocked_reason: "manual", my_capabilities: ["block"] } }).map((x) => x.key)).toEqual(["room"]);
    expect(needsMe({ ...base, room: { ...base.room, blocked_reason: "manual", my_capabilities: [] } })).toEqual([]);
    const approver = { id: "u1", email: "", display_name: "서연", avatar_url: null, created_at: "" };
    expect(needsMe({ ...base, room: { ...base.room, blocked_reason: "budget", blocked_detail: { approver } } }).map((x) => x.key)).toEqual(["room"]);
    expect(needsMe({ ...base, me: "u2", room: { ...base.room, blocked_reason: "budget", blocked_detail: { approver } } })).toEqual([]);
  });
  it("질문(blocked) — 내가 Director 인 미션의 것, 미션 밖이면 방장·부방장일 때", () => {
    const q = [lane("l1", "w1", "blocked", { blocked_message_id: "q1" }), lane("l2", null, "blocked", { blocked_message_id: "q2" })];
    expect(needsMe({ ...base, lanes: q }).map((x) => x.key)).toEqual(["question:q1"]);
    expect(needsMe({ ...base, me: "u9", room: { ...base.room, my_room_role: "owner" }, lanes: q }).map((x) => x.key)).toEqual(["question:q2"]);
  });
});

describe("방 전체 칸 — 미션별 묶음", () => {
  const items = [
    { id: "a", work_id: "w1", created_at: "1" },
    { id: "b", work_id: null, created_at: "2" },
    { id: "c", work_id: "w2", created_at: "3" },
  ];
  it("선택된 미션 묶음이 맨 위(✓), 「미션 없음」 묶음은 맨 뒤", () => {
    const { groups, headers } = groupByWork(items, [w("w1"), w("w2")], "w1");
    expect(groups.map((g) => g.workId)).toEqual(["w1", "w2", null]);
    expect(groups[0].selected).toBe(true);
    expect(headers).toBe(true);
  });
  it("묶을 것이 하나면 머리글을 그리지 않는다(SCR-A P-11)", () => {
    expect(groupByWork([items[0]], [w("w1")], null).headers).toBe(false);
  });
  it("각 묶음은 최근 5건 + 더 보기 N", () => {
    const many = Array.from({ length: 8 }, (_, i) => ({ id: String(i), work_id: "w1", created_at: String(i) }));
    const g = groupByWork(many, [w("w1")], null).groups[0];
    expect(g.items.map((x) => x.id)).toEqual(["7", "6", "5", "4", "3"]);
    expect(g.more).toBe(3);
  });
});

describe("게시가 막힌 방", () => {
  it("보관 · 감사 열람(초대 방을 참여하지 않은 소유자·관리자가 볼 때)", () => {
    expect(postBlockedBy({ status: "archived", visibility: "workspace", my_room_role: "owner", my_capabilities: [] })).toBe("archived");
    expect(postBlockedBy({ status: "active", visibility: "invited", my_room_role: null, my_capabilities: ["archive"] })).toBe("audit");
    expect(postBlockedBy({ status: "active", visibility: "workspace", my_room_role: null, my_capabilities: ["post"] })).toBeNull();
  });
});
