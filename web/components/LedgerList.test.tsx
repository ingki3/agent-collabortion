/**
 * 미션 상태 원장 「원장」 탭(PRD FR-4.6 6 — 읽기 전용 목록) — WorkPanel 탭 규칙과 LedgerList 의 그림.
 * 회귀 주입(PR 표): WorkPanel 의 `ledger.length > 0` 조건을 빼면 (항목 0 — 탭 없음) FAIL; work_id 거르개를 빼면 (다른 미션) FAIL;
 * ledgerGroups 의 순서를 받은 순서로 바꾸면 (묶음 순서) FAIL; dim 클래스를 빼면 (대체·철회 흐림) FAIL; 「새 판 보기」의 setHl 을 빼면 (강조) FAIL;
 * promoted 판정을 빼면 (아직 한 번) FAIL.
 */
import "@testing-library/jest-dom/vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { WorkPanel } from "./WorkPanel";
import { LedgerList } from "./LedgerList";
import { MEMORY_LEDGER } from "@/lib/wording";
import type { MemoryItem, Work } from "@/lib/api/types";

afterEach(cleanup);

const work = (id: string): Work => ({
  id, room_id: "r1", title: `미션 ${id}`, goal: "마리오 카트", acceptance_criteria: [], director_user_id: "u1", deputy_user_id: null, assignee_agent_id: null,
  completion_condition: { type: "user_approval" }, completion_progress: { met: 0, total: 1, satisfied: false, human_gate: true, conditions: [] },
  limits: { budget_usd: null }, autonomy: "guided", status: "active", paused_reason: null, cost_usd: 0, cost_estimated: false, my_work_role: "director",
  director: { id: "u1", email: "", display_name: "형주", avatar_url: null, created_at: "" }, created_by: "u1", created_at: "", updated_at: "",
} as unknown as Work);

let n = 0;
const mi = (over: Partial<MemoryItem>): MemoryItem => ({
  id: `m${++n}`, work_id: "w1", kind: "fact", content: `내용 ${n}`, certainty: null, outcome: null, support_count: 0, promoted: true, status: "active",
  supersedes: null, superseded_by: null, invalidated_at: null, source_message_ids: [], created_by: { kind: "agent", id: "a1", name: "Lead" },
  created_at: new Date(Date.now() - 5 * 60000).toISOString(), ...over,
});

// 서버 순서(오래된 것부터) — 종류가 섞여 온다. 묶음은 계획·진행·사실·담당·열린 질문·교훈 순으로 다시 선다.
const LEDGER: MemoryItem[] = [
  mi({ id: "f-old", kind: "fact", content: "타일 크기는 16px 이다", certainty: "to_verify", status: "superseded", superseded_by: "f-new", invalidated_at: "2026-10-05T00:00:00Z" }),
  mi({ id: "les1", kind: "lesson", content: "커브 판정을 타일 경계로 하면 끼인다", outcome: "dead_end", support_count: 2, promoted: true, created_by: { kind: "agent", id: "a3", name: "Designer" } }),
  mi({ id: "asg", kind: "assignment", content: "스프라이트는 Designer" }),
  mi({ id: "pl", kind: "plan", content: "조사와 스프라이트를 나란히" }),
  mi({ id: "f-new", kind: "fact", content: "타일 크기는 32px 이다", certainty: "given", supersedes: "f-old", created_by: { kind: "user", id: "u1", name: "형주" } }),
  mi({ id: "f-guess", kind: "fact", content: "Mode 7 같다", certainty: "guess" }),
  mi({ id: "f-ret", kind: "fact", content: "배경 음악은 직접 작곡", certainty: "given", status: "retired", invalidated_at: "2026-10-05T00:00:00Z" }),
  mi({ id: "oq", kind: "open_question", content: "미니 터보를 넣을까?" }),
  mi({ id: "les2", kind: "lesson", content: "시트를 합치면 빨라진다", outcome: "useful", support_count: 1, promoted: false }),
  mi({ id: "pg", kind: "progress", content: "C-1 수락 · C-3 판정 대기" }),
];

describe("WorkPanel — 「원장」 탭은 그 미션에 원장 항목이 하나라도 있을 때만", () => {
  it("항목 0(null · 빈 목록 · 다른 미션의 목록) — 탭 줄 없음, 칸이 옛 section 그대로", () => {
    for (const ledger of [null, [], [mi({ work_id: "w2" })]]) {
      const { container } = render(<WorkPanel mode={{ kind: "picked", workId: "w1" }} work={work("w1")} ledger={ledger} />);
      expect(screen.queryByRole("tablist")).toBeNull();
      expect(screen.queryByTestId("work-panel-tab-ledger")).toBeNull();
      expect(container.firstElementChild).toBe(screen.getByTestId("work-panel"));
      cleanup();
    }
  });
  it("항목이 있으면 「개요 · 원장」(카드가 없어도) — 원장 탭을 누르면 목록, 쓰기 폼·입력이 없다", () => {
    render(<WorkPanel mode={{ kind: "picked", workId: "w1" }} work={work("w1")} ledger={[mi({})]} />);
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["개요", "원장"]);
    fireEvent.click(screen.getByTestId("work-panel-tab-ledger"));
    const panel = screen.getByRole("tabpanel");
    expect(within(panel).getByTestId("ledger")).toBeInTheDocument();
    expect(panel.querySelector("form, input, textarea, select")).toBeNull();
  });
  it("카드도 있으면 「개요 · 분담표 · 원장」 순", () => {
    const board = { work_id: "w1", total: 1, pending_judgement: 0, items: [{ id: "c1", label: "C-1", number: 1, version: 1, parent_card_id: null, assignee: { agent_id: "a", name: "Researcher" }, goal: "조사", status: "in_progress", met: null, total_criteria: 1, cost_usd: null, lane_id: "l1", latest_message_id: "m1" }] } as never;
    render(<WorkPanel mode={{ kind: "picked", workId: "w1" }} work={work("w1")} board={board} ledger={LEDGER} />);
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["개요", "분담표", "원장"]);
  });
});

describe("LedgerList — 종류별 묶음 · 라벨 · 대체·철회는 흐리게 · 「새 판 보기」", () => {
  it("묶음 순서: 계획 · 진행 · 사실 · 담당 · 열린 질문 · 교훈(묶음 안은 받은 순서)", () => {
    render(<LedgerList items={LEDGER} />);
    const groups = screen.getAllByTestId("ledger-group");
    expect(groups.map((g) => g.dataset.kind)).toEqual(["plan", "progress", "fact", "assignment", "open_question", "lesson"]);
    expect(groups.map((g) => g.querySelector("h3")!.firstChild!.textContent!.trim())).toEqual(["계획", "진행", "사실", "담당", "열린 질문", "교훈"]);
    const facts = within(groups[2]).getAllByTestId("ledger-item");
    expect(facts.map((x) => x.dataset.memoryId)).toEqual(["f-old", "f-new", "f-guess", "f-ret"]);
  });
  it("사실은 확실도 라벨, 교훈은 결과 · N번 겪음 · (support<2 이면) 아직 한 번, 작성자와 시각", () => {
    render(<LedgerList items={LEDGER} />);
    const item = (id: string) => document.querySelector<HTMLElement>(`[data-memory-id="${id}"]`)!;
    expect(within(item("f-old")).getByTestId("ledger-certainty")).toHaveTextContent("확인 필요");
    expect(within(item("f-new")).getByTestId("ledger-certainty")).toHaveTextContent("받은 값");
    expect(within(item("f-guess")).getByTestId("ledger-certainty")).toHaveTextContent("추측");
    expect(within(item("les1")).getByTestId("ledger-outcome")).toHaveTextContent("안 됨");
    expect(within(item("les1")).getByTestId("ledger-support")).toHaveTextContent("2번 겪음");
    expect(within(item("les1")).queryByTestId("ledger-once")).toBeNull();
    expect(within(item("les2")).getByTestId("ledger-outcome")).toHaveTextContent("도움 됨");
    expect(within(item("les2")).getByTestId("ledger-once")).toHaveTextContent("아직 한 번");
    // 확실도·결과는 제 종류에만.
    expect(within(item("asg")).queryByTestId("ledger-certainty")).toBeNull();
    expect(within(item("asg")).queryByTestId("ledger-support")).toBeNull();
    expect(within(item("les1")).getByTestId("ledger-author")).toHaveTextContent("@Designer");
    expect(within(item("f-new")).getByTestId("ledger-author")).toHaveTextContent(/^형주$/);
    expect(item("pl").querySelector("time")).toHaveTextContent("5분 전");
  });
  it("대체된 항목·철회된 항목은 흐리게, 유효한 항목은 그대로 — 철회는 「철회됨」, 대체는 「새 판 보기」", () => {
    render(<LedgerList items={LEDGER} />);
    const item = (id: string) => document.querySelector<HTMLElement>(`[data-memory-id="${id}"]`)!;
    expect(item("f-old")).toHaveClass("ledger__item--dim");
    expect(item("f-ret")).toHaveClass("ledger__item--dim");
    expect(item("f-new")).not.toHaveClass("ledger__item--dim");
    expect(within(item("f-ret")).getByTestId("ledger-retired")).toHaveTextContent("철회됨");
    expect(within(item("f-ret")).queryByTestId("ledger-new-version")).toBeNull();
    expect(screen.getAllByTestId("ledger-new-version")).toHaveLength(1);
    expect(within(item("f-old")).getByTestId("ledger-new-version")).toHaveTextContent("새 판 보기");
  });
  it("「새 판 보기」는 superseded_by 항목을 강조·포커스할 뿐 — 버튼은 그것 하나(바꾸는 동작 없음)", () => {
    render(<LedgerList items={LEDGER} />);
    const item = (id: string) => document.querySelector<HTMLElement>(`[data-memory-id="${id}"]`)!;
    expect(item("f-new")).not.toHaveClass("ledger__item--hl");
    fireEvent.click(within(item("f-old")).getByTestId("ledger-new-version"));
    expect(item("f-new")).toHaveClass("ledger__item--hl");
    expect(item("f-new")).toHaveAttribute("data-highlight", "true");
    expect(document.activeElement).toBe(item("f-new"));
    expect(screen.getAllByRole("button")).toHaveLength(1);
  });
});

describe("MEMORY_LEDGER 표 — enum 은 계약과 1:1, 컴포넌트는 표를 그린다", () => {
  const src = (f: string) => readFileSync(join(__dirname, "..", f), "utf8");
  it("종류·확실도·결과 키가 schema.d.ts 의 enum 과 같다 · 묶음 순서는 종류 전부", () => {
    const schema = src("lib/api/schema.d.ts");
    const en = (name: string) => [...(new RegExp(`\\b${name}: ([^;]+);`).exec(schema)?.[1] ?? "").matchAll(/"([a-z_]+)"/g)].map((m) => m[1]).sort();
    expect(Object.keys(MEMORY_LEDGER.kind).sort()).toEqual(en("MemoryKind"));
    expect([...MEMORY_LEDGER.order].sort()).toEqual(en("MemoryKind"));
    expect(Object.keys(MEMORY_LEDGER.certainty).sort()).toEqual(en("MemoryCertainty"));
    expect(Object.keys(MEMORY_LEDGER.outcome).sort()).toEqual(en("MemoryOutcome"));
  });
  it("컴포넌트에 원장 문구 리터럴이 없다", () => {
    const code = src("components/LedgerList.tsx").replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
    expect(code).not.toMatch(/"(원장|새 판 보기|철회됨|아직 한 번|받은 값|확인 필요|추론|추측|안 됨|고침|도움 됨)"/);
  });
});
