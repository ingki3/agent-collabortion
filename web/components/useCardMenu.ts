"use client";
/**
 * 카드 「…」 메뉴의 열고 닫기·키보드 — S5 세션 카드(`SessionCardMenu`, T-W13)와 방 카드(`RoomCardMenu`, T-R2-W1)가 같은 동작을 쓴다.
 *
 * 키보드: 버튼에서 Enter·Space·↓ 로 열고 첫 항목에 초점, ↑↓ 로 항목 이동, Esc 로 닫고 버튼에 초점. 바깥 클릭(마우스·터치)·초점 이탈(focusin·focusout)로 닫는다.
 */
import { useCallback, useEffect, useRef, useState } from "react";

export function useCardMenu() {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);

  const close = useCallback((refocus = false) => {
    setOpen(false);
    if (refocus) button.current?.focus();
  }, []);

  // 바깥 클릭(마우스·터치)·초점 이탈 — document 에서 듣는다(카드 링크를 눌러 이동해도 남지 않게).
  // touchstart: 터치 기기는 mousedown 이 늦거나(300ms) 스크롤 제스처에서 안 온다(W-14, PR #219 NN3).
  // focusin(document): 바깥 요소가 초점을 **받을 때**. focusout(root, 아래 onRootBlur): 메뉴 안 요소가 초점을 **잃을 때** — relatedTarget 이
  // 바깥이면 닫는다. 둘을 같이 두는 이유: 초점이 바깥 요소로 가면 둘 다 잡히지만, 프로그램이 focus() 로 옮기는 경우와 브라우저에 따라
  // 어느 한쪽만 오는 경우가 있다. relatedTarget 이 null(창 전환·비초점 영역 클릭)이면 여기서는 닫지 않는다 — 메뉴 여백을 누른 것일 수
  // 있고, 바깥이면 mousedown/touchstart 가 이미 닫는다.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent | TouchEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) close();
    };
    const onFocus = (e: FocusEvent) => {
      if (root.current && e.target instanceof Node && !root.current.contains(e.target)) close();
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("touchstart", onDown);
    document.addEventListener("focusin", onFocus);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("touchstart", onDown);
      document.removeEventListener("focusin", onFocus);
    };
  }, [open, close]);
  const onRootBlur = (e: React.FocusEvent) => {
    if (open && e.relatedTarget instanceof Node && root.current && !root.current.contains(e.relatedTarget)) close();
  };

  const items = () => Array.from(root.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? []);
  const focusItem = (i: number) => {
    const list = items();
    if (!list.length) return;
    list[((i % list.length) + list.length) % list.length].focus();
  };

  function onButtonKey(e: React.KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setOpen(true);
      // 열린 뒤 첫(마지막) 항목으로 — 렌더 뒤에 초점을 준다.
      requestAnimationFrame(() => focusItem(e.key === "ArrowDown" ? 0 : -1));
    }
  }
  function onMenuKey(e: React.KeyboardEvent) {
    const list = items();
    const at = list.indexOf(document.activeElement as HTMLElement);
    if (e.key === "Escape") {
      e.preventDefault();
      close(true);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      focusItem(at + 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      focusItem(at - 1);
    } else if (e.key === "Home") {
      e.preventDefault();
      focusItem(0);
    } else if (e.key === "End") {
      e.preventDefault();
      focusItem(-1);
    } else if (e.key === "Tab") {
      close();
    }
  }

  const toggle = () => (open ? close() : setOpen(true));
  return { open, root, button, close, toggle, onRootBlur, onButtonKey, onMenuKey };
}
