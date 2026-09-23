/**
 * T-R2-W4a 화면 조각 — S14 「방 기본값」 탭 · 컨텍스트 탭(다른 방 읽기) · 알림 구독 3층 · S9 「참여 중인 방 N」 · 내비 안 읽음 합계.
 * 목 서버에 물려 잰다(fetch 다리 — 저장이 실제 경로·본문으로 나간다).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("next/link", () => ({ default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => <a href={href} {...rest}>{children}</a> }));

import { WorkspaceSettingsTab, NotificationsTab } from "./SettingsTabs";
import { AgentRooms } from "./AgentRooms";
import { AppNav } from "./AppNav";
import { setup } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { AGENT_ROOMS, ROOM_DEFAULTS_TAB, ROOM_SUB_LABEL } from "@/lib/screens-v19";
import type { Agent, WorkspaceSettings } from "@/lib/api/types";

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

const settings = async () => (await bridge.call<WorkspaceSettings>("GET", `/workspaces/${wsId}/settings`)).body;
const save = async (patch: unknown) => (await bridge.call<WorkspaceSettings>("PATCH", `/workspaces/${wsId}/settings`, patch)).body;

describe("S14 「방 기본값」 탭", () => {
  it("머리에 「이 값이 새로 만드는 모든 방의 기본값입니다」 · 격리 없음 한 줄 · 바꾼 칸만 room_defaults 로 저장", async () => {
    const onSave = vi.fn(save);
    render(<WorkspaceSettingsTab tab="rooms" settings={await settings()} role="owner" onSave={onSave} />);
    expect(screen.getByTestId("room-defaults-head")).toHaveTextContent(ROOM_DEFAULTS_TAB.head);
    expect(screen.getByTestId("room-default-isolation-note")).toHaveTextContent(ROOM_DEFAULTS_TAB.isolation_none_note);
    // 새 방의 기본 격리는 없음·워크트리 둘뿐(서버 422 room_isolation).
    expect([...(screen.getByTestId("room-default-isolation") as HTMLSelectElement).options].map((o) => o.value)).toEqual(["none", "worktree"]);
    fireEvent.change(screen.getByTestId("room-default-visibility"), { target: { value: "invited" } });
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ room_defaults: { visibility: "invited" } });
    expect((await settings()).room_defaults?.visibility).toBe("invited");
  });

  it("멤버는 잠김(읽기만)", async () => {
    render(<WorkspaceSettingsTab tab="rooms" settings={await settings()} role="member" onSave={vi.fn()} />);
    expect(screen.getByTestId("room-default-visibility")).toBeDisabled();
  });
});

describe("S14 알림 — 구독 단위 3층", () => {
  it("방을 고르면 방 층이 펼쳐지고, 고르는 즉시 setRoomSubscription 으로 저장된다", async () => {
    render(<NotificationsTab settings={{ email: true, push: false, default_subscription: "all" }} onSave={vi.fn()} workspaceId={wsId} />);
    const pick = await screen.findByTestId("subs-room");
    await waitFor(() => expect([...(pick as HTMLSelectElement).options].some((o) => o.value === roomId)).toBe(true));
    fireEvent.change(pick, { target: { value: roomId } });
    const level = await screen.findByTestId("subs-room-level");
    expect([...(level as HTMLSelectElement).options].map((o) => o.textContent)).toEqual(Object.values(ROOM_SUB_LABEL));
    fireEvent.change(level, { target: { value: "hitl_only" } });
    await screen.findByTestId("sub-saved");
    expect((await bridge.call<{ my_subscription: string }>("GET", `/rooms/${roomId}`)).body.my_subscription).toBe("hitl_only");
    expect(screen.getByTestId("subs-works")).toBeInTheDocument();
    expect(screen.getByTestId("subs-lanes")).toBeInTheDocument();
  });
});

describe("S9 참여 중인 방 N", () => {
  const agent = (over: Partial<Agent>) => ({ id: "a1", max_concurrent_tasks: 3, running_task_count: 2, rooms: [{ id: "r1", name: "결제팀" }], room_count: 1, hidden_room_count: 1, ...over }) as Agent;
  it("합계에 볼 수 없는 방이 들고, 펼치면 볼 수 있는 방만 이름으로 + 「+ 볼 수 없는 방 N」 · 동시 사용", () => {
    render(<AgentRooms agent={agent({})} />);
    expect(screen.getByTestId("agent-concurrent")).toHaveTextContent(AGENT_ROOMS.concurrent(3, 2));
    const t = screen.getByTestId("agent-rooms-toggle");
    expect(t).toHaveTextContent(AGENT_ROOMS.count(2));
    expect(t).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(t);
    const list = screen.getByTestId("agent-rooms-list");
    expect(within(list).getAllByTestId("agent-room-link").map((a) => a.getAttribute("href"))).toEqual(["/rooms/r1"]);
    expect(within(list).getByTestId("agent-rooms-hidden")).toHaveTextContent(AGENT_ROOMS.hidden(1));
  });
  it("참여 방이 없으면 펼칠 것이 없다", () => {
    render(<AgentRooms agent={agent({ rooms: [], room_count: 0, hidden_room_count: 0 })} />);
    expect(screen.getByTestId("agent-rooms-none")).toHaveTextContent(AGENT_ROOMS.none);
  });
});

describe("내비 — 「방」 옆 안 읽음 합계(M4)", () => {
  it("0 이거나 모르면 그리지 않고, 있으면 라벨 붙은 수", () => {
    const { rerender } = render(<AppNav workspaceName="w" current="/rooms" inboxCount={0} roomsUnread={0} showSettings />);
    expect(screen.queryByTestId("rooms-unread-badge")).toBeNull();
    rerender(<AppNav workspaceName="w" current="/rooms" inboxCount={0} roomsUnread={null} showSettings />);
    expect(screen.queryByTestId("rooms-unread-badge")).toBeNull();
    rerender(<AppNav workspaceName="w" current="/rooms" inboxCount={0} roomsUnread={7} showSettings />);
    expect(screen.getByTestId("rooms-unread-badge")).toHaveAttribute("aria-label", "안 읽은 메시지 7개");
  });
});
