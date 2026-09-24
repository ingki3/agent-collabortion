/**
 * S21 미션 열기(T-R2-W3, SCREEN §4.7) — 목 서버에 물려 잰다: 정의 한 줄 · goal 필수(비면 「열기」 비활성 + 사유) · 담당 → 종료 조건 문장이
 * 바뀐다(없으면 Director 승인 단독 + 이유) · 검토 승인은 리뷰어 필수 · 동시 미션 상한은 열기 전에 · 예산 「방 한도 $50 중 이 미션에 $20」 ·
 * 메시지에서 열기(인용 · 멘션 하나면 제시 · 원 메시지 귀속) · 보관된 방은 전체 비활성 · 에이전트가 없으면 초대 링크.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));

import { CreateWorkDialog } from "./CreateWorkDialog";
import { setup } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { store } from "@/lib/mock/store";
import { CREATE_WORK } from "@/lib/room-dialogs";
import type { Message } from "@/lib/api/types";
import type { components } from "@/lib/api/schema";

type Work = components["schemas"]["Work"];
let bridge: FetchBridge;
let roomId = "";
beforeEach(async () => {
  ({ bridge, roomId } = await setup());
});
afterEach(() => {
  cleanup();
  bridge.restore();
});
const agent = () => [...store().agents.values()][0];
const invite = () => bridge.call("POST", `/rooms/${roomId}/participants`, { agent_id: agent().id });
const hintOf = (el: HTMLElement) => document.getElementById(el.getAttribute("aria-describedby") ?? "");

describe("S21 미션 열기", () => {
  it("새 미션 — 정의 한 줄 · goal 이 비면 「열기」 비활성 + 사유 · 담당이 없으면 Director 승인 단독 + 이유 · 열면 onOpened", async () => {
    const onOpened = vi.fn();
    render(<CreateWorkDialog roomId={roomId} mode="new" onOpened={onOpened} onClose={() => {}} />);
    expect(screen.getByTestId("rd-create-work-title")).toHaveTextContent(CREATE_WORK.title_new);
    expect(screen.getByTestId("rd-create-work-sub")).toHaveTextContent(CREATE_WORK.definition);
    const open = screen.getByTestId("rd-create-work-open");
    await waitFor(() => expect(open).toHaveAttribute("aria-disabled", "true"));
    expect(hintOf(open)).toHaveTextContent(CREATE_WORK.goal_required);
    expect(screen.getByTestId("rd-create-work-condition-sentence")).toHaveTextContent("Director 승인");
    expect(screen.getByTestId("rd-create-work-condition-why")).toHaveTextContent(CREATE_WORK.condition_default_hint);
    fireEvent.change(screen.getByTestId("rd-create-work-goal"), { target: { value: "결제 실패율 보고서\n표 3개" } });
    expect(open).not.toHaveAttribute("aria-disabled");
    fireEvent.click(open);
    await waitFor(() => expect(onOpened).toHaveBeenCalled());
    const w = onOpened.mock.calls[0][0] as Work;
    expect(w).toMatchObject({ title: "결제 실패율 보고서", status: "active", assignee_agent_id: null });
    expect(w.completion_condition).toEqual({ op: "and", conditions: [{ type: "user_approval" }] });
  });

  it("에이전트가 없으면 담당 자리에 「먼저 초대하세요」 + 참여자 초대 링크(S19)", async () => {
    render(<CreateWorkDialog roomId={roomId} mode="new" onOpened={() => {}} onClose={() => {}} />);
    const none = await screen.findByTestId("rd-create-work-no-agents");
    expect(none).toHaveTextContent(CREATE_WORK.no_agents);
    expect(none.querySelector("a")).toHaveAttribute("href", `/rooms/${roomId}/participants`);
  });

  it("제출자를 고르면 종료 조건 문장이 바뀐다 — 아티팩트 제출(@제출자) 그리고 Director 승인 · 서버도 그 기본값", async () => {
    await invite();
    const onOpened = vi.fn();
    render(<CreateWorkDialog roomId={roomId} mode="new" onOpened={onOpened} onClose={() => {}} />);
    fireEvent.change(await screen.findByTestId("rd-create-work-assignee"), { target: { value: agent().id } });
    expect(screen.getByTestId("rd-create-work-condition-sentence")).toHaveTextContent(`아티팩트 제출 (@${agent().name}) 그리고 Director 승인`);
    expect(screen.queryByTestId("rd-create-work-condition-why")).toBeNull();
    fireEvent.change(screen.getByTestId("rd-create-work-goal"), { target: { value: "번역" } });
    fireEvent.click(screen.getByTestId("rd-create-work-open"));
    await waitFor(() => expect(onOpened).toHaveBeenCalled());
    expect((onOpened.mock.calls[0][0] as Work).completion_condition).toEqual({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }] });
  });

  it("종료 조건 바꾸기 — 「에이전트 검토 승인」은 리뷰어 없이 열 수 없다(비활성 + 방의 말 사유)", async () => {
    await invite();
    render(<CreateWorkDialog roomId={roomId} mode="new" onOpened={() => {}} onClose={() => {}} />);
    fireEvent.change(screen.getByTestId("rd-create-work-goal"), { target: { value: "검토" } });
    fireEvent.click(await screen.findByTestId("rd-create-work-condition-edit"));
    fireEvent.click(screen.getAllByTestId("condition-row").find((r) => r.dataset.type === "agent_approval")!);
    const open = screen.getByTestId("rd-create-work-open");
    expect(open).toHaveAttribute("aria-disabled", "true");
    expect(hintOf(open)).toHaveTextContent(CREATE_WORK.reviewer_required);
    expect(screen.getByTestId("reviewer-required")).toHaveTextContent(CREATE_WORK.reviewer_required); // 편집기 안의 문장도 「세션」이 아니다
  });

  it("동시 미션 상한 — 열기 전에 알린다(수는 슬롯) · 열린 미션 목록 · 방 설정 링크 · 「열기」 비활성", async () => {
    await bridge.call("PATCH", `/rooms/${roomId}`, { limits: { max_concurrent_works: 1, budget_usd: 50 } });
    await bridge.call("POST", `/rooms/${roomId}/works`, { goal: "주간 보고" });
    render(<CreateWorkDialog roomId={roomId} mode="new" onOpened={() => {}} onClose={() => {}} />);
    const cap = await screen.findByTestId("rd-create-work-cap");
    expect(cap).toHaveTextContent(CREATE_WORK.limit_reached.join("1") + CREATE_WORK.limit_cap.join("1"));
    expect(screen.getByTestId("rd-create-work-open-works")).toHaveTextContent("주간 보고");
    expect(cap.querySelector(`a[href="/rooms/${roomId}/settings"]`)).not.toBeNull();
    fireEvent.change(screen.getByTestId("rd-create-work-goal"), { target: { value: "둘째" } });
    expect(screen.getByTestId("rd-create-work-open")).toHaveAttribute("aria-disabled", "true");
    // 예산 줄 — 방 한도 $50 중 이 미션에 $20 · 작은 쪽이 이긴다.
    fireEvent.change(screen.getByTestId("rd-create-work-budget"), { target: { value: "20" } });
    expect(screen.getByTestId("rd-create-work-budget-line")).toHaveTextContent(CREATE_WORK.budget_room.join("$50") + CREATE_WORK.budget_work.join("$20"));
    expect(screen.getByTestId("rd-create-work-budget-line")).toHaveTextContent(CREATE_WORK.smaller_wins);
  });

  it("메시지에서 — 인용된 원 메시지 · goal 기본값 = 본문 · 멘션한 에이전트가 하나면 제시(확인 문장) · 원 메시지가 미션에 귀속", async () => {
    await invite();
    await bridge.call("POST", `/__mock/rooms/${roomId}/seed`, { unread: 1 });
    const m = [...store().messages.values()].find((x) => x.session_id === roomId && x.kind === "system" && x.content.startsWith("안 읽음"))!;
    const message = { ...m, content: "@Lead 결제 실패율을 정리해 줘", mentions: [{ kind: "agent", id: agent().id }] } as Message;
    const onOpened = vi.fn();
    render(<CreateWorkDialog roomId={roomId} mode="from" messageId={m.id} message={message} onOpened={onOpened} onClose={() => {}} />);
    expect(screen.getByTestId("rd-create-work-title")).toHaveTextContent(CREATE_WORK.title_from);
    expect(screen.getByTestId("rd-create-work-quote")).toHaveTextContent("@Lead 결제 실패율을 정리해 줘");
    expect(screen.getByTestId("rd-create-work-goal")).toHaveValue("@Lead 결제 실패율을 정리해 줘");
    await waitFor(() => expect(screen.getByTestId("rd-create-work-assignee")).toHaveValue(agent().id));
    expect(screen.getByTestId("rd-create-work-suggested")).toHaveTextContent(CREATE_WORK.assignee_suggested);
    fireEvent.click(screen.getByTestId("rd-create-work-open"));
    await waitFor(() => expect(onOpened).toHaveBeenCalled());
    expect((onOpened.mock.calls[0][0] as Work).opened_from_message_id).toBe(m.id);
  });

  it("보관된 방 — 「보관된 방에서는 새 미션을 열 수 없습니다」 + 입력 전체 비활성", async () => {
    await bridge.call("POST", `/rooms/${roomId}/archive`);
    render(<CreateWorkDialog roomId={roomId} mode="new" onOpened={() => {}} onClose={() => {}} />);
    await waitFor(() => expect(screen.getByTestId("rd-create-work")).toHaveTextContent(CREATE_WORK.archived));
    expect(hintOf(screen.getByTestId("rd-create-work-open"))).toHaveTextContent(CREATE_WORK.archived);
    expect(screen.getByTestId("rd-create-work-goal")).toBeDisabled();
    expect(screen.getByTestId("rd-create-work-open")).toHaveAttribute("aria-disabled", "true");
  });
});
