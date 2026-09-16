/**
 * 종료 조건 변환·판정(T-W15) — 화면 상태 ↔ 계약 CompletionCondition, 리뷰어 게이트, 요약 문장.
 */
import { describe, expect, it } from "vitest";
import { DEFAULT_DRAFT, conditionGate, draftNames, fromCompletionCondition, progressSummary, toCompletionCondition, topOp } from "./completion";
import { conditionSentence } from "./wording";

const names: Record<string, string> = { "a-lead": "Lead", "a-writer": "Writer" };
const nameOf = (id: string) => names[id] ?? id;

describe("toCompletionCondition — 계약 모양", () => {
  it("기본값은 보고서 제출(담당) AND Director 승인", () => {
    expect(toCompletionCondition(DEFAULT_DRAFT)).toEqual({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "user_approval" }] });
  });
  it("제출자·리뷰어를 지정하면 agent_id 만(who 와 함께 보내지 않는다) · 순서는 고른 순서가 아니라 고정", () => {
    expect(toCompletionCondition({ op: "or", conds: ["manual", "agent_approval", "artifact_submitted"], submitter: "a-writer", reviewer: "a-lead" })).toEqual({
      op: "or",
      conditions: [{ type: "artifact_submitted", agent_id: "a-writer" }, { type: "agent_approval", agent_id: "a-lead" }, { type: "manual" }],
    });
  });
});

describe("fromCompletionCondition — 기존 조건으로 시작", () => {
  it("평평한 그룹을 읽고 지정 에이전트를 채운다", () => {
    const { draft, dropped } = fromCompletionCondition({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval", agent_id: "a-lead" }, { type: "user_approval" }] });
    expect(draft).toEqual({ op: "and", conds: ["artifact_submitted", "agent_approval", "user_approval"], submitter: "", reviewer: "a-lead" });
    expect(dropped).toBe(0);
  });
  it("원자 하나 · 없음 · v1 화면이 못 만드는 것(중첩 그룹·criteria_met)은 센다", () => {
    expect(fromCompletionCondition({ type: "manual" }).draft.conds).toEqual(["manual"]);
    expect(fromCompletionCondition(null).draft.conds).toEqual([]);
    const r = fromCompletionCondition({ op: "and", conditions: [{ type: "criteria_met" }, { op: "or", conditions: [{ type: "manual" }] }, { type: "user_approval" }] });
    expect(r.draft.conds).toEqual(["user_approval"]);
    expect(r.dropped).toBe(2);
  });
  it("왕복이 같다 — 리뷰어 없는 옛 조건도 그대로(리뷰어 빈 채) 시작한다", () => {
    const legacy = { op: "and" as const, conditions: [{ type: "artifact_submitted" as const, who: "assignee" }, { type: "agent_approval" as const }] };
    const { draft } = fromCompletionCondition(legacy);
    expect(draft.reviewer).toBe("");
    expect(toCompletionCondition({ ...draft, reviewer: "a-lead" })).toEqual({ op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval", agent_id: "a-lead" }] });
  });
});

describe("conditionGate — 리뷰어 필수(계약 v0.1.4)", () => {
  const parts = ["a-lead", "a-writer"];
  it("조건 0개 → 사유", () => expect(conditionGate({ ...DEFAULT_DRAFT, conds: [] }, parts)).toEqual({ ok: false, reason: "종료 조건을 하나 이상 고르세요" }));
  it("agent_approval 인데 리뷰어 없음 → 사유", () => expect(conditionGate({ ...DEFAULT_DRAFT, conds: ["agent_approval"] }, parts)).toMatchObject({ ok: false, reason: expect.stringContaining("리뷰어를 고르세요") }));
  it("리뷰어가 참여자가 아님 → 사유", () => expect(conditionGate({ ...DEFAULT_DRAFT, conds: ["agent_approval"], reviewer: "a-gone" }, parts)).toEqual({ ok: false, reason: "리뷰어는 참여자 중에서 골라야 합니다" }));
  it("리뷰어가 참여자 → 통과 · agent_approval 없으면 리뷰어 검사 없음", () => {
    expect(conditionGate({ ...DEFAULT_DRAFT, conds: ["agent_approval"], reviewer: "a-lead" }, parts)).toEqual({ ok: true });
    expect(conditionGate(DEFAULT_DRAFT, parts)).toEqual({ ok: true });
  });
});

describe("요약 문장 — 사람 말", () => {
  it("보고서 제출 (담당 에이전트) 그리고 Lead 의 검토 승인 그리고 Director 승인", () => {
    const d = { op: "and" as const, conds: ["user_approval", "agent_approval", "artifact_submitted"] as const, submitter: "", reviewer: "a-lead" };
    expect(conditionSentence(draftNames({ ...d, conds: [...d.conds] }, nameOf), d.op)).toBe("보고서 제출 (담당 에이전트) 그리고 Lead 의 검토 승인 그리고 Director 승인");
  });
  it("리뷰어를 아직 안 골랐으면 일반형 · OR 은 또는", () => {
    expect(conditionSentence(draftNames({ op: "or", conds: ["agent_approval", "manual"], submitter: "", reviewer: "" }, nameOf), "or")).toBe("에이전트 검토 승인 또는 수동 종료");
  });
});

describe("progressSummary — 남은 것 · 막힘", () => {
  const cond = (type: string, met: boolean, extra: Record<string, unknown> = {}) => ({ path: "/x", type, met, ...extra });
  it("남은 것: Director 승인 1개 · 막힘 1개", () => {
    const prog = { met: 1, total: 3, satisfied: false, conditions: [cond("artifact_submitted", true), cond("agent_approval", false, { blocked_reason: "reviewer_missing" }), cond("user_approval", false)] };
    expect(progressSummary(prog, "and", false)).toBe("남은 것: Director 승인 1개 · 막힘 1개");
  });
  it("막힘 없으면 이름만 · 충족이면 곧 완료 · 끝났으면 끝났습니다", () => {
    // 2개 이상이면 이름마다 개수(W-20) — "이름, 이름 2개" 가 아니다.
    expect(progressSummary({ met: 0, total: 2, satisfied: false, conditions: [cond("agent_approval", false, { agent_name: "Lead" }), cond("user_approval", false)] }, "and", false)).toBe("남은 것: Lead 의 검토 승인 1개 · Director 승인 1개");
    // 같은 이름이 둘이면 묶는다.
    expect(progressSummary({ met: 0, total: 2, satisfied: false, conditions: [cond("agent_approval", false, { agent_name: "Lead" }), cond("agent_approval", false, { agent_name: "Lead" })] }, "and", false)).toBe("남은 것: Lead 의 검토 승인 2개");
    expect(progressSummary({ met: 2, total: 2, satisfied: true, conditions: [] }, "and", false)).toBe("조건을 모두 충족했습니다 — 곧 완료됩니다");
    expect(progressSummary({ met: 0, total: 2, satisfied: false, conditions: [] }, "and", true)).toBe("세션이 끝났습니다");
  });
  it("topOp — 원자 하나면 single(결합이 없다), 트리면 그 op, 없으면 single (W-20)", () => {
    expect(topOp({ type: "manual" })).toBe("single");
    expect(topOp({ op: "or", conditions: [] })).toBe("or");
    expect(topOp({ op: "and", conditions: [] })).toBe("and");
    expect(topOp(null)).toBe("single");
    // single 은 요약에 '하나만 충족하면 끝' 을 붙이지 않는다 — 조건 하나에 결합을 물을 것이 없다.
    expect(progressSummary({ met: 0, total: 1, satisfied: false, conditions: [cond("user_approval", false)] }, "single", false)).toBe("남은 것: Director 승인 1개");
  });
});
