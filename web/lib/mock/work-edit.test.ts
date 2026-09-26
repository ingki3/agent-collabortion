/**
 * 미션 편집 목(T-R2-W4b) — 서버 규칙 대조: updateWork(끝난 미션 409 · 리뷰어 없는 조건 422 · 보낸 칸만) · changeWorkDirector(시스템 메시지) ·
 * summarizeRoom 의 직접 고르기(from·to 사이만 센다). 문장 글자 대조는 `server-wording/k-work-edit.test.ts`.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { W } from "./wording";
import { RW } from "./rooms-dialogs-wording";
import { WE_SERVER } from "./work-edit";
import { resetStore, store } from "./store";
import type { components } from "@/lib/api/schema";
import type { Message, Room } from "@/lib/api/types";

type Work = components["schemas"]["Work"];
let cookie = "";
async function call(method: string, path: string, body?: unknown) {
  const req: Req = { method, path, query: new URLSearchParams(), headers: new Headers(), body, cookies: cookie ? { colab_session: cookie } : {} };
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
const prob = (res: { body?: unknown }) => res.body as { code?: string; detail?: string; errors?: { field: string; message: string }[] };
let roomId = "";
beforeEach(async () => {
  resetStore();
  await login();
  roomId = (await must<Room>("POST", `/workspaces/${store().members[0].workspace_id}/rooms`, { name: "결제팀" })).id;
});

describe("updateWork", () => {
  it("보낸 칸만 바뀐다 · 끝난 미션은 409 work_closed(서버 문장)", async () => {
    const w = await must<Work>("POST", `/rooms/${roomId}/works`, { goal: "보고서", limits: { budget_usd: 20 } });
    const u = await must<Work>("PATCH", `/works/${w.id}`, { title: "새 제목" });
    expect(u).toMatchObject({ title: "새 제목", goal: "보고서", limits: { budget_usd: 20 } });
    await must("POST", `/works/${w.id}/cancel`);
    const res = await call("PATCH", `/works/${w.id}`, { goal: "다시" });
    expect(res.status).toBe(409);
    expect(prob(res)).toMatchObject({ code: "work_closed", detail: WE_SERVER.work_closed_edit.text });
  });

  it("Director 가 아니면 403 · 리뷰어 없는 검토 승인은 422(편집기와 같은 검사)", async () => {
    const w = await must<Work>("POST", `/rooms/${roomId}/works`, { goal: "보고서" });
    const bad = await call("PATCH", `/works/${w.id}`, { completion_condition: { op: "and", conditions: [{ type: "agent_approval" }] } });
    expect(bad.status).toBe(422);
    await login("seoyeon@colab.dev");
    const res = await call("PATCH", `/works/${w.id}`, { goal: "x" });
    expect(res.status).toBe(403);
    expect(prob(res).detail).toBe(W.work_director_required);
  });
});

describe("changeWorkDirector", () => {
  it("새 Director 로 바뀌고 방에 「〈이름〉 님이 이 미션의 Director 가 되었습니다.」 · owner 는 권한 통과 후 멤버 검사 422 · 그 밖은 403", async () => {
    const w = await must<Work>("POST", `/rooms/${roomId}/works`, { goal: "보고서" });
    const to = [...store().users.values()].find((u) => u.email === "seoyeon@colab.dev")!;
    const out = await must<Work>("PUT", `/works/${w.id}/director`, { director_user_id: to.id });
    expect(out).toMatchObject({ director_user_id: to.id, my_work_role: "member" });
    const sys = [...store().messages.values()].filter((m: Message) => m.session_id === roomId && m.kind === "system" && m.work_id === w.id).map((m) => m.content);
    expect(sys).toContain(to.display_name + WE_SERVER.sys_director_tail.text);
    // demo 는 이제 Director 가 아니지만 ws owner 다 — 권한은 통과하고 멤버 검사(422)에서 걸린다.
    const res = await call("PUT", `/works/${w.id}/director`, { director_user_id: "00000000-0000-0000-0000-000000000000" });
    expect(res.status).toBe(422);
    expect(prob(res).errors?.[0]).toMatchObject({ field: "user_id", message: RW.default_director_not_member }); // 옛 W.new_director_not_member(changeDirector, R4 삭제)와 같은 문장
    // Director 도 owner·admin 도 아니면 403(방장이어도 — 서버 판정에 방 역할이 없다).
    await login("junho@colab.dev");
    const no = await call("PUT", `/works/${w.id}/director`, { director_user_id: to.id });
    expect(no.status).toBe(403);
    expect(prob(no).detail).toBe(WE_SERVER.change_director_role.text);
  });
});

describe("summarizeRoom — 직접 고르기", () => {
  it("from·to 사이(양 끝 포함)만 센다 — 순서가 거꾸로 와도", async () => {
    await must("POST", `/__mock/rooms/${roomId}/seed`, { messages: ["하나", "둘", "셋", "넷"].map((content) => ({ content, work: null })) });
    const ms = [...store().messages.values()].filter((m) => m.session_id === roomId && m.kind !== "system");
    const id = (c: string) => ms.find((m) => m.content === c)!.id;
    const out = await must<Message>("POST", `/rooms/${roomId}/summaries`, { from_message_id: id("셋"), to_message_id: id("둘") });
    expect(out.kind).toBe("summary");
    expect(out.content).toContain("메시지 2건");
  });
});
