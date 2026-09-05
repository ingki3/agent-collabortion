"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import "./composer.css";
import { activeMentionQuery, extractMentions, mentionLink, type MentionTarget } from "@/lib/mentions";

export interface ComposerAgent {
  id: string;
  name: string;
  /** 세션 참여자인가. 아니면 멘션 시 경고 칩(E1-04). */
  participant: boolean;
}
export interface ComposerMember {
  id: string;
  name: string;
}

export interface ComposerWarning {
  code: string;
  message: string;
  agent_id?: string | null;
}

export interface ComposerProps {
  /** 워크스페이스 에이전트 전부(참여 여부 포함) — 자동완성은 참여자를 위에 둔다. */
  agents: ComposerAgent[];
  members?: ComposerMember[];
  replyTo?: { id: string; authorName: string } | null;
  onCancelReply?: () => void;
  onSubmit: (input: { content: string; parentId: string | null; suppressAgentIds: string[] }) => Promise<ComposerWarning[] | void>;
  disabled?: boolean;
  disabledReason?: string;
  placeholder?: string;
}

/**
 * 트리거 미리보기의 판정 근거(PRD FR-3.3, 위에서부터 우선).
 * - `note`: 규칙 1 — `/note ` 접두 → 트리거 없음(기록만). 칩을 전부 숨긴다.
 * - `mention`: 규칙 2 — 에이전트 명시 멘션 → 참여자는 트리거, 비참여자는 경고만(E1-04).
 * - `no_trigger`: 규칙 3 — `@all` 또는 사람만 멘션 → 에이전트 트리거 없음.
 * - `implicit`: 멘션 없음 — 규칙 4~6(답글·assignee)은 서버 상태가 필요해 로컬로 예고하지 않는다(`previewTriggers` 는 P2).
 */
export type TriggerRule = "note" | "mention" | "no_trigger" | "implicit";

export const NOTE_PREFIX = "/note ";

/** 규칙 1 — `/note ` 접두(앞 공백 무시). */
export function isNote(content: string): boolean {
  const t = content.trimStart();
  return t.startsWith(NOTE_PREFIX) || t === NOTE_PREFIX.trim();
}

export interface TriggerPreview {
  rule: TriggerRule;
  /** 트리거될 참여 에이전트(규칙 2). */
  triggers: ComposerAgent[];
  /** 멘션됐지만 참여자가 아닌 에이전트 — 경고 칩(E1-04). */
  nonParticipants: MentionTarget[];
}

/** 이 메시지가 트리거할 참여 에이전트를 로컬 규칙으로 판정한다. 틀릴 수 있는 규칙(4~6)은 판정하지 않는다. */
export function classifyMentions(content: string, agents: ComposerAgent[]): TriggerPreview {
  if (isNote(content)) return { rule: "note", triggers: [], nonParticipants: [] };
  const all = extractMentions(content);
  const mentions = all.filter((m) => m.kind === "agent");
  if (mentions.length === 0) {
    return { rule: all.length > 0 ? "no_trigger" : "implicit", triggers: [], nonParticipants: [] };
  }
  const byId = new Map(agents.map((a) => [a.id, a]));
  const triggers: ComposerAgent[] = [];
  const nonParticipants: MentionTarget[] = [];
  for (const m of mentions) {
    const a = byId.get(m.id);
    if (a?.participant) triggers.push(a);
    else nonParticipants.push(m);
  }
  return { rule: "mention", triggers, nonParticipants };
}

export function Composer(props: ComposerProps) {
  const [text, setText] = useState("");
  const [caret, setCaret] = useState(0);
  const [sel, setSel] = useState(0);
  const [busy, setBusy] = useState(false);
  const [serverWarnings, setServerWarnings] = useState<ComposerWarning[]>([]);
  const [suppressed, setSuppressed] = useState<Set<string>>(new Set());
  const taRef = useRef<HTMLTextAreaElement>(null);

  const query = useMemo(() => activeMentionQuery(text, caret), [text, caret]);
  const candidates = useMemo(() => {
    if (!query) return [];
    const q = query.query.toLowerCase();
    const agents = [...props.agents].sort((a, b) => Number(b.participant) - Number(a.participant));
    const list: (MentionTarget & { sub: string })[] = agents
      .filter((a) => a.name.toLowerCase().includes(q))
      .map((a) => ({ kind: "agent", id: a.id, name: a.name, sub: a.participant ? "참여자" : "비참여 에이전트" }));
    for (const m of props.members ?? []) {
      if (m.name.toLowerCase().includes(q)) list.push({ kind: "user", id: m.id, name: m.name, sub: "멤버" });
    }
    if ("all".startsWith(q)) list.push({ kind: "all", id: "all", name: "all", sub: "모두(트리거 없음)" });
    return list.slice(0, 8);
  }, [query, props.agents, props.members]);

  useEffect(() => setSel(0), [candidates.length, query?.query]);

  const { rule, triggers, nonParticipants } = useMemo(() => classifyMentions(text, props.agents), [text, props.agents]);

  function insertMention(t: MentionTarget) {
    if (!query) return;
    const before = text.slice(0, query.start);
    const after = text.slice(caret);
    const link = mentionLink(t) + " ";
    const next = before + link + after;
    setText(next);
    const pos = before.length + link.length;
    requestAnimationFrame(() => {
      taRef.current?.focus();
      taRef.current?.setSelectionRange(pos, pos);
      setCaret(pos);
    });
  }

  async function submit() {
    const content = text.trim();
    if (!content || busy || props.disabled) return;
    setBusy(true);
    try {
      const warnings = await props.onSubmit({
        content,
        parentId: props.replyTo?.id ?? null,
        suppressAgentIds: [...suppressed],
      });
      setServerWarnings(warnings ?? []);
      setText("");
      setSuppressed(new Set());
      setCaret(0);
    } finally {
      setBusy(false);
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (candidates.length && query) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSel((s) => (s + 1) % candidates.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setSel((s) => (s - 1 + candidates.length) % candidates.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        insertMention(candidates[sel]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setCaret(0);
        return;
      }
    }
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void submit();
    }
  }

  const disabled = props.disabled || busy;

  return (
    <div className="composer" data-testid="composer">
      {props.replyTo && (
        <div className="composer__reply" data-testid="composer-reply-target">
          답글 → <b>{props.replyTo.authorName}</b>
          {props.onCancelReply && (
            <button type="button" className="chip__x" onClick={props.onCancelReply} aria-label="답글 취소">
              ✕
            </button>
          )}
        </div>
      )}
      {query && candidates.length > 0 && (
        <div className="mention-menu" role="listbox" data-testid="mention-menu">
          {candidates.map((c, i) => (
            <button
              key={`${c.kind}:${c.id}`}
              type="button"
              role="option"
              aria-selected={i === sel}
              className="mention-menu__item"
              onMouseDown={(e) => {
                e.preventDefault();
                insertMention(c);
              }}
            >
              <span>@{c.name}</span>
              <span className="mention-menu__kind">{c.sub}</span>
            </button>
          ))}
        </div>
      )}
      <textarea
        ref={taRef}
        className="composer__ta"
        value={text}
        placeholder={props.placeholder ?? "메시지 — @로 에이전트를 부릅니다"}
        disabled={props.disabled}
        aria-label="메시지 작성"
        data-testid="composer-input"
        onChange={(e) => {
          setText(e.target.value);
          setCaret(e.target.selectionStart ?? e.target.value.length);
        }}
        onSelect={(e) => setCaret((e.target as HTMLTextAreaElement).selectionStart ?? 0)}
        onKeyDown={onKeyDown}
      />
      <div className="composer__chips" data-testid="composer-chips" data-rule={rule}>
        {rule === "note" && (
          <span className="chip" data-testid="chip-note-only" title="PRD FR-3.3 규칙 1 — /note 접두 메시지는 트리거 없음">
            기록만 — 아무도 깨우지 않습니다(규칙 1)
          </span>
        )}
        {rule === "no_trigger" && (
          <span className="chip" data-testid="chip-no-trigger" title="PRD FR-3.3 규칙 3 — @all·사람만 멘션이면 에이전트 트리거 없음">
            트리거 없음 — @all·사람만 멘션(규칙 3)
          </span>
        )}
        {nonParticipants.map((m) => (
          <span key={m.id} className="chip chip--warn" data-testid="chip-not-participant" title="게시는 되지만 트리거되지 않습니다(E1-04)">
            ⚠ @{m.name}는 이 세션 참여자가 아님
          </span>
        ))}
        {triggers.map((a) =>
          suppressed.has(a.id) ? (
            <span key={a.id} className="chip" data-testid="chip-suppressed">
              @{a.name} 트리거 억제됨
              <button
                type="button"
                className="chip__x"
                aria-label={`@${a.name} 트리거 복원`}
                onClick={() => setSuppressed((s) => { const n = new Set(s); n.delete(a.id); return n; })}
              >
                ↺
              </button>
            </span>
          ) : (
            <span key={a.id} className="chip chip--trigger" data-testid="chip-trigger">
              @{a.name}를 트리거합니다
              <button
                type="button"
                className="chip__x"
                aria-label={`@${a.name} 트리거 억제`}
                title="이번 메시지에서만 깨우지 않음(FR-3.6)"
                onClick={() => setSuppressed((s) => new Set(s).add(a.id))}
              >
                ✕
              </button>
            </span>
          ),
        )}
        {serverWarnings.map((w, i) => (
          <span key={i} className="chip chip--warn" data-testid="chip-server-warning">
            ⚠ {w.message}
          </span>
        ))}
      </div>
      <div className="composer__foot">
        <span className="composer__hint">⌘/Ctrl+Enter 로 전송 · @ 로 멘션</span>
        <button
          type="button"
          className="btn btn--primary btn--sm"
          disabled={disabled || !text.trim()}
          title={props.disabled ? props.disabledReason : undefined}
          onClick={() => void submit()}
          data-testid="composer-send"
        >
          {busy ? "전송 중…" : "전송"}
        </button>
      </div>
    </div>
  );
}

export default Composer;
