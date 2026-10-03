/**
 * 작업 카드 말풍선 두 종류(SCREEN §4.6 「작업 카드」 · COMPONENTS §9.13 · Pencil S7-K) — `TimelineItemView` 표가 `card_role` 로 고르고,
 * 칸은 방 화면의 카드 캐시(ctx.cards)에서 메시지의 `card_id`·`card_version` 으로 채운다.
 *
 * 회귀 주입(PR 표): timelineEntry 의 card_role 분기를 빼면 (고르기) FAIL; 위임 카드의 경계 Row 를 빼면 (칸 전부) FAIL; RefChip 의 missing 분기를
 * 빼면 (지워진 자료) FAIL; downgraded 분기를 빼면 (근거 없음) FAIL; 가정함 Row 의 tcard__row--warn 을 빼면 (가정함 경고색) FAIL;
 * cardMenuItems 가 actions 를 안 보면 (권한별 메뉴) FAIL; auto 분기를 빼면 (자동) FAIL; needCard 부탁을 빼면 (캐시에 없음) FAIL;
 * 판 고르기(card_version)를 빼고 현재 판만 그리면 (1판 · 2판) FAIL; cardMenuItems 의 accepted 에서 actions 를 안 보면 (수락됨 · 권한 없음) FAIL(#397 NN1);
 * cardNeed 의 actions 분기를 빼면 (카드 먼저) FAIL(B1); cardVersion 이 빌리면 (지난 판 빈 칸) FAIL(B2); lane 칩 숨김을 빼면 (칩 하나) FAIL(NN5);
 * TimelineItemView 의 memo 를 빼면 (렌더 수) FAIL(NN4).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { TimelineItemView, sameTimelineItem, timelineEntry, type TimelineCtx } from "./TimelineItemView";
import { cardMenuItems } from "./TaskCardBubble";
import { timelineItems } from "@/lib/parts";
import { cardsOnFetched, cardsOnUpserted } from "@/lib/cards";
import { TASK_CARD } from "@/lib/wording";
import type { Message, TaskCard } from "@/lib/api/types";

afterEach(cleanup);

const T0 = "2026-09-30T06:24:00Z";
function msg(id: string, extra: Partial<Message> = {}): Message {
  return {
    id, session_id: "r1", parent_id: null, source_task_id: "t1", lane_id: null, state: "posted", reply_count: 0, is_note: false,
    author_type: "agent", author_id: "a1", author: { name: "Lead", avatar_url: null }, kind: "text", content: `@Developer 요약 ${id}`, mentions: [],
    speech: "delegate", addressees: [{ kind: "agent", id: "a2", name: "Developer" }], created_at: T0, edited_at: null, work_id: "w1", ...extra,
  } as Message;
}
const result = (over: Partial<NonNullable<TaskCard["result"]>> = {}): NonNullable<TaskCard["result"]> => ({
  summary: "횡가속도 상한을 곡률에 묶었습니다.",
  verdicts: [
    { criterion: 1, verdict: "met", stated_verdict: "met", evidence: [{ kind: "artifact", ref: "art-log", label: "test-corner.log" }] },
    { criterion: 2, verdict: "partial", stated_verdict: "partial", evidence: [], note: "대각선 벽은 확인 못 함" },
    { criterion: 3, verdict: "partial", stated_verdict: "met", downgraded: true, evidence: [] },
  ],
  confirmed: ["직선·곡선 벽 충돌 20회 재생"], assumed: ["기록 화면은 그대로일 것"], deviations: "복귀 0.4초", open_issues: "대각선 벽 판정",
  met_count: 1, auto: false, cost_usd: 1.2, duration_s: 14 * 60, message_id: "res1", submitted_at: T0, ...over,
} as NonNullable<TaskCard["result"]>);
function card(over: Partial<TaskCard> = {}): TaskCard {
  return {
    id: "c3", room_id: "r1", work_id: "w1", number: 3, label: "C-3", version: 1, status: "in_progress",
    delegator: { agent_id: "a1", name: "Lead" }, assignee: { agent_id: "a2", name: "Developer" }, lane_id: "l3", parent_card_id: null,
    goal: "커브에서 차가 미끄러지는 감각을 실차처럼",
    criteria: [{ n: 1, text: "코너 진입 속도가 곡률에 따라 줄어든다", method: "test" }, { n: 2, text: "벽 충돌 뒤 0.5초 안에 다시 가속된다", method: "run" }, { n: 3, text: "기존 레이스 기록 화면이 깨지지 않는다", method: "inspect" }],
    boundaries: "스프라이트·효과음 파일(Designer 담당)",
    refs: [{ kind: "artifact", id: "art1", label: "기획서 v3", missing: false }, { kind: "decision", id: "d1", label: "타일 32px", missing: false }, { kind: "message", id: "gone", label: "옛 초안", missing: true }],
    output_format: "코드 diff + 실행 영상", budget_usd: 3, revise_reason: null, delegate_message_id: "del1", result: null, judgement: null,
    created_at: T0, updated_at: T0, actions: [], ...over,
  } as TaskCard;
}
function ctx(over: Partial<TimelineCtx> = {}): TimelineCtx {
  return {
    me: "u1", now: Date.parse(T0), replies: {}, held: {}, hitls: [], busy: false, roomBudget: { current: null, spent: 0 }, archived: false, showWorkLink: true,
    pickButton: () => null,
    conversation: (m) => ({ speech: { kind: (m.speech ?? "chat") as never, to: (m.addressees ?? []).map((a) => ({ kind: a.kind as "agent", id: a.id, name: a.name })) }, grouped: false, onJump: () => undefined }),
    layers: () => undefined, groupProcess: () => undefined, taskActivity: () => null, onLoadReplies: () => undefined, onReply: () => undefined,
    workLabel: () => undefined, workTitle: () => "마리오 카트", onToWork: () => undefined, onRespondHitl: async () => undefined, onOpenWork: () => undefined, userName: () => undefined,
    cards: {}, needCard: () => undefined, onCardAction: async () => undefined, onJump: () => undefined, onJumpRef: () => undefined,
    ...over,
  };
}
const one = (m: Message) => timelineItems([m])[0];
const delegation = (over: Partial<Message> = {}) => msg("del1", { card_id: "c3", card_role: "delegation", card_version: 1, ...over });
const resultMsg = (over: Partial<Message> = {}) =>
  msg("res1", { author_id: "a2", author: { name: "Developer", avatar_url: null }, speech: "report", addressees: [{ kind: "agent", id: "a1", name: "Lead" }], responds_to_message_id: "del1", card_id: "c3", card_role: "result", card_version: 1, ...over });

describe("고르기 — card_role 로 렌더러를 고른다", () => {
  it("delegation → task_card · result → result_card · card_id 없으면 보통 메시지", () => {
    expect(timelineEntry(one(delegation())).kind).toBe("task_card");
    expect(timelineEntry(one(resultMsg())).kind).toBe("result_card");
    expect(timelineEntry(one(msg("x", { card_role: "delegation" }))).kind).toBe("message");
  });
});

describe("위임 카드 — 칸 전부", () => {
  it("머리(C-3 · 1판 · 진행 중) · 목표 · 완료 기준 ol + 확인 방법 칩 · 하지 않을 것(경계 막대) · 참고 칩 · 결과물 · 예산 · aria-label", () => {
    render(<TimelineItemView item={one(delegation())} ctx={ctx({ cards: { c3: card() } })} />);
    const art = screen.getByRole("article");
    expect(art).toHaveAttribute("aria-label", TASK_CARD.aria_delegation("Lead", "C-3", "@Developer", "진행 중"));
    const head = screen.getByTestId("card-head");
    expect(head).toHaveTextContent("C-3");
    expect(head).toHaveTextContent("1판");
    expect(within(head).getByRole("img", { name: "진행 중" })).toHaveAttribute("data-tone", "run");
    expect(screen.getByTestId("card-goal")).toHaveTextContent("목표커브에서 차가 미끄러지는 감각을 실차처럼");
    const crit = screen.getByTestId("card-criteria");
    expect(within(crit).getByRole("list").tagName).toBe("OL");
    expect(screen.getAllByTestId("card-method").map((x) => x.textContent)).toEqual(["테스트", "실행 결과", "눈으로 확인"]);
    expect(screen.getByTestId("card-boundaries").querySelector(".tcard__bound")).toHaveTextContent("스프라이트·효과음 파일(Designer 담당)");
    expect(screen.getByTestId("card-output")).toHaveTextContent("결과물코드 diff + 실행 영상 · 예산 $3");
    // 본문(서버의 요약)은 그리지 않는다 — 카드가 본문 자리다.
    expect(art).not.toHaveTextContent("요약 del1");
  });
  it("확인 방법 5종 라벨(「산출물」 대신 「아티팩트」 — §8.4) · 상태 4종 · 판정 3종", () => {
    expect(TASK_CARD.method).toEqual({ test: "테스트", artifact: "아티팩트", run: "실행 결과", review: "검토", inspect: "눈으로 확인" });
    expect(Object.keys(TASK_CARD.status).sort()).toEqual(["accepted", "cancelled", "in_progress", "result_submitted"]);
    expect(Object.keys(TASK_CARD.verdict).sort()).toEqual(["met", "partial", "unmet"]);
  });
  it("참고 칩 — 누르면 그 자리로(onJumpRef) · 지워진 것은 흐린 「지워진 자료」(누를 수 없음)", () => {
    const onJumpRef = vi.fn();
    render(<TimelineItemView item={one(delegation())} ctx={ctx({ cards: { c3: card() }, onJumpRef })} />);
    const chips = screen.getAllByTestId("card-ref");
    expect(chips.map((c) => c.getAttribute("data-ref-kind"))).toEqual(["artifact", "decision", "message"]);
    fireEvent.click(chips[1]);
    expect(onJumpRef).toHaveBeenCalledWith(expect.objectContaining({ kind: "decision", id: "d1" }));
    expect(chips[2]).toHaveAttribute("data-missing", "true");
    expect(chips[2]).toHaveTextContent(TASK_CARD.ref_missing);
    expect(chips[2].tagName).toBe("SPAN");
  });
  it("캐시에 없으면 needCard 를 한 번 부탁하고 서버 요약을 흐리게 — 칸을 짐작해 그리지 않는다", () => {
    const needCard = vi.fn();
    render(<TimelineItemView item={one(delegation())} ctx={ctx({ needCard })} />);
    expect(needCard).toHaveBeenCalledWith("c3", 1, "card");
    expect(screen.getByTestId("card-loading")).toHaveTextContent("요약 del1");
    expect(screen.queryByTestId("card-goal")).toBeNull();
  });
  it("2판 수정 요청 — 같은 카드 캐시 하나로 1판·2판 말풍선이 각자의 판을 그린다(1판은 지난 판 · 수정 요청 칩)", () => {
    const c = card({
      version: 2, revise_reason: "대각선 벽도", goal: "커브 + 대각선 벽",
      versions: [{ version: 1, goal: "커브만", result: result(), judgement: { action: "revise_requested", by: { kind: "agent", id: "a1", name: "Lead" }, at: T0, reason: "대각선 벽도" } }],
    });
    const ms = [delegation(), msg("del2", { card_id: "c3", card_role: "delegation", card_version: 2, created_at: "2026-09-30T07:00:00Z" })];
    render(<>{timelineItems(ms).map((it) => <TimelineItemView key={it.kind === "group" ? it.groupId : it.message.id} item={it} ctx={ctx({ cards: { c3: c } })} />)}</>);
    const [v1, v2] = screen.getAllByTestId("task-card");
    expect(v1).toHaveAttribute("data-card-version", "1");
    expect(within(v1).getByTestId("card-goal")).toHaveTextContent("커브만");
    expect(within(v2).getByTestId("card-goal")).toHaveTextContent("커브 + 대각선 벽");
    expect(within(v2).getByTestId("card-revise-reason")).toHaveTextContent("수정 요청 · 대각선 벽도");
    const heads = screen.getAllByTestId("card-head");
    expect(heads[0]).toHaveTextContent("1판");
    expect(within(heads[0]).getByRole("img", { name: "수정 요청" })).toBeInTheDocument();
    expect(heads[1]).toHaveTextContent("2판");
  });
});

describe("지난 판 말풍선은 그 판의 칸만 (#397 B2)", () => {
  it("1판 위임 말풍선 — 1판에 없는 참고·결과물·예산은 그리지 않는다(2판의 지워진 칩·결과물이 1판에 서지 않는다)", () => {
    const c = card({
      version: 2, output_format: "새 결과물", budget_usd: 9, refs: [{ kind: "message", id: "gone", label: "x", missing: true }],
      versions: [{ version: 1, goal: "커브만", criteria: [{ n: 1, text: "코너", method: "test" }], boundaries: "없음" }],
    });
    render(<TimelineItemView item={one(delegation())} ctx={ctx({ cards: { c3: c } })} />);
    expect(screen.getByTestId("card-goal")).toHaveTextContent("커브만");
    expect(screen.queryByTestId("card-refs")).toBeNull();
    expect(screen.queryByTestId("card-output")).toBeNull();
  });
});

describe("카드 말풍선의 상태 칩은 하나 (#397 NN5)", () => {
  it("대화 머리의 lane 위임 상태 칩을 숨기고 카드 칩만", () => {
    const lane = { id: "l3", status: "running" } as never;
    const conv: TimelineCtx["conversation"] = (m) => ({ speech: { kind: "delegate", to: [{ kind: "agent", id: "a2", name: "Developer" }], lane }, grouped: false, onJump: () => undefined }) as never;
    render(<TimelineItemView item={one(delegation())} ctx={ctx({ cards: { c3: card() }, conversation: conv })} />);
    expect(screen.getAllByRole("img", { name: "진행 중" })).toHaveLength(1);
    expect(screen.queryByRole("img", { name: /실행 중/ })).toBeNull();
    cleanup();
    // 보통 위임 말풍선(카드 없음)은 그대로 lane 칩을 그린다.
    render(<TimelineItemView item={one(msg("plain"))} ctx={ctx({ conversation: conv })} />);
    expect(screen.getByRole("img", { name: /실행 중/ })).toBeInTheDocument();
  });
});

describe("TimelineItemView 는 memo (#397 NN4)", () => {
  it("같은 ctx · 같은 메시지면 부모가 다시 그려도(새 항목 껍데기) 항목은 다시 안 그린다 · ctx 가 바뀌면 다시 그린다", () => {
    const m = delegation();
    let renders = 0;
    // 항목이 그려질 때마다 부르는 ctx 칸(층 나누기)으로 센다.
    const c1 = ctx({ cards: { c3: card() }, layers: () => { renders += 1; return undefined; } });
    const Host = ({ c, tick }: { c: TimelineCtx; tick: number }) => (
      <div data-tick={tick}>
        <TimelineItemView item={timelineItems([m])[0]} ctx={c} />
      </div>
    );
    const { rerender } = render(<Host c={c1} tick={0} />);
    const first = renders;
    expect(first).toBeGreaterThan(0);
    rerender(<Host c={c1} tick={1} />);
    rerender(<Host c={c1} tick={2} />);
    expect(renders).toBe(first);
    rerender(<Host c={{ ...c1 }} tick={3} />);
    expect(renders).toBeGreaterThan(first);
    // NN7 — ctx 가 같아도 알맹이(메시지 · 묶음의 부분)가 바뀌면 다시 그린다(sameTimelineItem 이 유일한 방어가 되는 경로).
    const before = renders;
    const HostM = ({ mm }: { mm: Message }) => <TimelineItemView item={timelineItems([mm])[0]} ctx={c1} />;
    rerender(<HostM mm={m} />);
    const mid = renders;
    rerender(<HostM mm={{ ...m }} />);
    expect(renders).toBeGreaterThan(mid);
    expect(sameTimelineItem({ kind: "group", groupId: "g", size: 2, parts: [m] }, { kind: "group", groupId: "g", size: 2, parts: [{ ...m }] })).toBe(false);
    expect(sameTimelineItem({ kind: "group", groupId: "g", size: 2, parts: [m] }, { kind: "group", groupId: "g", size: 2, parts: [m] })).toBe(true);
    expect(before).toBeGreaterThan(0);
    expect(renders).toBeGreaterThan(first);
  });
});

describe("결과 카드 — 칸 전부 · downgraded · 자동", () => {
  const submitted = () => card({ status: "result_submitted", result: result() });
  it("머리(C-3 결과 · 기준 1/3 충족 · 판정 대기) · ↩ C-3 「목표…」 · 요약 · 기준별 글리프 · 근거 링크 · 사유 · 확인함 · 가정함(경고색) · 벗어난 점 · 남은 문제 · 비용·시간", () => {
    const onJump = vi.fn();
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: submitted() }, onJump })} />);
    expect(screen.getByRole("article")).toHaveAttribute("aria-label", TASK_CARD.aria_result("Developer", "C-3", 1, 3));
    const head = screen.getByTestId("card-head");
    expect(head).toHaveTextContent("C-3 결과");
    expect(head).toHaveTextContent("기준 1/3 충족");
    expect(within(head).getByRole("img", { name: "판정 대기" })).toHaveAttribute("data-tone", "wait");
    fireEvent.click(screen.getByTestId("card-back"));
    expect(onJump).toHaveBeenCalledWith("del1");
    expect(screen.getByTestId("card-back")).toHaveTextContent("↩ C-3 「커브에서 차가 미끄러지는 감각을 실차…」");
    // 대화 배치의 「↩ … 에 대한 보고」는 겹쳐 그리지 않는다.
    expect(screen.queryByTestId("report-of")).toBeNull();
    expect(screen.getByTestId("card-summary")).toHaveTextContent("횡가속도 상한을 곡률에 묶었습니다.");
    const rows = screen.getAllByTestId("card-verdict");
    expect(rows.map((r) => r.querySelector(".tcard__glyph")!.textContent)).toEqual(["✓", "◐", "◐"]);
    expect(rows[0]).toHaveTextContent("코너 진입 속도가 곡률에 따라 줄어든다");
    expect(within(rows[0]).getByTestId("card-evidence")).toHaveAttribute("href", "/api/v1/artifacts/art-log/content");
    expect(rows[1]).toHaveTextContent("부분 — 대각선 벽은 확인 못 함");
    expect(screen.getByTestId("card-confirmed")).toHaveTextContent("직선·곡선 벽 충돌 20회 재생");
    expect(screen.getByTestId("card-assumed")).toHaveClass("tcard__row--warn");
    expect(screen.getByTestId("card-deviations")).toHaveTextContent("복귀 0.4초");
    expect(screen.getByTestId("card-open-issues")).toHaveTextContent("대각선 벽 판정");
    expect(screen.getByTestId("card-cost")).toHaveTextContent("$1.20 · 14분");
    expect(screen.queryByTestId("card-judgement")).toBeNull();
  });
  it("downgraded — 서버가 met 을 partial 로 낮춘 줄은 「부분 · 근거 없음」(에이전트 판정과 다름을 숨기지 않는다)", () => {
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: submitted() } })} />);
    const rows = screen.getAllByTestId("card-verdict");
    expect(rows[2]).toHaveAttribute("data-downgraded", "true");
    expect(within(rows[2]).getByTestId("card-downgraded")).toHaveTextContent("부분 · 근거 없음");
    expect(screen.getAllByTestId("card-downgraded")).toHaveLength(1);
  });
  it("수락 — 판정 칩 수락 · 판정 줄 「수락 · @Lead 15:58 — 〈코멘트〉」(v0.19.17)", () => {
    const c = card({ status: "accepted", result: result(), judgement: { action: "accepted", by: { kind: "agent", id: "a1", name: "Lead" }, at: "2026-09-30T06:58:00Z", reason: null, comment: "커브 테스트 로그와 영상을 확인했습니다" } });
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: c } })} />);
    expect(within(screen.getByTestId("card-head")).getByRole("img", { name: "수락" })).toHaveAttribute("data-tone", "done");
    // 시각은 lib/time clockTime 그대로(다른 줄과 같은 HH:MM:SS) — 코멘트는 「 — 」 뒤.
    expect(screen.getByTestId("card-judgement")).toHaveTextContent(/^✓수락 · @Lead \d\d:58(:\d\d)? — 커브 테스트 로그와 영상을 확인했습니다$/);
  });
  it("수정 요청 판정 줄 — 「수정 요청 · 형주 — 사유」 + 「새 판 보기」(새 판 위임 카드로)", () => {
    const onJump = vi.fn();
    const c = card({
      version: 2, delegate_message_id: "del2",
      versions: [{ version: 1, result: result(), judgement: { action: "revise_requested", by: { kind: "user", id: "u1", name: "형주" }, at: T0, reason: "대각선 벽도" } }],
    });
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: c }, onJump })} />);
    expect(screen.getByTestId("card-judgement")).toHaveTextContent("수정 요청 · 형주 — 대각선 벽도");
    fireEvent.click(screen.getByTestId("card-new-version"));
    expect(onJump).toHaveBeenCalledWith("del2");
  });
  it("자동 결과 카드 — 「자동」 칩 · 요약 자리 문장 · 확인함·가정함 없음 · 모든 기준 ✗", () => {
    const c = card({ status: "result_submitted", result: result({ auto: true, summary: "", verdicts: [1, 2, 3].map((n) => ({ criterion: n, verdict: "unmet", stated_verdict: "unmet", evidence: [] })), met_count: 0, confirmed: [], assumed: [] }) });
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: c } })} />);
    expect(screen.getByTestId("card-auto")).toHaveTextContent("자동");
    expect(screen.getByTestId("card-summary")).toHaveTextContent(TASK_CARD.auto_summary);
    expect(screen.getAllByTestId("card-verdict").map((r) => r.querySelector(".tcard__glyph")!.textContent)).toEqual(["✗", "✗", "✗"]);
    expect(screen.queryByTestId("card-confirmed")).toBeNull();
    expect(screen.queryByTestId("card-assumed")).toBeNull();
  });
  it("결과 말풍선이 card.updated 보다 먼저 오면(캐시의 판에 결과 없음) needCard(result) 를 부탁한다", () => {
    const needCard = vi.fn();
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: card() }, needCard })} />);
    expect(needCard).toHaveBeenCalledWith("c3", 1, "result");
    expect(screen.getByTestId("card-loading")).toBeInTheDocument();
  });
});

describe("사람의 되돌리기 — 「⋯」 메뉴는 TaskCard.actions 만 본다", () => {
  const open = () => fireEvent.click(screen.getByTestId("message-menu"));
  it("권한 없음(actions 빈 목록) — 카드 항목이 하나도 없다(「이걸 미션으로」만)", () => {
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: card({ status: "result_submitted", result: result(), actions: [] }) } })} />);
    open();
    expect(screen.queryByTestId("card-menu-accept")).toBeNull();
    expect(screen.queryByTestId("card-menu-revise")).toBeNull();
    expect(screen.queryByTestId("card-menu-unaccept")).toBeNull();
    expect(screen.getByTestId("message-to-work")).toBeInTheDocument();
  });
  it("Director·결과 제출 — 「수락…」·「수정 요청…」: 수락은 코멘트 한 칸(비면 보내기 막힘) → onCardAction(accept, 코멘트), 수정 요청은 사유 한 칸(비면 막힘) → onCardAction(revise, 사유)", async () => {
    const onCardAction = vi.fn(async () => undefined);
    const c = card({ status: "result_submitted", result: result(), actions: ["accept", "revise"] });
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: c }, onCardAction })} />);
    open();
    expect(screen.getByTestId("card-menu-accept")).toHaveTextContent("수락…");
    fireEvent.click(screen.getByTestId("card-menu-accept"));
    // v0.19.17 — 빈 수락은 없다: 메뉴는 바로 수락하지 않고 코멘트 칸을 연다.
    expect(onCardAction).not.toHaveBeenCalled();
    const form = screen.getByTestId("card-reason");
    expect(form).toHaveAttribute("data-kind", "accept");
    expect(within(form).getByText(TASK_CARD.comment_label)).toBeInTheDocument();
    const input = screen.getByTestId("card-reason-input");
    expect(input).toHaveAttribute("placeholder", TASK_CARD.comment_hint);
    expect(input).toHaveAttribute("maxLength", "600");
    expect(screen.getByTestId("card-reason-send")).toBeDisabled();
    fireEvent.change(input, { target: { value: "   " } });
    expect(screen.getByTestId("card-reason-send")).toBeDisabled();
    fireEvent.submit(form);
    expect(onCardAction).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "  테스트 로그를 확인했습니다 " } });
    expect(screen.getByTestId("card-reason-send")).toBeEnabled();
    expect(screen.getByTestId("card-reason-send")).toHaveTextContent("수락");
    fireEvent.click(screen.getByTestId("card-reason-send"));
    await waitFor(() => expect(onCardAction).toHaveBeenCalledWith(c, "accept", "테스트 로그를 확인했습니다"));
    await waitFor(() => expect(screen.queryByTestId("card-reason")).toBeNull());
    open();
    fireEvent.click(screen.getByTestId("card-menu-revise"));
    fireEvent.click(screen.getByTestId("card-reason-send"));
    expect(screen.getByTestId("card-reason-error")).toHaveTextContent(TASK_CARD.reason_required);
    fireEvent.change(screen.getByTestId("card-reason-input"), { target: { value: "대각선 벽도" } });
    fireEvent.click(screen.getByTestId("card-reason-send"));
    await waitFor(() => expect(onCardAction).toHaveBeenLastCalledWith(c, "revise", "대각선 벽도"));
    await waitFor(() => expect(screen.queryByTestId("card-reason")).toBeNull());
  });
  it("수락 실패(서버 422 등)는 코멘트 칸 안에 문장으로 — 칸은 닫히지 않는다", async () => {
    const onCardAction = vi.fn(async () => { throw new Error("무엇을 확인했는지 코멘트를 적으세요"); });
    const c = card({ status: "result_submitted", result: result(), actions: ["accept", "revise"] });
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: c }, onCardAction })} />);
    open();
    fireEvent.click(screen.getByTestId("card-menu-accept"));
    fireEvent.change(screen.getByTestId("card-reason-input"), { target: { value: "봤다" } });
    fireEvent.click(screen.getByTestId("card-reason-send"));
    await waitFor(() => expect(screen.getByTestId("card-reason-error")).toHaveTextContent("무엇을 확인했는지 코멘트를 적으세요"));
    expect(screen.getByTestId("card-reason")).toHaveAttribute("data-kind", "accept");
  });
  it("Director·수락됨 — 「수락 취소…」 하나(= revise) · 수락 없음 · 지난 판 말풍선에는 메뉴 항목 없음", () => {
    const c = card({ status: "accepted", result: result(), actions: ["revise"] });
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: c } })} />);
    open();
    expect(screen.getByTestId("card-menu-unaccept")).toHaveTextContent("수락 취소…");
    expect(screen.queryByTestId("card-menu-accept")).toBeNull();
    fireEvent.click(screen.getByTestId("card-menu-unaccept"));
    expect(screen.getByTestId("card-reason")).toHaveAttribute("data-kind", "unaccept");
    cleanup();
    const c2 = card({ version: 2, status: "result_submitted", result: result({ message_id: "res2" }), actions: ["accept", "revise"], versions: [{ version: 1, result: result() }] });
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: c2 } })} />);
    open();
    expect(screen.queryByTestId("card-menu-accept")).toBeNull();
  });
  it("수락됨 + actions 빈 목록(권한 없음) — 「수락 취소…」 없음 (#397 NN1)", () => {
    render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: { c3: card({ status: "accepted", result: result(), actions: [] }) } })} />);
    open();
    expect(screen.queryByTestId("card-menu-unaccept")).toBeNull();
    expect(screen.queryByTestId("card-menu-revise")).toBeNull();
    expect(cardMenuItems(card({ status: "accepted", actions: [] }))).toEqual([]);
  });
  it("카드 이벤트가 말풍선보다 먼저(처음 보는 카드 · 방송 actions 버림) — 결과 말풍선이 getCard(actions) 를 부탁하고, 받으면 메뉴가 선다 (#397 B1)", () => {
    const needCard = vi.fn();
    // ① card.updated(결과 제출) 가 먼저 — 위임 말풍선은 페이지 밖이라 캐시에 없던 카드.
    const cache1 = cardsOnUpserted({}, card({ status: "result_submitted", result: result(), actions: ["accept", "revise"] }));
    expect(cache1.c3.actions).toEqual([]);
    // ② 뒤이어 결과 말풍선 — 판에 결과가 있어도 actions 를 믿은 적 없으니 부탁한다.
    const { rerender } = render(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: cache1, needCard })} />);
    expect(needCard).toHaveBeenCalledWith("c3", 1, "actions");
    expect(needCard.mock.calls.every((c) => c[2] === "actions")).toBe(true); // 방 화면이 (카드·판·종류) 로 한 번만 부른다
    const asked = needCard.mock.calls.length;
    expect(screen.getByTestId("card-head")).toBeInTheDocument(); // 칸은 이미 그린다
    // ③ getCard 응답(Director) — 메뉴가 선다.
    const cache2 = cardsOnFetched(cache1, card({ status: "result_submitted", result: result(), actions: ["accept", "revise"] }));
    rerender(<TimelineItemView item={one(resultMsg())} ctx={ctx({ cards: cache2, needCard })} />);
    open();
    expect(screen.getByTestId("card-menu-accept")).toBeInTheDocument();
    expect(needCard).toHaveBeenCalledTimes(asked); // 믿을 곳에서 받았으니 더 부탁하지 않는다
  });
  it("cardMenuItems — 상태와 actions 가 둘 다 맞을 때만", () => {
    expect(cardMenuItems(card({ status: "in_progress", actions: ["accept", "revise"] }))).toEqual([]);
    expect(cardMenuItems(card({ status: "result_submitted", actions: ["revise"] })).map((x) => x.kind)).toEqual(["revise"]);
    expect(cardMenuItems(null)).toEqual([]);
  });
});

describe("‹질문› — 카드 없는 에이전트 → 에이전트 멘션", () => {
  it("speech question 은 「질문」 라벨 · block 톤(?) · 받는 쪽 @에이전트 · 그 답은 「답」", () => {
    const q = msg("q1", { author_id: "a5", author: { name: "Writer", avatar_url: null }, speech: "question", addressees: [{ kind: "agent", id: "a4", name: "Researcher" }], content: "드리프트도 봤나요?", work_id: null });
    render(<TimelineItemView item={one(q)} ctx={ctx()} />);
    const chip = screen.getByTestId("speech-kind");
    expect(chip).toHaveTextContent("질문");
    expect(chip).toHaveAttribute("data-tone", "block");
    expect(chip).toHaveTextContent("?");
    expect(screen.getByTestId("speech-to")).toHaveTextContent("@Researcher");
    expect(screen.getByRole("article")).toHaveAttribute("data-side", "left");
    cleanup();
    render(<TimelineItemView item={one(msg("a1m", { speech: "answer", addressees: [{ kind: "agent", id: "a5", name: "Writer" }] }))} ctx={ctx()} />);
    expect(screen.getByTestId("speech-kind")).toHaveTextContent("답");
  });
});
