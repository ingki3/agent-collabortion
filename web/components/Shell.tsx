"use client";
/**
 * 앱 셸 — 좌측 App Nav + 상단바 + 실시간 연결 배너. 워크스페이스 단위 SSE **하나**를 `StreamProvider` 로 열고
 * `inbox.summary`(뱃지)와 연결 상태를 받는다. 화면들(S5·S7·S11·S12)은 같은 연결을 구독한다 — 화면당 연결 1개(R4).
 */
import { usePathname } from "next/navigation";
import { useCallback, useState } from "react";
import { AppNav } from "./AppNav";
import { ConnectionBanner } from "./ConnectionBanner";
import { useAuth } from "@/lib/auth/AuthContext";
import { StreamProvider, useStreamState, useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type { StreamEvent } from "@/lib/api/types";

export function Shell({ children, title }: { children: React.ReactNode; title?: string }) {
  const { me, workspace, loading } = useAuth();

  if (loading || !me) {
    return (
      <main className="page muted" data-testid="shell-loading">
        불러오는 중…
      </main>
    );
  }
  if (!workspace) {
    return (
      <main className="page muted" data-testid="shell-no-workspace">
        워크스페이스로 이동 중…
      </main>
    );
  }

  return (
    <StreamProvider workspaceId={workspace.id}>
      <ShellFrame title={title}>{children}</ShellFrame>
    </StreamProvider>
  );
}

function ShellFrame({ children, title }: { children: React.ReactNode; title?: string }) {
  const pathname = usePathname();
  const { me, workspace, canManage, logout, selectWorkspace } = useAuth();
  const [inbox, setInbox] = useState<number | null>(null);

  const onEvent = useCallback((ev: StreamEvent) => {
    if (ev.type === "inbox.summary") {
      const p = ev.payload as { action_required?: number };
      if (typeof p.action_required === "number") setInbox(p.action_required);
    }
  }, []);
  // resync(보존 창 밖): 인박스 요약 REST(getInboxSummary)는 P2 라 다시 읽을 수 없다 → 낡은 수를 지운다(N4).
  const onResync = useCallback(() => setInbox(null), []);
  useWorkspaceStream(workspace?.id, onEvent, { onResync });
  const conn = useStreamState() ?? "connecting";
  if (!me || !workspace) return null;

  return (
    <div className="shell">
      <AppNav
        workspaceName={workspace.name}
        current={pathname}
        inboxCount={inbox}
        showSettings={canManage}
        userName={me.user.display_name}
        onLogout={() => void logout()}
      />
      <div className="shell__main">
        <ConnectionBanner state={conn} />
        <header className="topbar">
          <span className="topbar__title">{title ?? ""}</span>
          {me.workspaces.length > 1 && (
            <select
              className="select"
              style={{ width: "auto" }}
              value={workspace.id}
              onChange={(e) => selectWorkspace(e.target.value)}
              aria-label="워크스페이스 선택"
            >
              {me.workspaces.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </select>
          )}
        </header>
        <div className="content">{children}</div>
      </div>
    </div>
  );
}

export default Shell;
