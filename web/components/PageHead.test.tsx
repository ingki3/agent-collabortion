/** 화면 머리·비활성 사유(§8.5) — 설명이 실제로 그려지고, 사유가 버튼에 aria 로 묶이는지. */
import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { PageHead, DisabledHint, PAGE_COPY } from "./PageHead";

afterEach(cleanup);

describe("PageHead", () => {
  it("제목과 한 줄 설명을 그린다", () => {
    render(<PageHead screen="computers" />);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe(PAGE_COPY.computers.title);
    expect(screen.getByTestId("page-desc").textContent).toBe(PAGE_COPY.computers.desc);
  });

  it("행동이 없으면 오른쪽 칸을 만들지 않는다", () => {
    const { container } = render(<PageHead screen="settings" />);
    expect(container.querySelector(".page-head__actions")).toBeNull();
  });

  it("비활성 사유가 버튼 아래에 서고 aria-describedby 로 이어진다", () => {
    const why = "먼저 컴퓨터를 연결하세요";
    render(
      <PageHead screen="sessions">
        <a href="/sessions/new" className="btn" aria-disabled="true" aria-describedby="new-session-hint" title={why}>
          새 세션
        </a>
        <DisabledHint id="new-session-hint">{why}</DisabledHint>
      </PageHead>,
    );
    const btn = screen.getByText("새 세션");
    const hint = screen.getByTestId("new-session-hint");
    expect(hint.textContent).toBe(why);
    expect(document.getElementById(btn.getAttribute("aria-describedby")!)).toBe(hint);
    // 사유는 버튼 **다음**에 온다(아래에 선다).
    expect(btn.compareDocumentPosition(hint) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
