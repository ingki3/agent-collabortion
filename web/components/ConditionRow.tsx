"use client";
/**
 * Condition Row(COMPONENTS §2.5 `XMNop`) — 종료 조건 한 줄. 조건 편집기(S21 미션 열기·편집 · 조건 고치기 — 테두리 있음, 옛 이름 `wizard` 변형)와 S7 우열 진행률(테두리 없음)이 함께 쓴다.
 *
 * 이름은 **사람 말**이다(T-W15, S-84 · SCREEN §4.4 6단계): 보고서 제출 · Lead 의 검토 승인 · Director 승인 · 수동 종료 — `conditionName`.
 * 진행률 행은 두 번째 줄이 답을 말한다 — 충족했으면 **누가·언제**(Writer, 9/13), 아니면 **다음 행동**(Lead 차례 · 받은 요청에서
 * 승인하세요 — `PROGRESS` 표). `blocked_reason` 이 있으면 ✗ 대신 **이유 문장**(`BLOCKED_REASON` 표)을
 * 그대로 보인다 — 세션이 왜 안 닫히는지 이 칸만 보고 알 수 있어야 한다(SCREEN §4.5).
 */
import "./condition-row.css";
import { BLOCKED_REASON, CONDITION_DESC, PROGRESS, blockedReasonText, conditionName } from "@/lib/wording";

/** 옛 이름 — 호출부 호환(요약·테스트). 새 코드는 `conditionName` 을 쓴다. */
export { conditionName, CONDITION_DESC };

export interface ConditionRowProps {
  type: string;
  /** ☑ 충족 / ☐ 미충족 / — 해당 없음(마법사). */
  met: boolean | null;
  /** 마법사 — 이름 뒤 괄호("보고서 제출 (담당 에이전트)"). */
  who?: string | null;
  /** `agent_approval`·`artifact_submitted` 의 지정 에이전트 이름 — "Lead 의 검토 승인". */
  agentName?: string | null;
  /** 충족시킨 주체의 **이름**(id 가 아니다 — 호출부가 푼다). */
  metBy?: string | null;
  metAt?: string | null;
  nextActor?: string | null;
  /** 계약 `blocked_reason` — 있으면 ✗ 대신 이유. */
  blockedReason?: string | null;
  /** 계약 `held_reason`(v0.3.3) — `user_approval` 이 작업 중이라 보류됐다. 두 번째 줄이 그 말을 한다(T-APPROVAL). */
  heldReason?: string | null;
  /** `user_approval` 대기 중인 확인 요청 — 있으면 두 번째 줄이 그 카드로 가는 링크가 된다. */
  hitlRequestId?: string | null;
  onOpenHitl?: (hitlRequestId: string) => void;
  /** 마법사용 — 테두리와 선택 상태. */
  variant?: "wizard" | "progress";
  selected?: boolean;
  disabled?: boolean;
  disabledNote?: string;
  onToggle?: (next: boolean) => void;
  children?: React.ReactNode;
}

/** "9/13" — 충족 시각은 상대 시각보다 날짜가 낫다(며칠 뒤에 봐도 같은 말). */
export function shortDate(iso: string | null | undefined): string | null {
  if (!iso) return null;
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return null;
  return `${t.getMonth() + 1}/${t.getDate()}`;
}

/** 진행률 행의 두 번째 줄 — 순수 함수라 테스트가 바로 잰다. */
export function progressLine(p: Pick<ConditionRowProps, "type" | "met" | "metBy" | "metAt" | "nextActor" | "agentName" | "blockedReason" | "heldReason">): string {
  if (p.blockedReason) return blockedReasonText(p.blockedReason);
  if (p.met) return PROGRESS.met_by(p.metBy ?? null, shortDate(p.metAt));
  if (p.heldReason === "running_tasks") return PROGRESS.held_running_tasks;
  if (p.type === "user_approval") return PROGRESS.user_approval_next;
  if (p.type === "manual") return PROGRESS.manual_next;
  const actor = p.nextActor ?? p.agentName;
  return actor ? PROGRESS.turn(actor) : PROGRESS.waiting;
}

export function ConditionRow(props: ConditionRowProps) {
  const variant = props.variant ?? "progress";
  const blocked = !!props.blockedReason && !props.met;
  const check = props.met === null ? "—" : props.met ? "✓" : blocked ? "⚠" : "☐";
  const label = conditionName(props.type, props.agentName);
  const Tag = props.onToggle ? "button" : "div";
  const line = variant === "wizard" ? (props.disabledNote ?? (CONDITION_DESC as Record<string, string>)[props.type] ?? "") : progressLine(props);
  const linkable = variant === "progress" && !props.met && !blocked && props.type === "user_approval" && !!props.hitlRequestId && !!props.onOpenHitl;
  return (
    <Tag
      type={props.onToggle ? "button" : undefined}
      className={`cond cond--${variant}${props.selected ? " cond--selected" : ""}${props.disabled ? " cond--disabled" : ""}${blocked ? " cond--blocked" : ""}`}
      data-testid="condition-row"
      data-type={props.type}
      data-met={props.met === null ? "na" : String(props.met)}
      data-blocked={blocked ? props.blockedReason ?? undefined : undefined}
      data-held={!props.met && props.heldReason ? props.heldReason : undefined}
      disabled={props.disabled}
      title={props.disabled ? props.disabledNote : undefined}
      onClick={props.onToggle ? () => props.onToggle!(!props.selected) : undefined}
    >
      <span className="cond__check" aria-hidden="true">{variant === "wizard" ? (props.selected ? "☑" : "☐") : check}</span>
      <span className="cond__text">
        <span className="cond__name" data-testid="condition-name">
          {label}
          {props.who ? ` (${props.who})` : ""}
        </span>
        {linkable ? (
          <button type="button" className="cond__link" onClick={() => props.onOpenHitl!(props.hitlRequestId!)} data-testid="condition-hitl-link">
            {line}
          </button>
        ) : (
          <span className={`cond__desc${blocked ? " cond__desc--blocked" : ""}`} data-testid="condition-line">{line}</span>
        )}
      </span>
      {props.children}
    </Tag>
  );
}

/** 계약 enum 셋 전부에 문장이 있다 — 자물쇠 테스트가 이 표를 잰다. */
export const BLOCKED_REASONS = Object.keys(BLOCKED_REASON);

export default ConditionRow;
