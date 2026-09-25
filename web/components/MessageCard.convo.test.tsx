/**
 * 대화 배치의 DOM — 사람 오른쪽 · 에이전트 왼쪽, 5분 묶음, 받는 쪽 칩과 「외 N명」,
 * 보고의 「↩」 버튼, aria-label (PRD FR-3.1.3 · SCREEN §4.6 「대화 배치」 · COMPONENTS §9.8).
 *
 * 판정은 서버가 내려준 칸으로 `lib/conversation.ts` 가 옮기고(그쪽 테스트가 잰다), 이 파일은
 * **그 결과가 화면에 어떻게 놓이는가**만 잰다 — 리뷰 #335 NN4 가 가리킨 빈 자리다.
 *
 * 회귀 주입(#335 리뷰 W4·W5):
 *   W4 사람 메시지를 왼쪽으로(MessageCard 의 side 삼항을 뒤집기) → 「사람은 오른쪽…」 FAIL.
 *   W5 받는 쪽 칩 미렌더(AddresseeLine 의 <AddresseeChips/> 제거) → 칩·「외 N명」 FAIL.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MessageCard } from "./MessageCard";
import { groupsWith, speechOf, type ConversationCtx } from "@/lib/conversation";
import type { Message } from "@/lib/api/types";

afterEach(cleanup);

const T0 = "2026-09-25T10:00:00Z";
const at = (min: number) => new Date(Date.parse(T0) + min * 60_000).toISOString();

const msg = (over: Partial<Message>): Message => ({
  id: "m1", session_id: "s1", author_type: "user", author_id: "u1", author: { name: "민지" }, parent_id: null,
  content: "hello", mentions: [], source_task_id: null, kind: "text", state: "posted", created_at: T0,
  speech: "chat", addressees: [], ...over,
});

const agent = (name: string, id: string) => ({ kind: "agent" as const, id, name });

/** 방 화면이 하는 그대로: 서버 칸 → speechOf → 앞 메시지와 묶임 판정 → 카드. */
function renderTimeline(items: Message[], ctx: Partial<ConversationCtx> = {}) {
  const full: ConversationCtx = {
    lanes: [],
    messageById: (id) => items.find((m) => m.id === id) ?? null,
    authorName: (m) => m.author?.name ?? (m.author_type === "system" ? "시스템" : "member"),
    ...ctx,
  };
  const onJump = vi.fn();
  let prev: { m: Message; s: ReturnType<typeof speechOf> } | undefined;
  const cards = items.map((m) => {
    const s = speechOf(m, full);
    const grouped = groupsWith(prev, { m, s });
    prev = { m, s };
    return (
      <MessageCard key={m.id} message={m} conversation={() => ({ speech: s, grouped, onJump })} />
    );
  });
  render(<div>{cards}</div>);
  return { onJump };
}

const cardOf = (id: string) => document.querySelector(`[data-message-id="${id}"]`) as HTMLElement;

describe("대화 배치 — 좌우 · 가운데", () => {
  it("사람은 오른쪽, 에이전트는 왼쪽, 시스템은 가운데 줄(말풍선 아님)", () => {
    renderTimeline([
      msg({ id: "sys", author_type: "system", author_id: null, author: undefined, kind: "system", content: "미션을 열었습니다", speech: "system" }),
      msg({ id: "human", speech: "instruct", addressees: [agent("Lead", "a-lead")], content: "[@Lead](mention://agent/a-lead) 조사 부탁" }),
      msg({ id: "bot", author_type: "agent", author_id: "a-lead", author: { name: "Lead" }, speech: "report", addressees: [{ kind: "user", id: "u1", name: "민지" }], content: "끝냈습니다" }),
    ]);
    expect(cardOf("human").dataset.side).toBe("right");
    expect(cardOf("bot").dataset.side).toBe("left");
    // 시스템 줄은 좌우를 가지지 않는다 — 가운데 작은 줄이다.
    expect(cardOf("sys").dataset.side).toBeUndefined();
    expect(cardOf("sys").className).toContain("convo--system");
  });

  it("질문·요약 카드는 전폭(wide) — 말풍선으로 좌우를 나누지 않는다", () => {
    renderTimeline([
      msg({ id: "q", author_type: "agent", author_id: "a-w", author: { name: "Writer" }, kind: "blocked_q", speech: "question", addressees: [agent("Lead", "a-lead")], content: "앞에 둘까요?" }),
      msg({ id: "sum", author_type: "system", author_id: null, author: undefined, kind: "summary", speech: "summary", content: "## 미션 요약" }),
    ]);
    expect(cardOf("q").dataset.side).toBe("wide");
    expect(cardOf("sum").dataset.side).toBe("wide");
  });
});

describe("대화 배치 — 머리 한 줄", () => {
  it("받는 쪽 칩이 이름으로 그려지고, 넷째부터는 「외 N명」으로 접힌다", () => {
    renderTimeline([
      msg({
        id: "many", speech: "instruct", content: "다 같이 봅시다",
        addressees: [agent("Lead", "a1"), agent("Researcher", "a2"), agent("Writer", "a3"), agent("QA", "a4"), agent("Ops", "a5")],
      }),
    ]);
    const chips = [...cardOf("many").querySelectorAll('[data-testid="speech-to"] .convo__to-chip')];
    expect(chips.map((c) => c.textContent)).toEqual(["@Lead", "@Researcher", "@Writer", "외 2명"]);
  });

  it("받는 쪽이 비면 「방 전체」, 메모는 「기록만」", () => {
    renderTimeline([
      msg({ id: "chat", speech: "chat", addressees: [], content: "좋네요" }),
      msg({ id: "note", speech: "note", addressees: [], content: "/note 금요일 보고" }),
    ]);
    expect(cardOf("chat").querySelector('[data-testid="speech-to"]')!.textContent).toBe("방 전체");
    expect(cardOf("note").querySelector('[data-testid="speech-to"]')!.textContent).toBe("기록만");
  });

  it("종류 칩은 서버가 준 speech 그대로다 — 대화·시스템은 칩이 없다", () => {
    renderTimeline([
      msg({ id: "d", author_type: "agent", author_id: "a1", author: { name: "Lead" }, speech: "delegate", addressees: [agent("Researcher", "a2")], content: "조사해 주세요" }),
      msg({ id: "c", speech: "chat", addressees: [], content: "네" }),
    ]);
    expect(cardOf("d").querySelector('[data-testid="speech-kind"]')!.textContent).toContain("위임");
    expect(cardOf("d").dataset.speech).toBe("delegate");
    expect(cardOf("c").querySelector('[data-testid="speech-kind"]')).toBeNull();
  });
});

describe("대화 배치 — 묶음(5분)", () => {
  const run = (gapMin: number, over: Partial<Message> = {}) =>
    renderTimeline([
      msg({ id: "a", author_type: "agent", author_id: "a1", author: { name: "Lead" }, speech: "report", addressees: [agent("R", "a2")], content: "하나" }),
      msg({
        id: "b", author_type: "agent", author_id: "a1", author: { name: "Lead" }, speech: "report",
        addressees: [agent("R", "a2")], content: "둘", created_at: at(gapMin), ...over,
      }),
    ]);

  it("같은 작성자·같은 종류·같은 받는 쪽이 5분 안이면 머리를 한 번만 그린다", () => {
    run(3);
    expect(cardOf("a").dataset.grouped).toBeUndefined();
    expect(cardOf("b").dataset.grouped).toBe("true");
    // 묶여도 받는 쪽은 남는다(누구에게 한 말인지가 사라지면 안 된다).
    expect(cardOf("b").querySelector('[data-testid="speech-to"]')!.textContent).toBe("@R");
    // 아바타 자리는 비워 둔다 — 줄이 어긋나지 않게.
    expect(cardOf("b").querySelector(".convo__avatar")!.getAttribute("data-blank")).toBe("true");
  });

  it("5분을 넘기면 묶이지 않는다", () => {
    run(6);
    expect(cardOf("b").dataset.grouped).toBeUndefined();
  });

  it("받는 쪽이 다르면 묶이지 않는다", () => {
    run(1, { addressees: [agent("W", "a3")] });
    expect(cardOf("b").dataset.grouped).toBeUndefined();
  });
});

describe("대화 배치 — 보고의 「↩ … 에 대한 보고」", () => {
  const trigger = msg({ id: "trig", speech: "instruct", addressees: [agent("Lead", "a1")], content: "[@Lead](mention://agent/a1) STO 시장 레포트 만들어 줘" });
  const report = msg({
    id: "rep", author_type: "agent", author_id: "a1", author: { name: "Lead" }, speech: "report",
    addressees: [{ kind: "user", id: "u1", name: "민지" }], responds_to_message_id: "trig", content: "제출했습니다", created_at: at(1),
  });

  it("요청자와 원문 첫 줄을 인용하고, 누르면 그 메시지로 보낸다", () => {
    const { onJump } = renderTimeline([trigger, report]);
    const link = screen.getByTestId("report-of");
    expect(link.textContent).toContain("민지");
    expect(link.textContent).toContain("STO 시장 레포트");
    fireEvent.click(link);
    expect(onJump).toHaveBeenCalledWith("trig");
  });

  it("그 메시지를 아직 읽을 수 없으면 링크 없이 라벨만 — 빈 인용을 그리지 않는다", () => {
    renderTimeline([report], { messageById: () => null });
    expect(screen.queryByTestId("report-of")).toBeNull();
    expect(cardOf("rep").querySelector('[data-testid="speech-kind"]')!.textContent).toContain("보고");
  });
});

describe("대화 배치 — 스크린리더", () => {
  it("aria-label 이 「누가: 누구에게 무엇을」을 다 읽는다(묶여서 이름이 안 보여도)", () => {
    renderTimeline([
      msg({ id: "x", author_type: "agent", author_id: "a1", author: { name: "Lead" }, speech: "delegate", addressees: [agent("Researcher", "a2")], content: "조사" }),
      msg({ id: "y", author_type: "agent", author_id: "a1", author: { name: "Lead" }, speech: "delegate", addressees: [agent("Researcher", "a2")], content: "하나 더", created_at: at(1) }),
    ]);
    expect(cardOf("x").getAttribute("aria-label")).toBe("Lead: @Researcher에게 위임");
    expect(cardOf("y").dataset.grouped).toBe("true");
    expect(cardOf("y").getAttribute("aria-label")).toBe("Lead: @Researcher에게 위임");
  });

  it("받는 쪽이 없으면 「방 전체에」, 메모는 「기록만」으로 읽는다", () => {
    renderTimeline([
      msg({ id: "c", speech: "chat", addressees: [], content: "좋네요" }),
      msg({ id: "n", speech: "note", addressees: [], content: "/note 확인", created_at: at(1) }),
    ]);
    expect(cardOf("c").getAttribute("aria-label")).toBe("민지: 방 전체에");
    expect(cardOf("n").getAttribute("aria-label")).toBe("민지: 기록만 메모");
  });
});
