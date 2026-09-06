"use client";
/**
 * Condition Row(COMPONENTS §2.5 `XMNop`) — 종료 조건 한 줄. S6 마법사(테두리 있음)와 S7 우열 진행률(테두리 없음)이 함께 쓴다.
 * 진행률 행은 **누가 충족시킬 차례인지**(`next_actor`)까지 말한다 — 그게 없으면 "왜 안 끝나지"의 답이 화면에 없다.
 */
import "./condition-row.css";
import { relativeTime } from "@/lib/time";

export const CONDITION_LABEL: Record<string, string> = {
  artifact_submitted: "아티팩트 제출",
  agent_approval: "에이전트 승인",
  user_approval: "Director 승인",
  criteria_met: "성공 기준 충족",
  manual: "수동 종료",
};

export const CONDITION_DESC: Record<string, string> = {
  artifact_submitted: "지정한 에이전트가 산출물을 제출하면 충족",
  agent_approval: "지정한 에이전트가 검토를 승인하면 충족",
  user_approval: "Director 가 승인하면 충족 — 사람 게이트",
  criteria_met: "성공 기준을 모두 만족하면 충족",
  manual: "Director 가 직접 종료",
};

export interface ConditionRowProps {
  type: string;
  /** ☑ 충족 / ☐ 미충족 / — 해당 없음. */
  met: boolean | null;
  who?: string | null;
  nextActor?: string | null;
  metAt?: string | null;
  /** 마법사용 — 테두리와 선택 상태. */
  variant?: "wizard" | "progress";
  selected?: boolean;
  disabled?: boolean;
  disabledNote?: string;
  onToggle?: (next: boolean) => void;
  children?: React.ReactNode;
}

export function ConditionRow(props: ConditionRowProps) {
  const variant = props.variant ?? "progress";
  const check = props.met === null ? "—" : props.met ? "☑" : "☐";
  const label = CONDITION_LABEL[props.type] ?? props.type;
  const Tag = props.onToggle ? "button" : "div";
  return (
    <Tag
      type={props.onToggle ? "button" : undefined}
      className={`cond cond--${variant}${props.selected ? " cond--selected" : ""}${props.disabled ? " cond--disabled" : ""}`}
      data-testid="condition-row"
      data-type={props.type}
      data-met={props.met === null ? "na" : String(props.met)}
      disabled={props.disabled}
      title={props.disabled ? props.disabledNote : undefined}
      onClick={props.onToggle ? () => props.onToggle!(!props.selected) : undefined}
    >
      <span className="cond__check" aria-hidden="true">{variant === "wizard" ? (props.selected ? "☑" : "☐") : check}</span>
      <span className="cond__text">
        <span className="cond__name">
          {label}
          {props.who ? ` (${props.who})` : ""}
        </span>
        <span className="cond__desc">
          {variant === "wizard"
            ? (props.disabledNote ?? CONDITION_DESC[props.type] ?? "")
            : props.met
              ? `충족${props.metAt ? ` · ${relativeTime(props.metAt)}` : ""}`
              : props.nextActor
                ? `다음 차례: ${props.nextActor}`
                : "대기 중"}
        </span>
      </span>
      {props.children}
    </Tag>
  );
}

export default ConditionRow;
