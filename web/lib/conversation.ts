/**
 * 타임라인을 대화로 — 누가 → 누구에게 · 무엇을 (PRD FR-3.1.3 · SCREEN §4.6 「대화 배치」 · COMPONENTS §9.8).
 *
 * **판정은 서버가 이미 내려주는 칸으로만 한다 — 추측 금지.** 본문 문장을 읽어 종류를 짐작하지 않는다. 근거가 아직 없으면(보고 판정에 필요한
 * task 를 불러오는 중) `pending` 을 돌려주고 화면은 종류 라벨을 그리지 않는다 — 틀린 라벨을 먼저 그리지 않는다.
 *
 * 판정 순서(PRD FR-3.1.3 표): system → hitl → blocked_q(질문) → summary(요약) → 질문 카드 답글(답) → /note(메모) → 위임 → 보고 → 지시 → 요청 → 대화.
 * 계약 칸: Message.kind·mentions·parent_id·is_note·author_type·author_id·source_task_id, Lane.delegated_from_task_id·agent_id·brief·
 * blocked_message_id·waiting_for·status, Task.trigger_message_id.
 */
import type { Lane, Message } from "@/lib/api/types";

export type SpeechKind = "order" | "delegate" | "request" | "report" | "question" | "answer" | "note" | "summary" | "talk" | "system" | "hitl";

/** 받는 쪽 한 칸. `room` = 방 전체, `all` = @all, `record` = 기록만(/note). */
export interface Addressee {
  kind: "agent" | "user" | "all" | "room" | "record";
  id?: string;
  name: string;
}

export interface Speech {
  kind: SpeechKind;
  to: Addressee[];
  /** 위임 — 그 위임이 만든 서브 미션(상태 칩). */
  lane?: Lane;
  /** 보고 — 이 메시지를 쓴 턴을 깨운 메시지(요청자 · 첫 줄). */
  reportOf?: { messageId: string; requester: string; excerpt: string };
  /** 판정에 필요한 것이 아직 없다(task 불러오는 중) — 라벨을 그리지 않는다. */
  pending?: boolean;
}

export interface ConversationCtx {
  lanes: readonly Lane[];
  /**
   * task id → 그 task 의 `trigger_message_id`. `undefined` = 아직 모른다(불러오는 중), `null` = 트리거 메시지 없음.
   */
  triggerOf: (taskId: string) => string | null | undefined;
  /** 메시지 id → 메시지(타임라인·스레드·getMessage 로 읽은 것). `undefined` = 아직 모른다, `null` = 읽을 수 없다(없음·권한). */
  messageById: (id: string) => Message | null | undefined;
  /** 작성자 표시 이름(`authorName`). */
  authorName: (m: Message) => string;
}

/** 멘션 링크 `[@이름](mention://…)` → `@이름`, 공백 접기. 요약 줄·aria 에 쓴다. */
export function plainText(content: string): string {
  return content.replace(/\[(@[^\]]+)\]\(mention:\/\/[^)]+\)/g, "$1").replace(/\s+/g, " ").trim();
}

/** 「↩ … 에 대한 보고」의 인용 — 트리거 본문 앞의 멘션을 떼고 40자. */
export function excerpt(content: string, max = 40): string {
  const t = plainText(content).replace(/^(@\S+\s*)+/, "").trim();
  return t.length > max ? `${t.slice(0, max).trimEnd()}…` : t;
}

function sameAddressee(a: Addressee, b: Addressee): boolean {
  return a.kind === b.kind && (a.id ?? a.name) === (b.id ?? b.name);
}

function uniq(list: Addressee[]): Addressee[] {
  const out: Addressee[] = [];
  for (const a of list) if (!out.some((b) => sameAddressee(a, b))) out.push(a);
  return out;
}

/** 작성자를 받는 쪽 한 칸으로(요청자·스레드 대상). 시스템은 받는 쪽이 될 수 없다. */
export function authorAsAddressee(m: Message, name: string): Addressee | null {
  if (m.author_type === "system" || !m.author_id) return null;
  return { kind: m.author_type === "agent" ? "agent" : "user", id: m.author_id, name };
}

/** 본문(`content`)의 멘션만 — `detail` 속 멘션은 받는 쪽이 아니다(라우팅 FR-3.3 과 같은 이유). 작성자 자신은 뺀다. */
export function mentionAddressees(m: Message): Addressee[] {
  const out: Addressee[] = [];
  for (const x of m.mentions ?? []) {
    if (x.kind === "all") {
      out.push({ kind: "all", id: "all", name: "all" });
      continue;
    }
    if (x.id === m.author_id) continue;
    out.push({ kind: x.kind, id: x.id, name: x.display_name ?? "" });
  }
  return uniq(out);
}

const isIn = (list: Addressee[], a: Addressee) => list.some((b) => sameAddressee(a, b));

/**
 * 한 메시지의 말 — 종류와 받는 쪽. `parent` 는 스레드 답글일 때 그 루트.
 */
export function classify(m: Message, ctx: ConversationCtx, parent?: Message): Speech {
  if (m.kind === "system") return { kind: "system", to: [] };
  if (m.kind === "hitl") return { kind: "hitl", to: [] };
  const mentioned = mentionAddressees(m);
  if (m.kind === "blocked_q") {
    if (mentioned.length) return { kind: "question", to: mentioned };
    const lane = ctx.lanes.find((l) => l.blocked_message_id === m.id);
    return { kind: "question", to: lane?.waiting_for ? [{ kind: "user", name: lane.waiting_for }] : [] };
  }
  if (m.kind === "summary") return { kind: "summary", to: [{ kind: "room", name: "" }] };
  const parentAuthor = parent ? authorAsAddressee(parent, ctx.authorName(parent)) : null;
  if (parent?.kind === "blocked_q") {
    const to = uniq([...(parentAuthor && parentAuthor.id !== m.author_id ? [parentAuthor] : []), ...mentioned]);
    return { kind: "answer", to };
  }
  if (m.is_note) return { kind: "note", to: [{ kind: "record", name: "" }] };
  // 스레드 대상은 멘션이 없을 때만 받는 쪽이 된다.
  const base = mentioned.length ? mentioned : parentAuthor && parentAuthor.id !== m.author_id ? [parentAuthor] : [];

  if (m.author_type === "agent" && m.source_task_id) {
    // 위임 — `colab lane delegate` 가 호출 에이전트 이름으로 쓴 메시지(서버 router.Delegate: 내용 = 멘션 링크 + " " + brief).
    const body = m.content.trimEnd();
    const lane = ctx.lanes.find(
      (l) =>
        l.delegated_from_task_id === m.source_task_id &&
        !!l.brief &&
        body.endsWith(l.brief.trim()) &&
        mentioned.some((a) => a.kind === "agent" && a.id === l.agent_id),
    );
    if (lane) {
      const target = mentioned.find((a) => a.kind === "agent" && a.id === lane.agent_id)!;
      return { kind: "delegate", to: [{ ...target, name: target.name || lane.agent_name || "" }], lane };
    }
    // 보고 — 이 메시지를 쓴 턴을 깨운 메시지의 작성자(요청자)에게.
    const trig = ctx.triggerOf(m.source_task_id);
    if (trig === undefined) return { kind: "talk", to: base.length ? base : [{ kind: "room", name: "" }], pending: true };
    if (trig) {
      const t = ctx.messageById(trig);
      if (t === undefined) return { kind: "talk", to: base.length ? base : [{ kind: "room", name: "" }], pending: true };
      const requester = t ? authorAsAddressee(t, ctx.authorName(t)) : null;
      if (t && requester && requester.id !== m.author_id && (base.length === 0 || isIn(base, requester))) {
        return {
          kind: "report",
          to: base.length ? base : [requester],
          reportOf: { messageId: t.id, requester: ctx.authorName(t), excerpt: excerpt(t.content) },
        };
      }
    }
  }
  const agentsMentioned = mentioned.some((a) => a.kind === "agent");
  if (m.author_type === "user" && agentsMentioned) return { kind: "order", to: mentioned };
  if (m.author_type === "agent" && agentsMentioned) return { kind: "request", to: mentioned };
  return { kind: "talk", to: base.length ? base : [{ kind: "room", name: "" }] };
}

/** 말풍선으로 그리는 메시지인가 — 시스템은 가운데 줄, 질문·요약·HITL 은 전폭 카드. */
export function isBubble(m: Message): boolean {
  return m.kind === "text";
}

const GROUP_MS = 5 * 60_000;

/** 바로 앞 메시지와 묶이는가 — 같은 작성자 · 같은 말의 종류 · 같은 받는 쪽 · 5분 안(SCREEN §4.6). 받는 쪽은 묶여도 숨기지 않는다. */
export function groupsWith(prev: { m: Message; s: Speech } | undefined, cur: { m: Message; s: Speech }): boolean {
  if (!prev) return false;
  if (!isBubble(prev.m) || !isBubble(cur.m)) return false;
  if (prev.m.author_type !== cur.m.author_type || prev.m.author_id !== cur.m.author_id) return false;
  if (prev.s.kind !== cur.s.kind || !!prev.s.pending !== !!cur.s.pending) return false;
  if (prev.s.to.length !== cur.s.to.length || !prev.s.to.every((a, i) => sameAddressee(a, cur.s.to[i]))) return false;
  const dt = Date.parse(cur.m.created_at) - Date.parse(prev.m.created_at);
  return dt >= 0 && dt <= GROUP_MS;
}
