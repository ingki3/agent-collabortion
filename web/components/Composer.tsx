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
import "./media-preview.css";
import { activeMentionQuery, toWire, type MentionTarget } from "@/lib/mentions";
import { MEDIA, WORK_SELECTOR } from "@/lib/wording";
import type { TriggerPreview } from "@/lib/api/types";
import { Slot, slotText } from "@/components/Slot";
import { attachmentsOnlyContent, formatBytes, kindGlyph, mediaKind, MAX_ATTACHMENT_BYTES, MAX_ATTACHMENTS, type UploadResult } from "@/lib/media";

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
  /**
   * v0.19 — 작성창 미션 선택기의 값(FR-3.1.1 규칙 1). `undefined` 면 키를 보내지 않는다(옛 S7). `null` 은 「미션 없음」을 골랐다는 뜻이
   * 아니라 **규칙 2~4 로 정해 달라**는 것이다(서버 router.attribute — 스레드·실행 중 서브 미션이 자동으로 귀속시킬 수 있다).
   */
  workId?: string | null;
  /** v0.19.12 파일 붙이기(PRD FR-3.7) — 다 올라간 첨부의 아티팩트 id, 고른 순서. 없으면 빈 배열. */
  attachmentIds: string[];
}

/** 작성창 첨부 칩 하나(COMPONENTS §9.12 Attachment Chip). */
export interface ComposerAttachment {
  key: string;
  file: File;
  state: "uploading" | "done" | "error" | "too_big";
  progress: number;
  error?: string;
  artifactId?: string;
  /** 서버 판정 종류(올라간 뒤) — 올라가기 전엔 브라우저가 준 file.type 으로 글리프만 고른다. */
  contentType?: string | null;
  /** 이미지 칩 썸네일(Object URL) — 칩이 빠지면 해제한다. */
  thumb?: string;
}

/** v0.19 방 화면(T-R2-W2) — 미션 선택기(COMPONENTS §9.2). 상태 셋: 열림 · 잠김(스레드) · 자동(규칙 3). 판정은 서버 미리보기가 한다. */
export interface ComposerWorkSelector {
  /** 고를 수 있는 미션(열린 미션). */
  options: { id: string; title: string }[];
  /** 지금 값 — 기본은 고른 칩(미션이면 그 미션, `(전체)`·`(미션 없음)` 이면 null). */
  value: string | null;
  onChange: (id: string | null) => void;
}

export interface ComposerProps {
  agents: ComposerAgent[];
  members?: ComposerMember[];
  replyTo?: { id: string; authorName: string } | null;
  onCancelReply?: () => void;
  /** 서버 트리거 미리보기(`POST /rooms/{roomId}/messages/preview`). 없으면 칩을 그리지 않는다. */
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
  /** v0.19 — 미션 선택기 + 귀속 칩. 없으면 그리지 않는다(옛 S7). */
  workSelector?: ComposerWorkSelector;
  /** textarea 에 붙일 ref — 「작성창으로 건너뛰기」·빈 방 「그냥 말 걸기」가 초점을 준다. */
  inputRef?: React.Ref<HTMLTextAreaElement>;
  /**
   * v0.19.12 파일 붙이기(PRD FR-3.7 · SCREEN §4.6) — 파일 하나를 사람 아티팩트(type `attachment`)로 올린다. 없으면 📎·끌어다 놓기·붙여넣기를
   * 그리지 않는다(재지시·테스트 채팅처럼 첨부를 받지 않는 작성창). 권한은 게시와 같다 — `disabled` 면 📎 도 비활성.
   */
  onUpload?: (file: File, onProgress: (ratio: number) => void) => Promise<UploadResult>;
}

/** 규칙 번호 → 사람 문구. 미리보기 칩의 근거를 숨기지 않는다. */
export const RULE_NOTE: Record<number, string> = {
  1: "기록만(규칙 1)",
  2: "명시 멘션(규칙 2)",
  3: "트리거 없음(규칙 3)",
  4: "스레드 답글(규칙 4)",
  5: "질문 답글(규칙 5)",
  6: "담당 에이전트 기본(규칙 6)",
  7: "담당 에이전트 폴백(규칙 7)",
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
  const fileRef = useRef<HTMLInputElement>(null);
  const [attachments, setAttachments] = useState<ComposerAttachment[]>([]);
  const [dragging, setDragging] = useState(false);
  const [limitNote, setLimitNote] = useState(false);
  const { onUpload } = props;

  const parentId = props.replyTo?.id ?? null;
  const suppressKey = [...suppressed.keys()].sort().join(",");
  const suppressIds = useMemo(() => (suppressKey ? suppressKey.split(",") : []), [suppressKey]);
  const { onPreview } = props;
  const delay = props.previewDelayMs ?? 250;
  const workId = props.workSelector ? props.workSelector.value : undefined;

  // 외부 초안(lane "중단하고 다시 지시") — nonce 가 바뀔 때만 덮어쓴다.
  const draftNonce = props.draft?.nonce;
  const draftContent = props.draft?.content;
  useEffect(() => {
    if (draftNonce === undefined) return;
    setText(draftContent ?? "");
    taRef.current?.focus();
  }, [draftNonce, draftContent]);

  // 화면 글 → 전송 본문(멘션 링크). 미리보기와 전송이 같은 것을 본다.
  const mentionTargets = useMemo<MentionTarget[]>(() => [
    ...props.agents.map((a) => ({ kind: "agent" as const, id: a.id, name: a.name })),
    ...(props.members ?? []).map((m) => ({ kind: "user" as const, id: m.id, name: m.name })),
  ], [props.agents, props.members]);
  const wire = useMemo(() => toWire(text, mentionTargets), [text, mentionTargets]);
  // ── 파일 붙이기(FR-3.7) ──
  const doneIds = attachments.filter((a) => a.state === "done" && a.artifactId).map((a) => a.artifactId!);
  const doneKey = doneIds.join(",");
  /** 다 올라가기 전에는 보내기 비활성(SCREEN §4.6) — 실패·50MB 초과 칩도 「올라가지 않은」 칩이다(✕ 로 빼거나 다시 시도). */
  const pending = attachments.some((a) => a.state !== "done");
  const uploading = attachments.some((a) => a.state === "uploading");
  /** 본문 없이 첨부만 보내면 「(파일 N개)」(SCREEN §4.6 「첨부만」). 미리보기와 전송이 같은 글을 본다. */
  const effective = wire.trim() || (doneIds.length > 0 ? attachmentsOnlyContent(doneIds.length) : "");

  // ── 서버 트리거 미리보기(FR-3.6) — 디바운스, 마지막 응답만 채택 ──
  useEffect(() => {
    if (!onPreview) return;
    const content = effective;
    if (!content) {
      setPreview(null);
      setPreviewError(null);
      return;
    }
    let live = true;
    setPreviewing(true);
    const t = setTimeout(() => {
      onPreview({ content, parentId, newLane, suppressAgentIds: suppressIds, workId, attachmentIds: doneKey ? doneKey.split(",") : [] })
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
  }, [effective, parentId, newLane, suppressIds, onPreview, delay, workId, doneKey]);

  const startUpload = useCallback((a: ComposerAttachment) => {
    if (!onUpload) return;
    const patch = (p: Partial<ComposerAttachment>) => setAttachments((cur) => cur.map((x) => (x.key === a.key ? { ...x, ...p } : x)));
    onUpload(a.file, (r) => patch({ progress: r }))
      .then((res) => patch({ state: "done", progress: 1, artifactId: res.id, contentType: res.content_type }))
      .catch((e) => patch({ state: "error", error: e instanceof Error ? e.message : String(e) }));
  }, [onUpload]);

  /** 고르는 즉시 올린다(진행 막대). 10개를 넘는 것은 받지 않고 한 줄 알린다. 50MB 넘는 칩은 빨간 줄 — 올리지 않는다. */
  const addFiles = useCallback((files: FileList | File[] | null) => {
    if (!files || !onUpload || props.disabled) return;
    const list = Array.from(files);
    if (list.length === 0) return;
    setAttachments((cur) => {
      const room = MAX_ATTACHMENTS - cur.length;
      setLimitNote(list.length > room);
      const next = list.slice(0, Math.max(0, room)).map((file, i): ComposerAttachment => ({
        key: `${Date.now()}-${cur.length + i}-${file.name}`,
        file,
        state: file.size > MAX_ATTACHMENT_BYTES ? "too_big" : "uploading",
        progress: 0,
        thumb: file.type.startsWith("image/") && typeof URL.createObjectURL === "function" ? URL.createObjectURL(file) : undefined,
      }));
      queueMicrotask(() => next.filter((a) => a.state === "uploading").forEach(startUpload));
      return [...cur, ...next];
    });
  }, [onUpload, props.disabled, startUpload]);

  const removeAttachment = useCallback((key: string) => {
    setAttachments((cur) => {
      const gone = cur.find((a) => a.key === key);
      if (gone?.thumb) URL.revokeObjectURL(gone.thumb);
      return cur.filter((a) => a.key !== key);
    });
    setLimitNote(false);
  }, []);

  const retryAttachment = useCallback((key: string) => {
    setAttachments((cur) => cur.map((a) => (a.key === key ? { ...a, state: "uploading", progress: 0, error: undefined } : a)));
    const a = attachments.find((x) => x.key === key);
    if (a) startUpload({ ...a, state: "uploading", progress: 0 });
  }, [attachments, startUpload]);

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
    const link = `@${t.name} `; // 화면에는 이름만 — 링크는 toWire() 가 전송 직전에 만든다(W-15)
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
    const content = effective;
    if (!content || busy || props.disabled || pending) return;
    setBusy(true);
    try {
      const warnings = await props.onSubmit({ content, parentId, newLane, suppressAgentIds: suppressIds, workId, attachmentIds: doneIds });
      setServerWarnings(warnings ?? []);
      setText("");
      attachments.forEach((a) => a.thumb && URL.revokeObjectURL(a.thumb));
      setAttachments([]);
      setLimitNote(false);
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
  const ws = props.workSelector;
  // 귀속 칩(FR-3.1.1) — 서버 미리보기의 `work`·`work_source` 를 그대로 말한다. 미리보기 전에는 선택기 값으로.
  const source = preview?.work_source ?? (ws?.value ? "chosen" : "none");
  const attributed = preview ? (preview.work ?? null) : ws?.value ? { id: ws.value, title: ws.options.find((o) => o.id === ws.value)?.title ?? "" } : null;
  const locked = source === "thread" && !!attributed;
  const auto = source === "running_lane" && !!attributed;
  const shown = locked || auto ? attributed!.id : (ws?.value ?? "");
  const lockedHintId = "work-selector-locked";

  const canAttach = !!onUpload;
  const dropProps = canAttach && !props.disabled ? {
    onDragOver: (e: React.DragEvent) => {
      if (![...e.dataTransfer.types].includes("Files")) return;
      e.preventDefault();
      setDragging(true);
    },
    onDragLeave: (e: React.DragEvent) => {
      if (e.currentTarget.contains(e.relatedTarget as Node | null)) return;
      setDragging(false);
    },
    onDrop: (e: React.DragEvent) => {
      if (!e.dataTransfer.files?.length) return;
      e.preventDefault();
      setDragging(false);
      addFiles(e.dataTransfer.files);
    },
  } : {};
  const filesChip = doneIds.length > 0 ? slotText(MEDIA.trigger_files, doneIds.length) : "";

  return (
    <div className="composer" data-testid="composer" data-dragging={dragging ? "true" : undefined} {...dropProps}>
      {dragging && <div className="composer__drop" data-testid="composer-drop" aria-hidden="true">{MEDIA.drop_here}</div>}
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
        ref={(el) => {
          taRef.current = el;
          const r = props.inputRef;
          if (typeof r === "function") r(el);
          else if (r) (r as React.MutableRefObject<HTMLTextAreaElement | null>).current = el;
        }}
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
        onPaste={canAttach ? (e) => {
          const files = e.clipboardData?.files;
          if (files && files.length > 0) {
            e.preventDefault();
            addFiles(files);
          }
        } : undefined}
      />
      {attachments.length > 0 && (
        <ul className="attach-chips" data-testid="attach-chips" aria-label={MEDIA.attachments_label}>
          {attachments.map((a) => {
            const kind = mediaKind(a.contentType ?? a.file.type);
            const isImage = !!a.thumb && a.state !== "too_big";
            return (
              <li key={a.key} className={`achip${isImage ? " achip--image" : ""}`} data-state={a.state === "too_big" ? "error" : a.state} data-testid="attach-chip" data-artifact-id={a.artifactId}>
                {isImage ? (
                  // eslint-disable-next-line @next/next/no-img-element -- 로컬 Object URL 썸네일
                  <img className="achip__thumb" src={a.thumb} alt="" />
                ) : (
                  <span className="achip__glyph" aria-hidden="true">{kindGlyph(kind)}</span>
                )}
                <span className="achip__text">
                  <span className="achip__name" title={a.file.name}>{a.file.name || "pasted.png"}</span>
                  {a.state === "too_big" ? (
                    <span className="achip__err" data-testid="attach-too-big">{MEDIA.too_big}</span>
                  ) : a.state === "error" ? (
                    <>
                      <span className="achip__err" data-testid="attach-error" title={a.error}>{a.error}</span>
                      <button type="button" className="achip__retry" onClick={() => retryAttachment(a.key)} data-testid="attach-retry">{MEDIA.retry}</button>
                    </>
                  ) : (
                    <span className="achip__size">
                      {formatBytes(a.file.size)}
                      {a.state === "uploading" && <> · {MEDIA.uploading}</>}
                    </span>
                  )}
                </span>
                {a.state === "uploading" && (
                  <span className="achip__bar" role="progressbar" aria-label={`${a.file.name} ${MEDIA.uploading}`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(a.progress * 100)} style={{ width: `${Math.round(a.progress * 100)}%` }} data-testid="attach-progress" />
                )}
                <button type="button" className="achip__x" aria-label={MEDIA.remove(a.file.name || "pasted.png")} onClick={() => removeAttachment(a.key)} data-testid="attach-remove">✕</button>
              </li>
            );
          })}
        </ul>
      )}
      {limitNote && (
        <div className="composer__notice" role="status" data-testid="attach-limit"><Slot text={MEDIA.too_many} n={MAX_ATTACHMENTS} /></div>
      )}
      {ws && (
        <div className="composer__work" data-testid="work-selector" data-mode={locked ? "locked" : auto ? "auto" : "open"}>
          <select
            className="composer__work-select"
            aria-label={WORK_SELECTOR.label}
            value={shown}
            disabled={props.disabled || locked}
            aria-describedby={locked ? lockedHintId : undefined}
            onChange={(e) => ws.onChange(e.target.value || null)}
            data-testid="work-selector-select"
          >
            <option value="">{WORK_SELECTOR.none}</option>
            {ws.options.map((o) => (
              <option key={o.id} value={o.id}>{o.title}</option>
            ))}
            {attributed && !ws.options.some((o) => o.id === attributed.id) && <option value={attributed.id}>{attributed.title}</option>}
          </select>
          <span className="chip chip--work" id={locked ? lockedHintId : undefined} data-testid="chip-work" data-source={source} data-work-id={attributed?.id ?? undefined}>
            {locked ? (
              WORK_SELECTOR.locked(attributed!.title)
            ) : auto ? (
              <>
                <b data-testid="chip-work-auto">{WORK_SELECTOR.auto_prefix}</b>
                {WORK_SELECTOR.into(attributed!.title)}
                {WORK_SELECTOR.auto_tail}
              </>
            ) : attributed ? (
              WORK_SELECTOR.into(attributed.title)
            ) : (
              WORK_SELECTOR.into_none
            )}
          </span>
        </div>
      )}
      <div className="composer__chips" data-testid="composer-chips" data-previewing={previewing ? "true" : "false"}>
        {previewError && (
          <span className="chip chip--warn" data-testid="chip-preview-error">
            ⚠ {previewError}
          </span>
        )}
        {p.note_only && (
          <span className="chip" data-testid="chip-note-only" title="/note 로 시작하는 메시지는 아무도 깨우지 않습니다(규칙 1)">
            기록만 — 아무도 깨우지 않습니다(규칙 1)
          </span>
        )}
        {p.implicit_routing_suppressed && !p.note_only && (
          <span className="chip" data-testid="chip-no-trigger" title="@all 이나 사람만 멘션하면 에이전트를 깨우지 않습니다(규칙 3)">
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
              {t.lane.lane_id === null ? " · 새 서브 미션" : ""}
              {t.deferred_until ? " · 5분 뒤 폴백" : ""}
              {filesChip && <span data-testid="chip-trigger-files">{filesChip}</span>}
            </span>
            <button
              type="button"
              className="chip__x"
              aria-label={`@${t.agent_name} 트리거 억제`}
              title="이번 메시지에서만 깨우지 않습니다. 멘션은 본문에 남습니다"
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
        {canAttach && (
          <>
            <button
              type="button"
              className="btn btn--sm composer__attach"
              aria-label={MEDIA.attach}
              title={MEDIA.attach}
              disabled={props.disabled || attachments.length >= MAX_ATTACHMENTS}
              onClick={() => fileRef.current?.click()}
              data-testid="composer-attach"
            >
              📎
            </button>
            <input
              ref={fileRef}
              type="file"
              multiple
              hidden
              tabIndex={-1}
              aria-hidden="true"
              data-testid="composer-file-input"
              onChange={(e) => {
                addFiles(e.target.files);
                e.target.value = "";
              }}
            />
          </>
        )}
        <label className="composer__toggle" data-testid="new-lane-toggle-label">
          <input
            type="checkbox"
            checked={newLane}
            disabled={props.disabled}
            onChange={(e) => setNewLane(e.target.checked)}
            data-testid="new-lane-toggle"
            aria-label="새 서브 미션으로 보내기"
          />
          <span>새 서브 미션으로 보내기</span>
        </label>
        {newLane && (
          <span className="composer__lane-note" data-testid="new-lane-note">
            새 서브 미션으로 전송됨 — 전송하면 해제됩니다
          </span>
        )}
        <span className="composer__hint">⌘/Ctrl+Enter 로 전송 · @ 로 멘션</span>
        <button
          type="button"
          className="btn btn--primary btn--sm"
          disabled={disabled || !effective || pending}
          title={props.disabled ? props.disabledReason : uploading ? MEDIA.uploading_block : undefined}
          onClick={() => void submit()}
          data-testid="composer-send"
        >
          {busy ? "전송 중…" : "전송"}
        </button>
        {uploading && !props.disabled && (
          <span className="composer__hint" role="status" data-testid="attach-pending">{MEDIA.uploading_block}</span>
        )}
      </div>
    </div>
  );
}

export default Composer;
