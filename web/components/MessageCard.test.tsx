import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MessageCard, kindBadgeFor } from "./MessageCard";
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
