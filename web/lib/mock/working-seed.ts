/**
 * 목 — 「작업 중」 말풍선(SCREEN §4.6 v0.19.10 · COMPONENTS §9.10) 시드. 두 에이전트가 **동시에** 턴을 돌리며 진행 메모(`message.delta`)를 흘린다.
 *
 * `POST /__mock/rooms/{id}/seed-working`(계약 밖, `__mock` 접두) — 참여 에이전트 앞 둘(없으면 하나)에 대해:
 *   - 서브 미션 running + 현재 할 일 running + 활동 기록(과거로 펼친 시각 — 「12분 · 셸 명령 …」이 나오게, 첫째는 실패 2).
 *   - 진행 메모를 데몬과 같은 모양으로 흘린다: `text` 는 **턴 처음부터의 누적 전문**이다. 첫째 에이전트는 새 데몬(`runner.go` `appendSay` — 도구 경계마다
 *     빈 줄), 둘째는 **옛 데몬**(구분자 없이 이어 붙임 — 「…짜고 있습니다.테스트…」)이라 화면이 델타 사이의 도구 이벤트로 문단을 나누는 폴백을 탄다.
 *   델타는 SSE 로만 흐르고 저장되지 않는다 — 방을 **연 뒤에** 부른다. 응답은 `{ tasks: [{agent_id, task_id}] }`.
 *
 * `POST /__mock/rooms/{id}/working-step` `{agent_id, action: "post" | "end"}` — 「post」는 그 에이전트가 메시지를 게시한다(말풍선이 그 자리에서 메시지로),
 * 「end」는 턴을 끝낸다(runtime/turn_end + task.updated completed + lane done — 말풍선이 사라지고 꼬리는 마지막 메시지의 작업 과정으로).
 */
import type { Lane, Message, Task, TaskEvent } from "@/lib/api/types";
import type { Session } from "@/lib/legacy-session";
import { emit, now, store, type MockTask, type Store } from "./store";
import type { Req, Res } from "./handlers";

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

export interface WorkingSeedCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  sessionOf: (s: Store, req: Req, id: string) => Session;
  requireMember: (s: Store, req: Req, workspaceId: string) => unknown;
  addMessage: (s: Store, sess: Session, m: Partial<Message> & Pick<Message, "author_type" | "author_id" | "kind" | "content" | "mentions">) => Message;
  createTask: (s: Store, sess: Session, agentId: string, triggerId: string | null, opts?: { brief?: string | null }) => MockTask;
  pushEvent: (s: Store, sess: Session, task: MockTask, e: Partial<TaskEvent> & Pick<TaskEvent, "class">) => TaskEvent;
  setLaneStatus: (s: Store, sess: Session, laneId: string, patch: Partial<Lane>) => void;
  toTask: (s: Store, t: MockTask) => Task;
}

/** 에이전트별 진행 메모 조각(도구 호출 사이마다 하나) — 이어 붙이면 문장이 공백 없이 붙는다(실측 모양). */
export const WORKING_MEMOS: string[][] = [
  [
    "밸런스 하네스를 돌려 봤는데 1등 쏠림이 남아 있습니다.",
    "원인을 찾았습니다. 하네스 자체가 편향돼 있었습니다 — 시작 위치가 고정이라 1번 자리가 늘 유리했습니다.",
    "시작 위치를 섞고 다시 돌렸더니 테스트 28항목이 모두 통과합니다.",
    "밸런스 하네스 자체가 편향돼 있었습니다 — 4명 승률 25% 씩으로 맞췄습니다.",
  ],
  [
    "BGM v2 를 16분음표 격자로 다시 짜고 있습니다.",
    "테스트 28항목을 돌려 보겠습니다.",
  ],
];

export function registerWorkingSeed(ctx: WorkingSeedCtx): void {
  const { on, Problem, sessionOf, requireMember, addMessage, createTask, pushEvent, setLaneStatus, toTask } = ctx;
  const ok = (b: unknown, status = 200): Res => ({ status, body: b });

  on("POST", "/__mock/rooms/{id}/seed-working", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    requireMember(s, req, sess.workspace_id);
    const agents = (sess.participants ?? []).map((x) => s.agents.get(x.agent_id)).filter((a): a is NonNullable<typeof a> => !!a).slice(0, 2);
    if (agents.length === 0) throw new Problem(409, "no_agent", "참여 에이전트가 없습니다");
    const out: { agent_id: string; task_id: string }[] = [];
    // 다시 부르면(스크린샷 여러 장) 앞서 이 방에서 돌던 그 에이전트들의 턴을 조용히 끝낸다 — 보드에 도는 줄이 쌓이지 않게.
    for (const t of s.tasks.values()) {
      if (t.session_id !== sess.id || t.status !== "running" || !agents.some((a) => a.id === t.agent_id)) continue;
      t.status = "completed";
      t.finished_at = now();
      setLaneStatus(s, sess, t.lane_id, { status: "done", current_activity: null, finished_at: t.finished_at, brief: null });
    }
    agents.forEach((agent, ai) => {
      const minutes = ai === 0 ? 12 : 4;
      const t0 = Date.now() - minutes * 60_000;
      const at = (m: number) => new Date(t0 + m * 60_000).toISOString();
      const task = createTask(s, sess, agent.id, null, { brief: null });
      task.status = "running";
      task.started_at = at(0);
      setLaneStatus(s, sess, task.lane_id, { status: "running", current_activity: "셸 명령을 실행하는 중…", has_runtime_session: true });
      pushEvent(s, sess, task, { class: "runtime", verb: "start", outcome: "started", payload: { runtime_kind: "claude_code", session_id: `acp-${task.id.slice(0, 8)}` }, created_at: at(0) });
      const memos = WORKING_MEMOS[ai] ?? WORKING_MEMOS[0];
      // 조각 사이의 도구 호출 — 첫째: 셸 명령 36 · 파일 읽기 5 · 실패 2. 둘째: 파일 편집 3 · 셸 명령 6.
      const tools: (Partial<TaskEvent> & Pick<TaskEvent, "class">)[] = ai === 0
        ? [
          ...Array.from({ length: 36 }, (_, i) => ({ class: "tool" as const, verb: "run_shell", outcome: i === 7 || i === 21 ? "failed" : "ok", payload: { command: "node test/headless.js" }, sentence: `${agent.name}가 셸 명령을 실행했다` })),
          ...Array.from({ length: 5 }, () => ({ class: "tool" as const, verb: "read", outcome: "ok", object_ref: "src/balance.ts", sentence: `${agent.name}가 파일을 읽었다` })),
        ]
        : [
          ...["src/bgm.ts", "src/bgm-grid.ts", "test/bgm.test.ts"].map((path) => ({ class: "tool" as const, verb: "edit_file", outcome: "ok", payload: { path }, object_ref: path })),
          ...Array.from({ length: 6 }, () => ({ class: "tool" as const, verb: "run_shell", outcome: "ok", payload: { command: "node test/headless.js" } })),
        ];
      const per = Math.ceil(tools.length / memos.length);
      let text = "";
      memos.forEach((memo, mi) => {
        text += (mi > 0 && ai === 0 ? "\n\n" : "") + memo; // 첫째: 새 데몬(도구 경계에 빈 줄) · 둘째: 옛 데몬(구분자 없음).
        emit(s, sess.workspace_id, "message.delta", { session_id: sess.id, task_id: task.id, agent_id: agent.id, text }, sess.id, true);
        tools.slice(mi * per, (mi + 1) * per).forEach((e, k) => {
          const m = ((mi * per + k + 1) / (tools.length + 1)) * minutes;
          pushEvent(s, sess, task, { ...e, created_at: at(m) });
        });
      });
      // 마지막 조각 뒤 도구가 없으면 경계가 생기지 않는다 — 마지막 델타를 한 번 더(heartbeat 가 같은 전문을 다시 보내는 모양).
      emit(s, sess.workspace_id, "message.delta", { session_id: sess.id, task_id: task.id, agent_id: agent.id, text }, sess.id, true);
      out.push({ agent_id: agent.id, task_id: task.id });
    });
    return ok({ tasks: out }, 201);
  });

  on("POST", "/__mock/rooms/{id}/working-step", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    requireMember(s, req, sess.workspace_id);
    const b = (req.body ?? {}) as { agent_id?: string; action?: "post" | "end" };
    const agent = b.agent_id ? s.agents.get(b.agent_id) : undefined;
    const task = agent ? [...s.tasks.values()].filter((t) => t.session_id === sess.id && t.agent_id === agent.id && t.status === "running").sort((x, y) => (x.created_at < y.created_at ? 1 : -1))[0] : undefined;
    if (!task || !agent) throw new Problem(404, "not_found", "도는 턴이 없습니다");
    if (b.action === "post") {
      const content = "@Director 밸런스를 맞췄습니다 — 4명 승률이 25% 씩입니다. 하네스의 시작 위치 편향이 원인이었습니다.";
      const msg = addMessage(s, sess, { author_type: "agent", author_id: agent.id, author: { name: agent.name, avatar_url: null, role: agent.role }, kind: "text", content, mentions: [], source_task_id: task.id, lane_id: task.lane_id });
      pushEvent(s, sess, task, { class: "status", verb: "post_message", object_ref: msg.id, outcome: "ok", payload: { command: "message post", result_ref: msg.id } });
      return ok({ message_id: msg.id }, 201);
    }
    pushEvent(s, sess, task, { class: "runtime", verb: "turn_end", outcome: "ok", sentence: "턴 종료 → ok" });
    task.status = "completed";
    task.finished_at = now();
    setLaneStatus(s, sess, task.lane_id, { status: "done", current_activity: null, finished_at: task.finished_at, brief: null });
    emit(s, sess.workspace_id, "task.updated", toTask(s, task), sess.id);
    return ok({ task_id: task.id }, 200);
  });
}
