/**
 * 목 op — 미션 설정 편집·조건 고치기(`updateWork` PATCH /works/{id}) · Director 교체(`changeWorkDirector` PUT /works/{id}/director), T-R2-W4b.
 *
 * 서버 `handlers_works.go` UpdateWork · ChangeWorkDirector 를 흉내 낸다 — 권한(그 미션의 director / + ws owner·admin) · 끝난 미션 409 ·
 * 칸 검증 422 · 담당은 방 참여 에이전트 · 종료 조건은 리뷰어 검사(`validateCondition`) · 교체 시스템 메시지.
 * 문장은 `WE_SERVER`(이 파일)와 이미 있는 표(`W`·`RW`)에서만 온다 — `server-wording/k-work-edit.test.ts` 가 글자 단위로 대조한다.
 *
 * **handlers.ts 에는 등록 한 줄만 둔다**(`registerWorkEdit(…)`) — 다른 웹 워커가 같은 시기에 handlers.ts 를 고친다.
 * 참여 에이전트 목록은 방 다이얼로그 목(`./rooms-dialogs.ts`)만 안다 — 그 op(`GET /rooms/{id}/participants`)를 `dispatch` 로 불러 쓴다.
 * 옛 세션의 미션(미션 id = 세션 id)은 옛 op(`PATCH /sessions/{id}`)로 넘겨 진행률 계산을 그쪽에 맡긴다.
 */
import type { CompletionCondition, CompletionProgress, RoomParticipant, User } from "@/lib/api/types";
import type { components } from "@/lib/api/schema";
import { emit, now, store, stripUser, uuid, type MockRoom, type MockWork, type Store } from "./store";
import { VALIDATION_DETAIL, W, type ServerSentence } from "./wording";
import { RW } from "./rooms-dialogs-wording";
import type { Req, Res } from "./handlers";

type WorkUpdate = components["schemas"]["WorkUpdate"];
type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;
type WorkRow = MockWork & { legacy: boolean };

/** 서버 문장 — 이 op 들에서만 쓰는 것. 이미 `SERVER`·`RD_SERVER` 에 있는 것은 거기서 가져다 쓴다(두 벌 금지). */
export const WE_SERVER = {
  work_closed_edit: { text: "끝난 미션은 고칠 수 없습니다", at: "internal/httpapi/handlers_works.go" },
  condition_completing: { text: "끝나는 중인 미션의 종료 조건은 바꿀 수 없습니다", at: "internal/httpapi/handlers_works.go" },
  change_director_role: { text: "현재 Director 나 소유자·관리자만 교체할 수 있습니다", at: "internal/httpapi/handlers_works.go" },
  sys_director_tail: { text: " 님이 이 미션의 Director 가 되었습니다.", at: "internal/httpapi/handlers_works.go" },
} satisfies Record<string, ServerSentence>;
const WE = Object.fromEntries(Object.entries(WE_SERVER).map(([k, v]) => [k, v.text])) as { [K in keyof typeof WE_SERVER]: string };

export interface WorkEditCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  dispatch: (req: Req) => Promise<Res>;
  workGate: (s: Store, req: Req, workId: string) => { w: WorkRow; room: MockRoom; user: User };
  workView: (s: Store, id: string) => WorkRow | null;
  toWork: (s: Store, w: WorkRow, userId: string) => unknown;
  /** 상태를 바꾸고 `work.updated`(닫히면 `work.closed`)를 낸다 — 빈 patch 면 알림만. */
  setWork: (s: Store, id: string, patch: Partial<MockWork>) => void;
  validateCondition: (cc: CompletionCondition, participantIds: string[]) => { field: string; code: string; message: string }[];
}

const body = <T,>(req: Req) => (req.body ?? {}) as T;
const ok = (b: unknown, status = 200): Res => ({ status, body: b });
const CLOSED = new Set(["completed", "cancelled"]);
const isMember = (s: Store, room: MockRoom, userId: string) => s.members.some((m) => m.workspace_id === room.workspace_id && m.user.id === userId);
const TIME = /^P(?:\d+D)?(?:T(?:\d+H)?(?:\d+M)?(?:\d+S)?)?$/;

/**
 * 새 미션의 진행률 — 원자마다 한 행. 리뷰어 없는 `agent_approval` 은 `reviewer_missing`, 방에 없는 리뷰어는 `reviewer_not_participant`
 * (서버 `sessions.Progress` 의 blocked_reason 두 값). 이미 충족된 원자는 같은 자리(`type`·`agent_id`)면 그대로 둔다(계약: 고쳐도 유지).
 */
export function workProgress(cc: CompletionCondition, agentIds: string[], agentName: (id: string) => string | undefined, prev?: CompletionProgress | null): CompletionProgress {
  const atoms = ("conditions" in cc ? cc.conditions.filter((c) => "type" in c) : [cc]) as { type: string; agent_id?: string | null }[];
  const conditions = atoms.map((a, i) => {
    const kept = prev?.conditions.find((c) => c.met && c.type === a.type && (c.agent_id ?? null) === (a.agent_id ?? null));
    const blocked = a.type === "agent_approval" ? (!a.agent_id ? "reviewer_missing" : !agentIds.includes(a.agent_id) ? "reviewer_not_participant" : null) : null;
    return {
      path: "conditions" in cc ? `/conditions/${i}` : "",
      type: a.type,
      met: !!kept,
      ...(kept ? { met_at: kept.met_at, met_by: kept.met_by } : {}),
      ...(a.agent_id ? { agent_id: a.agent_id, agent_name: agentName(a.agent_id) } : {}),
      ...(blocked ? { blocked_reason: blocked } : {}),
    };
  }) as CompletionProgress["conditions"];
  const met = conditions.filter((c) => c.met).length;
  const op = "conditions" in cc ? cc.op : "and";
  return { met, total: conditions.length, satisfied: op === "or" ? met > 0 : met === conditions.length && conditions.length > 0, human_gate: atoms.some((a) => a.type === "user_approval" || a.type === "manual"), conditions };
}

export function registerWorkEdit(ctx: WorkEditCtx): void {
  const { on, Problem } = ctx;
  const validation = (errors: { field: string; code?: string; message: string }[]) => new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors });

  /** 방의 참여 에이전트 — 방 다이얼로그 목의 op 를 같은 사람으로 부른다(참여자 목록의 정본이 거기 있다). */
  const roomAgents = async (req: Req, roomId: string): Promise<{ id: string; name: string }[]> => {
    const r = await ctx.dispatch({ ...req, method: "GET", path: `/rooms/${roomId}/participants`, query: new URLSearchParams(), body: undefined });
    const items = ((r.body as { items?: RoomParticipant[] } | undefined)?.items ?? []).filter((p) => p.kind === "agent" && !p.left_at && p.agent);
    return items.map((p) => ({ id: p.agent!.id, name: p.agent!.name }));
  };
  const systemPost = (s: Store, room: MockRoom, content: string, workId: string) => {
    const m = {
      id: uuid(), session_id: room.id, parent_id: null, source_task_id: null, lane_id: null, state: "posted", reply_count: 0, is_note: false,
      author_type: "system", author_id: null, kind: "system", content, mentions: [], created_at: now(), edited_at: null, work_id: workId,
    };
    s.messages.set(m.id, m as never);
    emit(s, room.workspace_id, "message.created", m, room.id);
  };

  /** 서버 UpdateWork 순서 — 미션·방 열람(404) → Director(403) → 한도 검증(422) → 끝난 미션(409) → 칸마다(제목·목표·자율성·담당·deputy·종료 조건). */
  on("PATCH", "/works/{id}", async (req, p) => {
    const s = store();
    const { w, room, user } = ctx.workGate(s, req, p.id);
    if (w.director_user_id !== user.id) throw new Problem(403, "director_required", W.work_director_required);
    const b = body<WorkUpdate>(req);
    if (b.limits?.budget_usd != null && b.limits.budget_usd < 0) throw validation([{ field: "limits/budget_usd", code: "minimum", message: RW.work_budget_min }]);
    if (b.limits?.time_limit && !TIME.test(b.limits.time_limit)) throw validation([{ field: "limits/time_limit", code: "invalid", message: RW.work_time_invalid }]);
    if (CLOSED.has(w.status)) throw new Problem(409, "work_closed", WE.work_closed_edit);
    const title = b.title !== undefined ? b.title.trim() : undefined;
    if (title !== undefined && (!title || [...title].length > 200)) throw validation([{ field: "title", code: "length", message: W.title_1_200 }]);
    const goal = b.goal !== undefined ? b.goal.trim() : undefined;
    if (goal !== undefined && !goal) throw validation([{ field: "goal", code: "required", message: RW.goal_required }]);
    if (b.autonomy === "supervised") throw validation([{ field: "autonomy", code: "unsupported", message: RW.supervised_unsupported }]);
    const agents = await roomAgents(req, room.id);
    const agentIds = agents.map((a) => a.id);
    if (b.assignee_agent_id && !agentIds.includes(b.assignee_agent_id)) throw validation([{ field: "assignee_agent_id", code: "not_participant", message: RW.assignee_not_participant }]);
    if (b.deputy_user_id && !isMember(s, room, b.deputy_user_id)) throw validation([{ field: "user_id", code: "not_member", message: RW.default_director_not_member }]);
    if (b.completion_condition) {
      if (w.status === "completing") throw validation([{ field: "completion_condition", code: "immutable", message: WE.condition_completing }]);
      const errs = ctx.validateCondition(b.completion_condition, agentIds);
      if (errs.length) throw validation(errs);
    }

    if (w.legacy) {
      // 옛 세션의 미션 — 세션 op 가 진행률·`session.*` 알림까지 한다. 담당은 옛 세션 칸이 아니라 여기서 옮긴다.
      const r = await ctx.dispatch({
        ...req, method: "PATCH", path: `/sessions/${w.id}`,
        body: {
          ...(title !== undefined ? { title } : {}), ...(goal !== undefined ? { goal } : {}),
          ...(b.acceptance_criteria !== undefined ? { acceptance_criteria: b.acceptance_criteria } : {}),
          ...(b.autonomy !== undefined ? { autonomy: b.autonomy } : {}), ...(b.limits ? { limits: b.limits } : {}),
          ...(b.deputy_user_id !== undefined ? { deputy_director_user_id: b.deputy_user_id } : {}),
          ...(b.completion_condition ? { completion_condition: b.completion_condition } : {}),
        },
      });
      if (r.status >= 400) return r;
      const sess = s.sessions.get(w.id);
      if (sess && b.assignee_agent_id !== undefined) sess.assignee_agent_id = b.assignee_agent_id;
    } else {
      const row = s.works.get(w.id)!;
      if (title !== undefined) row.title = title;
      if (goal !== undefined) row.goal = goal;
      if (b.acceptance_criteria !== undefined) row.acceptance_criteria = b.acceptance_criteria;
      if (b.autonomy !== undefined) row.autonomy = b.autonomy;
      if (b.limits) row.limits = { ...row.limits, ...b.limits };
      if (b.assignee_agent_id !== undefined) row.assignee_agent_id = b.assignee_agent_id;
      if (b.deputy_user_id !== undefined) row.deputy_user_id = b.deputy_user_id;
      if (b.completion_condition) {
        row.completion_condition = b.completion_condition;
        row.completion_progress = workProgress(b.completion_condition, agentIds, (id) => agents.find((a) => a.id === id)?.name, row.completion_progress);
        emit(s, room.workspace_id, "work.completion_progress", { work_id: row.id, room_id: room.id, completion_progress: row.completion_progress }, room.id);
      }
    }
    ctx.setWork(s, w.id, {});
    return ok(ctx.toWork(s, ctx.workView(s, w.id)!, user.id));
  });

  /** 서버 ChangeWorkDirector — 현재 Director · ws owner·admin(403) → 새 Director·deputy 는 워크스페이스 멤버(422) → 시스템 메시지. */
  on("PUT", "/works/{id}/director", (req, p) => {
    const s = store();
    const { w, room, user } = ctx.workGate(s, req, p.id);
    const role = s.members.find((m) => m.workspace_id === room.workspace_id && m.user.id === user.id)?.role;
    if (w.director_user_id !== user.id && role !== "owner" && role !== "admin") throw new Problem(403, "director_required", WE.change_director_role);
    const b = body<{ director_user_id?: string; deputy_user_id?: string | null }>(req);
    const to = b.director_user_id ?? "";
    if (!isMember(s, room, to)) throw validation([{ field: "user_id", code: "not_member", message: RW.default_director_not_member }]);
    if (b.deputy_user_id && !isMember(s, room, b.deputy_user_id)) throw validation([{ field: "user_id", code: "not_member", message: RW.default_director_not_member }]);
    const next = s.users.get(to)!;
    if (w.legacy) {
      const sess = s.sessions.get(w.id)!;
      sess.director_user_id = to;
      sess.director = stripUser(next);
      if (b.deputy_user_id !== undefined) sess.deputy_director_user_id = b.deputy_user_id;
    } else {
      const row = s.works.get(w.id)!;
      row.director_user_id = to;
      if (b.deputy_user_id !== undefined) row.deputy_user_id = b.deputy_user_id;
    }
    systemPost(s, room, (next.display_name || next.email) + WE.sys_director_tail, w.id);
    ctx.setWork(s, w.id, {});
    return ok(ctx.toWork(s, ctx.workView(s, w.id)!, user.id));
  });

  /**
   * 목 전용 — 스크린샷·테스트가 「조건 고치기」 장면을 만든다: 새 미션의 종료 조건을 리뷰어 없는 `agent_approval`(옛 모양)로 놓는다.
   * 서버에서는 v0.1.4 전에 만들어진 조건이 이 모양이다(지금 op 는 422 로 막는다).
   */
  on("POST", "/__mock/works/{id}/seed-reviewerless", async (req, p) => {
    const s = store();
    const { w, room } = ctx.workGate(s, req, p.id);
    const row = s.works.get(w.id);
    if (!row) return ok({}, 204);
    const cc: CompletionCondition = { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval" }] };
    const agents = await roomAgents(req, room.id);
    row.completion_condition = cc;
    row.completion_progress = workProgress(cc, agents.map((a) => a.id), (id) => agents.find((a) => a.id === id)?.name);
    ctx.setWork(s, w.id, {});
    return ok({}, 204);
  });

  /**
   * 목 전용 — 방 멈춤 배너의 다음 권한자(0.2.8 `next_approver`·`next_approver_role`)를 멈춘 방에 얹는다. 실서버는 위임 경로(방장 → 부방장 →
   * ws owner 최고참)에서 계산한다 — 목의 `blockRoom`·시드는 그 계산이 없어 스크린샷·테스트가 여기로 놓는다.
   */
  on("POST", "/__mock/rooms/{id}/seed-next-approver", (req, p) => {
    const s = store();
    const room = s.rooms.get(p.id);
    const b = body<{ email?: string; role?: "room_deputy" | "workspace_owner" | null }>(req);
    const u = [...s.users.values()].find((x) => x.email === b.email);
    if (!room?.blocked_detail) return ok({}, 204);
    Object.assign(room.blocked_detail, { next_approver: u ? stripUser(u) : null, next_approver_role: u ? b.role ?? null : null });
    return ok({}, 204);
  });
}
