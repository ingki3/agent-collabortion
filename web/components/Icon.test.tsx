/** 아이콘 한 벌(§8.5) — stroke 기반 16px 인라인 SVG, 이모지 없음. 내비 다섯 + 상태 점. */
import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { Icon, ICON_NAMES } from "./Icon";
import { AppNav, NAV_ITEMS } from "./AppNav";

afterEach(cleanup);

describe("Icon", () => {
  it("한 벌은 내비 다섯 + 상태 점", () => {
    expect([...ICON_NAMES].sort()).toEqual(["agents", "computers", "dot", "inbox", "sessions", "settings"]);
  });

  it.each(ICON_NAMES)("%s — 16px · stroke currentColor · 글자 옆이라 aria-hidden", (name) => {
    const { container } = render(<Icon name={name} />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("width")).toBe("16");
    expect(svg.getAttribute("height")).toBe("16");
    expect(svg.getAttribute("stroke")).toBe("currentColor");
    expect(svg.getAttribute("aria-hidden")).toBe("true");
    expect(svg.textContent).toBe(""); // 글리프 텍스트가 아니다
  });

  it("라벨을 주면 그때만 img 역할", () => {
    render(<Icon name="dot" label="온라인" />);
    expect(screen.getByRole("img", { name: "온라인" })).toBeTruthy();
  });
});

describe("AppNav 의 아이콘", () => {
  it("항목마다 아이콘이 있고 이모지가 없다", () => {
    render(<AppNav workspaceName="ws" current="/sessions" inboxCount={2} showSettings userName="u" />);
    for (const item of NAV_ITEMS) {
      const link = screen.getByTestId(`nav-${item.key}`);
      expect(link.querySelector(`svg[data-icon="${item.icon}"]`)).not.toBeNull();
      // 이모지 금지 — 확장 그림 문자 범위가 라벨에 없어야 한다.
      expect(link.textContent ?? "").not.toMatch(/\p{Extended_Pictographic}/u);
    }
  });

  it("워크스페이스가 둘 이상이면 이름 자리가 선택 상자가 된다(상단 바에서 옮겨 옴)", () => {
    const ws = [{ id: "a", name: "A" }, { id: "b", name: "B" }];
    render(<AppNav workspaceName="A" current="/" inboxCount={0} showSettings={false} workspaces={ws} currentWorkspaceId="a" onSelectWorkspace={() => {}} />);
    expect((screen.getByLabelText("워크스페이스 선택") as HTMLSelectElement).value).toBe("a");
  });

  it("워크스페이스가 하나면 이름만", () => {
    render(<AppNav workspaceName="A" current="/" inboxCount={0} showSettings={false} workspaces={[{ id: "a", name: "A" }]} currentWorkspaceId="a" onSelectWorkspace={() => {}} />);
    expect(screen.queryByLabelText("워크스페이스 선택")).toBeNull();
    expect(screen.getByText("A")).toBeTruthy();
  });
});
