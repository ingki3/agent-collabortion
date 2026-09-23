/**
 * S23 맥락 읽기 기록(T-R2-W3, SCREEN §4.13) — 목 서버에 물려 잰다: 세 묶음 · 읽힌 쪽은 읽은 쪽 방 이름을 적는다 · 거부 행은 방 이름을
 * 적지 않는다(존재 숨김) · originator_left 만 PRD 문장 그대로(방 이름 포함) · 잘림 칩 · 빈 상태 · 활동 로그 링크.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));

import { RoomReadsTable } from "./RoomReadsTable";
import { setup } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { READS } from "@/lib/room-dialogs";
import type { Room } from "@/lib/api/types";

let bridge: FetchBridge;
let roomId = "";
let wsId = "";
beforeEach(async () => {
  ({ bridge, roomId, wsId } = await setup());
});
afterEach(() => {
  cleanup();
  bridge.restore();
});

describe("S23 맥락 읽기 기록", () => {
  it("빈 상태 — 「이 방의 맥락이 오간 적이 없습니다」", async () => {
    render(<RoomReadsTable roomId={roomId} />);
    expect(await screen.findByTestId("rd-reads-empty")).toHaveTextContent(READS.empty_head);
  });

  it("세 묶음 — 읽은 것(대상 방) · 읽힌 것(읽은 쪽 방) · 거부(방 이름 없음, originator_left 만 문장째) · 잘림 칩", async () => {
    const other = (await bridge.call<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "비밀 인수합병" })).body.id;
    await bridge.call("POST", `/__mock/rooms/${roomId}/reads`, { target_room_id: other, truncated: true, recent_n: 20 });
    await bridge.call("POST", `/__mock/rooms/${other}/reads`, { target_room_id: roomId });
    await bridge.call("POST", `/__mock/rooms/${roomId}/reads`, { target_room_id: other, denied_reason: "originator_not_participant" });
    await bridge.call("POST", `/__mock/rooms/${roomId}/reads`, { target_room_id: other, denied_reason: "no_originator", originator_email: null });
    await bridge.call("POST", `/__mock/rooms/${roomId}/reads`, { target_room_id: other, denied_reason: "originator_left" });
    render(<RoomReadsTable roomId={roomId} />);
    const out = await screen.findByTestId("rd-reads-out");
    expect(within(out).getByTestId("rd-read-room")).toHaveTextContent("비밀 인수합병");
    expect(within(out).getByTestId("rd-read-truncated")).toHaveTextContent(READS.truncated);
    expect(out).toHaveTextContent(`${READS.scope_summary}${READS.scope_join}${READS.scope_recent.join("20")}`);
    expect(within(screen.getByTestId("rd-reads-in")).getByTestId("rd-read-room")).toHaveTextContent("비밀 인수합병");
    const denied = screen.getByTestId("rd-reads-denied");
    const reasons = within(denied).getAllByTestId("rd-read-reason").map((c) => c.textContent);
    expect(reasons).toEqual(expect.arrayContaining([READS.denied.originator_not_participant, READS.denied.no_originator, READS.originator_left("데모", "비밀 인수합병", "Lead")]));
    // 존재 숨김 — originator_left 한 줄 말고는 거부 묶음 어디에도 방 이름이 없다.
    const rows = within(denied).getAllByTestId("rd-read-row");
    expect(rows.filter((r) => r.textContent?.includes("비밀 인수합병"))).toHaveLength(1);
    expect(within(denied).getAllByText(READS.no_originator_user)).toHaveLength(1);
    expect(screen.getByTestId("rd-reads-audit")).toHaveAttribute("href", "/settings/audit");
  });
});
