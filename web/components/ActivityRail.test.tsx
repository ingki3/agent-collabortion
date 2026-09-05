import { describe, expect, it, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ActivityRail, foldEvents, latestEvents, toolCallKey } from "./ActivityRail";
import type { TaskEvent } from "@/lib/api/types";

afterEach(cleanup);

type Wire = TaskEvent & { payload?: { tool_call_id?: string; kind?: string } };

function ev(seq: number, over: Partial<Wire> = {}): Wire {
  return {
    id: `e${seq}`,
    task_id: "t1",
    seq,
    class: "tool",
    verb: "edit_file",
    object_ref: { path: "src/a.ts" },
    outcome: "ok",
    created_at: `2026-09-06T14:03:${String(seq).padStart(2, "0")}Z`,
    ...over,
  };
}

describe("foldEvents — SCREEN §4.5 '상태 줄을 쌓지 않는다' (R1)", () => {
  it("같은 tool_call_id 의 started + ok 두 이벤트는 행 1개, 상태 ok", () => {
    const rows = foldEvents([
      ev(1, { outcome: "started", payload: { tool_call_id: "call_1", kind: "edit" } }),
      ev(2, { outcome: "ok", payload: { tool_call_id: "call_1", kind: "edit" } }),
    ]);
    expect(rows).toHaveLength(1);
    expect(rows[0].first.id).toBe("e1");
    expect(rows[0].latest.id).toBe("e2");
    expect(rows[0].latest.outcome).toBe("ok");
    expect(latestEvents([ev(1, { outcome: "started", payload: { tool_call_id: "call_1" } }), ev(2, { payload: { tool_call_id: "call_1" } })]).map((e) => e.outcome)).toEqual(["ok"]);
  });

  it("started → failed 도 제자리 갱신되고, 행의 자리는 처음 이벤트의 seq 를 따른다", () => {
    const rows = foldEvents([
      ev(3, { outcome: "ok", payload: { tool_call_id: "call_b" } }),
      ev(1, { outcome: "started", payload: { tool_call_id: "call_a" } }),
      ev(2, { outcome: "started", payload: { tool_call_id: "call_b" } }),
      ev(4, { outcome: "failed", payload: { tool_call_id: "call_a" } }),
    ]);
    expect(rows.map((r) => r.first.id)).toEqual(["e1", "e2"]);
    expect(rows.map((r) => r.latest.outcome)).toEqual(["failed", "ok"]);
  });

  it("tool_call_id 가 없는 이벤트는 접지 않고, class 가 다르면 같은 id 라도 다른 행이다", () => {
    const rows = foldEvents([
      ev(1, { class: "message", verb: "think", outcome: "started" }),
      ev(2, { class: "message", verb: "think", outcome: "ok" }),
      ev(3, { class: "tool", payload: { tool_call_id: "c" } }),
      ev(4, { class: "permission", verb: "ask", payload: { tool_call_id: "c" } }),
    ]);
    expect(rows).toHaveLength(4);
    expect(toolCallKey(ev(1))).toBeNull();
    expect(toolCallKey(ev(1, { payload: { tool_call_id: "" } }))).toBeNull();
    expect(toolCallKey(ev(3, { payload: { tool_call_id: "c" } }))).toBe("tool:c");
  });

  it("superseded_by 경로는 그대로 — 옛 판은 숨고 새 판만 남는다", () => {
    const rows = foldEvents([
      ev(1, { class: "message", verb: "think", outcome: "started", superseded_by: "e2" }),
      ev(2, { class: "message", verb: "think", outcome: "ok" }),
    ]);
    expect(rows.map((r) => r.latest.id)).toEqual(["e2"]);
  });
});

describe("ActivityRail — 렌더", () => {
  it("started + ok 이벤트 2개를 주면 <li> 1개가 data-outcome=ok 로 그려진다", () => {
    render(
      <ActivityRail
        events={[
          ev(1, { outcome: "started", sentence: "Lead가 src/a.ts 를 고치는 중…", payload: { tool_call_id: "call_1" } }),
          ev(2, { outcome: "ok", sentence: "Lead가 src/a.ts 를 고쳤다 → ok", payload: { tool_call_id: "call_1" } }),
        ]}
      />,
    );
    const rows = screen.getByTestId("activity-rail").querySelectorAll("li.rail__row");
    expect(rows).toHaveLength(1);
    expect(rows[0].getAttribute("data-outcome")).toBe("ok");
    expect(rows[0].getAttribute("data-tool-call-id")).toBe("call_1");
    expect(rows[0].textContent).toContain("고쳤다 → ok");
    expect(rows[0].textContent).not.toContain("고치는 중");
  });
});
