"use client";
import Link from "next/link";
import { Icon, type IconName } from "./Icon";
import "./app-nav.css";

export interface AppNavProps {
  workspaceName: string;
  /** 현재 경로(usePathname). 접두 일치로 활성 항목을 정한다. */
  current: string;
  /** 받은 요청 뱃지 — action_required 개수만(SCREEN §4.6). null 이면 자리만 둔다(P1). */
  inboxCount: number | null;
  /** owner·admin 만 설정을 본다(SCREEN §3.1). 숨기는 것이 명세다 — U13 변형. */
  showSettings: boolean;
  userName?: string;
  onLogout?: () => void;
  /**
   * 워크스페이스가 둘 이상일 때만 이름 자리가 선택 상자가 된다. 예전에는 상단 바에 있었는데,
   * 상단 바는 그것 말고 넣을 것이 없어 47px 을 비워 두고 있었다(§8.5 "빈 상단 바를 없앤다").
   */
  workspaces?: { id: string; name: string }[];
  currentWorkspaceId?: string;
  onSelectWorkspace?: (id: string) => void;
}

/**
 * 메뉴는 **한국어 한 벌**이다(COMPONENTS §8.4 "한 화면 안에서 언어를 섞지 않는다") — 옆 버튼이
 * "새 에이전트"인데 메뉴만 영어면 두 언어가 한 화면에 선다. `key` 는 화면 식별자라 문구가 바뀌어도
 * `data-testid` 가 따라 움직이지 않는다(예전에는 라벨을 소문자로 바꿔 testid 를 만들었다).
 */
export const NAV_ITEMS: readonly { href: string; key: string; label: string; icon: IconName }[] = [
  { href: "/sessions", key: "sessions", label: "세션", icon: "sessions" },
  { href: "/inbox", key: "inbox", label: "받은 요청", icon: "inbox" },
  { href: "/agents", key: "agents", label: "에이전트", icon: "agents" },
  { href: "/runtimes", key: "runtimes", label: "연결된 컴퓨터", icon: "computers" },
  { href: "/settings", key: "settings", label: "설정", icon: "settings" },
];

export function AppNav({
  workspaceName,
  current,
  inboxCount,
  showSettings,
  userName,
  onLogout,
  workspaces,
  currentWorkspaceId,
  onSelectWorkspace,
}: AppNavProps) {
  const items = NAV_ITEMS.filter((i) => i.href !== "/settings" || showSettings);
  const canSwitch = !!workspaces && workspaces.length > 1 && !!onSelectWorkspace;
  return (
    <nav className="app-nav" aria-label="주 내비게이션" data-testid="app-nav">
      <div className="app-nav__brand">COLAB</div>
      {canSwitch ? (
        <select
          className="select app-nav__ws-select"
          value={currentWorkspaceId ?? ""}
          onChange={(e) => onSelectWorkspace?.(e.target.value)}
          aria-label="워크스페이스 선택"
          data-testid="workspace-select"
        >
          {workspaces!.map((w) => (
            <option key={w.id} value={w.id}>
              {w.name}
            </option>
          ))}
        </select>
      ) : (
        <div className="app-nav__ws" title={workspaceName}>
          {workspaceName}
        </div>
      )}
      {items.map((item) => {
        const active = current === item.href || current.startsWith(item.href + "/");
        return (
          <Link
            key={item.href}
            href={item.href}
            className="app-nav__item"
            aria-current={active ? "page" : undefined}
            data-testid={`nav-${item.key}`}
          >
            <span className="app-nav__label">
              <Icon name={item.icon} />
              <span>{item.label}</span>
            </span>
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
