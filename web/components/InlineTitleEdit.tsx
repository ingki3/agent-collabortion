"use client";
/**
 * Inline Title Edit — 방 이름 그 자리 편집(COMPONENTS §9.9 · SCREEN §4.6 「방 이름 그 자리 편집」 · PRD FR-2.1.2).
 *
 * S7 방 머리와 S5 방 카드(「…」 「이름 바꾸기」)가 **같은 컴포넌트**를 쓴다. S20 「이름·설명」 묶음은 두 칸 + 「저장」 모양이라 이 칸을 그리지 않지만
 * 같은 판정(`roomNameProblem`)·같은 오류 문장(`renameErrorText`)·같은 말(`ROOM_RENAME`)을 여기서 가져간다 — 세 곳이 같은 규칙·같은 문구(FR-2.1.2 「어디서」).
 *
 * - 보기(권한자): 이름 글자 + hover·focus 때 ✎. 글자와 ✎ 둘 다 누를 수 있다. 권한 없으면 ✎ 가 **없다**(비활성 버튼을 두지 않는다 — §4.6).
 * - 편집: 같은 글자 크기의 입력 칸 + 「저장」·「취소」 + 도움말 한 줄. Enter = 저장 · Esc = 취소 · **바깥을 누르면 취소**(말없이 저장하지 않는다).
 * - 저장 중: 입력 칸 흐리게 + 「저장 중…」, 버튼 비활성. 실패하면 칸을 그대로 두고 아래 서버 문장(403 이면 누구의 일인지).
 * - 편집 중에 다른 사람이 바꾸면(`value` 가 바뀌면) 칸을 유지하고 「다른 사람이 이름을 〈새 이름〉(으)로 바꿨습니다」 — 저장하면 내 것이 이긴다.
 * - 바뀌지 않았으면 저장하지 않는다(요청도 없다 — 서버도 같은 판정으로 시스템 메시지를 남기지 않는다).
 */
import { useEffect, useId, useRef, useState } from "react";
import { Slot } from "./Slot";
import { errorMessage, isApiError } from "@/lib/api/client";
import { ROOM_RENAME } from "@/lib/wording";
import "./inline-title-edit.css";

/** 서버 UpdateRoom 과 같은 판정 — 앞뒤 공백을 떼고 1~200자. 문제가 없으면 null. */
export function roomNameProblem(draft: string): string | null {
  const n = draft.trim();
  if (!n) return ROOM_RENAME.required;
  if ([...n].length > 200) return ROOM_RENAME.help;
  return null;
}

/** 저장 실패의 문장 — 403 은 누구의 일인지, 422 는 그 칸의 서버 문장, 나머지는 서버 detail. */
export function renameErrorText(e: unknown, field: "name" | "description" = "name"): string {
  if (isApiError(e)) {
    if (e.status === 403) return ROOM_RENAME.forbidden;
    const f = (e.problem.errors ?? []).find((x) => x.field === field);
    if (f) return f.message;
  }
  return errorMessage(e);
}

export interface InlineTitleEditProps {
  value: string;
  /** 방 설정 권한(`my_capabilities` 의 configure 또는 목록의 방장·부방장·ws owner·admin). false 면 ✎ 가 없다. */
  canEdit: boolean;
  /** 새 이름(앞뒤 공백을 뗀 것)을 저장한다 — 실패는 throw(ApiError). */
  onSave: (name: string) => Promise<unknown>;
  /** 보기 상태의 글자 태그 — S7 머리는 h1. */
  as?: "h1" | "span";
  className?: string;
  /** 제어형 — S5 카드는 「…」 메뉴가 편집을 연다. 주지 않으면 스스로 연다(✎ · 글자). */
  editing?: boolean;
  onEditingChange?: (editing: boolean) => void;
  testId?: string;
}

export function InlineTitleEdit({ value, canEdit, onSave, as: Tag = "span", className, editing: editingProp, onEditingChange, testId = "title-edit" }: InlineTitleEditProps) {
  const [ownEditing, setOwnEditing] = useState(false);
  const editing = canEdit && (editingProp ?? ownEditing);
  const setEditing = (v: boolean) => {
    if (editingProp === undefined) setOwnEditing(v);
    onEditingChange?.(v);
  };
  const [draft, setDraft] = useState(value);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  /** 편집을 시작할 때의 이름 — 편집 중에 `value` 가 이것과 달라지면 다른 사람이 바꾼 것이다. */
  const [base, setBase] = useState(value);
  const savingAs = useRef<string | null>(null);
  const root = useRef<HTMLFormElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const helpId = useId();

  // 편집에 들어갈 때마다 지금 이름에서 시작한다.
  const wasEditing = useRef(false);
  useEffect(() => {
    if (editing && !wasEditing.current) {
      setDraft(value);
      setBase(value);
      setError(null);
      requestAnimationFrame(() => {
        input.current?.focus();
        input.current?.select();
      });
    }
    wasEditing.current = editing;
  }, [editing, value]);

  const problem = roomNameProblem(draft);
  // 내 저장의 room.updated 가 응답보다 먼저 올 수 있다 — 그 값은 「다른 사람」이 아니다.
  const changedElsewhere = editing && value !== base && value !== savingAs.current;

  const close = (refocus: boolean) => {
    setEditing(false);
    setError(null);
    if (refocus) requestAnimationFrame(() => trigger.current?.focus());
  };
  async function save() {
    if (saving || problem) return;
    const next = draft.trim();
    if (next === value) return close(true);
    setSaving(true);
    setError(null);
    savingAs.current = next;
    try {
      await onSave(next);
      close(true);
    } catch (e) {
      setError(renameErrorText(e));
    } finally {
      setSaving(false);
      savingAs.current = null;
    }
  }

  if (!editing) {
    return (
      <span className="title-edit" data-testid={testId}>
        <Tag className={`title-edit__text${canEdit ? " title-edit__text--editable" : ""}${className ? ` ${className}` : ""}`} onClick={canEdit ? () => setEditing(true) : undefined} data-testid={`${testId}-text`}>
          {value}
        </Tag>
        {canEdit && (
          <button ref={trigger} type="button" className="title-edit__pencil" aria-label={ROOM_RENAME.edit} onClick={() => setEditing(true)} data-testid={`${testId}-pencil`}>
            <span aria-hidden="true">✎</span>
          </button>
        )}
      </span>
    );
  }

  const message = error ?? problem ?? null;
  return (
    <form
      ref={root}
      className={`title-edit title-edit--editing${saving ? " title-edit--saving" : ""}${error ? " title-edit--error" : ""}`}
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          e.stopPropagation();
          if (!saving) close(true);
        }
      }}
      onBlur={(e) => {
        // 바깥을 누르면 취소 — 칸 안의 버튼으로 옮겨 가는 것은 바깥이 아니다. 저장 중에는 결과를 기다린다.
        if (saving) return;
        if (e.relatedTarget && root.current?.contains(e.relatedTarget as Node)) return;
        close(false);
      }}
      data-testid={`${testId}-form`}
    >
      <span className="title-edit__row">
        <input
          ref={input}
          className={`title-edit__input${className ? ` ${className}` : ""}`}
          value={draft}
          onChange={(e) => {
            setDraft(e.target.value);
            setError(null);
          }}
          readOnly={saving}
          aria-label={ROOM_RENAME.input_label}
          aria-invalid={!!error || !!problem || undefined}
          aria-describedby={helpId}
          maxLength={400}
          data-testid={`${testId}-input`}
        />
        <button type="submit" className="btn btn--sm btn--primary" disabled={saving || !!problem} aria-describedby={problem ? helpId : undefined} data-testid={`${testId}-save`}>
          {saving ? ROOM_RENAME.saving : ROOM_RENAME.save}
        </button>
        <button type="button" className="btn btn--sm" disabled={saving} onClick={() => close(true)} data-testid={`${testId}-cancel`}>
          {ROOM_RENAME.cancel}
        </button>
      </span>
      <span id={helpId} className={`title-edit__help${message ? " title-edit__help--error" : ""}`} role={error ? "alert" : undefined} data-testid={`${testId}-help`}>
        {message ?? ROOM_RENAME.help}
      </span>
      {changedElsewhere && (
        <span className="title-edit__help" role="status" data-testid={`${testId}-elsewhere`}>
          <Slot text={ROOM_RENAME.changed_elsewhere} n={value} />
        </span>
      )}
    </form>
  );
}

export default InlineTitleEdit;
