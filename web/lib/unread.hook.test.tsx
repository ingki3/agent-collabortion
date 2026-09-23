/**
 * 안 읽음(M4) 훅 — 목 서버에 물려 잰다: 방 화면을 **보고 있을 때만** markRoomRead 가 나가고, 가려진 탭은 옮기지 않는다 ·
 * 참여자가 아니면(enabled=false) 부르지 않는다.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, renderHook, waitFor } from "@testing-library/react";

vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));

import { useMarkRoomRead } from "./unread";
import { setup } from "@/lib/mock/room-dialogs-testkit";
import { store } from "@/lib/mock/store";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import type { Room } from "@/lib/api/types";

let bridge: FetchBridge;
let roomId = "";
beforeEach(async () => {
  ({ bridge, roomId } = await setup());
  await bridge.call("POST", `/__mock/rooms/${roomId}/seed`, { unread: 3 });
});
afterEach(() => {
  cleanup();
  bridge.restore();
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
});
const msgs = () => [...store().messages.values()].filter((m) => m.session_id === roomId);
const unread = async () => (await bridge.call<Room>("GET", `/rooms/${roomId}`)).body.unread_count;

describe("useMarkRoomRead", () => {
  it("보이는 탭 — 마지막 메시지까지 읽음으로 옮겨 안 읽음이 0", async () => {
    expect(await unread()).toBe(3);
    renderHook(() => useMarkRoomRead(roomId, msgs(), true));
    await waitFor(async () => expect(await unread()).toBe(0));
  });
  it("가려진 탭 — 옮기지 않는다(안 본 것을 본 것으로 만들지 않는다)", async () => {
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    renderHook(() => useMarkRoomRead(roomId, msgs(), true));
    await new Promise((r) => setTimeout(r, 30));
    expect(await unread()).toBe(3);
  });
  it("참여자가 아니면 부르지 않는다", async () => {
    renderHook(() => useMarkRoomRead(roomId, msgs(), false));
    await new Promise((r) => setTimeout(r, 30));
    expect(await unread()).toBe(3);
  });
});
