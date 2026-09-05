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
    expect(deriveAgentStatus({ taskStatuses: ["dispatched"] })).toBe("working");
    expect(deriveAgentStatus({ taskStatuses: ["waiting_human", "completed"] })).toBe("waiting_human");
    expect(deriveAgentStatus({ taskStatuses: ["completed", "failed"] })).toBe("idle");
    expect(deriveAgentStatus({})).toBe("idle");
  });

  it("blocked · paused lane 은 파생에 들어가지 않는다 — 그 에이전트는 idle (C1′)", () => {
    expect(deriveAgentStatus({ taskStatuses: ["paused"] })).toBe("idle");
    expect(deriveAgentStatus({ taskStatuses: ["queued"] })).toBe("idle");
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
