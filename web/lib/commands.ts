/**
 * 역할별 허용 명령(v1.1 K-19 — PRD FR-1.9.1 표 · `colab-cli.md` §2.5) — S10 역할 구역이 그린다.
 *
 * 정본은 서버가 계산한 `Agent.allowed_commands`(읽기 전용 파생값)다. 여기 표는 그 **미리보기**용이다 — 역할 select 를 바꾸면 저장
 * 전에도 그 역할의 목록을 보여 주려는 것(저장된 역할과 같으면 서버 값을 그대로 쓴다). 표를 바꾸는 것은 계약 변경(§2.5)이고,
 * `lib/wording.test.ts` 가 이 표를 `colab-cli.md` §2.5 표와 대조한다.
 */
import type { AgentRole, ColabCommand } from "@/lib/api/types";
import { COMMAND_LABEL, ROLE_COMMANDS } from "@/lib/wording";

/** 계약 enum 순서 그대로(openapi `ColabCommand`). */
export const ALL_COMMANDS: readonly ColabCommand[] = [
  "session_get", "session_messages", "artifact_get", "message_post", "status_set", "decision_record",
  "lane_delegate", "artifact_submit", "review_approve", "review_reject", "hitl_ask", "hitl_approve_request", "hitl_request_info",
];

/** §2.5 첫 행 — 모든 역할이 쓰는 여덟. */
const COMMON: readonly ColabCommand[] = ["session_get", "session_messages", "artifact_get", "message_post", "status_set", "decision_record", "hitl_ask", "hitl_request_info"];

/** §2.5 표 — 열 순서대로. `lead`·`custom` 은 전부. */
export const ROLE_COMMAND_TABLE: Record<AgentRole, readonly ColabCommand[]> = {
  lead: ALL_COMMANDS,
  researcher: [...COMMON, "artifact_submit"],
  writer: [...COMMON, "artifact_submit"],
  engineer: [...COMMON, "artifact_submit"],
  reviewer: [...COMMON, "review_approve", "review_reject"],
  custom: ALL_COMMANDS,
};

/** 역할의 허용 명령 — 계약 enum 순서로 정렬해 돌려준다(서버 값과 순서까지 같게). */
export function commandsForRole(role: AgentRole): ColabCommand[] {
  const set = new Set(ROLE_COMMAND_TABLE[role] ?? ALL_COMMANDS);
  return ALL_COMMANDS.filter((c) => set.has(c));
}

export interface CommandSummary {
  /** "전부" 또는 "메시지 게시 · 산출물 제출 · …". */
  can: string;
  /** 전부일 때 이유 한 마디(lead·custom), 아니면 null. */
  allNote: string | null;
  /** "위임 · 검토 승인 · 완료 승인 요청은 못 합니다 — Lead 의 일". 전부면 null. */
  cannot: string | null;
  /** 사람 말 목록(전부여도 채운다 — 펼쳐 볼 수 있게). */
  labels: string[];
  denied: ColabCommand[];
}

/** 명령 목록 → 사람 말 요약. `commands` 가 비면 계약은 "전부" 다(daemon-protocol §4.1 "비면 전부"). */
export function summarizeCommands(role: AgentRole, commands: readonly ColabCommand[] | null | undefined): CommandSummary {
  const allowed = commands && commands.length ? ALL_COMMANDS.filter((c) => commands.includes(c)) : [...ALL_COMMANDS];
  const denied = ALL_COMMANDS.filter((c) => !allowed.includes(c));
  const labels = allowed.map((c) => COMMAND_LABEL[c]);
  if (denied.length === 0) {
    return { can: ROLE_COMMANDS.all, allNote: role === "lead" ? ROLE_COMMANDS.all_lead : role === "custom" ? ROLE_COMMANDS.all_custom : null, cannot: null, labels, denied };
  }
  const reason = role === "reviewer" ? ROLE_COMMANDS.reason_reviewer : ROLE_COMMANDS.reason_worker;
  return {
    can: labels.join(" · "),
    allNote: null,
    cannot: `${ROLE_COMMANDS.cannot(denied.map((c) => COMMAND_LABEL[c]).join(" · "))} — ${reason}`,
    labels,
    denied,
  };
}
