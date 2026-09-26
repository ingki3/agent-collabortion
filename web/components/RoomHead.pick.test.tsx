/**
 * T-R2-W4b — 「여기까지 정리」 직접 고르기(SCREEN §4.6 범위 표) · 방 멈춤 배너 다음 권한자(0.2.8 `next_approver`, #305 NN1).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RoomHead, type PickedRange } from "./RoomHead";
import { RoomBlockedBanner } from "./RoomBlockedBanner";
import { SUMMARIZE_DIALOG } from "@/lib/wording";
import type { Room, User } from "@/lib/api/types";

afterEach(cleanup);

const user = (id: string, name: string): User => ({ id, email: `${id}@x`, display_name: name, avatar_url: null, created_at: "" });
const room = {
  id: "r1", workspace_id: "ws1", name: "결제팀", description: null, status: "active", visibility: "workspace", owner_user_id: "u1", deputy_owner_user_id: null,
  runtime_id: null, isolation: { kind: "none", remote_url: null }, limits: {}, autonomy: "guided", default_director_user_id: null, blocked_reason: null, blocked_detail: null,
  counts: { works_active: 0, lanes_active: 0, tasks_active: 0 }, cost_usd: 0, cost_estimated: false, unread_count: 0, my_room_role: "member", my_capabilities: ["post", "summarize"],
  created_by: "u1", created_at: "", updated_at: "", last_activity_at: null,
} as unknown as Room;

function head(extra: Partial<Parameters<typeof RoomHead>[0]> = {}) {
  const props = {
    room, needs: 0, onJumpNeed: vi.fn(), onParticipants: vi.fn(), onLeave: vi.fn(), onBlock: vi.fn(), onUnblock: vi.fn(), onSummarize: vi.fn(async () => undefined),
    onArchive: vi.fn(), onUnarchive: vi.fn(), onDelete: vi.fn(), runningTurns: 0, openWorks: 0, onStartPick: vi.fn(), ...extra,
  };
  return { props, ...render(<RoomHead {...props} />) };
}
const openSummarize = () => {
  fireEvent.click(screen.getByTestId("room-more"));
  fireEvent.click(screen.getByTestId("room-menu-summarize"));
};

describe("여기까지 정리 — 범위 셋(최근 7일 · 최근 30일 · 직접 고르기)", () => {
  it("직접 고르기를 고르면 범위가 없는 동안 「정리」가 비활성 + 사유, 「타임라인에서 고르기」는 다이얼로그를 닫고 집기 모드로", () => {
    const { props } = head();
    openSummarize();
    fireEvent.click(screen.getByTestId("summarize-pick"));
    const confirm = screen.getByTestId("summarize-dialog-confirm");
    expect(confirm).toBeDisabled();
    expect(document.getElementById(confirm.getAttribute("aria-describedby")!)).toHaveTextContent(SUMMARIZE_DIALOG.pick_need);
    fireEvent.click(screen.getByTestId("summarize-pick-start"));
    expect(props.onStartPick).toHaveBeenCalled();
    expect(screen.queryByTestId("summarize-dialog")).toBeNull();
  });

  it("타임라인에서 집고 돌아오면(pickNonce) 다이얼로그가 「직접 고르기」로 다시 서고 시작·끝·수를 보인다 → from/to 로 정리", async () => {
    const { props, rerender } = head();
    const picked: PickedRange = { from: { id: "m1", text: "서연 · 초안 방향" }, to: { id: "m3", text: "Writer · 수수료 표" }, count: 3 };
    rerender(<RoomHead {...props} picked={picked} pickNonce={1} />);
    expect(screen.getByTestId("summarize-dialog")).toBeInTheDocument();
    expect(screen.getByTestId("summarize-pick")).toBeChecked();
    expect(screen.getByTestId("summarize-picked-from")).toHaveTextContent("서연 · 초안 방향");
    expect(screen.getByTestId("summarize-picked-to")).toHaveTextContent("Writer · 수수료 표");
    expect(screen.getByTestId("summarize-preview").querySelector("[data-slot]")).toHaveTextContent("3");
    expect(screen.getByTestId("summarize-pick-start")).toHaveTextContent(SUMMARIZE_DIALOG.pick_again);
    fireEvent.click(screen.getByTestId("summarize-dialog-confirm"));
    await waitFor(() => expect(props.onSummarize).toHaveBeenCalledWith({ from: "m1", to: "m3" }));
  });

  it("최근 7일은 전처럼 days 로", async () => {
    const { props } = head();
    openSummarize();
    fireEvent.click(screen.getByTestId("summarize-dialog-confirm"));
    await waitFor(() => expect(props.onSummarize).toHaveBeenCalledWith({ days: 7 }));
  });
});

describe("방 멈춤 배너 — 다음 권한자(0.2.8)", () => {
  const blocked = (d: Record<string, unknown>) => ({ blocked_reason: "budget", blocked_detail: { works_stopped: 1, approver: user("u2", "민호"), delegate_at: "2026-09-22T05:30:00Z", ...d }, my_capabilities: [] }) as unknown as Room;

  it("부방장 — 「HH:MM부터 부방장 「서연」님이 답할 수 있습니다」(시각·이름은 슬롯)", () => {
    render(<RoomBlockedBanner me="u9" room={blocked({ next_approver: user("u3", "서연"), next_approver_role: "room_deputy" })} />);
    const next = screen.getByTestId("room-banner-next");
    expect(next).toHaveAttribute("data-role", "room_deputy");
    expect(next.textContent).toMatch(/^\d\d:\d\d부터 부방장 「서연」님이 답할 수 있습니다$/);
    expect([...next.querySelectorAll("[data-slot]")].map((x) => x.textContent)).toEqual([expect.stringMatching(/^\d\d:\d\d$/), "서연"]);
    expect(screen.getByTestId("room-banner-waiting").textContent).toMatch(/^「민호」의 승인을 기다립니다 · /);
  });

  it("부방장이 없으면 워크스페이스 소유자 최고참", () => {
    render(<RoomBlockedBanner me="u9" room={blocked({ next_approver: user("u4", "지훈"), next_approver_role: "workspace_owner" })} />);
    expect(screen.getByTestId("room-banner-next").textContent).toMatch(/부터 워크스페이스 소유자 「지훈」님이 답할 수 있습니다$/);
  });

  it("다음 권한자 칸이 없으면(옛 서버·null) 지금의 축약 「다음 권한자」", () => {
    render(<RoomBlockedBanner me="u9" room={blocked({ next_approver: null, next_approver_role: null })} />);
    expect(screen.queryByTestId("room-banner-next")).toBeNull();
    expect(screen.getByTestId("room-banner-waiting").textContent).toMatch(/부터 다음 권한자가 답할 수 있습니다$/);
  });
});
