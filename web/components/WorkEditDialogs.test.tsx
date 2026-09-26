/**
 * 미션 칸의 편집 동작(T-R2-W4b, SCREEN v0.19.2 §4.6 우열 (가) 「미션 동작」 · §2.3 미션 층) — 목 서버에 물려 잰다.
 *  - 설정 편집 = S21 폼의 편집 모드: 지금 값으로 채워 열고 **바꾼 칸만** updateWork 로 보낸다 · Director 는 여기서 안 바꾼다 · director 가 아니면 비활성 + 사유.
 *  - Director 교체(changeWorkDirector): 새 Director 를 고르면 켜진다 · 시스템 메시지 · 권한 없는 사람에게는 서버 문장 그대로.
 *  - 조건 고치기: 막힌 조건(리뷰어 없음)으로 시작 → 같은 편집기에서 리뷰어를 고르면 저장 → 진행률의 막힘이 풀린다.
 *  - 우열 버튼: 권한 층 둘(설정 편집 = director / Director 교체 = director · ws owner·admin) · 막힌 조건의 「조건 고치기」.
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
import { ChangeDirectorDialog, FixWorkConditionDialog } from "./WorkEditDialogs";
import { WorkPanel } from "./WorkPanel";
import { setup, uid } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { store } from "@/lib/mock/store";
import { WE_SERVER } from "@/lib/mock/work-edit";
import { CREATE_WORK } from "@/lib/room-dialogs";
import { WORK_PANEL } from "@/lib/wording";
import { CHANGE_DIRECTOR, WORK_EDIT } from "@/lib/work-edit";
import type { Member, Message } from "@/lib/api/types";
import type { components } from "@/lib/api/schema";

type Work = components["schemas"]["Work"];
let bridge: FetchBridge;
let roomId = "";
let as: (email: string) => Promise<void>;
beforeEach(async () => {
  ({ bridge, roomId, as } = await setup());
});
afterEach(() => {
  cleanup();
  bridge.restore();
});
const agents = () => [...store().agents.values()];
const hintOf = (el: HTMLElement) => document.getElementById(el.getAttribute("aria-describedby") ?? "");
async function openWork(extra: Record<string, unknown> = {}): Promise<Work> {
  await bridge.call("POST", `/rooms/${roomId}/participants`, { agent_id: agents()[0].id });
  return (await bridge.call<Work>("POST", `/rooms/${roomId}/works`, { goal: "결제 실패율 보고서", assignee_agent_id: agents()[0].id, limits: { budget_usd: 20 }, ...extra })).body;
}
const getWork = async (id: string) => (await bridge.call<Work>("GET", `/works/${id}`)).body;
/** 화면이 fetch 로 보낸 마지막 PATCH·PUT 본문. */
const lastBody = (method: string) => {
  const calls = (globalThis.fetch as unknown as { mock: { calls: [string, RequestInit | undefined][] } }).mock.calls.filter(([, i]) => i?.method === method);
  return JSON.parse(String(calls.at(-1)![1]!.body));
};

describe("설정 편집 — S21 폼의 편집 모드", () => {
  it("지금 값으로 채워 열리고(Director 는 이름만 + 「Director 교체」 안내) 바꾼 칸만 보낸다", async () => {
    const w = await openWork();
    const onSaved = vi.fn();
    render(<CreateWorkDialog roomId={roomId} mode="edit" work={w} onOpened={onSaved} onClose={() => {}} />);
    expect(screen.getByTestId("rd-edit-work-title")).toHaveTextContent(WORK_EDIT.title);
    expect(screen.getByTestId("rd-edit-work-sub")).toHaveTextContent(WORK_EDIT.sub);
    expect(screen.getByTestId("rd-create-work-goal")).toHaveValue("결제 실패율 보고서");
    expect(screen.getByTestId("rd-create-work-budget")).toHaveValue("20");
    expect(screen.queryByTestId("rd-create-work-director")).toBeNull();
    expect(screen.getByTestId("rd-edit-work-director")).toHaveTextContent(WORK_EDIT.director_elsewhere);
    // 담당이 있는 미션의 조건 문장은 지금 조건 그대로(새 미션의 기본값 규칙이 끼어들지 않는다), 기본값 이유 줄도 없다.
    await waitFor(() => expect(screen.getByTestId("rd-create-work-condition-sentence")).toHaveTextContent("아티팩트 제출"));
    expect(screen.queryByTestId("rd-create-work-condition-why")).toBeNull();
    fireEvent.change(screen.getByTestId("rd-create-work-goal"), { target: { value: "결제 실패율 보고서 — 표 3개" } });
    const save = screen.getByTestId("rd-create-work-open");
    expect(save).toHaveTextContent(WORK_EDIT.save);
    fireEvent.click(save);
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(lastBody("PATCH")).toEqual({ goal: "결제 실패율 보고서 — 표 3개" });
    expect((onSaved.mock.calls[0][0] as Work).goal).toBe("결제 실패율 보고서 — 표 3개");
    expect((await getWork(w.id)).goal).toBe("결제 실패율 보고서 — 표 3개");
  });

  it("한도를 비우면 null(방 한도를 따른다) · 담당을 빼면 null — 두 칸만 간다", async () => {
    const w = await openWork();
    const onSaved = vi.fn();
    render(<CreateWorkDialog roomId={roomId} mode="edit" work={w} onOpened={onSaved} onClose={() => {}} />);
    fireEvent.change(await screen.findByTestId("rd-create-work-assignee"), { target: { value: "" } });
    fireEvent.change(screen.getByTestId("rd-create-work-budget"), { target: { value: "" } });
    fireEvent.click(screen.getByTestId("rd-create-work-open"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(lastBody("PATCH")).toEqual({ assignee_agent_id: null, limits: { budget_usd: null, time_limit: null } });
  });

  it("이 미션의 Director 가 아니면 폼 전체가 비활성 + 「〈이름〉님이 이 미션의 Director 입니다」", async () => {
    const w = await openWork();
    await as("seoyeon@colab.dev");
    const mine = await getWork(w.id);
    expect(mine.my_work_role).toBe("member");
    render(<CreateWorkDialog roomId={roomId} mode="edit" work={mine} onOpened={() => {}} onClose={() => {}} />);
    expect(await screen.findByText(WORK_PANEL.not_director("데모"))).toBeInTheDocument();
    expect(screen.getByTestId("rd-create-work-open")).toHaveAttribute("aria-disabled", "true");
  });
});

describe("Director 교체", () => {
  const members = (): Member[] => store().members.filter((m) => m.workspace_id === store().members[0].workspace_id);

  it("새 Director 를 고르기 전에는 비활성 + 사유 · 지금 Director 를 고르면 「다른 사람을 고르세요」 · 바꾸면 시스템 메시지", async () => {
    const w = await openWork();
    const onSaved = vi.fn();
    render(<ChangeDirectorDialog work={w} members={members()} onSaved={onSaved} onClose={() => {}} />);
    const save = screen.getByTestId("change-director-save");
    expect(save).toBeDisabled();
    expect(hintOf(save)).toHaveTextContent(CHANGE_DIRECTOR.pick);
    fireEvent.change(screen.getByTestId("change-director-to"), { target: { value: w.director_user_id } });
    expect(hintOf(save)).toHaveTextContent(CHANGE_DIRECTOR.same);
    fireEvent.change(screen.getByTestId("change-director-to"), { target: { value: uid("seoyeon@colab.dev") } });
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    // deputy 는 「그대로 둡니다」가 기본 — 본문에 싣지 않는다.
    expect(lastBody("PUT")).toEqual({ director_user_id: uid("seoyeon@colab.dev") });
    const saved = onSaved.mock.calls[0][0] as Work;
    expect(saved.director_user_id).toBe(uid("seoyeon@colab.dev"));
    expect(saved.my_work_role).toBe("member");
    const sys = [...store().messages.values()].filter((m: Message) => m.session_id === roomId && m.kind === "system").map((m) => m.content);
    expect(sys.some((c) => c.endsWith(WE_SERVER.sys_director_tail.text))).toBe(true);
  });

  it("Director 도 owner·admin 도 아니면 서버가 403 — 다이얼로그 안에서 서버 문장 그대로", async () => {
    const w = await openWork();
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("seoyeon@colab.dev") });
    await as("seoyeon@colab.dev");
    const role = store().members.find((m) => m.user.id === uid("seoyeon@colab.dev"))!.role;
    expect(role).toBe("member");
    render(<ChangeDirectorDialog work={w} members={members()} onSaved={() => {}} onClose={() => {}} />);
    fireEvent.change(screen.getByTestId("change-director-to"), { target: { value: uid("seoyeon@colab.dev") } });
    fireEvent.click(screen.getByTestId("change-director-save"));
    expect(await screen.findByTestId("change-director-error")).toHaveTextContent(WE_SERVER.change_director_role.text);
  });
});

describe("조건 고치기", () => {
  it("리뷰어 없는 검토 승인에 걸린 미션 — 같은 편집기에서 리뷰어를 고르면 저장되고 막힘이 풀린다", async () => {
    const w = await openWork();
    await bridge.call("POST", `/__mock/works/${w.id}/seed-reviewerless`);
    const stuck = await getWork(w.id);
    expect(stuck.completion_progress.conditions.map((c) => c.blocked_reason ?? null)).toEqual([null, "reviewer_missing"]);
    const onSaved = vi.fn();
    const a = agents()[0];
    render(<FixWorkConditionDialog work={stuck} agents={[{ id: a.id, name: a.name }]} onSaved={onSaved} onClose={() => {}} />);
    expect(screen.getByTestId("condition-editor")).toBeInTheDocument();
    const save = screen.getByTestId("fix-work-condition-save");
    expect(save).toBeDisabled();
    expect(screen.getByTestId("reviewer-required")).toHaveTextContent(CREATE_WORK.reviewer_required);
    fireEvent.change(screen.getByTestId("reviewer-select"), { target: { value: a.id } });
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(Object.keys(lastBody("PATCH"))).toEqual(["completion_condition"]);
    const fixed = onSaved.mock.calls[0][0] as Work;
    expect(fixed.completion_progress.conditions.some((c) => c.blocked_reason)).toBe(false);
  });
});

describe("우열 미션 칸 — 편집 동작의 권한 층", () => {
  const base = (over: Partial<Work> = {}): Work => ({
    id: "w1", room_id: "r1", title: "보고서", goal: "보고서", acceptance_criteria: [], director_user_id: "u2", director: { id: "u2", email: "", display_name: "민호", avatar_url: null, created_at: "" },
    deputy_user_id: null, assignee_agent_id: null, completion_condition: { type: "user_approval" } as Work["completion_condition"],
    completion_progress: { met: 0, total: 1, satisfied: false, human_gate: true, conditions: [] }, limits: {}, autonomy: "guided", status: "active", paused_reason: null,
    cost_usd: 0, created_by: "u2", created_at: "", updated_at: "", my_work_role: "director", ...over,
  });
  const panel = (w: Work, canManage = false, mode: "picked" | "recent" = "picked") => render(
    <WorkPanel mode={mode === "picked" ? { kind: "picked", workId: w.id } : { kind: "recent", workId: w.id }} work={w} canManage={canManage}
      onEdit={vi.fn()} onChangeDirector={vi.fn()} onFixCondition={vi.fn()} />,
  );

  it("Director — 설정 편집·Director 교체 둘 다 켜진다", () => {
    panel(base());
    expect(screen.getByTestId("work-action-edit")).toBeEnabled();
    expect(screen.getByTestId("work-action-director")).toBeEnabled();
  });

  it("ws owner·admin(Director 아님) — 설정 편집은 꺼지고 Director 교체는 켜진다", () => {
    panel(base({ my_work_role: "member" }), true);
    expect(screen.getByTestId("work-action-edit")).toBeDisabled();
    expect(hintOf(screen.getByTestId("work-action-edit"))).toHaveTextContent(WORK_PANEL.not_director("민호"));
    expect(screen.getByTestId("work-action-director")).toBeEnabled();
  });

  it("그 밖의 사람 — 둘 다 꺼지고 교체 사유는 owner·admin 층까지 말한다", () => {
    panel(base({ my_work_role: "member" }));
    expect(screen.getByTestId("work-action-director")).toBeDisabled();
    expect(hintOf(screen.getByTestId("work-action-director"))).toHaveTextContent(WORK_EDIT.change_director_role);
  });

  it("(전체)의 최근 활동 미션 — 고르지 않은 미션은 편집도 교체도 못 한다(「어느 미션인지 먼저 고르세요」)", () => {
    panel(base(), true, "recent");
    expect(screen.getByTestId("work-action-edit")).toBeDisabled();
    expect(screen.getByTestId("work-action-director")).toBeDisabled();
    expect(hintOf(screen.getByTestId("work-action-edit"))).toHaveTextContent(WORK_PANEL.pick_first);
  });

  it("막힌 조건 — Director 에게는 이유 + 「조건 고치기」, 그 밖에게는 누가 고쳐야 하는지만", () => {
    const blocked = base({
      completion_progress: { met: 0, total: 1, satisfied: false, human_gate: false, conditions: [{ path: "", type: "agent_approval", met: false, blocked_reason: "reviewer_missing" }] as Work["completion_progress"]["conditions"] },
    });
    const { unmount } = panel(blocked);
    expect(screen.getByTestId("progress-blocked")).toHaveTextContent(WORK_EDIT.blocked_director);
    expect(screen.getByTestId("work-fix-condition")).toHaveTextContent(WORK_EDIT.fix_condition);
    unmount();
    panel({ ...blocked, my_work_role: "member" }, true);
    expect(screen.getByTestId("progress-blocked")).toHaveTextContent(WORK_EDIT.blocked_member);
    expect(screen.queryByTestId("work-fix-condition")).toBeNull();
  });
});
