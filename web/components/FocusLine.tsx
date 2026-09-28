"use client";
/**
 * 「지금」 줄(PRD FR-3.1.5 · SCREEN v0.19.13 · COMPONENTS §9.10 「지금」 줄 · Pencil S7-C `CGMWw`·`LgIO6`, S7-CD `j8dkp`·`KBkri`).
 *
 *   지금 〈lane focus.text〉 · n분 전
 *
 *  - 「지금 」 `$ink-2` + 문장 `$ink` 500 + 「· n분 전」 `$ink-2` `--fs-meta`, 두 줄까지 말줄임.
 *  - 서버의 **대신 문장**(`focus.source = derived`)이면 문장도 `$ink-2`·보통 굵기(흐리게) — 에이전트가 말한 것과 화면이 만든 것이 구분돼야 한다.
 *  - 문장이 바뀌면 150ms 페이드(`key` 로 다시 그려 CSS 애니메이션; reduced-motion 이면 즉시).
 *  - `live` 면 `aria-live="polite"` — 「작업 중」 말풍선에서는 사람이 가장 알고 싶은 줄이다. 서브 미션 카드에서는 끈다(같은 문장을 두 번 읽지 않게).
 * 문장이 없으면(`focus` null — 대기·끝남) 아무것도 그리지 않는다. 부른 쪽이 옛 문구를 그 자리에 둔다.
 */
import "./focus-line.css";
import { relativeTime } from "@/lib/time";
import { FOCUS } from "@/lib/wording";
import type { Lane } from "@/lib/api/types";

export type LaneFocus = NonNullable<Lane["focus"]>;

export function FocusLine({ focus, now, live = false, variant = "bubble", testId = "focus-line" }: {
  focus: LaneFocus | null | undefined; now?: number; live?: boolean; variant?: "bubble" | "lane"; testId?: string;
}) {
  if (!focus || !focus.text) return null;
  const derived = focus.source === "derived";
  return (
    <p
      className={`focus focus--${variant}`}
      data-testid={testId}
      data-source={focus.source}
      aria-live={live ? "polite" : undefined}
      title={derived ? FOCUS.derived_title : undefined}
    >
      <span key={focus.text} className="focus__fade">
        <span className="focus__label">{FOCUS.now} </span>
        <span className={`focus__text${derived ? " focus__text--derived" : ""}`} data-testid={`${testId}-text`}>{focus.text}</span>
        <span className="focus__age" data-testid={`${testId}-age`}>
          <span aria-hidden="true">{" · "}</span>
          {relativeTime(focus.at, now)}
        </span>
      </span>
    </p>
  );
}

export default FocusLine;
