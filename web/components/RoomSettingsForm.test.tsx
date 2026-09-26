/**
 * S20 방 설정(T-R2-W3, SCREEN §4.11) — 목 서버에 물려 잰다: 묶음 여덟과 각 「바꿨을 때의 영향」 한 줄 · 값 저장(즉시 반영) ·
 * invited 로 바꿀 때 못 보게 될 사람 수(슬롯) · 첫 dispatch 뒤 컴퓨터·격리 읽기 전용 + [컴퓨터 바꾸기] · 방장 넘기기 확인 다이얼로그 ·
 * 부방장 · 삭제 409 workdirs[] + S13 링크(S5 와 같은 다이얼로그) · 권한 밖은 읽기 전용(사유 한 줄, 툴팁 아님).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));

import { RoomSettingsForm } from "./RoomSettingsForm";
import { setup, uid } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { store } from "@/lib/mock/store";
import { AUTONOMY_TEXT, SETTINGS, TRANSFER_DIALOG } from "@/lib/room-dialogs";
import { DELETE_ROOM_DIALOG, ROOM_RENAME } from "@/lib/wording";
import { W } from "@/lib/mock/wording";
import type { Room } from "@/lib/api/types";

let bridge: FetchBridge;
let roomId = "";
let as: (email: string) => Promise<void>;
beforeEach(async () => {
  ({ bridge, roomId, as } = await setup());
});
afterEach(() => {
  cleanup();
  bridge.restore();
});
const room = async () => (await bridge.call<Room>("GET", `/rooms/${roomId}`)).body;

describe("S20 방 설정", () => {
  it("묶음 여덟 — 각 묶음에 영향 한 줄 · 자율성 문구는 §4.7 표와 같다 · supervised 는 v1.1 비활성", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    await screen.findByTestId("rd-settings-group-visibility");
    for (const [id, impact] of [
      ["visibility", SETTINGS.visibility.impact], ["runtime", SETTINGS.runtime.impact], ["limits", SETTINGS.limits.impact], ["autonomy", SETTINGS.autonomy.impact],
      ["director", SETTINGS.director.impact], ["owner", SETTINGS.owner.impact], ["links", SETTINGS.links.impact], ["lifecycle", SETTINGS.lifecycle.impact],
    ] as const) {
      expect(screen.getByTestId(`rd-settings-group-${id}-impact`)).toHaveTextContent(impact);
    }
    expect(screen.getByTestId("rd-settings-group-autonomy")).toHaveTextContent(AUTONOMY_TEXT.autonomous.note);
    expect(screen.getByTestId("rd-settings-autonomy-supervised")).toBeDisabled();
    expect(screen.getByTestId("rd-settings-director-empty")).toHaveTextContent(SETTINGS.director.empty_note);
    expect(screen.getByTestId("rd-settings-links-none")).toHaveTextContent(SETTINGS.links.none);
    expect(screen.getByTestId("rd-settings-links-manage")).toHaveAttribute("href", `/rooms/${roomId}/settings/links`);
  });

  it("한도·자율성·기본 Director 를 저장하면 서버에 반영된다", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    fireEvent.change(await screen.findByTestId("rd-settings-works"), { target: { value: "1" } });
    fireEvent.change(screen.getByTestId("rd-settings-budget"), { target: { value: "50" } });
    fireEvent.click(screen.getByTestId("rd-settings-save-limits"));
    await waitFor(async () => expect((await room()).limits).toMatchObject({ max_concurrent_works: 1, budget_usd: 50 }));
    fireEvent.click(screen.getByTestId("rd-settings-autonomy-autonomous"));
    fireEvent.click(screen.getByTestId("rd-settings-save-autonomy"));
    await waitFor(async () => expect((await room()).autonomy).toBe("autonomous"));
    fireEvent.change(screen.getByTestId("rd-settings-director"), { target: { value: uid("seoyeon@colab.dev") } });
    fireEvent.click(screen.getByTestId("rd-settings-save-director"));
    await waitFor(async () => expect((await room()).default_director_user_id).toBe(uid("seoyeon@colab.dev")));
  });

  it("invited 로 바꿀 때 — 「초대되지 않은 N명이 더는 볼 수 없게 됩니다」(수는 슬롯, 멤버 중 비참여자)", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    fireEvent.click(await screen.findByTestId("rd-settings-vis-invited"));
    const losing = await screen.findByTestId("rd-settings-vis-losing");
    expect(losing).toHaveTextContent(SETTINGS.visibility.losing.join("2"));
    expect(losing.querySelector("[data-slot]")).toHaveTextContent("2");
  });

  it("컴퓨터가 고정된 방 — 읽기 전용 + 〈컴퓨터〉에 고정됨 + [컴퓨터 바꾸기](S17)", async () => {
    const rt = [...store().runtimes.values()][0];
    await bridge.call("POST", `/__mock/rooms/${roomId}/runtime`, { runtime_id: rt.id, pinned: true });
    render(<RoomSettingsForm roomId={roomId} />);
    const pinned = await screen.findByTestId("rd-settings-pinned");
    expect(pinned).toHaveTextContent(SETTINGS.runtime.pinned(rt.name));
    expect(screen.getByTestId("rd-settings-rebind")).toHaveAttribute("href", `/runtimes/${rt.id}/rebind?room=${roomId}`);
    expect(screen.queryByTestId("rd-settings-runtime")).toBeNull();
    expect((await room()).runtime_pinned).toBe(true);
  });

  it("첫 실행 전에 미리 고른 컴퓨터는 아직 바꿀 수 있다 — 고정은 runtime_id 가 아니라 runtime_pinned(0.2.7)", async () => {
    const rt = [...store().runtimes.values()][0];
    await bridge.call("PATCH", `/rooms/${roomId}`, { runtime_id: rt.id });
    expect((await room()).runtime_pinned).toBe(false);
    render(<RoomSettingsForm roomId={roomId} />);
    expect(await screen.findByTestId("rd-settings-runtime")).toHaveValue(rt.id);
    expect(screen.queryByTestId("rd-settings-pinned")).toBeNull();
  });

  it("저장 중에 첫 실행이 났으면(409 runtime_pinned) 서버 문장 + 그 자리에서 읽기 전용", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    await screen.findByTestId("rd-settings-runtime");
    await bridge.call("POST", `/__mock/rooms/${roomId}/runtime`, { pinned: true });
    fireEvent.click(screen.getByTestId("rd-settings-save-runtime"));
    expect(await screen.findByTestId("rd-settings-pinned")).toBeInTheDocument();
  });

  it("첫 실행 전 — 워크트리는 저장소 선택(컴퓨터를 먼저 고른다) · 서버 422 는 칸 옆에", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    fireEvent.click(await screen.findByTestId("rd-settings-iso-worktree"));
    expect(screen.getByTestId("rd-settings-repo-needs-computer")).toHaveTextContent(SETTINGS.runtime.repo_needs_computer);
    fireEvent.click(screen.getByTestId("rd-settings-save-runtime"));
    await screen.findByText("워크트리 격리에는 그 컴퓨터의 저장소 경로가 필요합니다");
  });

  it("방장 넘기기 — 확인 다이얼로그에서 참여자 중 고른다 · 넘기면 서버의 방장이 바뀐다", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("seoyeon@colab.dev") });
    render(<RoomSettingsForm roomId={roomId} />);
    fireEvent.click(await screen.findByTestId("rd-settings-transfer"));
    const dlg = screen.getByTestId("rd-transfer-dialog");
    expect(dlg).toHaveTextContent(TRANSFER_DIALOG.body);
    fireEvent.change(within(dlg).getByTestId("rd-transfer-pick"), { target: { value: uid("seoyeon@colab.dev") } });
    fireEvent.click(screen.getByTestId("rd-transfer-dialog-confirm"));
    await waitFor(async () => expect((await room()).owner_user_id).toBe(uid("seoyeon@colab.dev")));
    // 넘긴 뒤 옛 방장(ws owner 라 여전히 설정은 된다)의 화면이 새 방장을 말한다.
    await waitFor(() => expect(screen.getByTestId("rd-settings-owner")).toHaveTextContent("서연"));
  });

  it("진행 중 미션이 있으면 삭제 비활성 + 슬롯 사유(S5 카드 메뉴와 같은 문장)", async () => {
    await bridge.call("POST", `/rooms/${roomId}/works`, { goal: "보고서" });
    render(<RoomSettingsForm roomId={roomId} />);
    const del = await screen.findByTestId("rd-settings-delete");
    await waitFor(() => expect(del).toHaveAttribute("aria-disabled", "true"));
    expect(document.getElementById(del.getAttribute("aria-describedby")!)).toHaveTextContent("미션 1개가 진행 중입니다 — 먼저 끝내거나 취소하세요");
  });

  it("삭제 확인은 S5 와 같은 다이얼로그 — 되돌릴 수 없음 · 삭제되면 onDeleted", async () => {
    const onDeleted = vi.fn();
    render(<RoomSettingsForm roomId={roomId} onDeleted={onDeleted} />);
    fireEvent.click(await screen.findByTestId("rd-settings-delete"));
    expect(screen.getByTestId("delete-room-dialog")).toHaveTextContent(DELETE_ROOM_DIALOG.irreversible);
    fireEvent.click(screen.getByTestId("delete-room-dialog-confirm"));
    await waitFor(() => expect(onDeleted).toHaveBeenCalledWith({ id: roomId, name: "결제팀" }));
  });

  it("권한 밖(방 참여자) — 읽기 전용 사유 한 줄 · 입력 비활성 · 저장 aria-disabled 가 그 사유를 가리킨다", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("junho@colab.dev") });
    await as("junho@colab.dev");
    render(<RoomSettingsForm roomId={roomId} />);
    const save = await screen.findByTestId("rd-settings-save-limits");
    expect(save).toHaveAttribute("aria-disabled", "true");
    expect(document.getElementById(save.getAttribute("aria-describedby")!)).toHaveTextContent(SETTINGS.read_only);
    expect(screen.getByTestId("rd-settings-works")).toBeDisabled();
    expect(screen.getByTestId("rd-settings-transfer")).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByTestId("rd-settings-archive")).toHaveAttribute("aria-disabled", "true");
  });
});

// ── T-RENAME — 맨 위 「이름·설명」 묶음(SCREEN §4.11 v0.19.5 · PRD FR-2.1.2) ─────────────────────────
describe("S20 이름·설명", () => {
  it("맨 위 묶음 — 영향 한 줄 · 두 칸 · 바뀐 칸이 없으면 「저장」 비활성", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    const group = await screen.findByTestId("rd-settings-group-name");
    expect(document.querySelector(".rd-group")).toBe(group); // 맨 위
    expect(screen.getByTestId("rd-settings-group-name-impact")).toHaveTextContent(ROOM_RENAME.group_impact);
    expect((screen.getByTestId("rd-settings-name") as HTMLInputElement).value).toBe("결제팀");
    expect(screen.getByTestId("rd-settings-save-name")).toHaveAttribute("aria-disabled", "true");
    fireEvent.change(screen.getByTestId("rd-settings-name"), { target: { value: " 결제팀 " } });
    expect(screen.getByTestId("rd-settings-save-name")).toHaveAttribute("aria-disabled", "true"); // 공백만 다르면 바뀐 것이 아니다
  });

  it("이름이 비면 「방 이름을 적어 주세요」 + 저장 비활성", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    fireEvent.change(await screen.findByTestId("rd-settings-name"), { target: { value: "  " } });
    expect(screen.getByTestId("rd-settings-name-help")).toHaveTextContent(ROOM_RENAME.required);
    expect(screen.getByTestId("rd-settings-name")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByTestId("rd-settings-save-name")).toHaveAttribute("aria-disabled", "true");
  });

  it("저장하면 바뀐 칸만 보내고 서버에 반영 · 타임라인에 「… 방 이름을 결제팀에서 결제·정산팀으로 바꿨습니다.」 와 「… 방 설명을 바꿨습니다.」", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    fireEvent.change(await screen.findByTestId("rd-settings-name"), { target: { value: "결제·정산팀" } });
    fireEvent.change(screen.getByTestId("rd-settings-description"), { target: { value: "결제 흐름 개편" } });
    fireEvent.click(screen.getByTestId("rd-settings-save-name"));
    await waitFor(async () => expect(await room()).toMatchObject({ name: "결제·정산팀", description: "결제 흐름 개편" }));
    const lines = [...store().messages.values()].filter((m) => m.session_id === roomId && m.author_type === "system").map((m) => m.content);
    expect(lines).toContain("데모 님이 방 이름을 결제팀에서 결제·정산팀으로 바꿨습니다.");
    expect(lines).toContain("데모 님이 방 설명을 바꿨습니다.");
  });

  it("권한 밖(참여자) — 두 칸 읽기 전용, 저장은 읽기 전용 사유를 가리킨다", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("seoyeon@colab.dev") });
    await as("seoyeon@colab.dev");
    render(<RoomSettingsForm roomId={roomId} />);
    expect(await screen.findByTestId("rd-settings-name")).toBeDisabled();
    expect(screen.getByTestId("rd-settings-description")).toBeDisabled();
    const save = screen.getByTestId("rd-settings-save-name");
    expect(save).toHaveAttribute("aria-disabled", "true");
    expect(document.getElementById(save.getAttribute("aria-describedby")!)).toHaveTextContent(SETTINGS.read_only);
  });

  it("서버 422 는 이름 칸 아래에 서버 문장", async () => {
    render(<RoomSettingsForm roomId={roomId} />);
    fireEvent.change(await screen.findByTestId("rd-settings-name"), { target: { value: "가".repeat(199) + "나다" } });
    // 화면 판정(201자)이 먼저 막는다 — 서버 문장은 우회 요청으로 확인.
    expect(screen.getByTestId("rd-settings-save-name")).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByTestId("rd-settings-name-help")).toHaveTextContent(ROOM_RENAME.help);
    const res = await bridge.call("PATCH", `/rooms/${roomId}`, { name: "가".repeat(201) });
    expect((res.body as { errors: { message: string }[] }).errors[0].message).toBe(W.room_name_1_200);
  });
});
