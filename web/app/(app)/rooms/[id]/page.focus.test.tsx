/**
 * T-FOCUS(PRD FR-3.1.5 · SCREEN v0.19.13 · COMPONENTS §9.10 「지금」 줄) — 방 화면의 두 자리.
 *  1. 「작업 중」 말풍선 **첫 줄**(머리 아래 · 진행 메모 위): 「지금 〈문장〉 · n분 전」, aria-live=polite, 대신 문장(derived)은 흐리게.
 *  2. 서브 미션 카드의 상태 문구 자리: 같은 문장(두 줄 말줄임), 「취소는 즉시 가능」은 「중단」 버튼의 title 로.
 *  - lane.updated 로 문장이 바뀌면 그 자리에서 바뀐다(150ms 페이드 — key 로 다시 그림) · focus 가 null 이면 줄이 없다.
 *
 * 회귀 주입: WorkingBubble 에서 FocusLine 을 빼면 (bubble) FAIL; FocusLine 의 aria-live 를 빼면 (live) FAIL; derived 분기를 지우면
 * (derived) FAIL; LaneCard 의 focus 분기를 지우면 (lane) FAIL; 중단 버튼 title 을 빼면 (cancel title) FAIL; 말풍선 안 순서를 바꾸면
 * (order) FAIL; FocusLine 의 key 를 지우면 (fade) FAIL.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import type { Lane, Me, Room, StreamEvent, Task } from "@/lib/api/types";

vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "r1" }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
  usePathname: () => "/rooms/r1",
}));
const get = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  const post = (...a: unknown[]) => (a[0] === "/rooms/{roomId}/read" ? Promise.resolve({ room_id: "r1", unread_count: 0 }) : Promise.resolve({}));
  return { ...actual, api: { ...actual.api, get: (...a: unknown[]) => get(...a), post } };
});
const me: Me = {
  user: { id: "u1", email: "seoyeon@x", display_name: "서연", avatar_url: null, created_at: "" },
  workspaces: [{ id: "ws1", name: "Colab", slug: "colab", my_role: "member", created_at: "", updated_at: "" }],
  pending_invites: [],
};
vi.mock("@/lib/auth/AuthContext", () => ({
  useAuth: () => ({ me, workspace: me.workspaces[0], loading: false, canManage: false, selectWorkspace: vi.fn(), refresh: vi.fn(), logout: vi.fn() }),
}));
let stream: ((ev: StreamEvent) => void) | null = null;
vi.mock("@/lib/realtime/StreamContext", () => ({
  useWorkspaceStream: (_ws: string, h: (ev: StreamEvent) => void) => {
    stream = h;
    return "open";
  },
}));

import RoomPage from "./page";

const room: Room = {
  id: "r1", workspace_id: "ws1", name: "마리오 카트", description: "", status: "active", visibility: "workspace",
  owner_user_id: "u1", deputy_owner_user_id: null, runtime_id: null, isolation: { kind: "none", remote_url: null },
  limits: { budget_usd: 50, time_limit: null, max_concurrent_works: 3, max_parallel_lanes: 5 }, autonomy: "guided", default_director_user_id: null,
  blocked_reason: null, blocked_detail: null, counts: { works_active: 0, lanes_active: 2, tasks_active: 2 }, cost_usd: 0, cost_estimated: false,
  unread_count: 0, my_room_role: "owner", my_capabilities: ["post"], created_by: "u1", created_at: "", updated_at: "", last_activity_at: null,
};
const NOW = Date.now();
const ago = (min: number) => new Date(NOW - min * 60_000).toISOString();
const AGENT_SENTENCE = "코너에서 차가 미끄러지는 원인을 찾고 있습니다 — 타이어 접지 한계를 점검하는 중입니다";
const DERIVED_SENTENCE = "@Lead 의 「BGM v2 를 16분음표 격자로」 요청을 처리하고 있습니다";

let LANES: Lane[] = [];
const TASKS: Record<string, Task> = {
  t1: { id: "t1", status: "running", attempt: 1, started_at: ago(12) } as Task,
  t2: { id: "t2", status: "running", attempt: 1, started_at: ago(4) } as Task,
};
function routes(path: string, opts?: { path?: Record<string, string>; query?: Record<string, unknown> }) {
  if (path === "/rooms/{roomId}") return Promise.resolve(room);
  if (path === "/rooms/{roomId}/works") return Promise.resolve({ items: [], next_cursor: null });
  if (path === "/rooms/{roomId}/participants") return Promise.resolve({ items: [
    { id: "p-u1", room_id: "r1", kind: "user", user: { id: "u1", email: "", display_name: "서연", avatar_url: null, created_at: "" }, room_role: "owner", joined_at: "", left_at: null },
    { id: "p-a1", room_id: "r1", kind: "agent", agent: { id: "a1", name: "Lead", role: "lead" }, status: "working", room_role: "member", joined_at: "", left_at: null },
    { id: "p-a2", room_id: "r1", kind: "agent", agent: { id: "a2", name: "Developer", role: "developer" }, status: "working", room_role: "member", joined_at: "", left_at: null },
  ] });
  if (path === "/workspaces/{wsId}/agents" || path.endsWith("/agents")) return Promise.resolve({ items: [{ id: "a1", name: "Lead", role: "lead" }, { id: "a2", name: "Developer", role: "developer" }] });
  if (path === "/rooms/{roomId}/messages") return Promise.resolve({ items: [], has_more_before: false, has_more_after: false });
  if (path === "/tasks/{taskId}/events") return Promise.resolve({ items: [], has_more: false, structured: true });
  if (path === "/tasks/{taskId}") return Promise.resolve(TASKS[opts!.path!.taskId]);
  if (path === "/rooms/{roomId}/lanes") return Promise.resolve(LANES);
  if (path.endsWith("/decisions") || path === "/rooms/{roomId}/artifacts") return Promise.resolve([]);
  if (path.endsWith("/runtimes")) return Promise.resolve([{ id: "rt1", status: "online", name: "MacBook" }]);
  return Promise.resolve({ items: [] });
}
const lane = (id: string, agent: string, name: string, task: Task, focus: Lane["focus"]): Lane => ({
  id, session_id: "r1", agent_id: agent, agent_name: name, status: "running", current_task: task, updated_at: ago(1), created_at: ago(15),
  depends_on: [], actions: ["restart", "cancel"], work_id: null, focus,
} as unknown as Lane);

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  try { window.localStorage.clear(); } catch { /* */ }
  LANES = [
    lane("l1", "a1", "Lead", TASKS.t1, { text: AGENT_SENTENCE, at: ago(3), source: "agent" }),
    lane("l2", "a2", "Developer", TASKS.t2, { text: DERIVED_SENTENCE, at: ago(0), source: "derived" }),
  ];
  stream = null;
  get.mockReset().mockImplementation(routes);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

let seq = 0;
const send = (type: string, payload: unknown) => act(() => stream!({ id: String(++seq), type, at: "", room_id: "r1", payload: payload as Record<string, unknown> } as StreamEvent));
const bubbleOf = (agent: string) => document.querySelector(`[data-testid="working-bubble"][data-agent-id="${agent}"]`) as HTMLElement;
const cardOf = (laneId: string) => document.querySelector(`[data-testid="lane-card"][data-lane-id="${laneId}"]`) as HTMLElement;
async function ready() {
  render(<RoomPage />);
  await screen.findByTestId("room-title");
  await waitFor(() => expect(screen.queryAllByTestId("working-bubble")).toHaveLength(2));
}

describe("「작업 중」 말풍선의 「지금」 줄", () => {
  it("(bubble) 첫 줄 — 「지금 〈문장〉 · n분 전」, 진행 메모보다 위 (order)", async () => {
    await ready();
    send("message.delta", { session_id: "r1", task_id: "t1", agent_id: "a1", text: "하네스를 다시 돌립니다." });
    const b = bubbleOf("a1");
    const line = within(b).getByTestId("working-focus");
    expect(line.textContent).toBe(`지금 ${AGENT_SENTENCE} · 3분 전`);
    const body = within(b).getByTestId("working-bubble-body");
    expect(body.firstElementChild).toBe(line);
    const memo = within(b).getByTestId("working-memo-line");
    expect(line.compareDocumentPosition(memo) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("(live) aria-live=polite 는 이 줄에도 — 머리와 둘", async () => {
    await ready();
    expect(within(bubbleOf("a1")).getByTestId("working-focus")).toHaveAttribute("aria-live", "polite");
    expect(within(bubbleOf("a1")).getByTestId("working-head")).toHaveAttribute("aria-live", "polite");
    // 진행 메모 줄은 여전히 밖.
    send("message.delta", { session_id: "r1", task_id: "t1", agent_id: "a1", text: "하나." });
    expect(within(bubbleOf("a1")).getByTestId("working-memo-line").closest("[aria-live]")).toBeNull();
  });

  it("(derived) 대신 문장은 흐리게 — 에이전트가 말한 문장과 구분된다", async () => {
    await ready();
    const derived = within(bubbleOf("a2")).getByTestId("working-focus");
    expect(derived).toHaveAttribute("data-source", "derived");
    expect(within(derived).getByTestId("working-focus-text")).toHaveClass("focus__text--derived");
    expect(derived.getAttribute("title")).toContain("받은 요청으로 만든 문장");
    const agent = within(bubbleOf("a1")).getByTestId("working-focus");
    expect(within(agent).getByTestId("working-focus-text")).not.toHaveClass("focus__text--derived");
    expect(agent).not.toHaveAttribute("title");
  });

  it("(fade) lane.updated 로 문장이 바뀌면 그 자리에서 — 새 노드로 다시 그려 페이드 · null 이면 줄이 없다", async () => {
    await ready();
    const before = within(bubbleOf("a2")).getByTestId("working-focus-text").parentElement;
    send("lane.updated", { ...LANES[1], focus: { text: "BGM 격자를 16분음표로 맞추고 있습니다", at: new Date().toISOString(), source: "agent" } });
    const text = within(bubbleOf("a2")).getByTestId("working-focus-text");
    expect(text).toHaveTextContent("BGM 격자를 16분음표로 맞추고 있습니다");
    expect(text.parentElement).not.toBe(before);
    expect(text.parentElement).toHaveClass("focus__fade");
    send("lane.updated", { ...LANES[1], focus: null });
    expect(within(bubbleOf("a2")).queryByTestId("working-focus")).toBeNull();
  });
});

describe("서브 미션 카드의 「지금」 줄", () => {
  it("(lane) 상태 문구 자리에 같은 문장 · 옛 「실행 중 — 취소는 즉시 가능」 없음 · (cancel title) 중단 버튼 툴팁", async () => {
    await ready();
    const card = cardOf("l1");
    const line = within(card).getByTestId("lane-focus");
    expect(within(line).getByTestId("lane-focus-text")).toHaveTextContent(AGENT_SENTENCE);
    expect(line).not.toHaveAttribute("aria-live");
    expect(card.textContent).not.toContain("실행 중 — 취소는 즉시 가능");
    expect(within(card).queryByTestId("lane-note")).toBeNull();
    expect(within(card).getByTestId("lane-action-cancel")).toHaveAttribute("title", "취소는 즉시 가능");
    expect(within(cardOf("l2")).getByTestId("lane-focus")).toHaveAttribute("data-source", "derived");
  });
});
