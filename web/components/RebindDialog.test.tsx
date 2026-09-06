/**
 * S17 재바인딩 다이얼로그(SCREEN §4.9 · E14-03~07 · U12 4·5).
 *
 * 이 다이얼로그가 지켜야 하는 것은 **두 경로의 차이**다(U12 성공 기준: "4와 5의 차이를 이해"):
 *   · `none`     — 후보는 온라인 런타임 전부, 유실 경고 **없음**, 확인 체크박스도 없다.
 *   · `worktree` — 후보는 remote URL 이 같은 머신만, 유실 경고 **있음**, 확인 없이는 보낼 수 없고
 *                  diff 아티팩트가 **제출 순서 그대로** 미리 보인다(E14-06: 뒤바뀐 diff 는 충돌한다).
 * 그리고 후보가 아닌 런타임은 **숨기지 않고 사유와 함께 비활성**이다 — 사라진 선택지는 이유를 말하지 못한다.
 */
import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Artifact, Runtime, RuntimeCandidate } from "@/lib/api/types";

const get = vi.fn();
const post = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { get: (...a: unknown[]) => get(...a), post: (...a: unknown[]) => post(...a) } };
});

import { RebindDialog, lossWarning, offlineSentence } from "./RebindDialog";

const rt = (id: string, name: string, status: Runtime["status"] = "online"): Runtime => ({
  id, workspace_id: "w1", name, host: null, status, daemon_version: "0.4.0", last_seen_at: "2026-09-06T09:00:00Z",
  capabilities: [], repos: [], max_concurrent_tasks: null, running_task_count: 0, workdir_disk_bytes: 0,
  offline_since: null, grace_ends_at: null, paused_session_count: 0,
  created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z",
});

const candidates: RuntimeCandidate[] = [
  { runtime: rt("r2", "데스크탑"), eligible: true, reason: null, matched_repo: { path: "/srv/app", remote_url: "git@x:app.git", branch: "main", clean: true } },
  { runtime: rt("r3", "다른 저장소"), eligible: false, reason: "같은 remote URL 의 저장소가 없음" },
];

const diffs: Artifact[] = [1, 2, 3].map((n) => ({
  id: `a${n}`, session_id: "s1", name: `diff-${n}.patch`, version: 1, type: "diff",
  storage_ref: `mock://${n}`, size_bytes: 100, content_type: "text/x-diff", submitted_by_task_id: null,
  submitted_by: { agent_id: "ag1", agent_name: "Frontend" }, description: null, latest: true,
  created_at: `2026-09-0${n}T00:00:00Z`,
}));

const session = (kind: "none" | "worktree") => ({
  id: "s1", title: "결제 구현", status: "paused" as const, workspace_id: "w1",
  isolation: { kind, remote_url: "git@x:app.git" },
  paused_detail: { reason: "runtime_offline" as const, paused_at: "2026-08-28T00:00:00Z", runtime: { offline_since: "2026-08-27T00:00:00Z" } },
  runtime: { name: "MacBook" },
});

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  get.mockImplementation((path: string) =>
    path.includes("runtime-candidates") ? Promise.resolve({ auto_select_allowed: false, candidates }) : Promise.resolve(diffs),
  );
});
afterEach(cleanup);

describe("상황 문장 · 유실 경고 문구", () => {
  it("오프라인 일수와 정지 시각을 한 문장으로 말한다(SCREEN §4.9 상황 칸)", () => {
    const s = offlineSentence("MacBook", "2026-08-30T00:00:00Z", "2026-09-06T00:00:00Z");
    expect(s).toContain("MacBook");
    expect(s).toContain("일간 오프라인");
    expect(s).toContain("일시정지");
  });

  it("유실 경고는 아티팩트 수와 '커밋 이력은 복원되지 않습니다' 를 담는다(U12 5 성공 기준)", () => {
    const w = lossWarning(3);
    expect(w).toContain("3개");
    expect(w).toContain("커밋 이력은 복원되지 않습니다");
  });
});

describe("worktree — 유실 경고와 확인 게이트", () => {
  it("경고를 확인하기 전에는 재바인딩을 보낼 수 없다(계약 acknowledge_loss, 없으면 422)", async () => {
    render(<RebindDialog session={session("worktree")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("rebind-loss")).toBeTruthy());
    expect((screen.getByTestId("rebind-submit") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("rebind-ack"));
    expect((screen.getByTestId("rebind-submit") as HTMLButtonElement).disabled).toBe(false);
  });

  it("diff 아티팩트를 제출 순서 그대로 미리 보여 준다(E14-06)", async () => {
    render(<RebindDialog session={session("worktree")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getAllByTestId("rebind-artifact").length).toBe(3));
    expect(screen.getAllByTestId("rebind-artifact").map((li) => li.getAttribute("data-artifact-id"))).toEqual(["a1", "a2", "a3"]);
  });

  it("보내는 페이로드는 runtime_id + acknowledge_loss 다", async () => {
    post.mockResolvedValue({});
    render(<RebindDialog session={session("worktree")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("rebind-ack")).toBeTruthy());
    fireEvent.click(screen.getByTestId("rebind-ack"));
    fireEvent.click(screen.getByTestId("rebind-submit"));
    await waitFor(() => expect(post).toHaveBeenCalled());
    expect(post.mock.calls[0][0]).toBe("/sessions/{sessionId}/rebind");
    expect((post.mock.calls[0][1] as { body: unknown }).body).toEqual({ runtime_id: "r2", acknowledge_loss: true });
  });
});

describe("none — 잃을 워크트리가 없다", () => {
  it("유실 경고도 확인 체크박스도 내지 않는다(U12 4단계 '유실 경고 없음')", async () => {
    render(<RebindDialog session={session("none")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("rebind-candidates")).toBeTruthy());
    expect(screen.queryByTestId("rebind-loss")).toBeNull();
    expect((screen.getByTestId("rebind-submit") as HTMLButtonElement).disabled).toBe(false);
  });

  it("acknowledge_loss 를 보내지 않는다 — worktree 가 아닌데 확인을 요구하면 없는 위험을 말하는 것이다", async () => {
    post.mockResolvedValue({});
    render(<RebindDialog session={session("none")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("rebind-submit")).toBeTruthy());
    fireEvent.click(screen.getByTestId("rebind-submit"));
    await waitFor(() => expect(post).toHaveBeenCalled());
    expect((post.mock.calls[0][1] as { body: unknown }).body).toEqual({ runtime_id: "r2" });
  });
});

describe("후보 목록", () => {
  it("후보가 아닌 런타임도 사유와 함께 남는다 — 비활성 + 사유(SCREEN §7)", async () => {
    render(<RebindDialog session={session("worktree")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getAllByTestId("rebind-candidate").length).toBe(2));
    const off = screen.getAllByTestId("rebind-candidate").find((li) => li.getAttribute("data-eligible") === "false")!;
    expect(off.textContent).toContain("같은 remote URL");
    expect((off.querySelector("input") as HTMLInputElement).disabled).toBe(true);
  });

  it("후보가 하나도 없으면 그 사실과 이유를 말한다", async () => {
    get.mockImplementation((path: string) =>
      path.includes("runtime-candidates")
        ? Promise.resolve({ auto_select_allowed: false, candidates: [{ runtime: rt("r3", "다른 저장소"), eligible: false, reason: "저장소가 없음" }] })
        : Promise.resolve([]),
    );
    render(<RebindDialog session={session("worktree")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("rebind-no-candidate")).toBeTruthy());
    expect((screen.getByTestId("rebind-submit") as HTMLButtonElement).disabled).toBe(true);
  });

  it("세션 종료는 한 번 더 확인을 받는다(E14-07 — 되돌릴 수 없다)", async () => {
    post.mockResolvedValue({});
    render(<RebindDialog session={session("none")} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("rebind-end")).toBeTruthy());
    fireEvent.click(screen.getByTestId("rebind-end"));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("rebind-end-confirm"));
    await waitFor(() => expect(post).toHaveBeenCalled());
    expect(post.mock.calls[0][0]).toBe("/sessions/{sessionId}/cancel");
  });
});
