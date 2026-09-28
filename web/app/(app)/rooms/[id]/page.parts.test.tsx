/**
 * T-PARTS(Director 요청 2026-09-27 · PRD FR-3.1.4 · SCREEN §4.6 v0.19.11 「부분 메시지」 · COMPONENTS §9.11) — 방 화면.
 *  - 같은 group_id 행 셋 = 말풍선 하나(작성자 머리 한 번) · 부분마다 Part Head(‹종류› → 받는 쪽) · 보고면 ↩.
 *    (부분 사이 1px 선은 CSS 라 jsdom 이 재지 못한다 — 스크린샷 `__screenshots__/parts/parts-01-light.png` 대조 몫이다. 리뷰 #374b NN1)
 *  - 답글은 부분마다(부분이 곧 행) · 작업 내용은 부분마다 · 작업 과정은 말풍선 맨 아래 하나(묶음 = 경계 하나).
 *  - 5분 묶음에서 빠진다 · 「나」 강조 · 접근성(article · section aria-label).
 *  - 실시간: message.created 가 부분마다 오면 같은 말풍선에 채운다 · 「작업 중」 말풍선은 첫 부분에서 바뀐다.
 *
 * 회귀 주입: page 의 timelineItems 를 messages.map 으로 되돌리면 (하나) FAIL; processBoundaries 를 빼면 (작업 과정 하나) FAIL(조각이 부분 수만큼);
 * PartBubble 의 Part 답글 버튼을 말풍선 하나로 모으면 (답글 부분마다) FAIL; groupsWith 의 group_id 가드를 빼면 (5분 묶음) FAIL;
 * AddresseeChips 의 data-me 를 빼면 (나) FAIL; partLayers 의 noProcess 를 빼면 (작업 과정 하나) FAIL.
 */
import "@testing-library/jest-dom/vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
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
  blocked_reason: null, blocked_detail: null, counts: { works_active: 0, lanes_active: 2, tasks_active: 2 }, cost_usd: 0, cost_estimated: false,
  unread_count: 0, my_room_role: "owner", my_capabilities: ["post"], created_by: "u1", created_at: "", updated_at: "", last_activity_at: null,
};
const T0 = Date.parse("2026-09-26T13:00:00Z");
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();
let seq = 0;
const ev = (task: string, min: number, over: Partial<TaskEvent> = {}): TaskEvent => ({
  id: `${task}-e${++seq}`, task_id: task, attempt: 1, seq, class: "tool", verb: "run_shell", object_ref: null, outcome: "ok", payload: { command: "node test/headless.js" },
  tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: null, created_at: at(min), ...over,
} as TaskEvent);
const lead0: Message = {
  id: "m0", session_id: "r1", author_type: "agent", author_id: "a1", author: { name: "Lead" }, parent_id: null, content: "밸런스 하네스를 보겠습니다.", mentions: [],
  source_task_id: "t1", kind: "text", state: "posted", created_at: at(0.5), work_id: null,
} as Message;

let EVENTS: Record<string, TaskEvent[]> = {};
let TASKS: Record<string, Task> = {};
let LANES: Lane[] = [];
let MSGS: Message[] = [];
let REPLIES: Message[] = [];

function routes(path: string, opts?: { path?: Record<string, string>; query?: Record<string, unknown> }) {
  if (path === "/rooms/{roomId}") return Promise.resolve(room);
  if (path === "/rooms/{roomId}/works") return Promise.resolve({ items: [], next_cursor: null });
  if (path === "/rooms/{roomId}/participants") return Promise.resolve({ items: [
    { id: "p-u1", room_id: "r1", kind: "user", user: { id: "u1", email: "", display_name: "서연", avatar_url: null, created_at: "" }, room_role: "owner", joined_at: "", left_at: null },
    { id: "p-a1", room_id: "r1", kind: "agent", agent: { id: "a1", name: "Lead", role: "lead" }, status: "working", room_role: "member", joined_at: "", left_at: null },
    { id: "p-a2", room_id: "r1", kind: "agent", agent: { id: "a2", name: "Developer", role: "developer" }, status: "working", room_role: "member", joined_at: "", left_at: null },
    { id: "p-a3", room_id: "r1", kind: "agent", agent: { id: "a3", name: "Designer", role: "custom" }, status: "idle", room_role: "member", joined_at: "", left_at: null },
  ] });
  if (path === "/workspaces/{wsId}/agents" || path.endsWith("/agents")) return Promise.resolve({ items: [{ id: "a1", name: "Lead", role: "lead" }, { id: "a2", name: "Developer", role: "developer" }] });
  if (path === "/rooms/{roomId}/messages") {
    const th = opts?.query?.thread as string | undefined;
    return Promise.resolve({ items: th ? [...MSGS, ...REPLIES].filter((m) => m.id === th || m.parent_id === th) : MSGS, has_more_before: false, has_more_after: false, total: th ? 1 : null });
  }
  if (path === "/tasks/{taskId}/events") return Promise.resolve({ items: EVENTS[opts!.path!.taskId] ?? [], has_more: false, structured: true });
  if (path === "/tasks/{taskId}") return Promise.resolve(TASKS[opts!.path!.taskId]);
  if (path === "/rooms/{roomId}/lanes") return Promise.resolve(LANES);
  if (path.endsWith("/decisions") || path === "/rooms/{roomId}/artifacts") return Promise.resolve([]);
  if (path.endsWith("/runtimes")) return Promise.resolve([{ id: "rt1", status: "online", name: "MacBook" }]);
  return Promise.resolve({ items: [] });
}


const DIR = { kind: "user" as const, id: "u1", name: "서연" };
const part = (i: number, over: Partial<Message>): Message => ({
  id: `p${i}`, session_id: "r1", author_type: "agent", author_id: "a1", author: { name: "Lead" }, parent_id: null, mentions: [],
  source_task_id: "t1", kind: "text", state: "posted", created_at: at(10 + i * 0.01), work_id: null, reply_count: 0,
  group_id: "g1", group_index: i, group_size: 3, ...over,
} as Message);
const order: Message = {
  id: "o1", session_id: "r1", author_type: "user", author_id: "u1", author: { name: "서연" }, parent_id: null, content: "@Lead 커브가 어색해", mentions: [],
  source_task_id: null, kind: "text", state: "posted", created_at: at(0), work_id: null, speech: "instruct", addressees: [{ kind: "agent", id: "a1", name: "Lead" }],
} as Message;
const P0 = part(0, { content: "v9 올렸습니다. 커브 원인은 횡가속도 상한이었습니다.", detail: "## 원인\n\n| a | b |\n|---|---|\n| 1 | 2 |", speech: "report", addressees: [DIR], responds_to_message_id: "o1", reply_count: 1 });
const P1 = part(1, { content: "스프라이트 24방향으로 맞춰 주세요", speech: "request", addressees: [{ kind: "agent", id: "a3", name: "Designer" }] });
const P2 = part(2, { content: "FX 합쳤습니다 — [@Designer](mention://agent/a3) 시안 기준", speech: "request", addressees: [{ kind: "agent", id: "a2", name: "Developer" }] });
const R0: Message = { ...P0, id: "r0", parent_id: "p0", content: "고마워요", author_type: "user", author_id: "u1", author: { name: "서연" }, group_id: null, group_index: null, group_size: null, speech: "chat", addressees: [], detail: null } as Message;

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  try { window.localStorage.clear(); } catch { /* */ }
  seq = 0;
  // Lead(t1): 셸 35 → p0·p1·p2 게시(같은 턴).
  EVENTS = {
    t1: [
      ev("t1", 0, { class: "runtime", verb: "start", outcome: "started", payload: null }),
      ...Array.from({ length: 35 }, (_, i) => ev("t1", 1 + i * 0.2)),
      ...[0, 1, 2].map((i) => ev("t1", 10 + i * 0.01, { class: "status", verb: "post_message", object_ref: `p${i}`, seq: 2 ** 30 + i, payload: { command: "message post", result_ref: `p${i}` } })),
      ev("t1", 10.1, { class: "runtime", verb: "turn_end", outcome: "ok", payload: null }),
    ],
  };
  TASKS = { t1: { id: "t1", status: "completed", attempt: 1, started_at: at(0) } as Task };
  LANES = [{ id: "l1", session_id: "r1", agent_id: "a1", agent_name: "Lead", status: "done", current_task: TASKS.t1, updated_at: at(11), depends_on: [], actions: [], work_id: null } as unknown as Lane];
  MSGS = [order, P0, P1, P2];
  REPLIES = [R0];
  stream = null;
  get.mockReset().mockImplementation(routes);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const send = (type: string, payload: unknown) => act(() => stream!({ id: String(++seq), type, at: "", room_id: "r1", payload: payload as Record<string, unknown> } as StreamEvent));
const bubblesP = () => screen.queryAllByTestId("part-bubble");
async function ready(n = 1) {
  render(<RoomPage />);
  await screen.findByTestId("room-title");
  await waitFor(() => expect(bubblesP()).toHaveLength(n));
}

describe("부분 메시지 — 한 말풍선, 받는 쪽마다 한 부분", () => {
  it("(하나) 같은 group_id 행 셋 = 말풍선 하나 · 작성자 머리 한 번 · 부분 셋이 group_index 순", async () => {
    await ready();
    const b = bubblesP()[0];
    expect(within(b).getAllByTestId("part-author")).toHaveLength(1);
    expect(within(b).getByTestId("part-author")).toHaveTextContent("Lead");
    // 작성자 머리에는 받는 쪽·종류가 없다(부분마다 다르다).
    expect(within(b).getByTestId("part-author").querySelector("[data-testid=speech-kind], .convo__to")).toBeNull();
    const parts = within(b).getAllByTestId("part");
    expect(parts.map((p) => p.dataset.messageId)).toEqual(["p0", "p1", "p2"]);
    // 부분을 따로 그린 옛 카드가 없다.
    expect(document.querySelectorAll('[data-testid="message-card"][data-message-id="p1"]')).toHaveLength(0);
  });

  it("부분 머리 — ‹보고› → 서연 + ↩ / ‹요청› → @Designer / ‹요청› → @Developer · 본문 속 @Designer 칩은 받는 쪽이 아니다", async () => {
    await ready();
    const [a, b, c] = within(bubblesP()[0]).getAllByTestId("part");
    expect(within(a).getByTestId("speech-kind")).toHaveTextContent("보고");
    expect(within(a).getByTestId("speech-to")).toHaveTextContent("서연");
    expect(within(a).getByTestId("report-of")).toHaveTextContent("서연");
    expect(within(b).getByTestId("speech-kind")).toHaveTextContent("요청");
    expect(within(b).getByTestId("speech-to")).toHaveTextContent("@Designer");
    expect(within(b).queryByTestId("report-of")).toBeNull();
    expect(within(c).getByTestId("speech-to").textContent).toBe("@Developer");
  });

  it("(작업 내용 부분마다 · 작업 과정 하나) 작업 내용은 그 부분에만 · 작업 과정 줄은 말풍선 맨 아래 하나(부분 셋 전부의 조각)", async () => {
    await ready();
    const b = bubblesP()[0];
    const [a, bb, c] = within(b).getAllByTestId("part");
    expect(within(a).getAllByTestId("detail-layer")).toHaveLength(1);
    expect(within(bb).queryByTestId("detail-layer")).toBeNull();
    expect(within(c).queryByTestId("detail-layer")).toBeNull();
    expect(within(b).getAllByTestId("process-layer")).toHaveLength(1);
    for (const p of [a, bb, c]) expect(within(p).queryByTestId("process-layer")).toBeNull();
    const proc = within(b).getByTestId("part-process");
    expect(proc.compareDocumentPosition(c) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
    // 한 턴의 조각 전부(셸 35) — 부분마다 쪼개졌으면 첫 부분 조각만 35 가 되고 마지막(경계)은 0 이다.
    await waitFor(() => expect(within(proc).getByTestId("process-fold").textContent).toContain("셸 명령 35회"));
  });

  it("(답글 부분마다) 답글 버튼·답글 수는 부분마다 · 누르면 그 부분 행의 스레드", async () => {
    await ready();
    const [a, b] = within(bubblesP()[0]).getAllByTestId("part");
    expect(within(a).getAllByTestId("reply-button")).toHaveLength(1);
    expect(within(b).getAllByTestId("reply-button")).toHaveLength(1);
    expect(within(a).getByTestId("thread-toggle")).toHaveTextContent("답글 1개 보기");
    expect(within(b).queryByTestId("thread-toggle")).toBeNull();
    fireEvent.click(within(a).getByTestId("thread-toggle"));
    await waitFor(() => expect(within(a).getByTestId("thread")).toHaveTextContent("고마워요"));
    fireEvent.click(within(b).getByTestId("reply-button"));
    await waitFor(() => expect(screen.getByTestId("composer-reply-target")).toHaveTextContent("Lead"));
  });

  it("(나) 보는 사람(서연)이 받는 쪽인 부분만 강조 · 칩 data-me", async () => {
    await ready();
    const [a, b] = within(bubblesP()[0]).getAllByTestId("part");
    expect(a).toHaveAttribute("data-me", "true");
    expect(a.querySelector('.convo__to-chip[data-me="true"]')).not.toBeNull();
    expect(b).not.toHaveAttribute("data-me");
  });

  it("(5분 묶음) 부분 메시지 앞뒤 메시지와 묶지 않는다 — 바로 뒤 같은 작성자·종류의 말도 머리를 그린다", async () => {
    // 받는 쪽·종류·작성자가 마지막 부분(P2)과 같고 1분 안 — 가드가 없으면 묶인다.
    MSGS = [order, P0, P1, P2, { ...P2, id: "m9", group_id: null, group_index: null, group_size: null, created_at: at(10.5), content: "하나 더" } as Message];
    await ready();
    const after = document.querySelector('[data-testid="message-card"][data-message-id="m9"]')!;
    expect(after).not.toHaveAttribute("data-grouped");
  });

  it("접근성 — article 「Lead · 부분 3개 · 시각」, 부분 section 「〈받는 쪽〉에게 〈종류〉」", async () => {
    await ready();
    const b = bubblesP()[0];
    expect(b.tagName).toBe("ARTICLE");
    expect(b.getAttribute("aria-label")).toMatch(/^Lead · 부분 3개 · /);
    const [a, bb] = within(b).getAllByTestId("part");
    expect(a.tagName).toBe("SECTION");
    expect(a.getAttribute("aria-label")).toBe("서연에게 보고");
    expect(bb.getAttribute("aria-label")).toBe("@Designer에게 요청");
  });
});

describe("실시간 — 부분이 도착하는 대로 같은 말풍선에 채운다", () => {
  it("첫 부분 → 말풍선 하나(1/3, 채우는 중) · 둘째·셋째 → 같은 말풍선에 · 「작업 중」 말풍선은 첫 부분에서 바뀐다", async () => {
    // 턴이 돌고 있고 아직 아무 부분도 없다.
    EVENTS.t1 = EVENTS.t1.filter((e) => e.class === "tool" || e.verb === "start");
    TASKS.t1 = { ...TASKS.t1, status: "running" } as Task;
    LANES = [{ ...LANES[0], status: "running", current_task: TASKS.t1 } as Lane];
    MSGS = [order];
    render(<RoomPage />);
    await screen.findByTestId("room-title");
    await waitFor(() => expect(screen.queryAllByTestId("working-bubble")).toHaveLength(1));
    expect(bubblesP()).toHaveLength(0);

    send("task_event.appended", EVENTS_POST(0));
    send("message.created", P0);
    await waitFor(() => expect(bubblesP()).toHaveLength(1));
    expect(bubblesP()[0]).toHaveAttribute("data-filling", "true");
    expect(within(bubblesP()[0]).getAllByTestId("part")).toHaveLength(1);
    await waitFor(() => expect(screen.queryAllByTestId("working-bubble")).toHaveLength(0));

    // 사이에 다른 메시지가 끼어도 같은 말풍선에 채운다.
    send("message.created", { ...order, id: "o2", created_at: at(10.005), content: "잠깐만요" });
    send("task_event.appended", EVENTS_POST(1));
    send("message.created", P1);
    send("task_event.appended", EVENTS_POST(2));
    send("message.created", P2);
    await waitFor(() => expect(within(bubblesP()[0]).getAllByTestId("part")).toHaveLength(3));
    expect(bubblesP()).toHaveLength(1);
    expect(bubblesP()[0]).not.toHaveAttribute("data-filling");
    expect(within(bubblesP()[0]).getAllByTestId("part").map((p) => p.dataset.messageId)).toEqual(["p0", "p1", "p2"]);
    expect(within(bubblesP()[0]).getAllByTestId("process-layer")).toHaveLength(1);
  });
});

function EVENTS_POST(i: number): TaskEvent {
  return ev("t1", 10 + i * 0.01, { class: "status", verb: "post_message", object_ref: `p${i}`, seq: 2 ** 30 + i, payload: { command: "message post", result_ref: `p${i}` } });
}
