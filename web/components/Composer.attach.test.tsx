/**
 * 작성창 파일 붙이기(SCREEN v0.19.12 §4.6 「파일 붙이기」 표 · COMPONENTS §9.12 Attachment Chip · PRD FR-3.7).
 *
 * 회귀 주입(끄면 그 괄호가 FAIL):
 *   고르는 즉시 올리지 않음 → (upload-now)
 *   pending 인데 보내기 활성 → (send-blocked)
 *   50MB 검사를 지움 → (too-big)
 *   10개 한도를 지움 → (limit)
 *   재시도를 지움 → (retry)
 *   첨부만 보내기의 「(파일 N개)」를 지움 → (only-files)
 *   트리거 칩 꼬리를 지움 → (trigger-chip)
 *   disabled 인데 📎 활성 → (permission)
 *   붙여넣기·끌어다 놓기를 지움 → (paste)(drop)
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Composer, type ComposerInput, type ComposerWarning } from "./Composer";
import type { TriggerPreview } from "@/lib/api/types";
import type { UploadResult } from "@/lib/media";

afterEach(cleanup);

type SubmitFn = (i: ComposerInput) => Promise<ComposerWarning[]>;
type PreviewFn = (i: ComposerInput) => Promise<TriggerPreview>;
const AGENTS = [{ id: "a-lead", name: "Lead", participant: true }];

/** 손으로 끝내는 업로드 — 테스트가 진행·성공·실패를 정한다. */
function manualUploads() {
  const calls: { file: File; progress: (r: number) => void; resolve: (r: UploadResult) => void; reject: (e: Error) => void }[] = [];
  const onUpload = vi.fn((file: File, progress: (r: number) => void) =>
    new Promise<UploadResult>((resolve, reject) => calls.push({ file, progress, resolve, reject })));
  const done = (i: number, id = `art-${i}`) =>
    act(async () => calls[i].resolve({ id, name: calls[i].file.name, version: 1, content_type: calls[i].file.type || "application/octet-stream", size_bytes: calls[i].file.size }));
  return { calls, onUpload, done };
}

const file = (name: string, size = 10, type = "image/png") => {
  const f = new File([new Uint8Array(Math.min(size, 16))], name, { type });
  if (size > 16) Object.defineProperty(f, "size", { value: size });
  return f;
};
const pick = (...files: File[]) => fireEvent.change(screen.getByTestId("composer-file-input"), { target: { files } });
const flush = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });

describe("Composer — 파일 붙이기", () => {
  it("📎 는 aria-label 「파일 붙이기」 버튼 + 숨은 input[type=file multiple]; onUpload 가 없으면 그리지 않는다", () => {
    const { rerender } = render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={vi.fn()} />);
    const btn = screen.getByTestId("composer-attach");
    expect(btn.tagName).toBe("BUTTON");
    expect(btn.getAttribute("aria-label")).toBe("파일 붙이기");
    const input = screen.getByTestId("composer-file-input") as HTMLInputElement;
    expect(input.type).toBe("file");
    expect(input.multiple).toBe(true);
    rerender(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} />);
    expect(screen.queryByTestId("composer-attach")).toBeNull();
  });

  it("(upload-now)(send-blocked) 고르는 즉시 올리고 진행 막대, 다 올라가기 전 보내기 비활성 + 「파일을 올리는 중입니다」", async () => {
    const up = manualUploads();
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={up.onUpload} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "이 사진처럼" } });
    pick(file("ae86.jpg", 2000, "image/jpeg"), file("bgm-ref.mp3", 3_250_585, "audio/mpeg"));
    await flush();
    expect(up.onUpload).toHaveBeenCalledTimes(2);
    expect(screen.getAllByTestId("attach-chip")).toHaveLength(2);
    act(() => up.calls[0].progress(0.4));
    const bar = screen.getAllByTestId("attach-progress")[0];
    expect(bar.getAttribute("role")).toBe("progressbar");
    expect(bar.getAttribute("aria-valuenow")).toBe("40");
    expect((screen.getByTestId("composer-send") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("attach-pending").textContent).toBe("파일을 올리는 중입니다");
    await up.done(0);
    expect((screen.getByTestId("composer-send") as HTMLButtonElement).disabled).toBe(true);
    await up.done(1);
    expect((screen.getByTestId("composer-send") as HTMLButtonElement).disabled).toBe(false);
    expect(screen.queryByTestId("attach-pending")).toBeNull();
    // ✕ 는 「〈이름〉 빼기」
    expect(screen.getAllByTestId("attach-remove")[1].getAttribute("aria-label")).toBe("bgm-ref.mp3 빼기");
  });

  it("(only-files) 본문 없이 첨부만 — content 「(파일 N개)」 · attachment_ids 는 고른 순서, 보낸 뒤 칩이 비워진다", async () => {
    const up = manualUploads();
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onSubmit={onSubmit} onUpload={up.onUpload} />);
    pick(file("a.png"), file("b.png"));
    await flush();
    await up.done(0, "A");
    await up.done(1, "B");
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0]).toMatchObject({ content: "(파일 2개)", attachmentIds: ["A", "B"] });
    await waitFor(() => expect(screen.queryByTestId("attach-chips")).toBeNull());
  });

  it("✕ 로 뺀 칩은 보내지 않는다", async () => {
    const up = manualUploads();
    const onSubmit = vi.fn<SubmitFn>(async () => []);
    render(<Composer agents={AGENTS} onSubmit={onSubmit} onUpload={up.onUpload} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "하나만" } });
    pick(file("a.png"), file("b.png"));
    await flush();
    await up.done(0, "A");
    await up.done(1, "B");
    fireEvent.click(screen.getAllByTestId("attach-remove")[0]);
    fireEvent.click(screen.getByTestId("composer-send"));
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0]).toMatchObject({ content: "하나만", attachmentIds: ["B"] });
  });

  it("(too-big) 50MB 넘는 칩은 빨간 줄 「50MB 를 넘습니다」 — 올리지 않고, 뺄 때까지 보내기 비활성", async () => {
    const up = manualUploads();
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={up.onUpload} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "영상" } });
    pick(file("big.mp4", 50 * 1024 * 1024 + 1, "video/mp4"));
    await flush();
    expect(up.onUpload).not.toHaveBeenCalled();
    expect(screen.getByTestId("attach-too-big").textContent).toBe("50MB 를 넘습니다");
    expect(screen.getByTestId("attach-chip").dataset.state).toBe("error");
    expect((screen.getByTestId("composer-send") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("attach-remove"));
    expect((screen.getByTestId("composer-send") as HTMLButtonElement).disabled).toBe(false);
  });

  it("(limit) 10개까지 — 넘는 것은 받지 않고 한 줄, 📎 비활성", async () => {
    const up = manualUploads();
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={up.onUpload} />);
    pick(...Array.from({ length: 12 }, (_, i) => file(`f${i}.png`)));
    await flush();
    expect(screen.getAllByTestId("attach-chip")).toHaveLength(10);
    expect(up.onUpload).toHaveBeenCalledTimes(10);
    expect(screen.getByTestId("attach-limit").textContent).toBe("한 메시지에 파일은 10개까지 붙일 수 있습니다");
    expect((screen.getByTestId("composer-attach") as HTMLButtonElement).disabled).toBe(true);
  });

  it("(retry) 실패한 칩에 서버 문장 + 「다시 시도」 — 나머지는 그대로, 다시 올라가면 보낼 수 있다", async () => {
    const up = manualUploads();
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={up.onUpload} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "x" } });
    pick(file("a.png"), file("b.png"));
    await flush();
    await act(async () => up.calls[0].reject(new Error("파일이 너무 큽니다")));
    await up.done(1);
    expect(screen.getByTestId("attach-error").textContent).toBe("파일이 너무 큽니다");
    expect(screen.getAllByTestId("attach-chip").map((c) => c.dataset.state)).toEqual(["error", "done"]);
    expect((screen.getByTestId("composer-send") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("attach-retry"));
    await flush();
    expect(up.onUpload).toHaveBeenCalledTimes(3);
    await up.done(2);
    expect((screen.getByTestId("composer-send") as HTMLButtonElement).disabled).toBe(false);
  });

  it("(trigger-chip) 트리거 미리보기 칩 끝에 「 · 파일 N개를 함께 받습니다」, 미리보기 요청에 attachmentIds", async () => {
    const up = manualUploads();
    const onPreview = vi.fn<PreviewFn>(async () => ({ triggers: [{ agent_id: "a-lead", agent_name: "Lead", rule: 2, lane: { resolution: 3, lane_id: "l", reentry: false }, will_queue: false, deferred_until: null }], warnings: [], note_only: false }));
    render(<Composer agents={AGENTS} onPreview={onPreview} previewDelayMs={0} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={up.onUpload} />);
    fireEvent.change(screen.getByTestId("composer-input"), { target: { value: "[@Lead](mention://agent/a-lead) 봐 주세요" } });
    pick(file("a.png"), file("b.png"));
    await flush();
    await up.done(0);
    await up.done(1);
    await waitFor(() => expect(screen.getByTestId("chip-trigger-files").textContent).toBe(" · 파일 2개를 함께 받습니다"));
    expect(onPreview.mock.calls.at(-1)![0].attachmentIds).toEqual(["art-0", "art-1"]);
  });

  it("(permission) 보관·감사 열람(disabled)이면 📎 도 비활성이고 붙여넣기·놓기를 받지 않는다", async () => {
    const onUpload = vi.fn();
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={onUpload} disabled disabledReason="보관된 방" />);
    expect((screen.getByTestId("composer-attach") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.paste(screen.getByTestId("composer-input"), { clipboardData: { files: [file("p.png")] } });
    fireEvent.drop(screen.getByTestId("composer"), { dataTransfer: { files: [file("d.png")], types: ["Files"] } });
    await flush();
    expect(onUpload).not.toHaveBeenCalled();
  });

  it("(paste)(drop) 붙여넣기(클립보드 이미지)와 끌어다 놓기(점선 + 「여기에 놓으면 붙습니다」)가 같은 동작", async () => {
    const up = manualUploads();
    render(<Composer agents={AGENTS} onSubmit={vi.fn<SubmitFn>(async () => [])} onUpload={up.onUpload} />);
    fireEvent.paste(screen.getByTestId("composer-input"), { clipboardData: { files: [file("clip.png")] } });
    await flush();
    expect(up.onUpload).toHaveBeenCalledTimes(1);
    const box = screen.getByTestId("composer");
    fireEvent.dragOver(box, { dataTransfer: { files: [], types: ["Files"] } });
    expect(box.dataset.dragging).toBe("true");
    expect(screen.getByTestId("composer-drop").textContent).toBe("여기에 놓으면 붙습니다");
    fireEvent.drop(box, { dataTransfer: { files: [file("drop.mp3", 20, "audio/mpeg")], types: ["Files"] } });
    await flush();
    expect(box.dataset.dragging).toBeUndefined();
    expect(up.onUpload).toHaveBeenCalledTimes(2);
    expect(screen.getAllByTestId("attach-chip")).toHaveLength(2);
  });
});
