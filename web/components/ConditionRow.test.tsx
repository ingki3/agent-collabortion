/**
 * Condition Row — S7 진행률 한 줄이 **사람 말**로 답한다(T-W15, S-84 · SCREEN §4.5 "종료 조건 진행률").
 *
 * 세 이름: "보고서 제출" · "Lead 의 검토 승인" · "Director 승인"(+ "수동 종료"). 충족이면 누가·언제("Writer, 9/13"), 아니면 다음 행동
 * ("Lead 차례" · "받은 요청에서 승인하세요" — 확인 요청이 있으면 그 카드로 가는 링크). `blocked_reason` 이면 ✗ 대신 **이유 문장**.
 * Director 가 이 칸만 보고 "왜 안 닫히는지" 알아야 한다 — 실사용에서 ✗ 만 보여 원인을 못 찾았다.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ConditionRow, progressLine, shortDate } from "./ConditionRow";
import { BLOCKED_REASON } from "@/lib/wording";

afterEach(cleanup);

const name = () => screen.getByTestId("condition-name").textContent;
const line = () => screen.getByTestId("condition-line").textContent;

describe("ConditionRow — 사람 말 3종", () => {
  it("artifact_submitted → 보고서 제출 · 미충족이면 지정 에이전트 차례", () => {
    render(<ConditionRow type="artifact_submitted" met={false} agentName="Writer" nextActor="Writer" />);
    expect(name()).toBe("보고서 제출");
    expect(line()).toBe("Writer 차례");
  });

  it("agent_approval → <에이전트> 의 검토 승인 · 리뷰어 차례", () => {
    render(<ConditionRow type="agent_approval" met={false} agentName="Lead" nextActor="Lead" />);
    expect(name()).toBe("Lead 의 검토 승인");
    expect(line()).toBe("Lead 차례");
  });

  it("user_approval → Director 승인 · 받은 요청에서 승인하세요(확인 요청이 없으면 링크가 아니다)", () => {
    render(<ConditionRow type="user_approval" met={false} />);
    expect(name()).toBe("Director 승인");
    expect(line()).toBe("받은 요청에서 승인하세요");
    expect(screen.queryByTestId("condition-hitl-link")).toBeNull();
  });

  it("manual → 수동 종료 · Director 가 「종료」 로 끝냅니다", () => {
    render(<ConditionRow type="manual" met={false} />);
    expect(name()).toBe("수동 종료");
    expect(line()).toBe("Director 가 「종료」 로 끝냅니다");
  });

  it("계약 enum 그대로가 화면에 새지 않는다", () => {
    render(<ConditionRow type="agent_approval" met={false} agentName="Lead" />);
    expect(screen.getByTestId("condition-row").textContent).not.toMatch(/agent_approval|artifact_submitted|user_approval/);
  });
});

describe("ConditionRow — 충족 · 막힘 · 링크", () => {
  it("충족이면 ✓ 와 (누가, 날짜) — 'Writer, 9/13'", () => {
    render(<ConditionRow type="artifact_submitted" met metBy="Writer" metAt="2026-09-13T10:00:00Z" />);
    expect(screen.getByTestId("condition-row").getAttribute("data-met")).toBe("true");
    expect(screen.getByTestId("condition-row").textContent).toContain("✓");
    expect(line()).toBe("Writer, 9/13");
  });

  it("blocked_reason 이면 ✗ 대신 이유 문장 — 리뷰어 없음", () => {
    render(<ConditionRow type="agent_approval" met={false} blockedReason="reviewer_missing" />);
    const row = screen.getByTestId("condition-row");
    expect(row.getAttribute("data-blocked")).toBe("reviewer_missing");
    expect(row.textContent).toContain("⚠");
    expect(row.textContent).not.toContain("✗");
    expect(line()).toBe("리뷰어가 지정되지 않아 아무도 승인할 수 없습니다");
    // 리뷰어가 없으니 "차례" 도 없다 — 다음 행동은 조건 고치기다.
    expect(row.textContent).not.toContain("차례");
  });

  it("계약 enum 셋 전부에 이유 문장이 있고 모르는 값도 문장으로 답한다", () => {
    expect(Object.keys(BLOCKED_REASON).sort()).toEqual(["agent_archived", "reviewer_missing", "reviewer_not_participant"]);
    for (const r of Object.keys(BLOCKED_REASON)) expect(progressLine({ type: "agent_approval", met: false, blockedReason: r })).toMatch(/승인할 수 없습니다$/);
    expect(progressLine({ type: "agent_approval", met: false, blockedReason: "something_new" })).toMatch(/충족될 수 없는 조건/);
  });

  it("user_approval 에 hitl_request_id 가 있으면 두 번째 줄이 그 카드로 가는 링크다", () => {
    const open = vi.fn();
    render(<ConditionRow type="user_approval" met={false} hitlRequestId="h-1" onOpenHitl={open} />);
    const link = screen.getByTestId("condition-hitl-link");
    expect(link.textContent).toBe("받은 요청에서 승인하세요");
    fireEvent.click(link);
    expect(open).toHaveBeenCalledWith("h-1");
  });

  it("충족된 user_approval 은 링크가 아니고 Director 가 충족시켰다", () => {
    render(<ConditionRow type="user_approval" met metBy="Director" metAt="2026-09-13T10:00:00Z" hitlRequestId="h-1" onOpenHitl={vi.fn()} />);
    expect(screen.queryByTestId("condition-hitl-link")).toBeNull();
    expect(line()).toBe("Director, 9/13");
  });

  it("shortDate — 월/일, 없거나 깨지면 null", () => {
    expect(shortDate("2026-09-13T10:00:00Z")).toBe("9/13");
    expect(shortDate(null)).toBeNull();
    expect(shortDate("nope")).toBeNull();
  });
});

describe("ConditionRow — 마법사 변형", () => {
  it("설명 줄이 무엇을 하면 충족되는지 말하고, 리뷰어 이름이 들어가면 이름으로 부른다", () => {
    render(<ConditionRow type="agent_approval" met={null} variant="wizard" selected agentName="Lead" onToggle={vi.fn()} />);
    expect(name()).toBe("Lead 의 검토 승인");
    expect(line()).toContain("리뷰어로 고른 에이전트가 검토를 승인하면");
  });
});
