/**
 * 부분 메시지 묶음(PRD FR-3.1.4 · SCREEN §4.6 v0.19.11) — 타임라인 항목 접기 · 작업 과정 경계 하나.
 * 회귀 주입: timelineItems 의 groups 맵을 빼면(부분마다 항목) (하나) FAIL; byIndex 정렬을 빼면 (순서) FAIL;
 * processBoundaries 가 모든 부분을 남기면 (경계 하나) FAIL.
 */
import { describe, expect, it } from "vitest";
import type { Message } from "@/lib/api/types";
import { boundaryOf, isPart, processBoundaries, timelineItems } from "./parts";

const msg = (id: string, at: string, g?: { id: string; i: number; n: number }): Message => ({
  id, session_id: "r1", author_type: "agent", author_id: "a1", parent_id: null, content: id, mentions: [], source_task_id: "t1",
  kind: "text", state: "posted", created_at: at, group_id: g?.id ?? null, group_index: g?.i ?? null, group_size: g?.n ?? null,
} as Message);

describe("timelineItems — 같은 group_id 는 항목 하나", () => {
  it("(하나) 부분 셋 = 묶음 하나, 첫 부분 자리 · (순서) group_index 순 · 다른 메시지는 그대로", () => {
    const g = { id: "g1", n: 3 };
    const items = timelineItems([
      msg("m0", "10:00"),
      msg("p1", "10:01", { ...g, i: 1 }), // 같은 시각이면 도착 순이 뒤섞일 수 있다
      msg("p0", "10:01", { ...g, i: 0 }),
      msg("m1", "10:01"),
      msg("p2", "10:01", { ...g, i: 2 }),
    ]);
    expect(items.map((x) => (x.kind === "group" ? `G:${x.parts.map((p) => p.id).join(",")}` : x.message.id))).toEqual(["m0", "G:p0,p1,p2", "m1"]);
  });

  it("실시간 — 첫 부분만 온 묶음은 부분 하나짜리 말풍선(size 3) · 같은 부분 두 번은 하나", () => {
    const g = { id: "g1", n: 3, i: 0 };
    const items = timelineItems([msg("p0", "10:01", g), msg("p0", "10:01", g)]);
    expect(items).toHaveLength(1);
    const it0 = items[0];
    expect(it0.kind === "group" && it0.parts.length === 1 && it0.size === 3).toBe(true);
  });

  it("옛 메시지(group 칸 없음)는 부분이 아니다", () => {
    expect(isPart(msg("m", "x"))).toBe(false);
    expect(isPart({ group_id: "g", group_index: null, group_size: 2 })).toBe(false);
  });
});

describe("processBoundaries — 묶음은 경계 하나(도착한 마지막 부분)", () => {
  it("(경계 하나) 부분 셋 중 마지막만 남는다 · 보통 메시지는 그대로", () => {
    const g = { id: "g1", n: 3 };
    const all = [msg("m0", "10:00"), msg("p0", "10:01", { ...g, i: 0 }), msg("p1", "10:01", { ...g, i: 1 }), msg("p2", "10:01", { ...g, i: 2 })];
    expect(processBoundaries(all).map((m) => m.id)).toEqual(["m0", "p2"]);
    const items = timelineItems(all);
    expect(items[1].kind === "group" && boundaryOf(items[1]).id).toBe("p2");
  });

  it("아직 둘만 왔으면 둘째가 경계", () => {
    const g = { id: "g1", n: 3 };
    expect(processBoundaries([msg("p0", "10:01", { ...g, i: 0 }), msg("p1", "10:01", { ...g, i: 1 })]).map((m) => m.id)).toEqual(["p1"]);
  });
});
