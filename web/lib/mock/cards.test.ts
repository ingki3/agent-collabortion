/**
 * 목 작업 카드(lib/mock/cards) — 계약 op 다섯과 시드가 서버가 쓰는 모양 그대로인가(openapi v0.3.10 · PRD FR-3.8):
 *  · 시드 — C-1 수락 · C-2 자동 · C-3 판정 대기(근거 없는 met → partial, downgraded) · C-4 하위 카드(parent) · C-5 2판 진행 중(지난 판 versions)
 *  · 말풍선 — 위임 `delegate`/`delegation`, 결과 `report`/`result`(↩ 그 판의 위임 말풍선), 카드 없는 에이전트 멘션은 `question`, 그 답은 `answer`
 *  · 권한 — actions 는 미션 Director 에게만(방송은 빈 목록) · Director 아니면 403 · 판정할 때가 아니면 409 · 사유 없으면 422
 *  · card.* 방송 · 수정 요청은 같은 lane 재진입(판 +1)
 * 회귀 주입(PR 표): report 의 downgraded 판정을 빼면 (C-3) FAIL; actionsFor 의 judgeUser 를 빼면 (권한) FAIL; speech 의 question 을 request 로
 * 되돌리면 (질문) FAIL; revise 의 versions.push 를 빼면 (2판) FAIL; versions 에 계약 밖 칸을 넣거나 빼면 (계약 모양) FAIL(#397 B2);
 * cardFirst 순서를 빼면 (두 순서) FAIL(#397 B1).
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { resetStore, store } from "./store";
import type { CardBoard, Message, Room, TaskCard, Work } from "@/lib/api/types";

let cookie = "";
// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function call(method: string, path: string, body?: unknown): Promise<{ status: number; body: any }> {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(), body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req) as never;
}
async function login(email: string) {
  const res = await dispatch({ method: "POST", path: "/auth/login", query: new URLSearchParams(), headers: new Headers(), body: { email, password: "password123" }, cookies: {} } as Req);
  cookie = /colab_session=([^;]+)/.exec(((res as { headers?: Record<string, string> }).headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
}

describe("작업 카드 목 — seed-cards · op 다섯", () => {
  let rid = "";
  let wid = "";
  let seed: { work_id: string; cards: Record<string, string>; messages: Record<string, string> };
  beforeEach(async () => {
    resetStore();
    await login("demo@colab.dev");
    const me = await call("GET", "/me");
    rid = ((await call("POST", `/workspaces/${me.body.workspaces[0].id}/rooms`, { name: "마리오 카트" })).body as Room).id;
    wid = ((await call("POST", `/rooms/${rid}/works`, { goal: "마리오 카트 만들기" })).body as Work).id;
    const r = await call("POST", `/__mock/rooms/${rid}/seed-cards`, {});
    expect(r.status).toBe(201);
    seed = r.body;
  });

  it("분담표 — 번호순 다섯 · 판정 대기 하나 · C-4 는 C-3 의 하위 · k/N · 비용", async () => {
    const b = (await call("GET", `/rooms/${rid}/cards?work_id=${wid}`)).body as CardBoard;
    expect(b.work_id).toBe(wid);
    expect(b.items.map((x) => x.label)).toEqual(["C-1", "C-2", "C-3", "C-4", "C-5"]);
    expect(b.items.map((x) => x.status)).toEqual(["accepted", "result_submitted", "result_submitted", "in_progress", "in_progress"]);
    expect(b.total).toBe(5);
    expect(b.pending_judgement).toBe(2);
    expect(b.items[3].parent_card_id).toBe(seed.cards["C-3"]);
    expect(b.items.map((x) => x.met)).toEqual([3, 0, 1, null, null]);
    expect(b.items[1].auto_result).toBe(true);
    expect(b.items[0].cost_usd).toBe(0.8);
    expect(b.items[2].latest_message_id).toBe(seed.messages.c3_result);
    expect((await call("GET", `/rooms/${rid}/cards?work_id=none`)).body.items).toEqual([]);
  });

  it("C-3 — 근거 없는 met 은 partial 로 낮춰 저장(downgraded · stated_verdict met) · Director 에게 수락·수정 요청", async () => {
    const c = (await call("GET", `/cards/${seed.cards["C-3"]}`)).body as TaskCard;
    expect(c.result!.verdicts.map((v) => [v.verdict, v.stated_verdict, !!v.downgraded])).toEqual([["met", "met", false], ["partial", "partial", false], ["partial", "met", true]]);
    expect(c.result!.met_count).toBe(1);
    expect(c.result!.assumed.length).toBe(1);
    expect(c.actions).toEqual(["accept", "revise"]);
    expect(c.parent_card_id).toBeNull();
    expect(c.refs.map((r) => r.kind)).toEqual(["artifact", "decision", "message"]);
    expect(c.refs.every((r) => !r.missing)).toBe(true);
  });

  it("말풍선 — 위임 delegate/delegation · 결과 report/result(↩ 위임 말풍선) · 카드 없는 멘션 question · 그 답 answer", async () => {
    const ms = (await call("GET", `/rooms/${rid}/messages?limit=200`)).body.items as Message[];
    const c3 = (await call("GET", `/cards/${seed.cards["C-3"]}`)).body as TaskCard;
    const del = ms.find((m) => m.id === c3.delegate_message_id)!;
    expect([del.speech, del.card_role, del.card_version, del.card_id]).toEqual(["delegate", "delegation", 1, c3.id]);
    const res = ms.find((m) => m.id === seed.messages.c3_result)!;
    expect([res.speech, res.card_role, res.responds_to_message_id]).toEqual(["report", "result", c3.delegate_message_id]);
    expect(res.addressees!.map((a) => a.name)).toEqual(["Lead"]);
    expect(ms.find((m) => m.id === seed.messages.question)!.speech).toBe("question");
    expect(ms.find((m) => m.id === seed.messages.answer)!.speech).toBe("answer");
    expect(ms.filter((m) => m.card_role === "delegation").length).toBe(6); // 다섯 카드 + C-5 2판
  });

  it("C-5 2판 — 지난 판(versions)에 1판 결과·수정 요청 판정 · 지금 판은 진행 중 · 지워진 참고 칩은 missing", async () => {
    const c = (await call("GET", `/cards/${seed.cards["C-5"]}`)).body as TaskCard;
    expect(c.version).toBe(2);
    expect(c.status).toBe("in_progress");
    expect(c.revise_reason).toBeTruthy();
    const v1 = (c.versions as { version: number; judgement: { action: string }; result: { summary: string } }[])[0];
    expect(v1.version).toBe(1);
    expect(v1.judgement.action).toBe("revise_requested");
    expect(v1.result.summary).toBeTruthy();
    expect(c.refs.map((r) => r.missing)).toEqual([false, true]);
    expect(store().lanes.get(c.lane_id)!.reentry_count).toBe(1);
    // 계약 모양 그대로(openapi v0.3.10 #397 B2) — 판마다 전체 모양, 더도 덜도 아닌 키.
    expect(Object.keys(c.versions![0]).sort()).toEqual(["boundaries", "budget_usd", "criteria", "delegate_message_id", "goal", "judgement", "output_format", "refs", "result", "revise_reason", "version"]);
    const v1b = c.versions![0] as { refs: { missing: boolean }[]; delegate_message_id: string };
    expect(v1b.refs.map((r) => r.missing)).toEqual([false]); // 1판의 참고 자료(2판의 지워진 칩이 아님)
    expect(v1b.delegate_message_id).not.toBe(c.delegate_message_id);
    expect(c.actions).toEqual([]);
  });

  it("사람의 판정 — 수락은 result_submitted 에서만(409) · 수락 취소 = revise(사유 필수 422) · 판 +1 · card.updated 방송은 actions 비움", async () => {
    const id = seed.cards["C-3"];
    const acc = await call("POST", `/cards/${id}/accept`, { comment: "테스트 로그를 열어 확인했습니다" });
    expect(acc.status).toBe(200);
    expect(acc.body.status).toBe("accepted");
    expect(acc.body.actions).toEqual(["revise"]);
    expect(acc.body.judgement.by.kind).toBe("user");
    expect((await call("POST", `/cards/${id}/accept`, { comment: "또" })).status).toBe(409);
    expect((await call("POST", `/cards/${id}/revise`, { reason: " " })).status).toBe(422);
    const rv = await call("POST", `/cards/${id}/revise`, { reason: "대각선 벽도" });
    expect(rv.status).toBe(200);
    expect(rv.body.card.version).toBe(2);
    expect(rv.body.message.card_version).toBe(2);
    // #400 리뷰 400b NN3: task 는 계약 Task 모양(서버 tasks.ToAPI 와 같은 칸) — id 만이 아니다.
    expect(rv.body.task).toMatchObject({ id: expect.any(String), lane_id: expect.any(String), agent_id: expect.any(String), status: "queued", attempt: 1 });
    expect(Object.keys(rv.body.task)).toEqual(expect.arrayContaining(["session_id", "trigger_message_id", "created_at"]));
    const ev = store().events.filter((e) => e.type === "card.updated").at(-1)!;
    expect((ev.payload as TaskCard).actions).toEqual([]);
    expect((ev.payload as TaskCard).versions).toBeUndefined();
  });

  it("권한 — 미션 Director 가 아니면 actions 비고 accept 403", async () => {
    await login("seoyeon@colab.dev");
    const c = await call("GET", `/cards/${seed.cards["C-3"]}`);
    expect(c.status).toBe(200);
    expect(c.body.actions).toEqual([]);
    expect((await call("POST", `/cards/${seed.cards["C-3"]}/accept`, { comment: "봤다" })).status).toBe(403);
  });

  // T-CARD-COMMENT(openapi v0.3.11 · PRD FR-3.8 4) — 수락에는 코멘트가 필수: 없음·빈 값·공백만은 서버처럼 422 judgement_comment_required
  // (errors[] 같은 모양) · 카드는 그대로 판정 대기 · 통과하면 trim 해 judgement.comment 에, reason 은 null. 시드 C-1 수락에도 코멘트.
  it("수락 코멘트 — 없음·빈·공백만 422 judgement_comment_required · 600자 넘으면 422 · 통과하면 judgement.comment", async () => {
    const id = seed.cards["C-3"];
    for (const body of [undefined, {}, { comment: "" }, { comment: "  \n " }]) {
      const r = await call("POST", `/cards/${id}/accept`, body);
      expect(r.status).toBe(422);
      expect(r.body.code).toBe("judgement_comment_required");
      expect(r.body.errors).toEqual([{ field: "comment", code: "judgement_comment_required", message: "무엇을 확인했는지 코멘트를 적으세요" }]);
    }
    expect((await call("POST", `/cards/${id}/accept`, { comment: "가".repeat(601) })).status).toBe(422);
    expect((await call("GET", `/cards/${id}`)).body.status).toBe("result_submitted");
    const ok = await call("POST", `/cards/${id}/accept`, { comment: "  기준 1·2 는 로그로, 3 은 화면으로 확인  " });
    expect(ok.status).toBe(200);
    expect(ok.body.judgement).toMatchObject({ action: "accepted", comment: "기준 1·2 는 로그로, 3 은 화면으로 확인", reason: null });
    const c1 = (await call("GET", `/cards/${seed.cards["C-1"]}`)).body as TaskCard;
    expect(c1.judgement).toMatchObject({ action: "accepted", comment: "표의 5종과 출처 링크를 열어 확인했습니다", reason: null });
    // 수정 요청은 영향 없음 — 사유만, comment 는 null.
    const rv = await call("POST", `/cards/${id}/revise`, { reason: "대각선 벽도" });
    expect(rv.status).toBe(200);
    const v = (await call("GET", `/cards/${id}`)).body.versions.at(-1);
    expect(v.judgement).toMatchObject({ action: "revise_requested", reason: "대각선 벽도", comment: null });
  });

  it("결과 제출 op — 기준을 빠짐없이(422) · 진행 중이 아니면 409 · 낮춘 번호는 downgraded·notice", async () => {
    const id = seed.cards["C-4"];
    expect((await call("POST", `/cards/${id}/result`, { summary: "x", verdicts: [], confirmed: ["x"], assumed: [] })).status).toBe(422);
    // #400 리뷰 400b NN2: openapi CardResultInput.confirmed minItems 1 — 서버처럼 422, 칸 confirmed.
    const noConfirmed = await call("POST", `/cards/${id}/result`, { summary: "했다", verdicts: [{ criterion: 1, verdict: "met" }], confirmed: [], assumed: [] });
    expect(noConfirmed.status).toBe(422);
    expect(noConfirmed.body.errors.map((e: { field: string }) => e.field)).toEqual(["confirmed"]);
    const r = await call("POST", `/cards/${id}/result`, { summary: "했다", verdicts: [{ criterion: 1, verdict: "met" }], confirmed: ["봤다"], assumed: [] });
    expect(r.status).toBe(201);
    expect(r.body.downgraded).toEqual([1]);
    expect(r.body.notice).toContain("1");
    expect((await call("POST", `/cards/${id}/result`, { summary: "또", verdicts: [{ criterion: 1, verdict: "met" }], confirmed: ["x"], assumed: [] })).status).toBe(409);
  });
});

describe("결과 제출의 두 순서 — 말풍선 먼저(기본) · 카드 먼저(?order=card_first) (#397 B1)", () => {
  it("두 순서 모두 내고, 카드 먼저면 card.updated 의 result.message_id 가 곧 올 말풍선 id", async () => {
    resetStore();
    await login("demo@colab.dev");
    const me = await call("GET", "/me");
    const rid = ((await call("POST", `/workspaces/${me.body.workspaces[0].id}/rooms`, { name: "순서" })).body as Room).id;
    const wid = ((await call("POST", `/rooms/${rid}/works`, { goal: "g" })).body as Work).id;
    const seed = (await call("POST", `/__mock/rooms/${rid}/seed-cards`, { work_id: wid })).body as { cards: Record<string, string> };
    const order = (from: number) => store().events.slice(from).filter((e) => e.type === "card.updated" || (e.type === "message.created" && (e.payload as Message).card_role === "result")).map((e) => e.type);
    const body = { summary: "했다", verdicts: [{ criterion: 1, verdict: "partial", note: "n" }], confirmed: ["봤다"], assumed: [] };

    let from = store().events.length;
    const a = await call("POST", `/cards/${seed.cards["C-4"]}/result`, body);
    expect(a.status).toBe(201);
    expect(order(from)).toEqual(["message.created", "card.updated"]);

    // 둘째 결과를 내려면 진행 중 카드가 하나 더 필요 — C-5(2판 진행 중).
    from = store().events.length;
    const b = await call("POST", `/cards/${seed.cards["C-5"]}/result?order=card_first`, { ...body, verdicts: [1, 2, 3].map((n) => ({ criterion: n, verdict: "partial", note: "n" })) });
    expect(b.status).toBe(201);
    expect(order(from)).toEqual(["card.updated", "message.created"]);
    const ev = store().events.slice(from).find((e) => e.type === "card.updated")!;
    expect((ev.payload as TaskCard).result!.message_id).toBe(b.body.message.id);
  });
});
