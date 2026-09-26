/**
 * 진행 메모(SCREEN §4.6 v0.19.10 「작업 중」 말풍선 · COMPONENTS §9.10) — `message.delta` 를 말풍선 한 줄·펼침으로 바꾸는 순수 규칙.
 *
 * `message.delta` 는 게시될 메시지의 초안이 아니라 에이전트가 도구 사이에 남기는 **진행 메모**다. 데몬(`daemon/internal/harness/acp/runner.go`
 * `appendSay`)은 턴의 `agent_message_chunk` 를 이어 붙여 15초 heartbeat 의 `preview.text` 로 보내고, 서버는 그것을 그대로
 * `{task_id, agent_id, message_id?, text}` 로 흘린다(openapi SSE 표) — `text` 는 언제나 **턴 처음부터의 누적 전문**이다.
 *
 * 조각 경계는 두 가지로 잡는다:
 *  1. **빈 줄(`\n\n`)** — 데몬이 도구 호출 경계마다 빈 줄 하나를 넣는다(Lead 판정 2026-09-26, daemon-protocol v0.10.1 문단 규칙). 정본.
 *  2. **옛 데몬 대비 폴백** — 빈 줄 없이 이어 붙이던 데몬(「…통과합니다.BGM v2 가…」)이면, 같은 task 의 **도구 이벤트(`task_event.appended`,
 *     class `tool`)가 두 델타 사이에 도착했을 때** 앞 델타까지의 길이를 경계로 둔다(heartbeat 15초 안에 도구 여럿이 끼면 한 조각으로 남는다).
 * 그 밖의 추측 분리(문장 끝 뒤 공백 없는 대문자·한글 시작에 줄바꿈 넣기 등)는 하지 않는다.
 *
 * 메시지가 게시되면(`message.created`, 같은 task) 그때까지의 메모는 그 메시지 앞의 것이므로 **기준점(base)** 을 옮긴다 — 말풍선은 그 뒤의 메모만 보인다.
 */

export interface ProgressMemo {
  taskId: string | null;
  /** 지금까지의 누적 전문(마지막 델타). */
  text: string;
  /** 이 길이 앞은 이미 게시된 메시지 앞의 메모 — 말풍선에 보이지 않는다. */
  base: number;
  /** 조각 경계(문자 offset, 오름차순, base 보다 큼). */
  cuts: number[];
  /** 마지막 델타 뒤에 도구 이벤트가 왔다 — 다음 델타가 새 조각을 연다. */
  pendingCut: boolean;
}

export type ProgressMemos = Readonly<Record<string, ProgressMemo>>;

/** 델타 하나 — 누적 전문으로 바꿔 끼운다. 앞 전문의 연장이 아니면(재시도·새 턴) 처음부터. */
export function applyDelta(memos: ProgressMemos, d: { agent_id: string; task_id?: string | null; text: string }): ProgressMemos {
  const prev = memos[d.agent_id];
  const taskId = d.task_id ?? prev?.taskId ?? null;
  const same = prev && (prev.taskId == null || d.task_id == null || prev.taskId === d.task_id) && d.text.startsWith(prev.text);
  if (!same) return { ...memos, [d.agent_id]: { taskId, text: d.text, base: 0, cuts: [], pendingCut: false } };
  if (prev.text === d.text && !prev.pendingCut) return memos;
  const cuts = [...prev.cuts];
  const last = cuts.length ? cuts[cuts.length - 1] : prev.base;
  if (prev.pendingCut && d.text.length > prev.text.length && prev.text.length > last) cuts.push(prev.text.length);
  const pendingCut = prev.pendingCut && d.text.length === prev.text.length;
  return { ...memos, [d.agent_id]: { ...prev, taskId, text: d.text, cuts, pendingCut } };
}

/** 같은 task 의 도구 이벤트 — 다음 델타에서 조각을 나눈다. */
export function noteToolEvent(memos: ProgressMemos, e: { task_id: string; class: string }): ProgressMemos {
  if (e.class !== "tool") return memos;
  let out: Record<string, ProgressMemo> | null = null;
  for (const [agent, m] of Object.entries(memos)) {
    if (m.taskId !== e.task_id || m.pendingCut) continue;
    out = out ?? { ...memos };
    out[agent] = { ...m, pendingCut: true };
  }
  return out ?? memos;
}

/** 그 에이전트가 메시지를 게시했다 — 지금까지의 메모는 그 메시지 앞의 것. */
export function notePosted(memos: ProgressMemos, agentId: string): ProgressMemos {
  const m = memos[agentId];
  if (!m || m.base === m.text.length) return memos;
  return { ...memos, [agentId]: { ...m, base: m.text.length, cuts: [], pendingCut: false } };
}

/** 턴이 끝났다(또는 서브 미션이 멈췄다) — 진행 메모는 영속되지 않는다. `taskId` 가 주어지면 그 task 의 메모만. */
export function dropMemo(memos: ProgressMemos, by: { agentId?: string; taskId?: string }): ProgressMemos {
  const keys = Object.keys(memos).filter((k) => (by.agentId ? k === by.agentId : false) || (by.taskId ? memos[k].taskId === by.taskId : false));
  if (keys.length === 0) return memos;
  const out = { ...memos };
  for (const k of keys) delete out[k];
  return out;
}

/** 말풍선에 보일 조각들 — base 뒤, 빈 줄과 폴백 경계마다 하나, 빈 조각은 뺀다. */
export function memoSegments(m: ProgressMemo | undefined): string[] {
  if (!m) return [];
  const edges = [m.base, ...m.cuts.filter((c) => c > m.base && c < m.text.length), m.text.length];
  const out: string[] = [];
  for (let i = 0; i < edges.length - 1; i++) {
    for (const part of m.text.slice(edges[i], edges[i + 1]).split(/\n[ \t]*\n/)) {
      const s = part.trim();
      if (s) out.push(s);
    }
  }
  return out;
}

/** 마크다운 기호를 걷어 한 줄 글로 — 한 줄 말줄임 자리에 기호가 보이지 않게. */
function plain(s: string): string {
  return s
    .replace(/```[^\n]*\n?/g, " ")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/\*\*|__|~~/g, "")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s*(#{1,6}\s+|[-*+]\s+|\d+\.\s+|>\s*)/gm, "")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * 진행 메모 한 줄 — 마지막 조각의 **마지막 문장 하나**. 문장 끝은 `.!?。…` 뒤 **공백·줄바꿈**(또는 빈 줄)에서만 나눈다 —
 * 공백 없이 붙은 「…합니다.BGM」은 나누지 않는다(추측 분리 금지). 흘러 들어오는 중이면 쓰다 만 문장이 마지막 문장이다.
 */
export function lastSentence(m: ProgressMemo | undefined): string | null {
  const segs = memoSegments(m);
  const lastSeg = segs[segs.length - 1];
  if (!lastSeg) return null;
  const para = plain(lastSeg);
  if (!para) return null;
  const sentences = para.split(/(?<=[.!?。…])\s+/).map((x) => x.trim()).filter(Boolean);
  return sentences[sentences.length - 1] ?? null;
}
