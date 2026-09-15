/**
 * deleteSession 목(T-W13, 계약 PR #218) — **계약 모양 대조**. 서버(T-S17)가 동시에 만들어지므로 목의 순서·code·모양을 계약 description 에
 * 맞춰 두고, PR 본문의 "목이 흉내 낸 서버 응답 목록"이 이 파일이다.
 *
 * 재는 것:
 *   · 204 → 목록에서 빠짐 · GET 404 · 두 번째 DELETE 404(멱등 아님) · SSE `session.deleted {session_id}`(envelope session_id 도 그 세션)
 *   · 끝난 세션만 — active 409 session_active(문장은 계약 description 그대로) · draft·cancelled 204
 *   · 권한 — Director 아님 + member 403 · owner 는 남의 세션도 204
 *   · 미병합/미커밋 worktree 409 workdir_unmerged + `Problem.workdirs[]`(계약 Workdir 모양, runtime_id 없음) · 정리 뒤 204 · 남은 workdir 행도 사라짐
 *   · 물리 삭제 — 메시지·작업 줄기·workdir 이 함께 사라진다
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { W } from "./wording";
import { resetStore, store, type Subscriber } from "./store";
import type { Runtime, Session, Workdir } from "@/lib/api/types";

let cookie = "";
async function call(method: string, path: string, opts: { body?: unknown } = {}) {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(), body: opts.body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req);
}
async function must<T>(method: string, path: string, opts: { body?: unknown } = {}): Promise<T> {
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
async function makeSession(id: string, over: Record<string, unknown> = {}): Promise<Session> {
  const rt = (await must<Runtime[]>("GET", `/workspaces/${id}/runtimes`))[0];
  const ags = (await must<{ items: { id: string }[] }>("GET", `/workspaces/${id}/agents`)).items;
  return must<Session>("POST", `/workspaces/${id}/sessions`, { body: { title: "지울 세션", goal: "g", isolation: { kind: "none" }, runtime_id: rt.id, participants: [{ agent_id: ags[0].id }], assignee_agent_id: ags[0].id, ...over } });
}
/** 진행 중 세션을 끝낸다(cancelled) — Director 만. */
const cancel = (sid: string) => must("POST", `/sessions/${sid}/cancel`, { body: {} });
function tap(workspaceId: string) {
  const frames: { type: string; session_id: string | null; payload: Record<string, unknown> }[] = [];
  const sub: Subscriber = { workspace_id: workspaceId, session_ids: null, write: (f) => { const m = /data: (.*)\n\n$/s.exec(f); if (m) frames.push(JSON.parse(m[1])); } };
  store().subs.add(sub);
  return frames;
}
const ids = async (id: string) => (await must<{ items: { id: string }[] }>("GET", `/workspaces/${id}/sessions`)).items.map((s) => s.id);

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

describe("204 — 물리 삭제 · SSE · 멱등 아님", () => {
  it("cancelled 세션 → 204, 목록에서 빠지고 GET 404, 두 번째 DELETE 404, SSE session.deleted {session_id}", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await cancel(sess.id);
    const frames = tap(id);
    const r = await call("DELETE", `/sessions/${sess.id}`);
    expect(r.status).toBe(204);
    expect(r.body).toBeUndefined();
    expect(await ids(id)).not.toContain(sess.id);
    expect((await call("GET", `/sessions/${sess.id}`)).status).toBe(404);
    expect((await call("GET", `/sessions/${sess.id}/messages`)).status).toBe(404);
    expect((await call("GET", `/sessions/${sess.id}/lanes`)).status).toBe(404);
    const again = await call("DELETE", `/sessions/${sess.id}`);
    expect(again.status).toBe(404);
    expect(again.body).toMatchObject({ code: "not_found", detail: "세션을 찾을 수 없습니다" });
    const del = frames.filter((f) => f.type === "session.deleted");
    expect(del).toHaveLength(1);
    expect(del[0].payload).toEqual({ session_id: sess.id });
    expect(del[0].session_id).toBe(sess.id);
  });

  it("draft 세션도 204 (끝난 세션 셋 — draft·completed·cancelled)", async () => {
    const id = await ws();
    const sess = await makeSession(id, { draft: true });
    expect(sess.status).toBe("draft");
    expect((await call("DELETE", `/sessions/${sess.id}`)).status).toBe(204);
  });

  it("저장소의 세션 소유 행이 전부 사라진다(메시지·할 일·작업 줄기·workdir)", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await cancel(sess.id);
    const s = store();
    expect([...s.messages.values()].some((m) => m.session_id === sess.id)).toBe(true);
    expect((await call("DELETE", `/sessions/${sess.id}`)).status).toBe(204);
    expect([...s.messages.values()].some((m) => m.session_id === sess.id)).toBe(false);
    expect([...s.tasks.values()].some((t) => t.session_id === sess.id)).toBe(false);
    expect([...s.lanes.values()].some((l) => l.session_id === sess.id)).toBe(false);
    expect([...s.workdirs.values()].some((w) => w.session_id === sess.id)).toBe(false);
    expect(s.sessions.has(sess.id)).toBe(false);
  });
});

describe("409 session_active — 끝나지 않은 세션", () => {
  it("active → 409 session_active, 문장은 계약 description 그대로, 세션은 남는다", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    expect(sess.status).toBe("active");
    const r = await call("DELETE", `/sessions/${sess.id}`);
    expect(r.status).toBe(409);
    expect(r.body).toEqual({ type: "https://colab.dev/problems/session_active", status: 409, title: "지금은 할 수 없음", code: "session_active", detail: W.session_active });
    expect(W.session_active).toBe("진행 중인 세션은 먼저 종료하세요");
    expect(await ids(id)).toContain(sess.id);
  });

  it("paused 도 409 session_active", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await must("POST", `/sessions/${sess.id}/pause`, { body: {} });
    expect((await call("DELETE", `/sessions/${sess.id}`)).body).toMatchObject({ code: "session_active" });
  });
});

describe("권한 — Director 또는 owner·admin", () => {
  it("Director 도 admin 도 아닌 member → 403(끝난 세션이어도), Director 인 member → 204", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await cancel(sess.id);
    await login("seoyeon@colab.dev"); // member · Director 아님
    const r = await call("DELETE", `/sessions/${sess.id}`);
    expect(r.status).toBe(403);
    expect(r.body).toMatchObject({ code: "director_or_admin_required", detail: W.delete_forbidden });
    // Director 로 바꾸면(owner 가 교체) 같은 member 가 지울 수 있다.
    await login();
    const seo = (await must<{ items: { user: { id: string; email: string } }[] }>("GET", `/workspaces/${id}/members`)).items.find((m) => m.user.email === "seoyeon@colab.dev")!;
    await must("PUT", `/sessions/${sess.id}/director`, { body: { director_user_id: seo.user.id } });
    await login("seoyeon@colab.dev");
    expect((await call("DELETE", `/sessions/${sess.id}`)).status).toBe(204);
  });

  it("권한이 상태보다 먼저다 — 권한 없는 member 는 진행 중 세션에도 403(409 가 아니라)", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await login("seoyeon@colab.dev");
    expect((await call("DELETE", `/sessions/${sess.id}`)).status).toBe(403);
  });

  it("owner 는 남의(Director 가 다른 멤버인) 끝난 세션도 204", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    const seo = (await must<{ items: { user: { id: string; email: string } }[] }>("GET", `/workspaces/${id}/members`)).items.find((m) => m.user.email === "seoyeon@colab.dev")!;
    await must("PUT", `/sessions/${sess.id}/director`, { body: { director_user_id: seo.user.id } });
    await login("seoyeon@colab.dev");
    await cancel(sess.id);
    await login();
    expect((await call("DELETE", `/sessions/${sess.id}`)).status).toBe(204);
  });

  it("로그인 없이는 401, 없는 id 는 404", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    cookie = "";
    expect((await call("DELETE", `/sessions/${sess.id}`)).status).toBe(401);
    await login();
    expect((await call("DELETE", `/sessions/00000000-0000-0000-0000-000000000000`)).status).toBe(404);
  });
});

describe("409 workdir_unmerged — 미병합/미커밋 worktree (FR-6.4 M4)", () => {
  it("차단 workdir 만 Problem.workdirs[] 에(계약 Workdir 모양) · 정리(force 삭제) 뒤 204 · 남은 행도 사라진다", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await cancel(sess.id);
    const seeded = await must<Workdir[]>("POST", `/__mock/sessions/${sess.id}/seed-workdirs`, { body: {} });
    expect(seeded).toHaveLength(3); // 깨끗 1 · 미병합 1 · 미커밋 1
    const r = await call("DELETE", `/sessions/${sess.id}`);
    expect(r.status).toBe(409);
    const body = r.body as { code: string; detail: string; workdirs: Workdir[] };
    expect(body.code).toBe("workdir_unmerged");
    expect(body.detail).toBe(W.workdir_unmerged);
    expect(body.workdirs.map((w) => w.gc_blocked_reason).sort()).toEqual(["uncommitted_changes", "unmerged_commits"]);
    for (const w of body.workdirs) {
      for (const k of ["id", "session_id", "kind", "path_or_ref", "status", "disk_bytes", "created_at", "updated_at", "branch", "dirty", "commits_ahead", "gc_blocked_reason"]) expect(w).toHaveProperty(k);
      expect(w).not.toHaveProperty("runtime_id"); // 목의 곁 칸은 계약 밖 — 응답에 싣지 않는다
      expect(w.kind).toBe("worktree");
      expect(w.session_id).toBe(sess.id);
    }
    expect(await ids(id)).toContain(sess.id); // 세션은 남는다
    // S13 에서 정리(force) → 다시 시도하면 204, 나머지 workdir 행도 사라진다.
    for (const w of body.workdirs) expect((await call("DELETE", `/workdirs/${w.id}?force=true`)).status).toBe(202);
    expect((await call("DELETE", `/sessions/${sess.id}`)).status).toBe(204);
    expect([...store().workdirs.values()].some((w) => w.session_id === sess.id)).toBe(false);
  });

  it("순서 — 진행 중이면 workdir 을 보기 전에 409 session_active", async () => {
    const id = await ws();
    const sess = await makeSession(id);
    await must<Workdir[]>("POST", `/__mock/sessions/${sess.id}/seed-workdirs`, { body: {} });
    expect((await call("DELETE", `/sessions/${sess.id}`)).body).toMatchObject({ code: "session_active" });
  });
});
