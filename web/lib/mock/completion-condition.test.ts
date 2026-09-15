/**
 * T-W15 목 — 종료 조건(계약 PR #232, openapi 0.1.4 · S-84). **T-S18 대조용**: 여기 잰 응답 모양이 PR 본문의 "목 응답 목록"이다.
 *
 * 재는 것:
 *   · createSession — `agent_approval` 에 `agent_id` 없으면 422 `errors[].code: reviewer_required`(field `completion_condition/conditions/<i>/agent_id`),
 *     참여자 아니면 `reviewer_not_participant`, 둘 다 다른 422 와 한 응답에 모인다.
 *   · CompletionProgress.conditions[] — `agent_id`·`agent_name`·`next_actor`·`blocked_reason`·`hitl_request_id` 가 계약 키 그대로.
 *   · updateSession — Director 만(403 director_required) · `completion_condition` 은 active·paused 에서도 · 끝난 세션 409 · 검증은 createSession 과 같다
 *     · 바꾸면 진행률을 다시 계산해 SSE `session.completion_progress {session_id, completion_progress}` · **이미 충족된 원자는 유지**.
 *   · 시작 뒤 `isolation`·`runtime_id` 는 422 immutable(서버 문장).
 *   · `/__mock/sessions/{id}/seed-legacy-condition` — 리뷰어 없는 옛 세션 → `blocked_reason: reviewer_missing`.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { MOCK_ONLY, W } from "./wording";
import { resetStore, store, type Subscriber } from "./store";
import type { Agent, Session } from "@/lib/api/types";

let cookie = "";
async function call(method: string, path: string, opts: { body?: unknown } = {}) {
  const req: Req = { method, path, query: new URLSearchParams(""), headers: new Headers(), body: opts.body, cookies: cookie ? { colab_session: cookie } : {} };
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
async function ws(): Promise<string> {
  return (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
}
async function agents(): Promise<Agent[]> {
  return (await must<{ items: Agent[] }>("GET", `/workspaces/${await ws()}/agents`)).items;
}
async function create(cc: unknown, extra: Record<string, unknown> = {}) {
  const [lead, researcher] = await agents();
  return call("POST", `/workspaces/${await ws()}/sessions`, {
    body: { title: "T", goal: "G", isolation: { kind: "none" }, participants: [{ agent_id: lead.id }, { agent_id: researcher.id }], assignee_agent_id: researcher.id, completion_condition: cc, ...extra },
  });
}
function listen(workspaceId: string) {
  const frames: { type: string; session_id: string | null; payload: Record<string, unknown> }[] = [];
  const sub: Subscriber = { workspace_id: workspaceId, session_ids: null, write: (f) => { const m = /data: (.*)\n\n$/s.exec(f); if (m) frames.push(JSON.parse(m[1])); } };
  store().subs.add(sub);
  return frames;
}

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

describe("createSession — 리뷰어 검사(422 두 코드)", () => {
  it("agent_approval 에 agent_id 가 없으면 422 reviewer_required · field 는 conditions 경로", async () => {
    const res = await create({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval" }] });
    expect(res.status).toBe(422);
    const b = res.body as { code: string; errors: { field: string; code: string; message: string }[] };
    expect(b.code).toBe("validation_failed");
    expect(b.errors).toEqual([{ field: "completion_condition/conditions/1/agent_id", code: "reviewer_required", message: MOCK_ONLY.reviewer_required }]);
  });

  it("리뷰어가 참여자가 아니면 422 reviewer_not_participant", async () => {
    const [lead, , ] = await agents();
    const wsId = await ws();
    const res = await call("POST", `/workspaces/${wsId}/sessions`, {
      body: { title: "T", goal: "G", isolation: { kind: "none" }, participants: [{ agent_id: lead.id }], completion_condition: { op: "and", conditions: [{ type: "agent_approval", agent_id: "00000000-0000-0000-0000-000000000000" }] } },
    });
    expect(res.status).toBe(422);
    expect((res.body as { errors: { code: string }[] }).errors.map((e) => e.code)).toEqual(["reviewer_not_participant"]);
  });

  it("다른 422 와 한 응답에 모인다(제목 없음 + 리뷰어 없음)", async () => {
    const res = await create({ op: "and", conditions: [{ type: "agent_approval" }] }, { title: "" });
    expect(res.status).toBe(422);
    expect((res.body as { errors: { field: string }[] }).errors.map((e) => e.field)).toEqual(["title", "completion_condition/conditions/0/agent_id"]);
  });

  it("리뷰어가 참여자면 201 — 진행률 행에 agent_id·agent_name·next_actor, blocked_reason null", async () => {
    const [lead, researcher] = await agents();
    const res = await create({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval", agent_id: lead.id }, { type: "user_approval" }] });
    expect(res.status).toBe(201);
    const s = res.body as Session;
    expect(s.completion_progress.total).toBe(3);
    expect(s.completion_progress.human_gate).toBe(true);
    const [art, appr, user] = s.completion_progress.conditions;
    // 계약 키 집합 그대로(T-S18 대조) — path·type·met 필수 + 선택 7개.
    for (const c of s.completion_progress.conditions) expect(Object.keys(c).sort()).toEqual(["agent_id", "agent_name", "blocked_reason", "hitl_request_id", "met", "met_at", "met_by", "next_actor", "path", "type"]);
    expect(art).toMatchObject({ path: "/conditions/0", type: "artifact_submitted", met: false, agent_id: researcher.id, agent_name: "Researcher", next_actor: "Researcher", blocked_reason: null });
    expect(appr).toMatchObject({ path: "/conditions/1", type: "agent_approval", met: false, agent_id: lead.id, agent_name: "Lead", next_actor: "Lead", blocked_reason: null });
    expect(user).toMatchObject({ path: "/conditions/2", type: "user_approval", met: false, agent_id: null, agent_name: null, next_actor: "director", hitl_request_id: null });
  });

  it("completion_condition 을 안 보내면 기본값(보고서 제출 AND Director 승인)", async () => {
    const res = await create(undefined);
    expect(res.status).toBe(201);
    expect((res.body as Session).completion_progress.conditions.map((c) => c.type)).toEqual(["artifact_submitted", "user_approval"]);
  });
});

describe("updateSession — completion_condition 은 active·paused 에서도(Director), 진행률 재계산 + SSE", () => {
  async function activeSession(): Promise<Session> {
    const res = await create({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }] });
    expect(res.status).toBe(201);
    return res.body as Session;
  }

  it("active 에서 리뷰어를 넣어 고치면 200 · 진행률 재계산 · session.completion_progress 한 번", async () => {
    const s = await activeSession();
    const [lead] = await agents();
    const frames = listen(s.workspace_id);
    const res = await call("PATCH", `/sessions/${s.id}`, { body: { completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval", agent_id: lead.id }, { type: "user_approval" }] } } });
    expect(res.status).toBe(200);
    const next = res.body as Session;
    expect(next.status).toBe("active");
    expect(next.completion_progress.total).toBe(3);
    expect(next.completion_progress.conditions[1]).toMatchObject({ type: "agent_approval", agent_id: lead.id, agent_name: "Lead", blocked_reason: null });
    const ev = frames.filter((f) => f.type === "session.completion_progress");
    expect(ev).toHaveLength(1);
    expect(ev[0].session_id).toBe(s.id);
    expect(Object.keys(ev[0].payload).sort()).toEqual(["completion_progress", "session_id"]);
    expect(ev[0].payload.completion_progress).toEqual(next.completion_progress);
  });

  it("이미 충족된 원자는 그대로 유지된다(met·met_at·met_by)", async () => {
    const s = await activeSession();
    const [lead] = await agents();
    await must("POST", `/__mock/sessions/${s.id}/seed-legacy-condition`, { body: { met_artifact: true } });
    const before = await must<Session>("GET", `/sessions/${s.id}`);
    expect(before.completion_progress.conditions[0].met).toBe(true);
    expect(before.completion_progress.conditions[1].blocked_reason).toBe("reviewer_missing");
    const after = await must<Session>("PATCH", `/sessions/${s.id}`, { body: { completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval", agent_id: lead.id }] } } });
    expect(after.completion_progress.conditions[0]).toMatchObject({ met: true, met_at: before.completion_progress.conditions[0].met_at, met_by: before.completion_progress.conditions[0].met_by });
    expect(after.completion_progress.conditions[1]).toMatchObject({ met: false, blocked_reason: null, agent_name: "Lead" });
    expect(after.completion_progress.met).toBe(1);
  });

  it("검증은 createSession 과 같다 — 리뷰어 없으면 422 reviewer_required, 참여자 아니면 reviewer_not_participant", async () => {
    const s = await activeSession();
    const r1 = await call("PATCH", `/sessions/${s.id}`, { body: { completion_condition: { op: "and", conditions: [{ type: "agent_approval" }] } } });
    expect(r1.status).toBe(422);
    expect((r1.body as { errors: { code: string }[] }).errors[0].code).toBe("reviewer_required");
    const r2 = await call("PATCH", `/sessions/${s.id}`, { body: { completion_condition: { op: "and", conditions: [{ type: "agent_approval", agent_id: "00000000-0000-0000-0000-000000000000" }] } } });
    expect((r2.body as { errors: { code: string }[] }).errors[0].code).toBe("reviewer_not_participant");
  });

  it("Director 가 아니면 403 director_required(서버 문장)", async () => {
    const s = await activeSession();
    await login("seoyeon@colab.dev");
    const res = await call("PATCH", `/sessions/${s.id}`, { body: { completion_condition: { op: "and", conditions: [{ type: "manual" }] } } });
    expect(res.status).toBe(403);
    expect(res.body).toMatchObject({ code: "director_required", detail: W.director_required });
  });

  it("끝난 세션은 422 immutable(서버 T-S18 과 같은 code·문장)", async () => {
    const s = await activeSession();
    await must("POST", `/sessions/${s.id}/cancel`, { body: { reason: "x" } });
    const res = await call("PATCH", `/sessions/${s.id}`, { body: { completion_condition: { op: "and", conditions: [{ type: "manual" }] } } });
    expect(res.status).toBe(422);
    expect((res.body as { errors: unknown[] }).errors).toEqual([{ field: "completion_condition", code: "immutable", message: MOCK_ONLY.condition_immutable }]);
  });

  it("artifact_submitted 의 지정 제출자가 참여자가 아니어도 같은 코드(reviewer_not_participant) · 제출자 문장", async () => {
    const s = await activeSession();
    const res = await call("PATCH", `/sessions/${s.id}`, { body: { completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", agent_id: "00000000-0000-0000-0000-000000000000" }, { type: "user_approval" }] } } });
    expect(res.status).toBe(422);
    expect((res.body as { errors: unknown[] }).errors).toEqual([{ field: "completion_condition/conditions/0/agent_id", code: "reviewer_not_participant", message: MOCK_ONLY.submitter_not_participant }]);
  });

  it("시작 뒤 isolation·runtime_id 는 422 immutable — 서버 문장 그대로", async () => {
    const s = await activeSession();
    const res = await call("PATCH", `/sessions/${s.id}`, { body: { isolation: { kind: "none" }, runtime_id: null } });
    expect(res.status).toBe(422);
    expect((res.body as { errors: { field: string; code: string; message: string }[] }).errors).toEqual([
      { field: "isolation", code: "immutable", message: W.isolation_immutable },
      { field: "runtime_id", code: "immutable", message: W.runtime_immutable },
    ]);
  });

  it("goal·한도만 바꾸면 진행률 이벤트는 없다(session.updated 만)", async () => {
    const s = await activeSession();
    const frames = listen(s.workspace_id);
    const next = await must<Session>("PATCH", `/sessions/${s.id}`, { body: { goal: "새 목표", limits: { budget_usd: 50 } } });
    expect(next.goal).toBe("새 목표");
    expect(next.limits.budget_usd).toBe(50);
    expect(next.limits.time_limit).toBe("PT4H"); // 부분 갱신 — 다른 키 보존
    expect(frames.some((f) => f.type === "session.completion_progress")).toBe(false);
    expect(frames.some((f) => f.type === "session.updated")).toBe(true);
  });
});

describe("__mock seed-legacy-condition — 리뷰어 없는 옛 세션", () => {
  it("agent_approval 에 리뷰어가 없어 blocked_reason reviewer_missing · next_actor 없음 · 총 2", async () => {
    const res = await create(undefined);
    const s = res.body as Session;
    const seeded = await must<Session>("POST", `/__mock/sessions/${s.id}/seed-legacy-condition`, { body: {} });
    expect(seeded.completion_condition).toEqual({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval" }] });
    expect(seeded.completion_progress.conditions[1]).toMatchObject({ type: "agent_approval", met: false, agent_id: null, agent_name: null, next_actor: null, blocked_reason: "reviewer_missing" });
    expect(seeded.completion_progress.human_gate).toBe(false);
    expect(seeded.completion_progress.satisfied).toBe(false);
  });
});
