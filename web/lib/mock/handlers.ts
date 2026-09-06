/**
 * 목 API 핸들러 — openapi P1~P3 범위. 라우터는 FR-3.3 규칙 2(명시 멘션 → task, 비참여자 경고)·6(그 외 → assignee)만.
 * 에이전트 실행은 타이머로 흉내 낸다(task_event 원본 레일·typing·delta·답글 메시지·참여자 상태).
 */
import type {
  Agent, AgentProfile, AgentTemplate, Artifact, Decision, HitlRequest, InboxItem, Lane, Member, Message, Pairing,
  Participant, Session, SessionListItem, Task, TaskEvent, TriggerPreview, TriggerTarget, User,
} from "@/lib/api/types";
import {
  emit, makeAgent, makeRuntime, now, participantStatus, resetStore, runtimeModels, runtimeOptionRanges, sseFrame,
  store, stripUser, TEMPLATES, uuid, type MockTask, type Store, type Subscriber,
} from "./store";

export class Problem extends Error {
  constructor(public status: number, public title: string, public code?: string, public detail?: string, public extra?: Record<string, unknown>) {
    super(detail ?? title);
  }
}

export interface Req {
  method: string;
  path: string; // /auth/login
  query: URLSearchParams;
  headers: Headers;
  body: unknown;
  cookies: Record<string, string>;
}
export interface Res {
  status: number;
  body?: unknown;
  headers?: Record<string, string>;
  stream?: ReadableStream<Uint8Array>;
}

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
const routes: { method: string; re: RegExp; keys: string[]; h: Handler }[] = [];
function on(method: string, pattern: string, h: Handler) {
  const keys: string[] = [];
  const re = new RegExp("^" + pattern.replace(/\{(\w+)\}/g, (_, k) => { keys.push(k); return "([^/]+)"; }) + "$");
  routes.push({ method, re, keys, h });
}

export async function dispatch(req: Req): Promise<Res> {
  for (const r of routes) {
    if (r.method !== req.method) continue;
    const m = req.path.match(r.re);
    if (!m) continue;
    const params: Record<string, string> = {};
    r.keys.forEach((k, i) => (params[k] = decodeURIComponent(m[i + 1])));
    try {
      return await r.h(req, params);
    } catch (e) {
      if (e instanceof Problem) return problem(e.status, e.title, e.code, e.detail, e.extra);
      return problem(500, "internal", "internal", String(e));
    }
  }
  return problem(404, "not found", "not_found", `${req.method} ${req.path} 는 목 API 에 없습니다(P1 범위 밖이거나 미구현)`);
}

function problem(status: number, title: string, code?: string, detail?: string, extra?: Record<string, unknown>): Res {
  return { status, body: { type: "about:blank", title, status, code, detail, ...extra }, headers: { "Content-Type": "application/problem+json" } };
}
const ok = (body: unknown, status = 200, headers?: Record<string, string>): Res => ({ status, body, headers });

// ── auth helpers ──
function currentUser(s: Store, req: Req) {
  const tok = req.cookies["colab_session"];
  const uid = tok && s.cookies.get(tok);
  return uid ? s.users.get(uid) ?? null : null;
}
function requireUser(s: Store, req: Req) {
  const u = currentUser(s, req);
  if (!u) throw new Problem(401, "unauthorized", "unauthenticated", "로그인이 필요합니다");
  return u;
}
function requireMember(s: Store, req: Req, workspaceId: string) {
  const u = requireUser(s, req);
  const m = s.members.find((m) => m.workspace_id === workspaceId && m.user.id === u.id);
  if (!m) throw new Problem(403, "forbidden", "not_member", "이 워크스페이스의 멤버가 아닙니다");
  return { user: u, member: m };
}
function setCookie(token: string): Record<string, string> {
  return { "Set-Cookie": `colab_session=${token}; Path=/; HttpOnly; SameSite=Lax` };
}
function issueSession(s: Store, userId: string) {
  const tok = uuid();
  s.cookies.set(tok, userId);
  return tok;
}
function acceptInvite(s: Store, token: string, user: User): Member {
  const inv = s.invites.get(token);
  if (!inv) throw new Problem(404, "not found", "invite_not_found", "초대를 찾을 수 없습니다");
  if (inv.status === "revoked") throw new Problem(410, "gone", "invite_revoked", "초대가 취소되었습니다");
  if (inv.status === "expired" || Date.parse(inv.expires_at) < Date.now()) throw new Problem(410, "gone", "invite_expired", "초대 링크가 만료되었습니다");
  const existing = s.members.find((m) => m.workspace_id === inv.workspace_id && m.user.id === user.id);
  if (existing) return existing;
  const m: Member = { id: uuid(), workspace_id: inv.workspace_id, user, role: inv.role, created_at: now() };
  s.members.push(m);
  inv.status = "accepted";
  return m;
}
const body = <T,>(req: Req) => (req.body ?? {}) as T;

// ── auth ──
on("POST", "/auth/signup", (req) => {
  const s = store();
  const b = body<{ display_name?: string; email?: string; password?: string; invite_token?: string }>(req);
  const errors: { field: string; message: string }[] = [];
  if (!b.display_name?.trim()) errors.push({ field: "display_name", message: "이름을 입력하세요" });
  if (!b.email?.includes("@")) errors.push({ field: "email", message: "이메일 형식이 아닙니다" });
  if (!b.password || b.password.length < 8) errors.push({ field: "password", message: "비밀번호는 8자 이상" });
  if (errors.length) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors });
  if ([...s.users.values()].some((u) => u.email.toLowerCase() === b.email!.toLowerCase())) throw new Problem(409, "conflict", "email_taken", "이미 가입된 이메일입니다");
  const u = { id: uuid(), email: b.email!, display_name: b.display_name!.trim(), avatar_url: null, created_at: now(), password: b.password! };
  s.users.set(u.id, u);
  const tok = issueSession(s, u.id);
  const accepted = b.invite_token ? acceptInvite(s, b.invite_token, stripUser(u)) : undefined;
  return ok({ user: stripUser(u), accepted_invite: accepted, return_to: null }, 201, setCookie(tok));
});
on("POST", "/auth/login", (req) => {
  const s = store();
  const b = body<{ email?: string; password?: string; invite_token?: string }>(req);
  const u = [...s.users.values()].find((u) => u.email.toLowerCase() === (b.email ?? "").toLowerCase());
  if (!u) throw new Problem(401, "unauthorized", "account_not_found", "계정이 없습니다");
  if (u.password !== b.password) throw new Problem(401, "unauthorized", "password_mismatch", "비밀번호 불일치");
  const tok = issueSession(s, u.id);
  const accepted = b.invite_token ? acceptInvite(s, b.invite_token, stripUser(u)) : undefined;
  return ok({ user: stripUser(u), accepted_invite: accepted, return_to: null }, 200, setCookie(tok));
});
on("POST", "/auth/logout", (req) => {
  const s = store();
  requireUser(s, req);
  s.cookies.delete(req.cookies["colab_session"]);
  return { status: 204, headers: { "Set-Cookie": "colab_session=; Path=/; Max-Age=0" } };
});
on("GET", "/me", (req) => {
  const s = store();
  const u = requireUser(s, req);
  const workspaces = s.members.filter((m) => m.user.id === u.id).map((m) => ({ ...s.workspaces.get(m.workspace_id)!, my_role: m.role }));
  const pending = [...s.invites.values()].filter((i) => i.status === "pending").filter(() => false); // 이메일 지정 초대는 P2
  return ok({ user: stripUser(u), workspaces, pending_invites: pending });
});

// ── invites ──
function invitePreview(s: Store, token: string) {
  const inv = s.invites.get(token);
  if (!inv) throw new Problem(404, "not found", "invite_not_found", "초대를 찾을 수 없습니다");
  if (inv.status === "revoked") throw new Problem(410, "gone", "invite_revoked", "초대가 취소되었습니다");
  if (inv.status === "expired" || Date.parse(inv.expires_at) < Date.now()) throw new Problem(410, "gone", "invite_expired", "초대 링크가 만료되었습니다");
  return { token, workspace: s.workspaces.get(inv.workspace_id)!, role: inv.role, invited_by: stripUser(s.users.get(inv.invited_by)!), expires_at: inv.expires_at };
}
on("GET", "/invites/{token}", (_req, p) => ok(invitePreview(store(), p.token)));
on("POST", "/invites/{token}/accept", (req, p) => {
  const s = store();
  const u = requireUser(s, req);
  return ok(acceptInvite(s, p.token, stripUser(u)));
});

// ── workspaces ──
on("GET", "/workspaces", (req) => {
  const s = store();
  const u = requireUser(s, req);
  return ok(s.members.filter((m) => m.user.id === u.id).map((m) => s.workspaces.get(m.workspace_id)));
});
on("POST", "/workspaces", (req) => {
  const s = store();
  const u = requireUser(s, req);
  const b = body<{ name?: string; slug?: string }>(req);
  if (!b.name?.trim()) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: [{ field: "name", message: "이름을 입력하세요" }] });
  const slug = b.slug ?? (b.name.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || `ws-${s.workspaces.size + 1}`);
  const w = { id: uuid(), name: b.name.trim(), slug, created_at: now(), updated_at: now() };
  s.workspaces.set(w.id, w);
  s.members.push({ id: uuid(), workspace_id: w.id, user: stripUser(u), role: "owner", created_at: now() });
  return ok(w, 201);
});
on("GET", "/workspaces/{id}", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  return ok(s.workspaces.get(p.id));
});
on("GET", "/workspaces/{id}/members", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  return ok({ items: s.members.filter((m) => m.workspace_id === p.id), next_cursor: null });
});

// ── runtimes · pairing ──
on("GET", "/workspaces/{id}/runtimes", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  return ok([...s.runtimes.values()].filter((r) => r.workspace_id === p.id));
});
on("POST", "/workspaces/{id}/runtimes/pairings", (req, p) => {
  const s = store();
  const { member } = requireMember(s, req, p.id);
  if (member.role === "member") throw new Problem(403, "forbidden", "not_admin", "런타임 추가는 owner·admin 만 할 수 있습니다");
  const token = `cpk_${uuid().replace(/-/g, "").slice(0, 16)}`;
  // **W-4 확정(2026-09-07)**: `install_commands` 는 **API 오리진**(`COLAB_SERVER_URL`, 기본 :8080)이고
  // 초대 링크만 웹 오리진(`COLAB_WEB_URL`, :3000)이다 — 서버 `runtimes.go:85` + `g3_test.go` S-5 가 그렇게
  // 고정돼 있다. 목이 요청 오리진(:3000 프록시)을 쓰면 화면이 실서버와 다른 명령을 가르친다.
  // 웹은 이 문자열을 **그대로** 보여 줄 뿐 호스트를 다시 쓰지 않는다(`PairingPanel`).
  const origin = process.env.COLAB_MOCK_SERVER_URL ?? "http://localhost:8080";
  const pr: Pairing = {
    id: uuid(), status: "waiting",
    install_commands: [`curl -fsSL ${origin}/install.sh | sh`, `colab-daemon pair ${token} --server ${origin}`],
    pairing_token: token, expires_at: new Date(Date.now() + 30 * 60e3).toISOString(), created_at: now(),
  };
  s.pairings.set(pr.id, pr);
  (pr as Pairing & { workspace_id?: string }).workspace_id = p.id;
  return ok(pr, 201);
});
on("GET", "/workspaces/{id}/runtimes/pairings/{pid}", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  const pr = s.pairings.get(p.pid);
  if (!pr) throw new Problem(404, "not found", "not_found");
  if (pr.status !== "ready" && Date.parse(pr.expires_at) < Date.now()) throw new Problem(410, "gone", "pairing_expired", "페어링이 만료되었습니다");
  return ok(pr);
});
/** 목 전용: 데몬 대신 페어링 단계를 진행시킨다(E2E 2단계). `?to=ready` 기본. */
on("POST", "/__mock/pairings/{pid}/advance", (req, p) => {
  const s = store();
  const pr = s.pairings.get(p.pid) as (Pairing & { workspace_id: string }) | undefined;
  if (!pr) throw new Problem(404, "not found", "not_found");
  const order: Pairing["status"][] = ["waiting", "connected", "probing", "ready"];
  const to = (req.query.get("to") as Pairing["status"]) || "ready";
  const target = order.indexOf(to);
  let i = order.indexOf(pr.status);
  const step = () => {
    if (i >= target) return;
    i += 1;
    pr.status = order[i];
    if (pr.status === "ready") {
      const rt = makeRuntime(pr.workspace_id, req.query.get("name") ?? "my-macbook");
      s.runtimes.set(rt.id, rt);
      pr.runtime = rt;
      emit(s, pr.workspace_id, "runtime.updated", rt);
    }
    emit(s, pr.workspace_id, "pairing.updated", pr);
    setTimeout(step, 700);
  };
  step();
  return ok({ ok: true, to });
});
/** 목 전용: 마지막으로 발급된 페어링(E2E 가 advance 대상 id 를 얻는다). */
on("GET", "/__mock/last-pairing", () => {
  const s = store();
  const last = [...s.pairings.values()].at(-1);
  if (!last) throw new Problem(404, "not found", "not_found");
  return ok({ id: last.id, status: last.status });
});
on("POST", "/__mock/reset", () => {
  resetStore();
  return ok({ ok: true });
});

// ── agents ──
on("GET", "/workspaces/{id}/agents", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  return ok({ items: [...s.agents.values()].filter((a) => a.workspace_id === p.id && !a.archived_at), next_cursor: null });
});
on("POST", "/workspaces/{id}/agents", (req, p) => {
  const s = store();
  const { user } = requireMember(s, req, p.id);
  const b = body<{ name?: string; role?: Agent["role"]; role_description?: string; instructions?: string; profiles?: { runtime_kind: Agent["profiles"][number]["runtime_kind"]; model: string }[] }>(req);
  if (!b.name?.trim()) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: [{ field: "name", message: "이름을 입력하세요" }] });
  if ([...s.agents.values()].some((a) => a.workspace_id === p.id && a.name.toLowerCase() === b.name!.trim().toLowerCase())) throw new Problem(409, "conflict", "name_taken", "같은 이름의 에이전트가 있습니다");
  const a = makeAgent(p.id, user.id, b.name.trim(), b.role ?? "custom", b.role_description ?? "", b.profiles?.[0]);
  if (b.instructions) a.instructions = b.instructions;
  s.agents.set(a.id, a);
  return ok(a, 201);
});

// ── sessions ──
function participantsOf(s: Store, sess: Session): Participant[] {
  return (sess.participants ?? []).map((p) => ({ ...p, status: participantStatus(s, sess.id, p.agent_id) }));
}
function toListItem(s: Store, sess: Session): SessionListItem {
  const tasks = [...s.tasks.values()].filter((t) => t.session_id === sess.id);
  return {
    id: sess.id, title: sess.title, goal: sess.goal, status: sess.status, paused_reason: sess.paused_reason, director: sess.director!,
    participants: (sess.participants ?? []).map((p) => ({ agent_id: p.agent_id, name: p.agent.name, avatar_url: null })),
    completion_progress: { met: sess.completion_progress.met, total: sess.completion_progress.total },
    cost_usd: sess.cost_usd, budget_usd: sess.limits.budget_usd ?? null, cost_estimated: sess.cost_estimated ?? false,
    attention: { hitl_open: 0, blocked: 0, failed: tasks.filter((t) => t.status === "failed").length },
    running_lane_count: tasks.filter((t) => t.status === "running").length, runtime_id: sess.runtime_id,
    last_activity_at: sess.last_activity_at ?? null, created_at: sess.created_at,
  };
}
on("GET", "/workspaces/{id}/sessions", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  const items = [...s.sessions.values()].filter((x) => x.workspace_id === p.id).sort((a, b) => (b.last_activity_at ?? b.created_at).localeCompare(a.last_activity_at ?? a.created_at)).map((x) => toListItem(s, x));
  return ok({ items, next_cursor: null });
});
on("POST", "/workspaces/{id}/sessions", (req, p) => {
  const s = store();
  const { user } = requireMember(s, req, p.id);
  const b = body<{ title?: string; goal?: string; participants?: { agent_id: string; profile_id?: string | null }[]; assignee_agent_id?: string; isolation?: { kind: string }; runtime_id?: string | null; director_user_id?: string; autonomy?: Session["autonomy"]; draft?: boolean }>(req);
  const errors: { field: string; message: string }[] = [];
  if (!b.title?.trim()) errors.push({ field: "title", message: "제목을 입력하세요" });
  if (!b.goal?.trim()) errors.push({ field: "goal", message: "goal 을 입력하세요" });
  if (!b.participants?.length) errors.push({ field: "participants", message: "참여자가 1명 이상 필요합니다" });
  if (b.isolation?.kind === "container") errors.push({ field: "isolation.kind", message: "container 는 v1.1" });
  if (errors.length) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors });
  const online = [...s.runtimes.values()].filter((r) => r.workspace_id === p.id && r.status === "online");
  if (online.length === 0) throw new Problem(409, "conflict", "no_runtime", "런타임이 없습니다 — 먼저 컴퓨터를 연결하세요");
  const t = now();
  const id = uuid();
  const parts: Participant[] = [];
  for (const pp of b.participants!) {
    const a = s.agents.get(pp.agent_id);
    if (!a || a.workspace_id !== p.id) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: [{ field: "participants", message: `에이전트 ${pp.agent_id} 없음` }] });
    parts.push({
      session_id: id, agent_id: a.id, agent: { id: a.id, name: a.name, role: a.role, role_description: a.role_description, avatar_url: null, respond_to: a.respond_to },
      profile: a.profiles[0], status: "idle", status_note: null, is_assignee: false, mention_link: `[@${a.name}](mention://agent/${a.id})`, warnings: [], joined_at: t,
    });
  }
  const assignee = parts.find((x) => x.agent_id === b.assignee_agent_id) ?? parts[0];
  assignee.is_assignee = true;
  const director = s.users.get(b.director_user_id ?? user.id) ?? user;
  const sess: Session = {
    id, workspace_id: p.id, title: b.title!.trim(), goal: b.goal!.trim(), acceptance_criteria: [], director_user_id: director.id, director: stripUser(director),
    deputy_director_user_id: null, assignee_agent_id: assignee.agent_id, runtime_id: b.runtime_id ?? null, isolation: { kind: (b.isolation?.kind as Session["isolation"]["kind"]) ?? "none" },
    completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }] },
    completion_progress: { met: 0, total: 2, satisfied: false, human_gate: true, conditions: [{ path: "/conditions/0", type: "artifact_submitted", met: false, next_actor: assignee.agent.name }, { path: "/conditions/1", type: "user_approval", met: false, next_actor: "director" }] },
    limits: { budget_usd: 20, budget_tokens: null, time_limit: "PT4H", max_tasks: null, max_parallel_lanes: 5 }, autonomy: b.autonomy ?? "guided",
    status: b.draft ? "draft" : "active", paused_reason: null, cost_usd: 0, cost_estimated: false, participants: parts, context: [], my_role: "director",
    created_by: user.id, created_at: t, updated_at: t, started_at: b.draft ? null : t, finished_at: null, last_activity_at: t,
  };
  s.sessions.set(id, sess);
  // goal 시스템 메시지(U1 13단계) + assignee 초기 task(E16-A 1단계)
  const sys = addMessage(s, sess, { author_type: "system", author_id: null, author: undefined, kind: "system", content: `세션 시작 — goal: ${sess.goal}\nDirector: ${director.display_name} · assignee: @${assignee.agent.name}`, mentions: [] });
  if (!b.draft) {
    const task = createTask(s, sess, assignee.agent_id, sys.id);
    simulateRun(s, sess, task, `goal 을 받았습니다. "${sess.goal}" 를 3단계로 진행하겠습니다.\n1) 범위 확정 2) 조사·초안 3) 검토 후 제출\n필요하면 @로 지시를 추가하세요.`);
  }
  return ok({ ...sess, participants: participantsOf(s, sess) }, 201);
});
on("GET", "/sessions/{id}", (req, p) => {
  const s = store();
  const sess = s.sessions.get(p.id);
  if (!sess) throw new Problem(404, "not found", "not_found");
  const { user } = requireMember(s, req, sess.workspace_id);
  return ok(sessionFor(s, sess, user.id));
});

/**
 * 호출자 시점의 세션 — `my_role` 과 **paused 배너의 권한 칸**을 채운다.
 * deputy 에게는 "언제부터 승인 가능한지"(`can_resolve_from`)를, 일반 멤버에게는 아무 동작도 주지 않는다.
 */
function sessionFor(s: Store, sess: Session, userId: string): Session {
  const out: Session = { ...sess, participants: participantsOf(s, sess), my_role: roleOf(sess, userId) };
  if (out.status === "paused" && out.paused_detail) {
    const gate = resumeGate(sess, userId);
    out.paused_detail = {
      ...out.paused_detail,
      can_resolve_from: gate.from,
      resolve_actions: gate.allowed
        ? out.paused_detail.resolve_actions
        : (out.paused_detail.resolve_actions ?? []).filter(() => false),
    };
  }
  return out;
}

// ── messages ──
function addMessage(s: Store, sess: Session, m: Partial<Message> & Pick<Message, "author_type" | "author_id" | "kind" | "content" | "mentions">): Message {
  const msg: Message = {
    id: uuid(), session_id: sess.id, parent_id: null, source_task_id: null, lane_id: null, state: "posted", reply_count: 0, is_note: false,
    created_at: now(), edited_at: null, ...m,
  };
  s.messages.set(msg.id, msg);
  if (msg.parent_id) {
    const root = s.messages.get(msg.parent_id);
    if (root) {
      root.reply_count = (root.reply_count ?? 0) + 1;
      emit(s, sess.workspace_id, "message.updated", root, sess.id);
    }
  }
  sess.last_activity_at = msg.created_at;
  emit(s, sess.workspace_id, "message.created", msg, sess.id);
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, status: sess.status, last_activity_at: sess.last_activity_at }, sess.id);
  return msg;
}
/** lane 하나. `brief` 는 첫 트리거 메시지 발췌다(카드의 위임 요약). */
function createLane(s: Store, sess: Session, agentId: string, brief: string | null): Lane {
  const a = s.agents.get(agentId);
  const t = now();
  const lane: Lane = {
    id: uuid(), session_id: sess.id, parent_lane_id: null, agent_id: agentId, agent_name: a?.name ?? "agent",
    profile_id: a?.profiles[0]?.id ?? uuid(), depends_on: [], workdir_id: null, workdir_ref: null,
    delegated_from_task_id: null, has_runtime_session: false, brief, status: "queued", blocked_note: null,
    blocked_message_id: null, waiting_for: null, hitl_request_id: null, paused_over_usd: null, failure_kind: null,
    reentry_count: 0, current_activity: null, queue_position: null, actions: laneActions(sess, "queued"),
    created_at: t, updated_at: t, finished_at: null,
  };
  s.lanes.set(lane.id, lane);
  emit(s, sess.workspace_id, "lane.updated", lane, sess.id);
  return lane;
}
/** 호출자가 지금 할 수 있는 동작(Lane.actions). 목은 항상 Director 시점이다. */
function laneActions(_sess: Session, status: Lane["status"]): Lane["actions"] {
  switch (status) {
    case "running":
      return ["restart", "cancel"];
    case "queued":
      return ["cancel"];
    case "blocked":
      return ["open_question"];
    case "waiting_human":
      return ["respond_hitl"];
    case "paused":
      return ["approve_budget", "cancel"];
    case "failed":
      return ["restart"];
    default:
      return [];
  }
}
/**
 * lane 중단·재지시 권한(FR-3.4 t-3 · golden E10-05·E10-06) — **Director 와 deputy 즉시**.
 * deputy 의 비대칭이 여기 있다: 승인은 기한 절반을 기다리지만 **취소는 시점 제한이 없다** — 폭주는
 * 기다릴수록 비싸지기 때문이다. 일반 멤버는 403 이고 버튼은 보이되 비활성이다.
 */
function mayControlLane(sess: Session, userId: string): boolean {
  const r = roleOf(sess, userId);
  return r === "director" || r === "deputy";
}
/** 호출자 시점의 lane — `actions` 는 권한을 반영한 목록이다(계약 `Lane.actions`). */
function laneFor(sess: Session, lane: Lane, userId: string): Lane {
  if (mayControlLane(sess, userId)) return lane;
  return { ...lane, actions: (lane.actions ?? []).filter((a) => a === "open_question") };
}
function setLaneStatus(s: Store, sess: Session, laneId: string, patch: Partial<Lane>) {
  const lane = s.lanes.get(laneId);
  if (!lane) return;
  Object.assign(lane, patch, { updated_at: now() });
  lane.actions = laneActions(sess, lane.status);
  emit(s, sess.workspace_id, "lane.updated", lane, sess.id);
}
function createTask(s: Store, sess: Session, agentId: string, triggerId: string | null, opts: { laneId?: string; brief?: string | null; restartedFrom?: string | null } = {}): MockTask {
  const laneId = opts.laneId ?? createLane(s, sess, agentId, opts.brief ?? null).id;
  const t: MockTask = {
    id: uuid(), session_id: sess.id, agent_id: agentId, lane_id: laneId, status: "queued", attempt: 1,
    trigger_message_id: triggerId, created_at: now(), restarted_from_task_id: opts.restartedFrom ?? null,
    failure_kind: null, started_at: null, finished_at: null, resumed: null, cost_usd: 0,
  };
  s.tasks.set(t.id, t);
  s.taskEvents.set(t.id, []);
  return t;
}

/** MockTask → 계약 `Task`(lane 카드 펼침의 정보 5종). */
function toTask(s: Store, t: MockTask): Task {
  const lane = s.lanes.get(t.lane_id);
  const sess = s.sessions.get(t.session_id);
  return {
    id: t.id, lane_id: t.lane_id, session_id: t.session_id, runtime_id: sess?.runtime_id ?? null, agent_id: t.agent_id,
    profile_id: lane?.profile_id ?? uuid(), trigger_message_id: t.trigger_message_id,
    delegated_from_task_id: null, restarted_from_task_id: t.restarted_from_task_id ?? null, originator_user_id: null,
    coalesced_message_ids: [], attempt: t.attempt, max_attempts: 3,
    pending_hitl: [...s.hitls.values()].some((h) => h.task_id === t.id && h.status === "open"),
    budget_override: t.budget_override ?? null,
    status: t.status as Task["status"],
    paused_reason: (t.status === "paused" ? "budget" : null) as Task["paused_reason"],
    failure_kind: (t.failure_kind ?? null) as Task["failure_kind"],
    transport: "acp", resumed: t.resumed ?? null,
    attempts: t.attempts?.length
      ? t.attempts
      : [{ attempt: t.attempt, started_at: t.started_at ?? null, finished_at: t.finished_at ?? null, resumed: t.resumed ?? null, outcome: t.failure_kind ?? t.status, cost_usd: t.cost_usd ?? 0 }],
    usage: { input_tokens: 1200, output_tokens: 180, cache_read: 0, cost_usd: t.cost_usd ?? 0, estimated: false },
    open_hitl_request_id: [...s.hitls.values()].find((h) => h.task_id === t.id && h.status === "open")?.id ?? null,
    heartbeat_at: null,
    created_at: t.created_at, updated_at: now(), dispatched_at: t.started_at ?? null, started_at: t.started_at ?? null, finished_at: t.finished_at ?? null,
  };
}
function pushEvent(s: Store, sess: Session, task: MockTask, e: Partial<TaskEvent> & Pick<TaskEvent, "class">): TaskEvent {
  const list = s.taskEvents.get(task.id)!;
  const ev: TaskEvent = { id: uuid(), task_id: task.id, seq: list.length + 1, verb: null, object_ref: null, outcome: null, tool: null, input: null, output: null, usage: null, superseded_by: null, masked: false, sentence: null, created_at: now(), ...e };
  list.push(ev);
  emit(s, sess.workspace_id, "task_event.appended", ev, sess.id);
  return ev;
}
function supersede(s: Store, sess: Session, task: MockTask, old: TaskEvent, e: Partial<TaskEvent> & Pick<TaskEvent, "class">) {
  const nv = pushEvent(s, sess, task, e);
  old.superseded_by = nv.id;
  emit(s, sess.workspace_id, "task_event.superseded", { task_id: task.id, event_id: old.id, superseded_by: nv.id }, sess.id);
}
function emitParticipant(s: Store, sess: Session, agentId: string, note: string | null) {
  const p = (sess.participants ?? []).find((x) => x.agent_id === agentId);
  if (!p) return;
  p.status = participantStatus(s, sess.id, agentId);
  p.status_note = note;
  emit(s, sess.workspace_id, "participant.updated", p, sess.id);
}
/** 에이전트 실행 흉내 — 실제 데몬·ACP 는 D 스트림. 여기서는 타임라인·레일·typing·delta 가 실시간으로 흐르는지 보기 위한 것. */
function simulateRun(s: Store, sess: Session, task: MockTask, reply: string) {
  const agent = s.agents.get(task.agent_id)!;
  const rt = [...s.runtimes.values()].find((r) => r.workspace_id === sess.workspace_id && r.status === "online");
  if (rt && !sess.runtime_id) {
    sess.runtime_id = rt.id; // none 격리 자동 선택 — 첫 dispatch 시 고정(M10)
    emit(s, sess.workspace_id, "session.updated", { id: sess.id, runtime_id: rt.id }, sess.id);
  }
  const at = (ms: number, f: () => void) => setTimeout(f, ms);
  at(300, () => {
    task.status = "running";
    task.started_at = now();
    // attempt 1 은 콜드 스타트(카드가 그것을 표시하는지 보기 위해서다), 재개는 resume 우선이다(FR-5.4).
    task.resumed = task.attempt > 1;
    task.attempts = [
      ...(task.attempts ?? []),
      { attempt: task.attempt, started_at: task.started_at, finished_at: null, resumed: task.resumed, outcome: null, cost_usd: 0 },
    ];
    setLaneStatus(s, sess, task.lane_id, { status: "running", current_activity: "세션을 시작했다 → cold_start", has_runtime_session: true, queue_position: null });
    emitParticipant(s, sess, agent.id, "lane 실행 중");
    pushEvent(s, sess, task, { class: "runtime", verb: "start", object_ref: null, outcome: "cold_start", payload: { runtime_kind: "claude_code", session_id: `acp-${task.id.slice(0, 8)}` }, sentence: `${agent.name}가 세션을 시작했다 → cold_start` });
    emit(s, sess.workspace_id, "agent.typing", { session_id: sess.id, agent_id: agent.id, typing: true }, sess.id, true);
  });
  let thinking: TaskEvent | undefined;
  at(700, () => {
    thinking = pushEvent(s, sess, task, { class: "message", verb: "think", object_ref: null, outcome: "started", payload: { kind: "thought" }, sentence: `${agent.name}가 생각하는 중…` });
  });
  at(1200, () => {
    if (thinking) supersede(s, sess, task, thinking, { class: "message", verb: "think", object_ref: null, outcome: "ok", payload: { kind: "thought", chars: 412 }, sentence: `${agent.name}가 계획을 생각했다 → ok` });
    // 데몬(PR #20)처럼 한 툴 호출을 started → ok 두 이벤트로, superseded_by 없이 tool_call_id 로만 잇는다(R1 검증용)
    const tcid = `call_${task.id.slice(0, 8)}`;
    pushEvent(s, sess, task, { class: "tool", verb: "read", object_ref: "README.md", outcome: "started", tool: "Read", sentence: `${agent.name}가 README.md 를 읽는 중…`, payload: { tool_call_id: tcid, kind: "read" } } as Partial<TaskEvent> & Pick<TaskEvent, "class">);
    setLaneStatus(s, sess, task.lane_id, { current_activity: `README.md 를 읽는 중…` });
  });
  at(1400, () => {
    const tcid = `call_${task.id.slice(0, 8)}`;
    pushEvent(s, sess, task, { class: "tool", verb: "read", object_ref: "README.md", outcome: "ok", tool: "Read", sentence: `${agent.name}가 README.md 를 읽었다 → ok`, payload: { tool_call_id: tcid, kind: "read" } } as Partial<TaskEvent> & Pick<TaskEvent, "class">);
  });
  const chunks = reply.match(/.{1,12}/gs) ?? [reply];
  chunks.forEach((c, i) => at(1500 + i * 60, () => emit(s, sess.workspace_id, "message.delta", { session_id: sess.id, task_id: task.id, agent_id: agent.id, text: c }, sess.id, true)));
  at(1500 + chunks.length * 60 + 100, () => {
    emit(s, sess.workspace_id, "agent.typing", { session_id: sess.id, agent_id: agent.id, typing: false }, sess.id, true);
    const msg = addMessage(s, sess, { author_type: "agent", author_id: agent.id, author: { name: agent.name, avatar_url: null, role: agent.role }, kind: "text", content: reply, mentions: [], source_task_id: task.id, lane_id: task.lane_id });
    pushEvent(s, sess, task, { class: "status", verb: "post_message", object_ref: msg.id, outcome: "ok", sentence: `${agent.name}가 메시지를 게시했다 → ok` });
    pushEvent(s, sess, task, { class: "usage", verb: "report", object_ref: null, outcome: "report", usage: { input_tokens: 1200, output_tokens: 180, cost_usd: 0.02 } });
    pushEvent(s, sess, task, { class: "runtime", verb: "turn_end", object_ref: null, outcome: "ok", sentence: `턴 종료 → ok` });
    task.status = "completed";
    task.finished_at = now();
    task.cost_usd = 0.02;
    const cur = task.attempts?.find((a) => a.attempt === task.attempt);
    if (cur) { cur.finished_at = task.finished_at; cur.outcome = "completed"; cur.cost_usd = 0.02; }
    setLaneStatus(s, sess, task.lane_id, { status: "done", current_activity: null, finished_at: task.finished_at, brief: reply.slice(0, 60) });
    sess.cost_usd = Math.round((sess.cost_usd + 0.02) * 100) / 100;
    emit(s, sess.workspace_id, "cost.updated", { session_id: sess.id, cost_usd: sess.cost_usd, estimated: false }, sess.id);
    emitParticipant(s, sess, agent.id, null);
  });
}

// FR-3.2: `mention://<kind>/<id>` — @all 은 `mention://all/all` 이다(서버 정규식과 같은 모양).
const MENTION_RE = /\[@([^\]]+)\]\(mention:\/\/(agent|user|all)\/([0-9a-zA-Z-]+)\)/g;

on("GET", "/sessions/{id}/messages", (req, p) => {
  const s = store();
  const sess = s.sessions.get(p.id);
  if (!sess) throw new Problem(404, "not found", "not_found");
  requireMember(s, req, sess.workspace_id);
  const thread = req.query.get("thread");
  const includeReplies = req.query.get("include_replies") === "true";
  let items = [...s.messages.values()].filter((m) => m.session_id === sess.id);
  if (thread) items = items.filter((m) => m.id === thread || m.parent_id === thread);
  else if (!includeReplies) items = items.filter((m) => !m.parent_id);
  items.sort((a, b) => a.created_at.localeCompare(b.created_at));
  const limit = Number(req.query.get("limit") ?? 50);
  const total = items.length;
  items = items.slice(-limit);
  return ok({ items, before_cursor: null, after_cursor: items.at(-1)?.created_at ?? null, has_more_before: total > items.length, has_more_after: false, total: thread ? total : null });
});
/**
 * 라우터(FR-3.3) — 규칙 1(`/note`) · 2(명시 멘션) · 3(`@all`·사람만 멘션 → 트리거 없음) · 6(그 외 → assignee).
 * **미리보기와 실제 게시가 같은 함수를 쓴다** — 그래야 칩과 결과가 반대로 말하지 않는다(P1 S-1 이 그랬다).
 *
 * lane 해소: 1 새 lane · 3 실행 중 lane 재사용(큐잉) · 4 done·blocked 재진입.
 * `new_lane` 토글이 켜지면 해소를 건너뛰고 **항상 새 lane**(t-2).
 */
function routeMessage(
  s: Store,
  sess: Session,
  input: { content: string; mentions: Message["mentions"]; parentId: string | null; newLane: boolean; suppress: Set<string> },
): TriggerPreview {
  const warnings: TriggerPreview["warnings"] = [];
  const noteOnly = input.content.trimStart().startsWith("/note ");
  const parts = new Set((sess.participants ?? []).map((x) => x.agent_id));
  const agentMentions = input.mentions.filter((m) => m.kind === "agent");
  const hasOtherMentions = input.mentions.some((m) => m.kind !== "agent");

  if (noteOnly) return { note_only: true, implicit_routing_suppressed: false, triggers: [], warnings };

  const targets: { id: string; rule: number }[] = [];
  for (const m of agentMentions) {
    const a = s.agents.get(m.id);
    if (!a || a.workspace_id !== sess.workspace_id) { warnings.push({ code: "unknown_agent", message: `@${m.display_name} 를 찾을 수 없음`, agent_id: null }); continue; }
    if (!parts.has(a.id)) { warnings.push({ code: "not_participant", message: `${a.name}는 이 세션 참여자가 아닙니다 — 트리거되지 않습니다`, agent_id: a.id }); continue; } // E1-04
    if (a.respond_to === "nobody") { warnings.push({ code: "agent_disabled", message: `${a.name}는 정지 상태입니다(respond_to: nobody)`, agent_id: a.id }); continue; }
    if (input.suppress.has(a.id)) continue; // 억제된 대상은 트리거 목록에서 빠진다(칩은 화면이 들고 있다)
    targets.push({ id: a.id, rule: 2 });
  }
  // 규칙 3 — 에이전트 멘션이 없고 @all·사람 멘션만 있으면 암묵 라우팅을 끈다
  const implicitSuppressed = agentMentions.length === 0 && hasOtherMentions;
  if (agentMentions.length === 0 && !implicitSuppressed && sess.assignee_agent_id && !input.parentId) {
    if (!input.suppress.has(sess.assignee_agent_id)) targets.push({ id: sess.assignee_agent_id, rule: 6 });
  }

  const triggers: TriggerTarget[] = [];
  for (const { id, rule } of targets) {
    const a = s.agents.get(id)!;
    const prof = a.profiles.find((x) => x.is_default) ?? a.profiles[0];
    const lanes = [...s.lanes.values()].filter((l) => l.session_id === sess.id && l.agent_id === id);
    const active = lanes.find((l) => l.status === "running" || l.status === "queued");
    const reusable = lanes.find((l) => l.status === "done" || l.status === "blocked");
    // 기본은 규칙 4(새 lane) 다 — 규칙 1 은 스레드 답글의 lane 이고 목은 그것을
    // 흉내내지 않는다. "새 lane 으로 보내기" 는 규칙 3 을 건너뛰어 4 로 간다(EVAL E2-07).
    // **재사용은 진행 중이든 재진입이든 전부 규칙 3 이다** — PRD lane 해소 규칙 3 이
    // "가장 최근 lane 재사용, `done`·`blocked` 면 재진입(reentry_count 증가)" 이고
    // 4 는 "그 외 → 새 lane" 이다. 재진입을 4 로 주면 EVAL E2-04·05 와 어긋난다.
    let resolution = 4;
    let laneId: string | null = null;
    let reentry = false;
    if (!input.newLane) {
      if (active) { resolution = 3; laneId = active.id; }
      else if (reusable) { resolution = 3; laneId = reusable.id; reentry = true; }
    }
    triggers.push({
      agent_id: id,
      agent_name: a.name,
      profile: prof ? { id: prof.id, name: prof.name, runtime_kind: prof.runtime_kind, model: prof.model } : undefined,
      rule,
      lane: { resolution, lane_id: laneId, reentry },
      will_queue: active?.status === "running",
      deferred_until: null,
    });
  }
  return { note_only: false, implicit_routing_suppressed: implicitSuppressed, triggers, warnings };
}

function parseMentions(content: string): Message["mentions"] {
  const mentions: Message["mentions"] = [];
  for (const m of content.matchAll(MENTION_RE)) {
    mentions.push({ kind: m[2] as "agent" | "user" | "all", id: m[3], display_name: m[1] });
  }
  return mentions;
}

/** 트리거 미리보기(FR-3.6) — 게시하지 않는다. */
on("POST", "/sessions/{id}/messages/preview", (req, p) => {
  const s = store();
  const sess = s.sessions.get(p.id);
  if (!sess) throw new Problem(404, "not found", "not_found");
  requireMember(s, req, sess.workspace_id);
  const b = body<{ content?: string; parent_id?: string | null; new_lane?: boolean; suppress_agent_ids?: string[] }>(req);
  const content = b.content ?? "";
  return ok(routeMessage(s, sess, {
    content,
    mentions: parseMentions(content),
    parentId: b.parent_id ?? null,
    newLane: b.new_lane ?? false,
    suppress: new Set(b.suppress_agent_ids ?? []),
  }));
});

on("POST", "/sessions/{id}/messages", (req, p) => {
  const s = store();
  const sess = s.sessions.get(p.id);
  if (!sess) throw new Problem(404, "not found", "not_found");
  const { user } = requireMember(s, req, sess.workspace_id);
  const key = req.headers.get("idempotency-key");
  if (!key) throw new Problem(422, "validation failed", "validation_failed", "Idempotency-Key 헤더가 필요합니다", { errors: [{ field: "Idempotency-Key", message: "required" }] });
  if (s.idem.has(key)) return ok(s.idem.get(key), 201, { "Idempotent-Replayed": "true" });
  const b = body<{ content?: string; parent_id?: string | null; new_lane?: boolean; suppress_agent_ids?: string[] }>(req);
  if (!b.content?.trim()) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: [{ field: "content", message: "내용을 입력하세요" }] });
  if (sess.status === "completed" || sess.status === "cancelled") throw new Problem(409, "conflict", "invalid_transition", "종료된 세션에는 게시할 수 없습니다");
  let parentId = b.parent_id ?? null;
  if (parentId) {
    const parent = s.messages.get(parentId);
    if (!parent || parent.session_id !== sess.id) throw new Problem(404, "not found", "not_found", "답글 대상 메시지가 없습니다");
    parentId = parent.parent_id ?? parent.id; // 루트로 정규화
  }
  const mentions = parseMentions(b.content);
  const isNote = b.content.startsWith("/note ");
  const suppress = new Set(b.suppress_agent_ids ?? []);
  const preview = routeMessage(s, sess, { content: b.content, mentions, parentId, newLane: b.new_lane ?? false, suppress });
  const msg = addMessage(s, sess, { author_type: "user", author_id: user.id, author: { name: user.display_name, avatar_url: null }, kind: "text", content: b.content, mentions, parent_id: parentId, is_note: isNote });

  const warnings = [...preview.warnings];
  for (const id of suppress) {
    const a = s.agents.get(id);
    if (a) warnings.push({ code: "suppressed", message: `@${a.name} 트리거 억제됨(FR-3.6)`, agent_id: a.id });
  }
  const triggers: { agent_id: string; task_id: string; lane_id: string; coalesced: boolean; deferred_until: null }[] = [];
  // 세션이 paused 면 lane·task 는 만들되 dispatch 하지 않는다(C3′ · U15-9)
  const dispatchable = sess.status === "active";
  for (const t of preview.triggers) {
    const queued = [...s.tasks.values()].find((x) => x.session_id === sess.id && x.agent_id === t.agent_id && x.status === "queued");
    if (queued) { triggers.push({ agent_id: t.agent_id, task_id: queued.id, lane_id: queued.lane_id, coalesced: true, deferred_until: null }); continue; }
    const brief = b.content.replace(MENTION_RE, "@$1").trim().slice(0, 80);
    const laneId = t.lane.lane_id ?? undefined;
    if (laneId && t.lane.reentry) {
      const lane = s.lanes.get(laneId)!;
      setLaneStatus(s, sess, laneId, { status: "queued", reentry_count: lane.reentry_count + 1, finished_at: null, brief });
    }
    const task = createTask(s, sess, t.agent_id, msg.id, { laneId, brief });
    triggers.push({ agent_id: t.agent_id, task_id: task.id, lane_id: task.lane_id, coalesced: false, deferred_until: null });
    const agent = s.agents.get(t.agent_id)!;
    if (dispatchable) simulateRun(s, sess, task, `안녕하세요, ${agent.name}입니다. "${brief}" 잘 받았습니다. 바로 진행하겠습니다.`);
    else setLaneStatus(s, sess, task.lane_id, { status: "queued", queue_position: 1, current_activity: "세션 재개 후 처리됩니다" });
  }
  const result = { message: msg, triggers, warnings, session_paused: null };
  s.idem.set(key, result);
  return ok(result, 201);
});

// ── tasks ──
on("GET", "/tasks/{id}/events", (req, p) => {
  const s = store();
  const task = s.tasks.get(p.id);
  if (!task) throw new Problem(404, "not found", "not_found");
  const sess = s.sessions.get(task.session_id)!;
  requireMember(s, req, sess.workspace_id);
  const includeSuperseded = req.query.get("include_superseded") === "true";
  const after = Number(req.query.get("after_seq") ?? -1);
  let items = (s.taskEvents.get(task.id) ?? []).filter((e) => e.seq > after);
  if (!includeSuperseded) items = items.filter((e) => !e.superseded_by);
  return ok({ items, has_more: false, structured: true });
});

// ── realtime ──
on("GET", "/workspaces/{id}/stream", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  const sessionIds = req.query.get("session_id")?.split(",").filter(Boolean) ?? null;
  const lastId = Number(req.headers.get("last-event-id") ?? req.query.get("last_event_id") ?? 0);
  const enc = new TextEncoder();
  let sub: Subscriber | null = null;
  let ping: ReturnType<typeof setInterval> | null = null;
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const write = (frame: string) => controller.enqueue(enc.encode(frame));
      write(`: connected\n\n`);
      if (lastId > 0) {
        const oldest = s.events[0]?.id ?? 0;
        if (lastId < oldest - 1) write(sseFrame({ id: s.eventSeq, type: "resync", workspace_id: p.id, session_id: null, at: now(), payload: { reason: "out_of_window" }, ephemeral: false }));
        else for (const ev of s.events) if (ev.id > lastId && ev.workspace_id === p.id && (!sessionIds || !ev.session_id || sessionIds.includes(ev.session_id))) write(sseFrame(ev));
      }
      sub = { workspace_id: p.id, session_ids: sessionIds, write };
      s.subs.add(sub);
      ping = setInterval(() => { try { write(`: ping\n\n`); } catch { /* closed */ } }, 15000);
    },
    cancel() {
      if (sub) s.subs.delete(sub);
      if (ping) clearInterval(ping);
    },
  });
  return { status: 200, stream, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache, no-transform", Connection: "keep-alive", "X-Accel-Buffering": "no" } };
});

// ── lanes (S7 좌열) ──
function sessionOf(s: Store, req: Req, sessionId: string): Session {
  const sess = s.sessions.get(sessionId);
  if (!sess) throw new Problem(404, "not found", "not_found");
  requireMember(s, req, sess.workspace_id);
  return sess;
}
on("GET", "/sessions/{id}/lanes", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  const filter = req.query.get("status")?.split(",").filter(Boolean);
  let items = [...s.lanes.values()].filter((l) => l.session_id === sess.id);
  if (filter?.length) items = items.filter((l) => filter.includes(l.status));
  items.sort((a, b) => a.created_at.localeCompare(b.created_at));
  return ok(items.map((l) => laneFor(sess, l, user.id)));
});
on("GET", "/lanes/{id}", (req, p) => {
  const s = store();
  const lane = s.lanes.get(p.id);
  if (!lane) throw new Problem(404, "not found", "not_found");
  const sess = sessionOf(s, req, lane.session_id);
  const { user } = requireMember(s, req, sess.workspace_id);
  return ok(laneFor(sess, lane, user.id));
});
on("GET", "/lanes/{id}/tasks", (req, p) => {
  const s = store();
  const lane = s.lanes.get(p.id);
  if (!lane) throw new Problem(404, "not found", "not_found");
  sessionOf(s, req, lane.session_id);
  const items = [...s.tasks.values()].filter((t) => t.lane_id === lane.id).sort((a, b) => a.created_at.localeCompare(b.created_at));
  return ok(items.map((t) => toTask(s, t)));
});
/** 중단(FR-3.4) — 진행 중 턴만 취소한다. lane `failed(cancelled)`. `paused`는 실패가 아니지만 명시 종료는 이것이다. */
on("POST", "/lanes/{id}/cancel", (req, p) => {
  const s = store();
  const lane = s.lanes.get(p.id);
  if (!lane) throw new Problem(404, "not found", "not_found");
  const sess = sessionOf(s, req, lane.session_id);
  const { user: actor } = requireMember(s, req, sess.workspace_id);
  // E10-05 — 버튼이 비활성인 것은 강제가 아니다. API 도 막는다.
  if (!mayControlLane(sess, actor.id)) throw new Problem(403, "forbidden", "not_director", "Director·deputy 만 lane 을 중단할 수 있습니다");
  if (lane.status === "done" || lane.status === "failed") throw new Problem(409, "conflict", "invalid_transition", "이미 종료된 lane 입니다");
  for (const t of s.tasks.values()) {
    if (t.lane_id !== lane.id || t.status === "completed" || t.status === "cancelled") continue;
    t.status = "cancelled";
    t.failure_kind = "cancelled";
    t.finished_at = now();
    pushEvent(s, sess, t, { class: "status", verb: "cancel", object_ref: lane.id, outcome: "cancelled", sentence: "사람이 중단함" });
  }
  setLaneStatus(s, sess, lane.id, { status: "failed", failure_kind: "cancelled", current_activity: null, finished_at: now() });
  emitParticipant(s, sess, lane.agent_id, null);
  return ok(s.lanes.get(lane.id), 202);
});
/** 중단하고 다시 지시(FR-3.4 B) — 진행 중 턴을 취소하고 **새 task**(`restarted_from_task_id`)를 만든다. lane 은 running 유지. */
on("POST", "/lanes/{id}/restart", (req, p) => {
  const s = store();
  const lane = s.lanes.get(p.id);
  if (!lane) throw new Problem(404, "not found", "not_found");
  const sess = sessionOf(s, req, lane.session_id);
  const { user: actor } = requireMember(s, req, sess.workspace_id);
  if (!mayControlLane(sess, actor.id)) throw new Problem(403, "forbidden", "not_director", "Director·deputy 만 다시 지시할 수 있습니다");
  const key = req.headers.get("idempotency-key");
  if (!key) throw new Problem(422, "validation failed", "validation_failed", "Idempotency-Key 헤더가 필요합니다", { errors: [{ field: "Idempotency-Key", message: "required" }] });
  if (s.idem.has(key)) return ok(s.idem.get(key), 202, { "Idempotent-Replayed": "true" });
  const b = body<{ content?: string }>(req);
  if (!b.content?.trim()) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: [{ field: "content", message: "새 지시를 입력하세요" }] });
  if (!["running", "failed", "paused"].includes(lane.status)) throw new Problem(409, "conflict", "invalid_transition", "running·failed·paused lane 만 다시 지시할 수 있습니다");
  const agent = s.agents.get(lane.agent_id)!;
  let cancelled: string | null = null;
  for (const t of s.tasks.values()) {
    if (t.lane_id !== lane.id || t.status === "completed" || t.status === "cancelled" || t.status === "failed") continue;
    t.status = "cancelled";
    t.failure_kind = "cancelled";
    t.finished_at = now();
    cancelled = t.id;
    pushEvent(s, sess, t, { class: "status", verb: "cancel", object_ref: lane.id, outcome: "cancelled", sentence: "사람이 중단하고 다시 지시함" });
  }
  const mention = `[@${agent.name}](mention://agent/${agent.id})`;
  const content = b.content.includes(`mention://agent/${agent.id}`) ? b.content : `${mention} ${b.content}`;
  const { user } = requireMember(s, req, sess.workspace_id);
  const msg = addMessage(s, sess, { author_type: "user", author_id: user.id, author: { name: user.display_name, avatar_url: null }, kind: "text", content, mentions: parseMentions(content) });
  const task = createTask(s, sess, agent.id, msg.id, { laneId: lane.id, restartedFrom: cancelled, brief: b.content.slice(0, 80) });
  setLaneStatus(s, sess, lane.id, { status: "running", failure_kind: null, finished_at: null, brief: b.content.slice(0, 80) });
  simulateRun(s, sess, task, `다시 받았습니다. "${b.content.slice(0, 60)}" 로 진행하겠습니다.`);
  const result = { lane: s.lanes.get(lane.id), message: msg, task: toTask(s, task), cancelled_task_id: cancelled };
  s.idem.set(key, result);
  return ok(result, 202);
});

// ── 아티팩트 · 결정 기록 · 비용 (S7 우열) ──
on("GET", "/sessions/{id}/artifacts", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const latestOnly = req.query.get("latest_only") === "true";
  let items = [...s.artifacts.values()].filter((a) => a.session_id === sess.id);
  if (latestOnly) items = items.filter((a) => a.latest !== false);
  return ok(items.sort((a, b) => b.created_at.localeCompare(a.created_at)));
});
on("GET", "/sessions/{id}/decisions", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  return ok([...s.decisions.values()].filter((d) => d.session_id === sess.id).sort((a, b) => b.created_at.localeCompare(a.created_at)));
});
on("GET", "/sessions/{id}/cost", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  return ok({
    total_usd: sess.cost_usd, budget_usd: sess.limits.budget_usd ?? null, estimated: sess.cost_estimated ?? false,
    input_tokens: 12000, output_tokens: 1800, cache_read: 0,
  });
});

// ── 세션 일시정지 · 재개(FR-2.3 · O6) ──
/**
 * "계속 진행 승인" 권한(SCREEN §4.5 paused 해소 표) — Director, **그리고 예산·시간·루프면 기한 절반 후 deputy**.
 * `director`(수동 일시정지)와 `runtime_offline` 은 Director 전용이다. deputy 의 시점은 HITL 과 같은 12h 다.
 */
function resumeGate(sess: Session, userId: string): { allowed: boolean; from: string | null; reason: string } {
  const role = roleOf(sess, userId);
  if (role === "director") return { allowed: true, from: null, reason: "" };
  const delegable = sess.paused_reason === "budget" || sess.paused_reason === "time" || sess.paused_reason === "loop";
  if (role === "deputy" && delegable) {
    const at = sess.paused_detail?.paused_at ? Date.parse(sess.paused_detail.paused_at) : Date.now();
    const from = new Date(at + DEPUTY_HALF_MS).toISOString();
    return Date.now() >= at + DEPUTY_HALF_MS
      ? { allowed: true, from: null, reason: "" }
      : { allowed: false, from, reason: "기한의 절반이 지나야 승인할 수 있습니다" };
  }
  return { allowed: false, from: null, reason: "Director 만 할 수 있습니다" };
}
on("POST", "/sessions/{id}/pause", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  requireDirector(sess, user.id);
  if (sess.status !== "active") throw new Problem(409, "conflict", "invalid_transition", "active 세션만 일시정지할 수 있습니다");
  sess.status = "paused";
  sess.paused_reason = "director";
  sess.paused_detail = { reason: "director", paused_at: now(), resolve_actions: ["resume", "cancel"], can_resolve_from: null };
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, status: sess.status, paused_reason: sess.paused_reason, paused_detail: sess.paused_detail }, sess.id);
  return ok(sessionFor(s, sess, requireMember(s, req, sess.workspace_id).user.id));
});
on("POST", "/sessions/{id}/resume", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user: actor } = requireMember(s, req, sess.workspace_id);
  const gate = resumeGate(sess, actor.id);
  if (!gate.allowed) throw new Problem(403, "forbidden", "deputy_not_yet", gate.reason, { can_respond_from: gate.from });
  if (sess.status !== "paused") throw new Problem(409, "conflict", "invalid_transition", "paused 세션만 재개할 수 있습니다");
  if (sess.paused_reason === "runtime_offline") throw new Problem(409, "conflict", "invalid_transition", "런타임 오프라인은 재바인딩하거나 세션을 종료해야 합니다");
  const b = body<{ limits?: { budget_usd?: number; time_limit?: string }; reset_loop_counters?: boolean }>(req);
  if (sess.paused_reason === "budget" && b.limits?.budget_usd != null) {
    if (b.limits.budget_usd < sess.cost_usd) throw new Problem(422, "validation failed", "validation_failed", "새 상한은 현재 소진액 이상이어야 합니다", { errors: [{ field: "limits.budget_usd", message: `$${sess.cost_usd} 이상` }] });
    sess.limits = { ...sess.limits, budget_usd: b.limits.budget_usd };
  }
  if (sess.paused_reason === "time" && b.limits?.time_limit) sess.limits = { ...sess.limits, time_limit: b.limits.time_limit };
  sess.status = "active";
  sess.paused_reason = null;
  sess.paused_detail = undefined;
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, status: sess.status, paused_reason: null, limits: sess.limits }, sess.id);
  // queued lane 을 큐 순서대로 dispatch(E5-05)
  for (const t of [...s.tasks.values()].filter((x) => x.session_id === sess.id && x.status === "queued")) {
    const agent = s.agents.get(t.agent_id);
    if (agent) simulateRun(s, sess, t, `재개했습니다. ${agent.name}가 이어서 진행합니다.`);
  }
  return ok(sessionFor(s, sess, requireMember(s, req, sess.workspace_id).user.id));
});

// ── 런타임 후보(S6 4단계 · S17) · 저장소 검증(S6 3단계) ──
on("GET", "/workspaces/{id}/runtime-candidates", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  const isolation = req.query.get("isolation");
  if (!isolation) throw new Problem(422, "validation failed", "validation_failed", "isolation 이 필요합니다", { errors: [{ field: "isolation", message: "required" }] });
  const remoteUrl = req.query.get("remote_url");
  const candidates = [...s.runtimes.values()]
    .filter((r) => r.workspace_id === p.id)
    .map((r) => {
      if (r.status !== "online") return { runtime: r, eligible: false, reason: "오프라인" };
      if (isolation === "worktree") {
        // 경로 문자열이 아니라 remote URL 로 판정한다(FR-9.2 F)
        const repo = r.repos.find((x) => !remoteUrl || x.remote_url === remoteUrl);
        if (!repo) return { runtime: r, eligible: false, reason: remoteUrl ? "같은 remote URL 의 저장소가 없음" : "저장소가 없음" };
        return { runtime: r, eligible: true, reason: null, matched_repo: repo };
      }
      return { runtime: r, eligible: true, reason: null };
    });
  return ok({ auto_select_allowed: isolation === "none", candidates });
});
on("POST", "/runtimes/{id}/repo-checks", (req, p) => {
  const s = store();
  const rt = s.runtimes.get(p.id);
  if (!rt) throw new Problem(404, "not found", "not_found");
  requireMember(s, req, rt.workspace_id);
  if (rt.status !== "online") throw new Problem(409, "conflict", "runtime_offline", "런타임이 오프라인입니다");
  const b = body<{ repo_path?: string }>(req);
  const path = b.repo_path ?? "";
  const known = rt.repos.find((r) => r.path === path);
  // "dirty" 를 포함한 경로는 미커밋 변경이 있는 저장소로 흉내 낸다(E13-01 경로 확인용)
  const dirty = path.includes("dirty");
  if (!known && !dirty) {
    return ok({ ok: false, repo_path: path, exists: false, is_git: false, clean: null, default_branch: null, current_branch: null, remote_url: null, tracks_brief_file: null, problems: ["경로가 없습니다"], checked_at: now() });
  }
  return ok({
    ok: !dirty, repo_path: path, exists: true, is_git: true, clean: !dirty,
    default_branch: "main", current_branch: known?.branch ?? "main", remote_url: known?.remote_url ?? null,
    tracks_brief_file: false, problems: dirty ? ["미커밋 변경 3개 — 커밋하거나 stash 후 다시 시도하세요"] : [],
    checked_at: now(),
  });
});

// ── S9·S10 에이전트 · 프로파일 · 팀 템플릿 ──
/** 옵션은 런타임이 광고한 범위 안이어야 한다(§8.2.6). 광고가 없으면 그 키는 쓸 수 없다 — 추측하지 않는다. */
function optionErrors(s: Store, workspaceId: string, kind: string | undefined, options: Record<string, unknown> | undefined): { field: string; message: string }[] {
  if (!kind || !options) return [];
  const adv = runtimeOptionRanges(s, workspaceId).get(kind) ?? {};
  const out: { field: string; message: string }[] = [];
  for (const [k, v] of Object.entries(options)) {
    const range = adv[k];
    if (!range) out.push({ field: `options.${k}`, message: `${kind} 는 ${k} 의 지원 범위를 광고하지 않습니다` });
    else if (!range.includes(String(v))) out.push({ field: `options.${k}`, message: `${k} 는 ${range.join(" · ")} 중 하나여야 합니다` });
  }
  return out;
}

function agentOf(s: Store, req: Req, agentId: string): Agent {
  const a = s.agents.get(agentId);
  if (!a) throw new Problem(404, "not found", "not_found");
  requireMember(s, req, a.workspace_id);
  return a;
}
/** 계약 SSE 에는 `agent.updated` 가 없다 — S9·S10 은 변경 후 다시 읽는다. 여기서는 갱신 시각만 올린다. */
function touchAgent(_s: Store, a: Agent) {
  a.updated_at = now();
}
on("GET", "/agents/{id}", (req, p) => ok(agentOf(store(), req, p.id)));
on("PATCH", "/agents/{id}", (req, p) => {
  const s = store();
  const a = agentOf(s, req, p.id);
  const b = body<Partial<Agent>>(req);
  if (b.name !== undefined) {
    const dup = [...s.agents.values()].find((x) => x.workspace_id === a.workspace_id && x.id !== a.id && x.name === b.name);
    if (dup) throw new Problem(409, "conflict", "name_taken", "워크스페이스 안에서 이름이 유일해야 합니다");
    a.name = b.name;
  }
  for (const k of ["role", "role_description", "instructions", "tools", "max_concurrent_tasks", "respond_to", "respond_to_allowlist", "budget_per_task"] as const) {
    if (b[k] !== undefined) (a as Record<string, unknown>)[k] = b[k];
  }
  // 킬 스위치(FR-1.9) — respond_to: nobody 는 실행 중 턴을 취소하고 대기 중 task 를 취소한다. 열린 HITL 은 남는다.
  if (b.respond_to === "nobody") {
    for (const t of s.tasks.values()) {
      if (t.agent_id !== a.id || t.status === "completed" || t.status === "cancelled" || t.status === "failed") continue;
      t.status = "cancelled";
      t.failure_kind = "cancelled";
      t.finished_at = now();
      const sess = s.sessions.get(t.session_id);
      if (sess) {
        setLaneStatus(s, sess, t.lane_id, { status: "failed", failure_kind: "cancelled", current_activity: null, finished_at: now() });
        emitParticipant(s, sess, a.id, "정지됨(respond_to: nobody)");
      }
    }
  }
  a.status = b.respond_to === "nobody" ? "disabled" : a.respond_to === "nobody" ? "disabled" : a.status;
  if (a.respond_to !== "nobody" && a.status === "disabled") a.status = "idle";
  touchAgent(s, a);
  return ok(a);
});
/** 보관 — 물리 삭제가 아니라 `archived_at`(세션 이력이 참조한다). 진행 중 lane 이 있으면 409. */
on("DELETE", "/agents/{id}", (req, p) => {
  const s = store();
  const a = agentOf(s, req, p.id);
  const busy = [...s.lanes.values()].some((l) => l.agent_id === a.id && (l.status === "running" || l.status === "queued"));
  if (busy) throw new Problem(409, "conflict", "agent_busy", "진행 중 lane 이 있습니다 — 먼저 정지시키거나 lane 을 끝내세요");
  a.archived_at = now();
  touchAgent(s, a);
  return { status: 204 };
});
/** 프로파일 추가(FR-1.6) — `model` 은 워크스페이스 런타임이 probe 로 광고한 범위 안이어야 한다(§8.2.6). */
on("POST", "/agents/{id}/profiles", (req, p) => {
  const s = store();
  const a = agentOf(s, req, p.id);
  const b = body<{ name?: string; runtime_kind?: AgentProfile["runtime_kind"]; model?: string; options?: Record<string, unknown>; env?: Record<string, string>; args?: string[]; is_default?: boolean; fallback_profile_id?: string | null }>(req);
  const errors: { field: string; message: string }[] = [];
  if (!b.name?.trim()) errors.push({ field: "name", message: "이름을 입력하세요" });
  if (!b.runtime_kind) errors.push({ field: "runtime_kind", message: "런타임 종류를 고르세요" });
  if (!b.model?.trim()) errors.push({ field: "model", message: "모델을 고르세요" });
  if (a.profiles.some((x) => x.name === b.name)) errors.push({ field: "name", message: "에이전트 안에서 이름이 유일해야 합니다" });
  const models = runtimeModels(s, a.workspace_id);
  if (b.runtime_kind && b.model && !(models.get(b.runtime_kind) ?? []).includes(b.model)) {
    errors.push({ field: "model", message: `이 워크스페이스의 런타임이 광고하지 않은 모델입니다(${b.runtime_kind})` });
  }
  if (b.fallback_profile_id && !a.profiles.some((x) => x.id === b.fallback_profile_id)) errors.push({ field: "fallback_profile_id", message: "같은 에이전트의 다른 프로파일이어야 합니다" });
  errors.push(...optionErrors(s, a.workspace_id, b.runtime_kind, b.options));
  if (errors.length) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors });
  const t = now();
  const prof: AgentProfile = {
    id: uuid(), agent_id: a.id, name: b.name!.trim(), runtime_kind: b.runtime_kind!, model: b.model!,
    options: b.options ?? {}, env: b.env ?? {}, args: b.args ?? [], is_default: !!b.is_default,
    fallback_profile_id: b.fallback_profile_id ?? null, created_at: t, updated_at: t,
  };
  if (prof.is_default) for (const x of a.profiles) x.is_default = false;
  a.profiles.push(prof);
  touchAgent(s, a);
  return ok(prof, 201);
});
on("PATCH", "/agents/{id}/profiles/{pid}", (req, p) => {
  const s = store();
  const a = agentOf(s, req, p.id);
  const prof = a.profiles.find((x) => x.id === p.pid);
  if (!prof) throw new Problem(404, "not found", "not_found");
  const b = body<Partial<AgentProfile>>(req);
  if (b.fallback_profile_id !== undefined && b.fallback_profile_id !== null) {
    if (b.fallback_profile_id === prof.id || !a.profiles.some((x) => x.id === b.fallback_profile_id)) {
      throw new Problem(422, "validation failed", "validation_failed", "폴백은 같은 에이전트의 다른 프로파일이어야 합니다", { errors: [{ field: "fallback_profile_id", message: "다른 프로파일" }] });
    }
  }
  if (b.model && b.runtime_kind !== undefined) {
    const models = runtimeModels(s, a.workspace_id);
    if (!(models.get(b.runtime_kind) ?? []).includes(b.model)) {
      throw new Problem(422, "validation failed", "validation_failed", "런타임이 광고하지 않은 모델입니다", { errors: [{ field: "model", message: "범위 밖" }] });
    }
  }
  const optErrs = optionErrors(s, a.workspace_id, b.runtime_kind ?? prof.runtime_kind, b.options as Record<string, unknown> | undefined);
  if (optErrs.length) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: optErrs });
  for (const k of ["name", "runtime_kind", "model", "options", "env", "args", "fallback_profile_id"] as const) {
    if (b[k] !== undefined) (prof as Record<string, unknown>)[k] = b[k];
  }
  if (b.is_default) for (const x of a.profiles) x.is_default = x.id === prof.id;
  prof.updated_at = now();
  touchAgent(s, a);
  return ok(prof);
});
on("DELETE", "/agents/{id}/profiles/{pid}", (req, p) => {
  const s = store();
  const a = agentOf(s, req, p.id);
  const prof = a.profiles.find((x) => x.id === p.pid);
  if (!prof) throw new Problem(404, "not found", "not_found");
  if (a.profiles.length === 1) throw new Problem(409, "conflict", "last_profile", "마지막 프로파일은 삭제할 수 없습니다");
  if (prof.is_default) throw new Problem(409, "conflict", "default_profile", "먼저 다른 프로파일을 기본으로 지정하세요");
  a.profiles = a.profiles.filter((x) => x.id !== prof.id);
  for (const x of a.profiles) if (x.fallback_profile_id === prof.id) x.fallback_profile_id = null;
  touchAgent(s, a);
  return { status: 204 };
});

/** 템플릿 목록 — 매핑은 요청 시점의 온라인 런타임 능력으로 계산한다(FR-1.4). */
on("GET", "/workspaces/{id}/agent-templates", (req, p) => {
  const s = store();
  requireMember(s, req, p.id);
  const models = runtimeModels(s, p.id);
  const out: AgentTemplate[] = TEMPLATES.map((t) => ({
    key: t.key,
    name: t.name,
    description: t.description,
    version: t.version,
    agents: t.agents.map((a) => {
      const ms = models.get(a.prefer) ?? [];
      // 선호 런타임이 없으면 감지된 다른 런타임으로 떨어진다 — 매핑 실패도 등록은 한다
      const fallbackKind = [...models.keys()][0] as AgentProfile["runtime_kind"] | undefined;
      if (ms.length) return { key: a.key, name: a.name, role: a.role, role_description: a.role_description, mapping: { status: "mapped" as const, runtime_kind: a.prefer, model: ms[0], reason: null } };
      if (fallbackKind) {
        return { key: a.key, name: a.name, role: a.role, role_description: a.role_description, mapping: { status: "mapped" as const, runtime_kind: fallbackKind, model: (models.get(fallbackKind) ?? [])[0], reason: `${a.prefer} 가 없어 ${fallbackKind} 로 매핑했습니다` } };
      }
      return { key: a.key, name: a.name, role: a.role, role_description: a.role_description, mapping: { status: "unmapped" as const, reason: "감지된 런타임이 없습니다 — 먼저 컴퓨터를 연결하세요" } };
    }),
  }));
  return ok(out);
});
on("POST", "/workspaces/{id}/agent-templates/{key}/apply", (req, p) => {
  const s = store();
  const { user } = requireMember(s, req, p.id);
  const tpl = TEMPLATES.find((t) => t.key === p.key);
  if (!tpl) throw new Problem(404, "not found", "not_found", "그런 템플릿이 없습니다");
  const b = body<{ runtime_id?: string | null; name_overrides?: Record<string, string> }>(req);
  const models = runtimeModels(s, p.id, b.runtime_id ?? null);
  const fallbackKind = [...models.keys()][0] as AgentProfile["runtime_kind"] | undefined;
  const agents: Agent[] = [];
  const unmapped: { agent_id: string; reason: string }[] = [];
  for (const seed of tpl.agents) {
    let name = b.name_overrides?.[seed.key] ?? seed.name;
    // 이름 충돌은 접미사로 피한다(계약: name_overrides 로 피할 수 있게 하되 막지는 않는다)
    let n = 2;
    while ([...s.agents.values()].some((x) => x.workspace_id === p.id && x.name === name)) name = `${b.name_overrides?.[seed.key] ?? seed.name} ${n++}`;
    const kind = (models.has(seed.prefer) ? seed.prefer : fallbackKind) as AgentProfile["runtime_kind"] | undefined;
    const model = kind ? (models.get(kind) ?? [])[0] : undefined;
    const a = makeAgent(p.id, user.id, name, seed.role, seed.role_description, kind && model ? { runtime_kind: kind, model } : undefined);
    a.instructions = seed.instructions;
    a.definition_source = tpl.key;
    a.definition_version = tpl.version;
    s.agents.set(a.id, a);
    agents.push(a);
    if (!kind || !model) unmapped.push({ agent_id: a.id, reason: "감지된 런타임이 없어 프로파일을 매핑하지 못했습니다 — 컴퓨터를 연결한 뒤 S10 에서 지정하세요" });
  }
  return ok({ agents, unmapped }, 201);
});

/**
 * dev·테스트용 — lane 7상태를 한 번에 만든다(계약 밖 경로, `__mock` 접두).
 * `statuses`·`agent_id` 로 **한 에이전트에 한 상태만** 만들 수도 있다 — lane 해소 규칙(W-5)처럼
 * "이 에이전트에게 done lane 하나뿐" 이라는 전제가 필요한 검증이 있기 때문이다.
 */
on("POST", "/__mock/sessions/{id}/seed-lanes", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const b = body<{ statuses?: Lane["status"][]; agent_id?: string }>(req);
  const agents = b.agent_id ? [b.agent_id] : (sess.participants ?? []).map((x) => x.agent_id);
  const states: Lane["status"][] = b.statuses?.length ? b.statuses : ["queued", "running", "waiting_human", "blocked", "paused", "done", "failed"];
  const made: Lane[] = [];
  states.forEach((st, i) => {
    const agentId = agents[i % Math.max(1, agents.length)] ?? uuid();
    const lane = createLane(s, sess, agentId, `${st} 상태 예시 lane`);
    const patch: Partial<Lane> = { status: st };
    if (st === "queued") patch.queue_position = 2;
    if (st === "running") patch.current_activity = "src/app.ts 를 편집하는 중…";
    if (st === "waiting_human") patch.waiting_for = "Director 승인 대기";
    if (st === "blocked") { patch.waiting_for = "Lead"; patch.blocked_note = "국내만인가요, 글로벌 포함인가요?"; patch.blocked_message_id = uuid(); }
    if (st === "paused") patch.paused_over_usd = 1.4;
    if (st === "done") { patch.finished_at = now(); patch.brief = "경쟁사 5곳 정리 완료"; }
    if (st === "failed") { patch.failure_kind = "timeout"; patch.finished_at = now(); }
    if (st === "done" || st === "failed") patch.reentry_count = 1;
    setLaneStatus(s, sess, lane.id, patch);
    made.push(s.lanes.get(lane.id)!);
  });
  return ok(made, 201);
});

// ═══════════════════════════════════════════════════════════════════════════
// P3 — HITL(FR-5.1·5.2·5.4) · 인박스(FR-8) · 세션 종료/취소/Director/참여자
//
// **수치는 골든 표에서 왔다**(§0-9 (b) — 목이 통과시키는 값은 계약·EVAL 원문과 기계 대조한다):
//   · 기한 기본 24h            golden `dueIn = 24 * time.Hour` (FR-5.4)
//   · deputy 는 기한의 **절반** 후  golden E7-09/E7-10 (`CanRespondFrom == dueIn/2`)
//   · 일반 멤버는 `can_respond_from` **null**  golden E7-11 ("a plain member never becomes eligible")
//   · 두 번째 응답은 오류가 아니라 `ignored: true` + 첫 답 유지  golden E7-08
//   · 예산 승인은 **task 범위**($3) 이고 에이전트 `budget_per_task`($1)는 불변  golden E9-02
//   · 예산 거절은 `failed`·`cancelled` 가 아니라 `paused(budget)` 유지  golden E9-03
//   · 취소는 deputy 즉시(`AvailableFrom == 0`), 일반 멤버 403  golden E10-05·E10-06
// 이 상수를 바꾸면 `lib/mock/p3-golden.test.ts` 가 깨진다 — 그것이 이 표의 자물쇠다.
// ═══════════════════════════════════════════════════════════════════════════

/** FR-5.4 기본 기한. golden `dueIn`. */
export const HITL_DUE_IN_MS = 24 * 3600_000;
/** deputy 위임 시점 = 기한의 절반. golden E7-09 `CanRespondFrom = dueIn/2`. */
export const DEPUTY_HALF_MS = HITL_DUE_IN_MS / 2;

/** 호출자의 세션 역할(계약 `Session.my_role`). 워크스페이스 역할이 아니다(SCREEN §2.3). */
function roleOf(sess: Session, userId: string): Session["my_role"] {
  if (sess.director_user_id === userId) return "director";
  if (sess.deputy_director_user_id === userId) return "deputy";
  return "member";
}

/**
 * `approver_spec` 판정(FR-5.4 M7) — golden `hitl.Authorize` 와 같은 순서다.
 * `director` 는 "Director, **그리고 기한 절반 경과 후 deputy**" 이지 "Director 만" 이 아니다.
 * 권한이 영영 없는 사람에게는 `from` 을 비운다(E7-11).
 */
function authorizeHitl(
  h: HitlRequest,
  sess: Session,
  userId: string,
  nowMs = Date.now(),
): { allowed: boolean; from: string | null } {
  const half = new Date(Date.parse(h.created_at) + DEPUTY_HALF_MS).toISOString();
  if (h.approver_spec === "any_member") return { allowed: true, from: null };
  if (h.approver_spec === "director") {
    if (sess.director_user_id === userId) return { allowed: true, from: null };
    if (sess.deputy_director_user_id === userId) {
      return nowMs - Date.parse(h.created_at) >= DEPUTY_HALF_MS
        ? { allowed: true, from: null }
        : { allowed: false, from: half };
    }
    return { allowed: false, from: null };
  }
  return { allowed: h.approver_spec === userId, from: null };
}

/** 호출자 시점의 HITL 카드 — `can_respond`·`can_respond_from`·`overdue` 는 볼 때마다 계산한다. */
function hitlFor(s: Store, h: HitlRequest, userId: string): HitlRequest {
  const sess = s.sessions.get(h.session_id)!;
  const az = authorizeHitl(h, sess, userId);
  return { ...h, overdue: h.status === "open" && Date.parse(h.due_at) <= Date.now(), can_respond: h.status === "open" && az.allowed, can_respond_from: az.from };
}

/** 인박스 항목 한 줄. 소유자별로 만든다 — 남의 항목이 섞이면 결함이 아니라 유출이다. */
function addInboxItem(
  s: Store,
  userId: string,
  item: Omit<InboxItem, "id" | "created_at" | "read_at"> & { read_at?: string | null },
): InboxItem {
  const it: InboxItem & { user_id: string } = {
    ...item, id: uuid(), created_at: now(), read_at: item.read_at ?? null, user_id: userId,
  };
  s.inbox.set(it.id, it);
  emit(s, it.workspace_id, "inbox.item_created", stripInbox(it), it.session_id ?? null);
  emitInboxSummary(s, userId, it.workspace_id);
  return stripInbox(it);
}
const stripInbox = (it: InboxItem & { user_id: string }): InboxItem => {
  const { user_id: _u, ...rest } = it;
  return rest;
};

/** SCREEN §4.6 정렬: overdue → action_required → attention → info. 서버 `inbox.SortRank` 와 같은 값. */
export function inboxSortRank(severity: InboxItem["severity"], overdue: boolean): number {
  if (overdue) return 0;
  if (severity === "action_required") return 1;
  if (severity === "attention") return 2;
  return 3;
}

/** 심각도(FR-8 · 서버 `inbox.Severity` 와 같은 분류). */
export function inboxSeverity(type: InboxItem["type"]): InboxItem["severity"] {
  switch (type) {
    case "hitl_request":
    case "lane_blocked":
    case "session_paused":
      return "action_required";
    case "run_failed":
    case "runtime_offline":
      return "attention";
    default:
      return "info";
  }
}

/**
 * 인라인 동작(계약 `InboxItem.actions`) — **권한을 반영한다**. 403 을 내는 버튼은 없는 버튼보다 나쁘다.
 * 서버 `inbox.Actions` 와 같은 표다.
 */
export function inboxActions(type: InboxItem["type"], hitlType: string | undefined, canRespond: boolean): NonNullable<InboxItem["actions"]> {
  switch (type) {
    case "hitl_request":
      if (!canRespond) return ["open_session"];
      return hitlType === "approval" ? ["approve", "reject", "open_session"] : ["answer", "open_session"];
    case "lane_blocked":
    case "mention":
      return ["reply", "open_session"];
    case "session_paused":
      return canRespond ? ["approve_continue", "open_session"] : ["open_session"];
    case "run_failed":
      return canRespond ? ["restart", "open_session"] : ["open_session"];
    case "runtime_offline":
      return ["open_runtimes"];
    default:
      return ["open_session"];
  }
}

/** 호출자 시점의 인박스 항목 — overdue·actions 를 볼 때마다 다시 계산한다. */
function inboxFor(s: Store, it: InboxItem & { user_id: string }): InboxItem {
  const out = stripInbox(it);
  if (it.type === "hitl_request" && it.ref_id) {
    const h = s.hitls.get(it.ref_id);
    if (h) {
      const view = hitlFor(s, h, it.user_id);
      out.overdue = view.overdue;
      out.due_at = h.due_at;
      out.actions = inboxActions("hitl_request", h.type, view.can_respond);
      out.card = { ...out.card, hitl_type: h.type, title: h.question, body: h.context ?? undefined, proposed_default: h.proposed_default, agent_name: h.agent?.name ?? null };
    }
  }
  return out;
}

function inboxSummaryOf(s: Store, userId: string, workspaceId?: string) {
  const mine = [...s.inbox.values()].filter((x) => x.user_id === userId && (!workspaceId || x.workspace_id === workspaceId)).map((x) => inboxFor(s, x));
  return {
    // `info` 는 세지 않는다 — 세면 뱃지가 영구히 켜져 아무 의미가 없다(SCREEN §4.6).
    action_required: mine.filter((x) => x.severity === "action_required").length,
    overdue: mine.filter((x) => x.overdue).length,
    unread: mine.filter((x) => !x.read_at).length,
  };
}
function emitInboxSummary(s: Store, userId: string, workspaceId: string) {
  emit(s, workspaceId, "inbox.summary", inboxSummaryOf(s, userId, workspaceId), null);
}

// ── HITL 요청 ──
on("GET", "/sessions/{id}/hitl-requests", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  const status = req.query.get("status");
  const items = [...s.hitls.values()]
    .filter((h) => h.session_id === sess.id && (!status || h.status === status))
    .map((h) => hitlFor(s, h, user.id))
    .sort((a, b) => a.created_at.localeCompare(b.created_at));
  return ok({ items, next_cursor: null });
});
on("GET", "/hitl-requests/{id}", (req, p) => {
  const s = store();
  const h = s.hitls.get(p.id);
  if (!h) throw new Problem(404, "not found", "not_found");
  const sess = sessionOf(s, req, h.session_id);
  const { user } = requireMember(s, req, sess.workspace_id);
  return ok(hitlFor(s, h, user.id));
});

/**
 * HITL 응답(멱등) — 계약 `respondHitlRequest`.
 * 두 번째 응답은 **오류가 아니라 무시**(`ignored: true`, E7-08). `overdue` 여도 답할 수 있다(E7-15).
 * 거절도 정상 흐름이다 — task 는 `queued` 로 돌아간다(E7-17). 예산 HITL 만 다르다(E9-02·E9-03).
 */
on("POST", "/hitl-requests/{id}/response", (req, p) => {
  const s = store();
  const h = s.hitls.get(p.id);
  if (!h) throw new Problem(404, "not found", "not_found");
  const sess = sessionOf(s, req, h.session_id);
  const { user } = requireMember(s, req, sess.workspace_id);
  if (!req.headers.get("idempotency-key")) {
    throw new Problem(422, "validation failed", "validation_failed", "Idempotency-Key 헤더가 필요합니다", { errors: [{ field: "Idempotency-Key", message: "required" }] });
  }
  const az = authorizeHitl(h, sess, user.id);
  if (!az.allowed) {
    // 화면의 비활성 툴팁이 읽는 칸이다 — 권한이 영영 없으면 비운다(E7-11).
    throw new Problem(403, "forbidden", "deputy_not_yet", az.from ? `기한의 절반이 지나야 응답할 수 있습니다` : "Director·deputy 만 응답할 수 있습니다", { can_respond_from: az.from });
  }
  if (h.status !== "open") return ok({ hitl_request: hitlFor(s, h, user.id), ignored: true, decision_id: null });

  const b = body<{ answer?: string; approved?: boolean; reason?: string; budget_override_usd?: number; time_extension?: string }>(req);
  const task = [...s.tasks.values()].find((t) => t.id === h.task_id);
  const budget = h.purpose === "budget";

  h.status = "answered";
  h.answer = b.answer ?? null;
  h.approved = b.approved ?? null;
  h.answered_by = user.id;
  h.answered_at = now();
  if (budget && b.approved && b.budget_override_usd != null) h.budget_override_usd = b.budget_override_usd;

  // 결정 기록 1건(E7-07). 거절도 결정이다 — 사유가 재개 프롬프트로 간다(E7-17).
  const decision: Decision = {
    id: uuid(), session_id: sess.id, ref_id: h.id,
    summary: h.type === "approval" ? `${b.approved ? "승인" : "거절"} — ${h.question}` : `${h.question} → ${b.answer ?? h.proposed_default ?? ""}`,
    rationale: b.reason ?? null, source: "hitl", auto: false, created_at: now(),
  };
  s.decisions.set(decision.id, decision);
  emit(s, sess.workspace_id, "decision.created", decision, sess.id);

  if (task) {
    if (budget && !b.approved) {
      // E9-03 — 거절은 실패도 취소도 아니다. `paused(budget)` 그대로 두고 사람이 "중단" 을 눌러야 끝난다.
      setLaneStatus(s, sess, task.lane_id, { status: "paused" });
    } else {
      if (budget && b.budget_override_usd != null) task.budget_override = b.budget_override_usd;
      task.status = "queued";
      task.attempt += 1; // 재개는 **새 attempt** 다(FR-5.4, E7-07)
      setLaneStatus(s, sess, task.lane_id, { status: "running", paused_over_usd: null, hitl_request_id: null, waiting_for: null });
      const agent = s.agents.get(task.agent_id);
      if (agent) simulateRun(s, sess, task, `답변을 받았습니다: "${b.answer ?? (b.approved ? "승인" : "거절")}". 이어서 진행합니다.`);
    }
  }

  // 인박스에서 내린다 — 처리됐다는 유일한 신호다(U3 2단계).
  for (const it of [...s.inbox.values()]) {
    if (it.ref_id === h.id) {
      s.inbox.delete(it.id);
      emitInboxSummary(s, it.user_id, it.workspace_id);
    }
  }
  emit(s, sess.workspace_id, "hitl.updated", h, sess.id);
  return ok({ hitl_request: hitlFor(s, h, user.id), ignored: false, decision_id: decision.id, task: task ? toTask(s, task) : undefined });
});

// ── 인박스(S8) ──
on("GET", "/inbox", (req) => {
  const s = store();
  const user = requireUser(s, req);
  const ws = req.query.get("workspace_id");
  const sessionId = req.query.get("session_id");
  const filter = req.query.get("filter") ?? "all";
  const types = req.query.get("type")?.split(",").filter(Boolean);
  const limit = Number(req.query.get("limit") ?? 50);
  let items = [...s.inbox.values()]
    .filter((x) => x.user_id === user.id)
    .filter((x) => !ws || x.workspace_id === ws)
    .filter((x) => !sessionId || x.session_id === sessionId)
    .filter((x) => !types?.length || types.includes(x.type))
    .map((x) => inboxFor(s, x));
  // 읽어도 `action_required` 는 해소되지 않는다 — 그래서 필터가 read_at 이 아니라 severity 다.
  if (filter === "unread") items = items.filter((x) => !x.read_at);
  if (filter === "action_required") items = items.filter((x) => x.severity === "action_required");
  items.sort((a, b) => {
    const r = inboxSortRank(a.severity, a.overdue === true) - inboxSortRank(b.severity, b.overdue === true);
    if (r !== 0) return r;
    if (a.due_at && b.due_at) return a.due_at.localeCompare(b.due_at); // 묶음 안에서 기한 임박 순
    if (a.due_at) return -1;
    if (b.due_at) return 1;
    return b.created_at.localeCompare(a.created_at);
  });
  const has_more = items.length > limit;
  return ok({ items: items.slice(0, limit), next_cursor: null, has_more });
});
on("GET", "/inbox/summary", (req) => {
  const s = store();
  const user = requireUser(s, req);
  return ok(inboxSummaryOf(s, user.id, req.query.get("workspace_id") ?? undefined));
});
on("POST", "/inbox/{id}/read", (req, p) => {
  const s = store();
  const user = requireUser(s, req);
  const it = s.inbox.get(p.id);
  if (!it || it.user_id !== user.id) throw new Problem(404, "not found", "not_found");
  it.read_at = it.read_at ?? now();
  emitInboxSummary(s, user.id, it.workspace_id);
  return ok(inboxFor(s, it));
});
on("POST", "/inbox/read-all", (req) => {
  const s = store();
  const user = requireUser(s, req);
  const ws = req.query.get("workspace_id");
  let updated = 0;
  for (const it of s.inbox.values()) {
    // `action_required` 는 건드리지 않는다(계약 markAllInboxRead).
    if (it.user_id !== user.id || (ws && it.workspace_id !== ws) || it.severity === "action_required" || it.read_at) continue;
    it.read_at = now();
    updated++;
  }
  if (ws) emitInboxSummary(s, user.id, ws);
  return ok({ updated });
});

// ── 세션 종료 · 취소 · Director 교체 · 참여자(S7 상단 액션) ──
function requireDirector(sess: Session, userId: string): void {
  if (roleOf(sess, userId) !== "director") {
    throw new Problem(403, "forbidden", "not_director", "Director 만 할 수 있습니다");
  }
}
on("POST", "/sessions/{id}/complete", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  requireDirector(sess, user.id);
  const b = body<{ confirm?: boolean }>(req);
  const running = [...s.lanes.values()].filter((l) => l.session_id === sess.id && ["queued", "running", "waiting_human", "paused"].includes(l.status));
  if (running.length > 0 && !b.confirm) {
    throw new Problem(409, "conflict", "running_lanes", `진행 중 lane 이 ${running.length}개 있습니다`, { running_lane_count: running.length });
  }
  for (const l of running) setLaneStatus(s, sess, l.id, { status: "failed", failure_kind: "cancelled", finished_at: now() });
  sess.status = "completing";
  sess.updated_at = now();
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, status: sess.status }, sess.id);
  return ok(sessionFor(s, sess, user.id));
});
on("POST", "/sessions/{id}/cancel", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  requireDirector(sess, user.id);
  if (sess.status !== "active" && sess.status !== "paused") throw new Problem(409, "conflict", "invalid_transition", "active·paused 세션만 취소할 수 있습니다");
  for (const l of [...s.lanes.values()].filter((l) => l.session_id === sess.id && l.status !== "done" && l.status !== "failed")) {
    setLaneStatus(s, sess, l.id, { status: "failed", failure_kind: "cancelled", finished_at: now() });
  }
  sess.status = "cancelled";
  sess.paused_reason = null;
  sess.finished_at = now();
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, status: sess.status }, sess.id);
  return ok(sessionFor(s, sess, user.id));
});
on("PUT", "/sessions/{id}/director", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  requireDirector(sess, user.id);
  const b = body<{ director_user_id?: string }>(req);
  const next = b.director_user_id ? s.users.get(b.director_user_id) : undefined;
  if (!next || !s.members.some((m) => m.workspace_id === sess.workspace_id && m.user.id === next.id)) {
    throw new Problem(422, "validation failed", "validation_failed", "새 Director 는 워크스페이스 멤버여야 합니다", { errors: [{ field: "director_user_id", message: "멤버가 아닙니다" }] });
  }
  sess.director_user_id = next.id;
  sess.director = stripUser(next);
  // 열린 HITL 의 `director` 승인자가 새 Director 로 따라간다(계약 changeDirector) — approver_spec 은 그대로다.
  addMessage(s, sess, { author_type: "system", author_id: null, author: undefined, kind: "system", content: `Director 가 ${next.display_name} 으로 바뀌었습니다.`, mentions: [] });
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, director_user_id: sess.director_user_id }, sess.id);
  return ok(sessionFor(s, sess, user.id));
});
on("GET", "/sessions/{id}/participants", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  return ok(participantsOf(s, sess));
});
on("POST", "/sessions/{id}/participants", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  requireDirector(sess, user.id);
  const b = body<{ agent_id?: string; profile_id?: string | null }>(req);
  const a = b.agent_id ? s.agents.get(b.agent_id) : undefined;
  if (!a || a.workspace_id !== sess.workspace_id) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: [{ field: "agent_id", message: "에이전트를 찾을 수 없습니다" }] });
  if ((sess.participants ?? []).some((x) => x.agent_id === a.id)) throw new Problem(409, "conflict", "already_participant", "이미 참여 중입니다");
  // 초대 권한은 `respond_to` 가 정한다(FR-1.9). `nobody` 면 403 — 킬 스위치가 초대까지 막는다(E10-09).
  if (a.respond_to === "nobody") throw new Problem(403, "forbidden", "not_invitable", `${a.name} 는 정지 상태입니다(respond_to: nobody)`);
  const prof = a.profiles.find((x) => x.id === b.profile_id) ?? a.profiles.find((x) => x.is_default) ?? a.profiles[0];
  const rtKinds = new Set([...s.runtimes.values()].filter((r) => r.workspace_id === sess.workspace_id).flatMap((r) => r.capabilities.map((c) => c.kind)));
  const warnings = rtKinds.has(prof.runtime_kind) ? [] : [`프로파일의 runtime_kind(${prof.runtime_kind}) 가 세션 런타임에 없습니다`];
  const part: Participant = {
    session_id: sess.id, agent_id: a.id,
    agent: { id: a.id, name: a.name, role: a.role, role_description: a.role_description, avatar_url: null, respond_to: a.respond_to },
    profile: prof, status: "idle", status_note: null, is_assignee: false,
    mention_link: `[@${a.name}](mention://agent/${a.id})`, warnings, joined_at: now(),
  };
  sess.participants = [...(sess.participants ?? []), part];
  addMessage(s, sess, { author_type: "system", author_id: null, author: undefined, kind: "system", content: `@${a.name} 가 참여자로 추가되었습니다.`, mentions: [] });
  emit(s, sess.workspace_id, "participant.updated", part, sess.id);
  return ok(part, 201);
});
on("PATCH", "/sessions/{id}/participants/{agentId}", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  requireDirector(sess, user.id);
  const part = (sess.participants ?? []).find((x) => x.agent_id === p.agentId);
  if (!part) throw new Problem(404, "not found", "not_found");
  const b = body<{ profile_id?: string; assignee?: boolean }>(req);
  if (b.profile_id) {
    const a = s.agents.get(part.agent_id);
    const prof = a?.profiles.find((x) => x.id === b.profile_id);
    if (!prof) throw new Problem(422, "validation failed", "validation_failed", undefined, { errors: [{ field: "profile_id", message: "프로파일을 찾을 수 없습니다" }] });
    part.profile = prof;
  }
  if (b.assignee) {
    for (const x of sess.participants ?? []) x.is_assignee = x.agent_id === part.agent_id;
    sess.assignee_agent_id = part.agent_id;
  }
  emit(s, sess.workspace_id, "participant.updated", part, sess.id);
  return ok(part);
});
on("DELETE", "/sessions/{id}/participants/{agentId}", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  requireDirector(sess, user.id);
  const part = (sess.participants ?? []).find((x) => x.agent_id === p.agentId);
  if (!part) throw new Problem(404, "not found", "not_found");
  if (part.is_assignee) throw new Problem(409, "conflict", "assignee_required", "assignee 는 제거할 수 없습니다 — 먼저 다른 assignee 를 지정하세요");
  // 진행 중 lane 4상태가 있으면 409 (계약 removeParticipant).
  const busy = [...s.lanes.values()].filter((l) => l.session_id === sess.id && l.agent_id === part.agent_id && ["queued", "running", "waiting_human", "paused"].includes(l.status));
  if (busy.length > 0) throw new Problem(409, "conflict", "running_lanes", `진행 중 lane 이 ${busy.length}개 있습니다 — 먼저 끝내거나 중단하세요`);
  sess.participants = (sess.participants ?? []).filter((x) => x.agent_id !== part.agent_id);
  addMessage(s, sess, { author_type: "system", author_id: null, author: undefined, kind: "system", content: `@${part.agent.name} 가 참여자에서 제외되었습니다.`, mentions: [] });
  return { status: 204 };
});

// ── dev·테스트용 시드(계약 밖 경로, `__mock` 접두) ──

/**
 * HITL 요청 하나를 만들고 타임라인 카드 + 인박스 항목까지 채운다.
 * `age_ms` 로 발행 시각을 뒤로 밀 수 있다 — deputy 시점 제한(E7-09 11h vs E7-10 12h 1분)을 클럭 없이 재현한다.
 * 에이전트 발행(`source: agent`)은 `pending_hitl` → `turn_end` 에 `waiting_human` 이므로 lane 도 그렇게 둔다(E7-03).
 */
on("POST", "/__mock/sessions/{id}/seed-hitl", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const b = body<{
    type?: HitlRequest["type"]; source?: HitlRequest["source"]; purpose?: HitlRequest["purpose"];
    question?: string; context?: string; proposed_default?: string | null; approver_spec?: string;
    age_ms?: number; status?: HitlRequest["status"]; agent_id?: string; lane_id?: string;
  }>(req);
  const created = new Date(Date.now() - (b.age_ms ?? 0)).toISOString();
  const agent = b.agent_id ? s.agents.get(b.agent_id) : s.agents.get((sess.participants ?? [])[0]?.agent_id ?? "");
  const source = b.source ?? "agent";
  const type = b.type ?? "question";
  const lane = b.lane_id ? s.lanes.get(b.lane_id) : [...s.lanes.values()].find((l) => l.session_id === sess.id && l.agent_id === agent?.id);
  const task = lane ? [...s.tasks.values()].find((t) => t.lane_id === lane.id) : undefined;
  const h: HitlRequest = {
    id: uuid(), session_id: sess.id,
    // system 발행이라도 **예산 초과 HITL 은 task_id 를 채운다**(계약 s-13, E9-01).
    task_id: source === "agent" || b.purpose === "budget" ? (task?.id ?? null) : null,
    lane_id: lane?.id ?? null,
    agent: source === "agent" && agent ? { id: agent.id, name: agent.name } : undefined,
    source, type, purpose: b.purpose ?? (source === "agent" ? "agent" : "user_approval"),
    question: b.question ?? "타깃 독자가 투자자인지 내부 경영진인지 알려주세요",
    context: b.context ?? null,
    options: [],
    proposed_default: b.proposed_default !== undefined ? b.proposed_default : type === "question" || type === "choice" ? "투자자" : null,
    artifact_id: null,
    approver_spec: b.approver_spec ?? "director",
    due_at: new Date(Date.parse(created) + HITL_DUE_IN_MS).toISOString(),
    overdue: false, status: b.status ?? "open", approved: null, answer: null, answered_by: null, answered_at: null,
    budget_override_usd: null, can_respond: false, can_respond_from: null, message_id: null, created_at: created,
  };
  const card = addMessage(s, sess, {
    author_type: source === "agent" ? "agent" : "system", author_id: source === "agent" ? (agent?.id ?? null) : null,
    author: source === "agent" && agent ? { name: agent.name, role: agent.role, avatar_url: null } : undefined,
    kind: "hitl", content: h.question, mentions: [], lane_id: lane?.id ?? null, source_task_id: task?.id ?? null,
  });
  h.message_id = card.id;
  s.hitls.set(h.id, h);
  if (lane && h.status === "open") {
    setLaneStatus(s, sess, lane.id, {
      status: b.purpose === "budget" ? "paused" : "waiting_human",
      waiting_for: b.purpose === "budget" ? null : h.question.slice(0, 40),
      hitl_request_id: h.id,
      ...(b.purpose === "budget" ? { paused_over_usd: 1.4 } : {}),
    });
  }
  if (task && h.status === "open") task.status = b.purpose === "budget" ? "paused" : "waiting_human";
  emit(s, sess.workspace_id, "hitl.created", h, sess.id);
  // 인박스는 **응답 권한이 있는 사람에게만** 만든다 — deputy 는 기한 절반 전이면 아예 보이지 않는다(O5, U9-1).
  for (const m of s.members.filter((m) => m.workspace_id === sess.workspace_id)) {
    const az = authorizeHitl(h, sess, m.user.id);
    if (!az.allowed || h.status !== "open") continue;
    addInboxItem(s, m.user.id, {
      workspace_id: sess.workspace_id, type: "hitl_request", severity: inboxSeverity("hitl_request"),
      session_id: sess.id, session: { id: sess.id, title: sess.title, status: sess.status },
      ref_id: h.id, due_at: h.due_at, overdue: false,
      delegated: sess.deputy_director_user_id === m.user.id,
      card: { title: h.question, body: h.context ?? undefined, agent_name: h.agent?.name ?? null, proposed_default: h.proposed_default, hitl_type: h.type, lane_id: lane?.id ?? null, message_id: card.id },
      actions: inboxActions("hitl_request", h.type, true),
    });
  }
  return ok(h, 201);
});

/** 세션을 사유별로 `paused` 로 만든다 — 배너 5종(SCREEN §4.5 O6)을 한 번에 볼 수 있게. */
on("POST", "/__mock/sessions/{id}/pause", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const b = body<{ reason?: Session["paused_reason"] }>(req);
  const reason = (b.reason ?? "budget") as NonNullable<Session["paused_reason"]>;
  const agentIds = (sess.participants ?? []).slice(0, 2).map((x) => x.agent_id);
  sess.status = "paused";
  sess.paused_reason = reason;
  sess.paused_detail = {
    reason, paused_at: now(),
    ...(reason === "budget" ? { budget: { limit_usd: sess.limits.budget_usd ?? 20, spent_usd: 21.4 } } : {}),
    ...(reason === "time" ? { time: { limit: sess.limits.time_limit ?? "PT4H", elapsed: "PT4H12M" } } : {}),
    ...(reason === "loop" ? { loop: { limit: "pair_roundtrips", count: 5, agents: agentIds } } : {}),
    ...(reason === "runtime_offline" ? { runtime: { runtime_id: sess.runtime_id ?? uuid(), offline_since: new Date(Date.now() - 7 * 864e5).toISOString() } } : {}),
    // `runtime_offline` 은 여기서 재개할 수 없다 — 재바인딩이나 종료다(계약 resumeSession 409).
    resolve_actions: reason === "runtime_offline" ? ["rebind", "cancel"] : ["resume", "cancel"],
    can_resolve_from: null,
  };
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, status: sess.status, paused_reason: reason, paused_detail: sess.paused_detail }, sess.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  addInboxItem(s, user.id, {
    workspace_id: sess.workspace_id, type: "session_paused", severity: inboxSeverity("session_paused"),
    session_id: sess.id, session: { id: sess.id, title: sess.title, status: sess.status },
    ref_id: sess.id, due_at: null, overdue: false, delegated: false,
    card: { title: "세션이 멈췄습니다", body: pausedCardBody(sess), paused_reason: reason },
    actions: inboxActions("session_paused", undefined, roleOf(sess, user.id) === "director"),
  });
  return ok(sessionFor(s, sess, requireMember(s, req, sess.workspace_id).user.id));
});
function pausedCardBody(sess: Session): string {
  const d = sess.paused_detail;
  if (!d) return "사유 정보 없음";
  switch (d.reason) {
    case "budget":
      return `예산 초과 — $${d.budget?.spent_usd ?? 0} / $${d.budget?.limit_usd ?? 0}`;
    case "time":
      return `시간 상한 ${d.time?.limit ?? ""} 도달`;
    case "loop":
      return `에이전트 간 왕복이 상한(${d.loop?.limit})에 ${d.loop?.count ?? 0}회 도달`;
    case "runtime_offline":
      return "세션 런타임이 오프라인입니다";
    default:
      return "Director 가 일시정지했습니다";
  }
}

/**
 * 호출자의 **세션 역할**을 바꾼다(S7-P·S7-D 변형 확인용). Director·deputy 자리를 다른 멤버에게 넘겨
 * 지금 사용자가 deputy 또는 일반 멤버가 되게 한다.
 */
on("POST", "/__mock/sessions/{id}/role", (req, p) => {
  const s = store();
  const sess = sessionOf(s, req, p.id);
  const { user } = requireMember(s, req, sess.workspace_id);
  const b = body<{ role?: Session["my_role"] }>(req);
  const other = s.members.find((m) => m.workspace_id === sess.workspace_id && m.user.id !== user.id);
  if (!other) throw new Problem(409, "conflict", "no_other_member", "다른 멤버가 없습니다");
  switch (b.role ?? "director") {
    case "director":
      sess.director_user_id = user.id;
      sess.director = stripUser(s.users.get(user.id)!);
      sess.deputy_director_user_id = other.user.id;
      sess.deputy_director = other.user;
      break;
    case "deputy":
      sess.director_user_id = other.user.id;
      sess.director = other.user;
      sess.deputy_director_user_id = user.id;
      sess.deputy_director = stripUser(s.users.get(user.id)!);
      break;
    default:
      sess.director_user_id = other.user.id;
      sess.director = other.user;
      sess.deputy_director_user_id = null;
      sess.deputy_director = undefined;
  }
  emit(s, sess.workspace_id, "session.updated", { id: sess.id, director_user_id: sess.director_user_id }, sess.id);
  return ok(sessionFor(s, sess, user.id));
});

/** 인박스 7종을 한 번에 만든다 — S8 목록 전체(심각도 3종 · 정렬 · 버튼)를 한 화면에서 본다. */
on("POST", "/__mock/inbox/seed", (req) => {
  const s = store();
  const user = requireUser(s, req);
  const sess = [...s.sessions.values()].find((x) => s.members.some((m) => m.workspace_id === x.workspace_id && m.user.id === user.id));
  if (!sess) throw new Problem(409, "conflict", "no_session", "세션이 없습니다");
  const ref = { id: sess.id, title: sess.title, status: sess.status };
  const lane = [...s.lanes.values()].find((l) => l.session_id === sess.id);
  const made: InboxItem[] = [];
  const add = (type: InboxItem["type"], card: InboxItem["card"], extra: Partial<InboxItem> = {}) =>
    made.push(addInboxItem(s, user.id, {
      workspace_id: sess.workspace_id, type, severity: inboxSeverity(type), session_id: sess.id, session: ref,
      ref_id: lane?.id ?? sess.id, due_at: null, overdue: false, delegated: false, card,
      actions: inboxActions(type, undefined, true), ...extra,
    }));
  add("lane_blocked", { title: "Researcher: '국내만인가요, 글로벌 포함인가요?'", body: "위임자가 없는 lane 입니다 — 답글이 곧 지시가 됩니다.", agent_name: "Researcher", lane_id: lane?.id ?? null });
  add("session_paused", { title: "세션이 멈췄습니다", body: "예산 초과 — $21.40 / $20", paused_reason: "budget" });
  add("run_failed", { title: "작업이 실패했습니다", body: "자동 재시도가 소진되었습니다", failure_kind: "timeout", lane_id: lane?.id ?? null });
  add("runtime_offline", { title: "MacBook 이 오프라인입니다", body: "7일 유예 중 5일 남음", runtime_name: "demo-macbook", grace_ends_at: new Date(Date.now() + 5 * 864e5).toISOString() });
  add("mention", { title: "민지님을 멘션했습니다", body: "@민지 이 부분 확인 부탁드립니다", agent_name: "Lead" });
  add("session_completed", { title: "세션이 완료되었습니다", body: "보고서 1건 제출, 승인됨", summary: "결정 3건 · 아티팩트 1건 · $1.20" }, { read_at: now() });
  return ok(made, 201);
});
