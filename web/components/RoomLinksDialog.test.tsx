/**
 * S24 참고 방 링크(T-R2-W3, SCREEN §4.12) — 목 서버에 물려 잰다: 설명 한 줄 · 빈 상태 · 내가 참여한 방만 후보 · 연결/풀기 ·
 * 「연결은 에이전트 쪽 조건만 풉니다」 · 권한 밖이면 읽기 전용(목록은 보인다).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));

import { RoomLinksDialog } from "./RoomLinksDialog";
import { setup, uid } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { LINKS } from "@/lib/room-dialogs";
import type { Room } from "@/lib/api/types";

let bridge: FetchBridge;
let roomId = "";
let wsId = "";
let as: (email: string) => Promise<void>;
beforeEach(async () => {
  ({ bridge, roomId, wsId, as } = await setup());
});
afterEach(() => {
  cleanup();
  bridge.restore();
});

describe("S24 참고 방 링크", () => {
  it("빈 상태 · 설명 · 아래 한 줄 — 후보는 내가 참여한 방만(서연 방은 없다) · 연결하면 목록에, 풀면 빠진다", async () => {
    await bridge.call<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "인프라" });
    await as("seoyeon@colab.dev");
    await bridge.call<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "서연 방" });
    await as("demo@colab.dev");
    render(<RoomLinksDialog roomId={roomId} onClose={() => {}} />);
    expect(await screen.findByTestId("rd-links-empty")).toHaveTextContent(LINKS.empty);
    expect(screen.getByTestId("rd-links-sub")).toHaveTextContent(LINKS.explain);
    expect(screen.getByTestId("rd-links-agent-side")).toHaveTextContent(LINKS.agent_side_only);
    const cands = await screen.findAllByTestId("rd-link-candidate");
    expect(cands.map((c) => c.textContent)).toEqual([expect.stringContaining("인프라")]);
    fireEvent.click(within(cands[0]).getByTestId("rd-link-add"));
    const row = await screen.findByTestId("rd-link-row");
    expect(row).toHaveTextContent("인프라");
    expect(row).toHaveTextContent(`${LINKS.linked_by} 데모`);
    await waitFor(() => expect(screen.getByTestId("rd-links-search-empty")).toHaveTextContent(LINKS.search_empty));
    fireEvent.click(within(row).getByTestId("rd-link-unlink"));
    await screen.findByTestId("rd-links-empty");
  });

  it("권한 밖(방 참여자) — 연결된 방은 보이고 풀기·연결은 비활성 + 사유", async () => {
    const other = (await bridge.call<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "인프라" })).body.id;
    await bridge.call("POST", `/rooms/${roomId}/links`, { target_room_id: other });
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("junho@colab.dev") });
    await as("junho@colab.dev");
    render(<RoomLinksDialog roomId={roomId} onClose={() => {}} />);
    const unlink = within(await screen.findByTestId("rd-link-row")).getByTestId("rd-link-unlink");
    expect(unlink).toHaveAttribute("aria-disabled", "true");
    expect(document.getElementById(unlink.getAttribute("aria-describedby")!)).toHaveTextContent(LINKS.read_only);
  });
});
