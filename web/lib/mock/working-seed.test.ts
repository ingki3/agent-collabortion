/**
 * 목 — 「작업 중」 말풍선 시드(`seed-working` · `working-step`, SCREEN §4.6 v0.19.10). 두 에이전트가 동시에 턴을 돌리고
 * 진행 메모가 데몬 모양(누적 전문)으로 흐르는지 · 게시·턴 끝이 계약 이벤트로 나가는지.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { dispatch, type Req } from "./handlers";
import { resetStore, store } from "./store";
import type { Lane, Room } from "@/lib/api/types";
import { applyDelta, memoSegments, noteToolEvent, type ProgressMemos } from "@/lib/progress-memo";

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

/** 구독자 하나를 붙여 SSE 프레임(ephemeral 포함)을 모은다 — 델타는 저장되지 않으므로 링 버퍼로는 못 본다. */
function tap(): { type: string; payload: Record<string, unknown> }[] {
  const out: { type: string; payload: Record<string, unknown> }[] = [];
  const ws = [...store().workspaces.keys()];
  for (const w of ws) {
    store().subs.add({ workspace_id: w, room_ids: null, write: (frame) => {
      const data = /^data: (.*)$/m.exec(frame)?.[1];
      if (data) out.push(JSON.parse(data));
    } });
  }
  return out;
}

beforeEach(async () => {
  resetStore();
  const res = await call("POST", "/auth/login", { email: "demo@colab.dev", password: "password123" });
  cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
});

describe("seed-working — 두 에이전트 동시 작업 + 진행 메모 흐름", () => {
  it("에이전트 둘이 running · 델타는 누적 전문 · 화면 규칙으로 조각이 나뉜다(새 데몬 빈 줄 / 옛 데몬 도구 경계)", async () => {
    const ws = (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
    const room = await must<Room>("POST", `/workspaces/${ws}/rooms`, { name: "마리오 카트" });
    await must("POST", `/__mock/rooms/${room.id}/seed`, { agents: ["Lead", "Researcher"] });
    const evs = tap();
    const seeded = await must<{ tasks: { agent_id: string; task_id: string }[] }>("POST", `/__mock/rooms/${room.id}/seed-working`);
    expect(seeded.tasks).toHaveLength(2);
    const lanes = await must<Lane[]>("GET", `/rooms/${room.id}/lanes`);
    for (const t of seeded.tasks) expect(lanes.find((l) => l.current_task?.id === t.task_id)?.status).toBe("running");

    // 이 시드가 흘린 이벤트를 화면과 같은 순서로 먹여 본다.
    let memos: ProgressMemos = {};
    for (const e of evs) {
      if (e.type === "message.delta") memos = applyDelta(memos, e.payload as unknown as { agent_id: string; task_id: string; text: string });
      if (e.type === "task_event.appended") memos = noteToolEvent(memos, e.payload as unknown as { task_id: string; class: string });
    }
    const [lead, dev] = seeded.tasks.map((t) => t.agent_id);
    expect(memoSegments(memos[lead])).toHaveLength(4);
    expect(memos[lead].text).toContain("\n\n");
    expect(memos[dev].text).not.toContain("\n\n");
    expect(memoSegments(memos[dev])).toEqual(["BGM v2 를 16분음표 격자로 다시 짜고 있습니다.", "테스트 28항목을 돌려 보겠습니다."]);
  });

  it("working-step post → message.created(같은 task) · end → turn_end + task.updated completed", async () => {
    const ws = (await must<{ workspaces: { id: string }[] }>("GET", "/me")).workspaces[0].id;
    const room = await must<Room>("POST", `/workspaces/${ws}/rooms`, { name: "마리오 카트" });
    await must("POST", `/__mock/rooms/${room.id}/seed`, { agents: ["Lead", "Researcher"] });
    const seeded = await must<{ tasks: { agent_id: string; task_id: string }[] }>("POST", `/__mock/rooms/${room.id}/seed-working`);
    const evs = tap();
    const lead = seeded.tasks[0];
    await must("POST", `/__mock/rooms/${room.id}/working-step`, { agent_id: lead.agent_id, action: "post" });
    await must("POST", `/__mock/rooms/${room.id}/working-step`, { agent_id: lead.agent_id, action: "end" });
    const created = evs.find((e) => e.type === "message.created")!.payload as unknown as { source_task_id: string };
    expect(created.source_task_id).toBe(lead.task_id);
    expect(evs.some((e) => e.type === "task_event.appended" && (e.payload as unknown as { verb: string }).verb === "turn_end")).toBe(true);
    expect(evs.some((e) => e.type === "task.updated" && (e.payload as unknown as { status: string }).status === "completed")).toBe(true);
  });
});
