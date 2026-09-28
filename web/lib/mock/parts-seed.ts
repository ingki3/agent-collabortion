/**
 * 목 — 부분 메시지(PRD FR-3.1.4 · SCREEN §4.6 v0.19.11 · openapi v0.3.6 postMessageGroup) 시드.
 *
 * `POST /__mock/rooms/{id}/seed-parts`(계약 밖, `__mock` 접두) — Lead · Designer · Developer 를 방에 들이고(없으면 만든다), 방장이 @Lead 에게
 * 「커브가 어색해」 지시 → Lead 턴이 **부분 3개**(보고 → 방장 · 요청 → @Designer · 요청 → @Developer)를 한 게시로 올린다. 서버가 쓰는 모양 그대로:
 *   - 부분마다 행 하나 · 같은 `group_id` · `group_index` = 순서 · `group_size` = 3.
 *   - 행의 `mentions` = 그 부분의 `to`(서버 `dec.Mentions` — 본문 속 멘션은 칩일 뿐 트리거·받는 쪽이 아니다) → 말의 종류는 행마다 서버 표(목 `applySpeech`).
 *   - 첫 부분(방장에게)에 `detail` · 아티팩트 없음(목 아티팩트는 별도) · 작업 과정은 Lead task 의 기록(셸 35 · 읽기 2).
 *   - 받는 에이전트(Designer · Developer)는 제 부분이 트리거인 task 하나씩.
 * `{ stream: true }` 면 **첫 부분만** 올리고 나머지는 `POST /__mock/rooms/{id}/parts-step` 로 하나씩(실시간 채움 · 「작업 중」 말풍선 교체).
 * 응답 `{ group_id, part_ids, lead_task_id }`.
 */
import type { Lane, Message, TaskEvent } from "@/lib/api/types";
import type { Session } from "@/lib/legacy-session";
import { makeAgent, store, type MockTask, type Store } from "./store";
import type { Req, Res } from "./handlers";
import type { SpeechPremises } from "./speech";

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

export interface PartsSeedCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  sessionOf: (s: Store, req: Req, id: string) => Session;
  addMessage: (s: Store, sess: Session, m: Partial<Message> & Pick<Message, "author_type" | "author_id" | "kind" | "content" | "mentions">, speech?: SpeechPremises) => Message;
  createTask: (s: Store, sess: Session, agentId: string, triggerId: string | null, opts?: { brief?: string | null }) => MockTask;
  pushEvent: (s: Store, sess: Session, task: MockTask, e: Partial<TaskEvent> & Pick<TaskEvent, "class">) => TaskEvent;
  setLaneStatus: (s: Store, sess: Session, laneId: string, patch: Partial<Lane>) => void;
  // v0.3.7 — 부분의 attachment_ids 를 AttachmentRef[] 로(같은 방·10개·중복 한 번, 아니면 422).
  resolveAttachments: (s: Store, sessionId: string, ids: string[] | undefined, Problem: ProblemCtor) => NonNullable<Message["attachments"]>;
  // 스크린샷·테스트용: base64 본문으로 아티팩트 하나를 저장하고 id 를 준다(종류는 목이 첫 바이트로 판정).
  seedArtifact: (s: Store, sess: Session, name: string, b64: string) => string;
  parseMentions: (content: string) => Message["mentions"];
}

/** 부분 3개의 본문(SCREEN §4.6 v0.19.11 그림 그대로). */
export const PART_BODIES = {
  report: "v9 올렸습니다. 커브가 어색했던 진짜 원인은 횡가속도 상한이 없던 것이었습니다.",
  designer: "자동차 디자인 v2 를 스프라이트 24방향으로 맞춰 주세요",
  developer: "FX 코드 합쳤습니다 — 부스트 색수차만 절반으로",
};

// `attach` 는 그 부분의 아티팩트 id 들(v0.3.7 MessagePartCreate.attachment_ids) — 부분마다 제 것만 가진다.
interface Pending { groupId: string; sessId: string; taskId: string; next: number; parts: { to: string; content: string; detail?: string; agentId?: string; attach?: string[] }[] }
const pending = new Map<string, Pending>();

export function registerPartsSeed(ctx: PartsSeedCtx): void {
  const { on, Problem, sessionOf, addMessage, createTask, pushEvent, setLaneStatus, parseMentions } = ctx;

  const postPart = (s: Store, sess: Session, lead: { id: string; name: string; role: string }, task: MockTask, pend: Pending) => {
    const i = pend.next++;
    const pt = pend.parts[i];
    const m = addMessage(s, sess, {
      author_type: "agent", author_id: lead.id, author: { name: lead.name, avatar_url: null, role: lead.role as NonNullable<NonNullable<Message["author"]>["role"]> },
      kind: "text", content: pt.content, mentions: parseMentions(pt.to), detail: pt.detail ?? null,
      source_task_id: task.id, lane_id: task.lane_id,
      group_id: pend.groupId, group_index: i, group_size: pend.parts.length,
      attachments: ctx.resolveAttachments(s, sess.id, pt.attach, Problem),
    });
    pushEvent(s, sess, task, { class: "status", verb: "post_message", object_ref: m.id, outcome: "ok", payload: { command: "message post", result_ref: m.id } });
    if (pt.agentId) createTask(s, sess, pt.agentId, m.id, { brief: null });
    return m;
  };

  on("POST", "/__mock/rooms/{id}/seed-parts", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const b = (req.body ?? {}) as { stream?: boolean };
    const room = s.rooms.get(sess.id);
    const owner = s.users.get(room?.owner_user_id ?? "") ?? [...s.users.values()][0];
    const ROLES = { Lead: ["lead", "팀을 이끌고 위임·종합한다"], Designer: ["custom", "화면과 스프라이트를 그린다"], Developer: ["engineer", "코드를 쓴다"] } as const;
    for (const [name, [role, desc]] of Object.entries(ROLES)) {
      let a = [...s.agents.values()].find((x) => x.workspace_id === sess.workspace_id && x.name === name);
      if (!a) {
        a = makeAgent(sess.workspace_id, owner.id, name, role, desc);
        s.agents.set(a.id, a);
      }
      if ((sess.participants ?? []).some((x) => x.agent_id === a!.id)) continue;
      (sess.participants ??= []).push({
        session_id: sess.id, agent_id: a.id, agent: { id: a.id, name: a.name, role: a.role, role_description: a.role_description, avatar_url: null, respond_to: a.respond_to },
        profile: a.profiles[0], status: "idle", status_note: null, is_assignee: false, mention_link: `[@${a.name}](mention://agent/${a.id})`, warnings: [], joined_at: new Date().toISOString(),
      });
    }
    const byName = (n: string) => (sess.participants ?? []).map((x) => s.agents.get(x.agent_id)).find((a) => a?.name === n);
    const lead = byName("Lead");
    const designer = byName("Designer");
    const developer = byName("Developer");
    if (!lead || !designer || !developer) throw new Problem(409, "no_agent", "Lead · Designer · Developer 가 방에 있어야 합니다");
    const link = (a: { name: string; id: string }) => `[@${a.name}](mention://agent/${a.id})`;
    const userLink = `[@${owner.display_name}](mention://user/${owner.id})`;

    // 방장 → @Lead 지시(「커브가 어색해」) — Lead task 의 트리거. 첫 부분(방장에게)은 그 지시에 대한 보고가 된다.
    const c1 = `${link(lead)} 커브가 어색해. 원인 찾아서 고쳐 줘.`;
    const order = addMessage(s, sess, { author_type: "user", author_id: owner.id, author: { name: owner.display_name, avatar_url: null }, kind: "text", content: c1, mentions: parseMentions(c1) });
    const task = createTask(s, sess, lead.id, order.id, { brief: null });
    task.status = "running";
    task.started_at = new Date(Date.now() - 16 * 60_000).toISOString();
    setLaneStatus(s, sess, task.lane_id, { status: "running", current_activity: "셸 명령을 실행하는 중…", has_runtime_session: true });
    const t0 = Date.now() - 16 * 60_000;
    const at = (m: number) => new Date(t0 + m * 60_000).toISOString();
    pushEvent(s, sess, task, { class: "runtime", verb: "start", outcome: "started", payload: { runtime_kind: "claude_code" }, created_at: at(0) });
    for (let i = 0; i < 35; i++) pushEvent(s, sess, task, { class: "tool", verb: "run_shell", outcome: "ok", payload: { command: "node test/headless.js" }, created_at: at(0.2 + i * 0.4) });
    for (let i = 0; i < 2; i++) pushEvent(s, sess, task, { class: "tool", verb: "read", outcome: "ok", object_ref: "src/physics.ts", created_at: at(14.5 + i * 0.5) });

    // 부분마다 제 첨부(v0.3.7) — Designer 에게만 시안 이미지를 준다. 다른 부분에는 나오지 않는다.
    const attachBody = (req.body ?? {}) as { attach_png_b64?: string };
    let designerAttach: string[] | undefined;
    if (attachBody.attach_png_b64) {
      designerAttach = [ctx.seedArtifact(s, sess, "ref-sprite.png", attachBody.attach_png_b64)];
    }
    const pend: Pending = {
      groupId: crypto.randomUUID(), sessId: sess.id, taskId: task.id, next: 0,
      parts: [
        { to: userLink, content: PART_BODIES.report, detail: ["## 1. 코너링 — 근본 원인", "", "| 항목 | v8 | v9 |", "|---|---|---|", "| 횡가속도 상한 | 없음 | 1.2g |"].join("\n") },
        { to: link(designer), content: PART_BODIES.designer, agentId: designer.id, attach: designerAttach },
        // 본문 속 @Designer 멘션은 칩일 뿐 — 트리거·받는 쪽은 `to`(@Developer)만(FR-3.1.4 3번).
        { to: link(developer), content: `${PART_BODIES.developer} (색은 ${link(designer)} 시안 기준)`, agentId: developer.id },
      ],
    };
    pending.set(pend.groupId, pend);
    const ids = [postPart(s, sess, lead, task, pend).id];
    if (!b.stream) {
      while (pend.next < pend.parts.length) ids.push(postPart(s, sess, lead, task, pend).id);
      task.status = "completed";
      task.finished_at = new Date().toISOString();
      setLaneStatus(s, sess, task.lane_id, { status: "done", current_activity: null, finished_at: task.finished_at });
    }
    return { status: 201, body: { group_id: pend.groupId, part_ids: ids, lead_task_id: task.id, order_id: order.id } };
  });

  /** 실시간 채움 — 다음 부분 하나를 올린다(서버는 한 트랜잭션에 N번 연달아 내지만, 화면이 부분이 도착하는 대로 같은 말풍선을 채우는지 본다). */
  on("POST", "/__mock/rooms/{id}/parts-step", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const b = (req.body ?? {}) as { group_id?: string };
    const pend = b.group_id ? pending.get(b.group_id) : undefined;
    if (!pend || pend.sessId !== sess.id || pend.next >= pend.parts.length) throw new Problem(404, "not_found", "채울 부분이 없습니다");
    const task = s.tasks.get(pend.taskId)!;
    const lead = s.agents.get(task.agent_id)!;
    const m = postPart(s, sess, lead, task, pend);
    return { status: 201, body: { message_id: m.id, remaining: pend.parts.length - pend.next } };
  });
}
