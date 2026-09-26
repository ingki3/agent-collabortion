/**
 * 목 op — T-R2-W4a(r2w4a.ts): 활동 로그 · 알림 구독 3층 · S9/S11 방 칸 · 방 기본값 · getMessage · 방장 승인 요청.
 * 문장 대조는 server-wording/k-r2w4a.test.ts 가 하고, 여기서는 **모양과 판정**을 잰다(서버 핸들러의 순서·권한·칸).
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { W } from "./wording";
import { RW4 } from "./r2w4a-wording";
import { resetStore, store } from "./store";
import type { components } from "@/lib/api/schema";
import type { Agent, InboxItem, Room, Runtime, WorkspaceSettings } from "@/lib/api/types";

type S = components["schemas"];
let cookie = "";
async function call(method: string, path: string, body?: unknown, headers: Record<string, string> = {}) {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(headers), body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req);
}
async function must<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<T> {
  const res = await call(method, path, body, headers);
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(res.body)}`);
  return res.body as T;
}
async function login(email = "demo@colab.dev") {
  const res = await call("POST", "/auth/login", { email, password: "password123" });
  cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
}
const prob = (res: { body?: unknown }) => res.body as { code?: string; detail?: string; errors?: { field: string; message: string }[] };
let wsId = "";
beforeEach(async () => {
  resetStore();
  await login();
  wsId = store().members[0].workspace_id;
});

describe("listActivityLog(S15)", () => {
  it("owner·admin 만 — 멤버는 403 admin_only 문장", async () => {
    await login("seoyeon@colab.dev");
    const res = await call("GET", `/workspaces/${wsId}/activity-log`);
    expect(res.status).toBe(403);
    expect(prob(res).detail).toBe(W.admin_only);
  });
  it("최신순 · 필터(방·행위·기간) · id 커서 · §4.18 필수 행 전부", async () => {
    await must("POST", `/workspaces/${wsId}/rooms`, { name: "결제팀" });
    await must("POST", "/__mock/activity/seed", {});
    const all = await must<{ items: S["ActivityLogEntry"][]; next_cursor: string | null }>("GET", `/workspaces/${wsId}/activity-log`);
    expect(all.items.map((e) => e.action)).toEqual(expect.arrayContaining(["room.read", "room.read.denied", "room_link.created", "room.visibility_changed", "room.audit_viewed", "room.deleted", "room.owner_succeeded"]));
    expect(all.items.map((e) => Number(e.id))).toEqual([...all.items.map((e) => Number(e.id))].sort((a, b) => b - a));
    const denied = await must<{ items: S["ActivityLogEntry"][] }>("GET", `/workspaces/${wsId}/activity-log?action=room.read.denied`);
    expect(denied.items).toHaveLength(1);
    const page1 = await must<{ items: S["ActivityLogEntry"][]; next_cursor: string | null }>("GET", `/workspaces/${wsId}/activity-log?limit=3`);
    expect(page1.items).toHaveLength(3);
    const page2 = await must<{ items: S["ActivityLogEntry"][] }>("GET", `/workspaces/${wsId}/activity-log?limit=3&cursor=${page1.next_cursor}`);
    expect(Number(page2.items[0].id)).toBeLessThan(Number(page1.items[2].id));
    const future = await must<{ items: unknown[] }>("GET", `/workspaces/${wsId}/activity-log?since=${encodeURIComponent(new Date(Date.now() + 60000).toISOString())}`);
    expect(future.items).toEqual([]);
  });
  it("마스킹이 켜지면 자유 글은 80자 + masked: true — id 는 그대로(서버 maskActivity)", async () => {
    await must("POST", `/workspaces/${wsId}/rooms`, { name: "결제팀" });
    await must("POST", "/__mock/activity/seed", {});
    await must("PATCH", `/workspaces/${wsId}/settings`, { task_event_masking: true });
    const read = (await must<{ items: S["ActivityLogEntry"][] }>("GET", `/workspaces/${wsId}/activity-log?action=room.read`)).items.find((e) => e.payload.direction === "out")!;
    expect(read.payload.masked).toBe(true);
    expect([...(read.payload.note as string)].length).toBe(81); // 80 + …
    expect(read.payload.other_room_id).toMatch(/^[0-9a-f-]{36}$/);
  });
});

describe("알림 구독 3층(0.2.9)", () => {
  it("방 — PUT 204 · Room.my_subscription 이 따라온다 · enum 밖 422", async () => {
    const r = await must<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "결제팀" });
    expect((await must<Room>("GET", `/rooms/${r.id}`)).my_subscription).toBe("all");
    expect((await call("PUT", `/rooms/${r.id}/subscription`, { level: "my_works" })).status).toBe(204);
    expect((await must<Room>("GET", `/rooms/${r.id}`)).my_subscription).toBe("my_works");
    expect((await call("PUT", `/rooms/${r.id}/subscription`, { level: "completion_only" })).status).toBe(422);
  });
  it("미션 — null 은 방을 따름 · enum 밖은 서버 문장", async () => {
    const r = await must<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "결제팀" });
    const w = await must<{ id: string }>("POST", `/rooms/${r.id}/works`, { title: "비교표", goal: "비교표", completion_condition: { type: "manual" } }, { "Idempotency-Key": crypto.randomUUID() });
    expect((await call("PUT", `/works/${w.id}/subscription`, { level: "hitl_only" })).status).toBe(204);
    expect((await must<{ subscription: string }>("GET", `/works/${w.id}`)).subscription).toBe("hitl_only");
    expect((await call("PUT", `/works/${w.id}/subscription`, { level: null })).status).toBe(204);
    const bad = await call("PUT", `/works/${w.id}/subscription`, { level: "off" });
    expect(prob(bad).errors?.[0].message).toBe(RW4.work_sub_enum);
  });
});

describe("S9 · S11 방 칸(0.2.9)", () => {
  it("에이전트 — rooms[]·room_count·hidden_room_count·running_task_count 가 붙는다", async () => {
    const list = await must<Agent[] | { items: Agent[] }>("GET", `/workspaces/${wsId}/agents`);
    const agents = Array.isArray(list) ? list : list.items;
    for (const a of agents) {
      expect(Array.isArray(a.rooms)).toBe(true);
      expect(typeof a.room_count).toBe("number");
      expect(typeof a.hidden_room_count).toBe("number");
      expect(typeof a.running_task_count).toBe("number");
    }
  });
  it("컴퓨터 — room_count", async () => {
    const rts = await must<Runtime[]>("GET", `/workspaces/${wsId}/runtimes`);
    for (const rt of rts) expect(typeof rt.room_count).toBe("number");
  });
});

describe("방 기본값 · 다른 방 읽기(S14)", () => {
  it("GET 은 빈 칸을 기본값으로 채운다 · PATCH 는 키 단위로 합친다 · 범위 밖은 서버 문장", async () => {
    const s = await must<WorkspaceSettings>("GET", `/workspaces/${wsId}/settings`);
    expect(s.room_defaults).toMatchObject({ visibility: "workspace", isolation_kind: "none", autonomy: "guided", limits: { max_concurrent_works: 3, max_parallel_lanes: 5 } });
    expect(s.room_read).toEqual({ max_rooms_per_turn: 3, max_tokens: 4000 });
    const next = await must<WorkspaceSettings>("PATCH", `/workspaces/${wsId}/settings`, { room_defaults: { visibility: "invited" }, room_read: { max_tokens: 6000 } });
    expect(next.room_defaults?.visibility).toBe("invited");
    expect(next.room_defaults?.autonomy).toBe("guided");
    expect(next.room_read).toEqual({ max_rooms_per_turn: 3, max_tokens: 6000 });
    const bad = await call("PATCH", `/workspaces/${wsId}/settings`, { room_read: { max_tokens: 100 }, room_defaults: { isolation_kind: "container" } });
    expect(bad.status).toBe(422);
    expect(prob(bad).errors?.map((e) => e.message)).toEqual([RW4.min_500, RW4.room_isolation]);
  });
});

describe("getMessage 404 · 방장 승인 요청", () => {
  it("없는 메시지는 서버 messageNotFound 와 같은 문장", async () => {
    const res = await call("GET", `/messages/${crypto.randomUUID()}`);
    expect(res.status).toBe(404);
    expect(prob(res).detail).toBe("메시지를 찾을 수 없습니다");
  });
  it("방장 승인 — 멱등키 없으면 422 · 거절에는 사유 · 방장이 아니면 403", async () => {
    const r = await must<Room>("POST", `/workspaces/${wsId}/rooms`, { name: "결제팀" });
    const [paused] = await must<InboxItem[]>("POST", "/__mock/inbox/seed-v19", { room_id: r.id, types: ["room_paused"] });
    const url = `/hitl-requests/${paused.ref_id}/response`;
    expect((await call("POST", url, { approved: true })).status).toBe(422);
    const noReason = await call("POST", url, { approved: false }, { "Idempotency-Key": "k1" });
    expect(prob(noReason).errors?.[0].message).toBe(RW4.reject_reason);
    await login("seoyeon@colab.dev");
    const other = await call("POST", url, { approved: true }, { "Idempotency-Key": "k2" });
    expect(other.status).toBe(403);
    expect(prob(other).detail).toBe(W.not_approver);
  });
});
