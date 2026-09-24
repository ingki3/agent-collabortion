/**
 * S5 방 카드 「…」 메뉴(T-R2-W1, SCREEN §4.3) — 열기·닫기·키보드(`useCardMenu`)와 비활성 항목의 동작.
 *
 * 훅 테스트(↓↑·Esc·바깥 클릭·touchstart·focusout)는 원래 세션 카드 메뉴 테스트에만 있었다 — R1.5b(#318)가 `SessionCardMenu` 를
 * 지우며 함께 사라졌으므로(리뷰 NN1) 같은 훅을 쓰는 방 카드 메뉴로 옮긴다. 상태 × 권한 사유 조합은 `app/(app)/rooms/page.test.tsx` 가 잰다.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { RoomCardMenu } from "./RoomCardMenu";
import { ROOM_MENU, type RoomGate } from "@/lib/wording";

afterEach(cleanup);

const OK: RoomGate = { ok: true };
const NO_ROLE: RoomGate = { ok: false, reason: ROOM_MENU.delete_role };

function mount({ archived = false, archiveGate = OK, deleteGate = OK }: { archived?: boolean; archiveGate?: RoomGate; deleteGate?: RoomGate } = {}) {
  const onArchive = vi.fn();
  const onUnarchive = vi.fn();
  const onDelete = vi.fn();
  render(
    <div>
      <button type="button" data-testid="outside">밖</button>
      <RoomCardMenu
        archived={archived}
        archiveGate={archiveGate}
        deleteGate={deleteGate}
        onArchive={onArchive}
        onUnarchive={onUnarchive}
        onDelete={onDelete}
        testId="r1"
      />
    </div>,
  );
  return { onArchive, onUnarchive, onDelete, button: screen.getByRole("button", { name: ROOM_MENU.button }) };
}

describe("열기·닫기 (useCardMenu)", () => {
  it("버튼은 aria-label 「방 옵션」· haspopup=menu · 닫힌 채로 시작하고, 클릭으로 열고 다시 클릭으로 닫는다", () => {
    const { button } = mount();
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByRole("menu", { name: ROOM_MENU.button })).toBeTruthy();
    const items = screen.getAllByRole("menuitem");
    expect(items.map((i) => i.getAttribute("data-testid"))).toEqual(["room-menu-archive", "room-menu-delete"]);
    fireEvent.click(button);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("바깥 클릭(mousedown)으로 닫히고, 메뉴 안 mousedown 은 닫지 않는다", () => {
    const { button } = mount();
    fireEvent.click(button);
    fireEvent.mouseDown(screen.getByTestId("room-menu-delete"));
    expect(screen.getByRole("menu")).toBeTruthy();
    fireEvent.mouseDown(screen.getByTestId("outside"));
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("바깥 터치(touchstart)로도 닫힌다 — 터치 기기는 mousedown 이 늦거나 안 온다(W-14)", () => {
    const { button } = mount();
    fireEvent.click(button);
    fireEvent.touchStart(screen.getByTestId("outside"));
    expect(screen.queryByRole("menu")).toBeNull();
    // 메뉴 안 터치는 닫지 않는다.
    fireEvent.click(button);
    fireEvent.touchStart(screen.getByTestId("room-menu-delete"));
    expect(screen.getByRole("menu")).toBeTruthy();
  });

  it("바깥 요소가 초점을 받으면(focusin) 닫힌다", () => {
    const { button } = mount();
    fireEvent.click(button);
    fireEvent.focusIn(screen.getByTestId("outside"));
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("초점 이탈(focusout, relatedTarget 이 바깥)로 닫히고, 메뉴 안으로의 이동·relatedTarget 없음은 닫지 않는다(W-14)", () => {
    const { button } = mount();
    fireEvent.click(button);
    const del = screen.getByTestId("room-menu-delete");
    fireEvent.blur(del, { relatedTarget: screen.getByTestId("room-menu-archive") });
    expect(screen.getByRole("menu")).toBeTruthy();
    // relatedTarget 없음(창 전환·메뉴 여백 클릭) — 여기서는 닫지 않는다(바깥이면 mousedown 이 닫는다).
    fireEvent.blur(del, { relatedTarget: null });
    expect(screen.getByRole("menu")).toBeTruthy();
    fireEvent.blur(del, { relatedTarget: screen.getByTestId("outside") });
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("키보드 — ↓ 로 열고 첫 항목에 초점, ↓↑ 순환 이동, Home·End, Esc 로 닫고 버튼으로 초점 복귀", async () => {
    const { button } = mount();
    button.focus();
    fireEvent.keyDown(button, { key: "ArrowDown" });
    expect(screen.getByRole("menu")).toBeTruthy();
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    const [archive, del] = screen.getAllByRole("menuitem");
    const menu = screen.getByRole("menu");
    expect(document.activeElement).toBe(archive);
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(del);
    fireEvent.keyDown(menu, { key: "ArrowDown" }); // 끝에서 처음으로
    expect(document.activeElement).toBe(archive);
    fireEvent.keyDown(menu, { key: "ArrowUp" }); // 처음에서 끝으로
    expect(document.activeElement).toBe(del);
    fireEvent.keyDown(menu, { key: "Home" });
    expect(document.activeElement).toBe(archive);
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement).toBe(del);
    fireEvent.keyDown(menu, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(button);
  });

  it("키보드 — ↑ 로 열면 마지막 항목에 초점", async () => {
    const { button } = mount();
    button.focus();
    fireEvent.keyDown(button, { key: "ArrowUp" });
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(document.activeElement).toBe(screen.getByTestId("room-menu-delete"));
  });

  it("Tab 은 메뉴를 닫는다(초점은 브라우저가 다음 요소로)", () => {
    const { button } = mount();
    fireEvent.click(button);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Tab" });
    expect(screen.queryByRole("menu")).toBeNull();
  });
});

describe("항목", () => {
  it("활성 항목을 누르면 콜백이 불리고 메뉴가 닫힌다 — 보관된 방이면 「보관 해제」", () => {
    const a = mount();
    fireEvent.click(a.button);
    fireEvent.click(screen.getByTestId("room-menu-archive"));
    expect(a.onArchive).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
    cleanup();

    const b = mount({ archived: true });
    fireEvent.click(b.button);
    expect(screen.getByTestId("room-menu-unarchive").textContent).toBe(ROOM_MENU.unarchive);
    fireEvent.click(screen.getByTestId("room-menu-unarchive"));
    expect(b.onUnarchive).toHaveBeenCalledTimes(1);
    expect(b.onArchive).not.toHaveBeenCalled();
  });

  it("비활성 항목 — aria-disabled + 사유가 aria-describedby 로 묶이고, 눌러도 콜백 없이 메뉴가 남는다(사유를 읽어야 한다)", () => {
    const { button, onDelete } = mount({ deleteGate: NO_ROLE });
    fireEvent.click(button);
    const del = screen.getByTestId("room-menu-delete");
    expect(del.getAttribute("aria-disabled")).toBe("true");
    expect(document.getElementById(del.getAttribute("aria-describedby")!)!.textContent).toBe(ROOM_MENU.delete_role);
    fireEvent.click(del);
    expect(onDelete).not.toHaveBeenCalled();
    expect(screen.getByRole("menu")).toBeTruthy();
    // 삭제 항목은 「되돌릴 수 없음」 꼬리를 단다.
    expect(del.textContent).toContain(ROOM_MENU.delete_tail);
  });

  it("사유에 수가 들면 슬롯으로 그린다 — 「미션 N개가 진행 중입니다」", () => {
    const { button } = mount({ deleteGate: { ok: false, reason: ROOM_MENU.delete_works, count: 2 } });
    fireEvent.click(button);
    const del = screen.getByTestId("room-menu-delete");
    expect(document.getElementById(del.getAttribute("aria-describedby")!)!.textContent).toBe(`${ROOM_MENU.delete_works[0]}2${ROOM_MENU.delete_works[1]}`);
  });
});
