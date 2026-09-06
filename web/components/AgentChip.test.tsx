import { describe, expect, it, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { AgentChip, deriveAgentStatus } from "./AgentChip";

afterEach(cleanup);

describe("deriveAgentStatus — PRD FR-1.3 파생 순서", () => {
  it("disabled > offline > error > working > waiting_human > idle", () => {
    expect(deriveAgentStatus({ disabled: true, offline: true, taskStatuses: ["running"] })).toBe("disabled");
    expect(deriveAgentStatus({ offline: true, error: true, taskStatuses: ["running"] })).toBe("offline");
    expect(deriveAgentStatus({ error: true, taskStatuses: ["running"] })).toBe("error");
    expect(deriveAgentStatus({ taskStatuses: ["completed", "running", "waiting_human"] })).toBe("working");
    // W-5 — `dispatched` 는 아직 턴이 시작되지 않았다. 자세한 경계는 AgentChip.derive.test.tsx
    expect(deriveAgentStatus({ taskStatuses: ["dispatched"] })).toBe("idle");
    expect(deriveAgentStatus({ taskStatuses: ["waiting_human", "completed"] })).toBe("waiting_human");
    expect(deriveAgentStatus({ taskStatuses: ["completed", "failed"] })).toBe("idle");
    expect(deriveAgentStatus({})).toBe("idle");
  });

  it("blocked · paused lane 은 파생에 들어가지 않는다 — 그 에이전트는 idle (C1′)", () => {
    expect(deriveAgentStatus({ taskStatuses: ["paused"] })).toBe("idle");
    expect(deriveAgentStatus({ taskStatuses: ["queued"] })).toBe("idle");
  });
});

describe("AgentChip — 칩을 특정하는 수단", () => {
  // G4 2판 W7: e2e 가 참여자 칩을 하나로 집으려 했는데 셀렉터가 아무것도 못 잡았다.
  // 이름은 화면에 `@` 가 붙어 나오고 워크스페이스 안에서만 유일하다 — id 만이
  // 시나리오가 만든 에이전트와 화면의 칩을 잇는다.
  it("agentId 를 주면 data-agent-id 로 나간다", () => {
    const id = "b7d3f0a2-1c9e-4b55-9f11-2a6c8d4e0f31";
    render(<AgentChip agentId={id} name="Researcher" role="researcher" status="working" />);
    const chip = screen.getByTestId("agent-chip");
    expect(chip.getAttribute("data-agent-id")).toBe(id);
    // 셀렉터가 실제로 이 칩 하나만 집는지까지 본다 — 속성이 있어도 못 집으면 W7 이 그대로다.
    expect(document.querySelectorAll(`[data-agent-id="${id}"]`)).toHaveLength(1);
  });

  it("agentId 가 없으면 속성 자체가 없다 (데모 화면)", () => {
    render(<AgentChip name="Lead" role="lead" status="idle" />);
    expect(screen.getByTestId("agent-chip").hasAttribute("data-agent-id")).toBe(false);
  });

  it("같은 이름이 둘이어도 id 로는 하나씩 집힌다", () => {
    render(<AgentChip agentId="id-1" name="Researcher" status="working" />);
    render(<AgentChip agentId="id-2" name="Researcher" status="idle" />);
    expect(screen.getAllByTestId("agent-chip")).toHaveLength(2);
    expect(document.querySelectorAll('[data-agent-id="id-1"]')).toHaveLength(1);
    expect(document.querySelector('[data-agent-id="id-2"]')?.getAttribute("data-status")).toBe("idle");
  });
});

describe("AgentChip — 파생 상태 표시", () => {
  it("derive 를 주면 계산된 상태 배지를 그린다 (running task → working ●)", () => {
    render(<AgentChip name="Lead" role="lead" derive={{ taskStatuses: ["running"] }} />);
    const chip = screen.getByTestId("agent-chip");
    expect(chip.getAttribute("data-status")).toBe("working");
    const badge = chip.querySelector(".badge")!;
    expect(badge.getAttribute("data-value")).toBe("working");
    expect(badge.querySelector(".badge__glyph")!.textContent).toBe("●");
    expect(chip.textContent).toContain("@Lead");
  });

  it("offline 과 disabled 는 글리프가 다르다 (✕ solid vs ⊘ neutral)", () => {
    render(<AgentChip name="A" status="offline" />);
    render(<AgentChip name="B" status="disabled" />);
    const [a, b] = screen.getAllByTestId("agent-chip");
    expect(a.querySelector(".badge__glyph")!.textContent).toBe("✕");
    expect(a.querySelector(".badge")!.getAttribute("data-variant")).toBe("solid");
    expect(b.querySelector(".badge__glyph")!.textContent).toBe("⊘");
    expect(b.querySelector(".badge")!.getAttribute("data-tone")).toBe("neutral");
  });

  it("부가 텍스트는 상태가 아니라 둘째 줄에 들어간다 (N2) — idle 이면서 'lane #2 질문 대기'", () => {
    render(<AgentChip name="QA" role="reviewer" status="idle" statusNote="lane #2 질문 대기" profile="Hermes · gpt" />);
    const chip = screen.getByTestId("agent-chip");
    expect(chip.getAttribute("data-status")).toBe("idle");
    expect(chip.querySelector(".badge")!.getAttribute("data-value")).toBe("idle");
    expect(screen.getByTestId("agent-chip-line2").textContent).toBe("reviewer · Hermes · gpt · lane #2 질문 대기");
  });

  it("assignee 표시와 sm 크기(둘째 줄 생략)", () => {
    render(<AgentChip name="Lead" role="lead" status="working" isAssignee size="sm" statusNote="숨김" />);
    const chip = screen.getByTestId("agent-chip");
    expect(chip.textContent).toContain("assignee");
    expect(screen.queryByTestId("agent-chip-line2")).toBeNull();
  });
});
