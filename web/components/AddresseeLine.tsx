"use client";
/**
 * Addressee Line — 메시지 머리 한 줄 「작성자 ‹종류› → 받는 쪽」(COMPONENTS §9.8 · SCREEN §4.6 「대화 배치」 · PRD FR-3.1.3).
 * 판정은 `lib/conversation.ts` 가 서버 칸으로만 한다. 여기는 그리기만.
 */
import type { ReactNode } from "react";
import { Badge } from "@/components/Badge";
import { Slot } from "@/components/Slot";
import type { Addressee, Speech, SpeechKind } from "@/lib/conversation";
import { CONVERSATION as L } from "@/lib/wording";

type Tone = "run" | "done" | "block" | "neutral";
const KIND_STYLE: Partial<Record<SpeechKind, { glyph: string; tone: Tone }>> = {
  instruct: { glyph: "▶", tone: "run" },
  delegate: { glyph: "↪", tone: "run" },
  request: { glyph: "→", tone: "run" },
  report: { glyph: "✓", tone: "done" },
  question: { glyph: "?", tone: "block" },
  answer: { glyph: "↳", tone: "neutral" },
  note: { glyph: "✎", tone: "neutral" },
  summary: { glyph: "✓", tone: "done" },
};

const MAX_TO = 3;

/** 종류 라벨 — 대화(chat)·시스템·HITL 은 라벨이 없다(표에 없는 종류도 마찬가지). */
export function speechLabel(s: Speech): string | null {
  return (L.kind as Record<string, string>)[s.kind] ?? null;
}

export function addresseeName(a: Addressee): string {
  switch (a.kind) {
    case "room":
      return L.to_room;
    case "all":
      return L.to_all;
    case "record":
      return L.to_record;
    case "agent":
      return `@${a.name}`;
    default:
      return a.name;
  }
}

/** 스크린리더용 한 줄 — 「〈작성자〉: 〈받는 쪽〉에게 〈종류〉」. 묶여서 이름이 안 보여도 전부 읽힌다. */
export function speechAria(author: string, s: Speech): string {
  const label = speechLabel(s);
  const to = s.to.filter((a) => a.kind !== "room" && a.kind !== "record");
  const target = to.length ? `${to.map(addresseeName).join(", ")}${L.aria_to}` : s.to.some((a) => a.kind === "record") ? L.to_record : L.aria_room;
  return [`${author}:`, target, label].filter(Boolean).join(" ");
}

export function KindChip({ speech }: { speech: Speech }) {
  const label = speechLabel(speech);
  const st = KIND_STYLE[speech.kind];
  if (!label || !st) return null;
  return (
    <span className="msg__kind" data-tone={st.tone} data-testid="speech-kind" data-speech={speech.kind}>
      <span aria-hidden="true">{st.glyph}</span>
      {label}
    </span>
  );
}

export function AddresseeChips({ to }: { to: Addressee[] }) {
  const shown = to.slice(0, MAX_TO);
  const rest = to.length - shown.length;
  return (
    <span className="convo__to" data-testid="speech-to">
      {shown.map((a) => (
        <span key={`${a.kind}:${a.id ?? a.name}`} className="convo__to-chip" data-to-kind={a.kind}>
          {addresseeName(a)}
        </span>
      ))}
      {rest > 0 && (
        <span className="convo__to-chip" data-to-kind="more">
          <Slot text={L.more} n={rest} />
        </span>
      )}
    </span>
  );
}

export interface AddresseeLineProps {
  speech: Speech;
  author: ReactNode;
  /** 묶음 — 작성자를 숨긴다(받는 쪽은 남긴다). */
  grouped?: boolean;
  meta?: ReactNode;
  tail?: ReactNode;
}

export function AddresseeLine({ speech, author, grouped, meta, tail }: AddresseeLineProps) {
  return (
    <div className="msg__head convo__head" data-testid="addressee-line">
      {!grouped && author}
      <KindChip speech={speech} />
      {speech.kind !== "system" && speech.kind !== "hitl" && (
        <>
          <span className="convo__arrow" aria-hidden="true">→</span>
          <AddresseeChips to={speech.to} />
        </>
      )}
      {speech.kind === "delegate" && speech.lane && <Badge kind="lane" value={speech.lane.status} size="sm" />}
      {meta}
      {tail}
    </div>
  );
}

/** 보고의 「↩ 〈요청자〉의 「첫 줄」에 대한 보고」 — 누르면 원래 메시지로. */
export function ReportOfLink({ speech, onJump }: { speech: Speech; onJump?: (messageId: string) => void }) {
  const r = speech.reportOf;
  if (speech.kind !== "report" || !r) return null;
  return (
    <button type="button" className="msg__link convo__report" onClick={() => onJump?.(r.messageId)} data-testid="report-of">
      {L.report_of_head}
      {r.requester}
      {L.report_of_mid}
      {r.excerpt}
      {L.report_of_tail}
    </button>
  );
}
