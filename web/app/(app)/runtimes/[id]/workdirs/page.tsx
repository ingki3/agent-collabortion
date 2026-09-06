"use client";
/**
 * S13 Workdir 관리(SCREEN §4.8 · FR-6.4 M4 · U6 9·10·11).
 *
 * 목록 한 행 = 하나의 작업 공간이다: 종류 · 소유(에이전트 또는 lane) · 경로·브랜치 · 용량 · 마지막 사용 ·
 * 보존 만료일 · 상태. 상단은 워크스페이스 용량 상한 대비 사용률(E13-16 은 `>` 가 아니라 `≥` 다).
 *
 * **수동 정리는 행 단위 삭제다**(Lead 판정 2026-09-07). 계약에 "지금 GC 를 돌려라" op 이 없다 —
 * 보존 기한이 지나면 서버 스케줄러가 알아서 정리하고(FR-6.4), 지금 지우려면 그 행을 지운다.
 * 없는 op 을 화면이 만들어 내지 않는다.
 *
 * 삭제는 **기본 차단**이다(FR-6.4 M4): 미병합 커밋이나 미커밋 변경이 있으면 서버가 `409 workdir_dirty` 를
 * 주고, 화면은 사유를 **두 갈래로 갈라** 보여 준다 — `unmerged_commits` 는 병합하라(E13-12),
 * `uncommitted_changes` 는 커밋하거나 버리라(E13-13). 확인 뒤 `force=true` 로 다시 보낸다.
 * worktree 는 워크트리만 지우고 **브랜치는 남는다** — 그 사실을 삭제 전에 말한다.
 *
 * 실제 삭제는 데몬이 하므로 응답은 `202` 고 결과는 SSE `workdir.updated` 로 온다(계약 deleteWorkdir).
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import { relativeTime } from "@/lib/time";
import {
  deleteBlocked, formatBytes, gcBlockText, quotaView, retentionLabel,
  WORKDIR_KIND_LABEL, WORKDIR_STATUS_LABEL,
} from "@/lib/workdir";
import type { Agent, Runtime, StreamEvent, Workdir } from "@/lib/api/types";

export default function WorkdirsPage() {
  const params = useParams<{ id: string }>();
  const runtimeId = params.id;
  const { workspace } = useAuth();
  const [runtime, setRuntime] = useState<Runtime | null>(null);
  const [items, setItems] = useState<Workdir[] | null>(null);
  const [total, setTotal] = useState<number>(0);
  const [quotaGb, setQuotaGb] = useState<number | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [error, setError] = useState<string | null>(null);
  /** 삭제가 409 로 막힌 행 → 사람에게 보여줄 사유. 확인하면 `force` 로 다시 보낸다. */
  const [refused, setRefused] = useState<Record<string, string>>({});
  const [busyId, setBusyId] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const [rts, page, ags] = await Promise.all([
        api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }),
        api.get("/runtimes/{runtimeId}/workdirs", { path: { runtimeId } }),
        api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: workspace.id } }).catch(() => ({ items: [] as Agent[] })),
      ]);
      setRuntime(rts.find((r) => r.id === runtimeId) ?? null);
      setItems(page.items ?? []);
      setTotal(page.disk_bytes_total ?? 0);
      setQuotaGb(page.disk_quota_gb ?? null);
      setAgents(ags.items);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
      setItems([]);
    }
  }, [workspace, runtimeId]);

  useEffect(() => {
    void load();
  }, [load]);

  // 삭제는 데몬이 수행한다 — 결과는 `workdir.updated` 로 온다(계약 deleteWorkdir 202).
  const onEvent = useCallback((ev: StreamEvent) => {
    if (ev.type !== "workdir.updated") return;
    const w = ev.payload as unknown as Workdir;
    setItems((cur) => (cur ? cur.map((x) => (x.id === w.id ? w : x)) : cur));
    if (w.status === "deleted") setToast(`${w.path_or_ref} 를 정리했습니다${w.branch ? ` — 브랜치 ${w.branch} 는 남아 있습니다` : ""}`);
  }, []);
  useWorkspaceStream(workspace?.id, onEvent, { onResync: () => void load() });

  const agentName = useMemo(() => {
    const m = new Map(agents.map((a) => [a.id, a.name]));
    return (id: string | null | undefined) => (id ? (m.get(id) ?? id.slice(0, 8)) : null);
  }, [agents]);

  const quota = quotaView(total, quotaGb);
  /** GC 가 막은 행 — 상단에 모아 알린다(FR-6.4 "삭제하지 않고 알린다"). 인박스 `workdir_gc_blocked` 와 같은 사실이다. */
  const blockedRows = (items ?? []).filter((w) => w.gc_blocked_reason != null);

  async function remove(w: Workdir, force: boolean) {
    setBusyId(w.id);
    setError(null);
    try {
      await api.delete("/workdirs/{workdirId}", { path: { workdirId: w.id }, query: force ? { force: true } : undefined });
      setRefused((cur) => {
        const n = { ...cur };
        delete n[w.id];
        return n;
      });
      setToast("삭제를 요청했습니다 — 데몬이 지우면 목록이 갱신됩니다.");
    } catch (e) {
      if (isApiError(e) && e.status === 409) {
        // 계약: `Problem.detail` 에 사유(gc_blocked_reason 과 같은 값). 사람이 읽는 문장은 행이 그린다.
        setRefused((cur) => ({ ...cur, [w.id]: e.problem.detail ?? e.code ?? "미커밋 변경 또는 미병합 커밋이 있습니다" }));
      } else {
        setError(errorMessage(e));
      }
    } finally {
      setBusyId(null);
    }
  }

  return (
    <div data-testid="workdirs-page" data-runtime-id={runtimeId}>
      <div className="page-head">
        <h1>Workdir 관리</h1>
        <Link href="/runtimes" className="btn btn--ghost btn--sm">Runtimes</Link>
      </div>
      <p className="small muted-3" data-testid="workdirs-runtime">{runtime?.name ?? runtimeId}</p>

      <div className={`wd__quota${quota.atLimit ? " wd__quota--full" : ""}`} data-testid="workdir-quota" data-at-limit={String(quota.atLimit)}>
        <div className="wd__quota-text">{quota.text}</div>
        {quota.ratio != null && (
          <div className="wd__bar" aria-hidden="true">
            <span style={{ width: `${Math.min(100, Math.round(quota.ratio * 100))}%` }} />
          </div>
        )}
        {quota.atLimit && (
          <div className="wd__quota-note" role="alert" data-testid="workdir-quota-full">
            용량 상한에 도달했습니다 — 정리하기 전까지 새 세션을 만들 수 없습니다(FR-6.4).
          </div>
        )}
      </div>

      {blockedRows.length > 0 && (
        <div className="notice" role="status" data-testid="workdir-gc-blocked-alert">
          자동 정리가 <b>{blockedRows.length}개</b>의 workdir 을 지우지 못했습니다 — 아래 행의 사유를 보고 병합하거나 커밋하세요.
        </div>
      )}
      {toast && <p className="wd__toast" role="status" data-testid="workdir-toast">{toast}</p>}
      {error && <p className="problem" role="alert" data-testid="workdirs-error">{error}</p>}

      {items === null ? (
        <p className="muted">불러오는 중…</p>
      ) : items.length === 0 ? (
        <div className="empty" data-testid="workdirs-empty">
          <div className="empty__title">이 컴퓨터에 남은 작업 공간이 없습니다</div>
          <div className="empty__body">worktree 는 세션이 끝난 뒤 보존 기한(기본 14일)까지 남고, container·none 은 즉시 정리됩니다.</div>
        </div>
      ) : (
        <ul className="wd__list" data-testid="workdir-list">
          {items.map((w) => {
            const gc = gcBlockText(w);
            const blocked = deleteBlocked(w);
            const asked = refused[w.id];
            const owner = w.agent_id ? `@${agentName(w.agent_id)}` : w.lane_id ? `lane ${w.lane_id.slice(0, 8)}` : "—";
            return (
              <li
                key={w.id}
                className="wd__row"
                data-testid="workdir-row"
                data-workdir-id={w.id}
                data-kind={w.kind}
                data-status={w.status}
                data-blocked={String(blocked)}
                data-gc-reason={w.gc_blocked_reason ?? ""}
              >
                <div className="wd__main">
                  <span className="wd__kind" data-testid="workdir-kind">{WORKDIR_KIND_LABEL[w.kind]}</span>
                  <code className="wd__path" data-testid="workdir-path">{w.path_or_ref}</code>
                  {w.branch && <span className="wd__branch" data-testid="workdir-branch">{w.branch}</span>}
                  <span className="wd__spacer" />
                  <span className="wd__status" data-testid="workdir-status">{WORKDIR_STATUS_LABEL[w.status]}</span>
                </div>
                <div className="wd__meta small muted-3">
                  <span data-testid="workdir-owner">{owner}</span>
                  {w.session?.title && <> · <Link href={`/sessions/${w.session_id}`}>{w.session.title}</Link></>}
                  {" · "}<span data-testid="workdir-size">{formatBytes(w.disk_bytes)}</span>
                  {" · 마지막 사용 "}{relativeTime(w.last_used_at)}
                  {" · 보존 "}<span data-testid="workdir-retention">{retentionLabel(w)}</span>
                </div>

                {gc && (
                  <div className="wd__gc" data-testid="workdir-gc-block">
                    <b data-testid="workdir-gc-title">{gc.title}</b>
                    <div data-testid="workdir-gc-next">{gc.next}</div>
                  </div>
                )}

                {asked && (
                  <div className="wd__refused" role="alert" data-testid="workdir-refused">
                    <b>삭제가 막혔습니다</b> — {asked}
                    <div className="small">
                      그래도 지우면 이 작업 공간의 변경을 되돌릴 수 없습니다.
                      {w.kind === "worktree" ? " 브랜치는 남습니다." : ""}
                    </div>
                  </div>
                )}

                {w.status !== "deleted" && (
                  <div className="wd__actions">
                    {asked ? (
                      <>
                        <button
                          type="button"
                          className="btn btn--sm wd__danger"
                          disabled={busyId === w.id}
                          onClick={() => void remove(w, true)}
                          data-testid="workdir-delete-force"
                        >
                          확인하고 삭제
                        </button>
                        <button type="button" className="btn btn--sm btn--ghost" onClick={() => setRefused((c) => { const n = { ...c }; delete n[w.id]; return n; })} data-testid="workdir-delete-abort">
                          그만두기
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        className="btn btn--sm"
                        disabled={busyId === w.id}
                        title={blocked ? "미병합 커밋 또는 미커밋 변경이 있습니다 — 삭제를 시도하면 사유를 보여 줍니다" : undefined}
                        onClick={() => void remove(w, false)}
                        data-testid="workdir-delete"
                      >
                        삭제
                      </button>
                    )}
                    {w.kind === "worktree" && (
                      <span className="small muted-3" data-testid="workdir-branch-note">워크트리만 지웁니다 — 브랜치는 남습니다.</span>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <p className="small muted-3" style={{ marginTop: 12 }} data-testid="workdirs-foot">
        보존 기한이 지나면 자동으로 정리됩니다. 지금 지우려면 행에서 삭제하세요.
      </p>

      <style>{`
        .wd__quota { margin: 8px 0 12px; }
        .wd__quota-text { font-size: 12px; color: var(--ink-2); }
        .wd__bar { height: 6px; border-radius: 3px; background: var(--surface); margin-top: 4px; overflow: hidden; }
        .wd__bar > span { display: block; height: 100%; background: var(--ink-3); }
        .wd__quota--full .wd__bar > span { background: var(--s-fail); }
        .wd__quota-note { margin-top: 4px; font-size: 12px; color: var(--s-fail-text); }
        .wd__list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
        .wd__row { border: 1px solid var(--line); border-radius: 10px; padding: 10px; display: flex; flex-direction: column; gap: 4px; }
        .wd__main { display: flex; align-items: center; gap: 8px; font-size: 13px; flex-wrap: wrap; }
        .wd__kind { border: 1px solid var(--line); border-radius: 999px; padding: 1px 8px; font-size: 11px; color: var(--ink-2); }
        .wd__path { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
        .wd__branch { font-size: 11px; color: var(--ink-3); }
        .wd__spacer { flex: 1; }
        .wd__status { font-size: 11px; color: var(--ink-2); }
        .wd__meta { display: flex; gap: 4px; flex-wrap: wrap; align-items: center; }
        .wd__gc {
          margin-top: 4px; padding: 6px 8px; border-radius: 8px; font-size: 12px;
          border: 1px solid var(--s-wait); color: var(--s-wait-text);
          background: color-mix(in srgb, var(--s-wait) var(--soft-alpha), transparent);
        }
        .wd__refused {
          margin-top: 4px; padding: 6px 8px; border-radius: 8px; font-size: 12px;
          border: 1px solid var(--s-fail); color: var(--s-fail-text);
          background: color-mix(in srgb, var(--s-fail) var(--soft-alpha), transparent);
        }
        .wd__actions { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; margin-top: 4px; }
        .wd__danger { border-color: var(--s-fail); color: var(--s-fail-text); }
        .wd__toast { margin: 0 0 8px; font-size: 12px; color: var(--s-done-text); }
      `}</style>
    </div>
  );
}
