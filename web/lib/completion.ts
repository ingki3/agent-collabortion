/**
 * 종료 조건 — 화면의 편집 상태(`ConditionDraft`) ↔ 계약 `CompletionCondition` 변환과 판정(T-W15, S-84).
 *
 * 마법사 6단계와 S7 「조건 고치기」 다이얼로그가 **같은 편집기**(`components/ConditionEditor.tsx`)를 그리므로 상태 모양도 하나다.
 * 문장은 `lib/wording.ts` 에서만 온다 — 여기는 규칙만.
 *
 * 규칙(계약 createSession/updateSession 검증, v0.1.4):
 *   - 조건은 하나 이상.
 *   - `agent_approval` 은 리뷰어(`agent_id`) 필수이고 그 에이전트가 참여자여야 한다(`reviewer_required` / `reviewer_not_participant`).
 *     리뷰어 없는 조건은 아무도 승인할 수 없어 세션이 영영 안 닫힌다 — Director 실사용에서 실제로 났던 일이다.
 *   - `artifact_submitted` 의 제출자는 `who: "assignee"`(기본) 또는 `agent_id`(지정) — 둘을 함께 보내지 않는다.
 */
import type { CompletionAtom, CompletionCondition, CompletionProgress } from "@/lib/api/types";
import { CONDITION_EDITOR, PROGRESS, conditionName, type TopOp } from "@/lib/wording";

export type CondType = "artifact_submitted" | "agent_approval" | "user_approval" | "manual";
export const COND_ORDER: readonly CondType[] = ["artifact_submitted", "agent_approval", "user_approval", "manual"];

export interface ConditionDraft {
  op: "and" | "or";
  conds: CondType[];
  /** `artifact_submitted` 의 제출자 에이전트 id. 빈 문자열이면 담당 에이전트(`who: "assignee"`). */
  submitter: string;
  /** `agent_approval` 의 리뷰어 에이전트 id. 빈 문자열이면 아직 안 골랐다(저장 불가). */
  reviewer: string;
}

/** SCREEN §4.4 기본값 — `보고서 제출(담당) AND Director 승인`. */
export const DEFAULT_DRAFT: ConditionDraft = { op: "and", conds: ["artifact_submitted", "user_approval"], submitter: "", reviewer: "" };

export function hasHumanGate(d: Pick<ConditionDraft, "conds">): boolean {
  return d.conds.includes("user_approval") || d.conds.includes("manual");
}

/** 화면 상태 → 계약 본문. 순서는 `COND_ORDER` 로 고정한다(고른 순서가 아니라). */
export function toCompletionCondition(d: ConditionDraft): CompletionCondition {
  const conditions: CompletionAtom[] = COND_ORDER.filter((t) => d.conds.includes(t)).map((t) => {
    if (t === "artifact_submitted") return d.submitter ? { type: t, agent_id: d.submitter } : { type: t, who: "assignee" };
    if (t === "agent_approval") return d.reviewer ? { type: t, agent_id: d.reviewer } : { type: t };
    return { type: t };
  });
  return { op: d.op, conditions };
}

/**
 * 계약 본문 → 화면 상태(조건 고치기가 기존 조건으로 시작한다). 마법사는 평평한 그룹 하나만 만들므로 그것만 읽는다 —
 * 중첩 그룹이나 `criteria_met` 은 v1 화면이 만들 수 없어 버린다(저장하면 사라진다 — 다이얼로그가 그 사실을 말한다).
 */
export function fromCompletionCondition(cc: CompletionCondition | null | undefined): { draft: ConditionDraft; dropped: number } {
  if (!cc) return { draft: { ...DEFAULT_DRAFT, conds: [] }, dropped: 0 };
  const atoms: CompletionAtom[] = "conditions" in cc ? (cc.conditions.filter((c) => "type" in c) as CompletionAtom[]) : [cc];
  const dropped = ("conditions" in cc ? cc.conditions.length : 1) - atoms.length;
  const draft: ConditionDraft = { op: "conditions" in cc ? cc.op : "and", conds: [], submitter: "", reviewer: "" };
  let skipped = 0;
  for (const a of atoms) {
    if (!(COND_ORDER as readonly string[]).includes(a.type)) { skipped++; continue; }
    const t = a.type as CondType;
    if (!draft.conds.includes(t)) draft.conds.push(t);
    if (t === "artifact_submitted" && a.agent_id) draft.submitter = a.agent_id;
    if (t === "agent_approval" && a.agent_id) draft.reviewer = a.agent_id;
  }
  return { draft, dropped: dropped + skipped };
}

export type ConditionGate = { ok: true } | { ok: false; reason: string };

/** 다음 단계·저장을 막는 사유 — 서버가 422 로 다시 검사하지만 화면은 먼저 근처에서 말한다(§8.5). */
export function conditionGate(d: ConditionDraft, participantIds: readonly string[]): ConditionGate {
  if (d.conds.length === 0) return { ok: false, reason: CONDITION_EDITOR.need_one };
  if (d.conds.includes("agent_approval")) {
    if (!d.reviewer) return { ok: false, reason: CONDITION_EDITOR.reviewer_required };
    if (!participantIds.includes(d.reviewer)) return { ok: false, reason: CONDITION_EDITOR.reviewer_not_participant };
  }
  return { ok: true };
}

/** 요약 문장의 이름 목록 — "보고서 제출 (담당 에이전트)" · "Lead 의 검토 승인" · "Director 승인". */
export function draftNames(d: ConditionDraft, nameOf: (agentId: string) => string): string[] {
  return COND_ORDER.filter((t) => d.conds.includes(t)).map((t) => {
    if (t === "artifact_submitted") return `${conditionName(t)} (${d.submitter ? `@${nameOf(d.submitter)}` : CONDITION_EDITOR.submitter_default_short})`;
    if (t === "agent_approval") return conditionName(t, d.reviewer ? nameOf(d.reviewer) : null);
    return conditionName(t);
  });
}

type ProgressCond = CompletionProgress["conditions"][number];

/** 상단 한 줄 — 남은 조건 이름과 막힌 개수. 트리 op 은 `completion_condition` 최상위에서 읽는다(진행률에는 없다). */
export function progressSummary(prog: CompletionProgress, op: TopOp, closed: boolean): string {
  if (closed) return PROGRESS.summary_completed;
  if (prog.satisfied) return PROGRESS.summary_satisfied;
  const open = prog.conditions.filter((c) => !c.met);
  const blocked = open.filter((c) => !!c.blocked_reason).length;
  const remaining = open.filter((c) => !c.blocked_reason).map((c: ProgressCond) => conditionName(c.type, c.agent_name ?? null));
  return PROGRESS.summary(remaining, blocked, op);
}

/**
 * `completion_condition` 최상위의 결합 — 트리면 그 `op`, **원자 하나면 `single`**(결합이 없다 — W-20, PR #234 NN4: 예전엔 and 로
 * 뭉뚱그렸다). 없으면(옛 세션·아직 없음) `single` — 조건 하나짜리와 같이 결합을 묻지 않는다.
 */
export function topOp(cc: CompletionCondition | null | undefined): TopOp {
  return cc && "op" in cc ? cc.op : "single";
}
