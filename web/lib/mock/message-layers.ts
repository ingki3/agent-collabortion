/**
 * 목 — 에이전트 메시지 세 층(PRD FR-3.1.2 · SCREEN §4.6 · openapi v0.3.1 `Message.detail`) 시드와 아티팩트 본문.
 *
 * `POST /__mock/rooms/{id}/seed-layers`(계약 밖, `__mock` 접두) — 한 방에 넷을 붙인다:
 *   1. Researcher 의 조사 결과 — `detail` 1.2만 자(1만 자 넘음 → 「새 창으로 보기」) · 표 4개 · 작업 과정(검색 14 · 파일 읽기 6 · 5분).
 *   2. 그 메시지 스레드의 Writer 답글 — `detail` 짧은 것(표 1개). 스레드 답글도 세 층인지 보는 자리.
 *   3. Writer 의 제출 알림 — `detail` 없음 · 그 턴이 낸 아티팩트(`submitted_by_task_id`)가 붙는다 · 작업 과정에 실패 1(셸 명령).
 *   4. Researcher 의 긴 폴백 메시지 — `detail` 없이 본문 1,200자 초과 + 표 → 화면이 「자동으로 접음」.
 * 메시지 시각은 `addMessage` 의 단조 시계(nextMsgAt) 그대로다 — 활동 기록의 시각만 과거로 펼쳐 「걸린 시간」을 만든다.
 * 메시지는 `detail` 칸을 그대로 저장하고 `GET /rooms/{id}/messages` 가 그대로 싣는다(목 저장소가 Message 객체를 통째로 든다).
 *
 * `GET /artifacts/{id}/content`(계약 downloadArtifact) — 참조 줄 「열기」가 가는 곳. 목은 본문 대신 이름·버전을 적은 글을 준다.
 *
 * **handlers.ts 에는 등록 한 줄만 둔다**(`registerMessageLayers(…)`) — 병렬 워커와 그 파일을 나눠 쓴다.
 */
import type { Artifact, Lane, Message, TaskEvent } from "@/lib/api/types";
import type { Session } from "@/lib/legacy-session";
import { store, uuid, type MockTask, type Store } from "./store";
import type { Req, Res } from "./handlers";

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

export interface MessageLayersCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  sessionOf: (s: Store, req: Req, id: string) => Session;
  requireMember: (s: Store, req: Req, workspaceId: string) => unknown;
  addMessage: (s: Store, sess: Session, m: Partial<Message> & Pick<Message, "author_type" | "author_id" | "kind" | "content" | "mentions">) => Message;
  createTask: (s: Store, sess: Session, agentId: string, triggerId: string | null, opts?: { brief?: string | null }) => MockTask;
  pushEvent: (s: Store, sess: Session, task: MockTask, e: Partial<TaskEvent> & Pick<TaskEvent, "class">) => TaskEvent;
  setLaneStatus: (s: Store, sess: Session, laneId: string, patch: Partial<Lane>) => void;
  parseMentions: (content: string) => Message["mentions"];
  notFound: () => Error;
}

const PG = ["A사", "B사", "C사", "D사", "E사"];

/** 조사 전문 — 절 여섯 개(A~F), 표 넷, 1.2만 자 안팎. */
export function researchDetail(): string {
  const table = (head: string[], rows: string[][]) => [`| ${head.join(" | ")} |`, `|${head.map(() => "---").join("|")}|`, ...rows.map((r) => `| ${r.join(" | ")} |`)].join("\n");
  const prose = (topic: string, n: number) =>
    Array.from({ length: n }, (_, i) =>
      `${topic} ${i + 1}. 공시와 홈페이지 요율표를 대조했고, 두 자료가 다르면 최근 공시를 따랐다. 영업 문의로만 안내하는 곳은 「미확인」으로 남겼다 — 추정치를 적으면 표 3 의 비교가 틀어진다. 수수료는 부가세 별도 기준이고, 정산 주기와 최소 수수료는 별도 칸에 둔다.`,
    ).join("\n\n");
  const sections = [
    "## A. PG사별 수수료 비교 — 카드·간편결제·해외결제 (2026-09 기준)",
    table(["PG사", "카드", "간편결제", "출처"], [["A사", "2.1%", "2.5%", "공시 2026-08"], ["B사", "2.4%", "미확인", "홈페이지"], ["C사", "3.2%", "2.9%", "보도 2026-07"], ["D사", "2.6%", "2.7%", "공시 2026-06"], ["E사", "2.9%", "미확인", "영업 문의"]]),
    prose("간편결제 요율", 18),
    "## B. 해외결제",
    table(["PG사", "해외 카드", "환전 수수료", "정산 통화"], PG.map((p, i) => [p, `${(3.4 + i * 0.2).toFixed(1)}%`, `${(1 + i * 0.1).toFixed(1)}%`, i % 2 ? "USD" : "KRW"])),
    prose("해외결제", 18),
    "## C. 정산 주기와 최소 수수료",
    table(["PG사", "정산", "최소 수수료", "보증보험"], PG.map((p, i) => [p, `D+${1 + (i % 3)}`, `${100 + i * 50}원`, i === 2 ? "필요" : "없음"])),
    prose("정산", 18),
    "## D. 연동 방식",
    table(["PG사", "REST", "SDK", "웹훅 재시도"], PG.map((p, i) => [p, "지원", i % 2 ? "JS·iOS" : "JS·iOS·Android", `${3 + i}회`])),
    prose("연동", 18),
    "## E. 미확인 항목",
    "- B사 간편결제 요율 — 영업 문의 필요\n- E사 간편결제 요율 — 영업 문의 필요\n- C사 해외결제 환전 기준 시각",
    "## F. 다음 할 일",
    prose("다음 할 일", 12),
  ];
  return sections.join("\n\n");
}

/** 폴백 메시지 본문 — detail 없이 1,200자 넘게, 첫 문단 뒤에 표. */
export function longFallbackContent(): string {
  const rows = PG.map((p, i) => `| ${p} | ${(2 + i * 0.3).toFixed(1)}% | ${i % 2 ? "있음" : "없음"} |`).join("\n");
  const body = Array.from({ length: 12 }, (_, i) =>
    `근거 ${i + 1}. 가맹점 규모별 우대 요율은 영세·중소 구간에서만 공시되고 일반 가맹점은 개별 계약이라 표에서는 공시 요율만 적었다. 우대 구간이 바뀌면 표 전체가 다시 계산돼야 한다.`,
  ).join("\n\n");
  return [
    "@Lead 해외결제 요율도 정리했습니다. 공시가 있는 곳은 셋이고 나머지는 추정 없이 미확인으로 뒀습니다.",
    "",
    "| PG사 | 해외 요율 | 우대 구간 |",
    "|---|---|---|",
    rows,
    "",
    body,
  ].join("\n");
}

export function registerMessageLayers(ctx: MessageLayersCtx): void {
  const { on, Problem, sessionOf, addMessage, createTask, pushEvent, setLaneStatus, parseMentions } = ctx;
  const ok = (b: unknown, status = 200): Res => ({ status, body: b });

  on("POST", "/__mock/rooms/{id}/seed-layers", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const parts = sess.participants ?? [];
    const researcher = s.agents.get(parts[0]?.agent_id ?? "");
    const writer = s.agents.get(parts[1]?.agent_id ?? "") ?? researcher;
    if (!researcher || !writer) throw new Problem(409, "no_agent", "참여 에이전트가 없습니다");
    const t0 = Date.now() - 20 * 60_000;
    const at = (min: number) => new Date(t0 + min * 60_000).toISOString();
    const authorOf = (a: typeof researcher) => ({ name: a.name, avatar_url: null, role: a.role });
    /** 턴 하나 — task · 활동 기록 · 끝난 lane. */
    const turn = (agentId: string, from: number, evs: (Partial<TaskEvent> & Pick<TaskEvent, "class">)[]): MockTask => {
      const task = createTask(s, sess, agentId, null, { brief: null });
      task.status = "completed";
      task.started_at = at(from);
      evs.forEach((e) => pushEvent(s, sess, task, { outcome: "ok", created_at: at(from), ...e }));
      task.finished_at = at(from + 5);
      setLaneStatus(s, sess, task.lane_id, { status: "done", current_activity: null, finished_at: task.finished_at, brief: null });
      return task;
    };
    const spread = (n: number, from: number, minutes: number, e: Partial<TaskEvent> & Pick<TaskEvent, "class">) =>
      Array.from({ length: n }, (_, i) => ({ ...e, created_at: at(from + (i / Math.max(1, n - 1)) * minutes) }));

    // 1. 조사 결과 — detail 1.2만 자, 표 4개, 검색 14 · 파일 읽기 6 · 5분.
    const t1 = turn(researcher.id, 0, [
      // 실서버 모양(T-FEED 실측) — runtime/start 는 outcome=started 로 오고 짝 갱신 없이 turn_end 가 따로 온다. 끝난 턴이라 「진행 중…」이 붙으면 안 된다.
      { class: "runtime", verb: "start", outcome: "started", created_at: at(0) },
      ...spread(14, 0, 4, { class: "tool", verb: "search", sentence: `${researcher.name}가 웹을 검색했다 → ok` }),
      ...spread(6, 1, 3, { class: "tool", verb: "read", sentence: `${researcher.name}가 원문을 확인했다 → ok` }),
      { class: "runtime", verb: "turn_end", created_at: at(5) },
    ]);
    const mentionWriter = `[@${writer.name}](mention://agent/${writer.id})`;
    const c1 = `@Lead 주요 PG 5곳 수수료를 정리했습니다. 카드 2.1~3.2%, 간편결제는 3곳만 공개라 2곳은 미확인으로 남겼습니다. 다음은 ${mentionWriter} 가 표 3 으로 옮기면 됩니다.`;
    const m1 = addMessage(s, sess, {
      author_type: "agent", author_id: researcher.id, author: authorOf(researcher), kind: "text", content: c1, mentions: parseMentions(c1),
      detail: researchDetail(), source_task_id: t1.id, lane_id: t1.lane_id,
    });

    // 2. 스레드 답글 — Writer 의 짧은 detail(표 1개).
    const t2 = turn(writer.id, 6, [
      { class: "tool", verb: "read", created_at: at(6) },
      { class: "tool", verb: "edit_file", payload: { path: "notes/table3.md" }, created_at: at(7) },
    ]);
    addMessage(s, sess, {
      author_type: "agent", author_id: writer.id, author: authorOf(writer), kind: "text", parent_id: m1.id, source_task_id: t2.id, lane_id: t2.lane_id,
      content: "표 3 초안을 만들었습니다. 미확인 두 칸은 각주로 뺐습니다.", mentions: [],
      detail: ["### 표 3 초안", "", "| PG사 | 카드 | 간편결제 |", "|---|---|---|", ...PG.map((p, i) => `| ${p} | ${(2.1 + i * 0.3).toFixed(1)}% | ${i === 1 || i === 4 ? "미확인¹" : "2.5%"} |`), "", "¹ 영업 문의로만 안내."].join("\n"),
    });

    // 3. 제출 알림 — detail 없음 · 아티팩트 v2 · 셸 명령 실패 1.
    const t3 = turn(writer.id, 9, [
      { class: "tool", verb: "edit_file", payload: { path: "report/draft.md" }, created_at: at(9) },
      { class: "tool", verb: "edit_file", payload: { path: "report/table3.md" }, created_at: at(10) },
      { class: "tool", verb: "run_shell", outcome: "failed", payload: { command: "pandoc report/draft.md -o draft.pdf", exit_code: 1 }, object_ref: "pandoc", created_at: at(10.5) },
      // 짝(ok/failed)이 끝내 안 온 도구 호출 — 중단·유실. 끝난 턴에서는 「결과 없음」(T-FEED).
      { class: "tool", verb: "read", outcome: "started", object_ref: "report/sources.md", payload: { tool_call_id: "seed-orphan-read", kind: "read" }, created_at: at(11) },
      { class: "status", verb: "submit_artifact", object_ref: "report-draft.md", created_at: at(11.5) },
      { class: "runtime", verb: "turn_end", created_at: at(12) },
    ]);
    const art: Artifact = {
      id: uuid(), session_id: sess.id, name: "report-draft.md", version: 2, type: "document", storage_ref: "mock://document/report-draft-v2",
      size_bytes: 18_432, content_type: "text/markdown", submitted_by_task_id: t3.id, submitted_by: { agent_id: writer.id, agent_name: writer.name },
      description: null, latest: true, created_at: at(11.5), work_id: null,
    };
    s.artifacts.set(art.id, art);
    addMessage(s, sess, {
      author_type: "agent", author_id: writer.id, author: authorOf(writer), kind: "text", content: "초안 v2 를 제출했습니다. 검토 부탁드립니다.", mentions: [],
      source_task_id: t3.id, lane_id: t3.lane_id,
    });

    // 4. 폴백 — detail 없이 1,200자 넘는 본문 + 표.
    const t4 = turn(researcher.id, 13, spread(5, 13, 2, { class: "tool", verb: "search" }));
    const c4 = longFallbackContent();
    addMessage(s, sess, {
      author_type: "agent", author_id: researcher.id, author: authorOf(researcher), kind: "text", content: c4, mentions: parseMentions(c4),
      source_task_id: t4.id, lane_id: t4.lane_id,
    });
    return ok({ research_id: m1.id, artifact_id: art.id }, 201);
  });

  on("GET", "/artifacts/{id}/content", (req, p) => {
    const s = store();
    const a = s.artifacts.get(p.id);
    if (!a) throw ctx.notFound();
    const sess = s.sessions.get(a.session_id);
    if (sess) ctx.requireMember(s, req, sess.workspace_id);
    const bytes = new TextEncoder().encode(`${a.name} v${a.version}\n\n(목 본문 — ${a.storage_ref})\n`);
    const stream = new ReadableStream<Uint8Array>({ start(c) { c.enqueue(bytes); c.close(); } });
    return { status: 200, stream, headers: { "Content-Type": a.content_type ?? "text/plain; charset=utf-8" } };
  });
}
