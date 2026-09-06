"use client";
/**
 * HITL 본문 — **타임라인 카드(S7)와 Inbox Item(S8)이 함께 쓰는 하위 컴포넌트**(COMPONENTS §2.3 리뷰 #03 K2,
 * PLAN §8 결정 6). 두 자리의 겉껍질은 다르지만(인박스는 세션명·심각도가 더 필요하다) **질문·제안 기본값·기한·
 * 권한·버튼 2** 는 같은 코드다. F2("인박스에서 맥락 없이 답한다")가 성립하려면 인박스 카드가 세션 카드와
 * 같은 것을 담아야 하기 때문이다.
 *
 * 권한 3상태 — 계약이 준 `can_respond` · `can_respond_from` 만 읽는다(판정은 서버, 화면은 그리기만).
 *   1) `can_respond: true`                      → 버튼 활성
 *   2) `false` + `can_respond_from` 있음(deputy) → 버튼 **비활성 + "🔒 HH:MM부터"**(E7-09, U9-2)
 *   3) `false` + `can_respond_from` **null**     → 응답 컨트롤 자체를 내지 않는다(E7-11 · openapi
 *      `Problem.can_respond_from`: "권한이 영영 없는 사람에게는 비운다 — 생기지 않을 권리에 시각을 약속하지 않는다").
 *      숨기는 것이 아니라 **사유를 문장으로** 남긴다(SCREEN §7 "비활성 + 사유").
 * 어떤 응답 컨트롤을 낼지는 호출부가 `actions` 로 정한다 — 인박스는 계약 `InboxItem.actions`(서버가 권한을
 * 반영해 준 목록) 를, 타임라인은 타입이 정하는 기본 집합을 넘긴다.
 *
 * v1 타입은 `question`·`approval` 둘이고 `choice`·`info` 는 v1.1 이지만 **입력부만 갈아끼우면 되게**
 * 설계한다(SCREEN §2.3 C4). 그래서 입력부가 `hitlInputFor(type)` 하나로 갈린다.
 */
import { useState } from "react";
import "./hitl-card.css";
import { clockTime } from "@/lib/time";
import type { HitlResponse, HitlStatus, HitlType } from "@/lib/api/types";

/** 응답 컨트롤 종류 — 계약 `InboxItem.actions` 중 HITL 이 쓰는 값과 같은 철자를 쓴다. */
export type HitlAction = "answer" | "approve" | "reject";

/** 타입별 기본 응답 컨트롤(FR-5.1 표 · COMPONENTS §2.4 타입별 조합). */
export function defaultActionsFor(type: HitlType): HitlAction[] {
  return type === "approval" ? ["approve", "reject"] : ["answer"];
}

export const HITL_STATUS_LABEL: Record<HitlStatus, string> = {
  open: "응답 대기",
  answered: "응답됨",
  auto_answered: "자동 응답됨",
  cancelled: "취소됨",
};

export interface HitlBodyProps {
  type: HitlType;
  status: HitlStatus;
  question: string;
  context?: string | null;
  options?: string[];
  proposedDefault?: string | null;
  dueAt?: string | null;
  overdue?: boolean;
  canRespond: boolean;
  /** deputy 가 응답 가능해지는 시각(기한 절반). 권한이 영영 없으면 null. */
  canRespondFrom?: string | null;
  /** 낼 응답 컨트롤. 비우면 타입 기본값. */
  actions?: HitlAction[];
  /** 이미 답이 있으면 그 값을 보인다(E7-08 이후·auto_answered). */
  answer?: string | null;
  approved?: boolean | null;
  answeredBy?: string | null;
  /**
   * 예산 초과 시스템 HITL(`purpose: budget`) — 승인에 **상향 금액 입력**을 붙인다(E9-02, U7-1).
   *
   * 조건은 **`purpose` 하나**다. 세션이 멈췄는지·인박스 항목이 무엇인지와는 무관하다 — task 범위 초과
   * (`HitlRequest.task_id` 채움, E9-01·E9-10)는 lane 만 멈추고 세션은 `active` 로 남기 때문에, 화면이
   * `session_paused` 를 조건으로 삼으면 그 절반을 놓친다(W-6: Director 가 웹에서 금액을 못 정했다).
   *
   *   · `scope`   — `task`(기본) 는 새 **task** 상한, `session` 은 새 **세션** 상한이다. 라벨이 갈린다.
   *   · `current` — 지금 상한(`task.budget_override` · `session.limits.budget_usd`). 기본값 후보.
   *   · `spent`   — **그 범위에서** 지금까지 쓴 금액. 최소값이다. 모르면 비운다 — 세션 소진액을 task
   *                 범위의 최소로 쓰면 $3 상향이 막힌다(범위가 다른 수를 섞지 않는다).
   */
  budgetOverride?: { scope?: "task" | "session"; current?: number | null; spent?: number | null } | null;
  onRespond?: (body: HitlResponse) => Promise<void> | void;
  busy?: boolean;
  /** 인박스는 카드 폭이 넓어 한 줄, 타임라인은 좁아 두 줄 — 자리에 따른 밀도만 바꾼다. */
  dense?: boolean;
}

/** 기한 한 줄 — overdue 면 빨강 굵게(COMPONENTS §2.4 `ZHNoQ`). */
export function dueLabel(dueAt: string | null | undefined, overdue: boolean | undefined, now = Date.now()): string {
  if (!dueAt) return "기한 없음";
  const t = Date.parse(dueAt);
  if (Number.isNaN(t)) return dueAt;
  if (overdue || t <= now) return `기한 지남 · ${clockTime(dueAt)}`;
  const h = Math.floor((t - now) / 3_600_000);
  return h >= 1 ? `${h}시간 남음 · ${clockTime(dueAt)}까지` : `${Math.max(1, Math.round((t - now) / 60_000))}분 남음`;
}

/** `proposed_default` 는 계약상 문자열이다 — 예산 HITL 이 숫자를 담았을 때만 금액 기본값으로 쓴다. */
export function numericDefault(v: string | null | undefined): number | null {
  if (v == null) return null;
  const n = Number(String(v).replace(/[$,\s]/g, ""));
  return Number.isFinite(n) && n >= 0 && String(v).trim() !== "" ? n : null;
}

export function HitlBody(props: HitlBodyProps) {
  const { type, status, canRespond } = props;
  const [answer, setAnswer] = useState(props.proposedDefault ?? "");
  const [reason, setReason] = useState("");
  // 상향 금액은 **아직 손대지 않았으면 `null`** 이다 — 인박스는 `purpose` 를 알아내려 HITL 상세를 뒤늦게
  // 읽으므로(`budgetOverride` 가 마운트 뒤에 붙는다) `useState(기본값)` 으로 굳히면 그때 계산한 빈 문자열이
  // 남는다. 손댄 적이 없으면 늘 지금의 기본값을 보인다.
  const [raiseEdit, setRaiseEdit] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const open = status === "open";
  const gateFrom = props.canRespondFrom ?? null;
  // 3상태(위 주석). `never` 는 컨트롤을 내지 않고 사유만 남긴다.
  const permission: "allowed" | "later" | "never" = canRespond ? "allowed" : gateFrom ? "later" : "never";
  const actions = props.actions ?? defaultActionsFor(type);
  const lock = gateFrom ? `🔒 ${clockTime(gateFrom)}부터` : null;

  // ── 예산 상향 입력(E9-02) ────────────────────────────────────────────────
  const bo = props.budgetOverride ?? null;
  const scope = bo?.scope ?? "task";
  const spent = typeof bo?.spent === "number" ? bo.spent : null;
  // 기본값 우선순위: 계약의 제안 기본값(`proposed_default`, 숫자일 때만) → 지금 상한 → 빈 칸.
  // 빈 칸이면 `budget_override_usd` 없이 승인이 나간다 — "상한은 그대로 두고 계속" 도 답이기 때문이다.
  const proposedNum = numericDefault(props.proposedDefault);
  const raiseDefault = String(proposedNum ?? bo?.current ?? "");
  const raise = raiseEdit ?? raiseDefault;
  const raiseNum = Number(raise);
  const raiseValid = raise.trim() === "" || (Number.isFinite(raiseNum) && raiseNum >= 0);
  // 이미 쓴 돈보다 낮은 상한으로 승인하면 다음 턴에 곧바로 다시 멈춘다 — 승인이 아니라 헛걸음이다.
  const raiseTooLow = spent != null && raise.trim() !== "" && Number.isFinite(raiseNum) && raiseNum > 0 && raiseNum < spent;

  async function send(body: HitlResponse) {
    setError(null);
    try {
      await props.onRespond?.(body);
    } catch (e) {
      setError(e instanceof Error ? e.message : "응답하지 못했습니다");
    }
  }

  const disabled = permission !== "allowed" || props.busy || !props.onRespond;
  const disabledTitle =
    permission === "later"
      ? `Director 응답 대기 중 · ${clockTime(gateFrom)}부터 응답 가능`
      : permission === "never"
        ? "Director·deputy 만 응답할 수 있습니다"
        : undefined;

  return (
    <div className="hitl__body" data-testid="hitl-body" data-type={type} data-status={status} data-permission={permission}>
      <p className="hitl__q" data-testid="hitl-question">{props.question}</p>
      {props.context && <p className="hitl__ctx" data-testid="hitl-context">{props.context}</p>}

      {/* 제안 기본값 — question·choice 는 필수(FR-5.1). 없는 타입에서는 자리를 만들지 않는다(COMPONENTS §2.3 `g71PvC` 기본 끔). */}
      {props.proposedDefault && (
        <p className="hitl__default" data-testid="hitl-proposed-default">
          에이전트 제안: <b>{props.proposedDefault}</b>
        </p>
      )}

      <span
        className={`hitl__due${props.overdue ? " hitl__due--overdue" : ""}`}
        data-testid="hitl-due"
        data-overdue={props.overdue ? "true" : "false"}
      >
        {dueLabel(props.dueAt, props.overdue)}
      </span>

      {status !== "open" && (
        <p className="hitl__answer" data-testid="hitl-answer">
          {status === "cancelled" ? (
            <>취소됨 — 발행 조건을 잃어 서버가 닫았습니다. 사람이 답하지 않았으므로 결정 기록이 없습니다.</>
          ) : (
            <>
              {HITL_STATUS_LABEL[status]}:{" "}
              <b>
                {type === "approval"
                  ? props.approved === false
                    ? "거절"
                    : "승인"
                  : (props.answer ?? props.proposedDefault ?? "—")}
              </b>
              {props.answeredBy ? ` — ${props.answeredBy}` : status === "auto_answered" ? " — 자동(제안 기본값)" : ""}
            </>
          )}
        </p>
      )}

      {open && permission === "never" && (
        <p className="hitl__gate" data-testid="hitl-no-right">
          응답 권한이 없습니다 — Director·deputy 만 답할 수 있습니다. 카드는 누구나 볼 수 있습니다.
        </p>
      )}
      {open && permission === "later" && (
        <p className="hitl__gate" data-testid="hitl-gate">
          {lock} 응답 가능 — 기한의 절반이 지나면 deputy 에게 위임됩니다(FR-5.2).
        </p>
      )}

      {open && permission !== "never" && actions.length > 0 && (
        <>
          {/* 입력부 — 타입별로 갈아끼운다(SCREEN §2.3 C4). v1은 question·approval. */}
          {actions.includes("answer") && (
            <label className="hitl__field">
              <span>답변</span>
              {props.options && props.options.length > 0 ? (
                <select className="select" value={answer} onChange={(e) => setAnswer(e.target.value)} disabled={disabled} data-testid="hitl-choice">
                  {props.options.map((o) => (
                    <option key={o} value={o}>{o}</option>
                  ))}
                </select>
              ) : (
                <textarea
                  className="input"
                  rows={props.dense ? 2 : 3}
                  value={answer}
                  onChange={(e) => setAnswer(e.target.value)}
                  disabled={disabled}
                  placeholder={props.proposedDefault ? `비워 두면 제안값(${props.proposedDefault})` : "답변을 입력하세요"}
                  data-testid="hitl-answer-input"
                />
              )}
            </label>
          )}
          {bo && actions.includes("approve") && (
            <label className="hitl__field" data-testid="hitl-budget-field" data-scope={scope}>
              <span>{scope === "session" ? "새 세션 상한 (USD)" : "새 task 상한 (USD)"}</span>
              <input
                className="input"
                type="number"
                min={spent ?? 0}
                step="1"
                value={raise}
                onChange={(e) => setRaiseEdit(e.target.value)}
                disabled={disabled}
                placeholder={spent != null ? `$${spent.toFixed(2)} 보다 큰 금액` : "비워 두면 상한 그대로 승인"}
                data-testid="hitl-budget-input"
              />
              <span className="hitl__hint" data-testid="hitl-budget-hint">
                {spent != null ? `지금까지 $${spent.toFixed(2)} 사용` : "지금까지 쓴 금액보다 큰 값을 넣으세요"}
                {bo.current != null ? ` · 지금 상한 $${bo.current.toFixed(2)}` : ""}
              </span>
            </label>
          )}
          {actions.includes("reject") && (
            <label className="hitl__field">
              <span>거절 사유 {bo ? "" : "(거절 시 필수)"}</span>
              <input
                className="input"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                disabled={disabled}
                data-testid="hitl-reason-input"
              />
            </label>
          )}

          <div className="hitl__actions" data-testid="hitl-actions">
            {actions.includes("answer") && (
              <button
                type="button"
                className="btn btn--sm btn--primary"
                disabled={disabled}
                title={disabledTitle}
                onClick={() => void send({ answer: answer.trim() || (props.proposedDefault ?? "") })}
                data-testid="hitl-answer"
              >
                답변 보내기
              </button>
            )}
            {actions.includes("approve") && (
              <button
                type="button"
                className="btn btn--sm btn--primary"
                disabled={disabled || raiseTooLow || !raiseValid}
                title={
                  raiseTooLow
                    ? `새 상한은 지금까지 쓴 $${spent!.toFixed(2)} 보다 커야 합니다 — 그보다 낮으면 다음 턴에 다시 멈춥니다`
                    : disabledTitle
                }
                onClick={() =>
                  void send({
                    approved: true,
                    ...(bo && Number.isFinite(raiseNum) && raiseNum > 0 ? { budget_override_usd: raiseNum } : {}),
                  })
                }
                data-testid="hitl-approve"
              >
                {bo ? "계속 진행 승인" : "승인"}
              </button>
            )}
            {actions.includes("reject") && (
              <button
                type="button"
                className="btn btn--sm"
                disabled={disabled || (!bo && reason.trim() === "")}
                title={
                  !bo && reason.trim() === "" && permission === "allowed"
                    ? "거절에는 사유가 필요합니다(결정 기록에 남습니다)"
                    : disabledTitle
                }
                onClick={() => void send({ approved: false, reason: reason.trim() })}
                data-testid="hitl-reject"
              >
                거절
              </button>
            )}
            {lock && permission === "later" && (
              <span className="hitl__gate" data-testid="hitl-lock">{lock}</span>
            )}
          </div>
        </>
      )}

      {error && <p className="hitl__err" role="alert" data-testid="hitl-error">{error}</p>}
    </div>
  );
}

export default HitlBody;
