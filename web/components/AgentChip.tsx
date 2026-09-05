"use client";
import "./agent-chip.css";
import { Badge } from "./Badge";
import type { AgentStatus, AgentRole } from "@/lib/api/types";

/** PRD FR-1.3 파생 순서(SCREEN §4.5): disabled > offline > error > working > waiting_human > idle. */
export const AGENT_STATUS_ORDER: readonly AgentStatus[] = ["disabled", "offline", "error", "working", "waiting_human", "idle"];

export interface DeriveInput {
  disabled?: boolean;
  /** 런타임 오프라인(세션 런타임 기준). */
  offline?: boolean;
  /** 재시도 없는 실패(auth·quota·config)를 가진 task 가 있는가. */
  error?: boolean;
  /** 이 에이전트의 task 상태 목록. blocked·paused lane 은 파생에 들어가지 않는다(C1′). */
  taskStatuses?: readonly string[];
}

/**
 * 서버가 `Participant.status` 로 파생값을 주지만, 실시간 `task.updated` 만 받았을 때 화면이 같은 규칙으로
 * 다시 계산할 수 있어야 한다. 규칙은 한 곳(여기)에만 둔다.
 */
export function deriveAgentStatus(input: DeriveInput): AgentStatus {
  if (input.disabled) return "disabled";
  if (input.offline) return "offline";
  if (input.error) return "error";
  const ts = input.taskStatuses ?? [];
  if (ts.some((s) => s === "running" || s === "dispatched" || s === "preparing")) return "working";
  if (ts.some((s) => s === "waiting_human")) return "waiting_human";
  return "idle";
}

export const AGENT_ROLE_LABEL: Record<AgentRole, string> = {
  lead: "lead",
  researcher: "researcher",
  writer: "writer",
  engineer: "engineer",
  reviewer: "reviewer",
  custom: "custom",
};

export interface AgentChipProps {
  name: string;
  role?: AgentRole;
  /** 파생 상태값. `derive` 를 주면 그것으로 계산한다(둘 다 있으면 derive 우선). */
  status?: AgentStatus;
  derive?: DeriveInput;
  /** 둘째 줄 부가 텍스트 — 상태 값이 아니다("lane #2 질문 대기", N2). */
  statusNote?: string | null;
  /** 프로파일 요약(예 "Claude Code · sonnet"). 둘째 줄에 role 과 함께. */
  profile?: string | null;
  avatarUrl?: string | null;
  isAssignee?: boolean;
  size?: "md" | "sm";
  onClick?: () => void;
  className?: string;
}

export function AgentChip(props: AgentChipProps) {
  const status = props.derive ? deriveAgentStatus(props.derive) : (props.status ?? "idle");
  const line2 = [props.role && AGENT_ROLE_LABEL[props.role], props.profile, props.statusNote].filter(Boolean).join(" · ");
  const Tag = props.onClick ? "button" : "div";
  const size = props.size ?? "md";
  return (
    <Tag
      type={props.onClick ? "button" : undefined}
      className={["agent-chip", size === "sm" ? "agent-chip--sm" : "", props.className].filter(Boolean).join(" ")}
      onClick={props.onClick}
      data-testid="agent-chip"
      data-status={status}
      title={props.statusNote ?? undefined}
    >
      <span className="agent-chip__avatar" aria-hidden="true">
        {props.avatarUrl ? <img src={props.avatarUrl} alt="" /> : props.name.slice(0, 1).toUpperCase()}
      </span>
      <span className="agent-chip__text">
        <span className="agent-chip__line1">
          <span className="agent-chip__name">@{props.name}</span>
          {props.isAssignee && <span className="agent-chip__assignee">assignee</span>}
          <Badge kind="agent" value={status} size="sm" />
        </span>
        {size === "md" && line2 && (
          <span className="agent-chip__line2" data-testid="agent-chip-line2">
            {line2}
          </span>
        )}
      </span>
    </Tag>
  );
}

export default AgentChip;
