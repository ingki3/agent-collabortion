/** 테마 선택(§8.3) — 저장과 <html data-theme> 반영이 한 번에 일어나는지. */
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { ThemeSelect } from "./ThemeSelect";
import { THEME_KEY, THEME_INIT } from "@/lib/theme";

function opt(label: string) {
  return screen.getByLabelText(label) as HTMLInputElement;
}

describe("ThemeSelect", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
  });
  afterEach(cleanup);

  it("저장된 값이 없으면 시스템 따름이고 data-theme 를 걸지 않는다", () => {
    render(<ThemeSelect />);
    expect(opt("시스템 따름").checked).toBe(true);
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
  });

  it("어둡게를 고르면 <html data-theme=dark> 와 localStorage 가 함께 바뀐다", () => {
    render(<ThemeSelect />);
    opt("어둡게").click();
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(window.localStorage.getItem(THEME_KEY)).toBe("dark");
  });

  it("밝게는 시스템이 어두워도 밝음을 지키도록 data-theme=light 를 남긴다", () => {
    render(<ThemeSelect />);
    opt("밝게").click();
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(window.localStorage.getItem(THEME_KEY)).toBe("light");
  });

  it("시스템 따름으로 돌아가면 속성과 저장이 모두 지워진다", () => {
    render(<ThemeSelect />);
    opt("어둡게").click();
    opt("시스템 따름").click();
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
    expect(window.localStorage.getItem(THEME_KEY)).toBe(null);
  });

  it("저장된 값을 마운트 뒤에 따라잡는다", () => {
    window.localStorage.setItem(THEME_KEY, "dark");
    render(<ThemeSelect />);
    expect(opt("어둡게").checked).toBe(true);
  });
});

describe("THEME_INIT (첫 페인트 깜빡임 방지)", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
  });

  it("저장된 dark 를 하이드레이션 전에 <html> 에 심는다", () => {
    window.localStorage.setItem(THEME_KEY, "dark");
    // eslint-disable-next-line no-eval
    (0, eval)(THEME_INIT);
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
  });

  it("시스템 따름(저장 없음)이면 아무 속성도 심지 않는다", () => {
    (0, eval)(THEME_INIT);
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
  });

  it("localStorage 가 막혀도 던지지 않는다", () => {
    const orig = Object.getOwnPropertyDescriptor(window, "localStorage")!;
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      get() { throw new Error("blocked"); },
    });
    expect(() => (0, eval)(THEME_INIT)).not.toThrow();
    Object.defineProperty(window, "localStorage", orig);
  });
});
