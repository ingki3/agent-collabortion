"use client";
/**
 * S26 미션 제안 확인(`/rooms/:id?work_proposal=:id`, 다이얼로그) — SCREEN v0.19.2 §4.9, T-R2-W3.
 *
 * 에이전트는 미션을 **열지 못하고 제안만 한다**(FR-2A.1 — 무한 자기 위임을 막는다). 사람이 확인해야 열린다.
 *   - 제안한 에이전트·시각 · 제안 goal · 제안 근거 한 줄(`rationale`) · 트리거 메시지 인용(호출부가 넘기면).
 *   - 「이대로 열기」 — S21 을 goal 이 채워진 채로(고칠 수 없게) · 「고쳐서 열기」 — 같은 S21, 편집 가능 · 「거절」 — 사유(선택) 한 칸.
 *     거절은 방 타임라인에 남아 **제안한 에이전트도 안다**(같은 제안을 반복하지 않게).
 *   - 여는 사람이 Director 다 — 방 기본값도, 제안한 에이전트도 아니다(서버가 강제한다).
 *   - **이미 처리된 제안**(빈 상태): 「이 제안은 〈서연〉 님이 2026-09-21 에 열었습니다」 + 그 미션 링크, 또는 「…거절했습니다」 + 사유.
 *     제안에는 기한이 없다 — 「만료됨」을 그리지 않는다. 다른 사람이 먼저 처리하면 `work_proposal.resolved` 로 이 상태로 바뀐다.
 * 권한: 방 참여자(사람) 누구나 — 아니면 버튼 비활성 + 사유(서버 403 not_participant 와 같은 선).
 */
import { useCallback, useEffect, useId, useState } from "react";
import Link from "next/link";
import { CreateWorkDialog } from "./CreateWorkDialog";
import { DisabledHint } from "./PageHead";
import { RoomDialogShell, useRoomEvents } from "./RoomDialogShell";
import { api, errorMessage, isApiError, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { relativeTime } from "@/lib/time";
import { COMMON, personName, PROPOSAL, ymd, type Work, type WorkProposal } from "@/lib/room-dialogs";
import type { Message, Room } from "@/lib/api/types";

export interface WorkProposalDialogProps {
  roomId: string;
  proposalId: string;
  /** 트리거 메시지를 찾는다 — 호출부(S7)가 타임라인에서(서버 `getMessage` 는 아직 없다). 못 찾으면 인용을 그리지 않는다. */
  findMessage?: (id: string) => Message | null | undefined;
  onOpened: (work: Work) => void;
  onClose: () => void;
}

const PROPOSAL_EVENTS = ["work_proposal.resolved", "work_proposal.created", "room.updated"] as const;

export function WorkProposalDialog({ roomId, proposalId, findMessage, onOpened, onClose }: WorkProposalDialogProps) {
  const { workspace } = useAuth();
  const [prop, setProp] = useState<WorkProposal | null>(null);
  const [room, setRoom] = useState<Room | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [opening, setOpening] = useState<null | "as_is" | "edit">(null);
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const base = useId();

  const load = useCallback(async () => {
    try {
      const [p, r] = await Promise.all([api.get("/work-proposals/{workProposalId}", { path: { workProposalId: proposalId } }), api.get("/rooms/{roomId}", { path: { roomId } })]);
      setProp(p);
      setRoom(r);
      setError(null);
      setNotFound(p.room_id !== roomId);
    } catch (e) {
      if (isApiError(e) && e.status === 404) setNotFound(true);
      else setError(errorMessage(e));
    }
  }, [proposalId, roomId]);
  useEffect(() => {
    void load();
  }, [load]);
  useRoomEvents(workspace?.id, roomId, PROPOSAL_EVENTS, () => void load());

  async function reject() {
    setBusy(true);
    setError(null);
    try {
      const res = await api.post("/work-proposals/{workProposalId}/resolution", {
        path: { workProposalId: proposalId },
        body: { action: "reject", ...(reason.trim() ? { reason: reason.trim() } : {}) },
        idempotencyKey: newIdempotencyKey(),
      });
      setProp(res.proposal);
      setRejecting(false);
    } catch (e) {
      setError(errorMessage(e));
      // 다른 사람이 먼저 처리했으면(409 already_resolved) 처리된 상태를 다시 읽어 빈 상태로 보인다.
      if (isApiError(e) && e.code === "already_resolved") void load();
    } finally {
      setBusy(false);
    }
  }

  if (opening && prop) {
    return <CreateWorkDialog roomId={roomId} mode="proposal" proposal={prop} goalLocked={opening === "as_is"} onOpened={onOpened} onClose={() => setOpening(null)} />;
  }

  const canAct = !!room?.my_room_role;
  const triggerMessage = prop?.trigger_message_id ? findMessage?.(prop.trigger_message_id) : null;
  const hint = `${base}-hint`;
  const decider = personName(prop?.decided_by);

  return (
    <RoomDialogShell title={PROPOSAL.title} testId="rd-proposal" onClose={onClose} busy={busy}>
      {notFound ? (
        <p className="rd-hint" data-testid="rd-proposal-not-found">{PROPOSAL.not_found}</p>
      ) : !prop ? (
        error ? <p className="problem" role="alert">{error}</p> : <p className="rd-hint">{COMMON.loading}</p>
      ) : (
        <>
          <p className="rd-section__note" data-testid="rd-proposal-by">
            {PROPOSAL.proposed_by(prop.agent.name)} · {relativeTime(prop.created_at)}
          </p>
          <div className="rd-section">
            <span className="rd-field__label">{PROPOSAL.goal}</span>
            <blockquote className="rd-quote" data-testid="rd-proposal-goal">{prop.goal}</blockquote>
          </div>
          <div className="rd-section">
            <span className="rd-field__label">{PROPOSAL.rationale}</span>
            <p className="confirm-dlg__p" data-testid="rd-proposal-rationale">{prop.rationale}</p>
          </div>
          {triggerMessage && (
            <div className="rd-section">
              <span className="rd-field__label">{PROPOSAL.trigger}</span>
              <blockquote className="rd-quote">{triggerMessage.content}</blockquote>
            </div>
          )}
          {prop.status !== "open" ? (
            <div className="notice notice--info" role="status" data-testid="rd-proposal-resolved" data-status={prop.status}>
              <p className="confirm-dlg__p">
                {prop.status === "accepted" ? PROPOSAL.accepted(decider, ymd(prop.decided_at)) : PROPOSAL.rejected(decider, ymd(prop.decided_at))}
              </p>
              {prop.status === "accepted" && prop.work_id && (
                <Link href={`/rooms/${roomId}?work=${prop.work_id}`} data-testid="rd-proposal-go-work">{PROPOSAL.go_work}</Link>
              )}
              {prop.status === "rejected" && prop.reject_reason && (
                <p className="confirm-dlg__p" data-testid="rd-proposal-reject-reason">{PROPOSAL.reason_prefix}{prop.reject_reason}</p>
              )}
            </div>
          ) : (
            <>
              <p className="rd-hint">{PROPOSAL.director_note}</p>
              {rejecting && (
                <label className="rd-field">
                  <span className="rd-field__label">{PROPOSAL.reject_reason}</span>
                  <textarea className="textarea" rows={2} value={reason} placeholder={PROPOSAL.reject_placeholder} onChange={(e) => setReason(e.target.value)} data-testid="rd-proposal-reason" />
                  <span className="rd-hint">{PROPOSAL.reject_note}</span>
                </label>
              )}
              {error && <p className="problem confirm-dlg__problem" role="alert" data-testid="rd-proposal-error">{error}</p>}
              <div className="confirm-dlg__actions">
                {rejecting ? (
                  <>
                    <button type="button" className="btn" disabled={busy} onClick={() => setRejecting(false)}>{COMMON.cancel}</button>
                    <button type="button" className="btn confirm-dlg__danger" disabled={busy} onClick={() => void reject()} data-testid="rd-proposal-reject-confirm">
                      {busy ? PROPOSAL.rejecting : PROPOSAL.reject_confirm}
                    </button>
                  </>
                ) : (
                  <>
                    <button type="button" className="btn confirm-dlg__danger" aria-disabled={!canAct || undefined} aria-describedby={!canAct ? hint : undefined} onClick={() => canAct && setRejecting(true)} data-testid="rd-proposal-reject">
                      {PROPOSAL.reject}
                    </button>
                    <button type="button" className="btn" aria-disabled={!canAct || undefined} aria-describedby={!canAct ? hint : undefined} onClick={() => canAct && setOpening("edit")} data-testid="rd-proposal-edit">
                      {PROPOSAL.edit}
                    </button>
                    <button type="button" className="btn btn--primary" aria-disabled={!canAct || undefined} aria-describedby={!canAct ? hint : undefined} onClick={() => canAct && setOpening("as_is")} data-testid="rd-proposal-accept">
                      {PROPOSAL.accept}
                    </button>
                  </>
                )}
              </div>
              {!canAct && <DisabledHint id={hint}>{PROPOSAL.not_participant}</DisabledHint>}
            </>
          )}
        </>
      )}
    </RoomDialogShell>
  );
}

export default WorkProposalDialog;
