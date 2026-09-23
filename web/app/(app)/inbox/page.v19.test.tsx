/**
 * S8 받은 요청 v0.19(T-R2-W4a, SCREEN §4.14) — 목 서버에 물려 잰다(fetch 다리 — 경로·본문·Problem 이 실제 길을 지난다).
 *
 * 못 박는 것:
 *   1. 새 타입 전부가 그려지고, 맥락 한 줄에 **방 이름**(0.2.9 `room.name`)이 줄지 않고 나온다 · 미션 밖 항목은 방 바로가기 하나만.
 *   2. `room_paused` — 「미션 N개와 대화 전부가 멈췄습니다」 + 「승인하면 … 한꺼번에 다시 돕니다 — 잔여 합계 $X」, 예산이면 새 상한을 받아 승인하면 방 멈춤이 풀린다.
 *   3. `isolation_confirm` — 승인(approval)은 「워크트리로 나눔 / 이대로 진행」, 저장소 고르기(choice)는 보기 중 하나. 답하면 방 격리가 바뀐다.
 *   4. 수신자 근거와 위임 줄 — 부방장 항목은 「HH:MM부터 답할 수 있습니다」로 비활성이 먼저.
 *   5. 필터 두 줄 — 칩(첫 줄)과 방·미션 선택 상자(둘째 줄).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));
const push = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ push, replace: vi.fn() }), usePathname: () => "/inbox" }));
vi.mock("next/link", () => ({ default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => <a href={href} {...rest}>{children}</a> }));

import InboxPage from "./page";
import { setup } from "@/lib/mock/room-dialogs-testkit";
import { store } from "@/lib/mock/store";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { INBOX_V19, ISOLATION_CARD, RECIPIENT_BASIS, ROOM_PAUSED_CARD } from "@/lib/inbox-v19";
import type { InboxItem, Room } from "@/lib/api/types";

let bridge: FetchBridge;
let roomId = "";
let wsId = "";
let as: (email: string) => Promise<void>;
beforeEach(async () => {
  ({ bridge, roomId, wsId, as } = await setup("결제팀 — 이름이 아주 긴 방이어도 줄이지 않는다"));
  push.mockReset();
});
afterEach(() => {
  cleanup();
  bridge.restore();
});

const card = (type: string) => screen.getAllByTestId("inbox-item").filter((e) => e.getAttribute("data-type") === type);

describe("S8 v0.19 — 방 층 항목", () => {
  it("새 타입이 다 그려지고, 맥락 한 줄의 방 이름은 줄지 않으며, 미션 밖 항목은 방 바로가기 하나만", async () => {
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: roomId });
    render(<InboxPage />);
    await screen.findAllByTestId("inbox-item");
    for (const t of ["room_paused", "isolation_confirm", "work_proposed", "room_invited", "workdir_quota", "lane_blocked", "hitl_request"]) {
      expect(card(t).length, t).toBeGreaterThan(0);
    }
    const paused = card("room_paused")[0];
    const room = within(paused).getByTestId("inbox-room");
    expect(room).toHaveTextContent("결제팀 — 이름이 아주 긴 방이어도 줄이지 않는다");
    expect(room.className).toBe("inbox-item__room"); // flex: none — 말줄임은 미션 제목(__work)의 몫
    expect(within(paused).getByTestId("inbox-open-room")).toHaveAttribute("href", `/rooms/${roomId}`);
    expect(within(paused).queryByTestId("inbox-open-work")).toBeNull();
    // 미션 밖 에이전트 질문 — 트리거 인용은 둘째 줄(첫 줄에 넣으면 방 이름이 밀린다).
    expect(within(card("lane_blocked")[0]).getByTestId("inbox-quote").textContent).toMatch(/^"경쟁사 수수료 정책/);
  });

  it("room_paused — 두 문장(수·잔여 합계는 getRoom 의 blocked_detail) · 예산이면 새 상한을 받아 승인하면 방 멈춤이 풀린다", async () => {
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: roomId, types: ["room_paused"] });
    render(<InboxPage />);
    const c = (await screen.findAllByTestId("inbox-item"))[0];
    expect(within(c).getByTestId("inbox-basis")).toHaveTextContent(RECIPIENT_BASIS.room_owner);
    await waitFor(() => expect(within(c).getByTestId("inbox-room-paused-stopped")).toHaveTextContent(ROOM_PAUSED_CARD.stopped(2)));
    expect(within(c).getByTestId("inbox-room-paused-resume")).toHaveTextContent(`${ROOM_PAUSED_CARD.resume(2)} — 열린 미션 잔여 예산 합계 $6.50`);
    fireEvent.change(within(c).getByTestId("inbox-room-budget"), { target: { value: "40" } });
    fireEvent.click(within(c).getByTestId("inbox-room-approve"));
    await waitFor(() => expect(screen.getByTestId("inbox-toast")).toHaveTextContent(INBOX_V19.room_resumed));
    const r = (await bridge.call<Room>("GET", `/rooms/${roomId}`)).body;
    expect(r.blocked_reason).toBeNull();
    expect(r.limits.budget_usd).toBe(40);
    expect(screen.queryAllByTestId("inbox-item").filter((e) => e.getAttribute("data-type") === "room_paused")).toHaveLength(0);
  });

  it("isolation_confirm(approval) — 버튼이 결과의 이름이다 · 「워크트리로 나눔」은 격리를 worktree 로", async () => {
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: roomId, types: ["isolation_confirm"] });
    render(<InboxPage />);
    await screen.findAllByTestId("inbox-item");
    const approval = card("isolation_confirm").find((e) => within(e).queryByTestId("inbox-isolation")?.getAttribute("data-kind") === "approval")!;
    expect(within(approval).getByTestId("inbox-isolation-held")).toHaveTextContent(ISOLATION_CARD.held);
    expect(within(approval).getByTestId("inbox-isolation-split")).toHaveTextContent(ISOLATION_CARD.split);
    expect(within(approval).getByTestId("inbox-isolation-keep")).toHaveTextContent(ISOLATION_CARD.keep);
    expect(within(approval).getByTestId("inbox-isolation-settings")).toHaveAttribute("href", `/rooms/${roomId}/settings`);
    fireEvent.click(within(approval).getByTestId("inbox-isolation-split"));
    await waitFor(() => expect(screen.getByTestId("inbox-toast")).toHaveTextContent(INBOX_V19.isolation_answered));
    expect(store().rooms.get(roomId)!.isolation?.kind).toBe("worktree");
  });

  it("isolation_confirm(choice) — 저장소는 보기 중 하나(getHitlRequest 의 options, Lead Q6)", async () => {
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: roomId, types: ["isolation_confirm"] });
    render(<InboxPage />);
    await screen.findAllByTestId("inbox-item");
    const choice = card("isolation_confirm").find((e) => within(e).queryByTestId("inbox-isolation")?.getAttribute("data-kind") === "choice")!;
    const sel = await within(choice).findByTestId("inbox-isolation-repo");
    await waitFor(() => expect([...(sel as HTMLSelectElement).options].map((o) => o.value)).toEqual(["~/src/payments", "~/src/payments-web"]));
    fireEvent.change(sel, { target: { value: "~/src/payments-web" } });
    fireEvent.click(within(choice).getByTestId("inbox-isolation-choose"));
    await waitFor(() => expect(store().rooms.get(roomId)!.isolation).toMatchObject({ kind: "worktree", repo_path: "~/src/payments-web" }));
  });

  it("부방장 항목 — 기한 절반 전에는 「HH:MM부터 답할 수 있습니다」로 비활성이 먼저", async () => {
    const r = store().rooms.get(roomId)!;
    const seoyeon = [...store().users.values()].find((u) => u.email === "seoyeon@colab.dev")!;
    // 방장은 서연, 나는 부방장.
    r.people.push({ user_id: seoyeon.id, role: "owner", joined_at: r.created_at });
    const me = r.people.find((p) => p.user_id === r.owner_user_id)!;
    me.role = "deputy";
    r.deputy_owner_user_id = r.owner_user_id;
    r.owner_user_id = seoyeon.id;
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: roomId, types: ["hitl_request"] });
    render(<InboxPage />);
    const c = (await screen.findAllByTestId("inbox-item"))[0];
    expect(within(c).getByTestId("inbox-basis")).toHaveTextContent(RECIPIENT_BASIS.room_deputy);
    await waitFor(() => expect(within(c).getByTestId("inbox-delegation").textContent).toMatch(/^\d\d:\d\d부터 답할 수 있습니다$/));
    expect(within(c).getByTestId("inbox-basis")).toHaveAttribute("data-locked", "true");
    expect(within(c).queryByTestId("hitl-answer")).toBeNull(); // 서버 actions 에 answer 가 없다 — 버튼을 지어내지 않는다
  });
});

describe("S8 v0.19 — 필터 두 줄", () => {
  it("칩은 첫 줄, 방·미션 선택 상자는 둘째 줄 — 방을 고르면 그 방 항목만", async () => {
    const other = (await bridge.call<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "인프라" })).body.id;
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: roomId, types: ["room_invited", "work_proposed"] });
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: other, types: ["room_invited"] });
    render(<InboxPage />);
    await screen.findAllByTestId("inbox-item");
    const scope = await screen.findByTestId("inbox-scope");
    const filters = screen.getByRole("navigation", { name: "인박스 필터" });
    expect(filters.compareDocumentPosition(scope) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.change(within(scope).getByTestId("inbox-filter-room"), { target: { value: other } });
    await waitFor(() => expect(screen.getAllByTestId("inbox-item")).toHaveLength(1));
    expect(screen.getByTestId("inbox-room")).toHaveTextContent("인프라");
  });

  it("뱃지는 action_required 만 — room_paused 는 action_required, work_proposed 는 attention(서버 inbox.Severity)", async () => {
    await bridge.call("POST", "/__mock/inbox/seed-v19", { room_id: roomId, types: ["room_paused", "work_proposed", "room_invited"] });
    const page = (await bridge.call<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${wsId}`)).body;
    const sev = Object.fromEntries(page.items.map((x) => [x.type, x.severity]));
    expect(sev).toMatchObject({ room_paused: "action_required", work_proposed: "attention", room_invited: "info" });
    const sum = (await bridge.call<{ action_required: number }>("GET", `/inbox/summary?workspace_id=${wsId}`)).body;
    expect(sum.action_required).toBe(1);
    void as;
  });
});
