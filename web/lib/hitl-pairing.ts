/**
 * HITL 카드 짝짓기(T-APPROVAL, Director 2026-09-25) — 타임라인의 `kind: hitl` 메시지를 그 확인 요청(`HitlRequest`)과 잇는다.
 *
 * 카드는 `message.created` 로, 요청은 목록(`listHitlRequests`)·`hitl.created` 로 따로 온다. 둘 중 요청이 늦거나 빠지면
 * (서버가 `hitl.created` 를 안 보냈던 경로 — 종료 조건 승인·예산·루프·격리) 화면은 카드 대신 **시스템 문장 한 줄 + 「답글」** 을
 * 그렸다. 규칙: **`kind: hitl` 은 절대 평문으로 떨어지지 않는다** — 짝이 없으면 요청을 직접 읽어 오고(`getHitlRequest`,
 * 메시지의 `hitl_request_id` 로), 그것도 모르면 목록을 다시 읽는다. 그 사이에는 「불러오는 중」 카드 자리를 그린다.
 */
import type { HitlRequest, Message } from "@/lib/api/types";

/** 메시지의 요청 — 요청의 `message_id` 로, 없으면 메시지의 `hitl_request_id` 로 찾는다. */
export function pairHitl(m: Pick<Message, "id" | "kind" | "hitl_request_id">, hitls: HitlRequest[]): HitlRequest | undefined {
  if (m.kind !== "hitl") return undefined;
  return hitls.find((h) => h.message_id === m.id) ?? (m.hitl_request_id ? hitls.find((h) => h.id === m.hitl_request_id) : undefined);
}

/**
 * 짝이 없는 `hitl` 카드가 무엇을 불러야 하는가.
 *   · `ids`     — 메시지가 요청 id 를 알면 그 요청을 하나씩 읽는다(`getHitlRequest`).
 *   · `refetch` — id 도 모르는 카드가 있으면 목록을 다시 읽는다.
 * `tried` 에 든 것(이미 한 번 불렀다)은 다시 부르지 않는다 — 실패가 무한 루프가 되지 않게.
 */
export function unpairedHitlCards(
  messages: Pick<Message, "id" | "kind" | "hitl_request_id">[],
  hitls: HitlRequest[],
  tried: ReadonlySet<string> = new Set(),
): { ids: string[]; refetch: boolean; messageIds: string[] } {
  const ids: string[] = [];
  const messageIds: string[] = [];
  let refetch = false;
  for (const m of messages) {
    if (m.kind !== "hitl" || pairHitl(m, hitls) || tried.has(m.id)) continue;
    messageIds.push(m.id);
    if (m.hitl_request_id) ids.push(m.hitl_request_id);
    else refetch = true;
  }
  return { ids: [...new Set(ids)], refetch, messageIds };
}

/** 목록에 요청 하나를 넣거나 바꾼다(`hitl.created`·`hitl.updated`·직접 읽기 공용). */
export function upsertHitl(cur: HitlRequest[], h: HitlRequest): HitlRequest[] {
  return cur.some((x) => x.id === h.id) ? cur.map((x) => (x.id === h.id ? h : x)) : [...cur, h];
}
