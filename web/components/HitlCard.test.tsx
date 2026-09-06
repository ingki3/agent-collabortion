/**
 * HITL 카드와 그 **공용 하위 컴포넌트**(COMPONENTS §2.3 리뷰 #03 K2 · PLAN §8 결정 6).
 *
 * 여기서 못 박는 것은 세 가지다.
 *   1. 권한 3상태 — `can_respond` / `can_respond_from` 만 읽는다. deputy 는 **비활성 + "🔒 HH:MM부터"**(E7-09,
 *      U9-2)이고 일반 멤버(`can_respond_from: null`)에게는 응답 컨트롤 자체를 내지 않는다(E7-11).
 *      **회귀 주입 대상이 이 규칙이다** — 시점 제한을 지우면 아래 두 케이스가 함께 깨진다.
 *   2. 상태 4종 — open / answered / auto_answered / **cancelled "취소됨"**(계약 HitlStatus, K-4).
 *   3. 시스템 발행 문구 — `purpose` 가 판정 기준이다(0012). `source: system` + `approval` 만으로는
 *      완료 승인과 예산 정지를 구분할 수 없다.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { HitlCard, hitlMetaLine, PURPOSE_REASON } from "./HitlCard";
import { HitlBody, defaultActionsFor } from "./HitlBody";
import type { HitlRequest } from "@/lib/api/types";

afterEach(cleanup);

const T0 = "2026-09-06T10:00:00Z";
const req = (over: Partial<HitlRequest> = {}): HitlRequest => ({
  id: "h1", session_id: "s1", task_id: "t1", lane_id: "l1",
  agent: { id: "a1", name: "Writer" },
  source: "agent", type: "question", purpose: "agent",
  question: "타깃 독자가 투자자인지 내부 경영진인지 알려주세요",
  context: null, options: [], proposed_default: "투자자", artifact_id: null,
  approver_spec: "director",
  due_at: "2026-09-07T10:00:00Z", overdue: false, status: "open",
  approved: null, answer: null, answered_by: null, answered_at: null, budget_override_usd: null,
  can_respond: true, can_respond_from: null, message_id: "m1", created_at: T0,
  ...over,
});

describe("권한 3상태(SCREEN §7 · E7-09·E7-11)", () => {
  it("Director — 버튼이 활성이고 제안 기본값이 보인다(U3 1단계)", () => {
    render(<HitlCard request={req()} onRespond={vi.fn()} />);
    expect(screen.getByTestId("hitl-body").getAttribute("data-permission")).toBe("allowed");
    expect(screen.getByTestId("hitl-proposed-default").textContent).toContain("투자자");
    expect(screen.getByTestId("hitl-answer")).toBeTruthy;
    expect((screen.getByTestId("hitl-answer") as HTMLButtonElement).disabled).toBe(false);
  });

  it("deputy(기한 절반 전) — 버튼은 **보이되 비활성**이고 '🔒 HH:MM부터'가 함께 나온다(U9-2)", () => {
    const from = "2026-09-06T22:00:00Z";
    render(<HitlCard request={req({ can_respond: false, can_respond_from: from })} onRespond={vi.fn()} />);
    expect(screen.getByTestId("hitl-body").getAttribute("data-permission")).toBe("later");
    const btn = screen.getByTestId("hitl-answer") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    // "언제부터 가능한지"가 사유의 핵심이다 — 없으면 사용자는 영영 못 하는 줄 안다.
    expect(btn.title).toContain("부터 응답 가능");
    expect(screen.getByTestId("hitl-lock").textContent).toContain("🔒");
    expect(screen.getByTestId("hitl-gate").textContent).toContain("기한의 절반");
  });

  it("일반 멤버(can_respond_from null) — 응답 컨트롤이 없고 사유만 남는다(E7-11)", () => {
    render(<HitlCard request={req({ can_respond: false, can_respond_from: null })} onRespond={vi.fn()} />);
    expect(screen.getByTestId("hitl-body").getAttribute("data-permission")).toBe("never");
    expect(screen.queryByTestId("hitl-answer")).toBeNull();
    expect(screen.queryByTestId("hitl-lock")).toBeNull();
    // 화면을 숨기지 않는다 — 질문은 보이고 사유가 붙는다(SCREEN §7).
    expect(screen.getByTestId("hitl-question")).toBeTruthy();
    expect(screen.getByTestId("hitl-no-right").textContent).toContain("Director·deputy");
  });

  it("권한이 없는 사람에게 시각을 약속하지 않는다 — 두 케이스의 차이가 곧 계약 문언이다", () => {
    const { unmount } = render(<HitlBody type="question" status="open" question="q" canRespond={false} canRespondFrom="2026-09-06T22:00:00Z" onRespond={vi.fn()} />);
    expect(screen.getByTestId("hitl-body").getAttribute("data-permission")).toBe("later");
    unmount();
    render(<HitlBody type="question" status="open" question="q" canRespond={false} canRespondFrom={null} onRespond={vi.fn()} />);
    expect(screen.getByTestId("hitl-body").getAttribute("data-permission")).toBe("never");
  });
});

describe("타입별 입력부(FR-5.1 · SCREEN §2.3 C4)", () => {
  it("question 은 답변 하나, approval 은 승인·거절 둘이다", () => {
    expect(defaultActionsFor("question")).toEqual(["answer"]);
    expect(defaultActionsFor("approval")).toEqual(["approve", "reject"]);
  });

  it("approval 은 제안 기본값 자리를 만들지 않는다(COMPONENTS §2.3 `g71PvC` 기본 끔)", () => {
    render(<HitlCard request={req({ type: "approval", proposed_default: null })} onRespond={vi.fn()} />);
    expect(screen.queryByTestId("hitl-proposed-default")).toBeNull();
    expect(screen.getByTestId("hitl-approve")).toBeTruthy();
  });

  it("거절은 사유 없이 보낼 수 없다(U3 approval 변형: 사유 필수)", () => {
    render(<HitlCard request={req({ type: "approval", proposed_default: null })} onRespond={vi.fn()} />);
    const reject = screen.getByTestId("hitl-reject") as HTMLButtonElement;
    expect(reject.disabled).toBe(true);
    fireEvent.change(screen.getByTestId("hitl-reason-input"), { target: { value: "근거가 부족합니다" } });
    expect((screen.getByTestId("hitl-reject") as HTMLButtonElement).disabled).toBe(false);
  });

  it("답변을 비워 보내면 제안 기본값이 실려 간다(E7-12 와 같은 값으로 진행)", () => {
    const onRespond = vi.fn();
    render(<HitlCard request={req()} onRespond={onRespond} />);
    fireEvent.change(screen.getByTestId("hitl-answer-input"), { target: { value: "  " } });
    fireEvent.click(screen.getByTestId("hitl-answer"));
    expect(onRespond).toHaveBeenCalledWith({ answer: "투자자" });
  });

  it("거절 응답의 페이로드는 계약 HitlResponse 그대로다 — approved:false + reason(E7-17)", () => {
    const onRespond = vi.fn();
    render(<HitlCard request={req({ type: "approval", proposed_default: null })} onRespond={onRespond} />);
    fireEvent.change(screen.getByTestId("hitl-reason-input"), { target: { value: "근거가 부족합니다" } });
    fireEvent.click(screen.getByTestId("hitl-reject"));
    expect(onRespond).toHaveBeenCalledWith({ approved: false, reason: "근거가 부족합니다" });
  });

  it("예산 HITL 승인은 budget_override_usd 를 함께 보낸다(E9-02)", () => {
    const onRespond = vi.fn();
    render(
      <HitlCard
        request={req({ source: "system", purpose: "budget", type: "approval", proposed_default: null, question: "예산 $1 을 초과했습니다" })}
        budget={{ current: 1, spent: 1.01 }}
        onRespond={onRespond}
      />,
    );
    fireEvent.change(screen.getByTestId("hitl-budget-input"), { target: { value: "3" } });
    fireEvent.click(screen.getByTestId("hitl-approve"));
    expect(onRespond).toHaveBeenCalledWith({ approved: true, budget_override_usd: 3 });
  });
});

describe("상태 4종(계약 HitlStatus)", () => {
  it("answered — 응답과 답변자가 남는다", () => {
    render(<HitlCard request={req({ status: "answered", answer: "경영진" })} />);
    expect(screen.getByTestId("hitl-answer").textContent).toContain("경영진");
    expect(screen.queryByTestId("hitl-answer-input")).toBeNull();
  });

  it("auto_answered — '자동(제안 기본값)' 이라고 밝힌다(E7-12 결정 기록의 '자동')", () => {
    render(<HitlCard request={req({ status: "auto_answered", answer: "투자자" })} />);
    expect(screen.getByTestId("hitl-answer").textContent).toContain("자동");
  });

  it("cancelled — '취소됨' 과 '결정 기록 없음' 을 말한다(K-4)", () => {
    render(<HitlCard request={req({ status: "cancelled" })} />);
    expect(screen.getByTestId("hitl-card").getAttribute("data-status")).toBe("cancelled");
    expect(screen.getByTestId("hitl-status-label").textContent).toContain("취소됨");
    expect(screen.getByTestId("hitl-answer").textContent).toContain("결정 기록이 없습니다");
    expect(screen.queryByTestId("hitl-answer-input")).toBeNull();
  });

  it("overdue 는 상태가 아니라 플래그다 — open 인 채로 기한만 붉어진다(FR-5.4 s-9)", () => {
    render(<HitlCard request={req({ overdue: true })} onRespond={vi.fn()} />);
    expect(screen.getByTestId("hitl-card").getAttribute("data-status")).toBe("open");
    expect(screen.getByTestId("hitl-due").getAttribute("data-overdue")).toBe("true");
    expect((screen.getByTestId("hitl-answer") as HTMLButtonElement).disabled).toBe(false); // E7-15
  });
});

describe("시스템 발행 문구 — purpose 가 판정 기준이다(0012)", () => {
  it("purpose 별 사유 + `source: system`", () => {
    for (const [purpose, reason] of Object.entries(PURPOSE_REASON)) {
      const line = hitlMetaLine({ source: "system", purpose: purpose as never, created_at: T0 });
      expect(line).toContain(reason);
      expect(line).toContain("source: system");
    }
  });

  it("에이전트 발행에는 사유 줄이 없다 — 발행 이유가 곧 질문이다", () => {
    expect(hitlMetaLine({ source: "agent", purpose: "agent", created_at: T0 })).toContain("source: agent");
    render(<HitlCard request={req()} />);
    expect(screen.getByTestId("hitl-author").textContent).toBe("Writer");
  });

  it("완료 승인과 예산 정지는 source·type 이 같아도 문구가 갈린다", () => {
    const approvalBySystem = { source: "system", created_at: T0 } as const;
    expect(hitlMetaLine({ ...approvalBySystem, purpose: "user_approval" })).toContain("종료 조건");
    expect(hitlMetaLine({ ...approvalBySystem, purpose: "budget" })).toContain("예산");
    render(<HitlCard request={req({ source: "system", purpose: "budget", type: "approval", proposed_default: null })} />);
    expect(screen.getByTestId("hitl-author").textContent).toBe("시스템");
    expect(screen.getByTestId("hitl-card").getAttribute("data-purpose")).toBe("budget");
  });
});
