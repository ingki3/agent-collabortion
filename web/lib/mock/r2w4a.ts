/**
 * 목 op — T-R2-W4a: S8 받은 요청 v0.19 · 안 읽음 · S15 활동 로그 · 알림 구독 3층 · S9/S11 방 칸(계약 0.2.9 #308).
 *
 * **handlers.ts 에는 등록 한 줄만 둔다**(`registerR2W4a(…)`) — 같은 시기에 다른 웹 워커가 handlers.ts 를 고친다(T-R2-W3 과 같은 방식).
 * 옛 op 을 덮을 때는 **앞에 끼우고**(prepend) 안에서 원래 핸들러를 불러 결과에 이 모듈의 칸만 얹는다(`next`) — 옛 판정·문장을 두 벌 두지 않는다.
 *
 * 서버를 흉내 내는 자리:
 *   · 인박스 항목의 방 칸(`room{id,name}`, 0.2.9)·미션 칸(`session` = 미션 SessionRef, handlers_inbox.go `wk.title`)·`recipient_basis`
 *   · 방장 승인 요청(`approver_spec: room_owner` — `room_paused`·`isolation_confirm`)의 응답: 방장 → 기한 절반 뒤 부방장(FR-2A.3),
 *     예산은 새 상한으로 방 멈춤을 풀고(resumeRoomForBudget), 루프는 그냥 풀고, 격리는 approve=worktree · reject=none(AnswerIsolation)
 *   · `listActivityLog`(handlers_activity.go) — owner·admin, 최신순, id 커서, 마스킹이면 자유 글 80자 + `masked: true`
 *   · `setRoomSubscription`·`setWorkSubscription`·`setLaneSubscription` + 읽는 칸(`Room.my_subscription`·`Work.subscription`·`Lane.my_subscription`)
 *   · `Agent.rooms[]`·`room_count`·`hidden_room_count`·`running_task_count` · `Runtime.room_count`
 *
 * 상태는 `Store` 에 붙인 `WeakMap` 에 둔다 — `resetStore()` 가 새 Store 를 만들면 같이 비워진다(store.ts 를 고치지 않는다).
 */
import type { components } from "@/lib/api/schema";
import type { Agent, HitlRequest, InboxItem, Lane, Member, Room, RoomListItem, Runtime, User } from "@/lib/api/types";
import { emit, now, store, stripUser, uuid, type MockRoom, type MockWork, type Store } from "./store";
import type { Req, Res } from "./handlers";
import { R4_MOCK_ONLY, RW4 } from "./r2w4a-wording";
import { VALIDATION_DETAIL } from "./wording";

type S = components["schemas"];
type ActivityLogEntry = S["ActivityLogEntry"];
type RoomSubscriptionLevel = S["RoomSubscriptionLevel"];
type SubscriptionLevel = S["SubscriptionLevel"];
type RecipientBasis = NonNullable<InboxItem["recipient_basis"]>;
type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;
interface Standing { roomRole: Room["my_room_role"]; wsRole: Member["role"] | null; visibility: MockRoom["visibility"]; archived: boolean }

export interface R2W4aCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  routes: { method: string; re: RegExp; keys: string[]; h: Handler }[];
  Problem: ProblemCtor;
  requireUser: (s: Store, req: Req) => User;
  syncRooms: (s: Store) => void;
  standingOf: (s: Store, r: MockRoom, userId: string) => Standing;
  roomDecide: (a: "view", f: Standing) => boolean;
  emitRoom: (s: Store, r: MockRoom) => void;
  addInboxItem: (s: Store, userId: string, item: Omit<InboxItem, "id" | "created_at" | "read_at"> & { read_at?: string | null }) => InboxItem;
  emitInboxSummary: (s: Store, userId: string, workspaceId: string) => void;
  inboxSeverity: (t: InboxItem["type"]) => InboxItem["severity"];
  inboxActions: (t: InboxItem["type"], hitlType: string | undefined, canRespond: boolean) => NonNullable<InboxItem["actions"]>;
  roomWorks: (s: Store, r: { id: string }) => (MockWork & { legacy: boolean })[];
  notFound: (what: string) => string;
  /** `./wording.ts` 의 `SERVER` 에 이미 있는 문장(`W`) — 활동 로그 403 은 s.admin 의 `admin_only`. */
  W: { idempotency_key_required: string; not_approver: string; admin_only: string; room_visibility_enum: string };
  /** `HITL_DUE_IN_MS` — handlers.ts 의 const 는 등록 시점에 아직 초기화 전이라 함수로 받는다. */
  hitlDueMs: () => number;
}

// ── 모듈 상태 ────────────────────────────────────────────────────────────────
interface Extra {
  /** 인박스 항목 id → 받는 근거(서버 inbox_item.recipient_basis). */
  basis: Map<string, RecipientBasis>;
  activity: (ActivityLogEntry & { workspace_id: string; seq: number; actor_id_raw: string | null })[];
  seq: number;
  /** `${room}:${user}` → 방 구독. 없으면 `all`. */
  roomSub: Map<string, RoomSubscriptionLevel>;
  /** `${work}:${user}` → 미션 구독(null 은 방을 따름 = 행 없음). */
  workSub: Map<string, SubscriptionLevel>;
  /** `${lane}:${user}` → 서브 미션 켜기/끄기. 행이 없으면 null(따로 안 정함). */
  laneSub: Map<string, boolean>;
  /** 방 멈춤 요청 id → 방 id(방장 승인으로 풀 방). */
  gate: Map<string, { room_id: string; kind: "budget" | "loop" | "isolation" }>;
}
const EXTRA = new WeakMap<Store, Extra>();
function ex(s: Store): Extra {
  let e = EXTRA.get(s);
  if (!e) {
    e = { basis: new Map(), activity: [], seq: 0, roomSub: new Map(), workSub: new Map(), laneSub: new Map(), gate: new Map() };
    EXTRA.set(s, e);
  }
  return e;
}

const ok = (body: unknown, status = 200): Res => ({ status, body });
/** 서버 `at.Format("15:04")` — UTC 시각. */
const hhmm = (iso: string) => iso.slice(11, 16);
function body<T>(req: Req): T {
  return (req.body ?? {}) as T;
}

/** 서버 `maskActivity` — 자유 글(uuid 아닌 문자열)을 80자로 자르고 `masked: true`. */
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function maskActivity(p: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = { ...p };
  let masked = false;
  for (const [k, v] of Object.entries(out)) {
    if (typeof v !== "string" || UUID_RE.test(v)) continue;
    const r = [...v];
    if (r.length > 80) {
      out[k] = r.slice(0, 80).join("") + "…";
      masked = true;
    }
  }
  if (masked) out.masked = true;
  return out;
}

export function registerR2W4a(ctx: R2W4aCtx): void {
  const { on, routes, Problem } = ctx;
  const mine = new Set<Handler>();
  /** 이 모듈의 라우트를 **맨 앞에** 끼운다 — 옛 op 을 덮는다. */
  const prepend = (method: string, pattern: string, h: Handler) => {
    mine.add(h);
    on(method, pattern, h);
    routes.unshift(routes.pop()!);
  };
  /** 같은 요청을 이 모듈 밖의 첫 라우트로 보낸다(덮은 op 의 원래 핸들러). */
  const next = async (req: Req): Promise<Res> => {
    for (const r of routes) {
      if (mine.has(r.h) || r.method !== req.method) continue;
      const m = req.path.match(r.re);
      if (!m) continue;
      const params: Record<string, string> = {};
      r.keys.forEach((k, i) => (params[k] = decodeURIComponent(m[i + 1])));
      return r.h(req, params);
    }
    return { status: 404 };
  };
  const isOk = (r: Res) => r.status >= 200 && r.status < 300;

  const memberOf = (s: Store, wsId: string, userId: string) => s.members.find((m) => m.workspace_id === wsId && m.user.id === userId) ?? null;
  const roomOf = (s: Store, id: string | null | undefined) => {
    if (!id) return undefined;
    ctx.syncRooms(s);
    return s.rooms.get(id);
  };
  const canView = (s: Store, r: MockRoom, userId: string) => ctx.roomDecide("view", ctx.standingOf(s, r, userId));

  /** 활동 로그 한 줄(서버 `logActivity`). */
  function logActivity(s: Store, wsId: string, roomId: string | null, actor: { kind: "user" | "agent" | "system"; id: string | null }, action: string, objectRef: string, payload: Record<string, unknown>, at = now()) {
    const e = ex(s);
    const u = actor.kind === "user" && actor.id ? s.users.get(actor.id) : undefined;
    const a = actor.kind === "agent" && actor.id ? s.agents.get(actor.id) : undefined;
    const r = roomOf(s, roomId);
    e.seq += 1;
    e.activity.push({
      id: String(e.seq), seq: e.seq, workspace_id: wsId, at, actor_id_raw: actor.id,
      actor: { kind: actor.kind, id: actor.id, name: actor.kind === "system" ? "Colab" : (u?.display_name ?? a?.name ?? "") },
      action, object_ref: objectRef, room: r ? { id: r.id, name: r.name } : null, payload,
    });
  }

  // ── S8 인박스 — 방·미션 칸과 받는 근거 ─────────────────────────────────────
  /**
   * 서버 selectInbox 의 모양: `session` 은 **미션**(항목의 work_id, 없으면 옛 세션 방의 legacy 미션)이고 방 층 항목(`room_paused`·
   * `isolation_confirm`)은 비운다. `room` 은 0.2.9 — 볼 수 있는 방이면 이름.
   */
  function inboxOut(s: Store, it: InboxItem): InboxItem {
    const out: InboxItem = { ...it };
    const r = roomOf(s, it.room_id ?? it.session_id);
    out.room_id = it.room_id ?? it.session_id ?? null;
    out.room = r ? { id: r.id, name: r.name } : null;
    const basis = ex(s).basis.get(it.id);
    out.recipient_basis = basis ?? it.recipient_basis ?? null;
    if (it.type === "room_paused" || it.type === "isolation_confirm") out.session = undefined;
    else if (it.work_id) {
      const w = s.works.get(it.work_id);
      if (w) out.session = { id: w.id, title: w.title, status: w.status as never };
    }
    return out;
  }
  prepend("GET", "/inbox", async (req) => {
    const res = await next(req);
    if (!isOk(res)) return res;
    const s = store();
    const b = res.body as { items: InboxItem[] };
    return { ...res, body: { ...b, items: b.items.map((it) => inboxOut(s, it)) } };
  });

  /**
   * 방장 승인 요청의 권한(FR-2A.3 · 서버 authorizeHitl room_owner): 방장은 바로, 부방장은 기한 절반 뒤, 둘 다 없으면 ws owner 최고참.
   * `from` 은 그 사람이 답할 수 있게 되는 시각(없으면 영영 없음).
   */
  function roomOwnerAuthz(s: Store, h: HitlRequest, r: MockRoom, userId: string): { allowed: boolean; from: string | null; basis: RecipientBasis | null } {
    const half = new Date(Date.parse(h.created_at) + ctx.hitlDueMs() / 2).toISOString();
    const halfPassed = Date.now() >= Date.parse(half);
    if (r.owner_user_id === userId) return { allowed: true, from: null, basis: "room_owner" };
    if (r.deputy_owner_user_id === userId) return { allowed: halfPassed, from: halfPassed ? null : half, basis: "room_deputy" };
    const owners = s.members.filter((m) => m.workspace_id === r.workspace_id && m.role === "owner");
    if (!r.deputy_owner_user_id && owners[0]?.user.id === userId) return { allowed: halfPassed, from: halfPassed ? null : half, basis: "workspace_owner" };
    return { allowed: false, from: null, basis: null };
  }
  const roomOwnerHitl = (s: Store, id: string) => {
    const h = s.hitls.get(id);
    return h && h.approver_spec === "room_owner" ? h : null;
  };
  const view = (s: Store, h: HitlRequest, userId: string): HitlRequest => {
    const r = roomOf(s, h.session_id)!;
    const az = roomOwnerAuthz(s, h, r, userId);
    return { ...h, overdue: h.status === "open" && Date.parse(h.due_at) <= Date.now(), can_respond: h.status === "open" && az.allowed, can_respond_from: az.from };
  };
  prepend("GET", "/hitl-requests/{id}", (req, p) => {
    const s = store();
    const h = roomOwnerHitl(s, p.id);
    if (!h) return next(req);
    const user = ctx.requireUser(s, req);
    return ok(view(s, h, user.id));
  });
  prepend("POST", "/hitl-requests/{id}/response", (req, p) => {
    const s = store();
    const h = roomOwnerHitl(s, p.id);
    if (!h) return next(req);
    const user = ctx.requireUser(s, req);
    const r = roomOf(s, h.session_id);
    if (!r) throw new Problem(404, "not_found", ctx.notFound("hitl_request"));
    if (!req.headers.get("idempotency-key")) {
      throw new Problem(422, "idempotency_key_required", ctx.W.idempotency_key_required, { errors: [{ field: "Idempotency-Key", message: "required" }] });
    }
    const az = roomOwnerAuthz(s, h, r, user.id);
    if (!az.allowed) {
      throw az.from
        ? new Problem(403, "deputy_not_yet", RW4.room_owner_not_yet.replace("%s", hhmm(az.from)), { can_respond_from: az.from })
        : new Problem(403, "not_approver", ctx.W.not_approver, { can_respond_from: null });
    }
    if (h.status !== "open") return ok({ hitl_request: view(s, h, user.id), ignored: true, decision_id: null });
    const b = body<{ answer?: string; approved?: boolean; reason?: string; budget_override_usd?: number }>(req);
    // 서버 validateHitlResponse — 승인 요청은 approved 필수, 거절은 사유 필수. choice 는 답이 선택지 안에.
    if (h.type === "approval") {
      if (b.approved == null) throw new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field: "approved", code: "required", message: RW4.approved_required }] });
      if (!b.approved && !(b.reason ?? "").trim()) throw new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field: "reason", code: "required", message: RW4.reject_reason }] });
    } else if (!(b.answer ?? "").trim()) {
      throw new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field: "answer", code: "required", message: RW4.answer_required }] });
    } else if (h.purpose === "isolation" && !(h.options ?? []).includes(b.answer!.trim())) {
      throw new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field: "answer", code: "not_an_option", message: RW4.not_an_option }] });
    }
    h.status = "answered";
    h.approved = b.approved ?? null;
    h.answer = b.answer ?? null;
    h.answered_by = user.id;
    h.answered_at = now();
    const gate = ex(s).gate.get(h.id);
    if (gate?.kind === "isolation") {
      // AnswerIsolation — approve 는 worktree, reject 는 none 그대로. choice 는 고른 저장소로 worktree.
      if (h.type === "choice") r.isolation = { kind: "worktree", repo_path: b.answer ?? null } as MockRoom["isolation"];
      else if (b.approved) r.isolation = { ...(r.isolation ?? { kind: "none" }), kind: "worktree" } as MockRoom["isolation"];
    } else if (gate && b.approved !== false) {
      if (gate.kind === "budget" && b.budget_override_usd != null) r.limits = { ...r.limits, budget_usd: b.budget_override_usd };
      logActivity(s, r.workspace_id, r.id, { kind: "user", id: user.id }, "room.unblocked", `room:${r.id}`, { reason: gate.kind });
      r.blocked_reason = null;
      r.blocked_detail = null;
    }
    r.updated_at = now();
    ctx.emitRoom(s, r);
    for (const it of [...s.inbox.values()]) {
      if (it.ref_id !== h.id) continue;
      s.inbox.delete(it.id);
      ctx.emitInboxSummary(s, it.user_id, it.workspace_id);
    }
    emit(s, r.workspace_id, "hitl.updated", h, r.id);
    return ok({ hitl_request: view(s, h, user.id), ignored: false, decision_id: null });
  });

  /**
   * 인박스 v0.19 시드 — S8 의 새 항목 전부를 한 방(첫 옛 세션 방)에 만든다: 방 멈춤(예산) · 격리 확인(승인·저장소 고르기) ·
   * 미션 밖 요청(부방장 위임 전) · 미션 제안 · 미션 일시정지 · 미션 완료 · 방 초대 · 용량 · 미션 밖 에이전트 질문.
   */
  on("POST", "/__mock/inbox/seed-v19", (req) => {
    const s = store();
    const user = ctx.requireUser(s, req);
    ctx.syncRooms(s);
    const b = body<{ room_id?: string; deputy_room_id?: string; delegate_in_min?: number; types?: InboxItem["type"][] }>(req);
    const rooms = [...s.rooms.values()].filter((r) => memberOf(s, r.workspace_id, user.id));
    const room = (b.room_id ? s.rooms.get(b.room_id) : undefined) ?? rooms.find((r) => r.owner_user_id === user.id) ?? rooms[0];
    if (!room) throw new Problem(409, "no_room", R4_MOCK_ONLY.seed_no_room);
    const ws = room.workspace_id;
    const want = (t: InboxItem["type"]) => !b.types || b.types.includes(t);
    const made: InboxItem[] = [];
    const works = ctx.roomWorks(s, room).filter((w) => !w.legacy);
    const work = works[0] ?? null;
    const lane = [...s.lanes.values()].find((l) => l.session_id === room.id) ?? null;
    const add = (type: InboxItem["type"], basis: RecipientBasis | null, extra: Partial<InboxItem>, r: MockRoom = room) => {
      const it = ctx.addInboxItem(s, user.id, {
        workspace_id: ws, type, severity: ctx.inboxSeverity(type), session_id: r.id, room_id: r.id, ref_id: null, due_at: null, overdue: false, delegated: false,
        actions: ctx.inboxActions(type, extra.card?.hitl_type, true), ...extra,
      });
      if (basis) ex(s).basis.set(it.id, basis);
      made.push(it);
      return it;
    };
    const mkHitl = (r: MockRoom, over: Partial<HitlRequest>): HitlRequest => {
      const created = over.created_at ?? now();
      const h: HitlRequest = {
        id: uuid(), session_id: r.id, task_id: null, lane_id: null, source: "system", type: "approval", purpose: "user_approval",
        question: "", context: null, options: [], proposed_default: null, artifact_id: null, approver_spec: "room_owner",
        due_at: new Date(Date.parse(created) + ctx.hitlDueMs()).toISOString(), overdue: false, status: "open", approved: null, answer: null,
        answered_by: null, answered_at: null, budget_override_usd: null, can_respond: false, can_respond_from: null, message_id: null, created_at: created, ...over,
      };
      s.hitls.set(h.id, h);
      return h;
    };
    const computer = [...s.runtimes.values()].find((x) => x.workspace_id === ws)?.name ?? "MacBook";

    if (want("room_paused")) {
      // 방 멈춤(예산) — 서버 router.pauseForLoop / budget 의 방 문: 방 blocked + 방장 승인 요청 + room_paused.
      const open = ctx.roomWorks(s, room).filter((w) => w.status === "active" || w.status === "paused").length || 2;
      const q = "방 예산 상한 $20.00 에 닿았습니다 — 계속하려면 새 상한을 정해 주세요";
      const h = mkHitl(room, { purpose: "budget", question: q });
      ex(s).gate.set(h.id, { room_id: room.id, kind: "budget" });
      const owner = s.users.get(room.owner_user_id);
      room.blocked_reason = "budget";
      room.blocked_detail = {
        reason: "budget", works_stopped: open, blocked_at: now(), blocked_by_user: null, approver: owner ? stripUser(owner) : null,
        delegate_at: new Date(Date.now() + (b.delegate_in_min ?? 90) * 60000).toISOString(), next_approver: null, next_approver_role: null,
        budget_usd: 20, cost_usd: 21.4, open_works_remaining_usd: 6.5,
      };
      ctx.emitRoom(s, room);
      logActivity(s, ws, room.id, { kind: "system", id: null }, "room.blocked", `room:${room.id}`, { reason: "budget" });
      add("room_paused", "room_owner", { ref_id: h.id, due_at: h.due_at, card: { title: "방이 멈췄습니다", body: q, purpose: "budget" }, actions: ctx.inboxActions("room_paused", undefined, true) });
    }
    if (want("isolation_confirm")) {
      // 격리 확인 — 서버 roomgate.AskIsolation(approval) · AskRepo(choice). 두 번째 방이 있으면 거기에 저장소 고르기를 둔다.
      const q = `이 방의 첫 실행이 저장소가 있는 컴퓨터 ${computer} 에서 시작됩니다 (~/src/payments). 지금 설정으로는 에이전트들이 같은 폴더를 함께 고치게 됩니다. 워크트리로 나눌까요? 승인하면 에이전트마다 워크트리를 따로 쓰고, 거절하면 한 폴더를 함께 씁니다`;
      const iso = rooms.find((r) => r.id !== room.id && r.owner_user_id === user.id) ?? room;
      const h = mkHitl(iso, { purpose: "isolation", question: q, proposed_default: null });
      ex(s).gate.set(h.id, { room_id: iso.id, kind: "isolation" });
      add("isolation_confirm", "room_owner", { ref_id: h.id, due_at: h.due_at, card: { title: q, hitl_type: "approval", purpose: "isolation" }, actions: ctx.inboxActions("isolation_confirm", "approval", true) }, iso);
      const repos = ["~/src/payments", "~/src/payments-web"];
      const q2 = `이 방은 워크트리로 나눠 돕니다. 첫 실행을 맡을 컴퓨터 ${computer} 에 저장소가 ${repos.length}개 있습니다 — 어느 저장소에서 나눌지 골라 주세요`;
      const h2 = mkHitl(iso, { purpose: "isolation", type: "choice", question: q2, options: repos, proposed_default: repos[0] });
      ex(s).gate.set(h2.id, { room_id: iso.id, kind: "isolation" });
      add("isolation_confirm", "room_owner", { ref_id: h2.id, due_at: h2.due_at, card: { title: q2, hitl_type: "choice", proposed_default: repos[0], purpose: "isolation" }, actions: ctx.inboxActions("isolation_confirm", "choice", true) }, iso);
    }
    if (want("hitl_request")) {
      // 미션 밖 사람 확인 — 내가 **부방장**인 방(없으면 이 방을 부방장 자리로)이라 기한 절반 전에는 「14:30부터 답할 수 있습니다」.
      const dep = (b.deputy_room_id ? s.rooms.get(b.deputy_room_id) : undefined) ?? room;
      const h = mkHitl(dep, { type: "question", source: "agent", purpose: "agent", question: "경쟁사 수수료 정책을 어디까지 조사할까요?", proposed_default: "국내 5개사" });
      const az = roomOwnerAuthz(s, h, dep, user.id);
      add("hitl_request", az.basis ?? "room_deputy", {
        ref_id: h.id, due_at: h.due_at, delegated: az.basis === "room_deputy",
        card: { title: h.question, hitl_type: "question", proposed_default: h.proposed_default, agent_name: "Researcher" },
        actions: ctx.inboxActions("hitl_request", "question", az.allowed),
      }, dep);
    }
    if (want("lane_blocked")) {
      add("lane_blocked", "room_owner", { ref_id: lane?.id ?? null, lane_id: lane?.id ?? null, card: { title: "Researcher 의 질문", body: "경쟁사 수수료 정책 어떻게 돼? 해외 사례까지 보면 표가 두 배로 길어지는데 괜찮을까요", agent_name: "Researcher", lane_id: lane?.id ?? null } });
    }
    if (want("work_proposed")) {
      add("work_proposed", "room_owner", { card: { title: "미션 제안", body: "결제 수수료 비교표 만들기 — 대화에서 같은 질문이 세 번 나왔습니다", agent_name: "Lead" } });
    }
    if (want("work_paused") && work) {
      add("work_paused", "director", { work_id: work.id, card: { title: "미션이 멈췄습니다", paused_reason: "budget" } });
    }
    if (want("work_completed") && work) {
      add("work_completed", "director", { work_id: work.id, card: { title: "미션이 끝났습니다", summary: "비교표 1건 제출 · 결정 2건 · $3.10" } }).read_at;
    }
    if (want("room_invited")) {
      add("room_invited", null, { card: { title: "방에 초대되었습니다", body: room.name } });
    }
    if (want("workdir_quota")) {
      add("workdir_quota", "room_owner", { card: { title: "작업 폴더가 용량 상한에 닿았습니다", body: "정리하기 전까지 새 작업 폴더를 만들 수 없습니다 — 끝난 방의 작업 폴더를 정리해 주세요" } });
    }
    return ok(made.map((it) => inboxOut(s, it)), 201);
  });

  // ── getMessage(서버 handlers_message_get.go) — 404 는 messageNotFound 와 같은 문장 ─────────────
  on("GET", "/messages/{id}", (req, p) => {
    const s = store();
    const user = ctx.requireUser(s, req);
    const m = s.messages.get(p.id);
    const r = m ? roomOf(s, m.session_id) : undefined;
    if (!m || !r || !canView(s, r, user.id)) throw new Problem(404, "not_found", ctx.notFound("message"));
    return ok(m);
  });

  // ── S15 활동 로그 ──────────────────────────────────────────────────────────
  on("GET", "/workspaces/{id}/activity-log", (req, p) => {
    const s = store();
    const user = ctx.requireUser(s, req);
    const m = memberOf(s, p.id, user.id);
    if (!m) throw new Problem(404, "not_found", ctx.notFound("workspace"));
    if (m.role !== "owner" && m.role !== "admin") throw new Problem(403, "admin_required", ctx.W.admin_only);
    const q = req.query;
    const limit = Math.min(Math.max(Number(q.get("limit") ?? 50) || 50, 1), 200);
    const cursor = q.get("cursor");
    const masking = !!s.settings.get(p.id)?.task_event_masking;
    const rows = ex(s).activity
      .filter((e) => e.workspace_id === p.id)
      .filter((e) => !q.get("room_id") || e.room?.id === q.get("room_id") || e.payload.room_id === q.get("room_id"))
      .filter((e) => !q.get("action") || e.action === q.get("action"))
      .filter((e) => !q.get("actor_id") || e.actor_id_raw === q.get("actor_id"))
      .filter((e) => !q.get("since") || e.at >= q.get("since")!)
      .filter((e) => !q.get("until") || e.at < q.get("until")!)
      .filter((e) => !cursor || e.seq < Number(cursor))
      .sort((a, b) => b.seq - a.seq);
    const page = rows.slice(0, limit).map(({ workspace_id: _w, seq: _s, actor_id_raw: _a, ...e }) => ({ ...e, payload: masking ? maskActivity(e.payload) : e.payload }));
    const more = rows.length > limit;
    return ok({ items: page, next_cursor: more ? page[page.length - 1].id : null });
  });
  /** 활동 로그 시드 — S15 가 반드시 담아야 하는 행(SCREEN §4.18 표) 전부. */
  on("POST", "/__mock/activity/seed", (req) => {
    const s = store();
    const user = ctx.requireUser(s, req);
    ctx.syncRooms(s);
    const rooms = [...s.rooms.values()].filter((r) => memberOf(s, r.workspace_id, user.id));
    const a = rooms[0];
    if (!a) throw new Problem(409, "no_room", R4_MOCK_ONLY.seed_no_room);
    const b2 = rooms[1] ?? a;
    const ws = a.workspace_id;
    const agent = [...s.agents.values()].find((x) => x.workspace_id === ws);
    const t = (minAgo: number) => new Date(Date.now() - minAgo * 60000).toISOString();
    const long = "경쟁사 수수료 표를 만들려고 결제팀 방의 최근 대화 50개와 요약을 읽었습니다. 표의 열은 수수료율·정산 주기·환불 규칙이고, 해외 사례는 빼기로 했습니다(방장 결정).";
    const ag = { kind: "agent" as const, id: agent?.id ?? null };
    const me = { kind: "user" as const, id: user.id };
    logActivity(s, ws, a.id, me, "room.visibility_changed", `room:${a.id}`, { from: "workspace", to: "invited" }, t(300));
    logActivity(s, ws, a.id, me, "room_link.created", `room_link:${uuid()}`, { target_room_id: b2.id, target_room_name: b2.name }, t(240));
    logActivity(s, ws, a.id, ag, "room.read", `room:${b2.id}`, { direction: "out", other_room_id: b2.id, recent_n: 50, summary: true, note: long }, t(120));
    logActivity(s, ws, b2.id, ag, "room.read", `room:${a.id}`, { direction: "in", other_room_id: a.id, recent_n: 50, summary: true }, t(120));
    logActivity(s, ws, a.id, ag, "room.read.denied", "room", { direction: "denied", denied_reason: "not_linked", note: "참고 방으로 연결되지 않은 방은 읽을 수 없습니다" }, t(60));
    logActivity(s, ws, b2.id, me, "room.audit_viewed", `room:${b2.id}`, {}, t(45));
    logActivity(s, ws, a.id, { kind: "system", id: null }, "room.owner_succeeded", `room:${a.id}`, { from_user_id: uuid(), to_user_id: user.id }, t(30));
    logActivity(s, ws, null, me, "room.deleted", `room:${uuid()}`, { room_id: uuid(), name: "지난 분기 회고" }, t(10));
    return ok({ count: 8 }, 201);
  });

  // ── 알림 구독 3층(FR-8, 0.2.9) ─────────────────────────────────────────────
  const ROOM_SUB: readonly RoomSubscriptionLevel[] = ["all", "my_works", "hitl_only", "off"];
  const WORK_SUB: readonly SubscriptionLevel[] = ["all", "hitl_only", "completion_only"];
  const enumErr = (field: string, text: string) => new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field, code: "enum", message: text }] });
  on("PUT", "/rooms/{id}/subscription", (req, p) => {
    const s = store();
    const user = ctx.requireUser(s, req);
    const r = roomOf(s, p.id);
    if (!r || !canView(s, r, user.id)) throw new Problem(404, "not_found", ctx.notFound("room"));
    const lv = body<{ level?: RoomSubscriptionLevel }>(req).level;
    if (!lv || !ROOM_SUB.includes(lv)) throw enumErr("level", RW4.room_sub_enum);
    ex(s).roomSub.set(`${r.id}:${user.id}`, lv);
    return { status: 204 };
  });
  on("PUT", "/works/{id}/subscription", (req, p) => {
    const s = store();
    const user = ctx.requireUser(s, req);
    const w = s.works.get(p.id);
    const r = w ? roomOf(s, w.room_id) : undefined;
    if (!w || !r || !canView(s, r, user.id)) throw new Problem(404, "not_found", ctx.notFound("work"));
    const lv = body<{ level?: SubscriptionLevel | null }>(req).level;
    if (lv !== null && (!lv || !WORK_SUB.includes(lv))) throw enumErr("level", RW4.work_sub_enum);
    if (lv === null) ex(s).workSub.delete(`${w.id}:${user.id}`);
    else ex(s).workSub.set(`${w.id}:${user.id}`, lv);
    return { status: 204 };
  });
  on("PUT", "/lanes/{id}/subscription", (req, p) => {
    const s = store();
    const user = ctx.requireUser(s, req);
    const l = s.lanes.get(p.id);
    const r = l ? roomOf(s, l.session_id) : undefined;
    if (!l || !r || !canView(s, r, user.id)) throw new Problem(404, "not_found", ctx.notFound("lane"));
    const en = body<{ enabled?: boolean }>(req).enabled;
    if (typeof en !== "boolean") throw new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field: "enabled", code: "required", message: R4_MOCK_ONLY.lane_sub_required }] });
    ex(s).laneSub.set(`${l.id}:${user.id}`, en);
    return { status: 204 };
  });
  prepend("GET", "/rooms/{id}", async (req, p) => {
    const res = await next(req);
    if (!isOk(res)) return res;
    const s = store();
    const user = ctx.requireUser(s, req);
    return { ...res, body: { ...(res.body as Room), my_subscription: ex(s).roomSub.get(`${p.id}:${user.id}`) ?? "all" } };
  });
  prepend("GET", "/works/{id}", async (req, p) => {
    const res = await next(req);
    if (!isOk(res)) return res;
    const s = store();
    const user = ctx.requireUser(s, req);
    const sub = ex(s).workSub.get(`${p.id}:${user.id}`);
    return sub ? { ...res, body: { ...(res.body as object), subscription: sub } } : res;
  });
  prepend("GET", "/rooms/{id}/lanes", async (req) => {
    const res = await next(req);
    if (!isOk(res)) return res;
    const s = store();
    const user = ctx.requireUser(s, req);
    const withSub = (l: Lane) => ({ ...l, my_subscription: ex(s).laneSub.get(`${l.id}:${user.id}`) ?? null });
    const b = res.body as Lane[] | { items: Lane[] };
    return { ...res, body: Array.isArray(b) ? b.map(withSub) : { ...b, items: b.items.map(withSub) } };
  });

  // ── S14 방 기본값 · 다른 방 읽기(handlers_settings.go — 키 단위 얕은 합치기, 읽을 때는 빈 칸을 기본값으로 채운다) ──────
  /** 서버 effectiveRoomDefaults — 새 방이 실제로 물려받을 값. */
  const effective = (st: S["WorkspaceSettings"]): S["WorkspaceSettings"] => {
    const d = st.room_defaults ?? {};
    return {
      ...st,
      room_defaults: {
        visibility: d.visibility ?? "workspace", isolation_kind: d.isolation_kind ?? st.default_isolation, autonomy: d.autonomy ?? "guided",
        limits: { ...(d.limits ?? {}), max_concurrent_works: d.limits?.max_concurrent_works ?? 3, max_parallel_lanes: d.limits?.max_parallel_lanes ?? 5 },
      },
      room_read: { max_rooms_per_turn: 3, max_tokens: 4000, ...(st.room_read ?? {}) },
    };
  };
  prepend("GET", "/workspaces/{id}/settings", async (req) => {
    const res = await next(req);
    return isOk(res) ? { ...res, body: effective(res.body as S["WorkspaceSettings"]) } : res;
  });
  prepend("PATCH", "/workspaces/{id}/settings", async (req, p) => {
    const b = body<S["WorkspaceSettingsUpdate"]>(req);
    const errs: { field: string; code: string; message: string }[] = [];
    const rr = b.room_read;
    if (rr?.max_rooms_per_turn != null && rr.max_rooms_per_turn < 1) errs.push({ field: "room_read.max_rooms_per_turn", code: "out_of_range", message: RW4.min_one });
    if (rr?.max_tokens != null && rr.max_tokens < 500) errs.push({ field: "room_read.max_tokens", code: "out_of_range", message: RW4.min_500 });
    const d = b.room_defaults;
    if (d?.visibility != null && d.visibility !== "workspace" && d.visibility !== "invited") errs.push({ field: "room_defaults.visibility", code: "invalid", message: ctx.W.room_visibility_enum });
    if (d?.isolation_kind != null && d.isolation_kind !== "none" && d.isolation_kind !== "worktree") errs.push({ field: "room_defaults.isolation_kind", code: "unsupported", message: RW4.room_isolation });
    if (d?.autonomy === "supervised") errs.push({ field: "room_defaults.autonomy", code: "unsupported", message: RW4.supervised_unsupported });
    if (d?.limits?.max_concurrent_works != null && d.limits.max_concurrent_works < 1) errs.push({ field: "room_defaults.limits.max_concurrent_works", code: "out_of_range", message: RW4.min_one });
    if (d?.limits?.max_parallel_lanes != null && d.limits.max_parallel_lanes < 1) errs.push({ field: "room_defaults.limits.max_parallel_lanes", code: "out_of_range", message: RW4.min_one });
    if (errs.length) throw new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: errs });
    const { room_defaults: _d, room_read: _r, ...rest } = b;
    const res = await next({ ...req, body: rest });
    if (!isOk(res)) return res;
    const s = store();
    const cur = s.settings.get(p.id);
    if (cur) {
      if (d) cur.room_defaults = { ...(cur.room_defaults ?? {}), ...d };
      if (rr) cur.room_read = { ...(cur.room_read ?? {}), ...rr };
    }
    return { ...res, body: effective({ ...(res.body as S["WorkspaceSettings"]), room_defaults: cur?.room_defaults, room_read: cur?.room_read }) };
  });

  // ── S9 에이전트 · S11 컴퓨터 — 방 칸(0.2.9) ─────────────────────────────────
  /** 에이전트가 참여한 방 — 방 목록의 참여자(`participants`)가 정본(옛 세션 방 · 새 방 둘 다). `asUser` 로 그 사람이 볼 수 있는 방만. */
  async function agentRooms(s: Store, req: Req, wsId: string, asUser?: string): Promise<Map<string, RoomListItem[]>> {
    // 워크스페이스 전체(볼 수 없는 방 수를 세려고)는 owner 의 눈으로 한 번 더 읽는다 — 목 안에서만 쓰는 일회용 쿠키.
    let tok: string | null = null;
    if (asUser) {
      tok = uuid();
      s.cookies.set(tok, asUser);
    }
    const listReq: Req = {
      ...req, method: "GET", path: `/workspaces/${wsId}/rooms`, query: new URLSearchParams({ participating: "false", include_archived: "true", limit: "200" }),
      ...(tok ? { cookies: { colab_session: tok } } : {}),
    };
    try {
      const res = await next(listReq);
      const items = isOk(res) ? ((res.body as { items: RoomListItem[] }).items ?? []) : [];
      const out = new Map<string, RoomListItem[]>();
      for (const r of items) for (const p of r.participants) if (p.kind === "agent") out.set(p.id, [...(out.get(p.id) ?? []), r]);
      return out;
    } finally {
      if (tok) s.cookies.delete(tok);
    }
  }
  const running = (s: Store, agentId: string) => [...s.tasks.values()].filter((t) => t.agent_id === agentId && (t.status === "running" || t.status === "dispatched")).length;
  async function withRooms(s: Store, req: Req, wsId: string, agents: Agent[]): Promise<Agent[]> {
    const byAgent = await agentRooms(s, req, wsId);
    const owner = s.members.find((m) => m.workspace_id === wsId && m.role === "owner")?.user.id;
    const everything = owner ? await agentRooms(s, req, wsId, owner) : byAgent;
    return agents.map((a) => {
      const rs = byAgent.get(a.id) ?? [];
      const visible = new Set(rs.map((r) => r.id));
      const hidden = (everything.get(a.id) ?? []).filter((r) => !visible.has(r.id)).length;
      return { ...a, rooms: rs.map((r) => ({ id: r.id, name: r.name })), room_count: rs.length, hidden_room_count: hidden, running_task_count: running(s, a.id) };
    });
  }
  prepend("GET", "/workspaces/{id}/agents", async (req, p) => {
    const res = await next(req);
    if (!isOk(res)) return res;
    const s = store();
    const b = res.body as Agent[] | { items: Agent[] };
    if (Array.isArray(b)) return { ...res, body: await withRooms(s, req, p.id, b) };
    return { ...res, body: { ...b, items: await withRooms(s, req, p.id, b.items) } };
  });
  prepend("GET", "/agents/{id}", async (req) => {
    const res = await next(req);
    if (!isOk(res)) return res;
    const s = store();
    const a = res.body as Agent;
    return { ...res, body: (await withRooms(s, req, a.workspace_id, [a]))[0] };
  });
  prepend("GET", "/workspaces/{id}/runtimes", async (req) => {
    const res = await next(req);
    if (!isOk(res)) return res;
    const s = store();
    ctx.syncRooms(s);
    // 묶인 방 = 컴퓨터가 고정된 방(옛 세션 방은 세션의 runtime_id).
    const count = (rt: Runtime) => [...s.rooms.values()].filter((r) => s.sessions.get(r.id)?.runtime_id === rt.id).length;
    // 제자리에 얹는다 — 목 테스트 몇이 받은 런타임 객체를 고쳐 저장소 상태를 바꾼다(rooms-dialogs.test 의 capabilities).
    for (const rt of res.body as Runtime[]) rt.room_count = count(rt);
    return res;
  });
}
