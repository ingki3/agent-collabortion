/**
 * T-BUBBLE(Director 요청 2026-09-26 · SCREEN §4.6 v0.19.10 「작업 중」 말풍선 · COMPONENTS §9.10) — 방 화면에서 두 에이전트가 동시에 일한다.
 *  - 옛 「작성 중…」 블록과 옛 「작업 중」 한 줄이 없고, 에이전트 말풍선 자리(왼쪽)에 점선 말풍선 하나로 합쳐진다.
 *  - 에이전트마다 하나 · 순서는 턴 시작 시각 · 받는 쪽(→)·말의 종류 없음.
 *  - 진행 메모는 마지막 문장 한 줄 · 펼치면 조각마다 한 문단(빈 줄 · 옛 데몬은 도구 이벤트 사이) → 활동 피드.
 *  - 게시되면 그 자리에서 메시지로 · 턴이 끝나면 사라지고 꼬리는 마지막 메시지의 작업 과정으로.
 *  - role=status/aria-live 는 머리에만 · 「···」 aria-hidden · reduced-motion 이면 애니메이션 없음.
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

function routes(path: string, opts?: { path?: Record<string, string>; query?: Record<string, unknown> }) {
  if (path === "/rooms/{roomId}") return Promise.resolve(room);
  if (path === "/rooms/{roomId}/works") return Promise.resolve({ items: [], next_cursor: null });
  if (path === "/rooms/{roomId}/participants") return Promise.resolve({ items: [
    { id: "p-u1", room_id: "r1", kind: "user", user: { id: "u1", email: "", display_name: "서연", avatar_url: null, created_at: "" }, room_role: "owner", joined_at: "", left_at: null },
    { id: "p-a1", room_id: "r1", kind: "agent", agent: { id: "a1", name: "Lead", role: "lead" }, status: "working", room_role: "member", joined_at: "", left_at: null },
    { id: "p-a2", room_id: "r1", kind: "agent", agent: { id: "a2", name: "Developer", role: "developer" }, status: "working", room_role: "member", joined_at: "", left_at: null },
  ] });
  if (path === "/workspaces/{wsId}/agents" || path.endsWith("/agents")) return Promise.resolve({ items: [{ id: "a1", name: "Lead", role: "lead" }, { id: "a2", name: "Developer", role: "developer" }] });
  if (path === "/rooms/{roomId}/messages") return Promise.resolve({ items: opts?.query?.thread ? [] : MSGS, has_more_before: false, has_more_after: false });
  if (path === "/tasks/{taskId}/events") return Promise.resolve({ items: EVENTS[opts!.path!.taskId] ?? [], has_more: false, structured: true });
  if (path === "/tasks/{taskId}") return Promise.resolve(TASKS[opts!.path!.taskId]);
  if (path === "/rooms/{roomId}/lanes") return Promise.resolve(LANES);
  if (path.endsWith("/decisions") || path === "/rooms/{roomId}/artifacts") return Promise.resolve([]);
  if (path.endsWith("/runtimes")) return Promise.resolve([{ id: "rt1", status: "online", name: "MacBook" }]);
  return Promise.resolve({ items: [] });
}

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  try { window.localStorage.clear(); } catch { /* */ }
  seq = 0;
  // Lead(t1, 12분 전 시작): 메시지 m0 뒤 셸 36 · 읽기 5 · 실패 2. Developer(t2, 4분 전 시작): 편집 3 · 셸 6.
  EVENTS = {
    t1: [
      ev("t1", 0, { class: "runtime", verb: "start", outcome: "started", payload: null }),
      ev("t1", 0.5, { class: "status", verb: "post_message", object_ref: "m0", seq: 2 ** 30, payload: { command: "message post", result_ref: "m0" } }),
      ...Array.from({ length: 36 }, (_, i) => ev("t1", 1 + i * 0.3, { outcome: i === 3 || i === 9 ? "failed" : "ok" })),
      ...Array.from({ length: 5 }, (_, i) => ev("t1", 12 + i * 0.01, { verb: "read", object_ref: "src/balance.ts", payload: null })),
    ],
    t2: [
      ev("t2", 8, { class: "runtime", verb: "start", outcome: "started", payload: null }),
      ...["a.ts", "b.ts", "c.ts"].map((f, i) => ev("t2", 9 + i, { verb: "edit_file", payload: { path: f }, object_ref: f })),
      ...Array.from({ length: 6 }, (_, i) => ev("t2", 11 + i * 0.1)),
    ],
  };
  TASKS = { t1: { id: "t1", status: "running", attempt: 1, started_at: at(0) } as Task, t2: { id: "t2", status: "running", attempt: 1, started_at: at(8) } as Task };
  // lanes 순서를 일부러 거꾸로 — 말풍선 순서는 턴 시작 시각이다.
  LANES = [
    { id: "l2", session_id: "r1", agent_id: "a2", agent_name: "Developer", status: "running", current_task: TASKS.t2, updated_at: at(12), depends_on: [], actions: [], work_id: null } as unknown as Lane,
    { id: "l1", session_id: "r1", agent_id: "a1", agent_name: "Lead", status: "running", current_task: TASKS.t1, updated_at: at(11), depends_on: [], actions: [], work_id: null } as unknown as Lane,
  ];
  MSGS = [lead0];
  stream = null;
  get.mockReset().mockImplementation(routes);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const send = (type: string, payload: unknown) => act(() => stream!({ id: String(++seq), type, at: "", room_id: "r1", payload: payload as Record<string, unknown> } as StreamEvent));
const delta = (agent: string, task: string, text: string) => send("message.delta", { session_id: "r1", task_id: task, agent_id: agent, text });
const bubbles = () => screen.queryAllByTestId("working-bubble");
const bubbleOf = (agent: string) => document.querySelector(`[data-testid="working-bubble"][data-agent-id="${agent}"]`) as HTMLElement;
async function ready() {
  render(<RoomPage />);
  await screen.findByTestId("room-title");
  await waitFor(() => expect(bubbles()).toHaveLength(2));
  await waitFor(() => expect(within(bubbleOf("a1")).getByTestId("working-summary").textContent).toContain("셸 명령"));
}

describe("「작업 중」 말풍선 — 하나로 합쳐진다", () => {
  it("옛 「작성 중…」 블록 · 옛 「작업 중」 한 줄 0 — 델타가 흘러도 말풍선 안에만", async () => {
    await ready();
    delta("a1", "t1", "원인을 찾았습니다.");
    expect(screen.queryByTestId("message-delta")).toBeNull();
    expect(screen.queryByTestId("working-row")).toBeNull();
    expect(document.body.textContent).not.toContain("작성 중…");
    expect(bubbles()).toHaveLength(2);
    expect(within(bubbleOf("a1")).getByTestId("working-memo-line")).toHaveTextContent("원인을 찾았습니다.");
  });

  it("에이전트마다 하나 · 턴 시작 시각 순 · 왼쪽 말풍선 자리 · 마지막 메시지 뒤", async () => {
    await ready();
    delta("a1", "t1", "하나.");
    delta("a1", "t1", "하나.\n\n둘.");
    delta("a2", "t2", "셋.");
    expect(bubbles().map((b) => b.dataset.agentId)).toEqual(["a1", "a2"]);
    for (const b of bubbles()) expect(b).toHaveAttribute("data-side", "left");
    const card = document.querySelector('[data-message-id="m0"]')!;
    expect(card.compareDocumentPosition(bubbles()[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("머리 — 배지 작업 중 + 요약(시간 · 많은 동작 2개) + 실패 꼬리 + 「···」, 받는 쪽(→)·말의 종류 없음", async () => {
    await ready();
    const head = within(bubbleOf("a1")).getByTestId("working-head");
    expect(within(head).getByTestId("working-badge")).toHaveTextContent("작업 중");
    const sum = within(head).getByTestId("working-summary").textContent ?? "";
    expect(sum).toMatch(/^\d+분/);
    expect(sum).toContain("셸 명령 36회");
    expect(sum).toContain("파일 읽기 5회");
    expect(within(head).getByTestId("fold-fail")).toHaveTextContent("실패 2");
    expect(within(head).getByTestId("working-dots")).toHaveAttribute("aria-hidden", "true");
    expect(bubbleOf("a1").querySelector(".convo__arrow, .convo__to, [data-testid=speech-kind]")).toBeNull();
    expect(head.textContent).not.toContain("→");
    // 실패 없는 Developer 는 꼬리가 없다.
    expect(within(bubbleOf("a2")).queryByTestId("fold-fail")).toBeNull();
  });
});

describe("진행 메모 — 마지막 문장 한 줄 · 펼침 문단", () => {
  it("한 줄은 마지막 조각의 마지막 문장만 · 델타가 없으면 줄이 없다", async () => {
    await ready();
    expect(within(bubbleOf("a2")).queryByTestId("working-memo-line")).toBeNull();
    delta("a1", "t1", "원인을 찾았습니다.\n\n하네스가 편향돼 있었습니다. 4명 승률 25% 씩으로 맞췄습니다.");
    const line = within(bubbleOf("a1")).getByTestId("working-memo-line");
    expect(line).toHaveTextContent("4명 승률 25% 씩으로 맞췄습니다.");
    expect(line.textContent).not.toContain("원인을");
    expect(line.textContent).not.toContain("편향");
  });

  it("펼치면 조각마다 한 문단(빈 줄) → 활동 피드 · 실시간 갱신에도 펼침 유지", async () => {
    await ready();
    delta("a1", "t1", "BGM v2 를 16분음표 격자로 다시 짜고 있습니다.\n\n테스트 28항목을 돌려 보겠습니다.");
    fireEvent.click(within(bubbleOf("a1")).getByTestId("working-fold"));
    const body = within(bubbleOf("a1")).getByTestId("working-body");
    expect(within(body).getAllByTestId("working-memo-para").map((p) => p.textContent)).toEqual(["BGM v2 를 16분음표 격자로 다시 짜고 있습니다.", "테스트 28항목을 돌려 보겠습니다."]);
    // 문단 다음에 활동 피드(꼬리 조각).
    const feed = within(body).getByTestId("activity-feed");
    expect(within(body).getAllByTestId("working-memo-para")[1].compareDocumentPosition(feed) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    delta("a1", "t1", "BGM v2 를 16분음표 격자로 다시 짜고 있습니다.\n\n테스트 28항목을 돌려 보겠습니다.\n\n모두 통과합니다.");
    expect(within(bubbleOf("a1")).getAllByTestId("working-memo-para")).toHaveLength(3);
    expect(within(bubbleOf("a1")).getByTestId("working-fold")).toHaveAttribute("aria-expanded", "true");
  });

  it("옛 데몬(빈 줄 없음) — 델타 사이의 같은 task 도구 이벤트로 나눈다 · 도구 없이 붙은 문장은 나누지 않는다", async () => {
    await ready();
    delta("a2", "t2", "테스트가 통과합니다.");
    send("task_event.appended", ev("t2", 12, {}));
    delta("a2", "t2", "테스트가 통과합니다.BGM v2 가 준비됐습니다.");
    delta("a2", "t2", "테스트가 통과합니다.BGM v2 가 준비됐습니다.다음은 효과음입니다.");
    fireEvent.click(within(bubbleOf("a2")).getByTestId("working-fold"));
    expect(within(bubbleOf("a2")).getAllByTestId("working-memo-para").map((p) => p.textContent)).toEqual(["테스트가 통과합니다.", "BGM v2 가 준비됐습니다.다음은 효과음입니다."]);
  });
});

describe("끝날 때 — 게시 · 턴 끝", () => {
  it("게시되면 그 자리에서 메시지로 — 말풍선은 없어지고(게시 뒤 새 일 없음) 새 메시지 카드가 선다 · 교체 첫 프레임은 높이 유지", async () => {
    await ready();
    delta("a2", "t2", "BGM 을 고쳤습니다.");
    // 말풍선 높이(jsdom 은 배치가 없어 0) — 교체 첫 프레임에 그 높이를 min-height 로 쥔다.
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ height: 120, width: 600, top: 0, left: 0, right: 600, bottom: 120, x: 0, y: 0, toJSON: () => ({}) } as DOMRect);
    delta("a2", "t2", "BGM 을 고쳤습니다. 올립니다.");
    const m: Message = { ...lead0, id: "m9", author_id: "a2", author: { name: "Developer" }, source_task_id: "t2", content: "BGM v2 올렸습니다.", created_at: new Date().toISOString() };
    send("message.created", m);
    send("task_event.appended", ev("t2", 13, { class: "status", verb: "post_message", object_ref: "m9", seq: 2 ** 30 + 1, created_at: new Date().toISOString(), payload: { command: "message post", result_ref: "m9" } }));
    const held = document.querySelector('[data-message-id="m9"]')!.parentElement as HTMLElement;
    expect(held).toHaveAttribute("data-held", "true");
    expect(held.style.minHeight).toBe("120px");
    expect(bubbleOf("a2")).toBeNull();
    // 한 프레임 뒤 놓는다.
    await waitFor(() => expect(held).not.toHaveAttribute("data-held"));
    expect(bubbles()).toHaveLength(1);
    // 게시 뒤 진행 메모가 다시 흐르면 그 메시지 아래에 다시 선다 — 게시 전 메모는 보이지 않는다.
    delta("a2", "t2", "BGM 을 고쳤습니다.\n\n효과음을 맞추는 중입니다.");
    await waitFor(() => expect(bubbleOf("a2")).not.toBeNull());
    expect(within(bubbleOf("a2")).getByTestId("working-memo-line")).toHaveTextContent("효과음을 맞추는 중입니다.");
    expect(bubbleOf("a2").textContent).not.toContain("BGM 을 고쳤습니다");
    expect(document.querySelector('[data-message-id="m9"]')!.compareDocumentPosition(bubbleOf("a2")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("턴이 끝나면 말풍선이 사라지고 꼬리는 마지막 메시지의 작업 과정으로 · 진행 메모도 사라진다", async () => {
    await ready();
    delta("a1", "t1", "밸런스를 맞췄습니다.");
    send("task_event.appended", ev("t1", 13, { class: "runtime", verb: "turn_end", outcome: "ok", payload: null }));
    send("task.updated", { ...TASKS.t1, status: "completed" });
    await waitFor(() => expect(bubbleOf("a1")).toBeNull());
    expect(bubbleOf("a2")).not.toBeNull();
    const fold = within(document.querySelector('[data-message-id="m0"]') as HTMLElement).getByTestId("process-fold");
    expect(fold.textContent).toContain("셸 명령 36회");
    expect(document.body.textContent).not.toContain("밸런스를 맞췄습니다.");
  });

  it("task.updated 단독 종료(turn-close 줄도 lane.updated 도 없이) — 말풍선도 진행 메모도 사라진다", async () => {
    await ready();
    delta("a1", "t1", "밸런스를 맞췄습니다.\n\n마무리하는 중입니다.");
    fireEvent.click(within(bubbleOf("a1")).getByTestId("working-fold"));
    expect(within(bubbleOf("a1")).getAllByTestId("working-memo-para")).toHaveLength(2);
    // 끝났다는 신호가 task.updated 하나뿐인 종료(데몬이 죽어 turn_end 줄이 안 오고 lane.updated 도 늦는 경우).
    send("task.updated", { ...TASKS.t1, status: "failed" });
    await waitFor(() => expect(bubbleOf("a1")).toBeNull());
    // 메모가 남아 있으면 같은 에이전트의 말풍선이 기록 없이 다시 선다 — 진행 메모는 영속되지 않는다(SCREEN §4.6).
    expect(document.body.textContent).not.toContain("마무리하는 중입니다.");
    expect(document.body.textContent).not.toContain("밸런스를 맞췄습니다.");
    expect(bubbleOf("a2")).not.toBeNull();
  });
});

describe("접근성 · 모션", () => {
  it("role=status · aria-live 는 머리 요약에만 — 진행 메모 줄·펼침은 aria-live 밖", async () => {
    await ready();
    delta("a1", "t1", "하나.\n\n둘.");
    const b = bubbleOf("a1");
    const head = within(b).getByTestId("working-head");
    expect(head).toHaveAttribute("role", "status");
    expect(head).toHaveAttribute("aria-live", "polite");
    expect(b).not.toHaveAttribute("aria-live");
    const line = within(b).getByTestId("working-memo-line");
    expect(line.closest("[aria-live]")).toBeNull();
    fireEvent.click(within(b).getByTestId("working-fold"));
    expect(within(b).getByTestId("working-body").closest("[aria-live]")).toBeNull();
    expect(within(b).getByTestId("working-fold")).toHaveAttribute("aria-controls", within(b).getByTestId("working-body").id);
  });

  it("점선 1px $line · 배경 없음 · 「···」 1.2s 애니메이션은 prefers-reduced-motion 이면 없다", () => {
    const css = readFileSync(join(process.cwd(), "components/message-layers.css"), "utf8");
    expect(css).toMatch(/\.wbub \.convo__bubble\.wbub__bubble \{[^}]*background: transparent;[^}]*border: 1px dashed var\(--line\);/);
    expect(css).toMatch(/\.wbub__dots > span \{[^}]*animation: wbub-dot 1\.2s/);
    expect(css).toMatch(/@media \(prefers-reduced-motion: reduce\) \{ \.wbub__dots > span \{ animation: none; \} \}/);
    // 옛 「작업 중」 줄의 좌측선 3px 실행 색은 없다.
    expect(css).not.toMatch(/\.working \.fold/);
  });
});
