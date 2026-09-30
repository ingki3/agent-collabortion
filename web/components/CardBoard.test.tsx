/**
 * 분담표(SCREEN §4.6 (가) 「분담표」 · COMPONENTS §9.13 Card Board) — 우열 미션 칸의 「개요 · 분담표」 탭.
 * 회귀 주입(PR 표): WorkPanel 의 `board.total > 0` 조건을 빼면 (카드 0 — 탭 줄 없음) FAIL; boardTree 를 빼고 items 를 그대로 그리면 (트리 └) FAIL;
 * PanelTabs 의 value 제어를 빼면 (고른 탭 유지) FAIL; boardChip 을 빼면 (판정 대기 칩) FAIL; 머리 aside 를 빼면 (카드 N · 판정 대기 N) FAIL;
 * 행 aria-label 을 「말풍선으로 가기」로 되돌리면 (행 이름) FAIL; 담당을 이니셜로 되돌리면 (두 줄 행) FAIL.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { WorkPanel } from "./WorkPanel";
import { CardBoard } from "./CardBoard";
import type { CardBoard as Board, CardBoardItem, Work } from "@/lib/api/types";

afterEach(cleanup);

const work = (id: string): Work => ({
  id, room_id: "r1", title: `미션 ${id}`, goal: "마리오 카트", acceptance_criteria: [], director_user_id: "u1", deputy_user_id: null, assignee_agent_id: null,
  completion_condition: { type: "user_approval" }, completion_progress: { met: 0, total: 1, satisfied: false, human_gate: true, conditions: [] },
  limits: { budget_usd: null }, autonomy: "guided", status: "active", paused_reason: null, cost_usd: 0, cost_estimated: false, my_work_role: "director",
  director: { id: "u1", email: "", display_name: "형주", avatar_url: null, created_at: "" }, created_by: "u1", created_at: "", updated_at: "",
} as unknown as Work);
const item = (n: number, over: Partial<CardBoardItem> = {}): CardBoardItem => ({
  id: `c${n}`, label: `C-${n}`, number: n, version: 1, parent_card_id: null, assignee: { agent_id: "a", name: ["Researcher", "Designer", "Developer", "Developer", "Writer"][n - 1] },
  goal: ["경쟁작 조사", "차량 스프라이트 24방향", "커브 주행 감각", "대각선 벽 판정", "플레이 가이드"][n - 1], status: "in_progress", met: null, total_criteria: 3,
  cost_usd: null, lane_id: `l${n}`, latest_message_id: `m${n}`, ...over,
});
const BOARD: Board = {
  work_id: "w1", total: 5, pending_judgement: 1,
  items: [
    item(1, { status: "accepted", met: 3, cost_usd: 0.8 }), item(2, { cost_usd: 1.1 }), item(3, { status: "result_submitted", met: 1, cost_usd: 1.2 }),
    item(4, { parent_card_id: "c3", cost_usd: 0.3, total_criteria: 1 }), item(5, { status: "cancelled" }),
  ],
};

describe("WorkPanel 탭 — 카드 0 이면 탭 줄 없음(지금 화면 그대로), 있으면 「개요 · 분담표」", () => {
  it("카드 0(board 없음 · total 0 · 다른 미션의 표) — tablist 없음, 칸 맨 바깥이 옛 section 그대로", () => {
    for (const board of [null, { ...BOARD, total: 0, pending_judgement: 0, items: [] }, { ...BOARD, work_id: "w2" }]) {
      const { container } = render(<WorkPanel mode={{ kind: "picked", workId: "w1" }} work={work("w1")} board={board} />);
      expect(screen.queryByRole("tablist")).toBeNull();
      expect(container.firstElementChild).toBe(screen.getByTestId("work-panel"));
      cleanup();
    }
  });
  it("카드가 있으면 탭 둘 · 머리 「카드 5 · 판정 대기 1」 · 개요가 먼저", () => {
    render(<WorkPanel mode={{ kind: "picked", workId: "w1" }} work={work("w1")} board={BOARD} />);
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["개요", "분담표"]);
    expect(screen.getByTestId("card-board-head")).toHaveTextContent("카드 5 · 판정 대기 1");
    expect(screen.getByRole("tabpanel")).toContainElement(screen.getByTestId("work-panel"));
    fireEvent.click(screen.getByTestId("work-panel-tab-board"));
    expect(screen.getByTestId("card-board")).toBeInTheDocument();
  });
  it("고른 탭은 부른 쪽(방 화면) 상태 — 미션을 바꿔도 분담표 그대로, 카드 없는 미션으로 가면 개요, 돌아오면 다시 분담표 (#392 NN3)", () => {
    function Host() {
      const [tab, setTab] = useState("overview");
      const [w, setW] = useState("w1");
      const board = w === "w3" ? null : { ...BOARD, work_id: w };
      return (
        <>
          <button type="button" onClick={() => setW("w2")} data-testid="go-w2">w2</button>
          <button type="button" onClick={() => setW("w3")} data-testid="go-w3">w3</button>
          <button type="button" onClick={() => setW("w1")} data-testid="go-w1">w1</button>
          <WorkPanel mode={{ kind: "picked", workId: w }} work={work(w)} board={board} tab={tab} onTab={setTab} />
        </>
      );
    }
    render(<Host />);
    fireEvent.click(screen.getByTestId("work-panel-tab-board"));
    fireEvent.click(screen.getByTestId("go-w2"));
    expect(screen.getByTestId("work-panel-tab-board")).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("card-board")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("go-w3"));
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(screen.getByTestId("work-panel")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("go-w1"));
    expect(screen.getByTestId("card-board")).toBeInTheDocument();
  });
});

describe("CardBoard — 행 · 트리 · 누르기", () => {
  it("두 줄 행 — 1줄 「C-n · 목표」, 2줄 「@담당 · 상태 칩 · k/N · 비용」(이니셜 칩 대신 이름) · 하위 카드는 └ 들여쓰기", () => {
    const onOpen = vi.fn();
    render(<CardBoard board={BOARD} onOpen={onOpen} />);
    const rows = screen.getAllByTestId("card-board-row");
    expect(rows.map((r) => r.getAttribute("data-card-id"))).toEqual(["c1", "c2", "c3", "c4", "c5"]);
    expect(rows.map((r) => r.getAttribute("data-depth"))).toEqual(["0", "0", "0", "1", "0"]);
    expect(rows[3]).toHaveTextContent("└");
    expect(rows[3].querySelector(".cboard__tree")).not.toBeNull();
    expect(rows[0].querySelector(".cboard__tree")).toBeNull();
    expect(rows.map((r) => within(r).getByRole("img").getAttribute("aria-label"))).toEqual(["수락", "진행 중", "판정 대기", "진행 중", "취소"]);
    expect(rows.map((r) => within(r).getByTestId("card-board-met").textContent)).toEqual(["3/3", "–", "1/3", "–", "–"]);
    expect(rows.map((r) => within(r).getByTestId("card-board-cost").textContent)).toEqual(["$0.80", "$1.10", "$1.20", "$0.30", "–"]);
    expect(rows[1]).toHaveTextContent("차량 스프라이트 24방향");
    expect(rows.map((r) => within(r).getByTestId("card-board-who").textContent)).toEqual(["@Researcher", "@Designer", "@Developer", "@Developer", "@Writer"]);
    for (const r of rows) {
      const l1 = r.querySelector(".cboard__line1")!, l2 = r.querySelector(".cboard__line2")!;
      expect(l1).toContainElement(within(r).getByTestId("card-board-goal"));
      expect(l2).toContainElement(within(r).getByRole("img"));
      expect(l2).toContainElement(within(r).getByTestId("card-board-met"));
      expect(l2).toContainElement(within(r).getByTestId("card-board-cost"));
    }
    fireEvent.click(rows[2]);
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: "c3", latest_message_id: "m3" }));
    // 행 이름은 내용(번호 · 목표 · 담당 · 상태 · k/N) — 「말풍선으로 가기」가 덮지 않는다(#397 NN6).
    expect(rows[2]).toHaveAccessibleName("C-3 · 커브 주행 감각 · @Developer · 판정 대기 · 기준 1/3");
    expect(rows[1]).toHaveAccessibleName("C-2 · 차량 스프라이트 24방향 · @Designer · 진행 중 · 기준 –");
    expect(rows[2]).toHaveAttribute("title", "C-3 말풍선으로 가기");
  });
  it("읽는 중이면 한 줄", () => {
    render(<CardBoard board={null} onOpen={() => undefined} />);
    expect(screen.getByTestId("card-board-loading")).toBeInTheDocument();
  });
});
