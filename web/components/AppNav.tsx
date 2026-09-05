"use client";
import Link from "next/link";
import "./app-nav.css";

export interface AppNavProps {
  workspaceName: string;
  /** 현재 경로(usePathname). 접두 일치로 활성 항목을 정한다. */
  current: string;
  /** Inbox 뱃지 — action_required 개수만(SCREEN §4.6). null 이면 자리만 둔다(P1). */
  inboxCount: number | null;
  /** owner·admin 만 Settings 를 본다(SCREEN §3.1). 숨기는 것이 명세다 — U13 변형. */
  showSettings: boolean;
  userName?: string;
  onLogout?: () => void;
}

export const NAV_ITEMS = [
  { href: "/sessions", label: "Sessions" },
  { href: "/inbox", label: "Inbox" },
  { href: "/agents", label: "Agents" },
  { href: "/runtimes", label: "Runtimes" },
  { href: "/settings", label: "Settings" },
] as const;

export function AppNav({ workspaceName, current, inboxCount, showSettings, userName, onLogout }: AppNavProps) {
  const items = NAV_ITEMS.filter((i) => i.href !== "/settings" || showSettings);
  return (
    <nav className="app-nav" aria-label="주 내비게이션" data-testid="app-nav">
      <div className="app-nav__brand">COLAB</div>
      <div className="app-nav__ws" title={workspaceName}>
        {workspaceName}
      </div>
      {items.map((item) => {
        const active = current === item.href || current.startsWith(item.href + "/");
        return (
          <Link
            key={item.href}
            href={item.href}
            className="app-nav__item"
            aria-current={active ? "page" : undefined}
            data-testid={`nav-${item.label.toLowerCase()}`}
          >
            <span>{item.label}</span>
            {item.href === "/inbox" && (
              <span
                className={`app-nav__badge${!inboxCount ? " app-nav__badge--zero" : ""}`}
                aria-label={`조치 필요 ${inboxCount ?? 0}건`}
                data-testid="inbox-badge"
              >
                {inboxCount ?? 0}
              </span>
            )}
          </Link>
        );
      })}
      <div className="app-nav__spacer" />
      {userName && (
        <div className="app-nav__user">
          <b title={userName}>{userName}</b>
          {onLogout && (
            <button type="button" className="btn btn--sm btn--ghost" onClick={onLogout}>
              로그아웃
            </button>
          )}
        </div>
      )}
    </nav>
  );
}

export default AppNav;
