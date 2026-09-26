"use client";
/**
 * 미션 닫기 확인(SCREEN §4.16 `[FOLDERS]` · PRD FR-6.4 · Director 판정 D8 B).
 *
 * 미션을 닫아도 작업 폴더는 **바로 지우지 않는다** — 마지막 사용 뒤 `workdir_retention_days` 가 지나면 서버가 정리한다.
 * 그래서 이 다이얼로그가 그 사실과 기한을 한 줄로 말한다: 「이 미션의 작업 폴더 N개(〈용량〉)는 〈retention〉일 뒤
 * 정리됩니다 — 남길 것은 아티팩트로 제출하세요」. 폴더 수·용량은 `listRuntimeWorkdirs?work_id=`(openapi v0.3.4)가,
 * 기한은 워크스페이스 설정이 준다. 폴더가 0개(아직 dispatch 전 · 방에 컴퓨터가 없다)면 줄을 그리지 않는다.
 */
import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { ConfirmDialog } from "./ConfirmDialog";
import { FOLDERS_WORDING } from "@/lib/workdir-tree";

export const CLOSE_WORK_DIALOG = {
  title: "미션을 종료할까요?",
  body: "종료 조건과 무관하게 이 미션을 끝냅니다. 대기 중인 할 일은 취소됩니다.",
  confirm: "종료",
  busy: "종료하는 중…",
  cancel: "취소",
} as const;

export interface CloseWorkDialogProps {
  workId: string;
  runtimeId: string | null;
  workspaceId: string | null;
  busy: boolean;
  error?: string | null;
  onConfirm: () => void;
  onClose: () => void;
}

export function CloseWorkDialog({ workId, runtimeId, workspaceId, busy, error, onConfirm, onClose }: CloseWorkDialogProps) {
  const [folders, setFolders] = useState<{ count: number; bytes: number } | null>(null);
  const [retention, setRetention] = useState<number>(14);

  useEffect(() => {
    let alive = true;
    if (runtimeId) {
      api.get("/runtimes/{runtimeId}/workdirs", { path: { runtimeId }, query: { work_id: workId } })
        .then((page) => alive && setFolders({ count: (page.items ?? []).length, bytes: page.disk_bytes_total ?? 0 }))
        .catch(() => alive && setFolders(null));
    }
    if (workspaceId) {
      api.get("/workspaces/{workspaceId}/settings", { path: { workspaceId } })
        .then((s) => alive && typeof s.workdir_retention_days === "number" && setRetention(s.workdir_retention_days))
        .catch(() => undefined);
    }
    return () => {
      alive = false;
    };
  }, [workId, runtimeId, workspaceId]);

  return (
    <ConfirmDialog
      title={CLOSE_WORK_DIALOG.title}
      confirmLabel={CLOSE_WORK_DIALOG.confirm}
      busyLabel={CLOSE_WORK_DIALOG.busy}
      cancelLabel={CLOSE_WORK_DIALOG.cancel}
      busy={busy}
      error={error}
      onConfirm={onConfirm}
      onClose={onClose}
      testId="close-work-dialog"
    >
      <p className="confirm-dlg__p">{CLOSE_WORK_DIALOG.body}</p>
      {folders && folders.count > 0 && (
        <p className="confirm-dlg__p" data-testid="close-work-folders">
          {FOLDERS_WORDING.closeLine(folders.count, folders.bytes, retention)}
        </p>
      )}
    </ConfirmDialog>
  );
}
