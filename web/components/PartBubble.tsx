"use client";
/**
 * Part Bubble · Part Head — 부분 메시지 말풍선(COMPONENTS §9.11 · SCREEN §4.6 v0.19.11 · PRD FR-3.1.4).
 *
 * 에이전트 Chat Bubble(§9.8)을 부분(section) 여럿으로 나눈 것. 테두리·배경·폭·아바타는 §9.8 그대로(`.convo[data-side=left]`).
 *  - 작성자 머리: 아바타 + 「@작성자」 + 시각 + 미션 라벨 + 「⋯」 — **한 번**. 받는 쪽·종류는 부분마다 다르므로 여기 없다.
 *  - 부분마다: Part Head(종류 배지 → 받는 쪽 칩 · 위임 상태 칩) · 보고면 「↩ … 에 대한 보고」 · 본문 · 부분 꼬리(아티팩트 · 작업 내용 · 답글).
 *  - 부분 사이 1px `$line`. 말풍선 꼬리: 작업 과정 **하나**(한 턴 — 묶음은 경계 하나).
 *  - 「나」 강조: 보는 사람이 받는 쪽인 부분은 받는 쪽 칩 굵게 + 부분 왼쪽 2px `$s-run` 선.
 * 판정(말의 종류·받는 쪽)은 서버 칸을 `conversation` 이 옮긴 것 그대로 — 여기서 다시 계산하지 않는다.
 */
import { useState, type ReactNode } from "react";
import "./message-card.css";
import type { Message } from "@/lib/api/types";
import { clockTime, relativeTime } from "@/lib/time";
import { Badge } from "@/components/Badge";
import { AddresseeChips, KindChip, ReportOfLink, addresseeName, speechLabel } from "./AddresseeLine";
import { MessageBody, MessageCard, authorName, type ConversationSlot, type MessageLayerSlots } from "./MessageCard";
import { PARTS as L } from "@/lib/wording";
import type { Speech } from "@/lib/conversation";

/** 부분 머리 — 「‹종류› → 받는 쪽」(+ 위임 상태 칩). 보는 사람이 받는 쪽이면 그 칩에 「나」 표시. */
export function PartHead({ speech, me }: { speech: Speech; me?: string | null }) {
  return (
    <div className="part__head" data-testid="part-head">
      <span className="part__kindrow">
        <KindChip speech={speech} />
        <span className="convo__arrow" aria-hidden="true">→</span>
      </span>
      <AddresseeChips to={speech.to} me={me} />
      {speech.kind === "delegate" && speech.lane && <Badge kind="lane" value={speech.lane.status} size="sm" />}
    </div>
  );
}

/** 「〈받는 쪽〉에게 〈종류〉」 — 부분 section 의 aria-label. */
export function partAria(s: Speech): string {
  const to = s.to.filter((a) => a.kind !== "room" && a.kind !== "record");
  const target = to.length ? `${to.map(addresseeName).join(", ")}${L.aria_to}` : L.aria_room;
  return [target, speechLabel(s)].filter(Boolean).join(" ");
}

/** 보는 사람이 이 부분의 받는 쪽인가. */
export function addressedTo(s: Speech, me: string | null | undefined): boolean {
  return !!me && s.to.some((a) => a.kind === "user" && a.id === me);
}

interface PartProps {
  message: Message;
  speech: Speech;
  me?: string | null;
  layers?: MessageLayerSlots;
  replies?: Message[];
  onLoadReplies?: (rootId: string) => Promise<void> | void;
  onReply?: (m: Message) => void;
  onJump?: (messageId: string) => void;
  now?: number;
  /** 스레드 답글을 그릴 때 쓰는 대화·세 층 슬롯(MessageCard 와 같은 것). */
  threadLayers?: (m: Message, opts: { asAnswer: boolean }) => MessageLayerSlots | undefined;
  threadConversation?: (m: Message, opts: { parent?: Message }) => ConversationSlot | undefined;
}

/** 부분 하나 — 부분이 곧 메시지 행이라 답글·답글 수·「답글 N개 보기」도 부분마다다. */
function Part({ message: m, speech, me, layers, replies, onLoadReplies, onReply, onJump, now, threadLayers, threadConversation }: PartProps) {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const replyCount = replies?.length ?? m.reply_count ?? 0;
  const mine = addressedTo(speech, me);
  async function toggleThread() {
    if (!open && !replies && onLoadReplies) {
      setLoading(true);
      try {
        await onLoadReplies(m.id);
      } finally {
        setLoading(false);
      }
    }
    setOpen((v) => !v);
  }
  return (
    <section
      className="part"
      data-testid="part"
      data-message-id={m.id}
      data-group-index={m.group_index ?? undefined}
      data-speech={speech.kind}
      data-me={mine ? "true" : undefined}
      aria-label={partAria(speech)}
    >
      <PartHead speech={speech} me={me} />
      <ReportOfLink speech={speech} onJump={onJump} />
      {layers ? (
        <>
          {layers.body && <MessageBody content={layers.body} />}
          {layers.below}
        </>
      ) : (
        <MessageBody content={m.content} />
      )}
      <div className="msg__actions">
        {replyCount > 0 && (
          <button type="button" className="msg__link" onClick={toggleThread} aria-expanded={open} data-testid="thread-toggle">
            {loading ? L.replies_loading : open ? `${L.replies_hide[0]}${replyCount}${L.replies_hide[1]}` : `${L.replies_show[0]}${replyCount}${L.replies_show[1]}`}
          </button>
        )}
        {onReply && (
          <button type="button" className="msg__link" onClick={() => onReply(m)} data-testid="reply-button">
            {L.reply}
          </button>
        )}
      </div>
      {open && replies && (
        <div className="msg__thread" data-testid="thread">
          {replies.map((r) => (
            <MessageCard key={r.id} message={r} onReply={onReply} now={now} layers={threadLayers} conversation={threadConversation} parent={m} />
          ))}
        </div>
      )}
    </section>
  );
}

export interface PartBubbleProps {
  /** 도착한 부분, `group_index` 순(lib/parts `timelineItems`). */
  parts: Message[];
  /**
   * 묶음의 부분 수 — 아직 다 안 왔으면 `data-filling`(실시간 채우는 중).
   * **테스트 훅일 뿐 시각 표시가 아니다**: 정본은 채우는 중을 따로 그리라고 하지 않는다(부분이 조용히 붙는다).
   * 이름이 상태를 암시해 CSS 가 빠진 줄 오해하기 쉬워 적어 둔다(리뷰 #374b NN3).
   */
  size: number;
  /** 보는 사람의 user id — 「나」 강조. */
  me?: string | null;
  conversation: (m: Message, opts: { parent?: Message }) => ConversationSlot | undefined;
  /** 부분의 세 층 — **작업 과정 없이**(작업 과정은 말풍선 꼬리 `process` 하나). */
  partLayers: (m: Message) => MessageLayerSlots | undefined;
  /** 스레드 답글용(MessageCard 와 같은 슬롯). */
  layers?: (m: Message, opts: { asAnswer: boolean }) => MessageLayerSlots | undefined;
  /** 말풍선 맨 아래 작업 과정 하나. */
  process?: ReactNode;
  replies: Record<string, Message[] | undefined>;
  onLoadReplies?: (rootId: string) => Promise<void> | void;
  onReply?: (m: Message) => void;
  now?: number;
  workLabel?: ReactNode;
  menu?: ReactNode;
}

export function PartBubble(props: PartBubbleProps) {
  const { parts } = props;
  const first = parts[0];
  const name = authorName(first);
  const slots = parts.map((m) => props.conversation(m, {}));
  const aria = `${name} · ${L.count[0]}${props.size}${L.count[1]} · ${clockTime(first.created_at)}`;
  return (
    <article
      className="msg convo part-bubble"
      data-kind="text"
      data-side="left"
      data-testid="part-bubble"
      data-group-id={first.group_id ?? undefined}
      data-filling={parts.length < props.size ? "true" : undefined}
      aria-label={aria}
    >
      <span className="convo__avatar" aria-hidden="true">{name.slice(0, 1).toUpperCase()}</span>
      <div className="convo__col">
        <div className="msg__head convo__head" data-testid="part-author">
          <span className="msg__author msg__author--agent">{name}</span>
          <span className="msg__meta" title={first.created_at}>
            {clockTime(first.created_at)} · {relativeTime(first.created_at, props.now)}
          </span>
          {props.workLabel}
          {props.menu && <span className="msg__menu">{props.menu}</span>}
        </div>
        <div className="convo__bubble part-bubble__bubble">
          {parts.map((m, i) => {
            const conv = slots[i];
            if (!conv) return null;
            return (
              <Part
                key={m.id}
                message={m}
                speech={conv.speech}
                me={props.me}
                layers={props.partLayers(m)}
                replies={props.replies[m.id]}
                onLoadReplies={props.onLoadReplies}
                onReply={props.onReply}
                onJump={conv.onJump}
                now={props.now}
                threadLayers={props.layers}
                threadConversation={props.conversation}
              />
            );
          })}
          {props.process && <div className="part-bubble__process" data-testid="part-process">{props.process}</div>}
        </div>
      </div>
    </article>
  );
}

export default PartBubble;
