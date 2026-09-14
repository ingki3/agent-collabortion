/**
 * S5 카드 「…」 메뉴(T-W13, SCREEN §4.3) — 열기·닫기·키보드, 삭제 활성/비활성 4조합(상태 × 권한)과 사유.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SessionCardMenu } from "./SessionCardMenu";
import { deleteGate, SESSION_MENU } from "@/lib/wording";

afterEach(cleanup);

function mount(gate = deleteGate("completed", true), onDelete = vi.fn()) {
  render(
    <div>
      <button type="button" data-testid="outside">밖</button>
      <SessionCardMenu href="/sessions/s1" gate={gate} onDelete={onDelete} testId="s1" />
    </div>,
  );
  return { onDelete, button: screen.getByRole("button", { name: SESSION_MENU.button }) };
}

describe("열기·닫기", () => {
  it("버튼은 aria-label 「세션 옵션」· haspopup=menu · 닫힌 채로 시작하고, 클릭으로 열고 다시 클릭으로 닫는다", () => {
    const { button } = mount();
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByRole("menu", { name: SESSION_MENU.button })).toBeTruthy();
    // 항목 둘 — 세션 열기(링크, 카드와 같은 href)·삭제
    const items = screen.getAllByRole("menuitem");
    expect(items.map((i) => i.textContent)).toEqual([SESSION_MENU.open, SESSION_MENU.delete]);
    expect(items[0].getAttribute("href")).toBe("/sessions/s1");
    fireEvent.click(button);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("바깥 클릭으로 닫힌다", () => {
    const { button } = mount();
    fireEvent.click(button);
    expect(screen.getByRole("menu")).toBeTruthy();
    fireEvent.mouseDown(screen.getByTestId("outside"));
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("키보드 — ↓ 로 열고 첫 항목에 초점, ↓↑ 이동, Esc 로 닫고 버튼으로 초점 복귀", async () => {
    const { button } = mount();
    button.focus();
    fireEvent.keyDown(button, { key: "ArrowDown" });
    expect(screen.getByRole("menu")).toBeTruthy();
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    const [open, del] = screen.getAllByRole("menuitem");
    expect(document.activeElement).toBe(open);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "ArrowDown" });
    expect(document.activeElement).toBe(del);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "ArrowDown" }); // 끝에서 처음으로
    expect(document.activeElement).toBe(open);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "ArrowUp" });
    expect(document.activeElement).toBe(del);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(button);
  });

  it("「세션 열기」 를 누르면 메뉴가 닫힌다(링크 이동은 Link 의 몫)", () => {
    const { button } = mount();
    fireEvent.click(button);
    // jsdom 은 링크 이동을 구현하지 않는다 — 기본 동작만 막고 메뉴가 닫히는지 본다.
    document.addEventListener("click", (e) => e.preventDefault(), { once: true });
    fireEvent.click(screen.getByTestId("session-menu-open"));
    expect(screen.queryByRole("menu")).toBeNull();
  });
});

describe("삭제 — 상태 × 권한 4조합 (SCREEN §4.3 · 계약 deleteSession)", () => {
  it("끝난 세션 + 권한 있음 → 활성, 누르면 onDelete 가 불리고 메뉴가 닫힌다", () => {
    const { button, onDelete } = mount(deleteGate("completed", true));
    fireEvent.click(button);
    const del = screen.getByTestId("session-menu-delete");
    expect(del.getAttribute("aria-disabled")).toBeNull();
    expect(screen.queryByText(SESSION_MENU.blocked_active)).toBeNull();
    expect(screen.queryByText(SESSION_MENU.blocked_role)).toBeNull();
    fireEvent.click(del);
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("진행 중 + 권한 있음 → 비활성 + 「진행 중인 세션은 먼저 종료하세요」, 눌러도 onDelete 없음", () => {
    const { button, onDelete } = mount(deleteGate("active", true));
    fireEvent.click(button);
    const del = screen.getByTestId("session-menu-delete");
    expect(del.getAttribute("aria-disabled")).toBe("true");
    const hint = document.getElementById(del.getAttribute("aria-describedby")!)!;
    expect(hint.textContent).toBe(SESSION_MENU.blocked_active);
    expect(del.getAttribute("title")).toBe(SESSION_MENU.blocked_active);
    fireEvent.click(del);
    expect(onDelete).not.toHaveBeenCalled();
    expect(screen.getByRole("menu")).toBeTruthy(); // 비활성 항목은 메뉴를 닫지 않는다 — 사유를 읽어야 한다
  });

  it("끝난 세션 + 권한 없음 → 비활성 + 「Director 나 소유자·관리자만 삭제할 수 있습니다」", () => {
    const { button, onDelete } = mount(deleteGate("cancelled", false));
    fireEvent.click(button);
    const del = screen.getByTestId("session-menu-delete");
    expect(del.getAttribute("aria-disabled")).toBe("true");
    expect(document.getElementById(del.getAttribute("aria-describedby")!)!.textContent).toBe(SESSION_MENU.blocked_role);
    fireEvent.click(del);
    expect(onDelete).not.toHaveBeenCalled();
  });

  it("진행 중 + 권한 없음 → 상태 사유가 먼저다(먼저 종료가 다음 행동)", () => {
    const { button } = mount(deleteGate("paused", false));
    fireEvent.click(button);
    const del = screen.getByTestId("session-menu-delete");
    expect(del.getAttribute("aria-disabled")).toBe("true");
    expect(document.getElementById(del.getAttribute("aria-describedby")!)!.textContent).toBe(SESSION_MENU.blocked_active);
  });

  it.each([["draft", true], ["completed", true], ["cancelled", true], ["active", false], ["paused", false], ["completing", false]] as const)(
    "deleteGate — %s 는 삭제 가능 %s (계약: draft·completed·cancelled 만)",
    (status, ok) => {
      expect(deleteGate(status, true).ok).toBe(ok);
    },
  );
});
