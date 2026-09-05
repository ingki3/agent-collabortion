"use client";
/** S11 Runtimes — P1 최소 카드: 이름 · 온라인 · CLI 목록 · 마지막 접속. 실시간 runtime.updated. */
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useStream } from "@/lib/realtime/stream";
import { relativeTime } from "@/lib/time";
import type { Runtime, StreamEvent } from "@/lib/api/types";
import { RuntimeCard } from "@/components/RuntimeCard";

export default function RuntimesPage() {
  const { workspace, canManage } = useAuth();
  const [items, setItems] = useState<Runtime[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      setItems(await api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }));
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [workspace]);
  useEffect(() => {
    void load();
  }, [load]);

  const onEvent = useCallback(
    (ev: StreamEvent) => {
      if (ev.type !== "runtime.updated" && ev.type !== "pairing.updated") return;
      if (ev.type === "pairing.updated") {
        void load();
        return;
      }
      const rt = ev.payload as unknown as Runtime;
      setItems((cur) => (cur ? (cur.some((r) => r.id === rt.id) ? cur.map((r) => (r.id === rt.id ? rt : r)) : [...cur, rt]) : cur));
    },
    [load],
  );
  useStream(workspace?.id, onEvent, { onResync: () => void load() });

  return (
    <div>
      <div className="page-head">
        <h1>Runtimes</h1>
        <Link href="/runtimes/new" className="btn btn--primary" aria-disabled={!canManage || undefined} title={!canManage ? "owner·admin 만 런타임을 추가할 수 있습니다" : undefined} onClick={(e) => !canManage && e.preventDefault()} data-testid="add-computer">
          Add a computer
        </Link>
      </div>
      {error && <p className="problem">{error}</p>}
      {items === null ? (
        <p className="muted">불러오는 중…</p>
      ) : items.length === 0 ? (
        <div className="empty" data-testid="empty-runtimes">
          <div className="empty__title">연결된 컴퓨터가 없습니다</div>
          <div className="empty__body">에이전트는 여러분의 컴퓨터에서 실행됩니다. 설치 명령 2줄로 연결하세요.</div>
          <Link href="/runtimes/new" className="btn btn--primary">Add a computer</Link>
        </div>
      ) : (
        <div className="story__grid">
          {items.map((rt) => (
            <RuntimeCard key={rt.id} rt={rt} />
          ))}
        </div>
      )}
    </div>
  );
}
