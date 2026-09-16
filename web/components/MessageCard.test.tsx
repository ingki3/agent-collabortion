import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MessageBody, MessageCard, kindBadgeFor } from "./MessageCard";
import type { Message } from "@/lib/api/types";

afterEach(cleanup);

const base = (over: Partial<Message>): Message => ({
  id: "m1", session_id: "s1", author_type: "user", author_id: "u1", author: { name: "민지" }, parent_id: null,
  content: "hello", mentions: [], source_task_id: null, kind: "text", state: "posted", created_at: "2026-09-05T10:00:00Z", ...over,
});

describe("MessageCard — 스레드 접기/펼치기", () => {
  it("답글이 있으면 접힌 채로 '답글 N개 보기'를 보이고, 누르면 답글 카드가 펼쳐진다", () => {
    const root = base({ reply_count: 2 });
    const replies = [base({ id: "r1", parent_id: "m1", content: "첫 답글" }), base({ id: "r2", parent_id: "m1", content: "둘째 답글", author_type: "agent", author: { name: "Lead" } })];
    render(<MessageCard message={root} replies={replies} />);
    expect(screen.queryByTestId("thread")).toBeNull();
    const toggle = screen.getByTestId("thread-toggle");
    expect(toggle.textContent).toBe("답글 2개 보기");
    fireEvent.click(toggle);
    const thread = screen.getByTestId("thread");
    expect(thread.querySelectorAll('[data-testid="message-card"]')).toHaveLength(2);
    expect(thread.textContent).toContain("첫 답글");
    expect(screen.getByTestId("thread-toggle").textContent).toBe("답글 2개 접기");
    fireEvent.click(screen.getByTestId("thread-toggle"));
    expect(screen.queryByTestId("thread")).toBeNull();
  });

  it("답글이 아직 없으면 onLoadReplies 로 요청한 뒤 펼친다 (reply_count 만 있는 경우)", async () => {
    const onLoad = vi.fn(async () => {});
    const { rerender } = render(<MessageCard message={base({ reply_count: 1 })} onLoadReplies={onLoad} />);
    fireEvent.click(screen.getByTestId("thread-toggle"));
    await waitFor(() => expect(onLoad).toHaveBeenCalledWith("m1"));
    rerender(<MessageCard message={base({ reply_count: 1 })} onLoadReplies={onLoad} replies={[base({ id: "r1", parent_id: "m1", content: "로드된 답글" })]} />);
    expect(screen.getByTestId("thread").textContent).toContain("로드된 답글");
  });

  it("답글이 0개면 토글이 없다", () => {
    render(<MessageCard message={base({ reply_count: 0 })} />);
    expect(screen.queryByTestId("thread-toggle")).toBeNull();
  });
});

describe("MessageCard — kind 배지와 본문(COMPONENTS §2.2 K3)", () => {
  it("text 는 배지 없음, system · blocked_q · summary 는 배지 있음, 질문 카드 답글은 answer", () => {
    expect(kindBadgeFor({ kind: "text" })).toBeNull();
    expect(kindBadgeFor({ kind: "system" })?.label).toBe("system");
    expect(kindBadgeFor({ kind: "blocked_q" }, { askee: "Lead" })).toMatchObject({ label: "질문 → @Lead", tone: "block", glyph: "?" });
    expect(kindBadgeFor({ kind: "summary" })?.tone).toBe("done");
    expect(kindBadgeFor({ kind: "text" }, { answer: true })?.label).toBe("answer");
  });

  it("blocked_q 카드의 스레드 답글은 answer 배지를 단다", () => {
    render(<MessageCard message={base({ kind: "blocked_q", reply_count: 1, author_type: "agent", author: { name: "Backend" } })} askee="Lead" replies={[base({ id: "a1", parent_id: "m1", content: "네" })]} defaultOpen />);
    const kinds = screen.getAllByTestId("message-kind").map((e) => e.textContent);
    expect(kinds[0]).toContain("질문 → @Lead");
    expect(kinds[1]).toContain("answer");
    expect(document.querySelector('[data-testid="message-card"]')!.getAttribute("data-kind")).toBe("blocked_q");
  });

  it("멘션 링크는 @이름으로 하이라이트된다", () => {
    render(<MessageCard message={base({ content: "[@Lead](mention://agent/a1) 인사해줘" })} />);
    const m = document.querySelector(".msg__mention")!;
    expect(m.textContent).toBe("@Lead");
    expect(m.getAttribute("data-mention")).toBe("agent:a1");
  });

  it("활동 슬롯은 '활동 보기'를 눌러야 열린다", () => {
    render(<MessageCard message={base({ author_type: "agent", author: { name: "Lead" }, source_task_id: "t1" })} activity={<div data-testid="rail">rail</div>} />);
    expect(screen.queryByTestId("rail")).toBeNull();
    fireEvent.click(screen.getByTestId("activity-toggle"));
    expect(screen.getByTestId("rail")).not.toBeNull();
  });
});

// PRD FR-3.1 마크다운(Director 지적 2026-09-15 · W-12) — 본문은 `lib/markdown.tsx` 가 그린다. 문법별 세부는 `lib/markdown.test.tsx`.
describe("MessageCard — 본문 마크다운(FR-3.1)", () => {
  const md = "## 결과\n상위 **5개** 를 비교했습니다.\n\n- 항목 하나\n- 항목 둘\n\n```\nnpm test\n```";

  it("에이전트 메시지의 마크다운이 제목·굵게·목록·코드 블록으로 그려진다(원문 그대로가 아니다)", () => {
    render(<MessageCard message={base({ author_type: "agent", author: { name: "Lead" }, content: md })} />);
    const body = document.querySelector(".msg__body")!;
    expect(body.querySelector("h2")!.textContent).toBe("결과");
    expect(body.querySelector("strong")!.textContent).toBe("5개");
    expect(body.querySelectorAll("ul li")).toHaveLength(2);
    expect(body.querySelector("pre code")!.textContent).toBe("npm test");
    expect(body.textContent).not.toContain("**");
    expect(body.textContent).not.toContain("## ");
  });

  it("요약(summary, W-12)·시스템 메시지도 같은 렌더러를 탄다", () => {
    render(<MessageCard message={base({ kind: "summary", author_type: "agent", author: { name: "Lead" }, content: "**끝났습니다** — `3` 건" })} />);
    render(<MessageCard message={base({ id: "m2", kind: "system", author_type: "system", content: "세션 시작 · *goal*" })} />);
    const bodies = document.querySelectorAll(".msg__body");
    expect(bodies[0].querySelector("strong")!.textContent).toBe("끝났습니다");
    expect(bodies[0].querySelector("code")!.textContent).toBe("3");
    expect(bodies[1].querySelector("em")!.textContent).toBe("goal");
  });

  it("사람 메시지도 같은 렌더러로 마크다운이 그려진다 — 작성자 구분 없음(W-18 결정, 2026-09-16)", () => {
    const c = render(<MessageCard message={base({ author_type: "user", content: "**확인**했습니다\n\n- 항목 하나\n- 항목 둘" })} />).container;
    expect(c.querySelector(".msg__body strong")!.textContent).toBe("확인");
    expect(c.querySelectorAll(".msg__body li")).toHaveLength(2);
    expect(c.querySelector(".msg__body")!.textContent).not.toContain("**");
  });

  it("멘션 칩은 마크다운 안에서도 그대로(굵게 안·목록 안)", () => {
    render(<MessageCard message={base({ content: "- **[@Lead](mention://agent/a1)** 검토\n- [@all](mention://all/all)" })} />);
    const chips = document.querySelectorAll(".msg__mention");
    expect([...chips].map((c) => c.getAttribute("data-mention"))).toEqual(["agent:a1", "all:all"]);
    expect(chips[0].parentElement!.tagName).toBe("STRONG");
  });

  it("HTML 은 문자 그대로 — 태그가 생기지 않는다(XSS 0)", () => {
    render(<MessageCard message={base({ content: '<img src=x onerror="alert(1)"> [x](javascript:alert(1))' })} />);
    const body = document.querySelector(".msg__body")!;
    expect(body.querySelector("img, a, script")).toBeNull();
    expect(body.textContent).toContain("<img src=x onerror=");
    expect(body.textContent).toContain("[x](javascript:alert(1))");
  });

  it("「작성 중…」 델타 — 닫히지 않은 펜스도 열린 채로 그려지고 커서 표시(data-typing)가 붙는다", () => {
    render(<MessageBody content={"정리하면\n```\nconst a ="} typing />);
    const body = document.querySelector(".msg__body")!;
    expect(body.getAttribute("data-typing")).toBe("true");
    expect(body.querySelector("p")!.textContent).toBe("정리하면");
    expect(body.querySelector("pre[data-open=\"true\"] code")!.textContent).toBe("const a =");
  });

  it("빈 델타도 깨지지 않는다", () => {
    render(<MessageBody content="" typing />);
    expect(document.querySelector(".msg__body .md")).not.toBeNull();
  });
});
