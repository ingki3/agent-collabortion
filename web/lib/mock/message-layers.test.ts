/**
 * 목 — 메시지 세 층 시드(`seed-layers`)가 계약 모양으로 `detail` 을 싣는지(openapi v0.3.1 `Message.detail`), 화면이 폴백·아티팩트
 * 참조·실패 꼬리를 그릴 재료가 다 있는지.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { resetStore } from "./store";
import { charCount, countTables, exceedsAutoFold, messageLayers, summarizeProcess, DETAIL_WINDOW_CHARS } from "@/lib/message-layers";
import type { Artifact, MessagePage, Room, TaskEvent } from "@/lib/api/types";

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

beforeEach(async () => {
  resetStore();
  const res = await call("POST", "/auth/login", { email: "demo@colab.dev", password: "password123" });
  cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
});

describe("seed-layers — detail · 폴백 · 스레드 답글 · 아티팩트 · 실패", () => {
  it("넷을 붙이고 listMessages 가 detail 칸을 그대로 싣는다", async () => {
    const ws = (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
    const room = await must<Room>("POST", `/workspaces/${ws}/rooms`, { name: "결제팀" });
    await must("POST", `/__mock/rooms/${room.id}/seed`, { agents: ["Researcher", "Writer"] });
    const seeded = await must<{ research_id: string; artifact_id: string }>("POST", `/__mock/rooms/${room.id}/seed-layers`);

    const page = await must<MessagePage>("GET", `/rooms/${room.id}/messages?limit=50`);
    const agentMsgs = page.items.filter((m) => m.author_type === "agent" && m.kind === "text");
    const research = agentMsgs.find((m) => m.id === seeded.research_id)!;
    // 1. 1만 자 넘는 detail · 표 여러 개.
    expect(charCount(research.detail!)).toBeGreaterThan(DETAIL_WINDOW_CHARS);
    expect(countTables(research.detail!)).toBeGreaterThanOrEqual(4);
    expect(research.reply_count).toBe(1);
    // 4. detail 없는 긴 폴백(표 포함).
    const fallback = agentMsgs.find((m) => !m.detail && exceedsAutoFold(m.content))!;
    expect(fallback).toBeTruthy();
    expect(messageLayers(fallback).work).toMatchObject({ auto: true });
    expect(countTables(messageLayers(fallback).work!.text)).toBe(1);

    // 2. 스레드 답글의 detail.
    const thread = await must<MessagePage>("GET", `/rooms/${room.id}/messages?thread=${research.id}`);
    const reply = thread.items.find((m) => m.parent_id === research.id)!;
    expect(reply.detail).toMatch(/표 3 초안/);

    // 3. 제출 알림 — 그 턴의 아티팩트 · 작업 과정 실패 1.
    const submit = agentMsgs.find((m) => m.content.startsWith("초안 v2"))!;
    expect(submit.detail ?? null).toBeNull();
    const arts = await must<Artifact[]>("GET", `/rooms/${room.id}/artifacts`);
    const art = arts.find((a) => a.id === seeded.artifact_id)!;
    expect(art.submitted_by_task_id).toBe(submit.source_task_id);
    const evs = await must<{ items: TaskEvent[]; structured: boolean }>("GET", `/tasks/${submit.source_task_id}/events`);
    expect(summarizeProcess({ events: evs.items, structured: evs.structured, loading: false })).toMatchObject({ state: "ready", failures: 1 });
    const researchEvs = await must<{ items: TaskEvent[]; structured: boolean }>("GET", `/tasks/${research.source_task_id}/events`);
    const sum = summarizeProcess({ events: researchEvs.items, structured: true, loading: false });
    expect(sum.state === "ready" && sum.top.map((p) => p.text.join(String(p.n)))).toEqual(["검색 14회", "파일 읽기 6회"]);

    // 참조 줄 「열기」가 가는 본문.
    const content = await call("GET", `/artifacts/${art.id}/content`);
    expect(content.status).toBe(200);
    expect(content.stream).toBeTruthy();
  });
});
