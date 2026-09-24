/**
 * v1.1 첫 라운드(T-W16) 목 — **계약 모양 대조**(openapi 0.1.5, 서버 T-S19 와 동시에 만든다 — 응답 모양은 계약 그대로).
 *
 * 재는 것:
 *   · getWorkspaceObservations — 정확히 5행 · `ObservationRow.key` enum 순서 · required 7칸(key·label·note·n·value·median·p95) · 분포형은
 *     median/p95 에 값·value null, 비율형은 value 에 값·median/p95 null · n 0 이면 값 전부 null · breakdown 은 routing_concentration 에만
 *     (kind 는 "1"~"8" 또는 platform, share 합 1) · window 422 · 멤버 아님 403
 *   · Agent.allowed_commands — 모든 Agent 응답(목록·단건·생성·수정)에 있고 role 로 계산한 읽기 전용 파생값(보내온 값은 무시) · `custom`·`lead` 는 13개 전부
 *     · 계약 enum 순서
 *   · 빈 턴 시드(`__mock/sessions/{id}/seed-empty-turn`) — PRD FR-7.2 "판정과 기록" 모양 그대로 한 행(status/turn_end/empty_turn/info, payload.args.note),
 *     새 키 없음(closed schema, S-52) · 줄기는 done · `current_task.id` 가 그 할 일 · SSE task_event.appended 로도 흐른다
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, OBSERVATION_DEFS, type Req } from "./handlers";
import { W } from "./wording";
import { resetStore, store, type Subscriber } from "./store";
import type { Agent, ColabCommand, Lane, ObservationReport, Session, TaskEvent } from "@/lib/api/types";

const ALL: ColabCommand[] = ["session_get", "session_messages", "artifact_get", "message_post", "status_set", "decision_record", "lane_delegate", "artifact_submit", "review_approve", "review_reject", "hitl_ask", "hitl_approve_request", "hitl_request_info", "room_list", "room_read", "work_propose"];
const ORDER = ["chain_scale", "chain_depth", "join_breadth", "routing_concentration", "empty_turn_rate"];

let cookie = "";
async function call(method: string, path: string, opts: { body?: unknown; headers?: Record<string, string> } = {}) {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(opts.headers ?? {}), body: opts.body, cookies: cookie ? { colab_session: cookie } : {} };
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

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

describe("getWorkspaceObservations — PRD §11 관찰 표 5행, 목표치 없음", () => {
  it("정확히 5행 · enum 순서 · required 7칸 · 보고서 4키", async () => {
    const r = await must<ObservationReport>("GET", `/workspaces/${await ws()}/observations`);
    expect(Object.keys(r).sort()).toEqual(["computed_at", "rows", "window", "workspace_id"]);
    expect(r.window).toBe("P30D");
    expect(r.rows).toHaveLength(5);
    expect(r.rows.map((x) => x.key)).toEqual(ORDER);
    expect(OBSERVATION_DEFS.map((d) => d.key)).toEqual(ORDER);
    for (const row of r.rows) {
      for (const k of ["key", "label", "note", "n", "value", "median", "p95"]) expect(row).toHaveProperty(k);
      expect(Object.keys(row).filter((k) => k !== "breakdown").sort()).toEqual(["key", "label", "median", "n", "note", "p95", "value"]);
      expect(row.label.length).toBeGreaterThan(2);
      expect(row.note.length).toBeGreaterThan(10);
      // 목표 열은 없다 — 지표 표(Metric)와 섞이지 않는다(Director 확정).
      expect(row).not.toHaveProperty("target");
      expect(row).not.toHaveProperty("target_op");
    }
  });

  it("분포형(chain_scale·chain_depth·join_breadth)은 median/p95 · 비율형은 value — 서로의 칸은 null", async () => {
    const r = await must<ObservationReport>("GET", `/workspaces/${await ws()}/observations`);
    for (const row of r.rows) {
      const dist = ["chain_scale", "chain_depth", "join_breadth"].includes(row.key);
      if (dist) expect(row.value).toBeNull();
      else { expect(row.median).toBeNull(); expect(row.p95).toBeNull(); }
      if (row.n === 0) { expect(row.value).toBeNull(); expect(row.median).toBeNull(); expect(row.p95).toBeNull(); }
      if (row.value != null) expect(row.value).toBeGreaterThanOrEqual(0);
      if (row.value != null) expect(row.value).toBeLessThanOrEqual(1);
    }
    // "아직 잴 수 없음" 경로와 "p95 없이 중앙값만" 경로가 화면에 보이게 — 일부는 null.
    expect(r.rows.some((x) => x.n === 0)).toBe(true);
    expect(r.rows.some((x) => x.n > 0 && x.median != null && x.p95 == null)).toBe(true);
    expect(r.rows.some((x) => x.n > 0 && x.median != null && x.p95 != null)).toBe(true);
  });

  it("breakdown 은 routing_concentration 에만 — kind 는 규칙 번호 문자열 또는 platform, share 합 1, n 합 = 행의 n", async () => {
    const r = await must<ObservationReport>("GET", `/workspaces/${await ws()}/observations`);
    for (const row of r.rows) {
      if (row.key !== "routing_concentration") { expect(row.breakdown).toBeUndefined(); continue; }
      expect(row.breakdown!.length).toBeGreaterThan(1);
      for (const b of row.breakdown!) {
        expect(Object.keys(b).sort()).toEqual(["kind", "n", "share"]);
        expect(b.kind).toMatch(/^([1-8]|platform)$/);
      }
      expect(Math.round(row.breakdown!.reduce((a, b) => a + b.share, 0) * 100) / 100).toBe(1);
      expect(row.breakdown!.reduce((a, b) => a + b.n, 0)).toBe(row.n);
      // value = 규칙 6·7 폴백 비율(계약 정의 4).
      const fallback = row.breakdown!.filter((b) => b.kind === "6" || b.kind === "7").reduce((a, b) => a + b.share, 0);
      expect(Math.round(fallback * 100) / 100).toBe(Math.round(row.value! * 100) / 100);
    }
  });

  it("window 를 돌려준다 · 기간 표기가 아니면 422(지표 op 과 같은 문장) · 멤버 아니면 403", async () => {
    const id = await ws();
    expect((await must<ObservationReport>("GET", `/workspaces/${id}/observations?window=P7D`)).window).toBe("P7D");
    const bad = await call("GET", `/workspaces/${id}/observations?window=30d`);
    expect(bad.status).toBe(422);
    expect((bad.body as { errors: { field: string; message: string }[] }).errors[0]).toEqual({ field: "window", message: W.metrics_window_format });
    await call("POST", "/auth/signup", { body: { email: "x@colab.dev", password: "password123", display_name: "x", workspace_name: "다른팀" } });
    await login("x@colab.dev");
    expect((await call("GET", `/workspaces/${id}/observations`)).status).toBe(403);
  });
});

describe("Agent.allowed_commands — role 로 계산한 읽기 전용 파생값(K-19)", () => {
  const byRole: Record<Agent["role"], ColabCommand[]> = {
    lead: ALL,
    researcher: ALL.filter((c) => !["lane_delegate", "review_approve", "review_reject", "hitl_approve_request", "work_propose"].includes(c)),
    writer: ALL.filter((c) => !["lane_delegate", "review_approve", "review_reject", "hitl_approve_request", "work_propose"].includes(c)),
    engineer: ALL.filter((c) => !["lane_delegate", "review_approve", "review_reject", "hitl_approve_request", "work_propose"].includes(c)),
    reviewer: ALL.filter((c) => !["lane_delegate", "artifact_submit", "hitl_approve_request", "work_propose"].includes(c)),
    custom: ALL,
  };

  it("시드 에이전트(Lead·Researcher)의 목록이 §2.5 표와 같고 enum 순서다", async () => {
    const items = (await must<{ items: Agent[] }>("GET", `/workspaces/${await ws()}/agents`)).items;
    const lead = items.find((a) => a.name === "Lead")!, res = items.find((a) => a.name === "Researcher")!;
    expect(lead.allowed_commands).toEqual(byRole.lead);
    expect(res.allowed_commands).toEqual(byRole.researcher);
    expect(res.allowed_commands).toHaveLength(11);
  });

  it.each(Object.keys(byRole) as Agent["role"][])("createAgent(%s) → 그 역할의 목록 · PATCH role 이 바뀌면 다시 계산 · 보내온 allowed_commands 는 무시", async (role) => {
    const id = await ws();
    const a = await must<Agent>("POST", `/workspaces/${id}/agents`, { body: { name: `t-${role}`, role, role_description: "x", instructions: "x" } });
    expect(a.allowed_commands).toEqual(byRole[role]);
    expect(await must<Agent>("GET", `/agents/${a.id}`).then((x) => x.allowed_commands)).toEqual(byRole[role]);
    const next: Agent["role"] = role === "reviewer" ? "lead" : "reviewer";
    const p = await must<Agent>("PATCH", `/agents/${a.id}`, { body: { role: next, allowed_commands: ["lane_delegate"] } });
    expect(p.allowed_commands).toEqual(byRole[next]);
    const same = await must<Agent>("PATCH", `/agents/${a.id}`, { body: { allowed_commands: [] } });
    expect(same.allowed_commands).toEqual(byRole[next]);
  });
});

describe("빈 턴 시드 — PRD FR-7.2 「판정과 기록」 모양 그대로", () => {
  async function session(): Promise<{ sess: Session; researcher: Agent }> {
    const id = await ws();
    const items = (await must<{ items: Agent[] }>("GET", `/workspaces/${id}/agents`)).items;
    const researcher = items.find((a) => a.name === "Researcher")!;
    const rt = (await must<{ id: string }[]>("GET", `/workspaces/${id}/runtimes`))[0];
    const sess = await must<Session>("POST", `/workspaces/${id}/sessions`, {
      body: { title: "t", goal: "g", isolation: { kind: "none" }, runtime_id: rt.id, participants: [{ agent_id: researcher.id }], assignee_agent_id: researcher.id },
    });
    return { sess, researcher };
  }

  it("한 행 — status/turn_end/empty_turn/info · payload {command, args.note} · 새 키 없음 · sentence 없음(note 가 문장)", async () => {
    const { sess } = await session();
    const seeded = await must<{ lane_id: string; task_id: string; event_id: string }>("POST", `/__mock/sessions/${sess.id}/seed-empty-turn`);
    const evs = (await must<{ items: TaskEvent[] }>("GET", `/tasks/${seeded.task_id}/events`)).items;
    const row = evs.find((e) => e.id === seeded.event_id)!;
    expect(row).toMatchObject({ class: "status", verb: "turn_end", object_ref: "empty_turn", outcome: "info", sentence: null });
    expect(row.payload).toEqual({ command: "turn_end", args: { note: W.empty_turn_note } });
    expect(W.empty_turn_note).toBe("아무것도 하지 않고 턴을 끝냈습니다");
    // 이 실행에는 메시지 게시·플랫폼 조작·편집이 없다(판정 조건) — status 행은 빈 턴 행 하나뿐, tool edit_file 0.
    expect(evs.filter((e) => e.class === "status")).toHaveLength(1);
    expect(evs.some((e) => e.class === "tool" && e.verb === "edit_file")).toBe(false);
    expect(evs.some((e) => e.class === "status" && e.verb === "post_message")).toBe(false);
  });

  it("줄기는 done · brief 없음(사람이 만든 줄기) · current_task.id 가 그 할 일 — 카드가 이벤트와 줄기를 잇는 열쇠", async () => {
    const { sess } = await session();
    const seeded = await must<{ lane_id: string; task_id: string }>("POST", `/__mock/sessions/${sess.id}/seed-empty-turn`);
    const lanes = await must<Lane[]>("GET", `/sessions/${sess.id}/lanes`);
    const lane = lanes.find((l) => l.id === seeded.lane_id)!;
    expect(lane.status).toBe("done");
    expect(lane.brief).toBeNull();
    expect(lane.current_task?.id).toBe(seeded.task_id);
    expect(lane.current_task?.status).toBe("completed");
  });

  it("SSE — task_event.appended 로 그 행이 흐른다(활동 보기를 열지 않은 화면도 받는다)", async () => {
    const { sess } = await session();
    const got: { type: string; payload: unknown }[] = [];
    const sub: Subscriber = { workspace_id: sess.workspace_id, session_ids: null, write: (f) => { const m = /data: (.*)\n\n$/s.exec(f); if (m) got.push(JSON.parse(m[1])); } };
    store().subs.add(sub);
    await must("POST", `/__mock/sessions/${sess.id}/seed-empty-turn`);
    store().subs.delete(sub);
    const appended = got.filter((g) => g.type === "task_event.appended").map((g) => g.payload as TaskEvent);
    expect(appended.some((e) => e.class === "status" && e.verb === "turn_end" && e.object_ref === "empty_turn")).toBe(true);
    expect(got.some((g) => g.type === "lane.updated" && (g.payload as Lane).status === "done")).toBe(true);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
/**
 * K-16(계약 cancelLane v0.1.6, PR #255) — 취소 판정은 lane 이 아니라 **현재 할 일**. `colab status set done` 뒤에도 그 턴의 프로세스가
 * 돌면(현재 task running) done lane 에 `actions: ["cancel"]` 이 실리고 취소가 202 다 — lane 은 done 그대로, 할 일만 cancelled.
 * 도는 할 일이 없는 done lane 은 예전대로 409 lane_not_cancellable. 권한(멤버)은 목록에서도 빠지고 403.
 */
describe("K-16 — done 인데 실행이 아직 도는 줄기의 취소(계약 v0.1.6)", () => {
  async function seeded() {
    await login();
    const w = await ws();
    const agents = await must<{ items: Agent[] }>("GET", `/workspaces/${w}/agents`);
    const sess = await must<Session>("POST", `/workspaces/${w}/sessions`, { body: { title: "K-16", goal: "done 뒤 취소", isolation: { kind: "none" }, participants: [{ agent_id: agents.items[0].id }], assignee_agent_id: agents.items[0].id } });
    const seed = await must<{ lane_id: string; task_id: string; lane: Lane }>("POST", `/__mock/sessions/${sess.id}/seed-done-running`, {});
    return { sess, seed };
  }

  it("시드: lane done · current_task running · actions 에 cancel(restart 는 없다)", async () => {
    const { sess, seed } = await seeded();
    const lanes = await must<Lane[]>("GET", `/sessions/${sess.id}/lanes`);
    const lane = lanes.find((l) => l.id === seed.lane_id)!;
    expect(lane.status).toBe("done");
    expect(lane.current_task?.id).toBe(seed.task_id);
    expect(lane.current_task?.status).toBe("running");
    expect(lane.actions).toEqual(["cancel"]);
    expect(lane.brief).toBe("경쟁사 5곳 정리 완료");
  });

  it("취소 202 — lane 은 done 그대로(산출물은 제출됐다), 할 일만 cancelled, actions 는 빈다, 활동 '사람이 중단함'", async () => {
    const { sess, seed } = await seeded();
    const res = await call("POST", `/lanes/${seed.lane_id}/cancel`, {});
    expect(res.status).toBe(202);
    const lane = res.body as Lane;
    expect(lane.status).toBe("done");
    expect(lane.failure_kind).toBeNull();
    expect(lane.current_task?.status).toBe("cancelled");
    expect(lane.current_task?.failure_kind).toBe("cancelled");
    expect(lane.actions).toEqual([]);
    const events = (await must<{ items: TaskEvent[] }>("GET", `/tasks/${seed.task_id}/events`)).items;
    expect(events.some((e) => e.class === "status" && e.verb === "cancel" && e.sentence === "사람이 중단함")).toBe(true);
    // 두 번째 취소는 이제 409 — 도는 할 일이 없다.
    const again = await call("POST", `/lanes/${seed.lane_id}/cancel`, {});
    expect(again.status).toBe(409);
    expect((again.body as { code: string }).code).toBe("lane_not_cancellable");
    void sess;
  });

  it("보통의 done lane(도는 할 일 없음)은 예전대로 409 · actions 에 cancel 없음", async () => {
    await login();
    const w = await ws();
    const agents = await must<{ items: Agent[] }>("GET", `/workspaces/${w}/agents`);
    const sess = await must<Session>("POST", `/workspaces/${w}/sessions`, { body: { title: "K-16b", goal: "보통 done", isolation: { kind: "none" }, participants: [{ agent_id: agents.items[0].id }], assignee_agent_id: agents.items[0].id } });
    const [done] = await must<Lane[]>("POST", `/__mock/sessions/${sess.id}/seed-lanes`, { body: { statuses: ["done"] } });
    expect(done.actions).toEqual([]);
    const res = await call("POST", `/lanes/${done.id}/cancel`, {});
    expect(res.status).toBe(409);
  });

  it("일반 멤버 시점 — done+running 이어도 목록에서 빠지고 403(E10-05 와 같다)", async () => {
    const { sess, seed } = await seeded();
    await must("POST", `/__mock/sessions/${sess.id}/role`, { body: { role: "member" } });
    const lanes = await must<Lane[]>("GET", `/sessions/${sess.id}/lanes`);
    expect(lanes.find((l) => l.id === seed.lane_id)!.actions).toEqual([]);
    expect((await call("POST", `/lanes/${seed.lane_id}/cancel`, {})).status).toBe(403);
  });
});
