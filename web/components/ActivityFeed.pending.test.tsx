/**
 * T-FEED(Director 요청 2026-09-25) — 「진행 중…」이 끝난 턴에도 남던 결함.
 * 실측: task completed, seq1 `runtime/start` outcome=started, seq65 `runtime/turn_end` ok, superseded_by 전부 null →
 * 옛 판정(`outcome === "started"`)은 첫 줄에 「진행 중…」을 영영 붙였다.
 *
 * (a) 같은 tool_call_id 의 뒤 줄이 있으면 그 결과로 제자리 갱신
 * (b) runtime/start 는 같은 attempt 의 runtime/turn_end · error · cancel 이 있으면 끝남
 * (c) task 가 끝난 상태거나 그 attempt 가 끝났으면 어떤 줄에도 「진행 중」 없음 — 짝 없는 도구 started 는 「결과 없음」
 * (d) 실행 중 턴은 여전히 「진행 중…」
 */
import { describe, expect, it, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ActivityFeed } from "./ActivityFeed";
import { LIVE_TASK_STATUSES, pendingJudge } from "@/lib/feed";
import { FEED_ROW } from "@/lib/wording";
import type { TaskEvent, TaskStatus } from "@/lib/api/types";

afterEach(cleanup);

let seq = 0;
function ev(over: Partial<TaskEvent> & Pick<TaskEvent, "class">): TaskEvent {
  seq += 1;
  return {
    id: `e${seq}`, task_id: "t1", attempt: 1, seq, verb: null, object_ref: null, outcome: null, payload: null,
    tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: null,
    created_at: "2026-09-25T13:18:49Z", ...over,
  };
}
const start = (o: Partial<TaskEvent> = {}) => ev({ class: "runtime", verb: "start", outcome: "started", ...o });
const turnEnd = (o: Partial<TaskEvent> = {}) => ev({ class: "runtime", verb: "turn_end", outcome: "ok", ...o });
const toolStarted = (id: string, o: Partial<TaskEvent> = {}) =>
  ev({ class: "tool", verb: "read", object_ref: "README.md", outcome: "started", payload: { tool_call_id: id, kind: "read" }, ...o });
const toolDone = (id: string, outcome: string, o: Partial<TaskEvent> = {}) =>
  ev({ class: "tool", verb: "read", object_ref: "README.md", outcome, payload: { tool_call_id: id, kind: "read" }, ...o });

const pendingRows = () => screen.queryAllByTestId("feed-pending").length;
const unresolvedRows = () => screen.queryAllByTestId("feed-unresolved").length;

describe("T-FEED 실측 재현 — 끝난 턴의 runtime/start 는 「진행 중」이 아니다", () => {
  it("task completed · start(started) … turn_end(ok) — 어떤 줄에도 「진행 중…」이 없고, runtime/start 에 「결과 없음」도 달지 않는다", () => {
    const events = [start(), ev({ class: "status", verb: "post_message", outcome: "ok" }), turnEnd()];
    render(<ActivityFeed events={events} taskStatus="completed" attempt={1} />);
    expect(pendingRows()).toBe(0);
    expect(unresolvedRows()).toBe(0);
    expect(screen.getAllByText(/runtime\/start/)[0].closest("li")!.getAttribute("data-pending")).toBe("false");
  });
});

describe("(a) 같은 tool_call_id 의 뒤 줄 — 결과로 제자리 갱신", () => {
  it.each(["ok", "failed", "cancelled"])("started → %s 는 한 행, 진행 중 아님, 결과 outcome", (outcome) => {
    const events = [toolStarted("c1"), toolDone("c1", outcome)];
    const { container } = render(<ActivityFeed events={events} taskStatus="running" attempt={1} />);
    const rows = container.querySelectorAll("li.feed__row");
    expect(rows).toHaveLength(1);
    expect(rows[0].getAttribute("data-outcome")).toBe(outcome);
    expect(pendingRows()).toBe(0);
    expect(unresolvedRows()).toBe(0);
  });
});

describe("(b) runtime/start 는 같은 attempt 의 turn_end · error · cancel 로 끝난다 — task 상태를 몰라도", () => {
  it.each(["turn_end", "error", "cancel"])("start + runtime/%s → 진행 중 아님", (verb) => {
    render(<ActivityFeed events={[start(), ev({ class: "runtime", verb, outcome: verb === "turn_end" ? "ok" : "failed" })]} />);
    expect(pendingRows()).toBe(0);
  });
  it("다른 attempt 의 turn_end 는 이 attempt 를 끝내지 않는다 — attempt 2 의 start 는 여전히 진행 중(task running)", () => {
    const events = [start({ attempt: 1 }), turnEnd({ attempt: 1 }), start({ attempt: 2 })];
    render(<ActivityFeed events={events} taskStatus="running" attempt={2} />);
    expect(pendingRows()).toBe(1);
  });
  it("같은 attempt 가 끝났으면 그 attempt 의 짝 없는 도구 started 도 「결과 없음」", () => {
    render(<ActivityFeed events={[start(), toolStarted("orphan"), turnEnd()]} />);
    expect(pendingRows()).toBe(0);
    expect(unresolvedRows()).toBe(1);
    expect(screen.getByTestId("feed-unresolved").textContent).toContain(FEED_ROW.unresolved);
  });
});

describe("(c) task 가 끝났거나 attempt 가 지났으면 — 어떤 줄에도 「진행 중」 없음", () => {
  const ENDED: TaskStatus[] = ["completed", "failed", "cancelled", "paused", "queued", "deferred"];
  it.each(ENDED)("task %s · turn_end 없이 끊긴 기록(start + 짝 없는 도구) → 진행 중 0, 도구 줄은 「결과 없음」", (status) => {
    render(<ActivityFeed events={[start(), toolStarted("c9")]} taskStatus={status} attempt={1} />);
    expect(pendingRows()).toBe(0);
    expect(unresolvedRows()).toBe(1);
  });
  it("앞 attempt 의 짝 없는 줄 — 뒤 attempt 가 시작됐으면(이벤트) 끝난 것", () => {
    render(<ActivityFeed events={[start({ attempt: 1 }), toolStarted("old", { attempt: 1 }), start({ attempt: 2 })]} taskStatus="running" />);
    // attempt 2 의 start 만 진행 중. attempt 1 의 두 줄은 끝났다.
    expect(pendingRows()).toBe(1);
    expect(unresolvedRows()).toBe(1);
  });
  it("앞 attempt 의 짝 없는 줄 — task.attempt 가 더 크면(이벤트가 아직 없어도) 끝난 것", () => {
    render(<ActivityFeed events={[start({ attempt: 1 }), toolStarted("old", { attempt: 1 })]} taskStatus="running" attempt={2} />);
    expect(pendingRows()).toBe(0);
  });
  it("LIVE_TASK_STATUSES 는 계약 TaskStatus 중 턴이 도는 넷뿐이다", () => {
    expect([...LIVE_TASK_STATUSES].sort()).toEqual(["dispatched", "preparing", "running", "waiting_human"]);
  });
});

describe("(d) 실행 중인 턴은 지금처럼 「진행 중…」", () => {
  it.each(["running", "waiting_human", "dispatched", "preparing"] as TaskStatus[])("task %s · start + 도구 started(짝 없음) → 둘 다 진행 중", (status) => {
    render(<ActivityFeed events={[start(), toolStarted("live")]} taskStatus={status} attempt={1} />);
    expect(pendingRows()).toBe(2);
    expect(unresolvedRows()).toBe(0);
    expect(screen.getAllByTestId("feed-pending")[0].textContent).toContain(FEED_ROW.pending);
  });
  it("task 상태를 모르고(getTask 실패) 턴 끝 줄도 없으면 — 진행 중(옛 동작 유지)", () => {
    render(<ActivityFeed events={[start(), toolStarted("live")]} />);
    expect(pendingRows()).toBe(2);
  });
  it("실행 중 → task.updated(completed) 로 다시 그리면 진행 중이 사라진다", () => {
    const events = [start(), toolStarted("x")];
    const { rerender } = render(<ActivityFeed events={events} taskStatus="running" attempt={1} />);
    expect(pendingRows()).toBe(2);
    rerender(<ActivityFeed events={events} taskStatus="completed" attempt={1} />);
    expect(pendingRows()).toBe(0);
    expect(unresolvedRows()).toBe(1);
  });
});

describe("pendingJudge — superseded 판은 판정에 쓰지 않는다", () => {
  it("superseded 된 turn_end 는 attempt 를 끝내지 않는다", () => {
    const s = start();
    const judge = pendingJudge([s, turnEnd({ superseded_by: "zzz" })], { taskStatus: "running" });
    expect(judge(s)).toBe(true);
  });
});
