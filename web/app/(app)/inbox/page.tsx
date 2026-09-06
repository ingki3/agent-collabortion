"use client";
/**
 * S8 Inbox(SCREEN §4.6) — "지금 내가 답해야 할 것"을 **세션을 열지 않고** 처리한다(F2, U3).
 *
 * 정렬은 계약이 정한 서버 몫이다(`listInbox`: overdue → action_required → attention → info, 묶음 안에서
 * 기한 임박 순). 화면이 다시 정렬하면 서버와 다른 순서를 말하게 되므로 **받은 순서를 그대로 그린다**.
 * 필터는 계약의 `filter`(all·unread·action_required)와 `type` 뿐이다 — 화면이 만든 필터는 서버 페이지네이션과
 * 어긋난다.
 *
 * **항목당 왕복은 하나도 없다**(K-9, #147). 예전에는 예산 HITL 을 알아보려고 `hitl_request` 항목마다
 * `getHitlRequest` 를 한 번 더 읽었다(N+1) — 목록 하나에 요청 N+1 개였고, 그 응답이 늦게 오면 카드가
 * 그려진 뒤에야 상향 입력이 붙었다. 계약이 `InboxItem.card.purpose` 를 실어 주면서 그 왕복이 사라졌다.
 *
 * 응답은 `respondHitlRequest`(멱등키 필수) 로 보낸다. 성공하면 그 항목을 목록에서 내린다 — U3 2단계의
 * "카드가 사라짐"이 처리됐다는 유일한 신호다.
 *
 * 모바일: v1 은 데스크톱 우선이되 **인박스 응답만 모바일 웹에서 동작한다**(SCREEN §8.2 Q6) — 이 화면은
 * 한 열로 접히고 버튼이 터치 크기를 유지한다.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { InboxItemCard, type InboxAction } from "@/components/InboxItemCard";
import { api, errorMessage, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type { HitlResponse, InboxItem, InboxSummary, StreamEvent } from "@/lib/api/types";

type Filter = "all" | "unread" | "action_required";

const FILTER_LABEL: Record<Filter, string> = {
  all: "전체",
  unread: "미읽음",
  action_required: "액션 필요",
};

export default function InboxPage() {
  const router = useRouter();
  const { workspace } = useAuth();
  const [items, setItems] = useState<InboxItem[] | null>(null);
  const [summary, setSummary] = useState<InboxSummary | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [sessionFilter, setSessionFilter] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const [page, sum] = await Promise.all([
        api.get("/inbox", {
          query: {
            workspace_id: workspace.id,
            filter,
            ...(sessionFilter ? { session_id: sessionFilter } : {}),
          },
        }),
        api.get("/inbox/summary", { query: { workspace_id: workspace.id } }),
      ]);
      setItems(page.items ?? []);
      setSummary(sum);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
      setItems([]);
    }
  }, [workspace, filter, sessionFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  // 실시간(R4) — 셸의 워크스페이스 SSE 하나를 구독한다. 항목이 생기거나 해소되면 다시 읽는다.
  const onEvent = useCallback((ev: StreamEvent) => {
    switch (ev.type) {
      case "inbox.item_created":
      case "inbox.item_updated": {
        const it = ev.payload as unknown as InboxItem;
        setItems((cur) => {
          if (!cur) return cur;
          const at = cur.findIndex((x) => x.id === it.id);
          if (at < 0) return [it, ...cur];
          const next = [...cur];
          next[at] = it;
          return next;
        });
        break;
      }
      case "inbox.summary": {
        const p = ev.payload as unknown as InboxSummary;
        if (typeof p.action_required === "number") setSummary(p);
        break;
      }
      default:
        break;
    }
  }, []);
  useWorkspaceStream(workspace?.id, onEvent, { onResync: () => void load() });

  /** 세션별 필터 목록 — 지금 인박스에 있는 세션만 고를 수 있게 한다(SCREEN §4.6 "필터: … 세션별"). */
  const sessions = useMemo(() => {
    const seen = new Map<string, string>();
    for (const it of items ?? []) if (it.session_id && it.session?.title) seen.set(it.session_id, it.session.title);
    return [...seen.entries()];
  }, [items]);

  async function respond(item: InboxItem, body: HitlResponse) {
    if (!item.ref_id) throw new Error("이 항목에는 HITL 요청 id 가 없습니다");
    setBusy(true);
    try {
      const r = await api.post("/hitl-requests/{hitlRequestId}/response", {
        path: { hitlRequestId: item.ref_id },
        // 계약 필수 헤더 — 두 번째 응답은 오류가 아니라 무시(`ignored: true`, E7-08).
        idempotencyKey: newIdempotencyKey(),
        body,
      });
      setItems((cur) => (cur ? cur.filter((x) => x.id !== item.id) : cur));
      setSummary((cur) => (cur ? { ...cur, action_required: Math.max(0, cur.action_required - 1) } : cur));
      setToast(
        r.ignored
          ? "이미 답변된 요청이라 무시했습니다 — 첫 응답이 유지됩니다."
          : `${item.card?.agent_name ? `@${item.card.agent_name}가` : "에이전트가"} 재개됩니다`,
      );
    } finally {
      setBusy(false);
    }
  }

  async function act(item: InboxItem, action: InboxAction) {
    const sessionHref = item.session_id ? `/sessions/${item.session_id}` : null;
    switch (action) {
      case "open_session":
      case "reply":
        if (sessionHref) router.push(sessionHref);
        return;
      case "restart":
        // 재지시는 lane 카드의 작성창을 거친다 — 인박스에서 맥락 없이 새 지시를 만들 수 없다(SCREEN §4.5 m6).
        if (sessionHref) router.push(`${sessionHref}?restart_lane=${item.card?.lane_id ?? ""}`);
        return;
      case "open_runtimes":
      case "rebind":
        router.push("/runtimes");
        return;
      default:
        return;
    }
  }

  /**
   * `session_paused` 의 "계속 승인" — 예산이면 **카드 안에서 새 상한까지 받는다**(U7-1: 카드만으로
   * "얼마를 얼마로 올릴지" 결정할 수 있어야 하고, 그러지 못하면 세션을 열게 된다).
   * 사유별 본문은 계약 `resumeSession` 표 그대로다.
   */
  async function approveContinue(item: InboxItem, limits: { budget_usd?: number }) {
    if (!item.session_id) return;
    setBusy(true);
    try {
      await api.post("/sessions/{sessionId}/resume", {
        path: { sessionId: item.session_id },
        body: limits.budget_usd != null ? { limits: { budget_usd: limits.budget_usd } } : {},
      });
      setItems((cur) => (cur ? cur.filter((x) => x.id !== item.id) : cur));
      setSummary((cur) => (cur ? { ...cur, action_required: Math.max(0, cur.action_required - 1) } : cur));
      setToast("세션을 재개했습니다");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function markRead(item: InboxItem) {
    try {
      const updated = await api.post("/inbox/{inboxItemId}/read", { path: { inboxItemId: item.id } });
      setItems((cur) => (cur ? cur.map((x) => (x.id === item.id ? updated : x)) : cur));
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  async function markAllRead() {
    if (!workspace) return;
    try {
      // `action_required` 는 건드리지 않는다(계약 markAllInboxRead) — 해소는 응답이 한다.
      await api.post("/inbox/read-all", { query: { workspace_id: workspace.id } });
      await load();
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  if (!workspace) return null;

  return (
    <div className="s8" data-testid="inbox-page" data-filter={filter}>
      <div className="page-head">
        <h1>Inbox</h1>
        <span className="s8__counts" data-testid="inbox-counts">
          조치 필요 <b data-testid="inbox-count-action">{summary?.action_required ?? 0}</b>
          {summary?.overdue ? <> · 기한 지남 <b data-testid="inbox-count-overdue">{summary.overdue}</b></> : null}
        </span>
      </div>

      <nav className="s8__filters" aria-label="인박스 필터">
        {(["all", "unread", "action_required"] as Filter[]).map((f) => (
          <button
            key={f}
            type="button"
            className={`s8__filter${filter === f ? " s8__filter--on" : ""}`}
            aria-pressed={filter === f}
            onClick={() => setFilter(f)}
            data-testid={`inbox-filter-${f}`}
          >
            {FILTER_LABEL[f]}
          </button>
        ))}
        {sessions.length > 1 && (
          <select
            className="select"
            style={{ width: "auto" }}
            value={sessionFilter}
            onChange={(e) => setSessionFilter(e.target.value)}
            aria-label="세션별 필터"
            data-testid="inbox-filter-session"
          >
            <option value="">모든 세션</option>
            {sessions.map(([id, title]) => (
              <option key={id} value={id}>{title}</option>
            ))}
          </select>
        )}
        <span className="s8__spacer" />
        <button type="button" className="msg__link" onClick={() => void markAllRead()} data-testid="inbox-read-all">
          전부 읽음(알림·주의만)
        </button>
      </nav>

      {toast && (
        <p className="s8__toast" role="status" data-testid="inbox-toast">{toast}</p>
      )}
      {error && <p className="problem" role="alert" data-testid="inbox-error">{error}</p>}

      {items === null ? (
        <p className="muted">불러오는 중…</p>
      ) : items.length === 0 ? (
        <div className="empty" data-testid="inbox-empty">
          {/* 부정형이 아니라 상태로(SCREEN §7). */}
          <div className="empty__title">지금 답할 것이 없습니다</div>
          <div className="empty__body">응답이 필요해지면 여기와 이메일로 알려 드립니다.</div>
        </div>
      ) : (
        <div className="s8__list" data-testid="inbox-list">
          {items.map((it) => (
            <InboxItemCard
              key={it.id}
              item={it}
              onRespond={respond}
              onApproveContinue={approveContinue}
              onAction={(i, a) => void act(i, a)}
              onMarkRead={(i) => void markRead(i)}
              busy={busy}
              now={now}
            />
          ))}
        </div>
      )}

      <style>{`
        .s8__counts { color: var(--ink-3); font-size: 12px; }
        .s8__filters { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; margin: 8px 0 10px; }
        .s8__filter { border: 1px solid var(--line); background: var(--bg); border-radius: 999px; padding: 4px 12px; font-size: 12px; cursor: pointer; }
        .s8__filter--on { border-color: var(--ink); font-weight: 600; }
        .s8__spacer { flex: 1; }
        .s8__list { display: flex; flex-direction: column; gap: 8px; }
        .s8__toast { margin: 0 0 8px; font-size: 12px; color: var(--s-done-text); }
        /* 인박스 응답만 모바일 웹 대상이다(SCREEN §8.2 Q6) — 한 열, 버튼은 줄바꿈해도 크기를 지킨다. */
        @media (max-width: 640px) {
          .s8__filters { gap: 4px; }
          .s8__list { gap: 10px; }
        }
      `}</style>
    </div>
  );
}
