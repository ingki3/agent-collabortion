"use client";
/**
 * 작업 카드 말풍선 두 종류(PRD FR-3.8 · SCREEN §4.6 「작업 카드」 · COMPONENTS §9.13 · Pencil `S7-K 작업 카드`).
 *  - `TaskCardBubble` — 위임 카드(`card_role: delegation`): 머리 「C-n · n판 · [상태]」, 목표 · 완료 기준(확인 방법 칩) · 하지 않을 것(경계 막대) ·
 *    참고(칩 — 누르면 그 자리로, 지워진 것은 흐림) · 결과물 · 예산. 2판부터는 맨 위에 「수정 요청 · 〈사유〉」.
 *  - `ResultCardBubble` — 결과 카드(`card_role: result`): 머리 「C-n 결과 · 기준 k/N 충족 · [판정] (· 자동)」, 「↩ C-n 「목표…」」, 요약,
 *    기준별 판정 글리프(✓ ◐ ✗) + 근거 링크 또는 사유 · 「부분 · 근거 없음」(서버가 낮춘 줄), 확인함 · 가정함(`$s-warn-text`) · 벗어난 점 ·
 *    남은 문제 · 비용 · 시간, 맨 아래 판정 줄.
 * 둘 다 에이전트 말풍선(`MessageCard` 대화 배치) 안에 본문 대신 카드를 그리고, 작업 내용·작업 과정은 지금 규칙 그대로 카드 아래다.
 *
 * 카드 칸은 메시지의 `card_id`·`card_version` 으로 **방 화면의 카드 캐시**(`ctx.cards`, lib/cards)에서 꺼낸다 — 같은 카드의 말풍선 여럿이 한 캐시를 쓴다.
 * 캐시에 그 판이 없으면 `ctx.needCard` 로 한 번 부탁하고(방 화면이 `getCard`), 그동안은 서버가 쓴 요약(`content`)을 흐리게 둔다.
 *
 * 사람의 되돌리기(「⋯」 메뉴): **`TaskCard.actions` 만 본다** — 미션 Director·deputy 에게 서버가 내려준 동작만 항목이 된다(권한 없는 사람에겐 항목 없음).
 * 결과 제출이면 「수락」·「수정 요청…」, 수락이면 「수락 취소…」(= revise). 사유는 말풍선 안 한 칸.
 */
import { useEffect, useId, useState, type ReactNode } from "react";
import "./task-card.css";
import { MessageCard, authorName, type ConversationSlot } from "./MessageCard";
import { Badge } from "./Badge";
import { Slot } from "./Slot";
import { clockTime } from "@/lib/time";
import { cardVersion, goalExcerpt, judgeOf, metCount, type CardVersionView } from "@/lib/cards";
import { TASK_CARD as L } from "@/lib/wording";
import type { CardAction, CardEvidence, CardRef, Message, TaskCard } from "@/lib/api/types";
import type { TimelineCtx } from "./TimelineItemView";

/** 카드 말풍선이 방 화면에서 받는 것 — `TimelineCtx` 의 카드 칸(부분). */
export type CardCtx = Pick<TimelineCtx, "cards" | "needCard" | "onCardAction" | "onJump" | "onJumpRef">;

export interface CardBubbleProps {
  message: Message;
  ctx: TimelineCtx;
  /** 「이걸 미션으로」 항목(방 화면의 메시지 메뉴 줄) — 카드 동작 뒤에 그대로 둔다. */
  toWork: ReactNode;
  workLabel: ReactNode;
}

/** 캐시에서 이 말풍선의 판을 꺼낸다 — 없으면 방 화면에 한 번 부탁한다. */
function useCardVersion(m: Message, ctx: CardCtx): { card: TaskCard | null; view: CardVersionView | null } {
  const card = m.card_id ? ctx.cards[m.card_id] ?? null : null;
  const view = card ? cardVersion(card, m.card_version) : null;
  const want = m.card_role === "result" ? "result" : "card";
  // 결과 말풍선이 먼저 왔는데(card.updated 보다) 캐시의 그 판에 결과가 없으면 — 그것도 없는 것이다.
  const missing = !!m.card_id && (!view || (want === "result" && !view.result));
  const { needCard } = ctx;
  useEffect(() => {
    if (missing && m.card_id) needCard(m.card_id, m.card_version ?? null, want);
  }, [missing, m.card_id, m.card_version, want, needCard]);
  return { card, view };
}

function Row({ label, children, testId, className }: { label: string; children: ReactNode; testId: string; className?: string }) {
  return (
    <div className={`tcard__row${className ? ` ${className}` : ""}`} data-testid={testId}>
      <span className="tcard__label">{label}</span>
      <div className="tcard__val">{children}</div>
    </div>
  );
}

function RefChip({ r, onJump }: { r: CardRef; onJump: (r: CardRef) => void }) {
  if (r.missing) {
    return (
      <span className="tcard__ref" data-missing="true" data-ref-kind={r.kind} data-testid="card-ref" aria-disabled="true">
        <span aria-hidden="true">{L.ref_glyph[r.kind]}</span> {L.ref_missing}
      </span>
    );
  }
  return (
    <button type="button" className="tcard__ref" data-ref-kind={r.kind} data-ref-id={r.id} data-testid="card-ref" title={r.label} onClick={() => onJump(r)}>
      <span aria-hidden="true">{L.ref_glyph[r.kind]}</span> <span className="tcard__ref-name">{r.label}</span>
    </button>
  );
}

function Evidence({ e, onJump }: { e: CardEvidence; onJump: (id: string) => void }) {
  const text = e.label ?? e.ref;
  const glyph = <span aria-hidden="true">{L.evidence_glyph[e.kind]} </span>;
  if (e.kind === "artifact") {
    return (
      <a className="tcard__ev" href={`/api/v1/artifacts/${e.ref}/content`} target="_blank" rel="noopener noreferrer" data-testid="card-evidence" data-ev-kind={e.kind}>
        {glyph}{text}
      </a>
    );
  }
  if (e.kind === "message") {
    return (
      <button type="button" className="tcard__ev" onClick={() => onJump(e.ref)} data-testid="card-evidence" data-ev-kind={e.kind}>
        {glyph}{text}
      </button>
    );
  }
  return <code className="tcard__ev tcard__ev--commit" data-testid="card-evidence" data-ev-kind={e.kind}>{glyph}{text.slice(0, 12)}</code>;
}

/** 「⋯」 메뉴의 카드 항목 + 사유 한 칸. 항목은 `actions` 와 상태가 둘 다 맞을 때만(권한 없는 사람에겐 없음). */
export function cardMenuItems(card: TaskCard | null): { action: CardAction; kind: "accept" | "revise" | "unaccept" }[] {
  if (!card) return [];
  const acts = new Set(card.actions ?? []);
  const out: { action: CardAction; kind: "accept" | "revise" | "unaccept" }[] = [];
  if (card.status === "result_submitted" && acts.has("accept")) out.push({ action: "accept", kind: "accept" });
  if (card.status === "result_submitted" && acts.has("revise")) out.push({ action: "revise", kind: "revise" });
  if (card.status === "accepted" && acts.has("revise")) out.push({ action: "revise", kind: "unaccept" });
  return out;
}

function CardMenu({ card, ctx, toWork, onAsk }: { card: TaskCard | null; ctx: CardCtx; toWork: ReactNode; onAsk: (k: "revise" | "unaccept") => void }) {
  const [open, setOpen] = useState(false);
  const items = cardMenuItems(card);
  return (
    <span className="msg-menu" onBlur={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setOpen(false); }}>
      <button type="button" className="msg__link" aria-haspopup="menu" aria-expanded={open} aria-label={L.menu} onClick={() => setOpen((v) => !v)} data-testid="message-menu">
        …
      </button>
      {open && (
        <span className="card-menu__list msg-menu__list" role="menu" data-testid="message-menu-list">
          {items.map((it) => (
            <button
              key={it.kind}
              type="button"
              role="menuitem"
              className="card-menu__item"
              data-testid={`card-menu-${it.kind}`}
              onClick={() => {
                setOpen(false);
                if (it.kind === "accept" && card) void ctx.onCardAction(card, "accept");
                else onAsk(it.kind as "revise" | "unaccept");
              }}
            >
              {it.kind === "accept" ? L.accept : it.kind === "revise" ? L.revise : L.unaccept}
            </button>
          ))}
          {toWork}
        </span>
      )}
    </span>
  );
}

function ReasonForm({ kind, card, ctx, onClose }: { kind: "revise" | "unaccept"; card: TaskCard; ctx: CardCtx; onClose: () => void }) {
  const [text, setText] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const id = useId();
  return (
    <form
      className="tcard__reason"
      data-testid="card-reason"
      data-kind={kind}
      onSubmit={async (e) => {
        e.preventDefault();
        if (!text.trim()) return setErr(L.reason_required);
        setBusy(true);
        try {
          await ctx.onCardAction(card, "revise", text.trim());
          onClose();
        } catch (x) {
          setErr(x instanceof Error ? x.message : String(x));
        } finally {
          setBusy(false);
        }
      }}
    >
      <label htmlFor={id} className="tcard__label">{kind === "unaccept" ? L.reason_unaccept : L.reason_revise}</label>
      <textarea id={id} className="input" rows={2} value={text} onChange={(e) => { setText(e.target.value); setErr(null); }} data-testid="card-reason-input" autoFocus />
      {err && <p className="problem small" role="alert" data-testid="card-reason-error">{err}</p>}
      <div className="row" style={{ gap: 6 }}>
        <button type="submit" className="btn btn--sm btn--primary" disabled={busy} data-testid="card-reason-send">{L.send}</button>
        <button type="button" className="btn btn--sm" onClick={onClose} data-testid="card-reason-close">{L.close}</button>
      </div>
    </form>
  );
}

/** 같은 뼈대 — MessageCard(대화 배치)에 카드 머리·본문·메뉴를 끼운다. 작업 내용·작업 과정(`layers.below`)은 카드 아래. */
function Bubble({ message: m, ctx, toWork, workLabel, head, body, aria, testId, noReportOf }: CardBubbleProps & { head: ReactNode; body: ReactNode; aria: string | undefined; testId: string; noReportOf?: boolean }) {
  const { card } = useCardVersion(m, ctx);
  const [ask, setAsk] = useState<"revise" | "unaccept" | null>(null);
  const current = !!card && (m.card_version ?? card.version) === card.version;
  const conversation = (mm: Message, o: { parent?: Message }): ConversationSlot => {
    const c = ctx.conversation(mm, o);
    // 결과 카드는 카드 안에 「↩ C-n 「목표…」」 를 그린다 — 대화 배치의 「↩ … 에 대한 보고」를 겹쳐 그리지 않는다.
    return noReportOf && mm.id === m.id ? { ...c, speech: { ...c.speech, reportOf: undefined } } : c;
  };
  const layers: TimelineCtx["layers"] = (mm, o) => {
    if (mm.id !== m.id) return ctx.layers(mm, o);
    const base = ctx.layers(mm, o);
    return {
      body: "",
      below: (
        <>
          <div className="tcard" data-testid={testId} data-card-id={m.card_id ?? undefined} data-card-label={card?.label} data-card-version={m.card_version ?? undefined}>
            {body}
            {ask && card && current && <ReasonForm kind={ask} card={card} ctx={ctx} onClose={() => setAsk(null)} />}
          </div>
          {base?.below}
        </>
      ),
    };
  };
  return (
    <MessageCard
      message={m}
      replies={ctx.replies[m.id]}
      onLoadReplies={ctx.onLoadReplies}
      onReply={ctx.onReply}
      layers={layers}
      conversation={conversation}
      now={ctx.now}
      workLabel={workLabel}
      headExtra={head}
      ariaLabel={aria}
      menu={<CardMenu card={current ? card : null} ctx={ctx} toWork={toWork} onAsk={setAsk} />}
    />
  );
}

/** 불러오는 중 — 서버가 쓴 요약을 흐리게(칸을 짐작해 그리지 않는다). */
function Loading({ m }: { m: Message }) {
  return (
    <p className="tcard__loading" data-testid="card-loading" aria-busy="true">
      {m.content || L.loading}
    </p>
  );
}

export function TaskCardBubble(props: CardBubbleProps) {
  const { message: m, ctx } = props;
  const { card, view } = useCardVersion(m, ctx);
  const head = card && view ? (
    <span className="tcard__head" data-testid="card-head">
      <span className="tcard__no">{card.label}</span>
      <span className="tcard__ver"><Slot text={L.version} n={view.version} /></span>
      {view.status ? <Badge kind="card" value={view.status} size="sm" /> : <Badge kind="card_judge" value="revise_requested" size="sm" />}
    </span>
  ) : null;
  const aria = card && view ? L.aria_delegation(authorName(m), card.label, `@${card.assignee.name}`, view.status ? L.status[view.status] : L.judge.revise_requested) : undefined;
  const body = !card || !view ? <Loading m={m} /> : (
    <>
      {view.revise_reason && (
        <p className="tcard__revise" data-testid="card-revise-reason">{L.revise_head}{view.revise_reason}</p>
      )}
      <Row label={L.field.goal} testId="card-goal"><span className="tcard__text">{view.goal}</span></Row>
      <Row label={L.field.criteria} testId="card-criteria">
        <ol className="tcard__crit">
          {view.criteria.map((c) => (
            <li key={c.n} className="tcard__crit-row" data-testid="card-criterion" data-n={c.n}>
              <span className="tcard__n" aria-hidden="true">{c.n}</span>
              <span className="tcard__text">{c.text}</span>
              <span className="tcard__method" data-testid="card-method" data-method={c.method}>{L.method[c.method]}</span>
            </li>
          ))}
        </ol>
      </Row>
      <Row label={L.field.boundaries} testId="card-boundaries"><span className="tcard__bound">{view.boundaries}</span></Row>
      {view.refs.length > 0 && (
        <Row label={L.field.refs} testId="card-refs">
          <span className="tcard__refs">{view.refs.map((r) => <RefChip key={`${r.kind}:${r.id}`} r={r} onJump={ctx.onJumpRef} />)}</span>
        </Row>
      )}
      {(view.output_format || view.budget_usd != null) && (
        <Row label={L.field.output} testId="card-output">
          <span className="tcard__text">
            {view.output_format}
            {view.output_format && view.budget_usd != null && " · "}
            {view.budget_usd != null && <Slot text={L.budget} n={view.budget_usd} />}
          </span>
        </Row>
      )}
    </>
  );
  return <Bubble {...props} head={head} body={body} aria={aria} testId="task-card" />;
}

const GLYPH = { met: "✓", partial: "◐", unmet: "✗" } as const;

export function ResultCardBubble(props: CardBubbleProps) {
  const { message: m, ctx } = props;
  const { card, view } = useCardVersion(m, ctx);
  const r = view?.result ?? null;
  // 같은 판에 두 번 내면 뒤의 것이 이긴다(계약) — 이 말풍선이 그 판의 지금 결과가 아니면 판정 칩은 그리지 않는다(옛 결과).
  const live = !!r && (!r.message_id || r.message_id === m.id);
  const k = r ? metCount(r) : 0;
  const n = view?.criteria.length ?? 0;
  const judge = view ? judgeOf(view) : "pending";
  const head = card && view && r ? (
    <span className="tcard__head" data-testid="card-head">
      <span className="tcard__no">{card.label} {L.result_of}</span>
      <span className="tcard__ver">· {L.met_count(k, n)}</span>
      {live && <Badge kind="card_judge" value={judge} size="sm" />}
      {r.auto && <span className="tcard__auto" data-testid="card-auto">{L.auto}</span>}
    </span>
  ) : null;
  const aria = card && view && r ? L.aria_result(authorName(m), card.label, k, n) : undefined;
  const byN = new Map((view?.criteria ?? []).map((c) => [c.n, c]));
  const j = live ? view?.judgement ?? null : null;
  const body = !card || !view || !r ? <Loading m={m} /> : (
    <>
      <button type="button" className="tcard__back" onClick={() => { const id = delegationMessageOf(m, card, view); if (id) ctx.onJump(id); }} data-testid="card-back">
        ↩ {card.label} 「{goalExcerpt(view.goal)}」
      </button>
      <p className={`tcard__summary${r.auto ? " tcard__summary--auto" : ""}`} data-testid="card-summary">{r.auto ? L.auto_summary : r.summary}</p>
      <ol className="tcard__verdicts" data-testid="card-verdicts">
        {r.verdicts.map((v) => (
          <li key={v.criterion} className="tcard__verdict" data-testid="card-verdict" data-verdict={v.verdict} data-downgraded={v.downgraded ? "true" : undefined}>
            <span className="tcard__glyph" data-verdict={v.verdict} aria-label={L.verdict[v.verdict]}>{GLYPH[v.verdict]}</span>
            <span className="tcard__n" aria-hidden="true">{v.criterion}</span>
            <span className="tcard__text">{byN.get(v.criterion)?.text ?? ""}</span>
            <span className="tcard__why">
              {v.downgraded ? (
                <span className="tcard__down" data-testid="card-downgraded">{L.downgraded}</span>
              ) : v.verdict === "met" && v.evidence.length > 0 ? (
                v.evidence.map((e, i) => <Evidence key={i} e={e} onJump={ctx.onJump} />)
              ) : v.note ? (
                <span className="tcard__note">{v.verdict === "met" ? v.note : `${L.verdict[v.verdict]} — ${v.note}`}</span>
              ) : v.verdict !== "met" ? (
                <span className="tcard__note">{L.verdict[v.verdict]}</span>
              ) : null}
            </span>
          </li>
        ))}
      </ol>
      {!r.auto && (
        <>
          <Row label={L.field.confirmed} testId="card-confirmed"><ul className="tcard__list">{r.confirmed.map((x, i) => <li key={i}>{x}</li>)}</ul></Row>
          {r.assumed.length > 0 && (
            <Row label={L.field.assumed} testId="card-assumed" className="tcard__row--warn"><ul className="tcard__list">{r.assumed.map((x, i) => <li key={i}>{x}</li>)}</ul></Row>
          )}
          {r.deviations && <Row label={L.field.deviations} testId="card-deviations"><span className="tcard__text">{r.deviations}</span></Row>}
          {r.open_issues && <Row label={L.field.open_issues} testId="card-open-issues"><span className="tcard__text">{r.open_issues}</span></Row>}
        </>
      )}
      {(r.cost_usd != null || r.duration_s != null) && (
        <p className="tcard__meta" data-testid="card-cost">
          {r.cost_usd != null && `$${r.cost_usd.toFixed(2)}`}
          {r.cost_usd != null && r.duration_s != null && " · "}
          {r.duration_s != null && L.duration(r.duration_s)}
        </p>
      )}
      {j && (
        <p className="tcard__judged" data-testid="card-judgement" data-action={j.action}>
          <span aria-hidden="true" className="tcard__glyph" data-verdict={j.action === "accepted" ? "met" : "partial"}>{j.action === "accepted" ? "✓" : "↺"}</span>
          {j.action === "accepted" ? L.judged_accept : L.judged_revise}
          {j.by.kind === "agent" ? `@${j.by.name}` : j.by.name}
          {j.action === "accepted" ? ` ${clockTime(j.at)}` : j.reason ? ` — ${j.reason}` : ""}
          {j.action === "revise_requested" && card.version > view.version && card.delegate_message_id && (
            <>
              {" · "}
              <button type="button" className="msg__link" onClick={() => ctx.onJump(card.delegate_message_id!)} data-testid="card-new-version">{L.new_version}</button>
            </>
          )}
        </p>
      )}
    </>
  );
  return <Bubble {...props} head={head} body={body} aria={aria} testId="result-card" noReportOf />;
}

/** 이 판의 위임 카드 말풍선 — 서버가 결과 말풍선에 채운 ↩(`responds_to_message_id` = 그 판의 위임 카드 말풍선), 없으면 현재 판의 것. */
function delegationMessageOf(m: Message, card: TaskCard, view: CardVersionView): string | null {
  return m.responds_to_message_id ?? (view.current ? card.delegate_message_id ?? null : null);
}
