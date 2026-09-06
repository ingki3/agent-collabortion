/**
 * Inbox Item(COMPONENTS §2.4 `T0qdqP` · SCREEN §4.6) — 항목 7종.
 *
 * 못 박는 것:
 *   1. **글리프는 심각도, 색은 원인 상태**(리뷰 #03 N4) — 다음 사람이 `attention` 을 빨강으로 통일하지 않도록.
 *   2. 버튼은 **서버가 준 `actions`** 로만 나온다 — 403 을 내는 버튼은 없는 버튼보다 나쁘다.
 *   3. `run_failed` 의 인라인 동작은 "재시도" 가 아니라 **"다시 지시"**(리뷰 #01 C4 — §4.5 와 §4.6 의 모순을
 *      캔버스 쪽으로 되돌린 항목이다).
 *   4. `hitl_request` 는 **타임라인과 같은 하위 컴포넌트**로 본문을 그린다(F2) — 세션을 열지 않고 답한다.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ACTION_LABEL, InboxItemCard, TONE_BY_TYPE, TYPE_LABEL, extraLine } from "./InboxItemCard";
import type { InboxItem } from "@/lib/api/types";

afterEach(cleanup);

const item = (over: Partial<InboxItem> = {}): InboxItem => ({
  id: "i1", workspace_id: "w1", type: "hitl_request", severity: "action_required",
  session_id: "s1", session: { id: "s1", title: "시장 조사", status: "active" },
  ref_id: "h1", due_at: "2026-09-07T10:00:00Z", overdue: false, delegated: false,
  card: { title: "타깃 독자가 투자자인가요?", proposed_default: "투자자", hitl_type: "question", agent_name: "Writer" },
  actions: ["answer", "open_session"], read_at: null, created_at: "2026-09-06T10:00:00Z",
  ...over,
});

describe("심각도 배지 — 글리프는 심각도, 색은 원인 상태(리뷰 #03 N4)", () => {
  it("7종 전부에 색 규칙이 있고 심각도 색으로 뭉뚱그리지 않는다", () => {
    // COMPONENTS §2.4 타입별 조합 표를 그대로 옮긴 것이다.
    expect(TONE_BY_TYPE).toEqual({
      hitl_request: "wait",
      lane_blocked: "block",
      session_paused: "pause",
      runtime_offline: "fail",
      run_failed: "fail",
      mention: "run",
      session_completed: "done",
    });
    // 같은 `action_required` 인데 색이 다르다 — 그것이 규칙의 요점이다.
    expect(TONE_BY_TYPE.hitl_request).not.toBe(TONE_BY_TYPE.lane_blocked);
    // 같은 `attention` 인데 색이 다르다.
    expect(TONE_BY_TYPE.session_paused).not.toBe(TONE_BY_TYPE.run_failed);
  });

  it("배지의 글리프는 심각도가 정하고(! ▲ i) tone 은 타입이 덮어쓴다", () => {
    render(<InboxItemCard item={item({ type: "session_paused", severity: "attention", card: { title: "멈췄습니다" }, actions: ["approve_continue", "open_session"] })} />);
    const badge = screen.getByRole("img", { name: "주의" });
    expect(badge.getAttribute("data-value")).toBe("attention");
    expect(badge.getAttribute("data-tone")).toBe("pause");
    expect(badge.textContent).toContain("▲");
  });

  it("7종 이름표가 모두 있다", () => {
    expect(Object.keys(TYPE_LABEL).sort()).toEqual(
      ["hitl_request", "lane_blocked", "mention", "run_failed", "runtime_offline", "session_completed", "session_paused"],
    );
  });
});

describe("버튼은 서버가 준 actions 로만 나온다", () => {
  it("응답 권한이 없으면 '세션 열기' 하나뿐이다(E7-11 · U10-1)", () => {
    render(<InboxItemCard item={item({ actions: ["open_session"] })} onAction={vi.fn()} />);
    expect(screen.queryByTestId("hitl-answer")).toBeNull();
    expect(screen.getByTestId("inbox-action-open_session")).toBeTruthy();
  });

  it("run_failed 의 인라인 동작은 '재시도' 가 아니라 '다시 지시' 다(리뷰 #01 C4)", () => {
    render(<InboxItemCard item={item({ type: "run_failed", severity: "attention", card: { title: "작업이 실패했습니다", failure_kind: "timeout" }, actions: ["restart", "open_session"], due_at: null })} onAction={vi.fn()} />);
    expect(screen.getByTestId("inbox-action-restart").textContent).toBe("다시 지시");
    expect(ACTION_LABEL.restart).toBe("다시 지시");
    expect(Object.values(ACTION_LABEL)).not.toContain("재시도");
  });

  it("runtime_offline 은 Runtimes 로만 보낸다", () => {
    const onAction = vi.fn();
    render(<InboxItemCard item={item({ type: "runtime_offline", severity: "attention", card: { title: "오프라인" }, actions: ["open_runtimes"], due_at: null })} onAction={onAction} />);
    fireEvent.click(screen.getByTestId("inbox-action-open_runtimes"));
    expect(onAction.mock.calls[0][1]).toBe("open_runtimes");
  });
});

describe("hitl_request — 인박스에서 맥락 없이 답한다(F2, U3)", () => {
  it("질문·제안 기본값·기한이 카드 안에 다 있다 — 세션을 열지 않아도 결정할 수 있다", () => {
    render(<InboxItemCard item={item()} onRespond={vi.fn()} onAction={vi.fn()} />);
    expect(screen.getByTestId("hitl-question").textContent).toContain("투자자인가요");
    expect(screen.getByTestId("hitl-proposed-default").textContent).toContain("투자자");
    expect(screen.getByTestId("hitl-due")).toBeTruthy();
    expect(screen.getByTestId("inbox-session").textContent).toContain("시장 조사");
  });

  it("답변을 보내면 계약 HitlResponse 모양으로 나간다", () => {
    const onRespond = vi.fn();
    render(<InboxItemCard item={item()} onRespond={onRespond} />);
    fireEvent.change(screen.getByTestId("hitl-answer-input"), { target: { value: "경영진" } });
    fireEvent.click(screen.getByTestId("hitl-answer"));
    expect(onRespond.mock.calls[0][1]).toEqual({ answer: "경영진" });
  });

  it("approval 이면 승인·거절 둘이 붙는다", () => {
    render(<InboxItemCard item={item({ card: { title: "보고서를 승인해 주세요", hitl_type: "approval" }, actions: ["approve", "reject", "open_session"] })} onRespond={vi.fn()} />);
    expect(screen.getByTestId("hitl-approve")).toBeTruthy();
    expect(screen.getByTestId("hitl-reject")).toBeTruthy();
    expect(screen.getByTestId("inbox-action-open_session")).toBeTruthy();
  });

  it("deputy 위임 항목은 '위임됨 · 지금부터 응답 가능' 이라고 밝힌다(O5, U9-4)", () => {
    render(<InboxItemCard item={item({ delegated: true })} onRespond={vi.fn()} />);
    expect(screen.getByTestId("inbox-item").getAttribute("data-delegated")).toBe("true");
    expect(screen.getByTestId("inbox-extra").textContent).toBe("위임됨 · 지금부터 응답 가능");
  });

  it("overdue 는 기한 칸이 붉게 굵어진다(COMPONENTS §2.4 `ZHNoQ`)", () => {
    render(<InboxItemCard item={item({ overdue: true })} onRespond={vi.fn()} />);
    expect(screen.getByTestId("inbox-item").getAttribute("data-overdue")).toBe("true");
    expect(screen.getByTestId("inbox-due").className).toContain("inbox-item__due--overdue");
  });
});

describe("session_paused — 카드 안에서 금액까지 정한다(U7-1)", () => {
  const paused = (reason: "budget" | "loop") =>
    item({
      type: "session_paused", severity: "attention", due_at: null,
      card: { title: "세션이 멈췄습니다", body: "예산 초과 — $21.40 / $20", paused_reason: reason },
      actions: ["approve_continue", "open_session"],
    });

  it("예산이면 새 상한 입력이 붙고 그 값이 resumeSession 본문으로 간다", () => {
    const onApproveContinue = vi.fn();
    render(<InboxItemCard item={paused("budget")} onApproveContinue={onApproveContinue} onAction={vi.fn()} />);
    // U7 성공 기준: "1단계 카드만으로 얼마를 얼마로 올릴지 결정 가능" — 입력이 없으면 세션을 열게 된다.
    fireEvent.change(screen.getByTestId("inbox-budget-input"), { target: { value: "30" } });
    fireEvent.click(screen.getByTestId("inbox-action-approve_continue"));
    expect(onApproveContinue.mock.calls[0][1]).toEqual({ budget_usd: 30 });
  });

  it("비워 두면 금액 없이 재개한다 — 계약이 '생략 시 현재 상한' 을 허용한다", () => {
    const onApproveContinue = vi.fn();
    render(<InboxItemCard item={paused("budget")} onApproveContinue={onApproveContinue} />);
    fireEvent.click(screen.getByTestId("inbox-action-approve_continue"));
    expect(onApproveContinue.mock.calls[0][1]).toEqual({});
  });

  it("예산이 아닌 사유에는 금액 입력을 만들지 않는다 — 루프·시간은 올릴 금액이 없다", () => {
    render(<InboxItemCard item={paused("loop")} onApproveContinue={vi.fn()} />);
    expect(screen.queryByTestId("inbox-budget-input")).toBeNull();
    expect(screen.getByTestId("inbox-action-approve_continue")).toBeTruthy();
  });
});

describe("부가 텍스트(COMPONENTS §2.4 `fDXjQ`, 기본 끔)", () => {
  it("타입마다 '더 알아야 할 한 줄' 이 다르고 없으면 자리를 만들지 않는다", () => {
    expect(extraLine(item({ type: "mention", card: { title: "멘션" } }))).toBeNull();
    // hitl_request 의 제안 기본값은 **본문이 이미 그린다** — 부가 칸이 되풀이하면 같은 문장이 두 줄이 된다.
    expect(extraLine(item())).toBeNull();
    expect(screen.queryByTestId("inbox-extra")).toBeNull();
    expect(extraLine(item({ type: "session_paused", card: { paused_reason: "budget" } }))).toContain("budget");
    expect(extraLine(item({ type: "run_failed", card: { failure_kind: "timeout" } }))).toContain("timeout");
    expect(extraLine(item({ type: "session_completed", card: { summary: "결정 3건" } }))).toBe("결정 3건");
  });
});
