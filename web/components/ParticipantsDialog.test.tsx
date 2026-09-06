/**
 * 참여 에이전트 추가·제거(SCREEN §4.5 O2 · FR-1.5 · FR-1.9 · §8.2 Q3).
 *
 * 세 규칙을 못 박는다.
 *   1. **초대 권한은 `respond_to` 가 정한다** — 계약 `Agent.invitable` 을 읽고 비활성 + 사유를 낸다.
 *      화면이 규칙을 다시 계산하면 서버와 반대로 말한다(S-1 이 그랬다).
 *   2. **아카이브된 에이전트는 신규 초대 목록에서 빠진다**(Q3 Lead 결정). 과거 칩은 `AgentChip` 이 유지한다.
 *   3. **진행 중 lane 이 있으면 제거 불가**(계약 removeParticipant 409, 4상태). assignee 도 불가.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ParticipantsDialog, removalBlock } from "./ParticipantsDialog";
import { AgentChip } from "./AgentChip";
import type { Agent, Lane, Participant } from "@/lib/api/types";

afterEach(cleanup);

const profile = (id: string) => ({
  id, agent_id: "x", name: "default", runtime_kind: "claude_code" as const, model: "claude-sonnet-5",
  options: {}, env: {}, args: [], is_default: true, fallback_profile_id: null, created_at: "", updated_at: "",
});

const agent = (over: Partial<Agent> & { id: string; name: string }): Agent => ({
  workspace_id: "w1", role: "researcher", role_description: "", instructions: "", tools: [], owner_id: "u1",
  respond_to: "workspace", respond_to_allowlist: [], avatar_url: null, budget_per_task: null, max_concurrent_tasks: 3,
  definition_source: null, definition_version: null, definition_update_available: null, status: "idle",
  profiles: [profile(`p-${over.id}`)], invitable: { allowed: true, reason: null },
  usage: { cost_usd: 0, task_count: 0 }, archived_at: null, created_at: "", updated_at: "",
  ...over,
});

const part = (agentId: string, name: string, isAssignee = false): Participant => ({
  session_id: "s1", agent_id: agentId,
  agent: { id: agentId, name, role: "researcher", role_description: "", avatar_url: null, respond_to: "workspace" },
  profile: profile(`p-${agentId}`), status: "idle", status_note: null, is_assignee: isAssignee,
  mention_link: `[@${name}](mention://agent/${agentId})`, warnings: [], joined_at: "",
});

const lane = (agentId: string, status: Lane["status"]): Lane => ({
  id: `l-${agentId}-${status}`, session_id: "s1", parent_lane_id: null, agent_id: agentId, agent_name: "x",
  profile_id: "p", depends_on: [], workdir_id: null, workdir_ref: null, delegated_from_task_id: null,
  status, blocked_note: null, blocked_message_id: null, reentry_count: 0, actions: [],
  created_at: "", updated_at: "", finished_at: null,
});

const base = {
  participants: [part("a1", "Lead", true), part("a2", "Researcher")],
  lanes: [] as Lane[],
  assigneeAgentId: "a1",
  canManage: true,
  onAdd: vi.fn(), onRemove: vi.fn(), onSetAssignee: vi.fn(), onClose: vi.fn(),
};

describe("초대 후보(FR-1.9 · Q3)", () => {
  it("보관된 에이전트는 신규 초대 목록에서 빠진다 — 과거 칩은 그대로 둔다", () => {
    const agents = [agent({ id: "a3", name: "Writer" }), agent({ id: "a4", name: "옛Writer", archived_at: "2026-01-01T00:00:00Z" })];
    render(<ParticipantsDialog {...base} agents={agents} />);
    const opts = [...(screen.getByTestId("participant-pick") as HTMLSelectElement).options].map((o) => o.value);
    expect(opts).toContain("a3");
    expect(opts).not.toContain("a4");

    // 같은 에이전트라도 과거 자리의 칩은 유지되고 "보관됨" 만 붙는다.
    render(<AgentChip agentId="a4" name="옛Writer" status="idle" archived />);
    expect(screen.getByTestId("agent-chip-archived").textContent).toBe("보관됨");
  });

  it("이미 참여 중인 에이전트도 후보에서 빠진다", () => {
    render(<ParticipantsDialog {...base} agents={[agent({ id: "a2", name: "Researcher" }), agent({ id: "a3", name: "Writer" })]} />);
    const opts = [...(screen.getByTestId("participant-pick") as HTMLSelectElement).options].map((o) => o.value);
    expect(opts).not.toContain("a2");
  });

  it("초대 불가는 **계약이 준 사유**로 비활성한다 — 규칙을 화면이 다시 계산하지 않는다", () => {
    const agents = [agent({ id: "a3", name: "Writer", respond_to: "nobody", invitable: { allowed: false, reason: "정지 상태입니다(respond_to: nobody)" } })];
    render(<ParticipantsDialog {...base} agents={agents} />);
    const opt = [...(screen.getByTestId("participant-pick") as HTMLSelectElement).options].find((o) => o.value === "a3")!;
    expect(opt.disabled).toBe(true);
    expect(opt.textContent).toContain("정지 상태");
    fireEvent.change(screen.getByTestId("participant-pick"), { target: { value: "a3" } });
    expect(screen.getByTestId("participant-not-invitable").textContent).toContain("정지 상태");
    expect((screen.getByTestId("participant-add") as HTMLButtonElement).disabled).toBe(true);
  });

  it("추가는 agent_id + profile_id 로 보낸다(계약 addParticipant)", () => {
    const onAdd = vi.fn();
    render(<ParticipantsDialog {...base} agents={[agent({ id: "a3", name: "Writer" })]} onAdd={onAdd} />);
    fireEvent.change(screen.getByTestId("participant-pick"), { target: { value: "a3" } });
    fireEvent.click(screen.getByTestId("participant-add"));
    expect(onAdd).toHaveBeenCalledWith("a3", null);
  });
});

describe("제거 조건(계약 removeParticipant 409)", () => {
  it("진행 중 lane 4상태 전부가 제거를 막는다", () => {
    for (const st of ["queued", "running", "waiting_human", "paused"] as const) {
      expect(removalBlock("a2", [lane("a2", st)], "a1")).toContain("진행 중 lane");
    }
    // 끝난 lane 은 막지 않는다.
    expect(removalBlock("a2", [lane("a2", "done"), lane("a2", "failed")], "a1")).toBeNull();
  });

  it("assignee 는 제거할 수 없고 사유가 다음 할 일을 말한다", () => {
    expect(removalBlock("a1", [], "a1")).toContain("다른 assignee");
  });

  it("막힌 이유가 버튼 툴팁으로 나온다 — 비활성 + 사유(SCREEN §7)", () => {
    render(<ParticipantsDialog {...base} agents={[]} lanes={[lane("a2", "running")]} />);
    const rows = screen.getAllByTestId("participant-remove") as HTMLButtonElement[];
    expect(rows[1].disabled).toBe(true);
    expect(rows[1].title).toContain("진행 중 lane");
  });
});

describe("권한 없음(S7-P·S7-D)", () => {
  it("Director 가 아니면 전부 비활성이고 사유가 붙는다 — 다이얼로그는 열린다", () => {
    render(<ParticipantsDialog {...base} agents={[agent({ id: "a3", name: "Writer" })]} canManage={false} />);
    expect((screen.getByTestId("participant-pick") as HTMLSelectElement).disabled).toBe(true);
    expect((screen.getByTestId("participant-add") as HTMLButtonElement).title).toContain("Director 만");
    for (const b of screen.getAllByTestId("participant-remove") as HTMLButtonElement[]) expect(b.disabled).toBe(true);
    expect(screen.getByTestId("participants-current")).toBeTruthy();
  });
});
