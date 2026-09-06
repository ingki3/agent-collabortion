/**
 * 멘션 문법(PRD FR-3.2): 본문에는 `[@표시명](mention://agent/<id>)` 링크가 남고, 서버가 mentions[] 로 정규화한다.
 * 작성창은 `@이름` 자동완성 결과를 이 링크로 삽입한다.
 */
export type MentionKind = "agent" | "user" | "all";

export interface MentionTarget {
  kind: MentionKind;
  id: string;
  name: string;
}

/**
 * PRD FR-3.2 의 형식은 `mention://<kind>/<id>` 이고 `@all` 도 예외가 아니다 —
 * `mention://all/all`. 여기서 `mention://all` 을 만들던 동안 서버 정규식
 * (`mention://(agent|user|all)/([^)\s]+)`)이 그것을 멘션으로 읽지 못했고, 멘션이
 * 하나도 없는 메시지로 취급해 규칙 3(암묵 라우팅 억제) 대신 규칙 6 으로 assignee 를
 * 깨웠다(E1-05 위반: 실측 preview suppressed=false, 게시 시 Lead task 1개).
 */
export function mentionLink(t: MentionTarget): string {
  if (t.kind === "all") return "[@all](mention://all/all)";
  return `[@${t.name}](mention://${t.kind}/${t.id})`;
}

const LINK_RE = /\[@([^\]]+)\]\(mention:\/\/(agent|user|all)\/([0-9a-zA-Z-]+)\)/g;

/** 본문에서 멘션 링크를 뽑는다(순서 유지, 중복 제거). */
export function extractMentions(content: string): MentionTarget[] {
  const out: MentionTarget[] = [];
  const seen = new Set<string>();
  for (const m of content.matchAll(LINK_RE)) {
    const t: MentionTarget = { kind: m[2] as MentionKind, id: m[3], name: m[1] };
    const key = `${t.kind}:${t.id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(t);
  }
  return out;
}

/**
 * 렌더용 분해: 텍스트와 멘션 조각의 배열. 마크다운 전체 렌더는 P1 범위 밖이라 멘션만 하이라이트한다.
 */
export type ContentPart = { type: "text"; text: string } | { type: "mention"; target: MentionTarget };

export function splitContent(content: string): ContentPart[] {
  const parts: ContentPart[] = [];
  let last = 0;
  for (const m of content.matchAll(LINK_RE)) {
    const idx = m.index ?? 0;
    if (idx > last) parts.push({ type: "text", text: content.slice(last, idx) });
    const target: MentionTarget = { kind: m[2] as MentionKind, id: m[3], name: m[1] };
    parts.push({ type: "mention", target });
    last = idx + m[0].length;
  }
  if (last < content.length) parts.push({ type: "text", text: content.slice(last) });
  return parts;
}

/**
 * 커서 앞의 `@질의` 를 찾는다(자동완성 트리거). 공백·줄 시작 뒤의 @ 만 인정한다.
 * 돌려주는 start 는 `@` 의 위치.
 */
export function activeMentionQuery(text: string, caret: number): { start: number; query: string } | null {
  const before = text.slice(0, caret);
  const at = before.lastIndexOf("@");
  if (at < 0) return null;
  if (at > 0 && !/\s/.test(before[at - 1])) return null;
  const query = before.slice(at + 1);
  if (/[\s\]\)]/.test(query)) return null;
  return { start: at, query };
}
