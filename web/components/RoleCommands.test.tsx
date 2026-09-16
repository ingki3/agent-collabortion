/**
 * S10 역할 구역의 허용 명령(v1.1 K-19) — 역할 6종 · 서버 값 우선 · 미리보기 · 명령 이름이 화면에 없다.
 */
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { RoleCommands } from "./RoleCommands";
import { commandsForRole } from "@/lib/commands";
import type { AgentRole } from "@/lib/api/types";

afterEach(cleanup);
const ROLES: AgentRole[] = ["lead", "researcher", "writer", "engineer", "reviewer", "custom"];

describe("RoleCommands", () => {
  it.each(ROLES)("%s — 머리말 + 할 수 있는 일 + (못 하는 것) + 읽기 전용 안내, 명령 이름 없음", (role) => {
    render(<RoleCommands role={role} commands={commandsForRole(role)} />);
    const box = screen.getByTestId("role-commands");
    expect(box.getAttribute("data-role")).toBe(role);
    expect(box.getAttribute("data-count")).toBe(String(commandsForRole(role).length));
    expect(box.textContent).toContain("이 에이전트가 할 수 있는 일:");
    expect(box.textContent).not.toMatch(/[a-z]+_[a-z_]+/);
    if (role === "lead" || role === "custom") {
      expect(screen.getByTestId("role-commands-can").textContent).toBe("전부");
      expect(screen.queryByTestId("role-commands-cannot")).toBeNull();
    } else {
      expect(screen.getByTestId("role-commands-can").textContent).toContain("메시지 게시");
      expect(screen.getByTestId("role-commands-cannot").textContent).toContain("은 못 합니다 — ");
      expect(screen.getByTestId("role-commands-cannot").textContent).toContain("Lead 의 일");
    }
    expect(screen.getByTestId("role-commands-meta").textContent).toBe("역할이 정합니다 — 여기서 고칠 수 없습니다");
  });

  it("실무자 — 위임·검토 승인은 못 한다, 산출물 제출은 한다", () => {
    render(<RoleCommands role="engineer" commands={commandsForRole("engineer")} />);
    expect(screen.getByTestId("role-commands-can").textContent).toContain("산출물 제출");
    expect(screen.getByTestId("role-commands-cannot").textContent).toBe("위임 · 검토 승인 · 검토 반려 · 완료 승인 요청은 못 합니다 — 위임·검토 승인·완료 승인 요청은 Lead 의 일");
  });

  it("reviewer — 산출물 제출은 못 하고 검토 승인·반려는 한다", () => {
    render(<RoleCommands role="reviewer" commands={commandsForRole("reviewer")} />);
    expect(screen.getByTestId("role-commands-can").textContent).toContain("검토 승인 · 검토 반려");
    expect(screen.getByTestId("role-commands-cannot").textContent).toContain("산출물 제출");
  });

  it("서버 값(Agent.allowed_commands)이 정본 — 표와 달라도 서버 값을 그린다", () => {
    render(<RoleCommands role="researcher" commands={["message_post", "hitl_ask"]} />);
    expect(screen.getByTestId("role-commands-can").textContent).toBe("메시지 게시 · 사람에게 질문");
  });

  it("미리보기(고른 역할 ≠ 저장된 역할) — 서버 값 대신 표로, '저장하면 이 목록으로 바뀝니다'", () => {
    render(<RoleCommands role="reviewer" commands={commandsForRole("researcher")} preview />);
    expect(screen.getByTestId("role-commands").getAttribute("data-preview")).toBe("true");
    expect(screen.getByTestId("role-commands-can").textContent).toContain("검토 승인");
    expect(screen.getByTestId("role-commands-can").textContent).not.toContain("산출물 제출");
    expect(screen.getByTestId("role-commands-meta").textContent).toBe("저장하면 이 목록으로 바뀝니다");
  });

  it("서버 값이 없으면(새 에이전트·옛 서버) 표로 계산한다", () => {
    render(<RoleCommands role="writer" />);
    expect(screen.getByTestId("role-commands").getAttribute("data-count")).toBe("9");
  });
});
