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
 * 키보드·바깥 클릭 동작은 `useCardMenu`(방 카드 메뉴와 같은 훅).
 */
import { useId } from "react";
import Link from "next/link";
import { Icon } from "./Icon";
import { DisabledHint } from "./PageHead";
import { useCardMenu } from "./useCardMenu";
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
  const { open, root, button, close, toggle, onRootBlur, onButtonKey, onMenuKey } = useCardMenu();
  const hintId = `${useId()}-hint`;

  return (
    <div className="card-menu" ref={root} onBlur={onRootBlur} data-testid={testId ? `session-menu-${testId}` : "session-menu"}>
      <button
        ref={button}
        type="button"
        className="card-menu__btn"
        aria-label={SESSION_MENU.button}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={toggle}
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
