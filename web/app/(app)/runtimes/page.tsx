"use client";
/** S11 Runtimes — P1 최소 카드: 이름 · 온라인 · CLI 목록 · 마지막 접속. 실시간 runtime.updated. */
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useStream } from "@/lib/realtime/stream";
import { relativeTime } from "@/lib/time";
import type { Runtime, StreamEvent } from "@/lib/api/types";

const KIND = { claude_code: "Claude Code", hermes: "Hermes", antigravity: "Antigravity" } as const;

export function RuntimeCard({ rt }: { rt: Runtime }) {
  const online = rt.status === "online";
  return (
    <div className="card" data-testid="runtime-card" data-status={rt.status}>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <b>{rt.name}</b>
        <span className="small" style={{ color: online ? "var(--s-done-text)" : "var(--s-fail-text)" }}>
          {online ? "● 온라인" : "✕ 오프라인"}
        </span>
      </div>
      <div className="small muted-3">
        {rt.host ?? "—"} · 데몬 {rt.daemon_version ?? "—"} · 마지막 접속 {relativeTime(rt.last_seen_at)}
      </div>
      <ul className="small muted" style={{ margin: "8px 0 0", paddingLeft: 18 }}>
        {rt.capabilities.length === 0 && <li>감지된 CLI 없음</li>}
        {rt.capabilities.map((c) => (
          <li key={c.kind}>
            {KIND[c.kind]} {c.version ?? ""} · {c.logged_in ? "로그인됨" : "로그인 필요"} · 모델 {c.models?.length ?? 0}개
          </li>
        ))}
      </ul>
      {!online && rt.grace_ends_at && (
        <div className="small" style={{ marginTop: 8, color: "var(--s-fail-text)" }}>
          오프라인 {relativeTime(rt.offline_since)} 부터 · 유예 만료 {new Date(rt.grace_ends_at).toLocaleDateString("ko-KR")}
        </div>
      )}
      <div className="small muted-3" style={{ marginTop: 6 }}>실행 중 task {rt.running_task_count}</div>
    </div>
  );
}

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
