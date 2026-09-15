/**
 * **목이 통과시키는 P4 값을 골든 표·계약과 기계 대조한다**(P2_TASKS §0-9 (b)).
 *
 * `p3-golden.test.ts` 와 같은 자물쇠다: 생성 타입은 *모양*만 지키므로 **수치와 판정**은 타입으로 잡히지
 * 않는다. 여기서 잰 값이 어긋나면 화면은 목 위에서만 맞고 실서버에서 틀린다.
 *
 * 기준 원문:
 *   · `server/internal/runtimes/offline_golden_test.go` — E14-01·02·03·04·05·06·07·08·09·10
 *   · `server/internal/workdirs/gc_golden_test.go`      — E13-01·09~16·18·19
 *   · `contracts/openapi.yaml` — `checkRepo`(200 + `ok:false`) · `listRuntimeWorkdirs` ·
 *     `deleteWorkdir`(`409 workdir_dirty`, 202, 브랜치 보존) · `deleteRuntime`(`409
 *     runtime_has_active_sessions` + `Problem.sessions[]`) · `rebindSession`(409/422) ·
 *     `listRuntimeCandidates`(remote URL 판정) · `listArtifacts`("제출 순") · `InboxItem.card.purpose`(K-9)
 *   · `EVAL.md` E13·E14 표, `EVAL_USER.md` U6·U12
 *
 * 서버 골든의 훅(`sweepOffline`·`judgeGC`·`rebindSession`…)은 Go 쪽 판정이고, 웹은 **그 결과를 그리는 쪽**이다.
 * 그래서 여기서는 (a) 목이 그 판정과 **같은 값을 통과시키는지**와 (b) 화면 계산(`lib/workdir.ts`·
 * `graceView`·`budgetScopeOf`)이 그 값을 **사람의 다음 행동으로 옳게 번역하는지** 를 잰다.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, RUNTIME_OFFLINE_GRACE_MS, WORKDIR_RETENTION_DAYS, type Req } from "./handlers";
import { resetStore, store } from "./store";
import { budgetScopeOf } from "@/components/InboxItemCard";
import { graceView } from "@/components/RuntimeCard";
import { deleteBlocked, gcBlockText, quotaView, GB } from "@/lib/workdir";
import type { Artifact, HitlRequest, InboxItem, Runtime, RuntimeCandidate, Session, Workdir } from "@/lib/api/types";

const DAY = 864e5;

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
  return dispatch(req);
}
async function must<T>(method: string, path: string, opts: { body?: unknown; headers?: Record<string, string> } = {}): Promise<T> {
  const res = await call(method, path, opts);
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(res.body)}`);
  return res.body as T;
}

async function login() {
  const res = await call("POST", "/auth/login", { body: { email: "demo@colab.dev", password: "password123" } });
  cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
  expect(cookie).not.toBe("");
}

/** 워크스페이스 · 시드 런타임(remote `git@github.com:ingki3/agent-collabortion.git`) · 에이전트 둘. */
async function base() {
  const me = await must<{ workspaces: { id: string }[] }>("GET", "/me");
  const ws = me.workspaces[0].id;
  const rts = await must<Runtime[]>("GET", `/workspaces/${ws}/runtimes`);
  const ags = await must<{ items: { id: string }[] }>("GET", `/workspaces/${ws}/agents`);
  return { ws, rt: rts[0], agents: ags.items.map((a) => a.id) };
}

/** 두 번째 런타임을 store 에 직접 넣는다 — 목 API 에는 런타임 생성 op 이 없다(페어링이 만든다). */
function addRuntime(ws: string, name: string, repos: Runtime["repos"], status: Runtime["status"] = "online"): Runtime {
  const s = store();
  const seed = [...s.runtimes.values()][0];
  const rt: Runtime = { ...seed, id: crypto.randomUUID(), workspace_id: ws, name, repos, status, offline_since: null, grace_ends_at: null, paused_session_count: 0 };
  s.runtimes.set(rt.id, rt);
  return rt;
}

const REMOTE = "git@github.com:ingki3/agent-collabortion.git";

async function newSession(opts: { isolation?: "none" | "worktree"; runtimeId?: string; ws: string; agents: string[] }): Promise<Session> {
  return must<Session>("POST", `/workspaces/${opts.ws}/sessions`, {
    body: {
      title: "P4 골든 대조", goal: "worktree 격리와 재바인딩",
      isolation: opts.isolation === "worktree" ? { kind: "worktree", repo_path: "~/dev/colab", remote_url: REMOTE } : { kind: "none" },
      runtime_id: opts.runtimeId ?? null,
      participants: opts.agents.map((id) => ({ agent_id: id })),
      assignee_agent_id: opts.agents[0],
    },
  });
}

beforeEach(async () => {
  resetStore();
  cookie = "";
  await login();
});

// ═══════════════════════════════════════════════════════════════════════════
describe("오프라인 유예 — golden E14 의 수치", () => {
  it("유예 기본값은 7일이다 (golden `Grace: 7 * p4Day`)", () => {
    expect(RUNTIME_OFFLINE_GRACE_MS).toBe(7 * DAY);
  });

  it("보존 기한 기본값은 14일이다 (golden `RetentionDays: 14`)", () => {
    expect(WORKDIR_RETENTION_DAYS).toBe(14);
  });

  it("E14-01 — 유예 직전(6일 23시간)은 세션 active · 알림 0", async () => {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    const before = (await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`)).items.length;
    await must<Runtime>("POST", `/__mock/runtimes/${rt.id}/offline`, { body: { days: 6.958333 } }); // 6일 23시간

    const after = await must<Session>("GET", `/sessions/${sess.id}`);
    expect(after.status).toBe("active"); // 주말 동안 닫아 둔 노트북은 정상이다 — 일찍 멈추면 알림이 의미를 잃는다
    const items = (await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`)).items;
    expect(items.filter((x) => x.type === "runtime_offline").length).toBe(0);
    expect(items.length).toBe(before);
  });

  it("E14-02 — 유예 도달은 paused(runtime_offline) + Dir 알림 + 선택지 정확히 2(재바인딩·종료)", async () => {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    await must<Runtime>("POST", `/__mock/runtimes/${rt.id}/offline`, { body: { days: 7 } });

    const after = await must<Session>("GET", `/sessions/${sess.id}`);
    expect(after.status).toBe("paused");
    expect(after.paused_reason).toBe("runtime_offline");
    // FR-9.2 는 선택지를 정확히 둘로 못 박는다 — 셋이 되면 화면이 없는 길을 제안한다.
    expect(after.paused_detail?.resolve_actions).toEqual(["rebind", "cancel"]);

    const items = (await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`)).items;
    expect(items.filter((x) => x.type === "runtime_offline").length).toBe(1);

    // `grace_ends_at` 이 있어야 S11 이 "언제까지"를 말한다(openapi Runtime.grace_ends_at).
    const rts = await must<Runtime[]>("GET", `/workspaces/${ws}/runtimes`);
    const off = rts.find((r) => r.id === rt.id)!;
    expect(off.grace_ends_at).toBeTruthy();
    expect(off.paused_session_count).toBe(1);
  });

  it("E14-10 — 이미 paused 인 세션의 다음 스윕은 알림을 다시 만들지 않는다(멱등)", async () => {
    const { ws, rt, agents } = await base();
    await newSession({ ws, agents, runtimeId: rt.id });
    await must<Runtime>("POST", `/__mock/runtimes/${rt.id}/offline`, { body: { days: 7 } });
    const once = (await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`)).items.filter((x) => x.type === "runtime_offline").length;
    await must<Runtime>("POST", `/__mock/runtimes/${rt.id}/offline`, { body: { days: 9 } });
    const twice = (await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`)).items.filter((x) => x.type === "runtime_offline").length;
    expect(twice).toBe(once); // 스윕은 주기적이다 — tick 마다 쌓으면 답해야 할 한 건이 묻힌다
    expect(once).toBe(1);
  });

  it("화면 문장 — 유예 안에서는 '남음', 넘기면 '만료 + 묶인 세션 수'(U12 1·2)", () => {
    const now = Date.parse("2026-09-08T00:00:00Z");
    const left = graceView(
      { offline_since: "2026-09-07T00:00:00Z", grace_ends_at: "2026-09-14T00:00:00Z", paused_session_count: 0 },
      now,
    );
    expect(left.expired).toBe(false);
    expect(left.daysLeft).toBe(6); // U12 1단계 "유예 6일 남음"
    expect(left.text).toContain("유예 6일 남음");

    const over = graceView(
      { offline_since: "2026-08-30T00:00:00Z", grace_ends_at: "2026-09-06T00:00:00Z", paused_session_count: 2 },
      now,
    );
    expect(over.expired).toBe(true);
    expect(over.text).toContain("세션 2개");
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("재바인딩 후보 — remote URL 판정(golden E14-03·04·05)", () => {
  it("E14-04 — 경로가 달라도 remote URL 이 같으면 후보다", async () => {
    const { ws, rt, agents } = await base();
    const b = addRuntime(ws, "desktop", [{ path: "/srv/checkouts/app", remote_url: REMOTE, branch: "main", clean: true }]);
    const sess = await newSession({ ws, agents, runtimeId: rt.id, isolation: "worktree" });
    const r = await must<{ auto_select_allowed: boolean; candidates: RuntimeCandidate[] }>(
      "GET", `/workspaces/${ws}/runtime-candidates?isolation=worktree&session_id=${sess.id}`,
    );
    const cand = r.candidates.find((c) => c.runtime.id === b.id)!;
    expect(cand.eligible).toBe(true);
    expect(cand.matched_repo?.path).toBe("/srv/checkouts/app"); // 경로는 달라도 된다
    expect(r.auto_select_allowed).toBe(false); // worktree 는 자동 선택 불가(E13-17 · U6 2단계)
  });

  it("E14-05 — 경로 문자열이 같아도 remote 가 다르면 후보가 아니다 + 사유가 붙는다", async () => {
    const { ws, rt, agents } = await base();
    const b = addRuntime(ws, "other", [{ path: "~/dev/colab", remote_url: "git@github.com:someone/else.git", branch: "main", clean: true }]);
    const sess = await newSession({ ws, agents, runtimeId: rt.id, isolation: "worktree" });
    const r = await must<{ candidates: RuntimeCandidate[] }>("GET", `/workspaces/${ws}/runtime-candidates?isolation=worktree&session_id=${sess.id}`);
    const cand = r.candidates.find((c) => c.runtime.id === b.id)!;
    expect(cand.eligible).toBe(false);
    expect(cand.reason).toBeTruthy(); // 비활성은 사유와 함께다 — 사라진 선택지는 이유를 말하지 못한다
  });

  it("E14-03 — none 은 온라인 런타임 전부가 후보이고 자동 선택이 허용된다", async () => {
    const { ws, rt, agents } = await base();
    const b = addRuntime(ws, "desktop", []);
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    const r = await must<{ auto_select_allowed: boolean; candidates: RuntimeCandidate[] }>("GET", `/workspaces/${ws}/runtime-candidates?isolation=none&session_id=${sess.id}`);
    expect(r.auto_select_allowed).toBe(true);
    expect(r.candidates.find((c) => c.runtime.id === b.id)!.eligible).toBe(true);
  });

  it("오프라인 런타임은 어떤 격리에서도 후보가 아니다", async () => {
    const { ws, rt, agents } = await base();
    const b = addRuntime(ws, "sleeping", [{ path: "/x", remote_url: REMOTE, branch: "main", clean: true }], "offline");
    const sess = await newSession({ ws, agents, runtimeId: rt.id, isolation: "worktree" });
    const r = await must<{ candidates: RuntimeCandidate[] }>("GET", `/workspaces/${ws}/runtime-candidates?isolation=worktree&session_id=${sess.id}`);
    expect(r.candidates.find((c) => c.runtime.id === b.id)!.eligible).toBe(false);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("재바인딩 실행 — golden E14-03·05·06", () => {
  async function pausedWorktreeSession() {
    const { ws, rt, agents } = await base();
    const b = addRuntime(ws, "desktop", [{ path: "/srv/app", remote_url: REMOTE, branch: "main", clean: true }]);
    const sess = await newSession({ ws, agents, runtimeId: rt.id, isolation: "worktree" });
    await must<Artifact[]>("POST", `/__mock/sessions/${sess.id}/seed-artifacts`, { body: { count: 3, type: "diff" } });
    await must<Runtime>("POST", `/__mock/runtimes/${rt.id}/offline`, { body: { days: 8 } });
    return { ws, rt, b, sess };
  }

  it("E14-06 — worktree 는 acknowledge_loss 없이 422 다", async () => {
    const { b, sess } = await pausedWorktreeSession();
    const res = await call("POST", `/sessions/${sess.id}/rebind`, { body: { runtime_id: b.id } });
    expect(res.status).toBe(422);
  });

  it("E14-05 — 후보가 아닌 런타임으로는 422 (화면을 거치지 않은 직접 호출도 막는다)", async () => {
    const { ws, sess } = await pausedWorktreeSession();
    const other = addRuntime(ws, "other", [{ path: "~/dev/colab", remote_url: "git@github.com:someone/else.git", branch: "main", clean: true }]);
    const res = await call("POST", `/sessions/${sess.id}/rebind`, { body: { runtime_id: other.id, acknowledge_loss: true } });
    expect(res.status).toBe(422);
  });

  it("E14-03 — paused(runtime_offline) 가 아닌 세션은 409 다", async () => {
    const { ws, rt, agents } = await base();
    const b = addRuntime(ws, "desktop", []);
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    const res = await call("POST", `/sessions/${sess.id}/rebind`, { body: { runtime_id: b.id } });
    expect(res.status).toBe(409); // 살아 있는 세션을 옮기면 아직 돌고 있는 머신에서 일을 빼앗는다
  });

  it("E14-03·06 — 재바인딩은 런타임을 바꾸고 active 로 되돌리며 대화·아티팩트를 그대로 둔다", async () => {
    const { b, sess } = await pausedWorktreeSession();
    const before = await must<{ items: unknown[] }>("GET", `/sessions/${sess.id}/messages`);
    const arts = await must<Artifact[]>("GET", `/sessions/${sess.id}/artifacts?type=diff`);

    const after = await must<Session>("POST", `/sessions/${sess.id}/rebind`, { body: { runtime_id: b.id, acknowledge_loss: true } });
    expect(after.runtime_id).toBe(b.id);
    expect(after.status).toBe("active"); // 재바인딩이 곧 재개다(openapi rebindSession)
    expect(after.paused_reason ?? null).toBeNull();

    // 아티팩트·메시지·결정 기록은 서버에 있다 — 재바인딩이 지울 이유가 없다(FR-9.2).
    const afterMsgs = await must<{ items: unknown[] }>("GET", `/sessions/${sess.id}/messages`);
    expect(afterMsgs.items.length).toBe(before.items.length);
    expect((await must<Artifact[]>("GET", `/sessions/${sess.id}/artifacts?type=diff`)).length).toBe(arts.length);
  });

  it("E14-06 — diff 아티팩트 목록은 제출 순서다(재적용 순서 = 이 순서)", async () => {
    const { sess } = await pausedWorktreeSession();
    const arts = await must<Artifact[]>("GET", `/sessions/${sess.id}/artifacts?type=diff`);
    expect(arts.map((a) => a.name)).toEqual(["diff-1.patch", "diff-2.patch", "diff-3.patch"]);
    // 시각도 오름차순이어야 한다 — 이름만 맞고 순서가 뒤집히면 화면이 뒤집힌 순서를 그린다.
    expect([...arts].sort((a, x) => a.created_at.localeCompare(x.created_at)).map((a) => a.id)).toEqual(arts.map((a) => a.id));
    // type 필터가 없으면 doc·file 이 섞여 재적용 목록이 거짓말을 한다.
    expect(arts.every((a) => a.type === "diff")).toBe(true);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("런타임 삭제 — golden E14-08", () => {
  it("활성 세션이 걸려 있으면 409 runtime_has_active_sessions + Problem.sessions[]", async () => {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    const res = await call("DELETE", `/runtimes/${rt.id}`);
    expect(res.status).toBe(409);
    const p = res.body as { code?: string; sessions?: { id: string }[] };
    expect(p.code).toBe("runtime_has_active_sessions");
    // "먼저 재바인딩/종료" 는 **어느 세션인지** 알아야 실행할 수 있는 지시다.
    expect(p.sessions?.map((x) => x.id)).toEqual([sess.id]);
    expect((await must<Runtime[]>("GET", `/workspaces/${ws}/runtimes`)).some((r) => r.id === rt.id)).toBe(true);
  });

  it("paused(runtime_offline) 세션도 삭제를 막는다 — 그것이 선택을 기다리는 세션이다", async () => {
    const { ws, rt, agents } = await base();
    await newSession({ ws, agents, runtimeId: rt.id });
    await must<Runtime>("POST", `/__mock/runtimes/${rt.id}/offline`, { body: { days: 8 } });
    expect((await call("DELETE", `/runtimes/${rt.id}`)).status).toBe(409);
  });

  it("끝난 세션만 있으면 204 로 삭제된다 — 영영 못 지우는 가드는 노트북을 버릴 수 없게 한다", async () => {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    await must<Session>("POST", `/sessions/${sess.id}/cancel`, { body: { reason: "테스트" } });
    expect((await call("DELETE", `/runtimes/${rt.id}`)).status).toBe(204);
  });

  it("런타임 상세는 걸린 세션을 준다(openapi getRuntime `active_sessions`)", async () => {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    const d = await must<{ active_sessions: { id: string }[] }>("GET", `/runtimes/${rt.id}`);
    expect(d.active_sessions.map((x) => x.id)).toEqual([sess.id]);
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("workdir — golden E13 의 판정과 화면 번역", () => {
  async function seeded() {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id, isolation: "worktree" });
    await must<Workdir[]>("POST", `/__mock/sessions/${sess.id}/seed-workdirs`, { body: {} });
    const page = await must<{ items: Workdir[]; disk_bytes_total: number; disk_quota_gb: number | null }>("GET", `/runtimes/${rt.id}/workdirs`);
    return { ws, rt, sess, page };
  }

  it("E13-12·13 — 차단 사유 둘은 값도 문장도 구별된다(Director 의 다음 행동이 다르다)", async () => {
    const { page } = await seeded();
    const unmerged = page.items.find((w) => w.gc_blocked_reason === "unmerged_commits")!;
    const dirty = page.items.find((w) => w.gc_blocked_reason === "uncommitted_changes")!;
    expect(unmerged.commits_ahead).toBe(3);
    expect(dirty.dirty).toBe(true);

    const a = gcBlockText(unmerged)!;
    const b = gcBlockText(dirty)!;
    expect(a.next).not.toBe(b.next); // 하나는 "병합해라", 하나는 "커밋하거나 버려라"
    expect(a.next).toContain("병합");
    expect(b.next).toContain("커밋");
    expect(a.title).toContain("3개");
    // 차단이 없는 행은 문장을 만들지 않는다 — 없는 경고를 그리면 목록 전체가 경고가 된다.
    expect(gcBlockText(page.items.find((w) => w.gc_blocked_reason == null)!)).toBeNull();
  });

  it("삭제는 기본 차단이고(409 workdir_dirty) 사유는 gc_blocked_reason 과 같은 값이다", async () => {
    const { page } = await seeded();
    const w = page.items.find((x) => x.gc_blocked_reason === "unmerged_commits")!;
    expect(deleteBlocked(w)).toBe(true);
    const res = await call("DELETE", `/workdirs/${w.id}`);
    expect(res.status).toBe(409);
    const p = res.body as { code?: string; detail?: string };
    expect(p.code).toBe("workdir_dirty");
    expect(p.detail).toBe("unmerged_commits");
  });

  it("force 확인 뒤 202 · 상태 deleted · **브랜치는 남는다**(FR-6.4 M4)", async () => {
    const { rt, page } = await seeded();
    const w = page.items.find((x) => x.gc_blocked_reason === "unmerged_commits")!;
    const res = await call("DELETE", `/workdirs/${w.id}?force=true`);
    expect(res.status).toBe(202); // 실제 삭제는 데몬이 한다 — 결과는 SSE workdir.updated
    const after = (await must<{ items: Workdir[] }>("GET", `/runtimes/${rt.id}/workdirs`)).items.find((x) => x.id === w.id)!;
    expect(after.status).toBe("deleted");
    expect(after.branch).toBe(w.branch); // 브랜치를 지우면 되돌릴 수 없다 — 워크트리만 지운다
    expect(after.disk_bytes).toBe(0);
  });

  it("용량 합계는 삭제된 것을 빼고 센다 — 정리해도 막대가 안 내려가면 정리한 보람이 없다", async () => {
    const { rt, page } = await seeded();
    const w = page.items[0];
    await call("DELETE", `/workdirs/${w.id}?force=true`);
    const after = await must<{ disk_bytes_total: number }>("GET", `/runtimes/${rt.id}/workdirs`);
    expect(after.disk_bytes_total).toBe(page.disk_bytes_total - w.disk_bytes);
  });

  it("E13-16 — 사용률은 `≥` 에서 막힌다(정확히 꽉 찬 상태도 포함)", () => {
    expect(quotaView(50 * GB, 50).atLimit).toBe(true);
    expect(quotaView(49 * GB, 50).atLimit).toBe(false);
  });

  it("E13-19 — 쿼터 미설정(null)은 0 이 아니라 무제한이다", () => {
    const v = quotaView(900 * GB, null);
    expect(v.atLimit).toBe(false);
    expect(v.ratio).toBeNull();
    expect(quotaView(900 * GB, 0).atLimit).toBe(false);
  });

  it("보존 기한은 세션 종료 시각 기준으로 채워져 온다(E13-09·18) — 목은 14일 뒤를 준다", async () => {
    const { page } = await seeded();
    for (const w of page.items) {
      expect(w.status).toBe("retained");
      expect(w.retain_until).toBeTruthy();
      const days = Math.round((Date.parse(w.retain_until!) - Date.now()) / DAY);
      expect(days).toBe(WORKDIR_RETENTION_DAYS);
    }
  });
});

// ═══════════════════════════════════════════════════════════════════════════
describe("인박스 card.purpose — K-9(#147) 의 N+1 제거", () => {
  it("예산 HITL 항목의 카드가 purpose 를 싣고, 상세와 같은 값이다", async () => {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    const h = await must<HitlRequest>("POST", `/__mock/sessions/${sess.id}/seed-hitl`, {
      body: { source: "system", purpose: "budget", type: "approval", proposed_default: null, agent_id: agents[0], question: "예산 $1 을 초과했습니다" },
    });
    const items = (await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`)).items;
    const it = items.find((x) => x.ref_id === h.id)!;
    expect(it.card?.purpose).toBe("budget");
    expect((await must<HitlRequest>("GET", `/hitl-requests/${h.id}`)).purpose).toBe("budget");
    // task 범위 초과는 세션을 멈추지 않는다 → 화면은 "새 task 상한" 을 그린다(E9-01).
    expect(it.session?.status).toBe("active");
    expect(budgetScopeOf(it)).toBe("task");
  });

  it("비-HITL 항목의 purpose 는 null 이다 — 완료 알림에 금액 칸이 붙으면 안 된다", async () => {
    const { ws, rt, agents } = await base();
    const sess = await newSession({ ws, agents, runtimeId: rt.id });
    await must<Runtime>("POST", `/__mock/runtimes/${rt.id}/offline`, { body: { days: 8 } });
    const items = (await must<{ items: InboxItem[] }>("GET", `/inbox?workspace_id=${ws}`)).items;
    const off = items.find((x) => x.type === "runtime_offline")!;
    expect(off.card?.purpose ?? null).toBeNull();
    expect(off.session_id).toBe(sess.id);
  });
});
