"use client";
/** S5 Sessions 목록 — P1 최소: 상태 배지 + 제목 + goal 한 줄 + 새 세션 CTA. 빈 상태(SCREEN §7). 실시간 session.updated. */
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Badge } from "@/components/Badge";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useStream } from "@/lib/realtime/stream";
import { relativeTime } from "@/lib/time";
import { sessionBadgeLabel } from "@/lib/session-label";
import type { Runtime, SessionListItem, StreamEvent } from "@/lib/api/types";

export default function SessionsPage() {
  const { workspace } = useAuth();
  const [items, setItems] = useState<SessionListItem[] | null>(null);
  const [runtimes, setRuntimes] = useState<Runtime[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const [page, rts] = await Promise.all([
        api.get("/workspaces/{workspaceId}/sessions", { path: { workspaceId: workspace.id } }),
        api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }),
      ]);
      setItems(page.items);
      setRuntimes(rts);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [workspace]);

  useEffect(() => {
    void load();
  }, [load]);

  const onEvent = useCallback(
    (ev: StreamEvent) => {
      if (ev.type === "session.updated" || ev.type === "cost.updated") {
        const p = ev.payload as Partial<SessionListItem> & { id?: string; session_id?: string };
        const id = p.id ?? p.session_id;
        setItems((cur) => {
          if (!cur) return cur;
          if (!cur.some((s) => s.id === id)) {
            void load();
            return cur;
          }
          return cur.map((s) => (s.id === id ? { ...s, ...p, id: s.id } : s));
        });
      }
      if (ev.type === "runtime.updated") void load();
    },
    [load],
  );
  useStream(workspace?.id, onEvent, { onResync: () => void load() });

  const noRuntime = runtimes !== null && runtimes.filter((r) => r.status === "online").length === 0;

  return (
    <div>
      <div className="page-head">
        <h1>Sessions</h1>
        <Link
          href="/sessions/new"
          className="btn btn--primary"
          aria-disabled={noRuntime || undefined}
          title={noRuntime ? "먼저 컴퓨터를 연결하세요 — 세션은 런타임에 묶입니다(FR-2.1)" : undefined}
          onClick={(e) => noRuntime && e.preventDefault()}
          data-testid="new-session"
        >
          새 세션
        </Link>
      </div>
      {error && <p className="problem">{error}</p>}
      {items === null ? (
        <p className="muted">불러오는 중…</p>
      ) : noRuntime ? (
        <div className="empty" data-testid="empty-no-runtime">
          <div className="empty__title">먼저 컴퓨터를 연결하세요</div>
          <div className="empty__body">세션은 에이전트가 실행될 런타임에 묶입니다. 연결되면 여기서 첫 세션을 만듭니다.</div>
          <Link href="/runtimes/new" className="btn btn--primary">
            Add a computer
          </Link>
        </div>
      ) : items.length === 0 ? (
        <div className="empty" data-testid="empty-no-session">
          <div className="empty__title">첫 세션을 만들어 보세요</div>
          <div className="empty__body">예: "국내 B2B SaaS 결제 시장 조사 보고서 10페이지" — goal 하나만 적으면 나머지는 기본값으로 시작됩니다.</div>
          <Link href="/sessions/new" className="btn btn--primary">
            새 세션
          </Link>
        </div>
      ) : (
        <div className="list" data-testid="session-list">
          {items.map((s) => (
            <Link key={s.id} href={`/sessions/${s.id}`} className="list-row" data-testid="session-row">
              <Badge kind="session" value={s.status} label={sessionBadgeLabel(s)} />
              <span style={{ minWidth: 0 }}>
                <div className="list-row__title">{s.title}</div>
                <div className="list-row__sub">{s.goal}</div>
              </span>
              <span className="small muted-3 row" style={{ gap: 10 }}>
                {s.attention.hitl_open > 0 && <span style={{ color: "var(--s-wait-text)" }}>⏳︎ {s.attention.hitl_open}</span>}
                {s.attention.blocked > 0 && <span style={{ color: "var(--s-block-text)" }}>? {s.attention.blocked}</span>}
                {s.attention.failed > 0 && <span style={{ color: "var(--s-fail-text)" }}>✕ {s.attention.failed}</span>}
                <span>{relativeTime(s.last_activity_at ?? s.created_at)}</span>
              </span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
