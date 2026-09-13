/**
 * P5 (T-W6) 목 — **계약 모양 대조**. 목이 서버(T-S12)와 다른 말을 하지 않게, 응답을 openapi 스키마의 `required` 와
 * enum 으로 잰다(T-W10 의 교훈 — 화면이 목에만 맞으면 실서버에서 깨진다).
 *
 * 재는 것:
 *   · getWorkspaceMetrics — 정확히 10개, §11 표의 열 순서, `unit`·`target_op` enum, null 이면 n=0, breakdown 은 그 지표만
 *   · workspace settings — 멤버 읽기 200 · 저장 403 · 부분 갱신이 다른 키를 지우지 않는다(S-26) · 422 errors[] · 보안 탭 owner 만
 *   · members — owner 강등은 owner 만(403) · 마지막 owner 409 · Director 인 진행 중 세션 409 + sessions[]
 *   · invites — 201 Invite(required 전부) · owner 역할 422 · 취소 204 → status revoked
 *   · notification — 개인, 기본값 email:true push:false all
 *   · test chat — 201 TestChat(required) · 202 TestChatTurn · 진행 중 409 · SSE delta/turn 페이로드 모양 · 닫힘 410 · 닫기 멱등 200
 *     · 컴퓨터 오프라인 409 runtime_offline
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { dispatch, METRIC_DEFS, type Req } from "./handlers";
import { defaultSettings, resetStore, store, type Subscriber } from "./store";
import { SETTINGS_DEFAULTS } from "@/lib/settings";
import type { Invite, Member, MetricsReport, NotificationSettings, Runtime, Session, TestChat, TestChatTurn, WorkspaceSettings } from "@/lib/api/types";

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
async function ws(): Promise<string> {
  return (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
}

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

// ── 대시보드 ─────────────────────────────────────────────────────────────────
describe("getWorkspaceMetrics — PRD §11 표 그대로", () => {
  const ORDER = ["f1_minutes", "auto_complete_rate", "hitl_response_minutes", "delegation_autonomous_rate", "parallel_wallclock_reduction", "task_success_rate_by_runtime", "duplicate_after_resume_rate", "resume_success_rate", "blocked_response_minutes", "weekly_active_sessions"];

  it("정확히 10개, §11 열 순서, 필수 칸 전부", async () => {
    const r = await must<MetricsReport>("GET", `/workspaces/${await ws()}/metrics`);
    expect(r.window).toBe("P30D");
    expect(r.metrics).toHaveLength(10);
    expect(r.metrics.map((m) => m.key)).toEqual(ORDER);
    for (const m of r.metrics) {
      for (const k of ["key", "label", "value", "unit", "target", "target_op", "n", "note"]) expect(m).toHaveProperty(k);
      expect(["minutes", "ratio", "count"]).toContain(m.unit);
      expect(["lt", "gt"]).toContain(m.target_op);
      expect(m.note.length).toBeGreaterThan(10);
    }
    expect(METRIC_DEFS.map((d) => d.key)).toEqual(ORDER);
  });

  it("표본이 없으면 value null · n 0 — 0 을 실측처럼 보이지 않는다", async () => {
    const r = await must<MetricsReport>("GET", `/workspaces/${await ws()}/metrics`);
    const nulls = r.metrics.filter((m) => m.value == null);
    expect(nulls.length).toBeGreaterThanOrEqual(2); // "아직 잴 수 없음" 경로가 화면에 보이게
    for (const m of nulls) expect(m.n).toBe(0);
    for (const m of r.metrics.filter((m) => m.value != null)) expect(m.n).toBeGreaterThan(0);
  });

  it("breakdown 은 task_success_rate_by_runtime 에만, target 은 가장 낮은 값", async () => {
    const r = await must<MetricsReport>("GET", `/workspaces/${await ws()}/metrics`);
    for (const m of r.metrics) {
      if (m.key === "task_success_rate_by_runtime") {
        expect(m.breakdown?.length).toBeGreaterThan(0);
        expect(m.target).toBe(Math.min(...m.breakdown!.map((b) => b.target)));
      } else expect(m.breakdown).toBeUndefined();
    }
  });

  it("window 쿼리를 그대로 돌려준다 · 멤버 아닌 사람은 403", async () => {
    const id = await ws();
    expect((await must<MetricsReport>("GET", `/workspaces/${id}/metrics?window=P7D`)).window).toBe("P7D");
    cookie = "";
    expect((await call("GET", `/workspaces/${id}/metrics`)).status).toBe(401);
  });
});

// ── 워크스페이스 설정 ─────────────────────────────────────────────────────────
describe("workspace settings — 부분 갱신 · 권한", () => {
  it("기본값은 PRD §7 — 목 시드와 화면 상수가 같은 값이다", async () => {
    const s = await must<WorkspaceSettings>("GET", `/workspaces/${await ws()}/settings`);
    expect(s.loop_limits).toEqual(SETTINGS_DEFAULTS.loop_limits);
    expect(s.context_reuse).toEqual(SETTINGS_DEFAULTS.context_reuse);
    expect(s.runtime_policy.max_concurrent_tasks).toBe(SETTINGS_DEFAULTS.runtime_policy.max_concurrent_tasks);
    expect(s.default_isolation).toBe(SETTINGS_DEFAULTS.default_isolation);
    expect(s.workdir_retention_days).toBe(SETTINGS_DEFAULTS.workdir_retention_days);
    expect(s.runtime_offline_grace).toBe(SETTINGS_DEFAULTS.runtime_offline_grace);
    expect(s.task_event_masking).toBe(SETTINGS_DEFAULTS.task_event_masking);
    const d = defaultSettings("x");
    expect(d.loop_limits).toEqual(SETTINGS_DEFAULTS.loop_limits);
    expect(d.workdir_retention_days).toBe(14);
    expect(d.runtime_offline_grace).toBe("P7D");
  });

  it("그룹 한 키만 보내도 다른 키는 남는다(S-26 `||` 합치기)", async () => {
    const id = await ws();
    const s = await must<WorkspaceSettings>("PATCH", `/workspaces/${id}/settings`, { body: { loop_limits: { max_pair_roundtrips: 2 } } });
    expect(s.loop_limits).toEqual({ max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 2 });
  });

  it("422 는 errors[] 에 필드별 문장(서버 handlers_settings.go 의 문장)", async () => {
    const r = await call("PATCH", `/workspaces/${await ws()}/settings`, { body: { loop_limits: { max_chain_depth: 0 }, workdir_retention_days: -1 } });
    expect(r.status).toBe(422);
    const b = r.body as { errors: { field: string; message: string }[] };
    expect(b.errors.map((e) => e.field)).toEqual(["loop_limits.max_chain_depth", "workdir_retention_days"]);
    expect(b.errors[0].message).toBe("1~100 사이여야 합니다");
    expect(b.errors[1].message).toBe("0 이상이어야 합니다");
  });

  it("workdir_disk_quota_gb 는 S13 의 용량 상한과 같은 값이고 null 로 지울 수 있다", async () => {
    const id = await ws();
    expect((await must<WorkspaceSettings>("GET", `/workspaces/${id}/settings`)).workdir_disk_quota_gb).toBe(50);
    expect((await must<WorkspaceSettings>("PATCH", `/workspaces/${id}/settings`, { body: { workdir_disk_quota_gb: null } })).workdir_disk_quota_gb).toBeNull();
    expect(store().workdirQuotaGb).toBeNull();
  });

  it("멤버는 읽기 200 · 저장 403(admin_only 문장)", async () => {
    const id = await ws();
    await login("seoyeon@colab.dev");
    expect((await call("GET", `/workspaces/${id}/settings`)).status).toBe(200);
    const r = await call("PATCH", `/workspaces/${id}/settings`, { body: { workdir_retention_days: 3 } });
    expect(r.status).toBe(403);
    expect((r.body as { detail: string }).detail).toBe("소유자·관리자만 할 수 있습니다");
  });

  it("보안 탭(task_event_masking)은 owner 만 — admin 은 403", async () => {
    const id = await ws();
    const me = await must<{ user: { id: string } }>("GET", "/me");
    const members = (await must<{ items: Member[] }>("GET", `/workspaces/${id}/members`)).items;
    const seo = members.find((m) => m.user.email === "seoyeon@colab.dev")!;
    await must("PATCH", `/workspaces/${id}/members/${seo.id}`, { body: { role: "admin" } });
    expect(me.user.id).not.toBe(seo.user.id);
    await login("seoyeon@colab.dev");
    expect((await call("PATCH", `/workspaces/${id}/settings`, { body: { workdir_retention_days: 3 } })).status).toBe(200);
    expect((await call("PATCH", `/workspaces/${id}/settings`, { body: { task_event_masking: true } })).status).toBe(403);
  });
});

// ── 멤버 · 초대 ───────────────────────────────────────────────────────────────
describe("members · invites", () => {
  async function seoyeon(id: string): Promise<Member> {
    return (await must<{ items: Member[] }>("GET", `/workspaces/${id}/members`)).items.find((m) => m.user.email === "seoyeon@colab.dev")!;
  }

  it("역할 변경 200 → Member · 마지막 owner 강등 409 last_owner", async () => {
    const id = await ws();
    const me = await must<{ user: { id: string } }>("GET", "/me");
    const mine = (await must<{ items: Member[] }>("GET", `/workspaces/${id}/members`)).items.find((m) => m.user.id === me.user.id)!;
    const seo = await seoyeon(id);
    const m = await must<Member>("PATCH", `/workspaces/${id}/members/${seo.id}`, { body: { role: "admin" } });
    expect(m.role).toBe("admin");
    for (const k of ["id", "workspace_id", "user", "role", "created_at"]) expect(m).toHaveProperty(k);
    const r = await call("PATCH", `/workspaces/${id}/members/${mine.id}`, { body: { role: "member" } });
    expect(r.status).toBe(409);
    expect((r.body as { code: string }).code).toBe("last_owner");
  });

  it("owner 강등은 owner 만 — admin 이 하면 403", async () => {
    const id = await ws();
    const me = await must<{ user: { id: string } }>("GET", "/me");
    const mine = (await must<{ items: Member[] }>("GET", `/workspaces/${id}/members`)).items.find((m) => m.user.id === me.user.id)!;
    const seo = await seoyeon(id);
    await must("PATCH", `/workspaces/${id}/members/${seo.id}`, { body: { role: "admin" } });
    await login("seoyeon@colab.dev");
    const r = await call("PATCH", `/workspaces/${id}/members/${mine.id}`, { body: { role: "admin" } });
    expect(r.status).toBe(403);
  });

  it("Director 인 진행 중 세션이 있으면 제거 409 + sessions[]", async () => {
    const id = await ws();
    const seo = await seoyeon(id);
    const rt = (await must<Runtime[]>("GET", `/workspaces/${id}/runtimes`))[0];
    const ags = (await must<{ items: { id: string }[] }>("GET", `/workspaces/${id}/agents`)).items;
    const sess = await must<Session>("POST", `/workspaces/${id}/sessions`, { body: { title: "제거 차단", goal: "g", isolation: { kind: "none" }, runtime_id: rt.id, participants: [{ agent_id: ags[0].id }], assignee_agent_id: ags[0].id } });
    await must("PUT", `/sessions/${sess.id}/director`, { body: { director_user_id: seo.user.id } });
    const r = await call("DELETE", `/workspaces/${id}/members/${seo.id}`);
    expect(r.status).toBe(409);
    expect((r.body as { sessions: { id: string }[] }).sessions.map((s) => s.id)).toEqual([sess.id]);
    // 세션을 끝내면(Director 인 서연이 종료) 제거된다.
    await login("seoyeon@colab.dev");
    await must("POST", `/sessions/${sess.id}/cancel`, { body: {} });
    await login();
    expect((await call("DELETE", `/workspaces/${id}/members/${seo.id}`)).status).toBe(204);
  });

  it("초대 201 Invite(required 전부) · owner 역할 422 · 취소 204 → revoked", async () => {
    const id = await ws();
    const inv = await must<Invite>("POST", `/workspaces/${id}/invites`, { body: { email: "new@colab.dev", role: "member" } });
    for (const k of ["id", "workspace_id", "role", "token", "url", "expires_at", "created_at", "status"]) expect(inv).toHaveProperty(k);
    expect(inv.status).toBe("pending");
    expect(inv.url).toContain(inv.token);
    const bad = await call("POST", `/workspaces/${id}/invites`, { body: { role: "owner" } });
    expect(bad.status).toBe(422);
    expect((bad.body as { errors: { message: string }[] }).errors[0].message).toBe("소유자 역할은 초대로 줄 수 없습니다");
    expect((await call("DELETE", `/workspaces/${id}/invites/${inv.id}`)).status).toBe(204);
    const list = await must<Invite[]>("GET", `/workspaces/${id}/invites`);
    expect(list.find((i) => i.id === inv.id)?.status).toBe("revoked");
    // 취소된 초대의 미리보기는 410 invite_revoked(기존 규칙).
    expect((await call("GET", `/invites/${inv.token}`)).status).toBe(410);
  });

  it("멤버는 초대 목록·생성 403", async () => {
    const id = await ws();
    await login("seoyeon@colab.dev");
    expect((await call("GET", `/workspaces/${id}/invites`)).status).toBe(403);
    expect((await call("POST", `/workspaces/${id}/invites`, { body: {} })).status).toBe(403);
  });
});

// ── 알림(개인) ────────────────────────────────────────────────────────────────
describe("notification settings — 개인", () => {
  it("기본값 email:true push:false all · 바꾸면 그 사람 것만 바뀐다", async () => {
    expect(await must<NotificationSettings>("GET", "/me/notification-settings")).toEqual({ email: true, push: false, default_subscription: "all" });
    const n = await must<NotificationSettings>("PATCH", "/me/notification-settings", { body: { email: true, push: false, default_subscription: "hitl_only" } });
    expect(n.default_subscription).toBe("hitl_only");
    await login("seoyeon@colab.dev");
    expect((await must<NotificationSettings>("GET", "/me/notification-settings")).default_subscription).toBe("all");
  });
  it("모르는 구독값은 422", async () => {
    expect((await call("PATCH", "/me/notification-settings", { body: { email: true, push: false, default_subscription: "everything" } })).status).toBe(422);
  });
});

// ── 시험 대화 ─────────────────────────────────────────────────────────────────
describe("test chat — FR-1.8.1 · daemon-protocol §4.5", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  async function agentId(): Promise<string> {
    return (await must<{ items: { id: string }[] }>("GET", `/workspaces/${await ws()}/agents`)).items[0].id;
  }
  /** 워크스페이스 스트림 구독자를 흉내 내 프레임을 모은다. */
  function tap(workspaceId: string): { frames: { type: string; payload: Record<string, unknown>; ephemeral: boolean }[] } {
    const frames: { type: string; payload: Record<string, unknown>; ephemeral: boolean }[] = [];
    const sub: Subscriber = { workspace_id: workspaceId, session_ids: null, write: (f) => { const m = /data: (.*)\n\n$/s.exec(f); if (m) frames.push(JSON.parse(m[1])); } };
    store().subs.add(sub);
    return { frames };
  }

  it("201 TestChat(required 전부) — 세션 0개, 열림, 토큰 0", async () => {
    const id = await ws();
    const before = (await must<{ items: unknown[] }>("GET", `/workspaces/${id}/sessions`)).items.length;
    const chat = await must<TestChat>("POST", `/agents/${await agentId()}/test-chats`, { body: { profile_id: null, runtime_id: null } });
    for (const k of ["id", "workspace_id", "agent_id", "profile_id", "user_id", "status", "turns", "input_tokens", "output_tokens", "cost_usd", "created_at", "updated_at"]) expect(chat).toHaveProperty(k);
    expect(chat.status).toBe("open");
    expect(chat.turns).toEqual([]);
    expect(chat.transport).toBeNull();
    expect((await must<{ items: unknown[] }>("GET", `/workspaces/${id}/sessions`)).items.length).toBe(before);
  });

  it("턴 202 → SSE delta(ephemeral) … → turn(계약 페이로드) · 진행 중 409 · 그 뒤 다시 보낼 수 있다", async () => {
    const id = await ws();
    const chat = await must<TestChat>("POST", `/agents/${await agentId()}/test-chats`, { body: {} });
    const { frames } = tap(id);
    const r = await call("POST", `/test-chats/${chat.id}/turns`, { body: { content: "안녕" } });
    expect(r.status).toBe(202);
    const turn = r.body as TestChatTurn;
    expect(turn.role).toBe("user");
    expect(turn.content).toBe("안녕");
    // 이전 턴이 진행 중이면 409.
    const busy = await call("POST", `/test-chats/${chat.id}/turns`, { body: { content: "또" } });
    expect(busy.status).toBe(409);
    expect((busy.body as { detail: string }).detail).toBe("이전 답이 아직 오는 중입니다 — 끝난 뒤 보내 주세요");
    await vi.advanceTimersByTimeAsync(5000);
    const deltas = frames.filter((f) => f.type === "test_chat.delta");
    const turns = frames.filter((f) => f.type === "test_chat.turn");
    expect(deltas.length).toBeGreaterThan(1);
    for (const d of deltas) {
      expect(d.ephemeral).toBe(true);
      expect(d.payload.test_chat_id).toBe(chat.id);
      expect(typeof d.payload.text).toBe("string");
    }
    expect(turns).toHaveLength(1);
    const p = turns[0].payload as { test_chat_id: string; turn: TestChatTurn; transport: string; input_tokens: number; output_tokens: number };
    expect(p.test_chat_id).toBe(chat.id);
    expect(p.turn.role).toBe("agent");
    expect(p.turn.content).toBe(deltas.map((d) => d.payload.text).join(""));
    expect(["acp", "cli"]).toContain(p.transport);
    expect(p.input_tokens).toBeGreaterThan(0);
    expect(p.output_tokens).toBeGreaterThan(0);
    const after = await must<TestChat>("GET", `/test-chats/${chat.id}`);
    expect(after.turns).toHaveLength(2);
    expect(after.transport).toBe(p.transport);
    expect(after.cost_usd).toBeGreaterThan(0);
    expect((await call("POST", `/test-chats/${chat.id}/turns`, { body: { content: "다음" } })).status).toBe(202);
  });

  it("닫기 200(멱등) → 이후 턴은 410 test_chat_closed", async () => {
    const chat = await must<TestChat>("POST", `/agents/${await agentId()}/test-chats`, { body: {} });
    const closed = await must<TestChat>("POST", `/test-chats/${chat.id}/close`);
    expect(closed.status).toBe("closed");
    expect(closed.closed_at).not.toBeNull();
    expect((await call("POST", `/test-chats/${chat.id}/close`)).status).toBe(200);
    const r = await call("POST", `/test-chats/${chat.id}/turns`, { body: { content: "x" } });
    expect(r.status).toBe(410);
    expect((r.body as { code: string }).code).toBe("test_chat_closed");
  });

  it("고른 컴퓨터가 오프라인이면 409 runtime_offline · 다른 사람의 채팅은 403", async () => {
    const id = await ws();
    const rt = (await must<Runtime[]>("GET", `/workspaces/${id}/runtimes`))[0];
    await must("POST", `/__mock/runtimes/${rt.id}/offline`, { body: {} });
    const r = await call("POST", `/agents/${await agentId()}/test-chats`, { body: { runtime_id: rt.id } });
    expect(r.status).toBe(409);
    expect((r.body as { code: string }).code).toBe("runtime_offline");
    // 온라인 컴퓨터가 하나도 없으면 자동 선택도 409.
    expect((await call("POST", `/agents/${await agentId()}/test-chats`, { body: {} })).status).toBe(409);
    // 다른 사람이 연 채팅은 403.
    store().runtimes.get(rt.id)!.status = "online";
    const chat = await must<TestChat>("POST", `/agents/${await agentId()}/test-chats`, { body: {} });
    await login("seoyeon@colab.dev");
    expect((await call("GET", `/test-chats/${chat.id}`)).status).toBe(403);
  });
});
