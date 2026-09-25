/**
 * 타임라인을 대화로 — 누가 → 누구에게 · 무엇을 (PRD FR-3.1.3 · SCREEN §4.6 「대화 배치」 · COMPONENTS §9.8).
 *
 * **판정은 서버가 한다**(openapi v0.3.2 D24 — `Message.speech`·`addressees`·`responds_to_message_id`·`delegated_lane_id`).
 * 이 파일은 그 칸을 화면이 쓰는 모양으로 옮기고, 화면에만 있는 것(묶음·아바타·「↩ 보고」의 인용문)을 만든다.
 * 규칙을 여기서 다시 계산하지 않는다 — 초안은 화면이 lane brief 접미·listLaneTasks 로 짐작했고 Lead 가 서버 판정으로 바꿨다.
 */
import type { Lane, Message } from "@/lib/api/types";

/** 계약 `Message.speech` 11종. */
export type SpeechKind = NonNullable<Message["speech"]>;

/** 계약 `Message.addressees[]` 한 칸 + 화면이 쓰는 「방 전체」·「기록만」. */
export interface Addressee {
  kind: "agent" | "user" | "all" | "room" | "record";
  id?: string | null;
  name: string;
}

export interface Speech {
  kind: SpeechKind;
  to: Addressee[];
  /** 위임 — 그 위임이 만든 서브 미션(상태 칩). 목록에 그 lane 이 있을 때만. */
  lane?: Lane;
  /** 보고 — 그 턴을 깨운 메시지(원래 지시·위임). 인용문은 그 메시지를 읽을 수 있을 때만 찬다. */
  reportOf?: { messageId: string; requester: string; excerpt: string };
}

export interface ConversationCtx {
  lanes: readonly Lane[];
  /** 메시지 id → 메시지(타임라인·스레드·getMessage). `undefined` = 아직 모른다, `null` = 읽을 수 없다. */
  messageById: (id: string) => Message | null | undefined;
  /** 작성자 표시 이름(`authorName`). */
  authorName: (m: Message) => string;
}

/** 멘션 링크 `[@이름](mention://…)` → `@이름`, 공백 접기. 인용문에 쓴다. */
export function plainText(content: string): string {
  return content.replace(/\[(@[^\]]+)\]\(mention:\/\/[^)]+\)/g, "$1").replace(/\s+/g, " ").trim();
}

/** 「↩ … 에 대한 보고」의 인용 — 트리거 본문 앞의 멘션을 떼고 40자. */
export function excerpt(content: string, max = 40): string {
  const t = plainText(content).replace(/^(@\S+\s*)+/, "").trim();
  return t.length > max ? `${t.slice(0, max).trimEnd()}…` : t;
}

/** 받는 쪽 — 서버 칸 그대로. 비면 「방 전체」, 메모는 「기록만」. */
function addressees(m: Message): Addressee[] {
  const to = (m.addressees ?? []).map((a) => ({ kind: a.kind, id: a.id ?? null, name: a.name }));
  if (to.length) return to;
  if (m.speech === "note") return [{ kind: "record", name: "" }];
  return [{ kind: "room", name: "" }];
}

/**
 * 한 메시지의 말 — 서버가 내려준 `speech`·`addressees` 에 화면 재료(위임 lane · 보고 인용문)를 붙인다.
 * 서버가 아직 안 내려준 칸(옛 서버)은 `chat` 으로 읽는다 — 짐작하지 않는다.
 */
export function speechOf(m: Message, ctx: ConversationCtx): Speech {
  const kind = (m.speech ?? "chat") as SpeechKind;
  const s: Speech = { kind, to: addressees(m) };
  if (kind === "delegate" && m.delegated_lane_id) {
    s.lane = ctx.lanes.find((l) => l.id === m.delegated_lane_id);
  }
  if (kind === "report" && m.responds_to_message_id) {
    const t = ctx.messageById(m.responds_to_message_id);
    if (t) s.reportOf = { messageId: t.id, requester: ctx.authorName(t), excerpt: excerpt(t.content) };
  }
  return s;
}

/** 말풍선으로 그리는 메시지인가 — 시스템은 가운데 줄, 질문·요약·HITL 은 전폭 카드. */
export function isBubble(m: Message): boolean {
  return m.kind === "text";
}

const GROUP_MS = 5 * 60_000;

function sameAddressee(a: Addressee, b: Addressee): boolean {
  return a.kind === b.kind && (a.id ?? a.name) === (b.id ?? b.name);
}

/** 바로 앞 메시지와 묶이는가 — 같은 작성자 · 같은 말의 종류 · 같은 받는 쪽 · 5분 안(SCREEN §4.6). 받는 쪽은 묶여도 숨기지 않는다. */
export function groupsWith(prev: { m: Message; s: Speech } | undefined, cur: { m: Message; s: Speech }): boolean {
  if (!prev) return false;
  if (!isBubble(prev.m) || !isBubble(cur.m)) return false;
  if (prev.m.author_type !== cur.m.author_type || prev.m.author_id !== cur.m.author_id) return false;
  if (prev.s.kind !== cur.s.kind) return false;
  if (prev.s.to.length !== cur.s.to.length || !prev.s.to.every((a, i) => sameAddressee(a, cur.s.to[i]))) return false;
  const dt = Date.parse(cur.m.created_at) - Date.parse(prev.m.created_at);
  return dt >= 0 && dt <= GROUP_MS;
}
