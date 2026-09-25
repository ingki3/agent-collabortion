/**
 * 목 — 타임라인 대화 배치(PRD FR-3.1.3 · SCREEN §4.6 「대화 배치」) 시드. STO 방처럼 사람 → Lead → Researcher·Writer 가 주고받는다.
 *
 * `POST /__mock/rooms/{id}/seed-conversation`(계약 밖, `__mock` 접두) — Lead · Researcher · Writer 를 방에 들이고(없으면 워크스페이스에 만든다) 대화를 붙인다.
 * 서버가 실제로 쓰는 모양 그대로 둔다(판정이 목에서만 맞는 일이 없게):
 *   1. 사람(방장) → @Lead 지시. Lead 의 task 는 이 메시지가 트리거(`trigger_message_id`).
 *   2·3. Lead 의 위임 둘 — 서버 `router.Delegate` 처럼 내용 = `[@대상](mention://agent/…) ` + brief, `source_task_id` = Lead task,
 *      새 lane `delegated_from_task_id` = Lead task · `brief`, 새 task 의 `trigger_message_id` = 위임 메시지.
 *   4. Researcher 보고(@Lead 멘션, `detail` 있음) — 트리거 = 위임 메시지 → 「보고」.
 *   5. Writer 질문 카드(`blocked_q`, 위임자 @Lead 멘션 — 서버 status.go 모양) + 스레드 답글 Lead(@Writer 멘션) → 「답」.
 *   6. Writer 보고(@Lead) — 트리거 = 위임 메시지. 6 뒤 1분 안에 Writer 가 한 번 더 @Lead 보고 → 묶음.
 *   7. `/note` 메모 · 시스템 줄 하나.
 */
import type { Lane, Message } from "@/lib/api/types";
import type { Session } from "@/lib/legacy-session";
import { makeAgent, store, type MockTask, type Store } from "./store";
import type { Req, Res } from "./handlers";
import type { SpeechPremises } from "./speech";

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

export interface ConversationSeedCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  sessionOf: (s: Store, req: Req, id: string) => Session;
  addMessage: (
    s: Store,
    sess: Session,
    m: Partial<Message> & Pick<Message, "author_type" | "author_id" | "kind" | "content" | "mentions">,
    speech?: SpeechPremises,
  ) => Message;
  createTask: (s: Store, sess: Session, agentId: string, triggerId: string | null, opts?: { brief?: string | null }) => MockTask;
  setLaneStatus: (s: Store, sess: Session, laneId: string, patch: Partial<Lane>) => void;
  parseMentions: (content: string) => Message["mentions"];
}

export function registerConversationSeed(ctx: ConversationSeedCtx): void {
  const { on, Problem, sessionOf, addMessage, createTask, setLaneStatus, parseMentions } = ctx;

  on("POST", "/__mock/rooms/{id}/seed-conversation", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const room = s.rooms.get(sess.id);
    const ownerId = room?.owner_user_id ?? [...s.users.values()][0].id;
    // 셋이 방에 없으면 워크스페이스에 만들고(Writer 는 기본 목에 없다) 방에 들인다 — `seed` 의 참여자 모양 그대로.
    const ROLES = { Lead: ["lead", "팀을 이끌고 위임·종합한다"], Researcher: ["researcher", "자료를 조사한다"], Writer: ["writer", "초안을 쓴다"] } as const;
    for (const [name, [role, desc]] of Object.entries(ROLES)) {
      let a = [...s.agents.values()].find((x) => x.workspace_id === sess.workspace_id && x.name === name);
      if (!a) {
        a = makeAgent(sess.workspace_id, ownerId, name, role, desc);
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
    const researcher = byName("Researcher");
    const writer = byName("Writer");
    if (!lead || !researcher || !writer) throw new Problem(409, "no_agent", "Lead · Researcher · Writer 가 방에 있어야 합니다");
    const owner = s.users.get(s.rooms.get(sess.id)?.owner_user_id ?? "") ?? [...s.users.values()][0];
    const link = (a: { name: string; id: string }) => `[@${a.name}](mention://agent/${a.id})`;
    const who = (a: typeof lead) => ({ name: a.name, avatar_url: null, role: a.role });
    const agentMsg = (a: typeof lead, task: MockTask | null, content: string, extra: Partial<Message> = {}, speech: SpeechPremises = {}) =>
      addMessage(s, sess, {
        author_type: "agent", author_id: a.id, author: who(a), kind: "text", content, mentions: parseMentions(content),
        source_task_id: task?.id ?? null, lane_id: task?.lane_id ?? null, ...extra,
      }, speech);
    const finish = (t: MockTask, status: Lane["status"] = "done") => {
      t.status = status === "done" ? "completed" : status;
      setLaneStatus(s, sess, t.lane_id, { status, finished_at: status === "done" ? new Date().toISOString() : null, current_activity: null });
    };

    addMessage(s, sess, { author_type: "system", author_id: null, kind: "system", content: `${owner.display_name}이 @Lead · @Researcher · @Writer 를 초대했습니다.`, mentions: [] });

    // 1. 사람 → Lead 지시.
    const c1 = `${link(lead)} STO 시장 레포트 10p 만들어 줘. 법 · 시장 규모 · 해외 사례는 꼭 넣어서.`;
    const m1 = addMessage(s, sess, { author_type: "user", author_id: owner.id, author: { name: owner.display_name, avatar_url: null }, kind: "text", content: c1, mentions: parseMentions(c1) });
    const tLead = createTask(s, sess, lead.id, m1.id, { brief: null });
    tLead.status = "running";
    setLaneStatus(s, sess, tLead.lane_id, { status: "running", current_activity: "하위 결과를 기다리는 중" });

    // 2·3. 위임 둘 — router.Delegate 모양(내용 = 멘션 링크 + " " + brief).
    const delegate = (to: typeof lead, brief: string) => {
      // 서버 `router.Delegate` 와 같은 차례: lane 을 만들고, 그 lane 을 premise 로 넘겨 메시지를 위임으로 판정한다
      // (본문이 brief 로 끝나는지 따위를 되짚지 않는다 — openapi v0.3.2 D24).
      const t = createTask(s, sess, to.id, null, { brief });
      setLaneStatus(s, sess, t.lane_id, { delegated_from_task_id: tLead.id, brief });
      const m = agentMsg(lead, tLead, `${link(to)} ${brief}`, {}, { delegatedLaneId: t.lane_id, delegateTargetId: to.id, delegateTargetName: to.name });
      const task = s.tasks.get(t.id);
      if (task) task.trigger_message_id = m.id;
      return { m, t };
    };
    const dR = delegate(researcher, "법 통과 여부 · 시장 규모 전망 · 해외 사례를 조사해 주세요. 출처는 꼭 남겨 주세요.");
    const dW = delegate(writer, "조사가 끝나면 10p 초안을 써 주세요.");

    // 4. Researcher 보고 — 트리거 = 위임 메시지.
    agentMsg(researcher, dR.t, `${link(lead)} 조사 끝났습니다. 법은 이미 통과(2026-01-15)했고 시행은 2027-02-04 입니다. 전망 367조 vs 실측 48.7억의 낙차가 핵심이라 봅니다.`, {
      detail: ["## A. 시장 규모 · 성장 전망", "", "| 출처 | 2030 전망 |", "|---|---|", "| 증권사 A | 367조 |", "| 실측(2026) | 48.7억 |"].join("\n"),
    });
    finish(dR.t);

    // 5. Writer 질문 카드(blocked_q) — 위임자 멘션(서버 status.go) + Lead 의 답(스레드 답글).
    const qc = `표 3 을 결론 앞에 둘까요, 부록으로 뺄까요? 독자가 임원이면 앞이 낫습니다.\n\n${link(lead)}`;
    const q = agentMsg(writer, dW.t, qc, { kind: "blocked_q" });
    setLaneStatus(s, sess, dW.t.lane_id, { status: "blocked", blocked_message_id: q.id, blocked_note: "표 3 위치", waiting_for: lead.name });
    agentMsg(lead, tLead, `${link(writer)} 결론 앞으로. 임원 보고용입니다.`, { parent_id: q.id });

    // 6. Writer 보고 둘(연속 → 묶음).
    dW.t.status = "running";
    setLaneStatus(s, sess, dW.t.lane_id, { status: "running", blocked_message_id: null, waiting_for: null });
    agentMsg(writer, dW.t, `${link(lead)} 초안 v1 을 올렸습니다. 표 3 은 결론 앞에 뒀습니다.`);
    agentMsg(writer, dW.t, `${link(lead)} 참고로 해외 사례는 두 쪽으로 줄였습니다.`);
    finish(dW.t);

    // 7. 메모.
    addMessage(s, sess, { author_type: "user", author_id: owner.id, author: { name: owner.display_name, avatar_url: null }, kind: "text", content: "/note 금요일 임원 보고 전에 최종본 확인", mentions: [], is_note: true });
    return { status: 201, body: { order_id: m1.id, delegate_ids: [dR.m.id, dW.m.id], question_id: q.id } };
  });
}
