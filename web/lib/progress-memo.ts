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
 *
 * **잘린 스냅숏(v0.10.2)** — 아주 긴 턴이면 데몬이 `preview.text` 를 마지막 16,000자로 자르고 맨 앞에 `…(앞부분 생략)` 한 줄을 붙인다
 * (Lead 판정 2026-09-26). 그 델타는 앞 전문의 연장이 아니라 **꼬리**이므로, 겹치는 부분을 찾아 이어 붙여(`spliceElided`) 기준점·경계를 지킨다
 * — 그래야 게시 뒤 말풍선이 게시 전 메모를 다시 보여 주지 않는다. 겹침을 못 찾으면(그 사이에 16,000자가 더 흘렀다) 잘린 꼬리부터 새로 센다.
 */

/** 데몬이 잘린 스냅숏 앞에 붙이는 한 줄(acp.PreviewElided) — 표시는 안 하고 이어 붙이기 판정에만 쓴다. */
export const ELIDED_MARK = "…(앞부분 생략)";
/**
 * 이어 붙이기를 인정하는 **최소 겹침**. 우연히 겹친 몇 글자로 앞 전문을 이어 버리면 없던 글이 생긴다 — 그보다는 꼬리부터 새로 세는 편이 안전하다.
 * 데몬은 16,000자를 보내므로 실제로는 겹침이 이보다 훨씬 길다.
 */
const MIN_OVERLAP = 24;

/**
 * 잘린 꼬리를 앞 전문에 이어 붙인다. 꼬리는 그 시점 전문의 **접미사**이므로 「앞 전문의 끝」과 「꼬리의 앞」이 겹친다 — 가장 긴 겹침을 찾아
 * 그 뒤만 보탠다. 꼬리가 이미 앞 전문 안에 다 있으면(같은 스냅숏 재수신) 앞 전문 그대로. 겹침이 `MIN_OVERLAP` 에 못 미치면 null.
 */
function spliceElided(prevText: string, tail: string): string | null {
  if (!tail) return null;
  if (prevText.endsWith(tail)) return prevText;
  const max = Math.min(prevText.length, tail.length);
  for (let k = max; k >= MIN_OVERLAP; k--) {
    if (prevText.endsWith(tail.slice(0, k))) return prevText + tail.slice(k);
  }
  return null;
}

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
  const sameTask = !!prev && (prev.taskId == null || d.task_id == null || prev.taskId === d.task_id);
  const elided = d.text.startsWith(ELIDED_MARK);
  const tail = elided ? d.text.slice(ELIDED_MARK.length).replace(/^\n+/, "") : d.text;
  // 잘린 스냅숏이면 앞 전문에 이어 붙인다 — 이어 붙지 않으면(그 사이가 통째로 잘렸다) 꼬리부터 새로.
  const text = elided && sameTask ? spliceElided(prev.text, tail) ?? tail : tail;
  const same = sameTask && text.startsWith(prev.text);
  if (!same) return { ...memos, [d.agent_id]: { taskId, text, base: 0, cuts: [], pendingCut: false } };
  if (prev.text === text && !prev.pendingCut) return memos;
  const cuts = [...prev.cuts];
  const last = cuts.length ? cuts[cuts.length - 1] : prev.base;
  if (prev.pendingCut && text.length > prev.text.length && prev.text.length > last) cuts.push(prev.text.length);
  const pendingCut = prev.pendingCut && text.length === prev.text.length;
  return { ...memos, [d.agent_id]: { ...prev, taskId, text, cuts, pendingCut } };
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

/** 진행 메모가 살아 있는 task 상태 — 이 밖이면 그 턴은 끝났다(`lib/feed` 의 `LIVE_TASK_STATUSES` 와 같은 표). */
const LIVE_TASK_STATUSES: ReadonlySet<string> = new Set(["dispatched", "preparing", "running", "waiting_human"]);
/** 턴을 닫는 런타임 줄(`lib/feed` 의 `isTurnClose` 와 같은 표). */
const TURN_CLOSE_VERBS: ReadonlySet<string> = new Set(["turn_end", "error", "cancel"]);

/**
 * SSE 프레임 하나 → 진행 메모. **화면이 아니라 여기가 규칙의 자리다** — 말풍선은 턴이 도는지(`workingTasks`)로 한 번 더 걸러지므로
 * 메모가 남아 있어도 화면에서는 안 보이고, 그래서 「끝난 턴의 메모를 버린다」는 규칙을 화면 테스트로는 잴 수 없다(PR #356 리뷰 NN3).
 *
 * 끝나는 길은 셋이고 **서로를 기다리지 않는다**: 턴을 닫는 기록 줄(`task_event.appended` 의 `turn_end`·`error`·`cancel`) ·
 * `task.updated` 가 끝난 상태 · 서브 미션이 더는 안 도는 `lane.updated`. 데몬이 죽으면 `task.updated` 하나만 오기도 한다.
 */
export function memoEffect(memos: ProgressMemos, ev: { type: string; payload: unknown }): ProgressMemos {
  const p = ev.payload as Record<string, unknown> | null | undefined;
  if (!p) return memos;
  switch (ev.type) {
    case "message.delta":
      return applyDelta(memos, p as unknown as { agent_id: string; task_id?: string | null; text: string });
    case "task_event.appended": {
      const te = p as unknown as { task_id: string; class: string; verb?: string | null; superseded_by?: string | null };
      if (te.class === "runtime" && !te.superseded_by && TURN_CLOSE_VERBS.has(te.verb ?? "")) return dropMemo(memos, { taskId: te.task_id });
      return noteToolEvent(memos, te);
    }
    case "task.updated": {
      const t = p as unknown as { id: string; status: string };
      return LIVE_TASK_STATUSES.has(t.status) ? memos : dropMemo(memos, { taskId: t.id });
    }
    case "lane.updated": {
      const l = p as unknown as { agent_id: string; status: string };
      return l.status === "running" ? memos : dropMemo(memos, { agentId: l.agent_id });
    }
    case "message.created": {
      const m = p as unknown as { author_type: string; author_id?: string | null };
      return m.author_type === "agent" && m.author_id ? notePosted(memos, m.author_id) : memos;
    }
    default:
      return memos;
  }
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
