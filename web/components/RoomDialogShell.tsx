"use client";
/**
 * 방 다이얼로그(S19 · S24 · S21 · S26)의 껍데기와 실시간 훅(T-R2-W3).
 *
 * 껍데기는 `ConfirmDialog` 의 scrim·틀(`confirm-dlg`)을 그대로 쓰되 폭이 넓고 `role="dialog"` 다 — 파괴적 확인(alertdialog)이 아니라 편집 화면이다.
 * Esc·바깥 클릭·「닫기」로 닫힌다(진행 중에는 닫히지 않는다). 첫 초점은 제목 — 긴 목록의 첫 버튼에 초점이 가면 스크린리더가 제목을 건너뛴다.
 */
import { useEffect, useId, useRef } from "react";
import type { StreamEvent } from "@/lib/api/types";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import { COMMON } from "@/lib/room-dialogs";
import "./confirm-dialog.css";
import "./room-dialogs.css";

export interface RoomDialogShellProps {
  title: string;
  /** 제목 바로 아래 회색 한 줄(S21 미션 정의 등). */
  sub?: React.ReactNode;
  testId: string;
  busy?: boolean;
  onClose: () => void;
  children: React.ReactNode;
}

export function RoomDialogShell({ title, sub, testId, busy = false, onClose, children }: RoomDialogShellProps) {
  const titleId = `${useId()}-title`;
  const titleRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    titleRef.current?.focus();
  }, []);
  return (
    <div className="confirm-dlg__scrim" role="presentation" onClick={(e) => e.target === e.currentTarget && !busy && onClose()} onKeyDown={(e) => e.key === "Escape" && !busy && onClose()}>
      <div className="confirm-dlg rd-dlg" role="dialog" aria-modal="true" aria-labelledby={titleId} data-testid={testId}>
        <div className="rd-dlg__head">
          <div>
            <h2 className="confirm-dlg__title" id={titleId} ref={titleRef} tabIndex={-1} data-testid={`${testId}-title`}>{title}</h2>
            {sub && <p className="rd-dlg__sub" data-testid={`${testId}-sub`}>{sub}</p>}
          </div>
          <button type="button" className="btn btn--sm btn--ghost" disabled={busy} onClick={onClose} data-testid={`${testId}-close`}>
            {COMMON.close}
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

/** 그 방의 프레임이면 `reload` — 이벤트 이름은 호출부가 고른다(S19 참여자 · S24 링크 · S23 읽기 · S26 제안 · S20 방). */
export function useRoomEvents(workspaceId: string | null | undefined, roomId: string, types: readonly string[], reload: (ev: StreamEvent) => void) {
  const key = types.join(",");
  useWorkspaceStream(workspaceId, (ev) => {
    if (!key.split(",").includes(ev.type)) return;
    const p = (ev.payload ?? {}) as { room_id?: string; id?: string };
    const rid = ev.room_id ?? p.room_id ?? p.id;
    if (rid && rid !== roomId) return;
    reload(ev);
  });
}

export default RoomDialogShell;
