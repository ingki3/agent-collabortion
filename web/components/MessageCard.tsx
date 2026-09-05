"use client";
import { useState } from "react";
import "./message-card.css";
import { splitContent } from "@/lib/mentions";
import { clockTime, relativeTime } from "@/lib/time";
import type { Message } from "@/lib/api/types";

/**
 * kind 별 배지(COMPONENTS §2.2 K3). `answer` 는 message_kind 가 아니라 질문 카드(blocked_q) 스레드의 답글이다.
 */
type KindBadge = { glyph: string; label: string; tone: "neutral" | "block" | "done" | "wait" } | null;

export function kindBadgeFor(m: Pick<Message, "kind">, opts: { answer?: boolean; askee?: string } = {}): KindBadge {
  if (opts.answer) return { glyph: "↳", label: "answer", tone: "neutral" };
  switch (m.kind) {
    case "system":
      return { glyph: "·", label: "system", tone: "neutral" };
    case "blocked_q":
      return { glyph: "?", label: opts.askee ? `질문 → @${opts.askee}` : "질문", tone: "block" };
    case "summary":
      return { glyph: "✓", label: "summary", tone: "done" };
    case "hitl":
      return { glyph: "⏳︎", label: "HITL", tone: "wait" };
    default:
      return null;
  }
}

export function authorName(m: Message): string {
  if (m.author?.name) return m.author.name;
  if (m.author_type === "system") return "시스템";
  return m.author_type === "agent" ? "agent" : "member";
}

export function MessageBody({ content }: { content: string }) {
  return (
    <div className="msg__body">
      {splitContent(content).map((p, i) =>
        p.type === "text" ? (
          <span key={i}>{p.text}</span>
        ) : (
          <span key={i} className="msg__mention" data-mention={`${p.target.kind}:${p.target.id}`}>
            @{p.target.name}
          </span>
        ),
      )}
    </div>
  );
}

export interface MessageCardProps {
  message: Message;
  /** 스레드 답글. 부모가 넘기면 접기/펼치기로 보여준다. 없고 reply_count 만 있으면 onLoadReplies 로 요청한다. */
  replies?: Message[];
  onLoadReplies?: (rootId: string) => Promise<void> | void;
  /** 기본 접힘 여부. */
  defaultOpen?: boolean;
  onReply?: (m: Message) => void;
  /** 에이전트 메시지의 "활동 보기" 슬롯(Activity Feed). */
  activity?: React.ReactNode;
  /** 슬롯을 토글로 감싼다(펼침 라벨). */
  activityLabel?: string;
  /** blocked_q 카드의 위임자 이름(배지 "질문 → @Lead"). */
  askee?: string;
  /** 스레드 안 답글로 렌더 중인가(질문 카드 답글이면 answer 배지). */
  asAnswer?: boolean;
  now?: number;
}

export function MessageCard(props: MessageCardProps) {
  const { message: m } = props;
  const [open, setOpen] = useState(props.defaultOpen ?? false);
  const [loading, setLoading] = useState(false);
  const [activityOpen, setActivityOpen] = useState(false);
  const replyCount = props.replies?.length ?? m.reply_count ?? 0;
  const badge = kindBadgeFor(m, { answer: props.asAnswer, askee: props.askee });

  async function toggleThread() {
    if (!open && !props.replies && props.onLoadReplies) {
      setLoading(true);
      try {
        await props.onLoadReplies(m.id);
      } finally {
        setLoading(false);
      }
    }
    setOpen((v) => !v);
  }

  return (
    <article className="msg" data-kind={m.kind} data-message-id={m.id} data-testid="message-card">
      <div className="msg__head">
        {badge && (
          <span className="msg__kind" data-tone={badge.tone} data-testid="message-kind">
            <span aria-hidden="true">{badge.glyph}</span>
            {badge.label}
          </span>
        )}
        <span className={`msg__author${m.author_type === "agent" ? " msg__author--agent" : ""}`}>{authorName(m)}</span>
        <span className="msg__meta" title={m.created_at}>
          {clockTime(m.created_at)} · {relativeTime(m.created_at, props.now)}
          {m.is_note ? " · note" : ""}
        </span>
      </div>
      <MessageBody content={m.content} />
      <div className="msg__actions">
        {replyCount > 0 && (
          <button type="button" className="msg__link" onClick={toggleThread} aria-expanded={open} data-testid="thread-toggle">
            {loading ? "불러오는 중…" : open ? `답글 ${replyCount}개 접기` : `답글 ${replyCount}개 보기`}
          </button>
        )}
        {props.onReply && m.kind !== "system" && (
          <button type="button" className="msg__link" onClick={() => props.onReply?.(m)} data-testid="reply-button">
            답글
          </button>
        )}
        {props.activity && (
          <button
            type="button"
            className="msg__link"
            onClick={() => setActivityOpen((v) => !v)}
            aria-expanded={activityOpen}
            data-testid="activity-toggle"
          >
            {activityOpen ? "활동 접기" : (props.activityLabel ?? "활동 보기")}
          </button>
        )}
      </div>
      {props.activity && activityOpen && <div className="msg__slot">{props.activity}</div>}
      {open && props.replies && (
        <div className="msg__thread" data-testid="thread">
          {props.replies.map((r) => (
            <MessageCard key={r.id} message={r} asAnswer={m.kind === "blocked_q"} onReply={props.onReply} now={props.now} />
          ))}
        </div>
      )}
    </article>
  );
}

export default MessageCard;
