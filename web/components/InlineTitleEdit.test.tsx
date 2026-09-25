/**
 * T-RENAME — Inline Title Edit(COMPONENTS §9.9) 와 S7 방 머리의 이름 그 자리 편집(SCREEN §4.6 v0.19.5 · PRD FR-2.1.2).
 * 보기(권한자 ✎ / 권한 없음 글자만) · 편집(Enter 저장 · Esc·취소·바깥 클릭 취소) · 저장 중 · 오류(403 · 422) · 동시 편집 · aria.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { InlineTitleEdit, roomNameProblem } from "./InlineTitleEdit";
import { RoomHead } from "./RoomHead";
import { ROOM_RENAME } from "@/lib/wording";
import { problemFixture } from "@/lib/mock/problem-fixture";
import { W } from "@/lib/mock/wording";
import type { Room } from "@/lib/api/types";

afterEach(cleanup);

const baseRoom = {
  id: "r1", workspace_id: "ws1", name: "결제팀", description: "결제 관련 논의와 작업", status: "active", visibility: "workspace", owner_user_id: "u1", deputy_owner_user_id: null,
  runtime_id: null, isolation: { kind: "none", remote_url: null }, limits: {}, autonomy: "guided", default_director_user_id: null, blocked_reason: null, blocked_detail: null,
  counts: { works_active: 0, lanes_active: 0, tasks_active: 0 }, cost_usd: 0, cost_estimated: false, unread_count: 0, my_room_role: "owner",
  my_capabilities: ["post", "invite", "configure", "archive", "delete", "summarize"], created_by: "u1", created_at: "", updated_at: "", last_activity_at: null,
} as unknown as Room;

function head(room: Room, onRename = vi.fn(async (_n: string) => undefined)) {
  const props = {
    room, needs: 0, onJumpNeed: vi.fn(), onParticipants: vi.fn(), onLeave: vi.fn(), onBlock: vi.fn(), onUnblock: vi.fn(), onSummarize: vi.fn(async () => undefined),
    onArchive: vi.fn(), onUnarchive: vi.fn(), onDelete: vi.fn(), runningTurns: 0, openWorks: 0, onRename,
  };
  return { onRename, ...render(<RoomHead {...props} />) };
}
const enterEdit = () => fireEvent.click(screen.getByTestId("room-title-pencil"));
const input = () => screen.getByTestId("room-title-input") as HTMLInputElement;

describe("S7 방 머리 — 보기", () => {
  it("권한자(configure)에게만 ✎ 가 있고 aria-label 「방 이름 바꾸기」 · 이름은 h1", () => {
    head(baseRoom);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("결제팀");
    expect(screen.getByRole("button", { name: ROOM_RENAME.edit })).toBe(screen.getByTestId("room-title-pencil"));
  });

  it("권한 없는 사람(configure 없음)에게는 ✎ 가 없고 이름을 눌러도 편집 칸이 열리지 않는다 — 비활성 버튼도 두지 않는다", () => {
    head({ ...baseRoom, my_room_role: "member", my_capabilities: ["post", "summarize"] } as Room);
    expect(screen.queryByTestId("room-title-pencil")).toBeNull();
    expect(screen.queryByRole("button", { name: ROOM_RENAME.edit })).toBeNull();
    fireEvent.click(screen.getByTestId("room-title-text"));
    expect(screen.queryByTestId("room-title-input")).toBeNull();
    expect(screen.getByTestId("room-title-text").className).not.toContain("editable");
  });
});

describe("S7 방 머리 — 편집", () => {
  it("✎ → 입력 칸(aria-label 「방 이름」, 도움말 aria-describedby) · Enter 로 저장하면 앞뒤 공백을 뗀 이름으로 부른다", async () => {
    const { onRename } = head(baseRoom);
    enterEdit();
    expect(input()).toHaveAttribute("aria-label", ROOM_RENAME.input_label);
    expect(input().value).toBe("결제팀");
    expect(document.getElementById(input().getAttribute("aria-describedby")!)).toHaveTextContent(ROOM_RENAME.help);
    fireEvent.change(input(), { target: { value: "  결제·정산팀 " } });
    fireEvent.submit(screen.getByTestId("room-title-form"));
    await waitFor(() => expect(onRename).toHaveBeenCalledWith("결제·정산팀"));
    await waitFor(() => expect(screen.queryByTestId("room-title-input")).toBeNull());
  });

  it("글자를 눌러도 편집에 들어간다 · Esc 는 취소(저장하지 않는다) · 「취소」도", () => {
    const { onRename } = head(baseRoom);
    fireEvent.click(screen.getByTestId("room-title-text"));
    fireEvent.change(input(), { target: { value: "딴 이름" } });
    fireEvent.keyDown(input(), { key: "Escape" });
    expect(screen.queryByTestId("room-title-input")).toBeNull();
    expect(screen.getByTestId("room-title-text")).toHaveTextContent("결제팀");
    enterEdit();
    fireEvent.change(input(), { target: { value: "딴 이름" } });
    fireEvent.click(screen.getByTestId("room-title-cancel"));
    expect(screen.queryByTestId("room-title-input")).toBeNull();
    expect(onRename).not.toHaveBeenCalled();
  });

  it("바깥을 누르면 취소 — 말없이 저장하지 않는다 · 칸 안의 「저장」 으로 옮기는 것은 바깥이 아니다", () => {
    const { onRename } = head(baseRoom);
    enterEdit();
    fireEvent.change(input(), { target: { value: "딴 이름" } });
    fireEvent.blur(input(), { relatedTarget: screen.getByTestId("room-title-save") });
    expect(screen.getByTestId("room-title-input")).toBeInTheDocument();
    fireEvent.blur(input(), { relatedTarget: document.body });
    expect(screen.queryByTestId("room-title-input")).toBeNull();
    expect(onRename).not.toHaveBeenCalled();
  });

  it("비면 「방 이름을 적어 주세요」 + 저장 비활성(공백만도) · 바뀌지 않았으면 부르지 않는다", () => {
    const { onRename } = head(baseRoom);
    enterEdit();
    fireEvent.change(input(), { target: { value: "   " } });
    expect(screen.getByTestId("room-title-save")).toBeDisabled();
    expect(screen.getByTestId("room-title-help")).toHaveTextContent(ROOM_RENAME.required);
    expect(input()).toHaveAttribute("aria-invalid", "true");
    fireEvent.submit(screen.getByTestId("room-title-form"));
    expect(onRename).not.toHaveBeenCalled();
    fireEvent.change(input(), { target: { value: " 결제팀 " } });
    fireEvent.submit(screen.getByTestId("room-title-form"));
    expect(onRename).not.toHaveBeenCalled();
    expect(screen.queryByTestId("room-title-input")).toBeNull();
    expect(roomNameProblem("가".repeat(201))).toBe(ROOM_RENAME.help);
    expect(roomNameProblem(` ${"가".repeat(200)} `)).toBeNull();
  });

  it("저장 중 — 칸이 흐려지고 「저장 중…」, 버튼 비활성 · 실패하면 칸을 두고 403 은 누구의 일인지(aria-describedby · role=alert)", async () => {
    let reject!: (e: unknown) => void;
    const onRename = vi.fn(() => new Promise((_res, rej) => (reject = rej)));
    head(baseRoom, onRename as never);
    enterEdit();
    fireEvent.change(input(), { target: { value: "새 이름" } });
    fireEvent.submit(screen.getByTestId("room-title-form"));
    expect(screen.getByTestId("room-title-save")).toHaveTextContent(ROOM_RENAME.saving);
    expect(screen.getByTestId("room-title-save")).toBeDisabled();
    expect(screen.getByTestId("room-title-cancel")).toBeDisabled();
    expect(screen.getByTestId("room-title-form").className).toContain("title-edit--saving");
    await act(async () => reject(problemFixture("forbidden", 403, { detail: "권한이 없습니다" })));
    expect(input().value).toBe("새 이름");
    const help = screen.getByTestId("room-title-help");
    expect(help).toHaveTextContent(ROOM_RENAME.forbidden);
    expect(help).toHaveAttribute("role", "alert");
    expect(input().getAttribute("aria-describedby")).toBe(help.id);
    expect(screen.getByTestId("room-title-form").className).toContain("title-edit--error");
  });

  it("422 는 name 칸의 서버 문장을 그대로", async () => {
    const onRename = vi.fn(async () => {
      throw problemFixture("validation_failed", 422, { detail: "입력값을 확인해 주세요", errors: [{ field: "name", message: W.room_name_1_200 }] });
    });
    head(baseRoom, onRename as never);
    enterEdit();
    fireEvent.change(input(), { target: { value: "새 이름" } });
    fireEvent.submit(screen.getByTestId("room-title-form"));
    expect(await screen.findByText(W.room_name_1_200)).toBeInTheDocument();
  });
});

describe("동시 편집 — room.updated 로 value 가 바뀌면", () => {
  it("편집 중이 아니면 새 이름으로 갈아 끼운다 · 편집 중이면 칸을 유지하고 「다른 사람이 이름을 〈새〉(으)로 바꿨습니다」 — 저장하면 내 것", async () => {
    const onSave = vi.fn(async () => undefined);
    const { rerender } = render(<InlineTitleEdit value="결제팀" canEdit onSave={onSave} />);
    rerender(<InlineTitleEdit value="정산팀" canEdit onSave={onSave} />);
    expect(screen.getByTestId("title-edit-text")).toHaveTextContent("정산팀");
    fireEvent.click(screen.getByTestId("title-edit-pencil"));
    fireEvent.change(screen.getByTestId("title-edit-input"), { target: { value: "내 이름" } });
    rerender(<InlineTitleEdit value="리서치" canEdit onSave={onSave} />);
    expect((screen.getByTestId("title-edit-input") as HTMLInputElement).value).toBe("내 이름");
    expect(screen.getByTestId("title-edit-elsewhere")).toHaveTextContent("다른 사람이 이름을 리서치(으)로 바꿨습니다");
    fireEvent.submit(screen.getByTestId("title-edit-form"));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith("내 이름"));
  });

  it("내 저장의 room.updated 가 응답보다 먼저 와도 「다른 사람이」 를 띄우지 않는다", async () => {
    let resolve!: () => void;
    const onSave = vi.fn(() => new Promise<void>((r) => (resolve = r)));
    const { rerender } = render(<InlineTitleEdit value="결제팀" canEdit onSave={onSave} />);
    fireEvent.click(screen.getByTestId("title-edit-pencil"));
    fireEvent.change(screen.getByTestId("title-edit-input"), { target: { value: "정산팀" } });
    fireEvent.submit(screen.getByTestId("title-edit-form"));
    rerender(<InlineTitleEdit value="정산팀" canEdit onSave={onSave} />);
    expect(screen.queryByTestId("title-edit-elsewhere")).toBeNull();
    await act(async () => resolve());
  });
});
