/**
 * 활동 피드 렌더 클래스 — `contracts/task_event.schema.json` 의 `x-render-class`.
 *
 * 규칙 배열은 **적힌 순서대로 first-match** 다(마지막이 `else` 라는 것이 순차 평가의 근거).
 * 그래서 `outcome=failed` 인 셸 명령은 `shell` 로 렌더되고 `error` 로 가지 않는다 — 그게 정답이다:
 * 명령과 출력이 보여야 원인을 안다. `error` 규칙은 **전용 렌더러가 없는 실패**만 잡는다(runtime/error · permission rejected ·
 * 컷 1 로 규칙이 빠진 뒤의 failed). "실패는 크게"(SCREEN §4.5)는 클래스가 아니라 **행의 `data-outcome`** 이 맡는다.
 *
 * `render` 는 서버가 주지 않는다(openapi `TaskEvent` 에 필드가 없다) — 순수 함수이므로 웹이 계산한다.
 *
 * | # | when | render |
 * |---|---|---|
 * | 1 | class=message verb=say | message |
 * | 2 | class=status | platform |
 * | 3 | class=tool verb=edit_file | file_edit |
 * | 4 | class=tool verb=run_shell | shell |
 * | 5 | outcome=failed OR (class=runtime verb=error) OR (class=tool verb=permission outcome=rejected) | error |
 * | 6 | else | raw |
 */
import type { TaskEvent, TaskStatus } from "@/lib/api/types";
import { EMPTY_TURN } from "@/lib/wording";

export type RenderClass = "message" | "platform" | "file_edit" | "shell" | "error" | "raw";

type Ev = Pick<TaskEvent, "class" | "verb" | "outcome">;
interface Rule {
  readonly render: RenderClass;
  readonly when: (e: Ev) => boolean;
  /** 컷 1(x-render-class.cut_1)에서 배열에서 빠지는 규칙. */
  readonly cut1?: true;
}

/**
 * 규칙 배열 = 계약의 `rules` 그대로. 컷 1 은 **배열에서 두 항목을 빼는 것**이지 결과를 raw 로 바꾸는 것이 아니다 —
 * 빠지면 그 실패는 아래 error 규칙이 잡는다(Lead 확인, 2026-09-06).
 */
export const RENDER_RULES: readonly Rule[] = [
  { render: "message", when: (e) => e.class === "message" && e.verb === "say" },
  { render: "platform", when: (e) => e.class === "status" },
  { render: "file_edit", when: (e) => e.class === "tool" && e.verb === "edit_file", cut1: true },
  { render: "shell", when: (e) => e.class === "tool" && e.verb === "run_shell", cut1: true },
  {
    render: "error",
    when: (e) =>
      e.outcome === "failed" ||
      (e.class === "runtime" && e.verb === "error") ||
      (e.class === "tool" && e.verb === "permission" && e.outcome === "rejected"),
  },
  { render: "raw", when: () => true },
];

function classify(e: Ev, rules: readonly Rule[]): RenderClass {
  for (const r of rules) if (r.when(e)) return r.render;
  return "raw";
}

/** 계약 배열 그대로 first-match. `render` 는 서버가 주지 않는다 — 웹이 이벤트를 받을 때 계산한다. */
export function renderClass(e: Ev): RenderClass {
  return classify(e, RENDER_RULES);
}

/** 컷 1 — file_edit·shell 규칙을 배열에서 뺀다. 그 실패는 error 로, 나머지는 raw 로 떨어진다. */
export function renderClassWithCut(e: Ev, cut1: boolean): RenderClass {
  return classify(e, cut1 ? RENDER_RULES.filter((r) => !r.cut1) : RENDER_RULES);
}

/** task_event payload — 계약(task_event.schema.json $defs)의 class 별 세부. openapi 는 열린 object 다. */
export interface FeedPayload {
  kind?: string;
  text?: string;
  chars?: number;
  tool_call_id?: string;
  title?: string;
  path?: string;
  lines_added?: number;
  lines_removed?: number;
  command?: string;
  exit_code?: number;
  summary?: string;
  duration_ms?: number;
  option_kind?: string;
  allow_once_missing?: boolean;
  policy?: string;
  failure_kind?: string;
  detail?: string;
  not_before?: string;
  resume_reason?: string;
  runtime_kind?: string;
  session_id?: string;
  args?: Record<string, unknown>;
  result_ref?: string;
  rejected_reason?: string;
  current?: string;
  entries_done?: number;
  entries_total?: number;
  cost_usd?: number;
  input_tokens?: number;
  output_tokens?: number;
  estimated?: boolean;
}

export function payloadOf(e: TaskEvent): FeedPayload {
  const p = e.payload as FeedPayload | null | undefined;
  return p && typeof p === "object" ? p : {};
}

/**
 * 한 문장 — "에이전트가 [동사]를 [목적어]에 했다 → [결과]"(FR-7.2).
 * 서버가 `sentence` 를 주면 그것이 우선이고(계약), 없을 때만 여기서 만든다.
 * `object_ref` 는 **문자열**이다(계약 v0.4) — 객체를 넣지 않는다.
 */
export function feedSentence(e: TaskEvent): string {
  if (e.sentence) return e.sentence;
  const p = payloadOf(e);
  const obj = e.object_ref ?? p.path ?? p.command ?? p.title ?? "";
  const head = `${e.class}${e.verb ? "/" + e.verb : ""}`;
  const parts = [head, obj].filter(Boolean).join(" ");
  return e.outcome ? `${parts} → ${e.outcome}` : parts;
}

// ── 「진행 중」 판정(T-FEED, 2026-09-25) ──────────────────────────────────────
/**
 * 턴이 아직 돌 수 있는 task 상태 — 계약 `TaskStatus`(openapi) 중 런타임 턴이 살아 있는 것.
 * `waiting_human` 은 권한 요청(permission)으로 턴이 멈춰 선 상태라 도구 호출이 그대로 열려 있다.
 * 나머지(deferred·queued·paused·completed·failed·cancelled)는 **지금 도는 턴이 없다** — 그 task 의 어떤 줄에도 「진행 중」을 붙이지 않는다.
 */
export const LIVE_TASK_STATUSES: ReadonlySet<TaskStatus> = new Set<TaskStatus>(["dispatched", "preparing", "running", "waiting_human"]);

/** 턴(attempt)을 끝내는 런타임 줄 — `runtime/turn_end` · `runtime/error` · `runtime/cancel`(contracts/task_event.schema.json verb). */
export function isTurnClose(e: Pick<TaskEvent, "class" | "verb">): boolean {
  return e.class === "runtime" && (e.verb === "turn_end" || e.verb === "error" || e.verb === "cancel");
}

/** 판정에 쓰는 task 쪽 사실 — 모르면 비워 둔다(이벤트만으로 판정). */
export interface PendingCtx {
  /** task 상태. 끝난 상태면 모든 줄이 「진행 중」이 아니다. */
  taskStatus?: TaskStatus | null;
  /** task 의 현재 attempt. 그보다 앞 attempt 의 줄은 끝난 턴이다. */
  attempt?: number | null;
}

/** 이벤트의 attempt — 생성 타입에서 선택 칸이라 없으면 1(서버 CHECK attempt >= 1). */
export function attemptOf(e: Pick<TaskEvent, "attempt">): number {
  return e.attempt ?? 1;
}

/**
 * task 의 턴이 지금 도는가 — 「작업 중」 줄(T-FEED B)과 꼬리 조각의 행방을 가른다. task 상태를 알면 그것이 도는 상태(`LIVE_TASK_STATUSES`)여야 하고,
 * 이벤트로도: 기록이 있고 마지막 attempt 에 턴을 끝내는 런타임 줄이 없어야 한다(`task.updated` 보다 `runtime/turn_end` 가 먼저 올 수 있다).
 */
export function isTaskLive(events: readonly TaskEvent[], ctx: PendingCtx = {}): boolean {
  if (ctx.taskStatus != null && !LIVE_TASK_STATUSES.has(ctx.taskStatus)) return false;
  const cur = events.filter((e) => !e.superseded_by);
  if (cur.length === 0) return ctx.taskStatus != null;
  const last = Math.max(...cur.map(attemptOf), ctx.attempt ?? 0);
  return !cur.some((e) => attemptOf(e) === last && isTurnClose(e));
}

/**
 * 「진행 중…」을 붙일 줄인가 — `latest` 는 `foldEvents` 가 접은 행의 최신 판이다.
 * started 줄은 원래 "나중에 제자리 갱신될 줄"이지만 짝이 끝내 안 올 수 있다(runtime/start 는 짝 대신 turn_end 가 따로 오고,
 * 도구 호출은 실패·중단으로 ok 가 빠진다). 그래서:
 *  (a) 같은 `tool_call_id` 의 뒤 줄이 있으면 `foldEvents` 가 이미 그 결과로 제자리 갱신했다 — `latest` 가 started 가 아니다.
 *  (b) 같은 attempt 에 턴을 끝내는 런타임 줄(`isTurnClose`)이 있거나 더 뒤 attempt 의 줄이 있으면 그 attempt 는 끝났다.
 *  (c) task 가 도는 상태(`LIVE_TASK_STATUSES`)가 아니거나 `ctx.attempt` 보다 앞 attempt 면 끝났다.
 * 끝난 턴의 짝 없는 started 는 「진행 중」이 아니라 중립(`isUnresolved`)이다.
 */
export function pendingJudge(events: readonly TaskEvent[], ctx: PendingCtx = {}): (latest: TaskEvent) => boolean {
  const live = ctx.taskStatus == null || LIVE_TASK_STATUSES.has(ctx.taskStatus);
  const att = attemptOf;
  const closed = new Set<number>();
  let maxAttempt = ctx.attempt ?? -Infinity;
  for (const e of events) {
    if (e.superseded_by) continue;
    if (isTurnClose(e)) closed.add(att(e));
    if (att(e) > maxAttempt) maxAttempt = att(e);
  }
  return (latest) => live && latest.outcome === "started" && !closed.has(att(latest)) && att(latest) >= maxAttempt;
}

/** 끝난 턴에 짝 없이 남은 started 줄 — 「결과 없음」(중립). runtime/start 는 턴의 끝이 turn_end 줄로 따로 오므로 꼬리를 달지 않는다. */
export function isUnresolved(latest: TaskEvent, pending: boolean): boolean {
  return !pending && latest.outcome === "started" && !(latest.class === "runtime" && latest.verb === "start");
}

export function isFailure(e: TaskEvent): boolean {
  return e.outcome === "failed" || e.outcome === "rejected" || (e.class === "runtime" && e.verb === "error");
}

// ── 빈 턴(FR-7.2 v0.17 · v1.1 K-18) ──────────────────────────────────────────
/**
 * 서버가 finish 에서 남기는 한 행 — `{class: status, verb: turn_end, object_ref: "empty_turn", outcome: info}`(PRD FR-7.2, 닫힌 스키마 안).
 * 렌더 클래스는 규칙 2(platform)지만 **오류가 아니라 정보 카드**(ⓘ)로 그린다 — 렌더러가 클래스 위에 한 겹을 얹는 유일한 자리다.
 */
export function isEmptyTurn(e: Pick<TaskEvent, "class" | "verb" | "object_ref">): boolean {
  return e.class === "status" && e.verb === "turn_end" && e.object_ref === "empty_turn";
}

/** 카드 문장 — `payload.args.note` **그대로**. 없을 때만 화면 표의 같은 문장. */
export function emptyTurnNote(e: TaskEvent): string {
  const note = payloadOf(e).args?.note;
  return typeof note === "string" && note.trim() ? note : EMPTY_TURN.note;
}
