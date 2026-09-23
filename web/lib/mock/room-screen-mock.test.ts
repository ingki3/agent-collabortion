/**
 * 방 화면 목(T-R2-W2, openapi 0.2.6) — **계약 모양 · 서버 규칙 대조**. PR 본문의 "목이 흉내 낸 서버 응답 목록" 2부.
 *
 * 재는 것:
 *   · 새 방(`createRoom`)의 메시지·서브 미션이 `/sessions/{방 id}/…` 로 돈다(서버: 방 id = 세션 id) — 그래도 옛 세션은 아니다(getSession 404)
 *   · 미션 op — listWorks(옛 세션의 미션 + 연 미션) · getWork · createWork · pause/resume/complete/cancel(Director 만, 전이 409 문장)
 *   · 귀속(FR-3.1.1, 서버 router.attribute 순서) — chosen · thread · running_lane · 옛 세션 규칙(키가 없을 때만) · none, 끝난 미션 422
 *   · listMessages — work_id · no_work · around_message_id(위아래 25)
 *   · blockRoom/unblockRoom — manual 만 풀고, 이미 멈췄으면 409, 시스템 메시지 한 줄 · `room.updated`
 *   · summarizeRoom — 범위 필수 · 서로 배타 · 202 summary 메시지
 *   · listRoomParticipants — 사람(방 역할) + 에이전트 한 목록
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { dispatch, type Req } from "./handlers";
import { W } from "./wording";
import { resetStore, store, type Subscriber } from "./store";
import type { Message, MessagePage, Room, Runtime, Session, TriggerPreview, Work, WorkListItem } from "@/lib/api/types";
import type { components } from "@/lib/api/schema";

let cookie = "";
async function call(method: string, path: string, opts: { body?: unknown; headers?: Record<string, string> } = {}) {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(opts.headers ?? {}), body: opts.body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req);
}
async function must<T>(method: string, path: string, opts: { body?: unknown; headers?: Record<string, string> } = {}): Promise<T> {
  const res = await call(method, path, opts);
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(res.body)}`);
  return res.body as T;
}
async function login(email = "demo@colab.dev") {
  const res = await call("POST", "/auth/login", { body: { email, password: "password123" } });
  cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
}
const ws = async () => (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
const post = (roomId: string, content: string, extra: Record<string, unknown> = {}) =>
  must<{ message: Message }>("POST", `/sessions/${roomId}/messages`, { body: { content, ...extra }, headers: { "Idempotency-Key": crypto.randomUUID() } });
function tap(workspaceId: string) {
  const frames: { type: string; payload: Record<string, unknown> }[] = [];
  const sub: Subscriber = { workspace_id: workspaceId, session_ids: null, write: (f) => { const m = /data: (.*)\n\n$/s.exec(f); if (m) frames.push(JSON.parse(m[1])); } };
  store().subs.add(sub);
  return frames;
}
async function newRoom(id: string, name = "결제팀") {
  return must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name } });
}
async function seedAgents(roomId: string) {
  await must("POST", `/__mock/rooms/${roomId}/seed`, { body: { agents: ["Lead", "Researcher"] } });
}
const agentId = (name: string) => [...store().agents.values()].find((a) => a.name === name)!.id;
const mention = (name: string) => `[@${name}](mention://agent/${agentId(name)})`;

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

describe("새 방 — 메시지·서브 미션은 /sessions/{방 id}/… 로 돈다(옛 세션은 아니다)", () => {
  it("게시·읽기가 되고, getSession·listSessions 에는 없다", async () => {
    const id = await ws();
    const r = await newRoom(id);
    const { message } = await post(r.id, "안녕");
    expect(message.work_id).toBeNull(); // 새 방의 말은 미션 없음(서버: legacy_work_id 없음)
    const page = await must<MessagePage>("GET", `/sessions/${r.id}/messages`);
    expect(page.items.map((m) => m.content)).toEqual(["안녕"]);
    expect((await call("GET", `/sessions/${r.id}`)).status).toBe(404);
    const ses = await must<{ items: { id: string }[] }>("GET", `/workspaces/${id}/sessions`);
    expect(ses.items.some((s) => s.id === r.id)).toBe(false);
    expect((await must<{ items: WorkListItem[] }>("GET", `/rooms/${r.id}/works`)).items).toEqual([]);
  });
});

describe("미션 op", () => {
  it("createWork → listWorks·getWork(계약 Work 모양 · my_work_role) · work.created", async () => {
    const id = await ws();
    const r = await newRoom(id);
    const frames = tap(id);
    const w = await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "보고서 초안\n10페이지" } });
    expect(w).toMatchObject({ room_id: r.id, title: "보고서 초안", status: "active", my_work_role: "director", assignee_agent_id: null });
    expect(w.completion_condition).toEqual({ op: "and", conditions: [{ type: "user_approval" }] }); // 제출자가 없으면 user_approval 단독(FR-2A.1, W3 openWork)
    const items = (await must<{ items: WorkListItem[] }>("GET", `/rooms/${r.id}/works`)).items;
    expect(items.map((x) => x.id)).toEqual([w.id]);
    expect(Object.keys(items[0]).sort()).toEqual(expect.arrayContaining(["id", "room_id", "title", "goal", "status", "paused_reason", "waiting_human", "director", "assignee_agent_id", "completion_progress", "cost_usd", "budget_usd", "last_activity_at"]));
    expect((await must<Work>("GET", `/works/${w.id}`)).goal).toBe("보고서 초안\n10페이지");
    expect(frames.some((f) => f.type === "work.created" && f.payload.id === w.id)).toBe(true);
    expect((await call("POST", `/rooms/${r.id}/works`, { body: { goal: " " } })).status).toBe(422);
  });

  it("pause/resume/complete/cancel — Director 만(403 문장 서버 그대로), 전이가 안 맞으면 409 (현재 상태: …)", async () => {
    const id = await ws();
    const r = await newRoom(id);
    const w = await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "보고서" } });
    expect((await must<Work>("POST", `/works/${w.id}/pause`)).status).toBe("paused");
    const again = await call("POST", `/works/${w.id}/pause`);
    expect(again.status).toBe(409);
    expect((again.body as { detail: string }).detail).toBe(W.work_pause_transition + "일시정지)");
    expect((await must<Work>("POST", `/works/${w.id}/resume`)).status).toBe("active");
    expect((await must<Work>("POST", `/works/${w.id}/complete`, { body: { confirm: true } })).status).toBe("completed");
    expect((await call("POST", `/works/${w.id}/cancel`)).status).toBe(409);
    // Director 가 아니면 403
    const w2 = await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "비교" } });
    await login("seoyeon@colab.dev");
    const res = await call("POST", `/works/${w2.id}/pause`);
    expect(res.status).toBe(403);
    expect((res.body as { detail: string }).detail).toBe(W.work_director_required);
  });

  it("옛 세션의 방 — 그 세션이 미션 하나(미션 id = 세션 id)", async () => {
    const id = await ws();
    const rt = (await must<Runtime[]>("GET", `/workspaces/${id}/runtimes`))[0];
    const sess = await must<Session>("POST", `/workspaces/${id}/sessions`, { body: { title: "시장 조사", goal: "g", isolation: { kind: "none" }, runtime_id: rt.id, participants: [{ agent_id: agentId("Lead") }] } });
    const items = (await must<{ items: WorkListItem[] }>("GET", `/rooms/${sess.id}/works`)).items;
    expect(items.map((x) => [x.id, x.title])).toEqual([[sess.id, "시장 조사"]]);
    expect((await must<Work>("GET", `/works/${sess.id}`)).my_work_role).toBe("director");
  });
});

describe("귀속(FR-3.1.1) — 서버 router.attribute 순서", () => {
  it("chosen · none · 끝난 미션 422 · 남의 방 미션 422", async () => {
    const id = await ws();
    const r = await newRoom(id);
    await seedAgents(r.id);
    const w = await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "보고서" } });
    const pv = (body: Record<string, unknown>) => must<TriggerPreview>("POST", `/sessions/${r.id}/messages/preview`, { body: { content: "안녕", ...body } });
    expect(await pv({ work_id: w.id })).toMatchObject({ work: { id: w.id, title: "보고서" }, work_source: "chosen" });
    expect(await pv({ work_id: null })).toMatchObject({ work: null, work_source: "none" });
    expect((await post(r.id, "이건 미션에", { work_id: w.id })).message.work_id).toBe(w.id);
    await must("POST", `/works/${w.id}/complete`, { body: { confirm: true } });
    const closed = await call("POST", `/sessions/${r.id}/messages/preview`, { body: { content: "x", work_id: w.id } });
    expect(closed.status).toBe(422);
    expect(JSON.stringify(closed.body)).toContain(W.work_closed_post);
    const other = await newRoom(id, "다른 방");
    const w2 = await must<Work>("POST", `/rooms/${other.id}/works`, { body: { goal: "남의 미션" } });
    expect(JSON.stringify((await call("POST", `/sessions/${r.id}/messages/preview`, { body: { content: "x", work_id: w2.id } })).body)).toContain(W.work_not_in_room);
  });

  it("thread — 스레드의 (열린) 미션 · running_lane — 멘션 대상의 실행 중 서브 미션의 미션(자동 귀속)", async () => {
    const id = await ws();
    const r = await newRoom(id);
    await seedAgents(r.id);
    const w = await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "보고서" } });
    const root = (await post(r.id, "루트", { work_id: w.id })).message;
    expect(await must<TriggerPreview>("POST", `/sessions/${r.id}/messages/preview`, { body: { content: "답", parent_id: root.id, work_id: null } }))
      .toMatchObject({ work: { id: w.id }, work_source: "thread" });
    // Researcher 의 서브 미션을 그 미션에 매어 실행 중으로 둔다(시드)
    await must("POST", `/__mock/rooms/${r.id}/seed`, { body: { lanes: [{ agent: "Researcher", status: "running", work: null }] } });
    const lane = [...store().lanes.values()].find((l) => l.session_id === r.id && l.status === "running")!;
    lane.work_id = w.id;
    const auto = await must<TriggerPreview>("POST", `/sessions/${r.id}/messages/preview`, { body: { content: `${mention("Researcher")} 이어서`, work_id: null } });
    expect(auto).toMatchObject({ work: { id: w.id, title: "보고서" }, work_source: "running_lane" });
  });

  it("옛 세션 규칙 — 키가 **없을** 때만 옛 세션의 미션(chosen). null 을 보내면 규칙 2~4", async () => {
    const id = await ws();
    const rt = (await must<Runtime[]>("GET", `/workspaces/${id}/runtimes`))[0];
    const sess = await must<Session>("POST", `/workspaces/${id}/sessions`, { body: { title: "시장 조사", goal: "g", isolation: { kind: "none" }, runtime_id: rt.id, participants: [{ agent_id: agentId("Lead") }] } });
    const pv = (body: Record<string, unknown>) => must<TriggerPreview>("POST", `/sessions/${sess.id}/messages/preview`, { body: { content: "/note 기록", ...body } });
    expect(await pv({})).toMatchObject({ work: { id: sess.id }, work_source: "chosen" });
    expect(await pv({ work_id: null })).toMatchObject({ work: null, work_source: "none" });
  });
});

describe("listMessages — work_id · no_work · around_message_id", () => {
  it("미션 칩 거르기와 앵커(위아래 25건)", async () => {
    const id = await ws();
    const r = await newRoom(id);
    const w = await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "보고서" } });
    await post(r.id, "미션 것", { work_id: w.id });
    await post(r.id, "미션 밖", { work_id: null });
    const q = async (qs: string) => (await must<MessagePage>("GET", `/sessions/${r.id}/messages?${qs}`)).items.map((m) => m.content);
    // 미션을 연 시스템 메시지도 그 미션의 것이다(서버 SystemPost 가 work_id 를 싣는다).
    expect(await q(`work_id=${w.id}`)).toEqual([expect.stringContaining("미션을 열었습니다"), "미션 것"]);
    expect(await q("no_work=true")).toEqual(["미션 밖"]);
    const ids: string[] = [];
    for (let i = 0; i < 60; i++) ids.push((await post(r.id, `m${i}`, { work_id: null })).message.id);
    const around = await must<MessagePage>("GET", `/sessions/${r.id}/messages?around_message_id=${ids[30]}`);
    expect(around.items).toHaveLength(51);
    expect(around.items[25].id).toBe(ids[30]);
    expect(around.has_more_before).toBe(true);
    expect(around.has_more_after).toBe(true);
    const latest = await must<MessagePage>("GET", `/sessions/${r.id}/messages?limit=50`);
    expect(latest.items).toHaveLength(50);
    expect(latest.has_more_before).toBe(true);
    const older = await must<MessagePage>("GET", `/sessions/${r.id}/messages?limit=50&before=${latest.items[0].id}`);
    expect(older.items.at(-1)!.created_at <= latest.items[0].created_at).toBe(true);
  });
});

describe("메시지 시각 — 만드는 곳이 달라도 한 시계(깜빡임 회귀, T-R2-W4b)", () => {
  // CI 에서 위 두 케이스가 가끔 빨갰다: 미션 열기 시스템 메시지(rooms-dialogs.ts)는 `now()`, 게시·멈춤 메시지(handlers.ts `addMessage`)는
  // 단조 시계 `nextMsgAt()` 였다. 같은 ms 에 떨어지면 목록 순서가 uuid 에 맡겨져 「미션을 열었습니다」가 뒤로 섞였다. 앞선 테스트가 단조
  // 시계를 벽시계보다 앞으로 밀어 두면 안 겹치고, 아니면 겹친다 — 그래서 실행 순서에 따라 깜빡였다. 시계를 멈춰 겹침을 매번 만든다.
  afterEach(() => vi.useRealTimers());
  it("시계가 멈춰도 미션 열기 시스템 메시지 → 게시 → 멈춤 메시지가 시각 순서 그대로(같은 ms 없음)", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2099-01-01T00:00:00Z")); // 단조 시계보다 앞 — 첫 메시지가 벽시계와 같은 ms 에 떨어진다
    const id = await ws();
    const r = await newRoom(id);
    const w = await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "보고서" } });
    await post(r.id, "미션 것", { work_id: w.id });
    await must<Room>("POST", `/rooms/${r.id}/block`);
    const items = (await must<MessagePage>("GET", `/sessions/${r.id}/messages`)).items;
    expect(items.map((m) => m.content)).toEqual([expect.stringContaining("미션을 열었습니다"), "미션 것", "데모" + W.room_block_system]);
    const at = items.map((m) => m.created_at);
    expect(new Set(at).size).toBe(at.length);
    expect([...at].sort()).toEqual(at);
  });
});

describe("blockRoom · unblockRoom · summarizeRoom · listRoomParticipants", () => {
  it("멈춤 — manual · 멈춘 미션 수 칸 · 시스템 메시지 · room.updated, 이미 멈췄으면 409, manual 아닌 사유는 unblock 409", async () => {
    const id = await ws();
    const r = await newRoom(id);
    await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "a" } });
    await must<Work>("POST", `/rooms/${r.id}/works`, { body: { goal: "b" } });
    const frames = tap(id);
    const b = await must<Room>("POST", `/rooms/${r.id}/block`);
    expect(b.blocked_reason).toBe("manual");
    expect(b.blocked_detail).toMatchObject({ reason: "manual", works_stopped: 2, blocked_by_user: { display_name: "데모" } });
    expect(frames.some((f) => f.type === "room.updated" && f.payload.blocked_reason === "manual")).toBe(true);
    const sys = (await must<MessagePage>("GET", `/sessions/${r.id}/messages`)).items.at(-1)!;
    expect(sys.content).toBe("데모" + W.room_block_system);
    const again = await call("POST", `/rooms/${r.id}/block`);
    expect(again.status).toBe(409);
    expect((again.body as { detail: string }).detail).toBe(W.room_already_blocked + W.room_blocked_manual);
    expect((await must<Room>("POST", `/rooms/${r.id}/unblock`)).blocked_reason).toBeNull();
    expect((await call("POST", `/rooms/${r.id}/unblock`)).status).toBe(409);
    await must("POST", `/__mock/rooms/${r.id}/seed`, { body: { blocked_reason: "budget" } });
    const nm = await call("POST", `/rooms/${r.id}/unblock`);
    expect((nm.body as { code: string; detail: string })).toMatchObject({ code: "not_manual", detail: W.room_blocked_budget + W.room_not_manual_tail });
    // 멈춘 방에서는 미션을 열 수 없다
    expect((await call("POST", `/rooms/${r.id}/works`, { body: { goal: "c" } })).status).toBe(409);
  });

  it("멈춤·해제 권한 — 방장·부방장·ws owner·admin(그 밖 403 문장)", async () => {
    const id = await ws();
    const r = await newRoom(id);
    await must("POST", `/__mock/rooms/${r.id}/seed`, { body: { people: [{ email: "seoyeon@colab.dev", role: "member" }] } });
    await login("seoyeon@colab.dev");
    const res = await call("POST", `/rooms/${r.id}/block`);
    expect(res.status).toBe(403);
    expect((res.body as { detail: string }).detail).toBe(W.room_steward_required);
    expect((await must<Room>("GET", `/rooms/${r.id}`)).my_capabilities).not.toContain("block");
  });

  it("여기까지 정리 — 범위 필수 · 기간과 메시지 범위는 배타 · 202 summary 메시지", async () => {
    const id = await ws();
    const r = await newRoom(id);
    await post(r.id, "하나");
    expect(JSON.stringify((await call("POST", `/rooms/${r.id}/summaries`, { body: {} })).body)).toContain(W.summary_range_required);
    expect(JSON.stringify((await call("POST", `/rooms/${r.id}/summaries`, { body: { since: "2026-01-01T00:00:00Z", from_message_id: "x" } })).body)).toContain(W.summary_range_exclusive);
    const res = await call("POST", `/rooms/${r.id}/summaries`, { body: { since: "2000-01-01T00:00:00Z" } });
    expect(res.status).toBe(202);
    expect((res.body as Message).kind).toBe("summary");
  });

  it("참여자 — 사람(방 역할) + 에이전트 한 목록", async () => {
    const id = await ws();
    const r = await newRoom(id);
    await seedAgents(r.id);
    const items = (await must<{ items: components["schemas"]["RoomParticipant"][] }>("GET", `/rooms/${r.id}/participants`)).items;
    expect(items.map((p) => [p.kind, p.kind === "user" ? p.room_role : p.agent?.name])).toEqual([["user", "owner"], ["agent", "Lead"], ["agent", "Researcher"]]);
  });
});
