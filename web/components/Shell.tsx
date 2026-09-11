"use client";
/**
 * 앱 셸 — 좌측 App Nav + 실시간 연결 배너. 상단 바는 없다(§8.5): 제목은 각 화면의 `PageHead` 가 쓰고,
 * 워크스페이스 전환은 내비 위의 이름 자리가 맡는다 — 그 둘을 빼면 상단 바에 남는 것이 없어 47px 을 비워 두고 있었다. 워크스페이스 단위 SSE **하나**를 `StreamProvider` 로 열고
 * `inbox.summary`(뱃지)와 연결 상태를 받는다. 화면들(S5·S7·S11·S12)은 같은 연결을 구독한다 — 화면당 연결 1개(R4).
 */
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { AppNav } from "./AppNav";
import { ConnectionBanner } from "./ConnectionBanner";
import { useAuth } from "@/lib/auth/AuthContext";
import { StreamProvider, useStreamState, useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type { StreamEvent } from "@/lib/api/types";

export function Shell({ children }: { children: React.ReactNode }) {
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
      <ShellFrame>{children}</ShellFrame>
    </StreamProvider>
  );
}

function ShellFrame({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { me, workspace, canManage, logout, selectWorkspace } = useAuth();
  const [inbox, setInbox] = useState<number | null>(null);

  const onEvent = useCallback((ev: StreamEvent) => {
    if (ev.type === "inbox.summary") {
      const p = ev.payload as { action_required?: number };
      if (typeof p.action_required === "number") setInbox(p.action_required);
    }
  }, []);
  /**
   * 첫 진입과 resync(보존 창 밖) 때는 REST 로 읽는다 — SSE `inbox.summary` 는 **변화가 있을 때만** 오므로
   * 그것만 기다리면 새로고침 직후 뱃지가 0 으로 보인다(P2 때는 `getInboxSummary` 가 없어 낡은 수를 지웠다).
   */
  const refreshInbox = useCallback(async () => {
    if (!workspace) return;
    try {
      const sum = await api.get("/inbox/summary", { query: { workspace_id: workspace.id } });
      setInbox(sum.action_required);
    } catch {
      setInbox(null); // 못 읽으면 낡은 수를 남기지 않는다(N4)
    }
  }, [workspace]);
  useEffect(() => {
    void refreshInbox();
  }, [refreshInbox]);
  const onResync = useCallback(() => void refreshInbox(), [refreshInbox]);
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
        workspaces={me.workspaces}
        currentWorkspaceId={workspace.id}
        onSelectWorkspace={selectWorkspace}
      />
      <div className="shell__main">
        <ConnectionBanner state={conn} />
        <main className="content">{children}</main>
      </div>
    </div>
  );
}

export default Shell;
