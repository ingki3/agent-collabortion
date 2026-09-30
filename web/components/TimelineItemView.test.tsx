/**
 * TimelineItemView(T-RF2) — 방 타임라인 항목 하나. 종류 고르기(`timelineEntry`)와 표의 렌더러, 메시지 종류별 꼬리표·메뉴.
 * 방 화면 통합(page.*.test.tsx)이 실제 흐름을 지키고, 여기는 컴포넌트 혼자의 계약을 잰다.
 * 회귀 주입(PR 표): FOOTER.summary 를 지우면 (요약 꼬리표) FAIL; NO_MENU 에서 system 을 빼면 (메뉴 없음) FAIL;
 * showWorkLink 조건을 빼면 (미션 보기) FAIL; hitl 분기를 빼면 (확인 요청) FAIL; heldProps 를 빼면 (높이 유지) FAIL.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { TimelineItemView, timelineEntry, timelineItemKey, type TimelineCtx } from "./TimelineItemView";
import { timelineItems } from "@/lib/parts";
import { ROOM_CENTER, ROOM_HEAD } from "@/lib/wording";
import type { HitlRequest, Message } from "@/lib/api/types";

afterEach(cleanup);

const T0 = "2026-09-29T10:00:00Z";
function msg(id: string, extra: Partial<Message> = {}): Message {
  return {
    id, session_id: "r1", parent_id: null, source_task_id: null, lane_id: null, state: "posted", reply_count: 0, is_note: false,
    author_type: "user", author_id: "u1", author: { name: "형주", avatar_url: null }, kind: "text", content: `본문 ${id}`, mentions: [],
    speech: "chat", addressees: [], created_at: T0, edited_at: null, work_id: null, ...extra,
  } as Message;
}
function ctx(over: Partial<TimelineCtx> = {}): TimelineCtx {
  return {
    me: "u1", now: Date.parse(T0), replies: {}, held: {}, hitls: [], busy: false, roomBudget: { current: 20, spent: 3 }, archived: false, showWorkLink: true,
    pickButton: () => null,
    conversation: () => ({ speech: { kind: "chat", to: [] } as never, grouped: false, onJump: () => undefined }),
    layers: () => undefined,
    groupProcess: () => undefined,
    taskActivity: () => <div data-testid="task-activity" />,
    onLoadReplies: () => undefined,
    onReply: () => undefined,
    workLabel: () => undefined,
    workTitle: (id) => (id ? "지도 만들기" : ""),
    onToWork: () => undefined,
    onRespondHitl: async () => undefined,
    onOpenWork: () => undefined,
    userName: () => undefined,
    cards: {},
    needCard: () => undefined,
    onCardAction: async () => undefined,
    onJump: () => undefined,
    onJumpRef: () => undefined,
    ...over,
  };
}
const one = (m: Message) => timelineItems([m])[0];

describe("timelineEntry — 항목 → 그리기 종류", () => {
  it("부분 묶음은 group · 확인 요청은 hitl · 나머지는 message", () => {
    const parts = [0, 1].map((i) => msg(`p${i}`, { group_id: "g1", group_index: i, group_size: 2, author_type: "agent", author_id: "a1" }));
    const items = timelineItems([...parts, msg("h", { kind: "hitl" }), msg("s", { kind: "system" })]);
    expect(items.map((i) => timelineEntry(i).kind)).toEqual(["group", "hitl", "message"]);
    expect(items.map(timelineItemKey)).toEqual(["group:g1", "h", "s"]);
  });
});

describe("TimelineItemView", () => {
  it("일반 메시지 — 카드 · 「…」 메뉴 · 꼬리표 없음", () => {
    render(<TimelineItemView item={one(msg("m1"))} ctx={ctx()} />);
    expect(screen.getByTestId("message-card")).toBeInTheDocument();
    expect(screen.getByTestId("message-menu")).toBeInTheDocument();
    expect(screen.queryByTestId("summary-label")).toBeNull();
    expect(screen.queryByTestId("system-work-link")).toBeNull();
  });

  it("요약 — 미션 요약이면 그 미션 이름, 아니면 방 요약 꼬리표 · 메뉴 없음", () => {
    const { rerender } = render(<TimelineItemView item={one(msg("s1", { kind: "summary", author_type: "system", work_id: "w1" }))} ctx={ctx()} />);
    expect(screen.getByTestId("summary-label")).toHaveTextContent(ROOM_CENTER.summary_of("지도 만들기"));
    expect(screen.queryByTestId("message-menu")).toBeNull();
    rerender(<TimelineItemView item={one(msg("s2", { kind: "summary", author_type: "system" }))} ctx={ctx()} />);
    expect(screen.getByTestId("summary-label")).toHaveTextContent(ROOM_CENTER.summary_room);
  });

  it("시스템 줄 — 미션에 매인 줄이면 「미션 보기」(그 미션을 보는 중이면 없음) · 메뉴 없음", () => {
    const onOpenWork = vi.fn();
    const m = msg("y1", { kind: "system", author_type: "system", author_id: null, work_id: "w1" });
    const { rerender } = render(<TimelineItemView item={one(m)} ctx={ctx({ onOpenWork })} />);
    expect(screen.queryByTestId("message-menu")).toBeNull();
    fireEvent.click(screen.getByTestId("system-work-link"));
    expect(onOpenWork).toHaveBeenCalledWith("w1");
    rerender(<TimelineItemView item={one(m)} ctx={ctx({ showWorkLink: false })} />);
    expect(screen.queryByTestId("system-work-link")).toBeNull();
    rerender(<TimelineItemView item={one({ ...m, work_id: null })} ctx={ctx()} />);
    expect(screen.queryByTestId("system-work-link")).toBeNull();
  });

  it("시스템 줄 — 「…」 메뉴 없음(대화 배치가 아닌 옛 카드에서도 — 대화 배치의 시스템 줄은 MessageCard 가 메뉴 칸을 그리지 않아 가려진다)", () => {
    const m = msg("y2", { kind: "system", author_type: "system", author_id: null });
    render(<TimelineItemView item={one(m)} ctx={ctx({ conversation: (() => undefined) as unknown as TimelineCtx["conversation"] })} />);
    expect(screen.getByTestId("message-card")).toBeInTheDocument();
    expect(screen.queryByTestId("message-menu")).toBeNull();
  });

  it("「이걸 미션으로」 — 이미 미션에 속했거나 보관된 방이면 비활성 + 사유, 아니면 부른다", () => {
    const onToWork = vi.fn();
    render(<TimelineItemView item={one(msg("m1"))} ctx={ctx({ onToWork })} />);
    fireEvent.click(screen.getByTestId("message-menu"));
    fireEvent.click(screen.getByTestId("message-to-work"));
    expect(onToWork).toHaveBeenCalledTimes(1);
    cleanup();
    render(<TimelineItemView item={one(msg("m2", { work_id: "w1" }))} ctx={ctx({ onToWork })} />);
    fireEvent.click(screen.getByTestId("message-menu"));
    expect(screen.getByTestId("message-to-work")).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByTestId("message-menu-list")).toHaveTextContent(ROOM_CENTER.has_work("지도 만들기"));
    cleanup();
    render(<TimelineItemView item={one(msg("m3"))} ctx={ctx({ onToWork, archived: true })} />);
    fireEvent.click(screen.getByTestId("message-menu"));
    expect(screen.getByTestId("message-menu-list")).toHaveTextContent(ROOM_HEAD.archived);
    fireEvent.click(screen.getByTestId("message-to-work"));
    expect(onToWork).toHaveBeenCalledTimes(1);
  });

  it("확인 요청 — 짝이 있으면 HitlCard(방 예산), 없으면 불러오는 중 자리", () => {
    const m = msg("h1", { kind: "hitl", author_type: "system", hitl_request_id: "q1" } as Partial<Message>);
    const { rerender } = render(<TimelineItemView item={one(m)} ctx={ctx()} />);
    const row = screen.getByTestId("timeline-hitl");
    expect(row).toHaveAttribute("data-message-id", "h1");
    expect(within(row).getByTestId("hitl-card-loading")).toHaveTextContent("본문 h1");
    const h = {
      id: "q1", session_id: "r1", message_id: "h1", type: "question", source: "agent", purpose: null, status: "open", question: "어느 쪽?", context: null,
      proposed_default: null, can_respond: true, task_id: null, created_at: T0,
    } as unknown as HitlRequest;
    rerender(<TimelineItemView item={one(m)} ctx={ctx({ hitls: [h] })} />);
    expect(within(screen.getByTestId("timeline-hitl")).getByTestId("hitl-card")).toBeInTheDocument();
    expect(screen.queryByTestId("hitl-card-loading")).toBeNull();
  });

  it("부분 묶음 — 말풍선 하나 · 메뉴는 첫 부분 · 작업 과정은 경계(마지막) 부분으로 묻는다", () => {
    const parts = [0, 1, 2].map((i) => msg(`p${i}`, { group_id: "g1", group_index: i, group_size: 3, author_type: "agent", author_id: "a1", author: { name: "Lead", avatar_url: null } }));
    const groupProcess = vi.fn(() => <div data-testid="gp" />);
    render(<TimelineItemView item={timelineItems(parts)[0]} ctx={ctx({ groupProcess })} />);
    expect(screen.getAllByTestId("part-bubble")).toHaveLength(1);
    expect(screen.getAllByTestId("message-menu")).toHaveLength(1);
    expect(groupProcess).toHaveBeenCalledWith(parts[2]);
    expect(screen.getByTestId("gp")).toBeInTheDocument();
  });

  it("높이 유지 — held 에 id 가 있으면 첫 프레임 min-height 와 data-held", () => {
    const { container, rerender } = render(<TimelineItemView item={one(msg("m1"))} ctx={ctx({ held: { m1: 120 } })} />);
    expect(container.firstElementChild).toHaveAttribute("data-held", "true");
    expect((container.firstElementChild as HTMLElement).style.minHeight).toBe("120px");
    rerender(<TimelineItemView item={one(msg("m1"))} ctx={ctx()} />);
    expect(container.firstElementChild).not.toHaveAttribute("data-held");
  });

  it("고르기 단추 · 세 층이 아닌 에이전트 메시지의 활동 피드는 ctx 가 준다", () => {
    render(<TimelineItemView item={one(msg("a1", { author_type: "agent", author_id: "ag", source_task_id: "t1" }))} ctx={ctx({ pickButton: (m) => <button data-testid="pick-message">{m.id}</button> })} />);
    expect(screen.getByTestId("pick-message")).toHaveTextContent("a1");
  });
});
