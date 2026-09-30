/**
 * 작업 카드(PRD FR-3.8 · SCREEN §4.6 「작업 카드」·「분담표」 · openapi v0.3.10) — 화면이 쓰는 순수 함수. React 없이 테스트한다(`cards.test.ts`).
 *
 * 캐시 하나: 방 화면은 카드 id → `TaskCard` 를 한 곳(`CardCache`)에 두고, 같은 카드의 말풍선 여러 개(위임 1판 · 결과 1판 · 위임 2판 …)가
 * 그 한 캐시를 읽는다 — 말풍선은 메시지의 `card_id`·`card_version` 으로 **그 판**을 꺼낸다(`cardVersion`). 채우는 길은 둘이다:
 *   ① `getCard`(지난 판 `versions` 포함) — 캐시에 없는 카드의 말풍선이 처음 보일 때 한 번.
 *   ② SSE `card.created`·`card.updated`(`versions` 없음 · 보는 사람 모양 칸 `actions` 는 방송이라 믿지 않는다) — `cardsOnUpserted`.
 * 분담표(`CardBoard`)는 `listRoomCards` 로 읽고 같은 이벤트로 행을 제자리 갱신한다(`boardOnCard`).
 */
import type { CardAction, CardBoard, CardBoardItem, CardJudgement, CardResult, CardStatus, TaskCard } from "./api/types";

export type CardCache = Record<string, TaskCard>;

/** 한 판의 모양 — 현재 판이면 카드 칸 그대로, 지난 판이면 `versions[]` 의 그 판(없는 칸은 현재 판에서 빌린다). */
export interface CardVersionView {
  version: number;
  current: boolean;
  goal: string;
  criteria: TaskCard["criteria"];
  boundaries: string;
  refs: TaskCard["refs"];
  output_format: string | null;
  budget_usd: number | null;
  revise_reason: string | null;
  result: CardResult | null;
  judgement: CardJudgement | null;
  /** 지난 판은 상태가 없다 — 그 판의 판정(수정 요청)이 말한다. */
  status: CardStatus | null;
}

type VersionSnap = Partial<Pick<TaskCard, "goal" | "criteria" | "boundaries" | "refs" | "output_format" | "budget_usd" | "revise_reason" | "result" | "judgement">> & { version: number };

/** 사람이 지금 할 수 있는 판정이 그 상태에서 말이 되는가(계약 `TaskCard.actions` 설명 — accept 는 결과 제출에서만, revise 는 결과 제출·수락에서). */
export function actionFits(status: CardStatus, a: CardAction): boolean {
  return a === "accept" ? status === "result_submitted" : status === "result_submitted" || status === "accepted";
}

/** 그 판을 꺼낸다. 판을 모르면(옛 메시지 · 칸 없음) 현재 판. 캐시에 그 판이 없으면 null(아직 `getCard` 전). */
export function cardVersion(card: TaskCard, version: number | null | undefined): CardVersionView | null {
  const v = version ?? card.version;
  if (v === card.version) {
    return {
      version: v, current: true, goal: card.goal, criteria: card.criteria, boundaries: card.boundaries, refs: card.refs,
      output_format: card.output_format, budget_usd: card.budget_usd, revise_reason: card.revise_reason ?? null,
      result: card.result ?? null, judgement: card.judgement ?? null, status: card.status,
    };
  }
  const snap = (card.versions as VersionSnap[] | undefined)?.find((x) => x.version === v);
  if (!snap) return null;
  return {
    version: v, current: false, goal: snap.goal ?? card.goal, criteria: snap.criteria ?? card.criteria, boundaries: snap.boundaries ?? card.boundaries,
    refs: snap.refs ?? card.refs, output_format: snap.output_format !== undefined ? snap.output_format : card.output_format,
    budget_usd: snap.budget_usd !== undefined ? snap.budget_usd : card.budget_usd, revise_reason: snap.revise_reason ?? null,
    result: snap.result ?? null, judgement: snap.judgement ?? null, status: null,
  };
}

/** 결과 카드 머리의 판정 칩 — 판정이 없으면 판정 대기. */
export function judgeOf(v: Pick<CardVersionView, "judgement">): "pending" | "accepted" | "revise_requested" {
  return v.judgement?.action ?? "pending";
}

/** 기준 충족 수 — 서버 `met_count` 가 있으면 그것, 없으면 센다(낮춘 줄은 이미 partial). */
export function metCount(r: CardResult): number {
  return typeof r.met_count === "number" ? r.met_count : r.verdicts.filter((x) => x.verdict === "met").length;
}

/** 「↩ C-3 「목표 앞부분…」」 의 인용 — 20자. */
export function goalExcerpt(goal: string, max = 20): string {
  const t = goal.replace(/\s+/g, " ").trim();
  return t.length > max ? `${t.slice(0, max).trimEnd()}…` : t;
}

/** `getCard` 응답을 캐시에 — 지난 판까지 가진 정본이라 통째로 바꾼다. */
export function cardsOnFetched(cache: CardCache, card: TaskCard): CardCache {
  return { ...cache, [card.id]: card };
}

/**
 * SSE `card.*`(openapi StreamEvent 표 — `TaskCard`, `versions` 없음). 캐시의 카드에 덮되:
 *  - `versions` 는 캐시의 것을 지키고, 판이 올랐으면 **캐시의 현재 판을 지난 판으로 민다**(수정 요청 — 1판 말풍선이 제 칸을 잃지 않게).
 *  - `actions` 는 보는 사람마다 다른 칸이라 방송 값을 믿지 않는다 — 캐시의 동작 중 새 상태에 말이 되는 것만 남긴다(수락되면 「수락」이
 *    사라진다). 새로 생긴 동작은 부른 쪽이 `getCard` 로 다시 읽어 채운다. `trustActions` 는 내 호출의 응답(acceptCard·reviseCard)일 때만.
 *  - 같은 판·같은 `updated_at` 이면 **같은 참조**(React 가 다시 그리지 않게).
 */
export function cardsOnUpserted(cache: CardCache, card: TaskCard, o: { trustActions?: boolean } = {}): CardCache {
  const prev = cache[card.id];
  if (!o.trustActions && prev && prev.version === card.version && prev.updated_at === card.updated_at && prev.status === card.status) return cache;
  let versions = prev?.versions;
  if (prev && card.version > prev.version) {
    const snap: VersionSnap = {
      version: prev.version, goal: prev.goal, criteria: prev.criteria, boundaries: prev.boundaries, refs: prev.refs, output_format: prev.output_format,
      budget_usd: prev.budget_usd, revise_reason: prev.revise_reason ?? null, result: prev.result ?? null, judgement: prev.judgement ?? null,
    };
    versions = [...((prev.versions as VersionSnap[] | undefined) ?? []).filter((x) => x.version !== prev.version), snap];
  }
  // 방송(SSE)의 actions 는 믿지 않는다 — 처음 보는 카드면 빈 목록(부른 쪽이 getCard 로 채운다). 내 호출의 응답(acceptCard·reviseCard)만 믿는다.
  const actions = o.trustActions ? card.actions ?? [] : (prev?.actions ?? []).filter((a) => actionFits(card.status, a));
  return { ...cache, [card.id]: { ...card, versions: card.versions ?? versions, actions } };
}

/** 카드 → 분담표 한 행(계약 `CardBoard.items`). 비용은 결과 카드의 비용(판의 카드 task 합), 없으면 모른다. */
export function boardItemOf(card: TaskCard, prev?: CardBoardItem): CardBoardItem {
  return {
    id: card.id, label: card.label, number: card.number, version: card.version, parent_card_id: card.parent_card_id, assignee: card.assignee,
    goal: card.goal, status: card.status, auto_result: card.result?.auto ?? false, met: card.result ? metCount(card.result) : null,
    total_criteria: card.criteria.length, cost_usd: card.result?.cost_usd ?? prev?.cost_usd ?? null, lane_id: card.lane_id,
    latest_message_id: card.result?.message_id ?? card.delegate_message_id ?? prev?.latest_message_id ?? null,
  };
}

/** `card.*` 로 분담표 행을 제자리 갱신 — 그 미션의 카드만(다른 미션 · 읽기 전이면 그대로). 머리 수(카드 N · 판정 대기 N)도 다시 센다. */
export function boardOnCard(board: CardBoard | null, card: TaskCard): CardBoard | null {
  if (!board || (board.work_id ?? null) !== (card.work_id ?? null)) return board;
  const prev = board.items.find((x) => x.id === card.id);
  const item = boardItemOf(card, prev);
  const items = (prev ? board.items.map((x) => (x.id === card.id ? item : x)) : [...board.items, item]).sort((a, b) => a.number - b.number);
  return { ...board, items, total: items.length, pending_judgement: items.filter((x) => x.status === "result_submitted").length };
}

/** 분담표 트리 — 번호순, 하위 카드는 부모 바로 아래로 들여쓰기(깊이). 부모가 목록에 없으면(다른 미션) 맨 위 층. */
export function boardTree(items: readonly CardBoardItem[]): { item: CardBoardItem; depth: number }[] {
  const ids = new Set(items.map((x) => x.id));
  const kids = new Map<string, CardBoardItem[]>();
  const roots: CardBoardItem[] = [];
  for (const x of [...items].sort((a, b) => a.number - b.number)) {
    if (x.parent_card_id && ids.has(x.parent_card_id) && x.parent_card_id !== x.id) kids.set(x.parent_card_id, [...(kids.get(x.parent_card_id) ?? []), x]);
    else roots.push(x);
  }
  const out: { item: CardBoardItem; depth: number }[] = [];
  const seen = new Set<string>();
  const walk = (x: CardBoardItem, depth: number) => {
    if (seen.has(x.id)) return;
    seen.add(x.id);
    out.push({ item: x, depth });
    for (const k of kids.get(x.id) ?? []) walk(k, depth + 1);
  };
  for (const r of roots) walk(r, 0);
  return out;
}

/** 분담표 행의 상태 칩 — 결과 제출은 「판정 대기」로 읽는다(SCREEN 분담표 그림), 나머지는 카드 상태 그대로. */
export function boardChip(status: CardStatus): { kind: "card"; value: CardStatus } | { kind: "card_judge"; value: "pending" } {
  return status === "result_submitted" ? { kind: "card_judge", value: "pending" } : { kind: "card", value: status };
}
