/**
 * 목 op — 방의 다이얼로그·설정(T-R2-W3): S19 참여자 · S20 방 설정 · S24 참고 방 링크 · S23 맥락 읽기 기록 · S21 미션 열기 · S26 미션 제안.
 *
 * 서버(T-R1b3 #291 · T-R1b2 #294 · T-R1c #290)를 흉내 낸다 — 권한은 `rooms.Decide`·`Deny` 그대로(handlers.ts 의 `roomDecide`·`roomDeny`
 * 를 빌려 쓰고, 그 표에 없는 넷 — `leave`·`set_deputy`·`open_work`·`proposals` — 만 여기서 authz.go 대로 더한다), 문장은 `RD_SERVER`
 * (+ 이미 `SERVER` 에 있는 것은 `W`).
 *
 * **handlers.ts 에는 등록 한 줄만 둔다**(`registerRoomDialogs(…)`) — 같은 시기에 S7 재작성(T-R2-W2)이 handlers.ts 를 고친다.
 * 옛 방 op 세 개(`GET /rooms/{id}` · `PATCH /rooms/{id}` · `GET /rooms/{id}/works`)는 **앞에 끼워** 덮는다(`prepend`): 옛 것은 방 설정의
 * 절반(이름·설명·공개 범위)만 저장하고, 새로 연 미션을 목록에 싣지 못한다. 덮은 핸들러는 옛 결과(`toRoom`)에 이 모듈의 칸을 얹는다.
 *
 * 이 모듈의 상태(참여자 id · 새 방의 에이전트 · 링크 · 읽기 기록 · 미션 · 제안 · 방 설정의 나머지 칸)는 `Store` 에 붙인 `WeakMap` 에 둔다 —
 * `resetStore()` 가 새 Store 를 만들면 같이 비워진다(store.ts 를 고치지 않는다).
 */
import type {
  AgentProfile, CompletionCondition, Member, Message, Room, RoomRole, RoomUpdate, Session, User, WorkListItem,
} from "@/lib/api/types";
import type { components } from "@/lib/api/schema";
import { emit, now, participantStatus, store, stripUser, uuid, type MockRoom, type MockWork, type Store } from "./store";
import { josa, notFound, VALIDATION_DETAIL, W } from "./wording";
import { RW } from "./rooms-dialogs-wording";
import type { Req, Res } from "./handlers";

type S = components["schemas"];
type RoomParticipant = S["RoomParticipant"];
type RoomLink = S["RoomLink"];
type RoomRead = S["RoomRead"];
type Work = S["Work"];
type WorkCreate = S["WorkCreate"];
type WorkProposal = S["WorkProposal"];

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type RoomAct = "view" | "post" | "configure" | "archive" | "delete" | "mark_read" | "invite" | "link" | "block" | "transfer_owner" | "summarize";
interface Standing { roomRole: RoomRole | null; wsRole: Member["role"] | null; visibility: MockRoom["visibility"]; archived: boolean }
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

/** handlers.ts 가 넘기는 것 — 그 파일 안에서만 사는 헬퍼들. */
export interface RoomDialogsCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  /** `on` 이 쌓은 라우트 배열 — 덮을 op 를 맨 앞으로 옮기는 데만 쓴다. */
  routes: { method: string; re: RegExp; keys: string[]; h: Handler }[];
  Problem: ProblemCtor;
  requireUser: (s: Store, req: Req) => User;
  syncRooms: (s: Store) => void;
  standingOf: (s: Store, r: MockRoom, userId: string) => Standing;
  roomDecide: (a: RoomAct, f: Standing) => boolean;
  roomDeny: (a: RoomAct, f: Standing) => Error;
  toRoom: (s: Store, r: MockRoom, userId: string) => Room;
  emitRoom: (s: Store, r: MockRoom) => void;
  validateCondition: (cc: CompletionCondition, participantIds: string[]) => { field: string; code: string; message: string }[];
}

interface StoredRead {
  id: string;
  at: string;
  /** 읽은 쪽 방(에이전트가 일하던 방). */
  room_id: string;
  target_room_id: string;
  agent: { id: string; name: string };
  originator_user_id: string | null;
  allowed: boolean;
  denied_reason: RoomRead["denied_reason"];
  summary: boolean;
  recent_n: number;
  truncated: boolean;
  task_id: string | null;
}
interface RoomExtra {
  default_director_user_id: string | null;
  /** 첫 실행 전 방 설정에서 고른 컴퓨터(옛 세션 방은 세션의 runtime_id). */
  runtime_id?: string | null;
  /** 첫 dispatch 가 있었다 — 컴퓨터·격리 고정(서버: task_attempt 존재). */
  pinned?: boolean;
}
interface RD {
  pids: Map<string, string>;
  /** 새 방(옛 세션 없음)의 에이전트 참여자. 옛 세션 방은 `sess.participants` 가 정본. */
  agents: Map<string, { agent_id: string; profile_id: string; joined_at: string }[]>;
  extra: Map<string, RoomExtra>;
  links: { id: string; room_id: string; target_room_id: string; created_by: string; created_at: string }[];
  reads: StoredRead[];
  works: Work[];
  /** 「이걸 미션으로」로 한 번 귀속된 메시지 → 미션. */
  msgWork: Map<string, string>;
  proposals: WorkProposal[];
}
const STATE = new WeakMap<Store, RD>();
function rd(s: Store): RD {
  let x = STATE.get(s);
  if (!x) {
    x = { pids: new Map(), agents: new Map(), extra: new Map(), links: [], reads: [], works: [], msgWork: new Map(), proposals: [] };
    STATE.set(s, x);
  }
  return x;
}
const extraOf = (s: Store, roomId: string): RoomExtra => {
  const e = rd(s).extra.get(roomId) ?? { default_director_user_id: null };
  rd(s).extra.set(roomId, e);
  return e;
};
const pid = (s: Store, roomId: string, kind: "u" | "a", id: string) => {
  const k = `${roomId}:${kind}:${id}`;
  let v = rd(s).pids.get(k);
  if (!v) {
    v = uuid();
    rd(s).pids.set(k, v);
  }
  return v;
};
const body = <T,>(req: Req) => (req.body ?? {}) as T;
const ok = (b: unknown, status = 200): Res => ({ status, body: b });
const name = (s: Store, userId: string) => {
  const u = s.users.get(userId);
  return u?.display_name || u?.email || "알 수 없는 사람";
};
const RUNNING = new Set(["active", "paused", "completing"]);
const DIRECTING = new Set(["draft", "active", "paused", "completing"]);

export function registerRoomDialogs(ctx: RoomDialogsCtx): void {
  const { on, Problem } = ctx;
  const validation = (errors: { field: string; code?: string; message: string }[]) => new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors });
  const notFoundP = (what: "room" | "participant" | "room_link" | "work_proposal") => new Problem(404, "not_found", notFound(what));
  /** `on` 으로 올린 뒤 맨 앞으로 — 같은 경로의 옛 핸들러보다 먼저 받는다. */
  const prepend = (method: string, pattern: string, h: Handler) => {
    on(method, pattern, h);
    ctx.routes.unshift(ctx.routes.pop()!);
  };

  /** authz.go 에서 handlers.ts 표에 없는 넷 — `leave`·`set_deputy`·`open_work`·`proposals`. */
  type Act = RoomAct | "leave" | "set_deputy" | "open_work" | "proposals";
  const decide = (a: Act, f: Standing): boolean => {
    if (a === "set_deputy") return ctx.roomDecide("transfer_owner", f);
    if (a === "leave" || a === "proposals") return ctx.roomDecide("view", f) && f.roomRole != null;
    if (a === "open_work") return ctx.roomDecide("view", f) && f.roomRole != null && !f.archived;
    return ctx.roomDecide(a, f);
  };
  const deny = (a: Act, f: Standing): Error => {
    if (a === "set_deputy") return ctx.roomDeny("transfer_owner", f);
    if (a === "leave") return ctx.roomDeny("mark_read", f);
    if (!f.wsRole || !ctx.roomDecide("view", f)) return notFoundP("room");
    if (a === "open_work" && f.archived && f.roomRole != null) return new Problem(409, "room_archived", W.room_archived);
    if (a === "open_work" || a === "proposals") return new Problem(403, "not_participant", RW.open_not_participant);
    return ctx.roomDeny(a, f);
  };
  const gate = (s: Store, req: Req, roomId: string, act: Act) => {
    ctx.syncRooms(s);
    const user = ctx.requireUser(s, req);
    const room = s.rooms.get(roomId);
    if (!room) throw notFoundP("room");
    const f = ctx.standingOf(s, room, user.id);
    if (!decide(act, f)) throw deny(act, f);
    return { room, user, f };
  };
  const sessOf = (s: Store, roomId: string): Session | undefined => s.sessions.get(roomId);
  /**
   * 옛 세션(미션 하나)인 방의 세션 — T-R2-W2 목은 새 방에도 같은 id 의 **뒷받침 세션**(`s.roomOnly`)을 둔다(참여자·메시지·서브 미션이 거기
   * 쌓인다). 그 세션은 미션이 아니다 — 미션 목록·Director 셈은 이것으로 본다.
   */
  const legacyOf = (s: Store, roomId: string): Session | undefined => (s.roomOnly.has(roomId) ? undefined : s.sessions.get(roomId));
  const runtimeIdOf = (s: Store, room: MockRoom) => {
    const e = rd(s).extra.get(room.id);
    return e && e.runtime_id !== undefined ? e.runtime_id : sessOf(s, room.id)?.runtime_id ?? null;
  };
  /** 방 컴퓨터가 고정됐나 — 첫 실행(할 일 시도)이 있었다. 목은 옛 세션 방의 할 일 · `__mock` 시드로 본다. */
  const pinned = (s: Store, room: MockRoom) => !!rd(s).extra.get(room.id)?.pinned || [...s.tasks.values()].some((t) => t.session_id === room.id && t.status !== "queued");

  /** 옛 `toRoom` + 이 모듈의 칸(기본 Director · 방 설정에서 고른 컴퓨터 · 고정 여부 `runtime_pinned`(0.2.7) · 새로 연 미션 수). */
  const roomOut = (s: Store, room: MockRoom, userId: string): Room => {
    const base = ctx.toRoom(s, room, userId);
    const rid = runtimeIdOf(s, room);
    const rt = rid ? s.runtimes.get(rid) : undefined;
    return {
      ...base,
      runtime_id: rid,
      runtime_pinned: pinned(s, room),
      ...(rt ? { runtime: rt } : {}),
      default_director_user_id: extraOf(s, room.id).default_director_user_id,
      // works_active 는 toRoom 이 이미 센다(옛 세션의 미션 + `s.works`, T-R2-W2).
    };
  };

  /** 시스템 메시지 — 서버 `Router.SystemPost`. 옛 세션 방이든 새 방이든 메시지의 `session_id` 는 방 id 다. */
  const systemPost = (s: Store, room: MockRoom, content: string, workId?: string) => {
    const m: Message = {
      id: uuid(), session_id: room.id, parent_id: null, source_task_id: null, lane_id: null, state: "posted", reply_count: 0, is_note: false,
      author_type: "system", author_id: null, kind: "system", content, mentions: [], created_at: now(), edited_at: null,
      ...(workId ? { work_id: workId } : {}),
    } as Message;
    s.messages.set(m.id, m);
    emit(s, room.workspace_id, "message.created", m, room.id);
    return m;
  };

  // ── 참여자 ────────────────────────────────────────────────────────────
  type AgentRow = { agent_id: string; profile_id: string | null; joined_at: string };
  const agentRows = (s: Store, room: MockRoom): AgentRow[] => {
    const sess = sessOf(s, room.id);
    if (sess) return (sess.participants ?? []).map((p) => ({ agent_id: p.agent_id, profile_id: p.profile?.id ?? null, joined_at: p.joined_at ?? room.created_at }));
    return rd(s).agents.get(room.id) ?? [];
  };
  const personRow = (s: Store, room: MockRoom, p: MockRoom["people"][number]): RoomParticipant => {
    const u = s.users.get(p.user_id);
    return {
      id: pid(s, room.id, "u", p.user_id), room_id: room.id, kind: "user", ...(u ? { user: stripUser(u) } : {}),
      room_role: p.role, joined_at: p.joined_at, left_at: null, status_note: null,
    };
  };
  const agentRow = (s: Store, room: MockRoom, r: AgentRow): RoomParticipant => {
    const a = s.agents.get(r.agent_id);
    const prof = a?.profiles.find((x) => x.id === r.profile_id) ?? a?.profiles.find((x) => x.is_default) ?? a?.profiles[0];
    return {
      id: pid(s, room.id, "a", r.agent_id), room_id: room.id, kind: "agent", ...(a ? { agent: a } : {}), ...(prof ? { profile: prof } : {}),
      status: agentStatusHere(s, room.id, r.agent_id), status_note: null, room_role: "member", joined_at: r.joined_at, left_at: null,
    };
  };
  /** 에이전트 상태는 워크스페이스 전역 할 일에서 파생된다(FR-1.3) — 이 방이 idle 이어도 다른 방에서 실행 중이면 working(T-R2-W2 「다른 방에서 작업 중」). */
  const agentStatusHere = (s: Store, roomId: string, agentId: string): RoomParticipant["status"] => {
    const here = participantStatus(s, roomId, agentId);
    if (here !== "idle") return here;
    return [...s.tasks.values()].some((t) => t.agent_id === agentId && t.status === "running" && t.session_id !== roomId) ? "working" : here;
  };
  const ROLE_ORDER: Record<string, number> = { owner: 0, deputy: 1, member: 2 };
  const listParticipants = (s: Store, room: MockRoom): RoomParticipant[] => [
    ...[...room.people].sort((a, b) => ROLE_ORDER[a.role] - ROLE_ORDER[b.role] || a.joined_at.localeCompare(b.joined_at)).map((p) => personRow(s, room, p)),
    ...agentRows(s, room).map((r) => agentRow(s, room, r)),
  ];
  const findRow = (s: Store, room: MockRoom, participantId: string) => listParticipants(s, room).find((p) => p.id === participantId);

  /** 미션 목록 — 옛 세션(미션 id = 세션 id) + 이 모듈이 연 미션. */
  const worksOf = (s: Store, room: MockRoom): WorkListItem[] => {
    const sess = legacyOf(s, room.id);
    const legacy: WorkListItem[] = sess ? [{
      id: sess.id, room_id: room.id, title: sess.title, goal: sess.goal, status: sess.status, paused_reason: null,
      waiting_human: [...s.hitls.values()].some((h) => h.session_id === sess.id && h.status === "open"), director: sess.director!,
      assignee_agent_id: sess.assignee_agent_id, completion_progress: { met: sess.completion_progress.met, total: sess.completion_progress.total },
      cost_usd: sess.cost_usd, budget_usd: sess.limits.budget_usd ?? null, last_activity_at: sess.last_activity_at ?? null, finished_at: sess.finished_at ?? null,
    }] : [];
    // 미션 저장소는 `s.works` 하나(T-R2-W2 와 공유 — getWork·pause/resume·귀속이 같은 행을 본다).
    const mine: WorkListItem[] = [...s.works.values()].filter((w) => w.room_id === room.id).map((w) => ({
      id: w.id, room_id: w.room_id, title: w.title, goal: w.goal, status: w.status, paused_reason: w.paused_reason, waiting_human: false,
      director: s.users.get(w.director_user_id) ? stripUser(s.users.get(w.director_user_id)!) : { id: w.director_user_id, email: "", display_name: "", avatar_url: null, created_at: w.created_at }, assignee_agent_id: w.assignee_agent_id, completion_progress: { met: w.completion_progress.met, total: w.completion_progress.total },
      cost_usd: w.cost_usd, budget_usd: w.limits.budget_usd ?? null, last_activity_at: w.last_activity_at ?? null, finished_at: w.finished_at ?? null,
    }));
    return [...legacy, ...mine];
  };

  prepend("GET", "/rooms/{id}", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "view");
    return ok(roomOut(s, room, user.id));
  });
  // `GET /rooms/{id}/works` 는 덮지 않는다 — T-R2-W2 의 handlers.ts 가 `s.works`(이 모듈이 연 미션 포함)·waiting_human·미션 비용까지 싣는다.

  on("GET", "/rooms/{id}/participants", (req, p) => {
    const s = store();
    const { room } = gate(s, req, p.id, "view");
    return ok({ items: listParticipants(s, room) });
  });
  on("POST", "/rooms/{id}/participants", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "invite");
    const b = body<{ user_id?: string; agent_id?: string; profile_id?: string }>(req);
    if (!b.user_id === !b.agent_id) throw validation([{ field: "user_id", code: "one_of", message: RW.one_of }]);
    const who = name(s, user.id);
    let row: RoomParticipant;
    let warnings: string[] = [];
    if (b.user_id) {
      if (!s.members.some((m) => m.workspace_id === room.workspace_id && m.user.id === b.user_id)) throw validation([{ field: "user_id", code: "not_member", message: RW.invite_not_member }]);
      if (room.people.some((x) => x.user_id === b.user_id)) throw new Problem(409, "already_participant", RW.already_person);
      const person = { user_id: b.user_id, role: "member" as const, joined_at: now() };
      room.people.push(person);
      systemPost(s, room, who + RW.sys_invited_mid + name(s, b.user_id) + RW.sys_invited_tail);
      row = personRow(s, room, person);
    } else {
      const a = s.agents.get(b.agent_id!);
      if (!a || a.workspace_id !== room.workspace_id) throw validation([{ field: "agent_id", code: "not_found", message: RW.agent_not_here }]);
      // FR-1.9 — 누른 사람에 대해(`tasks.MayTrigger`).
      if (a.respond_to === "nobody") throw new Problem(403, "not_invitable", RW.trig_nobody);
      if (a.respond_to === "allowlist" && a.owner_id !== user.id && !a.respond_to_allowlist.includes(user.id)) throw new Problem(403, "not_invitable", RW.trig_allowlist);
      if (a.respond_to === "owner" && a.owner_id !== user.id) throw new Problem(403, "not_invitable", RW.trig_owner);
      let prof: AgentProfile | undefined;
      if (b.profile_id) {
        prof = a.profiles.find((x) => x.id === b.profile_id);
        if (!prof) throw validation([{ field: "profile_id", code: "not_found", message: RW.profile_not_of_agent }]);
      } else prof = a.profiles.find((x) => x.is_default) ?? a.profiles[0];
      if (agentRows(s, room).some((x) => x.agent_id === a.id)) throw new Problem(409, "already_participant", RW.already_agent);
      const joined = now();
      const sess = sessOf(s, room.id);
      if (sess) {
        sess.participants = [...(sess.participants ?? []), {
          session_id: sess.id, agent_id: a.id,
          agent: { id: a.id, name: a.name, role: a.role, role_description: a.role_description, avatar_url: null, respond_to: a.respond_to },
          profile: prof!, status: "idle", status_note: null, is_assignee: false, mention_link: `[@${a.name}](mention://agent/${a.id})`, warnings: [], joined_at: joined,
        }];
      } else {
        rd(s).agents.set(room.id, [...(rd(s).agents.get(room.id) ?? []), { agent_id: a.id, profile_id: prof!.id, joined_at: joined }]);
      }
      systemPost(s, room, josa(a.name, "이", "가") + RW.sys_agent_joined);
      // 방 컴퓨터에 그 종류가 없으면 경고(거부 아님) — 컴퓨터가 아직 없으면 경고도 없다.
      const rid = runtimeIdOf(s, room);
      const rt = rid ? s.runtimes.get(rid) : undefined;
      if (rt && !rt.capabilities.some((c) => c.kind === prof!.runtime_kind)) warnings = ["runtime_kind_missing"];
      row = agentRow(s, room, { agent_id: a.id, profile_id: prof!.id, joined_at: joined });
    }
    emit(s, room.workspace_id, "participant.joined", { room_id: room.id, participant: row }, room.id);
    return ok({ ...row, warnings }, 201);
  });
  on("PATCH", "/rooms/{id}/participants/{pid}", (req, p) => {
    const s = store();
    const { room } = gate(s, req, p.id, "configure");
    const row = findRow(s, room, p.pid);
    if (!row) throw notFoundP("participant");
    const b = body<{ profile_id?: string }>(req);
    if (!b.profile_id) return ok(row);
    if (row.kind !== "agent") throw validation([{ field: "profile_id", code: "agent_only", message: RW.profile_agent_only }]);
    const a = s.agents.get(row.agent!.id)!;
    const prof = a.profiles.find((x) => x.id === b.profile_id);
    if (!prof) throw validation([{ field: "profile_id", code: "not_found", message: RW.profile_not_of_agent }]);
    const sess = sessOf(s, room.id);
    if (sess) {
      const part = (sess.participants ?? []).find((x) => x.agent_id === a.id);
      if (part) part.profile = prof;
    } else {
      const r = (rd(s).agents.get(room.id) ?? []).find((x) => x.agent_id === a.id);
      if (r) r.profile_id = prof.id;
    }
    return ok(findRow(s, room, p.pid));
  });
  /** 내보내기 · 이 방에서 나가기 — 본인 행이면 참여자 누구나, 남의 행이면 초대 권한. 방장(is_owner)·열린 미션의 Director(is_director)는 409. */
  on("DELETE", "/rooms/{id}/participants/{pid}", (req, p) => {
    const s = store();
    ctx.syncRooms(s);
    const user = ctx.requireUser(s, req);
    const room0 = s.rooms.get(p.id);
    if (!room0) throw notFoundP("room");
    const self = pid(s, room0.id, "u", user.id) === p.pid && room0.people.some((x) => x.user_id === user.id);
    const { room } = gate(s, req, p.id, self ? "leave" : "invite");
    const row = findRow(s, room, p.pid);
    if (!row) throw notFoundP("participant");
    let who: string;
    if (row.kind === "user") {
      const uid = row.user!.id;
      if (row.room_role === "owner") throw new Problem(409, "is_owner", RW.is_owner);
      const directing = worksOf(s, room).filter((w) => DIRECTING.has(w.status) && w.director?.id === uid).length;
      if (directing > 0) throw new Problem(409, "is_director", RW.is_director, { works_directed: directing });
      if (row.room_role === "deputy") room.deputy_owner_user_id = null;
      room.people = room.people.filter((x) => x.user_id !== uid);
      who = name(s, uid) + " 님";
    } else {
      const aid = row.agent!.id;
      const sess = sessOf(s, room.id);
      if (sess) sess.participants = (sess.participants ?? []).filter((x) => x.agent_id !== aid);
      else rd(s).agents.set(room.id, (rd(s).agents.get(room.id) ?? []).filter((x) => x.agent_id !== aid));
      who = row.agent!.name;
    }
    systemPost(s, room, self ? josa(who, "이", "가") + RW.sys_left : name(s, user.id) + RW.sys_invited_mid + josa(who, "을", "를") + RW.sys_removed);
    const at = now();
    emit(s, room.workspace_id, "participant.left", { room_id: room.id, participant_id: row.id, kind: row.kind, left_at: at }, room.id);
    ctx.emitRoom(s, room);
    return { status: 204 };
  });

  // ── 방장·부방장 ───────────────────────────────────────────────────────
  const requireLiveHuman = (room: MockRoom, userId: string) => {
    if (!room.people.some((x) => x.user_id === userId)) throw validation([{ field: "user_id", code: "not_participant", message: RW.seat_not_participant }]);
  };
  on("PUT", "/rooms/{id}/owner", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "transfer_owner");
    const target = body<{ user_id?: string }>(req).user_id ?? "";
    if (room.owner_user_id !== target) {
      requireLiveHuman(room, target);
      const from = room.owner_user_id;
      room.owner_user_id = target;
      if (room.deputy_owner_user_id === target) room.deputy_owner_user_id = null;
      for (const x of room.people) {
        if (x.user_id === from && x.role === "owner") x.role = "member";
        if (x.user_id === target) x.role = "owner";
      }
      room.updated_at = now();
      systemPost(s, room, name(s, user.id) + RW.sys_owner_mid + name(s, target) + RW.sys_owner_tail);
      ctx.emitRoom(s, room);
    }
    return ok(roomOut(s, room, user.id));
  });
  on("PUT", "/rooms/{id}/deputy", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "set_deputy");
    const target = body<{ user_id?: string | null }>(req).user_id ?? null;
    const cur = room.deputy_owner_user_id;
    if (cur !== target) {
      if (target) {
        if (target === room.owner_user_id) throw validation([{ field: "user_id", code: "is_owner", message: RW.deputy_is_owner }]);
        requireLiveHuman(room, target);
      }
      room.deputy_owner_user_id = target;
      for (const x of room.people) {
        if (x.user_id === cur && x.role === "deputy") x.role = "member";
        if (target && x.user_id === target) x.role = "deputy";
      }
      room.updated_at = now();
      systemPost(s, room, target ? name(s, user.id) + RW.sys_invited_mid + name(s, target) + RW.sys_deputy_set : name(s, user.id) + RW.sys_deputy_cleared);
      ctx.emitRoom(s, room);
    }
    return ok(roomOut(s, room, user.id));
  });

  // ── 방 설정(updateRoom 전체 칸) ────────────────────────────────────────
  prepend("PATCH", "/rooms/{id}", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "configure");
    const b = body<RoomUpdate>(req);
    const errors: { field: string; code?: string; message: string }[] = [];
    if (b.name !== undefined && (!b.name.trim() || [...b.name].length > 200)) errors.push({ field: "name", code: "length", message: W.room_name_1_200 });
    if (b.description !== undefined && [...b.description].length > 500) errors.push({ field: "description", code: "length", message: W.room_description_500 });
    if (b.visibility !== undefined && b.visibility !== "workspace" && b.visibility !== "invited") errors.push({ field: "visibility", code: "enum", message: W.room_visibility_enum });
    if (b.autonomy === "supervised") errors.push({ field: "autonomy", code: "unsupported", message: RW.supervised_unsupported });
    if (b.isolation) {
      if (b.isolation.kind === "worktree" && !b.isolation.repo_path?.trim()) errors.push({ field: "isolation/repo_path", code: "required", message: RW.repo_required });
      if (b.isolation.kind === "container") errors.push({ field: "isolation/kind", code: "unsupported", message: RW.container_unsupported });
    }
    const l = b.limits;
    if (l) {
      if (l.max_concurrent_works != null && l.max_concurrent_works < 1) errors.push({ field: "limits/max_concurrent_works", code: "out_of_range", message: RW.min_1 });
      if (l.max_parallel_lanes != null && l.max_parallel_lanes < 1) errors.push({ field: "limits/max_parallel_lanes", code: "out_of_range", message: RW.min_1 });
      if (l.budget_usd != null && l.budget_usd < 0) errors.push({ field: "limits/budget_usd", code: "out_of_range", message: RW.min_0 });
    }
    if (errors.length) throw validation(errors);
    if (("runtime_id" in b || b.isolation) && pinned(s, room)) throw new Problem(409, "runtime_pinned", RW.runtime_pinned);
    if ("runtime_id" in b && b.runtime_id) {
      const rt = s.runtimes.get(b.runtime_id);
      if (!rt || rt.workspace_id !== room.workspace_id) throw validation([{ field: "runtime_id", code: "runtime_not_in_workspace", message: RW.runtime_not_in_workspace }]);
    }
    if (b.default_director_user_id && !s.members.some((m) => m.workspace_id === room.workspace_id && m.user.id === b.default_director_user_id)) {
      throw validation([{ field: "default_director_user_id", code: "not_member", message: RW.default_director_not_member }]);
    }
    const changed: string[] = [];
    if ("runtime_id" in b) {
      extraOf(s, room.id).runtime_id = b.runtime_id ?? null;
      changed.push("runtime_id");
    }
    if (b.isolation) {
      room.isolation = { kind: b.isolation.kind, ...(b.isolation.repo_path ? { repo_path: b.isolation.repo_path } : {}) };
      changed.push("isolation");
    }
    if (b.name !== undefined) (room.name = b.name.trim(), changed.push("name"));
    if (b.description !== undefined) (room.description = b.description.trim(), changed.push("description"));
    if (b.autonomy) (room.autonomy = b.autonomy, changed.push("autonomy"));
    if ("default_director_user_id" in b) (extraOf(s, room.id).default_director_user_id = b.default_director_user_id ?? null, changed.push("default_director_user_id"));
    if (l) {
      const next = { ...room.limits } as Record<string, unknown>;
      for (const k of ["max_concurrent_works", "max_parallel_lanes"] as const) if (l[k] != null) next[k] = l[k];
      for (const k of ["budget_usd", "time_limit"] as const) if (k in l) {
        if (l[k] == null) delete next[k];
        else next[k] = l[k];
      }
      room.limits = next as MockRoom["limits"];
      changed.push("limits");
    }
    const visChanged = b.visibility !== undefined && b.visibility !== room.visibility;
    if (visChanged) room.visibility = b.visibility!;
    if (!changed.length && !visChanged) return ok(roomOut(s, room, user.id));
    room.updated_at = now();
    const who = name(s, user.id);
    if (visChanged) systemPost(s, room, who + (room.visibility === "invited" ? RW.sys_vis_invited : RW.sys_vis_workspace));
    if (changed.length) systemPost(s, room, who + RW.sys_settings);
    ctx.emitRoom(s, room);
    return ok(roomOut(s, room, user.id));
  });

  // ── 참고 방 링크 ──────────────────────────────────────────────────────
  const linkOut = (s: Store, l: RD["links"][number]): RoomLink => {
    const t = s.rooms.get(l.target_room_id);
    const u = s.users.get(l.created_by);
    const lastAct = [...s.messages.values()].filter((m) => m.session_id === l.target_room_id).map((m) => m.created_at).sort().pop() ?? null;
    return {
      id: l.id, room_id: l.room_id, target_room: { id: l.target_room_id, name: t?.name ?? "", description: t?.description ?? "", last_activity_at: lastAct },
      created_by: u ? stripUser(u) : ({ id: l.created_by, email: "", display_name: "", created_at: l.created_at } as User), created_at: l.created_at,
    };
  };
  on("GET", "/rooms/{id}/links", (req, p) => {
    const s = store();
    const { room } = gate(s, req, p.id, "view");
    const items = rd(s).links.filter((l) => l.room_id === room.id).sort((a, b) => b.created_at.localeCompare(a.created_at)).map((l) => linkOut(s, l));
    return ok({ items });
  });
  const announceLink = (s: Store, src: MockRoom, dst: MockRoom, actor: string, l: RD["links"][number], action: "created" | "deleted") => {
    const who = name(s, actor);
    if (action === "created") {
      systemPost(s, src, who + RW.sys_invited_mid + dst.name + RW.sys_link_src);
      systemPost(s, dst, who + RW.sys_link_dst_head + src.name + RW.sys_link_dst_tail);
    } else {
      systemPost(s, src, who + RW.sys_unlink_src_head + dst.name + RW.sys_unlink_src_tail);
      systemPost(s, dst, who + RW.sys_invited_mid + src.name + RW.sys_unlink_dst_tail);
    }
    const link = linkOut(s, l);
    for (const r of [src, dst]) emit(s, r.workspace_id, "room_link.updated", { room_id: r.id, action, link }, r.id);
  };
  on("POST", "/rooms/{id}/links", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "link");
    const target = body<{ target_room_id?: string }>(req).target_room_id ?? "";
    if (target === room.id) throw validation([{ field: "target_room_id", code: "self", message: RW.link_self }]);
    const t = s.rooms.get(target);
    if (!t || t.workspace_id !== room.workspace_id || !t.people.some((x) => x.user_id === user.id)) throw new Problem(403, "not_participant_of_target", RW.not_participant_of_target);
    if (rd(s).links.some((l) => l.room_id === room.id && l.target_room_id === target)) throw new Problem(409, "already_linked", RW.already_linked);
    const l = { id: uuid(), room_id: room.id, target_room_id: target, created_by: user.id, created_at: now() };
    rd(s).links.push(l);
    announceLink(s, room, t, user.id, l, "created");
    return ok(linkOut(s, l), 201);
  });
  on("DELETE", "/rooms/{id}/links/{lid}", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "link");
    const l = rd(s).links.find((x) => x.id === p.lid && (x.room_id === room.id || x.target_room_id === room.id));
    if (!l) throw notFoundP("room_link");
    const src = s.rooms.get(l.room_id)!;
    const dst = s.rooms.get(l.target_room_id)!;
    if (src && dst) announceLink(s, src, dst, user.id, l, "deleted");
    rd(s).links = rd(s).links.filter((x) => x.id !== l.id);
    return { status: 204 };
  });

  // ── 맥락 읽기 기록 ────────────────────────────────────────────────────
  const readOut = (s: Store, r: StoredRead, direction: RoomRead["direction"]): RoomRead => {
    const other = direction === "in" ? r.room_id : r.target_room_id;
    const u = r.originator_user_id ? s.users.get(r.originator_user_id) : undefined;
    // 거부 행은 대상 방을 지운다(존재 숨김) — 요청자가 그 방을 떠난 경우만 예외(FR-4.5).
    const reveal = direction !== "denied" || r.denied_reason === "originator_left";
    return {
      id: r.id, at: r.at, direction, agent: r.agent, originator_user: u ? stripUser(u) : null,
      other_room: reveal ? { id: other, name: s.rooms.get(other)?.name ?? "" } : null,
      scope: { summary: r.summary, recent_n: r.recent_n }, truncated: r.truncated, task_id: r.task_id, denied_reason: r.allowed ? null : r.denied_reason,
    };
  };
  on("GET", "/rooms/{id}/reads", (req, p) => {
    const s = store();
    ctx.syncRooms(s);
    const user = ctx.requireUser(s, req);
    const room = s.rooms.get(p.id);
    if (!room || !ctx.roomDecide("view", ctx.standingOf(s, room, user.id))) throw new Problem(404, "not_found", RW.read_room_not_found);
    const dir = req.query.get("direction");
    if (dir && !["out", "in", "denied"].includes(dir)) throw validation([{ field: "direction", code: "invalid", message: RW.read_direction }]);
    const agent = req.query.get("agent_id");
    const since = req.query.get("since");
    const rows: RoomRead[] = [];
    for (const r of rd(s).reads) {
      if (r.room_id === room.id && r.allowed) rows.push(readOut(s, r, "out"));
      if (r.target_room_id === room.id && r.allowed) rows.push(readOut(s, r, "in"));
      if (r.room_id === room.id && !r.allowed) rows.push(readOut(s, r, "denied"));
    }
    const items = rows
      .filter((r) => !dir || r.direction === dir)
      .filter((r) => !agent || r.agent.id === agent)
      .filter((r) => !since || r.at >= since)
      .sort((a, b) => b.at.localeCompare(a.at) || b.id.localeCompare(a.id));
    return ok({ items, next_cursor: null });
  });

  // ── 미션 열기 ─────────────────────────────────────────────────────────
  const DEFAULT_WITH: CompletionCondition = { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }] };
  const DEFAULT_WITHOUT: CompletionCondition = { op: "and", conditions: [{ type: "user_approval" }] };
  const atoms = (cc: CompletionCondition) => ("conditions" in cc ? cc.conditions.filter((c) => "type" in c) : [cc]) as { type: string }[];
  const isMember = (s: Store, room: MockRoom, userId: string) => s.members.some((m) => m.workspace_id === room.workspace_id && m.user.id === userId);

  /** 서버 `openWork` 순서 — 검증(422) → 보관(409) → 멈춤(409) → 동시 상한(409) → Director·deputy → 담당 → 종료 조건 → 원 메시지. */
  const openWork = (s: Store, room: MockRoom, user: User, b: WorkCreate): Work => {
    const goal = (b.goal ?? "").trim();
    const errors: { field: string; code?: string; message: string }[] = [];
    if (!goal) errors.push({ field: "goal", code: "required", message: RW.goal_required });
    const title = b.title?.trim() || goal.split("\n")[0].trim();
    if ([...title].length > 200) errors.push({ field: "title", code: "length", message: RW.title_200 });
    if (b.autonomy === "supervised") errors.push({ field: "autonomy", code: "unsupported", message: RW.supervised_unsupported });
    if (b.limits?.budget_usd != null && b.limits.budget_usd < 0) errors.push({ field: "limits/budget_usd", code: "minimum", message: RW.work_budget_min });
    if (b.limits?.time_limit && !/^P(?:\d+D)?(?:T(?:\d+H)?(?:\d+M)?(?:\d+S)?)?$/.test(b.limits.time_limit)) errors.push({ field: "limits/time_limit", code: "invalid", message: RW.work_time_invalid });
    if (errors.length) throw validation(errors);
    if (room.status === "archived") throw new Problem(409, "room_archived", W.room_archived);
    if (room.blocked_reason) throw new Problem(409, "room_blocked", RW.room_blocked_open);
    if (!b.draft) {
      const limit = room.limits?.max_concurrent_works ?? 3;
      const open = worksOf(s, room).filter((w) => RUNNING.has(w.status)).map((w) => ({ id: w.id, title: w.title, status: w.status }));
      if (open.length >= limit) throw new Problem(409, "max_concurrent_works", RW.max_concurrent + limit + RW.max_concurrent_tail, { open_works: open });
    }
    const director = b.director_user_id ?? extraOf(s, room.id).default_director_user_id ?? user.id;
    const deputy = b.deputy_user_id ?? null;
    for (const id of [director, deputy]) if (id && !isMember(s, room, id)) throw validation([{ field: "director_user_id", code: "not_member", message: RW.director_not_member }]);
    const agentIds = agentRows(s, room).map((r) => r.agent_id);
    const assignee = b.assignee_agent_id ?? null;
    if (assignee && !agentIds.includes(assignee)) throw validation([{ field: "assignee_agent_id", code: "not_participant", message: RW.assignee_not_participant }]);
    let cond: CompletionCondition = assignee ? DEFAULT_WITH : DEFAULT_WITHOUT;
    if (b.completion_condition) {
      const errs = ctx.validateCondition(b.completion_condition, agentIds);
      if (errs.length) throw validation(errs);
      cond = b.completion_condition;
    }
    if (b.from_message_id) {
      const m = s.messages.get(b.from_message_id);
      if (!m || m.session_id !== room.id) throw validation([{ field: "from_message_id", code: "not_in_room", message: W.room_not_in_room }]);
      const owner = rd(s).msgWork.get(m.id);
      if (owner) throw new Problem(409, "message_has_work", RW.message_has_work, { work_id: owner });
    }
    const t = now();
    const u = (id: string | null) => (id && s.users.get(id) ? stripUser(s.users.get(id)!) : undefined);
    const total = atoms(cond).length;
    const w: Work = {
      id: uuid(), room_id: room.id, title, goal, acceptance_criteria: b.acceptance_criteria ?? [], director_user_id: director, director: u(director),
      deputy_user_id: deputy, ...(deputy ? { deputy: u(deputy) } : {}), assignee_agent_id: assignee, completion_condition: cond,
      completion_progress: { met: 0, total, satisfied: false, conditions: [] } as Work["completion_progress"], limits: b.limits ?? {}, autonomy: b.autonomy ?? room.autonomy,
      status: b.draft ? "draft" : "active", paused_reason: null, cost_usd: 0, cost_estimated: false, summary_message_id: null,
      opened_from_message_id: b.from_message_id ?? null, my_work_role: director === user.id ? "director" : deputy === user.id ? "deputy" : "member",
      created_by: user.id, created_at: t, updated_at: t, started_at: b.draft ? null : t, finished_at: null, last_activity_at: t,
    };
    { const { director: _d, deputy: _p, my_work_role: _r, subscription: _s, ...row } = w; s.works.set(w.id, row as MockWork); }
    if (b.from_message_id) {
      rd(s).msgWork.set(b.from_message_id, w.id);
      // 사후 귀속(FR-3.1.1) — 원 메시지가 미션 밖이었으면 이 미션으로(타임라인 라벨·칩 거르기가 같은 칸을 본다).
      const m = s.messages.get(b.from_message_id);
      if (m && m.work_id == null) m.work_id = w.id;
    }
    systemPost(s, room, name(s, user.id) + RW.sys_work_opened + goal, w.id);
    emit(s, room.workspace_id, "work.created", worksOf(s, room).find((x) => x.id === w.id), room.id);
    ctx.emitRoom(s, room);
    return w;
  };
  on("POST", "/rooms/{id}/works", (req, p) => {
    const s = store();
    const { room, user } = gate(s, req, p.id, "open_work");
    return ok(openWork(s, room, user, body<WorkCreate>(req)), 201);
  });

  // ── 미션 제안 ─────────────────────────────────────────────────────────
  const proposalRoom = (s: Store, req: Req, id: string) => {
    const prop = rd(s).proposals.find((x) => x.id === id);
    if (!prop) throw notFoundP("work_proposal");
    try {
      return { prop, ...gate(s, req, prop.room_id, "proposals") };
    } catch (e) {
      // 볼 수 없는 방의 제안은 없는 제안과 같다(서버 proposalGate).
      if ((e as { status?: number }).status === 404) throw notFoundP("work_proposal");
      throw e;
    }
  };
  on("GET", "/rooms/{id}/work-proposals", (req, p) => {
    const s = store();
    const { room } = gate(s, req, p.id, "proposals");
    const status = req.query.get("status");
    const items = rd(s).proposals.filter((x) => x.room_id === room.id && (!status || x.status === status)).sort((a, b) => b.created_at.localeCompare(a.created_at));
    return ok({ items, next_cursor: null });
  });
  on("GET", "/work-proposals/{id}", (req, p) => ok(proposalRoom(store(), req, p.id).prop));
  on("POST", "/work-proposals/{id}/resolution", (req, p) => {
    const s = store();
    const { prop, room, user, f } = proposalRoom(s, req, p.id);
    const b = body<{ action?: string; work?: WorkCreate; reason?: string }>(req);
    if (b.action !== "accept" && b.action !== "reject") throw validation([{ field: "action", code: "enum", message: RW.action_enum }]);
    if (b.action === "accept" && !decide("open_work", f)) throw deny("open_work", f);
    if (prop.status !== "open") {
      throw new Problem(409, "already_resolved", RW.already_resolved, { status: prop.status, decided_by: prop.decided_by?.id ?? null, decided_at: prop.decided_at, work_id: prop.work_id });
    }
    let work: Work | undefined;
    const t = now();
    if (b.action === "accept") {
      const wc: WorkCreate = { ...(b.work ?? { goal: prop.goal }) };
      if (!wc.goal?.trim()) wc.goal = prop.goal;
      // 연 사람이 Director — 방 기본값도, 제안한 에이전트도 아니다.
      work = openWork(s, room, user, { ...wc, director_user_id: user.id, from_proposal_id: prop.id });
      Object.assign(prop, { status: "accepted", decided_by: stripUser(user), decided_at: t, work_id: work.id });
    } else {
      const reason = b.reason?.trim() ?? "";
      Object.assign(prop, { status: "rejected", decided_by: stripUser(user), decided_at: t, reject_reason: reason || null });
      systemPost(s, room, name(s, user.id) + RW.sys_invited_mid + prop.agent.name + RW.sys_rejected_mid + prop.goal.split("\n")[0].trim() + RW.sys_rejected_tail + (reason ? RW.sys_reject_reason + reason : ""));
    }
    emit(s, room.workspace_id, "work_proposal.resolved", { room_id: room.id, proposal_id: prop.id, action: b.action, work_id: work?.id ?? null }, room.id);
    return ok({ proposal: prop, ...(work ? { work } : {}) });
  });

  // ── 목 전용 시드(`__mock`) — 실서버에서는 에이전트·데몬이 만드는 상태 ─────────
  /** 맥락 읽기 기록 한 줄 — `room`(읽은 쪽) → `target`. `denied_reason` 이 있으면 거부 행. */
  on("POST", "/__mock/rooms/{id}/reads", (req, p) => {
    const s = store();
    ctx.syncRooms(s);
    const b = body<{ target_room_id: string; agent_id?: string; originator_email?: string | null; denied_reason?: RoomRead["denied_reason"]; summary?: boolean; recent_n?: number; truncated?: boolean; age_ms?: number }>(req);
    const a = b.agent_id ? s.agents.get(b.agent_id) : [...s.agents.values()][0];
    const origin = b.originator_email === null ? null : [...s.users.values()].find((u) => u.email === (b.originator_email ?? "demo@colab.dev"));
    const r: StoredRead = {
      id: uuid(), at: new Date(Date.now() - (b.age_ms ?? 0)).toISOString(), room_id: p.id, target_room_id: b.target_room_id,
      agent: { id: a?.id ?? uuid(), name: a?.name ?? "Agent" }, originator_user_id: origin?.id ?? null, allowed: !b.denied_reason, denied_reason: b.denied_reason ?? null,
      summary: b.summary ?? true, recent_n: b.recent_n ?? 20, truncated: !!b.truncated, task_id: uuid(),
    };
    rd(s).reads.push(r);
    const room = s.rooms.get(p.id);
    if (room) emit(s, room.workspace_id, "room_read.recorded", { room_id: p.id, direction: r.allowed ? "out" : "denied", entry: readOut(s, r, r.allowed ? "out" : "denied") }, p.id);
    return ok({ id: r.id }, 201);
  });
  /** 에이전트의 미션 제안 하나(`colab work propose`). */
  on("POST", "/__mock/rooms/{id}/work-proposals", (req, p) => {
    const s = store();
    ctx.syncRooms(s);
    const room = s.rooms.get(p.id);
    if (!room) throw notFoundP("room");
    const b = body<{ goal: string; rationale: string; agent_id?: string; trigger_message_id?: string | null }>(req);
    const a = (b.agent_id ? s.agents.get(b.agent_id) : undefined) ?? s.agents.get(agentRows(s, room)[0]?.agent_id ?? "") ?? [...s.agents.values()][0];
    const prop: WorkProposal = {
      id: uuid(), room_id: room.id, agent: { id: a?.id ?? uuid(), name: a?.name ?? "Lead" }, proposed_by_task_id: uuid(), goal: b.goal, rationale: b.rationale,
      trigger_message_id: b.trigger_message_id ?? null, status: "open", decided_by: null, decided_at: null, reject_reason: null, work_id: null, created_at: now(),
    };
    rd(s).proposals.push(prop);
    emit(s, room.workspace_id, "work_proposal.created", { room_id: room.id, proposal: prop }, room.id);
    return ok(prop, 201);
  });
  /** 방 컴퓨터 — 고르기(첫 실행 전) · 고정(첫 dispatch 가 났다). */
  on("POST", "/__mock/rooms/{id}/runtime", (req, p) => {
    const s = store();
    ctx.syncRooms(s);
    const b = body<{ runtime_id?: string | null; pinned?: boolean }>(req);
    const e = extraOf(s, p.id);
    if ("runtime_id" in b) e.runtime_id = b.runtime_id ?? null;
    if (b.pinned !== undefined) e.pinned = b.pinned;
    const room = s.rooms.get(p.id);
    if (room) ctx.emitRoom(s, room);
    return ok({ ok: true });
  });
}
