"use client";
/**
 * S5 세션 카드의 「…」 옵션 메뉴(SCREEN §4.3 「카드 옵션(…) — 삭제」, T-W13).
 *
 * 카드는 `<Link>` 하나가 통째였다 — 그 안에 버튼을 넣으면 a 안의 button(HTML 위반)이라, 카드는 `article` 이 되고 링크와
 * 이 메뉴는 **형제**다(`sessions/page.tsx`). 그래서 이 버튼을 눌러도 카드 이동이 일어나지 않고, 링크의 클릭 영역·키보드
 * 이동은 그대로다.
 *
 * 항목은 둘 — 「세션 열기」(링크와 같은 곳) · 「삭제」. 삭제는 **숨기지 않고** 비활성 + 사유(§8.5 · SCREEN §5 권한 비활성 버튼):
 * 사유는 `deleteGate`(lib/wording.ts) 가 고른다 — 진행 중이면 상태 사유, 권한이 없으면 역할 사유(문장은 그 표에만 있다).
 * 화면은 판정하지 않는다(서버가 409·403 으로 다시 검사한다).
 *
 * 키보드: 버튼에서 Enter·Space·↓ 로 열고 첫 항목에 초점, ↑↓ 로 항목 이동, Esc 로 닫고 버튼에 초점. 바깥 클릭(마우스·터치)·초점 이탈(focusin·focusout)로 닫는다.
 */
import { useCallback, useEffect, useId, useRef, useState } from "react";
import Link from "next/link";
import { Icon } from "./Icon";
import { DisabledHint } from "./PageHead";
import { SESSION_MENU, type DeleteGate } from "@/lib/wording";
import "./session-card-menu.css";

export interface SessionCardMenuProps {
  /** 「세션 열기」 가 가는 곳 — 카드 링크와 같은 href 를 넘긴다. */
  href: string;
  /** 삭제 가부와 사유(`deleteGate`). */
  gate: DeleteGate;
  /** 「삭제」 — 확인 다이얼로그를 여는 것은 호출부의 몫이다(여기서 API 를 부르지 않는다). */
  onDelete: () => void;
  /** data-testid 접미 — 카드마다 다르게. */
  testId?: string;
}

export function SessionCardMenu({ href, gate, onDelete, testId }: SessionCardMenuProps) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const hintId = `${useId()}-hint`;

  const close = useCallback((refocus = false) => {
    setOpen(false);
    if (refocus) button.current?.focus();
  }, []);

  // 바깥 클릭(마우스·터치)·초점 이탈 — document 에서 듣는다(카드 링크를 눌러 이동해도 남지 않게).
  // touchstart: 터치 기기는 mousedown 이 늦거나(300ms) 스크롤 제스처에서 안 온다(W-14, PR #219 NN3).
  // focusin(document): 바깥 요소가 초점을 **받을 때**. focusout(root, 아래 onBlur): 메뉴 안 요소가 초점을 **잃을 때** — relatedTarget 이
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

  return (
    <div className="card-menu" ref={root} onBlur={onRootBlur} data-testid={testId ? `session-menu-${testId}` : "session-menu"}>
      <button
        ref={button}
        type="button"
        className="card-menu__btn"
        aria-label={SESSION_MENU.button}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => (open ? close() : setOpen(true))}
        onKeyDown={onButtonKey}
        data-testid="session-menu-button"
      >
        <Icon name="more" />
      </button>
      {open && (
        <div className="card-menu__list" role="menu" aria-label={SESSION_MENU.button} onKeyDown={onMenuKey} data-testid="session-menu-list">
          <Link href={href} role="menuitem" className="card-menu__item" onClick={() => close()} data-testid="session-menu-open">
            {SESSION_MENU.open}
          </Link>
          <button
            type="button"
            role="menuitem"
            className="card-menu__item card-menu__item--danger"
            aria-disabled={!gate.ok || undefined}
            aria-describedby={!gate.ok ? hintId : undefined}
            title={!gate.ok ? gate.reason : undefined}
            onClick={() => {
              if (!gate.ok) return;
              close();
              onDelete();
            }}
            data-testid="session-menu-delete"
          >
            {SESSION_MENU.delete}
          </button>
          {!gate.ok && <DisabledHint id={hintId}>{gate.reason}</DisabledHint>}
        </div>
      )}
    </div>
  );
}

export default SessionCardMenu;
