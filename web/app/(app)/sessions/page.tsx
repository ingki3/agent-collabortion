"use client";
/**
 * S5 세션 목록 — P1 최소: 상태 배지 + 제목 + 목표 한 줄 + 새 세션 CTA. 빈 상태(SCREEN §7). 실시간 session.updated.
 *
 * P5(T-W13, FR-2.7 · SCREEN §4.3 「카드 옵션(…) — 삭제」): 카드 오른쪽 위 「…」 메뉴(세션 열기·삭제) + 삭제 확인 다이얼로그.
 * 카드는 `<Link>` 하나가 통째였다 — a 안에 button 을 둘 수 없으니(HTML) 카드는 `article` 이 되고, 링크(내용 전부)와 메뉴가 형제다.
 * 클릭 영역·키보드 이동은 그대로다: 링크가 카드 전체를 덮고 메뉴 버튼만 그 위에 앉는다(app.css .session-card).
 * 삭제 뒤 카드는 두 경로로 빠진다 — 204 즉시 · SSE `session.deleted` — 둘 다 "그 id 를 목록에서 뺀다" 라 멱등이다.
 * S7 을 보다가 삭제된 사람은 `?deleted=<제목>` 으로 돌아와 안내 한 줄을 본다(sessions/[id]/page.tsx).
 */
import { Suspense, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Badge } from "@/components/Badge";
import { PageHead, DisabledHint } from "@/components/PageHead";
import { SessionCardMenu } from "@/components/SessionCardMenu";
import { DeleteSessionDialog } from "@/components/DeleteSessionDialog";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import { relativeTime } from "@/lib/time";
import { sessionBadgeLabel } from "@/lib/session-label";
import { deleteGate, SESSION_DELETED_NOTICE } from "@/lib/wording";
import type { Runtime, SessionListItem, StreamEvent } from "@/lib/api/types";

export default function SessionsPage() {
  // `useSearchParams` 는 정적 경로에서 Suspense 경계가 필요하다(next build) — 설정 화면과 같은 모양.
  return (
    <Suspense fallback={null}>
      <SessionsView />
    </Suspense>
  );
}

function SessionsView() {
  const { workspace, me, canManage } = useAuth();
  const router = useRouter();
  const search = useSearchParams();
  const [items, setItems] = useState<SessionListItem[] | null>(null);
  const [runtimes, setRuntimes] = useState<Runtime[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<SessionListItem | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // S7 에서 `session.deleted` 를 받고 돌아온 사람 — 안내 한 줄을 보이고 주소는 깨끗이(새로고침해도 다시 뜨지 않게).
  const deletedTitle = search.get("deleted");
  useEffect(() => {
    if (deletedTitle == null) return;
    setNotice(SESSION_DELETED_NOTICE.elsewhere(deletedTitle));
    router.replace("/sessions");
  }, [deletedTitle, router]);

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

  /** 카드를 뺀다 — 204 뒤에도, SSE 뒤에도 같은 함수(멱등: 없으면 그대로). */
  const removeSession = useCallback((id: string) => {
    setItems((cur) => (cur && cur.some((s) => s.id === id) ? cur.filter((s) => s.id !== id) : cur));
  }, []);

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
      if (ev.type === "session.deleted") {
        const p = ev.payload as { session_id?: string };
        const id = p.session_id ?? ev.session_id;
        if (id) removeSession(id);
      }
      if (ev.type === "runtime.updated") void load();
    },
    [load, removeSession],
  );
  useWorkspaceStream(workspace?.id, onEvent, { onResync: () => void load() });

  const noRuntime = runtimes !== null && runtimes.filter((r) => r.status === "online").length === 0;

  // 비활성 사유는 버튼 아래에서도 말한다(§8.5) — 빈 상태 카드는 목록이 비었을 때만 보이기 때문이다.
  const noRuntimeWhy = "먼저 컴퓨터를 연결하세요 — 세션은 컴퓨터 한 대에 묶입니다";

  return (
    <div>
      <PageHead screen="sessions">
        <Link
          href="/rooms/new"
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
      {notice && (
        <p className="notice notice--info" role="status" data-testid="session-deleted-notice">
          {notice}
        </p>
      )}
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
          <Link href="/rooms/new" className="btn btn--primary">
            새 세션
          </Link>
        </div>
      ) : (
        <div className="cards" data-testid="session-list">
          {items.map((s) => {
            const href = `/sessions/${s.id}`;
            // 권한은 계약 deleteSession 그대로(Director 또는 owner·admin) — 판정은 deleteGate 안에서, 서버가 다시 한다(403).
            const gate = deleteGate(s, { userId: me?.user.id, canManage });
            return (
              <article key={s.id} className="session-card" data-testid="session-row" data-session-id={s.id} data-status={s.status}>
                <Link href={href} className="session-card__link" data-testid="session-link">
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
                <SessionCardMenu href={href} gate={gate} onDelete={() => setDeleting(s)} testId={s.id} />
              </article>
            );
          })}
        </div>
      )}
      {deleting && (
        <DeleteSessionDialog
          session={deleting}
          onDeleted={(id) => {
            removeSession(id);
            setNotice(SESSION_DELETED_NOTICE.mine(deleting.title));
          }}
          onClose={() => setDeleting(null)}
        />
      )}
    </div>
  );
}
