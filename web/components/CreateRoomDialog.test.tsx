/**
 * S18 방 만들기(T-R2-W1, SCREEN §4.5) — 칸 하나 · 이름 자동 포커스 · 비면 「만들기」 비활성 + 사유 · ⓘ 한 줄은 워크스페이스 기본값에서 ·
 * 「방 설정」 은 만들기 전 비활성 · 같은 이름이면 경고(막지 않는다) · 만들기 = createRoom(멱등키) · 422 는 칸 옆에 서버 문장 그대로 · Esc 로 닫힘.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

const get = vi.fn();
const post = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { ...actual.api, get: (...a: unknown[]) => get(...a), post: (...a: unknown[]) => post(...a) } };
});

import { CreateRoomDialog } from "./CreateRoomDialog";
import { problemFixture } from "@/lib/mock/problem-fixture";
import { W } from "@/lib/mock/wording";

let settings: Record<string, unknown> | Error;
let existing: { name: string }[];
beforeEach(() => {
  get.mockReset();
  post.mockReset();
  settings = { default_isolation: "none" };
  existing = [];
  get.mockImplementation((path: string) => {
    if (path.endsWith("/settings")) return settings instanceof Error ? Promise.reject(settings) : Promise.resolve(settings);
    if (path.endsWith("/rooms")) return Promise.resolve({ items: existing, next_cursor: null });
    return Promise.resolve({});
  });
});
afterEach(cleanup);

const mount = (over: Partial<Parameters<typeof CreateRoomDialog>[0]> = {}) => {
  const onCreated = vi.fn();
  const onClose = vi.fn();
  render(<CreateRoomDialog workspaceId="w1" onCreated={onCreated} onClose={onClose} {...over} />);
  return { onCreated, onClose };
};

describe("S18 방 만들기", () => {
  it("이름에 자동 포커스 · 비어 있으면 「만들기」 비활성 + 「방 이름을 적어 주세요」 · 누르면 아무것도 만들지 않는다", async () => {
    mount();
    expect(document.activeElement).toBe(screen.getByTestId("create-room-name"));
    const submit = screen.getByTestId("create-room-submit");
    expect(submit).toHaveAttribute("aria-disabled", "true");
    expect(document.getElementById(submit.getAttribute("aria-describedby")!)).toHaveTextContent("방 이름을 적어 주세요");
    fireEvent.click(submit);
    expect(post).not.toHaveBeenCalled();
  });

  it("ⓘ 한 줄은 워크스페이스 기본값을 읽는다 — none 이면 「격리 없음」, room_defaults 가 worktree 면 「워크트리로 나눔」", async () => {
    mount();
    await waitFor(() => expect(get).toHaveBeenCalledWith("/workspaces/{workspaceId}/settings", { path: { workspaceId: "w1" } }));
    expect(screen.getByTestId("create-room-defaults")).toHaveTextContent("격리 없음 · 컴퓨터는 첫 실행 때 정해집니다");
    expect(screen.getByTestId("create-room-info")).toHaveTextContent("방 설정에서 미리 바꿀 수 있습니다");
    cleanup();
    settings = { default_isolation: "none", room_defaults: { isolation_kind: "worktree" } };
    mount();
    await waitFor(() => expect(screen.getByTestId("create-room-defaults")).toHaveTextContent("워크트리로 나눔 · 컴퓨터는 첫 실행 때 정해집니다"));
  });

  it("설정을 못 읽어도 만들기는 된다 — ⓘ 는 계약 기본값(격리 없음)", async () => {
    settings = new Error("403");
    mount();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(screen.getByTestId("create-room-defaults")).toHaveTextContent("격리 없음");
  });

  it("「방 설정」 은 만들기 전 비활성 + 「만든 뒤 바꿀 수 있습니다」(마법사가 되지 않게)", () => {
    mount();
    const btn = screen.getByTestId("create-room-settings");
    expect(btn).toHaveAttribute("aria-disabled", "true");
    expect(document.getElementById(btn.getAttribute("aria-describedby")!)).toHaveTextContent("만든 뒤 바꿀 수 있습니다");
  });

  it("같은 이름이 있으면 경고 한 줄 — listRooms?q=(참여 무관 · 보관 포함) · 막지 않는다", async () => {
    existing = [{ name: "결제팀" }, { name: "결제팀 2" }];
    mount();
    vi.useFakeTimers();
    try {
      fireEvent.change(screen.getByTestId("create-room-name"), { target: { value: " 결제팀 " } });
      await act(async () => {
        vi.advanceTimersByTime(300);
      });
    } finally {
      vi.useRealTimers();
    }
    await waitFor(() => expect(screen.getByTestId("create-room-duplicate")).toHaveTextContent("같은 이름의 방이 이미 있습니다"));
    expect(get).toHaveBeenCalledWith("/workspaces/{workspaceId}/rooms", { path: { workspaceId: "w1" }, query: { q: "결제팀", participating: false, include_archived: true } });
    expect(screen.getByTestId("create-room-submit")).not.toHaveAttribute("aria-disabled");
  });

  it("부분 일치만 있으면 경고하지 않는다", async () => {
    existing = [{ name: "결제팀 2" }];
    mount();
    fireEvent.change(screen.getByTestId("create-room-name"), { target: { value: "결제팀" } });
    await waitFor(() => expect(get).toHaveBeenCalledWith("/workspaces/{workspaceId}/rooms", expect.anything()), { timeout: 1000 });
    expect(screen.queryByTestId("create-room-duplicate")).toBeNull();
  });

  it("만들기 → createRoom(이름·설명 다듬어서, 멱등키 하나) → onCreated", async () => {
    const room = { id: "r9", name: "결제팀" };
    post.mockResolvedValue(room);
    const { onCreated } = mount();
    fireEvent.change(screen.getByTestId("create-room-name"), { target: { value: "  결제팀 " } });
    fireEvent.change(screen.getByTestId("create-room-description"), { target: { value: " 결제 관련 논의와 작업 " } });
    fireEvent.click(screen.getByTestId("create-room-submit"));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(room));
    const [path, opts] = post.mock.calls[0];
    expect(path).toBe("/workspaces/{workspaceId}/rooms");
    expect(opts).toMatchObject({ path: { workspaceId: "w1" }, body: { name: "결제팀", description: "결제 관련 논의와 작업" } });
    expect(typeof opts.idempotencyKey).toBe("string");
  });

  it("422 — 칸 옆에 서버 문장 그대로 · 다시 눌러도 같은 멱등키", async () => {
    post.mockRejectedValueOnce(problemFixture("validation_failed", 422, { detail: "입력값을 확인해 주세요", errors: [{ field: "name", message: W.room_name_1_200 }] }));
    mount();
    fireEvent.change(screen.getByTestId("create-room-name"), { target: { value: "가" } });
    fireEvent.click(screen.getByTestId("create-room-submit"));
    expect(await screen.findByText(W.room_name_1_200)).toBeInTheDocument();
    expect(screen.getByTestId("create-room-name")).toHaveAttribute("aria-invalid", "true");
    post.mockResolvedValueOnce({ id: "r1" });
    fireEvent.click(screen.getByTestId("create-room-submit"));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    expect(post.mock.calls[1][1].idempotencyKey).toBe(post.mock.calls[0][1].idempotencyKey);
  });

  it("Esc·취소로 닫힌다 — 아무것도 만들지 않는다", () => {
    const { onClose } = mount();
    fireEvent.keyDown(screen.getByTestId("create-room-dialog"), { key: "Escape" });
    fireEvent.click(screen.getByTestId("create-room-cancel"));
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(post).not.toHaveBeenCalled();
    expect(screen.getByTestId("create-room-dialog")).toHaveAttribute("role", "dialog");
  });
});
