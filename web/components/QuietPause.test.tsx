/**
 * T-QUIET(PRD FR-2A.2.3 · SCREEN v0.19.14 · openapi v0.3.9) — 승인만 남은 일에서 에이전트끼리의 새 작업을 멈춰 둔 것이
 * 사람에게 보인다: 진행률 「Director 승인 — 받은 요청에서」 아래 작은 줄, 서브 미션 카드의 「승인 대기로 멈춤」 칩.
 *
 * 회귀 주입: ConditionRow 의 paused 줄 조건을 지우면 (line) FAIL; LaneCard 의 approval_pending 칩을 지우면 (chip) FAIL;
 * 문구를 한 글자 바꾸면 (lock) FAIL.
 */
import { describe, expect, it, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ConditionRow } from "./ConditionRow";
import { LaneCard } from "./LaneCard";
import { PROGRESS, ROOM_LEFT } from "@/lib/wording";
import type { Lane } from "@/lib/api/types";

afterEach(cleanup);

describe("T-QUIET — 진행률 줄", () => {
  it("(lock) SCREEN v0.19.14 문구 그대로", () => {
    expect(PROGRESS.paused_agent_triggers.join("3")).toBe("에이전트끼리의 새 작업 3건을 멈춰 두었습니다 — 승인하면 취소, 수정 요청하면 이어서");
    expect(ROOM_LEFT.queued_approval_pending).toBe("승인 대기로 멈춤");
  });

  it("(line) user_approval 미충족 + N>0 이면 행 아래 한 줄", () => {
    render(<ConditionRow type="user_approval" met={false} nextActor="director" pausedAgentTriggers={3} />);
    const el = screen.getByTestId("condition-paused-agent-triggers");
    expect(el.textContent).toBe("에이전트끼리의 새 작업 3건을 멈춰 두었습니다 — 승인하면 취소, 수정 요청하면 이어서");
    expect(el.getAttribute("data-count")).toBe("3");
  });

  it("(line) 0 · 없음 · 다른 조건 · 충족이면 줄이 없다", () => {
    for (const p of [
      { type: "user_approval", met: false, pausedAgentTriggers: 0 },
      { type: "user_approval", met: false, pausedAgentTriggers: null },
      { type: "artifact_submitted", met: false, pausedAgentTriggers: 2 },
      { type: "user_approval", met: true, pausedAgentTriggers: 2 },
    ]) {
      cleanup();
      render(<ConditionRow {...p} />);
      expect(screen.queryByTestId("condition-paused-agent-triggers")).toBeNull();
    }
  });
});

function lane(over: Partial<Lane> = {}): Lane {
  return {
    id: "l1", session_id: "s1", parent_lane_id: null, agent_id: "ag1", agent_name: "Writer",
    profile_id: "p1", depends_on: [], workdir_id: null, workdir_ref: null, delegated_from_task_id: null,
    has_runtime_session: true, brief: "가이드 각주", status: "queued", blocked_note: null, blocked_message_id: null,
    waiting_for: null, hitl_request_id: null, paused_over_usd: null, failure_kind: null, reentry_count: 0,
    current_activity: null, queue_position: 1, actions: ["cancel"],
    created_at: "2026-09-28T11:47:00Z", updated_at: "2026-09-28T11:47:00Z", finished_at: null, ...over,
  };
}

describe("T-QUIET — 서브 미션 카드 칩", () => {
  it("(chip) queued_reason approval_pending 이면 「승인 대기로 멈춤」 칩(neutral)", () => {
    render(<LaneCard lane={lane({ queued_reason: "approval_pending" })} />);
    const chip = screen.getByTestId("lane-approval-pending");
    expect(chip.textContent).toContain("승인 대기로 멈춤");
    expect(chip.querySelector(".badge")!.getAttribute("data-tone")).toBe("neutral");
  });

  it("(chip) 다른 대기 사유에는 칩이 없다", () => {
    render(<LaneCard lane={lane({ queued_reason: "room_lanes" })} />);
    expect(screen.queryByTestId("lane-approval-pending")).toBeNull();
  });
});
