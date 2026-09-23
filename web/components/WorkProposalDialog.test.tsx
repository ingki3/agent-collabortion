/**
 * S26 미션 제안 확인(T-R2-W3, SCREEN §4.9) — 목 서버에 물려 잰다: 제안한 에이전트·근거·goal · 「이대로 열기」(goal 고정 S21) ·
 * 「고쳐서 열기」(편집 가능) · 「거절」(사유 선택, 타임라인에 남는다) · 이미 처리된 제안(열었음 + 미션 링크 / 거절 + 사유 — 만료 없음) ·
 * 여는 사람이 Director · 참여자가 아니면 비활성 + 사유 · 쿼리 라우팅(`?work=new` · `?work=from&message=` · `?work_proposal=`).
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("@/lib/realtime/StreamContext", () => ({ useWorkspaceStream: () => "open" }));
const nav = { search: new URLSearchParams(), replace: vi.fn() };
vi.mock("next/navigation", () => ({
  useSearchParams: () => nav.search,
  useRouter: () => ({ replace: nav.replace, push: vi.fn() }),
  usePathname: () => "/rooms/r1",
}));

import { WorkProposalDialog } from "./WorkProposalDialog";
import { parseRoomDialogQuery, RoomQueryDialogs } from "./RoomQueryDialogs";
import { setup, uid } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { CREATE_WORK, PROPOSAL } from "@/lib/room-dialogs";
import { RW } from "@/lib/mock/rooms-dialogs-wording";
import type { components } from "@/lib/api/schema";

type WP = components["schemas"]["WorkProposal"];
type Work = components["schemas"]["Work"];
let bridge: FetchBridge;
let roomId = "";
let as: (email: string) => Promise<void>;
let prop: WP;
beforeEach(async () => {
  ({ bridge, roomId, as } = await setup());
  prop = (await bridge.call<WP>("POST", `/__mock/rooms/${roomId}/work-proposals`, { goal: "주간 결제 보고 자동화", rationale: "매주 같은 요청이 세 번 왔습니다" })).body;
  nav.search = new URLSearchParams();
  nav.replace.mockReset();
});
afterEach(() => {
  cleanup();
  bridge.restore();
});
const today = () => new Date().toISOString().slice(0, 10);

describe("S26 미션 제안 확인", () => {
  it("제안의 모양 — 에이전트 · 목표 · 근거 · 여는 사람이 Director · 「이대로 열기」는 goal 고정 S21 → 열면 연 사람이 Director", async () => {
    const onOpened = vi.fn();
    render(<WorkProposalDialog roomId={roomId} proposalId={prop.id} onOpened={onOpened} onClose={() => {}} />);
    expect(await screen.findByTestId("rd-proposal-by")).toHaveTextContent(PROPOSAL.proposed_by(prop.agent.name));
    expect(screen.getByTestId("rd-proposal-goal")).toHaveTextContent("주간 결제 보고 자동화");
    expect(screen.getByTestId("rd-proposal-rationale")).toHaveTextContent("매주 같은 요청이 세 번 왔습니다");
    expect(screen.getByTestId("rd-proposal")).toHaveTextContent(PROPOSAL.director_note);
    fireEvent.click(screen.getByTestId("rd-proposal-accept"));
    expect(await screen.findByTestId("rd-create-work-title")).toHaveTextContent(CREATE_WORK.title_proposal);
    expect(screen.getByTestId("rd-create-work-goal")).toHaveAttribute("readonly");
    expect(screen.getByTestId("rd-create-work-goal")).toHaveValue("주간 결제 보고 자동화");
    fireEvent.click(screen.getByTestId("rd-create-work-open"));
    await waitFor(() => expect(onOpened).toHaveBeenCalled());
    expect((onOpened.mock.calls[0][0] as Work).director_user_id).toBe(uid("demo@colab.dev"));
    expect((await bridge.call<WP>("GET", `/work-proposals/${prop.id}`)).body.status).toBe("accepted");
  });

  it("「고쳐서 열기」 — 같은 다이얼로그, goal 을 고칠 수 있다", async () => {
    const onOpened = vi.fn();
    render(<WorkProposalDialog roomId={roomId} proposalId={prop.id} onOpened={onOpened} onClose={() => {}} />);
    fireEvent.click(await screen.findByTestId("rd-proposal-edit"));
    const goal = await screen.findByTestId("rd-create-work-goal");
    expect(goal).not.toHaveAttribute("readonly");
    fireEvent.change(goal, { target: { value: "월간 결제 보고 자동화" } });
    fireEvent.click(screen.getByTestId("rd-create-work-open"));
    await waitFor(() => expect(onOpened).toHaveBeenCalled());
    expect((onOpened.mock.calls[0][0] as Work).goal).toBe("월간 결제 보고 자동화");
  });

  it("「거절」 — 사유(선택)를 적으면 처리된 상태로: 「이 제안은 데모 님이 … 에 거절했습니다」 + 사유 · 타임라인에 남는다", async () => {
    render(<WorkProposalDialog roomId={roomId} proposalId={prop.id} onOpened={() => {}} onClose={() => {}} />);
    fireEvent.click(await screen.findByTestId("rd-proposal-reject"));
    fireEvent.change(screen.getByTestId("rd-proposal-reason"), { target: { value: "이미 하는 중" } });
    fireEvent.click(screen.getByTestId("rd-proposal-reject-confirm"));
    const done = await screen.findByTestId("rd-proposal-resolved");
    expect(done).toHaveTextContent(PROPOSAL.rejected("데모", today()));
    expect(screen.getByTestId("rd-proposal-reject-reason")).toHaveTextContent(`${PROPOSAL.reason_prefix}이미 하는 중`);
    expect(screen.queryByTestId("rd-proposal-accept")).toBeNull();
  });

  it("이미 열린 제안 — 「이 제안은 서연 님이 … 에 열었습니다」 + 그 미션 링크(만료 상태는 없다)", async () => {
    await bridge.call("POST", `/rooms/${roomId}/participants`, { user_id: uid("seoyeon@colab.dev") });
    await as("seoyeon@colab.dev");
    const res = await bridge.call<{ work: Work }>("POST", `/work-proposals/${prop.id}/resolution`, { action: "accept" });
    await as("demo@colab.dev");
    render(<WorkProposalDialog roomId={roomId} proposalId={prop.id} onOpened={() => {}} onClose={() => {}} />);
    expect(await screen.findByTestId("rd-proposal-resolved")).toHaveTextContent(PROPOSAL.accepted("서연", today()));
    expect(screen.getByTestId("rd-proposal-go-work")).toHaveAttribute("href", `/rooms/${roomId}?work=${res.body.work.id}`);
    expect(screen.getByTestId("rd-proposal")).not.toHaveTextContent("만료");
  });

  it("방 참여자가 아니면 — 서버가 제안 자체를 403 으로 준다(authz 문장 그대로) · 버튼이 없다", async () => {
    await as("junho@colab.dev");
    render(<WorkProposalDialog roomId={roomId} proposalId={prop.id} onOpened={() => {}} onClose={() => {}} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(RW.open_not_participant);
    expect(screen.queryByTestId("rd-proposal-accept")).toBeNull();
  });

  it("다른 방의 제안 id 는 「이 방에 그 제안이 없습니다」", async () => {
    render(<WorkProposalDialog roomId="00000000-0000-0000-0000-000000000000" proposalId={prop.id} onOpened={() => {}} onClose={() => {}} />);
    expect(await screen.findByTestId("rd-proposal-not-found")).toHaveTextContent(PROPOSAL.not_found);
  });
});

describe("쿼리 라우팅 — S7 이 마운트 한 줄로 쓴다", () => {
  it("parseRoomDialogQuery 표", () => {
    const q = (s: string) => parseRoomDialogQuery(new URLSearchParams(s));
    expect(q("")).toEqual({ kind: "none" });
    expect(q("work=new")).toEqual({ kind: "new_work" });
    expect(q("work=from&message=m1")).toEqual({ kind: "work_from", messageId: "m1" });
    expect(q("work=from")).toEqual({ kind: "new_work" });
    expect(q("work=abc-123")).toEqual({ kind: "none" }); // S22 미션 패널의 몫
    expect(q("work_proposal=p1&work=new")).toEqual({ kind: "proposal", proposalId: "p1" });
  });

  it("?work=new 면 S21 을 띄우고, 열리면 ?work=<새 미션> 으로 바꾼다(S22) · 닫으면 두 쿼리만 지운다", async () => {
    nav.search = new URLSearchParams("work=new&tab=board");
    render(<RoomQueryDialogs roomId={roomId} />);
    fireEvent.change(await screen.findByTestId("rd-create-work-goal"), { target: { value: "보고서" } });
    fireEvent.click(screen.getByTestId("rd-create-work-open"));
    await waitFor(() => expect(nav.replace).toHaveBeenCalled());
    expect(nav.replace.mock.calls[0][0]).toMatch(/^\/rooms\/r1\?tab=board&work=[0-9a-f-]{36}$/);
    nav.replace.mockReset();
    fireEvent.click(screen.getByTestId("rd-create-work-cancel"));
    expect(nav.replace).toHaveBeenCalledWith("/rooms/r1?tab=board", { scroll: false });
  });

  it("?work_proposal= 면 S26", async () => {
    nav.search = new URLSearchParams(`work_proposal=${prop.id}`);
    render(<RoomQueryDialogs roomId={roomId} />);
    expect(await screen.findByTestId("rd-proposal-goal")).toHaveTextContent("주간 결제 보고 자동화");
  });
});
