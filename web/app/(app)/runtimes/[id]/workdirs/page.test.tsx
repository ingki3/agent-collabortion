/**
 * S13 Workdir 관리 — **삭제는 기본 차단이고, 차단 사유는 두 갈래다**(FR-6.4 M4 · E13-12·13 · U6 10·11).
 *
 * 이 화면에서 틀리기 쉬운 것은 "지운다/못 지운다" 가 아니라 **왜 못 지우는지**다. `unmerged_commits` 는
 * 병합을 요구하고 `uncommitted_changes` 는 커밋 또는 폐기를 요구한다 — 한 문장으로 뭉치면 사람이
 * 다음에 무엇을 할지 알 수 없다. 409 를 받은 뒤에야 `force` 로 다시 보내는 것도 여기서 못 박는다:
 * 처음부터 `force` 로 보내면 계약의 기본 차단이 화면에서 사라진다.
 */
import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Me, Runtime, Workdir } from "@/lib/api/types";

vi.mock("next/navigation", () => ({ useParams: () => ({ id: "r1" }) }));

const get = vi.fn();
const del = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { get: (...a: unknown[]) => get(...a), delete: (...a: unknown[]) => del(...a) } };
});

const me: Me = {
  user: { id: "u1", email: "dir@example.com", display_name: "Director", avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
  workspaces: [{ id: "w1", name: "Colab", slug: "colab", my_role: "owner", created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" }],
  pending_invites: [],
};
vi.mock("@/lib/auth/AuthContext", () => ({
  useAuth: () => ({ me, workspace: me.workspaces[0], loading: false, canManage: true, selectWorkspace: vi.fn(), refresh: vi.fn(), logout: vi.fn() }),
}));
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => undefined }));

import WorkdirsPage from "./page";
import { ApiError } from "@/lib/api/client";

const runtime: Runtime = {
  id: "r1", workspace_id: "w1", name: "데스크탑", host: null, status: "online", daemon_version: "0.4.0",
  last_seen_at: null, capabilities: [], repos: [], max_concurrent_tasks: null, running_task_count: 0,
  workdir_disk_bytes: 0, offline_since: null, grace_ends_at: null, paused_session_count: 0,
  created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z",
};

const GB = 1024 ** 3;
const wd = (over: Partial<Workdir> & Pick<Workdir, "id">): Workdir => ({
  session_id: "s1", session: { id: "s1", title: "결제 구현", status: "completed" },
  agent_id: "ag1", lane_id: null, kind: "worktree", path_or_ref: "~/dev/app-wt/backend", branch: "colab/S/backend",
  status: "retained", disk_bytes: GB, last_used_at: "2026-09-01T00:00:00Z",
  retain_until: "2026-09-20T00:00:00Z", dirty: false, merged: true, commits_ahead: 0, gc_blocked_reason: null,
  created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z", ...over,
});

const rows: Workdir[] = [
  wd({ id: "w-clean" }),
  wd({ id: "w-unmerged", branch: "colab/S/frontend", merged: false, commits_ahead: 3, gc_blocked_reason: "unmerged_commits" }),
  wd({ id: "w-dirty", branch: "colab/S/qa", dirty: true, commits_ahead: 0, gc_blocked_reason: "uncommitted_changes" }),
];

function mockList(quotaGb: number | null = 50, total = 3 * GB, items: Workdir[] = rows) {
  get.mockImplementation((path: string) => {
    if (path.includes("/runtimes") && path.includes("workdirs")) return Promise.resolve({ items, next_cursor: null, disk_bytes_total: total, disk_quota_gb: quotaGb });
    if (path.includes("/runtimes")) return Promise.resolve([runtime]);
    return Promise.resolve({ items: [{ id: "ag1", name: "Backend" }] });
  });
}

beforeEach(() => {
  get.mockReset();
  del.mockReset();
  mockList();
});
afterEach(cleanup);

describe("목록과 상단 사용률", () => {
  it("행마다 종류·경로·브랜치·용량·보존 만료·상태를 보여 준다(SCREEN §4.8 · U6 9)", async () => {
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getAllByTestId("workdir-row").length).toBe(3));
    const row = screen.getAllByTestId("workdir-row")[0];
    expect(row.querySelector('[data-testid="workdir-kind"]')!.textContent).toBe("worktree");
    expect(row.querySelector('[data-testid="workdir-branch"]')!.textContent).toBe("colab/S/backend");
    expect(row.querySelector('[data-testid="workdir-size"]')!.textContent).toBe("1.0GB");
    expect(row.querySelector('[data-testid="workdir-status"]')!.textContent).toBe("보존 중");
    expect(row.querySelector('[data-testid="workdir-retention"]')!.textContent).toContain("2026");
    // 삭제해도 브랜치는 남는다 — 그 사실을 삭제 전에 말한다(FR-6.4 M4).
    expect(row.querySelector('[data-testid="workdir-branch-note"]')!.textContent).toContain("브랜치는 남습니다");
  });

  it("용량 상한에 도달하면(≥) 새 세션이 막힌다고 미리 말한다(E13-16)", async () => {
    mockList(50, 50 * GB);
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getByTestId("workdir-quota").getAttribute("data-at-limit")).toBe("true"));
    expect(screen.getByTestId("workdir-quota-full").textContent).toContain("새 세션");
  });

  it("상한이 없으면 무제한이라고 말하고 막대를 그리지 않는다(E13-19)", async () => {
    mockList(null, 900 * GB);
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getByTestId("workdir-quota").getAttribute("data-at-limit")).toBe("false"));
    expect(screen.getByTestId("workdir-quota").textContent).toContain("무제한");
  });

  it("GC 가 막은 행이 있으면 상단에서 그 수를 알린다(FR-6.4 '삭제하지 않고 알린다')", async () => {
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getByTestId("workdir-gc-blocked-alert")).toBeTruthy());
    expect(screen.getByTestId("workdir-gc-blocked-alert").textContent).toContain("2개");
  });
});

describe("차단 사유는 두 갈래다(E13-12 · E13-13)", () => {
  it("미병합 커밋은 '병합해라', 미커밋 변경은 '커밋하거나 버려라' — 다음 행동이 다르다", async () => {
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getAllByTestId("workdir-gc-block").length).toBe(2));
    const byId = (id: string) => screen.getAllByTestId("workdir-row").find((r) => r.getAttribute("data-workdir-id") === id)!;
    const unmerged = byId("w-unmerged").querySelector('[data-testid="workdir-gc-next"]')!.textContent!;
    const dirty = byId("w-dirty").querySelector('[data-testid="workdir-gc-next"]')!.textContent!;
    expect(unmerged).toContain("병합");
    expect(dirty).toContain("커밋");
    expect(unmerged).not.toBe(dirty);
    expect(byId("w-unmerged").querySelector('[data-testid="workdir-gc-title"]')!.textContent).toContain("3개");
    // 차단이 없는 행에는 경고를 붙이지 않는다.
    expect(byId("w-clean").querySelector('[data-testid="workdir-gc-block"]')).toBeNull();
  });
});

describe("수동 삭제 — 기본 차단 → 사유 → 확인 → force", () => {
  it("처음 삭제는 force 없이 보낸다 — 계약의 기본 차단을 화면이 먼저 지우면 안 된다", async () => {
    del.mockResolvedValue(undefined);
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getAllByTestId("workdir-delete").length).toBe(3));
    fireEvent.click(screen.getAllByTestId("workdir-delete")[0]);
    await waitFor(() => expect(del).toHaveBeenCalled());
    expect((del.mock.calls[0][1] as { query?: unknown }).query).toBeUndefined();
  });

  it("409 workdir_dirty 를 받으면 사유를 보여 주고, 확인해야 force 로 다시 보낸다", async () => {
    del.mockRejectedValueOnce(new ApiError({ type: "about:blank", title: "conflict", status: 409, code: "workdir_dirty", detail: "unmerged_commits" }));
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getAllByTestId("workdir-row").length).toBe(3));
    const row = screen.getAllByTestId("workdir-row").find((r) => r.getAttribute("data-workdir-id") === "w-unmerged")!;
    fireEvent.click(row.querySelector('[data-testid="workdir-delete"]')!);
    await waitFor(() => expect(screen.getByTestId("workdir-refused")).toBeTruthy());
    expect(screen.getByTestId("workdir-refused").textContent).toContain("unmerged_commits");

    del.mockResolvedValueOnce(undefined);
    fireEvent.click(screen.getByTestId("workdir-delete-force"));
    await waitFor(() => expect(del).toHaveBeenCalledTimes(2));
    expect((del.mock.calls[1][1] as { query: { force: boolean } }).query).toEqual({ force: true });
  });

  it("'그만두기' 는 아무것도 보내지 않는다 — 확인 화면은 되돌릴 수 있어야 한다", async () => {
    del.mockRejectedValueOnce(new ApiError({ type: "about:blank", title: "conflict", status: 409, code: "workdir_dirty", detail: "uncommitted_changes" }));
    render(<WorkdirsPage />);
    await waitFor(() => expect(screen.getAllByTestId("workdir-row").length).toBe(3));
    const row = screen.getAllByTestId("workdir-row").find((r) => r.getAttribute("data-workdir-id") === "w-dirty")!;
    fireEvent.click(row.querySelector('[data-testid="workdir-delete"]')!);
    await waitFor(() => expect(screen.getByTestId("workdir-refused")).toBeTruthy());
    fireEvent.click(screen.getByTestId("workdir-delete-abort"));
    await waitFor(() => expect(screen.queryByTestId("workdir-refused")).toBeNull());
    expect(del).toHaveBeenCalledTimes(1);
  });
});
