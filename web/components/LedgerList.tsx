"use client";
/**
 * 미션 상태 원장(PRD FR-4.6 · openapi v0.3.12 `listMemory?status=all`) — 우열 미션 칸의 「원장」 탭. **읽기 전용**이다:
 * 쓰기 폼도, 대체·철회 버튼도 없다(사람이 쓰는 원장 입력 화면은 FR-4.6 6 범위 밖 — 읽기 전용 목록만 이번에).
 *
 * 종류별로 묶어 계획 → 진행 → 사실 → 담당 → 열린 질문 → 교훈 순(묶음 안은 서버가 준 대로 오래된 것부터).
 * 행: 1줄 내용, 2줄 「[확실도](사실) · [결과 · N번 겪음 · 아직 한 번](교훈) · 작성자 · 시각」.
 * 대체된 항목·철회된 항목은 흐리게(`ledger__item--dim`). 대체된 항목에는 「새 판 보기」 — 누르면 `superseded_by` 항목으로 스크롤·강조한다
 * (화면 안 이동일 뿐 아무것도 바꾸지 않는다). 철회된 항목은 「철회됨」.
 * 문구는 `lib/wording.ts` `MEMORY_LEDGER` 표에서만 온다.
 */
import { useEffect, useRef, useState } from "react";
import "./ledger.css";
import { relativeTime } from "@/lib/time";
import { MEMORY_LEDGER as L } from "@/lib/wording";
import type { MemoryItem, MemoryKind } from "@/lib/api/types";

/** 종류별 묶음 — `MEMORY_LEDGER.order` 순, 빈 묶음은 뺀다. 묶음 안 순서는 받은 순서(서버: 오래된 것부터). */
export function ledgerGroups(items: readonly MemoryItem[]): { kind: MemoryKind; items: MemoryItem[] }[] {
  return L.order.map((kind) => ({ kind, items: items.filter((it) => it.kind === kind) })).filter((g) => g.items.length > 0);
}

/** 교훈이 턴 프롬프트에 오르는가 — 서버 `promoted` 를 믿고, 없으면 같은 문턱(support_count ≥2)으로. */
const promotedOf = (it: MemoryItem) => it.promoted ?? (it.kind !== "lesson" || it.support_count >= 2);

export function LedgerList({ items, now }: { items: readonly MemoryItem[]; now?: number }) {
  const [hl, setHl] = useState<string | null>(null);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!hl) return;
    const el = root.current?.querySelector<HTMLElement>(`[data-memory-id="${hl}"]`);
    el?.scrollIntoView?.({ block: "nearest", behavior: "smooth" });
    el?.focus({ preventScroll: true });
  }, [hl]);
  const groups = ledgerGroups(items);
  return (
    <div className="ledger" data-testid="ledger" aria-label={L.aria} ref={root}>
      {groups.map((g) => (
        <section key={g.kind} className="ledger__group" data-testid="ledger-group" data-kind={g.kind} aria-label={L.group_aria(L.kind[g.kind], g.items.length)}>
          <h3 className="aside__h ledger__h">
            {L.kind[g.kind]} <span className="aside__count">{g.items.length}</span>
          </h3>
          <ul className="ledger__list">
            {g.items.map((it) => {
              const dim = it.status !== "active";
              const promoted = promotedOf(it);
              return (
                <li
                  key={it.id}
                  className={`ledger__item${dim ? " ledger__item--dim" : ""}${hl === it.id ? " ledger__item--hl" : ""}`}
                  tabIndex={-1}
                  data-testid="ledger-item"
                  data-memory-id={it.id}
                  data-status={it.status}
                  data-kind={it.kind}
                  data-highlight={hl === it.id ? "true" : undefined}
                >
                  <p className="ledger__content" data-testid="ledger-content">{it.content}</p>
                  <p className="ledger__meta">
                    {it.kind === "fact" && it.certainty && (
                      <span className="ledger__tag" data-testid="ledger-certainty" data-certainty={it.certainty}>{L.certainty[it.certainty]}</span>
                    )}
                    {it.kind === "lesson" && (
                      <>
                        {it.outcome && <span className="ledger__tag" data-testid="ledger-outcome" data-outcome={it.outcome}>{L.outcome[it.outcome]}</span>}
                        <span className="ledger__support" data-testid="ledger-support">{L.support(it.support_count)}</span>
                        {!promoted && <span className="ledger__once" title={L.once_title} data-testid="ledger-once">{L.once}</span>}
                      </>
                    )}
                    <span className="ledger__who" data-testid="ledger-author">{it.created_by.kind === "agent" ? `@${it.created_by.name}` : it.created_by.name}</span>
                    <span className="ledger__sep" aria-hidden="true">·</span>
                    <time className="ledger__at" dateTime={it.created_at} title={it.created_at}>{relativeTime(it.created_at, now)}</time>
                    {it.status === "superseded" && (
                      <>
                        <span className="ledger__sep" aria-hidden="true">·</span>
                        <span className="ledger__state">{L.superseded}</span>
                        {it.superseded_by && (
                          <button type="button" className="aside__link ledger__jump" title={L.new_version_title} onClick={() => setHl(it.superseded_by!)} data-testid="ledger-new-version" data-target={it.superseded_by}>
                            {L.new_version}
                          </button>
                        )}
                      </>
                    )}
                    {it.status === "retired" && (
                      <>
                        <span className="ledger__sep" aria-hidden="true">·</span>
                        <span className="ledger__state" data-testid="ledger-retired">{L.retired}</span>
                        {it.retire_reason && (
                          <span className="ledger__reason" data-testid="ledger-retire-reason">{L.retire_reason(it.retire_reason)}</span>
                        )}
                      </>
                    )}
                  </p>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}

export default LedgerList;
