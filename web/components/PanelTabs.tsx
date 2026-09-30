"use client";
/**
 * 패널 안 탭 틀(T-RF2) — 우열 미션 칸(S22 `WorkPanel`)에 「분담표」 같은 탭을 더할 자리.
 *
 * **탭이 하나뿐이면 탭 줄을 그리지 않고 그 탭의 내용만 그대로 그린다** — 감싸는 요소도 없다(탭 하나일 때의 DOM·픽셀이 틀 도입 전과 같다).
 * 둘 이상이면 tablist + tabpanel(WAI-ARIA tabs): 선택된 탭 하나만 Tab 순서에 들고 ←→·Home·End 로 옮긴다(`lib/tabs` — 방 화면 좁은 탭과 같은 규칙).
 * 탭 이름은 부르는 쪽이 표(`lib/wording.ts`)에서 넘긴다.
 *
 * 선택은 부르는 쪽이 쥘 수 있다(`value`·`onChange` — #392 리뷰 NN3): 미션 칸은 선택 탭을 방 화면 상태로 올려 **미션을 바꿔도 유지**한다.
 * 고른 탭이 지금 목록에 없으면(카드 없는 미션으로 옮김) 첫 탭을 그리고, 목록에 다시 생기면 그 탭으로 돌아온다.
 * `aside` 는 탭 줄 오른쪽 끝의 머리(분담표 「카드 N · 판정 대기 N」) — 탭 줄이 없으면 그리지 않는다.
 */
import { useState, type ReactNode } from "react";
import "./panel-tabs.css";
import { tabKeyTarget } from "@/lib/tabs";

export interface PanelTab {
  id: string;
  label: string;
  render: () => ReactNode;
}

export interface PanelTabsProps {
  /** tablist 의 aria-label · 탭·패널 id 접두. */
  label: string;
  idPrefix: string;
  tabs: readonly PanelTab[];
  /** 처음 열린 탭(없으면 첫 탭). 제어하지 않을 때만. */
  initial?: string;
  /** 제어 — 고른 탭 id. 주면 선택을 부르는 쪽이 쥔다. */
  value?: string;
  onChange?: (id: string) => void;
  /** 탭 줄 오른쪽 끝(탭이 둘 이상일 때만). */
  aside?: ReactNode;
}

export function PanelTabs({ label, idPrefix, tabs, initial, value, onChange, aside }: PanelTabsProps) {
  const [own, setOwn] = useState<string | undefined>(initial);
  const picked = value !== undefined ? value : own;
  const setPicked = (id: string) => {
    if (value === undefined) setOwn(id);
    onChange?.(id);
  };
  if (tabs.length === 0) return null;
  if (tabs.length === 1) return <>{tabs[0].render()}</>;
  const cur = tabs.find((t) => t.id === picked) ?? tabs[0];
  const tabId = (id: string) => `${idPrefix}-tab-${id}`;
  const panelId = (id: string) => `${idPrefix}-panel-${id}`;
  return (
    <div className="ptabs" data-testid={`${idPrefix}-tabs`}>
      <div className="ptabs__list" role="tablist" aria-label={label}>
        {tabs.map((t, i) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            id={tabId(t.id)}
            className={`ptabs__tab${t.id === cur.id ? " ptabs__tab--on" : ""}`}
            aria-selected={t.id === cur.id}
            aria-controls={panelId(t.id)}
            tabIndex={t.id === cur.id ? 0 : -1}
            onClick={() => setPicked(t.id)}
            onKeyDown={(e) => {
              const to = tabKeyTarget(e.key, i, tabs.length);
              if (to < 0) return;
              e.preventDefault();
              setPicked(tabs[to].id);
              document.getElementById(tabId(tabs[to].id))?.focus();
            }}
            data-testid={`${idPrefix}-tab-${t.id}`}
          >
            {t.label}
          </button>
        ))}
        {aside != null && <span className="ptabs__aside">{aside}</span>}
      </div>
      <div role="tabpanel" id={panelId(cur.id)} aria-labelledby={tabId(cur.id)} data-testid={`${idPrefix}-panel`} data-tab={cur.id}>
        {cur.render()}
      </div>
    </div>
  );
}

export default PanelTabs;
