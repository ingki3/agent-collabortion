/**
 * 방 목(T-R2-W1, openapi 0.2.4) — **계약 모양 · 서버(T-R1b3 #291) 규칙 대조**. PR 본문의 "목이 흉내 낸 서버 응답 목록"이 이 파일이다.
 *
 * 재는 것:
 *   · 옛 세션에서 파생 — 방 id = 세션 id, 이름 = 제목, 설명은 비움(goal 로 채우지 않는다), 방장 = Director, 진행 중인 미션 = 끝나지 않은 세션
 *   · listRooms 거르개 — participating 기본 켜짐 · invited 방은 비참여 멤버에게 숨김(owner·admin 은 감사 열람) · q(이름·설명) · unread_only · include_archived
 *   · createRoom — 이름 한 칸(422 문장은 서버 그대로), 컴퓨터 0 대여도 201, 멱등키, 새 방에는 옛 세션이 없다(getSession 404)
 *   · 권한 — rooms.Decide/Deny 순서(볼 수 없으면 404 → 보관이 막으면 409 room_archived → 403 문장)
 *   · archive — 진행 중 할 일 409 tasks_active · deleteRoom — 진행 중 미션 409 works_active · 204 + SSE room.deleted + session.deleted
 *   · markRoomRead — 앞으로만 · `room.unread` 는 부른 사람에게만 · 남의 방 메시지는 422
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { W } from "./wording";
import { resetStore, store, type Subscriber } from "./store";
import type { Room, RoomListItem, Runtime, Session, WorkListItem } from "@/lib/api/types";

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
  expect(cookie).not.toBe("");
}
const ws = async () => (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
async function makeSession(id: string, title = "결제 시장 조사"): Promise<Session> {
  const rt = (await must<Runtime[]>("GET", `/workspaces/${id}/runtimes`))[0];
  const ags = (await must<{ items: { id: string }[] }>("GET", `/workspaces/${id}/agents`)).items;
  return must<Session>("POST", `/workspaces/${id}/sessions`, { body: { title, goal: "보고서 10페이지", isolation: { kind: "none" }, runtime_id: rt.id, participants: ags.map((a) => ({ agent_id: a.id })), assignee_agent_id: ags[0].id } });
}
const list = async (id: string, qs = "") => (await must<{ items: RoomListItem[] }>("GET", `/workspaces/${id}/rooms${qs ? `?${qs}` : ""}`)).items;
function tap(workspaceId: string, userId?: string) {
  const frames: { type: string; session_id: string | null; payload: Record<string, unknown> }[] = [];
  const sub: Subscriber = { workspace_id: workspaceId, session_ids: null, user_id: userId, write: (f) => { const m = /data: (.*)\n\n$/s.exec(f); if (m) frames.push(JSON.parse(m[1])); } };
  store().subs.add(sub);
  return frames;
}
const userId = (email: string) => [...store().users.values()].find((u) => u.email === email)!.id;

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

describe("옛 세션에서 방을 파생한다(§7 이관 규칙)", () => {
  it("방 id = 세션 id · 이름 = 제목 · 설명은 비움 · 방장 = Director · 진행 중인 미션 1 · 참여자 = 사람 + 에이전트", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    const [r] = await list(id);
    expect(r).toMatchObject({ id: sess.id, name: "결제 시장 조사", description: "", status: "active", blocked_reason: null, my_room_role: "owner", active_work_count: 1 });
    expect(r.participants.map((p) => `${p.kind}:${p.name}`)).toEqual(["user:데모", "agent:Lead", "agent:Researcher"]);
    // 계약 RoomListItem 의 required 칸이 전부 있다.
    for (const k of ["id", "name", "description", "status", "blocked_reason", "unread_count", "active_work_count", "attention", "participants", "my_room_role", "last_activity_at"]) expect(r).toHaveProperty(k);
    await must("POST", `/sessions/${sess.id}/cancel`, { body: {} });
    expect((await list(id))[0].active_work_count).toBe(0);
  });

  it("getRoom — 계약 Room 모양 · my_capabilities 는 서버 표(방장: 전부)", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    const room = await must<Room>("GET", `/rooms/${sess.id}`);
    for (const k of ["id", "workspace_id", "name", "description", "status", "visibility", "owner_user_id", "deputy_owner_user_id", "runtime_id", "isolation", "limits", "autonomy", "default_director_user_id", "blocked_reason", "unread_count", "my_room_role", "created_by", "created_at", "updated_at"]) expect(room).toHaveProperty(k);
    expect(room.my_capabilities).toEqual(["post", "invite", "configure", "link", "block", "archive", "delete", "transfer_owner", "summarize"]);
    expect((await call("GET", "/rooms/00000000-0000-4000-8000-000000000000")).body).toMatchObject({ status: 404, detail: "방을 찾을 수 없습니다" });
  });

  it("listWorks 최소 — 옛 세션 하나가 미션 하나(WorkListItem 모양) · 새 방은 미션 0", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    const works = (await must<{ items: WorkListItem[] }>("GET", `/rooms/${sess.id}/works`)).items;
    expect(works).toHaveLength(1);
    expect(works[0]).toMatchObject({ room_id: sess.id, title: sess.title, goal: sess.goal, status: "active" });
    expect((await must<{ items: WorkListItem[] }>("GET", `/rooms/${sess.id}/works?status=completed`)).items).toEqual([]);
    const fresh = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "인프라" } });
    expect((await must<{ items: WorkListItem[] }>("GET", `/rooms/${fresh.id}/works`)).items).toEqual([]);
  });
});

describe("createRoom — 이름 한 칸(FR-2.1)", () => {
  it("201 · 만든 사람이 방장 · 설명 선택 · 컴퓨터가 0 대여도 만든다 · 새 방에는 옛 세션이 없다", async () => {
    const id = await ws();
    for (const rt of [...store().runtimes.values()]) rt.status = "offline";
    const frames = tap(id);
    const res = await call("POST", `/workspaces/${id}/rooms`, { body: { name: "  결제팀 ", description: "결제 관련 논의와 작업" } });
    expect(res.status).toBe(201);
    const room = res.body as Room;
    expect(room).toMatchObject({ name: "결제팀", description: "결제 관련 논의와 작업", my_room_role: "owner", status: "active", visibility: "workspace", isolation: { kind: "none" }, runtime_id: null });
    expect(frames.filter((f) => f.type === "room.updated").map((f) => f.payload.id)).toEqual([room.id]);
    expect((await list(id)).map((r) => r.id)).toEqual([room.id]);
    expect((await call("GET", `/sessions/${room.id}`)).status).toBe(404);
  });

  it("이름이 비거나 200자를 넘으면 422 — 서버 문장 그대로, 설명 500자 초과도", async () => {
    const id = await ws();
    const r1 = await call("POST", `/workspaces/${id}/rooms`, { body: { name: "  " } });
    expect(r1.status).toBe(422);
    expect(r1.body).toMatchObject({ code: "validation_failed", errors: [{ field: "name", code: "length", message: W.room_name_1_200 }] });
    const r2 = await call("POST", `/workspaces/${id}/rooms`, { body: { name: "가".repeat(201), description: "나".repeat(501) } });
    expect((r2.body as { errors: { field: string; message: string }[] }).errors.map((e) => e.message)).toEqual([W.room_name_1_200, W.room_description_500]);
  });

  it("같은 Idempotency-Key 로 두 번 → 방은 하나", async () => {
    const id = await ws();
    const h = { "idempotency-key": "k-1" };
    const a = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" }, headers: h });
    const b = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" }, headers: h });
    expect(b.id).toBe(a.id);
    expect(await list(id)).toHaveLength(1);
  });

  it("같은 이름이 있어도 거부하지 않는다 — 중복 경고는 화면이 listRooms?q= 로 한다", async () => {
    const id = await ws();
    await must("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" } });
    expect((await call("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" } })).status).toBe(201);
    expect((await list(id, "q=결제팀&participating=false&include_archived=true")).filter((r) => r.name === "결제팀")).toHaveLength(2);
  });
});

describe("listRooms 거르개(S25)", () => {
  it("participating 기본 켜짐 — 남의 공개 방은 끄면 보인다 · invited 방은 비참여 멤버에게 숨고 owner 에게는 보인다", async () => {
    const id = await ws();
    const mine = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "내 방" } });
    await login("seoyeon@colab.dev");
    const theirs = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "서연의 공개 방" } });
    const hidden = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "서연의 초대 방" } });
    await must("PATCH", `/rooms/${hidden.id}`, { body: { visibility: "invited" } });
    await login("junho@colab.dev"); // 멤버
    expect(await list(id)).toEqual([]);
    const all = await list(id, "participating=false");
    expect(all.map((r) => r.name).sort()).toEqual(["내 방", "서연의 공개 방"].sort());
    expect(all.every((r) => r.my_room_role === null)).toBe(true);
    expect((await call("GET", `/rooms/${hidden.id}`)).status).toBe(404); // 존재를 숨긴다
    await login(); // owner — 감사 열람
    expect((await list(id, "participating=false")).map((r) => r.id).sort()).toEqual([mine.id, theirs.id, hidden.id].sort());
  });

  it("q 는 이름·설명 부분 일치 · 보관된 방은 include_archived 에서만 · unread_only", async () => {
    const id = await ws();
    const a = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀", description: "PG 연동" } });
    const b = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "인프라", description: "배포·모니터링" } });
    expect((await list(id, "q=모니터")).map((r) => r.id)).toEqual([b.id]);
    expect((await list(id, "q=pg")).map((r) => r.id)).toEqual([a.id]);
    await must("POST", `/rooms/${b.id}/archive`);
    expect((await list(id)).map((r) => r.id)).toEqual([a.id]);
    expect((await list(id, "include_archived=true")).map((r) => r.id).sort()).toEqual([a.id, b.id].sort());
    await must("POST", `/__mock/rooms/${a.id}/seed`, { body: { unread: 2 } });
    expect((await list(id, "unread_only=true")).map((r) => [r.id, r.unread_count])).toEqual([[a.id, 2]]);
  });

  it("정렬은 마지막 활동순 하나 — 활동이 없으면 만든 시각", async () => {
    const id = await ws();
    const old = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "먼저" } });
    await new Promise((r) => setTimeout(r, 5));
    const later = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "나중" } });
    expect((await list(id)).map((r) => r.id)).toEqual([later.id, old.id]);
    await new Promise((r) => setTimeout(r, 5));
    await must("POST", `/__mock/rooms/${old.id}/seed`, { body: { unread: 1 } });
    expect((await list(id)).map((r) => r.id)).toEqual([old.id, later.id]);
  });
});

describe("권한 — rooms.Decide/Deny 순서", () => {
  it("멤버(비참여)는 공개 방을 보되 보관·삭제는 403 — 서버 문장 그대로", async () => {
    const id = await ws();
    const room = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" } });
    await login("junho@colab.dev");
    expect((await must<Room>("GET", `/rooms/${room.id}`)).my_capabilities).toEqual(["post", "summarize"]);
    expect((await call("POST", `/rooms/${room.id}/archive`)).body).toMatchObject({ status: 403, code: "room_steward_required", detail: W.room_steward_required });
    expect((await call("DELETE", `/rooms/${room.id}`)).body).toMatchObject({ status: 403, code: "room_owner_required", detail: W.room_owner_required });
  });

  it("부방장은 보관할 수 있지만 삭제는 못 한다(head 만)", async () => {
    const id = await ws();
    const room = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" } });
    await must("POST", `/__mock/rooms/${room.id}/seed`, { body: { people: [{ email: "junho@colab.dev", role: "deputy" }] } });
    await login("junho@colab.dev");
    expect((await call("POST", `/rooms/${room.id}/archive`)).status).toBe(200);
    expect((await call("DELETE", `/rooms/${room.id}`)).status).toBe(403);
  });

  it("보관하면 게시 능력이 빠지고(서버 Decide: post 는 !Archived) 보관 해제로 돌아온다", async () => {
    const id = await ws();
    const room = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" } });
    const archived = await must<Room>("POST", `/rooms/${room.id}/archive`);
    expect(archived.status).toBe("archived");
    expect(archived.my_capabilities).not.toContain("post");
    expect((await must<Room>("POST", `/rooms/${room.id}/unarchive`)).status).toBe("active");
  });
});

describe("보관·삭제", () => {
  it("진행 중 할 일이 있으면 보관 409 tasks_active — 서버 문장 그대로", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    const r = await call("POST", `/rooms/${sess.id}/archive`);
    expect(r.body).toMatchObject({ status: 409, code: "tasks_active", detail: W.room_tasks_active });
  });

  it("진행 중인 미션이 있으면 삭제 409 works_active(수 칸 포함) · 끝나면 204 + room.deleted + session.deleted · 목록에서 빠짐", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    const r = await call("DELETE", `/rooms/${sess.id}`);
    expect(r.body).toMatchObject({ status: 409, code: "works_active", detail: W.room_works_active, works_active: 1 });
    await must("POST", `/sessions/${sess.id}/cancel`, { body: {} });
    const frames = tap(id);
    expect((await call("DELETE", `/rooms/${sess.id}`)).status).toBe(204);
    expect(frames.filter((f) => f.type === "room.deleted" || f.type === "session.deleted").map((f) => [f.type, f.payload.room_id ?? f.payload.session_id])).toEqual([
      ["room.deleted", sess.id],
      ["session.deleted", sess.id],
    ]);
    expect(await list(id, "include_archived=true")).toEqual([]);
    expect((await call("GET", `/sessions/${sess.id}`)).status).toBe(404);
    expect((await call("DELETE", `/rooms/${sess.id}`)).status).toBe(404);
  });

  it("옛 deleteSession 으로 지워도 방이 함께 사라진다(방 id = 세션 id)", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await list(id);
    await must("POST", `/sessions/${sess.id}/cancel`, { body: {} });
    await call("DELETE", `/sessions/${sess.id}`);
    expect(await list(id)).toEqual([]);
  });
});

describe("markRoomRead — 앞으로만 · room.unread 는 부른 사람에게만", () => {
  it("안 읽음이 줄고, 뒤로 가는 표식은 무시되고, 프레임은 내 구독에만 간다", async () => {
    const id = await ws();
    const room = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "결제팀" } });
    await must("POST", `/__mock/rooms/${room.id}/seed`, { body: { unread: 3 } });
    expect((await list(id))[0].unread_count).toBe(3);
    const ms = [...store().messages.values()].filter((m) => m.session_id === room.id).sort((a, b) => a.created_at.localeCompare(b.created_at));
    const mine = tap(id, userId("demo@colab.dev"));
    const other = tap(id, userId("seoyeon@colab.dev"));
    expect(await must("POST", `/rooms/${room.id}/read`, { body: { last_read_message_id: ms[1].id } })).toEqual({ room_id: room.id, unread_count: 1 });
    expect(await must("POST", `/rooms/${room.id}/read`, { body: { last_read_message_id: ms[0].id } })).toEqual({ room_id: room.id, unread_count: 1 });
    expect(mine.filter((f) => f.type === "room.unread").map((f) => f.payload)).toEqual([
      { room_id: room.id, unread_count: 1, last_read_message_id: ms[1].id },
      { room_id: room.id, unread_count: 1, last_read_message_id: ms[1].id },
    ]);
    expect(other.filter((f) => f.type === "room.unread")).toEqual([]);
  });

  it("다른 방의 메시지는 422 not_in_room · 참여자가 아니면 403", async () => {
    const id = await ws();
    const a = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "A" } });
    const b = await must<Room>("POST", `/workspaces/${id}/rooms`, { body: { name: "B" } });
    await must("POST", `/__mock/rooms/${b.id}/seed`, { body: { unread: 1 } });
    const mb = [...store().messages.values()].find((m) => m.session_id === b.id)!;
    expect((await call("POST", `/rooms/${a.id}/read`, { body: { last_read_message_id: mb.id } })).body).toMatchObject({ status: 422, errors: [{ field: "last_read_message_id", code: "not_in_room", message: W.room_not_in_room }] });
    await login("junho@colab.dev");
    expect((await call("POST", `/rooms/${b.id}/read`, { body: { last_read_message_id: mb.id } })).body).toMatchObject({ status: 403, detail: W.room_not_participant });
  });
});
