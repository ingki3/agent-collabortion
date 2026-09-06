"use client";
/**
 * S17 런타임 재바인딩 다이얼로그(SCREEN §4.9 · FR-9.2 · E14-03~07 · U12 4·5).
 *
 * 데이터 유실을 다루는 다이얼로그라 명세가 있다. 네 구역을 그대로 그린다:
 *   1) 상황  — "노트북이 N일간 오프라인입니다. 이 세션은 M월 D일부터 일시정지 상태입니다"
 *   2) 대상  — 후보는 **서버가 정한다**(`listRuntimeCandidates?session_id=`). 격리가 후보를 제한하고,
 *              `worktree` 는 **경로가 아니라 remote URL** 로 같은 저장소를 판정한다(E14-04·05).
 *              후보가 아닌 런타임도 비활성 + 사유로 그린다 — 사라진 선택지는 이유를 말하지 못한다.
 *   3) 유실 경고 — `worktree` 일 때만. diff 아티팩트를 **제출 순서 그대로** 미리 보여 준다(E14-06:
 *              순서가 뒤바뀐 diff 는 충돌한다). 확인 체크박스가 계약의 `acknowledge_loss` 다 —
 *              worktree 인데 false 면 서버가 422 로 막는다.
 *   4) 선택  — 재바인딩 / 세션 종료(`cancelled`, E14-07) / 취소.
 *
 * 화면은 판정하지 않는다: 후보 여부도 `acknowledge_loss` 강제도 서버가 다시 검사한다(E14-05 "직접 호출도
 * 막는다"). 여기 있는 비활성은 편의지 방어가 아니다.
 */
import { useCallback, useEffect, useState } from "react";
import "./rebind-dialog.css";
import { api, errorMessage } from "@/lib/api/client";
import { relativeTime } from "@/lib/time";
import type { Artifact, IsolationKind, RuntimeCandidate, Session } from "@/lib/api/types";

export interface RebindDialogProps {
  /** 재바인딩 대상 세션. `paused(runtime_offline)` 가 아니면 서버가 409 로 막는다. */
  session: Pick<Session, "id" | "title" | "isolation" | "status"> & {
    workspace_id: string;
    paused_detail?: Session["paused_detail"];
    runtime?: { name?: string | null } | null;
  };
  /** 실행 후 호출부가 목록·배너를 다시 읽는다. */
  onDone?: (result: "rebound" | "cancelled") => void;
  onClose: () => void;
}

/** "N일간 오프라인입니다" — 상황 문장의 첫 줄(SCREEN §4.9 상황 칸). */
export function offlineSentence(runtimeName: string | null | undefined, offlineSince: string | null | undefined, pausedAt: string | null | undefined): string {
  const who = runtimeName ?? "이 세션의 런타임";
  const days = offlineSince ? Math.max(0, Math.floor((Date.now() - Date.parse(offlineSince)) / 86_400_000)) : null;
  const head = days == null ? `${who}이 오프라인입니다` : `${who}이 ${days}일간 오프라인입니다`;
  if (!pausedAt) return `${head}.`;
  const d = new Date(pausedAt);
  return `${head}. 이 세션은 ${d.getMonth() + 1}월 ${d.getDate()}일부터 일시정지 상태입니다.`;
}

/**
 * 유실 경고 문구(SCREEN §4.9 · U12 5). **`worktree` 일 때만** 나온다 — `none` 은 잃을 워크트리가 없다.
 * 아티팩트 수를 문장에 넣는 이유는 U12 성공 기준이 "커밋은 없어진다"를 말할 수 있는가이기 때문이다.
 */
export function lossWarning(diffCount: number): string {
  return (
    `완료된 lane 의 코드도 원래 머신의 브랜치에만 있습니다. 새 머신에서는 이 세션의 diff 아티팩트 ` +
    `${diffCount}개를 순서대로 적용해 복구합니다. 커밋 이력은 복원되지 않습니다.`
  );
}

export function RebindDialog({ session, onDone, onClose }: RebindDialogProps) {
  const isolation: IsolationKind = session.isolation?.kind ?? "none";
  const worktree = isolation === "worktree";
  const [candidates, setCandidates] = useState<RuntimeCandidate[] | null>(null);
  const [diffs, setDiffs] = useState<Artifact[] | null>(null);
  const [target, setTarget] = useState<string>("");
  const [ack, setAck] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmEnd, setConfirmEnd] = useState(false);

  const load = useCallback(async () => {
    try {
      // 후보는 세션에서 격리·저장소를 읽어 서버가 고른다 — 화면이 remote URL 을 다시 비교하지 않는다.
      const r = await api.get("/workspaces/{workspaceId}/runtime-candidates", {
        path: { workspaceId: session.workspace_id },
        query: { isolation, session_id: session.id },
      });
      setCandidates(r.candidates);
      setTarget((cur) => cur || (r.candidates.find((c) => c.eligible)?.runtime.id ?? ""));
    } catch (e) {
      setError(errorMessage(e));
      setCandidates([]);
    }
  }, [session.workspace_id, session.id, isolation]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!worktree) return;
    // 제출 순서 = 재적용 순서(계약 listArtifacts "제출 순"). 화면이 다시 정렬하면 순서를 잃는다.
    api
      // 계약 listArtifacts 는 **배열**이다(Page 봉투가 아니다) — 그리고 그 순서가 제출 순서다.
      .get("/sessions/{sessionId}/artifacts", { path: { sessionId: session.id }, query: { type: "diff" } })
      .then((arr) => setDiffs(arr ?? []), () => setDiffs([]));
  }, [worktree, session.id]);

  const eligible = (candidates ?? []).filter((c) => c.eligible);
  const chosen = (candidates ?? []).find((c) => c.runtime.id === target);
  const blocked = !target || (worktree && !ack) || busy;

  async function rebind() {
    setBusy(true);
    setError(null);
    try {
      await api.post("/sessions/{sessionId}/rebind", {
        path: { sessionId: session.id },
        // worktree 면 계약이 `acknowledge_loss` 를 요구한다(false 면 422). none 은 보내지 않아도 된다.
        body: { runtime_id: target, ...(worktree ? { acknowledge_loss: ack } : {}) },
      });
      onDone?.("rebound");
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function endSession() {
    setBusy(true);
    setError(null);
    try {
      // 종료는 `cancelled` 다(E14-07) — 아티팩트는 서버에 남아 회수된다.
      await api.post("/sessions/{sessionId}/cancel", { path: { sessionId: session.id }, body: { reason: "런타임 오프라인 — 재바인딩 대신 종료" } });
      onDone?.("cancelled");
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rebind__scrim" role="presentation" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="rebind" role="dialog" aria-modal="true" aria-label="런타임 재바인딩" data-testid="rebind-dialog" data-isolation={isolation}>
        <div className="rebind__head">
          <b>런타임 재바인딩</b>
          <span className="rebind__spacer" />
          <button type="button" className="rebind__x" aria-label="닫기" onClick={onClose} data-testid="rebind-close">✕</button>
        </div>

        {/* 1 상황 */}
        <p className="rebind__situation" data-testid="rebind-situation">
          {offlineSentence(session.runtime?.name, session.paused_detail?.runtime?.offline_since, session.paused_detail?.paused_at)}
        </p>
        <p className="rebind__sub">세션 <b>{session.title}</b></p>

        {/* 2 대상 선택 */}
        <div className="rebind__section">
          <div className="rebind__label">
            새 런타임
            <span className="rebind__hint">
              {worktree
                ? "worktree 격리 — 같은 remote URL 의 저장소를 가진 머신만 후보입니다(경로 문자열이 아닙니다)."
                : "격리 없음 — 온라인 런타임 전부가 후보입니다."}
            </span>
          </div>
          {candidates === null ? (
            <p className="muted small">후보를 확인하는 중…</p>
          ) : eligible.length === 0 ? (
            <p className="problem" data-testid="rebind-no-candidate">
              후보가 없습니다 — {worktree ? "이 세션의 저장소와 같은 remote URL 을 가진 온라인 머신이 없습니다." : "온라인 런타임이 없습니다."}{" "}
              컴퓨터를 연결하거나 세션을 종료하세요.
            </p>
          ) : (
            <ul className="rebind__cands" data-testid="rebind-candidates">
              {candidates.map((c) => (
                <li key={c.runtime.id} data-testid="rebind-candidate" data-eligible={String(c.eligible)} data-runtime-id={c.runtime.id}>
                  <label className={c.eligible ? "" : "rebind__cand--off"}>
                    <input
                      type="radio"
                      name="rebind-target"
                      disabled={!c.eligible || busy}
                      checked={target === c.runtime.id}
                      onChange={() => setTarget(c.runtime.id)}
                      data-testid="rebind-candidate-radio"
                    />{" "}
                    <b>{c.runtime.name}</b>
                    <span className="small muted-3">
                      {" "}· {c.runtime.status === "online" ? "온라인" : `오프라인 ${relativeTime(c.runtime.last_seen_at)}`}
                      {c.matched_repo ? ` · ${c.matched_repo.path}` : ""}
                    </span>
                    {!c.eligible && (
                      <div className="rebind__reason" data-testid="rebind-candidate-reason">{c.reason ?? "후보가 아닙니다"}</div>
                    )}
                  </label>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* 3 유실 경고 — worktree 일 때만 */}
        {worktree && (
          <div className="rebind__loss" data-testid="rebind-loss">
            <p className="rebind__loss-text">{lossWarning(diffs?.length ?? 0)}</p>
            {diffs && diffs.length > 0 && (
              <ol className="rebind__order" data-testid="rebind-artifact-order">
                {diffs.map((a, i) => (
                  // 번호는 `<ol>` 이 붙인다 — 손으로 한 번 더 쓰면 "1. 1." 이 된다.
                  <li key={a.id} data-testid="rebind-artifact" data-artifact-id={a.id} data-order={i + 1}>
                    {a.name} <span className="small muted-3">v{a.version}{a.submitted_by?.agent_name ? ` · @${a.submitted_by.agent_name}` : ""}</span>
                  </li>
                ))}
              </ol>
            )}
            {diffs && diffs.length === 0 && (
              <p className="small muted-3" data-testid="rebind-no-artifact">
                제출된 diff 아티팩트가 없습니다 — 새 workdir 은 비어 있는 채로 시작합니다.
              </p>
            )}
            <label className="rebind__ack">
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} disabled={busy} data-testid="rebind-ack" />{" "}
              위 내용을 확인했습니다(커밋 이력은 복원되지 않습니다).
            </label>
          </div>
        )}

        {error && <p className="problem" role="alert" data-testid="rebind-error">{error}</p>}

        {/* 4 선택 */}
        <div className="rebind__actions" data-testid="rebind-actions">
          <button
            type="button"
            className="btn btn--sm btn--primary"
            disabled={blocked}
            title={worktree && !ack ? "유실 경고를 확인해야 재바인딩할 수 있습니다" : !target ? "새 런타임을 고르세요" : undefined}
            onClick={() => void rebind()}
            data-testid="rebind-submit"
          >
            {chosen ? `${chosen.runtime.name} 으로 재바인딩` : "재바인딩"}
          </button>
          {confirmEnd ? (
            <button type="button" className="btn btn--sm rebind__danger" disabled={busy} onClick={() => void endSession()} data-testid="rebind-end-confirm">
              정말 종료합니다(되돌릴 수 없습니다)
            </button>
          ) : (
            <button type="button" className="btn btn--sm" disabled={busy} onClick={() => setConfirmEnd(true)} data-testid="rebind-end">
              세션 종료
            </button>
          )}
          <button type="button" className="btn btn--sm btn--ghost" disabled={busy} onClick={onClose} data-testid="rebind-cancel">취소</button>
        </div>
        <p className="rebind__foot">
          아티팩트·메시지·결정 기록은 서버에 있어 대화 맥락은 그대로입니다. 진행 중이던 lane 은 콜드 스타트합니다.
        </p>
      </div>
    </div>
  );
}

export default RebindDialog;
