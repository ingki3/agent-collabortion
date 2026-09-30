/**
 * 목 — 작업 카드(PRD FR-3.8 · openapi v0.3.10 태그 `cards` · SCREEN §4.6 「작업 카드」·「분담표」).
 *
 * op 다섯: `listRoomCards`(GET /rooms/{id}/cards) · `getCard`(GET /cards/{id}, 지난 판 포함) · `submitCardResult`(POST /cards/{id}/result) ·
 * `acceptCard`(POST /cards/{id}/accept) · `reviseCard`(POST /cards/{id}/revise). 저장은 이 모듈이 쥔다(`Store` 옆 WeakMap — 기존 등록 모듈과 같은 모양).
 * 위임(`delegateLane` 카드 본문)은 에이전트 TaskToken op 라 목 화면이 부르지 않는다 — 시드가 같은 순서(`delegate`)를 탄다:
 *   카드 번호(미션 안 1부터) → 새 lane + 첫 task → 위임 카드 말풍선(`speech: delegate`, `card_role: delegation`, 멘션 = 담당) → `card.created`.
 * 서버가 하는 대로: 근거 없는 `met` 은 `partial` 로 낮춰 저장(`downgraded`), 결과 카드 말풍선은 `speech: report`·↩ 그 판의 위임 카드 말풍선,
 * 판정·수정 요청은 `card.updated` + 결과 말풍선 `message.updated`, 수정 요청은 같은 lane 에 새 task · 새 판 위임 카드 말풍선.
 * `actions` 는 **보는 사람마다**(미션 Director·deputy — 미션 밖이면 방장·부방장) — 방송(`card.*`)에는 빈 목록을 싣는다.
 *
 * 시드 `POST /__mock/rooms/{id}/seed-cards` `{ work_id? }` — Pencil `S7-K` 의 마리오 카트 방: C-1 수락(Researcher) · C-2 자동 결과(Designer) ·
 * C-3 판정 대기 + 「부분 · 근거 없음」(Developer) · └ C-4 하위 카드 진행 중(Developer) · C-5 2판 수정 요청 진행 중(Writer, 지워진 참고 칩) ·
 * 카드 없는 에이전트 멘션 ‹질문›(Writer → Researcher)과 그 ‹답›. 응답 `{ work_id, cards: {C-1: id, …}, messages: {…} }`.
 */
import type { CardEvidence, Lane, Message, TaskCard, User } from "@/lib/api/types";
import type { Session } from "@/lib/legacy-session";
import { emit, makeAgent, now, store, uuid, type MockTask, type Store } from "./store";
import type { Req, Res } from "./handlers";
import type { SpeechPremises } from "./speech";
import { storeArtifact } from "./media";
import { CARD_MOCK, CARD_SEED, VALIDATION_DETAIL } from "./wording";

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

export interface CardsCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  sessionOf: (s: Store, req: Req, id: string) => Session;
  requireUser: (s: Store, req: Req) => User;
  addMessage: (s: Store, sess: Session, m: Partial<Message> & Pick<Message, "author_type" | "author_id" | "kind" | "content" | "mentions">, speech?: SpeechPremises) => Message;
  createTask: (s: Store, sess: Session, agentId: string, triggerId: string | null, opts?: { laneId?: string; brief?: string | null; workId?: string | null }) => MockTask;
  setLaneStatus: (s: Store, sess: Session, laneId: string, patch: Partial<Lane>) => void;
  parseMentions: (content: string) => Message["mentions"];
  /** 미션의 Director·deputy(없는 미션이면 null). */
  workPeople: (s: Store, workId: string) => { director: string | null; deputy: string | null; roomId: string } | null;
  /** 방의 미션 중 열린 첫 미션(시드 기본값). */
  openWorkOf: (s: Store, roomId: string) => string | null;
}

type Stored = Omit<TaskCard, "actions" | "refs"> & {
  refs: { kind: "artifact" | "decision" | "message"; id: string; label: string }[];
  versions: Record<string, unknown>[];
};
const STATE = new WeakMap<Store, Map<string, Stored>>();
const cardsOf = (s: Store) => {
  let m = STATE.get(s);
  if (!m) STATE.set(s, (m = new Map()));
  return m;
};

type Criterion = [text: string, method: string];
type Agentish = { id: string; name: string; role?: string };

export function registerCards(ctx: CardsCtx): void {
  const { on, Problem, sessionOf, requireUser, addMessage, createTask, setLaneStatus, parseMentions } = ctx;
  const ok = (b: unknown, status = 200): Res => ({ status, body: b });
  const link = (a: Agentish) => `[@${a.name}](mention://agent/${a.id})`;

  /** 참고 자료의 `missing` 은 읽을 때 판정한다(대상이 지워졌는가). */
  const refsOut = (s: Store, c: Stored): TaskCard["refs"] =>
    c.refs.map((r) => ({
      ...r,
      missing: r.kind === "artifact" ? !s.artifacts.has(r.id) : r.kind === "decision" ? !s.decisions.has(r.id) : !s.messages.has(r.id),
    }));
  /** 보는 사람의 동작 — 미션 Director·deputy(미션 밖이면 방장·부방장). */
  const judgeUser = (s: Store, c: Stored, userId: string | null): boolean => {
    if (!userId) return false;
    if (c.work_id) {
      const p = ctx.workPeople(s, c.work_id);
      return !!p && (p.director === userId || p.deputy === userId);
    }
    const r = s.rooms.get(c.room_id);
    return !!r && (r.owner_user_id === userId || r.deputy_owner_user_id === userId);
  };
  const actionsFor = (s: Store, c: Stored, userId: string | null): TaskCard["actions"] =>
    !judgeUser(s, c, userId) ? [] : c.status === "result_submitted" ? ["accept", "revise"] : c.status === "accepted" ? ["revise"] : [];
  const out = (s: Store, c: Stored, userId: string | null, withVersions: boolean): TaskCard => {
    const { versions, ...rest } = c;
    return { ...rest, refs: refsOut(s, c), actions: actionsFor(s, c, userId), ...(withVersions ? { versions } : {}) } as TaskCard;
  };
  const broadcast = (s: Store, c: Stored, type: "card.created" | "card.updated") => {
    const sess = s.sessions.get(c.room_id);
    if (sess) emit(s, sess.workspace_id, type, out(s, c, null, false), c.room_id);
  };
  const touch = (c: Stored) => { c.updated_at = now(); };
  const syncLane = (s: Store, sess: Session, c: Stored) => setLaneStatus(s, sess, c.lane_id, { card_id: c.id, card_label: c.label, card_status: c.status });
  const cardOr404 = (s: Store, req: Req, id: string) => {
    const c = cardsOf(s).get(id);
    if (!c) throw new Problem(404, "not_found", CARD_MOCK.not_found);
    const sess = sessionOf(s, req, c.room_id);
    return { c, sess };
  };

  /** 위임 — 서버 delegateLane(카드 본문)과 같은 차례. 시드 전용 길(TaskToken 없음). */
  function delegate(s: Store, sess: Session, a: { from: Agentish; fromTask: MockTask; to: Agentish; workId: string | null; goal: string; criteria: Criterion[]; boundaries: string; refs?: Stored["refs"]; output?: string | null; budget?: number | null; parent?: Stored | null }): { card: Stored; message: Message; task: MockTask } {
    const all = [...cardsOf(s).values()].filter((x) => x.room_id === sess.id && (x.work_id ?? null) === a.workId);
    const number = all.length + 1;
    const t = createTask(s, sess, a.to.id, null, { brief: a.goal, workId: a.workId });
    setLaneStatus(s, sess, t.lane_id, { delegated_from_task_id: a.fromTask.id, brief: a.goal });
    const at = now();
    const c: Stored = {
      id: uuid(), room_id: sess.id, work_id: a.workId, number, label: `C-${number}`, version: 1, status: "in_progress",
      delegator: { agent_id: a.from.id, name: a.from.name }, assignee: { agent_id: a.to.id, name: a.to.name }, lane_id: t.lane_id,
      parent_card_id: a.parent?.id ?? null, goal: a.goal, criteria: a.criteria.map(([text, method], i) => ({ n: i + 1, text, method: method as TaskCard["criteria"][number]["method"] })),
      boundaries: a.boundaries, refs: a.refs ?? [], output_format: a.output ?? null, budget_usd: a.budget ?? null, revise_reason: null,
      delegate_message_id: null, result: null, judgement: null, follow_ups: 0, versions: [], created_at: at, updated_at: at,
    };
    cardsOf(s).set(c.id, c);
    const m = addMessage(s, sess, {
      author_type: "agent", author_id: a.from.id, author: { name: a.from.name, avatar_url: null }, kind: "text",
      content: `${link(a.to)} ${a.goal}`, mentions: parseMentions(link(a.to)), source_task_id: a.fromTask.id, lane_id: a.fromTask.lane_id,
      work_id: a.workId, card_id: c.id, card_role: "delegation", card_version: 1,
    }, { delegatedLaneId: t.lane_id, delegateTargetId: a.to.id, delegateTargetName: a.to.name });
    c.delegate_message_id = m.id;
    const task = s.tasks.get(t.id)!;
    task.trigger_message_id = m.id;
    syncLane(s, sess, c);
    broadcast(s, c, "card.created");
    return { card: c, message: m, task };
  }

  /** 결과 카드 — 서버 submitCardResult. 근거 없는 met → partial(downgraded). */
  function report(s: Store, sess: Session, c: Stored, task: MockTask | null, b: { summary: string; verdicts: { criterion: number; verdict: "met" | "partial" | "unmet"; evidence?: CardEvidence[]; note?: string | null }[]; confirmed: string[]; assumed: string[]; deviations?: string | null; open_issues?: string | null }, extra: { auto?: boolean; cost?: number | null; duration?: number | null; cardFirst?: boolean } = {}): { message: Message; downgraded: number[] } {
    if (c.status !== "in_progress") throw new Problem(409, "card_not_open", CARD_MOCK.not_open);
    const ns = b.verdicts.map((v) => v.criterion).sort((x, y) => x - y);
    if (ns.length !== c.criteria.length || ns.some((n, i) => n !== i + 1)) {
      throw new Problem(422, "result_card_incomplete", VALIDATION_DETAIL, { errors: [{ field: "verdicts", code: "result_card_incomplete", message: CARD_MOCK.result_incomplete }] });
    }
    const downgraded: number[] = [];
    const verdicts = b.verdicts.map((v) => {
      const ev = v.evidence ?? [];
      const down = v.verdict === "met" && ev.length === 0;
      if (down) downgraded.push(v.criterion);
      return { criterion: v.criterion, verdict: down ? ("partial" as const) : v.verdict, stated_verdict: v.verdict, downgraded: down, evidence: ev, note: v.note ?? null };
    }).sort((x, y) => x.criterion - y.criterion);
    const assignee = { id: c.assignee.agent_id, name: c.assignee.name };
    // 계약은 결과 말풍선(`message.created`)과 `card.updated` 의 순서를 정하지 않는다(openapi StreamEvent) — 목은 둘 다 낸다(#397 B1).
    // 기본은 말풍선 먼저, `extra.cardFirst` 면 카드 먼저(말풍선 id 를 미리 정해 result.message_id 를 채운다).
    const mid = uuid();
    const post = () => addMessage(s, sess, {
      id: mid, author_type: "agent", author_id: assignee.id, author: { name: assignee.name, avatar_url: null }, kind: "text",
      content: `[${c.label} 결과 ${verdicts.filter((v) => v.verdict === "met").length}/${c.criteria.length}] ${extra.auto ? CARD_MOCK.auto_summary : b.summary}`, mentions: [],
      source_task_id: task?.id ?? null, lane_id: c.lane_id, work_id: c.work_id, card_id: c.id, card_role: "result", card_version: c.version,
    }, { cardResult: { respondsTo: c.delegate_message_id ?? "", to: { kind: "agent", id: c.delegator.agent_id, name: c.delegator.name } } });
    const settle = (submittedAt: string) => {
      c.result = {
        summary: b.summary, verdicts, confirmed: b.confirmed, assumed: b.assumed, deviations: b.deviations ?? null, open_issues: b.open_issues ?? null,
        met_count: verdicts.filter((v) => v.verdict === "met").length, auto: !!extra.auto, cost_usd: extra.cost ?? null, duration_s: extra.duration ?? null,
        message_id: mid, submitted_at: submittedAt,
      };
      c.status = "result_submitted";
      touch(c);
      syncLane(s, sess, c);
      broadcast(s, c, "card.updated");
    };
    let m: Message;
    if (extra.cardFirst) {
      settle(now());
      m = post();
    } else {
      m = post();
      settle(m.created_at);
    }
    return { message: m, downgraded };
  }

  function judge(s: Store, sess: Session, c: Stored, by: { kind: "agent" | "user"; id: string; name: string }, action: "accepted" | "revise_requested", reason: string | null) {
    c.judgement = { action, by, at: now(), reason };
    if (c.result?.message_id) {
      const rm = s.messages.get(c.result.message_id);
      if (rm) emit(s, sess.workspace_id, "message.updated", rm, sess.id);
    }
  }
  function accept(s: Store, sess: Session, c: Stored, by: { kind: "agent" | "user"; id: string; name: string }) {
    if (c.status !== "result_submitted") throw new Problem(409, "card_not_judgeable", CARD_MOCK.not_judgeable);
    judge(s, sess, c, by, "accepted", null);
    c.status = "accepted";
    touch(c);
    syncLane(s, sess, c);
    broadcast(s, c, "card.updated");
  }
  /** 수정 요청 — 판 +1, 지난 판을 versions 로, 같은 lane 재진입, 새 판 위임 카드 말풍선. */
  function revise(s: Store, sess: Session, c: Stored, by: { kind: "agent" | "user"; id: string; name: string }, reason: string, patch: { criteria?: Criterion[]; goal?: string; boundaries?: string; refs?: Stored["refs"] } = {}): { message: Message; task: MockTask } {
    const human = by.kind === "user";
    if (!(c.status === "result_submitted" || (human && c.status === "accepted"))) throw new Problem(409, "card_not_judgeable", CARD_MOCK.not_judgeable);
    judge(s, sess, c, by, "revise_requested", reason);
    // 계약(v0.3.10 #397 B2) — 지난 판은 그 판의 전체 모양: version · goal · criteria · boundaries · refs · output_format · budget_usd ·
    // revise_reason · delegate_message_id · result · judgement. 이것보다 더도 덜도 넣지 않는다(lib/mock/cards.test 가 키를 잰다).
    c.versions.push({
      version: c.version, goal: c.goal, criteria: c.criteria, boundaries: c.boundaries, refs: refsOut(s, c), output_format: c.output_format, budget_usd: c.budget_usd,
      revise_reason: c.revise_reason ?? null, delegate_message_id: c.delegate_message_id, result: c.result, judgement: c.judgement,
    });
    c.version += 1;
    if (patch.goal) c.goal = patch.goal;
    if (patch.criteria) c.criteria = patch.criteria.map(([text, method], i) => ({ n: i + 1, text, method: method as TaskCard["criteria"][number]["method"] }));
    if (patch.boundaries) c.boundaries = patch.boundaries;
    if (patch.refs) c.refs = patch.refs;
    c.revise_reason = reason;
    c.result = null;
    c.judgement = null;
    c.status = "in_progress";
    const delegator = { id: c.delegator.agent_id, name: c.delegator.name };
    const assignee = { id: c.assignee.agent_id, name: c.assignee.name };
    const t = createTask(s, sess, assignee.id, null, { laneId: c.lane_id, brief: c.goal, workId: c.work_id });
    const lane = s.lanes.get(c.lane_id);
    if (lane) lane.reentry_count = (lane.reentry_count ?? 0) + 1;
    // 새 판 위임 카드 말풍선은 사람이 눌렀어도 **카드의 위임자** 말풍선이다(카드는 위임자의 것) — 누가 뒤집었는지는 지난 판 결과 카드의 판정 줄이 남긴다.
    const m = addMessage(s, sess, {
      author_type: "agent", author_id: delegator.id, author: { name: delegator.name, avatar_url: null }, kind: "text",
      content: `${link(assignee)} ${c.goal}`, mentions: parseMentions(link(assignee)), lane_id: c.lane_id, work_id: c.work_id,
      card_id: c.id, card_role: "delegation", card_version: c.version,
    }, { delegatedLaneId: c.lane_id, delegateTargetId: assignee.id, delegateTargetName: assignee.name });
    c.delegate_message_id = m.id;
    t.trigger_message_id = m.id;
    touch(c);
    syncLane(s, sess, c);
    broadcast(s, c, "card.updated");
    return { message: m, task: t };
  }

  // ── op ──
  on("GET", "/rooms/{id}/cards", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const q = req.query.get("work_id");
    const wid = q === "none" ? null : q;
    const items = [...cardsOf(s).values()].filter((c) => c.room_id === sess.id && (q == null || (c.work_id ?? null) === wid)).sort((a, b) => a.number - b.number);
    const tasksCost = (c: Stored) => {
      let sum = 0;
      for (const t of s.tasks.values()) if (t.lane_id === c.lane_id) sum += t.cost_usd ?? 0;
      return sum > 0 ? Math.round(sum * 100) / 100 : c.result?.cost_usd ?? null;
    };
    return ok({
      work_id: q == null ? null : wid,
      total: items.length,
      pending_judgement: items.filter((c) => c.status === "result_submitted").length,
      items: items.map((c) => ({
        id: c.id, label: c.label, number: c.number, version: c.version, parent_card_id: c.parent_card_id, assignee: c.assignee, goal: c.goal, status: c.status,
        auto_result: c.result?.auto ?? false, met: c.result ? c.result.met_count : null, total_criteria: c.criteria.length, cost_usd: tasksCost(c), lane_id: c.lane_id,
        latest_message_id: c.result?.message_id ?? c.delegate_message_id,
      })),
    });
  });
  on("GET", "/cards/{id}", (req, p) => {
    const s = store();
    const { c } = cardOr404(s, req, p.id);
    return ok(out(s, c, requireUser(s, req).id, true));
  });
  on("POST", "/cards/{id}/result", (req, p) => {
    const s = store();
    const { c, sess } = cardOr404(s, req, p.id);
    const b = (req.body ?? {}) as Parameters<typeof report>[4];
    // 목 전용 `?order=card_first` — card.updated 를 결과 말풍선보다 먼저 낸다(두 순서 모두 재현, #397 B1).
    const r = report(s, sess, c, null, { ...b, confirmed: b.confirmed ?? [], assumed: b.assumed ?? [], verdicts: b.verdicts ?? [] }, { cardFirst: req.query.get("order") === "card_first" });
    return ok({ card: out(s, c, null, false), message: r.message, downgraded: r.downgraded, notice: r.downgraded.length ? CARD_MOCK.downgraded_notice + r.downgraded.join(", ") : null }, 201);
  });
  on("POST", "/cards/{id}/accept", (req, p) => {
    const s = store();
    const { c, sess } = cardOr404(s, req, p.id);
    const u = requireUser(s, req);
    if (!judgeUser(s, c, u.id)) throw new Problem(403, "not_card_judge", CARD_MOCK.not_card_judge);
    accept(s, sess, c, { kind: "user", id: u.id, name: u.display_name });
    return ok(out(s, c, u.id, false));
  });
  on("POST", "/cards/{id}/revise", (req, p) => {
    const s = store();
    const { c, sess } = cardOr404(s, req, p.id);
    const u = requireUser(s, req);
    if (!judgeUser(s, c, u.id)) throw new Problem(403, "not_card_judge", CARD_MOCK.not_card_judge);
    const reason = String(((req.body ?? {}) as { reason?: string }).reason ?? "").trim();
    if (!reason) throw new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field: "reason", code: "required", message: CARD_MOCK.reason_required }] });
    const r = revise(s, sess, c, { kind: "user", id: u.id, name: u.display_name }, reason);
    return ok({ card: out(s, c, u.id, false), message: r.message, task: { id: r.task.id } });
  });

  // ── 시드 ──
  on("POST", "/__mock/rooms/{id}/seed-cards", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const user = requireUser(s, req);
    const body = (req.body ?? {}) as { work_id?: string | null };
    const workId = body.work_id !== undefined ? body.work_id : ctx.openWorkOf(s, sess.id);
    const ownerId = s.rooms.get(sess.id)?.owner_user_id ?? user.id;
    const ROLES = { Lead: ["lead", "팀을 이끌고 위임·종합한다"], Researcher: ["researcher", "자료를 조사한다"], Designer: ["custom", "화면과 스프라이트를 그린다"], Developer: ["engineer", "코드를 쓴다"], Writer: ["writer", "글을 쓴다"] } as const;
    for (const [name, [role, desc]] of Object.entries(ROLES)) {
      let a = [...s.agents.values()].find((x) => x.workspace_id === sess.workspace_id && x.name === name);
      if (!a) {
        a = makeAgent(sess.workspace_id, ownerId, name, role, desc);
        s.agents.set(a.id, a);
      }
      if ((sess.participants ?? []).some((x) => x.agent_id === a!.id)) continue;
      (sess.participants ??= []).push({
        session_id: sess.id, agent_id: a.id, agent: { id: a.id, name: a.name, role: a.role, role_description: a.role_description, avatar_url: null, respond_to: a.respond_to },
        profile: a.profiles[0], status: "idle", status_note: null, is_assignee: false, mention_link: link(a), warnings: [], joined_at: now(),
      });
    }
    const by = (n: string) => (sess.participants ?? []).map((x) => s.agents.get(x.agent_id)).find((a) => a?.name === n);
    const [lead, researcher, designer, developer, writer] = ["Lead", "Researcher", "Designer", "Developer", "Writer"].map(by);
    if (!lead || !researcher || !designer || !developer || !writer) throw new Problem(409, "no_agent", CARD_MOCK.no_agent);
    const finish = (t: MockTask, cost: number) => {
      t.status = "completed";
      t.cost_usd = cost;
      t.finished_at = now();
      setLaneStatus(s, sess, t.lane_id, { status: "done", current_activity: null, finished_at: t.finished_at });
    };
    const agentAs = (a: Agentish) => ({ kind: "agent" as const, id: a.id, name: a.name });

    // 참고 자료 — 기획서(아티팩트) · 타일 결정 · 방장의 지시(메시지).
    const plan = storeArtifact(s, sess, "기획서-v3.md", "document", new TextEncoder().encode("# 기획서 v3\n"), {});
    plan.work_id = workId;
    emit(s, sess.workspace_id, "artifact.created", plan, sess.id);
    const dec = { id: uuid(), session_id: sess.id, summary: CARD_SEED.decision_tile, rationale: null, source: "agent" as const, ref_id: null, auto: false, created_at: now(), work_id: workId };
    s.decisions.set(dec.id, dec);
    emit(s, sess.workspace_id, "decision.created", dec, sess.id);
    const orderText = `${link(lead)} ${CARD_SEED.order}`;
    const order = addMessage(s, sess, { author_type: "user", author_id: user.id, author: { name: user.display_name, avatar_url: null }, kind: "text", content: orderText, mentions: parseMentions(orderText), work_id: workId });
    const tLead = createTask(s, sess, lead.id, order.id, { workId });
    tLead.status = "running";
    setLaneStatus(s, sess, tLead.lane_id, { status: "running", current_activity: null });
    const refs = { plan: { kind: "artifact" as const, id: plan.id, label: CARD_SEED.artifact_plan }, tile: { kind: "decision" as const, id: dec.id, label: CARD_SEED.decision_tile }, order: { kind: "message" as const, id: order.id, label: `${user.display_name} ${new Date(order.created_at).toISOString().slice(11, 16)}` } };

    // C-1 Researcher — 결과(전부 충족, 근거) → Lead 수락.
    const d1 = delegate(s, sess, { from: lead, fromTask: tLead, to: researcher, workId, goal: CARD_SEED.c1.goal, criteria: CARD_SEED.c1.criteria, boundaries: CARD_SEED.c1.boundaries, refs: [refs.plan], output: CARD_SEED.c1.output });
    d1.task.status = "running";
    const tbl = storeArtifact(s, sess, "경쟁작-조사.md", "document", new TextEncoder().encode("| 게임 | 드리프트 |\n"), { agentId: researcher.id, agentName: researcher.name, taskId: d1.task.id });
    tbl.work_id = workId;
    const ev1: CardEvidence = { kind: "artifact", ref: tbl.id, label: "경쟁작-조사.md" };
    report(s, sess, d1.card, d1.task, { summary: CARD_SEED.c1.summary, verdicts: [1, 2, 3].map((n) => ({ criterion: n, verdict: "met" as const, evidence: [ev1] })), confirmed: [...CARD_SEED.c1.confirmed], assumed: [] }, { cost: 0.8, duration: 9 * 60 });
    finish(d1.task, 0.8);
    accept(s, sess, d1.card, agentAs(lead));

    // C-2 Designer — 결과 카드 없이 끝나 서버가 쓴 자동 결과 카드(모든 기준 미충족).
    const d2 = delegate(s, sess, { from: lead, fromTask: tLead, to: designer, workId, goal: CARD_SEED.c2.goal, criteria: CARD_SEED.c2.criteria, boundaries: CARD_SEED.c2.boundaries, refs: [refs.tile], output: CARD_SEED.c2.output });
    report(s, sess, d2.card, d2.task, { summary: "", verdicts: d2.card.criteria.map((c) => ({ criterion: c.n, verdict: "unmet" as const })), confirmed: [], assumed: [] }, { auto: true, cost: 1.1, duration: 22 * 60 });
    finish(d2.task, 1.1);

    // C-3 Developer — 판정 대기 · 기준 1 충족(근거) · 2 부분(사유) · 3 「부분 · 근거 없음」(met 인데 근거 없음 → 서버가 낮춤).
    const d3 = delegate(s, sess, { from: lead, fromTask: tLead, to: developer, workId, goal: CARD_SEED.c3.goal, criteria: CARD_SEED.c3.criteria, boundaries: CARD_SEED.c3.boundaries, refs: [refs.plan, refs.tile, refs.order], output: CARD_SEED.c3.output, budget: 3 });
    d3.task.status = "running";
    const log = storeArtifact(s, sess, CARD_SEED.c3.evidence_log, "log", new TextEncoder().encode("ok 12/12\n"), { agentId: developer.id, agentName: developer.name, taskId: d3.task.id });
    log.work_id = workId;
    // C-4 — 카드로 받은 일(C-3) 안에서 다시 위임한 하위 카드(서버가 parent_card_id 를 채운다).
    const d4 = delegate(s, sess, { from: developer, fromTask: d3.task, to: developer === designer ? lead : designer, workId, goal: CARD_SEED.c4.goal, criteria: CARD_SEED.c4.criteria, boundaries: CARD_SEED.c4.boundaries, parent: d3.card });
    d4.task.status = "running";
    d4.task.cost_usd = 0.3;
    setLaneStatus(s, sess, d4.task.lane_id, { status: "running" });
    report(s, sess, d3.card, d3.task, {
      summary: CARD_SEED.c3.summary,
      verdicts: [
        { criterion: 1, verdict: "met", evidence: [{ kind: "artifact", ref: log.id, label: CARD_SEED.c3.evidence_log }] },
        { criterion: 2, verdict: "partial", note: CARD_SEED.c3.note2 },
        { criterion: 3, verdict: "met" },
      ],
      confirmed: [...CARD_SEED.c3.confirmed], assumed: [...CARD_SEED.c3.assumed], deviations: CARD_SEED.c3.deviations, open_issues: CARD_SEED.c3.open_issues,
    }, { cost: 1.2, duration: 14 * 60 });
    finish(d3.task, 1.2);

    // C-5 Writer — 1판 결과 → Lead 수정 요청 → 2판 진행 중(참고 칩 하나는 지워진 자료).
    const d5 = delegate(s, sess, { from: lead, fromTask: tLead, to: writer, workId, goal: CARD_SEED.c5.goal, criteria: CARD_SEED.c5.criteria, boundaries: CARD_SEED.c5.boundaries, refs: [refs.plan] });
    report(s, sess, d5.card, d5.task, {
      summary: CARD_SEED.c5.summary,
      verdicts: [{ criterion: 1, verdict: "partial", note: CARD_SEED.c5.note1 }, { criterion: 2, verdict: "met", evidence: [{ kind: "commit", ref: "a1b2c3d" }] }, { criterion: 3, verdict: "met", evidence: [{ kind: "artifact", ref: plan.id, label: "스크린샷" }] }],
      confirmed: [...CARD_SEED.c5.confirmed], assumed: [],
    }, { cost: 0.4, duration: 6 * 60 });
    finish(d5.task, 0.4);
    const r5 = revise(s, sess, d5.card, agentAs(lead), CARD_SEED.c5.revise, { criteria: CARD_SEED.c5.criteria2, refs: [refs.plan, { kind: "message", id: uuid(), label: CARD_SEED.missing_ref }] });
    r5.task.status = "running";
    setLaneStatus(s, sess, r5.task.lane_id, { status: "running" });

    // 카드 없는 에이전트 → 에이전트 멘션은 ‹질문›(Writer → Researcher), 그 질문이 깨운 턴의 말은 ‹답›.
    const qText = `${link(researcher)} ${CARD_SEED.question}`;
    const q = addMessage(s, sess, { author_type: "agent", author_id: writer.id, author: { name: writer.name, avatar_url: null }, kind: "text", content: qText, mentions: parseMentions(qText), source_task_id: r5.task.id, lane_id: r5.task.lane_id, work_id: workId });
    const tq = createTask(s, sess, researcher.id, q.id, { laneId: d1.task.lane_id, workId });
    const aText = `${link(writer)} ${CARD_SEED.answer}`;
    const ans = addMessage(s, sess, { author_type: "agent", author_id: researcher.id, author: { name: researcher.name, avatar_url: null }, kind: "text", content: aText, mentions: parseMentions(aText), source_task_id: tq.id, lane_id: tq.lane_id, work_id: workId });
    tq.status = "completed";

    const cards = Object.fromEntries([d1, d2, d3, d4, d5].map((d) => [d.card.label, d.card.id]));
    return ok({ work_id: workId, cards, messages: { order: order.id, question: q.id, answer: ans.id, c3_result: d3.card.result?.message_id, c5_v2: r5.message.id } }, 201);
  });
}
