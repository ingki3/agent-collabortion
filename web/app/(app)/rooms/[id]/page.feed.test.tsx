/**
 * T-FEED A·B(Director 요청 2026-09-25) — 방 화면에서: 한 턴이 메시지 둘을 올리고 뒤에도 일한다.
 *  - 메시지마다의 「작업 과정」은 그 메시지까지의 조각(겹치지 않음) — 요약 줄도.
 *  - 턴이 돌면 꼬리는 타임라인 맨 아래 「@Lead 작업 중」 줄, 끝나면(task.updated · turn_end) 줄이 사라지고 마지막 메시지로.
 *  - 끝난 task 의 짝 없는 runtime/start 에 「진행 중…」이 없다(T-FEED 1).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { Lane, Me, Message, Room, StreamEvent, Task, TaskEvent } from "@/lib/api/types";

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
  blocked_reason: null, blocked_detail: null, counts: { works_active: 0, lanes_active: 1, tasks_active: 1 }, cost_usd: 0, cost_estimated: false,
  unread_count: 0, my_room_role: "owner", my_capabilities: ["post"], created_by: "u1", created_at: "", updated_at: "", last_activity_at: null,
};
const T0 = Date.parse("2026-09-25T13:14:00Z");
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();
let seq = 0;
const ev = (min: number, over: Partial<TaskEvent> = {}): TaskEvent => ({
  id: `t1-e${++seq}`, task_id: "t1", attempt: 1, seq, class: "tool", verb: "run_shell", object_ref: null, outcome: "ok", payload: { command: "go test" },
  tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: null, created_at: at(min), ...over,
} as TaskEvent);
const post = (min: number, msgId: string) => ev(min, { class: "status", verb: "post_message", object_ref: msgId, seq: 2 ** 30 + seq, payload: { command: "message post", result_ref: msgId } });
const msg = (id: string, min: number, content: string): Message => ({
  id, session_id: "r1", author_type: "agent", author_id: "a1", author: { name: "Lead" }, parent_id: null, content, mentions: [],
  source_task_id: "t1", kind: "text", state: "posted", created_at: at(min + 0.01), work_id: null,
} as Message);

let EVENTS: TaskEvent[] = [];
let TASK: Task;
let LANE: Lane;
const MSGS = () => [msg("mA", 3, "@Researcher 조사를 맡깁니다."), msg("mB", 7, "빌드 확인 중입니다.")];

function routes(path: string, opts?: { path?: Record<string, string>; query?: Record<string, unknown> }) {
  if (path === "/rooms/{roomId}") return Promise.resolve(room);
  if (path === "/rooms/{roomId}/works") return Promise.resolve({ items: [], next_cursor: null });
  if (path === "/rooms/{roomId}/participants") return Promise.resolve({ items: [
    { id: "p-u1", room_id: "r1", kind: "user", user: { id: "u1", email: "", display_name: "서연", avatar_url: null, created_at: "" }, room_role: "owner", joined_at: "", left_at: null },
    { id: "p-a1", room_id: "r1", kind: "agent", agent: { id: "a1", name: "Lead", role: "lead" }, status: "working", room_role: "member", joined_at: "", left_at: null },
  ] });
  if (path === "/workspaces/{wsId}/agents" || path.endsWith("/agents")) return Promise.resolve({ items: [{ id: "a1", name: "Lead", role: "lead" }] });
  if (path === "/rooms/{roomId}/messages") return Promise.resolve({ items: opts?.query?.thread ? [] : MSGS(), has_more_before: false, has_more_after: false });
  if (path === "/tasks/{taskId}/events") return Promise.resolve({ items: EVENTS, has_more: false, structured: true });
  if (path === "/tasks/{taskId}") return Promise.resolve(TASK);
  if (path === "/rooms/{roomId}/lanes") return Promise.resolve([LANE]);
  if (path.endsWith("/decisions") || path === "/rooms/{roomId}/artifacts") return Promise.resolve([]);
  if (path.endsWith("/runtimes")) return Promise.resolve([{ id: "rt1", status: "online", name: "MacBook" }]);
  return Promise.resolve({ items: [] });
}

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  try { window.localStorage.clear(); } catch { /* */ }
  seq = 0;
  // 시작 · 셸 2 · 메시지 A · 셸 3 · 메시지 B · 셸 4(꼬리, 아직 도는 중)
  EVENTS = [
    ev(0, { class: "runtime", verb: "start", outcome: "started", payload: null }),
    ev(1), ev(2), post(3, "mA"), ev(4), ev(5), ev(6), post(7, "mB"), ev(8), ev(9), ev(10), ev(11),
  ];
  TASK = { id: "t1", status: "running", attempt: 1 } as Task;
  LANE = { id: "l1", session_id: "r1", agent_id: "a1", agent_name: "Lead", status: "running", current_task: TASK, updated_at: at(11), depends_on: [], actions: [], work_id: null } as unknown as Lane;
  stream = null;
  get.mockReset().mockImplementation(routes);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const card = (id: string) => document.querySelector(`[data-message-id="${id}"]`) as HTMLElement;
const processText = (id: string) => within(card(id)).getByTestId("process-fold").textContent ?? "";
async function ready() {
  render(<RoomPage />);
  await screen.findByTestId("room-title");
  await waitFor(() => expect(screen.getAllByTestId("message-card").length).toBe(2));
  await waitFor(() => expect(processText("mA")).toContain("셸 명령"));
}

describe("메시지의 「작업 과정」 = 그 메시지까지의 조각", () => {
  it("도는 턴 — A 는 셸 2회, B 는 셸 3회(꼬리 4회는 「@Lead 작업 중」 줄), 펼친 피드도 조각만", async () => {
    await ready();
    expect(processText("mA")).toContain("셸 명령 2회");
    expect(processText("mB")).toContain("셸 명령 3회");
    const row = await screen.findByTestId("working-row");
    expect(row.textContent).toContain("@Lead 작업 중");
    expect(row.textContent).toContain("셸 명령 4회");
    // 타임라인 맨 아래 — 마지막 메시지 카드 뒤.
    expect(card("mB").compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(within(card("mA")).getByTestId("process-fold"));
    const feedA = within(card("mA")).getByTestId("activity-feed");
    expect(feedA.querySelectorAll("li.feed__row")).toHaveLength(1 + 2 + 1); // start · 셸 2 · 게시
    fireEvent.click(within(row).getByTestId("working-fold"));
    expect(within(row).getByTestId("working-body").querySelectorAll("li.feed__row")).toHaveLength(4);
    // 도는 턴 — runtime/start 는 「진행 중…」.
    expect(within(feedA).getAllByTestId("feed-pending")).toHaveLength(1);
  });

  it("턴이 끝나면(turn_end + task.updated completed) 「작업 중」 줄이 사라지고 꼬리는 마지막 메시지로 · 「진행 중…」 없음", async () => {
    await ready();
    await screen.findByTestId("working-row");
    const end = ev(12, { class: "runtime", verb: "turn_end", outcome: "ok", payload: null });
    act(() => stream!({ id: "1", type: "task_event.appended", at: "", room_id: "r1", payload: end as unknown as Record<string, unknown> } as StreamEvent));
    act(() => stream!({ id: "2", type: "task.updated", at: "", room_id: "r1", payload: { ...TASK, status: "completed" } as unknown as Record<string, unknown> } as StreamEvent));
    await waitFor(() => expect(screen.queryByTestId("working-row")).toBeNull());
    expect(processText("mA")).toContain("셸 명령 2회");
    expect(processText("mB")).toContain("셸 명령 7회");
    fireEvent.click(within(card("mA")).getByTestId("process-fold"));
    expect(within(card("mA")).queryAllByTestId("feed-pending")).toHaveLength(0);
  });

  it("처음부터 끝난 task(getTask completed) — 줄 없음, B 가 꼬리까지, 어떤 줄에도 「진행 중…」 없음", async () => {
    EVENTS.push(ev(12, { class: "runtime", verb: "turn_end", outcome: "ok", payload: null }));
    TASK = { ...TASK, status: "completed" };
    LANE = { ...LANE, status: "done", current_task: TASK };
    await ready();
    expect(screen.queryByTestId("working-row")).toBeNull();
    expect(processText("mB")).toContain("셸 명령 7회");
    fireEvent.click(within(card("mA")).getByTestId("process-fold"));
    expect(screen.queryAllByTestId("feed-pending")).toHaveLength(0);
  });
});
