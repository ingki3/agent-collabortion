"use client";
/**
 * HITL Card(COMPONENTS §2.3 `n9PqY`) — **S7 타임라인 자리**의 겉껍질. 본문은 `HitlBody` 로 빠져 있어
 * Inbox Item(S8)과 같은 코드를 쓴다(리뷰 #03 K2, PLAN §8 결정 6).
 *
 * 겉껍질이 담는 것: kind 배지(`HITL · approval` / `HITL · question`) · 작성자(에이전트 or "시스템") ·
 * 메타(시스템 발행이면 **"… 때문에 발행 · source: system"**) · 상태(open / answered / auto_answered /
 * cancelled "취소됨").
 *
 * **`purpose` 가 시스템 발행의 판정 기준이다**(계약 `HitlRequest.purpose`, 0012·PR #103·#108) —
 * `source: system` + `type: approval` 만으로는 완료 승인과 예산·루프 정지를 구분할 수 없다.
 */
import "./hitl-card.css";
import { HitlBody, HITL_STATUS_LABEL, type HitlAction } from "./HitlBody";
import { clockTime } from "@/lib/time";
import type { HitlRequest, HitlResponse } from "@/lib/api/types";

type Purpose = NonNullable<HitlRequest["purpose"]>;

/**
 * 시스템 발행 사유 문구(purpose 별). 에이전트 발행(`agent`)에는 이 줄이 없다 — 발행 이유가 곧 질문이기 때문이다.
 * 문자열은 계약 enum 값(`user_approval`·`budget`·`time`·`loop`)에 붙어 있다.
 */
export const PURPOSE_REASON: Record<Exclude<Purpose, "agent">, string> = {
  user_approval: "종료 조건(Director 승인)",
  budget: "예산 상한 초과",
  time: "시간 상한 도달",
  loop: "루프 상한 도달",
};

/** 메타 한 줄(COMPONENTS §2.3 `i2BcH`). 시스템 발행이면 사유 + `source: system`. */
export function hitlMetaLine(r: Pick<HitlRequest, "source" | "purpose" | "created_at">): string {
  if (r.source !== "system") return `${clockTime(r.created_at)} · source: agent`;
  const p = r.purpose && r.purpose !== "agent" ? PURPOSE_REASON[r.purpose] : null;
  return p ? `${p} 때문에 발행 · source: system` : "플랫폼이 발행 · source: system";
}

export function hitlKindLabel(r: Pick<HitlRequest, "type">): string {
  return `HITL · ${r.type}`;
}

export interface HitlCardProps {
  request: HitlRequest;
  /** 낼 응답 컨트롤. 비우면 타입 기본값(question → 답변, approval → 승인·거절). */
  actions?: HitlAction[];
  onRespond?: (body: HitlResponse) => Promise<void> | void;
  /**
   * 예산 초과 시스템 HITL 이면 승인에 상향 입력을 붙인다(E9-02).
   * 비워 두면 `purpose` 로만 판정한다 — 범위(`scope`)·현재 상한·소진액을 아는 것은 호출부다.
   */
  budget?: { scope?: "task" | "session"; current?: number | null; spent?: number | null } | null;
  busy?: boolean;
}

export function HitlCard({ request: r, actions, onRespond, budget, busy }: HitlCardProps) {
  const author = r.source === "system" ? "시스템" : (r.agent?.name ?? "에이전트");
  return (
    <article
      className={`hitl${r.status === "open" ? " hitl--open" : ""}${r.status === "cancelled" ? " hitl--cancelled" : ""}`}
      data-testid="hitl-card"
      data-hitl-id={r.id}
      data-source={r.source}
      data-purpose={r.purpose ?? ""}
      data-status={r.status}
    >
      <div className="hitl__head">
        <span className="hitl__kind" data-testid="hitl-kind">
          <span aria-hidden="true">⏳︎</span>
          {hitlKindLabel(r)}
        </span>
        <span className="hitl__author" data-testid="hitl-author">{author}</span>
        <span className="hitl__meta" data-testid="hitl-meta">{hitlMetaLine(r)}</span>
        {r.status !== "open" && (
          <span className="hitl__meta" data-testid="hitl-status-label">{HITL_STATUS_LABEL[r.status]}</span>
        )}
      </div>
      <HitlBody
        type={r.type}
        status={r.status}
        question={r.question}
        context={r.context}
        options={r.options}
        proposedDefault={r.proposed_default}
        dueAt={r.due_at}
        overdue={r.overdue}
        canRespond={r.can_respond}
        canRespondFrom={r.can_respond_from}
        actions={actions}
        answer={r.answer}
        approved={r.approved}
        // 조건은 `purpose` 하나다 — 세션이 멈췄는지는 보지 않는다(task 범위 초과는 세션을 멈추지 않는다, W-6).
        budgetOverride={
          r.purpose === "budget"
            ? { scope: r.task_id ? "task" : "session", current: r.budget_override_usd, ...(budget ?? {}) }
            : null
        }
        onRespond={onRespond}
        busy={busy}
      />
    </article>
  );
}

export default HitlCard;
