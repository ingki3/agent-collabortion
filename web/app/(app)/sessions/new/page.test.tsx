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

/**
 * P4 — 3단계 저장소 검증 · 4단계 후보 필터(E13-01 · E13-17 · U6 1·2 · U15 4).
 *
 * 마법사가 여기서 막지 않으면 사람은 여섯 단계를 다 채운 뒤 첫 `git worktree add` 에서 실패를 만난다.
 * 그래서 `ok: false` 는 **오류 배너가 아니라 폼 안의 사유 + 다음 버튼 비활성**이어야 하고(계약: 200 이다),
 * `worktree` 에서 "자동 선택" 은 **숨기지 않고 비활성 + 사유**여야 한다 — 사라진 선택지는 이유를 말하지 못한다.
 */
describe("S6 3·4단계 — worktree 저장소 검증과 런타임 후보(P4)", () => {
  const REPO = "~/dev/app";
  const withRepo = (over: Partial<import("@/lib/api/types").RepoCheck> = {}) => {
    get.mockImplementation((path: string) => {
      if (path.endsWith("/runtimes")) return Promise.resolve([{ ...runtime, repos: [{ path: REPO, remote_url: "git@x:app.git", branch: "main", clean: true }] }]);
      if (path.endsWith("/agents")) return Promise.resolve({ items: [LEAD, WRITER] });
      if (path.endsWith("/members")) return Promise.resolve({ items: [member] });
      if (path.endsWith("/sessions")) return Promise.resolve({ items: [] });
      // worktree 면 자동 선택이 허용되지 않는다(계약 auto_select_allowed: none 만 true).
      if (path.endsWith("/runtime-candidates")) return Promise.resolve({ auto_select_allowed: false, candidates: [{ runtime, eligible: true }] });
      return Promise.resolve({ items: [] });
    });
    post.mockImplementation((path: string) =>
      path.includes("repo-checks")
        ? Promise.resolve({ ok: true, repo_path: REPO, exists: true, is_git: true, clean: true, default_branch: "main", current_branch: "main", remote_url: "git@x:app.git", tracks_brief_file: false, problems: [], checked_at: "2026-09-07T00:00:00Z", ...over })
        : Promise.resolve({ id: "s1" }),
    );
  };

  async function toIsolation() {
    render(<NewSessionPage />);
    await screen.findByTestId("session-wizard");
    fireEvent.change(screen.getByTestId("session-title"), { target: { value: "결제" } });
    fireEvent.change(screen.getByTestId("session-goal"), { target: { value: "결제 모듈" } });
    next(); // 2 Director
    next(); // 3 격리
    fireEvent.click(screen.getByTestId("isolation-worktree").querySelector("input")!);
    fireEvent.change(screen.getByTestId("repo-select"), { target: { value: REPO } });
  }

  it("검증 결과를 데몬 probe 그대로 보여 준다 — 경로·git·클린·브랜치·remote(FR-9)", async () => {
    withRepo();
    await toIsolation();
    await waitFor(() => expect(screen.getByTestId("repo-check")).toBeTruthy());
    expect(screen.getByTestId("repo-check").getAttribute("data-ok")).toBe("true");
    expect(screen.getByTestId("repo-check-line").textContent).toContain("클린");
    expect(screen.getByTestId("repo-check-git").textContent).toContain("git@x:app.git");
    expect(screen.getByTestId("worktree-binding-note").textContent).toContain("에이전트당 워크트리 1개");
  });

  it("E13-01 — 미커밋 변경이면 사유를 보여 주고 다음 버튼을 막는다(200 이지 오류가 아니다)", async () => {
    withRepo({ ok: false, clean: false, problems: ["미커밋 변경 3개"] });
    await toIsolation();
    await waitFor(() => expect(screen.getByTestId("repo-check").getAttribute("data-ok")).toBe("false"));
    expect(screen.getByTestId("repo-check-problems").textContent).toContain("미커밋 변경 3개");
    expect((screen.getByTestId("wizard-next") as HTMLButtonElement).disabled).toBe(true);
    // 오류 배너로 덮지 않는다 — `ok:false` 는 답이다.
    expect(screen.queryByTestId("repo-check-error")).toBeNull();
  });

  it("다시 확인 버튼이 있다 — 마지막 probe 뒤에 만든 저장소는 다음 probe 까지 '없음'으로 읽힌다(계약 checkRepo)", async () => {
    withRepo();
    await toIsolation();
    await waitFor(() => expect(screen.getByTestId("repo-recheck")).toBeTruthy());
    const before = post.mock.calls.filter((c) => String(c[0]).includes("repo-checks")).length;
    fireEvent.click(screen.getByTestId("repo-recheck"));
    await waitFor(() => expect(post.mock.calls.filter((c) => String(c[0]).includes("repo-checks")).length).toBe(before + 1));
  });

  it("E13-17 · U6 2 — worktree 에서 '자동 선택' 은 숨지 않고 비활성 + 사유다", async () => {
    withRepo();
    await toIsolation();
    await waitFor(() => expect(screen.getByTestId("repo-check").getAttribute("data-ok")).toBe("true"));
    next(); // 4 런타임
    await waitFor(() => expect(screen.getByTestId("runtime-auto")).toBeTruthy());
    const auto = screen.getByTestId("runtime-auto");
    expect(auto.getAttribute("data-allowed")).toBe("false");
    expect((auto.querySelector("input") as HTMLInputElement).disabled).toBe(true);
    expect(auto.textContent).toContain("worktree 격리에서는 고를 수 없습니다");
  });
});
