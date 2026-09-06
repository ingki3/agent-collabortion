"use client";
/**
 * Inbox Item(COMPONENTS §2.4 `T0qdqP` · SCREEN §4.6) — **항목 7종을 하나의 컴포넌트로**.
 *
 * 심각도 배지의 **글리프는 심각도**(`action_required` ! · `attention` ▲ · `info` i)이고
 * **색은 항목의 원인 상태**를 따른다(리뷰 #03 N4) — 다음 사람이 `attention` 을 빨강으로 통일하지 않도록
 * 규칙을 `TONE_BY_TYPE` 로 코드에 둔다.
 *
 * `hitl_request` 항목은 **본문을 `HitlBody` 로 그린다** — S7 타임라인 카드와 같은 하위 컴포넌트다.
 * 그래야 F2("세션을 열지 않고 인박스에서 답한다")가 성립한다(U3: 카드만 읽고 제출).
 *
 * 버튼은 **서버가 준 `actions`** 로만 정한다(계약 `InboxItem.actions` — 권한을 반영한 목록이다).
 * 화면이 버튼을 만들어 내면 403 을 누르게 된다.
 */
import { useState } from "react";
import "./inbox-item.css";
import { Badge } from "./Badge";
import { HitlBody, type HitlAction } from "./HitlBody";
import { dueLabel } from "./HitlBody";
import { clockTime, relativeTime } from "@/lib/time";
import type { HitlResponse, InboxItem } from "@/lib/api/types";
import type { Tone } from "./badge-map";

export type InboxAction = NonNullable<InboxItem["actions"]>[number];
type ItemType = InboxItem["type"];

/** 종류 한국어 이름(COMPONENTS §2.4 `SzQ6z`). */
export const TYPE_LABEL: Record<ItemType, string> = {
  hitl_request: "응답 요청",
  lane_blocked: "에이전트 질문",
  session_paused: "세션 일시정지",
  run_failed: "작업 실패",
  runtime_offline: "런타임 오프라인",
  session_completed: "세션 완료",
  mention: "멘션",
};

/**
 * 심각도 배지의 **색**(COMPONENTS §2.4 타입별 조합 표) — 심각도가 아니라 원인 상태를 따른다.
 * hitl_request `$s-wait` · lane_blocked `$s-block` · session_paused `$s-pause` ·
 * runtime_offline·run_failed `$s-fail` · mention `$s-run` · session_completed `$s-done`.
 */
export const TONE_BY_TYPE: Record<ItemType, Tone> = {
  hitl_request: "wait",
  lane_blocked: "block",
  session_paused: "pause",
  runtime_offline: "fail",
  run_failed: "fail",
  mention: "run",
  session_completed: "done",
};

/** 버튼 라벨(COMPONENTS §2.4 표 · SCREEN §4.6 인라인 동작). `restart` 는 "다시 지시"다 — 재시도가 아니다(리뷰 #01 C4). */
export const ACTION_LABEL: Record<InboxAction, string> = {
  answer: "답변 보내기",
  approve: "승인",
  reject: "거절",
  reply: "답글 작성",
  approve_continue: "계속 진행 승인",
  restart: "다시 지시",
  rebind: "재바인딩",
  open_session: "세션 열기",
  open_runtimes: "Runtimes 열기",
};

/** `actions` 중 HITL 본문이 직접 처리하는 것(입력부와 붙어 있어야 한다). 나머지는 카드 하단 버튼이다. */
const INLINE_HITL: readonly InboxAction[] = ["answer", "approve", "reject"];

/** 부가 텍스트(COMPONENTS §2.4 `fDXjQ`, 기본 끔) — 타입별로 "더 알아야 할 한 줄". */
export function extraLine(item: InboxItem): string | null {
  switch (item.type) {
    case "hitl_request":
      // 제안 기본값은 **본문(HitlBody)이 이미 그린다** — 여기서 되풀이하면 같은 문장이 두 줄이 된다.
      // 부가 칸은 본문에 없는 것만 말한다: deputy 위임 시점(O5).
      return item.delegated ? "위임됨 · 지금부터 응답 가능" : null;
    case "session_paused":
      return item.card?.paused_reason ? `사유 ${item.card.paused_reason} — 승인하면 같은 lane·workdir 로 이어갑니다` : null;
    case "run_failed":
      return item.card?.failure_kind ? `실패 분류 ${item.card.failure_kind} — 맥락을 더해 다시 지시하세요` : null;
    case "runtime_offline":
      return item.card?.grace_ends_at ? `유예 만료 ${clockTime(item.card.grace_ends_at)}` : null;
    case "session_completed":
      return item.card?.summary ?? null;
    default:
      return null;
  }
}

export interface InboxItemCardProps {
  item: InboxItem;
  /** 인라인 HITL 응답(F2). `hitl_request` 에서만 쓰인다. */
  onRespond?: (item: InboxItem, body: HitlResponse) => Promise<void> | void;
  /**
   * `session_paused` 의 "계속 승인" — **금액 입력과 함께**다(U7-1: "카드만으로 얼마를 얼마로 올릴지
   * 결정 가능"). 없으면 `onAction(item, "approve_continue")` 로 떨어진다.
   */
  onApproveContinue?: (item: InboxItem, limits: { budget_usd?: number }) => Promise<void> | void;
  /** 그 밖의 인라인 동작 — 세션 열기·답글·다시 지시·계속 승인·Runtimes. */
  onAction?: (item: InboxItem, action: InboxAction) => void;
  onMarkRead?: (item: InboxItem) => void;
  busy?: boolean;
  now?: number;
}

export function InboxItemCard({ item, onRespond, onApproveContinue, onAction, onMarkRead, busy, now }: InboxItemCardProps) {
  const hitl = item.type === "hitl_request" ? item.card : null;
  const actions = (item.actions ?? []) as InboxAction[];
  const inline = actions.filter((a): a is HitlAction => (INLINE_HITL as readonly string[]).includes(a));
  const rest = actions.filter((a) => !(INLINE_HITL as readonly string[]).includes(a));
  const overdue = item.overdue === true;
  // 예산으로 멈춘 세션만 금액을 받는다 — 시간·루프·수동은 올릴 금액이 없다(SCREEN §4.5 O6 표).
  const raiseBudget = item.type === "session_paused" && item.card?.paused_reason === "budget" && actions.includes("approve_continue");
  const [budget, setBudget] = useState("");

  return (
    <article
      className={`inbox-item inbox-item--${item.severity}${item.read_at ? "" : " inbox-item--unread"}`}
      data-testid="inbox-item"
      data-item-id={item.id}
      data-type={item.type}
      data-severity={item.severity}
      data-overdue={overdue ? "true" : "false"}
      data-delegated={item.delegated ? "true" : "false"}
    >
      <div className="inbox-item__head">
        <Badge kind="inbox" value={item.severity} size="sm" tone={TONE_BY_TYPE[item.type]} />
        <span className="inbox-item__type" data-testid="inbox-type">{TYPE_LABEL[item.type]}</span>
        {item.session?.title && (
          <span className="inbox-item__session" data-testid="inbox-session">· {item.session.title}</span>
        )}
        <span className="inbox-item__spacer" />
        <span
          className={`inbox-item__due${overdue ? " inbox-item__due--overdue" : ""}`}
          data-testid="inbox-due"
        >
          {item.due_at ? dueLabel(item.due_at, overdue, now) : relativeTime(item.created_at, now)}
        </span>
        {!item.read_at && onMarkRead && (
          <button type="button" className="msg__link" onClick={() => onMarkRead(item)} data-testid="inbox-read">
            읽음
          </button>
        )}
      </div>

      {hitl && hitl.hitl_type ? (
        // 세션을 열지 않고 답한다(F2) — 타임라인 카드와 **같은 하위 컴포넌트**.
        <HitlBody
          type={hitl.hitl_type}
          status="open"
          question={hitl.title ?? "응답이 필요합니다"}
          context={hitl.body}
          proposedDefault={hitl.proposed_default}
          dueAt={item.due_at}
          overdue={overdue}
          canRespond={inline.length > 0}
          canRespondFrom={null}
          actions={inline}
          onRespond={onRespond && ((body) => onRespond(item, body))}
          busy={busy}
          dense
        />
      ) : (
        <div className="inbox-item__body">
          {item.card?.title && <p className="inbox-item__title" data-testid="inbox-title">{item.card.title}</p>}
          {item.card?.body && <p className="inbox-item__text" data-testid="inbox-body">{item.card.body}</p>}
          {item.card?.agent_name && <p className="inbox-item__text">에이전트 @{item.card.agent_name}</p>}
        </div>
      )}

      {extraLine(item) && (
        <p className="inbox-item__extra" data-testid="inbox-extra">{extraLine(item)}</p>
      )}

      {raiseBudget && (
        <label className="inbox-item__field">
          <span>새 상한 (USD)</span>
          <input
            className="input"
            type="number"
            min={0}
            step="1"
            value={budget}
            onChange={(e) => setBudget(e.target.value)}
            disabled={busy}
            placeholder="비워 두면 현재 상한 그대로 재개"
            data-testid="inbox-budget-input"
          />
        </label>
      )}

      {rest.length > 0 && (
        <div className="inbox-item__actions" data-testid="inbox-actions">
          {rest.map((a, i) => (
            <button
              key={a}
              type="button"
              className={`btn btn--sm${i === 0 && a !== "open_session" ? " btn--primary" : ""}`}
              disabled={busy || (!onAction && !(a === "approve_continue" && onApproveContinue))}
              onClick={() => {
                if (a === "approve_continue" && onApproveContinue) {
                  const n = Number(budget);
                  onApproveContinue(item, raiseBudget && Number.isFinite(n) && n > 0 ? { budget_usd: n } : {});
                  return;
                }
                onAction?.(item, a);
              }}
              data-testid={`inbox-action-${a}`}
            >
              {ACTION_LABEL[a]}
            </button>
          ))}
        </div>
      )}
    </article>
  );
}

export default InboxItemCard;
