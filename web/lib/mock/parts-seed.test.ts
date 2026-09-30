/**
 * 목 부분 메시지 시드(lib/mock/parts-seed) — 서버가 쓰는 모양 그대로인가:
 * 행 셋 · 같은 group_id · group_index 순 · 행마다 speech(보고 → 방장 ↩ / 요청 → 받는 에이전트) · 본문 속 멘션은 받는 쪽이 아니다 ·
 * 받는 에이전트마다 제 부분이 트리거인 task 하나 · `?group` 목록 · stream 이면 부분이 하나씩(`parts-step`).
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { resetStore, store } from "./store";
import type { Message, Room } from "@/lib/api/types";

let cookie = "";
// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function call(method: string, path: string, body?: unknown): Promise<{ status: number; body: any }> {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(), body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req) as never;
}

describe("seed-parts", () => {
  let rid = "";
  beforeEach(async () => {
    resetStore();
    const res = await dispatch({ method: "POST", path: "/auth/login", query: new URLSearchParams(), headers: new Headers(), body: { email: "demo@colab.dev", password: "password123" }, cookies: {} } as Req);
    cookie = /colab_session=([^;]+)/.exec(((res as { headers?: Record<string, string> }).headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
    const me = await call("GET", "/me");
    rid = ((await call("POST", `/workspaces/${me.body.workspaces[0].id}/rooms`, { name: "마리오 카트" })).body as Room).id;
  });

  it("행 셋 · 같은 group_id · 행마다 판정 · 본문 멘션은 받는 쪽 아님 · 받는 에이전트마다 task 하나", async () => {
    const r = await call("POST", `/__mock/rooms/${rid}/seed-parts`, {});
    expect(r.status).toBe(201);
    const rows = (await call("GET", `/rooms/${rid}/messages?group=${r.body.group_id}`)).body.items as Message[];
    expect(rows.map((m) => m.group_index)).toEqual([0, 1, 2]);
    expect(new Set(rows.map((m) => m.group_id)).size).toBe(1);
    expect(rows.every((m) => m.group_size === 3)).toBe(true);
    // v0.3.10(PRD FR-3.8 2) — 카드 없는 에이전트 → 에이전트 부분은 「질문」이다(옛 「요청」).
    expect(rows.map((m) => m.speech)).toEqual(["report", "question", "question"]);
    expect(rows[0].responds_to_message_id).toBe(r.body.order_id);
    expect(rows[2].addressees!.map((a) => a.name)).toEqual(["Developer"]);
    const s = store();
    for (const m of rows.slice(1)) expect([...s.tasks.values()].filter((t) => t.trigger_message_id === m.id)).toHaveLength(1);
    expect([...s.tasks.values()].filter((t) => t.trigger_message_id === rows[0].id)).toHaveLength(0);
  });

  it("stream — 첫 부분만, parts-step 으로 하나씩, 다 차면 404", async () => {
    const r = await call("POST", `/__mock/rooms/${rid}/seed-parts`, { stream: true });
    expect(r.body.part_ids).toHaveLength(1);
    expect((await call("POST", `/__mock/rooms/${rid}/parts-step`, { group_id: r.body.group_id })).body.remaining).toBe(1);
    expect((await call("POST", `/__mock/rooms/${rid}/parts-step`, { group_id: r.body.group_id })).body.remaining).toBe(0);
    expect((await call("POST", `/__mock/rooms/${rid}/parts-step`, { group_id: r.body.group_id })).status).toBe(404);
  });
});
