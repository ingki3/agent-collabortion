/**
 * Lane Card 7상태(COMPONENTS §2.1 조합표 · SCREEN §4.5). lane 보드는 유일한 제어판이므로
 * **상태마다 무엇을 말하고 어떤 버튼을 주는지**가 계약이다. `paused` 는 `failed` 가 아니고, 일반 "재시도" 버튼은 없다.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { LaneCard, laneNote } from "./LaneCard";
import { LaneBoard, LANE_GROUP_ORDER } from "./LaneBoard";
import type { Lane, LaneStatus } from "@/lib/api/types";

afterEach(cleanup);

const ACTIONS: Record<LaneStatus, Lane["actions"]> = {
  queued: ["cancel"],
  running: ["restart", "cancel"],
  waiting_human: ["respond_hitl"],
  blocked: ["open_question"],
  paused: ["approve_budget", "cancel"],
  done: [],
  failed: ["restart"],
};

function lane(status: LaneStatus, over: Partial<Lane> = {}): Lane {
  return {
    id: `l-${status}`, session_id: "s1", parent_lane_id: null, agent_id: "ag1", agent_name: "Backend",
    profile_id: "p1", depends_on: [], workdir_id: null, workdir_ref: null, delegated_from_task_id: null,
    has_runtime_session: true, brief: "결제 모듈 정리", status, blocked_note: null, blocked_message_id: null,
    waiting_for: null, hitl_request_id: null, paused_over_usd: null, failure_kind: null, reentry_count: 0,
    current_activity: null, queue_position: null, actions: ACTIONS[status],
    created_at: "2026-09-06T10:00:00Z", updated_at: "2026-09-06T10:00:00Z", finished_at: null, ...over,
  };
}

const ALL: LaneStatus[] = ["queued", "running", "waiting_human", "blocked", "paused", "done", "failed"];

describe("LaneCard — 7상태", () => {
  it("일곱 상태가 모두 자기 배지와 문구로 그려진다", () => {
    for (const st of ALL) {
      cleanup();
      render(<LaneCard lane={lane(st)} />);
      const card = screen.getByTestId("lane-card");
      expect(card.getAttribute("data-status"), st).toBe(st);
      expect(card.querySelector('.badge[data-kind="lane"]')?.getAttribute("data-value"), st).toBe(st);
    }
  });

  it("상태별 부가 정보 한 줄이 원인을 말한다", () => {
    expect(laneNote(lane("queued", { queue_position: 2 }))).toContain("대기 순번 2");
    expect(laneNote(lane("running", { current_activity: "src/app.ts 편집 중" }))).toContain("src/app.ts 편집 중");
    expect(laneNote(lane("blocked", { waiting_for: "Lead", blocked_note: "국내만인가요?" }))).toContain("@Lead의 답을 기다림");
    expect(laneNote(lane("paused", { paused_over_usd: 1.4 }))).toContain("$1.40 초과");
    expect(laneNote(lane("waiting_human", { waiting_for: "Director 승인 대기" }))).toContain("Inbox");
    expect(laneNote(lane("failed", { failure_kind: "cancelled" }))).toContain("사람이 중단함");
    expect(laneNote(lane("failed", { failure_kind: "timeout" }))).toContain("자동 재시도 소진");
  });

  it("running 은 '중단하고 다시 지시'와 '중단' 둘 다 준다", () => {
    const onRestart = vi.fn();
    const onCancel = vi.fn();
    render(<LaneCard lane={lane("running")} onRestart={onRestart} onCancel={onCancel} />);
    fireEvent.click(screen.getByTestId("lane-action-restart"));
    fireEvent.click(screen.getByTestId("lane-action-cancel"));
    expect(onRestart).toHaveBeenCalled();
    expect(onCancel).toHaveBeenCalled();
  });

  it("paused 는 failed 가 아니다 — '계속 진행 승인'이지 '다시 지시'가 아니다(C2)", () => {
    render(<LaneCard lane={lane("paused", { paused_over_usd: 1.4 })} onApproveBudget={vi.fn()} />);
    expect(screen.getByTestId("lane-action-approve_budget").textContent).toBe("계속 진행 승인");
    expect(screen.queryByTestId("lane-action-restart")).toBeNull();
  });

  it("failed 에는 '다시 지시'만 있고 일반 '재시도' 버튼은 없다(m6)", () => {
    render(<LaneCard lane={lane("failed", { failure_kind: "timeout" })} onRestart={vi.fn()} />);
    expect(screen.getByTestId("lane-action-restart").textContent).toBe("다시 지시");
    expect(screen.queryByText("재시도")).toBeNull();
  });

  it("failed(runtime_offline) 은 다시 지시가 아니라 런타임 복구·재바인딩이라 버튼을 주지 않는다", () => {
    render(<LaneCard lane={lane("failed", { failure_kind: "runtime_offline" })} onRestart={vi.fn()} />);
    expect(screen.queryByTestId("lane-action-restart")).toBeNull();
  });

  it("blocked 는 질문 요약과 '질문 카드로 이동'을 준다(FR-6.2.1)", () => {
    const onOpen = vi.fn();
    render(<LaneCard lane={lane("blocked", { waiting_for: "Lead", blocked_note: "국내만인가요?", blocked_message_id: "m-9" })} onOpenQuestion={onOpen} />);
    expect(screen.getByTestId("lane-note").textContent).toContain("국내만인가요?");
    fireEvent.click(screen.getByTestId("lane-action-open_question"));
    expect(onOpen).toHaveBeenCalledWith("m-9");
  });

  it("done 은 동작이 없고, 권한이 없으면 숨기지 않고 비활성 + 사유(원칙 4)", () => {
    const { rerender } = render(<LaneCard lane={lane("done", { finished_at: "2026-09-06T10:05:00Z" })} />);
    expect(screen.queryByTestId("lane-actions")).toBeNull();
    rerender(<LaneCard lane={lane("running", { actions: [] })} onRestart={vi.fn()} onCancel={vi.fn()} disabledReason="Director 만" />);
    const btn = screen.getByTestId("lane-action-restart") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("title")).toBe("Director 만");
  });

  it("재진입 횟수와 콜드 스타트를 눈에 띄게 표시한다(§11 지표 신호)", () => {
    render(<LaneCard lane={lane("running", { reentry_count: 2, has_runtime_session: false, workdir_ref: "wt/backend" })} />);
    expect(screen.getByTestId("lane-reentry").textContent).toContain("재진입 2회");
    expect(screen.getByTestId("lane-cold-start")).not.toBeNull();
    expect(screen.getByTestId("lane-workdir").textContent).toBe("wt/backend");
  });
});

describe("LaneBoard — 상태별 묶음", () => {
  it("사람이 할 일이 위로 오고, 일곱 묶음이 모두 나온다", () => {
    render(<LaneBoard lanes={ALL.map((s) => lane(s))} />);
    for (const st of ALL) expect(screen.getByTestId(`lane-group-${st}`), st).not.toBeNull();
    const groups = [...document.querySelectorAll("[data-testid^='lane-group-']")].map((e) => e.getAttribute("data-status"));
    expect(groups).toEqual([...LANE_GROUP_ORDER]);
    expect(LANE_GROUP_ORDER[0]).toBe("blocked");
  });

  it("빈 보드도 렌더한다 — 침묵도 정보다(§7)", () => {
    render(<LaneBoard lanes={[]} />);
    expect(screen.getByTestId("lane-board-empty").textContent).toContain("아직 lane 이 없습니다");
  });
});
