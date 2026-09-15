/**
 * 「조건 고치기」 다이얼로그(T-W15, S-84) — 기존 조건으로 시작하고, 마법사와 같은 편집기로 리뷰어를 넣어 updateSession
 * `completion_condition` 을 보내고, 응답(Session)으로 호출부를 갱신한다. 리뷰어가 없으면 저장이 비활성 + 사유(§8.5).
 * 서버가 거절하면(422 errors[]) 다이얼로그 안에서 서버 문장 그대로.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "@/lib/api/client";
import type { Session } from "@/lib/api/types";

const patch = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { ...actual.api, patch: (...a: unknown[]) => patch(...a) } };
});

import { FixConditionDialog } from "./FixConditionDialog";

const participant = (id: string, name: string, is_assignee = false): NonNullable<Session["participants"]>[number] => ({
  session_id: "s1", agent_id: id, agent: { id, name, role: "custom", role_description: "" }, profile: { id: `p-${id}`, agent_id: id, name: "default", runtime_kind: "claude_code", model: "m", options: {}, env: {}, args: [], is_default: true, fallback_profile_id: null, created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" },
  status: "idle", is_assignee, joined_at: "2026-09-06T09:00:00Z",
});
/** 리뷰어 없는 옛 세션 — 보고서 제출(담당) AND 에이전트 검토 승인(리뷰어 없음). */
const legacy = {
  id: "s1",
  assignee_agent_id: "a-writer",
  participants: [participant("a-writer", "Writer", true), participant("a-lead", "Lead")],
  completion_condition: { op: "and" as const, conditions: [{ type: "artifact_submitted" as const, who: "assignee" }, { type: "agent_approval" as const }] },
};

beforeEach(() => patch.mockReset());
afterEach(cleanup);

describe("FixConditionDialog", () => {
  it("기존 조건으로 시작한다 — 두 조건이 켜져 있고 리뷰어가 비어 저장이 비활성 + 사유", () => {
    render(<FixConditionDialog session={legacy} onSaved={vi.fn()} onClose={vi.fn()} />);
    const rows = screen.getAllByTestId("condition-row");
    const selected = rows.filter((r) => r.className.includes("cond--selected")).map((r) => r.dataset.type);
    expect(selected).toEqual(["artifact_submitted", "agent_approval"]);
    expect((screen.getByTestId("reviewer-select") as HTMLSelectElement).value).toBe("");
    const save = screen.getByTestId("fix-condition-save") as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    const hintId = save.getAttribute("aria-describedby")!;
    expect(document.getElementById(hintId)!.textContent).toContain("리뷰어를 고르세요");
  });

  it("리뷰어를 고르고 저장하면 updateSession completion_condition 왕복 → onSaved(응답) → 닫힘", async () => {
    const saved = vi.fn();
    const close = vi.fn();
    const next = { id: "s1", completion_progress: { met: 0, total: 2, satisfied: false, human_gate: false, conditions: [] } } as unknown as Session;
    patch.mockResolvedValue(next);
    render(<FixConditionDialog session={legacy} onSaved={saved} onClose={close} />);
    fireEvent.change(screen.getByTestId("reviewer-select"), { target: { value: "a-lead" } });
    expect((screen.getByTestId("fix-condition-save") as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByTestId("fix-condition-save"));
    await waitFor(() => expect(saved).toHaveBeenCalledWith(next));
    expect(patch).toHaveBeenCalledWith("/sessions/{sessionId}", {
      path: { sessionId: "s1" },
      body: { completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval", agent_id: "a-lead" }] } },
    });
    expect(close).toHaveBeenCalled();
  });

  it("담당 에이전트를 리뷰어로 고르면 막지 않고 안내만 — 저장은 된다", () => {
    render(<FixConditionDialog session={legacy} onSaved={vi.fn()} onClose={vi.fn()} />);
    fireEvent.change(screen.getByTestId("reviewer-select"), { target: { value: "a-writer" } });
    expect(screen.getByTestId("reviewer-is-assignee").textContent).toContain("다른 에이전트를 권합니다");
    expect((screen.getByTestId("fix-condition-save") as HTMLButtonElement).disabled).toBe(false);
  });

  it("서버가 422 로 거절하면 errors[].message 를 다이얼로그 안에서 그대로 보인다", async () => {
    patch.mockRejectedValueOnce(new ApiError({ type: "https://colab.dev/problems/validation_failed", title: "입력값 확인 필요", status: 422, code: "validation_failed", detail: "입력값을 확인해 주세요", errors: [{ field: "completion_condition/conditions/1/agent_id", code: "reviewer_not_participant", message: "리뷰어는 이 세션의 참여자여야 합니다" }] }));
    render(<FixConditionDialog session={legacy} onSaved={vi.fn()} onClose={vi.fn()} />);
    fireEvent.change(screen.getByTestId("reviewer-select"), { target: { value: "a-lead" } });
    fireEvent.click(screen.getByTestId("fix-condition-save"));
    await waitFor(() => expect(screen.getByTestId("fix-condition-error").textContent).toBe("리뷰어는 이 세션의 참여자여야 합니다"));
  });

  it("취소 · Esc · 바깥 클릭이 닫는다", () => {
    const close = vi.fn();
    render(<FixConditionDialog session={legacy} onSaved={vi.fn()} onClose={close} />);
    fireEvent.click(screen.getByTestId("fix-condition-cancel"));
    fireEvent.keyDown(screen.getByTestId("fix-condition-dialog"), { key: "Escape" });
    expect(close).toHaveBeenCalledTimes(2);
  });
});
