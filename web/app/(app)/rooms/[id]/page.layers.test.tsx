/**
 * S7 에이전트 메시지 세 층(PRD FR-3.1.2 · SCREEN §4.6 v0.19.3) — 방 화면에서:
 * 대화만 기본 · 작업 내용/작업 과정 접힌 줄 · 폴백 「자동으로 접음」 · 접지 않는 메시지 · 보기 전환(작업 내용만 연다) ·
 * 카드마다의 펼침 · 실시간 갱신에도 펼침 유지 · 스레드 답글 · 아티팩트 참조 줄 · 델타는 대화 층에만 · localStorage 가 막혀도 돈다.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { Artifact, Me, Message, Room, StreamEvent, TaskEvent } from "@/lib/api/types";

const replace = vi.fn();
vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "r1" }),
  useRouter: () => ({ push: vi.fn(), replace }),
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
  id: "r1", workspace_id: "ws1", name: "결제팀", description: "", status: "active", visibility: "workspace",
  owner_user_id: "u1", deputy_owner_user_id: null, runtime_id: null, isolation: { kind: "none", remote_url: null },
  limits: { budget_usd: 50, time_limit: null, max_concurrent_works: 3, max_parallel_lanes: 5 }, autonomy: "guided", default_director_user_id: null,
  blocked_reason: null, blocked_detail: null, counts: { works_active: 0, lanes_active: 0, tasks_active: 0 }, cost_usd: 0, cost_estimated: false,
  unread_count: 0, my_room_role: "owner", my_capabilities: ["post"], created_by: "u1", created_at: "", updated_at: "", last_activity_at: null,
};

const TABLE = "| PG사 | 카드 |\n|---|---|\n| A사 | 2.1% |";
const LONG = ["@Lead 해외결제 요율도 정리했습니다.", "", TABLE, "", "근거 문단입니다. ".repeat(120)].join("\n");
let n = 0;
const m = (over: Partial<Message>): Message => ({
  id: `m${++n}`, session_id: "r1", author_type: "agent", author_id: "a1", author: { name: "Researcher" }, parent_id: null, content: "짧은 말", mentions: [],
  source_task_id: null, kind: "text", state: "posted", created_at: `2026-09-25T10:${String(10 + n).padStart(2, "0")}:00Z`, work_id: null, ...over,
});
const ev = (task: string, i: number, over: Partial<TaskEvent>): TaskEvent => ({
  id: `${task}-e${i}`, task_id: task, seq: i, class: "tool", verb: "search", object_ref: null, outcome: "ok", tool: null, input: null, output: null, usage: null,
  superseded_by: null, masked: false, sentence: null, created_at: `2026-09-25T10:0${Math.min(i, 9)}:00Z`, ...over,
} as TaskEvent);

let MSGS: Message[] = [];
let REPLIES: Message[] = [];
let ARTS: Artifact[] = [];
const EVENTS: Record<string, TaskEvent[]> = {
  t1: [ev("t1", 1, {}), ev("t1", 2, {}), ev("t1", 3, {}), ev("t1", 4, { verb: "read" }), ev("t1", 5, { verb: "run_shell", outcome: "failed" })],
  t3: [ev("t3", 1, { verb: "edit_file", payload: { path: "a.md" } }), ev("t3", 2, { class: "status", verb: "submit_artifact" })],
  t9: [],
};

function routes(path: string, opts?: { path?: Record<string, string>; query?: Record<string, unknown> }) {
  if (path === "/rooms/{roomId}") return Promise.resolve(room);
  if (path === "/rooms/{roomId}/works") return Promise.resolve({ items: [], next_cursor: null });
  if (path === "/rooms/{roomId}/participants") return Promise.resolve({ items: [
    { id: "p-u1", room_id: "r1", kind: "user", user: { id: "u1", email: "", display_name: "서연", avatar_url: null, created_at: "" }, room_role: "owner", joined_at: "", left_at: null },
    { id: "p-a1", room_id: "r1", kind: "agent", agent: { id: "a1", name: "Researcher", role: "researcher" }, status: "idle", room_role: "member", joined_at: "", left_at: null },
  ] });
  if (path === "/rooms/{roomId}/messages") {
    if (opts?.query?.thread) return Promise.resolve({ items: REPLIES, has_more_before: false, has_more_after: false });
    return Promise.resolve({ items: MSGS, has_more_before: false, has_more_after: false });
  }
  if (path === "/tasks/{taskId}/events") {
    const id = opts!.path!.taskId;
    return Promise.resolve({ items: EVENTS[id] ?? [], has_more: false, structured: id !== "t8" });
  }
  if (path === "/rooms/{roomId}/artifacts") return Promise.resolve(ARTS);
  if (path === "/rooms/{roomId}/lanes" || path.endsWith("/decisions")) return Promise.resolve([]);
  if (path.endsWith("/runtimes")) return Promise.resolve([{ id: "rt1", status: "online", name: "MacBook" }]);
  return Promise.resolve({ items: [] });
}

let research: Message, fallback: Message, human: Message, submit: Message, plain: Message;
beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  try { window.localStorage.clear(); } catch { /* */ }
  n = 0;
  research = m({ content: "@Lead 조사 끝났습니다.", detail: ["## A. PG사별 수수료 비교", "", TABLE, "", TABLE, "", "본문"].join("\n"), source_task_id: "t1", reply_count: 1 });
  fallback = m({ content: LONG, source_task_id: "t9" });
  human = m({ author_type: "user", author_id: "u1", author: { name: "서연" }, content: "사람 말. ".repeat(300) });
  submit = m({ author: { name: "Writer" }, content: "초안 v2 를 제출했습니다.", source_task_id: "t3" });
  plain = m({ content: "짧은 대답입니다." });
  MSGS = [
    research, fallback, human, submit, plain,
    m({ author_type: "system", author_id: null, kind: "system", content: "시스템. ".repeat(300) }),
    m({ kind: "summary", content: "요약. ".repeat(300) }),
    m({ kind: "blocked_q", content: "질문. ".repeat(300), source_task_id: "t9" }),
  ];
  REPLIES = [m({ parent_id: research.id, author: { name: "Writer" }, content: "표 3 초안입니다.", detail: "### 표 3\n\n" + TABLE, source_task_id: "t3" })];
  ARTS = [{ id: "art1", session_id: "r1", name: "report-draft.md", version: 2, type: "document", storage_ref: "x", submitted_by_task_id: "t3", created_at: "2026-09-25T10:00:00Z", latest: true } as Artifact];
  stream = null;
  get.mockReset().mockImplementation(routes);
  replace.mockReset();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const card = (id: string) => document.querySelector(`[data-message-id="${id}"]`) as HTMLElement;
async function ready() {
  render(<RoomPage />);
  await screen.findByTestId("room-title");
  await waitFor(() => expect(screen.getAllByTestId("message-card").length).toBe(MSGS.length));
}

describe("세 층 — 대화만 기본, 접힌 줄, 없는 층은 줄이 없다", () => {
  it("detail 이 있는 에이전트 메시지 — 대화는 보이고 「작업 내용 · 글자 수 · 표 N개 · 「첫 줄」」 줄이 접힌 채", async () => {
    await ready();
    const c = card(research.id);
    expect(within(c).getByText("@Lead 조사 끝났습니다.")).toBeInTheDocument();
    const fold = within(c).getByTestId("detail-fold");
    expect(fold).toHaveAttribute("aria-expanded", "false");
    expect(fold.textContent).toMatch(/^▸작업 내용\d+자 · 표 2개 · 「A\. PG사별 수수료 비교」$/);
    expect(within(c).queryByTestId("detail-body")).toBeNull();
    expect(within(c).queryByTestId("fold-auto")).toBeNull();
    // 펼치면 aria-controls 가 펼친 영역을 가리킨다.
    fireEvent.click(fold);
    expect(fold).toHaveAttribute("aria-expanded", "true");
    const body = within(c).getByTestId("detail-body");
    expect(body.id).toBe(fold.getAttribute("aria-controls"));
    expect(body.querySelectorAll(".md-table")).toHaveLength(2);
    expect(within(c).queryByTestId("detail-window")).toBeNull(); // 1만 자 이하
  });

  it("작업 과정 — 펼치기 전부터 요약(많은 동작 2개)과 실패 꼬리가 보이고, 펼치면 활동 피드", async () => {
    await ready();
    const c = card(research.id);
    const fold = within(c).getByTestId("process-fold");
    await waitFor(() => expect(fold.textContent).toContain("검색 3회"));
    expect(fold.textContent).toContain("파일 읽기 1회");
    expect(within(c).getByTestId("fold-fail").textContent).toBe("· 실패 1");
    expect(fold).toHaveAttribute("aria-expanded", "false");
    expect(within(c).queryByTestId("activity-feed")).toBeNull();
    // 예전 「활동 보기」 토글은 세 층 메시지에 없다 — 작업 과정 줄이 그 자리다.
    expect(within(c).queryByTestId("activity-toggle")).toBeNull();
    fireEvent.click(fold);
    expect(within(c).getByTestId("process-body").id).toBe(fold.getAttribute("aria-controls"));
    expect(within(c).getByTestId("activity-feed")).toBeInTheDocument();
  });

  it("작업 과정에 이벤트가 없으면 「대기 중…」(활동 피드 없음 규약), source_task_id 가 없으면 줄이 없다", async () => {
    await ready();
    await waitFor(() => expect(within(card(fallback.id)).getByTestId("process-fold").textContent).toContain("대기 중…"));
    expect(within(card(plain.id)).queryByTestId("process-fold")).toBeNull();
    expect(within(card(plain.id)).queryByTestId("detail-fold")).toBeNull();
    expect(within(card(submit.id)).queryByTestId("detail-fold")).toBeNull(); // detail 없고 짧다
  });

  it("폴백 — detail 없는 1,200자 초과 본문은 첫 문단만 대화, 나머지(표 포함)는 「작업 내용 · 자동으로 접음」", async () => {
    await ready();
    const c = card(fallback.id);
    const bodyText = c.querySelector(".msg__body")!.textContent!;
    expect(bodyText).toBe("@Lead 해외결제 요율도 정리했습니다.");
    expect(c.querySelector(".msg__body .md-table")).toBeNull(); // 표는 대화에 없다
    const fold = within(c).getByTestId("detail-fold");
    expect(within(fold).getByTestId("fold-auto").textContent).toBe("자동으로 접음");
    expect(fold.textContent).toContain("표 1개");
    expect(fold.textContent).toContain("「PG사 · 카드」");
    fireEvent.click(fold);
    expect(within(c).getByTestId("detail-body").querySelector(".md-table")).not.toBeNull();
  });

  it("접지 않는 것 — 사람 · 시스템 · 요약 · 질문 카드는 길어도 본문 그대로, 접힌 줄 없음", async () => {
    await ready();
    for (const x of MSGS.filter((y) => y.author_type !== "agent" || y.kind !== "text")) {
      const c = card(x.id);
      expect(within(c).queryByTestId("detail-fold"), x.kind).toBeNull();
      expect(within(c).queryByTestId("process-fold"), x.kind).toBeNull();
    }
    expect(card(human.id).querySelector(".msg__body")!.textContent!.length).toBeGreaterThan(1200);
  });

  it("아티팩트를 가리키는 메시지 — 그 턴(submitted_by_task_id)이 낸 아티팩트 참조 줄 「📄 이름 · 아티팩트 · vN · 열기」", async () => {
    await ready();
    const c = card(submit.id);
    const ref = await within(c).findByTestId("artifact-ref");
    expect(ref.textContent).toBe("📄report-draft.md아티팩트 · v2열기");
    expect(within(ref).getByTestId("artifact-ref-open")).toHaveAttribute("href", "/api/v1/artifacts/art1/content");
    expect(within(card(research.id)).queryByTestId("artifact-ref")).toBeNull();
  });
});

describe("타임라인 머리 보기 전환 — 「대화만 / 작업 내용 펼침」", () => {
  it("기본은 대화만 · 「작업 내용 펼침」은 작업 내용만 전부 연다(작업 과정은 닫힌 채) · 방 id 키로 기억", async () => {
    await ready();
    const toggle = screen.getByTestId("timeline-view");
    expect(toggle.textContent).toBe("보기대화만작업 내용 펼침");
    expect(screen.getByTestId("view-conversation")).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryAllByTestId("detail-body")).toHaveLength(0);
    fireEvent.click(screen.getByTestId("view-detail"));
    expect(screen.getByTestId("view-detail")).toHaveAttribute("aria-pressed", "true");
    const folds = screen.getAllByTestId("detail-fold");
    expect(folds.length).toBe(2);
    for (const f of folds) expect(f).toHaveAttribute("aria-expanded", "true");
    expect(screen.getAllByTestId("detail-body")).toHaveLength(2);
    // 작업 과정은 여전히 카드마다 — 전환이 열지 않는다.
    for (const f of screen.getAllByTestId("process-fold")) expect(f).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryAllByTestId("process-body")).toHaveLength(0);
    expect(window.localStorage.getItem("colab.timelineView.r1")).toBe(JSON.stringify("detail"));
  });

  it("저장된 보기로 시작한다 — 다시 열어도 작업 내용 펼침", async () => {
    window.localStorage.setItem("colab.timelineView.r1", JSON.stringify("detail"));
    await ready();
    await waitFor(() => expect(screen.getByTestId("view-detail")).toHaveAttribute("aria-pressed", "true"));
    for (const f of screen.getAllByTestId("detail-fold")) expect(f).toHaveAttribute("aria-expanded", "true");
  });

  it("카드 하나를 따로 접으면 그 카드만 바뀌고 전환은 그대로 · 전환을 다시 바꾸면 카드마다의 작업 내용 선택은 비운다(작업 과정 선택은 남는다)", async () => {
    await ready();
    fireEvent.click(screen.getByTestId("view-detail"));
    fireEvent.click(within(card(research.id)).getByTestId("detail-fold"));
    expect(within(card(research.id)).getByTestId("detail-fold")).toHaveAttribute("aria-expanded", "false");
    expect(within(card(fallback.id)).getByTestId("detail-fold")).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByTestId("view-detail")).toHaveAttribute("aria-pressed", "true");
    // 작업 과정 하나를 연 채로 전환을 두 번 바꾼다.
    fireEvent.click(within(card(submit.id)).getByTestId("process-fold"));
    fireEvent.click(screen.getByTestId("view-conversation"));
    fireEvent.click(screen.getByTestId("view-detail"));
    expect(within(card(research.id)).getByTestId("detail-fold")).toHaveAttribute("aria-expanded", "true");
    expect(within(card(submit.id)).getByTestId("process-fold")).toHaveAttribute("aria-expanded", "true");
  });

  it("localStorage 가 막혀도 화면은 돈다 — 기본 대화만, 전환도 된다", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    await ready();
    expect(screen.getByTestId("view-conversation")).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(screen.getByTestId("view-detail"));
    expect(screen.getAllByTestId("detail-body").length).toBe(2);
  });
});

describe("펼침 상태는 메시지 id 로 — 실시간 갱신·다시 그리기에도 남는다", () => {
  it("SSE message.created 로 목록이 바뀌고 message.updated 로 그 메시지 객체가 바뀌어도 펼친 작업 내용·작업 과정이 그대로", async () => {
    await ready();
    fireEvent.click(within(card(research.id)).getByTestId("detail-fold"));
    fireEvent.click(within(card(research.id)).getByTestId("process-fold"));
    const fresh = m({ content: "새 메시지", created_at: "2026-09-25T11:00:00Z" });
    act(() => stream!({ id: "1", type: "message.created", at: "", room_id: "r1", payload: fresh as unknown as Record<string, unknown> } as StreamEvent));
    await waitFor(() => expect(card(fresh.id)).not.toBeNull());
    act(() => stream!({ id: "2", type: "message.updated", at: "", room_id: "r1", payload: { ...research, reply_count: 2 } as unknown as Record<string, unknown> } as StreamEvent));
    const c = card(research.id);
    expect(within(c).getByTestId("detail-fold")).toHaveAttribute("aria-expanded", "true");
    expect(within(c).getByTestId("detail-body")).toBeInTheDocument();
    expect(within(c).getByTestId("process-fold")).toHaveAttribute("aria-expanded", "true");
  });

  it("스레드 답글도 같은 세 층 — 스레드를 접었다 다시 펼쳐도(카드 재마운트) 답글의 펼침이 남는다", async () => {
    await ready();
    fireEvent.click(within(card(research.id)).getByTestId("thread-toggle"));
    const thread = await within(card(research.id)).findByTestId("thread");
    const reply = within(thread).getByTestId("message-card");
    expect(within(reply).getByText("표 3 초안입니다.")).toBeInTheDocument();
    const fold = within(reply).getByTestId("detail-fold");
    expect(fold.textContent).toContain("표 1개");
    fireEvent.click(fold);
    expect(within(reply).getByTestId("detail-body")).toBeInTheDocument();
    // 같은 턴(t3)의 아티팩트는 제출 시각 뒤 첫 메시지 한 곳에만 — 앞선 제출 알림에 붙고 답글에는 없다.
    expect(within(reply).queryByTestId("artifact-ref")).toBeNull();
    fireEvent.click(within(card(research.id)).getByTestId("thread-toggle")); // 접기
    expect(within(card(research.id)).queryByTestId("thread")).toBeNull();
    fireEvent.click(within(card(research.id)).getByTestId("thread-toggle")); // 다시 펼치기 — 답글 카드가 새로 마운트된다
    const again = within(await within(card(research.id)).findByTestId("thread")).getByTestId("message-card");
    expect(within(again).getByTestId("detail-fold")).toHaveAttribute("aria-expanded", "true");
  });

  it("진행 메모(델타)는 「작업 중」 말풍선에만 — 길어도 작업 내용·작업 과정 접힌 줄이 없다(v0.19.10)", async () => {
    await ready();
    act(() => stream!({ id: "3", type: "message.delta", at: "", room_id: "r1", payload: { agent_id: "a1", text: "긴 델타. ".repeat(400) } } as unknown as StreamEvent));
    const bubble = await screen.findByTestId("working-bubble");
    expect(screen.queryByTestId("message-delta")).toBeNull();
    expect(within(bubble).queryByTestId("detail-fold")).toBeNull();
    expect(within(bubble).queryByTestId("process-fold")).toBeNull();
  });
});

describe("긴 작업 내용 — 1만 자 넘으면 「새 창으로 보기」", () => {
  it("1만 자 넘는 detail 은 새 창 단추가 있고 Blob URL 로 연다", async () => {
    research.detail = "가".repeat(10_001);
    await ready();
    const c = card(research.id);
    expect(within(c).getByTestId("detail-fold").textContent).toContain("1만 자");
    fireEvent.click(within(c).getByTestId("detail-fold"));
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    const created = vi.fn(() => "blob:x");
    Object.assign(URL, { createObjectURL: created, revokeObjectURL: vi.fn() });
    fireEvent.click(within(c).getByTestId("detail-window"));
    expect(within(c).getByTestId("detail-window").textContent).toBe("새 창으로 보기");
    expect(created).toHaveBeenCalled();
    expect(open).toHaveBeenCalledWith("blob:x", "_blank", "noopener");
  });
});
