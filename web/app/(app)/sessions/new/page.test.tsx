/**
 * S6 마법사 6단계 — `artifact_submitted` 의 **제출자**(W-4).
 *
 * PRD §4 시나리오 A 3단계의 종료 조건은 "**Writer 가** 아티팩트 제출" 이고, EVAL E6-02 는
 * "지정 에이전트가 아닌 다른 에이전트가 제출하면 미충족" 이다. 그 지정을 Director 가 화면에서
 * 만들 수 있어야 두 문장이 웹에서 성립한다 — 마법사가 `who: "assignee"` 로 고정하면
 * E6-02 를 만들 방법이 없다.
 *
 * 전송 모양은 계약이 정한다(CompletionAtom): `who` 는 역할(`assignee`), `agent_id` 는
 * **`who` 대신** 쓰는 지정 에이전트다. 둘을 함께 보내지 않는다.
 */
import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Agent, Me, Member, Runtime } from "@/lib/api/types";

const push = vi.fn();
const replace = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ push, replace }) }));

const get = vi.fn();
const post = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { get: (...a: unknown[]) => get(...a), post: (...a: unknown[]) => post(...a) } };
});

const me: Me = {
  user: { id: "u1", email: "dir@example.com", display_name: "Director", avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
  workspaces: [{ id: "w1", name: "Colab", slug: "colab", my_role: "owner", created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" }],
  pending_invites: [],
};
vi.mock("@/lib/auth/AuthContext", () => ({
  useAuth: () => ({ me, workspace: me.workspaces[0], loading: false, canManage: true, selectWorkspace: vi.fn(), refresh: vi.fn(), logout: vi.fn() }),
}));

import NewSessionPage from "./page";

const runtime: Runtime = {
  id: "r1", workspace_id: "w1", name: "MacBook", host: null, status: "online", daemon_version: "0.4.0",
  last_seen_at: null, capabilities: [{ kind: "claude_code", logged_in: true, models: ["claude-sonnet-5"] }],
  repos: [], max_concurrent_tasks: null, running_task_count: 0, workdir_disk_bytes: 0,
  offline_since: null, grace_ends_at: null, paused_session_count: 0,
  created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z",
};

const agent = (id: string, name: string, role: Agent["role"]): Agent => ({
  id, workspace_id: "w1", name, role, role_description: `${name} 역할`, instructions: "",
  tools: [], owner_id: "u1", respond_to: "workspace", respond_to_allowlist: [],
  max_concurrent_tasks: 3, status: "idle",
  profiles: [{
    id: `p-${id}`, agent_id: id, name: "default", runtime_kind: "claude_code", model: "claude-sonnet-5",
    options: {}, env: {}, args: [], is_default: true, fallback_profile_id: null,
    created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z",
  }],
  invitable: { allowed: true },
  created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z",
});

const LEAD = agent("a-lead", "Lead", "lead");
const WRITER = agent("a-writer", "Writer", "writer");

const member: Member = { id: "m1", workspace_id: "w1", user: me.user, role: "owner", created_at: "2026-09-06T09:00:00Z" };

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  replace.mockReset();
  get.mockImplementation((path: string) => {
    if (path.endsWith("/runtimes")) return Promise.resolve([runtime]);
    if (path.endsWith("/agents")) return Promise.resolve({ items: [LEAD, WRITER] });
    if (path.endsWith("/members")) return Promise.resolve({ items: [member] });
    if (path.endsWith("/sessions")) return Promise.resolve({ items: [] });
    if (path.endsWith("/runtime-candidates")) return Promise.resolve({ auto_select_allowed: true, candidates: [{ runtime, eligible: true }] });
    return Promise.resolve({ items: [] });
  });
  post.mockResolvedValue({ id: "s1" });
});
afterEach(cleanup);

const next = () => fireEvent.click(screen.getByTestId("wizard-next"));

/** 1 goal → … → 6 종료 조건. 격리는 기본 `none` 이라 런타임을 고르지 않아도 지나간다. */
async function walkToConditions(participants: string[]) {
  render(<NewSessionPage />);
  await screen.findByTestId("session-wizard");
  fireEvent.change(screen.getByTestId("session-title"), { target: { value: "시나리오 A" } });
  fireEvent.change(screen.getByTestId("session-goal"), { target: { value: "리서치하고 초안을 낸다" } });
  next(); // 2 Director
  next(); // 3 격리
  next(); // 4 런타임
  next(); // 5 참여자
  await waitFor(() => expect(screen.getAllByTestId("participant-option").length).toBe(2));
  for (const id of participants) {
    const card = screen.getAllByTestId("participant-option").find((el) => el.dataset.agentId === id)!;
    fireEvent.click(card.querySelector('input[type="checkbox"]')!);
  }
  next(); // 6 종료 조건
  await screen.findByTestId("wizard-conditions");
}

async function start() {
  next(); // 7 한도
  fireEvent.click(screen.getByTestId("session-start"));
  await waitFor(() => expect(post).toHaveBeenCalled());
  return post.mock.calls.at(-1)![1].body.completion_condition;
}

describe("S6 6단계 — artifact_submitted 의 제출자(W-4)", () => {
  it("기본값은 assignee 다 — 지정 없이 보내면 `{type, who: 'assignee'}` 하나뿐이다", async () => {
    await walkToConditions([LEAD.id, WRITER.id]);
    expect((screen.getByTestId("submitter-select") as HTMLSelectElement).value).toBe("");
    expect(screen.getAllByTestId("condition-row").find((el) => el.dataset.type === "artifact_submitted")!.textContent).toContain("(assignee)");

    expect(await start()).toEqual({
      op: "and",
      conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }],
    });
  });

  it("참여자 중 하나를 고르면 `{type, agent_id}` 로만 보낸다 — `who` 는 함께 보내지 않는다(계약: agent_id 는 who 대신)", async () => {
    await walkToConditions([LEAD.id, WRITER.id]);
    fireEvent.change(screen.getByTestId("submitter-select"), { target: { value: WRITER.id } });
    expect(screen.getAllByTestId("condition-row").find((el) => el.dataset.type === "artifact_submitted")!.textContent).toContain("(@Writer)");

    expect(await start()).toEqual({
      op: "and",
      conditions: [{ type: "artifact_submitted", agent_id: WRITER.id }, { type: "user_approval" }],
    });
  });

  it("제출자로 고른 에이전트를 참여자에서 빼면 assignee 로 돌아간다 — 아무도 못 채우는 조건을 만들지 않는다", async () => {
    await walkToConditions([LEAD.id, WRITER.id]);
    fireEvent.change(screen.getByTestId("submitter-select"), { target: { value: WRITER.id } });

    fireEvent.click(screen.getByTestId("wizard-back")); // 5 참여자
    const card = screen.getAllByTestId("participant-option").find((el) => el.dataset.agentId === WRITER.id)!;
    fireEvent.click(card.querySelector('input[type="checkbox"]')!); // Writer 해제
    next();
    await waitFor(() => expect((screen.getByTestId("submitter-select") as HTMLSelectElement).value).toBe(""));

    expect(await start()).toEqual({
      op: "and",
      conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }],
    });
  });

  it("artifact_submitted 를 끄면 제출자 선택도 사라진다", async () => {
    await walkToConditions([LEAD.id, WRITER.id]);
    const row = screen.getAllByTestId("condition-row").find((el) => el.dataset.type === "artifact_submitted")!;
    fireEvent.click(row);
    expect(screen.queryByTestId("submitter-select")).toBeNull();
  });
});
