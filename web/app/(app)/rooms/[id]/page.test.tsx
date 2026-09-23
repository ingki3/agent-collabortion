/**
 * S7 방 화면 · S22 미션 패널(T-R2-W2) — 칩 하나가 타임라인·보드·우열을 함께 바꾸는지(연동), (전체)·(미션 없음)에서 미션 동작이
 * 통째로 비활성인지, 끝난 미션은 읽기 전용인지, 「나에게 필요한 것」이 중복 없이 세는지, 좁은 화면 탭이 넷인지.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { HitlRequest, Lane, Me, Message, Room, StreamEvent, Work, WorkListItem } from "@/lib/api/types";

let search = new URLSearchParams();
const replace = vi.fn();
const push = vi.fn();
vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "r1" }),
  useRouter: () => ({ push, replace }),
  useSearchParams: () => search,
  usePathname: () => "/rooms/r1",
}));

const get = vi.fn();
const post = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { ...actual.api, get: (...a: unknown[]) => get(...a), post: (...a: unknown[]) => post(...a) } };
});

const me: Me = {
  user: { id: "u1", email: "seoyeon@x", display_name: "서연", avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
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
import { problemFixture } from "@/lib/mock/problem-fixture";
import { notFound } from "@/lib/mock/wording";

const u = (id: string, name: string) => ({ id, email: `${id}@x`, display_name: name, avatar_url: null, created_at: "" });
const room: Room = {
  id: "r1", workspace_id: "ws1", name: "결제팀", description: "결제 관련 논의와 작업", status: "active", visibility: "workspace",
  owner_user_id: "u1", deputy_owner_user_id: null, runtime_id: null, isolation: { kind: "none", remote_url: null },
  limits: { budget_usd: 50, time_limit: null, max_concurrent_works: 3, max_parallel_lanes: 5 }, autonomy: "guided", default_director_user_id: null,
  blocked_reason: null, blocked_detail: null, counts: { works_active: 2, lanes_active: 3, tasks_active: 0 }, cost_usd: 12.4, cost_estimated: false,
  unread_count: 0, my_room_role: "owner", my_capabilities: ["post", "invite", "configure", "link", "block", "archive", "delete", "transfer_owner", "summarize"],
  created_by: "u1", created_at: "2026-09-20T00:00:00Z", updated_at: "2026-09-20T00:00:00Z", last_activity_at: null,
};
const wli = (id: string, title: string, status: WorkListItem["status"], at: string): WorkListItem => ({
  id, room_id: "r1", title, goal: title, status, paused_reason: null, waiting_human: false, director: u("u1", "서연"), assignee_agent_id: null,
  completion_progress: { met: 0, total: 1 }, cost_usd: 3.2, budget_usd: 20, last_activity_at: at, finished_at: status === "completed" ? "2026-09-20T09:00:00Z" : null,
});
const WORKS = [
  wli("w1", "보고서 초안", "active", "2026-09-22T10:00:00Z"),
  wli("w2", "수수료 비교", "active", "2026-09-22T11:00:00Z"),
  wli("w9", "지난 보고서", "completed", "2026-09-20T09:00:00Z"),
];
const work = (id: string, over: Partial<Work> = {}): Work => {
  const li = WORKS.find((w) => w.id === id)!;
  return {
    id, room_id: "r1", title: li.title, goal: `${li.title} 목표`, acceptance_criteria: [], director_user_id: "u1", director: u("u1", "서연"), deputy_user_id: null,
    assignee_agent_id: null, completion_condition: { type: "user_approval" } as Work["completion_condition"],
    completion_progress: { met: 0, total: 1, satisfied: false, human_gate: true, conditions: [{ path: "/conditions/0", type: "user_approval", met: false, met_at: null, met_by: null, next_actor: "director", hitl_request_id: null, agent_id: null, agent_name: null, blocked_reason: null }] },
    limits: { budget_usd: 20 }, autonomy: "guided", status: li.status, paused_reason: null, cost_usd: 3.2, created_by: "u1", created_at: "", updated_at: "",
    my_work_role: "director", finished_at: li.finished_at, ...over,
  };
};
const msg = (id: string, work_id: string | null, content: string): Message => ({
  id, session_id: "r1", author_type: "user", author_id: "u1", author: { name: "서연", avatar_url: null }, parent_id: null, content, mentions: [],
  source_task_id: null, kind: "text", state: "posted", created_at: `2026-09-22T10:0${id}:00Z`, work_id,
});
const MSGS = [msg("1", "w1", "초안 방향"), msg("2", null, "점심 뭐 먹지"), msg("3", "w2", "수수료 표")];
const lane = (id: string, work_id: string | null, status: Lane["status"]): Lane => ({
  id, session_id: "r1", parent_lane_id: null, agent_id: "a1", agent_name: "Writer", profile_id: "p1", depends_on: [], workdir_id: null, delegated_from_task_id: null,
  status, blocked_note: null, blocked_message_id: null, reentry_count: 0, created_at: "2026-09-22T10:00:00Z", updated_at: "", finished_at: null, actions: [], work_id,
});
let roomNow: Room = room;
let hitls: HitlRequest[] = [];

function routes(path: string, opts?: { path?: Record<string, string>; query?: Record<string, unknown> }) {
  if (path === "/rooms/{roomId}") return Promise.resolve(roomNow);
  if (path === "/rooms/{roomId}/works") return Promise.resolve({ items: WORKS, next_cursor: null });
  if (path === "/rooms/{roomId}/participants") return Promise.resolve({ items: [
    { id: "p-u1", room_id: "r1", kind: "user", user: u("u1", "서연"), room_role: "owner", joined_at: "", left_at: null },
    { id: "p-a1", room_id: "r1", kind: "agent", agent: { id: "a1", name: "Writer", role: "writer" }, status: "working", room_role: "member", joined_at: "", left_at: null },
  ] });
  if (path === "/works/{workId}") {
    const id = opts!.path!.workId;
    if (!WORKS.some((w) => w.id === id)) return Promise.reject(problemFixture("not_found", 404, { detail: notFound("work") }));
    return Promise.resolve(work(id));
  }
  // 서버는 아직 work_id 를 안 읽는다(Lead 판정 A) — 목도 여기서 전부 돌려주고, 화면이 한 번 더 거르는지 본다.
  if (path === "/sessions/{sessionId}/messages") return Promise.resolve({ items: MSGS, has_more_before: false, has_more_after: false });
  if (path === "/sessions/{sessionId}/lanes") return Promise.resolve([lane("l1", "w1", "running"), lane("l2", "w2", "queued"), lane("l3", null, "done")]);
  if (path === "/sessions/{sessionId}/hitl-requests") return Promise.resolve({ items: hitls });
  if (path.endsWith("/artifacts") || path.endsWith("/decisions")) return Promise.resolve([]);
  if (path.endsWith("/runtimes")) return Promise.resolve([{ id: "rt1", status: "online", name: "MacBook" }]);
  return Promise.resolve({ items: [] });
}

beforeEach(() => {
  // jsdom 에는 스크롤이 없다 — 타임라인 끝·앵커로 내리는 호출만 받아 둔다.
  Element.prototype.scrollIntoView = vi.fn();
  search = new URLSearchParams();
  roomNow = room;
  hitls = [];
  stream = null;
  get.mockReset().mockImplementation(routes);
  post.mockReset();
  replace.mockReset();
  push.mockReset();
});
afterEach(cleanup);

async function ready() {
  render(<RoomPage />);
  await screen.findByTestId("room-title");
  await waitFor(() => expect(screen.getAllByTestId("message-card").length).toBeGreaterThan(0));
}

describe("S7 — 미션 칩은 거르개이자 선택자(타임라인·보드·우열 연동)", () => {
  it("(전체) — 거르지 않고 카드마다 미션 라벨, 우열은 「최근 활동」 미션을 보여 주기만(동작 비활성 + 사유 글자)", async () => {
    await ready();
    expect(screen.getAllByTestId("message-card")).toHaveLength(3);
    expect(screen.getAllByTestId("message-work-label").map((e) => e.textContent)).toEqual(["미션 「보고서 초안」", "미션 없음", "미션 「수수료 비교」"]);
    expect(screen.getAllByTestId("lane-work-label").length).toBeGreaterThan(0);
    const panel = await screen.findByTestId("work-panel");
    await waitFor(() => expect(panel.getAttribute("data-mode")).toBe("recent"));
    expect(panel.getAttribute("data-work-id")).toBe("w2"); // 최근 활동한 미션
    expect(screen.getByTestId("work-panel-recent").textContent).toBe("최근 활동: 「수수료 비교」 · 다른 미션을 보려면 위 칩을 누르세요");
    for (const k of ["pause", "complete", "cancel"]) {
      const b = screen.getByTestId(`work-action-${k}`);
      expect(b).toBeDisabled();
      expect(b.getAttribute("aria-describedby")).toBe("work-actions-why");
    }
    expect(screen.getByTestId("work-actions-why").textContent).toBe("어느 미션인지 먼저 고르세요 — 위 칩에서 미션을 누르면 이 버튼이 켜집니다");
    expect(screen.getByTestId("chip-all").getAttribute("aria-pressed")).toBe("true");
  });

  it("칩을 누르면 URL(?work=)로 선택이 바뀌고 — 타임라인·보드가 그 미션만, 라벨은 감추고, 우열은 그 미션(동작 켜짐)", async () => {
    await ready();
    fireEvent.click(screen.getAllByTestId("work-chip").find((c) => c.getAttribute("data-work-id") === "w1")!);
    expect(replace).toHaveBeenCalledWith("/rooms/r1?work=w1", { scroll: false });
    // 라우터가 URL 을 바꾼 뒤의 화면
    cleanup();
    search = new URLSearchParams("work=w1");
    await ready();
    expect(screen.getAllByTestId("message-card").map((c) => c.textContent)).toEqual([expect.stringContaining("초안 방향")]);
    expect(screen.queryByTestId("message-work-label")).toBeNull(); // 양방향 규칙 — 거른 목록에 같은 이름을 반복하지 않는다
    expect(screen.queryByTestId("lane-work-label")).toBeNull();
    expect(screen.getAllByTestId("lane-card").map((c) => c.getAttribute("data-lane-id"))).toEqual(["l1"]);
    // 계약 파라미터는 보낸다(서버가 읽기 시작하면 이중 거르기는 무해)
    expect(get).toHaveBeenCalledWith("/sessions/{sessionId}/messages", { path: { sessionId: "r1" }, query: expect.objectContaining({ work_id: "w1", limit: 50 }) });
    const panel = screen.getByTestId("work-panel");
    await waitFor(() => expect(panel.getAttribute("data-work-id")).toBe("w1"));
    expect(panel.getAttribute("data-mode")).toBe("picked");
    expect(screen.getByTestId("work-action-pause")).toBeEnabled();
    expect(screen.queryByTestId("work-actions-why")).toBeNull();
    expect(screen.getByTestId("work-cost").textContent).toContain("이 미션 $3.20 / $20");
    // 작성창 선택기의 기본값 = 지금 고른 칩
    expect((screen.getByTestId("work-selector-select") as HTMLSelectElement).value).toBe("w1");
    // 조용한 안내 — 두 곳이 바뀐 것을 소리로도
    expect(screen.getByTestId("chip-announce").textContent).toBe("「보고서 초안」 미션으로 걸렀습니다 · 서브 미션 1개 · 메시지 1개");
    // 미션 동작 — 고른 미션에 보낸다
    post.mockResolvedValueOnce(work("w1", { status: "paused", paused_reason: "director" }));
    fireEvent.click(screen.getByTestId("work-action-pause"));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/works/{workId}/pause", expect.objectContaining({ path: { workId: "w1" } })));
  });

  it("(미션 없음) — work_id = null 만 남고 우열은 칸을 남긴 채 비운다(동작 비활성, 사유 「미션 없이 오간 대화에는 끝이 없습니다」)", async () => {
    search = new URLSearchParams("work=none");
    await ready();
    expect(screen.getAllByTestId("message-card").map((c) => c.textContent)).toEqual([expect.stringContaining("점심 뭐 먹지")]);
    expect(get).toHaveBeenCalledWith("/sessions/{sessionId}/messages", { path: { sessionId: "r1" }, query: expect.objectContaining({ no_work: true }) });
    expect(screen.getByTestId("work-panel").getAttribute("data-mode")).toBe("none_view");
    expect(screen.getByTestId("work-panel-none").textContent).toBe("이 방에서 미션 없이 오간 대화입니다");
    expect(screen.getByTestId("work-panel-na").textContent).toBe("미션이 없어 해당 없음");
    expect(screen.getByTestId("work-action-complete")).toBeDisabled();
    expect(screen.getByTestId("work-actions-why").textContent).toBe("미션 없이 오간 대화에는 끝이 없습니다");
    // 방 전체 칸은 그대로(미션 선택과 무관)
    expect(screen.getByTestId("room-panel")).toBeInTheDocument();
  });

  it("S22 — 끝난 미션은 읽기 전용 + 「끝난 미션입니다」, 좁은 화면이면 미션 탭이 열린 채 시작", async () => {
    search = new URLSearchParams("work=w9");
    await ready().catch(() => undefined);
    await waitFor(() => expect(screen.getByTestId("work-ended")).toBeInTheDocument());
    expect(screen.getByTestId("work-ended").textContent).toContain("끝난 미션입니다");
    expect(screen.queryByTestId("work-actions")).toBeNull();
    expect(screen.getByTestId("room-detail").getAttribute("data-col")).toBe("work");
    // 지난 미션은 「지난 미션 ▾」 안에 있다
    expect(screen.getByTestId("chip-past").textContent).toContain("지난 미션 1개");
  });

  it("S22 — 이 방에 없는 미션이면 「이 방에 그 미션이 없습니다」", async () => {
    search = new URLSearchParams("work=nope");
    render(<RoomPage />);
    expect(await screen.findByTestId("work-not-found")).toHaveTextContent("이 방에 그 미션이 없습니다");
  });
});

describe("S7 — 상단 · 배너 · 좁은 화면", () => {
  it("세 층 요약은 수마다 라벨, 0 인 층은 생략", async () => {
    await ready();
    expect(screen.getByTestId("room-layers").textContent).toBe("미션 2개 · 서브 미션 3개");
    expect(screen.queryByTestId("layer-tasks")).toBeNull();
  });

  it("「나에게 필요한 것 N」 — 방 멈춤(manual, 내가 풀 수 있음) + 내가 답할 요청을 중복 없이", async () => {
    roomNow = { ...room, blocked_reason: "manual", blocked_detail: { reason: "manual", works_stopped: 2, blocked_by_user: u("u1", "서연") } };
    hitls = [{ id: "h1", session_id: "r1", status: "open", can_respond: true, message_id: "3" } as HitlRequest];
    await ready();
    expect(screen.getByTestId("needs-me").getAttribute("data-count")).toBe("2");
    const banner = screen.getByTestId("room-banner");
    expect(banner).toHaveAttribute("role", "alert");
    expect(within(banner).getByTestId("room-banner-stopped").querySelector("[data-slot]")!.textContent).toBe("2");
    // 멈춘 방에서는 상단 버튼도 「멈춤 해제」
    expect(screen.getByTestId("room-unblock")).toHaveTextContent("멈춤 해제");
  });

  it("room.updated 로 멈추면 배너가 선다(실시간)", async () => {
    await ready();
    expect(screen.queryByTestId("room-banner")).toBeNull();
    act(() => stream!({ id: "1", type: "room.updated", at: "", room_id: "r1", payload: { id: "r1", blocked_reason: "loop", blocked_detail: { reason: "loop", works_stopped: 1 } } }));
    expect(screen.getByTestId("room-banner").getAttribute("data-reason")).toBe("loop");
    // 다른 방의 프레임은 버린다
    act(() => stream!({ id: "2", type: "room.updated", at: "", room_id: "r2", payload: { id: "r2", blocked_reason: null } }));
    expect(screen.getByTestId("room-banner")).toBeInTheDocument();
  });

  it("「이 방 멈춤」 — 확인 다이얼로그가 턴·미션 수를 칸으로 적고 blockRoom 을 부른다", async () => {
    await ready();
    fireEvent.click(screen.getByTestId("room-block"));
    const dlg = screen.getByTestId("block-dialog");
    expect(within(dlg).getByTestId("block-dialog-turns").textContent).toBe("이 방의 진행 중인 턴 1개가 중단되고 새 트리거가 막힙니다.");
    expect(within(dlg).getByTestId("block-dialog-works").textContent).toBe("미션 2개와 미션 밖 대화 전부가 멈춥니다.");
    post.mockResolvedValueOnce({ ...room, blocked_reason: "manual", blocked_detail: { works_stopped: 2 } });
    fireEvent.click(screen.getByTestId("block-dialog-confirm"));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/rooms/{roomId}/block", { path: { roomId: "r1" } }));
    expect(await screen.findByTestId("room-banner")).toBeInTheDocument();
  });

  it("권한이 없으면 「이 방 멈춤」은 비활성 + 버튼 아래 사유", async () => {
    roomNow = { ...room, my_room_role: "member", my_capabilities: ["post", "summarize"] };
    await ready();
    const b = screen.getByTestId("room-block");
    expect(b).toBeDisabled();
    expect(document.getElementById(b.getAttribute("aria-describedby")!)!.textContent).toBe("방장·부방장이나 워크스페이스 소유자·관리자만 이 방을 멈출 수 있습니다");
  });

  it("대기 사유 runtime — 워크트리 방에 컴퓨터가 아직 없으면 「저장소가 있는 컴퓨터를 기다립니다」", async () => {
    roomNow = { ...room, isolation: { kind: "worktree", remote_url: null }, runtime_id: null };
    get.mockImplementation((path: string, o?: never) => (path === "/sessions/{sessionId}/lanes" ? Promise.resolve([{ ...lane("q1", null, "queued"), queued_reason: "runtime" }]) : routes(path, o)));
    await ready();
    expect(screen.getByTestId("lane-queued-reason").textContent).toBe("저장소가 있는 컴퓨터를 기다립니다");
  });

  it("좁은 화면 탭은 넷 — 타임라인 · 보드 · 미션 · 방", async () => {
    await ready();
    expect(["tab-timeline", "tab-board", "tab-work", "tab-room"].map((t) => screen.getByTestId(t).textContent)).toEqual(["타임라인", "보드", "미션", "방"]);
    fireEvent.click(screen.getByTestId("tab-room"));
    expect(screen.getByTestId("room-detail").getAttribute("data-col")).toBe("room");
  });

  it("「다른 방에서 작업 중」 — 이 방에 실행 중인 서브 미션이 없는데 working 이면 프로파일 자리를 바꿔 넣는다", async () => {
    get.mockImplementation((path: string, o?: never) => (path === "/sessions/{sessionId}/lanes" ? Promise.resolve([lane("l3", null, "done")]) : routes(path, o)));
    await ready();
    const chip = screen.getAllByTestId("agent-chip").find((c) => c.getAttribute("data-agent-id") === "a1")!;
    expect(within(chip).getByTestId("agent-chip-line2").textContent).toBe("writer · 다른 방에서 작업 중");
  });

  it("방 전체 칸은 기본 접힘 — 접혀도 머리줄에 수를 적는다", async () => {
    await ready();
    const t = screen.getByTestId("room-panel-toggle");
    expect(t.getAttribute("aria-expanded")).toBe("false");
    expect(t.textContent).toContain("방 전체 · 아티팩트 0 · 결정 0 · 누적 $12.40");
    fireEvent.click(t);
    expect(screen.getByTestId("room-cost-not-sum").textContent).toBe("미션 비용의 합이 아닙니다 — 미션 밖 대화 비용이 함께 듭니다");
  });

  it("「이걸 미션으로」 — 이미 미션에 속한 메시지는 비활성 + 사유, 미션 밖 메시지는 S21(?work=from&message=) 을 연다", async () => {
    await ready();
    const cards = screen.getAllByTestId("message-card");
    fireEvent.click(within(cards[0]).getByTestId("message-menu"));
    const item = within(cards[0]).getByTestId("message-to-work");
    expect(item.getAttribute("aria-disabled")).toBe("true");
    expect(document.getElementById(item.getAttribute("aria-describedby")!)!.textContent).toBe("이미 미션 「보고서 초안」에 속한 메시지입니다");
    fireEvent.click(within(cards[1]).getByTestId("message-menu"));
    fireEvent.click(within(cards[1]).getByTestId("message-to-work"));
    expect(replace).toHaveBeenCalledWith("/rooms/r1?work=from&message=2", { scroll: false });
  });

  it("S21 · S19 연결 — 「+ 새 미션」은 ?work=new, ?work=new 로 들어오면 미션 열기 다이얼로그, 「이 방에서 나가기」는 참여자 다이얼로그", async () => {
    await ready();
    fireEvent.click(screen.getByTestId("new-work"));
    expect(replace).toHaveBeenCalledWith("/rooms/r1?work=new", { scroll: false });
    cleanup();
    search = new URLSearchParams("work=new");
    render(<RoomPage />);
    expect(await screen.findByTestId("rd-create-work-goal")).toBeInTheDocument();
    cleanup();
    search = new URLSearchParams();
    await ready();
    fireEvent.click(screen.getByTestId("room-more"));
    const leave = screen.getByTestId("room-menu-leave");
    expect(leave.getAttribute("aria-disabled")).toBeNull();
    fireEvent.click(leave);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });
});
