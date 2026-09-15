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
import type { TaskEvent } from "@/lib/api/types";

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

/** 진행 중(started)인가 — 제자리 갱신 대상. 상태 줄을 쌓지 않는다. */
export function isPending(e: TaskEvent): boolean {
  return e.outcome === "started";
}

export function isFailure(e: TaskEvent): boolean {
  return e.outcome === "failed" || e.outcome === "rejected" || (e.class === "runtime" && e.verb === "error");
}
