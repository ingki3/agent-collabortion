/**
 * T-APPROVAL(Director 2026-09-25) — 확인 요청 카드 짝짓기.
 *
 * 실측: 웹 타임라인이 완료 승인 HITL 을 **버튼 카드가 아니라 시스템 문장 한 줄 + 「답글」** 로 그렸다. page.tsx 는 요청 목록에서
 * `message_id` 가 맞는 것을 찾을 때만 카드를 그렸는데, 서버의 일곱 발행 경로 중 둘만 `hitl.created` 를 보냈기 때문에 목록에 요청이
 * 없었다. 규칙: **`kind: hitl` 은 절대 평문으로 떨어지지 않는다.**
 */
import { describe, expect, it } from "vitest";
import { pairHitl, unpairedHitlCards, upsertHitl } from "./hitl-pairing";
import type { HitlRequest, Message } from "@/lib/api/types";

const msg = (id: string, over: Partial<Message> = {}): Message => ({
  id, session_id: "r1", author_type: "system", author_id: null, parent_id: null, content: "[확인 요청 · 승인] 종료 조건이 모두 충족되었습니다. 승인하시겠습니까?",
  mentions: [], source_task_id: null, kind: "hitl", state: "posted", created_at: "2026-09-25T13:41:01Z", work_id: null, ...over,
});
const req = (over: Partial<HitlRequest> = {}): HitlRequest => ({
  id: "h1", session_id: "r1", task_id: null, lane_id: null, source: "system", type: "approval", purpose: "user_approval",
  question: "종료 조건이 모두 충족되었습니다. 승인하시겠습니까?", context: null, options: [], proposed_default: null, artifact_id: null,
  approver_spec: "director", due_at: "2026-09-26T13:41:01Z", overdue: false, status: "open", approved: null, answer: null,
  answered_by: null, answered_at: null, budget_override_usd: null, can_respond: true, can_respond_from: null,
  message_id: "m1", created_at: "2026-09-25T13:41:01Z", ...over,
});

describe("pairHitl — 카드와 요청을 잇는 두 길", () => {
  it("요청의 message_id 로 잇는다(보통 길)", () => {
    expect(pairHitl(msg("m1"), [req()])?.id).toBe("h1");
  });

  it("요청이 message_id 를 아직 안 채웠어도 **메시지의 hitl_request_id** 로 잇는다(계약 Message.hitl_request_id)", () => {
    expect(pairHitl(msg("m1", { hitl_request_id: "h1" }), [req({ message_id: null })])?.id).toBe("h1");
  });

  it("hitl 이 아닌 메시지는 짝을 찾지 않는다 — 일반 메시지에 카드를 그리지 않는다", () => {
    expect(pairHitl(msg("m1", { kind: "text" }), [req()])).toBeUndefined();
  });

  it("짝이 없으면 undefined — 호출부가 「불러오는 중」 자리를 그린다(평문 금지)", () => {
    expect(pairHitl(msg("m9"), [req()])).toBeUndefined();
  });
});

describe("unpairedHitlCards — 무엇을 더 불러야 하는가", () => {
  it("메시지가 요청 id 를 알면 그 요청만 읽는다(getHitlRequest) — 목록 재조회는 안 한다", () => {
    const got = unpairedHitlCards([msg("m1", { hitl_request_id: "h7" })], []);
    expect(got).toEqual({ ids: ["h7"], refetch: false, messageIds: ["m1"] });
  });

  it("id 를 모르는 카드가 하나라도 있으면 목록을 다시 읽는다", () => {
    const got = unpairedHitlCards([msg("m1")], []);
    expect(got.refetch).toBe(true);
    expect(got.ids).toEqual([]);
  });

  it("이미 짝이 있는 카드는 부르지 않는다 — 렌더마다 같은 요청을 다시 읽지 않게", () => {
    expect(unpairedHitlCards([msg("m1")], [req()]).messageIds).toEqual([]);
  });

  it("한 번 불러 본 카드는 다시 부르지 않는다 — 404·권한 실패가 무한 루프가 되지 않게", () => {
    expect(unpairedHitlCards([msg("m1")], [], new Set(["m1"])).messageIds).toEqual([]);
  });

  it("같은 요청을 가리키는 카드 둘이면 한 번만 읽는다", () => {
    const got = unpairedHitlCards([msg("m1", { hitl_request_id: "h7" }), msg("m2", { hitl_request_id: "h7" })], []);
    expect(got.ids).toEqual(["h7"]);
    expect(got.messageIds).toEqual(["m1", "m2"]);
  });

  it("hitl 아닌 메시지는 세지 않는다", () => {
    expect(unpairedHitlCards([msg("m1", { kind: "text" }), msg("m2", { kind: "summary" })], []).messageIds).toEqual([]);
  });
});

describe("upsertHitl — 목록 갱신(hitl.created · hitl.updated · 직접 읽기 공용)", () => {
  it("없으면 넣고", () => {
    expect(upsertHitl([], req()).map((h) => h.id)).toEqual(["h1"]);
  });
  it("있으면 **바꾼다** — 답한 요청이 두 줄로 늘어나지 않는다", () => {
    const after = upsertHitl([req()], req({ status: "answered", approved: true }));
    expect(after).toHaveLength(1);
    expect(after[0].status).toBe("answered");
  });
});
