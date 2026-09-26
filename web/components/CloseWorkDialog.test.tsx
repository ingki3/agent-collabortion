/**
 * 미션 닫기 확인 — D8 B(Director 2026-09-26): 닫아도 폴더는 바로 지우지 않는다. 확인 창이 그 사실과 기한을 말한다.
 */
import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

const get = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { get: (...a: unknown[]) => get(...a) } };
});

import { CloseWorkDialog } from "./CloseWorkDialog";

const MB = 1024 ** 2;

beforeEach(() => {
  get.mockReset();
  get.mockImplementation((path: string, opts: { query?: { work_id?: string } }) => {
    if (path.includes("workdirs")) {
      expect(opts.query?.work_id).toBe("wk1");
      return Promise.resolve({ items: [{ id: "a" }, { id: "b" }, { id: "c" }], next_cursor: null, disk_bytes_total: 12 * MB, disk_quota_gb: null });
    }
    return Promise.resolve({ workdir_retention_days: 7 });
  });
});
afterEach(cleanup);

describe("CloseWorkDialog", () => {
  it("이 미션의 폴더 수·용량과 보존 기한을 한 줄로 — 「〈retention〉일 뒤 정리됩니다」", async () => {
    render(<CloseWorkDialog workId="wk1" runtimeId="r1" workspaceId="w1" busy={false} onConfirm={vi.fn()} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("close-work-folders")).toBeTruthy());
    expect(screen.getByTestId("close-work-folders").textContent).toBe(
      "이 미션의 작업 폴더 3개(12MB)는 7일 뒤 정리됩니다 — 남길 것은 아티팩트로 제출하세요",
    );
  });

  it("폴더가 0개면 줄이 없다 · 확인은 onConfirm", async () => {
    get.mockImplementation((path: string) =>
      path.includes("workdirs")
        ? Promise.resolve({ items: [], next_cursor: null, disk_bytes_total: 0, disk_quota_gb: null })
        : Promise.resolve({ workdir_retention_days: 14 }));
    const onConfirm = vi.fn();
    render(<CloseWorkDialog workId="wk1" runtimeId="r1" workspaceId="w1" busy={false} onConfirm={onConfirm} onClose={vi.fn()} />);
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId("close-work-folders")).toBeNull();
    fireEvent.click(screen.getByTestId("close-work-dialog-confirm"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("방에 컴퓨터가 없으면 폴더를 묻지 않는다", async () => {
    render(<CloseWorkDialog workId="wk1" runtimeId={null} workspaceId="w1" busy={false} onConfirm={vi.fn()} onClose={vi.fn()} />);
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
    expect(get.mock.calls[0][0]).toContain("settings");
    expect(screen.queryByTestId("close-work-folders")).toBeNull();
  });
});
