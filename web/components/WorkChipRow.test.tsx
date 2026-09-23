/**
 * 미션 칩 줄(COMPONENTS §9.1) · 방 멈춤 배너(§9.4) — T-R2-W2.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { WorkChipRow, WorkLabel } from "./WorkChipRow";
import { RoomBlockedBanner } from "./RoomBlockedBanner";
import type { Room, User, WorkListItem } from "@/lib/api/types";

afterEach(cleanup);

const user = (id: string, name: string): User => ({ id, email: `${id}@x`, display_name: name, avatar_url: null, created_at: "" });
const w = (id: string, title: string, status: WorkListItem["status"] = "active", extra: Partial<WorkListItem> = {}): WorkListItem => ({
  id, room_id: "r1", title, goal: title, status, paused_reason: status === "paused" ? "budget" : null, waiting_human: false, director: user("u2", "민호"),
  assignee_agent_id: null, completion_progress: { met: 0, total: 1 }, cost_usd: 0, budget_usd: null, last_activity_at: null, finished_at: null, ...extra,
});

describe("WorkChipRow", () => {
  it("한 그룹(「미션 거르개」) · 선택은 aria-pressed + ✓(색만으로 표시하지 않는다) · 누르면 선택을 올린다", () => {
    const onSelect = vi.fn();
    render(<WorkChipRow works={[w("w1", "보고서 초안"), w("w2", "수수료 비교", "paused")]} sel={{ kind: "work", id: "w1" }} onSelect={onSelect} announce="「보고서 초안」 미션으로 걸렀습니다" />);
    expect(screen.getByRole("group", { name: "미션 거르개" })).toBeInTheDocument();
    const chips = screen.getAllByTestId("work-chip");
    expect(chips.map((c) => c.getAttribute("aria-pressed"))).toEqual(["true", "false"]);
    expect(chips[0].querySelector(".work-chip__check")!.textContent).toBe("✓");
    expect(chips[1].querySelector(".work-chip__check")).toBeNull();
    expect(chips[1].textContent).toContain("⏸");
    expect(screen.getByTestId("chip-all").textContent).toBe("전체");
    expect(screen.getByTestId("chip-none").textContent).toBe("미션 없음");
    fireEvent.click(chips[1]);
    expect(onSelect).toHaveBeenCalledWith({ kind: "work", id: "w2" });
    fireEvent.click(screen.getByTestId("chip-none"));
    expect(onSelect).toHaveBeenLastCalledWith({ kind: "none" });
    // 두 곳이 바뀐 사실은 조용한 안내로(§9.1 규칙 4)
    expect(screen.getByTestId("chip-announce")).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("chip-announce").textContent).toBe("「보고서 초안」 미션으로 걸렀습니다");
  });

  it("⏳ 는 상태가 아니라 파생 — 툴팁이 아니라 aria-label 「사람 대기」", () => {
    render(<WorkChipRow works={[w("w1", "보고서", "active", { waiting_human: true })]} sel={{ kind: "all" }} onSelect={vi.fn()} announce="" />);
    expect(screen.getByRole("img", { name: "사람 대기" }).textContent).toBe("⏳\uFE0E");
  });

  it("미션이 하나도 없으면 칩 줄 없이 「+ 새 미션」만", () => {
    const onNewWork = vi.fn();
    render(<WorkChipRow works={[]} sel={{ kind: "all" }} onSelect={vi.fn()} onNewWork={onNewWork} announce="" />);
    expect(screen.queryByTestId("work-chips")).toBeNull();
    expect(screen.queryByTestId("chip-all")).toBeNull();
    fireEvent.click(screen.getByTestId("new-work"));
    expect(onNewWork).toHaveBeenCalled();
  });

  it("끝난 미션은 칩에서 빠져 「지난 미션 N개 ▾」로 — 펼쳐서 고를 수 있다(S22 읽기 전용)", () => {
    const onSelect = vi.fn();
    render(<WorkChipRow works={[w("w1", "보고서"), w("w9", "지난 보고서", "completed")]} sel={{ kind: "all" }} onSelect={onSelect} announce="" />);
    expect(screen.getAllByTestId("work-chip")).toHaveLength(1);
    const past = screen.getByTestId("chip-past");
    expect(past.querySelector("[data-slot]")!.textContent).toBe("1");
    fireEvent.click(past);
    fireEvent.click(screen.getByTestId("chip-past-item"));
    expect(onSelect).toHaveBeenCalledWith({ kind: "work", id: "w9" });
  });

  it("일시정지 2개 이상 — 「일시정지 2 ▾」 펼치면 이름 · 사유 · 승인 권한자", () => {
    render(<WorkChipRow works={[w("w1", "보고서", "paused"), w("w2", "수수료 비교", "paused")]} sel={{ kind: "all" }} onSelect={vi.fn()} announce="" />);
    fireEvent.click(screen.getByTestId("chip-paused"));
    const rows = screen.getAllByTestId("chip-paused-item");
    expect(rows).toHaveLength(2);
    expect(rows[1].textContent).toContain("수수료 비교");
    expect(rows[1].textContent).toContain("미션 예산 초과");
    expect(rows[1].textContent).toContain("승인: 민호");
  });

  it("미션 칩 5개부터 4 + 「미션 N개 더 ▾」", () => {
    render(<WorkChipRow works={["1", "2", "3", "4", "5"].map((i) => w(i, `m${i}`))} sel={{ kind: "all" }} onSelect={vi.fn()} announce="" />);
    expect(screen.getAllByTestId("work-chip")).toHaveLength(4);
    expect(screen.getByTestId("chip-overflow").textContent).toContain("미션 1개 더");
  });

  it("카드 안의 라벨은 누를 수 없다 — 버튼이 아니다", () => {
    render(<WorkLabel text="미션 「보고서」" />);
    expect(screen.getByTestId("work-label").tagName).toBe("SPAN");
    expect(screen.queryByRole("button")).toBeNull();
  });
});

const room = (over: Partial<Room>): Pick<Room, "blocked_reason" | "blocked_detail" | "my_capabilities" | "runtime"> => ({
  blocked_reason: null, blocked_detail: null, my_capabilities: [], ...over,
});

describe("RoomBlockedBanner — 몇 개가 멈췄는지 반드시 센다(수는 슬롯)", () => {
  it("role=alert · 멈춘 미션 수는 문장 보간이 아니라 [data-slot] 칸", () => {
    render(<RoomBlockedBanner me="u1" room={room({ blocked_reason: "budget", blocked_detail: { works_stopped: 2, budget_usd: 50, approver: user("u1", "서연") } })} />);
    const b = screen.getByTestId("room-banner");
    expect(b).toHaveAttribute("role", "alert");
    expect(b.textContent).toContain("이 방의 예산 $50을 넘겼습니다");
    const stopped = screen.getByTestId("room-banner-stopped");
    expect(stopped.textContent).toBe("미션 2개와 대화 전부가 멈췄습니다");
    expect(stopped.querySelector("[data-slot]")!.textContent).toBe("2");
    expect(screen.getByTestId("room-banner-lead").querySelector("[data-slot]")!.textContent).toBe("50");
    // 내가 승인 권한자면 위임 줄은 없다
    expect(screen.queryByTestId("room-banner-waiting")).toBeNull();
  });

  it("미션이 0 이면 「미션 밖 대화 전부가 멈췄습니다」", () => {
    render(<RoomBlockedBanner me="u1" room={room({ blocked_reason: "loop", blocked_detail: { works_stopped: 0 } })} />);
    expect(screen.getByTestId("room-banner-stopped").textContent).toBe("미션 밖 대화 전부가 멈췄습니다");
  });

  it("내가 승인 권한자가 아니면 누구의 승인을 기다리는지와 위임 시각을 적는다", () => {
    render(<RoomBlockedBanner me="u9" room={room({ blocked_reason: "budget", blocked_detail: { works_stopped: 1, approver: user("u2", "민호"), delegate_at: "2026-09-22T05:30:00Z" } })} />);
    const who = screen.getByTestId("room-banner-waiting");
    expect(who.textContent).toMatch(/^「민호」의 승인을 기다립니다 · \d\d:\d\d부터 다음 권한자가 답할 수 있습니다$/);
  });

  it("manual — 누가 멈췄는지 이름으로, 「멈춤 해제」는 권한자에게만 켜지고 그 밖에는 비활성 + 사유 글자", () => {
    const onUnblock = vi.fn();
    const by = user("u2", "서연");
    const { rerender } = render(<RoomBlockedBanner me="u1" onUnblock={onUnblock} room={room({ blocked_reason: "manual", blocked_detail: { works_stopped: 2, blocked_by_user: by, blocked_at: "2026-09-22T05:03:00Z" }, my_capabilities: ["block"] })} />);
    expect(screen.getByTestId("room-banner-lead").textContent).toBe("「서연」님이 이 방을 멈췄습니다");
    fireEvent.click(screen.getByTestId("room-banner-unblock"));
    expect(onUnblock).toHaveBeenCalled();
    rerender(<RoomBlockedBanner me="u1" onUnblock={onUnblock} room={room({ blocked_reason: "manual", blocked_detail: { works_stopped: 2, blocked_by_user: by }, my_capabilities: [] })} />);
    const btn = screen.getByTestId("room-banner-unblock");
    expect(btn).toBeDisabled();
    expect(btn.getAttribute("aria-describedby")).toBe("room-banner-unblock-hint");
    expect(screen.getByTestId("room-banner-unblock-hint").textContent).toBe("방장·부방장이나 워크스페이스 소유자·관리자만 이 방을 멈출 수 있습니다");
  });
});
