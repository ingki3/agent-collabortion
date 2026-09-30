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

/**
 * 캐시의 카드 — 계약 `TaskCard` + 이 화면만의 표 하나: `actions_trusted` 는 `actions` 를 **믿을 곳**(getCard · 내 accept/revise 응답)에서
 * 받은 적이 있는가. 방송(`card.*`)으로만 들어온 카드는 false — 말풍선이 그걸 「아직 없음」으로 보고 `getCard` 를 한 번 부른다(#397 B1:
 * 위임 말풍선이 페이지 밖인 방에서 `card.updated` 가 결과 말풍선보다 먼저 오면 Director 메뉴가 영영 안 서던 것).
 */
export type CachedCard = TaskCard & { actions_trusted?: boolean };
export type CardCache = Record<string, CachedCard>;

/**
 * 한 판의 모양 — 현재 판이면 카드 칸 그대로, 지난 판이면 `versions[]` 의 그 판(계약 v0.3.10 #397 B2: 판마다 전체 모양).
 * 지난 판에 칸이 없으면 **현재 판에서 빌리지 않는다** — null(목록은 빈 목록)로 두고 말풍선은 그 칸을 그리지 않는다.
 */
export interface CardVersionView {
  version: number;
  current: boolean;
  goal: string | null;
  criteria: TaskCard["criteria"];
  boundaries: string | null;
  /** null = 그 판의 참고 자료를 모른다(그리지 않는다). */
  refs: TaskCard["refs"] | null;
  output_format: string | null;
  budget_usd: number | null;
  revise_reason: string | null;
  delegate_message_id: string | null;
  result: CardResult | null;
  judgement: CardJudgement | null;
  /** 지난 판은 상태가 없다 — 그 판의 판정(수정 요청)이 말한다. */
  status: CardStatus | null;
}

type VersionSnap = Partial<Pick<TaskCard, "goal" | "criteria" | "boundaries" | "refs" | "output_format" | "budget_usd" | "revise_reason" | "delegate_message_id" | "result" | "judgement">> & { version: number };

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
      delegate_message_id: card.delegate_message_id ?? null, result: card.result ?? null, judgement: card.judgement ?? null, status: card.status,
    };
  }
  const snap = (card.versions as VersionSnap[] | undefined)?.find((x) => x.version === v);
  if (!snap) return null;
  // 빌리지 않는다(#397 B2) — 그 판에 없는 칸은 없는 것이다.
  return {
    version: v, current: false, goal: snap.goal ?? null, criteria: snap.criteria ?? [], boundaries: snap.boundaries ?? null,
    refs: snap.refs ?? null, output_format: snap.output_format ?? null, budget_usd: snap.budget_usd ?? null, revise_reason: snap.revise_reason ?? null,
    delegate_message_id: snap.delegate_message_id ?? null, result: snap.result ?? null, judgement: snap.judgement ?? null, status: null,
  };
}

/** 말풍선이 캐시에 무엇을 더 부탁해야 하나(null = 충분). 판이 없으면 card, 결과 말풍선인데 그 판에 결과가 없으면 result,
 *  사람이 판정할 수 있는 상태(결과 제출 · 수락)의 현재 판인데 `actions` 를 믿을 곳에서 받은 적이 없으면 actions(#397 B1). */
export function cardNeed(card: CachedCard | null, view: CardVersionView | null, role: "delegation" | "result"): "card" | "result" | "actions" | null {
  if (!card || !view) return "card";
  if (role === "result" && !view.result) return "result";
  if (view.current && (card.status === "result_submitted" || card.status === "accepted") && !card.actions_trusted) return "actions";
  return null;
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

const at = (x: string | undefined) => (x ? Date.parse(x) : 0);
/** `a` 가 `b` 보다 옛 모양인가 — 판이 낮거나, 같은 판에서 `updated_at` 이 이르다. */
function older(a: TaskCard, b: TaskCard): boolean {
  return a.version < b.version || (a.version === b.version && at(a.updated_at) < at(b.updated_at));
}

/**
 * `getCard` 응답을 캐시에 — 지난 판까지 가진 정본이라 통째로 바꾸고 `actions` 를 믿는다. 단 캐시가 이미 **더 새것**이면 버린다(#397 NN2:
 * `card.updated` 마다 getCard 를 부르니 두 이벤트가 한 RTT 안에 오면 늦게 도착한 옛 응답이 새 캐시를 덮던 것).
 */
export function cardsOnFetched(cache: CardCache, card: TaskCard): CardCache {
  const prev = cache[card.id];
  if (prev && older(card, prev)) return cache;
  return { ...cache, [card.id]: { ...card, actions_trusted: true } };
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
  // 늦게 도착한 옛 방송(재연결 재생 등)은 버린다 — 판·updated_at 이 캐시보다 이르다.
  if (!o.trustActions && prev && older(card, prev)) return cache;
  let versions = prev?.versions;
  if (prev && card.version > prev.version) {
    const snap: VersionSnap = {
      version: prev.version, goal: prev.goal, criteria: prev.criteria, boundaries: prev.boundaries, refs: prev.refs, output_format: prev.output_format,
      budget_usd: prev.budget_usd, revise_reason: prev.revise_reason ?? null, delegate_message_id: prev.delegate_message_id ?? null,
      result: prev.result ?? null, judgement: prev.judgement ?? null,
    };
    versions = [...((prev.versions as VersionSnap[] | undefined) ?? []).filter((x) => x.version !== prev.version), snap];
  }
  // 방송(SSE)의 actions 는 믿지 않는다 — 처음 보는 카드면 빈 목록(부른 쪽이 getCard 로 채운다). 내 호출의 응답(acceptCard·reviseCard)만 믿는다.
  const actions = o.trustActions ? card.actions ?? [] : (prev?.actions ?? []).filter((a) => actionFits(card.status, a));
  const actions_trusted = o.trustActions ? true : prev?.actions_trusted ?? false;
  return { ...cache, [card.id]: { ...card, versions: card.versions ?? versions, actions, actions_trusted } };
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
  // 읽기 전·실패(null)면 그대로 — 이 카드 한 장으로 표를 만들지 않는다(#397 NN6: 「카드 1」 틀린 머리). 부른 쪽이 다시 읽는다.
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
