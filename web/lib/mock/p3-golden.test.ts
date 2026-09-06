/**
 * **목이 통과시키는 값을 골든 표·계약과 기계 대조한다**(P2_TASKS §0-9 (b)).
 *
 * §0-9 는 두 구멍을 지목했다 — (a) 웹이 서버로 **보내는 문자열**, (b) 목이 **통과시키는 값**. 생성 타입은
 * *모양*만 지키므로 둘 다 타입으로는 잡히지 않는다. #67 에서 둘 다 놓쳐 실서버에서 `@all` 이 assignee 를
 * 깨웠다(E1-05 위반). 이 파일이 (b) 의 자물쇠다.
 *
 * 기준 원문:
 *   · `server/internal/hitl/hitl_golden_test.go` — `dueIn = 24h`, E7-08·09·10·11
 *   · `server/internal/sessions/budget_golden_test.go` — E9-02·E9-03
 *   · `server/internal/tasks/cancel_golden_test.go` — E10-05·E10-06
 *   · `server/internal/inbox/inbox.go`(feat/server-p3) — Severity · Actions · SortRank
 *   · `contracts/openapi.yaml` — respondHitlRequest(`Idempotency-Key` 필수, `ignored`), Problem.can_respond_from
 *
 * **W-5 도 여기 있다**: lane 해소 규칙을 지키던 것이 `e2e/p2-mock.sh` 뿐이었고 그 스모크는 CI 밖(목 서버 필요)이다.
 * `done` lane 재진입이 `resolution 3 · reentry true` 인지를 vitest 로 못박는다 — W-2·W-3′ 부류가 다시 슬며시
 * 바뀌어도 CI 가 모르는 상태를 끝낸다.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, DEPUTY_HALF_MS, HITL_DUE_IN_MS, inboxActions, inboxSeverity, inboxSortRank, type Req } from "./handlers";
import { resetStore, store } from "./store";
import type { HitlRequest, InboxItem, Lane, Message, Session, TriggerPreview } from "@/lib/api/types";

// ── 목 API 를 HTTP 처럼 부르는 얇은 클라이언트 ──────────────────────────────
let cookie = "";
async function call(method: string, path: string, opts: { body?: unknown; headers?: Record<string, string> } = {}) {
  const [p, qs] = path.split("?");
  const req: Req = {
    method,
    path: p,
    query: new URLSearchParams(qs ?? ""),
    headers: new Headers(opts.headers ?? {}),
    body: opts.body,
    cookies: cookie ? { colab_session: cookie } : {},
  };
  const res = await dispatch(req);
  return res;
}
async function must<T>(method: string, path: string, opts: { body?: unknown; headers?: Record<string, string> } = {}): Promise<T> {
  const res = await call(method, path, opts);
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(res.body)}`);
  return res.body as T;
}
const idem = () => ({ "idempotency-key": `k-${Math.random().toString(16).slice(2)}` });

async function login() {
  const res = await call("POST", "/auth/login", { body: { email: "demo@colab.dev", password: "password123" } });
  const set = (res.headers ?? {})["Set-Cookie"] ?? "";
  cookie = /colab_session=([^;]+)/.exec(set)?.[1] ?? "";
  expect(cookie).not.toBe("");
}

/** 세션 하나 + 참여자 둘. 나머지 테스트가 여기서 시작한다. */
async function newSession(): Promise<{ ws: string; session: Session }> {
  const me = await must<{ workspaces: { id: string }[] }>("GET", "/me");
  const ws = me.workspaces[0].id;
  const ags = await must<{ items: { id: string }[] }>("GET", `/workspaces/${ws}/agents`);
  const session = await must<Session>("POST", `/workspaces/${ws}/sessions`, {
    body: {
      title: "골든 대조", goal: "목이 통과시키는 값을 계약과 맞춘다", isolation: { kind: "none" },
      participants: [{ agent_id: ags.items[0].id }, { agent_id: ags.items[1].id }],
      assignee_agent_id: ags.items[0].id,
    },
  });
  return { ws, session };
}

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

// ═══════════════════════════════════════════════════════════════════════════
describe("HITL — golden E7 의 수치", () => {
  it("기한 기본값이 24h 이고 deputy 위임은 정확히 그 절반이다 (golden dueIn · CanRespondFrom = dueIn/2)", () => {
    expect(HITL_DUE_IN_MS).toBe(24 * 3600_000);
    expect(DEPUTY_HALF_MS).toBe(HITL_DUE_IN_MS / 2);
    expect(DEPUTY_HALF_MS).toBe(12 * 3600_000);
  });

  it("발행 시 due_at 이 created_at + 24h 다", async () => {
    const { session } = await newSession();
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: {} });
    expect(Date.parse(h.due_at) - Date.parse(h.created_at)).toBe(HITL_DUE_IN_MS);
    // question 은 제안 기본값이 필수다(FR-5.1, E7-05) — 목이 그것 없이 카드를 만들면 화면이 빈 제안을 그린다.
    expect(h.proposed_default).toBeTruthy();
    expect(h.status).toBe("open");
  });

  it("E7-09 — deputy 는 11h 에 거부되고 Problem.can_respond_from 이 절반 시각이다", async () => {
    const { session } = await newSession();
    await must("POST", `/__mock/sessions/${session.id}/role`, { body: { role: "deputy" } });
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: { age_ms: 11 * 3600_000 } });

    const view = await must<{ items: HitlRequest[] }>("GET", `/sessions/${session.id}/hitl-requests`);
    const card = view.items.find((x) => x.id === h.id)!;
    expect(card.can_respond).toBe(false);
    expect(Date.parse(card.can_respond_from!) - Date.parse(card.created_at)).toBe(DEPUTY_HALF_MS);

    const res = await call("POST", `/hitl-requests/${h.id}/response`, { body: { answer: "경영진" }, headers: idem() });
    expect(res.status).toBe(403);
    const problem = res.body as { can_respond_from?: string | null };
    expect(Date.parse(problem.can_respond_from!) - Date.parse(card.created_at)).toBe(DEPUTY_HALF_MS);
  });

  it("E7-10 — deputy 는 12h 1분에 수락되고 task 는 queued 로 재큐잉된다", async () => {
    const { session } = await newSession();
    await must("POST", `/__mock/sessions/${session.id}/role`, { body: { role: "deputy" } });
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, {
      body: { age_ms: 12 * 3600_000 + 60_000 },
    });
    const r = await must<{ hitl_request: HitlRequest; ignored: boolean; decision_id: string | null }>(
      "POST", `/hitl-requests/${h.id}/response`, { body: { answer: "경영진" }, headers: idem() },
    );
    expect(r.ignored).toBe(false);
    expect(r.hitl_request.status).toBe("answered");
    expect(r.hitl_request.answer).toBe("경영진");
    expect(r.decision_id).toBeTruthy(); // 결정 기록 정확히 1건(E7-07)
  });

  it("E7-11 — 일반 멤버는 거부되고 can_respond_from 은 null 이다(생기지 않을 권리에 시각을 약속하지 않는다)", async () => {
    const { session } = await newSession();
    await must("POST", `/__mock/sessions/${session.id}/role`, { body: { role: "member" } });
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: { age_ms: 20 * 3600_000 } });

    const view = await must<{ items: HitlRequest[] }>("GET", `/sessions/${session.id}/hitl-requests`);
    const card = view.items.find((x) => x.id === h.id)!;
    // 카드는 보이되(목록에 있다) 응답 권한은 영영 없다.
    expect(card.can_respond).toBe(false);
    expect(card.can_respond_from).toBeNull();

    const res = await call("POST", `/hitl-requests/${h.id}/response`, { body: { answer: "x" }, headers: idem() });
    expect(res.status).toBe(403);
    expect((res.body as { can_respond_from?: string | null }).can_respond_from).toBeNull();
  });

  it("E7-08 — 두 번째 응답은 오류가 아니라 ignored: true 이고 첫 답이 유지된다", async () => {
    const { session } = await newSession();
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: {} });
    await must("POST", `/hitl-requests/${h.id}/response`, { body: { answer: "경영진" }, headers: idem() });
    const second = await must<{ hitl_request: HitlRequest; ignored: boolean }>(
      "POST", `/hitl-requests/${h.id}/response`, { body: { answer: "실무자" }, headers: idem() },
    );
    expect(second.ignored).toBe(true);
    expect(second.hitl_request.answer).toBe("경영진");
  });

  it("E7-15 — overdue 여도 답할 수 있다(expired 는 상태가 아니다)", async () => {
    const { session } = await newSession();
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, {
      body: { type: "approval", proposed_default: null, age_ms: 30 * 3600_000 },
    });
    const view = await must<{ items: HitlRequest[] }>("GET", `/sessions/${session.id}/hitl-requests`);
    expect(view.items.find((x) => x.id === h.id)!.overdue).toBe(true);
    const r = await must<{ hitl_request: HitlRequest }>("POST", `/hitl-requests/${h.id}/response`, {
      body: { approved: true }, headers: idem(),
    });
    expect(r.hitl_request.status).toBe("answered");
  });

  it("계약 필수 헤더 — Idempotency-Key 없이 응답하면 422 다", async () => {
    const { session } = await newSession();
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: {} });
    const res = await call("POST", `/hitl-requests/${h.id}/response`, { body: { answer: "x" } });
    expect(res.status).toBe(422);
  });

  it("O5 — deputy 의 인박스에는 절반 전 항목이 아예 오지 않는다(U9-1 '카드 없음')", async () => {
    const { ws, session } = await newSession();
    await must("POST", `/__mock/sessions/${session.id}/role`, { body: { role: "deputy" } });
    await must("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: { age_ms: 11 * 3600_000 } });
    const page = await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`);
    expect(page.items.filter((x) => x.type === "hitl_request")).toHaveLength(0);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("예산 — golden E9 의 수치", () => {
  it("E9-02 — 승인은 task 범위($3)이고 에이전트 budget_per_task 는 건드리지 않는다", async () => {
    const { ws, session } = await newSession();
    // 에이전트에 상한 $1 을 둔다 — golden 의 AgentBudgetPerTask 기준값.
    const ags = await must<{ items: { id: string }[] }>("GET", `/workspaces/${ws}/agents`);
    const agentId = ags.items[0].id;
    store().agents.get(agentId)!.budget_per_task = 1;

    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, {
      body: { source: "system", purpose: "budget", type: "approval", proposed_default: null, agent_id: agentId, question: "예산 $1 을 초과했습니다" },
    });
    // E9-01 — 예산 HITL 은 system 발행이어도 **task_id 를 채운다**(계약 s-13).
    expect(h.source).toBe("system");
    expect(h.purpose).toBe("budget");
    expect(h.task_id).not.toBeNull();

    const r = await must<{ hitl_request: HitlRequest; task?: { budget_override: number | null; status: string } }>(
      "POST", `/hitl-requests/${h.id}/response`, { body: { approved: true, budget_override_usd: 3 }, headers: idem() },
    );
    expect(r.task?.budget_override).toBe(3);
    expect(r.task?.status).toBe("queued"); // 승인이 곧 트리거다 — 새 멘션이 필요 없다
    expect(store().agents.get(agentId)!.budget_per_task).toBe(1); // C2′ — 한 번의 클릭이 미래 세션을 재가격하지 않는다
  });

  /**
   * W-6 의 모양을 목에 못 박는다 — 화면이 무엇을 보고 상향 입력을 낼지 정하는 근거다.
   *
   * task 범위 예산 초과(E9-01·E9-10)는 **lane 만** 멈춘다. 세션은 `active` 이므로 인박스 항목 타입은
   * `session_paused` 가 아니라 `hitl_request` 고, 항목의 `card` 에는 `purpose` 가 없다(계약 InboxItem).
   * 따라서 화면은 `ref_id` 로 HITL 상세를 읽어 `purpose` 를 알아내야 한다 — 그 왕복이 성립하는지까지 본다.
   */
  it("W-6 — task 범위 예산 HITL 은 세션을 멈추지 않고, 인박스 항목의 ref_id 로만 purpose 를 알 수 있다", async () => {
    const { ws, session } = await newSession();
    const ags = await must<{ items: { id: string }[] }>("GET", `/workspaces/${ws}/agents`);
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, {
      body: { source: "system", purpose: "budget", type: "approval", proposed_default: null, agent_id: ags.items[0].id },
    });
    expect(h.task_id).not.toBeNull(); // task 범위다(s-13)

    // 세션은 멈추지 않는다 — 멈추는 것은 lane 뿐이다.
    const after = await must<Session>("GET", `/sessions/${session.id}`);
    expect(after.status).toBe("active");
    expect(after.paused_reason ?? null).toBeNull();
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    expect(lanes.find((l) => l.id === h.lane_id)!.status).toBe("paused");

    const inbox = await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`);
    const it = inbox.items.find((x) => x.ref_id === h.id)!;
    expect(it.type).toBe("hitl_request"); // session_paused 가 아니다 — 여기서 W-6 이 났다
    expect(it.card?.hitl_type).toBe("approval");
    // 항목만으로는 예산인지 알 수 없다. 이 단정이 깨지면(계약에 purpose 가 생기면) S8 의 상세 왕복을 지워도 된다.
    expect((it.card as Record<string, unknown>).purpose).toBeUndefined();
    expect(it.card?.paused_reason ?? null).toBeNull();
    // 상세는 준다 — 그것이 화면이 쓰는 경로다.
    expect((await must<HitlRequest>("GET", `/hitl-requests/${it.ref_id}`)).purpose).toBe("budget");

    // 승인 페이로드는 그대로 E9-02 다.
    const r = await must<{ task?: { budget_override: number | null } }>("POST", `/hitl-requests/${h.id}/response`, {
      body: { approved: true, budget_override_usd: 3 }, headers: idem(),
    });
    expect(r.task?.budget_override).toBe(3);
  });

  /**
   * S-45(T-S6 가 고치는 중) 대비 — **시스템 발행 HITL 도 타임라인 `hitl` 메시지를 만든다.**
   * 서버가 그것을 안 만들면 S7 에 카드가 아예 뜨지 않아 인박스 밖에서는 답할 자리가 없다.
   * 목이 그 모양을 먼저 지키고 있어야 서버가 고쳐졌을 때 화면이 그대로 붙는다.
   */
  it("시스템 발행(purpose=budget) HITL 도 kind=hitl 타임라인 메시지를 만든다 (S-45 대비)", async () => {
    const { ws, session } = await newSession();
    const ags = await must<{ items: { id: string }[] }>("GET", `/workspaces/${ws}/agents`);
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, {
      body: { source: "system", purpose: "budget", type: "approval", proposed_default: null, agent_id: ags.items[0].id },
    });
    expect(h.message_id).toBeTruthy();
    const msgs = await must<{ items: Message[] }>("GET", `/sessions/${session.id}/messages`);
    const card = msgs.items.find((m) => m.id === h.message_id)!;
    expect(card.kind).toBe("hitl");
    expect(card.author_type).toBe("system");
  });

  it("E9-03 — 거절은 failed 도 cancelled 도 아니고 paused(budget) 로 남는다", async () => {
    const { ws, session } = await newSession();
    const ags = await must<{ items: { id: string }[] }>("GET", `/workspaces/${ws}/agents`);
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, {
      body: { source: "system", purpose: "budget", type: "approval", proposed_default: null, agent_id: ags.items[0].id },
    });
    await must("POST", `/hitl-requests/${h.id}/response`, { body: { approved: false, reason: "여기까지만" }, headers: idem() });
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    const lane = lanes.find((l) => l.id === h.lane_id)!;
    expect(lane.status).toBe("paused");
    expect(lane.status).not.toBe("failed");
    // 거절도 답이다 — 결정 기록은 남는다.
    expect((await must<HitlRequest>("GET", `/hitl-requests/${h.id}`)).status).toBe("answered");
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("취소 권한 — golden E10-05·E10-06", () => {
  it("E10-05 — 일반 멤버는 lane 중단이 403 이고 버튼 목록에서도 빠진다", async () => {
    const { session } = await newSession();
    await must("POST", `/__mock/sessions/${session.id}/seed-lanes`, {});
    await must("POST", `/__mock/sessions/${session.id}/role`, { body: { role: "member" } });
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    const running = lanes.find((l) => l.status === "running")!;
    expect(running.actions).not.toContain("cancel");
    expect(running.actions).not.toContain("restart");
    const res = await call("POST", `/lanes/${running.id}/cancel`, {});
    expect(res.status).toBe(403);
  });

  it("E10-06 — deputy 는 시점 제한 없이 즉시 중단할 수 있다(승인과 다르다)", async () => {
    const { session } = await newSession();
    await must("POST", `/__mock/sessions/${session.id}/seed-lanes`, {});
    await must("POST", `/__mock/sessions/${session.id}/role`, { body: { role: "deputy" } });
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    const running = lanes.find((l) => l.status === "running")!;
    expect(running.actions).toContain("cancel");
    const res = await call("POST", `/lanes/${running.id}/cancel`, {});
    expect(res.status).toBe(202);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("인박스 — 서버 inbox.Severity · Actions · SortRank 와 같은 표", () => {
  it("심각도 7종이 서버 분류와 같다", () => {
    expect(inboxSeverity("hitl_request")).toBe("action_required");
    expect(inboxSeverity("lane_blocked")).toBe("action_required");
    expect(inboxSeverity("session_paused")).toBe("action_required");
    expect(inboxSeverity("run_failed")).toBe("attention");
    expect(inboxSeverity("runtime_offline")).toBe("attention");
    expect(inboxSeverity("session_completed")).toBe("info");
    expect(inboxSeverity("mention")).toBe("info");
  });

  it("정렬 순위가 overdue → action_required → attention → info 다", () => {
    expect(inboxSortRank("info", true)).toBe(0); // overdue 가 심각도를 이긴다
    expect(inboxSortRank("action_required", false)).toBe(1);
    expect(inboxSortRank("attention", false)).toBe(2);
    expect(inboxSortRank("info", false)).toBe(3);
  });

  it("동작 목록이 권한을 반영한다 — 응답할 수 없으면 세션 열기뿐이다", () => {
    expect(inboxActions("hitl_request", "approval", true)).toEqual(["approve", "reject", "open_session"]);
    expect(inboxActions("hitl_request", "question", true)).toEqual(["answer", "open_session"]);
    expect(inboxActions("hitl_request", "question", false)).toEqual(["open_session"]);
    expect(inboxActions("session_paused", undefined, true)).toEqual(["approve_continue", "open_session"]);
    expect(inboxActions("run_failed", undefined, true)).toEqual(["restart", "open_session"]);
    expect(inboxActions("runtime_offline", undefined, true)).toEqual(["open_runtimes"]);
  });

  it("overdue 항목이 목록 맨 위로 온다(E7-13 InboxTop)", async () => {
    const { ws, session } = await newSession();
    await must("POST", `/__mock/inbox/seed`, {});
    await must("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: { age_ms: 30 * 3600_000 } });
    const page = await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`);
    expect(page.items[0].overdue).toBe(true);
    expect(page.items[0].type).toBe("hitl_request");
  });

  it("뱃지는 action_required 만 센다 — info 를 세면 뱃지가 무의미해진다", async () => {
    const { ws } = await newSession();
    await must("POST", `/__mock/inbox/seed`, {});
    const sum = await must<{ action_required: number; overdue: number; unread: number }>("GET", `/inbox/summary?workspace_id=${ws}`);
    const page = await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`);
    expect(sum.action_required).toBe(page.items.filter((x) => x.severity === "action_required").length);
    expect(sum.action_required).toBeLessThan(page.items.length); // info·attention 은 빠져 있다
  });

  it("전부 읽음은 action_required 를 건드리지 않는다 — 해소는 응답이 한다", async () => {
    const { ws } = await newSession();
    await must("POST", `/__mock/inbox/seed`, {});
    await must("POST", `/inbox/read-all?workspace_id=${ws}`, {});
    const page = await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`);
    for (const it of page.items) {
      if (it.severity === "action_required") expect(it.read_at).toBeNull();
      else expect(it.read_at).not.toBeNull();
    }
  });

  it("U3 — 인박스에서 답하면 그 항목이 목록에서 사라진다(처리됐다는 유일한 신호)", async () => {
    const { ws, session } = await newSession();
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: {} });
    const before = await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`);
    expect(before.items.some((x) => x.ref_id === h.id)).toBe(true);
    await must("POST", `/hitl-requests/${h.id}/response`, { body: { answer: "투자자" }, headers: idem() });
    const after = await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`);
    expect(after.items.some((x) => x.ref_id === h.id)).toBe(false);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
// W-5 — lane 해소 규칙을 CI 안에서 지킨다(그동안 e2e/p2-mock.sh 만 지켰다)
// ═══════════════════════════════════════════════════════════════════════════
describe("W-5 — 목의 lane 해소 규칙(PRD FR-3.3 lane 규칙 · EVAL E2)", () => {
  async function previewMention(sessionId: string, agentId: string, name: string, opts: { newLane?: boolean } = {}) {
    return must<TriggerPreview>("POST", `/sessions/${sessionId}/messages/preview`, {
      body: { content: `[@${name}](mention://agent/${agentId}) 이어서 해줘`, new_lane: opts.newLane ?? false },
    });
  }

  it("done lane 재진입은 **규칙 3 + reentry true** 다 — 4 는 '그 외 → 새 lane' 이다(W-3′)", async () => {
    const { session } = await newSession();
    // 이 에이전트에게 **done lane 하나뿐** 이어야 한다 — 진행 중 lane 이 있으면 그쪽 재사용이 먼저다.
    const target = (session.participants ?? [])[1].agent_id;
    await must("POST", `/__mock/sessions/${session.id}/seed-lanes`, { body: { statuses: ["done"], agent_id: target } });
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    const done = lanes.find((l) => l.status === "done" && l.agent_id === target)!;
    const agent = (session.participants ?? []).find((p) => p.agent_id === done.agent_id)!;

    const pv = await previewMention(session.id, done.agent_id, agent.agent.name);
    const t = pv.triggers.find((x) => x.agent_id === done.agent_id)!;
    expect(t.lane.resolution).toBe(3);
    expect(t.lane.reentry).toBe(true);
    expect(t.lane.lane_id).toBe(done.id);
  });

  it("running lane 재사용도 규칙 3 이고 reentry 는 false, will_queue 는 true 다", async () => {
    const { session } = await newSession();
    const target = (session.participants ?? [])[1].agent_id;
    await must("POST", `/__mock/sessions/${session.id}/seed-lanes`, { body: { statuses: ["running"], agent_id: target } });
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    const running = lanes.find((l) => l.status === "running" && l.agent_id === target)!;
    const agent = (session.participants ?? []).find((p) => p.agent_id === running.agent_id)!;

    const pv = await previewMention(session.id, running.agent_id, agent.agent.name);
    const t = pv.triggers.find((x) => x.agent_id === running.agent_id)!;
    expect(t.lane.resolution).toBe(3);
    expect(t.lane.reentry).toBe(false);
    expect(t.will_queue).toBe(true);
  });

  it("'새 lane 으로 보내기' 는 해소를 건너뛰고 규칙 4(새 lane)로 간다(E2-07, t-2)", async () => {
    const { session } = await newSession();
    const target = (session.participants ?? [])[1].agent_id;
    await must("POST", `/__mock/sessions/${session.id}/seed-lanes`, { body: { statuses: ["done"], agent_id: target } });
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    const done = lanes.find((l) => l.status === "done" && l.agent_id === target)!;
    const agent = (session.participants ?? []).find((p) => p.agent_id === done.agent_id)!;

    const pv = await previewMention(session.id, done.agent_id, agent.agent.name, { newLane: true });
    const t = pv.triggers.find((x) => x.agent_id === done.agent_id)!;
    expect(t.lane.resolution).toBe(4);
    expect(t.lane.lane_id).toBeNull();
    expect(t.lane.reentry).toBe(false);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
// W-4 — install_commands 의 호스트(2026-09-07 확정)
// ═══════════════════════════════════════════════════════════════════════════
describe("W-4 — install_commands 는 API 오리진이다", () => {
  it("웹 오리진(:3000 프록시)이 아니라 서버 오리진을 쓴다 — 서버 S-5 와 같은 규칙", async () => {
    const me = await must<{ workspaces: { id: string }[] }>("GET", "/me");
    const pr = await must<{ install_commands: string[] }>(
      "POST", `/workspaces/${me.workspaces[0].id}/runtimes/pairings`,
      { body: {}, headers: { origin: "http://localhost:3113" } },
    );
    expect(pr.install_commands).toHaveLength(2);
    for (const cmd of pr.install_commands) {
      expect(cmd).toContain("localhost:8080");
      expect(cmd).not.toContain("3113");
    }
  });
});

// ═══════════════════════════════════════════════════════════════════════════
// lane 카드 펼침(O3) — HITL 재개는 **같은 task 의 새 attempt** 다(E7-07·E8-07)
// ═══════════════════════════════════════════════════════════════════════════
describe("task 이력 — 재개는 새 attempt, 재지시는 새 task", () => {
  it("HITL 응답 뒤 같은 task 의 attempt 가 2 가 되고 이력 행이 늘어난다(E7-07)", async () => {
    const { session } = await newSession();
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${session.id}/seed-hitl`, { body: {} });
    const r = await must<{ task?: { id: string; attempt: number } }>("POST", `/hitl-requests/${h.id}/response`, {
      body: { answer: "경영진" }, headers: idem(),
    });
    expect(r.task?.attempt).toBe(2);

    const tasks = await must<{ id: string; attempt: number; attempts: { attempt: number }[]; restarted_from_task_id: string | null }[]>(
      "GET", `/lanes/${h.lane_id}/tasks`,
    );
    const t = tasks.find((x) => x.id === r.task!.id)!;
    // **같은 task** 다 — 재지시가 아니므로 restarted_from_task_id 는 비어 있다(E8-06 과 갈리는 지점).
    expect(t.restarted_from_task_id).toBeNull();
    // 이력의 마지막 행이 현재 attempt 다 — lane 카드 펼침이 "#1-2 (재시도)" 를 그리는 근거(O3).
    expect(t.attempts.at(-1)!.attempt).toBe(t.attempt);
    expect(t.attempt).toBe(2);
  });

  it("재지시는 새 task 이고 restarted_from_task_id 가 이전 task 를 가리킨다(E8-06)", async () => {
    const { session } = await newSession();
    const target = (session.participants ?? [])[1].agent_id;
    await must("POST", `/__mock/sessions/${session.id}/seed-lanes`, { body: { statuses: ["running"], agent_id: target } });
    const lanes = await must<Lane[]>("GET", `/sessions/${session.id}/lanes`);
    const lane = lanes.find((l) => l.status === "running" && l.agent_id === target)!;
    const r = await must<{ task: { id: string; attempt: number; restarted_from_task_id: string | null }; message: { content: string } }>(
      "POST", `/lanes/${lane.id}/restart`, { body: { content: "범위를 국내로 좁혀줘" }, headers: idem() },
    );
    expect(r.task.attempt).toBe(1); // 새 task 이므로 attempt 는 1 부터다
    // 서버가 lane 에이전트 멘션을 앞에 붙인다(계약 restartLane) — 웹이 채운 멘션이 중복되지 않는다.
    expect(r.message.content).toContain("mention://agent/");
  });
});
