/**
 * S7 방 화면 — T-R2-W4b 연결을 목 서버에 물려 잰다(화면 → api → 목, 경로·본문까지):
 *  - 좁은 화면 탭은 탭 패턴(role=tablist/tab + aria-selected, ←→ 이동) — #305 NN3.
 *  - 「여기까지 정리」 직접 고르기: 타임라인에서 시작·끝을 집으면 다이얼로그가 돌아오고 summarizeRoom 이 from/to 로 간다.
 *  - 미션 칸 「설정 편집」·「Director 교체」가 다이얼로그를 열고, 저장하면 우열이 응답 본문으로 바뀐다.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

const nav = { roomId: "", search: new URLSearchParams() };
vi.mock("next/navigation", () => ({
  useParams: () => ({ id: nav.roomId }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  useSearchParams: () => nav.search,
  usePathname: () => `/rooms/${nav.roomId}`,
}));
vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));

import RoomPage from "./page";
import { setup, uid } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { store } from "@/lib/mock/store";
import { SUMMARY_PICK } from "@/lib/wording";
import { WORK_EDIT } from "@/lib/work-edit";
import type { Message } from "@/lib/api/types";
import type { components } from "@/lib/api/schema";

type Work = components["schemas"]["Work"];
let bridge: FetchBridge;
beforeEach(async () => {
  Element.prototype.scrollIntoView = vi.fn();
  const r = await setup();
  bridge = r.bridge;
  nav.roomId = r.roomId;
  nav.search = new URLSearchParams();
});
afterEach(() => {
  cleanup();
  bridge.restore();
});
/** 메시지 시드(목 전용 경로 — 멱등 키 없이, created_at 단조 증가) → 넣은 순서대로의 메시지. */
async function seed(...contents: string[]): Promise<Message[]> {
  await bridge.call("POST", `/__mock/rooms/${nav.roomId}/seed`, { messages: contents.map((content) => ({ content, work: null })) });
  const all = [...store().messages.values()].filter((m) => m.session_id === nav.roomId);
  return contents.map((c) => all.find((m) => m.content === c)!);
}
const fetchCalls = (method: string, path: RegExp) =>
  (globalThis.fetch as unknown as { mock: { calls: [string, RequestInit | undefined][] } }).mock.calls.filter(([u, i]) => (i?.method ?? "GET") === method && path.test(String(u)));

describe("S7 — T-R2-W4b", () => {
  it("좁은 화면 탭 — tablist 안의 tab 넷, 선택은 aria-selected 하나, ←→·Home·End 로 옮긴다", async () => {
    render(<RoomPage />);
    // 넓은 화면(jsdom)에서는 탭 줄이 display:none 이다 — 접근성 트리 밖이라 hidden 으로 찾는다.
    const list = await screen.findByRole("tablist", { hidden: true });
    const tabs = within(list).getAllByRole("tab", { hidden: true });
    expect(tabs.map((t) => t.textContent)).toEqual(["타임라인", "보드", "미션", "방"]);
    expect(tabs.map((t) => t.getAttribute("aria-selected"))).toEqual(["true", "false", "false", "false"]);
    expect(tabs.map((t) => t.tabIndex)).toEqual([0, -1, -1, -1]);
    expect(tabs.some((t) => t.hasAttribute("aria-pressed"))).toBe(false);
    fireEvent.keyDown(tabs[0], { key: "ArrowRight" });
    expect(screen.getByTestId("tab-board")).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("room-detail")).toHaveAttribute("data-col", "board");
    fireEvent.keyDown(screen.getByTestId("tab-board"), { key: "ArrowLeft" });
    fireEvent.keyDown(screen.getByTestId("tab-timeline"), { key: "ArrowLeft" });
    expect(screen.getByTestId("tab-room")).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(screen.getByTestId("tab-room"), { key: "Home" });
    expect(screen.getByTestId("tab-timeline")).toHaveAttribute("aria-selected", "true");
  });

  it("여기까지 정리 · 직접 고르기 — 끝을 먼저 집어도 시각 순서로 맞춰 from/to 로 보내고 요약 메시지가 남는다", async () => {
    const [a, b, c] = await seed("초안 방향", "점심 뭐 먹지", "수수료 표");
    render(<RoomPage />);
    await screen.findByText("수수료 표");
    fireEvent.click(screen.getByTestId("room-more"));
    fireEvent.click(screen.getByTestId("room-menu-summarize"));
    fireEvent.click(screen.getByTestId("summarize-pick"));
    fireEvent.click(screen.getByTestId("summarize-pick-start"));
    // 집기 모드 — 안내 줄 + 메시지마다 단추. 끝(c)을 먼저, 시작(a)을 나중에.
    expect(screen.getByTestId("pick-bar")).toHaveTextContent(SUMMARY_PICK.bar_from);
    const btnOf = (id: string) => within(document.querySelector(`[data-message-id="${id}"]`)!.parentElement!).getByTestId("pick-message");
    fireEvent.click(btnOf(c.id));
    expect(screen.getByTestId("pick-bar")).toHaveTextContent(SUMMARY_PICK.bar_to);
    expect(btnOf(a.id)).toHaveTextContent(SUMMARY_PICK.here_to);
    fireEvent.click(btnOf(a.id));
    expect(screen.queryByTestId("pick-bar")).toBeNull();
    expect(screen.getByTestId("summarize-picked-from")).toHaveTextContent("초안 방향");
    expect(screen.getByTestId("summarize-picked-to")).toHaveTextContent("수수료 표");
    expect(screen.getByTestId("summarize-preview").querySelector("[data-slot]")).toHaveTextContent("3");
    fireEvent.click(screen.getByTestId("summarize-dialog-confirm"));
    await waitFor(() => expect(screen.queryByTestId("summarize-dialog")).toBeNull());
    const sent = fetchCalls("POST", /\/summaries$/).at(-1)!;
    expect(JSON.parse(String(sent[1]!.body))).toEqual({ from_message_id: a.id, to_message_id: c.id });
    expect([...store().messages.values()].some((m) => m.session_id === nav.roomId && m.kind === "summary")).toBe(true);
    expect(b.id).toBeTruthy();
  });

  it("집기를 그만두면 다이얼로그로 돌아간다(범위는 비어 「정리」 비활성)", async () => {
    await seed("하나");
    render(<RoomPage />);
    await screen.findByText("하나");
    fireEvent.click(screen.getByTestId("room-more"));
    fireEvent.click(screen.getByTestId("room-menu-summarize"));
    fireEvent.click(screen.getByTestId("summarize-pick"));
    fireEvent.click(screen.getByTestId("summarize-pick-start"));
    fireEvent.click(screen.getByTestId("pick-cancel"));
    expect(screen.getByTestId("summarize-dialog")).toBeInTheDocument();
    expect(screen.getByTestId("summarize-dialog-confirm")).toBeDisabled();
  });

  it("미션 칸 — 「설정 편집」은 S21 편집 모드, 「Director 교체」는 교체 다이얼로그 · 저장하면 우열이 바뀐다", async () => {
    const w = (await bridge.call<Work>("POST", `/rooms/${nav.roomId}/works`, { goal: "결제 실패율 보고서" })).body;
    nav.search = new URLSearchParams({ work: w.id });
    render(<RoomPage />);
    await waitFor(() => expect(screen.getByTestId("work-goal")).toHaveTextContent("결제 실패율 보고서"));
    fireEvent.click(screen.getByTestId("work-action-edit"));
    expect(screen.getByTestId("rd-edit-work-title")).toHaveTextContent(WORK_EDIT.title);
    fireEvent.change(screen.getByTestId("rd-create-work-goal"), { target: { value: "결제 실패율 보고서 v2" } });
    fireEvent.click(screen.getByTestId("rd-create-work-open"));
    await waitFor(() => expect(screen.getByTestId("work-goal")).toHaveTextContent("결제 실패율 보고서 v2"));
    expect(screen.queryByTestId("rd-edit-work")).toBeNull();

    fireEvent.click(screen.getByTestId("work-action-director"));
    fireEvent.change(await screen.findByTestId("change-director-to"), { target: { value: uid("seoyeon@colab.dev") } });
    fireEvent.click(screen.getByTestId("change-director-save"));
    await waitFor(() => expect(screen.getByTestId("work-director")).toHaveTextContent(store().users.get(uid("seoyeon@colab.dev"))!.display_name));
    // 이제 내가 Director 가 아니다 — 편집 동작이 꺼지고 사유가 선다.
    expect(screen.getByTestId("work-action-edit")).toBeDisabled();
  });
});
