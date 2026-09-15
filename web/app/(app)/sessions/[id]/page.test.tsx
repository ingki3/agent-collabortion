/**
 * S7 세션 상세 — `session.deleted` 를 받으면 목록으로(T-W13, 계약 SSE 표 "그 세션을 보고 있던 S7 은 목록으로 돌아간다").
 * 제목을 `?deleted=` 에 실어 S5 가 안내 한 줄을 그린다. 다른 세션의 삭제는 무시한다.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import type { Me, Session, StreamEvent } from "@/lib/api/types";

const replace = vi.fn();
vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "s1" }),
  useRouter: () => ({ push: vi.fn(), replace }),
  useSearchParams: () => new URLSearchParams(),
}));

const get = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { ...actual.api, get: (...a: unknown[]) => get(...a) } };
});

const me: Me = {
  user: { id: "u1", email: "dir@example.com", display_name: "Director", avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
  workspaces: [{ id: "w1", name: "Colab", slug: "colab", my_role: "owner", created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" }],
  pending_invites: [],
};
vi.mock("@/lib/auth/AuthContext", () => ({
  useAuth: () => ({ me, workspace: me.workspaces[0], loading: false, canManage: true, selectWorkspace: vi.fn(), refresh: vi.fn(), logout: vi.fn() }),
}));

let streamHandler: ((ev: StreamEvent) => void) | null = null;
vi.mock("@/lib/realtime/StreamContext", () => ({
  useWorkspaceStream: (_ws: string, handler: (ev: StreamEvent) => void) => {
    streamHandler = handler;
    return "open";
  },
}));

import SessionPage from "./page";

const session: Session = {
  id: "s1", workspace_id: "w1", title: "결제 시장 조사", goal: "보고서", acceptance_criteria: [], director_user_id: "u1", director: me.user,
  deputy_director_user_id: null, assignee_agent_id: "a1", runtime_id: null, isolation: { kind: "none", remote_url: null },
  completion_condition: { op: "and", conditions: [] },
  completion_progress: { met: 0, total: 0, satisfied: false, human_gate: false, conditions: [] },
  limits: { budget_usd: 20, budget_tokens: null, time_limit: "PT4H", max_tasks: null, max_parallel_lanes: 5 }, autonomy: "guided",
  status: "completed", paused_reason: null, cost_usd: 0, cost_estimated: false, participants: [], context: [], my_role: "director",
  created_by: "u1", created_at: "2026-09-14T08:00:00Z", updated_at: "2026-09-14T08:00:00Z", started_at: null, finished_at: null, last_activity_at: null,
};

beforeEach(() => {
  get.mockReset();
  replace.mockReset();
  streamHandler = null;
  get.mockImplementation((path: string) => {
    if (path === "/sessions/{sessionId}") return Promise.resolve(session);
    if (path.endsWith("/messages")) return Promise.resolve({ items: [], next_cursor: null });
    if (path.endsWith("/runtimes")) return Promise.resolve([]);
    if (path.endsWith("/lanes") || path.endsWith("/artifacts") || path.endsWith("/decisions")) return Promise.resolve([]);
    return Promise.resolve({ items: [] });
  });
});
afterEach(cleanup);

const ev = (sessionId: string): StreamEvent => ({ id: "9", type: "session.deleted", at: "2026-09-14T09:01:00Z", workspace_id: "w1", session_id: sessionId, payload: { session_id: sessionId } });

describe("S7 — session.deleted", () => {
  it("내 세션이 지워지면 /sessions?deleted=<제목> 으로 돌아간다", async () => {
    render(<SessionPage />);
    await waitFor(() => expect(screen.getByTestId("session-title").textContent).toBe("결제 시장 조사"));
    streamHandler!(ev("s1"));
    expect(replace).toHaveBeenCalledWith(`/sessions?deleted=${encodeURIComponent("결제 시장 조사")}`);
  });

  it("다른 세션의 삭제는 무시한다", async () => {
    render(<SessionPage />);
    await waitFor(() => expect(streamHandler).not.toBeNull());
    streamHandler!(ev("s2"));
    expect(replace).not.toHaveBeenCalled();
  });
});

// W-16(2026-09-15, Director): 「작성 중…」 미리보기가 턴이 끝난 뒤에도 남고, heartbeat 마다 같은 글이 겹쳐 쌓였다.
// preview.text 는 **지금까지의 부분 출력 전체**(daemon-protocol §4.2) — 바꿔 끼우고, lane 이 running 을 벗어나면 지운다.
describe("S7 — message.delta 미리보기(작성 중…)", () => {
  beforeEach(() => { Element.prototype.scrollIntoView = vi.fn(); }); // jsdom 에 없다 — 화면은 델타마다 맨 아래로 스크롤한다
  const delta = (text: string): StreamEvent => ({ id: "10", type: "message.delta", at: "2026-09-14T09:01:00Z", workspace_id: "w1", session_id: "s1", payload: { session_id: "s1", task_id: "t1", agent_id: "a1", text } });
  const laneEv = (status: string): StreamEvent => ({ id: "11", type: "lane.updated", at: "2026-09-14T09:01:05Z", workspace_id: "w1", session_id: "s1", payload: { id: "l1", session_id: "s1", agent_id: "a1", status, actions: [], depends_on: [], reentry_count: 0 } as never });

  it("스냅숏은 이어 붙이지 않고 바꿔 끼운다", async () => {
    render(<SessionPage />);
    await waitFor(() => expect(streamHandler).not.toBeNull());
    act(() => { streamHandler!(delta("Posted the")); streamHandler!(delta("Posted the wrap-up.")); });
    await waitFor(() => expect(screen.getByTestId("message-delta").textContent).toContain("Posted the wrap-up."));
    expect(screen.getByTestId("message-delta").textContent).not.toContain("Posted thePosted");
    expect(screen.getByTestId("message-delta").textContent).toContain("작성 중…");
  });

  it("턴이 끝나면(lane 이 running 을 벗어나면) 미리보기가 사라진다", async () => {
    render(<SessionPage />);
    await waitFor(() => expect(streamHandler).not.toBeNull());
    act(() => { streamHandler!(delta("마무리 중")); });
    await waitFor(() => expect(screen.getByTestId("message-delta")).toBeTruthy());
    act(() => { streamHandler!(laneEv("done")); });
    await waitFor(() => expect(screen.queryByTestId("message-delta")).toBeNull());
  });

  it("아직 running 이면 남는다", async () => {
    render(<SessionPage />);
    await waitFor(() => expect(streamHandler).not.toBeNull());
    act(() => { streamHandler!(delta("쓰는 중")); streamHandler!(laneEv("running")); });
    await waitFor(() => expect(screen.getByTestId("message-delta")).toBeTruthy());
  });
});
