/**
 * lib/cards — 작업 카드의 캐시·판·분담표 리듀서(React 없이). 방 화면이 `card.*` 와 `getCard` 를 이 함수들에 건다.
 * 회귀 주입(PR 표): cardsOnUpserted 의 판 밀기(versions push)를 빼면 (수정 요청) FAIL; actions 필터(actionFits)를 빼면 (방송 actions) FAIL;
 * 같은 참조 반환을 빼면 (같은 참조) FAIL; boardTree 의 부모 아래 끼우기를 빼면 (트리) FAIL; boardOnCard 의 work_id 가드를 빼면 (다른 미션) FAIL;
 * cardVersion 이 지난 판의 빈 칸을 현재 판에서 빌리면 (빌리지 않는다) FAIL(#397 B2); cardNeed 의 actions 분기를 빼면 (카드 먼저) FAIL(B1);
 * cardsOnFetched 의 older 가드를 빼면 (옛 응답) FAIL(NN2).
 */
import { describe, expect, it } from "vitest";
import { actionFits, boardChip, boardOnCard, boardTree, cardNeed, cardVersion, cardsOnFetched, cardsOnUpserted, goalExcerpt, judgeOf, metCount } from "./cards";
import type { CardBoard, CardBoardItem, TaskCard } from "./api/types";

const T0 = "2026-09-30T06:00:00Z";
export function card(over: Partial<TaskCard> = {}): TaskCard {
  return {
    id: "c3", room_id: "r1", work_id: "w1", number: 3, label: "C-3", version: 1, status: "in_progress",
    delegator: { agent_id: "a1", name: "Lead" }, assignee: { agent_id: "a2", name: "Developer" }, lane_id: "l3", parent_card_id: null,
    goal: "커브에서 차가 미끄러지는 감각을 실차처럼", criteria: [{ n: 1, text: "코너 진입", method: "test" }, { n: 2, text: "벽 충돌", method: "run" }],
    boundaries: "스프라이트(Designer)", refs: [], output_format: null, budget_usd: null, revise_reason: null, delegate_message_id: "m-d1",
    result: null, judgement: null, created_at: T0, updated_at: T0, actions: [], ...over,
  } as TaskCard;
}
const result = (over: Partial<NonNullable<TaskCard["result"]>> = {}): NonNullable<TaskCard["result"]> => ({
  summary: "했다", verdicts: [
    { criterion: 1, verdict: "met", stated_verdict: "met", evidence: [{ kind: "artifact", ref: "x", label: "log" }] },
    { criterion: 2, verdict: "partial", stated_verdict: "met", downgraded: true, evidence: [] },
  ], confirmed: ["봤다"], assumed: [], auto: false, submitted_at: T0, message_id: "m-r1", met_count: 1, ...over,
} as NonNullable<TaskCard["result"]>);

describe("cardVersion — 말풍선의 판", () => {
  it("현재 판이면 카드 칸 그대로 · 판을 모르면 현재 판 · 지난 판은 versions 에서 · 없으면 null", () => {
    const c = card({ version: 2, revise_reason: "빠짐", versions: [{ version: 1, goal: "옛 목표", result: result(), judgement: { action: "revise_requested", by: { kind: "agent", id: "a1", name: "Lead" }, at: T0, reason: "빠짐" } }] });
    expect(cardVersion(c, 2)!.current).toBe(true);
    expect(cardVersion(c, null)!.version).toBe(2);
    const v1 = cardVersion(c, 1)!;
    expect([v1.current, v1.goal, v1.status]).toEqual([false, "옛 목표", null]);
    expect(judgeOf(v1)).toBe("revise_requested");
    expect(cardVersion(card(), 5)).toBeNull();
  });
});

describe("지난 판은 빌리지 않는다 (#397 B2 — 계약 v0.3.10: versions 는 판마다 전체 모양)", () => {
  it("계약 모양의 1판은 그 판의 칸 그대로 — 참고 자료·결과물·예산·위임 말풍선까지", () => {
    const c = card({
      version: 2, refs: [{ kind: "message", id: "gone", label: "옛 초안", missing: true }], output_format: "새 결과물", budget_usd: 9, delegate_message_id: "del2",
      versions: [{ version: 1, goal: "커브만", criteria: [{ n: 1, text: "코너", method: "test" }], boundaries: "없음", refs: [{ kind: "artifact", id: "a1", label: "기획서", missing: false }],
        output_format: null, budget_usd: null, revise_reason: null, delegate_message_id: "del1", result: null, judgement: null }],
    });
    const v1 = cardVersion(c, 1)!;
    expect(v1.refs!.map((r) => r.id)).toEqual(["a1"]);
    expect([v1.output_format, v1.budget_usd, v1.delegate_message_id, v1.boundaries]).toEqual([null, null, "del1", "없음"]);
  });
  it("지난 판에 칸이 없으면(옛 서버) 없는 것이다 — 현재 판의 지워진 자료·결과물·예산·경계를 1판에 그리지 않는다", () => {
    const c = card({ version: 2, refs: [{ kind: "message", id: "gone", label: "옛 초안", missing: true }], output_format: "새 결과물", budget_usd: 9, versions: [{ version: 1, goal: "커브만" }] });
    const v1 = cardVersion(c, 1)!;
    expect([v1.refs, v1.output_format, v1.budget_usd, v1.boundaries, v1.delegate_message_id]).toEqual([null, null, null, null, null]);
    expect(v1.criteria).toEqual([]);
  });
});

describe("cardNeed — 말풍선이 무엇을 부탁하나 (#397 B1)", () => {
  const sub = () => card({ status: "result_submitted", result: { summary: "x", verdicts: [], confirmed: [], assumed: [], auto: false, submitted_at: T0, message_id: "m-r1" } as never });
  it("카드 이벤트가 말풍선보다 먼저 온 처음 보는 카드(actions 를 방송에서만) — 결과 제출·수락이면 actions 를 부탁한다", () => {
    const cache = cardsOnUpserted({}, sub());
    const c = cache.c3;
    expect(c.actions).toEqual([]);
    expect(cardNeed(c, cardVersion(c, 1), "result")).toBe("actions");
    const fetched = cardsOnFetched(cache, { ...sub(), actions: ["accept", "revise"] });
    expect(cardNeed(fetched.c3, cardVersion(fetched.c3, 1), "result")).toBeNull();
    // 방송이 뒤따라와도 믿은 표는 남는다(수락되면 「수락」만 빠지고 여전히 믿음).
    const acc = cardsOnUpserted(fetched, { ...sub(), status: "accepted", updated_at: "2026-09-30T07:00:00Z" });
    expect(acc.c3.actions).toEqual(["revise"]);
    expect(cardNeed(acc.c3, cardVersion(acc.c3, 1), "result")).toBeNull();
  });
  it("진행 중이거나 지난 판이면 actions 는 부탁하지 않는다 · 판 없음 → card · 결과 없음 → result", () => {
    const ip = cardsOnUpserted({}, card()).c3;
    expect(cardNeed(ip, cardVersion(ip, 1), "delegation")).toBeNull();
    expect(cardNeed(null, null, "delegation")).toBe("card");
    expect(cardNeed(ip, cardVersion(ip, 1), "result")).toBe("result");
  });
});

describe("cardsOnFetched — 늦게 도착한 옛 getCard 응답은 버린다 (#397 NN2)", () => {
  it("캐시가 더 새 판·같은 판의 더 늦은 updated_at 이면 그대로 · 같거나 새것이면 바꾼다", () => {
    const acc = cardsOnFetched({}, card({ status: "accepted", actions: ["revise"], updated_at: "2026-09-30T07:00:00Z" }));
    const stale = card({ status: "result_submitted", actions: ["accept", "revise"], updated_at: "2026-09-30T06:30:00Z" });
    expect(cardsOnFetched(acc, stale)).toBe(acc);
    expect(cardsOnFetched(acc, card({ version: 0 as never }))).toBe(acc);
    const newer = cardsOnFetched(acc, card({ status: "accepted", actions: ["revise"], updated_at: "2026-09-30T07:05:00Z" }));
    expect(newer).not.toBe(acc);
    expect(newer.c3.actions_trusted).toBe(true);
    // 옛 방송도 같은 규칙.
    expect(cardsOnUpserted(acc, stale)).toBe(acc);
  });
});

describe("cardsOnUpserted — card.* 로 캐시 한 곳", () => {
  it("판이 오르면 캐시의 현재 판을 지난 판으로 민다 — 1판 말풍선이 제 칸을 잃지 않는다", () => {
    const c1 = card({ status: "result_submitted", result: result(), actions: ["accept", "revise"] });
    const next = cardsOnUpserted({ c3: c1 }, card({ version: 2, goal: "새 목표", revise_reason: "대각선", updated_at: "2026-09-30T07:00:00Z" }));
    expect(next.c3.version).toBe(2);
    const v1 = cardVersion(next.c3, 1)!;
    expect(v1.goal).toBe(c1.goal);
    expect(v1.result!.summary).toBe("했다");
    expect(cardVersion(next.c3, 2)!.goal).toBe("새 목표");
  });
  it("방송의 actions 는 믿지 않는다 — 캐시의 동작 중 새 상태에 맞는 것만(수락되면 「수락」이 빠진다) · 처음 보면 빈 목록", () => {
    const cur = { c3: card({ status: "result_submitted", actions: ["accept", "revise"] }) };
    const acc = cardsOnUpserted(cur, card({ status: "accepted", actions: ["accept", "revise"], updated_at: "x" }));
    expect(acc.c3.actions).toEqual(["revise"]);
    expect(cardsOnUpserted({}, card({ actions: ["accept"] })).c3.actions).toEqual([]);
    expect(cardsOnUpserted({}, card({ status: "result_submitted", actions: ["accept", "revise"] }), { trustActions: true }).c3.actions).toEqual(["accept", "revise"]);
  });
  it("같은 판·같은 updated_at·같은 상태면 같은 참조 · getCard 는 통째로(versions 포함)", () => {
    const cur = { c3: card() };
    expect(cardsOnUpserted(cur, card())).toBe(cur);
    const fetched = cardsOnFetched(cur, card({ versions: [{ version: 0 }] }));
    expect(fetched.c3.versions).toHaveLength(1);
    expect(actionFits("in_progress", "accept")).toBe(false);
    expect(actionFits("accepted", "revise")).toBe(true);
  });
});

describe("분담표 — boardOnCard · boardTree · boardChip", () => {
  const item = (id: string, n: number, parent: string | null = null, status: CardBoardItem["status"] = "in_progress"): CardBoardItem => ({
    id, label: `C-${n}`, number: n, version: 1, parent_card_id: parent, assignee: { agent_id: "a", name: "Dev" }, goal: `g${n}`, status,
    met: null, total_criteria: 2, cost_usd: null, lane_id: `l${n}`,
  });
  it("트리 — 번호순, 하위 카드는 부모 바로 아래(깊이 1), 부모가 없으면 맨 위", () => {
    const t = boardTree([item("c4", 4, "c3"), item("c1", 1), item("c3", 3), item("c5", 5, "gone"), item("c2", 2)]);
    expect(t.map((x) => [x.item.label, x.depth])).toEqual([["C-1", 0], ["C-2", 0], ["C-3", 0], ["C-4", 1], ["C-5", 0]]);
  });
  it("card.* 로 행이 제자리 갱신 · 머리 수 다시 셈 · 새 카드는 번호 자리에 · 다른 미션은 그대로", () => {
    const b: CardBoard = { work_id: "w1", total: 2, pending_judgement: 0, items: [item("c1", 1), item("c3", 3)] };
    const up = boardOnCard(b, card({ status: "result_submitted", result: result({ cost_usd: 1.2 }) }))!;
    expect(up.items.find((x) => x.id === "c3")).toMatchObject({ status: "result_submitted", met: 1, cost_usd: 1.2, latest_message_id: "m-r1" });
    expect(up.pending_judgement).toBe(1);
    const add = boardOnCard(up, card({ id: "c2", number: 2, label: "C-2" }))!;
    expect(add.items.map((x) => x.label)).toEqual(["C-1", "C-2", "C-3"]);
    expect(add.total).toBe(3);
    expect(boardOnCard(b, card({ work_id: "w2" }))).toBe(b);
    expect(boardOnCard(null, card())).toBeNull(); // 읽기 전·실패면 한 장짜리 표를 만들지 않는다(#397 NN6)
  });
  it("상태 칩 — 결과 제출은 「판정 대기」(card_judge pending) · 나머지는 카드 상태", () => {
    expect(boardChip("result_submitted")).toEqual({ kind: "card_judge", value: "pending" });
    expect(boardChip("accepted")).toEqual({ kind: "card", value: "accepted" });
  });
  it("metCount · goalExcerpt", () => {
    expect(metCount(result({ met_count: undefined as never }))).toBe(1);
    expect(goalExcerpt("커브에서 차가 미끄러지는 감각을 실차처럼 만들기")).toBe("커브에서 차가 미끄러지는 감각을 실차…");
  });
});
