"use client";
/**
 * S11 Runtimes(SCREEN §4.8 · FR-9 · FR-9.2 · U12).
 *
 * 카드가 그리는 것은 `RuntimeCard` 가 맡고, 이 화면은 **행동 셋**을 붙인다:
 *   · Workdir 관리(S13) — 이 머신에 남은 작업 공간.
 *   · 세션 재바인딩(S17) — 유예를 넘겨 `paused(runtime_offline)` 가 된 세션은 **세션마다 따로** 결정한다
 *     (SCREEN §4.9 "여러 세션이 걸렸으면 세션마다 따로"). 그래서 버튼 하나가 아니라 목록이다.
 *   · 삭제 — 활성 세션이 걸려 있으면 서버가 `409 runtime_has_active_sessions` 로 막고 `Problem.sessions[]`
 *     에 대상 세션을 준다(E14-08). 그 목록을 **링크로** 그린다: "먼저 재바인딩/종료" 는 어느 세션인지
 *     알아야 실행할 수 있는 지시다.
 *
 * `active_sessions` 는 목록 응답에 없다 — 필요할 때만 `getRuntime` 으로 한 장 더 읽는다(카드마다 미리 읽으면
 * 런타임 수만큼 왕복이 늘고, 대부분의 카드는 그 값을 쓰지 않는다).
 */
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { RebindDialog } from "@/components/RebindDialog";
import { RuntimeCard, graceView } from "@/components/RuntimeCard";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type { Problem, Runtime, RuntimeDetail, Session, StreamEvent } from "@/lib/api/types";

/** `Problem.sessions[]` 항목 — 계약은 `{id, title}` 이고 상태는 싣지 않는다(SessionRef 가 아니다). */
type BlockingSession = NonNullable<Problem["sessions"]>[number];

/** 재바인딩 다이얼로그가 받는 세션 모양 — 상세를 한 번 읽어 채운다. */
type RebindTarget = React.ComponentProps<typeof RebindDialog>["session"];

export default function RuntimesPage() {
  const { workspace, canManage } = useAuth();
  const [items, setItems] = useState<Runtime[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  /** 펼친 런타임의 상세(활성 세션 목록). 요청 시에만 읽는다. */
  const [detail, setDetail] = useState<Record<string, RuntimeDetail>>({});
  const [openId, setOpenId] = useState<string | null>(null);
  /** 삭제 409 의 차단 세션 목록(`Problem.sessions[]`). */
  const [blocked, setBlocked] = useState<Record<string, { detail: string; sessions: BlockingSession[] }>>({});
  const [busyId, setBusyId] = useState<string | null>(null);
  const [rebind, setRebind] = useState<RebindTarget | null>(null);

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
  useWorkspaceStream(workspace?.id, onEvent, { onResync: () => void load() });

  const openSessions = useCallback(async (rt: Runtime) => {
    if (openId === rt.id) {
      setOpenId(null);
      return;
    }
    setOpenId(rt.id);
    if (detail[rt.id]) return;
    try {
      const d = await api.get("/runtimes/{runtimeId}", { path: { runtimeId: rt.id } });
      setDetail((cur) => ({ ...cur, [rt.id]: d }));
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [openId, detail]);

  /** 재바인딩은 세션 단위다 — 세션 상세를 읽어야 격리·정지 사유를 다이얼로그가 그린다. */
  async function openRebind(sessionId: string) {
    try {
      const s: Session = await api.get("/sessions/{sessionId}", { path: { sessionId } });
      setRebind({
        id: s.id,
        title: s.title,
        isolation: s.isolation,
        status: s.status,
        workspace_id: s.workspace_id,
        paused_detail: s.paused_detail,
        runtime: s.runtime ?? null,
      });
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  async function removeRuntime(rt: Runtime) {
    setBusyId(rt.id);
    setError(null);
    try {
      await api.delete("/runtimes/{runtimeId}", { path: { runtimeId: rt.id } });
      setItems((cur) => (cur ? cur.filter((x) => x.id !== rt.id) : cur));
      setBlocked((cur) => {
        const n = { ...cur };
        delete n[rt.id];
        return n;
      });
    } catch (e) {
      if (isApiError(e) && e.status === 409) {
        setBlocked((cur) => ({
          ...cur,
          [rt.id]: {
            detail: e.problem.detail ?? "이 컴퓨터에 걸린 세션이 있습니다",
            sessions: e.problem.sessions ?? [],
          },
        }));
      } else {
        setError(errorMessage(e));
      }
    } finally {
      setBusyId(null);
    }
  }

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
          {items.map((rt) => {
            const grace = graceView(rt);
            const d = detail[rt.id];
            const block = blocked[rt.id];
            return (
              <RuntimeCard key={rt.id} rt={rt}>
                <div className="rt__actions" data-testid="runtime-actions">
                  <Link href={`/runtimes/${rt.id}/workdirs`} className="btn btn--sm" data-testid="runtime-workdirs">Workdir 관리</Link>
                  <button type="button" className="btn btn--sm" onClick={() => void openSessions(rt)} aria-expanded={openId === rt.id} data-testid="runtime-sessions-toggle">
                    {grace.expired ? "세션 재바인딩" : "걸린 세션"}
                  </button>
                  {canManage && (
                    <button type="button" className="btn btn--sm rt__danger" disabled={busyId === rt.id} onClick={() => void removeRuntime(rt)} data-testid="runtime-delete">
                      삭제
                    </button>
                  )}
                </div>

                {block && (
                  <div className="rt__blocked" role="alert" data-testid="runtime-delete-blocked">
                    <b>삭제할 수 없습니다</b> — {block.detail}
                    <div className="small">먼저 아래 세션을 재바인딩하거나 종료하세요.</div>
                    <ul className="rt__sessions">
                      {block.sessions.map((s, i) => (
                        <li key={s.id ?? i} data-testid="runtime-blocking-session">
                          {s.id ? <Link href={`/sessions/${s.id}`}>{s.title ?? s.id}</Link> : <span>{s.title ?? "세션"}</span>}
                          {s.id && (
                            <button type="button" className="rt__link" onClick={() => void openRebind(s.id!)} data-testid="runtime-blocking-rebind">재바인딩</button>
                          )}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                {openId === rt.id && (
                  <div className="rt__sessions-panel" data-testid="runtime-sessions">
                    {!d ? (
                      <p className="small muted">세션을 읽는 중…</p>
                    ) : d.active_sessions.length === 0 ? (
                      <p className="small muted-3">이 컴퓨터에 걸린 활성 세션이 없습니다.</p>
                    ) : (
                      <ul className="rt__sessions">
                        {d.active_sessions.map((s) => (
                          <li key={s.id} data-testid="runtime-active-session" data-session-status={s.status}>
                            <Link href={`/sessions/${s.id}`}>{s.title}</Link>
                            <span className="small muted-3"> · {s.status}</span>
                            {s.status === "paused" && (
                              <button type="button" className="rt__link" onClick={() => void openRebind(s.id)} data-testid="runtime-rebind">재바인딩</button>
                            )}
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                )}
              </RuntimeCard>
            );
          })}
        </div>
      )}

      {rebind && (
        <RebindDialog
          session={rebind}
          onClose={() => setRebind(null)}
          onDone={() => {
            setDetail({});
            setBlocked({});
            void load();
          }}
        />
      )}

      <style>{`
        .rt__actions { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 8px; }
        .rt__danger { border-color: var(--s-fail); color: var(--s-fail-text); }
        .rt__blocked {
          margin-top: 8px; padding: 6px 8px; border-radius: 8px; font-size: 12px;
          border: 1px solid var(--s-fail); color: var(--s-fail-text);
          background: color-mix(in srgb, var(--s-fail) var(--soft-alpha), transparent);
        }
        .rt__sessions { list-style: none; margin: 4px 0 0; padding: 0; display: flex; flex-direction: column; gap: 2px; font-size: 12px; }
        .rt__sessions li { display: flex; gap: 8px; align-items: baseline; }
        .rt__sessions-panel { margin-top: 8px; }
        .rt__link { border: 0; background: none; padding: 0; color: var(--ink-2); text-decoration: underline; cursor: pointer; font-size: 12px; }
      `}</style>
    </div>
  );
}
