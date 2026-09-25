/**
 * T-APPROVAL(Director 2026-09-25) — S7 타임라인의 확인 요청은 **명시적 버튼 카드**다.
 *
 * 실측(「게임 제작 방」): 완료 승인 HITL 이 타임라인에 **시스템 문장 한 줄 + 「답글」** 로 그려졌다. 요청 목록에 짝이 없었기
 * 때문인데(서버가 그 경로에서 `hitl.created` 를 안 보냈다), 화면 쪽 규칙도 함께 고친다 — **`kind: hitl` 은 절대 평문으로
 * 떨어지지 않는다.** 짝이 없으면 요청을 직접 읽어 와 카드로 그리고, 그동안에는 「불러오는 중」 자리를 그린다.
 *
 * 여기서 재는 것:
 *   1. 목록에 짝이 있으면 카드(버튼 포함), 일반 메시지 카드가 아니다.
 *   2. 짝이 없으면 **getHitlRequest 로 채워** 카드가 된다 — 평문으로 남지 않는다.
 *   3. `hitl.created` 가 늦게 와도 카드가 된다(실시간).
 *   4. 버튼이 실제로 계약 본문을 보낸다(승인 · 수정 요청+사유).
 *   5. 권한이 없으면 컨트롤 없이 사유만.
 *   6. 보류 중이면 미션 칸이 「조건 충족 — 진행 중인 작업이 끝나면 승인을 요청합니다」.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { HitlRequest, Me, Message, Room, StreamEvent, Work, WorkListItem } from "@/lib/api/types";

let search = new URLSearchParams();
vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "r1" }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  useSearchParams: () => search,
  usePathname: () => "/rooms/r1",
}));
const get = vi.fn();
const post = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  const read = (...a: unknown[]) => (a[0] === "/rooms/{roomId}/read" ? Promise.resolve({ room_id: "r1", unread_count: 0 }) : post(...a));
  return { ...actual, api: { ...actual.api, get: (...a: unknown[]) => get(...a), post: read } };
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
  useWorkspaceStream: (_ws: string, h: (ev: StreamEvent) => void) => { stream = h; return "open"; },
}));

import RoomPage from "./page";

const u = (id: string, name: string) => ({ id, email: `${id}@x`, display_name: name, avatar_url: null, created_at: "" });
const room: Room = {
  id: "r1", workspace_id: "ws1", name: "게임 제작 방", description: "", status: "active", visibility: "workspace",
  owner_user_id: "u1", deputy_owner_user_id: null, runtime_id: null, isolation: { kind: "none", remote_url: null },
  limits: { budget_usd: 50, time_limit: null, max_concurrent_works: 3, max_parallel_lanes: 5 }, autonomy: "guided", default_director_user_id: null,
  blocked_reason: null, blocked_detail: null, counts: { works_active: 1, lanes_active: 1, tasks_active: 1 }, cost_usd: 4.2, cost_estimated: false,
  unread_count: 0, my_room_role: "owner", my_capabilities: ["post", "block", "summarize"],
  created_by: "u1", created_at: "", updated_at: "", last_activity_at: null,
};
const WORKS: WorkListItem[] = [{
  id: "w1", room_id: "r1", title: "턴제 게임", goal: "턴제 게임", status: "active", paused_reason: null, waiting_human: false,
  director: u("u1", "서연"), assignee_agent_id: "a1", completion_progress: { met: 1, total: 2 }, cost_usd: 4.2, budget_usd: 20,
  last_activity_at: "2026-09-25T13:41:01Z", finished_at: null,
}];
/** 미션 — `held` 면 user_approval 행이 보류(openapi v0.3.3 held_reason). */
const work = (held: boolean): Work => ({
  id: "w1", room_id: "r1", title: "턴제 게임", goal: "턴제 게임 만들기", acceptance_criteria: [], director_user_id: "u1", director: u("u1", "서연"),
  deputy_user_id: null, assignee_agent_id: "a1",
  completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }] } as Work["completion_condition"],
  completion_progress: {
    met: 1, total: 2, satisfied: false, human_gate: true,
    conditions: [
      { path: "/conditions/0", type: "artifact_submitted", met: true, met_at: "2026-09-25T13:41:01Z", met_by: "a1", next_actor: null, held_reason: null, hitl_request_id: null, agent_id: "a1", agent_name: "Lead", blocked_reason: null },
      { path: "/conditions/1", type: "user_approval", met: false, met_at: null, met_by: null, next_actor: "director", held_reason: held ? "running_tasks" : null, hitl_request_id: held ? null : "h1", agent_id: null, agent_name: null, blocked_reason: null },
    ],
  },
  limits: { budget_usd: 20 }, autonomy: "guided", status: "active", paused_reason: null, cost_usd: 4.2, created_by: "u1", created_at: "", updated_at: "",
  my_work_role: "director", finished_at: null,
});
/** 완료 승인 카드 — 서버가 실제로 넣는 본문(messages.HitlCard.CardBody). */
const hitlMsg = (over: Partial<Message> = {}): Message => ({
  id: "m-hitl", session_id: "r1", author_type: "system", author_id: null, parent_id: null,
  content: "[확인 요청 · 승인] 종료 조건이 모두 충족되었습니다. 승인하시겠습니까?", mentions: [],
  source_task_id: null, kind: "hitl", state: "posted", created_at: "2026-09-25T13:41:01Z", work_id: "w1", ...over,
});
const approval = (over: Partial<HitlRequest> = {}): HitlRequest => ({
  id: "h1", session_id: "r1", task_id: null, lane_id: null, source: "system", type: "approval", purpose: "user_approval",
  question: "종료 조건이 모두 충족되었습니다. 승인하시겠습니까?", context: null, options: [], proposed_default: null, artifact_id: null,
  approver_spec: "director", due_at: "2026-09-26T13:41:01Z", overdue: false, status: "open", approved: null, answer: null,
  answered_by: null, answered_at: null, budget_override_usd: null, can_respond: true, can_respond_from: null,
  message_id: "m-hitl", created_at: "2026-09-25T13:41:01Z", ...over,
});

let hitls: HitlRequest[] = [];
let messages: Message[] = [];
let heldWork = false;
function routes(path: string, opts?: { path?: Record<string, string> }) {
  if (path === "/rooms/{roomId}") return Promise.resolve(room);
  if (path === "/rooms/{roomId}/works") return Promise.resolve({ items: WORKS, next_cursor: null });
  if (path === "/works/{workId}") return Promise.resolve(work(heldWork));
  if (path === "/workspaces/{workspaceId}/members") return Promise.resolve({ items: [
    { id: "m-u1", workspace_id: "ws1", user: u("u1", "서연"), role: "member", joined_at: "" },
  ] });
  if (path === "/rooms/{roomId}/participants") return Promise.resolve({ items: [
    { id: "p-u1", room_id: "r1", kind: "user", user: u("u1", "서연"), room_role: "owner", joined_at: "", left_at: null },
    { id: "p-a1", room_id: "r1", kind: "agent", agent: { id: "a1", name: "Lead", role: "lead" }, status: "working", room_role: "member", joined_at: "", left_at: null },
  ] });
  if (path === "/rooms/{roomId}/messages") return Promise.resolve({ items: messages, has_more_before: false, has_more_after: false });
  if (path === "/rooms/{roomId}/hitl-requests") return Promise.resolve({ items: hitls });
  if (path === "/hitl-requests/{hitlRequestId}") {
    const h = hitls.find((x) => x.id === opts!.path!.hitlRequestId) ?? approval();
    return Promise.resolve(h);
  }
  if (path === "/rooms/{roomId}/lanes") return Promise.resolve([]);
  if (path.endsWith("/artifacts") || path.endsWith("/decisions")) return Promise.resolve([]);
  if (path.endsWith("/runtimes")) return Promise.resolve([{ id: "rt1", status: "online", name: "MacBook" }]);
  return Promise.resolve({ items: [] });
}

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  search = new URLSearchParams();
  stream = null;
  hitls = [];
  heldWork = false;
  messages = [hitlMsg()];
  get.mockReset().mockImplementation(routes);
  post.mockReset();
});
afterEach(cleanup);

async function ready() {
  render(<RoomPage />);
  await screen.findByTestId("room-title");
}

describe("T-APPROVAL — 확인 요청은 버튼 카드다(평문 금지)", () => {
  it("목록에 짝이 있으면 버튼 카드 — 일반 메시지 카드(답글)가 아니다", async () => {
    hitls = [approval()];
    await ready();
    const card = await screen.findByTestId("hitl-card");
    expect(card.getAttribute("data-purpose")).toBe("user_approval");
    // 「승인」·「수정 요청」 두 버튼(승인 요청 카드의 명시적 컨트롤).
    expect(within(card).getByTestId("hitl-approve").textContent).toBe("승인");
    expect(within(card).getByTestId("hitl-reject").textContent).toBe("수정 요청");
    // 평문 메시지 카드로 그려지지 않는다 — 이것이 Director 가 본 화면이었다.
    expect(screen.queryByTestId("message-card")).toBeNull();
  });

  it("짝이 없으면 **요청을 직접 읽어** 카드가 된다 — 평문 한 줄로 남지 않는다(실측 버그)", async () => {
    messages = [hitlMsg({ hitl_request_id: "h1" })];
    hitls = []; // 서버가 hitl.created 를 안 보냈고 목록에도 아직 없다
    await ready();
    await waitFor(() => expect(get).toHaveBeenCalledWith("/hitl-requests/{hitlRequestId}", { path: { hitlRequestId: "h1" } }));
    const card = await screen.findByTestId("hitl-card");
    expect(within(card).getByTestId("hitl-approve")).toBeEnabled();
    expect(screen.queryByTestId("message-card")).toBeNull();
  });

  it("요청을 읽는 **동안에도** 평문이 아니다 — 「불러오는 중」 카드 자리(Director 가 본 화면이 이 순간이었다)", async () => {
    messages = [hitlMsg({ hitl_request_id: "h1" })];
    hitls = [];
    // 응답이 영영 오지 않는 상태를 고정해, 짝이 없는 **그 순간** 무엇이 그려지는지 잰다.
    get.mockImplementation((path: string, o?: never) =>
      (path === "/hitl-requests/{hitlRequestId}" ? new Promise(() => {}) : routes(path, o)));
    await ready();
    const slot = await screen.findByTestId("timeline-hitl");
    expect(within(slot).getByTestId("hitl-card-loading")).toBeInTheDocument();
    // 시스템 문장 한 줄 + 「답글」 로 떨어지지 않는다.
    expect(screen.queryByTestId("message-card")).toBeNull();
  });

  it("요청 id 도 모르면 목록을 다시 읽는다(두 번째 listHitlRequests)", async () => {
    messages = [hitlMsg()];
    await ready();
    await waitFor(() => {
      const listCalls = get.mock.calls.filter((c) => c[0] === "/rooms/{roomId}/hitl-requests");
      expect(listCalls.length).toBeGreaterThan(1);
    });
  });

  it("hitl.created 가 늦게 와도 그때 카드가 된다(실시간)", async () => {
    messages = [hitlMsg()];
    await ready();
    expect(screen.queryByTestId("hitl-card")).toBeNull();
    act(() => stream!({ id: "1", type: "hitl.created", at: "", room_id: "r1", payload: approval() as unknown as Record<string, unknown> }));
    expect(await screen.findByTestId("hitl-card")).toBeInTheDocument();
  });

  it("승인 버튼이 계약 본문을 보낸다 — approved: true", async () => {
    hitls = [approval()];
    post.mockResolvedValue({ hitl_request: approval({ status: "answered", approved: true, answered_by: "u1", answered_at: "2026-09-25T14:00:00Z" }), ignored: false });
    await ready();
    fireEvent.click(await screen.findByTestId("hitl-approve"));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/hitl-requests/{hitlRequestId}/response", expect.objectContaining({
      path: { hitlRequestId: "h1" }, body: { approved: true },
    })));
  });

  it("「수정 요청」은 사유 없이 못 보낸다 — 사유를 적으면 approved:false + reason", async () => {
    hitls = [approval()];
    post.mockResolvedValue({ hitl_request: approval({ status: "answered", approved: false }), ignored: false });
    await ready();
    const card = await screen.findByTestId("hitl-card");
    expect(within(card).getByTestId("hitl-reject")).toBeDisabled();
    fireEvent.change(within(card).getByTestId("hitl-reason-input"), { target: { value: "3장이 비었습니다" } });
    fireEvent.click(within(card).getByTestId("hitl-reject"));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/hitl-requests/{hitlRequestId}/response", expect.objectContaining({
      body: { approved: false, reason: "3장이 비었습니다" },
    })));
  });

  it("권한이 없으면 컨트롤이 없고 사유만 남는다(E7-11) — 카드는 누구나 본다", async () => {
    hitls = [approval({ can_respond: false, can_respond_from: null })];
    await ready();
    const card = await screen.findByTestId("hitl-card");
    expect(within(card).queryByTestId("hitl-approve")).toBeNull();
    expect(within(card).getByTestId("hitl-no-right").textContent).toContain("Director·deputy");
    expect(within(card).getByTestId("hitl-question")).toBeInTheDocument();
  });

  it("답한 뒤에는 결과(누가·언제·무엇)로 바뀐다", async () => {
    hitls = [approval({ status: "answered", approved: true, answered_by: "u1", answered_at: "2026-09-25T14:05:00Z" })];
    await ready();
    const card = await screen.findByTestId("hitl-card");
    expect(within(card).queryByTestId("hitl-approve")).toBeNull();
    expect(within(card).getByTestId("hitl-answer-what").textContent).toBe("승인");
    expect(within(card).getByTestId("hitl-answer-who").textContent).toContain("서연");
  });
});

describe("T-APPROVAL — 작업 중 보류 표시", () => {
  it("보류 중이면 미션 칸의 Director 승인 줄이 「조건 충족 — 진행 중인 작업이 끝나면 승인을 요청합니다」", async () => {
    heldWork = true;
    messages = [];
    search = new URLSearchParams("work=w1");
    await ready();
    const rows = await screen.findAllByTestId("condition-row");
    const held = rows.find((r) => r.getAttribute("data-type") === "user_approval")!;
    expect(held.getAttribute("data-held")).toBe("running_tasks");
    expect(within(held).getByTestId("condition-line").textContent).toBe("조건 충족 — 진행 중인 작업이 끝나면 승인을 요청합니다");
  });

  it("보류가 아니면 평소 문구 — 「받은 요청에서 승인하세요」", async () => {
    heldWork = false;
    messages = [];
    search = new URLSearchParams("work=w1");
    await ready();
    const rows = await screen.findAllByTestId("condition-row");
    const row = rows.find((r) => r.getAttribute("data-type") === "user_approval")!;
    expect(row.getAttribute("data-held")).toBeNull();
    expect(row.textContent).toContain("받은 요청에서 승인하세요");
  });
});
