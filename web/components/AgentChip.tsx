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
 * 상태의 **주인은 서버**다 — 화면은 `Participant.status` 를 그대로 그린다. 이 함수는 실시간 `task.updated` 만 받아
 * 참여자 이벤트가 아직 안 온 순간의 **거울**이고, 서버 값을 덮어쓰지 않는다. 규칙은 한 곳(여기)에만 둔다.
 *
 * **`working` 은 `running` task 뿐이다**(PRD FR-1.3 4행, W-5). `dispatched`·`preparing` 은 아직 턴이 시작되지 않았고,
 * 그것을 `working` 으로 세면 데몬이 claim 만 하고 멈춰도 칩이 계속 "작업 중"이라 침묵과 실행을 구분할 수 없다.
 * `blocked`·`paused` lane 도 파생에 넣지 않는다(C1′) — 왜 멈췄는지는 lane 카드가 말한다.
 */
export function deriveAgentStatus(input: DeriveInput): AgentStatus {
  if (input.disabled) return "disabled";
  if (input.offline) return "offline";
  if (input.error) return "error";
  const ts = input.taskStatuses ?? [];
  if (ts.some((s) => s === "running")) return "working";
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
