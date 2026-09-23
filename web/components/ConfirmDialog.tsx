"use client";
/**
 * 확인 다이얼로그(SCREEN §5 「확인 다이얼로그 — 일반화 권고」, COMPONENTS §9 "일곱 곳으로 늘었으므로 일반화한다") — `DeleteSessionDialog` 의
 * `role="alertdialog"` 패턴을 껍데기만 떼어 낸 것. 방 보관·방 삭제(T-R2-W1)가 먼저 쓰고 「이 방 멈춤」·퇴장·방장 넘기기 등이 뒤따른다.
 *
 * 규칙: 「취소」 가 기본 초점(파괴적 동작의 안전한 기본값) · Esc·바깥 클릭으로 닫힘(진행 중에는 닫히지 않는다) · 서버가 거절하면
 * 다이얼로그 **안에서** 서버 문장 그대로(`error`) · 위험한 동작은 `danger`(위험 색).
 */
import { useEffect, useId, useRef } from "react";
import "./confirm-dialog.css";

export interface ConfirmDialogProps {
  title: string;
  /** 본문 — 무엇이 바뀌고 무엇이 남는지(§5). */
  children: React.ReactNode;
  confirmLabel: string;
  busyLabel: string;
  cancelLabel: string;
  busy: boolean;
  danger?: boolean;
  /** 서버 거절 문장. */
  error?: string | null;
  /** 오류 아래에 더 그릴 것(409 workdir_unmerged 의 폴더 목록 등). */
  extra?: React.ReactNode;
  onConfirm: () => void;
  onClose: () => void;
  testId: string;
}

export function ConfirmDialog({ title, children, confirmLabel, busyLabel, cancelLabel, busy, danger, error, extra, onConfirm, onClose, testId }: ConfirmDialogProps) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  const titleId = `${useId()}-title`;
  const bodyId = `${useId()}-body`;
  useEffect(() => {
    cancelRef.current?.focus();
  }, []);
  return (
    <div
      className="confirm-dlg__scrim"
      role="presentation"
      onClick={(e) => e.target === e.currentTarget && !busy && onClose()}
      onKeyDown={(e) => e.key === "Escape" && !busy && onClose()}
    >
      <div className="confirm-dlg" role="alertdialog" aria-modal="true" aria-labelledby={titleId} aria-describedby={bodyId} data-testid={testId}>
        <h2 className="confirm-dlg__title" id={titleId} data-testid={`${testId}-title`}>{title}</h2>
        <div className="confirm-dlg__body" id={bodyId}>{children}</div>
        {error && <p className="problem confirm-dlg__problem" role="alert" data-testid={`${testId}-error`}>{error}</p>}
        {extra}
        <div className="confirm-dlg__actions">
          <button ref={cancelRef} type="button" className="btn" disabled={busy} onClick={onClose} data-testid={`${testId}-cancel`}>
            {cancelLabel}
          </button>
          <button
            type="button"
            className={danger ? "btn confirm-dlg__danger" : "btn btn--primary"}
            disabled={busy}
            onClick={onConfirm}
            data-testid={`${testId}-confirm`}
          >
            {busy ? busyLabel : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

export default ConfirmDialog;
