/**
 * 방 다이얼로그·설정 목(T-R2-W3) — **서버 규칙 대조**. 서버 소스(handlers_room_participants.go · handlers_rooms.go · handlers_works.go ·
 * handlers_work_proposals.go · handlers_rooms_read.go · rooms/authz.go)의 판정 순서·코드·문장을 목이 같게 내는지 잰다.
 * 문장 자체의 글자 대조는 `server-wording/j-room-dialogs.test.ts`.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { josa, W } from "./wording";
import { RW } from "./rooms-dialogs-wording";
import { resetStore, store, type Subscriber } from "./store";
import type { components } from "@/lib/api/schema";
import type { Room, Runtime } from "@/lib/api/types";

type S = components["schemas"];
let cookie = "";
async function call(method: string, path: string, body?: unknown) {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(), body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req);
}
async function must<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await call(method, path, body);
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(res.body)}`);
  return res.body as T;
}
async function login(email = "demo@colab.dev") {
  const res = await call("POST", "/auth/login", { email, password: "password123" });
  cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
}
const prob = (res: { body?: unknown }) => res.body as { code?: string; detail?: string; errors?: { field: string; code?: string; message: string }[]; [k: string]: unknown };
const userId = (email: string) => [...store().users.values()].find((u) => u.email === email)!.id;
const agentIds = () => [...store().agents.values()].map((a) => a.id);
let wsId = "";
async function newRoom(name = "결제팀") {
  return must<Room>("POST", `/workspaces/${wsId}/rooms`, { name });
}
const parts = async (roomId: string) => (await must<{ items: S["RoomParticipant"][] }>("GET", `/rooms/${roomId}/participants`)).items;
function tap() {
  const frames: { type: string; payload: Record<string, unknown> }[] = [];
  const sub: Subscriber = { workspace_id: wsId, room_ids: null, write: (f) => { const m = /data: (.*)\n\n$/s.exec(f); if (m) frames.push(JSON.parse(m[1])); } };
  store().subs.add(sub);
  return frames;
}

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
  wsId = (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
});

describe("S19 참여자 — listRoomParticipants · addRoomParticipant · removeRoomParticipant", () => {
  it("사람과 에이전트가 한 목록 · 방장이 먼저 · 초대하면 시스템 메시지와 participant.joined", async () => {
    const r = await newRoom();
    const frames = tap();
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("seoyeon@colab.dev") });
    await must("POST", `/rooms/${r.id}/participants`, { agent_id: agentIds()[0] });
    const list = await parts(r.id);
    expect(list.map((p) => [p.kind, p.room_role])).toEqual([["user", "owner"], ["user", "member"], ["agent", "member"]]);
    expect(frames.filter((f) => f.type === "participant.joined")).toHaveLength(2);
    const msgs = [...store().messages.values()].filter((m) => m.session_id === r.id).map((m) => m.content);
    expect(msgs).toContain("데모" + RW.sys_invited_mid + "서연" + RW.sys_invited_tail);
    expect(msgs).toContain(josa("Lead", "이", "가") + W.participant_joined); // 비한글 이름은 「Lead이(가)」 — apperr.Josa 와 같다
  });

  it("사람·에이전트 중 하나만 · 멤버 아님 422 · 이미 있음 409 (서버 문장)", async () => {
    const r = await newRoom();
    expect(prob(await call("POST", `/rooms/${r.id}/participants`, {})).errors?.[0]).toMatchObject({ field: "user_id", code: "one_of", message: RW.one_of });
    expect(prob(await call("POST", `/rooms/${r.id}/participants`, { user_id: "nobody" })).errors?.[0]).toMatchObject({ code: "not_member", message: RW.invite_not_member });
    const again = await call("POST", `/rooms/${r.id}/participants`, { user_id: userId("demo@colab.dev") });
    expect([again.status, prob(again).code, prob(again).detail]).toEqual([409, "already_participant", RW.already_person]);
  });

  it("에이전트 초대는 누른 사람의 respond_to 로 — nobody 403 · owner 인데 남이면 403", async () => {
    const r = await newRoom();
    const a = [...store().agents.values()][0];
    a.respond_to = "nobody";
    expect([(await call("POST", `/rooms/${r.id}/participants`, { agent_id: a.id })).status, prob(await call("POST", `/rooms/${r.id}/participants`, { agent_id: a.id })).detail]).toEqual([403, RW.trig_nobody]);
    a.respond_to = "owner";
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("seoyeon@colab.dev") });
    await must("PUT", `/rooms/${r.id}/deputy`, { user_id: userId("seoyeon@colab.dev") });
    await login("seoyeon@colab.dev");
    expect(prob(await call("POST", `/rooms/${r.id}/participants`, { agent_id: a.id })).detail).toBe(RW.trig_owner);
  });

  it("방 컴퓨터에 그 종류가 없으면 warnings[runtime_kind_missing] — 거부가 아니다 · 컴퓨터가 없으면 경고도 없다", async () => {
    const r = await newRoom();
    const res = await must<{ warnings: string[] }>("POST", `/rooms/${r.id}/participants`, { agent_id: agentIds()[0] });
    expect(res.warnings).toEqual([]);
    const rt = (await must<Runtime[]>("GET", `/workspaces/${wsId}/runtimes`))[0];
    rt.capabilities = [];
    await must("POST", `/__mock/rooms/${r.id}/runtime`, { runtime_id: rt.id });
    expect((await must<{ warnings: string[] }>("POST", `/rooms/${r.id}/participants`, { agent_id: agentIds()[1] })).warnings).toEqual(["runtime_kind_missing"]);
  });

  it("방장은 나갈 수 없다(409 is_owner) · 열린 미션의 Director 는 409 is_director · 그 밖은 204 + participant.left", async () => {
    const r = await newRoom();
    const me = (await parts(r.id))[0];
    expect([(await call("DELETE", `/rooms/${r.id}/participants/${me.id}`)).status, prob(await call("DELETE", `/rooms/${r.id}/participants/${me.id}`)).detail]).toEqual([409, RW.is_owner]);
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("seoyeon@colab.dev") });
    await must("POST", `/rooms/${r.id}/works`, { goal: "보고서", director_user_id: userId("seoyeon@colab.dev") });
    const seo = (await parts(r.id)).find((p) => p.user?.email === "seoyeon@colab.dev")!;
    const busy = await call("DELETE", `/rooms/${r.id}/participants/${seo.id}`);
    expect([busy.status, prob(busy).code, prob(busy).detail, prob(busy).works_directed]).toEqual([409, "is_director", RW.is_director, 1]);
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("junho@colab.dev") });
    const frames = tap();
    const jun = (await parts(r.id)).find((p) => p.user?.email === "junho@colab.dev")!;
    expect((await call("DELETE", `/rooms/${r.id}/participants/${jun.id}`)).status).toBe(204);
    expect(frames.find((f) => f.type === "participant.left")?.payload).toMatchObject({ room_id: r.id, participant_id: jun.id, kind: "user" });
  });

  it("본인 행은 참여자 누구나(나가기) · 남의 행은 방장·부방장·ws owner·admin 만(403 문장은 rooms.Deny)", async () => {
    const r = await newRoom();
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("seoyeon@colab.dev") });
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("junho@colab.dev") });
    const list = await parts(r.id);
    await login("seoyeon@colab.dev");
    const other = list.find((p) => p.user?.email === "junho@colab.dev")!;
    const denied = await call("DELETE", `/rooms/${r.id}/participants/${other.id}`);
    expect([denied.status, prob(denied).detail]).toEqual([403, W.room_steward_required]);
    const self = list.find((p) => p.user?.email === "seoyeon@colab.dev")!;
    expect((await call("DELETE", `/rooms/${r.id}/participants/${self.id}`)).status).toBe(204);
    expect([...store().messages.values()].some((m) => m.content === "서연 님이" + RW.sys_left)).toBe(true);
  });

  it("보관된 방의 초대는 409 room_archived(rooms.Deny — 권한이 있는 사람에게)", async () => {
    const r = await newRoom();
    await must("POST", `/rooms/${r.id}/archive`);
    const res = await call("POST", `/rooms/${r.id}/participants`, { user_id: userId("seoyeon@colab.dev") });
    expect([res.status, prob(res).code]).toEqual([409, "room_archived"]);
  });
});

describe("S20 방 설정 — updateRoom 전체 칸 · 방장 넘기기 · 부방장", () => {
  it("한도·자율성·기본 Director 를 저장하고 getRoom 이 돌려준다 · 시스템 메시지 「방 설정을 바꿨습니다」", async () => {
    const r = await newRoom();
    const out = await must<Room>("PATCH", `/rooms/${r.id}`, { limits: { budget_usd: 50, max_concurrent_works: 1 }, autonomy: "autonomous", default_director_user_id: userId("seoyeon@colab.dev") });
    expect(out).toMatchObject({ autonomy: "autonomous", default_director_user_id: userId("seoyeon@colab.dev") });
    expect(out.limits).toMatchObject({ budget_usd: 50, max_concurrent_works: 1 });
    expect((await must<Room>("GET", `/rooms/${r.id}`)).default_director_user_id).toBe(userId("seoyeon@colab.dev"));
    expect([...store().messages.values()].some((m) => m.content === "데모" + RW.sys_settings)).toBe(true);
  });

  it("검증 422 — 감독 모드 · 워크트리 저장소 없음 · 컨테이너 · 상한 1 미만(서버 필드 경로·문장)", async () => {
    const r = await newRoom();
    const res = await call("PATCH", `/rooms/${r.id}`, { autonomy: "supervised", isolation: { kind: "worktree" }, limits: { max_concurrent_works: 0 } });
    expect(prob(res).errors).toEqual([
      { field: "autonomy", code: "unsupported", message: RW.supervised_unsupported },
      { field: "isolation/repo_path", code: "required", message: RW.repo_required },
      { field: "limits/max_concurrent_works", code: "out_of_range", message: RW.min_1 },
    ]);
  });

  it("첫 dispatch 뒤 컴퓨터·격리는 409 runtime_pinned — 이름·한도는 그대로 바뀐다", async () => {
    const r = await newRoom();
    await must("POST", `/__mock/rooms/${r.id}/runtime`, { pinned: true });
    const res = await call("PATCH", `/rooms/${r.id}`, { isolation: { kind: "none" } });
    expect([res.status, prob(res).code, prob(res).detail]).toEqual([409, "runtime_pinned", RW.runtime_pinned]);
    expect((await call("PATCH", `/rooms/${r.id}`, { name: "결제팀 2" })).status).toBe(200);
    expect((await must<Room>("GET", `/rooms/${r.id}`)).runtime_pinned).toBe(true); // 계약 0.2.7 — 화면이 읽는 칸
  });

  it("공개 범위를 바꾸면 그 시스템 메시지 한 줄(설정 변경 줄과 따로)", async () => {
    const r = await newRoom();
    await must("PATCH", `/rooms/${r.id}`, { visibility: "invited" });
    const msgs = [...store().messages.values()].filter((m) => m.session_id === r.id).map((m) => m.content);
    expect(msgs).toContain("데모" + RW.sys_vis_invited);
    expect(msgs).not.toContain("데모" + RW.sys_settings);
  });

  it("방장 넘기기 — 참여자 아니면 422 · 넘기면 역할이 바뀌고 옛 방장은 참여자 · 부방장은 방장을 겸할 수 없다", async () => {
    const r = await newRoom();
    const bad = await call("PUT", `/rooms/${r.id}/owner`, { user_id: userId("seoyeon@colab.dev") });
    expect(prob(bad).errors?.[0]).toMatchObject({ field: "user_id", code: "not_participant", message: RW.seat_not_participant });
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("seoyeon@colab.dev") });
    const dep = await call("PUT", `/rooms/${r.id}/deputy`, { user_id: userId("demo@colab.dev") });
    expect(prob(dep).errors?.[0]).toMatchObject({ code: "is_owner", message: RW.deputy_is_owner });
    const out = await must<Room>("PUT", `/rooms/${r.id}/owner`, { user_id: userId("seoyeon@colab.dev") });
    expect(out.owner_user_id).toBe(userId("seoyeon@colab.dev"));
    expect((await parts(r.id)).map((p) => [p.user?.email, p.room_role])).toEqual([["seoyeon@colab.dev", "owner"], ["demo@colab.dev", "member"]]);
  });

  it("부방장 지정·해제는 방장·ws owner·admin 만 — 부방장 본인은 못 한다(403 room_owner_required)", async () => {
    const r = await newRoom();
    await must("POST", `/rooms/${r.id}/participants`, { user_id: userId("seoyeon@colab.dev") });
    await must("PUT", `/rooms/${r.id}/deputy`, { user_id: userId("seoyeon@colab.dev") });
    await login("seoyeon@colab.dev");
    const res = await call("PUT", `/rooms/${r.id}/deputy`, { user_id: null });
    expect([res.status, prob(res).code]).toEqual([403, "room_owner_required"]);
    expect((await must<Room>("GET", `/rooms/${r.id}`)).my_capabilities).toEqual(expect.arrayContaining(["invite", "configure", "link", "archive"]));
    expect((await must<Room>("GET", `/rooms/${r.id}`)).my_capabilities).not.toContain("transfer_owner");
  });
});

describe("S24 참고 방 링크 — list · create · delete", () => {
  it("내가 참여한 방만 연결 — 아니면 403 not_participant_of_target · 자기 방 422 · 중복 409 · 양쪽 방에 시스템 메시지 + room_link.updated 둘", async () => {
    const a = await newRoom("결제팀");
    const b = await newRoom("인프라");
    expect(prob(await call("POST", `/rooms/${a.id}/links`, { target_room_id: a.id })).errors?.[0]).toMatchObject({ code: "self", message: RW.link_self });
    const frames = tap();
    const link = await must<S["RoomLink"]>("POST", `/rooms/${a.id}/links`, { target_room_id: b.id });
    expect(link.target_room).toMatchObject({ id: b.id, name: "인프라" });
    expect(frames.filter((f) => f.type === "room_link.updated").map((f) => f.payload.room_id)).toEqual([a.id, b.id]);
    const dup = await call("POST", `/rooms/${a.id}/links`, { target_room_id: b.id });
    expect([dup.status, prob(dup).detail]).toEqual([409, RW.already_linked]);
    const msgs = (id: string) => [...store().messages.values()].filter((m) => m.session_id === id).map((m) => m.content);
    expect(msgs(a.id)).toContain("데모" + RW.sys_invited_mid + "인프라" + RW.sys_link_src);
    expect(msgs(b.id)).toContain("데모" + RW.sys_link_dst_head + "결제팀" + RW.sys_link_dst_tail);
    // 서연이 만든 방 — 데모는 참여자가 아니다.
    await login("seoyeon@colab.dev");
    const c = await newRoom("서연 방");
    await login();
    const res = await call("POST", `/rooms/${a.id}/links`, { target_room_id: c.id });
    expect([res.status, prob(res).code, prob(res).detail]).toEqual([403, "not_participant_of_target", RW.not_participant_of_target]);
    // 반대쪽 방에서도 풀 수 있다.
    expect((await call("DELETE", `/rooms/${b.id}/links/${link.id}`)).status).toBe(204);
    expect((await must<{ items: unknown[] }>("GET", `/rooms/${a.id}/links`)).items).toEqual([]);
  });
});

describe("S23 맥락 읽기 기록 — listRoomReads", () => {
  it("세 방향 · 거부 행은 대상 방을 지운다 — originator_left 만 예외 · direction 검증 · 볼 수 없는 방은 404(문장은 handlers_rooms_read.go)", async () => {
    const a = await newRoom("결제팀");
    const b = await newRoom("인프라");
    await must("POST", `/__mock/rooms/${a.id}/reads`, { target_room_id: b.id, truncated: true });
    await must("POST", `/__mock/rooms/${b.id}/reads`, { target_room_id: a.id });
    await must("POST", `/__mock/rooms/${a.id}/reads`, { target_room_id: b.id, denied_reason: "agent_not_allowed" });
    await must("POST", `/__mock/rooms/${a.id}/reads`, { target_room_id: b.id, denied_reason: "originator_left" });
    const items = (await must<{ items: S["RoomRead"][] }>("GET", `/rooms/${a.id}/reads`)).items;
    const by = (d: string) => items.filter((r) => r.direction === d);
    expect(by("out")[0]).toMatchObject({ other_room: { id: b.id, name: "인프라" }, truncated: true, denied_reason: null });
    expect(by("in")[0].other_room).toMatchObject({ id: b.id });
    expect(by("denied").map((r) => [r.denied_reason, r.other_room?.name ?? null]).sort()).toEqual([["agent_not_allowed", null], ["originator_left", "인프라"]]);
    expect(prob(await call("GET", `/rooms/${a.id}/reads?direction=sideways`)).errors?.[0]).toMatchObject({ field: "direction", message: RW.read_direction });
    expect((await must<{ items: unknown[] }>("GET", `/rooms/${a.id}/reads?direction=denied`)).items).toHaveLength(2);
    await must("PATCH", `/rooms/${a.id}`, { visibility: "invited" });
    await login("junho@colab.dev");
    const hidden = await call("GET", `/rooms/${a.id}/reads`);
    expect([hidden.status, prob(hidden).detail]).toEqual([404, RW.read_room_not_found]);
  });
});

describe("S21 미션 열기 — createWork", () => {
  it("goal 만으로 열린다 — 담당이 없으면 종료 조건은 Director 승인 단독 · 있으면 제출 AND 승인 · Director 는 연 사람 · work.created", async () => {
    const r = await newRoom();
    const frames = tap();
    const w1 = await must<S["Work"]>("POST", `/rooms/${r.id}/works`, { goal: "보고서 초안\n표 3개" });
    expect(w1).toMatchObject({ title: "보고서 초안", status: "active", director_user_id: userId("demo@colab.dev"), my_work_role: "director", assignee_agent_id: null });
    expect(w1.completion_condition).toEqual({ op: "and", conditions: [{ type: "user_approval" }] });
    await must("POST", `/rooms/${r.id}/participants`, { agent_id: agentIds()[0] });
    const w2 = await must<S["Work"]>("POST", `/rooms/${r.id}/works`, { goal: "번역", assignee_agent_id: agentIds()[0] });
    expect(w2.completion_condition).toEqual({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }] });
    expect(frames.filter((f) => f.type === "work.created")).toHaveLength(2);
    expect((await must<{ items: { id: string }[] }>("GET", `/rooms/${r.id}/works`)).items.map((w) => w.id).sort()).toEqual([w1.id, w2.id].sort()); // 순서는 서버 created_at DESC(같은 ms 면 id) — 여기서는 둘 다 실리는지만
    expect((await must<Room>("GET", `/rooms/${r.id}`)).counts?.works_active).toBe(2);
  });

  it("방 기본 Director 가 있으면 그 사람 · 담당은 방 참여자여야 · 리뷰어 없는 검토 승인 422 · goal 비면 422", async () => {
    const r = await newRoom();
    await must("PATCH", `/rooms/${r.id}`, { default_director_user_id: userId("seoyeon@colab.dev") });
    expect((await must<S["Work"]>("POST", `/rooms/${r.id}/works`, { goal: "a" })).director_user_id).toBe(userId("seoyeon@colab.dev"));
    expect(prob(await call("POST", `/rooms/${r.id}/works`, { goal: " " })).errors?.[0]).toMatchObject({ field: "goal", message: RW.goal_required });
    expect(prob(await call("POST", `/rooms/${r.id}/works`, { goal: "b", assignee_agent_id: agentIds()[0] })).errors?.[0]).toMatchObject({ code: "not_participant", message: RW.assignee_not_participant });
    const rv = await call("POST", `/rooms/${r.id}/works`, { goal: "c", completion_condition: { op: "and", conditions: [{ type: "agent_approval" }] } });
    expect(prob(rv).errors?.[0]).toMatchObject({ code: "reviewer_required", message: W.reviewer_required });
  });

  it("동시 미션 상한 — 409 max_concurrent_works + open_works[] · 문장은 %d 를 끼운 서버 문장", async () => {
    const r = await newRoom();
    await must("PATCH", `/rooms/${r.id}`, { limits: { max_concurrent_works: 1 } });
    const w = await must<S["Work"]>("POST", `/rooms/${r.id}/works`, { goal: "하나" });
    const res = await call("POST", `/rooms/${r.id}/works`, { goal: "둘" });
    expect([res.status, prob(res).code, prob(res).detail]).toEqual([409, "max_concurrent_works", RW.max_concurrent + "1" + RW.max_concurrent_tail]);
    expect(prob(res).open_works).toEqual([{ id: w.id, title: "하나", status: "active" }]);
  });

  it("권한·상태 순서 — 참여자 아님 403(authz 문장) · 보관 409 · 멈춤 409 · 원 메시지는 한 번만(409 message_has_work)", async () => {
    const r = await newRoom();
    await login("seoyeon@colab.dev");
    const np = await call("POST", `/rooms/${r.id}/works`, { goal: "x" });
    expect([np.status, prob(np).detail]).toEqual([403, RW.open_not_participant]);
    await login();
    await must("POST", `/__mock/rooms/${r.id}/seed`, { blocked_reason: "manual" });
    expect(prob(await call("POST", `/rooms/${r.id}/works`, { goal: "x" })).code).toBe("room_blocked");
    await must("POST", `/__mock/rooms/${r.id}/seed`, { blocked_reason: null, unread: 1 });
    const mid = [...store().messages.values()].find((m) => m.session_id === r.id)!.id;
    const w = await must<S["Work"]>("POST", `/rooms/${r.id}/works`, { goal: "x", from_message_id: mid });
    expect(w.opened_from_message_id).toBe(mid);
    const again = await call("POST", `/rooms/${r.id}/works`, { goal: "y", from_message_id: mid });
    expect([again.status, prob(again).code, prob(again).work_id]).toEqual([409, "message_has_work", w.id]);
    await must("POST", `/rooms/${r.id}/archive`);
    expect(prob(await call("POST", `/rooms/${r.id}/works`, { goal: "z" })).code).toBe("room_archived");
  });
});

describe("S26 미션 제안 — list · get · resolve", () => {
  it("열기 — 연 사람이 Director(방 기본값도 제안자도 아니다) · 다시 처리하면 409 already_resolved + 누가·언제", async () => {
    const r = await newRoom();
    await must("PATCH", `/rooms/${r.id}`, { default_director_user_id: userId("seoyeon@colab.dev") });
    const p = await must<S["WorkProposal"]>("POST", `/__mock/rooms/${r.id}/work-proposals`, { goal: "주간 보고 자동화", rationale: "매주 같은 요청이 옵니다" });
    const frames = tap();
    const res = await must<{ proposal: S["WorkProposal"]; work: S["Work"] }>("POST", `/work-proposals/${p.id}/resolution`, { action: "accept" });
    expect(res.work.director_user_id).toBe(userId("demo@colab.dev"));
    expect(res.proposal).toMatchObject({ status: "accepted", work_id: res.work.id, decided_by: { email: "demo@colab.dev" } });
    expect(frames.find((f) => f.type === "work_proposal.resolved")?.payload).toMatchObject({ proposal_id: p.id, action: "accept", work_id: res.work.id });
    const again = await call("POST", `/work-proposals/${p.id}/resolution`, { action: "reject" });
    expect([again.status, prob(again).code, prob(again).detail, prob(again).work_id]).toEqual([409, "already_resolved", RW.already_resolved, res.work.id]);
  });

  it("거절 — 사유가 타임라인에 · 고쳐서 열기는 고친 goal 로 · action 검증 · 참여자 아니면 403", async () => {
    const r = await newRoom();
    const p1 = await must<S["WorkProposal"]>("POST", `/__mock/rooms/${r.id}/work-proposals`, { goal: "문서 정리", rationale: "r" });
    expect(prob(await call("POST", `/work-proposals/${p1.id}/resolution`, { action: "maybe" })).errors?.[0]).toMatchObject({ field: "action", message: RW.action_enum });
    const out = await must<{ proposal: S["WorkProposal"] }>("POST", `/work-proposals/${p1.id}/resolution`, { action: "reject", reason: "이미 하는 중" });
    expect(out.proposal).toMatchObject({ status: "rejected", reject_reason: "이미 하는 중" });
    const agent = p1.agent.name;
    expect([...store().messages.values()].some((m) => m.content === "데모" + RW.sys_invited_mid + agent + RW.sys_rejected_mid + "문서 정리" + RW.sys_rejected_tail + RW.sys_reject_reason + "이미 하는 중")).toBe(true);
    const p2 = await must<S["WorkProposal"]>("POST", `/__mock/rooms/${r.id}/work-proposals`, { goal: "원래 목표", rationale: "r" });
    const edited = await must<{ work: S["Work"] }>("POST", `/work-proposals/${p2.id}/resolution`, { action: "accept", work: { goal: "고친 목표" } });
    expect(edited.work.goal).toBe("고친 목표");
    const p3 = await must<S["WorkProposal"]>("POST", `/__mock/rooms/${r.id}/work-proposals`, { goal: "g", rationale: "r" });
    await login("seoyeon@colab.dev");
    const np = await call("POST", `/work-proposals/${p3.id}/resolution`, { action: "reject" });
    expect([np.status, prob(np).code]).toEqual([403, "not_participant"]);
  });
});
