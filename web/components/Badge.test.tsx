import { describe, expect, it, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { Badge } from "./Badge";
import { BADGE_ENTRY_COUNT, BADGE_KINDS, badgeSpec, badgeValues, type BadgeKind } from "./badge-map";

afterEach(cleanup);

function renderAll(kind: BadgeKind) {
  for (const value of badgeValues(kind)) {
    // BadgeProps 는 kind 별 discriminated union — 순회 렌더는 좁혀 캐스팅한다
    render(<Badge {...({ kind, value } as Parameters<typeof Badge>[0])} />);
  }
}

describe("Badge", () => {
  it("모든 kind 의 모든 value 가 렌더되고 글리프가 맞다 (badge-map 순회)", () => {
    for (const kind of BADGE_KINDS) {
      renderAll(kind);
      for (const value of badgeValues(kind)) {
        const spec = badgeSpec(kind, value);
        const el = document.querySelector(`.badge[data-kind="${kind}"][data-value="${value}"]`);
        expect(el, `${kind}/${value} 렌더`).not.toBeNull();
        expect(el!.querySelector(".badge__glyph")!.textContent, `${kind}/${value} 글리프`).toBe(spec.glyph);
        expect(el!.querySelector(".badge__label")!.textContent, `${kind}/${value} 라벨`).toBe(spec.label);
        expect(el!.getAttribute("data-tone")).toBe(spec.tone);
      }
      cleanup();
    }
  });

  it("badge-map 항목 수 = PRD 열거값 수 (lane 7 · task 10 · session 6 · agent 6 · inbox 3 · room 4)", () => {
    expect(badgeValues("lane")).toHaveLength(7);
    expect(badgeValues("task")).toHaveLength(10);
    expect(badgeValues("session")).toHaveLength(6);
    expect(badgeValues("agent")).toHaveLength(6);
    expect(badgeValues("inbox")).toHaveLength(3);
    // v0.19 (T-R2-W1) — 방 멈춤 사유 4종(계약 RoomBlockedReason enum 그대로), 라벨은 「멈춤」 — 「일시정지」가 아니다(§8.4 층 분담).
    expect([...badgeValues("room")].sort()).toEqual(["budget", "loop", "manual", "runtime_offline"]);
    for (const v of badgeValues("room")) {
      expect(badgeSpec("room", v).label).toMatch(/멈춤$/);
      expect(badgeSpec("room", v).label).not.toContain("일시정지");
    }
    expect(badgeSpec("room", "manual").label).toBe("직접 멈춤");
    expect(badgeSpec("room", "runtime_offline").label).toBe("컴퓨터 연결 끊김으로 멈춤");
    expect(BADGE_ENTRY_COUNT).toBe(36);
  });

  it("failed / error / offline 기본 variant 는 solid, 나머지는 soft", () => {
    const solid = new Set(["failed", "error", "offline"]);
    for (const kind of BADGE_KINDS) {
      for (const value of badgeValues(kind)) {
        expect(badgeSpec(kind, value).variant, `${kind}/${value}`).toBe(solid.has(value) ? "solid" : "soft");
      }
    }
    render(<Badge kind="lane" value="failed" />);
    render(<Badge kind="agent" value="error" />);
    render(<Badge kind="lane" value="paused" />);
    expect(document.querySelector('[data-kind="lane"][data-value="failed"]')!.getAttribute("data-variant")).toBe("solid");
    expect(document.querySelector('[data-kind="agent"][data-value="error"]')!.getAttribute("data-variant")).toBe("solid");
    expect(document.querySelector('[data-kind="lane"][data-value="paused"]')!.getAttribute("data-variant")).toBe("soft");
  });

  it("aria-label 에 상태명이 있고 글리프는 aria-hidden 이다", () => {
    render(<Badge kind="session" value="active" />);
    const el = screen.getByLabelText("진행 중");
    expect(el.getAttribute("role")).toBe("img");
    expect(el.querySelector(".badge__glyph")!.getAttribute("aria-hidden")).toBe("true");

    render(<Badge kind="lane" value="blocked" label="@Lead 답 대기" />);
    expect(screen.getByLabelText("@Lead 답 대기").getAttribute("data-value")).toBe("blocked");
  });

  it("variant / tone / size 를 덮어쓸 수 있다", () => {
    render(<Badge kind="inbox" value="attention" tone="fail" variant="solid" size="sm" />);
    const el = screen.getByLabelText("주의");
    expect(el.getAttribute("data-tone")).toBe("fail");
    expect(el.getAttribute("data-variant")).toBe("solid");
    expect(el.className).toContain("badge--sm");
  });
});
