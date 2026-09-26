/**
 * 대화 배치 — 화면은 서버 판정(openapi v0.3.2 `speech`·`addressees`·`responds_to_message_id`·`delegated_lane_id`)을 그대로 쓴다.
 * 목의 판정표(lib/mock/speech.ts)는 서버 `internal/messages/speech.go` 를 옮긴 것이고, 여기서 PRD FR-3.1.3 표 11행을 그 목으로 잰다.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "@/lib/mock/handlers";
import { resetStore } from "@/lib/mock/store";
import type { Lane, Message, MessagePage, Room } from "@/lib/api/types";
import { excerpt, groupsWith, speechOf, type ConversationCtx } from "./conversation";

let cookie = "";
async function call(method: string, path: string, body?: unknown, headers?: Record<string, string>) {
  const [p, qs] = path.split("?");
  const req: Req = {
    method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(headers ?? {}), body,
    cookies: cookie ? { colab_session: cookie } : {},
  };
  return dispatch(req);
}
async function must<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<T> {
  const res = await call(method, path, body, headers);
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(res.body)}`);
  return res.body as T;
}

function ctx(msgs: Message[], lanes: Lane[] = []): ConversationCtx {
  const byId = new Map(msgs.map((m) => [m.id, m]));
  return {
    lanes,
    messageById: (id) => byId.get(id),
    authorName: (m) => m.author?.name ?? (m.author_type === "system" ? "시스템" : "agent"),
  };
}

describe("speechOf — 서버 칸을 화면 모양으로", () => {
  const base = {
    id: "m1", session_id: "r", author_type: "agent", author_id: "a1", parent_id: null, content: "x",
    mentions: [], source_task_id: null, kind: "text", state: "posted", created_at: "2026-09-25T12:00:00Z",
  } as unknown as Message;

  it("서버가 내려준 speech·addressees 를 그대로 쓴다", () => {
    const m = { ...base, speech: "instruct", addressees: [{ kind: "agent", id: "a2", name: "Lead" }] } as Message;
    expect(speechOf(m, ctx([m]))).toMatchObject({ kind: "instruct", to: [{ kind: "agent", id: "a2", name: "Lead" }] });
  });

  it("받는 쪽이 비면 「방 전체」, 메모는 「기록만」", () => {
    expect(speechOf({ ...base, speech: "chat", addressees: [] } as Message, ctx([])).to).toEqual([{ kind: "room", name: "" }]);
    expect(speechOf({ ...base, speech: "note", addressees: [] } as Message, ctx([])).to).toEqual([{ kind: "record", name: "" }]);
  });

  it("위임은 `delegated_lane_id` 의 lane 을 붙인다(상태 칩) — 목록에 없으면 칩 없이 라벨만", () => {
    const lane = { id: "l1", status: "running" } as Lane;
    const m = { ...base, speech: "delegate", addressees: [{ kind: "agent", id: "a2", name: "Researcher" }], delegated_lane_id: "l1" } as Message;
    expect(speechOf(m, ctx([m], [lane])).lane?.status).toBe("running");
    expect(speechOf(m, ctx([m], [])).lane).toBeUndefined();
  });

  it("보고는 `responds_to_message_id` 의 메시지를 인용한다 — 못 읽으면 링크 없이 라벨만", () => {
    const trig = { ...base, id: "m0", author_type: "agent", author: { name: "Lead" }, content: "[@Researcher](mention://agent/a1) 법 통과 여부를 조사해 주세요." } as Message;
    const m = { ...base, speech: "report", addressees: [{ kind: "agent", id: "a2", name: "Lead" }], responds_to_message_id: "m0" } as Message;
    expect(speechOf(m, ctx([trig, m]))).toMatchObject({ kind: "report", reportOf: { messageId: "m0", requester: "Lead", excerpt: "법 통과 여부를 조사해 주세요." } });
    expect(speechOf(m, ctx([m])).reportOf).toBeUndefined();
  });

  it("서버가 칸을 안 내려주면(옛 서버) 대화로 읽는다 — 짐작하지 않는다", () => {
    expect(speechOf(base, ctx([base]))).toMatchObject({ kind: "chat", to: [{ kind: "room" }] });
  });

  it("excerpt — 앞 멘션을 떼고 40자", () => {
    expect(excerpt(`[@R](mention://agent/a1) ${"가".repeat(50)}`)).toBe(`${"가".repeat(40)}…`);
  });

  it("묶음 — 같은 작성자 · 종류 · 받는 쪽 · 5분 안, 말풍선끼리만", () => {
    const a = { ...base, id: "a", created_at: "2026-09-25T12:00:00Z" } as Message;
    const b = { ...base, id: "b", created_at: "2026-09-25T12:02:00Z" } as Message;
    const s = { kind: "report" as const, to: [{ kind: "agent" as const, id: "a2", name: "Lead" }] };
    expect(groupsWith({ m: a, s }, { m: b, s })).toBe(true);
    expect(groupsWith({ m: a, s }, { m: b, s: { ...s, to: [{ kind: "agent", id: "a3", name: "W" }] } })).toBe(false);
    expect(groupsWith({ m: a, s }, { m: { ...b, created_at: "2026-09-25T12:06:00Z" } as Message, s })).toBe(false);
    expect(groupsWith({ m: { ...a, kind: "blocked_q" } as Message, s }, { m: b, s })).toBe(false);
  });
});

// ── 목 판정표(서버 speech.go 를 옮긴 것) — PRD FR-3.1.3 표 11행 ────────────────
describe("목 판정 — seed-conversation 이 표대로 speech·addressees 를 싣는다", () => {
  beforeEach(async () => {
    resetStore();
    const res = await call("POST", "/auth/login", { email: "demo@colab.dev", password: "password123" });
    cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
  });

  it("지시 → 위임 둘 → 보고 → 질문 · 답 → 보고 둘(묶음) → 메모", async () => {
    const me = await must<{ workspaces: { id: string }[] }>("GET", "/me");
    const room = await must<Room>("POST", `/workspaces/${me.workspaces[0].id}/rooms`, { name: "STO" });
    const seeded = await must<{ question_id: string; delegate_ids: string[]; order_id: string }>("POST", `/__mock/rooms/${room.id}/seed-conversation`, {});
    const page = await must<MessagePage>("GET", `/rooms/${room.id}/messages?limit=50`);
    const thr = await must<MessagePage>("GET", `/rooms/${room.id}/messages?thread=${seeded.question_id}&limit=50`);
    const all = [...page.items, ...thr.items.filter((x) => !page.items.some((y) => y.id === x.id))];
    const top = page.items;
    const lanes = await must<Lane[]>("GET", `/rooms/${room.id}/lanes`);
    expect(top.map((m) => m.speech)).toEqual(["system", "instruct", "delegate", "delegate", "report", "question", "report", "report", "note"]);

    const c = ctx(all, lanes);
    // 위임 — 받는 쪽 하나(대상 에이전트)에 그 서브 미션 칩.
    const deleg = top.filter((m) => m.speech === "delegate");
    expect(deleg.map((m) => m.id)).toEqual(seeded.delegate_ids);
    for (const d of deleg) {
      expect(d.addressees).toHaveLength(1);
      expect(speechOf(d, c).lane?.id).toBe(d.delegated_lane_id);
    }
    // 보고 — 위임 메시지를 가리키고, 「↩」 인용문이 그 본문이다.
    const rep = top[4];
    expect(rep.responds_to_message_id).toBe(deleg[0].id);
    expect(speechOf(rep, c).reportOf?.excerpt).toContain("법 통과 여부");
    // 질문 카드는 위임자를 받는 쪽으로, 그 답글은 answer.
    const q = top.find((m) => m.id === seeded.question_id)!;
    expect(q.speech).toBe("question");
    expect(q.addressees?.[0]?.name).toBe("Lead");
    const reply = all.find((m) => m.parent_id === seeded.question_id)!;
    expect(reply.speech).toBe("answer");
    // 메모는 받는 쪽이 없다(기록만), 시스템·요약도 비어 있다.
    expect(top.find((m) => m.speech === "note")!.addressees).toEqual([]);
    expect(top[0].addressees).toEqual([]);
    // 연속 보고 둘은 묶인다.
    expect(groupsWith({ m: top[6], s: speechOf(top[6], c) }, { m: top[7], s: speechOf(top[7], c) })).toBe(true);
  });

  it("사람이 보낸 멘션은 instruct, 에이전트끼리의 멘션은 request · detail 속 멘션은 받는 쪽이 아니다", async () => {
    const me = await must<{ workspaces: { id: string }[] }>("GET", "/me");
    const room = await must<Room>("POST", `/workspaces/${me.workspaces[0].id}/rooms`, { name: "STO2" });
    await must("POST", `/__mock/rooms/${room.id}/seed-conversation`, {});
    const parts = await must<{ items: { agent?: { id: string; name: string } }[] }>("GET", `/rooms/${room.id}/participants`);
    const writer = parts.items.map((p) => p.agent).find((a) => a?.name === "Writer")!;
    const posted = await must<{ message: Message }>("POST", `/rooms/${room.id}/messages`, {
      content: `[@${writer.name}](mention://agent/${writer.id}) 표 3 을 앞으로 옮겨 줘`,
    }, { "Idempotency-Key": "11111111-2222-4333-8444-555555555555" });
    expect(posted.message.speech).toBe("instruct");
    expect(posted.message.addressees).toEqual([{ kind: "agent", id: writer.id, name: writer.name }]);
  });
});
