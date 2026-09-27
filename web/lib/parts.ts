/**
 * 부분 메시지 — 한 말풍선, 받는 쪽마다 한 부분 (PRD FR-3.1.4 · SCREEN §4.6 v0.19.11 · COMPONENTS §9.11 · openapi v0.3.6 D26).
 *
 * 서버는 부분마다 메시지 **한 행**을 쓰고 같은 `group_id`·`group_index`·`group_size` 로 묶는다. 라우팅·말의 종류·↩·스레드는 행마다
 * 서버가 이미 정했다 — 화면은 **묶어 그리기만** 한다. 이 파일은 그 묶음을 만드는 순수 함수다.
 *
 *  - `timelineItems` — 타임라인 행을 「메시지 하나」 또는 「묶음 하나」로 접는다. 묶음은 **첫 부분이 놓인 자리**에 선다(부분이 늦게 도착해도,
 *    다른 메시지가 사이에 끼어도 같은 말풍선에 채운다 — 실시간 채움). 부분 순서 = `group_index`.
 *  - `processBoundaries` — 「작업 과정」 조각 경계(v0.19.6 #339)에 쓸 메시지. 묶음은 **경계 하나**(도착한 부분 중 마지막 `group_index`)다 —
 *    부분마다 경계를 두면 작업 과정이 부분 수만큼 쪼개진다.
 */
import type { Message } from "@/lib/api/types";

export interface PartGroup {
  kind: "group";
  groupId: string;
  /** 도착한 부분, `group_index` 순. */
  parts: Message[];
  /** 묶음의 부분 수(`group_size`) — 다 도착했는지 안다. */
  size: number;
}

export type TimelineItem = { kind: "message"; message: Message } | PartGroup;

/** 부분 메시지의 한 행인가 — 묶음 칸 셋이 모두 있어야 한다(옛 서버·옛 행은 `group_id` 없음). */
export function isPart(m: Pick<Message, "group_id" | "group_index" | "group_size">): boolean {
  return !!m.group_id && m.group_index != null && m.group_size != null;
}

const byIndex = (a: Message, b: Message) => (a.group_index ?? 0) - (b.group_index ?? 0);

/** 타임라인 행 → 항목. 입력 순서(시각 순)를 지키고, 묶음은 그 첫 부분 자리에 하나. */
export function timelineItems(messages: readonly Message[]): TimelineItem[] {
  const out: TimelineItem[] = [];
  const groups = new Map<string, PartGroup>();
  for (const m of messages) {
    if (!isPart(m)) {
      out.push({ kind: "message", message: m });
      continue;
    }
    const gid = m.group_id!;
    let g = groups.get(gid);
    if (!g) {
      g = { kind: "group", groupId: gid, parts: [], size: m.group_size! };
      groups.set(gid, g);
      out.push(g);
    }
    if (!g.parts.some((x) => x.id === m.id)) g.parts.push(m);
  }
  for (const g of groups.values()) g.parts.sort(byIndex);
  return out;
}

/** 작업 과정 조각 경계에 쓸 메시지 — 묶음마다 도착한 마지막 부분 하나(나머지 부분은 경계가 아니다). */
export function processBoundaries<M extends Pick<Message, "id" | "group_id" | "group_index" | "group_size">>(messages: readonly M[]): M[] {
  const last = new Map<string, M>();
  for (const m of messages) {
    if (!isPart(m)) continue;
    const cur = last.get(m.group_id!);
    if (!cur || (m.group_index ?? 0) > (cur.group_index ?? 0)) last.set(m.group_id!, m);
  }
  return messages.filter((m) => !isPart(m) || last.get(m.group_id!) === m);
}

/** 묶음의 경계 부분 — `processBoundaries` 가 남긴 것(작업 과정 줄이 붙는 행). */
export function boundaryOf(g: PartGroup): Message {
  return g.parts[g.parts.length - 1];
}
