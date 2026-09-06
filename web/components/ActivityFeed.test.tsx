/**
 * 활동 피드 5클래스 — `contracts/task_event.schema.json` 의 `x-render-class` 를 고정한다.
 * 규칙 배열은 **적힌 순서대로 first-match**(evaluation), 실패한 셸 명령은 error 카드가 아니라 **실패 강조된 shell 카드**다(failed_emphasis).
 */
import { describe, expect, it, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ActivityFeed } from "./ActivityFeed";
import { renderClass, renderClassWithCut } from "@/lib/feed";
import type { TaskEvent } from "@/lib/api/types";

afterEach(cleanup);

let seq = 0;
function ev(over: Partial<TaskEvent> & Pick<TaskEvent, "class">): TaskEvent {
  seq += 1;
  return {
    id: `e${seq}`, task_id: "t1", attempt: 1, seq, verb: null, object_ref: null, outcome: null, payload: null,
    tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: null,
    created_at: "2026-09-06T10:00:00Z", ...over,
  };
}

describe("renderClass — 계약 rules 배열 first-match", () => {
  it("다섯 클래스가 각자의 규칙으로 갈린다", () => {
    expect(renderClass({ class: "message", verb: "say", outcome: "ok" })).toBe("message");
    expect(renderClass({ class: "status", verb: "post_message", outcome: "ok" })).toBe("platform");
    expect(renderClass({ class: "tool", verb: "edit_file", outcome: "ok" })).toBe("file_edit");
    expect(renderClass({ class: "tool", verb: "run_shell", outcome: "ok" })).toBe("shell");
    expect(renderClass({ class: "runtime", verb: "error", outcome: "failed" })).toBe("error");
    expect(renderClass({ class: "tool", verb: "permission", outcome: "rejected" })).toBe("error");
    expect(renderClass({ class: "tool", verb: "read", outcome: "ok" })).toBe("raw");
  });

  it("실패한 셸 명령은 shell 이다 — error 는 전용 렌더러가 없는 실패만 잡는다", () => {
    expect(renderClass({ class: "tool", verb: "run_shell", outcome: "failed" })).toBe("shell");
    expect(renderClass({ class: "tool", verb: "edit_file", outcome: "failed" })).toBe("file_edit");
    expect(renderClass({ class: "usage", verb: "report", outcome: "failed" })).toBe("error");
  });

  it("컷 1 은 배열에서 두 규칙을 뺀다 — 그러면 그 실패는 error 로 떨어진다", () => {
    expect(renderClassWithCut({ class: "tool", verb: "run_shell", outcome: "ok" }, true)).toBe("raw");
    expect(renderClassWithCut({ class: "tool", verb: "run_shell", outcome: "failed" }, true)).toBe("error");
    expect(renderClassWithCut({ class: "tool", verb: "edit_file", outcome: "failed" }, true)).toBe("error");
    expect(renderClassWithCut({ class: "message", verb: "say", outcome: "ok" }, true)).toBe("message");
  });
});

describe("ActivityFeed — 5클래스가 각각 렌더된다", () => {
  const events = [
    ev({ class: "message", verb: "say", outcome: "ok", sentence: "Lead 가 계획을 말했다 → ok", payload: { kind: "text", text: "3단계로 진행합니다" } }),
    ev({ class: "status", verb: "post_message", outcome: "ok", object_ref: "m-1", sentence: "Lead 가 메시지를 게시했다 → ok", payload: { command: "message post", result_ref: "m-1" } }),
    ev({ class: "tool", verb: "edit_file", outcome: "ok", object_ref: "src/app.ts", sentence: "Engineer 가 src/app.ts 를 고쳤다 → ok", payload: { tool_call_id: "c1", kind: "edit", path: "src/app.ts", lines_added: 12, lines_removed: 3 } }),
    ev({ class: "tool", verb: "run_shell", outcome: "failed", object_ref: "npm", sentence: "Engineer 가 npm test 를 돌렸다 → failed", payload: { tool_call_id: "c2", kind: "execute", command: "npm test", exit_code: 1 } }),
    ev({ class: "runtime", verb: "error", outcome: "failed", sentence: "런타임 오류 → failed", payload: { failure_kind: "quota", detail: "쿼터 초과" } }),
    ev({ class: "tool", verb: "read", outcome: "ok", object_ref: "README.md", sentence: "README.md 를 읽었다 → ok", payload: { tool_call_id: "c3", kind: "read" } }),
  ];

  it("message · platform · file_edit · shell · error · raw 가 모두 자기 행으로 그려진다", () => {
    render(<ActivityFeed events={events} />);
    for (const cls of ["message", "platform", "file_edit", "shell", "error", "raw"]) {
      expect(screen.getByTestId(`feed-row-${cls}`), `${cls} 행이 없다`).not.toBeNull();
    }
    expect(screen.getByTestId("feed-diff").textContent).toBe("+12 -3");
    expect(screen.getByTestId("feed-command").textContent).toBe("npm test");
    expect(screen.getByTestId("feed-error-detail").textContent).toContain("quota");
    expect(screen.getByTestId("feed-result-ref").textContent).toBe("m-1");
  });

  it("실패한 셸 행은 shell 이면서 data-outcome=failed 로 강조된다(실패는 크게)", () => {
    render(<ActivityFeed events={events} />);
    const shell = screen.getByTestId("feed-row-shell");
    expect(shell.getAttribute("data-render-class")).toBe("shell");
    expect(shell.getAttribute("data-outcome")).toBe("failed");
    expect(screen.getByTestId("feed-has-failure")).not.toBeNull();
  });

  it("진행 중(started)은 제자리 갱신 — 같은 tool_call_id 는 한 행이고 상태 줄을 쌓지 않는다", () => {
    const started = ev({ class: "tool", verb: "run_shell", outcome: "started", sentence: "npm test 를 돌리는 중…", payload: { tool_call_id: "x1", kind: "execute", command: "npm test" } });
    const done = ev({ class: "tool", verb: "run_shell", outcome: "ok", sentence: "npm test 를 돌렸다 → ok", payload: { tool_call_id: "x1", kind: "execute", command: "npm test" } });
    const { rerender } = render(<ActivityFeed events={[started]} />);
    expect(screen.getAllByTestId("feed-row-shell")).toHaveLength(1);
    expect(screen.getByTestId("feed-pending")).not.toBeNull();
    rerender(<ActivityFeed events={[started, done]} />);
    const rows = screen.getAllByTestId("feed-row-shell");
    expect(rows).toHaveLength(1);
    expect(rows[0].getAttribute("data-outcome")).toBe("ok");
    expect(screen.queryByTestId("feed-pending")).toBeNull();
  });

  it("컷 1 이면 shell·file_edit 행이 raw 로, 그 실패는 error 로 그려진다", () => {
    render(<ActivityFeed events={events} cut1 />);
    expect(screen.queryByTestId("feed-row-shell")).toBeNull();
    expect(screen.queryByTestId("feed-row-file_edit")).toBeNull();
    expect(screen.getAllByTestId("feed-row-error")).toHaveLength(2); // 셸 실패 + runtime/error
  });

  it("침묵도 렌더한다 — 이벤트가 없으면 '대기 중…', 구조화 이벤트가 없으면 그 사실을 말한다", () => {
    const { rerender } = render(<ActivityFeed events={[]} />);
    expect(screen.getByTestId("feed-empty").textContent).toContain("대기 중");
    rerender(<ActivityFeed events={events} structured={false} />);
    expect(screen.getByTestId("feed-degraded").textContent).toContain("툴 단위 로그를 제공하지 않습니다");
  });

  it("원본 레일 토글로 가공 없는 출력을 본다", () => {
    render(<ActivityFeed events={events} />);
    expect(screen.queryByTestId("activity-rail")).toBeNull();
    fireEvent.click(screen.getByTestId("feed-raw-toggle"));
    expect(screen.getByTestId("activity-rail")).not.toBeNull();
  });
});
