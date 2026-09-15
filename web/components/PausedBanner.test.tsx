/**
 * 세션 paused 배너(SCREEN §4.5 O6) — 계약 `PausedDetail` 은 **객체**다.
 * 루프 상한이면 어느 상한(chain_depth·hops_per_hour·pair_roundtrips)에 몇 번 걸렸는지를 보여야 Director 가
 * "무엇을 올려야 하는지"를 안다. 승인만 하면 같은 핑퐁이 반복되기 때문이다.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { PausedBanner, pausedSummary } from "./PausedBanner";
import type { PausedDetail } from "@/lib/api/types";

afterEach(cleanup);

const NAMES: Record<string, string> = { "ag-1": "Backend", "ag-2": "QA" };
const name = (id: string) => NAMES[id] ?? id;

const loop: PausedDetail = {
  reason: "loop",
  paused_at: "2026-09-06T10:00:00Z",
  loop: { limit: "pair_roundtrips", count: 5, agents: ["ag-1", "ag-2"] },
  resolve_actions: ["resume"],
  can_resolve_from: null,
};

describe("PausedBanner — 루프 상한(U15-8)", () => {
  it("어느 상한에 몇 회 걸렸는지와 두 에이전트를 이름으로 보여준다", () => {
    render(<PausedBanner detail={loop} agentName={name} onResume={vi.fn()} />);
    expect(screen.getByTestId("paused-banner").getAttribute("data-reason")).toBe("loop");
    expect(screen.getByTestId("paused-title").textContent).toContain("주고받기 상한");
    const sum = screen.getByTestId("paused-summary").textContent ?? "";
    expect(sum).toContain("두 에이전트가 상한까지 주고받았습니다"); // 내부 키는 문장에 넣지 않는다(§8.4)
    expect(sum).toContain("@Backend ↔ @QA");
    expect(sum).toContain("5회");
    const hint = screen.getByTestId("paused-loop-limit");
    expect(hint.getAttribute("data-limit")).toBe("pair_roundtrips");
    expect(hint.getAttribute("data-count")).toBe("5");
    expect(hint.textContent).toContain("올려야 할 상한");
  });

  it("chain_depth·hops_per_hour 도 이름으로 구분된다 — 상한이 다르면 올릴 값이 다르다", () => {
    expect(pausedSummary({ ...loop, loop: { limit: "chain_depth", count: 8 } }, name)).toContain("주고받기 연쇄가 상한까지 깊어졌습니다");
    expect(pausedSummary({ ...loop, loop: { limit: "hops_per_hour", count: 40 } }, name)).toContain("한 시간에 오간 횟수가 상한에 닿았습니다");
  });

  it("계속 진행 승인은 루프 카운터를 리셋해 재개한다", async () => {
    const onResume = vi.fn(async () => {});
    render(<PausedBanner detail={loop} agentName={name} onResume={onResume} />);
    fireEvent.click(screen.getByTestId("paused-resume"));
    await waitFor(() => expect(onResume).toHaveBeenCalledWith({ reset_loop_counters: true }));
  });
});

describe("PausedBanner — 사유 5종마다 할 일이 다르다", () => {
  it("예산 초과는 새 상한 입력을 받아 재개한다", async () => {
    const onResume = vi.fn(async () => {});
    render(
      <PausedBanner
        detail={{ reason: "budget", paused_at: loop.paused_at, budget: { limit_usd: 20, spent_usd: 21.4 }, resolve_actions: ["resume"] }}
        onResume={onResume}
      />,
    );
    expect(screen.getByTestId("paused-summary").textContent).toContain("예산 $20을 초과했습니다 (현재 $21.40)");
    fireEvent.change(screen.getByTestId("paused-budget-input"), { target: { value: "30" } });
    fireEvent.click(screen.getByTestId("paused-resume"));
    await waitFor(() => expect(onResume).toHaveBeenCalledWith({ limits: { budget_usd: 30 } }));
  });

  it("시간 초과는 연장 시간을, 수동 일시정지는 본문 없이 재개한다", async () => {
    const onResume = vi.fn(async () => {});
    const { rerender } = render(
      <PausedBanner detail={{ reason: "time", paused_at: loop.paused_at, time: { limit: "PT4H", elapsed: "PT4H2M" }, resolve_actions: ["resume"] }} onResume={onResume} />,
    );
    expect(screen.getByTestId("paused-summary").textContent).toContain("시간 상한 4시간에 도달했습니다");
    fireEvent.change(screen.getByTestId("paused-time-input"), { target: { value: "PT1H" } });
    fireEvent.click(screen.getByTestId("paused-resume"));
    await waitFor(() => expect(onResume).toHaveBeenCalledWith({ limits: { time_limit: "PT1H" } }));

    onResume.mockClear();
    rerender(<PausedBanner detail={{ reason: "director", paused_at: loop.paused_at, resolve_actions: ["resume"] }} onResume={onResume} />);
    expect(screen.getByTestId("paused-resume").textContent).toBe("재개");
    fireEvent.click(screen.getByTestId("paused-resume"));
    await waitFor(() => expect(onResume).toHaveBeenCalledWith({}));
  });

  it("런타임 오프라인은 재개가 아니라 재바인딩·종료다 — 재개 버튼은 비활성", () => {
    render(
      <PausedBanner
        detail={{ reason: "runtime_offline", paused_at: loop.paused_at, runtime: { runtime_id: "r1", offline_since: "2026-08-28T00:00:00Z" }, resolve_actions: ["rebind", "cancel"] }}
        onResume={vi.fn()}
        onRebind={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect((screen.getByTestId("paused-resume") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("paused-rebind")).not.toBeNull();
    expect(screen.getByTestId("paused-cancel")).not.toBeNull();
  });

  it("deputy 는 기한 절반 전까지 비활성이고 '언제부터 승인 가능'을 말한다(t-3)", () => {
    const later = new Date(Date.now() + 3600_000).toISOString();
    render(<PausedBanner detail={{ ...loop, can_resolve_from: later }} agentName={name} onResume={vi.fn()} />);
    const btn = screen.getByTestId("paused-resume") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("title")).toContain("부터 승인 가능");
  });
});

describe("비활성 사유는 화면 텍스트 + aria-describedby 다(§8.5, PR #191 NN1)", () => {
  const paused_at = new Date(Date.now() - 60_000).toISOString();
  it("멤버(resolve_actions 에 resume 없음) — 「Director 만 할 수 있습니다」가 버튼 옆에 보이고 버튼이 그 줄을 가리킨다", () => {
    render(<PausedBanner detail={{ reason: "budget", paused_at, resolve_actions: ["cancel"] }} onResume={vi.fn()} />);
    const btn = screen.getByTestId("paused-resume");
    const why = screen.getByTestId("paused-why");
    expect(why.textContent).toBe("Director 만 할 수 있습니다");
    expect(btn.getAttribute("aria-describedby")).toBe(why.id);
    expect(why.id).toBeTruthy();
  });
  it("deputy 기한 전 — 사유가 '…부터 승인 가능' 이고 같은 줄을 가리킨다", () => {
    const later = new Date(Date.now() + 3600_000).toISOString();
    render(<PausedBanner detail={{ reason: "budget", paused_at, resolve_actions: ["resume"], can_resolve_from: later }} onResume={vi.fn()} />);
    const btn = screen.getByTestId("paused-resume");
    const why = screen.getByTestId("paused-why");
    expect(why.textContent).toContain("부터 승인 가능");
    expect(btn.getAttribute("aria-describedby")).toBe(why.id);
  });
  it("컴퓨터 연결 끊김 — 사람이 아니라 사유가 막는다고 말한다(「Director 만」이 아니다)", () => {
    render(<PausedBanner detail={{ reason: "runtime_offline", paused_at, resolve_actions: ["rebind", "cancel"] }} onResume={vi.fn()} onRebind={vi.fn()} onCancel={vi.fn()} />);
    const why = screen.getByTestId("paused-why");
    expect(why.textContent).toContain("재개할 수 없습니다");
    expect(why.textContent).not.toContain("Director");
    expect(screen.getByTestId("paused-resume").getAttribute("aria-describedby")).toBe(why.id);
  });
  it("Director 이고 기한이 없으면 사유 줄이 없고 aria-describedby 도 없다", () => {
    render(<PausedBanner detail={{ reason: "budget", paused_at, resolve_actions: ["resume", "cancel"] }} onResume={vi.fn()} />);
    expect(screen.queryByTestId("paused-why")).toBeNull();
    expect(screen.getByTestId("paused-resume").getAttribute("aria-describedby")).toBeNull();
  });
});
