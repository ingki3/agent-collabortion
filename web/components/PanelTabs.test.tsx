/**
 * 패널 안 탭 틀(T-RF2) — 탭 하나면 탭 줄이 없다(틀 도입 전과 같은 DOM) · 둘이면 tablist(←→·Home·End). 우열 미션 칸이 탭 하나로 이 틀을 쓴다.
 * 회귀 주입: PanelTabs 의 `tabs.length === 1` 분기를 빼면 (하나면 탭 줄 없음) · (WorkPanel) FAIL; tabKeyTarget 의 순환(% count)을 빼면 (키) FAIL.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { PanelTabs } from "./PanelTabs";
import { WorkPanel } from "./WorkPanel";
import { tabKeyTarget } from "@/lib/tabs";

afterEach(cleanup);

describe("tabKeyTarget — 방 화면 좁은 탭과 같은 키 규칙", () => {
  it("←→ 는 한 칸(끝에서 돈다) · Home · End · 다른 키는 -1", () => {
    expect(tabKeyTarget("ArrowRight", 0, 4)).toBe(1);
    expect(tabKeyTarget("ArrowRight", 3, 4)).toBe(0);
    expect(tabKeyTarget("ArrowLeft", 0, 4)).toBe(3);
    expect(tabKeyTarget("Home", 2, 4)).toBe(0);
    expect(tabKeyTarget("End", 0, 4)).toBe(3);
    expect(tabKeyTarget("Enter", 0, 4)).toBe(-1);
    expect(tabKeyTarget("ArrowRight", 0, 0)).toBe(-1);
  });
});

describe("PanelTabs", () => {
  it("탭이 하나면 탭 줄도 감싸는 요소도 없이 내용만 그린다", () => {
    const { container } = render(<PanelTabs label="미션" idPrefix="p" tabs={[{ id: "a", label: "개요", render: () => <p data-testid="only">내용</p> }]} />);
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(screen.queryByRole("tabpanel")).toBeNull();
    expect(container.firstElementChild).toBe(screen.getByTestId("only"));
  });

  it("둘이면 tablist · 선택 하나만 Tab 순서 · ←→·Home·End 로 옮기고 포커스도 따라간다", () => {
    render(
      <PanelTabs
        label="미션"
        idPrefix="p"
        tabs={[
          { id: "a", label: "개요", render: () => <p>개요 내용</p> },
          { id: "b", label: "분담", render: () => <p>분담 내용</p> },
        ]}
      />,
    );
    const list = screen.getByRole("tablist", { name: "미션" });
    const tabs = screen.getAllByRole("tab");
    expect(list).toContainElement(tabs[0]);
    expect(tabs.map((t) => t.getAttribute("aria-selected"))).toEqual(["true", "false"]);
    expect(tabs.map((t) => t.tabIndex)).toEqual([0, -1]);
    expect(screen.getByRole("tabpanel")).toHaveTextContent("개요 내용");
    expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", tabs[0].id);

    fireEvent.keyDown(tabs[0], { key: "ArrowRight" });
    expect(screen.getByTestId("p-tab-b")).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("p-tab-b")).toHaveFocus();
    expect(screen.getByRole("tabpanel")).toHaveTextContent("분담 내용");
    fireEvent.keyDown(screen.getByTestId("p-tab-b"), { key: "ArrowRight" });
    expect(screen.getByTestId("p-tab-a")).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(screen.getByTestId("p-tab-a"), { key: "End" });
    expect(screen.getByTestId("p-tab-b")).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(screen.getByTestId("p-tab-b"), { key: "Home" });
    expect(screen.getByTestId("p-tab-a")).toHaveAttribute("aria-selected", "true");
    fireEvent.click(screen.getByTestId("p-tab-b"));
    expect(screen.getByRole("tabpanel")).toHaveTextContent("분담 내용");
  });

  it("탭이 없으면 아무것도 그리지 않는다", () => {
    const { container } = render(<PanelTabs label="x" idPrefix="p" tabs={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("WorkPanel — 탭 하나(개요)라 탭 줄이 없고, 칸의 맨 바깥이 옛 그대로 section.aside__sec 이다", () => {
  it("미션이 없는 방", () => {
    const { container } = render(<WorkPanel mode={{ kind: "no_works" }} work={null} />);
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(container.firstElementChild).toBe(screen.getByTestId("work-panel"));
    expect(container.firstElementChild).toHaveClass("aside__sec");
  });
});
