"use client";
/** S5 세션 목록 — P1 최소: 상태 배지 + 제목 + 목표 한 줄 + 새 세션 CTA. 빈 상태(SCREEN §7). 실시간 session.updated. */
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Badge } from "@/components/Badge";
import { PageHead, DisabledHint } from "@/components/PageHead";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
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
  useWorkspaceStream(workspace?.id, onEvent, { onResync: () => void load() });

  const noRuntime = runtimes !== null && runtimes.filter((r) => r.status === "online").length === 0;

  // 비활성 사유는 버튼 아래에서도 말한다(§8.5) — 빈 상태 카드는 목록이 비었을 때만 보이기 때문이다.
  const noRuntimeWhy = "먼저 컴퓨터를 연결하세요 — 세션은 컴퓨터 한 대에 묶입니다";

  return (
    <div>
      <PageHead screen="sessions">
        <Link
          href="/sessions/new"
          className="btn btn--primary"
          aria-disabled={noRuntime || undefined}
          aria-describedby={noRuntime ? "new-session-hint" : undefined}
          title={noRuntime ? noRuntimeWhy : undefined}
          onClick={(e) => noRuntime && e.preventDefault()}
          data-testid="new-session"
        >
          새 세션
        </Link>
        {noRuntime && <DisabledHint id="new-session-hint">{noRuntimeWhy}</DisabledHint>}
      </PageHead>
      {error && <p className="problem">{error}</p>}
      {items === null ? (
        <p className="muted">불러오는 중…</p>
      ) : noRuntime ? (
        <div className="empty" data-testid="empty-no-runtime">
          <div className="empty__title">먼저 컴퓨터를 연결하세요</div>
          <div className="empty__body">세션은 에이전트가 실행될 컴퓨터 한 대에 묶입니다. 연결되면 여기서 첫 세션을 만듭니다.</div>
          <Link href="/runtimes/new" className="btn btn--primary">
            컴퓨터 연결
          </Link>
        </div>
      ) : items.length === 0 ? (
        <div className="empty" data-testid="empty-no-session">
          <div className="empty__title">첫 세션을 만들어 보세요</div>
          <div className="empty__body">예: "국내 B2B SaaS 결제 시장 조사 보고서 10페이지" — 목표 하나만 적으면 나머지는 기본값으로 시작됩니다.</div>
          <Link href="/sessions/new" className="btn btn--primary">
            새 세션
          </Link>
        </div>
      ) : (
        <div className="cards" data-testid="session-list">
          {items.map((s) => (
            <Link key={s.id} href={`/sessions/${s.id}`} className="session-card" data-testid="session-row">
              <span className="session-card__top">
                <Badge kind="session" value={s.status} label={sessionBadgeLabel(s)} />
                <span className="session-card__meta">
                  {s.attention.hitl_open > 0 && <span style={{ color: "var(--s-wait-text)" }}>⏳︎ {s.attention.hitl_open}</span>}
                  {s.attention.blocked > 0 && <span style={{ color: "var(--s-block-text)" }}>? {s.attention.blocked}</span>}
                  {s.attention.failed > 0 && <span style={{ color: "var(--s-fail-text)" }}>✕ {s.attention.failed}</span>}
                  <span>{relativeTime(s.last_activity_at ?? s.created_at)}</span>
                </span>
              </span>
              <span className="session-card__title" title={s.title}>{s.title}</span>
              <span className="session-card__sub" title={s.goal}>{s.goal}</span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
