"use client";
/**
 * 작성창(SCREEN §4.5 중앙 하단) — 멘션 자동완성 · **트리거 미리보기** · 개별 억제 · "새 lane으로 보내기" 토글.
 *
 * **트리거 미리보기는 서버가 판정한다**(`previewTriggers`, FR-3.6 — W-6). FR-3.3 의 8개 규칙과 lane 해소 규칙은
 * 서버 상태(실행 중 task·lane·합류 그룹)를 봐야 하므로 로컬로 흉내 내면 서버와 반대로 말하게 된다(P1 의 W-6·S-1 이 그랬다).
 * 여기서는 계약 `TriggerPreview` 를 그대로 그린다 — 로컬 규칙 계산은 없다.
 *
 * **`new_lane` 토글은 전송 후 자동 해제된다**(t-2, E2-07·E2-14). 해제되지 않으면 이후 모든 멘션이 lane 을 새로 만들어
 * 해소 규칙 3(실행 중 lane 재사용)이 사실상 죽는다. 켜져 있는 동안은 전송 버튼 옆에 "새 lane으로 전송됨"을 **상시** 표시한다.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import "./composer.css";
import { activeMentionQuery, mentionLink, type MentionTarget } from "@/lib/mentions";
import type { TriggerPreview } from "@/lib/api/types";

export interface ComposerAgent {
  id: string;
  name: string;
  /** 세션 참여자인가 — 자동완성 정렬·부제에만 쓴다. 트리거 판정은 서버 몫이다. */
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

export interface ComposerInput {
  content: string;
  parentId: string | null;
  /** "새 lane으로 보내기"(t-2). 전송 후 자동 해제된다. */
  newLane: boolean;
  suppressAgentIds: string[];
}

export interface ComposerProps {
  agents: ComposerAgent[];
  members?: ComposerMember[];
  replyTo?: { id: string; authorName: string } | null;
  onCancelReply?: () => void;
  /** 서버 트리거 미리보기(`POST /sessions/{id}/messages/preview`). 없으면 칩을 그리지 않는다. */
  onPreview?: (input: ComposerInput) => Promise<TriggerPreview>;
  onSubmit: (input: ComposerInput) => Promise<ComposerWarning[] | void>;
  disabled?: boolean;
  disabledReason?: string;
  placeholder?: string;
  /** 작성창 위 안내 — 예: 세션 `paused` 중 "재개 후 처리됩니다"(U15-9). */
  notice?: string;
  /** 미리보기 디바운스(ms). 테스트에서 0 으로 줄인다. */
  previewDelayMs?: number;
  /** 외부에서 채워 넣는 초안(lane 카드의 "중단하고 다시 지시" — 멘션이 미리 채워진다). */
  draft?: { content: string; nonce: number } | null;
}

/** 규칙 번호 → 사람 문구. 미리보기 칩의 근거를 숨기지 않는다. */
export const RULE_NOTE: Record<number, string> = {
  1: "기록만(규칙 1)",
  2: "명시 멘션(규칙 2)",
  3: "트리거 없음(규칙 3)",
  4: "스레드 답글(규칙 4)",
  5: "질문 답글(규칙 5)",
  6: "assignee 기본(규칙 6)",
  7: "assignee 폴백(규칙 7)",
  8: "합류(규칙 8)",
};

const EMPTY: TriggerPreview = { triggers: [], warnings: [], note_only: false };

export function Composer(props: ComposerProps) {
  const [text, setText] = useState("");
  const [caret, setCaret] = useState(0);
  const [sel, setSel] = useState(0);
  const [busy, setBusy] = useState(false);
  const [newLane, setNewLane] = useState(false);
  const [serverWarnings, setServerWarnings] = useState<ComposerWarning[]>([]);
  /** 억제된 에이전트 id → 이름. 억제하면 서버 미리보기에서 사라지므로 이름을 여기 들고 있어야 ↺ 칩을 그린다. */
  const [suppressed, setSuppressed] = useState<Map<string, string>>(new Map());
  const [preview, setPreview] = useState<TriggerPreview | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);

  const parentId = props.replyTo?.id ?? null;
  const suppressKey = [...suppressed.keys()].sort().join(",");
  const suppressIds = useMemo(() => (suppressKey ? suppressKey.split(",") : []), [suppressKey]);
  const { onPreview } = props;
  const delay = props.previewDelayMs ?? 250;

  // 외부 초안(lane "중단하고 다시 지시") — nonce 가 바뀔 때만 덮어쓴다.
  const draftNonce = props.draft?.nonce;
  const draftContent = props.draft?.content;
  useEffect(() => {
    if (draftNonce === undefined) return;
    setText(draftContent ?? "");
    taRef.current?.focus();
  }, [draftNonce, draftContent]);

  // ── 서버 트리거 미리보기(FR-3.6) — 디바운스, 마지막 응답만 채택 ──
  useEffect(() => {
    if (!onPreview) return;
    const content = text.trim();
    if (!content) {
      setPreview(null);
      setPreviewError(null);
      return;
    }
    let live = true;
    setPreviewing(true);
    const t = setTimeout(() => {
      onPreview({ content, parentId, newLane, suppressAgentIds: suppressIds })
        .then((p) => {
          if (!live) return;
          setPreview(p);
          setPreviewError(null);
        })
        .catch((e) => {
          if (!live) return;
          setPreview(null);
          setPreviewError(e instanceof Error ? e.message : "미리보기를 가져오지 못했습니다");
        })
        .finally(() => {
          if (live) setPreviewing(false);
        });
    }, delay);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [text, parentId, newLane, suppressIds, onPreview, delay]);

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

  function insertMention(t: MentionTarget) {
    if (!query) return;
    const before = text.slice(0, query.start);
    const after = text.slice(caret);
    const link = mentionLink(t) + " ";
    const next = before + link + after;
    setText(next);
    const pos = before.length + link.length;
    taRef.current?.focus();
    taRef.current?.setSelectionRange(pos, pos);
    setCaret(pos);
  }

  const suppress = useCallback((id: string, name: string) => {
    setSuppressed((s) => new Map(s).set(id, name));
  }, []);
  const unsuppress = useCallback((id: string) => {
    setSuppressed((s) => {
      const n = new Map(s);
      n.delete(id);
      return n;
    });
  }, []);

  async function submit() {
    const content = text.trim();
    if (!content || busy || props.disabled) return;
    setBusy(true);
    try {
      const warnings = await props.onSubmit({ content, parentId, newLane, suppressAgentIds: suppressIds });
      setServerWarnings(warnings ?? []);
      setText("");
      setSuppressed(new Map());
      setPreview(null);
      setCaret(0);
      setNewLane(false); // t-2 — 토글은 전송 후 자동 해제(E2-07·E2-14)
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

  const p = preview ?? EMPTY;
  const disabled = props.disabled || busy;

  return (
    <div className="composer" data-testid="composer">
      {props.notice && (
        <div className="composer__notice" data-testid="composer-notice">
          {props.notice}
        </div>
      )}
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
      <div className="composer__chips" data-testid="composer-chips" data-previewing={previewing ? "true" : "false"}>
        {previewError && (
          <span className="chip chip--warn" data-testid="chip-preview-error">
            ⚠ {previewError}
          </span>
        )}
        {p.note_only && (
          <span className="chip" data-testid="chip-note-only" title="FR-3.3 규칙 1 — /note 접두 메시지는 트리거 없음">
            기록만 — 아무도 깨우지 않습니다(규칙 1)
          </span>
        )}
        {p.implicit_routing_suppressed && !p.note_only && (
          <span className="chip" data-testid="chip-no-trigger" title="FR-3.3 규칙 3 — @all·사람만 멘션이면 에이전트 트리거 없음">
            트리거 없음 — @all·사람만 멘션(규칙 3)
          </span>
        )}
        {p.warnings.map((w, i) => (
          <span key={`w${i}`} className="chip chip--warn" data-testid="chip-warning" data-code={w.code}>
            ⚠ {w.message}
          </span>
        ))}
        {p.triggers.map((t) => (
          <span key={t.agent_id} className="chip chip--trigger" data-testid="chip-trigger" data-rule={t.rule} data-agent-id={t.agent_id}>
            @{t.agent_name}를 트리거합니다
            <span className="chip__sub">
              {" · "}
              {RULE_NOTE[t.rule] ?? `규칙 ${t.rule}`}
              {t.profile?.model ? ` · ${t.profile.model}` : ""}
              {t.will_queue ? " · 실행 중 → 현재 턴 종료 후 처리됩니다" : ""}
              {t.lane.reentry ? " · 재진입" : ""}
              {t.lane.lane_id === null ? " · 새 lane" : ""}
              {t.deferred_until ? " · 5분 뒤 폴백" : ""}
            </span>
            <button
              type="button"
              className="chip__x"
              aria-label={`@${t.agent_name} 트리거 억제`}
              title="이번 메시지에서만 깨우지 않음(FR-3.6). 멘션은 본문에 남습니다"
              onClick={() => suppress(t.agent_id, t.agent_name)}
            >
              ✕
            </button>
          </span>
        ))}
        {[...suppressed.entries()].map(([id, name]) => (
          <span key={id} className="chip" data-testid="chip-suppressed" data-agent-id={id}>
            @{name} 트리거 억제됨
            <button type="button" className="chip__x" aria-label={`@${name} 트리거 복원`} onClick={() => unsuppress(id)}>
              ↺
            </button>
          </span>
        ))}
        {serverWarnings.map((w, i) => (
          <span key={`s${i}`} className="chip chip--warn" data-testid="chip-server-warning">
            ⚠ {w.message}
          </span>
        ))}
      </div>
      <div className="composer__foot">
        <label className="composer__toggle" data-testid="new-lane-toggle-label">
          <input
            type="checkbox"
            checked={newLane}
            disabled={props.disabled}
            onChange={(e) => setNewLane(e.target.checked)}
            data-testid="new-lane-toggle"
            aria-label="새 lane으로 보내기"
          />
          <span>새 lane으로 보내기</span>
        </label>
        {newLane && (
          <span className="composer__lane-note" data-testid="new-lane-note">
            새 lane으로 전송됨 — 전송하면 해제됩니다
          </span>
        )}
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
