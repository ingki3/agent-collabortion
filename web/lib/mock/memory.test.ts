/**
 * 목 미션 상태 원장(lib/mock/memory) — openapi v0.3.12 태그 `memory` · PRD FR-4.6 의 서버 규칙을 목이 같게 하는가.
 *  · 시드 — 종류 여섯 · 대체된 사실과 그 새 판 · 철회 · 교훈 2번 겪음(promoted)과 아직 한 번
 *  · listMemory — 기본 active · status=all · kind 거르개 · 오래된 것부터
 *  · noteMemory — content 떼고 1..300(아니면 422) · certainty 는 fact 만 · outcome 은 lesson 만 · lesson 같은 내용 → 새 행 없이 support +1(200)
 *    · plan 새로 쓰면 이전 active plan 대체(active plan 하나)
 *  · supersede — active 아니면 409 memory_not_active · kind 주면 422 kind_immutable · 새 항목이 kind·support_count 를 물려받음
 *  · retire — retired · invalidated_at · status=all 로 계속 보임
 * 회귀 주입(PR 표): lesson 중복 검사를 빼면 (교훈 중복) FAIL; plan 자동 대체를 빼면 (plan 하나) FAIL; supersede 의 active 검사를 빼면 (409) FAIL.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { resetStore } from "./store";
import type { MemoryItem, Room, Work } from "@/lib/api/types";

let cookie = "";
// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function call(method: string, path: string, body?: unknown): Promise<{ status: number; body: any }> {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(), body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req) as never;
}
async function login(email: string) {
  const res = await dispatch({ method: "POST", path: "/auth/login", query: new URLSearchParams(), headers: new Headers(), body: { email, password: "password123" }, cookies: {} } as Req);
  cookie = /colab_session=([^;]+)/.exec(((res as { headers?: Record<string, string> }).headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
}

let wid = "";
let rid = "";
const list = async (q = ""): Promise<MemoryItem[]> => {
  const r = await call("GET", `/works/${wid}/memory${q}`);
  expect(r.status).toBe(200);
  return r.body;
};
const note = (body: Record<string, unknown>) => call("POST", `/works/${wid}/memory`, body);

beforeEach(async () => {
  resetStore();
  await login("demo@colab.dev");
  const me = await call("GET", "/me");
  rid = ((await call("POST", `/workspaces/${me.body.workspaces[0].id}/rooms`, { name: "마리오 카트" })).body as Room).id;
  wid = ((await call("POST", `/rooms/${rid}/works`, { goal: "마리오 카트 만들기" })).body as Work).id;
});

describe("원장 목 — 시드", () => {
  it("seed-memory: 종류 여섯 · 대체된 사실과 새 판 · 철회 · 교훈 promoted 와 아직 한 번 · 기본 목록은 active 만", async () => {
    const r = await call("POST", `/__mock/works/${wid}/seed-memory`, {});
    expect(r.status).toBe(201);
    const ids = r.body.items as Record<string, string>;
    const all = await list("?status=all");
    expect(new Set(all.map((x) => x.kind))).toEqual(new Set(["plan", "progress", "fact", "assignment", "open_question", "lesson"]));
    const by = (id: string) => all.find((x) => x.id === id)!;
    expect(by(ids.fact_old)).toMatchObject({ status: "superseded", superseded_by: ids.fact_new, certainty: "to_verify" });
    expect(by(ids.fact_new)).toMatchObject({ status: "active", supersedes: ids.fact_old, certainty: "given" });
    expect(by(ids.fact_guess).certainty).toBe("guess");
    expect(by(ids.retired)).toMatchObject({ status: "retired" });
    expect(by(ids.retired).invalidated_at).toBeTruthy();
    expect(by(ids.lesson_promoted)).toMatchObject({ support_count: 2, promoted: true, outcome: "dead_end" });
    expect(by(ids.lesson_once)).toMatchObject({ support_count: 1, promoted: false });
    expect(all.filter((x) => x.kind === "plan" && x.status === "active")).toHaveLength(1);
    // 오래된 것부터.
    expect([...all.map((x) => x.created_at)]).toEqual([...all.map((x) => x.created_at)].sort());
    const active = await list();
    expect(active.every((x) => x.status === "active")).toBe(true);
    expect(active.map((x) => x.id)).not.toContain(ids.fact_old);
    expect(active.map((x) => x.id)).not.toContain(ids.retired);
  });
  it("seed-cards 도 같은 미션에 원장을 깐다(「원장」 탭이 선다)", async () => {
    await call("POST", `/__mock/rooms/${rid}/seed-cards`, { work_id: wid });
    const all = await list("?status=all");
    expect(all.length).toBeGreaterThan(5);
    expect(all.find((x) => x.kind === "plan" && x.status === "active")!.created_by).toMatchObject({ kind: "agent", name: "Lead" });
  });
});

describe("원장 목 — noteMemory 규칙", () => {
  it("content 는 떼고 1..300자 — 빈칸·공백만·301자는 422, 300자(한글)는 201", async () => {
    for (const content of ["", "   ", "가".repeat(301)]) expect((await note({ kind: "fact", content })).status).toBe(422);
    const ok = await note({ kind: "fact", content: `  ${"가".repeat(300)}  ` });
    expect(ok.status).toBe(201);
    expect(ok.body.content).toBe("가".repeat(300));
    expect(ok.body).toMatchObject({ support_count: 0, promoted: true, status: "active", created_by: { kind: "user", name: "데모" } });
  });
  it("certainty 는 fact 만 · outcome 은 lesson 만 남는다", async () => {
    expect((await note({ kind: "assignment", content: "A", certainty: "given", outcome: "useful" })).body).toMatchObject({ certainty: null, outcome: null });
    expect((await note({ kind: "fact", content: "B", certainty: "derived", outcome: "useful" })).body).toMatchObject({ certainty: "derived", outcome: null });
    expect((await note({ kind: "lesson", content: "C", certainty: "given", outcome: "corrected" })).body).toMatchObject({ certainty: null, outcome: "corrected", support_count: 1, promoted: false });
  });
  it("교훈 중복 — 같은(뗀) 내용이면 새 행 없이 200 으로 그 행 · 다른 작성자일 때만 support_count 2 · last_reinforced_at 갱신(v0.3.13)", async () => {
    const a = await note({ kind: "lesson", content: "타일 경계 판정은 끼인다", outcome: "dead_end" });
    expect(a.status).toBe(201);
    expect(a.body.last_reinforced_at).toBe(a.body.created_at);
    const same = await note({ kind: "lesson", content: "타일 경계 판정은 끼인다" });
    expect(same.status).toBe(200);
    expect(same.body).toMatchObject({ id: a.body.id, support_count: 1, promoted: false, last_reinforced_at: a.body.created_at });
    await login("seoyeon@colab.dev");
    const b = await note({ kind: "lesson", content: "  타일 경계 판정은 끼인다 ", outcome: "dead_end" });
    expect(b.status).toBe(200);
    expect(b.body.id).toBe(a.body.id);
    expect(b.body).toMatchObject({ support_count: 2, promoted: true });
    expect(b.body.last_reinforced_at >= a.body.created_at).toBe(true);
    expect((await note({ kind: "lesson", content: "타일 경계 판정은 끼인다" })).body.support_count).toBe(2);
    const lessons = await list("?kind=lesson&status=all");
    expect(lessons).toHaveLength(1);
    expect(lessons[0].support_count).toBe(2);
  });
  it("plan 은 미션당 active 하나 — 새 plan 이 이전 plan 을 대체한다", async () => {
    const p1 = (await note({ kind: "plan", content: "1판" })).body as MemoryItem;
    const p2 = (await note({ kind: "plan", content: "2판" })).body as MemoryItem;
    const plans = await list("?kind=plan&status=all");
    expect(plans.map((x) => [x.id, x.status])).toEqual([[p1.id, "superseded"], [p2.id, "active"]]);
    expect(plans[0].superseded_by).toBe(p2.id);
    expect(plans[0].invalidated_at).toBeTruthy();
    expect(await list("?kind=plan")).toHaveLength(1);
  });
});

describe("원장 목 — supersede · retire", () => {
  it("대체 — 201 {item, superseded}, kind·support_count 를 물려받는다", async () => {
    const l = (await note({ kind: "lesson", content: "옛 교훈", outcome: "useful" })).body as MemoryItem;
    await login("seoyeon@colab.dev");
    await note({ kind: "lesson", content: "옛 교훈" });
    const r = await call("POST", `/memory/${l.id}/supersede`, { content: "고친 교훈" });
    expect(r.status).toBe(201);
    expect(r.body.item).toMatchObject({ kind: "lesson", support_count: 2, outcome: "useful", supersedes: l.id, status: "active", content: "고친 교훈" });
    expect(r.body.superseded).toMatchObject({ id: l.id, status: "superseded", superseded_by: r.body.item.id });
  });
  it("active 가 아니면 409 memory_not_active(대체된 것 · 철회된 것) · kind 를 주면 422 kind_immutable", async () => {
    const f = (await note({ kind: "fact", content: "16px" })).body as MemoryItem;
    expect((await call("POST", `/memory/${f.id}/supersede`, { content: "32px", kind: "assignment" }))).toMatchObject({ status: 422, body: { code: "kind_immutable" } });
    expect((await call("POST", `/memory/${f.id}/supersede`, { content: "32px" })).status).toBe(201);
    expect(await call("POST", `/memory/${f.id}/supersede`, { content: "64px" })).toMatchObject({ status: 409, body: { code: "memory_not_active" } });
    const g = (await note({ kind: "fact", content: "작곡" })).body as MemoryItem;
    expect((await call("POST", `/memory/${g.id}/retire`, { reason: "범위 밖" })).status).toBe(200);
    expect(await call("POST", `/memory/${g.id}/supersede`, { content: "x" })).toMatchObject({ status: 409, body: { code: "memory_not_active" } });
    expect(await call("POST", `/memory/${g.id}/retire`, { reason: "또" })).toMatchObject({ status: 409, body: { code: "memory_not_active" } });
    expect((await call("POST", `/memory/nope/retire`, { reason: "x" })).status).toBe(404);
    const h = (await note({ kind: "fact", content: "사유 없이" })).body as MemoryItem;
    expect(await call("POST", `/memory/${h.id}/retire`, { reason: "  " })).toMatchObject({ status: 422, body: { errors: [{ field: "reason", code: "required" }] } });
  });
  it("철회 — retired · invalidated_at, 기본 목록에서 빠지고 status=all · status=retired 로는 보인다(내용은 그대로)", async () => {
    const g = (await note({ kind: "open_question", content: "미니 터보?" })).body as MemoryItem;
    const r = await call("POST", `/memory/${g.id}/retire`, { reason: "결정됨" });
    expect(r.body).toMatchObject({ id: g.id, status: "retired", content: "미니 터보?" });
    expect(r.body.invalidated_at).toBeTruthy();
    expect(r.body.retire_reason).toBe("결정됨"); // v0.3.13 — 응답 칸(#409 리뷰 NN4)
    expect((await list()).map((x) => x.id)).not.toContain(g.id);
    expect((await list("?status=all")).map((x) => x.id)).toContain(g.id);
    expect((await list("?status=retired")).map((x) => x.id)).toEqual([g.id]);
  });
  it("없는 미션은 404", async () => {
    expect((await call("GET", `/works/00000000-0000-0000-0000-000000000000/memory`)).status).toBe(404);
  });
});
