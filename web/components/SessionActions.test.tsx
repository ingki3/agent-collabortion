/**
 * S7 상단 액션과 권한 변형 S7-P·S7-D(SCREEN §4.5 상단 액션 · §2.3 세션 역할 표 · §7).
 *
 * 핵심은 **숨기지 않고 비활성 + 사유**다. U10-1("왜 못 누르지"가 아니라 사유를 읽는다)과
 * U9-6(deputy 도 일시정지는 "Director만")이 이 파일의 실측 문장이다.
 *
 * **deputy 의 비대칭**(t-3)이 여기서 갈린다 — 세션 일시정지·종료는 Director 뿐이고, lane 중단은
 * deputy 도 즉시다(그쪽은 `LaneCard.test.tsx` 와 목 골든 대조가 지킨다).
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SessionActions, actionGate } from "./SessionActions";
import type { Member, Session } from "@/lib/api/types";

afterEach(cleanup);

const members: Member[] = [
  { id: "m1", workspace_id: "w1", user: { id: "u1", email: "a@b.c", display_name: "민지", avatar_url: null, created_at: "" }, role: "owner", created_at: "" },
  { id: "m2", workspace_id: "w1", user: { id: "u2", email: "d@e.f", display_name: "서연", avatar_url: null, created_at: "" }, role: "member", created_at: "" },
];

const sess = (over: Partial<Session> = {}): Session => ({
  id: "s1", workspace_id: "w1", title: "시장 조사", goal: "보고서", acceptance_criteria: [],
  director_user_id: "u1", deputy_director_user_id: "u2", assignee_agent_id: "a1", runtime_id: "r1",
  isolation: { kind: "none" },
  completion_condition: { op: "and", conditions: [] },
  completion_progress: { met: 0, total: 2, satisfied: false, human_gate: true, conditions: [] },
  limits: { budget_usd: 20, budget_tokens: null, time_limit: "PT4H", max_tasks: null, max_parallel_lanes: 5 },
  autonomy: "guided", status: "active", paused_reason: null, cost_usd: 0,
  my_role: "director", created_by: "u1", created_at: "", updated_at: "",
  ...over,
});

const noop = { onPause: vi.fn(), onResume: vi.fn(), onComplete: vi.fn(), onCancel: vi.fn(), onOpenParticipants: vi.fn(), onChangeDirector: vi.fn() };

describe("S7 — Director", () => {
  it("active 세션은 일시정지, paused 세션은 재개 버튼이 나온다(O6)", () => {
    const { unmount } = render(<SessionActions session={sess()} runningLanes={0} members={members} {...noop} />);
    expect(screen.getByTestId("session-pause")).toBeTruthy();
    expect(screen.queryByTestId("session-resume")).toBeNull();
    unmount();
    render(<SessionActions session={sess({ status: "paused", paused_reason: "budget" })} runningLanes={0} members={members} {...noop} />);
    expect(screen.getByTestId("session-resume")).toBeTruthy();
    expect(screen.queryByTestId("session-pause")).toBeNull();
  });

  it("런타임 오프라인은 재개가 아니라 재바인딩·종료다 — 버튼은 보이되 사유와 함께 비활성(계약 409)", () => {
    render(<SessionActions session={sess({ status: "paused", paused_reason: "runtime_offline" })} runningLanes={0} members={members} {...noop} />);
    const btn = screen.getByTestId("session-resume") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.title).toContain("재바인딩");
  });

  it("종료 확인은 **진행 중 lane 개수를 명시**하고 그때만 confirm 을 실어 보낸다", () => {
    const onComplete = vi.fn();
    render(<SessionActions session={sess()} runningLanes={3} members={members} {...noop} onComplete={onComplete} />);
    fireEvent.click(screen.getByTestId("session-complete"));
    expect(screen.getByTestId("complete-running-lanes").textContent).toContain("3개");
    fireEvent.click(screen.getByTestId("complete-confirm-yes"));
    expect(onComplete).toHaveBeenCalledWith(true);
  });

  it("진행 중 lane 이 없으면 confirm 없이 보낸다", () => {
    const onComplete = vi.fn();
    render(<SessionActions session={sess()} runningLanes={0} members={members} {...noop} onComplete={onComplete} />);
    fireEvent.click(screen.getByTestId("session-complete"));
    expect(screen.queryByTestId("complete-running-lanes")).toBeNull();
    fireEvent.click(screen.getByTestId("complete-confirm-yes"));
    expect(onComplete).toHaveBeenCalledWith(false);
  });

  it("세션 취소는 확인을 받고, v1 에 삭제가 없다는 것을 말한다(SCREEN §8.2 Q2)", () => {
    const onCancel = vi.fn();
    render(<SessionActions session={sess()} runningLanes={0} members={members} {...noop} onCancel={onCancel} />);
    fireEvent.click(screen.getByTestId("session-cancel"));
    expect(screen.getByTestId("cancel-session-confirm").textContent).toContain("보관됩니다");
    fireEvent.change(screen.getByTestId("cancel-session-reason"), { target: { value: "방향 전환" } });
    fireEvent.click(screen.getByTestId("cancel-session-yes"));
    expect(onCancel).toHaveBeenCalledWith("방향 전환");
  });

  it("Director 교체 목록에 현재 Director 는 없다", () => {
    const onChangeDirector = vi.fn();
    render(<SessionActions session={sess()} runningLanes={0} members={members} {...noop} onChangeDirector={onChangeDirector} />);
    fireEvent.click(screen.getByTestId("session-director"));
    const opts = [...(screen.getByTestId("director-select") as HTMLSelectElement).options].map((o) => o.value);
    expect(opts).not.toContain("u1");
    expect(opts).toContain("u2");
    fireEvent.change(screen.getByTestId("director-select"), { target: { value: "u2" } });
    fireEvent.click(screen.getByTestId("director-confirm"));
    expect(onChangeDirector).toHaveBeenCalledWith("u2");
  });
});

describe("S7-D(deputy) · S7-P(일반 멤버) — 숨기지 않고 비활성 + 사유(SCREEN §7)", () => {
  for (const role of ["deputy", "member"] as const) {
    it(`${role} — 상단 액션 5개가 전부 보이되 전부 비활성이고 사유가 붙는다`, () => {
      render(<SessionActions session={sess({ my_role: role })} runningLanes={0} members={members} {...noop} />);
      for (const key of ["pause", "complete", "participants", "director", "cancel"]) {
        const btn = screen.getByTestId(`session-${key}`) as HTMLButtonElement;
        expect(btn).toBeTruthy(); // 숨기지 않는다
        expect(btn.disabled).toBe(true);
        expect(btn.title).toContain("Director");
      }
    });
  }

  it("deputy 의 사유는 'lane 중단만 즉시 가능' 을 함께 말한다 — 취소는 되고 승인은 안 되는 비대칭 설명(U9 성공 기준)", () => {
    render(<SessionActions session={sess({ my_role: "deputy" })} runningLanes={0} members={members} {...noop} />);
    expect((screen.getByTestId("session-pause") as HTMLButtonElement).title).toContain("lane 중단만 즉시");
  });

  it("종료된 세션은 역할과 무관하게 잠긴다", () => {
    for (const status of ["completed", "cancelled"] as const) {
      expect(actionGate(sess({ status }), "pause").allowed).toBe(false);
      expect(actionGate(sess({ status }), "pause").reason).toContain("종료된 세션");
    }
  });
});
