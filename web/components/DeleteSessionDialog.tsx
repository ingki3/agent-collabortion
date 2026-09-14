"use client";
/**
 * 세션 삭제 확인 다이얼로그(SCREEN §4.3 「카드 옵션(…) — 삭제」 · §5 확인 다이얼로그 · FR-2.7, T-W13). S17(RebindDialog)처럼 컴포넌트다.
 *
 * §5 규칙 — **무엇이 사라지는지 명시하고, 되돌릴 수 없으면 그렇다고 쓴다**: 제목에 세션 이름, 본문에 메시지·작업 줄기·아티팩트·
 * 비용 기록(그리고 이 컴퓨터의 작업 폴더). 「삭제」 는 위험 색, 「취소」 가 기본 초점이다(파괴적 동작의 안전한 기본값).
 *
 * 서버가 거절하면 다이얼로그 **안에서** 말한다:
 *   - `409 workdir_unmerged` — `Problem.workdirs[]`(계약 Problem 확장 칸) 를 경로·브랜치·사유로 나열하고 「작업 폴더 관리」(S13) 링크.
 *     서버 문장(`detail`)도 그대로 둔다 — 화면이 다른 말로 바꾸지 않는다.
 *   - `409 session_active`·`403` 등 그 외 — 서버 문장 그대로(`errorMessage`).
 *   - `404` — 이미 없는 세션이다(계약: 두 번째 호출은 404). 목록에서 빠져야 하므로 삭제 성공과 같이 다룬다.
 * 성공(204)은 호출부가 카드를 즉시 뺀다 — SSE `session.deleted` 가 또 와도 같은 결과(멱등).
 */
import { useEffect, useId, useRef, useState } from "react";
import Link from "next/link";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { DELETE_DIALOG, workdirBlockLabel } from "@/lib/wording";
import type { Workdir } from "@/lib/api/types";
import "./delete-session-dialog.css";

export interface DeleteSessionDialogProps {
  session: { id: string; title: string; runtime_id?: string | null };
  /** 204(또는 404 — 이미 없음) 뒤. 호출부가 카드를 뺀다. */
  onDeleted: (sessionId: string) => void;
  onClose: () => void;
}

/** 「작업 폴더 관리」 가 가는 곳 — 세션의 컴퓨터가 있으면 그 S13, 없으면 컴퓨터 목록. */
export function workdirsHref(runtimeId: string | null | undefined): string {
  return runtimeId ? `/runtimes/${runtimeId}/workdirs` : "/runtimes";
}

export function DeleteSessionDialog({ session, onDeleted, onClose }: DeleteSessionDialogProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [workdirs, setWorkdirs] = useState<Workdir[] | null>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const titleId = `${useId()}-title`;
  const bodyId = `${useId()}-body`;

  useEffect(() => {
    cancelRef.current?.focus();
  }, []);

  async function confirm() {
    setBusy(true);
    setError(null);
    setWorkdirs(null);
    try {
      await api.delete("/sessions/{sessionId}", { path: { sessionId: session.id } });
      onDeleted(session.id);
      onClose();
    } catch (e) {
      if (isApiError(e) && e.status === 404) {
        onDeleted(session.id);
        onClose();
        return;
      }
      if (isApiError(e) && e.code === "workdir_unmerged") {
        const list = (e.problem as { workdirs?: Workdir[] }).workdirs;
        setWorkdirs(Array.isArray(list) ? list : []);
      }
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="del-session__scrim"
      role="presentation"
      onClick={(e) => e.target === e.currentTarget && !busy && onClose()}
      onKeyDown={(e) => e.key === "Escape" && !busy && onClose()}
    >
      <div className="del-session" role="alertdialog" aria-modal="true" aria-labelledby={titleId} aria-describedby={bodyId} data-testid="delete-session-dialog">
        <h2 className="del-session__title" id={titleId} data-testid="delete-session-title">{DELETE_DIALOG.title(session.title)}</h2>
        <div className="del-session__body" id={bodyId}>
          <p className="del-session__p">{DELETE_DIALOG.loses}</p>
          <p className="del-session__p del-session__p--warn">{DELETE_DIALOG.irreversible}</p>
        </div>
        {error && (
          <p className="problem del-session__problem" role="alert" data-testid="delete-session-error">{error}</p>
        )}
        {workdirs && (
          <div className="del-session__workdirs" data-testid="delete-session-workdirs">
            <p className="del-session__p">{DELETE_DIALOG.workdirs_head}</p>
            <ul className="del-session__list">
              {workdirs.map((w) => (
                <li key={w.id} data-testid="delete-session-workdir">
                  <code className="del-session__path">{w.path_or_ref}</code>
                  {w.branch && <span className="del-session__branch"> · {w.branch}</span>}
                  <span className="del-session__why"> · {workdirBlockLabel(w)}</span>
                </li>
              ))}
            </ul>
            <Link href={workdirsHref(session.runtime_id)} className="btn btn--sm" data-testid="delete-session-workdirs-link">
              {DELETE_DIALOG.workdirs_link}
            </Link>
          </div>
        )}
        <div className="del-session__actions">
          <button ref={cancelRef} type="button" className="btn" disabled={busy} onClick={onClose} data-testid="delete-session-cancel">
            {DELETE_DIALOG.cancel}
          </button>
          <button
            type="button"
            className="btn del-session__danger"
            // 미병합 작업 폴더가 나열된 뒤에는 정리 전까지 같은 요청이 같은 409 다 — 버튼은 남기되 다음 행동(링크)이 앞이다.
            disabled={busy}
            onClick={() => void confirm()}
            data-testid="delete-session-confirm"
          >
            {busy ? DELETE_DIALOG.busy : DELETE_DIALOG.confirm}
          </button>
        </div>
      </div>
    </div>
  );
}

export default DeleteSessionDialog;
