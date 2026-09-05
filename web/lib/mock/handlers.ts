/**
 * 목 API 핸들러 — openapi P1 범위. 라우터는 FR-3.3 규칙 2(명시 멘션 → task, 비참여자 경고)·6(그 외 → assignee)만.
 * 에이전트 실행은 타이머로 흉내 낸다(task_event 원본 레일·typing·delta·답글 메시지·참여자 상태).
 */
import type { Agent, Member, Message, Pairing, Participant, Session, SessionListItem, TaskEvent, User } from "@/lib/api/types";
import { emit, makeAgent, makeRuntime, now, participantStatus, resetStore, sseFrame, store, stripUser, uuid, type MockTask, type Store, type Subscriber } from "./store";

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
  const origin = req.headers.get("origin") ?? `http://${req.headers.get("host") ?? "localhost:3000"}`;
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
  return ok({ ...sess, participants: participantsOf(s, sess), my_role: sess.director_user_id === user.id ? "director" : "member" });
});

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
function createTask(s: Store, sess: Session, agentId: string, triggerId: string | null): MockTask {
  const t: MockTask = { id: uuid(), session_id: sess.id, agent_id: agentId, lane_id: uuid(), status: "queued", attempt: 1, trigger_message_id: triggerId, created_at: now() };
  s.tasks.set(t.id, t);
  s.taskEvents.set(t.id, []);
  return t;
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
    emitParticipant(s, sess, agent.id, "lane #1 실행 중");
    pushEvent(s, sess, task, { class: "runtime", verb: "start", object_ref: { runtime_kind: "claude_code", session_id: `acp-${task.id.slice(0, 8)}` }, outcome: "cold_start", sentence: `${agent.name}가 세션을 시작했다 → cold_start` });
    emit(s, sess.workspace_id, "agent.typing", { session_id: sess.id, agent_id: agent.id, typing: true }, sess.id, true);
  });
  let thinking: TaskEvent | undefined;
  at(700, () => {
    thinking = pushEvent(s, sess, task, { class: "message", verb: "think", object_ref: { kind: "thought" }, outcome: "started", sentence: `${agent.name}가 생각하는 중…` });
  });
  at(1200, () => {
    if (thinking) supersede(s, sess, task, thinking, { class: "message", verb: "think", object_ref: { kind: "thought", chars: 412 }, outcome: "ok", sentence: `${agent.name}가 계획을 생각했다 → ok` });
    pushEvent(s, sess, task, { class: "tool", verb: "read", object_ref: { path: "README.md" }, outcome: "ok", tool: "Read", sentence: `${agent.name}가 README.md 를 읽었다 → ok` });
  });
  const chunks = reply.match(/.{1,12}/gs) ?? [reply];
  chunks.forEach((c, i) => at(1500 + i * 60, () => emit(s, sess.workspace_id, "message.delta", { session_id: sess.id, task_id: task.id, agent_id: agent.id, text: c }, sess.id, true)));
  at(1500 + chunks.length * 60 + 100, () => {
    emit(s, sess.workspace_id, "agent.typing", { session_id: sess.id, agent_id: agent.id, typing: false }, sess.id, true);
    const msg = addMessage(s, sess, { author_type: "agent", author_id: agent.id, author: { name: agent.name, avatar_url: null, role: agent.role }, kind: "text", content: reply, mentions: [], source_task_id: task.id, lane_id: task.lane_id });
    pushEvent(s, sess, task, { class: "status", verb: "post_message", object_ref: { message_id: msg.id }, outcome: "ok", sentence: `${agent.name}가 메시지를 게시했다 → ok` });
    pushEvent(s, sess, task, { class: "usage", verb: "report", object_ref: null, outcome: "report", usage: { input_tokens: 1200, output_tokens: 180, cost_usd: 0.02 } });
    pushEvent(s, sess, task, { class: "runtime", verb: "turn_end", object_ref: null, outcome: "ok", sentence: `턴 종료 → ok` });
    task.status = "completed";
    sess.cost_usd = Math.round((sess.cost_usd + 0.02) * 100) / 100;
    emit(s, sess.workspace_id, "cost.updated", { session_id: sess.id, cost_usd: sess.cost_usd, estimated: false }, sess.id);
    emitParticipant(s, sess, agent.id, null);
  });
}

const MENTION_RE = /\[@([^\]]+)\]\(mention:\/\/(agent|user)\/([0-9a-zA-Z-]+)\)|\[@all\]\(mention:\/\/all\)/g;

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
  const mentions: Message["mentions"] = [];
  for (const m of b.content.matchAll(MENTION_RE)) {
    if (m[2]) mentions.push({ kind: m[2] as "agent" | "user", id: m[3], display_name: m[1] });
    else mentions.push({ kind: "all", id: "all", display_name: "all" });
  }
  const isNote = b.content.startsWith("/note ");
  const msg = addMessage(s, sess, { author_type: "user", author_id: user.id, author: { name: user.display_name, avatar_url: null }, kind: "text", content: b.content, mentions, parent_id: parentId, is_note: isNote });
  const warnings: { code: string; message: string; agent_id: string | null }[] = [];
  const triggers: { agent_id: string; task_id: string; lane_id: string; coalesced: boolean; deferred_until: null }[] = [];
  const parts = new Set((sess.participants ?? []).map((x) => x.agent_id));
  const suppress = new Set(b.suppress_agent_ids ?? []);
  const targets: string[] = [];
  const agentMentions = mentions.filter((m) => m.kind === "agent");
  if (!isNote) {
    for (const m of agentMentions) {
      const a = s.agents.get(m.id);
      if (!a || a.workspace_id !== sess.workspace_id) { warnings.push({ code: "unknown_agent", message: `@${m.display_name} 를 찾을 수 없음`, agent_id: null }); continue; }
      if (!parts.has(a.id)) { warnings.push({ code: "not_participant", message: `${a.name}는 이 세션 참여자가 아님`, agent_id: a.id }); continue; } // E1-04
      if (suppress.has(a.id)) { warnings.push({ code: "suppressed", message: `@${a.name} 트리거 억제됨(FR-3.6)`, agent_id: a.id }); continue; }
      targets.push(a.id); // 규칙 2
    }
    const onlyUsersOrAll = agentMentions.length === 0 && mentions.length > 0;
    if (agentMentions.length === 0 && !onlyUsersOrAll && sess.assignee_agent_id && !parentId) targets.push(sess.assignee_agent_id); // 규칙 6
  }
  for (const agentId of new Set(targets)) {
    const queued = [...s.tasks.values()].find((t) => t.session_id === sess.id && t.agent_id === agentId && t.status === "queued");
    if (queued) { triggers.push({ agent_id: agentId, task_id: queued.id, lane_id: queued.lane_id, coalesced: true, deferred_until: null }); continue; }
    const task = createTask(s, sess, agentId, msg.id);
    triggers.push({ agent_id: agentId, task_id: task.id, lane_id: task.lane_id, coalesced: false, deferred_until: null });
    const agent = s.agents.get(agentId)!;
    const plain = b.content.replace(MENTION_RE, "@$1").trim();
    simulateRun(s, sess, task, `안녕하세요, ${agent.name}입니다. "${plain.slice(0, 80)}" 잘 받았습니다. 바로 진행하겠습니다.`);
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
