/**
 * 메시지별 「작업 과정」 조각(T-FEED A·B, Director 요청 2026-09-25 · SCREEN §4.6 v0.19.6).
 *
 * 한 턴(task)이 메시지를 여럿 남기고, 메시지를 올린 뒤에도 같은 턴에서 일을 계속한다(실측: Lead 턴 하나가 위임 메시지 뒤 103줄).
 * 메시지의 「작업 과정」을 task 전체로 그리면 메시지 뒤의 현재 작업이 옛 메시지 아래에 계속 쌓이고, 한 턴의 메시지가 모두 같은 전체를 보인다.
 * 그래서 task 의 기록을 **메시지 경계로 자른다**:
 *
 *  - 경계(cut) = 그 메시지를 올린 `status/post_message` 줄의 시각(`payload.result_ref`·`object_ref` = message.id, 서버 router/service.go).
 *    그 줄이 없으면 `message.created_at`.
 *  - 메시지 k 의 조각 = (앞 메시지 경계, 이 메시지 경계] — 첫 메시지는 턴의 처음부터. 조각끼리 겹치지 않는다.
 *  - 마지막 경계 뒤(꼬리): 턴이 **돌고 있으면** 타임라인 맨 아래 「작업 중」 줄의 몫, **끝났으면** 마지막 메시지의 조각에 붙인다.
 *    메시지가 하나도 없는 턴이면 조각이 없다(빈 턴 규칙 — FR-7.2 · `isEmptyTurn`).
 *
 * 시각으로 자르는 이유: 서버 발행 줄(post_message 등)의 seq 는 `ServerSeqBase`(2^30)부터라 데몬 줄의 seq 와 순서를 비교할 수 없다.
 * 한 행(같은 tool_call_id 로 접힌 started → ok)은 **처음 줄의 시각**이 속한 조각에 산다 — 걸친 호출을 두 조각이 나눠 갖지 않는다.
 */
import { foldEvents, toolCallKey, type RailRow } from "@/components/ActivityRail";
import { isTaskLive } from "@/lib/feed";
import type { Lane, Message, Task, TaskEvent } from "@/lib/api/types";

/** 시각 창 (after, until] — ms. null 은 열린 끝. */
export interface ProcessWindow {
  after: number | null;
  until: number | null;
}

export interface ProcessSlices {
  /** message.id → 그 메시지의 조각. */
  byMessage: Map<string, ProcessWindow>;
  /** 턴이 도는 동안 마지막 메시지 뒤의 조각(「작업 중」 줄). 끝난 턴 · 메시지 없는 도는 턴이 아니면 null. */
  tail: ProcessWindow | null;
}

const ms = (iso: string) => Date.parse(iso);

export function inWindow(e: Pick<TaskEvent, "created_at">, w: ProcessWindow | null | undefined): boolean {
  if (!w) return true;
  const t = ms(e.created_at);
  if (Number.isNaN(t)) return w.after == null;
  return (w.after == null || t > w.after) && (w.until == null || t <= w.until);
}

/** 메시지의 경계 시각 — 그 메시지를 올린 post_message 줄, 없으면 message.created_at. */
export function messageCut(events: readonly TaskEvent[], m: Pick<Message, "id" | "created_at">): number {
  for (const e of events) {
    if (e.superseded_by || e.class !== "status" || e.verb !== "post_message") continue;
    const ref = (e.payload as { result_ref?: unknown } | null | undefined)?.result_ref;
    if (ref === m.id || e.object_ref === m.id) {
      const t = ms(e.created_at);
      if (!Number.isNaN(t)) return t;
    }
  }
  return ms(m.created_at);
}

/**
 * task 의 기록을 그 task 가 올린 메시지들로 자른다. `messages` 는 이 task 가 올린 것(순서·중복 무관).
 * `live` = 턴이 도는가(`isTaskLive`).
 */
export function sliceProcess(events: readonly TaskEvent[], messages: readonly Pick<Message, "id" | "created_at">[], live: boolean): ProcessSlices {
  const seen = new Set<string>();
  const cuts = messages
    .filter((m) => (seen.has(m.id) ? false : (seen.add(m.id), true)))
    .map((m) => ({ id: m.id, cut: messageCut(events, m), created: m.created_at }))
    .sort((a, b) => a.cut - b.cut || (a.created < b.created ? -1 : a.created > b.created ? 1 : 0));
  const byMessage = new Map<string, ProcessWindow>();
  let after: number | null = null;
  cuts.forEach((c, i) => {
    const last = i === cuts.length - 1;
    byMessage.set(c.id, { after, until: last && !live ? null : c.cut });
    after = c.cut;
  });
  const tail = live ? { after, until: null } : null;
  return { byMessage, tail };
}

/** 조각 안의 기록 — 같은 tool_call_id 로 접히는 줄은 **처음 줄의 시각**으로 한 조각에 모은다(started 는 앞, ok 는 뒤 조각으로 찢지 않는다). */
export function eventsInWindow(events: readonly TaskEvent[], w: ProcessWindow | null | undefined): TaskEvent[] {
  if (!w) return [...events];
  const firstAt = new Map<string, string>();
  for (const e of [...events].sort((a, b) => ms(a.created_at) - ms(b.created_at))) {
    const k = toolCallKey(e);
    if (k && !firstAt.has(k)) firstAt.set(k, e.created_at);
  }
  return events.filter((e) => {
    const k = toolCallKey(e);
    return inWindow({ created_at: (k && firstAt.get(k)) || e.created_at }, w);
  });
}

/** 조각 안의 행 — `foldEvents` 로 접은 뒤 처음 줄의 시각으로 거른다. */
export function rowsInWindow(events: TaskEvent[], w: ProcessWindow | null | undefined): RailRow[] {
  const rows = foldEvents(events);
  return w ? rows.filter((r) => inWindow(r.first, w)) : rows;
}

// ── 방 화면의 조각 표 ─────────────────────────────────────────────────────
type TaskFacts = Pick<Task, "status" | "attempt">;
export interface TaskRecord {
  events: TaskEvent[];
  /** getTask · task.updated 로 안 상태. 없으면 lane.current_task, 그것도 없으면 이벤트만으로. */
  task?: TaskFacts | null;
}

/**
 * task id → 조각. 메시지는 방에 보이는 에이전트 메시지(스레드 답글 포함) 전부 — 경계가 되려면 세 층이든 아니든 센다.
 * `workingIds` = 「작업 중」 줄이 그려지는 task(`workingTasks`). **꼬리는 그 줄이 있을 때만 떼어 낸다** — 줄이 없는데 떼면 꼬리가 어디에도 안 보인다.
 */
export function roomProcessSlices(
  records: Readonly<Record<string, TaskRecord | undefined>>,
  messages: readonly Pick<Message, "id" | "created_at" | "source_task_id" | "author_type">[],
  workingIds: ReadonlySet<string>,
): Map<string, ProcessSlices> {
  const byTask = new Map<string, Pick<Message, "id" | "created_at">[]>();
  for (const m of messages) {
    if (m.author_type !== "agent" || !m.source_task_id) continue;
    const l = byTask.get(m.source_task_id) ?? [];
    l.push(m);
    byTask.set(m.source_task_id, l);
  }
  const out = new Map<string, ProcessSlices>();
  for (const [tid, ms] of byTask) {
    const rec = records[tid];
    if (!rec) continue;
    out.set(tid, sliceProcess(rec.events, ms, workingIds.has(tid)));
  }
  return out;
}

/**
 * 「작업 중」 줄의 대상 — 도는 서브 미션(lane running)의 현재 할 일 중 턴이 실제로 도는 것(`isTaskLive`). **에이전트마다 하나**(가장 최근 갱신).
 * 기록을 아직 못 읽었으면 뺀다(읽히면 다시 그린다).
 */
export function workingTasks(
  lanes: readonly Pick<Lane, "status" | "agent_id" | "current_task" | "updated_at">[],
  records: Readonly<Record<string, TaskRecord | undefined>>,
): { taskId: string; agentId: string }[] {
  const byAgent = new Map<string, { taskId: string; agentId: string; at: string }>();
  for (const l of lanes) {
    const t = l.current_task;
    if (l.status !== "running" || !t) continue;
    const rec = records[t.id];
    if (!rec) continue;
    const facts = rec.task ?? t;
    if (!isTaskLive(rec.events, { taskStatus: facts.status, attempt: facts.attempt })) continue;
    const prev = byAgent.get(l.agent_id);
    if (!prev || prev.at < l.updated_at) byAgent.set(l.agent_id, { taskId: t.id, agentId: l.agent_id, at: l.updated_at });
  }
  return [...byAgent.values()].map(({ taskId, agentId }) => ({ taskId, agentId }));
}
