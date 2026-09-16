/**
 * S5 세션 목록 — 카드 옵션(…) 삭제(T-W13, SCREEN §4.3).
 *   · 카드 구조: article 안에 링크(내용 전부) + 「…」 버튼이 형제 — a 안에 button 없음, 버튼 클릭이 카드 이동을 일으키지 않는다.
 *   · 활성/비활성: Director 인 끝난 세션만 활성, 진행 중·남의 세션은 비활성 + 사유(owner·admin 이면 남의 것도 활성).
 *   · 성공 경로: 다이얼로그 「삭제」 → 204 → 카드 즉시 제거 + 안내 한 줄. SSE `session.deleted` 로도 제거(두 경로 멱등).
 *   · S7 에서 돌아온 `?deleted=<제목>` → 안내 한 줄 + 주소 정리.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Me, SessionListItem, StreamEvent } from "@/lib/api/types";

const replace = vi.fn();
let searchParams = new URLSearchParams();
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace }), useSearchParams: () => searchParams }));

const get = vi.fn();
const del = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { ...actual.api, get: (...a: unknown[]) => get(...a), delete: (...a: unknown[]) => del(...a) } };
});

const me: Me = {
  user: { id: "u1", email: "dir@example.com", display_name: "Director", avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
  workspaces: [{ id: "w1", name: "Colab", slug: "colab", my_role: "member", created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" }],
  pending_invites: [],
};
let canManage = false;
vi.mock("@/lib/auth/AuthContext", () => ({
  useAuth: () => ({ me, workspace: me.workspaces[0], loading: false, canManage, selectWorkspace: vi.fn(), refresh: vi.fn(), logout: vi.fn() }),
}));

let streamHandler: ((ev: StreamEvent) => void) | null = null;
vi.mock("@/lib/realtime/StreamContext", () => ({
  useWorkspaceStream: (_ws: string, handler: (ev: StreamEvent) => void) => {
    streamHandler = handler;
    return "open";
  },
}));

import SessionsPage from "./page";
import { SESSION_MENU } from "@/lib/wording";
import { problemFixture } from "@/lib/mock/problem-fixture";

const other = { id: "u2", email: "other@example.com", display_name: "서연", avatar_url: null, created_at: "2026-09-06T09:00:00Z" };
const item = (id: string, over: Partial<SessionListItem> = {}): SessionListItem => ({
  id, title: `세션 ${id}`, goal: `목표 ${id}`, status: "completed", paused_reason: null, director: me.user, participants: [],
  completion_progress: { met: 2, total: 2 }, cost_usd: 1.2, budget_usd: 20, cost_estimated: false,
  attention: { hitl_open: 0, blocked: 0, failed: 0 }, running_lane_count: 0, runtime_id: "r1",
  last_activity_at: "2026-09-14T09:00:00Z", created_at: "2026-09-14T08:00:00Z", ...over,
});
const runtime = { id: "r1", workspace_id: "w1", name: "MacBook", host: null, status: "online", daemon_version: "0.4.0", last_seen_at: null, capabilities: [], repos: [], max_concurrent_tasks: null, running_task_count: 0, workdir_disk_bytes: 0, offline_since: null, grace_ends_at: null, paused_session_count: 0, created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" };

const ITEMS = [item("s1"), item("s2", { status: "active" }), item("s3", { director: other }), item("s4", { status: "paused", director: other })];

beforeEach(() => {
  get.mockReset();
  del.mockReset();
  replace.mockReset();
  canManage = false;
  searchParams = new URLSearchParams();
  streamHandler = null;
  get.mockImplementation((path: string) => {
    if (path.endsWith("/sessions")) return Promise.resolve({ items: ITEMS, next_cursor: null });
    if (path.endsWith("/runtimes")) return Promise.resolve([runtime]);
    return Promise.resolve({ items: [] });
  });
});
afterEach(cleanup);

async function mount() {
  render(<SessionsPage />);
  await waitFor(() => expect(screen.getAllByTestId("session-row")).toHaveLength(4));
}
const rowOf = (id: string) => screen.getAllByTestId("session-row").find((r) => r.getAttribute("data-session-id") === id)!;
function openMenu(id: string) {
  fireEvent.click(rowOf(id).querySelector('[data-testid="session-menu-button"]')!);
  return rowOf(id).querySelector('[data-testid="session-menu-delete"]')!;
}

describe("카드 구조 — a 안에 button 없음, 링크는 카드 전체", () => {
  it("카드는 article, 링크(제목·목표·배지)와 「…」 버튼이 형제다", async () => {
    await mount();
    const row = rowOf("s1");
    expect(row.tagName).toBe("ARTICLE");
    const link = row.querySelector('[data-testid="session-link"]')!;
    expect(link.getAttribute("href")).toBe("/sessions/s1");
    expect(link.textContent).toContain("세션 s1");
    expect(link.textContent).toContain("목표 s1");
    expect(link.querySelector("button")).toBeNull(); // a 안에 button 없음
    const btn = row.querySelector('[data-testid="session-menu-button"]')!;
    expect(btn.closest("a")).toBeNull(); // 버튼은 링크 밖
    expect(btn.getAttribute("aria-label")).toBe(SESSION_MENU.button);
  });
});

describe("활성/비활성 — 상태 × 권한", () => {
  it("멤버(Director): 내 끝난 세션만 활성, 내 진행 중 세션·남의 끝난 세션·남의 진행 중 세션은 비활성 + 사유", async () => {
    await mount();
    expect(openMenu("s1").getAttribute("aria-disabled")).toBeNull();
    const s2 = openMenu("s2");
    expect(s2.getAttribute("aria-disabled")).toBe("true");
    expect(rowOf("s2").textContent).toContain(SESSION_MENU.blocked_active);
    const s3 = openMenu("s3");
    expect(s3.getAttribute("aria-disabled")).toBe("true");
    expect(rowOf("s3").textContent).toContain(SESSION_MENU.blocked_role);
    const s4 = openMenu("s4");
    expect(s4.getAttribute("aria-disabled")).toBe("true");
    expect(rowOf("s4").textContent).toContain(SESSION_MENU.blocked_active);
  });

  it("owner·admin(canManage): 남의 끝난 세션도 활성, 진행 중은 여전히 비활성", async () => {
    canManage = true;
    await mount();
    expect(openMenu("s3").getAttribute("aria-disabled")).toBeNull();
    expect(openMenu("s4").getAttribute("aria-disabled")).toBe("true");
  });
});

describe("삭제 성공 경로 — 카드 즉시 제거, SSE 로도 제거(멱등)", () => {
  it("메뉴 「삭제」 → 다이얼로그(제목에 세션 이름) → 「삭제」 → 204 → 카드가 빠지고 안내 한 줄", async () => {
    del.mockResolvedValueOnce(undefined);
    await mount();
    fireEvent.click(openMenu("s1"));
    expect(screen.getByTestId("delete-session-title").textContent).toBe("「세션 s1」 세션을 삭제할까요?");
    fireEvent.click(screen.getByTestId("delete-session-confirm"));
    await waitFor(() => expect(screen.getAllByTestId("session-row")).toHaveLength(3));
    expect(del).toHaveBeenCalledWith("/sessions/{sessionId}", { path: { sessionId: "s1" } });
    expect(screen.queryByTestId("delete-session-dialog")).toBeNull();
    expect(screen.getByTestId("session-deleted-notice").textContent).toBe("「세션 s1」 세션을 삭제했습니다.");
    // 같은 세션의 SSE session.deleted 가 뒤따라와도 그대로(멱등) — 다시 불러오지 않는다.
    const calls = get.mock.calls.length;
    streamHandler!({ id: "9", type: "session.deleted", at: "2026-09-14T09:01:00Z", workspace_id: "w1", session_id: "s1", payload: { session_id: "s1" } });
    await waitFor(() => expect(screen.getAllByTestId("session-row")).toHaveLength(3));
    expect(get.mock.calls.length).toBe(calls);
  });

  it("SSE session.deleted {session_id} 만으로도 카드가 빠진다(다른 사람이 지운 경우)", async () => {
    await mount();
    streamHandler!({ id: "9", type: "session.deleted", at: "2026-09-14T09:01:00Z", workspace_id: "w1", session_id: null, payload: { session_id: "s3" } });
    await waitFor(() => expect(screen.getAllByTestId("session-row")).toHaveLength(3));
    expect(screen.getAllByTestId("session-row").map((r) => r.getAttribute("data-session-id"))).toEqual(["s1", "s2", "s4"]);
  });

  it("409 이면 카드는 그대로, 다이얼로그 안에 서버 문장", async () => {
    del.mockRejectedValueOnce(problemFixture("session_active", 409, { detail: "진행 중인 세션은 먼저 종료하세요" }));
    await mount();
    fireEvent.click(openMenu("s1"));
    fireEvent.click(screen.getByTestId("delete-session-confirm"));
    await waitFor(() => expect(screen.getByTestId("delete-session-error").textContent).toBe("진행 중인 세션은 먼저 종료하세요"));
    expect(screen.getAllByTestId("session-row")).toHaveLength(4);
  });
});

describe("S7 에서 돌아온 사람 — ?deleted=<제목>", () => {
  it("안내 한 줄을 보이고 주소를 /sessions 로 정리한다", async () => {
    searchParams = new URLSearchParams({ deleted: "결제 시장 조사" });
    await mount();
    expect(screen.getByTestId("session-deleted-notice").textContent).toBe("「결제 시장 조사」 세션이 삭제되어 목록으로 돌아왔습니다.");
    expect(replace).toHaveBeenCalledWith("/sessions");
  });
});
