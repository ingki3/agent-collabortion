"use client";
/**
 * S7 상단 액션(SCREEN §4.5 "상단 액션") — 일시정지 / 재개 · 종료(`manual`) · 참여 에이전트 관리 · Director 교체.
 *
 * 권한은 **숨기지 않고 비활성 + 사유**다(SCREEN §1 원칙 4 · §7 "권한 없음"). S7-P(일반 멤버)와 S7-D(deputy)가
 * 같은 화면을 보되 버튼만 잠긴다 — U10-1("왜 못 누르지"가 아니라 사유를 읽는다), U9-6(deputy 도 일시정지는 "Director만").
 *
 * **deputy 의 비대칭**(t-3): 취소(lane 중단)는 즉시지만 세션 일시정지·종료는 Director 만이다. lane 버튼은
 * `LaneCard` 가, 여기 버튼은 세션 역할이 정한다.
 *
 * 종료는 확인이 필요하고 **진행 중 lane 개수를 명시**한다(계약 completeSession: `confirm` 없이 진행 중 lane 이
 * 있으면 `409 running_lanes`).
 */
import { useState } from "react";
import "./session-actions.css";
import type { Member, Session } from "@/lib/api/types";

export type SessionActionKey = "pause" | "resume" | "complete" | "cancel" | "participants" | "director";

export interface SessionActionsProps {
  session: Session;
  /** 진행 중 lane 수 — 종료 확인 문구가 개수를 명시한다. */
  runningLanes: number;
  members: Member[];
  onPause: () => Promise<void> | void;
  onResume: () => Promise<void> | void;
  onComplete: (confirm: boolean) => Promise<void> | void;
  onCancel: (reason: string) => Promise<void> | void;
  onOpenParticipants: () => void;
  onChangeDirector: (userId: string) => Promise<void> | void;
  busy?: boolean;
}

/**
 * 동작별 가능 여부와 사유. **세션 역할이 정한다**(SCREEN §2.3 두 번째 표) — 워크스페이스 역할이 아니다.
 * Director 교체만 예외로 owner·admin 도 할 수 있으나 그 판정은 서버가 하고(계약 changeDirector),
 * 화면은 Director 기준으로 그린 뒤 403 을 그대로 보여 준다.
 */
export function actionGate(s: Session, key: SessionActionKey): { allowed: boolean; reason?: string } {
  const isDirector = s.my_role === "director";
  const closed = s.status === "completed" || s.status === "cancelled";
  if (closed) return { allowed: false, reason: "종료된 세션입니다" };
  if (!isDirector) {
    return {
      allowed: false,
      reason: s.my_role === "deputy" ? "Director 만 할 수 있습니다 (deputy 는 lane 중단만 즉시 가능)" : "Director 만 할 수 있습니다",
    };
  }
  switch (key) {
    case "pause":
      return s.status === "active" ? { allowed: true } : { allowed: false, reason: "active 세션만 일시정지할 수 있습니다" };
    case "resume":
      if (s.status !== "paused") return { allowed: false, reason: "일시정지된 세션만 재개할 수 있습니다" };
      // 런타임 오프라인은 재개가 아니라 재바인딩·종료다(계약 resumeSession 409, FR-9.2).
      return s.paused_reason === "runtime_offline"
        ? { allowed: false, reason: "런타임 오프라인은 재바인딩하거나 세션을 종료해야 합니다" }
        : { allowed: true };
    default:
      return { allowed: true };
  }
}

export function SessionActions(props: SessionActionsProps) {
  const s = props.session;
  const [dialog, setDialog] = useState<null | "complete" | "cancel" | "director">(null);
  const [reason, setReason] = useState("");
  const [nextDirector, setNextDirector] = useState("");

  const btn = (key: SessionActionKey, label: string, onClick: () => void, primary = false) => {
    const gate = actionGate(s, key);
    return (
      <button
        key={key}
        type="button"
        className={`btn btn--sm${primary ? " btn--primary" : ""}`}
        disabled={!gate.allowed || props.busy}
        title={gate.reason}
        onClick={onClick}
        data-testid={`session-${key}`}
        data-allowed={gate.allowed ? "true" : "false"}
      >
        {label}
      </button>
    );
  };

  return (
    <div className="s7-actions" data-testid="session-actions" data-role={s.my_role}>
      {s.status === "paused"
        ? btn("resume", "재개", () => void props.onResume(), true)
        : btn("pause", "일시정지", () => void props.onPause())}
      {btn("complete", "종료", () => setDialog("complete"))}
      {btn("participants", "참여자", props.onOpenParticipants)}
      {btn("director", "Director 교체", () => setDialog("director"))}
      {btn("cancel", "세션 취소", () => setDialog("cancel"))}

      {dialog === "complete" && (
        <div className="s7-actions__dialog" role="dialog" aria-label="세션 종료 확인" data-testid="complete-confirm">
          <p className="small">
            이 세션을 종료합니다(<code>manual</code>) — 종료 조건과 무관하게 Director 가 직접 끝냅니다.
          </p>
          {props.runningLanes > 0 && (
            <p className="small s7-actions__warn" data-testid="complete-running-lanes">
              진행 중 lane 이 <b>{props.runningLanes}개</b> 있습니다. 계속하면 취소됩니다.
            </p>
          )}
          <div className="row">
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={props.busy}
              onClick={() => {
                void props.onComplete(props.runningLanes > 0);
                setDialog(null);
              }}
              data-testid="complete-confirm-yes"
            >
              종료
            </button>
            <button type="button" className="btn btn--sm" onClick={() => setDialog(null)}>취소</button>
          </div>
        </div>
      )}

      {dialog === "cancel" && (
        <div className="s7-actions__dialog" role="dialog" aria-label="세션 취소 확인" data-testid="cancel-session-confirm">
          <p className="small">
            세션을 <b>취소</b>합니다. 진행 중 턴은 편집 완료를 최대 30초 기다린 뒤 취소되고, 대기 중 task 는 바로 취소됩니다.
          </p>
          <p className="small muted-3">v1 에는 삭제가 없습니다 — 취소된 세션은 보관됩니다(SCREEN §8.2 Q2).</p>
          <label className="s7-actions__field">
            <span className="small">사유(선택)</span>
            <input className="input" value={reason} onChange={(e) => setReason(e.target.value)} data-testid="cancel-session-reason" />
          </label>
          <div className="row">
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={props.busy}
              onClick={() => {
                void props.onCancel(reason.trim());
                setDialog(null);
              }}
              data-testid="cancel-session-yes"
            >
              세션 취소
            </button>
            <button type="button" className="btn btn--sm" onClick={() => setDialog(null)}>돌아가기</button>
          </div>
        </div>
      )}

      {dialog === "director" && (
        <div className="s7-actions__dialog" role="dialog" aria-label="Director 교체" data-testid="director-dialog">
          <p className="small">
            새 Director 를 고릅니다. 시스템 메시지로 남고, 열린 HITL 의 <code>director</code> 승인자가 새 Director 로 바뀝니다.
          </p>
          <label className="s7-actions__field">
            <span className="small">새 Director</span>
            <select
              className="select"
              value={nextDirector}
              onChange={(e) => setNextDirector(e.target.value)}
              data-testid="director-select"
            >
              <option value="">고르세요</option>
              {props.members
                .filter((m) => m.user.id !== s.director_user_id)
                .map((m) => (
                  <option key={m.user.id} value={m.user.id}>
                    {m.user.display_name} · {m.role}
                  </option>
                ))}
            </select>
          </label>
          <div className="row">
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={props.busy || !nextDirector}
              onClick={() => {
                void props.onChangeDirector(nextDirector);
                setDialog(null);
              }}
              data-testid="director-confirm"
            >
              교체
            </button>
            <button type="button" className="btn btn--sm" onClick={() => setDialog(null)}>취소</button>
          </div>
        </div>
      )}
    </div>
  );
}

export default SessionActions;
