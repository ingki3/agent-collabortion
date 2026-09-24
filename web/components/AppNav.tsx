"use client";
import Link from "next/link";
import { Icon, type IconName } from "./Icon";
import { slotText } from "./Slot";
import { ROOM_LIST } from "@/lib/wording";
import "./app-nav.css";

export interface AppNavProps {
  workspaceName: string;
  /** 현재 경로(usePathname). 접두 일치로 활성 항목을 정한다. */
  current: string;
  /** 받은 요청 뱃지 — action_required 개수만(SCREEN §4.6). null 이면 자리만 둔다(P1). */
  inboxCount: number | null;
  /**
   * 「방」 옆 안 읽음 합계(v0.19 M4, SCREEN §4.3) — 내가 참여한 방의 `unread_count` 합. 0 이거나 모르면(null) 그리지 않는다 —
   * 받은 요청 뱃지(할 일)와 달리 안 읽음은 할 일이 아니라 0 자리를 둘 이유가 없다.
   */
  roomsUnread?: number | null;
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
  // v0.19 (T-R2-W1): 「방」이 S5 다. 옛 세션 주소는 v0.3.0(R4)에서 307 넘김까지 지웠다.
  { href: "/rooms", key: "rooms", label: "방", icon: "sessions" },
  { href: "/inbox", key: "inbox", label: "받은 요청", icon: "inbox" },
  { href: "/agents", key: "agents", label: "에이전트", icon: "agents" },
  { href: "/runtimes", key: "runtimes", label: "연결된 컴퓨터", icon: "computers" },
  { href: "/settings", key: "settings", label: "설정", icon: "settings" },
];

export function AppNav({
  workspaceName,
  current,
  inboxCount,
  roomsUnread,
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
            {item.href === "/rooms" && !!roomsUnread && roomsUnread > 0 && (
              <span className="app-nav__badge app-nav__badge--unread" role="img" aria-label={slotText(ROOM_LIST.unread_label, roomsUnread)} data-testid="rooms-unread-badge">
                {roomsUnread > 99 ? "99+" : roomsUnread}
              </span>
            )}
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
