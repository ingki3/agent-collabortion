"use client";
/**
 * S5 방 카드의 두 확인(SCREEN §4.3) — 보관 · 삭제. 껍데기는 `ConfirmDialog`, 문장은 `ARCHIVE_DIALOG`·`DELETE_ROOM_DIALOG`(lib/wording.ts).
 *
 * 보관은 **되돌릴 수 있다고** 말하고(FR-2.4 [V19-C]), 삭제는 사라지는 것을 나열하고 되돌릴 수 없다고 말한다(FR-2.6). 서버 거절은
 * 다이얼로그 안에서 서버 문장 그대로 — 보관 `409 tasks_active`, 삭제 `409 works_active`·`409 workdir_unmerged`(폴더 목록 + S13 링크)·`403`.
 * 삭제의 `404` 는 이미 없는 방이다 — 목록에서 빠져야 하므로 성공과 같이 다룬다(세션 삭제와 같은 규칙).
 */
import { useState } from "react";
import Link from "next/link";
import { ConfirmDialog } from "./ConfirmDialog";
import "./delete-session-dialog.css";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { ARCHIVE_DIALOG, DELETE_ROOM_DIALOG, workdirBlockLabel } from "@/lib/wording";
import type { Room, Workdir } from "@/lib/api/types";

export function ArchiveRoomDialog({ room, onArchived, onClose }: { room: { id: string; name: string }; onArchived: (r: Room) => void; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      onArchived(await api.post("/rooms/{roomId}/archive", { path: { roomId: room.id } }));
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <ConfirmDialog
      title={ARCHIVE_DIALOG.title(room.name)}
      confirmLabel={ARCHIVE_DIALOG.confirm}
      busyLabel={ARCHIVE_DIALOG.busy}
      cancelLabel={ARCHIVE_DIALOG.cancel}
      busy={busy}
      error={error}
      onConfirm={() => void confirm()}
      onClose={onClose}
      testId="archive-room-dialog"
    >
      <p className="confirm-dlg__p">{ARCHIVE_DIALOG.body}</p>
    </ConfirmDialog>
  );
}

export function DeleteRoomDialog({ room, onDeleted, onClose }: { room: { id: string; name: string }; onDeleted: (id: string) => void; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [workdirs, setWorkdirs] = useState<Workdir[] | null>(null);
  async function confirm() {
    setBusy(true);
    setError(null);
    setWorkdirs(null);
    try {
      await api.delete("/rooms/{roomId}", { path: { roomId: room.id } });
      onDeleted(room.id);
      onClose();
    } catch (e) {
      if (isApiError(e) && e.status === 404) {
        onDeleted(room.id);
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
    <ConfirmDialog
      title={DELETE_ROOM_DIALOG.title(room.name)}
      confirmLabel={DELETE_ROOM_DIALOG.confirm}
      busyLabel={DELETE_ROOM_DIALOG.busy}
      cancelLabel={DELETE_ROOM_DIALOG.cancel}
      busy={busy}
      danger
      error={error}
      extra={
        workdirs && (
          <div className="del-session__workdirs" data-testid="delete-room-workdirs">
            <p className="confirm-dlg__p">{DELETE_ROOM_DIALOG.workdirs_head}</p>
            <ul className="del-session__list">
              {workdirs.map((w) => (
                <li key={w.id} data-testid="delete-room-workdir">
                  <code className="del-session__path">{w.path_or_ref}</code>
                  {w.branch && <span className="del-session__branch"> · {w.branch}</span>}
                  <span className="del-session__why"> · {workdirBlockLabel(w)}</span>
                </li>
              ))}
            </ul>
            {/* 목록 카드는 방의 컴퓨터를 모른다 — 컴퓨터 목록(S11)에서 그 컴퓨터의 작업 폴더 관리(S13)로 간다. */}
            <Link href="/runtimes" className="btn btn--sm" data-testid="delete-room-workdirs-link">
              {DELETE_ROOM_DIALOG.workdirs_link}
            </Link>
          </div>
        )
      }
      onConfirm={() => void confirm()}
      onClose={onClose}
      testId="delete-room-dialog"
    >
      <p className="confirm-dlg__p">{DELETE_ROOM_DIALOG.loses}</p>
      <p className="confirm-dlg__p">{DELETE_ROOM_DIALOG.workdirs}</p>
      <p className="confirm-dlg__p confirm-dlg__p--warn">{DELETE_ROOM_DIALOG.irreversible}</p>
    </ConfirmDialog>
  );
}
