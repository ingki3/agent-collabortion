/**
 * 대화 배치 판정(PRD FR-3.1.3 표 11행) — 서버 칸만으로, 모르면 pending(라벨 없음).
 * 목 시드(`seed-conversation`)는 서버 router.Delegate · status.go 모양을 그대로 따라 판정이 목에서만 맞는 일이 없게 한다.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "@/lib/mock/handlers";
import { resetStore } from "@/lib/mock/store";
import type { Lane, Message, MessagePage, Room, Task } from "@/lib/api/types";
import { classify, excerpt, groupsWith, mentionAddressees, type ConversationCtx } from "./conversation";

const A = { lead: "a-lead", res: "a-res", wri: "a-wri" };
const U = "u-owner";
let seq = 0;
function msg(p: Partial<Message> & Pick<Message, "author_type" | "content">): Message {
  seq += 1;
  return {
    id: `m${seq}`, session_id: "r", author_id: p.author_type === "user" ? U : p.author_type === "agent" ? A.lead : null, parent_id: null,
    mentions: [], source_task_id: null, kind: "text", state: "posted", created_at: new Date(Date.UTC(2026, 8, 25, 12, seq)).toISOString(), ...p,
  } as Message;
}
const link = (n: string, id: string) => `[@${n}](mention://agent/${id})`;
const men = (n: string, id: string) => ({ kind: "agent" as const, id, display_name: n });
function ctx(o: Partial<ConversationCtx> & { msgs?: Message[]; triggers?: Record<string, string | null> } = {}): ConversationCtx {
  const byId = new Map((o.msgs ?? []).map((m) => [m.id, m]));
  return {
    lanes: o.lanes ?? [],
    triggerOf: o.triggerOf ?? ((t) => (o.triggers && t in o.triggers ? o.triggers[t] : undefined)),
    messageById: o.messageById ?? ((id) => byId.get(id)),
    authorName: (m) => m.author?.name ?? (m.author_type === "user" ? "서연" : m.author_id === A.res ? "Researcher" : m.author_id === A.wri ? "Writer" : "Lead"),
  };
}

describe("classify — PRD FR-3.1.3 판정표", () => {
  it("1·2 시스템 · HITL 은 머리 없이", () => {
    expect(classify(msg({ author_type: "system", kind: "system", content: "x" }), ctx()).kind).toBe("system");
    expect(classify(msg({ author_type: "system", kind: "hitl", content: "x" }), ctx()).kind).toBe("hitl");
  });

  it("3 질문(blocked_q) → 멘션된 위임자, 멘션 없으면 lane.waiting_for", () => {
    const q = msg({ author_type: "agent", author_id: A.wri, kind: "blocked_q", content: `표 3?\n\n${link("Lead", A.lead)}`, mentions: [men("Lead", A.lead)] });
    expect(classify(q, ctx())).toMatchObject({ kind: "question", to: [{ kind: "agent", id: A.lead, name: "Lead" }] });
    const q2 = msg({ author_type: "agent", author_id: A.wri, kind: "blocked_q", content: "범위?" });
    const lane = { id: "l1", blocked_message_id: q2.id, waiting_for: "Director" } as Lane;
    expect(classify(q2, ctx({ lanes: [lane] }))).toMatchObject({ kind: "question", to: [{ name: "Director" }] });
  });

  it("4 요약 → 방 전체", () => {
    expect(classify(msg({ author_type: "system", kind: "summary", content: "끝" }), ctx())).toMatchObject({ kind: "summary", to: [{ kind: "room" }] });
  });

  it("5 질문 카드 답글 → 답, 받는 쪽 = 질문한 에이전트(+멘션, 중복 없이)", () => {
    const q = msg({ author_type: "agent", author_id: A.wri, kind: "blocked_q", content: "?" });
    const a = msg({ author_type: "agent", author_id: A.lead, parent_id: q.id, content: `${link("Writer", A.wri)} 앞으로`, mentions: [men("Writer", A.wri)] });
    const s = classify(a, ctx(), q);
    expect(s.kind).toBe("answer");
    expect(s.to).toEqual([{ kind: "agent", id: A.wri, name: "Writer" }]);
  });

  it("10 /note → 메모 · 기록만", () => {
    expect(classify(msg({ author_type: "user", content: "/note x", is_note: true }), ctx())).toMatchObject({ kind: "note", to: [{ kind: "record" }] });
  });

  it("6 위임 — delegated_from_task_id = source_task_id · 멘션 · brief 로 끝남, 셋 다 맞아야", () => {
    const brief = "법 · 시장 규모를 조사해 주세요.";
    const m = msg({ author_type: "agent", author_id: A.lead, source_task_id: "t-lead", content: `${link("Researcher", A.res)} ${brief}`, mentions: [men("Researcher", A.res)] });
    const lane = { id: "l-r", agent_id: A.res, agent_name: "Researcher", delegated_from_task_id: "t-lead", brief, status: "running" } as Lane;
    expect(classify(m, ctx({ lanes: [lane], triggers: { "t-lead": null } }))).toMatchObject({ kind: "delegate", to: [{ id: A.res }], lane: { id: "l-r" } });
    // brief 가 다르면(같은 턴의 다른 멘션 메시지) 위임이 아니다 — 요청.
    const other = msg({ author_type: "agent", author_id: A.lead, source_task_id: "t-lead", content: `${link("Researcher", A.res)} 참고로 하나 더`, mentions: [men("Researcher", A.res)] });
    expect(classify(other, ctx({ lanes: [lane], triggers: { "t-lead": null } })).kind).toBe("request");
    // 다른 task 가 만든 lane 이면 위임이 아니다.
    expect(classify(m, ctx({ lanes: [{ ...lane, delegated_from_task_id: "t-x" }], triggers: { "t-lead": null } })).kind).toBe("request");
  });

  it("7 보고 — 턴 트리거의 작성자(요청자)가 받는 쪽에 있거나 받는 쪽이 비었다", () => {
    const order = msg({ author_type: "agent", author_id: A.lead, content: `${link("Researcher", A.res)} 법 통과 여부를 조사해 주세요.`, mentions: [men("Researcher", A.res)] });
    const rep = msg({ author_type: "agent", author_id: A.res, source_task_id: "t-r", content: `${link("Lead", A.lead)} 끝났습니다`, mentions: [men("Lead", A.lead)] });
    const c = ctx({ msgs: [order], triggers: { "t-r": order.id } });
    const s = classify(rep, c);
    expect(s).toMatchObject({ kind: "report", to: [{ id: A.lead }], reportOf: { messageId: order.id, requester: "Lead", excerpt: "법 통과 여부를 조사해 주세요." } });
    // 받는 쪽이 비었으면 요청자에게.
    const bare = msg({ author_type: "agent", author_id: A.res, source_task_id: "t-r", content: "끝났습니다" });
    expect(classify(bare, c)).toMatchObject({ kind: "report", to: [{ id: A.lead }] });
    // 요청자가 아닌 다른 에이전트에게 말하면 보고가 아니다(요청).
    const side = msg({ author_type: "agent", author_id: A.res, source_task_id: "t-r", content: `${link("Writer", A.wri)} 표 넘깁니다`, mentions: [men("Writer", A.wri)] });
    expect(classify(side, c).kind).toBe("request");
  });

  it("7 보고 — 트리거를 아직 모르면 pending(라벨 없음), 트리거가 없거나 읽을 수 없으면 보고 아님", () => {
    const rep = msg({ author_type: "agent", author_id: A.res, source_task_id: "t-r", content: "끝", mentions: [] });
    expect(classify(rep, ctx()).pending).toBe(true);
    expect(classify(rep, ctx({ triggers: { "t-r": "m-far" } })).pending).toBe(true);
    expect(classify(rep, ctx({ triggers: { "t-r": "m-far" }, messageById: () => null }))).toMatchObject({ kind: "talk" });
    expect(classify(rep, ctx({ triggers: { "t-r": "m-far" }, messageById: () => null })).pending).toBeUndefined();
    expect(classify(rep, ctx({ triggers: { "t-r": null } }))).toMatchObject({ kind: "talk", to: [{ kind: "room" }] });
    // 시스템이 깨운 턴(합류 통보 등)은 요청자가 없다.
    const sys = msg({ author_type: "system", kind: "system", content: "합류" });
    expect(classify(rep, ctx({ msgs: [sys], triggers: { "t-r": sys.id } })).kind).toBe("talk");
  });

  it("8 지시(사람 → 에이전트) · 9 요청(에이전트 → 에이전트) · 11 대화(스레드 대상 → 방 전체)", () => {
    expect(classify(msg({ author_type: "user", content: "x", mentions: [men("Lead", A.lead)] }), ctx())).toMatchObject({ kind: "order", to: [{ id: A.lead }] });
    expect(classify(msg({ author_type: "agent", author_id: A.lead, content: "x", mentions: [men("Writer", A.wri)] }), ctx()).kind).toBe("request");
    const root = msg({ author_type: "agent", author_id: A.res, content: "root" });
    expect(classify(msg({ author_type: "user", content: "좋네요" }), ctx(), root)).toMatchObject({ kind: "talk", to: [{ id: A.res, kind: "agent" }] });
    expect(classify(msg({ author_type: "user", content: "안녕" }), ctx())).toMatchObject({ kind: "talk", to: [{ kind: "room" }] });
  });

  it("받는 쪽 — 작성자 자신은 빼고, 중복은 한 번, @all 은 all", () => {
    const m = msg({ author_type: "agent", author_id: A.lead, content: "x", mentions: [men("Lead", A.lead), men("Writer", A.wri), men("Writer", A.wri), { kind: "all", id: "all", display_name: "all" }] });
    expect(mentionAddressees(m).map((a) => a.kind + ":" + (a.id ?? ""))).toEqual(["agent:" + A.wri, "all:all"]);
  });

  it("excerpt — 앞 멘션을 떼고 40자", () => {
    expect(excerpt(`${link("Researcher", A.res)} ${"가".repeat(50)}`)).toBe(`${"가".repeat(40)}…`);
  });

  it("묶음 — 같은 작성자 · 종류 · 받는 쪽 · 5분 안, 말풍선끼리만", () => {
    const a = msg({ author_type: "agent", author_id: A.wri, content: "1" });
    const b = msg({ author_type: "agent", author_id: A.wri, content: "2" });
    const s = { kind: "report" as const, to: [{ kind: "agent" as const, id: A.lead, name: "Lead" }] };
    expect(groupsWith({ m: a, s }, { m: b, s })).toBe(true);
    expect(groupsWith({ m: a, s }, { m: b, s: { ...s, to: [{ kind: "agent", id: A.res, name: "Researcher" }] } })).toBe(false);
    expect(groupsWith({ m: a, s }, { m: { ...b, created_at: new Date(Date.parse(a.created_at) + 6 * 60_000).toISOString() }, s })).toBe(false);
    expect(groupsWith({ m: { ...a, kind: "blocked_q" }, s }, { m: b, s })).toBe(false);
  });
});

// ── 목 시드 — 서버 모양 그대로(router.Delegate · status.go) ────────────────────────
let cookie = "";
async function call(method: string, path: string, body?: unknown) {
  const [p, qs] = path.split("?");
  const req: Req = { method, path: p, query: new URLSearchParams(qs ?? ""), headers: new Headers(), body, cookies: cookie ? { colab_session: cookie } : {} };
  return dispatch(req);
}
async function must<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await call(method, path, body);
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(res.body)}`);
  return res.body as T;
}

describe("seed-conversation — 목이 서버 칸으로 판정 재료를 싣는다", () => {
  beforeEach(async () => {
    resetStore();
    const res = await call("POST", "/auth/login", { email: "demo@colab.dev", password: "password123" });
    cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
  });

  it("지시 → 위임 둘 → 보고 → 질문 · 답 → 보고 둘(묶음) → 메모 가 판정표대로 읽힌다", async () => {
    const me = await must<{ workspaces: { id: string }[] }>("GET", "/me");
    const room = await must<Room>("POST", `/workspaces/${me.workspaces[0].id}/rooms`, { name: "STO" });
    const seeded = await must<{ question_id: string }>("POST", `/__mock/rooms/${room.id}/seed-conversation`, {});
    const page = await must<MessagePage>("GET", `/rooms/${room.id}/messages?limit=50`);
    const thr = await must<MessagePage>("GET", `/rooms/${room.id}/messages?thread=${seeded.question_id}&limit=50`);
    const all = [...page.items, ...thr.items.filter((x) => !page.items.some((y) => y.id === x.id))];
    const top = all.filter((m) => !m.parent_id);
    const lanes = await must<Lane[]>("GET", `/rooms/${room.id}/lanes`);
    const triggers: Record<string, string | null> = {};
    for (const l of lanes) for (const t of await must<Task[]>("GET", `/lanes/${l.id}/tasks`)) triggers[t.id] = t.trigger_message_id;
    const c = ctx({ lanes, msgs: all, triggers: triggers });
    const kinds = top.map((m) => classify(m, c).kind);
    expect(kinds).toEqual(["system", "order", "delegate", "delegate", "report", "question", "report", "report", "note"]);
    const reply = all.find((m) => m.parent_id === seeded.question_id)!;
    expect(classify(reply, c, top.find((m) => m.id === seeded.question_id)).kind).toBe("answer");
    // 위임 상태 칩 재료 — Researcher 는 끝났고 Writer 도 끝났다.
    const deleg = top.filter((m) => classify(m, c).kind === "delegate").map((m) => classify(m, c).lane?.status);
    expect(deleg).toEqual(["done", "done"]);
    // 보고의 「↩」 는 위임 메시지를 가리킨다.
    const rep = classify(top[4], c);
    expect(rep.reportOf?.messageId).toBe(top[2].id);
    // 연속 보고 둘은 묶인다.
    expect(groupsWith({ m: top[6], s: classify(top[6], c) }, { m: top[7], s: classify(top[7], c) })).toBe(true);
  });
});
