"use client";
/**
 * S15 활동 로그(`/settings/audit`, SCREEN v0.19.2 §4.18 — v1.1 → v1 승격, T-R2-W4a).
 *
 * `listActivityLog`(owner·admin) 하나를 표로 그린다: 시각 · 행위자(사람/에이전트/시스템) · 행위 · 대상 · 방 · 내용(마스킹 반영).
 * 필터는 계약 파라미터 그대로 — 방(`room_id`) · 행위(`action`) · 행위자(`actor_id`) · 기간(`since`·`until`). 서버가 거른다.
 * **실시간 없음**(§6) — 새로고침 버튼과 「더 보기」(커서)만 있다. 흐르는 감사 화면은 읽을 수 없다.
 *
 * 권한이 없으면 화면을 숨기지 않고 사유를 적는다(§7 「권한 없음」). 서버 403 문장이 오면 그것을 그대로 보인다.
 */
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { PageHead } from "@/components/PageHead";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { ACTION_FILTER, actionLabel, AUDIT, dayRange, objectCell, payloadCell, roomCell, type ActivityLogEntry } from "@/lib/audit";
import { clockTime } from "@/lib/time";
import type { Agent, Member, RoomListItem } from "@/lib/api/types";
import "@/components/settings.css";

interface Filters { room: string; action: string; actor: string; since: string; until: string }
const NO_FILTER: Filters = { room: "", action: "", actor: "", since: "", until: "" };

export default function AuditPage() {
  const { workspace, canManage } = useAuth();
  const [rows, setRows] = useState<ActivityLogEntry[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState<string | null>(null);
  const [draft, setDraft] = useState<Filters>(NO_FILTER);
  const [applied, setApplied] = useState<Filters>(NO_FILTER);
  const [rooms, setRooms] = useState<RoomListItem[]>([]);
  const [actors, setActors] = useState<{ id: string; name: string }[]>([]);
  const [busy, setBusy] = useState(false);

  const query = useCallback((f: Filters, after?: string | null) => ({
    ...(f.room ? { room_id: f.room } : {}),
    ...(f.action ? { action: f.action } : {}),
    ...(f.actor ? { actor_id: f.actor } : {}),
    ...dayRange(f.since, f.until),
    ...(after ? { cursor: after } : {}),
    limit: 50,
  }), []);

  const load = useCallback(async (f: Filters, after?: string | null) => {
    if (!workspace) return;
    setBusy(true);
    try {
      const page = await api.get("/workspaces/{workspaceId}/activity-log", { path: { workspaceId: workspace.id }, query: query(f, after) });
      setRows((cur) => (after && cur ? [...cur, ...(page.items ?? [])] : (page.items ?? [])));
      setCursor(page.next_cursor ?? null);
      setError(null);
      setForbidden(null);
    } catch (e) {
      if (isApiError(e) && e.status === 403) setForbidden(e.problem.detail ?? AUDIT.forbidden);
      else setError(errorMessage(e));
      if (!after) setRows([]);
    } finally {
      setBusy(false);
    }
  }, [workspace, query]);

  useEffect(() => {
    if (!workspace || !canManage) return;
    void load(applied);
  }, [workspace, canManage, applied, load]);

  // 필터 선택지 — 방(보관·공개 포함 전부, owner·admin 은 감사 열람으로 invited 방도 본다) · 행위자(멤버 + 에이전트).
  useEffect(() => {
    if (!workspace || !canManage) return;
    void Promise.all([
      api.get("/workspaces/{workspaceId}/rooms", { path: { workspaceId: workspace.id }, query: { participating: false, include_archived: true, limit: 200 } }).catch(() => null),
      api.get("/workspaces/{workspaceId}/members", { path: { workspaceId: workspace.id } }).catch(() => null),
      api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: workspace.id } }).catch(() => null),
    ]).then(([r, m, a]) => {
      setRooms(((r?.items ?? []) as RoomListItem[]));
      const people = ((m as { items?: Member[] } | null)?.items ?? []).map((x) => ({ id: x.user.id, name: x.user.display_name || x.user.email }));
      const agentList = (Array.isArray(a) ? a : ((a as { items?: Agent[] } | null)?.items ?? [])) as Agent[];
      setActors([...people, ...agentList.map((x) => ({ id: x.id, name: `@${x.name}` }))]);
    });
  }, [workspace, canManage]);

  if (!workspace) return null;

  return (
    <div data-testid="audit-page">
      <PageHead screen="settings">
        <Link href="/settings" className="btn btn--sm" data-testid="audit-back">{AUDIT.back}</Link>
      </PageHead>
      <section className="card" data-testid="audit-card">
        <div className="tab-head">
          <div>
            <h2>{AUDIT.title}</h2>
            <p className="tab-head__desc">{AUDIT.desc}</p>
          </div>
          <div className="tab-head__actions">
            <button type="button" className="btn btn--sm" disabled={busy || !canManage} onClick={() => void load(applied)} data-testid="audit-refresh">{AUDIT.refresh}</button>
          </div>
        </div>
        <p className="small muted-3" data-testid="audit-static">{AUDIT.static_note}</p>

        {!canManage || forbidden ? (
          <p className="notice" role="alert" data-testid="audit-forbidden">{forbidden ?? AUDIT.forbidden}</p>
        ) : (
          <>
            <form
              className="audit__filters"
              data-testid="audit-filters"
              onSubmit={(e) => {
                e.preventDefault();
                setApplied(draft);
              }}
            >
              <label>
                <span>{AUDIT.filters.room}</span>
                <select className="select" value={draft.room} onChange={(e) => setDraft({ ...draft, room: e.target.value })} data-testid="audit-filter-room">
                  <option value="">{AUDIT.filters.all_rooms}</option>
                  {rooms.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
                </select>
              </label>
              <label>
                <span>{AUDIT.filters.action}</span>
                <select className="select" value={draft.action} onChange={(e) => setDraft({ ...draft, action: e.target.value })} data-testid="audit-filter-action">
                  <option value="">{AUDIT.filters.all_actions}</option>
                  {ACTION_FILTER.map((a) => <option key={a} value={a}>{actionLabel(a)}</option>)}
                </select>
              </label>
              <label>
                <span>{AUDIT.filters.actor}</span>
                <select className="select" value={draft.actor} onChange={(e) => setDraft({ ...draft, actor: e.target.value })} data-testid="audit-filter-actor">
                  <option value="">{AUDIT.filters.all_actors}</option>
                  {actors.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                </select>
              </label>
              <label>
                <span>{AUDIT.filters.since}</span>
                <input className="input" type="date" value={draft.since} onChange={(e) => setDraft({ ...draft, since: e.target.value })} data-testid="audit-filter-since" />
              </label>
              <label>
                <span>{AUDIT.filters.until}</span>
                <input className="input" type="date" value={draft.until} onChange={(e) => setDraft({ ...draft, until: e.target.value })} data-testid="audit-filter-until" />
              </label>
              <div className="audit__filter-actions">
                <button type="submit" className="btn btn--sm btn--primary" data-testid="audit-apply">{AUDIT.filters.apply}</button>
                <button type="button" className="btn btn--sm btn--ghost" onClick={() => { setDraft(NO_FILTER); setApplied(NO_FILTER); }} data-testid="audit-clear">{AUDIT.filters.clear}</button>
              </div>
            </form>

            {error && <p className="problem" role="alert" data-testid="audit-error">{error}</p>}
            {rows === null ? (
              <p className="muted">불러오는 중…</p>
            ) : rows.length === 0 ? (
              <div className="empty" data-testid="audit-empty"><div className="empty__title">{AUDIT.empty}</div></div>
            ) : (
              <div className="audit__scroll">
                <table className="audit__table" data-testid="audit-table">
                  <thead>
                    <tr>
                      <th>{AUDIT.cols.at}</th>
                      <th>{AUDIT.cols.actor}</th>
                      <th>{AUDIT.cols.action}</th>
                      <th>{AUDIT.cols.object}</th>
                      <th>{AUDIT.cols.room}</th>
                      <th>{AUDIT.cols.payload}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((e) => {
                      const p = payloadCell(e.payload);
                      return (
                        <tr key={e.id} data-testid="audit-row" data-action={e.action} data-masked={String(p.masked)}>
                          <td className="audit__at" title={e.at}>{new Date(e.at).toLocaleDateString("ko-KR")} {clockTime(e.at).slice(0, 5)}</td>
                          <td data-testid="audit-actor">
                            {e.actor.kind === "agent" ? `@${e.actor.name}` : e.actor.name}
                            <span className="audit__kind"> · {AUDIT.actor_kind[e.actor.kind]}</span>
                          </td>
                          <td data-testid="audit-action">{actionLabel(e.action)}</td>
                          <td data-testid="audit-object">{objectCell(e)}</td>
                          <td data-testid="audit-room">{roomCell(e)}</td>
                          <td className="audit__payload" data-testid="audit-payload">
                            {p.text}
                            {p.masked && <div className="audit__masked" data-testid="audit-masked">{AUDIT.masked}</div>}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
            {cursor && (
              <button type="button" className="btn btn--sm" disabled={busy} onClick={() => void load(applied, cursor)} data-testid="audit-more">{AUDIT.more}</button>
            )}
          </>
        )}
      </section>
      <style>{`
        .audit__filters { display: flex; flex-wrap: wrap; gap: 8px 12px; align-items: flex-end; margin: 8px 0 12px; }
        .audit__filters label { display: flex; flex-direction: column; gap: 3px; font-size: var(--fs-sub); color: var(--ink-2); }
        .audit__filters .select, .audit__filters .input { width: auto; min-width: 140px; max-width: 100%; }
        .audit__filter-actions { display: flex; gap: 6px; }
        .audit__scroll { overflow-x: auto; }
        .audit__table { width: 100%; border-collapse: collapse; font-size: var(--fs-body); }
        .audit__table th { text-align: left; font-weight: 600; font-size: var(--fs-sub); color: var(--ink-2); border-bottom: 1px solid var(--line); padding: 6px 8px; white-space: nowrap; }
        .audit__table td { border-bottom: 1px solid var(--line); padding: 6px 8px; vertical-align: top; }
        .audit__at { white-space: nowrap; color: var(--ink-2); }
        .audit__kind { color: var(--ink-2); font-size: var(--fs-sub); }
        .audit__payload { color: var(--ink-2); max-width: 420px; overflow-wrap: anywhere; }
        .audit__masked { font-size: var(--fs-meta); color: var(--ink-2); font-style: italic; }
      `}</style>
    </div>
  );
}
