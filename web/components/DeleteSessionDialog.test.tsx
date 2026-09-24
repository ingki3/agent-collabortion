/**
 * 세션 삭제 확인 다이얼로그(T-W13, SCREEN §5) — 문장(제목에 세션 이름·사라지는 것·되돌릴 수 없음), 「삭제」 위험 색·「취소」 기본 초점,
 * 204 → onDeleted, 404 → onDeleted(이미 없음), 409 workdir_unmerged → Problem.workdirs[] 목록 + S13 링크, 409 session_active → 서버 문장 그대로.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Workdir } from "@/lib/api/types";

const del = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { ...actual.api, delete: (...a: unknown[]) => del(...a) } };
});

import { problemFixture } from "@/lib/mock/problem-fixture";
import { DeleteSessionDialog, workdirsHref } from "./DeleteSessionDialog";
import { DELETE_DIALOG } from "@/lib/wording";

const session = { id: "s1", title: "결제 시장 조사", runtime_id: "r1" };
const wd = (over: Partial<Workdir>): Workdir => ({
  id: "w1", session_id: "s1", agent_id: null, lane_id: null, kind: "worktree", path_or_ref: "~/dev/app-worktrees/colab-S-frontend", branch: "colab/S/frontend",
  status: "retained", disk_bytes: 1, last_used_at: null, retain_until: null, dirty: false, merged: false, commits_ahead: 3, gc_blocked_reason: "unmerged_commits",
  created_at: "2026-09-14T00:00:00Z", updated_at: "2026-09-14T00:00:00Z", ...over,
});
const problem = (status: number, code: string, detail: string, extra: Record<string, unknown> = {}) => problemFixture(code, status, { detail, extra });

beforeEach(() => del.mockReset());
afterEach(cleanup);

function mount() {
  const onDeleted = vi.fn();
  const onClose = vi.fn();
  render(<DeleteSessionDialog session={session} onDeleted={onDeleted} onClose={onClose} />);
  return { onDeleted, onClose };
}

describe("문장 (SCREEN §5 — 무엇이 사라지는지, 되돌릴 수 없으면 그렇다고)", () => {
  it("제목에 세션 이름, 본문에 메시지·작업 줄기·아티팩트·비용 기록 + 되돌릴 수 없음 + 이 컴퓨터의 작업 폴더", () => {
    mount();
    const dlg = screen.getByRole("alertdialog");
    expect(dlg.getAttribute("aria-modal")).toBe("true");
    expect(screen.getByTestId("delete-session-title").textContent).toBe("「결제 시장 조사」 방을 삭제할까요?");
    expect(dlg.textContent).toContain("메시지 · 서브 미션 · 아티팩트 · 비용 기록");
    expect(dlg.textContent).toContain("되돌릴 수 없습니다");
    expect(dlg.textContent).toContain("이 컴퓨터의 작업 폴더도 정리됩니다");
    expect(screen.getByTestId("delete-session-confirm").textContent).toBe("삭제");
    expect(screen.getByTestId("delete-session-cancel").textContent).toBe("취소");
  });

  it("「삭제」 는 위험 색 클래스, 기본 초점은 「취소」", () => {
    mount();
    expect(screen.getByTestId("delete-session-confirm").className).toContain("del-session__danger");
    expect(document.activeElement).toBe(screen.getByTestId("delete-session-cancel"));
  });

  it("취소·Esc·바깥 클릭은 onClose 만(삭제 없음)", () => {
    const { onClose, onDeleted } = mount();
    fireEvent.click(screen.getByTestId("delete-session-cancel"));
    fireEvent.keyDown(screen.getByRole("alertdialog"), { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(onDeleted).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
  });
});

describe("서버 응답 경로", () => {
  it("204 — DELETE /sessions/{sessionId} 를 부르고 onDeleted(id) → onClose", async () => {
    del.mockResolvedValueOnce(undefined);
    const { onDeleted, onClose } = mount();
    fireEvent.click(screen.getByTestId("delete-session-confirm"));
    await waitFor(() => expect(onDeleted).toHaveBeenCalledWith("s1"));
    expect(del).toHaveBeenCalledWith("/sessions/{sessionId}", { path: { sessionId: "s1" } });
    expect(onClose).toHaveBeenCalled();
  });

  it("404(이미 없음 — 계약: 두 번째 호출) — 카드는 빠져야 하므로 onDeleted", async () => {
    del.mockRejectedValueOnce(problem(404, "not_found", "방을 찾을 수 없습니다"));
    const { onDeleted } = mount();
    fireEvent.click(screen.getByTestId("delete-session-confirm"));
    await waitFor(() => expect(onDeleted).toHaveBeenCalledWith("s1"));
  });

  it("409 session_active — 서버 문장 그대로 다이얼로그 안에, 다이얼로그는 남는다", async () => {
    del.mockRejectedValueOnce(problem(409, "session_active", "진행 중인 미션은 먼저 종료하세요"));
    const { onDeleted, onClose } = mount();
    fireEvent.click(screen.getByTestId("delete-session-confirm"));
    await waitFor(() => expect(screen.getByTestId("delete-session-error").textContent).toBe("진행 중인 미션은 먼저 종료하세요"));
    expect(screen.queryByTestId("delete-session-workdirs")).toBeNull();
    expect(onDeleted).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("409 workdir_unmerged — Problem.workdirs[] 를 경로·브랜치·사유로 나열하고 「작업 폴더 관리」(S13) 링크", async () => {
    del.mockRejectedValueOnce(
      problem(409, "workdir_unmerged", "미병합 커밋이나 미커밋 변경이 남은 작업 폴더가 있어 삭제할 수 없습니다 — 먼저 병합하거나 정리해 주세요", {
        workdirs: [wd({}), wd({ id: "w2", path_or_ref: "~/dev/app-worktrees/colab-S-qa", branch: "colab/S/qa", dirty: true, commits_ahead: 0, gc_blocked_reason: "uncommitted_changes" })],
      }),
    );
    mount();
    fireEvent.click(screen.getByTestId("delete-session-confirm"));
    await waitFor(() => expect(screen.getByTestId("delete-session-workdirs")).toBeTruthy());
    // 서버 문장도 그대로 보인다 — 화면이 바꿔 말하지 않는다.
    expect(screen.getByTestId("delete-session-error").textContent).toContain("미병합 커밋이나 미커밋 변경이 남은 작업 폴더가 있어");
    const rows = screen.getAllByTestId("delete-session-workdir").map((r) => r.textContent);
    expect(rows).toEqual([
      "~/dev/app-worktrees/colab-S-frontend · colab/S/frontend · 미병합 커밋 3개",
      "~/dev/app-worktrees/colab-S-qa · colab/S/qa · 미커밋 변경",
    ]);
    expect(screen.getByTestId("delete-session-workdirs").textContent).toContain(DELETE_DIALOG.workdirs_head);
    const link = screen.getByTestId("delete-session-workdirs-link");
    expect(link.textContent).toBe("작업 폴더 관리");
    expect(link.getAttribute("href")).toBe("/runtimes/r1/workdirs");
  });

  it("workdirsHref — 세션의 컴퓨터가 없으면 컴퓨터 목록으로", () => {
    expect(workdirsHref("r1")).toBe("/runtimes/r1/workdirs");
    expect(workdirsHref(null)).toBe("/runtimes");
    expect(workdirsHref(undefined)).toBe("/runtimes");
  });

  it("삭제 중에는 두 버튼이 잠기고 「삭제 중…」", async () => {
    let resolve: () => void = () => {};
    del.mockReturnValueOnce(new Promise<void>((r) => { resolve = r; }));
    mount();
    fireEvent.click(screen.getByTestId("delete-session-confirm"));
    await waitFor(() => expect(screen.getByTestId("delete-session-confirm").textContent).toBe(DELETE_DIALOG.busy));
    expect((screen.getByTestId("delete-session-cancel") as HTMLButtonElement).disabled).toBe(true);
    resolve();
    await waitFor(() => expect((screen.getByTestId("delete-session-cancel") as HTMLButtonElement).disabled).toBe(false));
  });
});
