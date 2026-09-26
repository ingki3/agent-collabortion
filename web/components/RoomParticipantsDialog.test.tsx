/**
 * S19 참여자 초대·퇴장(T-R2-W3, SCREEN §4.10) — 화면을 목 서버에 그대로 물려(fetch 다리) 잰다:
 *   두 구역 · 본인 행은 「이 방에서 나가기」 · 방장/Director 거부는 누르기 전에(층을 적은 사유 + 방 설정 링크) · 사람/에이전트 탭 ·
 *   에이전트 탭 머리 한 줄 · respond_to 비활성 사유 · 컴퓨터 없는 방의 안내 · 읽기 전용(본인 나가기만 활성) · 보관된 방.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

const auth: { me: unknown; workspace: unknown; canManage: boolean } = { me: null, workspace: null, canManage: false };
vi.mock("@/lib/auth/AuthContext", () => ({ useAuth: () => auth }));
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));

import { RoomParticipantsDialog } from "./RoomParticipantsDialog";
import { installFetchBridge, type FetchBridge } from "@/lib/mock/fetch-bridge";
import { store } from "@/lib/mock/store";
import { PARTICIPANTS } from "@/lib/room-dialogs";
import type { Room } from "@/lib/api/types";

let bridge: FetchBridge;
let roomId = "";
const uid = (email: string) => [...store().users.values()].find((u) => u.email === email)!.id;
async function as(email: string) {
  await bridge.login(email);
  const u = [...store().users.values()].find((x) => x.email === email)!;
  const m = store().members.find((x) => x.user.id === u.id)!;
  auth.me = { user: { id: u.id, email: u.email, display_name: u.display_name } };
  auth.workspace = { id: m.workspace_id, my_role: m.role };
  auth.canManage = m.role === "owner" || m.role === "admin";
}
beforeEach(async () => {
  bridge = await installFetchBridge();
  await as("demo@colab.dev");
  const ws = store().members[0].workspace_id;
  roomId = (await bridge.call<Room>("POST", `/workspaces/${ws}/rooms`, { name: "결제팀" })).body.id;
});
afterEach(() => {
  cleanup();
  bridge.restore();
});

const rows = () => screen.getAllByTestId("rd-part-row");
const rowOf = (name: string) => rows().find((r) => r.textContent?.includes(name))!;

describe("S19 참여자", () => {
  it("방장 본인 행 — 「이 방에서 나가기」 비활성 + 「방장입니다 — 먼저 방장을 넘기세요」 + 방 설정 링크(툴팁 아님)", async () => {
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    await waitFor(() => expect(rows()).toHaveLength(1));
    const leave = within(rows()[0]).getByTestId("rd-part-leave");
    expect(leave).toHaveTextContent(PARTICIPANTS.leave);
    expect(leave).toHaveAttribute("aria-disabled", "true");
    const hint = document.getElementById(leave.getAttribute("aria-describedby")!)!;
    expect(hint).toHaveTextContent(PARTICIPANTS.owner_cannot_leave);
    expect(within(hint).getByTestId("rd-part-owner-link")).toHaveAttribute("href", `/rooms/${roomId}/settings`);
    expect(leave).not.toHaveAttribute("title");
  });

  it("사람 탭에서 초대하면 목록에 들고, 에이전트 탭은 머리 한 줄 + 컴퓨터가 없는 방의 안내", async () => {
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    const person = await screen.findAllByTestId("rd-invite-person");
    fireEvent.click(within(person.find((p) => p.textContent?.includes("서연"))!).getByTestId("rd-invite-person-btn"));
    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(within(rowOf("서연")).getByTestId("rd-part-role")).toHaveTextContent("참여자");

    fireEvent.click(screen.getByTestId("rd-invite-tab-agents"));
    expect(screen.getByTestId("rd-invite-agents-head")).toHaveTextContent(PARTICIPANTS.agents_head);
    const agents = await screen.findAllByTestId("rd-invite-agent");
    expect(within(agents[0]).getByTestId("rd-invite-agent-info")).toHaveTextContent(PARTICIPANTS.runtime_unset);
    fireEvent.click(within(agents[0]).getByTestId("rd-invite-agent-btn"));
    await waitFor(() => expect(rows().filter((r) => r.dataset.kind === "agent")).toHaveLength(1));
  });

  it("방 컴퓨터에 그 종류가 없으면 경고 문장(〈컴퓨터〉에 〈종류〉 가 없습니다…)", async () => {
    const rt = [...store().runtimes.values()][0];
    rt.capabilities = rt.capabilities.filter((c) => c.kind !== "claude_code");
    await bridge.call("POST", `/__mock/rooms/${roomId}/runtime`, { runtime_id: rt.id });
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    fireEvent.click(await screen.findByTestId("rd-invite-tab-agents"));
    const warn = (await screen.findAllByTestId("rd-invite-agent-warn"))[0];
    expect(warn).toHaveTextContent(PARTICIPANTS.runtime_missing(rt.name, "Claude Code"));
  });

  it("킬 스위치가 켜진 에이전트는 초대 비활성 + 사유", async () => {
    [...store().agents.values()][0].respond_to = "nobody";
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    fireEvent.click(await screen.findByTestId("rd-invite-tab-agents"));
    const first = (await screen.findAllByTestId("rd-invite-agent"))[0];
    const btn = within(first).getByTestId("rd-invite-agent-btn");
    expect(btn).toHaveAttribute("aria-disabled", "true");
    expect(document.getElementById(btn.getAttribute("aria-describedby")!)).toHaveTextContent(PARTICIPANTS.not_invitable_kill);
  });

  it("열린 미션의 Director 는 내보내기 비활성 + 그 미션 이름 · 누르면 아무것도 안 부른다", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("seoyeon@colab.dev") });
    await bridge.call("POST", `/rooms/${roomId}/works`, { goal: "보고서 초안", director_user_id: uid("seoyeon@colab.dev") });
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    await waitFor(() => expect(rows()).toHaveLength(2));
    const btn = within(rowOf("서연")).getByTestId("rd-part-remove");
    expect(btn).toHaveAttribute("aria-disabled", "true");
    expect(document.getElementById(btn.getAttribute("aria-describedby")!)).toHaveTextContent(PARTICIPANTS.director_remove("보고서 초안"));
    fireEvent.click(btn);
    expect(screen.queryByTestId("rd-remove-dialog")).toBeNull();
  });

  it("에이전트 내보내기 확인 — 퇴장은 취소가 아니다 · 진행 중인 턴은 계속 돈다", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { agent_id: [...store().agents.values()][0].id });
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    await waitFor(() => expect(rows()).toHaveLength(2));
    fireEvent.click(within(rowOf("Lead")).getByTestId("rd-part-remove"));
    const dlg = screen.getByTestId("rd-remove-dialog");
    expect(dlg).toHaveTextContent(PARTICIPANTS.remove_agent_body);
    expect(dlg).toHaveTextContent(PARTICIPANTS.remove_agent_running);
    fireEvent.click(screen.getByTestId("rd-remove-dialog-confirm"));
    await waitFor(() => expect(rows()).toHaveLength(1));
  });

  it("권한 밖(참여자) — 읽기 전용 사유 · 초대·내보내기 비활성 · 본인 「이 방에서 나가기」는 활성이고 확인 뒤 onLeft", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("seoyeon@colab.dev") });
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("junho@colab.dev") });
    await as("seoyeon@colab.dev");
    const onLeft = vi.fn();
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} onLeft={onLeft} />);
    await waitFor(() => expect(rows()).toHaveLength(3));
    expect(screen.getByTestId("rd-invite")).toHaveTextContent(PARTICIPANTS.read_only);
    expect(within(rowOf("준호")).getByTestId("rd-part-remove")).toHaveAttribute("aria-disabled", "true");
    expect(screen.queryByTestId("rd-part-more")).toBeNull(); // 부방장 지정은 방장·ws owner·admin 만
    const leave = within(rowOf("서연")).getByTestId("rd-part-leave");
    expect(leave).not.toHaveAttribute("aria-disabled");
    fireEvent.click(leave);
    expect(screen.getByTestId("rd-leave-dialog")).toHaveTextContent(PARTICIPANTS.leave_body);
    fireEvent.click(screen.getByTestId("rd-leave-dialog-confirm"));
    await waitFor(() => expect(onLeft).toHaveBeenCalled());
  });

  it("부방장 지정 — 사람 행 「…」 에서 · 역할이 부방장으로 바뀐다", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("seoyeon@colab.dev") });
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    await waitFor(() => expect(rows()).toHaveLength(2));
    fireEvent.click(within(rowOf("서연")).getByTestId("rd-part-make-deputy"));
    await waitFor(() => expect(within(rowOf("서연")).getByTestId("rd-part-role")).toHaveTextContent("부방장"));
  });

  it("보관된 방 — 초대 전체 비활성 + 「보관된 방에는 초대할 수 없습니다」", async () => {
    await bridge.call("POST", `/rooms/${roomId}/archive`);
    render(<RoomParticipantsDialog roomId={roomId} onClose={() => {}} />);
    await waitFor(() => expect(screen.getByTestId("rd-invite")).toHaveTextContent(PARTICIPANTS.archived));
    const btn = (await screen.findAllByTestId("rd-invite-person-btn"))[0];
    expect(btn).toHaveAttribute("aria-disabled", "true");
  });
});
