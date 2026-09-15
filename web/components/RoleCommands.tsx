"use client";
/**
 * S10 역할 구역 — 역할이 정하는 **허용 명령**(v1.1 K-19, PRD FR-1.9.1 · `colab-cli.md` §2.5)을 사람 말로, 읽기 전용.
 *
 * "이 에이전트가 할 수 있는 일: 메시지 게시 · 산출물 제출 · …" + 못 하는 것 한 줄("위임 · 검토 승인 · 완료 승인 요청은 못 합니다 — Lead 의 일").
 * `lead`·`custom` 은 "전부". 정본은 서버가 role 로 계산한 `Agent.allowed_commands` 이고, select 로 다른 역할을 골라 아직 저장하지 않았을 때는
 * 같은 표(`lib/commands.ts`)로 미리 보여 주고 "저장하면 이 목록으로 바뀝니다" 라고 말한다. 명령 이름(`lane_delegate`)은 화면에 나오지 않는다.
 */
import { commandsForRole, summarizeCommands } from "@/lib/commands";
import { ROLE_COMMANDS } from "@/lib/wording";
import type { AgentRole, ColabCommand } from "@/lib/api/types";
import "./role-commands.css";

export interface RoleCommandsProps {
  /** 지금 고른 역할(select 값). */
  role: AgentRole;
  /** 서버가 준 `Agent.allowed_commands` — 저장된 역할의 것. 없으면(새 에이전트·옛 서버) 표로 계산한다. */
  commands?: readonly ColabCommand[] | null;
  /** 고른 역할이 저장된 역할과 다른가 — 그러면 서버 값 대신 표로 미리 보인다. */
  preview?: boolean;
}

export function RoleCommands({ role, commands, preview = false }: RoleCommandsProps) {
  const list = !preview && commands && commands.length ? commands : commandsForRole(role);
  const s = summarizeCommands(role, list);
  return (
    <div className="role-cmds" data-testid="role-commands" data-role={role} data-preview={preview ? "true" : "false"} data-count={list.length}>
      <p className="role-cmds__line">
        <span className="role-cmds__head">{ROLE_COMMANDS.head}</span>{" "}
        <span className="role-cmds__can" data-testid="role-commands-can">{s.can}</span>
        {s.allNote && <span className="role-cmds__note"> — {s.allNote}</span>}
      </p>
      {s.cannot && <p className="role-cmds__line role-cmds__cannot" data-testid="role-commands-cannot">{s.cannot}</p>}
      <p className="role-cmds__line role-cmds__meta" data-testid="role-commands-meta">{preview ? ROLE_COMMANDS.preview : ROLE_COMMANDS.readonly}</p>
    </div>
  );
}

export default RoleCommands;
