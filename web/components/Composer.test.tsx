/**
 * 작성창 — 서버 트리거 미리보기(FR-3.6 · W-6)와 `new_lane` 토글(t-2 · W-3).
 * 로컬 규칙 계산은 없다: 칩은 계약 `TriggerPreview` 를 그대로 그린 것이고, 이 파일은 그 계약을 고정한다.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Composer, type ComposerInput, type ComposerWarning } from "./Composer";
import type { TriggerPreview } from "@/lib/api/types";

type SubmitFn = (i: ComposerInput) => Promise<ComposerWarning[]>;
type PreviewFn = (i: ComposerInput) => Promise<TriggerPreview>;

afterEach(cleanup);

const AGENTS = [
  { id: "a-lead", name: "Lead", participant: true },
  { id: "a-x", name: "X", participant: false },
];

const empty: TriggerPreview = { triggers: [], warnings: [], note_only: false };
const trigger = (over: Partial<TriggerPreview["triggers"][number]> = {}): TriggerPreview["triggers"][number] => ({
  agent_id: "a-lead",
  agent_name: "Lead",
  rule: 2,
  lane: { resolution: 3, lane_id: "lane-1", reentry: false },
  will_queue: false,
  deferred_until: null,
  ...over,
});

function type(value: string) {
  fireEvent.change(screen.getByTestId("composer-input"), { target: { value } });
}

describe("Composer — 트리거 미리보기는 서버가 판정한다(W-6)", () => {
  it("previewTriggers 응답을 그대로 칩으로 그린다 — 규칙 번호·큐잉·재진입까지", async () => {
    const onPreview = vi.fn<PreviewFn>(async () => ({
      ...empty,
      triggers: [trigger({ will_queue: true, lane: { resolution: 3, lane_id: "lane-9", reentry: true }, profile: { id: "p1", name: "default", runtime_kind: "claude_code", model: "claude-sonnet-5" } })],
    }));
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={vi.fn<SubmitFn>(async () => [])} previewDelayMs={0} />);
    type("[@Lead](mention://agent/a-lead) 범위를 좁혀줘");
    const chip = await screen.findByTestId("chip-trigger");
    expect(chip.textContent).toContain("@Lead를 트리거합니다");
    expect(chip.textContent).toContain("명시 멘션(규칙 2)");
    expect(chip.textContent).toContain("실행 중 → 현재 턴 종료 후 처리됩니다"); // U4-1
    expect(chip.textContent).toContain("재진입");
    expect(chip.textContent).toContain("claude-sonnet-5");
    expect(onPreview.mock.calls[0][0]).toMatchObject({ newLane: false, suppressAgentIds: [], parentId: null });
  });

  it("note_only 면 '기록만' 한 줄만, implicit_routing_suppressed 면 '트리거 없음'(규칙 1·3)", async () => {
    const onPreview = vi.fn<PreviewFn>(async (i) =>
      i.content.startsWith("/note") ? { ...empty, note_only: true } : { ...empty, implicit_routing_suppressed: true },
    );
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={vi.fn<SubmitFn>(async () => [])} previewDelayMs={0} />);
    type("/note [@Lead](mention://agent/a-lead) 참고만");
    expect((await screen.findByTestId("chip-note-only")).textContent).toContain("기록만");
    expect(screen.queryByTestId("chip-trigger")).toBeNull();
    type("[@all](mention://all/all) 공지");
    expect((await screen.findByTestId("chip-no-trigger")).textContent).toContain("트리거 없음");
  });

  it("서버 warnings 는 경고 칩으로 — 비참여자 멘션도 전송을 막지 않는다(E1-04)", async () => {
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(
      <Composer
        agents={AGENTS}
        onPreview={vi.fn<PreviewFn>(async () => ({ ...empty, warnings: [{ code: "not_participant", message: "X는 이 세션 참여자가 아닙니다 — 트리거되지 않습니다", agent_id: "a-x" }] }))}
        onSubmit={onSubmit}
        previewDelayMs={0}
      />,
    );
    type("[@X](mention://agent/a-x) 도와줘");
    const warn = await screen.findByTestId("chip-warning");
    expect(warn.getAttribute("data-code")).toBe("not_participant");
    const send = screen.getByTestId("composer-send") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
    fireEvent.click(send);
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
  });

  it("× 로 억제하면 suppress_agent_ids 에 실려 다시 미리보기를 요청한다(FR-3.6)", async () => {
    const onPreview = vi.fn<PreviewFn>(async (i) =>
      i.suppressAgentIds.includes("a-lead") ? empty : { ...empty, triggers: [trigger()] },
    );
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={onSubmit} previewDelayMs={0} />);
    type("[@Lead](mention://agent/a-lead) 인사해줘");
    fireEvent.click(await screen.findByLabelText("@Lead 트리거 억제"));
    expect(screen.getByTestId("chip-suppressed").textContent).toContain("@Lead 트리거 억제됨");
    await waitFor(() => expect(onPreview.mock.calls.at(-1)![0].suppressAgentIds).toEqual(["a-lead"]));
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0].suppressAgentIds).toEqual(["a-lead"]);
  });

  it("미리보기 실패는 조용히 넘어가지 않는다 — 오류 칩", async () => {
    render(<Composer agents={AGENTS} onPreview={vi.fn<PreviewFn>(async () => { throw new Error("서버 오류"); })} onSubmit={vi.fn<SubmitFn>(async () => [])} previewDelayMs={0} />);
    type("hi");
    expect((await screen.findByTestId("chip-preview-error")).textContent).toContain("서버 오류");
  });
});

describe("Composer — new_lane 토글은 전송 후 자동 해제된다 (t-2 · E2-07·E2-14 · W-3)", () => {
  it("켜면 전송 본문에 실리고 상시 표시가 뜬다, 전송하면 해제되어 다음 메시지는 new_lane=false 다", async () => {
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onSubmit={onSubmit} previewDelayMs={0} />);
    const toggle = screen.getByTestId("new-lane-toggle") as HTMLInputElement;

    fireEvent.click(toggle);
    expect(toggle.checked).toBe(true);
    expect(screen.getByTestId("new-lane-note").textContent).toContain("새 작업 줄기로 전송됨");

    type("[@Lead](mention://agent/a-lead) 첫 번째");
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0].newLane).toBe(true);

    // 여기가 핵심 — 해제되지 않으면 이후 모든 멘션이 lane 을 새로 만들어 해소 규칙 3 이 죽는다
    await waitFor(() => expect((screen.getByTestId("new-lane-toggle") as HTMLInputElement).checked).toBe(false));
    expect(screen.queryByTestId("new-lane-note")).toBeNull();

    type("[@Lead](mention://agent/a-lead) 두 번째");
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    expect(onSubmit.mock.calls[1][0].newLane).toBe(false);
  });

  it("토글이 켜져 있으면 미리보기도 new_lane 으로 묻는다 — 칩이 '새 lane' 이라고 말한다", async () => {
    const onPreview = vi.fn<PreviewFn>(async (i) => ({
      ...empty,
      triggers: [trigger(i.newLane ? { lane: { resolution: 4, lane_id: null, reentry: false } } : {})],
    }));
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={vi.fn<SubmitFn>(async () => [])} previewDelayMs={0} />);
    type("[@Lead](mention://agent/a-lead) 별도로");
    await screen.findByTestId("chip-trigger");
    fireEvent.click(screen.getByTestId("new-lane-toggle"));
    await waitFor(() => expect(screen.getByTestId("chip-trigger").textContent).toContain("새 작업 줄기"));
    expect(onPreview.mock.calls.at(-1)![0].newLane).toBe(true);
  });
});

describe("Composer — 멘션 자동완성 · paused 안내", () => {
  // W-15(2026-09-15, Director): 화면에는 `@이름` 만 보이고, 링크는 전송 본문에만 있다.
  it("@ 를 치면 참여자가 먼저 나오고, 선택하면 화면에는 @이름 이 — 전송 본문은 mention:// 링크", async () => {
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={[AGENTS[1], AGENTS[0]]} members={[{ id: "u1", name: "민지" }]} onSubmit={onSubmit} />);
    const ta = screen.getByTestId("composer-input") as HTMLTextAreaElement;
    fireEvent.change(ta, { target: { value: "@", selectionStart: 1 } });
    const opts = screen.getByTestId("mention-menu").querySelectorAll('[role="option"]');
    expect(opts[0].textContent).toContain("@Lead");
    expect(opts[0].textContent).toContain("참여자");
    fireEvent.keyDown(ta, { key: "Enter" });
    expect(ta.value).toBe("@Lead ");
    expect(ta.value).not.toContain("mention://");
    fireEvent.change(ta, { target: { value: "@Lead 범위를 좁혀줘", selectionStart: 12 } });
    fireEvent.keyDown(ta, { key: "Enter", metaKey: true });
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0].content).toBe("[@Lead](mention://agent/a-lead) 범위를 좁혀줘");
  });

  // FR-3.2 의 형식은 `mention://<kind>/<id>` 이고 @all 도 예외가 아니다: `mention://all/all`.
  // `mention://all` 을 넣던 동안 서버는 그것을 멘션으로 읽지 못해 규칙 3(억제) 대신
  // 규칙 6 으로 assignee 를 깨웠다(E1-05 위반). 형식을 여기서 고정한다.
  it("@all 을 고르면 화면에는 @all 이 삽입된다", () => {
    render(<Composer agents={AGENTS} onSubmit={async () => []} />);
    const ta = screen.getByTestId("composer-input") as HTMLTextAreaElement;
    fireEvent.change(ta, { target: { value: "@all", selectionStart: 4 } });
    const opts = screen.getByTestId("mention-menu").querySelectorAll('[role="option"]');
    expect(opts.length).toBeGreaterThan(0);
    expect(opts[0].textContent).toContain("@all");
    fireEvent.keyDown(ta, { key: "Enter" });
    expect(ta.value).toBe("@all ");
  });
  it("@all 의 전송 본문은 mention://all/all 이다 (FR-3.2 · E1-05)", async () => {
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onSubmit={onSubmit} />);
    const ta = screen.getByTestId("composer-input") as HTMLTextAreaElement;
    fireEvent.change(ta, { target: { value: "@all 공지", selectionStart: 7 } });
    fireEvent.keyDown(ta, { key: "Enter", metaKey: true });
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0].content).toBe("[@all](mention://all/all) 공지");
  });

  it("세션이 paused 면 작성창 위에 '재개 후 처리됩니다' 안내가 뜬다(U15-9)", () => {
    render(<Composer agents={AGENTS} onSubmit={async () => []} notice="일시정지 중 — 게시는 되지만 재개 후 처리됩니다" />);
    expect(screen.getByTestId("composer-notice").textContent).toContain("재개 후 처리됩니다");
  });
});

// ── v0.19 방 화면(T-R2-W2) — 미션 선택기와 귀속 칩(COMPONENTS §9.2 · SCREEN §4.6 귀속 규칙 1~4) ──
describe("Composer — 미션 선택기 · 귀속 칩은 서버 미리보기(work · work_source)를 그대로 말한다", () => {
  const OPTS = [{ id: "w1", title: "보고서 초안" }, { id: "w2", title: "수수료 비교" }];
  const sel = (value: string | null, onChange = vi.fn()) => ({ options: OPTS, value, onChange });

  it("자동 귀속(규칙 3 running_lane) — 「자동: 」 접두로 사람이 고른 것과 갈리고, 선택기가 그 미션으로 바뀐다", async () => {
    const onPreview = vi.fn<PreviewFn>(async () => ({ ...empty, triggers: [trigger()], work: { id: "w2", title: "수수료 비교" }, work_source: "running_lane" }));
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={vi.fn<SubmitFn>(async () => [])} previewDelayMs={0} workSelector={sel(null)} />);
    // 미리보기 전 — (전체) 보기의 기본값은 「미션 없음」
    expect(screen.getByTestId("chip-work").textContent).toBe("미션 없음에 들어갑니다");
    type("[@Lead](mention://agent/a-lead) 이어서");
    await waitFor(() => expect(screen.getByTestId("work-selector").getAttribute("data-mode")).toBe("auto"));
    const chip = screen.getByTestId("chip-work");
    expect(chip.textContent).toBe("자동: 이 메시지는 미션 「수수료 비교」에 들어갑니다 — 바꾸려면 선택기를 누르세요");
    expect(screen.getByTestId("chip-work-auto").textContent).toBe("자동: ");
    expect((screen.getByTestId("work-selector-select") as HTMLSelectElement).value).toBe("w2");
    // 선택기의 값(null)은 그대로 보낸다 — null 은 「규칙 2~4 로 정해 달라」(서버 router.attribute)
    expect(onPreview.mock.calls.at(-1)![0].workId).toBeNull();
  });

  it("사람이 고른 미션(규칙 1 chosen) — 접두 없이 「이 메시지는 미션 〈…〉에 들어갑니다」, 미리보기·전송에 work_id 를 싣는다", async () => {
    const onPreview = vi.fn<PreviewFn>(async () => ({ ...empty, work: { id: "w1", title: "보고서 초안" }, work_source: "chosen" }));
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={onSubmit} previewDelayMs={0} workSelector={sel("w1")} />);
    type("초안 방향 잡자");
    await waitFor(() => expect(screen.getByTestId("chip-work").getAttribute("data-source")).toBe("chosen"));
    expect(screen.getByTestId("chip-work").textContent).toBe("이 메시지는 미션 「보고서 초안」에 들어갑니다");
    expect(screen.queryByTestId("chip-work-auto")).toBeNull();
    expect(onPreview.mock.calls.at(-1)![0].workId).toBe("w1");
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0].workId).toBe("w1");
  });

  it("스레드 안(규칙 2 thread) — 선택기가 잠기고 사유는 글자로(「이 스레드는 미션 〈…〉의 것입니다」)", async () => {
    const onPreview = vi.fn<PreviewFn>(async () => ({ ...empty, work: { id: "w1", title: "보고서 초안" }, work_source: "thread" }));
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={vi.fn<SubmitFn>(async () => [])} previewDelayMs={0} replyTo={{ id: "m1", authorName: "서연" }} workSelector={sel(null)} />);
    type("답글");
    await waitFor(() => expect(screen.getByTestId("work-selector").getAttribute("data-mode")).toBe("locked"));
    const select = screen.getByTestId("work-selector-select") as HTMLSelectElement;
    expect(select.disabled).toBe(true);
    expect(select.getAttribute("aria-describedby")).toBe("work-selector-locked");
    expect(screen.getByTestId("chip-work").textContent).toBe("이 스레드는 미션 「보고서 초안」의 것입니다");
  });

  it("선택기를 바꾸면 onChange 로 올린다(칩 기본값을 사람이 덮는다)", () => {
    const onChange = vi.fn();
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} workSelector={sel(null, onChange)} />);
    fireEvent.change(screen.getByTestId("work-selector-select"), { target: { value: "w2" } });
    expect(onChange).toHaveBeenCalledWith("w2");
    fireEvent.change(screen.getByTestId("work-selector-select"), { target: { value: "" } });
    expect(onChange).toHaveBeenLastCalledWith(null);
  });

  it("선택기가 없으면(옛 S7) 그리지 않고 work_id 키도 보내지 않는다", async () => {
    const onPreview = vi.fn<PreviewFn>(async () => empty);
    render(<Composer agents={AGENTS} onPreview={onPreview} onSubmit={vi.fn<SubmitFn>(async () => [])} previewDelayMs={0} />);
    type("hi");
    await waitFor(() => expect(onPreview).toHaveBeenCalled());
    expect(screen.queryByTestId("work-selector")).toBeNull();
    expect(onPreview.mock.calls[0][0].workId).toBeUndefined();
  });
});
