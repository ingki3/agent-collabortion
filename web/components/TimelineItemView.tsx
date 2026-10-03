"use client";
/**
 * 방 타임라인 항목 하나(S7 가운데 칸 — SCREEN §4.6) — `app/(app)/rooms/[id]/page.tsx` 의 `timelineItems(messages).map` 몸통을 떼어 낸 것(T-RF2).
 *
 * **항목 종류를 하나 더할 때 고칠 곳은 이 파일 하나다**:
 *  ① `TimelineEntry` 유니온에 모양을 더하고 ② `timelineEntry()` 가 그 모양을 고르게 하고 ③ `RENDER` 표에 렌더러를 더한다.
 *  `RENDER` 는 유니온의 모든 `kind` 를 요구한다(빠지면 타입 오류) — 고르기만 하고 그리기를 잊는 일이 없다.
 * 메시지 종류(`Message.kind`)별 꼬리표(요약 라벨 · 시스템 줄의 「미션 보기」)와 「…」 메뉴를 감추는 종류도 아래 표 두 개가 정한다.
 * 작업 카드(v0.19.15, PRD FR-3.8)는 `card_role` 로 고른다 — `delegation` 은 위임 카드, `result` 는 결과 카드(`components/TaskCardBubble`).
 *
 * 화면이 가진 상태·동작(펼침·답글·고르기·확인 요청 응답 …)은 `TimelineCtx` 한 묶음으로 받는다 — 이 컴포넌트는 상태가 없다
 * (「…」 메뉴의 열림만 `MessageMenu` 가 쥔다).
 */
import { memo, useState, type ReactNode } from "react";
import { MessageCard, type ConversationSlot, type MessageLayerSlots } from "./MessageCard";
import { PartBubble } from "./PartBubble";
import { HitlCard } from "./HitlCard";
import { ResultCardBubble, TaskCardBubble } from "./TaskCardBubble";
import { DisabledHint } from "./PageHead";
import { boundaryOf, type PartGroup, type TimelineItem } from "@/lib/parts";
import { pairHitl } from "@/lib/hitl-pairing";
import { isLayered } from "@/lib/message-layers";
import { ROOM_CENTER, ROOM_HEAD } from "@/lib/wording";
import type { CardRef, HitlRequest, HitlResponse, Message, TaskCard } from "@/lib/api/types";
import type { CardCache } from "@/lib/cards";

/** 방 화면이 항목 렌더러에 넘기는 것 — 데이터와 동작. */
export interface TimelineCtx {
  me: string | null;
  now: number;
  replies: Record<string, Message[]>;
  /** 「작업 중」 말풍선 → 메시지 교체 첫 프레임의 높이(메시지 id → px). */
  held: Record<string, number>;
  hitls: HitlRequest[];
  busy: boolean;
  /** 방 단위 확인 요청(예산)의 상한·소진액 — 할 일 단위 요청은 요청 자신의 값을 쓴다. */
  roomBudget: { current: number | null; spent: number };
  /** 보관된 방 — 「이걸 미션으로」 비활성 사유. */
  archived: boolean;
  /** 시스템 줄의 「미션 보기」 — 이미 그 미션을 보고 있으면 감춘다. */
  showWorkLink: boolean;
  /** 「여기까지 정리」 직접 고르기 단추(고르는 중이 아니면 falsy). */
  pickButton: (m: Message) => ReactNode;
  conversation: (m: Message, o: { parent?: Message }) => ConversationSlot;
  layers: (m: Message, o: { asAnswer: boolean; noProcess?: boolean }) => MessageLayerSlots | undefined;
  /** 부분 메시지 말풍선 맨 아래 작업 과정 하나 — 경계 부분(`boundaryOf`)의 것. */
  groupProcess: (boundary: Message) => ReactNode | undefined;
  /** 세 층이 아닌 에이전트 메시지의 활동 피드. */
  taskActivity: (m: Message) => ReactNode;
  onLoadReplies: (rootId: string) => Promise<void> | void;
  onReply: (root: Message) => void;
  workLabel: (workId: string | null | undefined, testId: string) => ReactNode | undefined;
  workTitle: (workId: string | null | undefined) => string;
  onToWork: (m: Message) => void;
  onRespondHitl: (hitlRequestId: string, body: HitlResponse) => Promise<void>;
  onOpenWork: (workId: string) => void;
  userName: (userId: string) => string | undefined;
  // ── 작업 카드(v0.19.15, PRD FR-3.8) ──
  /** 방 화면의 카드 캐시(카드 id → TaskCard) — 같은 카드의 말풍선 여럿이 이 한 캐시를 읽는다(lib/cards). */
  cards: CardCache;
  /** 캐시에 그 카드(그 판)가 없거나, 결과 말풍선인데 그 판의 결과가 아직 없다 — 방 화면이 `getCard` 로 한 번 읽는다(판·종류마다 한 번). */
  needCard: (cardId: string, version: number | null, want: "card" | "result" | "actions") => void;
  /** 사람의 되돌리기 — 수락(`acceptCard`) · 수정 요청/수락 취소(`reviseCard`, 사유). 실패는 throw(말풍선의 사유 칸이 보여 준다). */
  /** 수락은 코멘트(v0.3.11 필수), 수정 요청은 사유 — 셋째 인자는 그 글. */
  onCardAction: (card: TaskCard, action: "accept" | "revise", text?: string) => Promise<void>;
  /** 메시지로 스크롤·강조(화면 밖이면 앵커). */
  onJump: (messageId: string) => void;
  /** 참고 자료 칩 — 아티팩트 카드·결정 행·메시지로 스크롤·강조. */
  onJumpRef: (ref: CardRef) => void;
}

/** 타임라인 항목의 그리기 종류. 새 종류는 여기에 한 줄 더한다. */
export type TimelineEntry =
  | { kind: "group"; group: PartGroup }
  | { kind: "hitl"; message: Message }
  | { kind: "task_card"; message: Message }
  | { kind: "result_card"; message: Message }
  | { kind: "message"; message: Message };

/** `timelineItems` 의 항목 → 그리기 종류. */
export function timelineEntry(item: TimelineItem): TimelineEntry {
  if (item.kind === "group") return { kind: "group", group: item };
  // T-APPROVAL: 확인 요청은 대화 배치(T-CONVO)에서도 가운데 전폭 카드다.
  if (item.message.kind === "hitl") return { kind: "hitl", message: item.message };
  // v0.19.15 작업 카드 — 서버가 내려준 `card_role` 로만 고른다(본문을 읽어 짐작하지 않는다). 카드 id 가 없으면 보통 말풍선.
  if (item.message.card_id && item.message.card_role === "delegation") return { kind: "task_card", message: item.message };
  if (item.message.card_id && item.message.card_role === "result") return { kind: "result_card", message: item.message };
  return { kind: "message", message: item.message };
}

/** React key — 부분 묶음은 group_id, 나머지는 메시지 id. */
export function timelineItemKey(item: TimelineItem): string {
  return item.kind === "group" ? `group:${item.groupId}` : item.message.id;
}

type Renderers = { [K in TimelineEntry["kind"]]: (e: Extract<TimelineEntry, { kind: K }>, ctx: TimelineCtx) => ReactNode };

/** 「이걸 미션으로」 비활성 사유 — 이미 미션에 속한 메시지 · 보관된 방. */
const toWorkWhy = (m: Message, ctx: TimelineCtx) => (m.work_id ? ROOM_CENTER.has_work(ctx.workTitle(m.work_id)) : ctx.archived ? ROOM_HEAD.archived : null);
/** 「…」 메뉴를 두지 않는 메시지 종류. */
const NO_MENU: ReadonlySet<Message["kind"]> = new Set<Message["kind"]>(["system", "summary"]);
/** 메시지 종류별 꼬리표(카드 아래 한 줄). 없으면 꼬리표 없음. */
const FOOTER: Partial<Record<Message["kind"], (m: Message, ctx: TimelineCtx) => ReactNode>> = {
  summary: (m, ctx) => <p className="small muted" data-testid="summary-label">{m.work_id ? ROOM_CENTER.summary_of(ctx.workTitle(m.work_id)) : ROOM_CENTER.summary_room}</p>,
  system: (m, ctx) =>
    m.work_id && ctx.showWorkLink ? (
      <button type="button" className="msg__link" onClick={() => ctx.onOpenWork(m.work_id!)} data-testid="system-work-link">{ROOM_CENTER.open_work_chip}</button>
    ) : undefined,
};
/** 높이 유지 래퍼(COMPONENTS §9.10) — 게시된 메시지가 말풍선 자리에 서는 첫 프레임. */
const heldProps = (ctx: TimelineCtx, id: string) => ({
  style: ctx.held[id] ? { minHeight: ctx.held[id] } : undefined,
  "data-held": ctx.held[id] ? "true" : undefined,
});

const RENDER: Renderers = {
  // 부분 메시지(PRD FR-3.1.4 · SCREEN §4.6 v0.19.11) — 같은 group_id 행을 말풍선 하나로. 부분이 도착하는 대로 같은 말풍선에 채운다.
  group: ({ group }, ctx) => {
    const first = group.parts[0];
    return (
      <div {...heldProps(ctx, first.id)}>
        {ctx.pickButton(first)}
        <PartBubble
          parts={group.parts}
          size={group.size}
          me={ctx.me}
          conversation={ctx.conversation}
          partLayers={(m) => ctx.layers(m, { asAnswer: false, noProcess: true })}
          layers={ctx.layers}
          process={ctx.groupProcess(boundaryOf(group))}
          replies={ctx.replies}
          onLoadReplies={ctx.onLoadReplies}
          onReply={ctx.onReply}
          now={ctx.now}
          workLabel={ctx.workLabel(first.work_id, "message-work-label")}
          menu={<MessageMenu id={first.id} why={toWorkWhy(first, ctx)} onToWork={() => ctx.onToWork(first)} />}
        />
      </div>
    );
  },
  // 확인 요청 — 짝(요청)을 못 찾았으면 불러오는 중 자리.
  hitl: ({ message: m }, ctx) => {
    const hitl = pairHitl(m, ctx.hitls);
    return (
      <div data-message-id={m.id} className="s7__hitl" data-testid="timeline-hitl">
        {ctx.pickButton(m)}
        {hitl ? (
          <HitlCard
            request={hitl}
            onRespond={(body) => ctx.onRespondHitl(hitl.id, body)}
            budget={hitl.task_id ? { scope: "task", current: hitl.budget_override_usd, spent: null } : { scope: "session", current: ctx.roomBudget.current, spent: ctx.roomBudget.spent }}
            busy={ctx.busy}
            userName={ctx.userName}
          />
        ) : (
          <article className="hitl hitl--loading" data-testid="hitl-card-loading" aria-busy="true">
            <p className="hitl__q">{m.content}</p>
            <p className="hitl__gate">확인 요청을 불러오는 중…</p>
          </article>
        )}
      </div>
    );
  },
  // 작업 카드 — 위임 카드 · 결과 카드. 「…」 메뉴에 사람의 되돌리기(권한이 있을 때만) + 「이걸 미션으로」.
  task_card: ({ message: m }, ctx) => (
    <div {...heldProps(ctx, m.id)}>
      {ctx.pickButton(m)}
      <TaskCardBubble message={m} ctx={ctx} workLabel={ctx.workLabel(m.work_id, "message-work-label")} toWork={<ToWorkItem m={m} ctx={ctx} />} />
    </div>
  ),
  result_card: ({ message: m }, ctx) => (
    <div {...heldProps(ctx, m.id)}>
      {ctx.pickButton(m)}
      <ResultCardBubble message={m} ctx={ctx} workLabel={ctx.workLabel(m.work_id, "message-work-label")} toWork={<ToWorkItem m={m} ctx={ctx} />} />
    </div>
  ),
  message: ({ message: m }, ctx) => {
    const agentMsg = m.author_type === "agent" && m.source_task_id;
    const askee = m.kind === "blocked_q" ? m.mentions.find((x) => x.kind === "agent")?.display_name : undefined;
    return (
      <div {...heldProps(ctx, m.id)}>
        {ctx.pickButton(m)}
        <MessageCard
          message={m}
          replies={ctx.replies[m.id]}
          onLoadReplies={ctx.onLoadReplies}
          onReply={ctx.onReply}
          activity={agentMsg && !isLayered(m) ? ctx.taskActivity(m) : undefined}
          layers={ctx.layers}
          conversation={ctx.conversation}
          askee={askee}
          now={ctx.now}
          workLabel={ctx.workLabel(m.work_id, "message-work-label")}
          menu={!NO_MENU.has(m.kind) ? <MessageMenu id={m.id} why={toWorkWhy(m, ctx)} onToWork={() => ctx.onToWork(m)} /> : undefined}
          footer={FOOTER[m.kind]?.(m, ctx)}
        />
      </div>
    );
  },
};

/** 타임라인 항목 하나를 그린다 — 종류를 고르고(`timelineEntry`) 표(`RENDER`)의 렌더러에 넘긴다. */
function TimelineItemViewInner({ item, ctx }: { item: TimelineItem; ctx: TimelineCtx }) {
  const e = timelineEntry(item);
  return <>{(RENDER[e.kind] as (e: TimelineEntry, ctx: TimelineCtx) => ReactNode)(e, ctx)}</>;
}

/**
 * 항목이 같은가 — `timelineItems` 는 매번 새 껍데기를 만들므로 알맹이(메시지 참조 · 묶음의 부분 참조)로 잰다.
 * 방 화면의 `timelineCtx` deps 에 `messages` 가 있는 동안은 **중복 방어**다(알맹이가 바뀌면 ctx 가 먼저 바뀐다, #397 R1 NN7).
 * deps 에서 `messages` 를 빼면 이것이 유일한 방어가 된다 — 그 경로는 `TaskCardBubble.test` 「부분 바뀜」이 잰다.
 */
export function sameTimelineItem(a: TimelineItem, b: TimelineItem): boolean {
  if (a === b) return true;
  if (a.kind === "message" || b.kind === "message") return a.kind === b.kind && (a as { message: Message }).message === (b as { message: Message }).message;
  return a.groupId === b.groupId && a.size === b.size && a.parts.length === b.parts.length && a.parts.every((m, i) => m === b.parts[i]);
}

/**
 * memo(#397 NN4) — 방 화면의 `timelineCtx`(useMemo, #392 NN2)가 같고 항목 알맹이가 같으면 다시 그리지 않는다. 진행 메모(`message.delta`)·
 * 입력 중·작성창처럼 타임라인 항목과 무관한 상태가 바뀌면 방 화면만 다시 그리고 항목은 그대로다.
 */
export const TimelineItemView = memo(TimelineItemViewInner, (p, n) => p.ctx === n.ctx && sameTimelineItem(p.item, n.item));

/** 「이걸 미션으로」 항목 하나 — 카드 말풍선의 「⋯」 메뉴가 카드 동작 뒤에 둔다(메시지 메뉴와 같은 항목·사유). */
function ToWorkItem({ m, ctx }: { m: Message; ctx: TimelineCtx }) {
  const why = toWorkWhy(m, ctx);
  const hint = `msg-menu-hint-${m.id}`;
  return (
    <>
      <button type="button" role="menuitem" className="card-menu__item" aria-disabled={!!why || undefined} aria-describedby={why ? hint : undefined}
        onClick={() => { if (!why) ctx.onToWork(m); }} data-testid="message-to-work">
        {ROOM_CENTER.to_work}
      </button>
      {why && <DisabledHint id={hint}>{why}</DisabledHint>}
    </>
  );
}

/** 메시지 「…」 메뉴 — 「이걸 미션으로」(S21 은 W3). 이미 미션에 속한 메시지·보관된 방에서는 비활성 + 사유(버튼 아래 글자). */
export function MessageMenu({ id, why, onToWork }: { id: string; why: string | null; onToWork: () => void }) {
  const [open, setOpen] = useState(false);
  const hint = `msg-menu-hint-${id}`;
  return (
    <span className="msg-menu" onBlur={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setOpen(false); }}>
      <button type="button" className="msg__link" aria-haspopup="menu" aria-expanded={open} aria-label={ROOM_CENTER.msg_menu} onClick={() => setOpen((v) => !v)} data-testid="message-menu">
        …
      </button>
      {open && (
        <span className="card-menu__list msg-menu__list" role="menu" data-testid="message-menu-list">
          <button type="button" role="menuitem" className="card-menu__item" aria-disabled={!!why || undefined} aria-describedby={why ? hint : undefined}
            onClick={() => { if (why) return; setOpen(false); onToWork(); }} data-testid="message-to-work">
            {ROOM_CENTER.to_work}
          </button>
          {why && <DisabledHint id={hint}>{why}</DisabledHint>}
        </span>
      )}
    </span>
  );
}
