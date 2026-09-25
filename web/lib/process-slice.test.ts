/**
 * T-FEED A·B(Director 요청 2026-09-25) — 메시지의 「작업 과정」은 그 메시지까지의 조각.
 * 실측: Lead 턴 하나가 위임 메시지를 올린 뒤에도 같은 턴에서 103줄을 더 했고, 웹은 그 전부를 옛 메시지 아래에 그렸다.
 */
import { describe, expect, it } from "vitest";
import { eventsInWindow, messageCut, roomProcessSlices, sliceProcess, workingTasks } from "./process-slice";
import { summarizeProcess } from "./message-layers";
import type { Lane, Task, TaskEvent } from "@/lib/api/types";

const T0 = Date.parse("2026-09-25T13:14:00Z");
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();
let seq = 0;
const ev = (min: number, over: Partial<TaskEvent> = {}): TaskEvent => ({
  id: `e${++seq}`, task_id: "t1", attempt: 1, seq, class: "tool", verb: "run_shell", object_ref: null, outcome: "ok", payload: null,
  tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: null, created_at: at(min), ...over,
} as TaskEvent);
/** 서버 router/service.go — 메시지 게시는 status/post_message(result_ref = message id), seq 는 ServerSeqBase(2^30)부터. */
const post = (min: number, msgId: string) => ev(min, { class: "status", verb: "post_message", object_ref: msgId, seq: 2 ** 30 + seq, payload: { command: "message post", result_ref: msgId } });
const msg = (id: string, min: number) => ({ id, created_at: at(min + 0.01), source_task_id: "t1", author_type: "agent" as const });

/** 한 턴 — 시작 · 셸 2 · 메시지 A · 셸 3 · 메시지 B · 셸 4(꼬리). */
function turn(ended: boolean) {
  seq = 0;
  const events = [
    ev(0, { class: "runtime", verb: "start", outcome: "started" }),
    ev(1), ev(2),
    post(3, "mA"),
    ev(4), ev(5), ev(6),
    post(7, "mB"),
    ev(8), ev(9), ev(10), ev(11),
    ...(ended ? [ev(12, { class: "runtime", verb: "turn_end", outcome: "ok" })] : []),
  ];
  return events;
}
const shells = (evs: TaskEvent[]) => evs.filter((e) => e.verb === "run_shell").length;

describe("sliceProcess — 한 턴 메시지 2개 + 뒤 작업", () => {
  it("각 메시지 조각은 겹치지 않고, 이어 붙이면 턴 전체다(끝난 턴 — 꼬리는 마지막 메시지에)", () => {
    const events = turn(true);
    const s = sliceProcess(events, [msg("mB", 7), msg("mA", 3)], false);
    const a = eventsInWindow(events, s.byMessage.get("mA"));
    const b = eventsInWindow(events, s.byMessage.get("mB"));
    expect(shells(a)).toBe(2);
    expect(shells(b)).toBe(3 + 4); // 끝난 턴 — 꼬리 넷이 마지막 메시지로
    expect(a.some((e) => b.includes(e))).toBe(false);
    expect(new Set([...a, ...b]).size).toBe(events.length);
    expect(s.tail).toBeNull();
    // 자기 post_message 는 자기 조각에(경계 포함), 앞 메시지의 것은 들지 않는다.
    expect(a.map((e) => e.object_ref)).toContain("mA");
    expect(b.map((e) => e.object_ref)).not.toContain("mA");
  });

  it("도는 턴 — 꼬리는 「작업 중」 줄 몫이고 마지막 메시지 조각에 들지 않는다", () => {
    const events = turn(false);
    const s = sliceProcess(events, [msg("mA", 3), msg("mB", 7)], true);
    expect(shells(eventsInWindow(events, s.byMessage.get("mA")))).toBe(2);
    expect(shells(eventsInWindow(events, s.byMessage.get("mB")))).toBe(3);
    expect(shells(eventsInWindow(events, s.tail))).toBe(4);
  });

  it("요약 줄(동작 수·시간)도 조각으로 센다 — 메시지 A 는 셸 2회 · 2분, task 전체(셸 9회 · 12분)가 아니다", () => {
    const events = turn(true);
    const s = sliceProcess(events, [msg("mA", 3), msg("mB", 7)], false);
    const sum = summarizeProcess({ events, structured: true, loading: false }, s.byMessage.get("mA"));
    expect(sum.state).toBe("ready");
    if (sum.state === "ready") {
      expect(sum.top[0].n).toBe(2); // 셸 2회(+ 메시지 게시 1회)
      expect(sum.duration).toEqual([{ text: expect.anything(), n: 3 }]);
    }
    const whole = summarizeProcess({ events, structured: true, loading: false });
    if (whole.state === "ready") expect(whole.top[0].n).toBe(9);
    else throw new Error(whole.state);
  });

  it("post_message 줄이 없으면 message.created_at 이 경계", () => {
    const events = turn(true).filter((e) => e.verb !== "post_message");
    expect(messageCut(events, { id: "mA", created_at: at(3.5) })).toBe(Date.parse(at(3.5)));
    const s = sliceProcess(events, [msg("mA", 3.5), msg("mB", 7.5)], false);
    expect(shells(eventsInWindow(events, s.byMessage.get("mA")))).toBe(2);
  });

  it("경계에 걸친 도구 호출(started 는 앞, ok 는 뒤)은 처음 줄의 조각 한 곳에만", () => {
    seq = 0;
    const events = [
      ev(1, { outcome: "started", payload: { tool_call_id: "c1" } }),
      post(2, "mA"),
      ev(3, { outcome: "ok", payload: { tool_call_id: "c1" } }),
      ev(4),
    ];
    const s = sliceProcess(events, [msg("mA", 2), msg("mB", 5)], false);
    const a = eventsInWindow(events, s.byMessage.get("mA"));
    const b = eventsInWindow(events, s.byMessage.get("mB"));
    expect(a.filter((e) => (e.payload as { tool_call_id?: string } | null)?.tool_call_id === "c1")).toHaveLength(2);
    expect(b.some((e) => (e.payload as { tool_call_id?: string } | null)?.tool_call_id === "c1")).toBe(false);
  });
});

describe("workingTasks · roomProcessSlices — 방 화면 배선", () => {
  const task = (id: string, status: Task["status"]): Task => ({ id, status, attempt: 1 } as Task);
  const lane = (agent: string, t: Task, status: Lane["status"] = "running", updated = at(0)): Lane =>
    ({ id: `l-${t.id}`, agent_id: agent, status, current_task: t, updated_at: updated } as Lane);

  it("도는 lane 의 도는 턴만, 에이전트마다 하나(가장 최근)", () => {
    const recs = { t1: { events: turn(false) }, t2: { events: turn(false) }, t3: { events: turn(true) }, t4: { events: turn(false) } };
    const w = workingTasks([
      lane("lead", task("t1", "running"), "running", at(1)),
      lane("lead", task("t2", "running"), "running", at(2)),
      lane("res", task("t3", "running")), // turn_end 가 먼저 왔다 — task.updated 보다 빨라도 끝난 턴
      lane("wri", task("t4", "running"), "done"),
    ], recs);
    expect(w).toEqual([{ taskId: "t2", agentId: "lead" }]);
  });

  it("「작업 중」 줄이 없는 task 는 꼬리를 떼지 않는다 — 떼면 어디에도 안 보인다", () => {
    const events = turn(false);
    const msgs = [msg("mA", 3), msg("mB", 7)];
    const off = roomProcessSlices({ t1: { events } }, msgs, new Set()).get("t1")!;
    expect(off.tail).toBeNull();
    expect(shells(eventsInWindow(events, off.byMessage.get("mB")))).toBe(7);
    const on = roomProcessSlices({ t1: { events } }, msgs, new Set(["t1"])).get("t1")!;
    expect(shells(eventsInWindow(events, on.byMessage.get("mB")))).toBe(3);
  });
});
