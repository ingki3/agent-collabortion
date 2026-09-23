"use client";
/**
 * S8 방 층 카드 본문 둘(SCREEN v0.19.2 §4.14, T-R2-W4a) — `isolation_confirm` · `room_paused`.
 *
 * 둘 다 **방장에게 가는 승인 요청**(`hitl_request`, `approver_spec: room_owner`)이라 응답은 인박스 페이지의 `respondHitlRequest` 하나로 간다.
 * 그래도 `HitlBody` 를 쓰지 않는 이유: 버튼이 「승인/거절」이 아니라 **결과의 이름**이어야 한다 — 「워크트리로 나눔」·「이대로 진행」,
 * 「계속 승인」(미션 N개가 한꺼번에 다시 돈다). 승인·거절이라는 말로는 무엇이 되돌릴 수 없는지 카드만 읽고 모른다(F2).
 *
 * 권한은 서버가 준 `actions` 로만 판단한다 — `approve`·`approve_continue` 가 없으면 버튼을 비활성으로 두고 위임 줄(카드 머리)이 사유를 말한다.
 */
import { useState } from "react";
import "./hitl-card.css";
import "./inbox-item.css";
import { INBOX_V19, ISOLATION_CARD, ROOM_PAUSED_CARD, roomPausedLines, usd, type BlockedDetail } from "@/lib/inbox-v19";
import type { HitlResponse } from "@/lib/api/types";

export interface IsolationConfirmBodyProps {
  /** 서버 질문(roomgate.IsolationQuestion) — 컴퓨터·경로가 들어 있다. */
  question?: string | null;
  /** 컴퓨터 이름을 알면 §4.14 문장으로, 모르면 일반 문장으로. */
  computer?: string | null;
  /** `choice` 면 저장소 고르기(T-S-wt `AskRepo`) — 선택지는 `getHitlRequest` 의 `options`(Lead Q6). */
  hitlType?: string | null;
  options?: string[] | null;
  proposedDefault?: string | null;
  canRespond: boolean;
  busy?: boolean;
  /** 방 설정(S20) — 답이 아니라 이동이다. */
  settingsHref?: string | null;
  onRespond?: (body: HitlResponse) => Promise<void> | void;
}

export function IsolationConfirmBody({ question, computer, hitlType, options, proposedDefault, canRespond, busy, settingsHref, onRespond }: IsolationConfirmBodyProps) {
  const choice = hitlType === "choice";
  const [pick, setPick] = useState(proposedDefault ?? options?.[0] ?? "");
  const [error, setError] = useState<string | null>(null);
  const disabled = !canRespond || busy || !onRespond;
  async function send(body: HitlResponse) {
    setError(null);
    try {
      await onRespond?.(body);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }
  return (
    <div className="inbox-room" data-testid="inbox-isolation" data-kind={choice ? "choice" : "approval"}>
      {!choice && <p className="inbox-room__lead" data-testid="inbox-isolation-shared">{computer ? ISOLATION_CARD.shared(computer) : ISOLATION_CARD.shared_plain}</p>}
      {question && <p className="inbox-room__question" data-testid="inbox-isolation-question">{question}</p>}
      <p className="inbox-room__held" data-testid="inbox-isolation-held">{ISOLATION_CARD.held}</p>
      {!choice && <p className="inbox-room__default">{ISOLATION_CARD.proposed}</p>}
      {choice && (
        <label className="hitl__field">
          <span>{ISOLATION_CARD.choose}</span>
          <select className="select" value={pick} onChange={(e) => setPick(e.target.value)} disabled={disabled} data-testid="inbox-isolation-repo">
            {(options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        </label>
      )}
      <div className="inbox-room__actions">
        {choice ? (
          <button type="button" className="btn btn--sm btn--primary" disabled={disabled || !pick} onClick={() => void send({ answer: pick })} data-testid="inbox-isolation-choose">
            {ISOLATION_CARD.choose_send}
          </button>
        ) : (
          <>
            <button type="button" className="btn btn--sm btn--primary" disabled={disabled} onClick={() => void send({ approved: true })} data-testid="inbox-isolation-split">
              {ISOLATION_CARD.split}
            </button>
            <button type="button" className="btn btn--sm" disabled={disabled} onClick={() => void send({ approved: false, reason: ISOLATION_CARD.keep_reason })} data-testid="inbox-isolation-keep">
              {ISOLATION_CARD.keep}
            </button>
          </>
        )}
        {settingsHref && (
          <a className="btn btn--sm btn--ghost" href={settingsHref} data-testid="inbox-isolation-settings">{INBOX_V19.open_settings}</a>
        )}
      </div>
      {error && <p className="hitl__err" role="alert" data-testid="inbox-room-error">{error}</p>}
    </div>
  );
}

export interface RoomPausedBodyProps {
  /** 서버 질문(무엇이 멈췄는지 — 한도 이름). */
  question?: string | null;
  /** `getRoom` 의 `blocked_detail` — 카드당 1회(Lead Q5). 아직 못 읽었으면 null(수를 지어내지 않는다). */
  detail?: BlockedDetail | null;
  /** 요청 목적 — `budget` 이면 새 상한을 받는다(방 예산 승인은 새 상한이 곧 답이다, 서버 resumeRoomForBudget). */
  purpose?: string | null;
  canRespond: boolean;
  busy?: boolean;
  onRespond?: (body: HitlResponse) => Promise<void> | void;
}

export function RoomPausedBody({ question, detail, purpose, canRespond, busy, onRespond }: RoomPausedBodyProps) {
  const lines = roomPausedLines(detail);
  const budget = purpose === "budget";
  const spent = detail?.cost_usd ?? null;
  const [raise, setRaise] = useState("");
  const [error, setError] = useState<string | null>(null);
  const n = Number(raise);
  const tooLow = budget && raise.trim() !== "" && spent != null && Number.isFinite(n) && n <= spent;
  const disabled = !canRespond || busy || !onRespond;
  async function approve() {
    setError(null);
    try {
      await onRespond?.({ approved: true, ...(budget && raise.trim() !== "" && Number.isFinite(n) && n > 0 ? { budget_override_usd: n } : {}) });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }
  return (
    <div className="inbox-room" data-testid="inbox-room-paused" data-purpose={purpose ?? ""}>
      {question && <p className="inbox-room__question" data-testid="inbox-room-paused-question">{question}</p>}
      <p className="inbox-room__lead" data-testid="inbox-room-paused-stopped" data-count={detail?.works_stopped ?? 0}>{lines.stopped}</p>
      <p className="inbox-room__held" data-testid="inbox-room-paused-resume">{lines.resume}</p>
      {budget && (
        <label className="hitl__field">
          <span>{ROOM_PAUSED_CARD.budget_field}</span>
          <input className="input" type="number" min={spent ?? 0} step="1" value={raise} onChange={(e) => setRaise(e.target.value)} disabled={disabled} data-testid="inbox-room-budget" />
          {spent != null && <span className="hitl__hint">{ROOM_PAUSED_CARD.budget_hint(usd(spent))}</span>}
        </label>
      )}
      <div className="inbox-room__actions">
        <button type="button" className="btn btn--sm btn--primary" disabled={disabled || tooLow} onClick={() => void approve()} data-testid="inbox-room-approve">
          {ROOM_PAUSED_CARD.approve}
        </button>
      </div>
      {error && <p className="hitl__err" role="alert" data-testid="inbox-room-error">{error}</p>}
    </div>
  );
}
