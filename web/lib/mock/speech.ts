/**
 * 목 — 말의 종류·받는 쪽 판정(openapi v0.3.2 D24 · PRD FR-3.1.3). **서버 `internal/messages/speech.go` 의 표를 그대로 옮긴 것**이고,
 * 화면은 목이든 실서버든 같은 칸을 읽는다(lib/conversation.ts). 목이 판정을 더 잘 해서도, 덜 해서도 안 된다 —
 * 표가 갈리면 목에서만 초록인 화면이 된다.
 *
 * 판정 순서: system → hitl → blocked_q(질문, 멘션 없으면 waiting_for) → summary(요약) → 질문 카드 답글(답) → /note(메모) → 위임 → 보고(요청자 한 명) → 지시 → 요청 → 대화.
 */
import type { Message } from "@/lib/api/types";
import type { MockTask, Store } from "./store";

type Addressee = NonNullable<Message["addressees"]>[number];

export interface SpeechPremises {
  /** 위임 — 부른 쪽(목의 `lane delegate` 경로)이 「이것이 위임이다」라고 말한다. 서버와 같은 이유다: 쓰는 순간만 안다. */
  delegatedLaneId?: string;
  delegateTargetId?: string;
  delegateTargetName?: string;
  /** 질문 — 멘션이 없는 질문 카드가 기다리는 상대(표 3행 후반, `Lane.waiting_for`). */
  waitingFor?: Addressee;
}

const same = (a: Addressee, b: Addressee) => a.kind === b.kind && (a.id ?? a.name) === (b.id ?? b.name);
const uniq = (list: Addressee[]) => list.filter((a, i) => list.findIndex((b) => same(a, b)) === i);

function mentionTo(m: Pick<Message, "mentions" | "author_id">): Addressee[] {
  const out: Addressee[] = [];
  for (const x of m.mentions ?? []) {
    if (x.kind === "all") out.push({ kind: "all", id: null, name: "all" });
    else if (x.id !== m.author_id) out.push({ kind: x.kind, id: x.id, name: x.display_name ?? "" });
  }
  return uniq(out);
}

function authorTo(s: Store, m: Message | undefined): Addressee | null {
  if (!m || m.author_type === "system" || !m.author_id) return null;
  const name = m.author?.name ?? s.agents.get(m.author_id)?.name ?? s.users.get(m.author_id)?.display_name ?? "";
  return { kind: m.author_type === "agent" ? "agent" : "user", id: m.author_id, name };
}

/** 이 메시지를 쓴 턴을 깨운 메시지(서버 `task.trigger_message_id`). */
function triggerOf(s: Store, m: Message): Message | undefined {
  if (!m.source_task_id) return undefined;
  const t: MockTask | undefined = s.tasks.get(m.source_task_id);
  return t?.trigger_message_id ? s.messages.get(t.trigger_message_id) : undefined;
}

/** 서버가 저장하는 네 칸을 메시지에 채운다(목 저장소는 Message 를 통째로 든다). */
export function applySpeech(s: Store, m: Message, p: SpeechPremises = {}): Message {
  m.addressees = [];
  m.responds_to_message_id = null;
  m.delegated_lane_id = null;
  if (m.kind === "system") { m.speech = "system"; return m; }
  if (m.kind === "hitl") { m.speech = "hitl"; return m; }
  const mentioned = mentionTo(m);
  if (m.kind === "blocked_q") {
    m.speech = "question";
    // 표 3행: 멘션(위임자)이 먼저, 없으면 그 lane 이 기다리는 상대.
    m.addressees = mentioned.length ? mentioned : p.waitingFor ? [p.waitingFor] : [];
    return m;
  }
  if (m.kind === "summary") { m.speech = "summary"; return m; }
  const parent = m.parent_id ? s.messages.get(m.parent_id) : undefined;
  const parentAuthor = authorTo(s, parent);
  if (parent?.kind === "blocked_q") {
    m.speech = "answer";
    m.addressees = uniq([...(parentAuthor && parentAuthor.id !== m.author_id ? [parentAuthor] : []), ...mentioned]);
    return m;
  }
  if (m.is_note || m.content.startsWith("/note")) { m.speech = "note"; return m; }
  const base = mentioned.length ? mentioned : parentAuthor && parentAuthor.id !== m.author_id ? [parentAuthor] : [];
  if (m.author_type === "agent") {
    if (p.delegatedLaneId && p.delegateTargetId) {
      m.speech = "delegate";
      m.addressees = [{ kind: "agent", id: p.delegateTargetId, name: p.delegateTargetName ?? "" }];
      m.delegated_lane_id = p.delegatedLaneId;
      return m;
    }
    const trig = triggerOf(s, m);
    const requester = authorTo(s, trig);
    if (trig && requester && requester.id !== m.author_id && (base.length === 0 || base.some((a) => same(a, requester)))) {
      m.speech = "report";
      // 표 7행 받는 쪽 = 요청자 한 명. 같이 부른 다른 에이전트는 트리거만 된다.
      m.addressees = [requester];
      m.responds_to_message_id = trig.id;
      return m;
    }
  }
  if (mentioned.some((a) => a.kind === "agent")) {
    m.speech = m.author_type === "user" ? "instruct" : "request";
    m.addressees = mentioned;
    return m;
  }
  m.speech = "chat";
  m.addressees = base;
  return m;
}
