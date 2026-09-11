/**
 * 아이콘 한 벌(COMPONENTS §8.5) — stroke 기반 16px 인라인 SVG. 이모지를 쓰지 않는다.
 *
 * 사이드바 다섯(세션·받은 요청·에이전트·연결된 컴퓨터·설정) + 상태 점 하나. 색은 `currentColor` 라
 * 놓이는 자리의 글자색을 그대로 따른다 — 아이콘만 따로 색을 갖지 않는다(상태 점은 호출부가 -text 토큰을 준다).
 * `aria-hidden` 이 기본이다: 아이콘 옆에는 언제나 글자가 있고(§8.4 "사용자의 말로"), 아이콘은 훑기 위한 보조다.
 */
import "./icon.css";

/** 24 뷰박스 · stroke 1.75 · round — Lucide 계열 비율. path 만 여기 두고 SVG 껍데기는 한 곳에서 그린다. */
const PATHS = {
  /** 세션 — 말풍선 두 장(주고받는 일) */
  sessions: (
    <>
      <path d="M8 10h.01M12 10h.01M16 10h.01" />
      <path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1.1-4.2A8 8 0 1 1 21 12z" />
    </>
  ),
  /** 받은 요청 — 받침 있는 서랍 */
  inbox: (
    <>
      <path d="M22 12h-6l-2 3h-4l-2-3H2" />
      <path d="M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z" />
    </>
  ),
  /** 에이전트 — 사람 둘 */
  agents: (
    <>
      <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
      <circle cx="9" cy="7" r="4" />
      <path d="M22 21v-2a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8" />
    </>
  ),
  /** 연결된 컴퓨터 — 모니터 */
  computers: (
    <>
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <path d="M8 21h8M12 17v4" />
    </>
  ),
  /** 설정 — 슬라이더 셋 */
  settings: (
    <>
      <path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3" />
      <path d="M1 14h6M9 8h6M17 16h6" />
    </>
  ),
  /** 상태 점 — 채움. 온라인/오프라인처럼 색으로 말하는 자리(글자가 옆에 있어야 한다) */
  dot: <circle cx="12" cy="12" r="5" fill="currentColor" stroke="none" />,
} as const;

export type IconName = keyof typeof PATHS;
export const ICON_NAMES = Object.keys(PATHS) as IconName[];

export interface IconProps {
  name: IconName;
  /** px. 기본 16 — 한 벌의 크기다. 다른 값은 카드 제목 옆 등 특별한 자리에만. */
  size?: number;
  /** 글자와 같이 서지 않을 때만 준다(그때만 role="img"). */
  label?: string;
  className?: string;
}

export function Icon({ name, size = 16, label, className }: IconProps) {
  return (
    <svg
      className={["icon", className].filter(Boolean).join(" ")}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      focusable="false"
      data-icon={name}
    >
      {PATHS[name]}
    </svg>
  );
}

export default Icon;
