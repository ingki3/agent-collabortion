import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Composer, classifyMentions, type ComposerWarning } from "./Composer";

type SubmitFn = (i: { content: string; parentId: string | null; suppressAgentIds: string[] }) => Promise<ComposerWarning[]>;

afterEach(cleanup);

const AGENTS = [
  { id: "a-lead", name: "Lead", participant: true },
  { id: "a-x", name: "X", participant: false },
];

describe("classifyMentions — FR-3.3 규칙 2 / E1-04", () => {
  it("참여자 멘션은 트리거, 비참여자 멘션은 경고", () => {
    const r = classifyMentions("[@Lead](mention://agent/a-lead) [@X](mention://agent/a-x) 도와줘", AGENTS);
    expect(r.triggers.map((a) => a.name)).toEqual(["Lead"]);
    expect(r.nonParticipants.map((m) => m.name)).toEqual(["X"]);
  });
  it("사람 멘션·@all 은 트리거도 경고도 아니다 — 규칙 3(no_trigger)", () => {
    const r = classifyMentions("[@민지](mention://user/u1) [@all](mention://all)", AGENTS);
    expect(r.rule).toBe("no_trigger");
    expect(r.triggers).toEqual([]);
    expect(r.nonParticipants).toEqual([]);
  });
  it("/note 접두(앞 공백 무시)는 참여자를 멘션해도 트리거 0개 — 규칙 1이 규칙 2보다 먼저", () => {
    const r = classifyMentions("  /note [@Lead](mention://agent/a-lead) 참고만 해", AGENTS);
    expect(r.rule).toBe("note");
    expect(r.triggers).toEqual([]);
    expect(r.nonParticipants).toEqual([]);
    expect(classifyMentions("/notes [@Lead](mention://agent/a-lead)", AGENTS).rule).toBe("mention");
    expect(classifyMentions("hi", AGENTS).rule).toBe("implicit");
  });
});

describe("Composer — 트리거 미리보기 규칙 1·3 (R2)", () => {
  it("'/note @Lead' 는 트리거 칩이 사라지고 '기록만' 한 줄만 보인다(규칙 1)", () => {
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} />);
    const ta = screen.getByTestId("composer-input");
    fireEvent.change(ta, { target: { value: "[@Lead](mention://agent/a-lead) 인사해줘" } });
    expect(screen.getByTestId("chip-trigger")).not.toBeNull();
    fireEvent.change(ta, { target: { value: "/note [@Lead](mention://agent/a-lead) [@X](mention://agent/a-x) 참고만 해" } });
    expect(screen.queryByTestId("chip-trigger")).toBeNull();
    expect(screen.queryByTestId("chip-not-participant")).toBeNull();
    expect(screen.getByTestId("chip-note-only").textContent).toContain("기록만");
    expect(screen.getByTestId("composer-chips").getAttribute("data-rule")).toBe("note");
  });

  it("@all·사람만 멘션이면 비참여자 경고 없이 '트리거 없음' 칩(규칙 3)", () => {
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "[@all](mention://all) [@민지](mention://user/u1) 공지" } });
    expect(screen.queryByTestId("chip-trigger")).toBeNull();
    expect(screen.queryByTestId("chip-not-participant")).toBeNull();
    expect(screen.getByTestId("chip-no-trigger").textContent).toContain("트리거 없음");
  });
});

describe("Composer — 비참여자 경고 칩", () => {
  it("비참여 에이전트를 멘션하면 경고 칩 'X는 이 세션 참여자가 아님' 이 보이고 전송은 막지 않는다", async () => {
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onSubmit={onSubmit} />);
    const ta = screen.getByTestId("composer-input") as HTMLTextAreaElement;
    fireEvent.change(ta, { target: { value: "[@X](mention://agent/a-x) 도와줘" } });
    const chip = screen.getByTestId("chip-not-participant");
    expect(chip.textContent).toContain("X는 이 세션 참여자가 아님");
    expect(screen.queryByTestId("chip-trigger")).toBeNull();
    const send = screen.getByTestId("composer-send") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
    fireEvent.click(send);
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0]).toMatchObject({ content: "[@X](mention://agent/a-x) 도와줘", parentId: null, suppressAgentIds: [] });
  });

  it("참여자 멘션은 트리거 칩, × 로 억제하면 suppress_agent_ids 에 들어간다 (FR-3.6)", async () => {
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onSubmit={onSubmit} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "[@Lead](mention://agent/a-lead) 인사해줘" } });
    expect(screen.getByTestId("chip-trigger").textContent).toContain("@Lead를 트리거합니다");
    fireEvent.click(screen.getByLabelText("@Lead 트리거 억제"));
    expect(screen.getByTestId("chip-suppressed")).not.toBeNull();
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0].suppressAgentIds).toEqual(["a-lead"]);
  });

  it("서버가 돌려준 warnings 는 전송 후 칩으로 남는다 (E1-04 서버 판정)", async () => {
    const onSubmit = vi.fn<SubmitFn>(async () => [{ code: "not_participant", message: "X는 이 세션 참여자가 아님", agent_id: "a-x" }]);
    render(<Composer agents={AGENTS} onSubmit={onSubmit} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "hi" } });
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(screen.getByTestId("chip-server-warning").textContent).toContain("X는 이 세션 참여자가 아님"));
    expect((screen.getByTestId("composer-input") as HTMLTextAreaElement).value).toBe("");
  });

  it("@ 를 치면 자동완성 메뉴가 열리고 참여자가 먼저, 선택하면 mention:// 링크가 삽입된다", () => {
    render(<Composer agents={[AGENTS[1], AGENTS[0]]} members={[{ id: "u1", name: "민지" }]} onSubmit={async () => []} />);
    const ta = screen.getByTestId("composer-input") as HTMLTextAreaElement;
    fireEvent.change(ta, { target: { value: "@", selectionStart: 1 } });
    const menu = screen.getByTestId("mention-menu");
    const opts = menu.querySelectorAll('[role="option"]');
    expect(opts[0].textContent).toContain("@Lead");
    expect(opts[0].textContent).toContain("참여자");
    fireEvent.keyDown(ta, { key: "Enter" });
    expect(ta.value).toBe("[@Lead](mention://agent/a-lead) ");
    expect(screen.getByTestId("chip-trigger")).not.toBeNull();
  });
});
