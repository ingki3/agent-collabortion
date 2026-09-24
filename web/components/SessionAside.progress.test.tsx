/**
 * S7 우열 「종료 조건 진행률」(T-W15, S-84 · SCREEN §4.5) — 상단 한 줄("남은 것: Director 승인 1개 · 막힘 1개"), 행마다 사람 말, 막힌
 * 조건이면 이유 + Director 의 「조건 고치기」. 세션이 왜 안 닫히는지 이 칸만 보고 알 수 있어야 한다.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SessionAside, metByName } from "./SessionAside";
import type { CompletionProgress, Session } from "@/lib/api/types";

afterEach(cleanup);

const base: Session = {
  id: "s1", workspace_id: "w1", title: "결제 시장 조사", goal: "보고서", acceptance_criteria: [], director_user_id: "u1",
  director: { id: "u1", email: "d@x", display_name: "Director", avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
  deputy_director_user_id: null, assignee_agent_id: "a-writer", runtime_id: null, isolation: { kind: "none", remote_url: null },
  completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval" }, { type: "user_approval" }] },
  completion_progress: { met: 0, total: 0, satisfied: false, human_gate: true, conditions: [] },
  limits: { budget_usd: 20, budget_tokens: null, time_limit: "PT4H", max_tasks: null, max_parallel_lanes: 5 }, autonomy: "guided",
  status: "active", paused_reason: null, cost_usd: 0, cost_estimated: false, participants: [], context: [], my_role: "director",
  created_by: "u1", created_at: "2026-09-14T08:00:00Z", updated_at: "2026-09-14T08:00:00Z", started_at: null, finished_at: null, last_activity_at: null,
};
const NAMES: Record<string, string> = { "a-writer": "Writer", "a-lead": "Lead" };
const agentName = (id: string) => NAMES[id] ?? id.slice(0, 8);

/** Director 실사용의 그 세션 — 보고서는 제출됐는데 리뷰어 없는 검토 승인에 걸려 안 닫힌다. */
const blocked: CompletionProgress = {
  met: 1, total: 3, satisfied: false, human_gate: true,
  conditions: [
    { path: "/conditions/0", type: "artifact_submitted", met: true, met_at: "2026-09-13T10:00:00Z", met_by: "a-writer", agent_id: "a-writer", agent_name: "Writer", next_actor: null, blocked_reason: null, hitl_request_id: null },
    { path: "/conditions/1", type: "agent_approval", met: false, met_at: null, met_by: null, agent_id: null, agent_name: null, next_actor: null, blocked_reason: "reviewer_missing", hitl_request_id: null },
    { path: "/conditions/2", type: "user_approval", met: false, met_at: null, met_by: null, agent_id: null, agent_name: null, next_actor: "director", blocked_reason: null, hitl_request_id: "h-1" },
  ],
};
const normal: CompletionProgress = {
  ...blocked,
  conditions: [blocked.conditions[0], { ...blocked.conditions[1], agent_id: "a-lead", agent_name: "Lead", next_actor: "Lead", blocked_reason: null }, blocked.conditions[2]],
};

function mount(prog: CompletionProgress, over: Partial<Session> = {}, props: Partial<React.ComponentProps<typeof SessionAside>> = {}) {
  render(<SessionAside session={{ ...base, ...over, completion_progress: prog }} artifacts={[]} decisions={[]} agentName={agentName} {...props} />);
}

describe("진행률 — 정상", () => {
  it("행마다 사람 말 한 줄: 보고서 제출 ✓ (Writer, 9/13) · Lead 의 검토 승인 — Lead 차례 · Director 승인 — 받은 요청에서(링크)", () => {
    const open = vi.fn();
    mount(normal, {}, { onOpenHitl: open, onFixCondition: vi.fn() });
    const rows = screen.getAllByTestId("condition-row");
    expect(rows.map((r) => r.querySelector('[data-testid="condition-name"]')!.textContent)).toEqual(["아티팩트 제출", "Lead 의 검토 승인", "Director 승인"]);
    expect(rows[0].textContent).toContain("Writer, 9/13");
    expect(rows[1].textContent).toContain("Lead 차례");
    fireEvent.click(screen.getByTestId("condition-hitl-link"));
    expect(open).toHaveBeenCalledWith("h-1");
    expect(screen.getByTestId("progress-count").textContent).toBe("1/3");
  });

  it("상단 한 줄 — 남은 것: Lead 의 검토 승인 1개 · Director 승인 1개 (이름마다 개수, W-20)", () => {
    mount(normal);
    expect(screen.getByTestId("progress-summary").textContent).toBe("남은 것: Lead 의 검토 승인 1개 · Director 승인 1개");
    expect(screen.queryByTestId("progress-blocked")).toBeNull();
  });

  it("막힌 것이 없어도 Director 에게는 「조건 고치기」 가 조용한 링크로 있다(active 에서도 고칠 수 있다, v0.1.4)", () => {
    const fix = vi.fn();
    mount(normal, {}, { onFixCondition: fix });
    fireEvent.click(screen.getByTestId("fix-condition-open"));
    expect(fix).toHaveBeenCalled();
  });

  it("전부 충족이면 '곧 완료' · 끝난 세션이면 '미션이 끝났습니다' 이고 버튼이 없다", () => {
    mount({ ...normal, met: 3, satisfied: true, conditions: normal.conditions.map((c) => ({ ...c, met: true })) }, {}, { onFixCondition: vi.fn() });
    expect(screen.getByTestId("progress-summary").textContent).toBe("조건을 모두 충족했습니다 — 곧 완료됩니다");
    cleanup();
    mount(blocked, { status: "completed" }, { onFixCondition: vi.fn() });
    expect(screen.getByTestId("progress-summary").textContent).toBe("미션이 끝났습니다");
    expect(screen.queryByTestId("fix-condition-open")).toBeNull();
  });
});

describe("진행률 — 막힘(리뷰어 없는 옛 세션)", () => {
  it("상단 한 줄 — 남은 것: Director 승인 1개 · 막힘 1개", () => {
    mount(blocked);
    expect(screen.getByTestId("progress-summary").textContent).toBe("남은 것: Director 승인 1개 · 막힘 1개");
  });

  it("막힌 행은 ✗ 대신 이유 문장이고, Director 에게 「조건 고치기」 버튼이 붙는다", () => {
    const fix = vi.fn();
    mount(blocked, {}, { onFixCondition: fix });
    const row = screen.getAllByTestId("condition-row")[1];
    expect(row.getAttribute("data-blocked")).toBe("reviewer_missing");
    expect(row.textContent).toContain("리뷰어가 지정되지 않아 아무도 승인할 수 없습니다");
    expect(row.textContent).not.toContain("✗");
    const box = screen.getByTestId("progress-blocked");
    expect(box.textContent).toContain("조건을 고쳐야 미션이 끝날 수 있습니다");
    fireEvent.click(screen.getByTestId("fix-condition-open"));
    expect(fix).toHaveBeenCalled();
  });

  it("Director 가 아니면 버튼 대신 'Director 가 조건을 고쳐야' 한 줄", () => {
    mount(blocked, { my_role: "member" });
    expect(screen.getByTestId("progress-blocked").textContent).toContain("Director 가 조건을 고쳐야 미션이 끝날 수 있습니다");
    expect(screen.queryByTestId("fix-condition-open")).toBeNull();
  });

  it("OR 면 '하나만 충족하면 끝' 을 덧붙인다", () => {
    mount(blocked, { completion_condition: { op: "or", conditions: [] } });
    expect(screen.getByTestId("progress-summary").textContent).toBe("남은 것: Director 승인 1개 · 막힘 1개 — 하나만 충족하면 끝");
  });
});

describe("metByName — id 가 화면에 새지 않는다(§8.4)", () => {
  it("사람 조건은 Director, 에이전트 조건은 agent_name, 없으면 이름표로 풀고, 못 풀면 null", () => {
    expect(metByName("user_approval", "u1", null)).toBe("Director");
    expect(metByName("manual", "u1", null)).toBe("Director");
    expect(metByName("artifact_submitted", "a-writer", "Writer")).toBe("Writer");
    expect(metByName("artifact_submitted", "a-writer", null, agentName)).toBe("Writer");
    expect(metByName("artifact_submitted", "zzzz-unknown-id", null, agentName)).toBeNull();
    expect(metByName("artifact_submitted", "platform", null, agentName)).toBeNull();
  });
});
